/**
 * Venom Score — two sub-scores, deliberately never fused into one published
 * number.
 *
 * VQ (quality) and VO (operational) have different coverage and different
 * evidence strength. Fusing them would let a cheap long-context model
 * impersonate an intelligent one on every row where quality is unknown, and the
 * reader could not tell which had happened. So both are always carried, and the
 * evidence level travels with the value.
 *
 * Weights are policy, not invention: they live in a versioned profile that is
 * committed, reviewable and selectable. The failure to avoid is not *having*
 * weights — it is having undeclared ones.
 */

import type { IdentityRule, Resolution } from '../identity.ts';
import { applyCalibration, isAcceptable, type Calibration } from './calibration.ts';

export type EvidenceLevel = 'measured' | 'calibrated' | 'bounded' | 'unrated';

export interface QualityEvidence {
  /** Direct measurement on the target scale, e.g. AA intelligence_index. */
  direct?: number;
  /** A value on a second scale that calibration can map, e.g. design_arena Elo. */
  calibratable?: number;
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
export function computeVQ(
  resolution: Resolution,
  evidence: QualityEvidence,
  calibration: Calibration | null,
): VQ {
  if (resolution.status !== 'resolved') return UNRATED;
  const base = {
    kind: 'VQ' as const,
    sourceModelId: resolution.target,
    identityRule: resolution.rule,
    bound: null,
  };

  if (typeof evidence.direct === 'number') {
    return {
      ...base,
      value: evidence.direct,
      // AA publishes one decimal; treat that as the resolution of the figure,
      // not as proof of perfect accuracy.
      uncertainty: 0.05,
      level: 'measured',
      source: 'artificial_analysis',
      precision: 1,
    };
  }

  if (typeof evidence.calibratable === 'number' && isAcceptable(calibration)) {
    const { value, uncertainty } = applyCalibration(calibration!, evidence.calibratable);
    return {
      ...base,
      value,
      uncertainty,
      level: 'calibrated',
      source: 'design_arena',
      // +/- 5-7 points of uncertainty cannot justify a decimal place.
      precision: 0,
    };
  }

  if (evidence.bound) {
    return {
      ...base,
      value: evidence.bound.value,
      uncertainty: null,
      bound: evidence.bound.side,
      level: 'bounded',
      source: `relation: ${evidence.bound.reason}`,
      precision: 0,
    };
  }

  return { ...UNRATED, sourceModelId: resolution.target, identityRule: resolution.rule };
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
  value: number;
  /** Per-dimension 0..100 contributions, so a score is always explainable. */
  dimensions: Record<VODimension, number | null>;
  /** Dimensions that had no data and were excluded from the weighted mean. */
  missing: VODimension[];
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
  // cheapest, not a missing value.
  if (typeof facts.costOutputPerM === 'number') {
    dims.cost = 100 - percentile(facts.costOutputPerM, pop.cost);
  }

  const missing = (Object.keys(dims) as VODimension[]).filter((d) => dims[d] === null);
  const present = (Object.keys(dims) as VODimension[]).filter((d) => dims[d] !== null);
  const totalWeight = present.reduce((s, d) => s + profile.weights[d], 0);
  const value =
    totalWeight === 0
      ? 0
      : present.reduce((s, d) => s + dims[d]! * profile.weights[d], 0) / totalWeight;

  return { kind: 'VO', value, dimensions: dims, missing, profileId: profile.id };
}
