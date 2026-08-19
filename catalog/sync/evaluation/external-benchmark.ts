import { smoothCriterionScore, type CriterionScore, type QualityDimension } from './score.ts';

export interface ExternalBenchmarkEvidence {
  expectedIdentity: string;
  sourceIdentity: string;
  dimension: QualityDimension;
  crosswalkVersion: string | null;
  point: number;
  rangeMin: number;
  rangeMax: number;
  methodologyUrl: string | null;
  sampleCount: number | null;
  confidenceInterval95: { lower: number; upper: number } | null;
  publishedAt: string;
  sourceUrl: string;
}

export type ExternalBenchmarkReason =
  | 'identity_mismatch'
  | 'missing_crosswalk'
  | 'missing_methodology'
  | 'invalid_range'
  | 'stale_evidence'
  | 'non_finite_evidence';

export interface ExternalBenchmarkDecision {
  status: 'accepted' | 'provenance_only';
  reason: ExternalBenchmarkReason | null;
  effectiveCriteria: number | null;
  result: CriterionScore | null;
}

const rejected = (reason: ExternalBenchmarkReason): ExternalBenchmarkDecision => ({
  status: 'provenance_only', reason, effectiveCriteria: null, result: null,
});

export function assessExternalBenchmark(evidence: ExternalBenchmarkEvidence, now: Date): ExternalBenchmarkDecision {
  if (evidence.expectedIdentity !== evidence.sourceIdentity) return rejected('identity_mismatch');
  if (!evidence.crosswalkVersion) return rejected('missing_crosswalk');
  if (!evidence.methodologyUrl || !/^https?:\/\//.test(evidence.methodologyUrl)) return rejected('missing_methodology');
  if (!Number.isFinite(evidence.rangeMin) || !Number.isFinite(evidence.rangeMax)
    || evidence.rangeMax <= evidence.rangeMin || evidence.point < evidence.rangeMin || evidence.point > evidence.rangeMax) {
    return rejected('invalid_range');
  }
  const publishedAt = new Date(evidence.publishedAt);
  if (!Number.isFinite(publishedAt.getTime()) || now.getTime() - publishedAt.getTime() > 180 * 24 * 60 * 60 * 1000) {
    return rejected('stale_evidence');
  }
  const normalizedPoint = (evidence.point - evidence.rangeMin) / (evidence.rangeMax - evidence.rangeMin) * 100;
  let effectiveCriteria: number | null = evidence.sampleCount;
  if (effectiveCriteria === null) {
    const interval = evidence.confidenceInterval95;
    if (!interval || interval.upper <= interval.lower) return rejected('non_finite_evidence');
    const normalizedLower = (interval.lower - evidence.rangeMin) / (evidence.rangeMax - evidence.rangeMin) * 100;
    const normalizedUpper = (interval.upper - evidence.rangeMin) / (evidence.rangeMax - evidence.rangeMin) * 100;
    const halfWidth = (normalizedUpper - normalizedLower) / 2 / 100;
    if (!Number.isFinite(halfWidth) || halfWidth <= 0) return rejected('non_finite_evidence');
    const p = Math.min(0.999999, Math.max(0.000001, normalizedPoint / 100));
    effectiveCriteria = Math.max(1, 3.8416 * p * (1 - p) / (halfWidth * halfWidth) - 4);
  }
  if (!Number.isFinite(effectiveCriteria) || effectiveCriteria <= 0) return rejected('non_finite_evidence');
  const result = smoothCriterionScore(normalizedPoint / 100 * effectiveCriteria, effectiveCriteria);
  return { status: 'accepted', reason: null, effectiveCriteria, result };
}
