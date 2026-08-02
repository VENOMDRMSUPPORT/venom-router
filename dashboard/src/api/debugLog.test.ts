import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createFetchMock, jsonResponse } from "../test/fetchMock";
import { debugLog, listAccounts, listProviders } from "./controlClient";

const SECRET_BODY_VALUE = "sk-canary-debug-log-000000000000";

beforeEach(() => {
  debugLog.clear();
});

afterEach(() => {
  debugLog.clear();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("debugLog — the secret-free operation ring buffer", () => {
  it("records method/path/status/duration for a successful call — and NOTHING that could carry a body", async () => {
    vi.stubGlobal(
      "fetch",
      createFetchMock({
        "GET /api/control/v1/providers": () =>
          jsonResponse(200, { data: { providers: [], leaked_secret: SECRET_BODY_VALUE } }),
      }),
    );

    await listProviders();

    const events = debugLog.snapshot();
    expect(events).toHaveLength(1);
    const event = events[0];
    expect(event.method).toBe("GET");
    expect(event.path).toBe("/api/control/v1/providers");
    expect(event.status).toBe(200);
    expect(event.ok).toBe(true);
    expect(typeof event.durationMs).toBe("number");
    // No field exists for a body, header, or query string — the event's
    // key set is closed, so a secret has nowhere to live.
    expect(Object.keys(event).sort()).toEqual(["at", "durationMs", "id", "method", "ok", "path", "status"]);
    expect(JSON.stringify(events)).not.toContain(SECRET_BODY_VALUE);
  });

  it("strips the query string (cursor/limit) from the recorded path", async () => {
    vi.stubGlobal(
      "fetch",
      createFetchMock({
        "GET /api/control/v1/accounts?limit=200": () => jsonResponse(200, { data: { accounts: [] } }),
      }),
    );

    await listAccounts({ limit: 200 });
    expect(debugLog.snapshot()[0].path).toBe("/api/control/v1/accounts");
  });

  it("records a typed failure's status and request_id, and a fetch rejection as network_error", async () => {
    let calls = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn(() => {
        calls += 1;
        if (calls === 1) {
          return Promise.resolve(
            jsonResponse(404, { error: { code: "not_found", message: "nope", request_id: "req-42", retryable: false } }),
          );
        }
        return Promise.reject(new TypeError("offline"));
      }),
    );

    await expect(listProviders()).rejects.toThrow();
    await expect(listProviders()).rejects.toThrow();

    const [typed, network] = debugLog.snapshot();
    expect(typed.status).toBe(404);
    expect(typed.ok).toBe(false);
    expect(typed.requestId).toBe("req-42");
    expect(network.status).toBe("network_error");
    expect(network.ok).toBe(false);
  });

  it("enforces the 200-event cap by dropping the OLDEST events", async () => {
    vi.stubGlobal(
      "fetch",
      createFetchMock({
        "GET /api/control/v1/providers": () => jsonResponse(200, { data: { providers: [] } }),
      }),
    );

    for (let i = 0; i < 205; i++) await listProviders();

    const events = debugLog.snapshot();
    expect(events).toHaveLength(200);
    // Oldest five dropped: ids are monotonic, so the retained window spans
    // exactly the LAST 200 recorded events (ids are process-global, so no
    // absolute values here).
    expect(events[events.length - 1].id - events[0].id).toBe(199);
  });

  it("clear() empties the buffer and notifies subscribers", async () => {
    vi.stubGlobal(
      "fetch",
      createFetchMock({
        "GET /api/control/v1/providers": () => jsonResponse(200, { data: { providers: [] } }),
      }),
    );
    await listProviders();
    expect(debugLog.snapshot()).toHaveLength(1);

    const listener = vi.fn();
    const unsubscribe = debugLog.subscribe(listener);
    debugLog.clear();
    expect(listener).toHaveBeenCalled();
    expect(debugLog.snapshot()).toHaveLength(0);
    unsubscribe();
  });
});
