/**
 * Venom Score — two independently published base scores.
 *
 * VQ (quality) and VO (operational) have different coverage and different
 * evidence strength. Fusing them would let a cheap long-context model
 * impersonate an intelligent one on every row where quality is unknown, and the
 * reader could not tell which had happened. So both are always carried, and the
 * evidence level travels with the value. The separate `model-score-v1` policy
 * may derive a composite only when both base scores exist.
 *
 * Weights are policy, not invention: they live in a versioned profile that is
 * committed, reviewable and selectable. The failure to avoid is not *having*
 * weights — it is having undeclared ones.
 */

import type { IdentityRule, Resolution } from '../identity.ts';
// Type-only, so no runtime coupling. Imported rather than redeclared: a second
// copy of the cost vocabulary is the duplication that let IntrinsicFacts drift.
import type { CostKind } from '../enrich/resolvers.ts';
import { applyCalibration, isAcceptable, type Calibration } from './calibration.ts';

export type EvidenceLevel = 'measured' | 'calibrated' | 'bounded' | 'unrated';

/**
 * Why a model has no quality score.
 *
 * Recorded because "unrated" alone is not accountable. These are four different
 * situations needing four different kinds of work, and a dash that cannot say
 * which one it is looks identical to a dash caused by not having looked:
 *
 *   identity_unresolved         nothing upstream matched; more benchmarks cannot help
 *   identity_ambiguous          candidates exist and a human must choose one
 *   no_published_benchmark      identity is proven, nobody has measured the model
 *   calibration_group_excluded  a figure exists, and the calibration was MEASURED
 *                               to have no predictive power for this vendor, so
 *                               mapping it would publish a number known to be wrong
 */
export type UnratedReason =
  | 'identity_unresolved'
  | 'identity_ambiguous'
  | 'no_published_benchmark'
  | 'calibration_group_excluded';

export interface QualityEvidence {
  /** Direct measurement on the target scale, e.g. AA intelligence_index. */
  direct?: number;
  /** A value on a second scale that calibration can map, e.g. design_arena Elo. */
  calibratable?: number;
  /**
   * The group the calibration reasons about (the upstream vendor). Decides both
   * whether calibration is permitted for this model at all and which measured
   * error applies to it.
   */
  group?: string;
  /**
   * A one-sided bound justified by a reviewed relation to a measured model,
   * e.g. a "pro" tier whose base is measured. Never inferred automatically.
   */
  bound?: { value: number; side: 'lower' | 'upper'; reason: string };
}

export interface VQ {
  kind: 'VQ';
  value: number | null;
  uncertainty: number | null;
  bound: 'lower' | 'upper' | null;
  level: EvidenceLevel;
  source: string | null;
  sourceModelId: string | null;
  identityRule: IdentityRule | null;
  /** The untransformed upstream figure this value was derived from. */
  rawValue: number | null;
  /** Which upstream field `rawValue` came from. */
  rawField: string | null;
  /** Why this row has no score. Null whenever it has one. */
  unratedReason: UnratedReason | null;
  /** How `rawValue` became `value`. 'identity' when it was used unchanged. */
  transformation: string | null;
  /** Decimals this value has earned. Precision follows evidence, never taste. */
  precision: number;
}

const UNRATED: VQ = {
  kind: 'VQ',
  value: null,
  uncertainty: null,
  bound: null,
  level: 'unrated',
  source: null,
  sourceModelId: null,
  identityRule: null,
  rawValue: null,
  rawField: null,
  transformation: null,
  unratedReason: null,
  precision: 0,
};

/**
 * Compute VQ from a resolved identity and whatever evidence that identity
 * carries.
 *
 * An unresolved or ambiguous identity yields `unrated` — never a value. That is
 * the whole point: a model whose identity is uncertain has no score, because a
 * score attached to the wrong model is indistinguishable from a right one.
 */
/** How many decimals a figure actually carries. Caps at 2: no source publishes more. */
function decimalsOf(value: number): number {
  const text = String(value);
  const dot = text.indexOf('.');
  return dot === -1 ? 0 : Math.min(2, text.length - dot - 1);
}

export function computeVQ(
  resolution: Resolution,
  evidence: QualityEvidence,
  calibration: Calibration | null,
): VQ {
  const base = {
    kind: 'VQ' as const,
    sourceModelId: resolution.status === 'resolved' ? resolution.target : null,
    identityRule: resolution.status === 'resolved' ? resolution.rule : null,
    bound: null,
  };

  // Everything DERIVED from an identity is gated on that identity being
  // settled. A reviewed bound is not derived from one — it is a human's claim
  // about this row, made because no index carries the model — so it is checked
  // after these and reached whether or not the identity resolved. It is the
  // only path here that an unresolved row can take.
  if (resolution.status === 'resolved' && typeof evidence.direct === 'number') {
    return {
      ...base,
      value: evidence.direct,
      // AA publishes one decimal; treat that as the resolution of the figure,
      // not as proof of perfect accuracy.
      uncertainty: 0.05,
      level: 'measured',
      source: 'artificial_analysis',
      rawValue: evidence.direct,
      rawField: 'benchmarks.artificial_analysis.intelligence_index',
      transformation: 'identity',
      unratedReason: null,
      precision: 1,
    };
  }

  if (resolution.status === 'resolved' && typeof evidence.calibratable === 'number' && isAcceptable(calibration)) {
    // Returns null for a group the calibration was measured to be biased on;
    // that model falls through to `unrated` rather than taking a value the
    // evidence says would be wrong.
    const applied = applyCalibration(calibration!, evidence.calibratable, evidence.group);
    if (applied) {
      return {
        ...base,
        value: applied.value,
        uncertainty: applied.uncertainty,
        level: 'calibrated',
        source: 'design_arena',
        rawValue: evidence.calibratable,
        rawField: 'benchmarks.design_arena[arena=models].elo (mean)',
        transformation: `y = ${calibration!.slope} * x + ${calibration!.intercept}`,
        unratedReason: null,
        // +/- 5-8 points of uncertainty cannot justify a decimal place.
        precision: 0,
      };
    }
  }

  if (evidence.bound) {
    return {
      ...base,
      // Explicitly NOT the reference model. `read-model.ts` derives
      // `canonicalId` from this field, so naming glm-5.2 here would make the row
      // assert that it IS glm-5.2 — the bind the bound exists to avoid making.
      // The reference lives in `source`, where a reader reads it as a relation.
      sourceModelId: null,
      identityRule: null,
      value: evidence.bound.value,
      uncertainty: null,
      bound: evidence.bound.side,
      level: 'bounded',
      source: `relation: ${evidence.bound.reason}`,
      rawValue: evidence.bound.value,
      rawField: 'reviewed relation',
      transformation: 'identity',
      unratedReason: null,
      // The precision of the figure itself, not a rule of thumb. A bound is
      // copied from a value somebody else measured, so it inherits that
      // value's resolution — and, more to the point, rounding a LOWER bound
      // up claims more than the evidence supports. Shipped defect: a bound of
      // 52.6, taken from a measurement rendered `52.6` one row above, printed
      // as `≥ 53`.
      precision: decimalsOf(evidence.bound.value),
    };
  }

  // No bound was reviewed for this row, so an unsettled identity is where it
  // ends — a score attached to the wrong model is indistinguishable from a
  // right one, and nothing below can improve on that.
  if (resolution.status !== 'resolved') {
    return {
      ...UNRATED,
      unratedReason: resolution.status === 'ambiguous' ? 'identity_ambiguous' : 'identity_unresolved',
    };
  }

  // Identity is proven, so the gap is in the evidence rather than the match.
  // A figure that EXISTS but belongs to a group the calibration was measured to
  // have no predictive power for is a different situation from no figure at all:
  // one is nobody's fault, the other is a deliberate refusal to publish a number
  // the evidence says would be wrong.
  const excludedHere =
    typeof evidence.calibratable === 'number' &&
    evidence.group !== undefined &&
    (calibration?.excludedGroups.includes(evidence.group) ?? false);
  return {
    ...UNRATED,
    sourceModelId: resolution.target,
    identityRule: resolution.rule,
    unratedReason: excludedHere ? 'calibration_group_excluded' : 'no_published_benchmark',
  };
}

/**
 * Two scores are only meaningfully ordered when their uncertainty intervals do
 * not overlap. A 0.3-point gap between two values carrying +/-5.7 is noise
 * wearing the costume of a ranking.
 */
export function comparable(a: VQ, b: VQ): boolean {
  if (a.value === null || b.value === null) return false;
  const ua = a.uncertainty ?? 0;
  const ub = b.uncertainty ?? 0;
  return Math.abs(a.value - b.value) > ua + ub;
}

/** Render a VQ at exactly the precision its evidence supports. */
export function formatVQ(vq: VQ): string {
  if (vq.value === null) return '—';
  const n = vq.value.toFixed(vq.precision);
  return vq.bound === 'lower' ? `≥ ${n}` : vq.bound === 'upper' ? `≤ ${n}` : n;
}

// ---------------------------------------------------------------------------
// VO — operational fitness
// ---------------------------------------------------------------------------

export interface OperationalFacts {
  contextTokens?: number | null;
  maxOutputTokens?: number | null;
  tools?: boolean;
  reasoning?: boolean;
  structuredOutput?: boolean;
  attachment?: boolean;
  inputModalities?: string[];
  /** USD per million output tokens. 0 is a real value (free), not missing. */
  costOutputPerM?: number | null;
  /**
   * How this provider bills, which decides whether an absent price is a gap.
   *
   * `included` means the model is covered by a subscription: there is no
   * per-token price to know, so the cost dimension does not apply. `unknown`
   * means a per-token provider published nothing, which is a real gap. The two
   * used to be indistinguishable, so 19 rows reported a fact we hold as a fact
   * we lack.
   */
  billingKind?: CostKind;
}

/** Dimensions VO is built from. Named so the UI can explain any single score. */
export type VODimension = 'context' | 'output' | 'capabilities' | 'cost';

export interface ScoreProfile {
  id: string;
  label: string;
  weights: Record<VODimension, number>;
}

export interface VO {
  kind: 'VO';
  /**
   * Null when NO dimension had data. Returning 0 there would read as "worst
   * operational fit" when the truth is "nothing published" — the same failure
   * `unrated` exists to prevent on the quality side. Unknown is not zero, on
   * either axis.
   */
  value: number | null;
  /** Per-dimension 0..100 contributions, so a score is always explainable. */
  dimensions: Record<VODimension, number | null>;
  /** Dimensions nobody published, excluded from the weighted mean. A real gap. */
  missing: VODimension[];
  /**
   * Dimensions that do not apply to this offering, also excluded from the mean.
   *
   * Distinct from `missing` because they are opposite claims. A subscription
   * model has no per-token price to publish, so reporting its cost as missing
   * counts a known answer as a hole — and would make full coverage unreachable
   * for a whole provider no matter how much evidence we gathered.
   */
  notApplicable: VODimension[];
  profileId: string;
}

/**
 * Percentile of `value` within `population`, 0..100.
 *
 * Percentile rather than an absolute curve because "good context" is only
 * meaningful relative to what is on offer. It is a statement of fact about this
 * catalog, not a judgement we invented.
 */
export function percentile(value: number, population: number[]): number {
  if (population.length === 0) return 50;
  const below = population.filter((p) => p < value).length;
  const equal = population.filter((p) => p === value).length;
  return ((below + equal / 2) / population.length) * 100;
}

export interface VOPopulations {
  context: number[];
  output: number[];
  cost: number[];
}

const CAPABILITY_FLAGS: (keyof OperationalFacts)[] = [
  'tools',
  'reasoning',
  'structuredOutput',
  'attachment',
];

export function computeVO(
  facts: OperationalFacts,
  pop: VOPopulations,
  profile: ScoreProfile,
): VO {
  const dims: Record<VODimension, number | null> = {
    context: facts.contextTokens ? percentile(Math.log(facts.contextTokens), pop.context) : null,
    output: facts.maxOutputTokens ? percentile(Math.log(facts.maxOutputTokens), pop.output) : null,
    capabilities: null,
    cost: null,
  };

  // Capability breadth: how many of the operational capabilities it actually
  // has, plus modality breadth. Objective count, no weighting inside.
  const flags = CAPABILITY_FLAGS.filter((k) => facts[k] === true).length;
  const modalities = facts.inputModalities?.length ?? 0;
  if (facts.inputModalities || CAPABILITY_FLAGS.some((k) => typeof facts[k] === 'boolean')) {
    const maxFlags = CAPABILITY_FLAGS.length;
    const maxModalities = 4; // text, image, audio, video/pdf
    dims.capabilities =
      ((flags / maxFlags) * 0.7 + (Math.min(modalities, maxModalities) / maxModalities) * 0.3) * 100;
  }

  // Cheaper is better, so the percentile is inverted. Free is genuinely the
  // cheapest, not a missing value — and `free` is the cheapest whether it came
  // from a feed-published $0 or from a free, quota-limited provider that
  // publishes no per-token figure at all. In the latter case there is no number
  // to read (fabricating one would launder a declared policy into a feed price),
  // so `free` billing is scored at $0 directly.
  const costForDim = facts.billingKind === 'free' ? 0 : facts.costOutputPerM;
  if (typeof costForDim === 'number') {
    dims.cost = 100 - percentile(costForDim, pop.cost);
  }

  // A subscription has no per-token price to publish. That absence is an answer,
  // so it is reported apart from the absences that are gaps. Either way the
  // dimension leaves the weighted mean and the remaining weights renormalise
  // below, so an excluded dimension never drags a score down.
  const notApplicable: VODimension[] = facts.billingKind === 'included' ? ['cost'] : [];
  const absent = (Object.keys(dims) as VODimension[]).filter((d) => dims[d] === null);
  const missing = absent.filter((d) => !notApplicable.includes(d));
  const present = (Object.keys(dims) as VODimension[]).filter((d) => dims[d] !== null);
  const totalWeight = present.reduce((s, d) => s + profile.weights[d], 0);
  const value =
    present.length === 0 || totalWeight === 0
      ? null
      : present.reduce((s, d) => s + dims[d]! * profile.weights[d], 0) / totalWeight;

  return { kind: 'VO', value, dimensions: dims, missing, notApplicable, profileId: profile.id };
}
