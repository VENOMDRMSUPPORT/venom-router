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
import { AuthApiError, isSessionExpired, request as sharedRequest, throwApiError, toApiError } from "./http";

export { AuthApiError, isSessionExpired, toApiError };

const CONTROL_BASE = "/api/control/v1";
const CSRF_HEADER = "X-CSRF-Token";
const IDEMPOTENCY_HEADER = "Idempotency-Key";

function request<T>(path: string, init: RequestInit): Promise<T> {
  return sharedRequest<T>(CONTROL_BASE, path, init);
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
export async function putSettings(next: SettingsResponse, csrfToken: string): Promise<SettingsResponse> {
  const body = await request<{ data: SettingsResponse }>("/settings", {
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
  const body = await request<{ data: Provider }>(`/providers/${encodeURIComponent(id)}`, { method: "GET" });
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
  const body = await request<{ data: { accounts: AccountProjection[] }; meta?: { next_cursor?: string } }>(
    `/accounts${qs ? `?${qs}` : ""}`,
    { method: "GET" },
  );
  return { accounts: body.data.accounts, nextCursor: body.meta?.next_cursor };
}

/** GET /accounts/{id} — the same projection for one account, including the
 * funding version token. */
export async function getAccount(id: string): Promise<AccountProjection> {
  const body = await request<{ data: AccountProjection }>(`/accounts/${encodeURIComponent(id)}`, { method: "GET" });
  return body.data;
}

// --- API-key enrollment (P2b-CAPI-003) ---

export interface ConnectApiKeyAccountBody {
  api_key: string;
  /** Owner-supplied funding override; omit to let the catalog's funding
   * policy decide. */
  funding?: "free" | "paid" | "unknown";
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
  const resp = await request<{ data: ConnectedAccount }>(`/providers/${encodeURIComponent(providerId)}/accounts`, {
    method: "POST",
    headers,
    body: JSON.stringify(body),
  });
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
  const response = await fetch(`${CONTROL_BASE}/accounts/${encodeURIComponent(accountId)}/reveal`, {
    method: "POST",
    credentials: "same-origin",
    headers: { [CSRF_HEADER]: csrfToken },
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
export async function updateFunding(accountId: string, body: UpdateFundingBody, csrfToken: string): Promise<AccountProjection> {
  const resp = await request<{ data: AccountProjection }>(`/accounts/${encodeURIComponent(accountId)}/funding`, {
    method: "PUT",
    headers: { [CSRF_HEADER]: csrfToken },
    body: JSON.stringify(body),
  });
  return resp.data;
}

// --- Lifecycle mutations (P2b-CAPI-004) ---

async function postAccountAction(accountId: string, action: string, csrfToken: string, body?: unknown): Promise<AccountProjection> {
  const resp = await request<{ data: AccountProjection }>(`/accounts/${encodeURIComponent(accountId)}/${action}`, {
    method: "POST",
    headers: { [CSRF_HEADER]: csrfToken },
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
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
export async function disconnectAccount(accountId: string, csrfToken: string): Promise<AccountProjection> {
  const resp = await request<{ data: AccountProjection }>(`/accounts/${encodeURIComponent(accountId)}`, {
    method: "DELETE",
    headers: { [CSRF_HEADER]: csrfToken },
  });
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
  probe_execution?: "pending" | "running" | "succeeded" | "inconclusive" | "retryable_failure" | "terminal_failure";
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
  const resp = await request<{ data: OAuthBeginResult }>(`/providers/${encodeURIComponent(providerId)}/oauth/begin`, {
    method: "POST",
    headers: { [CSRF_HEADER]: csrfToken },
  });
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
  const resp = await request<{ data: OAuthStatusResult }>(`/oauth/${encodeURIComponent(transactionId)}/status`, {
    method: "GET",
  });
  return resp.data;
}
