/**
 * The one path from "rosters fetched" to "scored and ready to serve".
 *
 * Both entry points — the CLI (`sync/run.ts`) and the service (`server/sync-runner.ts`,
 * reached by `POST /v1/sync` and the 6h scheduler) — call this and only this.
 *
 * They used to diverge: the service ran `enrich()` once, from the free shared
 * sources only; the CLI ran it twice, asking each provider's own detail
 * endpoint for whatever was still open between the two passes. That was not a
 * missing nice-to-have in the service — against `enrich()`'s per-run rebuild
 * (see enrich.ts), it was active data loss. A fact ONLY the detail endpoint can
 * prove looks, to a run that never asks it, identical to a fact the provider
 * withdrew, and the service was running exactly that "never asks" run every six
 * hours. `enrich()` itself now refuses to draw that conclusion for `provider_api`
 * facts unless it can see this run actually consulted detail — but that guard
 * only has a chance to matter if BOTH paths give detail the chance to run in
 * the first place. This file is what makes that true by construction: there is
 * now exactly one function that decides the order of "shared sources, detail,
 * rescoring", so a new step added here reaches both callers instead of one.
 */

import type { Db } from '../db/index.ts';
import type { FetchJson } from './http.ts';
import { syncProvider, type ProviderAdapter, type RunResult } from './engine.ts';
import { applyPublishPolicy, type PublishPolicySummary } from './publish-policy.ts';
import { enrich, canonicalFromBenchmarks, rowsNeedingDetail, type EnrichSummary } from './enrich/enrich.ts';
import type { BillingPolicy } from './enrich/resolvers.ts';
import type { SpecSource } from './sources/models-dev.ts';
import type { BenchmarkSource } from './sources/openrouter.ts';
import { DETAIL_FETCHERS, makePost, type ProviderDetail } from './sources/provider-detail.ts';
import { ingestRejections, type RejectionOverlay } from './identity-rejections.ts';
import { scoreAll, type ScoringSummary } from './score/pipeline.ts';
import { recalculatePublishedOffers, type OverallRecalculationSummary } from './evaluation/recalculate.ts';
import type { ScoreProfile } from './score/venom-score.ts';
import type { ReviewedFacts } from './reviewed-facts.ts';
import {
  finishResolutionAttempt,
  type ModelResolution,
  type ResolutionJob,
} from './resolution-jobs.ts';

export interface SyncPipelineConfig {
  db: Db;
  fetchJson: FetchJson;
  /** Providers to sync this run. The CLI's `--provider` filter narrows this; the service always passes every adapter. */
  adapters: ProviderAdapter[];
  /** Already fetched by the caller — each call site owns its own policy for what an unreachable shared source means. */
  specs: SpecSource;
  benchmarks: BenchmarkSource;
  billing: Record<string, BillingPolicy>;
  overlay: Record<string, string>;
  /** Absent means no rejection overlay is configured, not that it was checked and found empty — see `ingestRejections`. */
  rejections?: RejectionOverlay;
  /**
   * Reviewed one-sided VQ bounds. Absent means none was configured, which
   * leaves the affected rows unrated rather than scored from a guess.
   */
  bounds?: Record<string, import('./quality-bounds.ts').ReviewedBound>;
  /** Human-reviewed field facts that are persisted with their source evidence. */
  reviewedFacts?: ReviewedFacts;
  profile: ScoreProfile;
  methodologyVersion: string;
  sourceFetchedAt: string;
  now: () => string;
  /** Injectable so a test can prove behaviour without an HTTP call. Defaults to the real POST helper. */
  post?: (url: string, body: unknown) => Promise<unknown>;
  /** Injectable so a test can simulate a provider's detail endpoint without touching `sources/provider-detail.ts`. Defaults to the real registry. */
  detailFetchers?: typeof DETAIL_FETCHERS;
}

export interface SyncPipelineResult {
  providers: RunResult[];
  /** How many rows were still open after the free sources, and how many of those a detail call actually answered. */
  detail: { asked: number; answered: number };
  /** From the SECOND enrich pass — the one with whatever detail answered. This is the summary that matters to a caller. */
  enrich: EnrichSummary;
  /** What each free-only provider's publish policy withheld this run. */
  publish: PublishPolicySummary;
  rejections: { records: number; offerings: number; skipped: string[] } | null;
  scoring: ScoringSummary;
  overall: OverallRecalculationSummary;
}

export type ResolutionPipelineConfig = Omit<SyncPipelineConfig, 'adapters'> & {
  jobs: ResolutionJob[];
};

export interface ResolutionPipelineResult {
  attempted: number;
  detail: { asked: number; answered: number };
  enrich: EnrichSummary | null;
  scoring: ScoringSummary | null;
  overall: OverallRecalculationSummary | null;
  resolutions: ModelResolution[];
}

/**
 * Sync every given provider's roster, enrich twice around a provider-detail
 * phase, ingest identity rejections, and rescore.
 *
 * Two enrich passes, not one: the first uses only the sources every provider
 * shares (models.dev, OpenRouter, declared billing), and whatever is still open
 * afterwards is asked of the provider's own detail endpoint — targeted, because
 * a detail call is a per-model HTTP request and asking every provider about
 * every model to answer a handful of gaps would be rude to the provider and
 * slow for no gain. The second pass resolves again with that answer ranked
 * first, and it is what every caller's `enrich` result reflects.
 */
export async function runSyncPipeline(cfg: SyncPipelineConfig): Promise<SyncPipelineResult> {
  const {
    db, fetchJson, adapters, specs, benchmarks, billing, overlay, rejections, bounds, reviewedFacts,
    profile, methodologyVersion, sourceFetchedAt, now,
  } = cfg;
  const detailFetchers = cfg.detailFetchers ?? DETAIL_FETCHERS;

  const providers: RunResult[] = [];
  for (const adapter of adapters) {
    providers.push(await syncProvider(adapter, { db, fetchJson, now, lookupSpec: specs.lookup }));
  }

  // Withhold what a free-only provider's roster should not publish, BEFORE enrich
  // and scoring — both of which gate on active/missing, so an excluded row leaves
  // the published catalog (and its populations and calibration) by construction,
  // while the row survives for history. Runs after the engine so it never touches
  // the delta gate.
  const publish = applyPublishPolicy({ db, adapters, lookupSpec: specs.lookup, now });

  const canonical = canonicalFromBenchmarks(benchmarks);
  const base = {
    db, canonical, overlay, billing,
    lookupSpec: specs.lookup, intrinsic: specs.intrinsic,
    firstPartyLimits: specs.firstPartyLimits, vendorIdentity: specs.vendorIdentity, reviewedFacts,
    now,
  };

  enrich(base);

  const post = cfg.post ?? makePost();
  const open = rowsNeedingDetail(db, Object.keys(detailFetchers));
  const details = new Map<string, ProviderDetail>();
  for (const row of open) {
    const fetcher = detailFetchers[row.providerId];
    const d = fetcher ? await fetcher(row.modelId, post) : null;
    if (d) details.set(`${row.providerId}/${row.modelId}`, d);
  }

  const en = enrich({ ...base, details });

  // The mappings travel with the rejections so a contradiction — an id both
  // mapped and rejected — fails the run rather than letting the refused
  // candidate quietly become a resolved identity.
  const rej = rejections ? ingestRejections(db, rejections, now, { mappings: overlay }) : null;

  const scoring = scoreAll({ db, benchmarks, overlay, profile, bounds, methodologyVersion, sourceFetchedAt, now });
  const overall = recalculatePublishedOffers(db, now());

  return { providers, detail: { asked: open.length, answered: details.size }, enrich: en, publish, rejections: rej, scoring, overall };
}

/** Refresh only unresolved offerings, without touching any provider roster. */
export async function runResolutionPipeline(cfg: ResolutionPipelineConfig): Promise<ResolutionPipelineResult> {
  if (cfg.jobs.length === 0) {
    return { attempted: 0, detail: { asked: 0, answered: 0 }, enrich: null, scoring: null, overall: null, resolutions: [] };
  }
  const {
    db, specs, benchmarks, billing, overlay, bounds, reviewedFacts,
    profile, methodologyVersion, sourceFetchedAt, now,
  } = cfg;
  const targets = new Set(cfg.jobs.map((job) => `${job.providerId}/${job.modelId}`));
  const canonical = canonicalFromBenchmarks(benchmarks);
  const base = {
    db, canonical, overlay, billing,
    lookupSpec: specs.lookup, intrinsic: specs.intrinsic,
    firstPartyLimits: specs.firstPartyLimits, vendorIdentity: specs.vendorIdentity,
    reviewedFacts, targets, now,
  };

  enrich(base);
  const detailFetchers = cfg.detailFetchers ?? DETAIL_FETCHERS;
  const open = rowsNeedingDetail(db, Object.keys(detailFetchers))
    .filter((row) => targets.has(`${row.providerId}/${row.modelId}`));
  const post = cfg.post ?? makePost();
  const details = new Map<string, ProviderDetail>();
  for (const row of open) {
    const fetcher = detailFetchers[row.providerId];
    const detail = fetcher ? await fetcher(row.modelId, post) : null;
    if (detail) details.set(`${row.providerId}/${row.modelId}`, detail);
  }
  const en = enrich({ ...base, details });
  const scoring = scoreAll({
    db, benchmarks, overlay, profile, bounds, methodologyVersion, sourceFetchedAt, now,
  });
  const overall = recalculatePublishedOffers(db, now());
  const finishedAt = now();
  const resolutions = cfg.jobs.map((job) =>
    finishResolutionAttempt(db, job.providerId, job.modelId, finishedAt),
  );
  return {
    attempted: cfg.jobs.length,
    detail: { asked: open.length, answered: details.size },
    enrich: en,
    scoring,
    overall,
    resolutions,
  };
}
