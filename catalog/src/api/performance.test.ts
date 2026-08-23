import { test } from 'vitest';
import assert from 'node:assert/strict';
import { performanceRows, summarizePerformance } from './performance';
import type { ApiModel } from './client';

const model = (providerId: string, modelId: string, performance: Partial<ApiModel['performance']>): ApiModel => ({
  providerId, modelId, displayName: modelId, performance: {
    status: 'measured', runId: 1, evaluatedAt: '2026-08-23T10:00:00.000Z', sampleCount: 10, successfulSamples: 10,
    ttftMedianSeconds: 0.2, outputTokensPerSecondMedian: 40, endToEndP95Seconds: 2, successRate: 1,
    speedScore: 80, ...performance,
  },
} as ApiModel);

test('performance rows include only complete measured speed data and rank by throughput', () => {
  const rows = performanceRows([
    model('slow', 'slow-model', { outputTokensPerSecondMedian: 10 }),
    model('fast', 'fast-model', { outputTokensPerSecondMedian: 90 }),
    model('unknown', 'unknown-model', { status: 'not_measured', outputTokensPerSecondMedian: null }),
  ]);
  assert.deepEqual(rows.map((row) => row.model.modelId), ['fast-model', 'slow-model']);
});

test('summary reports measured coverage and truthful aggregate metrics', () => {
  const summary = summarizePerformance([
    model('a', 'a-model', { ttftMedianSeconds: 0.1, outputTokensPerSecondMedian: 20, endToEndP95Seconds: 1, successRate: 0.8, sampleCount: 5, successfulSamples: 4 }),
    model('b', 'b-model', { ttftMedianSeconds: 0.3, outputTokensPerSecondMedian: 60, endToEndP95Seconds: 3, successRate: 1, sampleCount: 7, successfulSamples: 7 }),
    model('c', 'c-model', { status: 'not_measured', ttftMedianSeconds: null, outputTokensPerSecondMedian: null, endToEndP95Seconds: null, successRate: null }),
  ]);
  assert.equal(summary.totalModels, 3);
  assert.equal(summary.measuredModels, 2);
  assert.equal(summary.sampleCount, 12);
  assert.equal(summary.successfulSamples, 11);
  assert.equal(summary.medianTtftSeconds, 0.2);
  assert.equal(summary.medianOutputTokensPerSecond, 40);
  assert.equal(summary.medianEndToEndP95Seconds, 2);
  assert.equal(summary.averageSuccessRate, 0.9);
  assert.equal(summary.bestThroughput?.model.modelId, 'b-model');
  assert.equal(summary.fastestTtft?.model.modelId, 'a-model');
  assert.equal(summary.highestSuccessRate?.model.modelId, 'b-model');
});

test('summary stays empty instead of inventing zeroes when no model was measured', () => {
  const summary = summarizePerformance([model('unknown', 'unknown-model', { status: 'not_measured', ttftMedianSeconds: null, outputTokensPerSecondMedian: null, endToEndP95Seconds: null, successRate: null })]);
  assert.equal(summary.measuredModels, 0);
  assert.equal(summary.medianTtftSeconds, null);
  assert.equal(summary.averageSuccessRate, null);
  assert.equal(summary.bestThroughput, null);
});
