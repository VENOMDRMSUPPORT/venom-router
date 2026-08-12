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

export type FactSource = 'models.dev' | 'openrouter' | 'provider_billing' | 'probe';

export interface ResolvedFact<T> {
  value: T;
  source: FactSource;
  /** The exact upstream id or field path the value came from. */
  ref: string;
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
 */
export type BillingModel = 'per_token' | 'subscription';

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
}

/** `structured_outputs` and friends, as OpenRouter names them. */
const PARAM_FOR: Record<string, string[]> = {
  tools: ['tools', 'tool_choice'],
  structured: ['structured_outputs', 'response_format'],
  reasoning: ['reasoning', 'include_reasoning', 'reasoning_effort'],
};

const has = <T>(v: T | null | undefined): v is T => v !== null && v !== undefined;

export interface ResolverInput {
  /** The provider's own feed entry, when models.dev carries one. */
  spec: ModelSpec | null;
  /** The canonical index entry, when identity resolved. */
  canonical: CanonicalRecord | null;
}

/** Context window, in tokens. */
export function resolveContext({ spec, canonical }: ResolverInput): ResolvedFact<number> | null {
  if (has(spec?.contextTokens)) return { value: spec.contextTokens, source: 'models.dev', ref: 'limit.context' };
  if (has(canonical?.contextLength)) return { value: canonical.contextLength, source: 'openrouter', ref: `${canonical.id}.context_length` };
  return null;
}

/**
 * Max output tokens. Resolved independently of context — they are different
 * limits, and a provider that publishes one often omits the other.
 */
export function resolveMaxOutput({ spec, canonical }: ResolverInput): ResolvedFact<number> | null {
  if (has(spec?.outputTokens)) return { value: spec.outputTokens, source: 'models.dev', ref: 'limit.output' };
  if (has(canonical?.maxCompletionTokens))
    return { value: canonical.maxCompletionTokens, source: 'openrouter', ref: `${canonical.id}.top_provider.max_completion_tokens` };
  return null;
}

export function resolveModalities({ spec, canonical }: ResolverInput): ResolvedFact<string[]> | null {
  if (has(spec?.inputModalities)) return { value: spec.inputModalities, source: 'models.dev', ref: 'modalities.input' };
  if (has(canonical?.inputModalities))
    return { value: canonical.inputModalities, source: 'openrouter', ref: `${canonical.id}.architecture.input_modalities` };
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
  field: 'tools' | 'structured' | 'reasoning',
  { spec, canonical }: ResolverInput,
): ResolvedFact<boolean> | null {
  const fromSpec = spec?.[field === 'structured' ? 'structured' : field];
  if (typeof fromSpec === 'boolean') return { value: fromSpec, source: 'models.dev', ref: field };
  const params = canonical?.supportedParameters;
  if (params && PARAM_FOR[field].some((p) => params.includes(p)))
    return { value: true, source: 'openrouter', ref: `${canonical.id}.supported_parameters` };
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
  billing: BillingModel,
): CostFact {
  const refIn = canonical?.refCostInPerM ?? null;
  const refOut = canonical?.refCostOutPerM ?? null;

  // The provider's own feed is the only source for what YOU pay.
  const inPerM = has(spec?.costInPerM) ? spec.costInPerM : null;
  const outPerM = has(spec?.costOutPerM) ? spec.costOutPerM : null;

  if (inPerM !== null || outPerM !== null) {
    const free = (inPerM ?? 0) === 0 && (outPerM ?? 0) === 0;
    return {
      kind: free ? 'free' : 'per_token',
      inPerM, outPerM, refInPerM: refIn, refOutPerM: refOut,
      source: 'models.dev', ref: 'cost',
    };
  }

  // No per-token price published. For a subscription provider that is not a
  // gap — it is the answer: the model is covered by the plan.
  if (billing === 'subscription') {
    return {
      kind: 'included',
      inPerM: null, outPerM: null, refInPerM: refIn, refOutPerM: refOut,
      source: 'provider_billing', ref: 'provider.billing=subscription',
    };
  }

  return {
    kind: 'unknown',
    inPerM: null, outPerM: null, refInPerM: refIn, refOutPerM: refOut,
    source: 'provider_billing', ref: 'no price published',
  };
}
