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
  /** Fields whose sellers disagreed; deliberately left unresolved. */
  conflicts: string[];
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
  // Every declaration of every intrinsic property, kept per field rather than
  // collapsed on sight — because sellers disagree, and which one we happened to
  // read first must never be the tie-breaker.
  const pooled = new Map<string, { declarations: Record<string, { value: unknown; by: string }[]> }>();

  for (const [key, provider] of Object.entries(feed)) {
    const index = new Map<string, ModelSpec>();
    for (const [id, model] of Object.entries(provider.models ?? {})) {
      const spec = toSpec(model);
      index.set(id, spec);
      index.set(normalizeId(id), spec);

      const norm = normalizeId(id);
      const acc = pooled.get(norm) ?? { declarations: {} as Record<string, { value: unknown; by: string }[]> };
      const declare = (field: string, value: unknown) => {
        if (value === undefined) return;
        (acc.declarations[field] ??= []).push({ value, by: `${key}/${id}` });
      };
      declare('tools', typeof model.tool_call === 'boolean' ? model.tool_call : undefined);
      declare('reasoning', typeof model.reasoning === 'boolean' ? model.reasoning : undefined);
      declare('structured', typeof model.structured_output === 'boolean' ? model.structured_output : undefined);
      declare('attachment', typeof model.attachment === 'boolean' ? model.attachment : undefined);
      declare('inputModalities', Array.isArray(model.modalities?.input) ? model.modalities.input : undefined);
      pooled.set(norm, acc);
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

  /**
   * Reduce a field's declarations to a single answer.
   *
   * Unanimous declarations are taken. A field whose sellers DISAGREE resolves to
   * unknown, not to a majority and not to the first one read.
   *
   * Measured 2026-08-12: hy3-preview is declared structured_output=true by
   * aihubmix and false by both kilo and openrouter. Taking the first hit made
   * the answer depend on object iteration order — a value that looks confident,
   * has no trace of the disagreement, and differs from a correct one only by
   * chance. `docs/catalog-data-sources.md` already names this failure: a quietly
   * picked winner is indistinguishable from a bug.
   */
  function settle(list: { value: unknown; by: string }[] | undefined): { value: unknown; by: string } | null {
    if (!list?.length) return null;
    const distinct = new Set(list.map((d) => JSON.stringify(d.value)));
    if (distinct.size > 1) return null;
    return list[0];
  }

  const intrinsic = (modelId: string): IntrinsicFacts | null => {
    const bare = modelId.replace(/^[^/]+\//, '');
    const acc = pooled.get(normalizeId(modelId)) ?? pooled.get(normalizeId(bare));
    if (!acc) return null;
    const out: IntrinsicFacts = { declaredBy: '', conflicts: [] };
    for (const field of ['tools', 'reasoning', 'structured', 'attachment', 'inputModalities'] as const) {
      const list = acc.declarations[field];
      const settled = settle(list);
      if (!settled) {
        if (list && list.length > 1) out.conflicts.push(field);
        continue;
      }
      (out as Record<string, unknown>)[field] = settled.value;
      if (!out.declaredBy) out.declaredBy = settled.by;
    }
    return out.declaredBy || out.conflicts.length ? out : null;
  };

  return { lookup, providerCount: byProvider.size, intrinsic, intrinsicCount: pooled.size };
}
