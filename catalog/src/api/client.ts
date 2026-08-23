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

/**
 * Where a row stands on identity.
 *
 * The service publishes three exclusive states — `resolved`, `identity_review`,
 * `unresolved`. `unknown` is a fourth that only this client can produce, and it
 * carries exactly one meaning: *the response we got did not state it.*
 *
 * It is not a finding about a model. That distinction is the whole reason it
 * exists: the two states we could otherwise have fallen back to are both
 * fabrications. `resolved` would invent a proof that this row is a specific
 * upstream model; `unresolved` would invent the finding that nothing upstream
 * matched. Deriving it from `canonicalId` is worse still — the axes are
 * independent by design, and a client that couples them cannot tell
 * "investigated and parked" from "never looked at".
 */
export type IdentityState = 'resolved' | 'identity_review' | 'unresolved' | 'unknown';

/** The three states a catalog service is allowed to assert. `unknown` is ours. */
function isServiceIdentityState(v: unknown): v is Exclude<IdentityState, 'unknown'> {
  return v === 'resolved' || v === 'identity_review' || v === 'unresolved';
}

/** One identity candidate that was examined and refused, with its evidence. */
export interface RejectedCandidate {
  /** The refused upstream id, or null when the finding is that none exists. */
  candidate: string | null;
  verdict: 'candidate_rejected' | 'no_candidate_exists';
  why: string;
  evidence: string[];
  source: string;
  sourceRef: string | null;
  sourceUrl: string | null;
  evidenceState: string;
  resolverVersion: string;
  candidateMeta: Record<string, unknown> | null;
  reviewedAt: string | null;
  recordedAt: string;
}

/** One field whose sources contradicted each other. Every side is kept. */
export interface FieldConflict {
  field: string;
  sides: { value: unknown; by: string }[];
  conflictType: string;
  status: 'open' | 'resolved';
  resolvedTo: string | null;
  detectedAt: string;
}

/** Where one resolved value came from. */
export interface FactProvenance {
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

export interface ApiModelScore {
  value: number | null;
  display: string;
  methodologyVersion: string | null;
  qualityWeight: number | null;
  operationalWeight: number | null;
  operationalPrecision: number | null;
  uncertainty: number | null;
  bound: 'lower' | 'upper' | null;
  reason: 'missing_vq' | 'missing_vo' | 'missing_both' | 'not_reported' | null;
  /** Evidence class of the VQ component; the composite itself is derived. */
  qualityEvidenceLevel: EvidenceLevel;
  operationalCoverage: 'complete' | 'partial' | 'missing' | 'unknown';
}

export interface ApiOverallScore {
  value: number | null;
  display: string;
  status: 'complete' | 'evaluating' | 'insufficient_evidence' | 'unknown';
  qualityScore: number | null;
  operationalScore: number | null;
  qualityCoverage: { scored: number; applicable: number; percent: number };
  overallCoverage: { scored: number; applicable: number; percent: number };
  includedDimensions: string[];
  excludedDimensions: string[];
  uncertainty: number | null;
  reasons: string[];
  methodologyVersion: string | null;
  computedAt: string | null;
}

export type ModelResolutionState =
  | 'complete'
  | 'processing'
  | 'awaiting_external_benchmark'
  | 'source_incomplete'
  | 'unknown';

export interface ModelResolution {
  state: ModelResolutionState;
  reasons: string[];
  firstDetectedAt: string | null;
  lastAttemptAt: string | null;
  nextAttemptAt: string | null;
}

export type PerformanceStatus = 'measured' | 'not_measured';

export interface ModelPerformance {
  status: PerformanceStatus;
  runId: number | null;
  evaluatedAt: string | null;
  sampleCount: number;
  successfulSamples: number;
  ttftMedianSeconds: number | null;
  outputTokensPerSecondMedian: number | null;
  endToEndP95Seconds: number | null;
  successRate: number | null;
  speedScore: number | null;
}

export interface ApiModel {
  /** The vendor's lifecycle marker: 'deprecated' when it says so. */
  lifecycle: string | null;
  providerId: string;
  modelId: string;
  canonicalId: string | null;
  /**
   * Which model this row IS, from a vendor listing — a different question from
   * `canonicalId`, which names the reference-index entry a SCORE came from. A
   * model no index lists yet has no canonical id and can still be identified.
   */
  vendorModelId: string | null;
  /** Exclusive; never inferred from canonicalId. `unknown` = the response didn't say. */
  identityState: IdentityState;
  rejectedCandidates: RejectedCandidate[];
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
    /** Why there is no score. Null when scored. */
    unratedReason: string | null;
    provenance: Provenance | null;
  };
  vo: {
    value: number | null;
    dimensions: Record<string, number | null>;
    /** Nobody published these. A real gap. */
    missingDimensions: string[];
    /** These do not apply — e.g. cost for a subscription model. NOT a gap. */
    notApplicableDimensions: string[];
    profileId: string;
  };
  /** Server-derived composite. The SPA must never calculate this field. */
  modelScore: ApiModelScore;
  /** Stored speed-probe aggregates; absent on legacy responses and normalized to not_measured. */
  performance?: ModelPerformance;
  /** Server-derived overall-score-v1 result. The SPA must never calculate it. */
  overallScore: ApiOverallScore;
  resolution: ModelResolution;
  catalogReady: boolean;
  missingFacts: string[];
  conflicts: FieldConflict[];
  provenanceByField: Record<string, FactProvenance>;
  qualityRank: number | null;
  tiedAtRank: boolean;
  modelRank: number | null;
  tiedAtModelRank: boolean;
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
  freshness: 'fresh' | 'stale' | 'never';
  hoursSinceSuccess: number | null;
  qualityScored: number;
  /** Published composite model scores, not merely a VQ component. */
  modelScoreScored: number;
  overallScoreScored: number;
  unrated: number;
}

export interface CatalogMeta {
  methodologyVersion: string;
  profileId: string;
  scoringPolicy: {
    methodologyVersion: string | null;
    qualityWeight: number | null;
    operationalWeight: number | null;
    operationalPrecision: number | null;
  };
  liveModels: number;
  catalogReady: number;
  needsVerification: number;
  qualityScored: number;
  modelScoreScored: number;
  overallScoreScored: number;
  operationalScored: number;
  unrated: number;
  /**
   * The identity breakdown, or `null`s when the response did not carry it.
   *
   * `number | null` rather than `number` because these types are a promise about
   * the current contract, not a guarantee about every payload that will ever
   * arrive over the wire. A counter that reads `0` claims the catalog looked and
   * found none; `null` says this response did not answer. Rendering the second
   * as the first is how a stale service comes to look like good news.
   */
  identity: { resolved: number | null; identityReview: number | null; unresolved: number | null };
  identityDetail: { ambiguousOpen: number; withRejectedCandidates: number; rejectedCandidates: number } | null;
  /** Models with at least one field withheld by a source disagreement. */
  conflictedModels: number | null;
  /** How many MODELS each disputed field affects. */
  conflictsByField: Record<string, number>;
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

/**
 * The offline fallback file.
 *
 * `meta` is optional here and required in `CatalogData` on purpose: this type
 * describes a FILE on disk, which may predate the current writer, and the check
 * in `fetchCatalog` is what turns that possibility into a refusal. A snapshot
 * that cannot state the catalog's own summary is not a stale view of it.
 */
interface SnapshotFile {
  generatedAt: string;
  models: WireModel[];
  providers: ApiProvider[];
  meta?: WireMeta;
}

// ---------------------------------------------------------------------------
// The wire boundary.
//
// A response is not an `ApiModel`, and the gap between the two is not
// theoretical: a service process started before a contract change keeps serving
// the older shape until it is restarted, and the fields it never heard of arrive
// as `undefined`. Reading `.length` off one of those unmounts the whole page.
//
// So exactly one place turns wire data into an `ApiModel`, and it holds exactly
// one rule: an absent field becomes a *renderable absence*, never a value.
// `[]`, `{}` and `unknown` all say "this response did not carry it". `0`, `false`
// and `resolved` would each say something about a model instead — a claim the
// service never made. Nothing here computes a score, resolves an identity, picks
// between conflicting sources, or fills a gap with a plausible default.
//
// It is deliberately not a validator. A field that IS present is passed through
// untouched, whatever it holds; the goal is to make a partial payload renderable
// and honest, not to second-guess a service about its own data.
// ---------------------------------------------------------------------------

/** A model as it arrives. Every field optional, because any of them may be. */
type WireModel = Omit<Partial<ApiModel>, 'vq' | 'vo' | 'identityState'> & {
  /** Just a string until it is checked against the contract. */
  identityState?: string;
  vq?: Partial<ApiModel['vq']>;
  vo?: Partial<ApiModel['vo']>;
  overallScore?: Partial<ApiOverallScore>;
};

/** A meta block as it arrives, including from a superseded contract. */
type WireMeta = Omit<Partial<CatalogMeta>, 'identity'> & {
  identity?: Partial<CatalogMeta['identity']>;
};

export function normalizeModel(raw: WireModel): ApiModel {
  const vq = (raw.vq ?? {}) as ApiModel['vq'];
  const vo = (raw.vo ?? {}) as ApiModel['vo'];
  const modelScore = raw.modelScore ?? {
    value: null,
    display: '—',
    methodologyVersion: null,
    qualityWeight: null,
    operationalWeight: null,
    operationalPrecision: null,
    uncertainty: null,
    bound: null,
    reason: 'not_reported' as const,
    qualityEvidenceLevel: vq.evidenceLevel ?? 'unrated',
    operationalCoverage: 'unknown' as const,
  };
  const overallScore: ApiOverallScore = {
    value: raw.overallScore?.value ?? null,
    display: raw.overallScore?.display ?? '—',
    status: raw.overallScore?.status ?? 'unknown',
    qualityScore: raw.overallScore?.qualityScore ?? null,
    operationalScore: raw.overallScore?.operationalScore ?? null,
    qualityCoverage: raw.overallScore?.qualityCoverage ?? { scored: 0, applicable: 0, percent: 0 },
    overallCoverage: raw.overallScore?.overallCoverage ?? { scored: 0, applicable: 0, percent: 0 },
    includedDimensions: raw.overallScore?.includedDimensions ?? [],
    excludedDimensions: raw.overallScore?.excludedDimensions ?? [],
    uncertainty: raw.overallScore?.uncertainty ?? null,
    reasons: raw.overallScore?.reasons ?? ['not_reported'],
    methodologyVersion: raw.overallScore?.methodologyVersion ?? 'overall-score-v1',
    computedAt: raw.overallScore?.computedAt ?? null,
  };

  return {
    // Everything the response did carry, exactly as it carried it. A null stays
    // null: unknown quality is not low quality, and an absent context window is
    // not a zero-token one.
    ...(raw as ApiModel),

    identityState: isServiceIdentityState(raw.identityState) ? raw.identityState : 'unknown',

    // A service that predates this field says nothing about lifecycle, which is
    // not the same as saying the model is current.
    lifecycle: (raw as { lifecycle?: string | null }).lifecycle ?? null,

    // Lists and records, so no consumer has to guard before counting. Empty is
    // the honest reading of silence here: it renders as "nothing to show", which
    // is what we know, and it cannot be mistaken for a measured figure.
    rejectedCandidates: raw.rejectedCandidates ?? [],
    conflicts: raw.conflicts ?? [],
    provenanceByField: raw.provenanceByField ?? {},

    // A reason nobody sent is "not recorded", not an invented token — the panel
    // renders a reason only when one exists, and says nothing otherwise.
    vq: { ...vq, unratedReason: vq.unratedReason ?? null },

    vo: {
      ...vo,
      dimensions: vo.dimensions ?? {},
      // These two are opposite claims and must never be filled from each other:
      // `missing` is open work nobody published, `notApplicable` is a settled
      // answer that the question does not apply here. Collapsing one into the
      // other either retires a real gap by renaming it, or invents a gap.
      missingDimensions: vo.missingDimensions ?? [],
      notApplicableDimensions: vo.notApplicableDimensions ?? [],
    },
    modelScore,
    performance: raw.performance ?? {
      status: 'not_measured', runId: null, evaluatedAt: null, sampleCount: 0, successfulSamples: 0,
      ttftMedianSeconds: null, outputTokensPerSecondMedian: null, endToEndP95Seconds: null, successRate: null, speedScore: null,
    },
    overallScore,
    resolution: raw.resolution ?? {
      state: 'unknown', reasons: [], firstDetectedAt: null, lastAttemptAt: null, nextAttemptAt: null,
    },
    modelRank: raw.modelRank ?? null,
    tiedAtModelRank: raw.tiedAtModelRank ?? false,
    overallRank: raw.overallRank ?? null,
    tiedAtOverallRank: raw.tiedAtOverallRank ?? false,
  };
}

export function normalizeMeta(raw: WireMeta): CatalogMeta {
  const identity = raw.identity ?? {};

  // `identity` is taken as one answer or not at all. A superseded shape that
  // happens to share the key `unresolved` is not a partial version of this one —
  // there it counted every row without a canonical id, which the current
  // contract splits into `identityReview` + `unresolved`. Reading it across would
  // republish an old number under a new name, which is worse than no number.
  const identityIsCurrent =
    typeof identity.resolved === 'number' &&
    typeof identity.identityReview === 'number' &&
    typeof identity.unresolved === 'number';

  return {
    ...(raw as CatalogMeta),
    identity: identityIsCurrent
      ? { resolved: identity.resolved!, identityReview: identity.identityReview!, unresolved: identity.unresolved! }
      : { resolved: null, identityReview: null, unresolved: null },
    identityDetail: raw.identityDetail ?? null,
    conflictedModels: raw.conflictedModels ?? null,
    conflictsByField: raw.conflictsByField ?? {},
    modelScoreScored: raw.modelScoreScored ?? raw.qualityScored ?? 0,
    overallScoreScored: raw.overallScoreScored ?? 0,
    scoringPolicy: raw.scoringPolicy ?? {
      methodologyVersion: null,
      qualityWeight: null,
      operationalWeight: null,
      operationalPrecision: null,
    },
  };
}

function normalizeProvider(raw: Partial<ApiProvider>): ApiProvider {
  return {
    ...(raw as ApiProvider),
    modelScoreScored: raw.modelScoreScored ?? raw.qualityScored ?? 0,
    overallScoreScored: raw.overallScoreScored ?? 0,
  };
}

/**
 * Why a `/v1` read failed: nothing answered, or the service answered badly.
 *
 * These are different problems with different fixes, and the page used to show
 * them identically. With the service stopped, vite turns ECONNREFUSED into a
 * bare `500 text/plain`, so every read surfaced as "(500)" — indistinguishable
 * from a service bug, and it cost a whole debugging session before anyone
 * checked whether the process was up.
 */
export class ServiceError extends Error {
  /** True when nothing that speaks `/v1` answered at all. */
  readonly unreachable: boolean;
  /** The HTTP status, or null when the request never produced a response. */
  readonly status: number | null;

  constructor(message: string, opts: { unreachable: boolean; status: number | null; cause?: unknown }) {
    super(message, { cause: opts.cause });
    this.name = 'ServiceError';
    this.unreachable = opts.unreachable;
    this.status = opts.status;
  }
}

/**
 * What the owner reads when the api port is dead.
 *
 * It names the cause rather than the remedy on purpose: `npm run serve` is the
 * fix on this machine and would be wrong advice anywhere the catalog is served
 * by something else.
 */
const UNREACHABLE = 'the catalog service is not answering on /v1 — the process is not running, or nothing is listening on its port';

/** Machine reason for the routes that report a refusal as a value. */
export const SERVICE_UNREACHABLE = 'service_unreachable';

/**
 * The parsed body, or null when the answer did not come from the service.
 *
 * Every response the service writes goes through `JSON.stringify` with a JSON
 * content-type — there is no path that emits an empty body. So a body that will
 * not parse did not come from the service.
 */
async function serviceBody(res: Response): Promise<Record<string, unknown> | null> {
  return (await res.json().catch(() => null)) as Record<string, unknown> | null;
}

/**
 * Did this failure come from in front of the service rather than from it?
 *
 * Two conditions, and the status half is not redundant. Only a 5xx can be the
 * hop in front: a proxy with a dead upstream answers 500, 502 or 504, never a
 * 409. A 4xx means SOMETHING understood the request well enough to reject it, so
 * it is reported as an answer even when its body will not parse — guessing "dead
 * service" from a 409 would send the reader looking for a stopped process that
 * is running fine.
 *
 * Note the body half is what keeps a genuine degraded-health 503 out of here: a
 * bare status rule would read the service's own honest answer as a dead socket.
 */
function unreachableResponse(status: number, body: Record<string, unknown> | null): boolean {
  return body === null && status >= 500;
}

/** True when the failure means the request never reached the service. */
export function isUnreachable(err: unknown): boolean {
  return err instanceof ServiceError && err.unreachable;
}

/**
 * One read of the service, and one place that decides why a read failed.
 *
 * Every caller went through its own `if (!res.ok) throw new Error(...)`, six
 * copies with six wordings and no way to tell a dead socket from a real answer.
 * Adding the distinction to each of them would have been the same defect with
 * more branches.
 */
async function readService<T>(path: string, init?: RequestInit): Promise<T> {
  let res: Response;
  try {
    res = await fetch(`${BASE}${path}`, init);
  } catch (cause) {
    // An abort is the caller's own doing — useCatalog cancels in-flight reads on
    // unmount and keys off the signal. Dressing it up as a service failure would
    // put a banner on a page the owner just left.
    if (cause instanceof DOMException && cause.name === 'AbortError') throw cause;
    throw new ServiceError(UNREACHABLE, { unreachable: true, status: null, cause });
  }

  if (res.ok) return (await res.json()) as T;

  const body = await serviceBody(res);
  if (unreachableResponse(res.status, body)) {
    throw new ServiceError(UNREACHABLE, { unreachable: true, status: res.status });
  }

  // 503 is the service saying "up, but degraded". That is an answer, and the
  // health caller reads its body rather than treating it as a failure.
  if (res.status === 503 && body !== null) return body as T;

  const detail = body && typeof body.error === 'string' ? body.error : null;
  throw new ServiceError(
    detail ? `${path} -> HTTP ${res.status}: ${detail}` : `${path} -> HTTP ${res.status}`,
    { unreachable: false, status: res.status },
  );
}

async function json<T>(path: string, signal?: AbortSignal): Promise<T> {
  return readService<T>(path, { signal });
}

export async function fetchCatalog(signal?: AbortSignal): Promise<CatalogData> {
  try {
    const [modelsRes, providersRes] = await Promise.all([
      json<{ models: WireModel[]; meta: WireMeta }>('/models', signal),
      json<{ providers: ApiProvider[] }>('/providers', signal),
    ]);
    return {
      models: modelsRes.models.map(normalizeModel),
      providers: providersRes.providers.map(normalizeProvider),
      meta: normalizeMeta(modelsRes.meta),
      origin: 'live',
    };
  } catch (err) {
    if (signal?.aborted) throw err;
    const snap = await fetch('/snapshot/catalog.json');
    if (!snap.ok) throw err;
    const data = (await snap.json()) as SnapshotFile;

    // The snapshot is a serialised API answer, so it goes through the same two
    // normalisers the live path uses and nothing else. This branch used to hold
    // a second translation — flat database columns to `ApiModel` — which is how
    // the offline page came to publish figures the catalog never produced: it
    // had no identity, conflict or completeness columns to read, so it filled
    // those tiles with "MISSING" and, for `catalogReady`/`needsVerification`,
    // with confident fabrications.
    if (!data.meta) throw new Error('the offline snapshot is in a superseded format — regenerate it with `npm run sync`');

    return {
      models: data.models.map(normalizeModel),
      providers: data.providers.map(normalizeProvider),
      meta: normalizeMeta(data.meta),
      origin: 'snapshot',
      snapshotGeneratedAt: data.generatedAt,
    };
  }
}

export async function fetchChanges(since?: string, signal?: AbortSignal): Promise<{ changes: Change[]; byClass: Record<string, number>; cursor: string | null }> {
  const q = since ? `?since=${encodeURIComponent(since)}` : '';
  return json(`/changes${q}`, signal);
}

/**
 * The newest recorded event timestamp, and nothing else.
 *
 * `limit=0` is already a pure cursor probe on the existing endpoint: the row
 * query becomes `LIMIT 0` while the cursor is still `MAX(at)` over the whole
 * event table. So "has anything changed" costs one aggregate and needs no second
 * endpoint to answer — which is why there isn't one.
 */
export async function fetchChangeCursor(signal?: AbortSignal): Promise<string | null> {
  const result = await json<{ cursor: string | null }>('/changes?limit=0', signal);
  return result.cursor ?? null;
}

export interface HealthServiceState {
  status: 'up' | 'degraded';
  databaseReadable: boolean;
  startedAt: string | null;
  syncInFlight: boolean;
  currentRunStartedAt: string | null;
  schedulerEnabled: boolean;
  nextScheduledRunAt: string | null;
}

export interface HealthCatalogProviderSummary {
  id: string;
  liveModels: number;
  freshness: 'fresh' | 'stale' | 'never';
  lastSuccessfulSyncAt: string | null;
  lastAttemptedSyncAt: string | null;
  lastOutcome: 'ok' | 'failed' | 'quarantined' | null;
  hoursSinceSuccess: number | null;
}

export interface HealthStaleProvider {
  id: string;
  freshness: string;
  lastSuccessfulSyncAt: string | null;
  lastOutcome: string | null;
}

export interface HealthCatalogState {
  status: 'current' | 'stale';
  liveModels: number;
  methodologyVersion: string | null;
  staleAfterHours: number;
  staleProviders: HealthStaleProvider[];
  providers: HealthCatalogProviderSummary[];
}

export interface HealthLastSyncProvider {
  provider: string;
  outcome: string;
  error: string | null;
}

export interface HealthLastSync {
  startedAt: string;
  finishedAt: string | null;
  aborted: string | null;
  providers: HealthLastSyncProvider[];
}

export interface HealthResponse {
  service: HealthServiceState;
  catalog: HealthCatalogState;
  lastSync: HealthLastSync | null;
}

export type AlertSeverity = 'critical' | 'warning' | 'info';
export type AlertStatus = 'open' | 'acknowledged' | 'resolved';

export type NotificationEvent = 'opened' | 'reopened' | 'acknowledged' | 'resolved';
export type NotificationStatus = 'pending' | 'delivered' | 'retrying' | 'failed';

export interface NotificationRecord {
  id: number;
  alertId: string;
  eventType: NotificationEvent;
  status: NotificationStatus;
  attempts: number;
  nextAttemptAt: string;
  lastAttemptAt: string | null;
  deliveredAt: string | null;
  responseStatus: number | null;
  lastError: string | null;
  createdAt: string;
}

export interface AlertRecord {
  id: string;
  kind: string;
  severity: AlertSeverity;
  title: string;
  detail: string;
  providerId: string | null;
  modelId: string | null;
  status: AlertStatus;
  firstSeenAt: string;
  lastSeenAt: string;
  acknowledgedAt: string | null;
  resolvedAt: string | null;
  occurrenceCount: number;
  /**
   * Removed from the polled list response: nothing rendered it, and it was an
   * unbounded per-alert array rebuilt from a full table scan on every poll. The
   * single-alert PATCH reply still carries it, where the history is bounded.
   */
  notifications?: NotificationRecord[];
}

export interface AlertSummary {
  total: number;
  active: number;
  open: number;
  acknowledged: number;
  resolved: number;
  critical: number;
  warning: number;
  info: number;
}

export interface AlertDeliverySummary {
  enabled: boolean;
  webhookConfigured: boolean;
  pending: number;
  failed: number;
}

export interface AlertsResponse {
  alerts: AlertRecord[];
  summary: AlertSummary;
  delivery?: AlertDeliverySummary;
  generatedAt: string;
}

export async function fetchHealth(signal?: AbortSignal): Promise<HealthResponse> {
  return json<HealthResponse>('/health', signal);
}

export async function fetchAlerts(status?: AlertStatus, signal?: AbortSignal): Promise<AlertsResponse> {
  const query = status ? `?status=${encodeURIComponent(status)}` : '';
  return readService<AlertsResponse>(`/alerts${query}`, { signal });
}

export async function updateAlertStatus(id: string, status: AlertStatus): Promise<AlertRecord> {
  return readService<AlertRecord>(`/alerts/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ status }),
  });
}

export type CatalogNotificationCategory = 'success' | 'error' | 'warning';
export type CatalogNotificationKind = 'model_added' | 'model_retired' | 'fetch_problem';

export interface CatalogNotification {
  id: string;
  category: CatalogNotificationCategory;
  kind: CatalogNotificationKind;
  title: string;
  detail: string;
  providerId: string | null;
  modelId: string | null;
  observedAt: string;
  readAt: string | null;
  createdAt: string;
}

export interface CatalogNotificationsResponse {
  notifications: CatalogNotification[];
  summary: { total: number; unread: number; read: number };
  generatedAt: string;
}

export async function fetchCatalogNotifications(providerId?: string, signal?: AbortSignal): Promise<CatalogNotificationsResponse> {
  const query = providerId ? `?provider=${encodeURIComponent(providerId)}` : '';
  return readService<CatalogNotificationsResponse>(`/notifications${query}`, { signal });
}

export async function markCatalogNotificationsRead(ids: string[] | null): Promise<{ updated: number }> {
  return readService<{ updated: number }>('/notifications/read', {
    method: 'PATCH',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(ids === null ? {} : { ids }),
  });
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

/** The plan the service will execute, as the modal needs to show it. */
export interface EvaluationPlanView {
  dimensions: string[];
  skipped: Array<{ dimension: string; reason: string }>;
  speed: 'missing' | 'scored';
  blocked: string | null;
  estimatedRequests: number;
}

export interface EvaluationStateView {
  state: 'idle' | 'running' | 'stopping';
  current: null | {
    providerId: string;
    modelId: string;
    dimension: string | null;
    samplesCompleted: number;
    samplesTotal: number;
    dimensionsCompleted: Array<{ dimension: string; score: number | null; status: string }>;
    dimensionsRemaining: string[];
  };
  queue: Array<{ providerId: string; modelId: string }>;
}

/** One scored dimension as the service holds it, with the trail behind the number. */
export interface EvaluationEvidenceView {
  dimension: string;
  score: number | null;
  status: string;
  confidence: number | null;
  sampleCount: number | null;
  evidence: string[];
  evaluatedAt: string | null;
  rubricVersion?: string;
  testSetHash?: string | null;
}

export interface EvaluationDetailView {
  identityId: string | null;
  plan: EvaluationPlanView;
  identityDimensions: EvaluationEvidenceView[];
  offerDimensions: EvaluationEvidenceView[];
}

/**
 * The plan AND the evidence behind what is already scored, in one read.
 *
 * This used to keep `.plan` and discard the rest of the same response, which is
 * why a fully-scored model produced a dialog with one sentence and no way out:
 * the endpoint was already answering "here is what you have, and when it was
 * measured" and the client threw that away.
 */
export async function fetchEvaluationDetail(providerId: string, modelId: string): Promise<EvaluationDetailView> {
  return readService<EvaluationDetailView>(
    `/models/${encodeURIComponent(providerId)}/${encodeURIComponent(modelId)}/evaluation`,
  );
}

export interface RegradeOutcomeView {
  rescored: Array<{ dimension: string; before: number | null; after: number }>;
  withdrawn: number;
  unreplayable: number;
}

/**
 * Re-read this model's stored responses with today's grader. No paid requests.
 *
 * A refusal is a value here for the same reason `startEvaluation` returns one:
 * "an evaluation is running" is a state the owner has to be shown, not an
 * exception.
 */
export async function regradeEvaluation(
  providerId: string,
  modelId: string,
): Promise<{ ok: true; outcome: RegradeOutcomeView } | { ok: false; reason: string }> {
  const res = await fetch(`${BASE}/evaluations/regrade`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ providerId, modelId }),
  });
  const payload = await serviceBody(res);
  // Nothing that speaks /v1 answered, so `http_500` would be reporting a service
  // error that never happened.
  if (unreachableResponse(res.status, payload)) return { ok: false, reason: SERVICE_UNREACHABLE };
  if (!res.ok) return { ok: false, reason: String(payload?.error ?? `http_${res.status}`) };
  // A 2xx whose body will not parse is not a success anyone can read.
  if (payload === null) return { ok: false, reason: SERVICE_UNREACHABLE };
  return {
    ok: true,
    outcome: {
      rescored: (payload.rescored ?? []) as RegradeOutcomeView['rescored'],
      withdrawn: Number(payload.withdrawn ?? 0),
      unreplayable: ((payload.unreplayable ?? []) as unknown[]).length,
    },
  };
}

/**
 * A refusal is a value, not an exception.
 *
 * A missing credential or an unresolved identity is something the modal must
 * show the owner, not something it should crash on: those are the two states
 * where a click would otherwise spend money on evidence that cannot be produced.
 */
export async function startEvaluation(
  providerId: string,
  modelId: string,
): Promise<{ ok: true } | { ok: false; status: number; reason: string }> {
  const res = await fetch(`${BASE}/evaluations`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ providerId, modelId }),
  });
  if (res.ok) return { ok: true };
  const body = await serviceBody(res);
  if (unreachableResponse(res.status, body)) {
    return { ok: false, status: res.status, reason: SERVICE_UNREACHABLE };
  }
  const { reason, error } = (body ?? {}) as { reason?: string; error?: string };
  return { ok: false, status: res.status, reason: reason ?? error ?? `http_${res.status}` };
}

export async function fetchEvaluationState(): Promise<EvaluationStateView> {
  return readService<EvaluationStateView>('/evaluations');
}

export async function stopEvaluations(): Promise<void> {
  await fetch(`${BASE}/evaluations`, { method: 'DELETE' });
}

// Database browser API
export interface DbTable {
  name: string;
  sql: string | null;
}

export interface DbSchema {
  table: string;
  columns: { name: string; type: string; notnull: number; dflt_value: string | null; pk: number }[];
  indexes: { name: string; unique: number; origin: string; partial: number }[];
  foreignKeys: { id: number; seq: number; table: string; from: string; to: string; on_update: string; on_delete: string; match: string }[];
}

export interface DbQueryResponse {
  columns: string[];
  rows: Record<string, unknown>[];
  rowCount: number;
  truncated: boolean;
  limit: number;
}

export async function fetchDbTables(signal?: AbortSignal): Promise<{ tables: DbTable[] }> {
  return readService<{ tables: DbTable[] }>('/db/tables', { signal });
}

export async function fetchDbSchema(table: string, signal?: AbortSignal): Promise<DbSchema> {
  return readService<DbSchema>(`/db/schema?table=${encodeURIComponent(table)}`, { signal });
}

export async function fetchDbQuery(sql: string, limit = 100, signal?: AbortSignal): Promise<DbQueryResponse> {
  return readService<DbQueryResponse>('/db/query', {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ sql, limit }),
    signal,
  });
}
