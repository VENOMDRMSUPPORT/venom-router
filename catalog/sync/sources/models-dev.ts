/**
 * models.dev — the spec source.
 *
 * Answers "what is this model" (context, output, modalities, capabilities,
 * cost) for 183 providers. It carries no benchmark of any kind — verified
 * across every model of every provider on 2026-08-12 — so it is never asked for
 * one.
 *
 * It is also NOT the roster: it lags providers in both directions. On the day
 * this was written it listed 26 OpenCode Zen models the provider no longer
 * served, and was missing models the live rosters carried.
 */

import type { FetchJson } from '../http.ts';
import type { ModelSpec, SpecLookup } from '../engine.ts';
import { normalizeId } from '../identity.ts';

export const MODELS_DEV_URL = 'https://models.dev/api.json';

interface FeedModel {
  id: string;
  name?: string;
  attachment?: boolean;
  reasoning?: boolean;
  tool_call?: boolean;
  structured_output?: boolean;
  modalities?: { input?: string[] };
  limit?: { context?: number; output?: number };
  cost?: { input?: number; output?: number };
}

type Feed = Record<string, { models?: Record<string, FeedModel> }>;

function toSpec(m: FeedModel): ModelSpec {
  return {
    displayName: m.name,
    contextTokens: m.limit?.context,
    outputTokens: m.limit?.output,
    inputModalities: m.modalities?.input,
    tools: m.tool_call,
    reasoning: m.reasoning,
    structured: m.structured_output,
    attachment: m.attachment,
    costInPerM: m.cost?.input,
    costOutPerM: m.cost?.output,
  };
}

export interface SpecSource {
  lookup: SpecLookup;
  providerCount: number;
  /**
   * Intrinsic model properties declared by ANY provider in the feed, keyed by
   * normalised model id.
   *
   * Only properties that belong to the model itself travel this way —
   * `structured_output`, `tool_call`, `reasoning` and input modalities. A
   * different seller's declaration is legitimate evidence about the same model.
   *
   * Serving limits and price deliberately do NOT: `limit.context`,
   * `limit.output` and `cost` describe what one seller offers, not what the
   * model is. Measured proof that this distinction is real — Ollama serves
   * nemotron-3-ultra at 262144 while the model itself supports 512288. Copying
   * another provider's ceiling in would be one seller's number wearing another
   * seller's label, the same error the pricing split exists to prevent.
   */
  intrinsic: (modelId: string) => IntrinsicFacts | null;
  intrinsicCount: number;
}

/** Model-level properties, safe to source from any provider that declares them. */
export interface IntrinsicFacts {
  tools?: boolean;
  reasoning?: boolean;
  structured?: boolean;
  attachment?: boolean;
  inputModalities?: string[];
  /** The `provider/model` key the value came from, for provenance. */
  declaredBy: string;
}

/**
 * Build the lookup once per run. Models are indexed both by their exact feed
 * key and by normalised id, because providers and the feed disagree on
 * separators for the same model — but normalisation never collapses size or
 * version tokens, so this cannot bind the wrong model.
 */
export async function loadSpecs(fetchJson: FetchJson): Promise<SpecSource> {
  const res = await fetchJson(MODELS_DEV_URL);
  const feed = res.body as Feed;
  if (!feed || typeof feed !== 'object') throw new Error('models.dev: expected an object of providers');

  const byProvider = new Map<string, Map<string, ModelSpec>>();
  // Intrinsic properties pooled across every provider. The first declaration
  // wins per field, so a model is not left unknown merely because the provider
  // we buy it from chose not to publish a flag another seller did.
  const intrinsicByModel = new Map<string, IntrinsicFacts>();

  for (const [key, provider] of Object.entries(feed)) {
    const index = new Map<string, ModelSpec>();
    for (const [id, model] of Object.entries(provider.models ?? {})) {
      const spec = toSpec(model);
      index.set(id, spec);
      index.set(normalizeId(id), spec);

      const norm = normalizeId(id);
      const existing = intrinsicByModel.get(norm) ?? { declaredBy: `${key}/${id}` };
      if (existing.tools === undefined && typeof model.tool_call === 'boolean') existing.tools = model.tool_call;
      if (existing.reasoning === undefined && typeof model.reasoning === 'boolean') existing.reasoning = model.reasoning;
      if (existing.structured === undefined && typeof model.structured_output === 'boolean') existing.structured = model.structured_output;
      if (existing.attachment === undefined && typeof model.attachment === 'boolean') existing.attachment = model.attachment;
      if (existing.inputModalities === undefined && Array.isArray(model.modalities?.input)) existing.inputModalities = model.modalities.input;
      intrinsicByModel.set(norm, existing);
    }
    byProvider.set(key, index);
  }

  const lookup: SpecLookup = (feedKey, modelId) => {
    if (!feedKey) return null;
    const index = byProvider.get(feedKey);
    if (!index) return null;
    const bare = modelId.replace(/^[^/]+\//, '');
    return index.get(modelId) ?? index.get(bare) ?? index.get(normalizeId(modelId)) ?? null;
  };

  const intrinsic = (modelId: string): IntrinsicFacts | null => {
    const bare = modelId.replace(/^[^/]+\//, '');
    return intrinsicByModel.get(normalizeId(modelId)) ?? intrinsicByModel.get(normalizeId(bare)) ?? null;
  };

  return { lookup, providerCount: byProvider.size, intrinsic, intrinsicCount: intrinsicByModel.size };
}
