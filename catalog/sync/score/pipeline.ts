/**
 * Scoring pass. Runs after the roster sync, over whatever is stored — so it is
 * a pure function of (stored models + evidence + methodology version) and can
 * be re-run at any time to reproduce the same numbers.
 */

import type { Db } from '../../db/index.ts';
import { transaction } from '../../db/index.ts';
import { resolveIdentity, normalizeId } from '../identity.ts';
import type { BenchmarkSource } from '../sources/openrouter.ts';
import { fitCalibration, isAcceptable, type Calibration } from './calibration.ts';
import { computeVQ, computeVO, type ScoreProfile, type VOPopulations } from './venom-score.ts';
import type { CostKind } from '../enrich/resolvers.ts';
import type { ReviewedBound } from '../quality-bounds.ts';

export interface ScoringDeps {
  db: Db;
  benchmarks: BenchmarkSource;
  overlay: Record<string, string>;
  /**
   * Reviewed one-sided bounds, keyed by catalog model id.
   *
   * The last resort, and the only route by which a row whose identity no index
   * can settle carries a figure at all. Optional so a caller can score without
   * one — absent means "no bound was reviewed", which leaves the row unrated,
   * never a value invented in its place.
   */
  bounds?: Record<string, ReviewedBound>;
  profile: ScoreProfile;
  methodologyVersion: string;
  /** When the upstream payloads behind this pass were fetched. */
  sourceFetchedAt: string;
  now: () => string;
}

export interface ScoringSummary {
  calibration: Calibration | null;
  calibrationVersion: string | null;
  calibrationAccepted: boolean;
  levels: Record<string, number>;
  reviewQueue: number;
  total: number;
}

interface ModelRow {
  provider_id: string;
  model_id: string;
  context_tokens: number | null;
  output_tokens: number | null;
  input_modalities: string | null;
  tools: number | null;
  reasoning: number | null;
  structured: number | null;
  attachment: number | null;
  cost_out_per_m: number | null;
  cost_kind: string | null;
}

/**
 * Fit the source-to-source calibration from the benchmark set itself, every
 * run. It is never a constant in the code: if the upstream relation drifts, the
 * fit drifts with it, and `isAcceptable` withholds calibrated values rather
 * than publishing a stale mapping.
 */
export function fitFromBenchmarks(benchmarks: BenchmarkSource): Calibration | null {
  const obs = [];
  for (const rec of benchmarks.byId.values()) {
    if (typeof rec.intelligence === 'number' && typeof rec.designElo === 'number') {
      // `identity` collapses plan twins (`X`, `X:batch`, `X:free`) that carry
      // the same figures; see the Observation docs for why that matters.
      obs.push({
        id: rec.id,
        identity: normalizeId(rec.id),
        group: rec.vendor,
        x: rec.designElo,
        y: rec.intelligence,
      });
    }
  }
  return fitCalibration(obs);
}

export function scoreAll(deps: ScoringDeps): ScoringSummary {
  const { db, benchmarks, overlay, profile, methodologyVersion, now } = deps;
  const calibration = fitFromBenchmarks(benchmarks);
  const accepted = isAcceptable(calibration);
  const calibrationVersion = calibration
    ? `cal-${calibration.n}-${calibration.slope.toFixed(5)}-${calibration.intercept.toFixed(2)}`
    : null;

  const models = db
    .prepare(`SELECT * FROM models WHERE status IN ('active','missing')`)
    .all() as unknown as ModelRow[];

  // Populations for the operational percentiles. Computed from the catalog
  // itself, because "a large context" only means anything relative to what is
  // on offer here.
  const pop: VOPopulations = {
    context: models.filter((m) => m.context_tokens).map((m) => Math.log(m.context_tokens!)),
    output: models.filter((m) => m.output_tokens).map((m) => Math.log(m.output_tokens!)),
    // `free` billing is $0 even when no per-token figure was published (a free,
    // quota-limited provider). It joins the population as 0 so the cost
    // percentile is computed over the same set the scorer treats as free — a
    // feed-published zero and a declared free both count once, as 0.
    cost: models
      .filter((m) => m.cost_out_per_m !== null || m.cost_kind === 'free')
      .map((m) => (m.cost_kind === 'free' ? 0 : m.cost_out_per_m!)),
  };

  const levels: Record<string, number> = { measured: 0, calibrated: 0, bounded: 0, unrated: 0 };
  let reviewQueue = 0;
  const at = now();

  transaction(db, () => {
    if (calibration && calibrationVersion) {
      db.prepare(
        `INSERT INTO calibrations (version, source_from, source_to, n, rho, r2, slope, intercept, loo_rmse, baseline_sd, accepted, excluded_json, bias_json, fitted_at)
         VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(version) DO UPDATE SET fitted_at = excluded.fitted_at`,
      ).run(
        calibrationVersion, 'design_arena', 'artificial_analysis', calibration.n, calibration.rho,
        calibration.r2, calibration.slope, calibration.intercept, calibration.looRmse,
        calibration.baselineSd, Number(accepted), JSON.stringify(calibration.excludedGroups),
        JSON.stringify(calibration.groupBias), at,
      );
    }

    const upsert = db.prepare(
      `INSERT INTO model_scores (provider_id, model_id, kind, value, uncertainty, bound, evidence_level, source,
                                 source_model_id, raw_value, raw_field, transformation, source_fetched_at,
                                 identity_rule, unrated_reason, precision_dp, dimensions, profile_id,
                                 methodology_ver, calibration_ver, computed_at)
       VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
       ON CONFLICT(provider_id, model_id, kind) DO UPDATE SET
         value = excluded.value, uncertainty = excluded.uncertainty, bound = excluded.bound,
         evidence_level = excluded.evidence_level, source = excluded.source,
         source_model_id = excluded.source_model_id, raw_value = excluded.raw_value,
         raw_field = excluded.raw_field, transformation = excluded.transformation,
         source_fetched_at = excluded.source_fetched_at, identity_rule = excluded.identity_rule, unrated_reason = excluded.unrated_reason,
         precision_dp = excluded.precision_dp, dimensions = excluded.dimensions,
         profile_id = excluded.profile_id, methodology_ver = excluded.methodology_ver,
         calibration_ver = excluded.calibration_ver, computed_at = excluded.computed_at`,
    );
    const event = db.prepare(
      'INSERT INTO model_events (provider_id, model_id, kind, field, old_value, new_value, reason, at) VALUES (?,?,?,?,?,?,?,?)',
    );

    for (const m of models) {
      const resolution = resolveIdentity(m.model_id, benchmarks.index, overlay);

      if (resolution.status === 'ambiguous') {
        reviewQueue++;
        db.prepare(
          `INSERT INTO identity_review (provider_id, model_id, candidates_json, first_seen_at)
           VALUES (?,?,?,?) ON CONFLICT(provider_id, model_id) DO UPDATE SET candidates_json = excluded.candidates_json`,
        ).run(m.provider_id, m.model_id, JSON.stringify(resolution.candidates), at);
      }

      const rec = resolution.status === 'resolved' ? benchmarks.byId.get(resolution.target) : undefined;
      // A reviewed bound is offered alongside the measured evidence, never
      // instead of it: `computeVQ` reaches it only after direct and calibrated
      // have both declined, so a model that gains a real figure supersedes its
      // bound automatically, with no overlay cleanup needed to stay correct.
      const reviewed = deps.bounds?.[m.model_id];
      const vq = computeVQ(
        resolution,
        {
          direct: rec?.intelligence,
          calibratable: rec?.designElo,
          group: rec?.vendor,
          bound: reviewed ? { value: reviewed.value, side: reviewed.side, reason: reviewed.reason } : undefined,
        },
        accepted ? calibration : null,
      );
      levels[vq.level]++;

      const previous = db
        .prepare(`SELECT value, evidence_level FROM model_scores WHERE provider_id=? AND model_id=? AND kind='VQ'`)
        .get(m.provider_id, m.model_id) as unknown as { value: number | null; evidence_level: string } | undefined;
      if (previous && (previous.value !== vq.value || previous.evidence_level !== vq.level)) {
        event.run(m.provider_id, m.model_id, 'score_changed', 'VQ', String(previous.value ?? '—'),
          String(vq.value ?? '—'), `${previous.evidence_level} -> ${vq.level}`, at);
      }

      upsert.run(m.provider_id, m.model_id, 'VQ', vq.value, vq.uncertainty, vq.bound, vq.level,
        vq.source, vq.sourceModelId, vq.rawValue, vq.rawField, vq.transformation, deps.sourceFetchedAt,
        vq.identityRule, vq.unratedReason, vq.precision, null, null,
        methodologyVersion, vq.level === 'calibrated' ? calibrationVersion : null, at);

      const vo = computeVO(
        {
          contextTokens: m.context_tokens,
          maxOutputTokens: m.output_tokens,
          tools: m.tools === null ? undefined : Boolean(m.tools),
          reasoning: m.reasoning === null ? undefined : Boolean(m.reasoning),
          structuredOutput: m.structured === null ? undefined : Boolean(m.structured),
          attachment: m.attachment === null ? undefined : Boolean(m.attachment),
          inputModalities: m.input_modalities ? (JSON.parse(m.input_modalities) as string[]) : undefined,
          costOutputPerM: m.cost_out_per_m,
          billingKind: (m.cost_kind ?? 'unknown') as CostKind,
        },
        pop,
        profile,
      );
      // 'derived' is VO's evidence level in the SAME vocabulary VQ uses.
      // Dimension coverage is not an evidence level and lives in `dimensions`.
      upsert.run(m.provider_id, m.model_id, 'VO', vo.value, null, null,
        'derived', 'models.dev', null, null, null, null, deps.sourceFetchedAt, null, null, 0,
        JSON.stringify({ dimensions: vo.dimensions, missing: vo.missing, notApplicable: vo.notApplicable }), profile.id,
        methodologyVersion, null, at);
    }
  });

  return {
    calibration,
    calibrationVersion,
    calibrationAccepted: accepted,
    levels,
    reviewQueue,
    total: models.length,
  };
}
