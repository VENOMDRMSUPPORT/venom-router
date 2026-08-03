// Thin fetch wrapper over the non-auth control-plane API
// (`/api/control/v1/*`, excluding `/auth/*` which authClient.ts owns) —
// see internal/httpapi/settings.go, providers.go, accounts.go,
// enrollment.go, oauth.go, which are authoritative for every shape and
// route here. Reuses ../api/http.ts's shared envelope handling
// (AuthApiError, isSessionExpired, request) rather than duplicating it —
// see that module's doc comment for why.
//
// Every mutating call here takes a caller-supplied csrfToken (the value
// AuthGate holds in React state) and attaches it as `X-CSRF-Token`,
// exactly like authClient.ts's own mutating calls. This module never
// stores anything itself — no localStorage/sessionStorage, no
// module-level cache of any token or secret.

import type { AccentName, DensityName, ThemeName } from "../theme-runtime";
import {
  AuthApiError,
  isSessionExpired,
  request as sharedRequest,
  throwApiError,
  toApiError,
} from "./http";

export { AuthApiError, isSessionExpired, toApiError };

const CONTROL_BASE = "/api/control/v1";
const CSRF_HEADER = "X-CSRF-Token";
const IDEMPOTENCY_HEADER = "Idempotency-Key";

// --- Debug operation log (providers-page Debug panel) ---
//
// A module-level ring buffer of the last 200 control-API exchanges,
// SECRET-FREE BY CONSTRUCTION: each event carries only the method, the
// path (query string stripped), the status, the duration, and — on a typed
// error — the server's request_id. No request or response body, no header,
// and no query parameter is ever captured, so there is no field a
// credential could reach.

/** One captured control-API exchange. */
export interface DebugLogEvent {
  id: number;
  /** Epoch ms at which the request settled. */
  at: number;
  method: string;
  /** The request path (control base included, query string stripped). */
  path: string;
  /** The HTTP status, or "network_error" when fetch itself rejected. */
  status: number | "network_error";
  ok: boolean;
  durationMs: number;
  /** The error envelope's request_id, when the exchange failed typed. */
  requestId?: string;
}

const DEBUG_LOG_CAP = 200;
let debugEventSeq = 0;
const debugEvents: DebugLogEvent[] = [];
const debugListeners = new Set<() => void>();
// Cached immutable snapshot so useSyncExternalStore gets a REFERENCE-
// stable value between mutations (a fresh array per read would loop it).
let debugSnapshotCache: DebugLogEvent[] | null = null;

function notifyDebugListeners(): void {
  debugSnapshotCache = null;
  for (const listener of debugListeners) listener();
}

function recordDebugEvent(event: Omit<DebugLogEvent, "id">): void {
  debugEvents.push({ id: ++debugEventSeq, ...event });
  if (debugEvents.length > DEBUG_LOG_CAP) debugEvents.splice(0, debugEvents.length - DEBUG_LOG_CAP);
  notifyDebugListeners();
}

/** The read/observe surface for the Debug Log panel. `snapshot()` returns
 * oldest-first; the panel reverses for newest-first display. */
export const debugLog = {
  subscribe(listener: () => void): () => void {
    debugListeners.add(listener);
    return () => {
      debugListeners.delete(listener);
    };
  },
  snapshot(): readonly DebugLogEvent[] {
    if (debugSnapshotCache === null) debugSnapshotCache = debugEvents.slice();
    return debugSnapshotCache;
  },
  clear(): void {
    debugEvents.length = 0;
    notifyDebugListeners();
  },
};

async function request<T>(path: string, init: RequestInit): Promise<T> {
  const startedAt = Date.now();
  const method = (init.method ?? "GET").toUpperCase();
  // Query strings can carry cursors/limits only today, but the log strips
  // them anyway so no future parameter can leak through this seam.
  const logPath = CONTROL_BASE + path.split("?")[0];
  let observedStatus: number | "network_error" = "network_error";
  try {
    const result = await sharedRequest<T>(CONTROL_BASE, path, init, (o) => {
      observedStatus = o.status;
    });
    recordDebugEvent({
      at: Date.now(),
      method,
      path: logPath,
      status: observedStatus,
      ok: true,
      durationMs: Date.now() - startedAt,
    });
    return result;
  } catch (err) {
    recordDebugEvent({
      at: Date.now(),
      method,
      path: logPath,
      // An AuthApiError always carries the real HTTP status it was thrown
      // with; anything else here is fetch itself rejecting.
      status: err instanceof AuthApiError ? err.status : "network_error",
      ok: false,
      durationMs: Date.now() - startedAt,
      requestId: err instanceof AuthApiError && err.requestId ? err.requestId : undefined,
    });
    throw err;
  }
}

// --- Settings (P2b-CAPI-005 / UI-001) ---

export interface SettingsResponse {
  theme: ThemeName;
  density: DensityName;
  accent: AccentName;
  radius_px: number;
  spacing_scale: number;
}

/** GET /settings — the persisted appearance (theme/density/accent/
 * radius_px/spacing_scale), or the frozen defaults if no row exists yet. */
export async function getSettings(): Promise<SettingsResponse> {
  const body = await request<{ data: SettingsResponse }>("/settings", { method: "GET" });
  return body.data;
}

/** PUT /settings — persists all five appearance fields (the server
 * validates each fail-closed and rejects a partial body). There is no
 * `version` field, so no `If-Match` is ever sent here. */
export async function putSettings(
  next: SettingsResponse,
  csrfToken: string,
): Promise<SettingsResponse> {
  const body = await request<{ data: SettingsResponse }>("/settings", {
    method: "PUT",
    headers: { [CSRF_HEADER]: csrfToken },
    body: JSON.stringify(next),
  });
  return body.data;
}

// --- Full settings (P6-CAPI-001 / P2b-CAPI-005, enables P6-UI-010) ---

/** The boot-resolved listen addresses, READ-ONLY (01 §6b). `data_plane_bind` is
 * null when the public /v1 API shares the control listener — its documented
 * default, not a missing value. These come from config.Load's
 * default -> env -> flag precedence and cannot be set over the API. */
export interface EffectiveConfig {
  bind: string;
  data_plane_bind: string | null;
}

/** GET /settings' full payload (internal/httpapi/settings.go's settingsJSON).
 *
 * The five appearance fields are REQUIRED on PUT; the operational fields and the
 * enrichment toggle are OPTIONAL pointers server-side where ABSENT MEANS
 * UNCHANGED — never "reset to the default", and a JSON null is a 400. Sending an
 * untouched field back, even at its current value, turns a read into a write. */
export interface FullSettings {
  theme: ThemeName;
  density: DensityName;
  accent: AccentName;
  radius_px: number;
  spacing_scale: number;
  enrichment_enabled: boolean;
  quota_staleness_seconds: number;
  probe_max_in_flight_per_provider: number;
  probe_expensive_enabled: boolean;
  probe_per_account_window_seconds: number;
  effective_config: EffectiveConfig;
}

/** PUT /settings' body. The appearance five are required; every operational field
 * is optional and must be OMITTED (not null) when unchanged. `effective_config` is
 * deliberately absent from this type — it is read-only and a PUT carrying it is
 * rejected. */
export interface SettingsUpdate {
  theme: ThemeName;
  density: DensityName;
  accent: AccentName;
  radius_px: number;
  spacing_scale: number;
  enrichment_enabled?: boolean;
  quota_staleness_seconds?: number;
  probe_max_in_flight_per_provider?: number;
  probe_expensive_enabled?: boolean;
  probe_per_account_window_seconds?: number;
}

/** GET /settings — the full owner settings including the read-only binds. */
export async function getFullSettings(): Promise<FullSettings> {
  const body = await request<{ data: FullSettings }>("/settings", { method: "GET" });
  return body.data;
}

/** PUT /settings — persists the appearance five plus whichever operational fields
 * the caller included. Typed error to expect: `validation_error` (400), whose
 * message NAMES the offending field. */
export async function putFullSettings(
  next: SettingsUpdate,
  csrfToken: string,
): Promise<FullSettings> {
  const body = await request<{ data: FullSettings }>("/settings", {
    method: "PUT",
    headers: { [CSRF_HEADER]: csrfToken },
    body: JSON.stringify(next),
  });
  return body.data;
}

// --- Providers (P2b-PROV-002, read-only catalog) ---

/** One provider catalog entry (GET /providers, GET /providers/{id}).
 * NOTE: the shipped handler does not return any account/health
 * aggregation here (no accountCount/healthyCount field) — those are
 * derived client-side from GET /accounts, grouped by `provider`. */
export interface Provider {
  id: string;
  display_name: string;
  description: string;
  auth_mode: "oauth2" | "api_key" | "custom_openai";
  base_url?: string;
  funding: { mode: string; locked: boolean; non_expiring: boolean; fixed: string | null };
  capabilities: string[];
  configured: boolean;
  missing_env?: string[];
}

/** GET /providers — the full catalog, sorted by id. The envelope's `data`
 * is `{providers: [...]}`, not a bare array. */
export async function listProviders(): Promise<Provider[]> {
  const body = await request<{ data: { providers: Provider[] } }>("/providers", { method: "GET" });
  return body.data.providers;
}

/** GET /providers/{id} — a single catalog entry. */
export async function getProvider(id: string): Promise<Provider> {
  const body = await request<{ data: Provider }>(`/providers/${encodeURIComponent(id)}`, {
    method: "GET",
  });
  return body.data;
}

// --- Accounts (P2b-CAPI-004) ---

export interface AccountIdentity {
  email?: string;
  plan?: string;
}

export interface AccountFunding {
  funding: "free" | "paid" | "unknown";
  source: "provider_policy" | "provider_evidence" | "owner_policy" | "owner_override";
  locked: boolean;
  /** Present only on the single-account GET — the optimistic-concurrency
   * token for PUT /accounts/{id}/funding's expected_version. */
  version?: string;
}

export interface AccountEligibility {
  eligible: boolean;
  reason?: string;
}

/** One quota window (P3b-CAPI-QUOTAREAD / internal/httpapi/accounts.go's
 * quotaWindowJSON, enables P3b-UI-001). Nullable numerics are `number |
 * null`, NEVER widened to plain `number` — `null` means unknown and must
 * be rendered as such, never coerced to 0. The three string fields are
 * exactly the internal/quota vocabularies, byte-identical to
 * `@venom/design-system/domain`'s QuotaEvidenceSource / QuotaWindowState /
 * QuotaFreshness unions — kept as real unions here (not widened to
 * `string`) so they pass straight through to those components without a
 * cast. */
export interface QuotaWindow {
  source: "provider_evidence" | "local_safety" | "owner_override";
  unit: string;
  window_type: string;
  window_key: string;
  state: "available" | "insufficient" | "exhausted" | "unknown" | "stale";
  freshness: "fresh" | "stale" | "unknown";
  used: number | null;
  remaining: number | null;
  total: number | null;
  limit_value: number | null;
  reserved: number;
  reset_at: number | null;
  observed_at: string;
}

/** Exactly @venom/design-system/domain's DisplayStatus vocabulary — the
 * server's domain.DeriveDisplayStatus (internal/accounts/domain/
 * display_status.go) emits precisely these 9 values verbatim. */
export type DisplayStatus =
  | "connecting"
  | "healthy"
  | "degraded"
  | "unavailable"
  | "expired"
  | "unknown"
  | "reauthenticating"
  | "cooling_down"
  | "stopped"
  | "disconnected";

/** The multi-axis account projection GET /accounts and GET /accounts/{id}
 * both return. `display_status` is the server's DERIVED status — render it
 * verbatim, never re-derive it client-side. */
export interface AccountProjection {
  id: string;
  provider: string;
  external_id: string;
  display_name?: string;
  /** Owner-supplied human-readable label; absent = fall back to "#account NN". */
  label?: string;
  auth_type: string;
  connection_state: "connecting" | "connected" | "stopped" | "disconnected";
  health_state: "unknown" | "healthy" | "degraded" | "unavailable" | "expired";
  reauth_in_progress: boolean;
  identity: AccountIdentity;
  funding: AccountFunding | null;
  display_status: DisplayStatus;
  eligibility: AccountEligibility;
  /** Every quota window tracked for this account, in the server's
   * canonical order. Always an array — an account with no windows yet
   * serializes `[]`, never `null`. */
  quota: QuotaWindow[];
  last_health_check_at?: string;
  last_health_error?: string;
  created_at: string;
  updated_at: string;
}

export interface ListAccountsParams {
  cursor?: string;
  limit?: number;
}

export interface ListAccountsResult {
  accounts: AccountProjection[];
  nextCursor?: string;
}

/** GET /accounts — cursor-paginated account projections (no funding
 * version token on the list view; use getAccount for that). */
export async function listAccounts(params: ListAccountsParams = {}): Promise<ListAccountsResult> {
  const query = new URLSearchParams();
  if (params.cursor) query.set("cursor", params.cursor);
  if (params.limit) query.set("limit", String(params.limit));
  const qs = query.toString();
  const body = await request<{
    data: { accounts: AccountProjection[] };
    meta?: { next_cursor?: string };
  }>(`/accounts${qs ? `?${qs}` : ""}`, { method: "GET" });
  return { accounts: body.data.accounts, nextCursor: body.meta?.next_cursor };
}

/** GET /accounts/{id} — the same projection for one account, including the
 * funding version token. */
export async function getAccount(id: string): Promise<AccountProjection> {
  const body = await request<{ data: AccountProjection }>(`/accounts/${encodeURIComponent(id)}`, {
    method: "GET",
  });
  return body.data;
}

// --- API-key enrollment (P2b-CAPI-003) ---

export interface ConnectApiKeyAccountBody {
  api_key: string;
  /** Owner-supplied funding override; omit to let the catalog's funding
   * policy decide. */
  funding?: "free" | "paid" | "unknown";
  /** Optional human-readable label for the account. */
  label?: string;
}

export interface ConnectedAccount {
  id: string;
  provider: string;
  external_id: string;
  connection_state: string;
  health_state: string;
  funding: string;
  display_status: string;
}

/** POST /providers/{id}/accounts — API-key enrollment. Sends an
 * Idempotency-Key so a retried submit never double-connects. Typed errors
 * to expect: validation_error (400), invalid_credential (422),
 * provider_unavailable (502), account_already_connected (409). Note: the
 * shipped request body accepts only {api_key, funding} — there is no
 * display_name field server-side, so one is never sent here. */
export async function connectApiKeyAccount(
  providerId: string,
  body: ConnectApiKeyAccountBody,
  csrfToken: string,
  idempotencyKey?: string,
): Promise<ConnectedAccount> {
  const headers: Record<string, string> = { [CSRF_HEADER]: csrfToken };
  if (idempotencyKey) headers[IDEMPOTENCY_HEADER] = idempotencyKey;
  const resp = await request<{ data: ConnectedAccount }>(
    `/providers/${encodeURIComponent(providerId)}/accounts`,
    {
      method: "POST",
      headers,
      body: JSON.stringify(body),
    },
  );
  return resp.data;
}

// --- Account label (off-tracker) ---

/** PATCH /accounts/{id} — set or clear the owner label. Pass "" to clear. */
export async function patchAccountLabel(
  accountId: string,
  label: string,
  csrfToken: string,
): Promise<AccountProjection> {
  const resp = await request<{ data: AccountProjection }>(
    `${CONTROL_BASE}/accounts/${encodeURIComponent(accountId)}`,
    {
      method: "PATCH",
      headers: { [CSRF_HEADER]: csrfToken },
      body: JSON.stringify({ label }),
    },
  );
  return resp.data;
}

// --- Credential reveal (P2b-CAPI-004, security-critical) ---

/** POST /accounts/{id}/reveal — the ONLY endpoint that returns plaintext
 * credential material. Its SUCCESS body is the raw plaintext string, NOT a
 * `{data: ...}` JSON envelope (internal/httpapi/accounts.go's ServeReveal
 * writes the decrypted bytes directly to the response body) — this
 * function reads it via response.text(), not response.json(). Its FAILURE
 * body is the normal `{error: ...}` envelope. Typed errors to expect:
 * reverification_required (401), no_active_credential / not_found (404),
 * rate_limited (429). */
export async function revealCredential(accountId: string, csrfToken: string): Promise<string> {
  const path = `${CONTROL_BASE}/accounts/${encodeURIComponent(accountId)}/reveal`;
  const startedAt = Date.now();
  let response: Response;
  try {
    response = await fetch(path, {
      method: "POST",
      credentials: "same-origin",
      headers: { [CSRF_HEADER]: csrfToken },
    });
  } catch (err) {
    // This call bypasses the shared request helper (its success body is raw
    // plaintext), so it records its own debug event — method/path/status
    // only, exactly like every other call; the plaintext never gets near it.
    recordDebugEvent({
      at: Date.now(),
      method: "POST",
      path,
      status: "network_error",
      ok: false,
      durationMs: Date.now() - startedAt,
    });
    throw err;
  }
  recordDebugEvent({
    at: Date.now(),
    method: "POST",
    path,
    status: response.status,
    ok: response.ok,
    durationMs: Date.now() - startedAt,
  });
  if (!response.ok) {
    await throwApiError(response);
  }
  return response.text();
}

// --- Funding owner override (P2b-CAPI-004) ---

export interface UpdateFundingBody {
  funding: "free" | "paid" | "unknown";
  /** Optimistic-concurrency token — the current funding row's `version`
   * field from GET /accounts/{id}. */
  expected_version?: string;
}

/** PUT /accounts/{id}/funding. Typed errors to expect: funding_locked
 * (409), precondition_failed (412), validation_error (400). */
export async function updateFunding(
  accountId: string,
  body: UpdateFundingBody,
  csrfToken: string,
): Promise<AccountProjection> {
  const resp = await request<{ data: AccountProjection }>(
    `/accounts/${encodeURIComponent(accountId)}/funding`,
    {
      method: "PUT",
      headers: { [CSRF_HEADER]: csrfToken },
      body: JSON.stringify(body),
    },
  );
  return resp.data;
}

// --- Lifecycle mutations (P2b-CAPI-004) ---

async function postAccountAction(
  accountId: string,
  action: string,
  csrfToken: string,
  body?: unknown,
): Promise<AccountProjection> {
  const resp = await request<{ data: AccountProjection }>(
    `/accounts/${encodeURIComponent(accountId)}/${action}`,
    {
      method: "POST",
      headers: { [CSRF_HEADER]: csrfToken },
      body: body !== undefined ? JSON.stringify(body) : undefined,
    },
  );
  return resp.data;
}

/** POST /accounts/{id}/stop — connected -> stopped. Typed error to expect:
 * invalid_state (409) for an illegal transition. */
export function stopAccount(accountId: string, csrfToken: string): Promise<AccountProjection> {
  return postAccountAction(accountId, "stop", csrfToken);
}

/** POST /accounts/{id}/resume — stopped -> connected. Typed error to
 * expect: invalid_state (409). */
export function resumeAccount(accountId: string, csrfToken: string): Promise<AccountProjection> {
  return postAccountAction(accountId, "resume", csrfToken);
}

/** POST /accounts/{id}/health — applies the pure-domain health transition.
 * No live probe is registered this phase (see accounts.go's ServeHealth
 * doc comment); this is a best-effort refresh, not a guaranteed live
 * check. */
export function refreshHealth(accountId: string, csrfToken: string): Promise<AccountProjection> {
  return postAccountAction(accountId, "health", csrfToken);
}

/**
 * DELETE /accounts/{id} — soft-disconnect (retires credentials, retains
 * the row/history; restorable only via re-enrollment). Reaches
 * internal/httpapi/accounts.go's ServeDisconnect via the method-specific
 * "DELETE /api/control/v1/accounts/{id}" route in controlmux.go.
 */
export async function disconnectAccount(
  accountId: string,
  csrfToken: string,
): Promise<AccountProjection> {
  const resp = await request<{ data: AccountProjection }>(
    `/accounts/${encodeURIComponent(accountId)}`,
    {
      method: "DELETE",
      headers: { [CSRF_HEADER]: csrfToken },
    },
  );
  return resp.data;
}

// --- Certification (P3c-CAPI-001, enables P3c-UI-001) ---

/** GET /offerings/{id}/certification's success payload
 * (internal/httpapi/discovery.go's certificationJSON), rendered verbatim —
 * `state`/`capability_truth` are exactly the domain vocabularies
 * `@venom/design-system/domain`'s CertState/CapabilityTruth already use, so
 * they pass straight through with no mapping table. `probe_execution` is
 * `undefined` (never a fabricated value) until a probe has actually run;
 * `review_reasons` is always an array (possibly empty), never omitted. */
export interface CertificationRead {
  offering_operation_id: string;
  account_id: string;
  provider_model_id: string;
  operation: string;
  state: "discovered" | "observed" | "probing" | "certified" | "suspended" | "expired";
  capability_truth: "unknown" | "supported" | "unsupported";
  version: number;
  certified_at: string | null;
  evidence_ref?: string;
  certified_and_supported: boolean;
  probe_execution?:
    "pending" | "running" | "succeeded" | "inconclusive" | "retryable_failure" | "terminal_failure";
  review_reasons: string[];
}

/** GET /offerings/{id}/certification — one offering-operation's
 * certification read. A read — no CSRF token. */
export async function getCertification(offeringOperationID: string): Promise<CertificationRead> {
  const body = await request<{ data: CertificationRead }>(
    `/offerings/${encodeURIComponent(offeringOperationID)}/certification`,
    { method: "GET" },
  );
  return body.data;
}

// --- Effective-offering read model (P3a-CAPI-001, enables P6-UI-002) ---
//
// Every interface below is typed against internal/httpapi/models.go's OWN
// projections (capabilityJSON, costJSON, tierJSON, effectiveOfferingJSON,
// modelGroupJSON), which are authoritative. Nullable numerics stay
// `number | null`, never widened to `number`: `null` means the fact is unknown
// and must be RENDERED as unknown, never coerced to 0. That is 04 §3's central
// truthfulness invariant, and widening the type here is all it would take to
// lose it.

/** One offering-operation's capability truth. `routable` is the SERVER's
 * conjunction (intelligence.Project over models.Routable) — certification state
 * `certified` AND capability truth `supported`. It is never re-derived client
 * side; see ModelsSurface's own capabilityRoutability for how the two are
 * cross-checked rather than recomputed. */
export interface OfferingCapability {
  operation: string;
  effective: boolean;
  state: "discovered" | "observed" | "probing" | "certified" | "suspended" | "expired";
  truth: "unknown" | "supported" | "unsupported";
  routable: boolean;
  /** The offering_operations row this capability's certification belongs to — the
   * id `POST /offerings/{id}/probe` is keyed by.
   *
   * ABSENT means NOT PROBEABLE, and that absence is load-bearing: an operation
   * reachable only through native/transport support has no offering_operations
   * row, so there is nothing to probe. The server omits the key rather than
   * sending `""` (internal/httpapi/models.go's capabilityJSON). Never compose one
   * from provider_model_id — the real ids are minted randomly by DiscoveryRepo, so
   * a composed id would address a different row or 404. */
  offering_operation_id?: string;
}

/** One offering's resolved cost fact. `is_free` is `null` when unknown — which
 * is NOT the same as `false`, and must never be rendered as "paid". */
export interface OfferingCost {
  is_free: boolean | null;
  source?: string;
  conflict: boolean;
  dataset_version?: string;
  observed_at?: string;
  confidence: number;
  exact_identity_match: boolean;
  stale: boolean;
}

/** One offering's per-tier eligibility. `reasons` is omitted (not `[]`) by the
 * server when there are none. */
export interface OfferingTierEligibility {
  eligible: boolean;
  stale: boolean;
  penalty: boolean;
  reasons?: string[];
}

/** The shared effective-offering projection (04 §3) that BOTH GET /offerings and
 * GET /models return. `effective_context_tokens` is `number | null`: a null
 * context is unknown and is ineligible for every tier (fail closed) — rendering
 * it as 0 would claim a verified context of zero tokens. `quality_known` is the
 * companion flag for `quality_score`: when it is false the score carries no
 * information and must not be shown as a rating. */
export interface EffectiveOffering {
  account_id: string;
  provider_id: string;
  provider_model_id: string;
  display_name?: string;
  availability: "available" | "withdrawn" | "catalog_only" | "unknown";
  effective_context_tokens: number | null;
  context_provenance: string;
  capabilities: OfferingCapability[];
  quality_score: number;
  quality_known: boolean;
  cost: OfferingCost;
  classification: string;
  tiers: Record<string, OfferingTierEligibility>;
}

/** One canonical model group (GET /models). Grouping is presentation only —
 * every offering carries exactly the projection GET /offerings returns. */
export interface ModelGroup {
  model_id: string;
  display_name?: string;
  native_context_tokens: number | null;
  quality_rating: number | null;
  offerings: EffectiveOffering[];
}

export interface ListPageParams {
  cursor?: string;
  limit?: number;
}

/** A cursor-paginated page. `nextCursor` is `undefined` on the LAST page — the
 * server omits `meta` entirely there. A caller must not treat one page as the
 * whole catalog; see fetchAllModelGroups in ModelsSurface. */
export interface ModelGroupsPage {
  groups: ModelGroup[];
  nextCursor?: string;
}

function pageQuery(params: ListPageParams): string {
  const query = new URLSearchParams();
  if (params.cursor) query.set("cursor", params.cursor);
  if (params.limit) query.set("limit", String(params.limit));
  const qs = query.toString();
  return qs ? `?${qs}` : "";
}

/** GET /models — canonical model groups, cursor-paginated (the underlying
 * offering list is paged and grouping happens WITHIN the page, so a group can
 * legitimately continue onto the next page). A read — no CSRF token. */
export async function listModelGroups(params: ListPageParams = {}): Promise<ModelGroupsPage> {
  const body = await request<{ data: ModelGroup[]; meta?: { next_cursor?: string } }>(
    `/models${pageQuery(params)}`,
    { method: "GET" },
  );
  return { groups: body.data ?? [], nextCursor: body.meta?.next_cursor };
}

export interface OfferingsPage {
  offerings: EffectiveOffering[];
  nextCursor?: string;
}

/** GET /offerings — the same projection, ungrouped and cursor-paginated.
 * Optional `account_id` restricts to one account. */
export async function listOfferings(
  params: ListPageParams & { accountId?: string } = {},
): Promise<OfferingsPage> {
  const query = new URLSearchParams();
  if (params.cursor) query.set("cursor", params.cursor);
  if (params.limit) query.set("limit", String(params.limit));
  if (params.accountId) query.set("account_id", params.accountId);
  const qs = query.toString();
  const body = await request<{ data: EffectiveOffering[]; meta?: { next_cursor?: string } }>(
    `/offerings${qs ? `?${qs}` : ""}`,
    { method: "GET" },
  );
  return { offerings: body.data ?? [], nextCursor: body.meta?.next_cursor };
}

// --- Async triggers that return a job (P3a-CAPI-002 / P6-CAPI-001) ---

/** The canonical async-job handle every 202 trigger returns (09 §3.12). Poll
 * GET /jobs/{job_id} for terminal status — an accepted trigger is NOT a
 * completed one. */
export interface AsyncJobHandle {
  job_id: string;
  status_url?: string;
}

/** POST /accounts/{id}/discover — triggers account-scoped catalog discovery
 * (202 + job). */
export async function startDiscovery(
  accountId: string,
  csrfToken: string,
): Promise<AsyncJobHandle> {
  const resp = await request<{ data: AsyncJobHandle }>(
    `/accounts/${encodeURIComponent(accountId)}/discover`,
    { method: "POST", headers: { [CSRF_HEADER]: csrfToken } },
  );
  return resp.data;
}

/** POST /accounts/{id}/quota's 202 payload — the canonical job handle
 * (internal/httpapi/quota.go's quotaRefreshResponseJSON). */
export interface QuotaRefreshHandle {
  job_id: string;
  status_url: string;
}

/** POST /accounts/{id}/quota — triggers an async quota-snapshot refresh
 * (202 + job; poll GET /jobs/{job_id} for terminal status — an accepted
 * trigger is NOT a completed one). Typed errors to expect:
 * quota_unsupported (409, the provider has no quota adapter),
 * credential_unavailable (409), not_found (404). */
export async function refreshQuota(
  accountId: string,
  csrfToken: string,
): Promise<QuotaRefreshHandle> {
  const resp = await request<{ data: QuotaRefreshHandle }>(
    `/accounts/${encodeURIComponent(accountId)}/quota`,
    { method: "POST", headers: { [CSRF_HEADER]: csrfToken } },
  );
  return resp.data;
}

/** POST /providers/{id}/sync's 200 payload (internal/httpapi/accounts.go's
 * ServeProviderSync): a per-account best-effort refresh COUNT, not a
 * result. `note` is the server's own honesty caveat — this phase no
 * discovery/quota/health adapter is registered, so "synced" means "the
 * account was visited", nothing stronger. */
export interface ProviderSyncResult {
  provider: string;
  synced: number;
  skipped: number;
  note?: string;
}

/** POST /providers/{id}/sync — best-effort sync of every account under one
 * provider (health · plan · usage, as far as the server's registered
 * adapters go). Synchronous 200, no job. */
export async function syncProvider(
  providerId: string,
  csrfToken: string,
): Promise<ProviderSyncResult> {
  const resp = await request<{ data: ProviderSyncResult }>(
    `/providers/${encodeURIComponent(providerId)}/sync`,
    { method: "POST", headers: { [CSRF_HEADER]: csrfToken } },
  );
  return resp.data;
}

/** POST /models/{id}/benchmark — triggers the canonical quality benchmark
 * (202 + job).
 *
 * HONESTY NOTE, load-bearing for P6-UI-002: this job can reach `succeeded`
 * WITHOUT producing a rating. The QualityIndex seam is nil in production
 * (internal/httpapi/controlmux.go), so the leaderboard always misses and the
 * handler completes the job without writing a rating — deliberately, rather
 * than fabricating one. A completed benchmark is therefore NOT evidence that a
 * rating changed, and must never be rendered as though it were.
 *
 * A 409 means enrichment is DISABLED — a state conflict the owner can resolve
 * in settings, not a permission problem. */
export async function startBenchmark(modelId: string, csrfToken: string): Promise<AsyncJobHandle> {
  const resp = await request<{ data: AsyncJobHandle }>(
    `/models/${encodeURIComponent(modelId)}/benchmark`,
    { method: "POST", headers: { [CSRF_HEADER]: csrfToken } },
  );
  return resp.data;
}

/** GET /jobs/{job_id}'s payload (internal/httpapi/jobs.go's jobJSON) — the
 * SINGLE canonical async-job status surface (09 §3.12); no per-resource status
 * route exists. `status` is storage.JobStatus's exact vocabulary. `error` is a
 * typed code plus a safe message, never raw provider text, and is absent on a
 * job that has not failed. */
export interface JobRead {
  job_id: string;
  kind: string;
  status: "pending" | "running" | "completed" | "failed" | "expired";
  started_at?: string;
  finished_at?: string;
  result_ref?: string;
  error?: { code: string; message: string };
}

/** GET /jobs/{job_id} — idempotent; polling mutates nothing. */
export async function getJob(jobId: string): Promise<JobRead> {
  const resp = await request<{ data: JobRead }>(`/jobs/${encodeURIComponent(jobId)}`, {
    method: "GET",
  });
  return resp.data;
}

// --- Routing-admission census (P6-CAPI-EXTRA, enables P6-UI-012) ---

/** One EVALUATED reason and its count. `reason` is a typed
 * intelligence.AdmissionReason code, rendered verbatim — never renamed. */
export interface ReviewReasonCount {
  reason: string;
  count: number;
}

/** GET /certifications/review's payload (internal/httpapi/reviewcensus.go's
 * reviewCensusJSON) — 04 §5's standing "review count grouped by reason".
 *
 * THE FIELD PAIR THAT MATTERS: `evaluated_reasons` and `not_evaluated_reasons`
 * partition the closed eight-value admission vocabulary, and `by_reason` carries
 * counts for the EVALUATED ones only. A reason in `not_evaluated_reasons` has no
 * count and must not be rendered with one — `0` would say "we looked and found
 * none", which for an unevaluated check is false in the direction of a false
 * all-clear. Rendering it as an absent row is equally wrong, for the same
 * reason: absence also reads as "none found".
 *
 * `truncated` says the scan hit `limit`, so every count is a floor, not a total.
 */
export interface ReviewCensus {
  scanned: number;
  limit: number;
  truncated: boolean;
  evaluated_reasons: string[];
  not_evaluated_reasons: string[];
  by_reason: ReviewReasonCount[];
}

/** GET /certifications/review — a read, no CSRF token. */
export async function getReviewCensus(): Promise<ReviewCensus> {
  const body = await request<{ data: ReviewCensus }>("/certifications/review", { method: "GET" });
  return body.data;
}

// --- Tier policy (P6-CAPI-EXTRA, enables P6-UI-003) ---

/** One scored tier's Step-5 factor weights (05 §2). A 0 here is a REAL declared
 * zero (05 §2's "—" cells for Pro evidence-confidence and Max latency), not an
 * unknown; the not-applicable case is an unscored tier, which serves a null
 * `weights` object instead. */
export interface TierScoreWeights {
  quality: number;
  reliability: number;
  quota_headroom: number;
  evidence_confidence: number;
  cost_class: number;
  latency: number;
}

/** One tier's complete served policy (internal/httpapi/routingpolicy.go's
 * tierPolicyJSON), read from routing.Policies() server-side.
 *
 * `weights` and `competitive_band` are `null` for an UNSCORED tier (Lite) —
 * not-applicable, not zero. `attempt_budget` is the fallback depth and `funding`
 * is the pool that fallback may draw from; together they are 05 §1's "fallback on
 * exhaustion" row, which is why Lite's `free_only` pool makes its fallback fail
 * closed rather than reach for a paid offering.
 *
 * Every field is OPTIONAL in this type on purpose: a field the API omits must
 * render as unknown, never as a client-side default. */
export interface TierPolicy {
  tier: string;
  funding?: string;
  context_ceiling_tokens?: number;
  thinking_ceiling?: string;
  attempt_budget?: number;
  scored?: boolean;
  weights?: TierScoreWeights | null;
  competitive_band?: number | null;
  latency_tie_break_only?: boolean;
}

/** GET /routing/policy — the three tier policies. Read-only: 05 §8.4 defers
 * owner weight tuning past V1, so there is no writer here or server-side. */
export async function getRoutingPolicy(): Promise<TierPolicy[]> {
  const body = await request<{ data: { tiers: TierPolicy[] } }>("/routing/policy", {
    method: "GET",
  });
  return body.data?.tiers ?? [];
}

// --- Route diagnostics (P6-CAPI-001 + P6-CAPI-EXTRA, enables P6-UI-001/008) ---

/** The LIST entry's rolled-up attempt outcome (P6-CAPI-EXTRA).
 *
 * BOTH fields are `null`-able and both nulls are load-bearing. A null
 * `terminal_status` means the decision has NO attempt rows — it made no attempt,
 * which is not a status and above all is not `success`. A null
 * `total_latency_ms` means at least one attempt's latency is unknown, so the
 * total is unknown — never 0, and never a sum that dropped the unknown term. */
export interface RouteOutcome {
  terminal_status: string | null;
  total_latency_ms: number | null;
}

/** One route decision as GET /diagnostics/routes lists it
 * (internal/httpapi/diagnostics.go's routeDecisionListEntryJSON). Secret-free by
 * construction: correlation ids, typed codes, counts, scores, clamp flags and
 * timestamps only — the payload has no field for a prompt, a response, a raw
 * provider error, a credential, or an account external id. */
export interface RouteDecisionEntry {
  request_id: string;
  decision_id: string;
  tier: string;
  workload_profile_bucket: string;
  created_at: string;
  candidates: { total: number; eligible_groups: number; group_keys: string[] };
  exclusion_reasons: Record<string, number>;
  chosen: {
    provider_id: string | null;
    provider_model_id: string | null;
    funding: string | null;
  };
  scores: Record<string, number> | null;
  thinking: {
    requested: string | null;
    applied: string | null;
    tier_clamped: boolean;
    certified_clamped: boolean;
  };
  outcome: RouteOutcome;
}

export interface RouteDecisionsPage {
  decisions: RouteDecisionEntry[];
  nextCursor?: string;
}

/** GET /diagnostics/routes — newest first, one entry per routing decision. */
export async function listRouteDecisions(params: ListPageParams = {}): Promise<RouteDecisionsPage> {
  const body = await request<{ data: RouteDecisionEntry[]; meta?: { next_cursor?: string } }>(
    `/diagnostics/routes${pageQuery(params)}`,
    { method: "GET" },
  );
  return { decisions: body.data ?? [], nextCursor: body.meta?.next_cursor };
}

/** One attempt under a decision (internal/httpapi/diagnostics.go's
 * routeAttemptJSON). `status` is the CLOSED normalized vocabulary — a value that
 * reached the column by any other path surfaces as `unknown`, never as free
 * provider text. `latency_ms` and `finished_at` are null when unknown.
 *
 * NOTE the two fields deliberately absent: an attempt's failure SCOPE and
 * RETRY-AFTER are not columns on the frozen route_attempts table, so they are
 * omitted rather than fabricated from a proxy. */
export interface RouteAttempt {
  attempt: number;
  provider_id: string;
  account_id: string;
  offering_operation_id: string;
  status: string;
  latency_ms: number | null;
  thinking_clamped: boolean;
  reservation_id: string | null;
  started_at: string;
  finished_at: string | null;
}

/** GET /diagnostics/routes/{request_id}'s payload: the decision's shared fields
 * plus every attempt made under it. It carries NO `outcome` rollup — the attempts
 * array IS the answer at this level, and a summary beside it could drift. */
export interface RouteExplanation extends Omit<RouteDecisionEntry, "outcome"> {
  attempts: RouteAttempt[];
}

/** GET /diagnostics/routes/{request_id} — one request's full "why this route?".
 * An unknown request id is a typed 404 (`not_found`), never an empty 200. */
export async function getRouteExplanation(requestID: string): Promise<RouteExplanation> {
  const body = await request<{ data: RouteExplanation }>(
    `/diagnostics/routes/${encodeURIComponent(requestID)}`,
    { method: "GET" },
  );
  return body.data;
}

// --- Reconciliation diagnostics (P3b-CAPI-002, enables P6-UI-008) ---

/** One allocation under a reservation (05 §4). `actual_cost` and
 * `actual_confidence` are null until the reservation settles — never 0, which
 * would claim a measured cost of nothing. */
export interface ReconciliationAllocation {
  window_id: string;
  unit: string;
  estimated_cost: number;
  actual_cost: number | null;
  actual_confidence: string | null;
  state: string;
}

/** One reservation as GET /diagnostics/reconciliation lists it
 * (internal/httpapi/diagnostics.go's reconciliationItemJSON). Epoch seconds;
 * `dispatched_at` is null when it was never dispatched. */
export interface ReconciliationItem {
  reservation_id: string;
  account_id: string;
  request_id: string;
  attempt_id: string;
  state: string;
  attempts: number;
  leased: boolean;
  dispatched_at: number | null;
  expires_at: number;
  rebaseline_flagged: boolean;
  allocations: ReconciliationAllocation[];
}

export interface ReconciliationPage {
  items: ReconciliationItem[];
  nextCursor?: string;
}

/** GET /diagnostics/reconciliation — the reconciliation_pending /
 * unknown_consumption read model. A read, so no CSRF token. */
export async function listReconciliation(params: ListPageParams = {}): Promise<ReconciliationPage> {
  const body = await request<{ data: ReconciliationItem[]; meta?: { next_cursor?: string } }>(
    `/diagnostics/reconciliation${pageQuery(params)}`,
    { method: "GET" },
  );
  return { items: body.data ?? [], nextCursor: body.meta?.next_cursor };
}

export type ReconciliationAction = "resync" | "accept_estimate";

/** POST /diagnostics/reconciliation/{reservation_id} — manual recovery (05 §4).
 *
 * Typed errors to expect: `reservation_terminal` (409) for a reservation that
 * reached the terminal `unknown_consumption` state — no manual action can
 * un-terminalize it — or one that is simply not `reconciliation_pending`;
 * `not_found` (404). */
export async function actOnReconciliation(
  reservationID: string,
  action: ReconciliationAction,
  csrfToken: string,
): Promise<{ reservation_id: string; account_id: string; action: string }> {
  const resp = await request<{
    data: { reservation_id: string; account_id: string; action: string };
  }>(`/diagnostics/reconciliation/${encodeURIComponent(reservationID)}`, {
    method: "POST",
    headers: { [CSRF_HEADER]: csrfToken },
    body: JSON.stringify({ action }),
  });
  return resp.data;
}

// --- Usage aggregate (P6-CAPI-EXTRA-2, enables P6-UI-005) ---

/** One summed numeric dimension (internal/httpapi/usageread.go's
 * usageMetricJSON).
 *
 * THE FOUR FIELDS EXIST TO PREVENT FOUR DIFFERENT LIES:
 *   sum            null when NO contributing row reported a value. Rendering it as
 *                  0 would claim a measured absence of consumption.
 *   average        sum / known_count, or null. Never sum / requests — dividing by
 *                  rows that measured nothing drags the average down.
 *   known_count    how many rows reported a value (the honest denominator).
 *   unknown_count  how many did not. Without it a FLOOR cannot be told from a
 *                  TOTAL, so a partial number reads as complete. */
export interface UsageMetric {
  sum: number | null;
  average: number | null;
  known_count: number;
  unknown_count: number;
}

/** One grouping bucket. `key` is null for the UNATTRIBUTED bucket — account_id and
 * provider_model_id are nullable columns, so a row can belong to no account or no
 * model, and folding it into a named group would misattribute consumption.
 * `requests` is a row count and is therefore always known. */
export interface UsageGroup {
  key: string | null;
  requests: number;
  tokens_in: UsageMetric;
  tokens_out: UsageMetric;
  latency_ms: UsageMetric;
}

/** GET /usage's payload. `truncated` says the bounded scan stopped at `limit`,
 * which makes every number a floor. Window ends are epoch seconds, null when
 * unbounded. */
export interface UsageAggregate {
  window: { from: number | null; to: number | null };
  scanned: number;
  limit: number;
  truncated: boolean;
  totals: UsageGroup;
  by_account: UsageGroup[];
  by_model: UsageGroup[];
  by_tier: UsageGroup[];
}

export interface UsageQueryParams {
  /** Epoch seconds, inclusive. */
  from?: number;
  /** Epoch seconds, exclusive. */
  to?: number;
  limit?: number;
}

/** GET /usage — the consumption aggregate. A read, so no CSRF token. */
export async function getUsage(params: UsageQueryParams = {}): Promise<UsageAggregate> {
  const query = new URLSearchParams();
  if (params.from !== undefined) query.set("from", String(params.from));
  if (params.to !== undefined) query.set("to", String(params.to));
  if (params.limit !== undefined) query.set("limit", String(params.limit));
  const qs = query.toString();
  const body = await request<{ data: UsageAggregate }>(`/usage${qs ? `?${qs}` : ""}`, {
    method: "GET",
  });
  return body.data;
}

// --- Probe trigger (P3c-CAPI-001, enables P3c-UI-001) ---

export interface StartProbeBody {
  /** At most one element, matching this offering-operation's own operation
   * (internal/httpapi/probe.go's resolveProbeOperation) — omit to probe
   * that operation directly. */
  operations?: string[];
  /** Bypasses ONLY the context-probe cooldown gate — every other safety
   * gate (cost caps, concurrency, the reservation itself) still applies. */
  force?: boolean;
}

export interface StartProbeResult {
  job_id: string;
  status_url: string;
}

/** POST /offerings/{id}/probe — triggers an async capability probe (202 +
 * job); poll GET /jobs/{job_id} (not yet wrapped here) for terminal status. */
export async function startProbe(
  offeringOperationID: string,
  csrfToken: string,
  body?: StartProbeBody,
): Promise<StartProbeResult> {
  const resp = await request<{ data: StartProbeResult }>(
    `/offerings/${encodeURIComponent(offeringOperationID)}/probe`,
    {
      method: "POST",
      headers: { [CSRF_HEADER]: csrfToken },
      body: body !== undefined ? JSON.stringify(body) : undefined,
    },
  );
  return resp.data;
}

// --- OAuth enrollment (P2b-PROV-006) ---

export interface OAuthBeginResult {
  transaction_id: string;
  authorize_url: string;
  expires_at: string;
}

/** POST /providers/{id}/oauth/begin. */
export async function oauthBegin(providerId: string, csrfToken: string): Promise<OAuthBeginResult> {
  const resp = await request<{ data: OAuthBeginResult }>(
    `/providers/${encodeURIComponent(providerId)}/oauth/begin`,
    {
      method: "POST",
      headers: { [CSRF_HEADER]: csrfToken },
    },
  );
  return resp.data;
}

export interface OAuthStatusResult {
  status: "pending" | "completed" | "failed" | "expired";
  account_id?: string;
  error?: string;
}

/** GET /oauth/{transaction_id}/status — network-gated only, no CSRF (the
 * transaction id itself is the unguessable capability token). */
export async function pollOAuthStatus(transactionId: string): Promise<OAuthStatusResult> {
  const resp = await request<{ data: OAuthStatusResult }>(
    `/oauth/${encodeURIComponent(transactionId)}/status`,
    {
      method: "GET",
    },
  );
  return resp.data;
}

/** POST /oauth/complete — the manual-code completion leg for providers whose
 * client never redirects back (capability "manual_code"; claude-code's hosted
 * code page). Owner-session + CSRF gated. code is the RAW pasted string from
 * the provider's hosted page (claude-code shows `<auth_code>#<fragment>`; the
 * fragment is the echoed state and is preserved verbatim). */
export async function oauthCompleteCode(
  transactionId: string,
  code: string,
  csrfToken: string,
): Promise<OAuthStatusResult> {
  const resp = await request<{ data: OAuthStatusResult }>("/oauth/complete", {
    method: "POST",
    headers: { [CSRF_HEADER]: csrfToken },
    body: JSON.stringify({ transaction_id: transactionId, code }),
  });
  return resp.data;
}

// --- Venom API keys (P5-CAPI-001, 09 §3.11 / enables P6-UI-009) ---

/** One key as the LIST reports it. There is deliberately no `raw_key` here:
 * the server stores only a hash, and the raw value exists exactly once, in
 * the create response (09 §3.11). Timestamps are epoch seconds; a null
 * last_used_at means "never used" and a null revoked_at means "still
 * active" — neither is ever coerced to a number or a date. */
export interface ApiKeySummary {
  id: string;
  label: string;
  rpm_limit: number | null;
  key_prefix: string;
  created_at: number;
  last_used_at: number | null;
  revoked_at: number | null;
}

/** POST /keys' 201 payload. `raw_key` (`vk_live_*`) is returned ONCE and is
 * never persisted anywhere by this client — not in module state, not in
 * localStorage/sessionStorage, not in a URL. */
export interface ApiKeyCreated {
  id: string;
  label: string;
  rpm_limit: number | null;
  raw_key: string;
}

export interface CreateApiKeyBody {
  label: string;
  rpm_limit?: number;
}

/** GET /keys — every key's non-secret projection. */
export async function listApiKeys(): Promise<ApiKeySummary[]> {
  const resp = await request<{ data: ApiKeySummary[] }>("/keys", { method: "GET" });
  return resp.data ?? [];
}

/** POST /keys — mints a key and returns its raw value ONCE. */
export async function createApiKey(
  body: CreateApiKeyBody,
  csrfToken: string,
): Promise<ApiKeyCreated> {
  const resp = await request<{ data: ApiKeyCreated }>("/keys", {
    method: "POST",
    headers: { [CSRF_HEADER]: csrfToken },
    body: JSON.stringify(body),
  });
  return resp.data;
}

/** DELETE /keys/{id} — revokes a key. Irreversible: the row is retained for
 * audit but the key can never authenticate again. */
export async function deleteApiKey(keyId: string, csrfToken: string): Promise<void> {
  await request<{ data: { id: string; status: string } }>(`/keys/${encodeURIComponent(keyId)}`, {
    method: "DELETE",
    headers: { [CSRF_HEADER]: csrfToken },
  });
}
