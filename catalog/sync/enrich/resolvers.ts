/**
 * Operational metadata resolution — one resolver per field.
 *
 * Metadata is NOT fetched per model as a block. Each field has its own ordered
 * source list and its own provenance, because in practice one model's context
 * comes from the provider feed while its capability flags come from a second
 * index and its price semantics from the provider's billing model. Treating
 * metadata as one blob means the weakest source drags the whole row down.
 *
 * The rules that make this safe are the same ones that govern the score:
 *
 *   - A source is consulted only for a field it actually publishes. Nothing is
 *     inferred from a sibling model, a name, or a size.
 *   - `unknown` never becomes `unsupported`. A capability the sources do not
 *     mention stays null; it is not reported as absent.
 *   - Every resolved value records which source produced it.
 *
 * Sources are ordered by how close they are to the thing being described:
 * the provider's own feed first, then the canonical model index. An active
 * probe would sit last and is deliberately not implemented here — see M5.
 */

import type { ModelSpec } from '../engine.ts';
import type { FirstPartyLimits, LimitDeclaration } from '../sources/models-dev.ts';
import type { ProviderDetail } from '../sources/provider-detail.ts';
// The pooling code owns this shape, because it is the thing that builds it.
// A second, structurally-compatible copy used to live here, which TypeScript
// accepted silently and which would have drifted the first time either changed.
import type { IntrinsicFacts } from '../sources/models-dev.ts';

export type { IntrinsicFacts };

export type FactSource = 'provider_api' | 'models.dev' | 'openrouter' | 'provider_billing' | 'probe'
  /** This catalog graded the capability against the provider itself. */
  | 'catalog_measurement';

/**
 * How strong a fact's evidence is — a question `source` cannot answer.
 *
 * A limit the seller published about its own serving and a capability a
 * different seller declared about the same model both arrive labelled
 * `models.dev`, and they are not equally authoritative. Without this, a consumer
 * would have to re-derive the difference from `source_ref` string shapes.
 */
export type EvidenceState =
  | 'first_party'
  | 'vendor_default'
  | 'pooled_third_party'
  | 'index_confirmation'
  | 'declared_policy'
  | 'measured';

/** Where each fixed-address source lives, for provenance. */
export const SOURCE_URL = {
  'models.dev': 'https://models.dev/api.json',
  openrouter: 'https://openrouter.ai/api/v1/models',
} as const;

export interface ResolvedFact<T> {
  value: T;
  source: FactSource;
  /** The exact upstream id or field path the value came from. */
  ref: string;
  /** Exactly where it was read from, so a reader can check it without our code. */
  url: string | null;
  state: EvidenceState;
  /** The untransformed upstream figure, kept so the value can be re-derived. */
  raw: unknown;
}

/** What a canonical model index can tell us. Currently OpenRouter. */
export interface CanonicalRecord {
  id: string;
  contextLength?: number;
  maxCompletionTokens?: number;
  inputModalities?: string[];
  /** Names of parameters the endpoint accepts, e.g. `tools`, `structured_outputs`. */
  supportedParameters?: string[];
  /** List price per million tokens, at the index — NOT at our provider. */
  refCostInPerM?: number;
  refCostOutPerM?: number;
}

/**
 * How a provider charges. Declared per provider because it is a fact about the
 * business model, not about any model — no feed publishes it and no probe can
 * discover it.
 *
 * `free_quota` is a third, genuinely distinct model: the provider charges
 * nothing per token and gates use by a usage quota (volume/concurrency) instead.
 * A missing price there is neither a gap (`per_token` → unknown) nor a plan
 * covering the model (`subscription` → included) — it is the answer `free`.
 * Crucially it produces NO numeric price: the KIND carries the answer while the
 * effective-price fields stay null, so nothing derived is ever mistaken for a
 * first-party feed figure. Measured 2026-08-13: Ollama Cloud, whose models carry
 * no per-token cost in models.dev and whose pricing page gates plans by usage.
 */
export type BillingModel = 'per_token' | 'subscription' | 'free_quota';

/**
 * A provider's billing model together with the evidence for it.
 *
 * The model alone used to be enough, with the reasoning kept in a source
 * comment. That made the one fact in the catalog whose provenance could not be
 * queried — and it is a fact that decides whether a missing price reads as
 * `included` or as `unknown`, which is not a small call.
 */
export interface BillingPolicy {
  model: BillingModel;
  /** The page this was read from. Cited so a reader can re-verify it. */
  evidenceUrl: string;
  /** What that page says, in one line. Becomes the fact's `source_ref`. */
  note: string;
}

/** Cost semantics. Every row gets one; `unknown` is a real, honest state. */
export type CostKind = 'free' | 'included' | 'per_token' | 'unknown';

export interface CostFact {
  kind: CostKind;
  /** Effective price at this provider, per million tokens. Null unless per_token. */
  inPerM: number | null;
  outPerM: number | null;
  /** List price elsewhere, for comparison only. Never shown as your cost. */
  refInPerM: number | null;
  refOutPerM: number | null;
  source: FactSource;
  ref: string;
  /**
   * Provenance for the BILLING KIND specifically.
   *
   * Cost is three facts with three sources: which billing applies, what you pay
   * here, and what the model lists for elsewhere. Recorded as one blob under a
   * single source label, the reference price inherited the effective price's
   * provenance — a market rate wearing this provider's label.
   */
  url: string | null;
  state: EvidenceState;
}

/** `structured_outputs` and friends, as OpenRouter names them. */
const PARAM_FOR: Record<string, string[]> = {
  tools: ['tools', 'tool_choice'],
  structured: ['structured_outputs', 'response_format'],
  reasoning: ['reasoning', 'include_reasoning', 'reasoning_effort'],
};

const has = <T>(v: T | null | undefined): v is T => v !== null && v !== undefined;

export interface ResolverInput {
  /**
   * The provider's OWN detail record, when it publishes one.
   *
   * Ranked first: this is the seller describing its own offer, which no index
   * can outrank for a provider-specific limit.
   */
  detail?: ProviderDetail | null;
  /** The provider's own feed entry, when models.dev carries one. */
  spec: ModelSpec | null;
  /**
   * The same model as declared by ANY provider in the spec feed.
   *
   * Consulted only for properties intrinsic to the model. Serving limits and
   * price never come from here — those belong to a seller, not to the model.
   */
  intrinsic: IntrinsicFacts | null;
  /** The canonical index entry, when identity resolved. */
  canonical: CanonicalRecord | null;
  /**
   * What the model's own vendor publishes, from its own storefront.
   *
   * Ranked LAST among sources for a limit, and only reached when no seller of
   * this deployment published one. It answers a different question from the
   * others — what the model supports, rather than what this host serves — which
   * is why it carries its own evidence state instead of passing as first party.
   */
  firstParty?: FirstPartyLimits | null;
  /**
   * What this catalog measured against the provider itself.
   *
   * Ranked above every third-party declaration and below the seller's own detail
   * record, because it answers a different question from either: not what
   * somebody says the model supports, but what it did when asked. Keyed by
   * capability, and only the three that have a graded dimension appear —
   * `attachment` has none, so nothing here can speak for it.
   */
  measured?: Partial<Record<'tools' | 'reasoning' | 'structured', MeasuredCapability | null>> | null;
}

/** One graded dimension, reduced to what a capability question needs. */
export interface MeasuredCapability {
  score: number;
  /** `run:<id>`, so the row cites the evaluation it came from. */
  runRef: string;
  sampleCount: number;
}

/**
 * Adopt a figure from the model vendor's own storefronts, or refuse.
 *
 * Reached only when nobody who actually SERVES the model published the limit —
 * the host's API says nothing and the feed has no entry under the host's key.
 * The declarations here come from the company that built the model, selling it
 * itself, so the figure is about the model rather than about a deployment.
 *
 * Which is also the catch, and the reason this is a decision and not a lookup:
 * the vendor's number is what the model *supports*, and a host is free to serve
 * it with less. Adopting one means publishing a ceiling as though it were this
 * host's setting. Returning null keeps the row in "needs verification", where it
 * is honest but unusable.
 *
 * @param declarations every vendor-storefront figure for this model, in feed
 *   order, each with the `provider/model` that published it. Never empty.
 * @returns the figure to adopt with the storefront that published it, or null to
 *   refuse — which leaves the fact missing rather than guessed.
 */
export function adoptFirstPartyLimit(declarations: LimitDeclaration[]): LimitDeclaration | null {
  // Unanimity, matching `settle()` in the pool above: a source that contradicts
  // itself has not published a fact. Owner decision, 2026-08-18 — the two
  // alternatives were rejected for the same reason. Taking the smaller figure
  // is safe at runtime but publishes a number no storefront stated, and a
  // majority counts shopfronts rather than evidence.
  if (!declarations.length) return null;
  const distinct = new Set(declarations.map((d) => d.value));
  return distinct.size === 1 ? declarations[0] : null;
}

/** Context window, in tokens. */
export function resolveContext({ detail, spec, firstParty }: ResolverInput): ResolvedFact<number> | null {
  // The provider's own answer about its own serving comes first.
  if (has(detail?.contextTokens))
    return { value: detail.contextTokens, source: 'provider_api', ref: detail.ref, url: detail.url, state: 'first_party', raw: detail.contextTokens };
  if (has(spec?.contextTokens))
    return { value: spec.contextTokens, source: 'models.dev', ref: 'limit.context', url: SOURCE_URL['models.dev'], state: 'first_party', raw: spec.contextTokens };
  // Still no index fallback. A context window is what THIS provider serves, and
  // OpenRouter reports what ITS providers serve. Measured proof they differ:
  // Ollama serves nemotron-3-ultra at 262144 while the model supports 512288.
  // A limit from one seller wearing another seller's label is the same class
  // of error as attaching one model's benchmark to another.
  //
  // The vendor's own figure is not that error, and is the one thing left to
  // ask: it is not another seller's deployment, it is the model's own ceiling,
  // published by the company that set it. Kept behind its own evidence state so
  // a reader is never told this host confirmed it.
  return vendorLimit(firstParty?.context, 'limit.context');
}

/** Shared by both limits, so their ranking cannot drift apart. */
function vendorLimit(declarations: LimitDeclaration[] | undefined, field: string): ResolvedFact<number> | null {
  const adopted = adoptFirstPartyLimit(declarations ?? []);
  if (!adopted) return null;
  return {
    value: adopted.value,
    source: 'models.dev',
    ref: `${adopted.by}.${field}`,
    url: adopted.url,
    state: 'vendor_default',
    raw: adopted.value,
  };
}

/**
 * Max output tokens. Resolved independently of context — they are different
 * limits, and a provider that publishes one often omits the other.
 */
export function resolveMaxOutput({ spec, firstParty }: ResolverInput): ResolvedFact<number> | null {
  if (has(spec?.outputTokens))
    return { value: spec.outputTokens, source: 'models.dev', ref: 'limit.output', url: SOURCE_URL['models.dev'], state: 'first_party', raw: spec.outputTokens };
  // Still no index fallback, for the same reason as context — and here the
  // upstream field says so itself: `top_provider.max_completion_tokens` is the
  // cap of whichever provider OpenRouter ranks first. That is a different
  // seller. The vendor's own ceiling is not, and is asked last.
  return vendorLimit(firstParty?.maxOutput, 'limit.output');
}

export function resolveModalities({ detail, spec, intrinsic, canonical }: ResolverInput): ResolvedFact<string[]> | null {
  // Ollama's vocabulary expresses vision, so it can state modality both ways.
  if (detail && typeof detail.vision === 'boolean')
    return { value: detail.vision ? ['text', 'image'] : ['text'], source: 'provider_api', ref: detail.ref, url: detail.url, state: 'first_party', raw: detail.vision };
  if (has(spec?.inputModalities))
    return { value: spec.inputModalities, source: 'models.dev', ref: 'modalities.input', url: SOURCE_URL['models.dev'], state: 'first_party', raw: spec.inputModalities };
  if (has(intrinsic?.inputModalities))
    return { value: intrinsic.inputModalities, source: 'models.dev', ref: `${intrinsic.declaredBy}.modalities.input`, url: SOURCE_URL['models.dev'], state: 'pooled_third_party', raw: intrinsic.inputModalities };
  // Unlike a serving limit, which modalities a model accepts is a property of
  // the model itself, so a canonical index may legitimately answer for it.
  if (has(canonical?.inputModalities))
    return { value: canonical.inputModalities, source: 'openrouter', ref: `${canonical.id}.architecture.input_modalities`, url: SOURCE_URL.openrouter, state: 'index_confirmation', raw: canonical.inputModalities };
  return null;
}

/**
 * A capability flag.
 *
 * The second source can only ever say *yes*: OpenRouter's `supported_parameters`
 * lists what an endpoint accepts, and a parameter being absent from that list is
 * not a statement that the model lacks the capability — it may simply not be
 * exposed there. So a miss returns null (unknown), never false.
 */
export function resolveCapability(
  field: 'tools' | 'structured' | 'reasoning' | 'attachment',
  { detail, spec, intrinsic, canonical, measured }: ResolverInput,
): ResolvedFact<boolean> | null {
  // A provider-declared capability outranks every index — but only for the two
  // fields a detail vocabulary actually expresses. Structured output and file
  // attachments have no representation there, so silence about them carries no
  // information and the record is not consulted. See OLLAMA_EXPRESSIBLE.
  const fromDetail = field === 'tools' || field === 'reasoning' ? detail?.[field] : undefined;
  if (typeof fromDetail === 'boolean')
    return { value: fromDetail, source: 'provider_api', ref: detail!.ref, url: detail!.url, state: 'first_party', raw: fromDetail };
  /*
   * A dimension this catalog graded against the provider.
   *
   * One-directional on purpose: a PASSING score proves the capability exists —
   * nothing scores 99.7 on structured output without supporting it. A zero or a
   * missing score proves nothing, because a failure need not be about the model
   * at all. Both failure modes are on record here: an evaluation whose every
   * sample returned 429, and one refused for an unsupported `temperature`
   * parameter before the model ever saw the request.
   *
   * Placed above the declarations because it is evidence rather than assertion,
   * and below `detail` because the seller describes what THIS deployment
   * exposes, which is a different question from what the model can do.
   */
  const proven = field === 'attachment' ? null : measured?.[field];
  if (proven && proven.score > 0) {
    return {
      value: true,
      source: 'catalog_measurement',
      ref: `${field}.${proven.runRef}`,
      url: null,
      state: 'measured',
      raw: { score: proven.score, sampleCount: proven.sampleCount, runRef: proven.runRef },
    };
  }
  const fromSpec = spec?.[field];
  if (typeof fromSpec === 'boolean')
    return { value: fromSpec, source: 'models.dev', ref: field, url: SOURCE_URL['models.dev'], state: 'first_party', raw: fromSpec };
  // Another seller's declaration about the SAME model. Legitimate for a
  // capability, which belongs to the model, and it can say no as well as yes.
  const fromPool = intrinsic?.[field];
  if (typeof fromPool === 'boolean')
    return { value: fromPool, source: 'models.dev', ref: `${intrinsic!.declaredBy}.${field}`, url: SOURCE_URL['models.dev'], state: 'pooled_third_party', raw: fromPool };
  // The canonical index can only ever CONFIRM, and only for a field whose
  // acceptance it actually reports. `attachment` has no entry in PARAM_FOR on
  // purpose: an input-modality list is a different question, and reading
  // "accepts images" as "accepts attachments" would be an inference, not a fact.
  const params = canonical?.supportedParameters;
  const names = PARAM_FOR[field];
  if (params && names?.some((p) => params.includes(p)))
    return { value: true, source: 'openrouter', ref: `${canonical.id}.supported_parameters`, url: SOURCE_URL.openrouter, state: 'index_confirmation', raw: params };
  return null;
}

/**
 * Cost semantics.
 *
 * The distinction this exists to protect: a *reference* price is what the model
 * lists for elsewhere; an *effective* price is what this provider charges you.
 * A subscription provider's model does not cost you the market rate, and writing
 * the market rate into the effective field would be a number from one seller
 * wearing another seller's label — the same class of error as attaching one
 * model's benchmark to another.
 */
export function resolveCost(
  { spec, canonical }: ResolverInput,
  billing: BillingPolicy,
): CostFact {
  const refIn = canonical?.refCostInPerM ?? null;
  const refOut = canonical?.refCostOutPerM ?? null;
  const feedIn = has(spec?.costInPerM) ? spec.costInPerM : null;
  const feedOut = has(spec?.costOutPerM) ? spec.costOutPerM : null;

  // How a provider bills is a fact about the PROVIDER, so it is settled before
  // any per-row figure is read.
  //
  // This used to run the other way: whichever rows the feed happened to price
  // became `per_token` and the rest fell through to the declared policy, so one
  // provider's table split into "costs $1.40" and "included" — with the two
  // halves scored on different bases, cost renormalised out of one and not the
  // other, while sitting in the same ranking. The split tracked models.dev's
  // coverage, nothing about the world.
  //
  // ClinePass is the case that proved which way round it goes. Its own docs:
  // "ClinePass is a flat monthly subscription, so you are not charged the
  // individual API prices below. These reference prices show the underlying
  // per-1M-token rates for each model." The feed publishes exactly that table,
  // and the catalog was putting it in the EFFECTIVE column — the one claim the
  // provider explicitly denies. Under a plan the published rate is a reference,
  // which is the field it now fills.
  if (billing.model === 'subscription' || billing.model === 'free_quota') {
    return {
      kind: billing.model === 'subscription' ? 'included' : 'free',
      // No numeric effective price, deliberately. Writing a derived 0 or a
      // reference figure here would make the next enrich pass re-read it from
      // the models table as a first-party models.dev price — a declared policy
      // laundered into a feed figure.
      inPerM: null,
      outPerM: null,
      // The provider's own published rate is a better reference than the vendor
      // list price, and it is the one the plan meters against.
      refInPerM: feedIn ?? refIn,
      refOutPerM: feedOut ?? refOut,
      // A business fact no feed publishes, so it is declared by hand — and the
      // declaration cites the page it was read from, like any other fact.
      source: 'provider_billing', ref: billing.note,
      url: billing.evidenceUrl, state: 'declared_policy',
    };
  }

  // A per-token provider: the feed price is what you actually pay.
  if (feedIn !== null || feedOut !== null) {
    const free = (feedIn ?? 0) === 0 && (feedOut ?? 0) === 0;
    return {
      kind: free ? 'free' : 'per_token',
      inPerM: feedIn, outPerM: feedOut, refInPerM: refIn, refOutPerM: refOut,
      source: 'models.dev', ref: 'cost',
      url: SOURCE_URL['models.dev'], state: 'first_party',
    };
  }

  return {
    kind: 'unknown',
    inPerM: null, outPerM: null, refInPerM: refIn, refOutPerM: refOut,
    source: 'provider_billing', ref: 'no price published',
    url: billing.evidenceUrl, state: 'declared_policy',
  };
}
