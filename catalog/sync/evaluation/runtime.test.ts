import assert from 'node:assert/strict';
import { describe, test } from 'node:test';
import { runDimensionEvaluation, runSpeedEvaluation, type RuntimeScenario } from './runtime.ts';
import type { EvaluationTransport } from './transport.ts';

const scenarios: RuntimeScenario[] = Array.from({ length: 20 }, (_, index) => ({
  id: `coding-${String(index + 1).padStart(2, '0')}`,
  payload: { prompt: `scenario ${index + 1}` },
  grade: () => ({ weightedSuccesses: 5, weightedCriteria: 5 }),
}));

describe('runtime evaluation scheduling', () => {
  test('excludes three warmups and retains 20 scenarios x three repetitions', async () => {
    let calls = 0;
    let active = 0;
    let maxActive = 0;
    const transport: EvaluationTransport = async () => {
      calls++;
      active++;
      maxActive = Math.max(maxActive, active);
      await Promise.resolve();
      active--;
      return { kind: 'success', response: { status: 200, body: { ok: true }, headers: {} }, attempts: 1 };
    };
    const result = await runDimensionEvaluation({
      providerId: 'p1', modelId: 'm1', dimension: 'coding', scenarios,
      transport, credential: 'secret', now: () => '2026-08-19T00:00:00.000Z',
    });
    assert.equal(calls, 63);
    assert.equal(result.status, 'complete');
    if (result.status !== 'complete') throw new Error('expected a complete runtime evaluation');
    assert.equal(result.samples.length, 60);
    assert.equal(result.score.sampleCount, 300);
    assert.ok(result.score.score < 100);
    assert.ok(maxActive <= 2);
  });

  test('missing credentials produces insufficient evidence without a request', async () => {
    let calls = 0;
    const result = await runDimensionEvaluation({
      providerId: 'p1', modelId: 'm1', dimension: 'coding', scenarios,
      transport: async () => { calls++; throw new Error('must not run'); }, credential: null,
      now: () => '2026-08-19T00:00:00.000Z',
    });
    assert.equal(result.status, 'insufficient_evidence');
    assert.equal(result.reason, 'missing_evaluation_credentials');
    assert.equal(calls, 0);
  });

  test('speed computes medians, p95 and exhausted-failure success rate', () => {
    const result = runSpeedEvaluation([
      { ttftSeconds: 1, outputTokensPerSecond: 50, endToEndSeconds: 10, success: true },
      { ttftSeconds: 2, outputTokensPerSecond: 40, endToEndSeconds: 20, success: true },
      { ttftSeconds: null, outputTokensPerSecond: null, endToEndSeconds: null, success: false },
    ]);
    assert.equal(result.metrics.successRate, 2 / 3);
    assert.equal(result.metrics.ttftMedianSeconds, 1.5);
    assert.equal(result.metrics.endToEndP95Seconds, 20);
  });
});
