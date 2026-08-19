export const QUALITY_DIMENSIONS = [
  'coding',
  'reasoning',
  'longContext',
  'toolCalling',
  'structuredOutput',
  'vision',
] as const;

export const OPERATIONAL_DIMENSIONS = ['speed', 'costEfficiency'] as const;

export type QualityDimension = (typeof QUALITY_DIMENSIONS)[number];
export type OperationalDimension = (typeof OPERATIONAL_DIMENSIONS)[number];
export type DimensionName = QualityDimension | OperationalDimension;
export type DimensionApplicability = 'supported' | 'unsupported' | 'unknown' | 'evaluating' | 'scored';

export const OVERALL_SCORE_POLICY = {
  methodologyVersion: 'overall-score-v1',
  evaluatorVersion: 'catalog-eval-v1',
  rubricVersion: 'catalog-rubrics-v1',
  testSetVersion: 'catalog-testset-v1',
  region: 'catalog-eval-cairo-1',
  warmupRequests: 3,
  scenarioCount: 20,
  repetitions: 3,
  requestTimeoutMs: 120_000,
  qualityProviderConcurrency: 3,
  // Speed is measured alone under this fixed load. Do not couple it to quality
  // throughput: changing it would make provider speed scores non-comparable.
  speedProviderConcurrency: 2,
  transientRetries: 3,
  qualityWeight: 0.70,
  operationalWeight: 0.30,
  costReferenceInputTokens: 800_000,
  costReferenceOutputTokens: 200_000,
  costZeroAnchorUsd: 0,
  costHundredAnchorUsd: 50,
  speedAnchors: {
    ttftMedianSeconds: { hundred: 0.5, zero: 8 },
    outputTokensPerSecondMedian: { hundred: 100, zero: 5 },
    endToEndP95Seconds: { hundred: 5, zero: 90 },
    successRate: { hundred: 0.995, zero: 0.90 },
  },
} as const;

export interface CriterionScore {
  rawRate: number;
  score: number;
  uncertainty: number;
  confidence: number;
  sampleCount: number;
}

export interface DimensionEvaluation {
  applicability: DimensionApplicability;
  score: number | null;
  uncertainty: number | null;
  confidence: number | null;
  sampleCount: number;
}

export interface SpeedInput {
  ttftMedianSeconds: number;
  outputTokensPerSecondMedian: number;
  endToEndP95Seconds: number;
  successRate: number;
  retainedRequests: number;
}

export interface SpeedScore extends CriterionScore {
  metricScores: {
    ttft: number;
    outputTokensPerSecond: number;
    endToEndP95: number;
    successRate: number;
  };
}

export interface CostInput {
  inputPricePerM: number;
  outputPricePerM: number;
}

export interface CostScore extends CriterionScore {
  referenceCostUsd: number;
}

export interface OverallScoreInput {
  quality: Record<QualityDimension, DimensionEvaluation>;
  operational: Record<OperationalDimension, DimensionEvaluation>;
}

export interface DimensionCoverage {
  scored: number;
  applicable: number;
  percent: number;
}

export interface OverallScoreResult {
  value: number | null;
  display: string;
  status: 'complete' | 'evaluating' | 'insufficient_evidence';
  qualityScore: number | null;
  operationalScore: number | null;
  qualityCoverage: DimensionCoverage;
  overallCoverage: DimensionCoverage;
  includedDimensions: string[];
  excludedDimensions: string[];
  reasons: string[];
  uncertainty: number | null;
}

export interface OverallRankInput {
  providerId: string;
  modelId: string;
  value: number | null;
  uncertainty: number | null;
}

export interface OverallRankResult extends OverallRankInput {
  rank: number | null;
  tied: boolean;
}

const clamp = (value: number, min: number, max: number): number => Math.min(max, Math.max(min, value));

function assertFinite(value: number, name: string): void {
  if (!Number.isFinite(value)) throw new Error(`${name} must be finite`);
}

/** Beta(1,1) smoothing expressed in the approved percent-point contract. */
export function smoothCriterionScore(successes: number, criteria: number): CriterionScore {
  assertFinite(successes, 'successes');
  assertFinite(criteria, 'criteria');
  if (criteria <= 0 || successes < 0 || successes > criteria) {
    throw new Error('successes must be between zero and positive criteria');
  }
  const rawRate = successes / criteria;
  const smoothedRate = (successes + 1) / (criteria + 2);
  const uncertainty = 196 * Math.sqrt((smoothedRate * (1 - smoothedRate)) / (criteria + 4));
  return {
    rawRate,
    score: smoothedRate * 100,
    uncertainty,
    confidence: clamp(1 - uncertainty / 100, 0, 1),
    sampleCount: criteria,
  };
}

function lowerBetter(value: number, hundred: number, zero: number): number {
  assertFinite(value, 'metric');
  return clamp(100 * (zero - value) / (zero - hundred), 0, 100);
}

function higherBetter(value: number, hundred: number, zero: number): number {
  assertFinite(value, 'metric');
  return clamp(100 * (value - zero) / (hundred - zero), 0, 100);
}

export function scoreSpeed(input: SpeedInput): SpeedScore {
  if (!Number.isInteger(input.retainedRequests) || input.retainedRequests <= 0) {
    throw new Error('retainedRequests must be a positive integer');
  }
  const metricScores = {
    ttft: lowerBetter(input.ttftMedianSeconds, OVERALL_SCORE_POLICY.speedAnchors.ttftMedianSeconds.hundred, OVERALL_SCORE_POLICY.speedAnchors.ttftMedianSeconds.zero),
    outputTokensPerSecond: higherBetter(input.outputTokensPerSecondMedian, OVERALL_SCORE_POLICY.speedAnchors.outputTokensPerSecondMedian.hundred, OVERALL_SCORE_POLICY.speedAnchors.outputTokensPerSecondMedian.zero),
    endToEndP95: lowerBetter(input.endToEndP95Seconds, OVERALL_SCORE_POLICY.speedAnchors.endToEndP95Seconds.hundred, OVERALL_SCORE_POLICY.speedAnchors.endToEndP95Seconds.zero),
    successRate: higherBetter(input.successRate, OVERALL_SCORE_POLICY.speedAnchors.successRate.hundred, OVERALL_SCORE_POLICY.speedAnchors.successRate.zero),
  };
  const rawScore = Object.values(metricScores).reduce((sum, value) => sum + value, 0) / 4;
  const result = smoothCriterionScore(rawScore / 100 * input.retainedRequests, input.retainedRequests);
  return { ...result, metricScores };
}

export function scoreCostEfficiency(input: CostInput): CostScore {
  assertFinite(input.inputPricePerM, 'inputPricePerM');
  assertFinite(input.outputPricePerM, 'outputPricePerM');
  if (input.inputPricePerM < 0 || input.outputPricePerM < 0) throw new Error('prices cannot be negative');
  const referenceCostUsd = input.inputPricePerM * 0.8 + input.outputPricePerM * 0.2;
  const rawScore = clamp(100 * (1 - referenceCostUsd / OVERALL_SCORE_POLICY.costHundredAnchorUsd), 0, 100);
  const result = smoothCriterionScore(rawScore, 100);
  return { ...result, referenceCostUsd };
}

function coverageFor(evaluations: Record<string, DimensionEvaluation>): DimensionCoverage {
  const entries = Object.entries(evaluations);
  const applicable = entries.filter(([, evaluation]) => evaluation.applicability !== 'unsupported').length;
  const scored = entries.filter(([, evaluation]) => evaluation.applicability === 'scored' && evaluation.score !== null).length;
  return { scored, applicable, percent: applicable === 0 ? 0 : scored / applicable * 100 };
}

function groupScore(
  evaluations: Record<string, DimensionEvaluation>,
): { value: number | null; uncertainty: number | null; complete: boolean; reasons: string[] } {
  const entries = Object.entries(evaluations);
  const applicable = entries.filter(([, evaluation]) => evaluation.applicability !== 'unsupported');
  const reasons: string[] = [];
  for (const [dimension, evaluation] of applicable) {
    if (evaluation.applicability === 'unknown') reasons.push(`unknown_${dimension}`);
    else if (evaluation.applicability === 'evaluating') reasons.push(`evaluating_${dimension}`);
    else if (evaluation.applicability !== 'scored' || evaluation.score === null) reasons.push(`missing_${dimension}_evaluation`);
  }
  if (applicable.length === 0) reasons.push('no_applicable_dimensions');
  const complete = reasons.length === 0;
  if (!complete) return { value: null, uncertainty: null, complete, reasons };
  const scores = applicable.map(([, evaluation]) => evaluation.score!);
  const uncertainties = applicable.map(([, evaluation]) => evaluation.uncertainty ?? 0);
  const value = scores.reduce((sum, score) => sum + score, 0) / scores.length;
  const weight = 1 / scores.length;
  const uncertainty = Math.sqrt(uncertainties.reduce((sum, item) => sum + (weight * item) ** 2, 0));
  return { value, uncertainty, complete, reasons };
}

export function aggregateOverallScore(input: OverallScoreInput): OverallScoreResult {
  const qualityCoverage = coverageFor(input.quality);
  const operationalCoverage = coverageFor(input.operational);
  const overallCoverage: DimensionCoverage = {
    scored: qualityCoverage.scored + operationalCoverage.scored,
    applicable: qualityCoverage.applicable + operationalCoverage.applicable,
    percent: qualityCoverage.applicable + operationalCoverage.applicable === 0
      ? 0
      : (qualityCoverage.scored + operationalCoverage.scored) /
        (qualityCoverage.applicable + operationalCoverage.applicable) * 100,
  };
  const quality = groupScore(input.quality);
  const operational = groupScore(input.operational);
  const reasons = [...quality.reasons, ...operational.reasons];
  const complete = quality.complete && operational.complete;
  const evaluating = Object.values(input.quality).some((item) => item.applicability === 'evaluating')
    || Object.values(input.operational).some((item) => item.applicability === 'evaluating');
  const status = complete ? 'complete' : evaluating ? 'evaluating' : 'insufficient_evidence';
  const includedDimensions = [...QUALITY_DIMENSIONS, ...OPERATIONAL_DIMENSIONS]
    .filter((dimension) => {
      const evaluation = (input.quality as Record<string, DimensionEvaluation>)[dimension]
        ?? (input.operational as Record<string, DimensionEvaluation>)[dimension];
      return evaluation?.applicability === 'scored';
    });
  const excludedDimensions = QUALITY_DIMENSIONS.filter((dimension) => input.quality[dimension].applicability === 'unsupported');
  const value = complete ? quality.value! * 0.70 + operational.value! * 0.30 : null;
  const uncertainty = complete
    ? Math.sqrt((0.70 * quality.uncertainty!) ** 2 + (0.30 * operational.uncertainty!) ** 2)
    : null;
  return {
    value,
    display: value === null ? '—' : `${value.toFixed(1)}%`,
    status,
    qualityScore: quality.value,
    operationalScore: operational.value,
    qualityCoverage,
    overallCoverage,
    includedDimensions,
    excludedDimensions,
    reasons: [...new Set(reasons)],
    uncertainty,
  };
}

/**
 * Dense global ranking over full-precision scores. Adjacent uncertainty
 * intervals that overlap form one evidence tie; unrated offers remain unplaced.
 */
export function rankOverallScores(items: OverallRankInput[]): OverallRankResult[] {
  const scored = items
    .filter((item): item is OverallRankInput & { value: number } => item.value !== null)
    .sort((a, b) => b.value - a.value || a.providerId.localeCompare(b.providerId) || a.modelId.localeCompare(b.modelId));
  const unscored = items
    .filter((item) => item.value === null)
    .sort((a, b) => a.providerId.localeCompare(b.providerId) || a.modelId.localeCompare(b.modelId));

  const ranked: OverallRankResult[] = [];
  let rank = 0;
  let index = 0;
  while (index < scored.length) {
    rank += 1;
    const members = [scored[index]];
    let groupLower = scored[index].value - (scored[index].uncertainty ?? 0);
    let next = index + 1;
    while (next < scored.length) {
      const candidateUpper = scored[next].value + (scored[next].uncertainty ?? 0);
      if (candidateUpper < groupLower) break;
      members.push(scored[next]);
      groupLower = Math.min(groupLower, scored[next].value - (scored[next].uncertainty ?? 0));
      next += 1;
    }
    for (const member of members) ranked.push({ ...member, rank, tied: members.length > 1 });
    index = next;
  }
  return [...ranked, ...unscored.map((item) => ({ ...item, rank: null, tied: false }))];
}
