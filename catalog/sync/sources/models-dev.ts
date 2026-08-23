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
import { normalizeId, PLAN_VARIANT } from '../identity.ts';

export const MODELS_DEV_URL = 'https://models.dev/api.json';

/**
 * Provider spellings that name a documented release under a different, exact
 * source id. These are source aliases, not fuzzy identity rules: each entry is
 * reviewed against the provider roster and the model vendor's own published
 * release date / model identifier. They let specifications travel from the
 * model's own storefront only; they do not assign quality scores.
 */
const SOURCE_MODEL_ALIASES: Readonly<Record<string, string>> = {
  'deepseek-v4-flash:preview': 'deepseek-v4-flash',
  'deepseek-v4-pro:preview': 'deepseek-v4-pro',
  'deepseek-v4-pro:0813': 'deepseek-v4-pro-0813',
  'mistral-large-3:675b': 'mistral-large-2512',
};

function sourceModelId(modelId: string): string {
  const bare = modelId.replace(/^[^/]+\//, '');
  return SOURCE_MODEL_ALIASES[bare] ?? bare;
}

function sourceKeys(modelId: string): string[] {
  const bare = modelId.replace(/^[^/]+\//, '');
  const aliased = sourceModelId(bare);
  return [...new Set([modelId, bare, aliased, normalizeId(modelId), normalizeId(bare), normalizeId(aliased)])];
}

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
  /**
   * The vendor's own lifecycle marker. `deprecated` means the provider is
   * retiring the model — it still answers, and OpenCode's own picker hides it.
   */
  status?: string;
}

/** `doc` is the provider's own documentation URL, published by the feed itself. */
type Feed = Record<string, { doc?: string; models?: Record<string, FeedModel> }>;

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
    status: m.status,
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
  intrinsic: (modelId: string, servingStorefront?: string) => IntrinsicFacts | null;
  intrinsicCount: number;
  /**
   * What the company that BUILT the model publishes about it, from its own
   * storefront in this same feed.
   *
   * The one legitimate exception to the rule above. A limit belongs to a seller
   * — but when the seller is the model's own vendor, the figure is about the
   * model rather than about somebody's deployment of it. That is the only
   * source that can answer for a model a host publishes nothing about, which is
   * the entire condition `cline-pass/glm-5.3` was stuck in.
   *
   * It returns DECLARATIONS, not an answer. Whether several vendor storefronts
   * that disagree may be adopted, and which figure wins, is a policy question
   * with real consequences for a caller, and it is settled in exactly one
   * reviewable place — `adoptFirstPartyLimit` — rather than here by whichever
   * storefront the feed happened to iterate first.
   */
  firstPartyLimits: (modelId: string) => FirstPartyLimits | null;
  /**
   * Which model a row is, independent of whether any benchmark measured it.
   *
   * The catalog treats identity and quality as separate axes, but the id a row
   * displayed came from the SCORE's `source_model_id` — so a model nobody had
   * benchmarked showed no identity at all, beside facts read from a listing of
   * that very model. This answers the identity question from the same listing.
   * It is never a canonical id for scoring: nothing is attached to it.
   */
  vendorIdentity: (modelId: string) => VendorIdentity | null;
}

/** A vendor's own storefronts in the feed, and how to tell its models apart. */
export interface Vendor {
  label: string;
  /** Feed provider keys the vendor itself operates. */
  storefronts: string[];
  /**
   * Id namespaces other sellers use for this vendor's models — `zai-org/glm-5.3`,
   * `qwen/qwen3.8-max`. Membership is READ from these rather than asserted,
   * which is what stops `alibaba/glm-5.2` (a reseller listing) from passing as
   * a first-party GLM figure just because Alibaba is a vendor of other models.
   */
  namespaces: string[];
  /**
   * The prefix this catalog canonicalises the vendor's models to.
   *
   * Declared, not derived: sellers write `zai-org/glm-5.3` while the reference
   * index writes `z-ai/...`, and for Alibaba the registry key is `alibaba` while
   * every index writes `qwen/...`. Building `${vendorId}/${slug}` would have
   * invented `alibaba/qwen3.8-max`. Each value below was read off the reference
   * index, not recalled. Absent means the vendor has not answered, and no
   * identity is produced.
   */
  canonicalPrefix?: string;
}

export type VendorRegistry = Record<string, Vendor>;

/**
 * One figure a vendor storefront published, with where to go and check it.
 *
 * `url` is the storefront's own `doc` as the feed publishes it, not a URL kept
 * in our registry — a documentation link that we maintain by hand is a claim
 * about a vendor's site that can rot without anything failing.
 */
export interface LimitDeclaration {
  value: number;
  by: string;
  url: string | null;
}

/** Which model a row IS, as established by a vendor-namespaced listing. */
export interface VendorIdentity {
  vendor: string;
  /** In the reference index's own convention, e.g. `z-ai/glm-5.3`. */
  canonicalId: string;
  /** The `provider/model` listing that placed this model with the vendor. */
  declaredBy: string;
}

export interface FirstPartyLimits {
  vendor: string;
  /** Every vendor-storefront declaration, in feed order. Never reduced here. */
  context: LimitDeclaration[];
  maxOutput: LimitDeclaration[];
}

/**
 * One field whose sellers disagreed.
 *
 * Every side is retained rather than just the field name. Knowing *that* a field
 * was disputed is enough to withhold a value, but not enough to show anyone why,
 * to audit the sources against each other, or to let a human resolve it later.
 */
/** One seller's declaration of one field, with enough context to judge standing. */
export interface Declaration {
  value: unknown;
  /** `feedKey/rawId`, the citation shown to a reader. */
  by: string;
  /** The feed provider key that published it. */
  storefront: string;
  /** The id as published, `:thinking` and all. */
  rawId: string;
}

export interface FieldConflict {
  field: string;
  /** Each distinct declared value and the `provider/model` that declared it. */
  sides: { value: unknown; by: string }[];
}

/**
 * On whose authority a disputed field was answered.
 *
 * Recorded per field so a settled dispute is auditable. `unanimous` needs no
 * authority — nobody disagreed. The other two are standings, not guesses, and
 * that distinction is the whole point: the rule this replaced refused to answer
 * at all rather than pick an ARBITRARY winner, and a named standing is not
 * arbitrary.
 */
export type FactStanding = 'unanimous' | 'serving-seller' | 'vendor-storefront';

/** Model-level properties, safe to source from any provider that declares them. */
export interface IntrinsicFacts {
  tools?: boolean;
  reasoning?: boolean;
  structured?: boolean;
  attachment?: boolean;
  inputModalities?: string[];
  /** The `provider/model` key the value came from, for provenance. */
  declaredBy: string;
  /** Fields whose sellers disagreed and nobody with standing could answer. */
  conflicts: FieldConflict[];
  /** Per field, on whose authority it was answered. Absent means not recorded. */
  standing?: Record<string, FactStanding>;
}

/**
 * Build the lookup once per run. Models are indexed both by their exact feed
 * key and by normalised id, because providers and the feed disagree on
 * separators for the same model — but normalisation never collapses size or
 * version tokens, so this cannot bind the wrong model.
 */
export async function loadSpecs(fetchJson: FetchJson, vendors: VendorRegistry = {}): Promise<SpecSource> {
  const res = await fetchJson(MODELS_DEV_URL);
  const feed = res.body as Feed;
  if (!feed || typeof feed !== 'object') throw new Error('models.dev: expected an object of providers');

  const byProvider = new Map<string, Map<string, ModelSpec>>();
  // Every declaration of every intrinsic property, kept per field rather than
  // collapsed on sight — because sellers disagree, and which one we happened to
  // read first must never be the tie-breaker.
  const pooled = new Map<string, { declarations: Record<string, Declaration[]> }>();

  // Two reverse indexes over the vendor registry, built once so the feed loop
  // stays a single pass over ~190 providers.
  const storefrontOwner = new Map<string, string>();
  const namespaceOwner = new Map<string, string>();
  for (const [vendorId, v] of Object.entries(vendors)) {
    for (const s of v.storefronts) storefrontOwner.set(s, vendorId);
    for (const n of v.namespaces) namespaceOwner.set(n.toLowerCase(), vendorId);
  }
  /** normalised bare model id -> the vendor whose namespace some seller used. */
  const memberOf = new Map<string, string>();
  /** normalised bare id -> the first listing that placed it, for the citation. */
  const declaredIn = new Map<string, { by: string; bare: string }>();
  /** normalised model id -> vendor -> that vendor's own storefront figures. */
  const fromStorefronts = new Map<string, Map<string, { context: LimitDeclaration[]; maxOutput: LimitDeclaration[] }>>();

  for (const [key, provider] of Object.entries(feed)) {
    const index = new Map<string, ModelSpec>();
    for (const [id, model] of Object.entries(provider.models ?? {})) {
      const spec = toSpec(model);
      index.set(id, spec);
      index.set(normalizeId(id), spec);

      const norm = normalizeId(id);
      const acc = pooled.get(norm) ?? { declarations: {} as Record<string, Declaration[]> };
      const declare = (field: string, value: unknown) => {
        if (value === undefined) return;
        // `storefront` and `rawId` are kept so standing can be judged later:
        // who published this, and was it the base model or one of its modes.
        (acc.declarations[field] ??= []).push({ value, by: `${key}/${id}`, storefront: key, rawId: id });
      };
      declare('tools', typeof model.tool_call === 'boolean' ? model.tool_call : undefined);
      declare('reasoning', typeof model.reasoning === 'boolean' ? model.reasoning : undefined);
      declare('structured', typeof model.structured_output === 'boolean' ? model.structured_output : undefined);
      declare('attachment', typeof model.attachment === 'boolean' ? model.attachment : undefined);
      declare('inputModalities', Array.isArray(model.modalities?.input) ? model.modalities.input : undefined);
      pooled.set(norm, acc);

      // Vendor membership, read off the feed's own namespacing: some seller
      // listing `zai-org/glm-5.3` is what establishes that glm-5.3 is a Z-AI
      // model. Recorded for EVERY provider, vendor storefront or not, because
      // it is the resellers who namespace and the vendor's own store that does
      // not.
      const ns = id.includes('/') ? id.slice(0, id.lastIndexOf('/')).toLowerCase() : null;
      if (ns && namespaceOwner.has(ns)) {
        const bareId = id.slice(id.lastIndexOf('/') + 1);
        const norm2 = normalizeId(bareId);
        memberOf.set(norm2, namespaceOwner.get(ns)!);
        // First listing wins only for the CITATION; the identity itself is
        // built from the registry's declared prefix, so which seller was read
        // first cannot change the id.
        if (!declaredIn.has(norm2)) declaredIn.set(norm2, { by: `${key}/${id}`, bare: bareId });
      }

      // And the storefront's own figures, kept per vendor rather than merged
      // into the pool — a limit still never travels between sellers.
      const vendorOfStore = storefrontOwner.get(key);
      if (vendorOfStore) {
        const store = fromStorefronts.get(norm) ?? new Map<string, { context: LimitDeclaration[]; maxOutput: LimitDeclaration[] }>();
        const slot = store.get(vendorOfStore) ?? { context: [], maxOutput: [] };
        const url = provider.doc ?? null;
        if (typeof model.limit?.context === 'number') slot.context.push({ value: model.limit.context, by: `${key}/${id}`, url });
        if (typeof model.limit?.output === 'number') slot.maxOutput.push({ value: model.limit.output, by: `${key}/${id}`, url });
        store.set(vendorOfStore, slot);
        fromStorefronts.set(norm, store);
      }
    }
    byProvider.set(key, index);
  }

  const lookup: SpecLookup = (feedKey, modelId) => {
    if (!feedKey) return null;
    const index = byProvider.get(feedKey);
    if (!index) return null;
    for (const key of sourceKeys(modelId)) {
      const match = index.get(key);
      if (match) return match;
    }
    return null;
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
  function unanimous(list: Declaration[]): Declaration | null {
    if (!list.length) return null;
    return new Set(list.map((d) => JSON.stringify(d.value))).size === 1 ? list[0] : null;
  }

  /** Did this vendor publish this listing itself, rather than resell it? */
  function isFirstParty(d: Declaration): boolean {
    const owner = storefrontOwner.get(d.storefront);
    if (!owner) return false;
    const ns = d.rawId.includes('/') ? d.rawId.slice(0, d.rawId.indexOf('/')).toLowerCase() : null;
    // The namespace guard is what stops `alibaba/glm-5.2` — Alibaba reselling a
    // Z-AI model — from answering as a first-party GLM declaration.
    return ns === null || namespaceOwner.get(ns) === owner;
  }

  function settle(
    list: Declaration[] | undefined,
    servingStorefront: string | undefined,
  ): { decl: Declaration; standing: FactStanding } | null {
    if (!list?.length) return null;
    const agreed = unanimous(list);
    if (agreed) return { decl: agreed, standing: 'unanimous' };

    // The seller whose offering this is. `reasoning` on `ollama-cloud/gemma4:31b`
    // is a fact about what ollama-cloud serves, and ollama-cloud publishes it.
    // Its own `:thinking` listing is excluded: that is a different product, and
    // pooling the two is what turned a mode difference into a disagreement.
    if (servingStorefront) {
      const own = unanimous(list.filter((d) => d.storefront === servingStorefront && !PLAN_VARIANT.test(d.rawId)));
      if (own) return { decl: own, standing: 'serving-seller' };
    }

    const firstParty = unanimous(list.filter(isFirstParty));
    if (firstParty) return { decl: firstParty, standing: 'vendor-storefront' };

    return null;
  }

  const intrinsic = (modelId: string, servingStorefront?: string): IntrinsicFacts | null => {
    const acc = sourceKeys(modelId).map((key) => pooled.get(normalizeId(key))).find(Boolean);
    if (!acc) return null;
    const out: IntrinsicFacts = { declaredBy: '', conflicts: [], standing: {} };
    for (const field of ['tools', 'reasoning', 'structured', 'attachment', 'inputModalities'] as const) {
      const list = acc.declarations[field];
      const settled = settle(list, servingStorefront);
      if (!settled) {
        // Keep one entry per DISTINCT value. Fifty sellers agreeing with each
        // other on two figures is a two-sided disagreement, and repeating each
        // side once per seller would bury that.
        if (list && list.length > 1) {
          const seen = new Map<string, { value: unknown; by: string }>();
          for (const d of list) {
            const key = JSON.stringify(d.value);
            // Projected to the published two-field shape on purpose: `storefront`
            // and `rawId` exist to judge standing, not to widen the wire format
            // every reader of a conflict already parses.
            if (!seen.has(key)) seen.set(key, { value: d.value, by: d.by });
          }
          out.conflicts.push({ field, sides: [...seen.values()] });
        }
        continue;
      }
      (out as unknown as Record<string, unknown>)[field] = settled.decl.value;
      (out.standing ??= {})[field] = settled.standing;
      if (!out.declaredBy) out.declaredBy = settled.decl.by;
    }
    return out.declaredBy || out.conflicts.length ? out : null;
  };

  const firstPartyLimits = (modelId: string): FirstPartyLimits | null => {
    const sourceId = sourceModelId(modelId);
    const bare = normalizeId(sourceId);
    const vendorId = memberOf.get(normalizeId(sourceId)) ?? memberOf.get(bare);
    if (!vendorId) return null;
    const slot = (fromStorefronts.get(normalizeId(sourceId)) ?? fromStorefronts.get(bare))?.get(vendorId);
    if (!slot || (!slot.context.length && !slot.maxOutput.length)) return null;
    return { vendor: vendorId, context: slot.context, maxOutput: slot.maxOutput };
  };

  const vendorIdentity = (modelId: string): VendorIdentity | null => {
    const sourceId = sourceModelId(modelId);
    const bare = normalizeId(sourceId);
    const vendorId = memberOf.get(normalizeId(sourceId)) ?? memberOf.get(bare);
    if (!vendorId) return null;
    const prefix = vendors[vendorId]?.canonicalPrefix;
    const listing = declaredIn.get(normalizeId(sourceId)) ?? declaredIn.get(bare);
    if (!prefix || !listing) return null;
    // Built from THIS ROW's own id, never from the listing's string. The listing
    // establishes which vendor and is cited verbatim; the row already says which
    // model. Taking the seller's spelling let a reasoning-mode variant through as
    // an identity — `zai-org/glm-5.3:thinking` became `z-ai/glm-5.3:thinking` —
    // and, more quietly, another seller's capitalisation produced
    // `moonshotai/Kimi-K2.6` beside a canonical `moonshotai/kimi-k2.6`.
    const own = sourceId.toLowerCase();
    return { vendor: vendorId, canonicalId: `${prefix}/${own}`, declaredBy: listing.by };
  };

  return { lookup, providerCount: byProvider.size, intrinsic, intrinsicCount: pooled.size, firstPartyLimits, vendorIdentity };
}
