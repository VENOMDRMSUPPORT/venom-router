import { afterEach, describe, expect, it, vi } from "vitest";
import { jsonResponse } from "../test/fetchMock";
import {
  AuthApiError,
  fetchAuthSession,
  fetchAuthStatus,
  isLockedOut,
  isSessionExpired,
  login,
  logout,
  reverify,
  setupOwner,
} from "./authClient";

describe("authClient", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("fetchAuthStatus GETs /auth/status and maps setup_complete", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { data: { setup_complete: true } }));
    vi.stubGlobal("fetch", fetchMock);

    const result = await fetchAuthStatus();

    expect(result).toEqual({ setupComplete: true });
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("/api/control/v1/auth/status");
    expect(init.method).toBe("GET");
    expect(init.credentials).toBe("same-origin");
  });

  it("setupOwner POSTs the password and returns the session + csrf token, without sending a CSRF header (no session exists yet)", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse(200, {
        data: { session: { idle_expires_at: "2026-07-24T00:30:00Z", absolute_expires_at: "2026-07-24T12:00:00Z" } },
        csrf_token: "csrf-setup-token",
      }),
    );
    vi.stubGlobal("fetch", fetchMock);

    const result = await setupOwner("a-long-enough-password");

    expect(result.csrfToken).toBe("csrf-setup-token");
    expect(result.session).toEqual({ idleExpiresAt: "2026-07-24T00:30:00Z", absoluteExpiresAt: "2026-07-24T12:00:00Z" });
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("/api/control/v1/auth/setup");
    expect(JSON.parse(init.body)).toEqual({ password: "a-long-enough-password" });
    expect(init.headers["X-CSRF-Token"]).toBeUndefined();
  });

  it("login throws a typed AuthApiError with code=invalid_credentials on 401", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse(401, {
        error: { code: "invalid_credentials", message: "invalid credentials", request_id: "req1", retryable: false },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);

    await expect(login("wrong-password")).rejects.toMatchObject({ code: "invalid_credentials", status: 401 });
  });

  it("login throws a typed AuthApiError with code=locked_out and retryAfterSeconds on 429", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse(429, {
        error: {
          code: "locked_out",
          message: "too many failed attempts, try again later",
          request_id: "req2",
          retryable: true,
          retry_after: 900,
        },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);

    try {
      await login("wrong-password");
      throw new Error("expected login to throw");
    } catch (err) {
      expect(err).toBeInstanceOf(AuthApiError);
      expect(isLockedOut(err)).toBe(true);
      expect((err as AuthApiError).retryAfterSeconds).toBe(900);
    }
  });

  it("fetchAuthSession throws code=session_expired on 401 and isSessionExpired recognizes it", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse(401, { error: { code: "session_expired", message: "session expired", request_id: "req3", retryable: false } }),
    );
    vi.stubGlobal("fetch", fetchMock);

    try {
      await fetchAuthSession();
      throw new Error("expected fetchAuthSession to throw");
    } catch (err) {
      expect(isSessionExpired(err)).toBe(true);
    }
  });

  it("logout sends the X-CSRF-Token header when a token is provided", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { data: { logged_out: true } }));
    vi.stubGlobal("fetch", fetchMock);

    await logout("my-csrf-token");

    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("/api/control/v1/auth/logout");
    expect(init.headers["X-CSRF-Token"]).toBe("my-csrf-token");
  });

  it("reverify sends the X-CSRF-Token header and the password, and returns reverify_fresh_until", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse(200, { data: { reverify_fresh_until: "2026-07-24T00:05:00Z" } }),
    );
    vi.stubGlobal("fetch", fetchMock);

    const result = await reverify("owner-password-123", "session-csrf-token");

    expect(result.reverifyFreshUntil).toBe("2026-07-24T00:05:00Z");
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("/api/control/v1/auth/reverify");
    expect(init.headers["X-CSRF-Token"]).toBe("session-csrf-token");
    expect(JSON.parse(init.body)).toEqual({ password: "owner-password-123" });
  });
});
