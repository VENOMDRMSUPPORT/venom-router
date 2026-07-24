// Thin fetch wrapper over the owner-auth control-plane API
// (`/api/control/v1/auth/*` — see internal/httpapi/auth.go,
// authsession.go, sessionvalidate.go, reverify.go, lockout.go, csrf.go,
// which are authoritative for every shape and header/cookie name here).
//
// This module owns exactly two things: (1) translating the documented
// JSON envelopes into typed values/errors, and (2) attaching the
// `X-CSRF-Token` header on mutating calls once a caller has one. It never
// stores anything itself — no localStorage/sessionStorage, no module-level
// cache of the password or the CSRF token — callers (AuthGate and its
// children) hold whatever state they need in React state only, for the
// lifetime of the page.
//
// The envelope handling itself (the {data}/{error} unwrap, the AuthApiError
// type, isSessionExpired/isLockedOut) is factored into ../api/http.ts so
// api/controlClient.ts (P2b-UI-001/UI-003's non-auth control routes) can
// reuse it verbatim rather than duplicating it. This file re-exports those
// three names so every existing import of them `from "./authClient"` keeps
// working unchanged.

import { AuthApiError, isLockedOut, isSessionExpired, request as sharedRequest } from "../api/http";

export { AuthApiError, isLockedOut, isSessionExpired };

const AUTH_BASE = "/api/control/v1/auth";

/** The session cookie is HttpOnly; `X-CSRF-Token` is the header name the
 * backend's `requireCSRF` (csrf.go) checks on every mutating request it
 * guards. */
const CSRF_HEADER = "X-CSRF-Token";

export interface SessionTimes {
  idleExpiresAt: string;
  absoluteExpiresAt: string;
}

interface RawSessionTimes {
  idle_expires_at: string;
  absolute_expires_at: string;
}

/** A session plus the CSRF token bound to it — issued by setup/login (in
 * the JSON body) and re-issued by GET /auth/session (restart-safe, since
 * the server's CSRF key is process-lifetime only). */
export interface LiveSession {
  session: SessionTimes;
  csrfToken: string;
}

function toSessionTimes(raw: RawSessionTimes): SessionTimes {
  return { idleExpiresAt: raw.idle_expires_at, absoluteExpiresAt: raw.absolute_expires_at };
}

function request<T>(path: string, init: RequestInit): Promise<T> {
  return sharedRequest<T>(AUTH_BASE, path, init);
}

/** GET /auth/status — whether the single owner_auth row exists yet. Does
 * NOT report session liveness (there is no session context on this
 * unauthenticated call) — that is what fetchAuthSession is for. */
export async function fetchAuthStatus(): Promise<{ setupComplete: boolean }> {
  const body = await request<{ data: { setup_complete: boolean } }>("/status", { method: "GET" });
  return { setupComplete: body.data.setup_complete };
}

/** GET /auth/session — enforces + reports current session validity and
 * recomputes the CSRF token (restart-safe: never assume a token survives
 * a server restart). Throws AuthApiError("session_expired" | "invalid_credentials")
 * when there is no live session. */
export async function fetchAuthSession(): Promise<LiveSession> {
  const body = await request<{ data: { session: RawSessionTimes }; csrf_token: string }>("/session", {
    method: "GET",
  });
  return { session: toSessionTimes(body.data.session), csrfToken: body.csrf_token };
}

/** POST /auth/setup — first-run owner creation. Never sends a CSRF header
 * (no session exists yet to bind one to). */
export async function setupOwner(password: string): Promise<LiveSession> {
  const body = await request<{ data: { session: RawSessionTimes }; csrf_token: string }>("/setup", {
    method: "POST",
    body: JSON.stringify({ password }),
  });
  return { session: toSessionTimes(body.data.session), csrfToken: body.csrf_token };
}

/** POST /auth/login. On invalid_credentials the backend's message is
 * already generic ("invalid credentials") and never reveals whether
 * setup has been completed — callers should display it verbatim rather
 * than inventing their own copy. */
export async function login(password: string): Promise<LiveSession> {
  const body = await request<{ data: { session: RawSessionTimes }; csrf_token: string }>("/login", {
    method: "POST",
    body: JSON.stringify({ password }),
  });
  return { session: toSessionTimes(body.data.session), csrfToken: body.csrf_token };
}

/** POST /auth/logout — idempotent; safe to call with no live session.
 * Sends the CSRF header when one is known, but never throws for its
 * absence (a caller that lost its session has nothing to send). */
export async function logout(csrfToken: string | null): Promise<void> {
  await request<unknown>("/logout", {
    method: "POST",
    headers: csrfToken ? { [CSRF_HEADER]: csrfToken } : undefined,
  });
}

/** POST /auth/reverify — stamps a 5-minute freshness window on the
 * current session. Requires the session's CSRF token (09 §5.4/§5.5). */
export async function reverify(password: string, csrfToken: string): Promise<{ reverifyFreshUntil: string }> {
  const body = await request<{ data: { reverify_fresh_until: string } }>("/reverify", {
    method: "POST",
    headers: { [CSRF_HEADER]: csrfToken },
    body: JSON.stringify({ password }),
  });
  return { reverifyFreshUntil: body.data.reverify_fresh_until };
}
