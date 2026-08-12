/**
 * Provider-native detail endpoints.
 *
 * The roster tells us which models exist; some providers also publish a
 * per-model detail endpoint describing how THEY serve it. That is the single
 * most authoritative source for a provider-specific limit, because it is the
 * seller describing its own offer — no index, no third party, no inference.
 *
 * Only Ollama publishes one of these unauthenticated today. The shape is
 * per-provider, so it lives with the provider rather than in a shared resolver.
 *
 * Discovered during the final gap audit: `deepseek-v4-flash:preview` appears in
 * no index anywhere — not models.dev, not OpenRouter — yet Ollama's own endpoint
 * reports its context window, parameter count and capabilities. A row we had
 * written off as unresolvable was simply never asked of its own provider.
 */

import type { FetchJson } from '../http.ts';

export interface ProviderDetail {
  contextTokens?: number;
  /** Capabilities the provider itself declares for its serving of this model. */
  tools?: boolean;
  reasoning?: boolean;
  vision?: boolean;
  parameterCount?: number;
  architecture?: string;
  /** The exact call this came from, for provenance. */
  ref: string;
}

/**
 * Ollama's capability vocabulary, as observed.
 *
 * This matters for the unknown-vs-unsupported rule. A token Ollama is KNOWN to
 * emit can be read negatively: if `vision` never appears for a model, Ollama is
 * saying it does not serve vision for it. A token it never emits at all —
 * structured output has no representation here — cannot be read negatively,
 * because its absence carries no information.
 *
 * Verified 2026-08-12 across glm-5.2, kimi-k3, deepseek-v4-pro, gpt-oss:120b and
 * qwen3.5:397b: the vocabulary is exactly {completion, tools, thinking, vision}.
 */
export const OLLAMA_CAPABILITY_VOCAB = ['completion', 'tools', 'thinking', 'vision'] as const;

/** Fields Ollama's vocabulary can express. Anything else stays unknown. */
export const OLLAMA_EXPRESSIBLE = { tools: true, reasoning: true, vision: true, structured: false };

interface ShowResponse {
  capabilities?: string[];
  details?: { family?: string; parameter_size?: string };
  model_info?: Record<string, unknown>;
}

export const OLLAMA_SHOW_URL = 'https://ollama.com/api/show';

/**
 * Read one Ollama model's own detail record.
 *
 * Returns null rather than throwing: a provider that declines to describe one
 * model must not fail the run, and a missing detail is simply one fewer source.
 */
export async function fetchOllamaDetail(
  modelId: string,
  post: (url: string, body: unknown) => Promise<unknown>,
): Promise<ProviderDetail | null> {
  let body: ShowResponse;
  try {
    body = (await post(OLLAMA_SHOW_URL, { model: modelId })) as ShowResponse;
  } catch {
    return null;
  }
  if (!body || typeof body !== 'object') return null;

  const info = body.model_info ?? {};
  const contextEntry = Object.entries(info).find(([k]) => k.endsWith('.context_length'));
  const caps = body.capabilities ?? [];
  const detail: ProviderDetail = { ref: `ollama.com/api/show(${modelId})` };

  if (typeof contextEntry?.[1] === 'number') detail.contextTokens = contextEntry[1];
  const params = info['general.parameter_count'];
  if (typeof params === 'number') detail.parameterCount = params;
  const arch = info['general.architecture'] ?? body.details?.family;
  if (typeof arch === 'string' && arch) detail.architecture = arch;

  // A capability in the vocabulary can be read both ways; one outside it cannot
  // be read at all. `structured` is deliberately never set from here.
  if (caps.length) {
    detail.tools = caps.includes('tools');
    detail.reasoning = caps.includes('thinking');
    detail.vision = caps.includes('vision');
  }
  return detail;
}

/** Providers with a usable detail endpoint, keyed by provider id. */
export const DETAIL_FETCHERS: Record<
  string,
  (modelId: string, post: (url: string, body: unknown) => Promise<unknown>) => Promise<ProviderDetail | null>
> = {
  'ollama-cloud': fetchOllamaDetail,
};

/** POST helper shaped like the rest of the fetch discipline. */
export function makePost(fetchJson: FetchJson): (url: string, body: unknown) => Promise<unknown> {
  return async (url, body) => {
    const res = await fetch(url, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify(body),
      signal: AbortSignal.timeout(20_000),
    });
    if (!res.ok) throw new Error(`${url} -> HTTP ${res.status}`);
    return res.json();
  };
}
