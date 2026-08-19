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
      if (attempt === maxAttempts) {
        return { kind: 'provider_failure', status: response.status, attempts: attempt, errorCode: `http_${response.status}` };
      }
    } catch (error) {
      if (attempt === maxAttempts) {
        return {
          kind: 'provider_failure', status: null, attempts: attempt,
          errorCode: error instanceof Error ? error.message : 'network_transient',
        };
      }
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
