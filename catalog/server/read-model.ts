/**
 * The API projection.
 *
 * This module is the single place where scoring semantics are turned into a wire
 * shape. The frontend must never re-derive calibration, evidence class,
 * uncertainty, canonical identity or tie behaviour — if it could, there would be
 * two implementations of `venom-score-v1` and they would drift.
 *
 * Base contract (venom-score-v2): VQ and VO stay independently available. The
 * `model-score-v1` projection derives the declared composite without replacing
 * those inputs; a missing component stays missing and outside composite rank.
 */

import type { Db } from '../db/index.ts';
import { rankByVQ, sortContract, type ScoredModel } from '../sync/score/ranking.ts';
import type { EvidenceLevel } from '../sync/score/venom-score.ts';
import {
  MODEL_SCORE_POLICY,
  computeModelScore,
  modelScoreSortContract,
  rankByModelScore,
  type ModelScore,
} from '../sync/score/model-score.ts';
import { loadResolution, type ModelResolution } from '../sync/resolution-jobs.ts';
import { createEvaluationRepository, type OverallScoreRow } from '../sync/evaluation/repository.ts';
import { OVERALL_SCORE_POLICY, rankOverallScores } from '../sync/evaluation/score.ts';
import { resolveIdentity } from '../sync/evaluation/plan.ts';
import { loadPerformanceByModel, type ModelPerformance } from './performance.ts';

/** Compact provenance, carried on every model row. Enough to explain a score. */
export interface ProvenanceSummary {
  evidenceLevel: EvidenceLevel | 'derived';
  source: string | null;
  sourceModelId: string | null;
  identityRule: string | null;
  methodologyVersion: string;
  calibrationVersion: string | null;
  sourceFetchedAt: string | null;
  computedAt: string;
}

/** Everything needed to reconstruct a score without re-fetching upstream. */
export interface ProvenanceDetail extends ProvenanceSummary {
  rawValue: number | null;
  rawField: string | null;
  transformation: string | null;
  uncertainty: number | null;
}

/**
 * Where a row stands on identity. Three states, and a rejection is not a fourth.
 *
 *   resolved         proven to be a specific upstream model
 *   identity_review  candidates were examined; a human must decide
 *   unresolved       nothing upstream matched at all
 *
 * `rejectedCandidates` is evidence ABOUT reaching one of these states, never a
 * state itself — which is why it is a separate field rather than a value here.
 */
export type IdentityState = 'resolved' | 'identity_review' | 'unresolved';

/** One identity candidate that was examined and refused, with its evidence. */
export interface RejectedCandidateView {
  /** The refused upstream id, or null when the finding is that none exists. */
  candidate: string | null;
  verdict: 'candidate_rejected' | 'no_candidate_exists';
  why: string;
  /** Re-verifiable lines, so "what evidence caused the rejection" is answerable. */
  evidence: string[];
  source: string;
  sourceRef: string | null;
  sourceUrl: string | null;
  evidenceState: string;
  resolverVersion: string;
  /** What was known about the candidate when it was refused. */
  candidateMeta: Record<string, unknown> | null;
  reviewedAt: string | null;
  recordedAt: string;
}

/**
 * Per-field provenance, as a consumer sees it.
 *
 * Already stored for every resolved value; it was simply never projected, so the
 * UI could show a number and nothing about where it came from. Exposing it does
 * not change any semantic — it stops discarding one.
 */
export interface FactProvenanceView {
  value: unknown;
  source: string;
  sourceRef: string | null;
  sourceUrl: string | null;
  evidenceState: string | null;
  rawValue: unknown;
  resolverVersion: string | null;
  probeVersion: string | null;
  resolvedAt: string;
}

/** One field whose sources disagreed, as a consumer sees it. */
export interface FieldConflictView {
  field: string;
  /** Every distinct declared value, with the `provider/model` that declared it. */
  sides: { value: unknown; by: string }[];
  conflictType: string;
  status: 'open' | 'resolved';
  resolvedTo: string | null;
  detectedAt: string;
}

export interface ApiModel {
  providerId: string;
  /** The id to call this model with at this provider. */
  modelId: string;
  /**
   * The upstream identity this row was proven to be, or null when identity could
   * not be established. Two providers serving one model share this.
   */
  canonicalId: string | null;
  /**
   * Which model this row IS, from a vendor-namespaced listing in the spec feed.
   *
   * A different question from `canonicalId`, which is the reference-index entry
   * a SCORE was taken from. A model no index has listed yet has no canonical id
   * and can still be identified — `cline-pass/glm-5.3` is `z-ai/glm-5.3`, and
   * that same listing is where its context window came from. Never used to
   * attach a score, and never a substitute for a canonical id.
   */
  vendorModelId: string | null;
  /**
   * Which of the three identity states this row is in.
   *
   * Derived, never stored: `resolved` exactly when `canonicalId` is set, and
   * `identity_review` when candidates were examined and refused. A consumer that
   * had to infer this from a null `canonicalId` could not tell "investigated and
   * parked" from "never looked at".
   */
  identityState: IdentityState;
  /**
   * Candidates examined and refused, each with the evidence that refused it.
   *
   * Always present — an empty list asserts that nothing was tried, which is a
   * different claim from the field being absent. Nothing in here is ever a
   * resolved identity, and none of these values may appear as `canonicalId`.
   */
  rejectedCandidates: RejectedCandidateView[];
  displayName: string;
  /** `deprecated` when the vendor says so, else null. */
  lifecycle: string | null;
  /** active = served now; missing = absent from the last roster; retired = gone. */
  state: 'active' | 'missing' | 'retired';
  contextTokens: number | null;
  maxOutputTokens: number | null;
  inputModalities: string[] | null;
  capabilities: { tools: boolean | null; reasoning: boolean | null; structured: boolean | null; attachment: boolean | null };
  /**
   * Cost, with its semantics attached.
   *
   * `kind` is the answer to "what does this cost you here": free, included in a
   * subscription, billed per token, or genuinely unknown. `reference*` is the
   * list price elsewhere, carried for comparison and never presented as your
   * cost — a market rate in the effective field would be one seller's number
   * wearing another seller's label.
   */
  pricing: {
    kind: 'free' | 'included' | 'per_token' | 'unknown';
    inputPerMTokens: number | null;
    outputPerMTokens: number | null;
    referenceInPerMTokens: number | null;
    referenceOutPerMTokens: number | null;
    isFree: boolean | null;
  };
  vq: {
    value: number | null;
    uncertainty: number | null;
    bound: 'lower' | 'upper' | null;
    evidenceLevel: EvidenceLevel;
    /** Decimals the evidence supports. The client must not exceed this. */
    precision: number;
    /** Pre-rendered at the correct precision, so the client cannot get it wrong. */
    display: string;
    /**
     * Why there is no score. Null whenever there is one.
     *
     * Present so a dash is always accountable: an unresolvable identity, an
     * identity awaiting a human choice, a model nobody has benchmarked, and a
     * vendor the calibration was measured to be useless for are four different
     * facts that would otherwise render identically.
     */
    unratedReason: string | null;
    provenance: ProvenanceSummary | null;
  };
  vo: {
    /** Null when no operational fact was published for this model. */
    value: number | null;
    dimensions: Record<string, number | null>;
    /** Dimensions nobody published. A real gap in our data. */
    missingDimensions: string[];
    /**
     * Dimensions that do not apply to this offering.
     *
     * The opposite claim to `missingDimensions`, and the UI must not render them
     * the same way: a subscription model has no per-token price to publish, so
     * showing that as a gap would make full coverage unreachable for a whole
     * provider no matter how much evidence we gathered.
     */
    notApplicableDimensions: string[];
    profileId: string;
  };
  /** Server-derived 70/30 quality and operational score. */
  modelScore: ModelScore;
  /** Stored speed-probe aggregates; never inferred from availability or score. */
  performance: ModelPerformance;
  /** Auditable task-quality and offer-operations score used by the catalog UI. */
  overallScore: OverallScoreRow & { display: string };
  /** Server-owned lifecycle for source and benchmark follow-up work. */
  resolution: ModelResolution;
  /**
   * Whether every operational fact this catalog promises is resolved.
   *
   * A row that is not ready is NOT hidden and NOT deleted — it is served with
   * this flag and its `missingFacts`, so the inventory stays complete while the
   * main table stays trustworthy. Forcing a dash into a column would make the
   * table look uniform and quietly lower what a complete row means.
   */
  catalogReady: boolean;
  /** Named gaps, so "not ready" is always accountable. */
  missingFacts: string[];
  /**
   * Fields this offering's sources contradicted each other on.
   *
   * Reported separately from `missingFacts` because they are different claims: a
   * missing fact is "nobody published this", a conflict is "we were told two
   * different things and refused to pick". Both render as a dash, and a consumer
   * that cannot tell them apart cannot tell a data gap from a source dispute.
   * Always present — an empty list is an assertion, an absent key is not.
   */
  /**
   * Full conflict history for this offering, including resolved disputes.
   * Resolved rows remain available for audit and evidence display.
   */
  conflicts: FieldConflictView[];
  /**
   * The server-owned subset that still withholds a field value. Consumers that
   * decide whether a field is currently conflicted must use this view rather than
   * re-deriving status from the historical conflict list.
   */
  openConflicts: FieldConflictView[];
  /**
   * Where every resolved value came from, keyed by field.
   *
   * Carried on the row so a reader can ask "why should I believe this number"
   * without a second request. `evidenceState` is the part `source` cannot answer:
   * the seller's own feed and another seller's declaration both read as
   * 'models.dev'.
   */
  provenanceByField: Record<string, FactProvenanceView>;
  /** Quality rank. Null for unrated models — they are unplaced, not last. */
  qualityRank: number | null;
  /** True when this row shares its rank with others the evidence cannot separate. */
  tiedAtRank: boolean;
  /** Dense global rank by modelScore. Null when either component is absent. */
  modelRank: number | null;
  /** True when this row shares its model-score rank with another offering. */
  tiedAtModelRank: boolean;
  /** Dense global rank by full-precision overallScore. */
  overallRank: number | null;
  tiedAtOverallRank: boolean;
  firstSeenAt: string;
  lastSeenAt: string;
}

export interface ApiProvider {
  id: string;
  name: string;
  rosterUrl: string;
  liveModels: number;
  lastSuccessfulSyncAt: string | null;
  lastAttemptedSyncAt: string | null;
  lastOutcome: 'ok' | 'failed' | 'quarantined' | null;
  /** fresh | stale | never — derived from the last SUCCESSFUL sync, not the last attempt. */
  freshness: 'fresh' | 'stale' | 'never';
  /** Hours since the last successful sync, or null if there has never been one. */
  hoursSinceSuccess: number | null;
  qualityScored: number;
  /** Published composite model scores, not merely a VQ component. */
  modelScoreScored: number;
  /** Offers with a complete overall-score-v1 result. */
  overallScoreScored: number;
  unrated: number;
}

interface ScoreRow {
  provider_id: string; model_id: string; kind: string; value: number | null;
  uncertainty: number | null; bound: string | null; evidence_level: string;
  source: string | null; source_model_id: string | null; raw_value: number | null;
  raw_field: string | null; transformation: string | null; source_fetched_at: string | null;
  identity_rule: string | null; unrated_reason: string | null; precision_dp: number; dimensions: string | null;
  profile_id: string | null; methodology_ver: string; calibration_ver: string | null;
  computed_at: string;
}

interface ModelRow {
  provider_id: string; model_id: string; display_name: string | null; status: string;
  lifecycle_status: string | null;
  context_tokens: number | null; output_tokens: number | null; input_modalities: string | null;
  tools: number | null; reasoning: number | null; structured: number | null; attachment: number | null;
  cost_in_per_m: number | null; cost_out_per_m: number | null;
  ref_cost_in_per_m: number | null; ref_cost_out_per_m: number | null; cost_kind: string | null;
  first_seen_at: string; last_seen_at: string;
}

interface FactRow {
  provider_id: string; model_id: string; field: string; value: string | null;
  source: string; source_ref: string | null; source_url: string | null;
  evidence_state: string | null; raw_value: string | null;
  resolver_version: string | null; probe_version: string | null; resolved_at: string;
}

interface RejectionRow {
  provider_id: string; model_id: string; rejected_candidate: string; verdict: string;
  reason: string; evidence_json: string | null; source: string; source_ref: string | null;
  source_url: string | null; evidence_state: string; resolver_version: string;
  candidate_meta_json: string | null; reviewed_at: string | null; recorded_at: string;
}

interface ConflictRow {
  provider_id: string; model_id: string; field: string; sides_json: string;
  conflict_type: string; status: string; resolved_to: string | null; detected_at: string;
}

const b = (v: number | null) => (v === null ? null : Boolean(v));

/**
 * Which identity state a row is in.
 *
 * A resolved canonical id wins outright — and note the ordering: a row can have
 * BOTH a resolved identity and historical rejections (candidates refused before
 * the right one was found), and it is resolved. What can never happen is the
 * reverse: rejections alone never produce `resolved`, because the only thing that
 * sets `canonicalId` is the deterministic resolver, which never reads this table.
 */
function identityStateOf(
  canonicalId: string | null,
  rejectionCount: number,
  ambiguousOpen: boolean,
): IdentityState {
  if (canonicalId !== null) return 'resolved';
  // Two ways a row lands in review, and they mean the same thing: the resolver
  // found several candidates it may not choose between, or a human examined
  // candidates and refused them. Treating only the second as "in review" left a
  // row ambiguous in the identity_review TABLE and merely unresolved in the API,
  // which is the same name carrying two meanings.
  return rejectionCount > 0 || ambiguousOpen ? 'identity_review' : 'unresolved';
}

/**
 * The composite key for anything keyed by (provider, model).
 *
 * A NUL separator cannot occur inside either half, so two different pairs can
 * never collide on one key. Defined once because scores and conflicts must agree
 * on it — two hand-written templates would be one typo away from silently
 * attributing a conflict to the wrong offering.
 */
const conflictKey = (providerId: string, modelId: string) => `${providerId}\u0000${modelId}`;

/** Format at exactly the precision the evidence supports. */
export function displayVQ(value: number | null, precision: number, bound: string | null): string {
  if (value === null) return '—';
  const n = value.toFixed(precision);
  return bound === 'lower' ? `≥ ${n}` : bound === 'upper' ? `≤ ${n}` : n;
}

/** How stale a provider may be before it is flagged. One scheduler period plus slack. */
export const STALE_AFTER_HOURS = 12;

export function loadModels(db: Db, opts: { includeRetired?: boolean; now?: () => Date } = {}): ApiModel[] {
  const statuses = opts.includeRetired ? ['active', 'missing', 'retired'] : ['active', 'missing'];
  const rows = db
    .prepare(`SELECT * FROM models WHERE status IN (${statuses.map(() => '?').join(',')}) ORDER BY provider_id, model_id`)
    .all(...statuses) as unknown as ModelRow[];
  const scores = db.prepare('SELECT * FROM model_scores').all() as unknown as ScoreRow[];
  const evaluation = createEvaluationRepository(db);
  const performanceByModel = loadPerformanceByModel(db);

  const byKey = new Map<string, { VQ?: ScoreRow; VO?: ScoreRow }>();
  for (const s of scores) {
    const k = `${s.provider_id}\u0000${s.model_id}`;
    const e = byKey.get(k) ?? {};
    if (s.kind === 'VQ') e.VQ = s;
    else e.VO = s;
    byKey.set(k, e);
  }

  // Rejections are keyed the same way, and deliberately read into their own map
  // rather than joined onto the model row: a rejection is evidence about how a
  // state was reached, and merging it into the row would invite treating it as
  // the state.
  // Rows the RESOLVER parked as ambiguous, as opposed to ones a human refused.
  const ambiguousOpen = new Set(
    (
      db
        .prepare(`SELECT provider_id, model_id FROM identity_review WHERE status='open'`)
        .all() as unknown as { provider_id: string; model_id: string }[]
    ).map((r) => conflictKey(r.provider_id, r.model_id)),
  );

  // Every resolved fact's provenance, keyed by (provider, model) then field.
  const factsByModel = new Map<string, Record<string, FactProvenanceView>>();
  for (const f of db
    .prepare('SELECT * FROM model_facts ORDER BY provider_id, model_id, field')
    .all() as unknown as FactRow[]) {
    const k = conflictKey(f.provider_id, f.model_id);
    const rec = factsByModel.get(k) ?? {};
    rec[f.field] = {
      value: f.value === null ? null : JSON.parse(f.value),
      source: f.source,
      sourceRef: f.source_ref,
      sourceUrl: f.source_url,
      evidenceState: f.evidence_state,
      rawValue: f.raw_value === null ? null : JSON.parse(f.raw_value),
      resolverVersion: f.resolver_version,
      probeVersion: f.probe_version,
      resolvedAt: f.resolved_at,
    };
    factsByModel.set(k, rec);
  }

  const rejectionsByModel = new Map<string, RejectedCandidateView[]>();
  for (const r of db
    .prepare('SELECT * FROM identity_rejections ORDER BY provider_id, model_id, rejected_candidate')
    .all() as unknown as RejectionRow[]) {
    const k = conflictKey(r.provider_id, r.model_id);
    const list = rejectionsByModel.get(k) ?? [];
    list.push({
      // Stored as '' because it is part of the primary key; restored to null so
      // "no candidate exists" is not mistaken for a candidate named "".
      candidate: r.rejected_candidate === '' ? null : r.rejected_candidate,
      verdict: r.verdict as RejectedCandidateView['verdict'],
      why: r.reason,
      evidence: r.evidence_json ? (JSON.parse(r.evidence_json) as string[]) : [],
      source: r.source,
      sourceRef: r.source_ref,
      sourceUrl: r.source_url,
      evidenceState: r.evidence_state,
      resolverVersion: r.resolver_version,
      candidateMeta: r.candidate_meta_json ? (JSON.parse(r.candidate_meta_json) as Record<string, unknown>) : null,
      reviewedAt: r.reviewed_at,
      recordedAt: r.recorded_at,
    });
    rejectionsByModel.set(k, list);
  }

  // Conflicts are keyed by (provider, model) exactly like scores: a disagreement
  // is recorded against the offering it was detected on, so another seller of
  // the same model never inherits it.
  const conflictsByModel = new Map<string, FieldConflictView[]>();
  for (const c of db
    .prepare('SELECT * FROM model_conflicts ORDER BY provider_id, model_id, field')
    .all() as unknown as ConflictRow[]) {
    const k = conflictKey(c.provider_id, c.model_id);
    const list = conflictsByModel.get(k) ?? [];
    list.push({
      field: c.field,
      sides: JSON.parse(c.sides_json) as { value: unknown; by: string }[],
      conflictType: c.conflict_type,
      status: c.status as 'open' | 'resolved',
      resolvedTo: c.resolved_to,
      detectedAt: c.detected_at,
    });
    conflictsByModel.set(k, list);
  }

  const models: ApiModel[] = rows.map((m) => {
    const s = byKey.get(`${m.provider_id}\u0000${m.model_id}`) ?? {};
    const vq = s.VQ;
    const vo = s.VO;
    const voParsed = vo?.dimensions ? (JSON.parse(vo.dimensions) as { dimensions: Record<string, number | null>; missing: string[]; notApplicable?: string[] }) : null;
    const level = (vq?.evidence_level ?? 'unrated') as EvidenceLevel;
    const rejections = rejectionsByModel.get(conflictKey(m.provider_id, m.model_id)) ?? [];
    const modelScore = computeModelScore({
      providerId: m.provider_id,
      modelId: m.model_id,
      vq: {
        value: vq?.value ?? null,
        uncertainty: vq?.uncertainty ?? null,
        bound: (vq?.bound ?? null) as 'lower' | 'upper' | null,
        evidenceLevel: level,
      },
      vo: {
        value: vo?.value ?? null,
        missingDimensions: voParsed?.missing ?? [],
      },
    });
    const overallScore = evaluation.overall(m.provider_id, m.model_id) ?? {
      providerId: m.provider_id,
      modelId: m.model_id,
      value: null,
      qualityScore: null,
      operationalScore: null,
      qualityCoverage: { scored: 0, applicable: 0, percent: 0 },
      overallCoverage: { scored: 0, applicable: 0, percent: 0 },
      includedDimensions: [],
      excludedDimensions: [],
      status: 'unknown' as const,
      uncertainty: null,
      reasons: ['not_evaluated'],
      methodologyVersion: OVERALL_SCORE_POLICY.methodologyVersion,
      computedAt: '',
    };

    const conflicts = conflictsByModel.get(conflictKey(m.provider_id, m.model_id)) ?? [];
    const openConflicts = conflicts.filter((conflict) => conflict.status === 'open');

    return {
      providerId: m.provider_id,
      modelId: m.model_id,
      canonicalId: vq?.source_model_id ?? null,
      vendorModelId: (() => {
        const f = factsByModel.get(conflictKey(m.provider_id, m.model_id))?.vendorIdentity;
        return typeof f?.value === 'string' ? f.value : null;
      })(),
      identityState: identityStateOf(
        vq?.source_model_id ?? null,
        rejections.length,
        ambiguousOpen.has(conflictKey(m.provider_id, m.model_id)),
      ),
      rejectedCandidates: rejections,
      displayName: m.display_name ?? m.model_id,
      // The vendor's own lifecycle marker, reported rather than acted on. A
      // deprecated model still answers — OpenCode's picker hides it, but a
      // reference catalog that silently dropped a working model would be hiding
      // evidence it had already paid for. The reader is told and decides.
      lifecycle: m.lifecycle_status ?? null,
      state: m.status as ApiModel['state'],
      contextTokens: m.context_tokens,
      maxOutputTokens: m.output_tokens,
      inputModalities: m.input_modalities ? (JSON.parse(m.input_modalities) as string[]) : null,
      capabilities: { tools: b(m.tools), reasoning: b(m.reasoning), structured: b(m.structured), attachment: b(m.attachment) },
      pricing: {
        kind: (m.cost_kind ?? 'unknown') as ApiModel['pricing']['kind'],
        inputPerMTokens: m.cost_in_per_m,
        outputPerMTokens: m.cost_out_per_m,
        referenceInPerMTokens: m.ref_cost_in_per_m,
        referenceOutPerMTokens: m.ref_cost_out_per_m,
        // Only claim "free" when a price was actually published as zero. An
        // absent price is unknown, and calling that free would be a guess.
        isFree: m.cost_kind === 'free' ? true : m.cost_kind === 'unknown' ? null : false,
      },
      vq: {
        value: vq?.value ?? null,
        uncertainty: vq?.uncertainty ?? null,
        bound: (vq?.bound ?? null) as 'lower' | 'upper' | null,
        evidenceLevel: level,
        precision: vq?.precision_dp ?? 0,
        display: displayVQ(vq?.value ?? null, vq?.precision_dp ?? 0, vq?.bound ?? null),
        unratedReason: vq?.unrated_reason ?? null,
        provenance: vq?.value === null || !vq ? null : {
          evidenceLevel: level,
          source: vq.source,
          sourceModelId: vq.source_model_id,
          identityRule: vq.identity_rule,
          methodologyVersion: vq.methodology_ver,
          calibrationVersion: vq.calibration_ver,
          sourceFetchedAt: vq.source_fetched_at,
          computedAt: vq.computed_at,
        },
      },
      vo: {
        value: vo?.value ?? null,
        dimensions: voParsed?.dimensions ?? {},
        missingDimensions: voParsed?.missing ?? [],
        notApplicableDimensions: voParsed?.notApplicable ?? [],
        profileId: vo?.profile_id ?? 'balanced',
      },
      modelScore,
      performance: performanceByModel.get(conflictKey(m.provider_id, m.model_id)) ?? {
        status: 'not_measured', runId: null, evaluatedAt: null, sampleCount: 0, successfulSamples: 0,
        ttftMedianSeconds: null, outputTokensPerSecondMedian: null, endToEndP95Seconds: null, successRate: null, speedScore: null,
      },
      overallScore: { ...overallScore, display: overallScore.value === null ? '—' : `${overallScore.value.toFixed(1)}%` },
      resolution: loadResolution(db, m.provider_id, m.model_id) ?? {
        state: 'complete', reasons: [], firstDetectedAt: null, lastAttemptAt: null, nextAttemptAt: null,
      },
      conflicts,
      openConflicts,
      provenanceByField: factsByModel.get(conflictKey(m.provider_id, m.model_id)) ?? {},
      catalogReady: false,
      missingFacts: [],
      qualityRank: null,
      tiedAtRank: false,
      modelRank: null,
      tiedAtModelRank: false,
      overallRank: null,
      tiedAtOverallRank: false,
      firstSeenAt: m.first_seen_at,
      lastSeenAt: m.last_seen_at,
    };
  });

  // The completeness gate. VQ is deliberately NOT part of it: a model with no
  // published benchmark is honestly unrated, which is a statement about the
  // world rather than a hole in our data.
  for (const m of models) {
    const missing: string[] = [];
    if (m.contextTokens === null) missing.push('context');
    if (m.maxOutputTokens === null) missing.push('maxOutput');
    if (m.inputModalities === null) missing.push('modalities');
    if (m.capabilities.tools === null) missing.push('tools');
    if (m.capabilities.reasoning === null) missing.push('reasoning');
    if (m.capabilities.structured === null) missing.push('structured');
    if (m.pricing.kind === 'unknown') missing.push('cost');
    m.missingFacts = missing;
    m.catalogReady = missing.length === 0;
  }

  // Ranking is computed here, once, by the same module the tests exercise — the
  // client receives ranks and never re-derives them.
  const scored: ScoredModel[] = models.map((m) => ({
    providerId: m.providerId,
    modelId: m.modelId,
    vq: { kind: 'VQ', value: m.vq.value, uncertainty: m.vq.uncertainty, bound: m.vq.bound, level: m.vq.evidenceLevel,
      source: null, sourceModelId: null, identityRule: null, rawValue: null, rawField: null, transformation: null,
      unratedReason: m.vq.unratedReason as never, precision: m.vq.precision },
    vo: { kind: 'VO', value: m.vo.value, dimensions: {} as never, missing: [], notApplicable: [], profileId: m.vo.profileId },
  }));
  const { ranked } = rankByVQ(scored);
  const rankOf = new Map<string, { rank: number; tied: boolean }>();
  for (const g of ranked)
    for (const member of g.members)
      rankOf.set(`${member.providerId}\u0000${member.modelId}`, { rank: g.rank, tied: g.members.length > 1 });

  for (const m of models) {
    const r = rankOf.get(`${m.providerId}\u0000${m.modelId}`);
    m.qualityRank = r?.rank ?? null;
    m.tiedAtRank = r?.tied ?? false;
  }

  const { ranked: modelScoreRanks } = rankByModelScore(models);
  const modelRankOf = new Map<string, { rank: number; tied: boolean }>();
  for (const group of modelScoreRanks)
    for (const member of group.members)
      modelRankOf.set(
        `${member.providerId}\u0000${member.modelId}`,
        { rank: group.rank, tied: group.members.length > 1 },
      );

  for (const m of models) {
    const rank = modelRankOf.get(`${m.providerId}\u0000${m.modelId}`);
    m.modelRank = rank?.rank ?? null;
    m.tiedAtModelRank = rank?.tied ?? false;
  }

  const overallRanks = rankOverallScores(models.map((model) => ({
    providerId: model.providerId,
    modelId: model.modelId,
    value: model.overallScore.status === 'complete' ? model.overallScore.value : null,
    uncertainty: model.overallScore.uncertainty,
  })));
  const overallRankOf = new Map(overallRanks.map((item) => [
    conflictKey(item.providerId, item.modelId), { rank: item.rank, tied: item.tied },
  ]));
  for (const model of models) {
    const item = overallRankOf.get(conflictKey(model.providerId, model.modelId));
    model.overallRank = item?.rank ?? null;
    model.tiedAtOverallRank = item?.tied ?? false;
  }
  return models;
}

export function loadProviders(db: Db, models: ApiModel[], now: Date = new Date()): ApiProvider[] {
  const rows = db.prepare('SELECT * FROM providers ORDER BY id').all() as unknown as {
    id: string; name: string; roster_url: string; last_sync_at: string | null;
    last_success_at: string | null; last_outcome: string | null;
  }[];

  return rows.map((p) => {
    const mine = models.filter((m) => m.providerId === p.id && m.state !== 'retired');
    const hours = p.last_success_at ? (now.getTime() - new Date(p.last_success_at).getTime()) / 3_600_000 : null;
    return {
      id: p.id,
      name: p.name,
      rosterUrl: p.roster_url,
      liveModels: mine.length,
      lastSuccessfulSyncAt: p.last_success_at,
      lastAttemptedSyncAt: p.last_sync_at,
      lastOutcome: p.last_outcome as ApiProvider['lastOutcome'],
      // Freshness follows the last SUCCESS. A provider that failed ten minutes
      // ago is not fresh just because it was attempted recently.
      freshness: hours === null ? 'never' : hours <= STALE_AFTER_HOURS ? 'fresh' : 'stale',
      hoursSinceSuccess: hours === null ? null : Math.round(hours * 10) / 10,
      qualityScored: mine.filter((m) => m.vq.value !== null).length,
      modelScoreScored: mine.filter((m) => m.modelScore.value !== null).length,
      overallScoreScored: mine.filter((m) => m.overallScore.status === 'complete' && m.overallScore.value !== null).length,
      unrated: mine.filter((m) => m.vq.value === null).length,
    };
  });
}

export interface CatalogMeta {
  methodologyVersion: string;
  profileId: string;
  scoringPolicy: typeof MODEL_SCORE_POLICY;
  liveModels: number;
  /** Rows passing the completeness gate — what the main table shows. */
  catalogReady: number;
  /** Rows held back for review, with their gaps named per row. */
  needsVerification: number;
  qualityScored: number;
  /** Live models with a non-null server-derived composite score. */
  modelScoreScored: number;
  overallScoreScored: number;
  operationalScored: number;
  unrated: number;
  /**
   * How many live models have at least one field withheld by a source
   * disagreement, and how many models each disputed field affects.
   *
   * Counted per MODEL, not per conflict row: one model with three disputed
   * fields is one affected model, and reporting it as three would overstate how
   * much of the catalogue is in dispute.
   */
  conflictedModels: number;
  conflictsByField: Record<string, number>;
  /**
   * The identity partition: three mutually exclusive states summing to
   * liveModels.
   *
   * It contains ONLY identity. An earlier version split `resolved` by whether a
   * benchmark existed, which is a quality question — and since that bucket
   * equalled `qualityScored` exactly, a reader could add it to the identity
   * states and get a total that looked plausible and was wrong. Quality lives in
   * `qualityScored`/`unrated`; "resolved but unbenchmarked" is
   * `identity.resolved - qualityScored`, derivable and deliberately not stored
   * here.
   */
  identity: { resolved: number; identityReview: number; unresolved: number };
  /**
   * Why rows are in review and how much evidence sits behind them.
   *
   * Explicitly NOT a partition: `ambiguousOpen` and `withRejectedCandidates` are
   * both subsets of `identity.identityReview` and a row can be in both.
   */
  identityDetail: {
    /** In review because the resolver found candidates it may not choose between. */
    ambiguousOpen: number;
    /** In review because a human examined candidates and refused them. */
    withRejectedCandidates: number;
    /** Refused-candidate records, counted per CANDIDATE rather than per model. */
    rejectedCandidates: number;
  };
  /**
   * Count per identity rule. This is a DIFFERENT axis from evidence level: a
   * rule can appear on a row that resolved but carries no benchmark, so these
   * counts sum to (resolvedWithEvidence + resolvedWithoutEvidence), not to
   * qualityScored. Reported separately so the two are never read as one
   * partition.
   */
  identityRules: Record<string, number>;
  calibration: {
    version: string | null; accepted: boolean; n: number; rho: number;
    looRmse: number; baselineSd: number; excludedGroups: string[];
  } | null;
  sortContracts: {
    vq: ReturnType<typeof sortContract>;
    vo: ReturnType<typeof sortContract>;
    modelScore: ReturnType<typeof modelScoreSortContract>;
    overallScore: { field: 'overallScore.value'; direction: 'desc'; nulls: 'last'; scope: 'global'; tieRule: 'uncertainty-overlap' };
  };
  snapshotGeneratedAt: string | null;
}

export function loadMeta(db: Db, models: ApiModel[]): CatalogMeta {
  const live = models.filter((m) => m.state !== 'retired');
  const rules = db
    .prepare(`SELECT identity_rule, COUNT(*) n FROM model_scores WHERE kind='VQ' AND identity_rule IS NOT NULL GROUP BY identity_rule`)
    .all() as unknown as { identity_rule: string; n: number }[];
  const cal = db.prepare('SELECT * FROM calibrations ORDER BY fitted_at DESC LIMIT 1').get() as unknown as
    | { version: string; accepted: number; n: number; rho: number; loo_rmse: number; baseline_sd: number; excluded_json: string }
    | undefined;
  const ambiguous = db.prepare(`SELECT COUNT(*) n FROM identity_review WHERE status='open'`).get() as unknown as { n: number };
  const method = db.prepare(`SELECT methodology_ver FROM model_scores LIMIT 1`).get() as unknown as { methodology_ver: string } | undefined;

  const withEvidence = live.filter((m) => m.vq.value !== null).length;

  return {
    methodologyVersion: method?.methodology_ver ?? 'unknown',
    profileId: live[0]?.vo.profileId ?? 'balanced',
    scoringPolicy: MODEL_SCORE_POLICY,
    liveModels: live.length,
    catalogReady: live.filter((m) => m.catalogReady).length,
    needsVerification: live.filter((m) => !m.catalogReady).length,
    qualityScored: withEvidence,
    modelScoreScored: live.filter((m) => m.modelScore.value !== null).length,
    overallScoreScored: live.filter((m) => m.overallScore.status === 'complete' && m.overallScore.value !== null).length,
    operationalScored: live.filter((m) => m.vo.value !== null).length,
    unrated: live.length - withEvidence,
    conflictedModels: live.filter((m) => m.openConflicts.length > 0).length,
    conflictsByField: live.reduce<Record<string, number>>((acc, m) => {
      for (const c of m.openConflicts) acc[c.field] = (acc[c.field] ?? 0) + 1;
      return acc;
    }, {}),
    // Counted straight off the per-row states, so the summary and the rows can
    // never disagree — a second derivation is a second chance to be wrong.
    identity: {
      resolved: live.filter((m) => m.identityState === 'resolved').length,
      identityReview: live.filter((m) => m.identityState === 'identity_review').length,
      unresolved: live.filter((m) => m.identityState === 'unresolved').length,
    },
    identityDetail: {
      ambiguousOpen: ambiguous.n,
      withRejectedCandidates: live.filter((m) => m.rejectedCandidates.length > 0).length,
      rejectedCandidates: live.reduce((n, m) => n + m.rejectedCandidates.length, 0),
    },
    identityRules: Object.fromEntries(rules.map((r) => [r.identity_rule, r.n])),
    calibration: cal
      ? { version: cal.version, accepted: Boolean(cal.accepted), n: cal.n, rho: cal.rho,
          looRmse: cal.loo_rmse, baselineSd: cal.baseline_sd, excludedGroups: JSON.parse(cal.excluded_json ?? '[]') }
      : null,
    sortContracts: {
      vq: sortContract('vq'),
      vo: sortContract('vo'),
      modelScore: modelScoreSortContract(),
      overallScore: { field: 'overallScore.value', direction: 'desc', nulls: 'last', scope: 'global', tieRule: 'uncertainty-overlap' },
    },
    snapshotGeneratedAt: null,
  };
}

/** Full provenance for one model, for the detail panel. */
export function loadProvenance(db: Db, providerId: string, modelId: string): ProvenanceDetail | null {
  const r = db
    .prepare(`SELECT * FROM model_scores WHERE provider_id=? AND model_id=? AND kind='VQ'`)
    .get(providerId, modelId) as unknown as ScoreRow | undefined;
  if (!r || r.value === null) return null;
  return {
    evidenceLevel: r.evidence_level as EvidenceLevel,
    source: r.source,
    sourceModelId: r.source_model_id,
    identityRule: r.identity_rule,
    methodologyVersion: r.methodology_ver,
    calibrationVersion: r.calibration_ver,
    sourceFetchedAt: r.source_fetched_at,
    computedAt: r.computed_at,
    rawValue: r.raw_value,
    rawField: r.raw_field,
    transformation: r.transformation,
    uncertainty: r.uncertainty,
  };
}

/** Sanitized evaluator evidence for audit tools. Raw provider responses and credentials are never stored. */
export function loadEvaluationDiagnostics(db: Db, providerId: string, modelId: string) {
  const exists = db.prepare(`SELECT 1 found FROM models WHERE provider_id=? AND model_id=?`).get(providerId, modelId) as
    | { found: number }
    | undefined;
  if (!exists) return null;
  const repository = createEvaluationRepository(db);
  // The planner's rule, not a second copy of it.
  //
  // This read `canonicalId ?? vendorModelId`, which is two of the three inputs
  // `resolveOfferIdentityId` weighs — it knows nothing of the reviewed
  // `evaluationIdentity` override. For the two offers that carry one, the two
  // resolutions disagreed: the planner keyed evidence under
  // `opencode-zen/x-preview-f-free` while this returned null, so the diagnostics
  // reported no dimensions at all and the Evaluate dialog showed an empty
  // evidence panel for a model the service had scores and withdrawals for.
  //
  // Quality is keyed by identity everywhere. There can only be one answer to
  // which identity an offer has.
  const identityId = resolveIdentity(db, providerId, modelId);
  const runs = db.prepare(`
    SELECT id, identity_id identityId, dimension, run_kind runKind, status,
           evaluator_version evaluatorVersion, rubric_version rubricVersion,
           test_set_version testSetVersion, test_set_hash testSetHash,
           methodology_ver methodologyVersion, region, independent_run_key independentRunKey,
           error_code errorCode, started_at startedAt, finished_at finishedAt,
           (SELECT COUNT(*) FROM evaluation_samples s WHERE s.run_id=evaluation_runs.id) sampleCount
      FROM evaluation_runs
     WHERE provider_id=? AND model_id=?
     ORDER BY started_at DESC, id DESC
  `).all(providerId, modelId);
  const overrides = db.prepare(`
    SELECT dimension, score, raw_rate rawRate, uncertainty, confidence,
           sample_count sampleCount, status, reason, run_ids_json runIds,
           evidence_json evidence, methodology_ver methodologyVersion,
           evaluated_at evaluatedAt
      FROM provider_quality_overrides
     WHERE provider_id=? AND model_id=?
     ORDER BY dimension
  `).all(providerId, modelId);
  return {
    providerId,
    modelId,
    identityId,
    identityDimensions: identityId ? repository.identityDimensions(identityId) : [],
    offerDimensions: repository.offerDimensions(providerId, modelId),
    overallScore: repository.overall(providerId, modelId),
    runs,
    providerQualityOverrides: overrides,
  };
}
