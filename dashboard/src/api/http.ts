// Shared control-plane HTTP envelope handling (09 §1), factored out of
// auth/authClient.ts so this module and api/controlClient.ts (the non-auth
// control routes, P2b-UI-001/UI-003) can share the identical {data}/{error}
// envelope handling, the AuthApiError type, and same-origin credentials
// behavior without duplicating it.
//
// authClient.ts's own public exports and behavior are UNCHANGED by this
// factoring: it re-exports AuthApiError/isSessionExpired/isLockedOut from
// here and calls request() with its own AUTH_BASE, exactly as it did when
// these lived inline in that file. Every existing authClient consumer and
// test keeps working unmodified.
//
// This module never stores anything itself — no localStorage/
// sessionStorage, no module-level cache of any token or secret. Callers
// hold whatever state they need in React state only, for the lifetime of
// the page.

/** The documented error-envelope body shape (09 §1 / §5.8): a stable, typed
 * code plus a safe, user-facing message. */
export interface ApiErrorBody {
  code: string;
  message: string;
  request_id: string;
  retryable: boolean;
  retry_after?: number;
}

/** Typed error carrying the backend's exact error code so callers can
 * branch on it (`invalid_credentials`, `locked_out`, `session_expired`,
 * `csrf_failed`, `reverification_required`, `funding_locked`,
 * `precondition_failed`, ...) without parsing prose. Shared by every
 * control-plane client (auth and non-auth alike) — there is exactly one
 * error type in this app. */
export class AuthApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly requestId: string;
  readonly retryable: boolean;
  readonly retryAfterSeconds?: number;

  constructor(status: number, body: ApiErrorBody) {
    super(body.message);
    this.name = "AuthApiError";
    this.status = status;
    this.code = body.code;
    this.requestId = body.request_id;
    this.retryable = body.retryable;
    this.retryAfterSeconds = body.retry_after;
  }
}

/** True for the one failure mode every authenticated call must react to
 * uniformly: clear all client-held auth state and route back to Login. */
export function isSessionExpired(err: unknown): boolean {
  return err instanceof AuthApiError && err.code === "session_expired";
}

/** True for the rate-limit failure mode: the caller should render a
 * locked-out state with `retryAfterSeconds`. */
export function isLockedOut(err: unknown): boolean {
  return err instanceof AuthApiError && err.code === "locked_out";
}

/** The generic fallback for a caught error that is not an AuthApiError
 * (e.g. `fetch` itself rejected — offline, DNS, CORS) — every call site in
 * this app that catches an unknown error and needs to display *something*
 * uses this same shape, mirroring FirstRunSetup/LoginScreen's own inline
 * fallback. */
export function networkError(): AuthApiError {
  return new AuthApiError(0, { code: "network_error", message: "Could not reach the server. Try again.", request_id: "", retryable: true });
}

/** Normalizes any caught value into an AuthApiError — the typed error as-is
 * if it already is one, else the generic networkError() fallback. */
export function toApiError(err: unknown): AuthApiError {
  return err instanceof AuthApiError ? err : networkError();
}

/** Parses a non-ok Response's `{error: ...}` envelope and throws the typed
 * AuthApiError for it — the shared failure path for both the generic
 * `request` helper below and any bespoke call (e.g. the credential-reveal
 * endpoint, whose SUCCESS body is raw plaintext rather than JSON, so it
 * cannot go through `request` itself but still needs identical error
 * handling). */
export async function throwApiError(response: Response): Promise<never> {
  const body: unknown = await response.json().catch(() => null);
  const errorBody =
    body && typeof body === "object" && "error" in body
      ? (body as { error: ApiErrorBody }).error
      : { code: "unknown", message: "Unexpected error.", request_id: "", retryable: false };
  throw new AuthApiError(response.status, errorBody);
}

/** What a request observer is told about a settled HTTP exchange — status
 * and ok only, by construction: no body, no headers, no URL beyond what the
 * caller already knows it asked for. Exists for controlClient's debug
 * operation log (see its `debugLog`); authClient passes no observer. */
export interface RequestObservation {
  status: number;
  ok: boolean;
}

/** The header name every mutating call attaches its CSRF token under
 * (mirrors CSRF_HEADER in authClient.ts/controlClient.ts — kept private
 * here since only the self-heal retry below needs to read/replace it). */
const CSRF_HEADER = "X-CSRF-Token";

/** Registered by AuthGate (the sole owner of CSRF-token state) so this
 * module can recover from a `csrf_failed` response without any calling
 * component knowing it happened. Returns a *fresh* token for the live
 * session, or rejects if there is no live session to refresh (in practice,
 * `fetchAuthSession()` rejecting with `session_expired`). */
export type CsrfRefreshHandler = () => Promise<string>;

let csrfRefreshHandler: CsrfRefreshHandler | null = null;

/** Registers (or, passed `null`, clears) the CSRF refresh handler. Exactly
 * one handler is meaningful at a time — AuthGate is the only component
 * that ever calls this. */
export function setCsrfRefreshHandler(handler: CsrfRefreshHandler | null): void {
  csrfRefreshHandler = handler;
}

function buildInit(init: RequestInit): RequestInit {
  return {
    ...init,
    credentials: "same-origin",
    headers: {
      "Content-Type": "application/json",
      ...(init.headers ?? {}),
    },
  };
}

async function doFetch(
  base: string,
  path: string,
  init: RequestInit,
  observe?: (o: RequestObservation) => void,
): Promise<Response> {
  const response = await fetch(base + path, buildInit(init));
  observe?.({ status: response.status, ok: response.ok });
  return response;
}

/** Reads a response's `{error: {code}}` without consuming it for the
 * caller — used only to decide whether the self-heal retry below applies;
 * the original `response` is still intact for `throwApiError` afterwards. */
async function peekErrorCode(response: Response): Promise<string | null> {
  const body: unknown = await response
    .clone()
    .json()
    .catch(() => null);
  if (body && typeof body === "object" && "error" in body) {
    return (body as { error: ApiErrorBody }).error.code;
  }
  return null;
}

/** Builds a copy of `init` with a fresh CSRF token in place of whatever
 * `X-CSRF-Token` it already carried. A GET (or any call that never had the
 * header) is returned unchanged — there is nothing to refresh. */
function withRefreshedCsrfToken(init: RequestInit, freshToken: string): RequestInit {
  const headers = init.headers;
  if (!headers || typeof headers !== "object" || Array.isArray(headers) || !(CSRF_HEADER in headers)) {
    return init;
  }
  return { ...init, headers: { ...(headers as Record<string, string>), [CSRF_HEADER]: freshToken } };
}

/** Envelope-aware fetch wrapper shared by every control-plane client
 * module: always `credentials: "same-origin"`, always JSON request/response
 * bodies, unwraps `{data: ...}` on success and throws AuthApiError on
 * `{error: ...}`. base is the caller's own route prefix (e.g.
 * `/api/control/v1/auth` for authClient, `/api/control/v1` for
 * controlClient) so each module keeps its own base path exactly as before.
 *
 * `observe` (optional) is called once with {status, ok} as soon as the
 * response arrives — never with any body or header content. A fetch-level
 * rejection (offline/DNS) propagates without an observation; the caller's
 * own error handling records that case.
 *
 * Self-heal: a `csrf_failed` response (backend's csrfKey rotated under an
 * already-open tab — see AuthGate's registration of setCsrfRefreshHandler)
 * is not surfaced directly. If a refresh handler is registered, it is
 * called for a fresh token and the ORIGINAL request is retried exactly
 * once with that token in place of the stale one. A handler rejection
 * (no live session left to refresh) propagates as-is instead of the
 * original csrf_failed — it's the more actionable error. With no handler
 * registered, or a `csrf_failed` on the retry too, this falls through to
 * the ordinary throwApiError behavior — no more than one retry, ever. */
export async function request<T>(
  base: string,
  path: string,
  init: RequestInit,
  observe?: (o: RequestObservation) => void,
): Promise<T> {
  const response = await doFetch(base, path, init, observe);

  if (!response.ok) {
    if (csrfRefreshHandler && (await peekErrorCode(response)) === "csrf_failed") {
      const freshToken = await csrfRefreshHandler();
      const retryResponse = await doFetch(base, path, withRefreshedCsrfToken(init, freshToken), observe);
      if (!retryResponse.ok) {
        await throwApiError(retryResponse);
      }
      const retryBody: unknown = await retryResponse.json().catch(() => null);
      return retryBody as T;
    }
    await throwApiError(response);
  }

  const body: unknown = await response.json().catch(() => null);
  return body as T;
}
