import assert from 'node:assert/strict';
import { describe, test } from 'node:test';
import {
  MODEL_SCORE_POLICY,
  computeModelScore,
  rankByModelScore,
  type ModelScoreInput,
} from './model-score.ts';

const input = (overrides: Partial<ModelScoreInput> = {}): ModelScoreInput => ({
  providerId: 'provider-a',
  modelId: 'model-a',
  vq: {
    value: 60,
    uncertainty: 4,
    bound: null,
    evidenceLevel: 'calibrated',
  },
  vo: {
    value: 80,
    missingDimensions: [],
  },
  ...overrides,
});

describe('model-score-v1', () => {
  test('combines quality and operational fit with the declared 70/30 policy', () => {
    const score = computeModelScore(input());

    assert.equal(MODEL_SCORE_POLICY.methodologyVersion, 'model-score-v1');
    assert.equal(score.value, 66);
    assert.equal(score.display, '66.0%');
    assert.equal(score.qualityWeight, 0.7);
    assert.equal(score.operationalWeight, 0.3);
    assert.equal(score.reason, null);
  });

  test('uses VO at its published integer precision', () => {
    const score = computeModelScore(input({
      vq: { value: 59.7, uncertainty: 0.05, bound: null, evidenceLevel: 'measured' },
      vo: { value: 79.5, missingDimensions: [] },
    }));

    assert.equal(MODEL_SCORE_POLICY.operationalPrecision, 0);
    assert.equal(score.value, 65.78999999999999);
    assert.equal(score.display, '65.8%');
  });

  test('propagates only VQ uncertainty because VO has no uncertainty contract', () => {
    const score = computeModelScore(input());

    assert.equal(score.uncertainty, 2.8);
  });

  test('inherits a quality bound and reports partial VO separately', () => {
    const score = computeModelScore(input({
      vq: { value: 50, uncertainty: null, bound: 'lower', evidenceLevel: 'bounded' },
      vo: { value: 70, missingDimensions: ['cost'] },
    }));

    assert.equal(score.value, 56);
    assert.equal(score.display, '≥ 56.0%');
    assert.equal(score.bound, 'lower');
    assert.equal(score.qualityEvidenceLevel, 'bounded');
    assert.equal(score.operationalCoverage, 'partial');
  });

  test('withholds a score and names which component is absent', () => {
    const missingVq = computeModelScore(input({
      vq: { value: null, uncertainty: null, bound: null, evidenceLevel: 'unrated' },
    }));
    const missingVo = computeModelScore(input({
      vo: { value: null, missingDimensions: ['context', 'output', 'capabilities', 'cost'] },
    }));
    const missingBoth = computeModelScore(input({
      vq: { value: null, uncertainty: null, bound: null, evidenceLevel: 'unrated' },
      vo: { value: null, missingDimensions: ['context', 'output', 'capabilities', 'cost'] },
    }));

    assert.deepEqual(
      [missingVq.reason, missingVo.reason, missingBoth.reason],
      ['missing_vq', 'missing_vo', 'missing_both'],
    );
    assert.deepEqual(
      [missingVq.value, missingVo.value, missingBoth.value],
      [null, null, null],
    );
  });
});

describe('global model-score ranking', () => {
  test('uses the unrounded value and assigns dense ranks', () => {
    const rows = [
      input({ providerId: 'p', modelId: 'third', vq: { value: 50, uncertainty: 0, bound: null, evidenceLevel: 'measured' }, vo: { value: 50, missingDimensions: [] } }),
      input({ providerId: 'p', modelId: 'first', vq: { value: 60.06, uncertainty: 0, bound: null, evidenceLevel: 'measured' }, vo: { value: 80, missingDimensions: [] } }),
      input({ providerId: 'p', modelId: 'second', vq: { value: 60.04, uncertainty: 0, bound: null, evidenceLevel: 'measured' }, vo: { value: 80, missingDimensions: [] } }),
    ].map((row) => ({ ...row, modelScore: computeModelScore(row) }));

    const result = rankByModelScore(rows);

    assert.deepEqual(result.ranked.map((group) => ({
      rank: group.rank,
      models: group.members.map((member) => member.modelId),
    })), [
      { rank: 1, models: ['first'] },
      { rank: 2, models: ['second'] },
      { rank: 3, models: ['third'] },
    ]);
  });

  test('ties overlapping uncertainty intervals and leaves missing scores unplaced', () => {
    const tiedA = input({ modelId: 'tied-a', vq: { value: 60, uncertainty: 4, bound: null, evidenceLevel: 'calibrated' } });
    const tiedB = input({ modelId: 'tied-b', vq: { value: 62, uncertainty: 4, bound: null, evidenceLevel: 'calibrated' } });
    const unplaced = input({
      modelId: 'unplaced',
      vq: { value: null, uncertainty: null, bound: null, evidenceLevel: 'unrated' },
    });
    const rows = [tiedA, tiedB, unplaced].map((row) => ({ ...row, modelScore: computeModelScore(row) }));

    const result = rankByModelScore(rows);

    assert.equal(result.ranked.length, 1);
    assert.equal(result.ranked[0].rank, 1);
    assert.equal(result.ranked[0].members.length, 2);
    assert.equal(result.ranked[0].tiedByUncertainty, true);
    assert.deepEqual(result.unplaced.map((row) => row.modelId), ['unplaced']);
  });
});
