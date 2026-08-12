/**
 * Fetch discipline (failure layer 2).
 *
 * Every upstream call goes through here so that timeout, retry and backoff
 * behaviour is defined once. A caller cannot accidentally get a naked fetch
 * with no timeout, because there is only one door.
 */

export interface JsonResponse {
  status: number;
  body: unknown;
  etag?: string;
}

export type FetchJson = (url: string, opts?: { etag?: string }) => Promise<JsonResponse>;

export interface RetryOptions {
  retries?: number;
  timeoutMs?: number;
  /** Injected so tests never actually wait. */
  sleep?: (ms: number) => Promise<void>;
  baseDelayMs?: number;
}

const defaultSleep = (ms: number) => new Promise<void>((r) => setTimeout(r, ms));

/** 5xx and 429 are worth retrying; a 4xx is a contract problem and is not. */
function retryable(status: number): boolean {
  return status === 429 || status >= 500;
}

export class FetchFailure extends Error {
  // Written longhand: Node's strip-only TypeScript mode does not support
  // parameter properties, and the service deliberately has no build step.
  readonly status: number | null;
  readonly attempts: number;

  constructor(message: string, status: number | null, attempts: number) {
    super(message);
    this.name = 'FetchFailure';
    this.status = status;
    this.attempts = attempts;
  }
}

/**
 * Fetch JSON with a timeout and bounded exponential backoff.
 *
 * Honours `Retry-After` when the server sends one — guessing a shorter delay
 * than the server asked for is how a client earns a longer ban.
 */
export function createFetchJson(
  impl: typeof globalThis.fetch = globalThis.fetch,
  { retries = 3, timeoutMs = 20_000, sleep = defaultSleep, baseDelayMs = 500 }: RetryOptions = {},
): FetchJson {
  return async function fetchJson(url, opts = {}) {
    let lastStatus: number | null = null;
    let lastError: unknown = null;

    for (let attempt = 1; attempt <= retries; attempt++) {
      const control = new AbortController();
      const timer = setTimeout(() => control.abort(), timeoutMs);
      try {
        const headers: Record<string, string> = { accept: 'application/json' };
        if (opts.etag) headers['if-none-match'] = opts.etag;
        const res = await impl(url, { signal: control.signal, headers });
        lastStatus = res.status;

        if (res.status === 304) return { status: 304, body: null, etag: opts.etag };
        if (res.ok) {
          return { status: res.status, body: await res.json(), etag: res.headers.get('etag') ?? undefined };
        }
        if (!retryable(res.status)) {
          throw new FetchFailure(`${url} -> HTTP ${res.status}`, res.status, attempt);
        }
        const retryAfter = Number(res.headers.get('retry-after'));
        const delay = Number.isFinite(retryAfter) && retryAfter > 0 ? retryAfter * 1000 : baseDelayMs * 2 ** (attempt - 1);
        if (attempt < retries) await sleep(delay);
      } catch (err) {
        if (err instanceof FetchFailure) throw err;
        lastError = err;
        if (attempt < retries) await sleep(baseDelayMs * 2 ** (attempt - 1));
      } finally {
        clearTimeout(timer);
      }
    }

    throw new FetchFailure(
      `${url} failed after ${retries} attempts: ${lastError instanceof Error ? lastError.message : `HTTP ${lastStatus}`}`,
      lastStatus,
      retries,
    );
  };
}
