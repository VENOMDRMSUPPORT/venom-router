import type { EvidenceLevel } from './venom-score.ts';

export const MODEL_SCORE_POLICY = {
  methodologyVersion: 'model-score-v1',
  qualityWeight: 0.7,
  operationalWeight: 0.3,
  operationalPrecision: 0,
} as const;

export type ModelScoreReason = 'missing_vq' | 'missing_vo' | 'missing_both';
export type OperationalCoverage = 'complete' | 'partial' | 'missing';

export interface ModelScoreInput {
  providerId: string;
  modelId: string;
  vq: {
    value: number | null;
    uncertainty: number | null;
    bound: 'lower' | 'upper' | null;
    evidenceLevel: EvidenceLevel;
  };
  vo: {
    value: number | null;
    missingDimensions: string[];
  };
}

export interface ModelScore {
  value: number | null;
  display: string;
  methodologyVersion: typeof MODEL_SCORE_POLICY.methodologyVersion;
  qualityWeight: typeof MODEL_SCORE_POLICY.qualityWeight;
  operationalWeight: typeof MODEL_SCORE_POLICY.operationalWeight;
  operationalPrecision: typeof MODEL_SCORE_POLICY.operationalPrecision;
  uncertainty: number | null;
  bound: 'lower' | 'upper' | null;
  reason: ModelScoreReason | null;
  qualityEvidenceLevel: EvidenceLevel;
  operationalCoverage: OperationalCoverage;
}

const missingReason = (vq: number | null, vo: number | null): ModelScoreReason | null => {
  if (vq === null && vo === null) return 'missing_both';
  if (vq === null) return 'missing_vq';
  if (vo === null) return 'missing_vo';
  return null;
};

export function computeModelScore(input: ModelScoreInput): ModelScore {
  const reason = missingReason(input.vq.value, input.vo.value);
  const operationalCoverage: OperationalCoverage =
    input.vo.value === null
      ? 'missing'
      : input.vo.missingDimensions.length > 0
        ? 'partial'
        : 'complete';
  const base = {
    methodologyVersion: MODEL_SCORE_POLICY.methodologyVersion,
    qualityWeight: MODEL_SCORE_POLICY.qualityWeight,
    operationalWeight: MODEL_SCORE_POLICY.operationalWeight,
    operationalPrecision: MODEL_SCORE_POLICY.operationalPrecision,
    qualityEvidenceLevel: input.vq.evidenceLevel,
    operationalCoverage,
  };

  if (reason !== null) {
    return {
      ...base,
      value: null,
      display: '—',
      uncertainty: null,
      bound: null,
      reason,
    };
  }

  const operationalValue = Math.round(input.vo.value!);
  const value = input.vq.value! * MODEL_SCORE_POLICY.qualityWeight
    + operationalValue * MODEL_SCORE_POLICY.operationalWeight;
  const bound = input.vq.bound;
  const prefix = bound === 'lower' ? '≥ ' : bound === 'upper' ? '≤ ' : '';

  return {
    ...base,
    value,
    display: `${prefix}${value.toFixed(1)}%`,
    uncertainty: input.vq.uncertainty === null
      ? null
      : input.vq.uncertainty * MODEL_SCORE_POLICY.qualityWeight,
    bound,
    reason: null,
  };
}

export interface RankedModelScore {
  providerId: string;
  modelId: string;
  modelScore: ModelScore;
}

export interface ModelScoreRankGroup {
  rank: number;
  members: RankedModelScore[];
  tiedByUncertainty: boolean;
}

export interface ModelScoreRanking {
  ranked: ModelScoreRankGroup[];
  unplaced: RankedModelScore[];
}

function scoresAreComparable(a: ModelScore, b: ModelScore): boolean {
  if (a.value === null || b.value === null) return false;
  return Math.abs(a.value - b.value) > (a.uncertainty ?? 0) + (b.uncertainty ?? 0);
}

export function rankByModelScore(models: RankedModelScore[]): ModelScoreRanking {
  const rankedModels = models
    .filter((model) => model.modelScore.value !== null)
    .sort((a, b) => b.modelScore.value! - a.modelScore.value!);
  const unplaced = models.filter((model) => model.modelScore.value === null);
  const ranked: ModelScoreRankGroup[] = [];

  for (const model of rankedModels) {
    const open = ranked[ranked.length - 1];
    if (open && !scoresAreComparable(open.members[0].modelScore, model.modelScore)) {
      open.members.push(model);
      open.tiedByUncertainty = open.members.some(
        (member) => member.modelScore.value !== model.modelScore.value,
      );
      continue;
    }
    ranked.push({ rank: ranked.length + 1, members: [model], tiedByUncertainty: false });
  }

  return { ranked, unplaced };
}

export function modelScoreSortContract() {
  return {
    key: 'modelScore' as const,
    field: 'modelScore.value' as const,
    unplacedLabel: 'No model score',
    tieRule: 'tied when |a - b| <= uncertainty(a) + uncertainty(b)',
  };
}
