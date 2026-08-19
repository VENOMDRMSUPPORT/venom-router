import { OVERALL_SCORE_POLICY } from './score.ts';
import type { SpeedProbe } from './speed-runner.ts';
import { evaluationHeaders } from './provider-transport.ts';

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
  const choices = event.choices;
  if (!Array.isArray(choices) || typeof choices[0] !== 'object' || choices[0] === null) return '';
  const delta = (choices[0] as Record<string, unknown>).delta;
  if (typeof delta !== 'object' || delta === null) return '';
  const content = (delta as Record<string, unknown>).content;
  return typeof content === 'string' ? content : '';
}

export function createStreamingSpeedProbe(input: CreateStreamingSpeedProbeInput): SpeedProbe {
  const baseUrl = PROVIDER_BASE_URLS[input.providerId];
  if (!baseUrl) throw new Error(`unsupported_evaluation_provider:${input.providerId}`);
  const fetchImpl = input.fetchImpl ?? fetch;
  const nowMs = input.nowMs ?? (() => performance.now());

  return async () => {
    const started = nowMs();
    try {
      const response = await fetchImpl(`${baseUrl}/chat/completions`, {
        method: 'POST',
        headers: evaluationHeaders(input.providerId, input.credential),
        body: JSON.stringify({
          model: input.modelId,
          messages: [{ role: 'user', content: 'Output exactly 512 space-separated copies of the lowercase token catalog. Do not add punctuation or explanation.' }],
          temperature: 0,
          max_tokens: 512,
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
