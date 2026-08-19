import assert from 'node:assert/strict';
import { describe, test } from 'node:test';
import {
  QUALITY_DIMENSIONS,
  OVERALL_SCORE_POLICY,
  aggregateOverallScore,
  scoreCostEfficiency,
  scoreSpeed,
  smoothCriterionScore,
  rankOverallScores,
  type DimensionEvaluation,
  type OverallScoreInput,
} from './score.ts';

const scored = (score: number, uncertainty = 1): DimensionEvaluation => ({
  applicability: 'scored', score, uncertainty, confidence: 0.99, sampleCount: 60,
});

function completeInput(overrides: Partial<OverallScoreInput> = {}): OverallScoreInput {
  const quality = Object.fromEntries(QUALITY_DIMENSIONS.map((dimension) => [dimension, scored(80)])) as OverallScoreInput['quality'];
  quality.vision = { applicability: 'unsupported', score: null, uncertainty: null, confidence: null, sampleCount: 0 };
  return {
    quality,
    operational: { speed: scored(60), costEfficiency: scored(40) },
    ...overrides,
  };
}

describe('overall-score-v1 math', () => {
  test('publishes the complete reproducible runtime decision gate', () => {
    assert.deepEqual({
      evaluatorVersion: OVERALL_SCORE_POLICY.evaluatorVersion,
      region: OVERALL_SCORE_POLICY.region,
      warmupRequests: OVERALL_SCORE_POLICY.warmupRequests,
      scenarioCount: OVERALL_SCORE_POLICY.scenarioCount,
      repetitions: OVERALL_SCORE_POLICY.repetitions,
      requestTimeoutMs: OVERALL_SCORE_POLICY.requestTimeoutMs,
      providerConcurrency: OVERALL_SCORE_POLICY.providerConcurrency,
      transientRetries: OVERALL_SCORE_POLICY.transientRetries,
    }, {
      evaluatorVersion: 'catalog-eval-v1', region: 'catalog-eval-cairo-1', warmupRequests: 3,
      scenarioCount: 20, repetitions: 3, requestTimeoutMs: 120_000,
      providerConcurrency: 2, transientRetries: 3,
    });
  });
  test('finite perfect criterion evidence is below 100', () => {
    const result = smoothCriterionScore(60, 60);
    assert.equal(result.score, 98.38709677419355);
    assert.ok(result.score < 100);
    assert.ok(result.score > 0);
  });

  test('maps speed metrics through fixed absolute anchors and smooths the result', () => {
    const result = scoreSpeed({
      ttftMedianSeconds: 0.5,
      outputTokensPerSecondMedian: 100,
      endToEndP95Seconds: 5,
      successRate: 0.995,
      retainedRequests: 60,
    });
    assert.ok(result.score < 100);
    assert.ok(result.rawRate > 0.99);
    assert.equal(result.sampleCount, 60);
  });

  test('cost efficiency uses the fixed 800k/200k reference workload', () => {
    const free = scoreCostEfficiency({ inputPricePerM: 0, outputPricePerM: 0 });
    const expensive = scoreCostEfficiency({ inputPricePerM: 50, outputPricePerM: 50 });
    assert.equal(free.referenceCostUsd, 0);
    assert.equal(expensive.referenceCostUsd, 50);
    assert.ok(free.score < 100);
    assert.ok(expensive.score > 0);
    assert.ok(expensive.score < free.score);
  });

  test('unsupported vision is excluded without lowering the other quality dimensions', () => {
    const result = aggregateOverallScore(completeInput());
    assert.equal(result.status, 'complete');
    assert.equal(result.qualityScore, 80);
    assert.equal(result.qualityCoverage.applicable, 5);
    assert.equal(result.qualityCoverage.scored, 5);
    assert.deepEqual(result.excludedDimensions, ['vision']);
  });

  test('unknown vision blocks final publication instead of becoming zero', () => {
    const quality = completeInput().quality;
    quality.vision = { applicability: 'unknown', score: null, uncertainty: null, confidence: null, sampleCount: 0 };
    const result = aggregateOverallScore({ ...completeInput(), quality });
    assert.equal(result.status, 'insufficient_evidence');
    assert.equal(result.value, null);
    assert.ok(result.reasons.includes('unknown_vision'));
  });

  test('overall uses full-precision 70/30 aggregation', () => {
    const result = aggregateOverallScore(completeInput());
    assert.equal(result.qualityScore, 80);
    assert.equal(result.operationalScore, 50);
    assert.equal(result.value, 71);
    assert.equal(result.display, '71.0%');
  });

  test('ranks complete overall scores globally and densely with uncertainty ties', () => {
    const result = rankOverallScores([
      { providerId: 'a', modelId: 'best', value: 84, uncertainty: 1 },
      { providerId: 'b', modelId: 'tie-a', value: 80, uncertainty: 2 },
      { providerId: 'c', modelId: 'tie-b', value: 77, uncertainty: 2 },
      { providerId: 'd', modelId: 'last', value: 60, uncertainty: null },
      { providerId: 'e', modelId: 'unrated', value: null, uncertainty: null },
    ]);

    assert.deepEqual(result.map((item) => [item.modelId, item.rank, item.tied]), [
      ['best', 1, false],
      ['tie-a', 2, true],
      ['tie-b', 2, true],
      ['last', 3, false],
      ['unrated', null, false],
    ]);
  });
});
