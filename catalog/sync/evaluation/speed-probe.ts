import { OVERALL_SCORE_POLICY } from './score.ts';
import { protocolFor } from './provider-transport.ts';
import type { SpeedProbe } from './speed-runner.ts';
import { evaluationHeaders } from './provider-transport.ts';
import { fetchForEvaluationProvider } from './proxy-pool.ts';

const PROVIDER_BASE_URLS: Record<string, string> = {
  'ollama-cloud': 'https://ollama.com/v1',
  'opencode-go': 'https://opencode.ai/zen/go/v1',
  'opencode-zen': 'https://opencode.ai/zen/v1',
  clinepass: 'https://api.cline.bot/api/v1',
};

export interface CreateStreamingSpeedProbeInput {
  providerId: string;
  modelId: string;
  credential: string;
  fetchImpl?: typeof fetch;
  nowMs?: () => number;
}

function completionTokens(event: Record<string, unknown>): number | null {
  const usage = event.usage;
  if (typeof usage !== 'object' || usage === null) return null;
  const value = (usage as Record<string, unknown>).completion_tokens;
  return typeof value === 'number' && Number.isFinite(value) && value > 0 ? value : null;
}

function deltaContent(event: Record<string, unknown>): string {
  // The Responses API streams its answer as `response.output_text.delta`, with
  // the text on the event itself. Reasoning arrives on its own event type and is
  // deliberately not counted: the wait for a model to finish thinking belongs in
  // time-to-first-token, not in its throughput.
  if (event.type === 'response.output_text.delta') {
    return typeof event.delta === 'string' ? event.delta : '';
  }
  if (typeof event.type === 'string' && event.type.startsWith('response.')) return '';

  const choices = event.choices;
  if (!Array.isArray(choices) || typeof choices[0] !== 'object' || choices[0] === null) return '';
  const delta = (choices[0] as Record<string, unknown>).delta;
  if (typeof delta !== 'object' || delta === null) return '';
  const content = (delta as Record<string, unknown>).content;
  return typeof content === 'string' ? content : '';
}

/**
 * What the speed probe asks for.
 *
 * The first version asked a model to "output exactly 512 space-separated copies
 * of the lowercase token catalog". A reasoning model treats that as a puzzle
 * and never stops thinking about it: measured on opencode-go/glm-5.3, an 8192
 * token budget produced 8,190 chunks of `reasoning_content` and not one token
 * of answer. No budget fixes that — the instrument was reading "no value" for a
 * whole class of models, which makes it a broken instrument rather than a
 * finding about those models.
 *
 * This asks for ordinary prose of a bounded length instead. A reasoning model
 * still thinks first, and that wait still lands in time-to-first-token where it
 * belongs — but it then answers, so there is something to measure.
 */
export const SPEED_PROMPT =
  'Describe what a software catalogue is, in plain prose, for a reader who has '
  + 'never seen one. Write four short paragraphs. Output only the prose.';

export function createStreamingSpeedProbe(input: CreateStreamingSpeedProbeInput): SpeedProbe {
  const baseUrl = PROVIDER_BASE_URLS[input.providerId];
  if (!baseUrl) throw new Error(`unsupported_evaluation_provider:${input.providerId}`);
  const fetchImpl = input.fetchImpl ?? fetchForEvaluationProvider(input.providerId);
  const nowMs = input.nowMs ?? (() => performance.now());

  return async () => {
    const started = nowMs();
    try {
      const usesResponses = protocolFor(input.providerId, input.modelId) === 'responses';
      const response = await fetchImpl(`${baseUrl}${usesResponses ? '/responses' : '/chat/completions'}`, {
        method: 'POST',
        headers: evaluationHeaders(input.providerId, input.credential),
        body: JSON.stringify(usesResponses ? {
          model: input.modelId,
          input: [{ role: 'user', content: [{ type: 'input_text', text: SPEED_PROMPT }] }],
          max_output_tokens: 2048,
          stream: true,
        } : {
          model: input.modelId,
          messages: [{ role: 'user', content: SPEED_PROMPT }],
          // The prompt asks for 512 output tokens. Capping at 512 left a
          // reasoning model no room to think and then answer, so it spent the
          // whole budget on `reasoning_content` and streamed no answer at all —
          // 23 paid requests to learn nothing. The headroom is for the preamble;
          // what is MEASURED is still the answer alone, because time-to-first
          // ANSWER token is the latency a caller actually waits through.
          max_tokens: 2048,
          stream: true,
          stream_options: { include_usage: true },
        }),
        signal: AbortSignal.timeout(OVERALL_SCORE_POLICY.requestTimeoutMs),
      });
      if (!response.ok || !response.body) {
        return { success: false, ttftSeconds: null, outputTokensPerSecond: null, endToEndSeconds: null, errorCode: `http_${response.status}` };
      }
      const reader = response.body.getReader();
      const decoder = new TextDecoder();
      let buffer = '';
      let content = '';
      let tokens: number | null = null;
      let firstContentAt: number | null = null;
      while (true) {
        const { value, done } = await reader.read();
        if (done) break;
        buffer += decoder.decode(value, { stream: true });
        const blocks = buffer.split(/\r?\n\r?\n/);
        buffer = blocks.pop() ?? '';
        for (const block of blocks) {
          const data = block.split(/\r?\n/).filter((line) => line.startsWith('data:'))
            .map((line) => line.slice(5).trim()).join('');
          if (!data || data === '[DONE]') continue;
          try {
            const event = JSON.parse(data) as Record<string, unknown>;
            const piece = deltaContent(event);
            if (piece && firstContentAt === null) firstContentAt = nowMs();
            content += piece;
            tokens = completionTokens(event) ?? tokens;
          } catch {
            // A malformed provider event makes this request unusable, not a model answer.
          }
        }
      }
      const ended = nowMs();
      if (firstContentAt === null || !content) {
        return { success: false, ttftSeconds: null, outputTokensPerSecond: null, endToEndSeconds: null, errorCode: 'empty_stream_content' };
      }
      const measuredTokens = tokens ?? Math.max(1, content.trim().split(/\s+/).length);
      const generationSeconds = Math.max((ended - firstContentAt) / 1000, 0.001);
      return {
        success: true,
        ttftSeconds: (firstContentAt - started) / 1000,
        outputTokensPerSecond: measuredTokens / generationSeconds,
        endToEndSeconds: (ended - started) / 1000,
        errorCode: null,
      };
    } catch (error) {
      return {
        success: false,
        ttftSeconds: null,
        outputTokensPerSecond: null,
        endToEndSeconds: null,
        errorCode: error instanceof Error && error.name === 'TimeoutError' ? 'evaluation_request_timeout' : 'network_transient',
      };
    }
  };
}
