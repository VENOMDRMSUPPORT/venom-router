import assert from 'node:assert/strict';
import { describe, test } from 'node:test';
import { runDimensionEvaluation, runSpeedEvaluation, type RuntimeScenario } from './runtime.ts';
import { buildEvaluationFixtures } from './fixtures.ts';
import { OVERALL_SCORE_POLICY } from './score.ts';
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
    assert.ok(maxActive <= OVERALL_SCORE_POLICY.qualityProviderConcurrency);
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

describe('stopping a dimension part-way', () => {
  const scenarios = () => buildEvaluationFixtures().structuredOutput;

  test('stops issuing requests once asked, and refuses to score what it has', async () => {
    const fixtures = scenarios();
    const bodies = new Map(fixtures.map((item) => [JSON.stringify(item.payload), item.expectedResponse]));
    let calls = 0;
    let stop = false;
    const result = await runDimensionEvaluation({
      providerId: 'p', modelId: 'm', dimension: 'structuredOutput',
      scenarios: fixtures,
      credential: 'secret',
      now: () => '2026-08-20T00:00:00.000Z',
      shouldStop: () => stop,
      transport: async (payload) => {
        calls++;
        if (calls >= 10) stop = true;
        return { kind: 'success', attempts: 1, response: { status: 200, headers: {}, body: bodies.get(JSON.stringify(payload)) } };
      },
    });

    assert.equal(result.status, 'insufficient_evidence');
    assert.equal(result.reason, 'stopped');
    assert.ok(calls < 63, `a stop must halt the run, but it issued ${calls} of 63 requests`);
    assert.ok(result.samples.length > 0, 'the samples already paid for are kept');
    assert.ok(result.samples.every((sample) => sample !== undefined), 'no holes in the retained samples');
  });

  test('a run nobody stopped is unaffected', async () => {
    const fixtures = scenarios();
    const bodies = new Map(fixtures.map((item) => [JSON.stringify(item.payload), item.expectedResponse]));
    const result = await runDimensionEvaluation({
      providerId: 'p', modelId: 'm', dimension: 'structuredOutput',
      scenarios: fixtures,
      credential: 'secret',
      now: () => '2026-08-20T00:00:00.000Z',
      shouldStop: () => false,
      transport: async (payload) => ({
        kind: 'success', attempts: 1,
        response: { status: 200, headers: {}, body: bodies.get(JSON.stringify(payload)) },
      }),
    });
    assert.equal(result.status, 'complete');
    assert.equal(result.samples.length, 60);
  });
});
