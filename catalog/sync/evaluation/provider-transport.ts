import { OVERALL_SCORE_POLICY } from './score.ts';
import { callWithPolicy, type EvaluationTransport, type TransportResponse } from './transport.ts';

export type EvaluationProtocol = 'chat-completions' | 'responses' | 'messages';

const PROVIDER_BASE_URLS: Record<string, string> = {
  'ollama-cloud': 'https://ollama.com/v1',
  'opencode-go': 'https://opencode.ai/zen/go/v1',
  'opencode-zen': 'https://opencode.ai/zen/v1',
  clinepass: 'https://api.cline.bot/api/v1',
};

const CREDENTIAL_ENV: Record<string, string> = {
  'ollama-cloud': 'VENOM_CATALOG_OLLAMA_CLOUD_API_KEY',
  'opencode-go': 'VENOM_CATALOG_OPENCODE_GO_API_KEY',
  'opencode-zen': 'VENOM_CATALOG_OPENCODE_ZEN_API_KEY',
  clinepass: 'VENOM_CATALOG_CLINEPASS_API_KEY',
};

export function resolveEvaluationCredential(providerId: string): string | null {
  const name = CREDENTIAL_ENV[providerId];
  if (!name) return null;
  const value = process.env[name];
  return value?.trim() ? value : null;
}

export interface CreateEvaluationTransportInput {
  providerId: string;
  modelId: string;
  protocol?: EvaluationProtocol;
  credential: string;
  fetchImpl?: typeof fetch;
}

export function evaluationHeaders(providerId: string, credential: string): Record<string, string> {
  const headers: Record<string, string> = {
    authorization: `Bearer ${credential}`,
    'content-type': 'application/json',
  };
  if (providerId === 'clinepass') {
    headers['user-agent'] = 'Cline/4.1.10';
    headers['x-is-multi-root'] = 'false';
    headers['x-client-type'] = 'cline-vscode';
    headers['x-client-version'] = '4.1.10';
    headers['x-platform'] = 'vscode';
    headers['x-platform-version'] = '4.1.10';
    headers['x-core-version'] = '4.1.10';
    headers['x-task-id'] = crypto.randomUUID();
  }
  return headers;
}

function headersToRecord(headers: Headers): Record<string, string> {
  return Object.fromEntries(headers.entries());
}

function normalizeResponseBody(providerId: string, body: unknown): unknown {
  if (providerId !== 'clinepass' || typeof body !== 'object' || body === null || Array.isArray(body)) {
    return body;
  }
  const envelope = body as Record<string, unknown>;
  return typeof envelope.data === 'object' && envelope.data !== null && !Array.isArray(envelope.data)
    ? envelope.data
    : body;
}

export function createEvaluationTransport(input: CreateEvaluationTransportInput): EvaluationTransport {
  const baseUrl = PROVIDER_BASE_URLS[input.providerId];
  if (!baseUrl) throw new Error(`unsupported_evaluation_provider:${input.providerId}`);
  const protocol = input.protocol ?? 'chat-completions';
  if (protocol !== 'chat-completions') throw new Error(`unsupported_evaluation_protocol:${protocol}`);
  const fetchImpl = input.fetchImpl ?? fetch;
  return async (payload, credential) => callWithPolicy(async (): Promise<TransportResponse> => {
    const body = typeof payload === 'object' && payload !== null && !Array.isArray(payload)
      ? payload as Record<string, unknown>
      : { messages: [{ role: 'user', content: String(payload) }] };
    const response = await fetchImpl(`${baseUrl}/chat/completions`, {
      method: 'POST',
      headers: evaluationHeaders(input.providerId, credential),
      body: JSON.stringify({ ...body, model: input.modelId, stream: false }),
    });
    const text = await response.text();
    let responseBody: unknown = null;
    if (text) {
      try {
        responseBody = JSON.parse(text) as unknown;
      } catch {
        responseBody = { error: 'non_json_provider_response' };
      }
    }
    return {
      status: response.status,
      body: normalizeResponseBody(input.providerId, responseBody),
      headers: headersToRecord(response.headers),
    };
  }, {
    timeoutMs: OVERALL_SCORE_POLICY.requestTimeoutMs,
    transientRetries: OVERALL_SCORE_POLICY.transientRetries,
  });
}
