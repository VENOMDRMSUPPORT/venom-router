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
  for (const [key, provider] of Object.entries(feed)) {
    const index = new Map<string, ModelSpec>();
    for (const [id, model] of Object.entries(provider.models ?? {})) {
      const spec = toSpec(model);
      index.set(id, spec);
      index.set(normalizeId(id), spec);
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

  return { lookup, providerCount: byProvider.size };
}
