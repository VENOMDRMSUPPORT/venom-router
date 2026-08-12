/**
 * The API projection.
 *
 * This module is the single place where scoring semantics are turned into a wire
 * shape. The frontend must never re-derive calibration, evidence class,
 * uncertainty, canonical identity or tie behaviour — if it could, there would be
 * two implementations of `venom-score-v1` and they would drift.
 *
 * Frozen contract (venom-score-v1): VQ and VO stay separate, a missing VQ stays
 * missing, unrated models are outside quality ranking, and uncertainty decides
 * ties.
 */

import type { Db } from '../db/index.ts';
import { rankByVQ, sortContract, type ScoredModel } from '../sync/score/ranking.ts';
import type { EvidenceLevel } from '../sync/score/venom-score.ts';

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

export interface ApiModel {
  providerId: string;
  /** The id to call this model with at this provider. */
  modelId: string;
  /**
   * The upstream identity this row was proven to be, or null when identity could
   * not be established. Two providers serving one model share this.
   */
  canonicalId: string | null;
  displayName: string;
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
    provenance: ProvenanceSummary | null;
  };
  vo: {
    /** Null when no operational fact was published for this model. */
    value: number | null;
    dimensions: Record<string, number | null>;
    missingDimensions: string[];
    profileId: string;
  };
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
  /** Quality rank. Null for unrated models — they are unplaced, not last. */
  qualityRank: number | null;
  /** True when this row shares its rank with others the evidence cannot separate. */
  tiedAtRank: boolean;
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
  unrated: number;
}

interface ScoreRow {
  provider_id: string; model_id: string; kind: string; value: number | null;
  uncertainty: number | null; bound: string | null; evidence_level: string;
  source: string | null; source_model_id: string | null; raw_value: number | null;
  raw_field: string | null; transformation: string | null; source_fetched_at: string | null;
  identity_rule: string | null; precision_dp: number; dimensions: string | null;
  profile_id: string | null; methodology_ver: string; calibration_ver: string | null;
  computed_at: string;
}

interface ModelRow {
  provider_id: string; model_id: string; display_name: string | null; status: string;
  context_tokens: number | null; output_tokens: number | null; input_modalities: string | null;
  tools: number | null; reasoning: number | null; structured: number | null; attachment: number | null;
  cost_in_per_m: number | null; cost_out_per_m: number | null;
  ref_cost_in_per_m: number | null; ref_cost_out_per_m: number | null; cost_kind: string | null;
  first_seen_at: string; last_seen_at: string;
}

const b = (v: number | null) => (v === null ? null : Boolean(v));

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

  const byKey = new Map<string, { VQ?: ScoreRow; VO?: ScoreRow }>();
  for (const s of scores) {
    const k = `${s.provider_id}\u0000${s.model_id}`;
    const e = byKey.get(k) ?? {};
    if (s.kind === 'VQ') e.VQ = s;
    else e.VO = s;
    byKey.set(k, e);
  }

  const models: ApiModel[] = rows.map((m) => {
    const s = byKey.get(`${m.provider_id}\u0000${m.model_id}`) ?? {};
    const vq = s.VQ;
    const vo = s.VO;
    const voParsed = vo?.dimensions ? (JSON.parse(vo.dimensions) as { dimensions: Record<string, number | null>; missing: string[] }) : null;
    const level = (vq?.evidence_level ?? 'unrated') as EvidenceLevel;

    return {
      providerId: m.provider_id,
      modelId: m.model_id,
      canonicalId: vq?.source_model_id ?? null,
      displayName: m.display_name ?? m.model_id,
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
        profileId: vo?.profile_id ?? 'balanced',
      },
      catalogReady: false,
      missingFacts: [],
      qualityRank: null,
      tiedAtRank: false,
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
      source: null, sourceModelId: null, identityRule: null, rawValue: null, rawField: null, transformation: null, precision: m.vq.precision },
    vo: { kind: 'VO', value: m.vo.value, dimensions: {} as never, missing: [], profileId: m.vo.profileId },
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
      unrated: mine.filter((m) => m.vq.value === null).length,
    };
  });
}

export interface CatalogMeta {
  methodologyVersion: string;
  profileId: string;
  liveModels: number;
  /** Rows passing the completeness gate — what the main table shows. */
  catalogReady: number;
  /** Rows held back for review, with their gaps named per row. */
  needsVerification: number;
  qualityScored: number;
  operationalScored: number;
  unrated: number;
  /** Exclusive partition of identity outcomes. Sums to liveModels. */
  identity: { resolvedWithEvidence: number; resolvedWithoutEvidence: number; unresolved: number; ambiguousOpen: number };
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
  sortContracts: { vq: ReturnType<typeof sortContract>; vo: ReturnType<typeof sortContract> };
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
  const resolvedNoEvidence = live.filter((m) => m.vq.value === null && m.canonicalId !== null).length;

  return {
    methodologyVersion: method?.methodology_ver ?? 'unknown',
    profileId: live[0]?.vo.profileId ?? 'balanced',
    liveModels: live.length,
    catalogReady: live.filter((m) => m.catalogReady).length,
    needsVerification: live.filter((m) => !m.catalogReady).length,
    qualityScored: withEvidence,
    operationalScored: live.filter((m) => m.vo.value !== null).length,
    unrated: live.length - withEvidence,
    identity: {
      resolvedWithEvidence: withEvidence,
      resolvedWithoutEvidence: resolvedNoEvidence,
      unresolved: live.length - withEvidence - resolvedNoEvidence,
      ambiguousOpen: ambiguous.n,
    },
    identityRules: Object.fromEntries(rules.map((r) => [r.identity_rule, r.n])),
    calibration: cal
      ? { version: cal.version, accepted: Boolean(cal.accepted), n: cal.n, rho: cal.rho,
          looRmse: cal.loo_rmse, baselineSd: cal.baseline_sd, excludedGroups: JSON.parse(cal.excluded_json ?? '[]') }
      : null,
    sortContracts: { vq: sortContract('vq'), vo: sortContract('vo') },
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
