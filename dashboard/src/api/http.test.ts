// Tests for the shared `request()` transport's CSRF self-heal (see the
// `setCsrfRefreshHandler` doc comment in ./http.ts): a process restart
// rotates the backend's csrfKey (internal/httpapi/auth.go), silently
// invalidating any CSRF token an already-open tab is holding even though
// its session cookie is still valid. Every mutating call that comes back
// `csrf_failed` should transparently refresh the token and retry exactly
// once, with no change to any calling component's props or signature.
import { afterEach, describe, expect, it, vi } from "vitest";
import { jsonResponse } from "../test/fetchMock";
import { AuthApiError, request, setCsrfRefreshHandler } from "./http";

const CSRF_HEADER = "X-CSRF-Token";

function csrfFailedResponse(): Response {
  return jsonResponse(403, {
    error: { code: "csrf_failed", message: "csrf validation failed", request_id: "r1", retryable: false },
  });
}

afterEach(() => {
  setCsrfRefreshHandler(null);
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("request — csrf_failed self-heal", () => {
  it("retries once with the fresh token when a handler is registered, and returns the retry's result", async () => {
    const calls: Array<RequestInit | undefined> = [];
    const fetchMock = vi.fn((_input: RequestInfo | URL, init?: RequestInit) => {
      calls.push(init);
      if (calls.length === 1) return Promise.resolve(csrfFailedResponse());
      return Promise.resolve(jsonResponse(200, { data: { ok: true } }));
    });
    vi.stubGlobal("fetch", fetchMock);
    setCsrfRefreshHandler(async () => "fresh-token");

    const result = await request<{ data: { ok: boolean } }>("/api/control/v1", "/settings", {
      method: "PUT",
      headers: { [CSRF_HEADER]: "stale-token" },
    });

    expect(result).toEqual({ data: { ok: true } });
    expect(calls).toHaveLength(2);
    expect((calls[1]?.headers as Record<string, string>)[CSRF_HEADER]).toBe("fresh-token");
  });

  it("throws AuthApiError(csrf_failed) exactly as before when no handler is registered", async () => {
    const fetchMock = vi.fn(() => Promise.resolve(csrfFailedResponse()));
    vi.stubGlobal("fetch", fetchMock);

    await expect(
      request("/api/control/v1", "/settings", { method: "PUT", headers: { [CSRF_HEADER]: "stale-token" } }),
    ).rejects.toMatchObject({ code: "csrf_failed" });
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("propagates the handler's own rejection instead of the original csrf_failed error", async () => {
    const fetchMock = vi.fn(() => Promise.resolve(csrfFailedResponse()));
    vi.stubGlobal("fetch", fetchMock);
    const sessionExpired = new AuthApiError(401, {
      code: "session_expired",
      message: "session expired",
      request_id: "r2",
      retryable: false,
    });
    setCsrfRefreshHandler(() => Promise.reject(sessionExpired));

    await expect(
      request("/api/control/v1", "/settings", { method: "PUT", headers: { [CSRF_HEADER]: "stale-token" } }),
    ).rejects.toBe(sessionExpired);
    // The dead-session case gives up immediately — no point retrying a
    // fetch we already know can't get a fresh token.
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("does not loop when the retry also comes back csrf_failed", async () => {
    const fetchMock = vi.fn(() => Promise.resolve(csrfFailedResponse()));
    vi.stubGlobal("fetch", fetchMock);
    setCsrfRefreshHandler(async () => "fresh-token");

    await expect(
      request("/api/control/v1", "/settings", { method: "PUT", headers: { [CSRF_HEADER]: "stale-token" } }),
    ).rejects.toMatchObject({ code: "csrf_failed" });
    // One original attempt + exactly one retry, never more.
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("leaves a GET request completely unaffected", async () => {
    const fetchMock = vi.fn<(input: RequestInfo | URL, init?: RequestInit) => Promise<Response>>(() =>
      Promise.resolve(jsonResponse(200, { data: { ok: true } })),
    );
    vi.stubGlobal("fetch", fetchMock);
    setCsrfRefreshHandler(async () => "fresh-token");

    const result = await request<{ data: { ok: boolean } }>("/api/control/v1", "/settings", { method: "GET" });

    expect(result).toEqual({ data: { ok: true } });
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [, init] = fetchMock.mock.calls[0];
    const headers = (init as RequestInit | undefined)?.headers as Record<string, string> | undefined;
    expect(headers?.[CSRF_HEADER]).toBeUndefined();
  });
});
