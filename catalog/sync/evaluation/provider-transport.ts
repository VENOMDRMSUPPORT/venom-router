import { deflateSync } from 'node:zlib';
import { OVERALL_SCORE_POLICY } from './score.ts';
import { ranOutOfRoom } from './fixtures.ts';
import { fetchForEvaluationProvider } from './proxy-pool.ts';
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

export type CredentialState = 'present' | 'missing' | 'malformed_name';

export interface CredentialStatus {
  providerId: string;
  /** The variable this code asks for. Public by construction — it is in this file. */
  envName: string;
  state: CredentialState;
  /** The corrupted key the value is actually filed under, when there is one. */
  foundAs?: string;
}

/** A variable name as it would read once the junk an editor may prepend is gone. */
const normalizeEnvName = (key: string) => key.replace(/^﻿/, '').trim();

/**
 * Which evaluation credentials THIS process can actually see, by name.
 *
 * The gap this closes: `missing_credentials` is decided by `process.env`, and a
 * key can be perfectly present in `catalog/.env` and still be invisible here —
 * either because nothing loaded the file, or because its NAME is corrupted. A
 * UTF-8 BOM (which PowerShell writes at the head of any redirected file) binds
 * to the first variable name in an env file, and `node --env-file` does not
 * strip it: the process ends up holding `﻿VENOM_..._API_KEY`, so every
 * lookup by the real name misses. That failure is indistinguishable from "no key
 * configured" unless something looks for it on purpose, which is what this does.
 *
 * Reads presence only. A credential VALUE is never returned, printed, compared,
 * or included in any state — the P1-SEC-006 rule applies to diagnostics too, and
 * a diagnostic is exactly where a secret would leak by accident.
 */
export function evaluationCredentialReport(
  env: Record<string, string | undefined> = process.env,
): CredentialStatus[] {
  return Object.entries(CREDENTIAL_ENV).map(([providerId, envName]): CredentialStatus => {
    if (env[envName]?.trim()) return { providerId, envName, state: 'present' };
    const foundAs = Object.keys(env)
      .find((key) => key !== envName && normalizeEnvName(key) === envName && env[key]?.trim());
    if (foundAs) return { providerId, envName, state: 'malformed_name', foundAs };
    return { providerId, envName, state: 'missing' };
  });
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

/**
 * Every image in a payload, as a format a vision model can actually open.
 *
 * The vision fixture ships its digit as an SVG data URL, and most vision
 * endpoints cannot read SVG at all. This was scoped to ClinePass, and the cost
 * of that scope was invisible: every other provider received the raw SVG, was
 * answered `400 "The image format is illegal and cannot be opened"`, and
 * `runtime.ts` records a 4xx as a model failure worth zero of five criteria. So
 * twelve identities across unrelated vendors published a vision score of 0.3 —
 * zero successes out of three hundred — while `kimi-k2.6`, measured on ClinePass,
 * scored 95.7 on the same fixture.
 *
 * The conversion belongs to the payload rather than to one provider: nothing
 * about needing a raster image is ClinePass-specific. Rasterising for a provider
 * that would have accepted SVG costs nothing, since PNG is universally read.
 *
 * The fixture itself is not changed, deliberately — its payload is inside the
 * test-set digest, so emitting PNG there would invalidate all 195 stored scores
 * and re-buy the entire corpus to fix twelve dimensions.
 */
function withReadableImages(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(withReadableImages);
  if (typeof value !== 'object' || value === null) return value;
  return Object.fromEntries(Object.entries(value).map(([key, item]) => [
    key,
    key === 'url' && typeof item === 'string' ? digitSvgToPngDataUrl(item) : withReadableImages(item),
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

/** ClinePass wraps every answer in a `data` envelope. Nothing downstream should know. */
function normalizeResponseBody(providerId: string, body: unknown): unknown {
  if (providerId !== 'clinepass' || typeof body !== 'object' || body === null || Array.isArray(body)) {
    return body;
  }
  const envelope = body as Record<string, unknown>;
  return typeof envelope.data === 'object' && envelope.data !== null && !Array.isArray(envelope.data)
    ? envelope.data
    : body;
}

/** The same body with the output cap the next attempt should pay for. */
function withOutputBudget(body: Record<string, unknown>, tokens: number): Record<string, unknown> {
  return { ...body, max_tokens: tokens };
}


/**
 * Which wire protocol a model is actually served on.
 *
 * OpenCode's own model table names an endpoint per model, and they are not all
 * the same one: GPT, Grok and Muse Spark sit on `/responses`, Claude and the
 * Qwen Max/Plus line on `/messages`, Gemini on a per-model path, and the rest on
 * `/chat/completions`. Assuming one endpoint for the whole provider made
 * `grok-4.5` answer 503 and look like a dead endpoint, when it answers fine on
 * the path it is published under.
 *
 * Only `/responses` is implemented, because it is the one carrying models this
 * catalog cannot otherwise evaluate. The `/messages` models answer on
 * `/chat/completions` through this gateway, so they need nothing yet — when one
 * stops, this is the function that should learn about it, not the caller.
 *
 * Source: https://opencode.ai/docs/zen (the Endpoints table), read 2026-08-20.
 *
 * One row per family, carrying every wire fact this gateway needs about it. A
 * second table keyed by the same prefixes would be a second place to remember
 * when the next family arrives — and the one that gets forgotten is the one
 * that returns a valid 200 with nothing in it to grade.
 */
const MODEL_FAMILIES: {
  prefix: string;
  protocol: EvaluationProtocol;
  /**
   * A floor the family needs before it emits any message at all.
   *
   * A saving, not a guarantee. `ranOutOfRoom` now catches a cut-off answer on
   * every protocol and buys one raised-budget retry, so a family missing from
   * this table costs one wasted request rather than a wrong score.
   * Declaring the floor here means the first attempt already pays enough.
   */
  minOutputTokens?: number;
  /**
   * A sampling parameter the family rejects outright rather than ignores.
   *
   * `gpt-5.6-luna` answers `400 invalid_request_error: Unsupported parameter:
   * 'temperature' is not supported with this model.` on EVERY request, so all
   * sixty vision samples failed and the offer published no score at all. The
   * reasoning families on the Responses API do not take a temperature.
   *
   * Outside the test-set digest, for the same reason as `minOutputTokens`: the
   * digest covers what was ASKED, and this is how the answer is obtained. No
   * stored score is invalidated and nothing is re-bought.
   */
  omitTemperature?: boolean;
}[] = [
  { prefix: 'gpt-', protocol: 'responses', omitTemperature: true },
  { prefix: 'grok-', protocol: 'responses' },
  // Muse Spark commonly spends about 500 tokens on hidden reasoning before it
  // emits any message. The shared 512-token fixture cap therefore produced a
  // valid HTTP 200 with `status: incomplete` and no answer to grade.
  { prefix: 'muse-spark-', protocol: 'responses', minOutputTokens: 2048 },
];

/** The id without its vendor namespace, which is what the family names match. */
const bareModelId = (modelId: string): string =>
  modelId.includes('/') ? modelId.slice(modelId.lastIndexOf('/') + 1) : modelId;

const familyFor = (modelId: string): (typeof MODEL_FAMILIES)[number] | null =>
  MODEL_FAMILIES.find((family) => bareModelId(modelId).startsWith(family.prefix)) ?? null;

export function protocolFor(providerId: string, modelId: string): EvaluationProtocol {
  if (providerId !== 'opencode-go' && providerId !== 'opencode-zen') return 'chat-completions';
  return familyFor(modelId)?.protocol ?? 'chat-completions';
}

/**
 * The Responses API, translated at the boundary.
 *
 * OpenCode serves some models through OpenAI's Responses API rather than
 * chat/completions — the official model table names the endpoint per model, and
 * `grok-4.5`, `gpt-5.6-luna` and the Muse Spark family are on `/responses`.
 * Calling them on `/chat/completions` produced an upstream 503 that looked
 * exactly like an outage, which is how a perfectly healthy model came within one
 * commit of being withheld as unreachable.
 *
 * Everything downstream — every grader, the runner, the score — reads
 * `choices[0].message`. So the translation lives here and nothing else learns a
 * second wire format. A grader quietly reading the wrong field is the failure
 * that already cost this catalog a full paid evaluation run.
 */
function toResponsesContent(content: unknown): unknown {
  if (typeof content === 'string') return [{ type: 'input_text', text: content }];
  if (!Array.isArray(content)) return content;
  return content.map((part) => {
    if (typeof part !== 'object' || part === null) return part;
    const entry = part as Record<string, unknown>;
    if (entry.type === 'text') return { type: 'input_text', text: entry.text };
    if (entry.type === 'image_url') {
      const image = entry.image_url as Record<string, unknown> | undefined;
      return { type: 'input_image', image_url: image?.url ?? entry.image_url };
    }
    return part;
  });
}

/**
 * Chat nests a tool under `function`; the Responses API keeps the same fields at
 * the top level. Sending the nested shape is rejected, which reads as the model
 * refusing to call tools rather than as a payload we never translated.
 */
function toResponsesTools(tools: unknown): unknown {
  if (!Array.isArray(tools)) return tools;
  return tools.map((tool) => {
    if (typeof tool !== 'object' || tool === null) return tool;
    const entry = tool as Record<string, unknown>;
    const fn = entry.function;
    if (typeof fn !== 'object' || fn === null) return tool;
    const { name, description, parameters } = fn as Record<string, unknown>;
    return { type: 'function', name, description, parameters };
  });
}

function toResponsesRequest(body: Record<string, unknown>, modelId: string): Record<string, unknown> {
  const { messages, max_tokens: maxTokens, stream, tools, ...rest } = body;
  const request: Record<string, unknown> = { ...rest, model: modelId };
  if (tools !== undefined) request.tools = toResponsesTools(tools);
  if (Array.isArray(messages)) {
    request.input = messages.map((message) => {
      const entry = message as Record<string, unknown>;
      return { ...entry, content: toResponsesContent(entry.content) };
    });
  }
  // The cap only changes name here. What it should be was decided once, before
  // the attempt, so both protocols spend the same budget on the same sample.
  if (typeof maxTokens === 'number') request.max_output_tokens = maxTokens;
  return request;
}

/** Fold a Responses envelope back into the one message shape every grader reads. */
function fromResponsesBody(body: unknown): unknown {
  if (typeof body !== 'object' || body === null || Array.isArray(body)) return body;
  const envelope = body as Record<string, unknown>;
  if (!Array.isArray(envelope.output)) return body;

  let content = '';
  let reasoning = '';
  const toolCalls: unknown[] = [];
  for (const item of envelope.output as Record<string, unknown>[]) {
    if (item?.type === 'function_call') {
      toolCalls.push({
        id: item.call_id ?? item.id,
        type: 'function',
        function: { name: item.name, arguments: item.arguments },
      });
      continue;
    }
    const parts = Array.isArray(item?.content) ? item.content as Record<string, unknown>[] : [];
    for (const part of parts) {
      const text = typeof part?.text === 'string' ? part.text : '';
      if (!text) continue;
      // Reasoning is kept, and kept SEPARATE: folding it into the answer is the
      // concatenation that scored perfect answers zero across three dimensions.
      if (item.type === 'reasoning' || part.type === 'reasoning_text') reasoning += text;
      else content += text;
    }
  }

  const message: Record<string, unknown> = { role: 'assistant', content };
  if (reasoning) message.reasoning = reasoning;
  if (toolCalls.length > 0) message.tool_calls = toolCalls;
  // The envelope's own verdict, carried across rather than asserted. A retained
  // sample that says `stop` when the provider said `incomplete` is a diagnostic
  // that lies about why a dimension has no score.
  const finishReason = envelope.status === 'incomplete' ? 'length' : 'stop';
  return { ...envelope, choices: [{ index: 0, message, finish_reason: finishReason }] };
}

export function createEvaluationTransport(input: CreateEvaluationTransportInput): EvaluationTransport {
  const baseUrl = PROVIDER_BASE_URLS[input.providerId];
  if (!baseUrl) throw new Error(`unsupported_evaluation_provider:${input.providerId}`);
  const protocol = input.protocol ?? protocolFor(input.providerId, input.modelId);
  if (protocol !== 'chat-completions' && protocol !== 'responses') {
    throw new Error(`unsupported_evaluation_protocol:${protocol}`);
  }
  const fetchImpl = input.fetchImpl ?? fetchForEvaluationProvider(input.providerId);
  const policy = {
    timeoutMs: OVERALL_SCORE_POLICY.requestTimeoutMs,
    transientRetries: OVERALL_SCORE_POLICY.transientRetries,
  };

  const attempt = (body: Record<string, unknown>, credential: string) =>
    callWithPolicy(async (): Promise<TransportResponse> => {
      const normalizedBody = withReadableImages(body) as Record<string, unknown>;
      const path = protocol === 'responses' ? '/responses' : '/chat/completions';
      const requestBody = protocol === 'responses'
        ? toResponsesRequest(normalizedBody, input.modelId)
        : { ...normalizedBody, model: input.modelId, stream: false };
      const response = await fetchImpl(`${baseUrl}${path}`, {
        method: 'POST',
        headers: evaluationHeaders(input.providerId, credential),
        body: JSON.stringify(requestBody),
        signal: AbortSignal.timeout(OVERALL_SCORE_POLICY.requestTimeoutMs),
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
        body: protocol === 'responses'
          ? fromResponsesBody(responseBody)
          : normalizeResponseBody(input.providerId, responseBody),
        headers: headersToRecord(response.headers),
      };
    }, policy);

  const ceiling = OVERALL_SCORE_POLICY.truncationRetryOutputTokens;

  /**
   * Whether this model has already shown it cannot answer inside the fixture
   * budget, so the next sample should not pay to find that out again.
   *
   * A model that truncates once on a dimension truncates on nearly all of it:
   * `opencode-go/hy3` did so on 60 of 60 coding samples. Re-learning it per
   * sample means a wasted request and a wasted 512 tokens of trace, sixty times
   * per dimension. Raising the cap costs nothing by itself — a cap is a limit,
   * not a target, and billing follows the tokens actually generated.
   *
   * Scoped to one transport, which is built per offer, so it never leaks a
   * conclusion about one model into a request for another.
   */
  let startAtCeiling = false;

  /**
   * One request, and one more only if the provider ran out of room.
   *
   * This buys the model space; it does not judge what came back. Judging needs
   * the grade, and the grade lives in `runtime.ts` — a cut-off response that
   * scored full marks is a perfectly good measurement, and a transport cannot
   * know that. An earlier version decided here, which meant it could only act on
   * a cut-off response that was completely EMPTY: every model that emitted half
   * an answer before the cap was never offered a bigger budget at all.
   *
   * Exactly one raised-budget retry. Riding the transient-retry loop instead
   * would have bought four paid attempts per sample for the same answer.
   */
  return async (payload, credential) => {
    const raw = typeof payload === 'object' && payload !== null && !Array.isArray(payload)
      ? payload as Record<string, unknown>
      : { messages: [{ role: 'user', content: String(payload) }] };
    // Dropped here rather than in the fixture: the fixture's payload is inside
    // the test-set digest, so editing it would invalidate every stored score.
    const body = familyFor(input.modelId)?.omitTemperature
      ? Object.fromEntries(Object.entries(raw).filter(([key]) => key !== 'temperature'))
      : raw;
    const declared = typeof body.max_tokens === 'number'
      ? Math.max(body.max_tokens, familyFor(input.modelId)?.minOutputTokens ?? 0)
      : null;
    const opening = declared === null ? null : startAtCeiling ? Math.max(declared, ceiling) : declared;

    const first = await attempt(opening === null ? body : withOutputBudget(body, opening), credential);
    if (first.kind !== 'success' || !ranOutOfRoom(first.response.body)) return first;

    startAtCeiling = true;
    // Already at the ceiling, so there is no more room to offer. The response is
    // handed on as it is; the grader decides whether it is evidence.
    if (opening !== null && opening >= ceiling) return first;
    // The second attempt's outcome is the outcome, including its failures. Falling
    // back to the truncated first response would turn a transient 429 on the retry
    // into `answer_truncated`, which stops the whole dimension instead of leaving
    // one sample to be picked up by the next run.
    return attempt(withOutputBudget(body, ceiling), credential);
  };
}
