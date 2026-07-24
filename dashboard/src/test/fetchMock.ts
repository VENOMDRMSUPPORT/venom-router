import { vi } from "vitest";

/** Builds a `Response` carrying a JSON body, the shape every `/auth/*`
 * endpoint returns (09 §1's `{data: ...}` / `{error: ...}` envelope). */
export function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

export type FetchHandler = (init: RequestInit) => Response | Promise<Response>;

/**
 * A minimal route-dispatching `fetch` mock: keys are `"METHOD /path"`
 * (e.g. `"GET /api/control/v1/auth/status"`), values are handlers
 * returning the mocked Response. Throws loudly on any unmapped call so a
 * test's fetch expectations stay explicit rather than silently
 * undefined-ing out.
 */
export function createFetchMock(handlers: Record<string, FetchHandler>): ReturnType<typeof vi.fn> {
  return vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const method = init?.method ?? "GET";
    const url = String(input);
    const key = `${method} ${url}`;
    const handler = handlers[key];
    if (!handler) {
      throw new Error(`createFetchMock: no handler registered for "${key}"`);
    }
    return Promise.resolve(handler(init ?? {}));
  });
}
