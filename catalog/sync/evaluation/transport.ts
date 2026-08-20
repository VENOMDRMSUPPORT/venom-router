export interface TransportResponse {
  status: number;
  body: unknown;
  headers: Record<string, string>;
}

export type TransportOutcome =
  | { kind: 'success'; response: TransportResponse; attempts: number }
  | { kind: 'model_failure'; status: number; attempts: number; errorCode: string }
  | { kind: 'provider_failure'; status: number | null; attempts: number; errorCode: string }
  | { kind: 'evaluator_failure'; attempts: number; errorCode: string };

export type EvaluationTransport = (payload: unknown, credential: string) => Promise<TransportOutcome>;

export interface TransportPolicy {
  timeoutMs: number;
  transientRetries: number;
  /** Test hooks also make the retry policy deterministic under fake timers. */
  sleep?: (milliseconds: number) => Promise<void>;
  random?: () => number;
  now?: () => number;
  backoffBaseMs?: number;
  maxBackoffMs?: number;
}

const DEFAULT_BACKOFF_BASE_MS = 1_000;
const DEFAULT_MAX_BACKOFF_MS = 30_000;

function headerValue(headers: Record<string, string>, name: string): string | undefined {
  const expected = name.toLowerCase();
  return Object.entries(headers).find(([key]) => key.toLowerCase() === expected)?.[1];
}

function retryAfterMilliseconds(headers: Record<string, string>, now: number): number | null {
  const retryAfter = headerValue(headers, 'retry-after')?.trim();
  if (!retryAfter) return null;
  const seconds = Number(retryAfter);
  if (Number.isFinite(seconds) && seconds >= 0) return Math.ceil(seconds * 1_000);
  const at = Date.parse(retryAfter);
  return Number.isFinite(at) ? Math.max(0, at - now) : null;
}

function exponentialBackoffMilliseconds(attempt: number, policy: TransportPolicy): number {
  const cap = policy.maxBackoffMs ?? DEFAULT_MAX_BACKOFF_MS;
  const base = policy.backoffBaseMs ?? DEFAULT_BACKOFF_BASE_MS;
  const unjittered = Math.min(cap, base * (2 ** (attempt - 1)));
  // Equal jitter maintains a non-zero pause even when the provider omitted Retry-After.
  const random = policy.random ?? Math.random;
  return Math.round(unjittered * (0.5 + random() * 0.5));
}

/**
 * How long a provider has asked us to wait, or null when it did not say.
 *
 * A Retry-After longer than the backoff cap is not a retry instruction — it is a
 * refusal with a time on it. OpenCode Zen answers a free-usage 429 with
 * `Retry-After: 41922`: eleven and a half hours. Sleeping that off would park
 * the single evaluation worker mid-dimension, with the whole queue behind it and
 * a progress modal frozen at whatever it last reported, for half a day, silently.
 * The caller is told instead, so the run ends cheaply and the queue moves on.
 */
function requestedWaitMs(response: TransportResponse | null, policy: TransportPolicy): number | null {
  if (!response) return null;
  return retryAfterMilliseconds(response.headers, (policy.now ?? Date.now)());
}

function exceedsWaitBudget(waitMs: number | null, policy: TransportPolicy): boolean {
  return waitMs !== null && waitMs > (policy.maxBackoffMs ?? DEFAULT_MAX_BACKOFF_MS);
}

async function waitBeforeRetry(response: TransportResponse | null, attempt: number, policy: TransportPolicy): Promise<void> {
  const retryAfter = requestedWaitMs(response, policy);
  const delay = Math.max(retryAfter ?? 0, exponentialBackoffMilliseconds(attempt, policy));
  const sleep = policy.sleep ?? ((milliseconds: number) => new Promise<void>((resolve) => setTimeout(resolve, milliseconds)));
  await sleep(delay);
}

function withTimeout<T>(operation: Promise<T>, timeoutMs: number): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error('evaluation_request_timeout')), timeoutMs);
    timer.unref?.();
    operation.then(
      (value) => { clearTimeout(timer); resolve(value); },
      (error) => { clearTimeout(timer); reject(error); },
    );
  });
}

export async function callWithPolicy(
  request: () => Promise<TransportResponse>,
  policy: TransportPolicy,
): Promise<TransportOutcome> {
  const maxAttempts = policy.transientRetries + 1;
  for (let attempt = 1; attempt <= maxAttempts; attempt++) {
    try {
      const response = await withTimeout(request(), policy.timeoutMs);
      if (response.status >= 200 && response.status < 300) return { kind: 'success', response, attempts: attempt };
      const transient = response.status === 429 || response.status >= 500;
      if (!transient) return { kind: 'model_failure', status: response.status, attempts: attempt, errorCode: `http_${response.status}` };
      if (exceedsWaitBudget(requestedWaitMs(response, policy), policy)) {
        return { kind: 'provider_failure', status: response.status, attempts: attempt, errorCode: 'retry_after_too_long' };
      }
      if (attempt === maxAttempts) {
        return { kind: 'provider_failure', status: response.status, attempts: attempt, errorCode: `http_${response.status}` };
      }
      await waitBeforeRetry(response, attempt, policy);
    } catch (error) {
      if (attempt === maxAttempts) {
        return {
          kind: 'provider_failure', status: null, attempts: attempt,
          errorCode: error instanceof Error ? error.message : 'network_transient',
        };
      }
      await waitBeforeRetry(null, attempt, policy);
    }
  }
  return { kind: 'evaluator_failure', attempts: maxAttempts, errorCode: 'unreachable_transport_state' };
}

export function redactSecrets(value: unknown, secrets: string[]): unknown {
  const active = secrets.filter(Boolean);
  if (typeof value === 'string') {
    return active.reduce((text, secret) => text.split(secret).join('[REDACTED]'), value);
  }
  if (Array.isArray(value)) return value.map((item) => redactSecrets(item, active));
  if (typeof value === 'object' && value !== null) {
    return Object.fromEntries(Object.entries(value).map(([key, item]) => [key, redactSecrets(item, active)]));
  }
  return value;
}
