/**
 * Catalog API client.
 *
 * The service is authoritative for every scoring semantic. Nothing in this file
 * — or anywhere else in the SPA — recomputes calibration, evidence class,
 * uncertainty, canonical identity or ranking. Those exist once, on the server,
 * and arrive here already decided.
 *
 * If the service is unreachable the SPA falls back to the snapshot baked at
 * build time, and says so. A stale page that admits it is stale beats a blank
 * one; a stale page that pretends to be live is the thing to avoid.
 */

export type EvidenceLevel = 'measured' | 'calibrated' | 'bounded' | 'unrated';

export interface Provenance {
  evidenceLevel: EvidenceLevel | 'derived';
  source: string | null;
  sourceModelId: string | null;
  identityRule: string | null;
  methodologyVersion: string;
  calibrationVersion: string | null;
  sourceFetchedAt: string | null;
  computedAt: string;
}

export interface ApiModel {
  providerId: string;
  modelId: string;
  canonicalId: string | null;
  displayName: string;
  state: 'active' | 'missing' | 'retired';
  contextTokens: number | null;
  maxOutputTokens: number | null;
  inputModalities: string[] | null;
  capabilities: { tools: boolean | null; reasoning: boolean | null; structured: boolean | null; attachment: boolean | null };
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
    precision: number;
    display: string;
    provenance: Provenance | null;
  };
  vo: { value: number | null; dimensions: Record<string, number | null>; missingDimensions: string[]; profileId: string };
  qualityRank: number | null;
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
  freshness: 'fresh' | 'stale' | 'never';
  hoursSinceSuccess: number | null;
  qualityScored: number;
  unrated: number;
}

export interface CatalogMeta {
  methodologyVersion: string;
  profileId: string;
  liveModels: number;
  qualityScored: number;
  operationalScored: number;
  unrated: number;
  identity: { resolvedWithEvidence: number; resolvedWithoutEvidence: number; unresolved: number; ambiguousOpen: number };
  identityRules: Record<string, number>;
  calibration: { version: string | null; accepted: boolean; n: number; rho: number; looRmse: number; baselineSd: number; excludedGroups: string[] } | null;
  sortContracts: Record<string, { key: string; field: string; unplacedLabel: string; tieRule: string }>;
}

export interface Change {
  class: string;
  providerId: string;
  modelId: string;
  field: string | null;
  from: string | null;
  to: string | null;
  note: string | null;
  observedAt: string;
}

export interface CatalogData {
  models: ApiModel[];
  providers: ApiProvider[];
  meta: CatalogMeta;
  /** 'live' when the service answered; 'snapshot' when it did not. */
  origin: 'live' | 'snapshot';
  /** Only set for a snapshot fallback: when that snapshot was generated. */
  snapshotGeneratedAt?: string;
}

const BASE = '/v1';

async function json<T>(path: string, signal?: AbortSignal): Promise<T> {
  const res = await fetch(`${BASE}${path}`, { signal });
  if (!res.ok && res.status !== 503) throw new Error(`${path} -> HTTP ${res.status}`);
  return (await res.json()) as T;
}

export async function fetchCatalog(signal?: AbortSignal): Promise<CatalogData> {
  try {
    const [modelsRes, providersRes] = await Promise.all([
      json<{ models: ApiModel[]; meta: CatalogMeta }>('/models', signal),
      json<{ providers: ApiProvider[] }>('/providers', signal),
    ]);
    return { models: modelsRes.models, providers: providersRes.providers, meta: modelsRes.meta, origin: 'live' };
  } catch (err) {
    if (signal?.aborted) throw err;
    const snap = await fetch('/snapshot/catalog.json');
    if (!snap.ok) throw err;
    const data = (await snap.json()) as { generatedAt: string; models: unknown[]; providers: unknown[] };
    return { ...snapshotToCatalog(data), origin: 'snapshot', snapshotGeneratedAt: data.generatedAt };
  }
}

export async function fetchChanges(since?: string): Promise<{ changes: Change[]; byClass: Record<string, number>; cursor: string | null }> {
  const q = since ? `?since=${encodeURIComponent(since)}` : '';
  return json(`/changes${q}`);
}

export async function fetchHealth(): Promise<Record<string, any>> {
  return json('/health');
}

/**
 * Rebuild the API shape from the flat snapshot rows.
 *
 * Deliberately conservative: fields the snapshot does not carry come back as
 * null rather than as a plausible default, so the fallback view can only ever
 * show less than the live one — never something different.
 */
function snapshotToCatalog(data: { models: any[]; providers: any[] }): Omit<CatalogData, 'origin'> {
  const models: ApiModel[] = data.models.map((m) => ({
    providerId: m.provider_id,
    modelId: m.model_id,
    canonicalId: m.vq_canonical ?? null,
    displayName: m.display_name ?? m.model_id,
    state: m.status,
    contextTokens: m.context_tokens,
    maxOutputTokens: m.output_tokens,
    inputModalities: m.input_modalities ? JSON.parse(m.input_modalities) : null,
    capabilities: {
      tools: m.tools === null ? null : Boolean(m.tools),
      reasoning: m.reasoning === null ? null : Boolean(m.reasoning),
      structured: m.structured === null ? null : Boolean(m.structured),
      attachment: m.attachment === null ? null : Boolean(m.attachment),
    },
    pricing: {
      kind: (m.cost_kind ?? 'unknown') as ApiModel['pricing']['kind'],
      inputPerMTokens: m.cost_in_per_m,
      outputPerMTokens: m.cost_out_per_m,
      referenceInPerMTokens: m.ref_cost_in_per_m ?? null,
      referenceOutPerMTokens: m.ref_cost_out_per_m ?? null,
      isFree: m.cost_kind === 'free' ? true : m.cost_kind === 'unknown' ? null : false,
    },
    vq: {
      value: m.vq_value ?? null,
      uncertainty: m.vq_uncertainty ?? null,
      bound: null,
      evidenceLevel: (m.vq_level ?? 'unrated') as EvidenceLevel,
      precision: m.vq_precision ?? 0,
      display: m.vq_value === null || m.vq_value === undefined ? '—' : Number(m.vq_value).toFixed(m.vq_precision ?? 0),
      provenance: null,
    },
    vo: { value: m.vo_value ?? null, dimensions: {}, missingDimensions: [], profileId: 'balanced' },
    qualityRank: null,
    tiedAtRank: false,
    firstSeenAt: m.first_seen_at,
    lastSeenAt: m.last_seen_at,
  }));

  const providers: ApiProvider[] = data.providers.map((p) => ({
    id: p.id,
    name: p.name,
    rosterUrl: p.roster_url,
    liveModels: models.filter((m) => m.providerId === p.id).length,
    lastSuccessfulSyncAt: p.last_success_at ?? null,
    lastAttemptedSyncAt: p.last_sync_at ?? null,
    lastOutcome: p.last_outcome ?? null,
    freshness: 'stale',
    hoursSinceSuccess: null,
    qualityScored: models.filter((m) => m.providerId === p.id && m.vq.value !== null).length,
    unrated: models.filter((m) => m.providerId === p.id && m.vq.value === null).length,
  }));

  const scored = models.filter((m) => m.vq.value !== null).length;
  return {
    models,
    providers,
    meta: {
      methodologyVersion: 'venom-score-v1',
      profileId: 'balanced',
      liveModels: models.length,
      qualityScored: scored,
      operationalScored: models.length,
      unrated: models.length - scored,
      identity: { resolvedWithEvidence: scored, resolvedWithoutEvidence: 0, unresolved: models.length - scored, ambiguousOpen: 0 },
      identityRules: {},
      calibration: null,
      sortContracts: {},
    },
  };
}

// ---------------------------------------------------------------------------
// Formatting shared by every view. Kept here so two components cannot disagree.
// ---------------------------------------------------------------------------

export function formatTokens(n: number | null): string {
  if (n === null) return '—';
  if (n % 1_000_000 === 0) return `${n / 1_000_000}M`;
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1).replace(/\.0$/, '')}M`;
  if (n % 1024 === 0 && n / 1024 >= 1) return `${Math.round(n / 1024)}K`;
  return `${Math.round(n / 1000)}K`;
}

/** Price per million tokens. `0` is a real price and renders as Free. */
export function formatPrice(v: number | null): string {
  if (v === null) return '—';
  if (v === 0) return 'Free';
  return v < 1 ? `$${v.toFixed(2)}` : `$${v.toFixed(2).replace(/\.00$/, '')}`;
}

export function formatAgo(iso: string | null): string {
  if (!iso) return 'never';
  const mins = Math.round((Date.now() - new Date(iso).getTime()) / 60000);
  if (mins < 1) return 'just now';
  if (mins < 60) return `${mins}m ago`;
  const hours = Math.round(mins / 60);
  if (hours < 24) return `${hours}h ago`;
  return `${Math.round(hours / 24)}d ago`;
}

export const EVIDENCE_LABEL: Record<EvidenceLevel, string> = {
  measured: 'Measured',
  calibrated: 'Calibrated',
  bounded: 'Bounded',
  unrated: 'Unrated',
};

export const EVIDENCE_HELP: Record<EvidenceLevel, string> = {
  measured: 'Published directly by the benchmark for this exact model.',
  calibrated: 'Converted from a second benchmark onto the same scale. Carries a measured error — not a direct measurement.',
  bounded: 'A one-sided bound from a reviewed relation to a measured model.',
  unrated: 'No benchmark publishes a figure for this model. Unknown quality — not low quality.',
};
