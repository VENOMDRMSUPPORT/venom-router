import { deflateSync } from 'node:zlib';
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

const DIGIT_GLYPHS: Record<string, string[]> = {
  '0': ['01110', '10001', '10011', '10101', '11001', '10001', '01110'],
  '1': ['00100', '01100', '00100', '00100', '00100', '00100', '01110'],
  '2': ['01110', '10001', '00001', '00010', '00100', '01000', '11111'],
  '3': ['11110', '00001', '00001', '01110', '00001', '00001', '11110'],
  '4': ['00010', '00110', '01010', '10010', '11111', '00010', '00010'],
  '5': ['11111', '10000', '10000', '11110', '00001', '00001', '11110'],
  '6': ['01110', '10000', '10000', '11110', '10001', '10001', '01110'],
  '7': ['11111', '00001', '00010', '00100', '01000', '01000', '01000'],
  '8': ['01110', '10001', '10001', '01110', '10001', '10001', '01110'],
  '9': ['01110', '10001', '10001', '01111', '00001', '00001', '01110'],
};

function pngChunk(type: string, data: Buffer): Buffer {
  const typeBytes = Buffer.from(type, 'ascii');
  const body = Buffer.concat([typeBytes, data]);
  let crc = 0xffffffff;
  for (const byte of body) {
    crc ^= byte;
    for (let bit = 0; bit < 8; bit++) crc = (crc >>> 1) ^ ((crc & 1) ? 0xedb88320 : 0);
  }
  const header = Buffer.alloc(4);
  header.writeUInt32BE(data.length, 0);
  const checksum = Buffer.alloc(4);
  checksum.writeUInt32BE((crc ^ 0xffffffff) >>> 0, 0);
  return Buffer.concat([header, body, checksum]);
}

function digitSvgToPngDataUrl(value: string): string {
  const match = /^data:image\/svg\+xml;base64,(.+)$/i.exec(value);
  if (!match) return value;
  const svg = Buffer.from(match[1], 'base64').toString('utf8');
  const digit = /<text\b[^>]*>\s*(\d)\s*<\/text>/i.exec(svg)?.[1];
  const glyph = digit ? DIGIT_GLYPHS[digit] : undefined;
  if (!glyph) return value;

  const width = 128;
  const height = 128;
  const scale = 12;
  const left = Math.floor((width - glyph[0].length * scale) / 2);
  const top = Math.floor((height - glyph.length * scale) / 2);
  const raw = Buffer.alloc((width * 4 + 1) * height, 255);
  for (let y = 0; y < height; y++) {
    raw[y * (width * 4 + 1)] = 0;
  }
  for (let row = 0; row < glyph.length; row++) {
    for (let column = 0; column < glyph[row].length; column++) {
      if (glyph[row][column] !== '1') continue;
      for (let dy = 0; dy < scale; dy++) {
        for (let dx = 0; dx < scale; dx++) {
          const x = left + column * scale + dx;
          const y = top + row * scale + dy;
          const offset = y * (width * 4 + 1) + 1 + x * 4;
          raw[offset] = 0;
          raw[offset + 1] = 0;
          raw[offset + 2] = 0;
          raw[offset + 3] = 255;
        }
      }
    }
  }
  const header = Buffer.alloc(13);
  header.writeUInt32BE(width, 0);
  header.writeUInt32BE(height, 4);
  header[8] = 8;
  header[9] = 6;
  const png = Buffer.concat([
    Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]),
    pngChunk('IHDR', header),
    pngChunk('IDAT', deflateSync(raw)),
    pngChunk('IEND', Buffer.alloc(0)),
  ]);
  return `data:image/png;base64,${png.toString('base64')}`;
}

function normalizeClinePayload(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(normalizeClinePayload);
  if (typeof value !== 'object' || value === null) return value;
  return Object.fromEntries(Object.entries(value).map(([key, item]) => [
    key,
    key === 'url' && typeof item === 'string' ? digitSvgToPngDataUrl(item) : normalizeClinePayload(item),
  ]));
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
    const normalizedBody = input.providerId === 'clinepass'
      ? normalizeClinePayload(body) as Record<string, unknown>
      : body;
    const response = await fetchImpl(`${baseUrl}/chat/completions`, {
      method: 'POST',
      headers: evaluationHeaders(input.providerId, credential),
      body: JSON.stringify({ ...normalizedBody, model: input.modelId, stream: false }),
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
