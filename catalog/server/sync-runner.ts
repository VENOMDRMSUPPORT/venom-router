/**
 * Sync orchestration for the service: one runner, one lock, one schedule.
 *
 * The lock exists because the scheduler and `POST /v1/sync` can fire at the same
 * moment. Two concurrent runs would interleave their transactions and, worse,
 * both compute a delta against the same "before" state — so the second would see
 * the first's additions as unexplained and could trip the removal gate.
 */

import type { Db } from '../db/index.ts';
import { createFetchJson, type FetchJson } from '../sync/http.ts';
import type { RunResult } from '../sync/engine.ts';
import { ADAPTERS, BILLING } from '../sync/providers/index.ts';
import { loadSpecs } from '../sync/sources/models-dev.ts';
import { loadVendors } from '../sync/vendor-registry.ts';
import { loadQualityBounds } from '../sync/quality-bounds.ts';
import { loadReviewedFacts } from '../sync/reviewed-facts.ts';
import { loadDisplayNames } from '../sync/display-names.ts';
import { loadEvaluationIdentities, type EvaluationIdentityOverlay } from '../sync/evaluation/identity.ts';
import { loadBenchmarks } from '../sync/sources/openrouter.ts';
import type { ScoringSummary } from '../sync/score/pipeline.ts';
import {
  runResolutionPipeline,
  runSyncPipeline,
  type SyncPipelineConfig,
} from '../sync/pipeline.ts';
import type { RejectionOverlay } from '../sync/identity-rejections.ts';
import type { ScoreProfile } from '../sync/score/venom-score.ts';
import { beginResolutionWindow, bootstrapResolutionJobs, listDueResolutionJobs } from '../sync/resolution-jobs.ts';

export interface SyncOutcome {
  startedAt: string;
  finishedAt: string;
  providers: RunResult[];
  scoring: ScoringSummary | null;
  /** Set when the shared sources could not be fetched; nothing was written. */
  aborted?: string;
}

export interface ResolutionOutcome {
  startedAt: string;
  finishedAt: string;
  attempted: number;
  resolved: number;
  dormant: number;
  aborted?: string;
}

export interface RunnerConfig {
  db: Db;
  profile: ScoreProfile;
  methodologyVersion: string;
  identityOverlay: Record<string, string>;
  evaluationIdentities?: EvaluationIdentityOverlay;
  /**
   * The overlay's refused-candidate records.
   *
   * Optional so a caller that has none is not forced to invent an empty shape,
   * but the real service always passes them: a decision the catalog cannot serve
   * is a decision no consumer can audit.
   */
  identityRejections?: RejectionOverlay;
  onSnapshot?: (db: Db) => void;
  /** Injectable for tests; defaults to the real fetch discipline, POST helper, and provider registry. See `runSyncPipeline`. */
  fetchJson?: FetchJson;
  post?: SyncPipelineConfig['post'];
  detailFetchers?: SyncPipelineConfig['detailFetchers'];
  /** Injectable wall clock for deterministic lifecycle tests. */
  now?: () => Date;
  /** Targeted passes wait for a full sync for at most this long. */
  resolutionLockWaitMs?: number;
}

export class SyncRunner {
  readonly config: RunnerConfig;
  private running = false;
  private last: SyncOutcome | null = null;
  private startedAt: string | null = null;
  private mode: 'full' | 'resolution' | null = null;
  private fullSyncQueued = false;

  constructor(config: RunnerConfig) {
    this.config = config;
  }

  get isRunning(): boolean {
    return this.running;
  }

  get lastOutcome(): SyncOutcome | null {
    return this.last;
  }

  get currentRunStartedAt(): string | null {
    return this.startedAt;
  }

  /**
   * Run a full sync. Returns `null` immediately if one is already in flight —
   * the caller reports 409 rather than queueing, because a second run started
   * seconds later would fetch the same upstream state anyway.
   */
  async run(): Promise<SyncOutcome | null> {
    if (this.running) {
      if (this.mode === 'resolution') this.fullSyncQueued = true;
      return null;
    }
    this.running = true;
    this.mode = 'full';
    const currentDate = () => this.config.now?.() ?? new Date();
    const startedAt = currentDate().toISOString();
    this.startedAt = startedAt;

    try {
      const { db, profile, methodologyVersion, identityOverlay, identityRejections, evaluationIdentities, post, detailFetchers } = this.config;
      const fetchJson = this.config.fetchJson ?? createFetchJson();
      const sourceFetchedAt = currentDate().toISOString();

      let specs, benchmarks;
      try {
        // The same registry the CLI passes. A source wired into one entry point
        // and not the other is the failure `sync/pipeline.ts` documents: this
        // scheduler runs every six hours and would prune what the CLI filled.
        [specs, benchmarks] = await Promise.all([loadSpecs(fetchJson, loadVendors()), loadBenchmarks(fetchJson)]);
      } catch (err) {
        // The shared sources are the specs and the benchmarks. Without them a
        // run would rewrite every row with nulls, so it does not start at all
        // and the previous catalog stands untouched.
        const outcome: SyncOutcome = {
          startedAt, finishedAt: currentDate().toISOString(), providers: [], scoring: null,
          aborted: err instanceof Error ? err.message : String(err),
        };
        this.last = outcome;
        return outcome;
      }

      // The same function the CLI calls — see `sync/pipeline.ts` for why this
      // one path, not two, is the point. Before this, this runner enriched only
      // once and never asked a provider's own detail endpoint, so a fact ONLY
      // detail could prove looked identical, to this path, to one the provider
      // had withdrawn.
      const result = await runSyncPipeline({
        db, fetchJson, adapters: ADAPTERS, specs, benchmarks, billing: BILLING,
        overlay: identityOverlay, rejections: identityRejections, evaluationIdentities: evaluationIdentities ?? loadEvaluationIdentities(), profile, methodologyVersion,
        // Read here rather than taken from config for the same reason as the
        // vendor registry: a source the CLI passes and the scheduler does not
        // is a six-hourly run that silently drops every reviewed bound.
        bounds: loadQualityBounds(), reviewedFacts: loadReviewedFacts(), displayNames: loadDisplayNames(),
        sourceFetchedAt, now: () => currentDate().toISOString(), post, detailFetchers,
      });

      const finishedAt = currentDate().toISOString();
      beginResolutionWindow(db, finishedAt);
      this.config.onSnapshot?.(db);
      const outcome: SyncOutcome = {
        startedAt, finishedAt, providers: result.providers, scoring: result.scoring,
      };
      this.last = outcome;
      return outcome;
    } finally {
      this.running = false;
      this.mode = null;
      this.startedAt = null;
    }
  }

  /** Run due source-resolution work without fetching any provider roster. */
  async runResolutionPass(): Promise<ResolutionOutcome | null> {
    const currentDate = () => this.config.now?.() ?? new Date();
    const waitMs = this.config.resolutionLockWaitMs ?? 30_000;
    const deadline = Date.now() + waitMs;
    while (this.running && Date.now() < deadline) {
      await new Promise((resolve) => setTimeout(resolve, Math.min(100, Math.max(1, deadline - Date.now()))));
    }
    if (this.running) return null;

    const startedAt = currentDate().toISOString();
    const jobs = listDueResolutionJobs(this.config.db, startedAt);
    if (jobs.length === 0) {
      return { startedAt, finishedAt: startedAt, attempted: 0, resolved: 0, dormant: 0 };
    }
    this.running = true;
    this.mode = 'resolution';
    this.startedAt = startedAt;
    try {
      const { db, profile, methodologyVersion, identityOverlay, evaluationIdentities, post, detailFetchers } = this.config;
      const fetchJson = this.config.fetchJson ?? createFetchJson();
      const sourceFetchedAt = currentDate().toISOString();
      let specs, benchmarks;
      try {
        [specs, benchmarks] = await Promise.all([
          loadSpecs(fetchJson, loadVendors()),
          loadBenchmarks(fetchJson),
        ]);
      } catch (err) {
        return {
          startedAt, finishedAt: currentDate().toISOString(), attempted: 0, resolved: 0, dormant: 0,
          aborted: err instanceof Error ? err.message : String(err),
        };
      }

      const result = await runResolutionPipeline({
        db, fetchJson, jobs, specs, benchmarks, billing: BILLING, overlay: identityOverlay, evaluationIdentities: evaluationIdentities ?? loadEvaluationIdentities(),
        profile, methodologyVersion, bounds: loadQualityBounds(), reviewedFacts: loadReviewedFacts(),
        sourceFetchedAt, now: () => currentDate().toISOString(), post, detailFetchers,
      });
      this.config.onSnapshot?.(db);
      return {
        startedAt,
        finishedAt: currentDate().toISOString(),
        attempted: result.attempted,
        resolved: result.resolutions.filter((resolution) => resolution.state === 'complete').length,
        dormant: result.resolutions.filter((resolution) => resolution.nextAttemptAt === null && resolution.state !== 'complete').length,
      };
    } finally {
      this.running = false;
      this.mode = null;
      this.startedAt = null;
      if (this.fullSyncQueued) {
        this.fullSyncQueued = false;
        queueMicrotask(() => void this.run().catch((err) => console.error('[scheduler] queued full sync failed:', err)));
      }
    }
  }

  /** Seed jobs missing from an older database, then resume anything due. */
  async resumeResolutionJobs(): Promise<ResolutionOutcome | null> {
    const now = (this.config.now?.() ?? new Date()).toISOString();
    bootstrapResolutionJobs(this.config.db, now);
    return this.runResolutionPass();
  }
}

export interface SchedulerHandle {
  stop(): void;
  readonly intervalMs: number;
  nextRunAt(): string;
}

/** Six hours: often enough to catch a same-day launch, rare enough to be polite. */
export const SCHEDULE_INTERVAL_MS = 6 * 60 * 60 * 1000;
export const RESOLUTION_POLL_MS = 30_000;

export function startScheduler(
  runner: SyncRunner,
  {
    intervalMs = SCHEDULE_INTERVAL_MS,
    resolutionPollMs = RESOLUTION_POLL_MS,
    runOnStart = false,
  }: { intervalMs?: number; resolutionPollMs?: number; runOnStart?: boolean } = {},
): SchedulerHandle {
  let nextAt = Date.now() + intervalMs;
  const timer = setInterval(() => {
    nextAt = Date.now() + intervalMs;
    // A skipped tick is correct behaviour, not an error: the lock means a long
    // run simply absorbs the next slot.
    void runner.run().catch((err) => console.error('[scheduler] sync failed:', err));
  }, intervalMs);
  timer.unref?.();
  const resolutionTimer = setInterval(() => {
    void runner.runResolutionPass().catch((err) => console.error('[scheduler] resolution pass failed:', err));
  }, resolutionPollMs);
  resolutionTimer.unref?.();
  // Resume durable jobs after a service restart and seed older databases that
  // predate the queue. Existing dormant jobs stay dormant until a real trigger.
  void runner.resumeResolutionJobs().catch((err) => console.error('[scheduler] initial resolution pass failed:', err));
  if (runOnStart) void runner.run().catch((err) => console.error('[scheduler] initial sync failed:', err));
  return {
    stop: () => {
      clearInterval(timer);
      clearInterval(resolutionTimer);
    },
    intervalMs,
    nextRunAt: () => new Date(nextAt).toISOString(),
  };
}
