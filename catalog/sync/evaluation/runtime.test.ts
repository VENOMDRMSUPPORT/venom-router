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

  test('a partly-written answer cut off at the cap is not a wrong answer', async () => {
    // What `gpt-oss:120b` actually returned: the JSON started, the cap hit, and
    // the fragment scored 1 of 5 as though the model had answered wrongly.
    // Nothing in a fragment separates wrong from unfinished, so the dimension
    // must report insufficient evidence rather than publish the fragment.
    let calls = 0;
    // The shared `scenarios` above grade everything 5/5, which is exactly the
    // case that must still be kept. A fragment has to score below full marks.
    const fragmentScores: RuntimeScenario[] = scenarios.map((scenario) => ({
      ...scenario,
      grade: () => ({ weightedSuccesses: 1, weightedCriteria: 5 }),
    }));
    const result = await runDimensionEvaluation({
      providerId: 'p1', modelId: 'm1', dimension: 'coding', scenarios: fragmentScores,
      transport: async () => {
        calls++;
        return {
          kind: 'success',
          response: {
            status: 200,
            headers: {},
            body: { choices: [{ finish_reason: 'length', message: { role: 'assistant', content: 'function isDiv' } }] },
          },
          attempts: 1,
        };
      },
      credential: 'secret', now: () => '2026-08-19T00:00:00.000Z',
    });

    assert.equal(result.status, 'insufficient_evidence');
    assert.equal(result.reason, 'incomplete_valid_scenarios');
    const truncated = result.samples.filter((sample) => sample.errorCode === 'answer_truncated');
    assert.ok(truncated.length > 0, 'the truncation is named on the sample, not hidden');
    assert.equal(truncated[0].weightedSuccesses, null, 'and it carries no score to be aggregated');
    // Doomed on the first such sample, so it stops rather than buying sixty more.
    assert.ok(calls < 63, `expected an early stop, made ${calls} requests`);
  });

  test('a cut-off response that still scored full marks is kept', async () => {
    // The answer arrived and the interruption came after it. Refusing this would
    // discard a paid measurement for nothing.
    const answered = buildEvaluationFixtures().coding[0].expectedResponse as {
      choices: [{ message: { content: string } }];
    };
    const result = await runDimensionEvaluation({
      providerId: 'p1', modelId: 'm1', dimension: 'coding', scenarios,
      transport: async () => ({
        kind: 'success',
        response: {
          status: 200,
          headers: {},
          body: { choices: [{ finish_reason: 'length', message: { content: answered.choices[0].message.content } }] },
        },
        attempts: 1,
      }),
      credential: 'secret', now: () => '2026-08-19T00:00:00.000Z',
    });

    assert.equal(result.status, 'complete');
    assert.ok(result.samples.every((sample) => sample.errorCode === null));
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

describe('abandoning a dimension the moment it cannot be scored', () => {
  test('keeps transient proxy failures in the pool instead of abandoning the dimension', async () => {
    const fixtures = buildEvaluationFixtures().structuredOutput;
    const bodies = new Map(fixtures.map((item) => [JSON.stringify(item.payload), item.expectedResponse]));
    let calls = 0;
    const result = await runDimensionEvaluation({
      providerId: 'p', modelId: 'm', dimension: 'structuredOutput',
      scenarios: fixtures, credential: 'secret', now: () => '2026-08-20T00:00:00.000Z',
      transport: async (payload) => {
        calls++;
        if (calls === 10) return { kind: 'provider_failure', status: 429, attempts: 1, errorCode: 'retry_after_too_long' };
        return { kind: 'success', attempts: 1, response: { status: 200, headers: {}, body: bodies.get(JSON.stringify(payload)) } };
      },
    });
    assert.equal(result.status, 'insufficient_evidence');
    assert.equal(result.samples.length, 60);
    assert.equal(calls, 63, 'the transient failure does not stop the remaining samples');
  });

  test('stops paying once a provider failure has already doomed the run', async () => {
    const fixtures = buildEvaluationFixtures().structuredOutput;
    const bodies = new Map(fixtures.map((item) => [JSON.stringify(item.payload), item.expectedResponse]));
    let calls = 0;
    const result = await runDimensionEvaluation({
      providerId: 'p', modelId: 'm', dimension: 'structuredOutput',
      scenarios: fixtures,
      credential: 'secret',
      now: () => '2026-08-20T00:00:00.000Z',
      transport: async (payload) => {
        calls++;
        // One unrecoverable failure is enough: every sample must succeed for the
        // dimension to be scored, so everything after this is bought for nothing.
        if (calls === 10) return { kind: 'provider_failure', status: 500, attempts: 4, errorCode: 'http_500' };
        return { kind: 'success', attempts: 1, response: { status: 200, headers: {}, body: bodies.get(JSON.stringify(payload)) } };
      },
    });

    assert.equal(result.status, 'insufficient_evidence');
    assert.equal(result.reason, 'incomplete_valid_scenarios');
    assert.ok(calls < 40, `a doomed dimension must stop early, but it issued ${calls} of 63 requests`);
    assert.ok(result.samples.some((sample) => sample.outcome === 'provider_failure'));
  });
});

/**
 * Both proxy-pool failures are transient.
 *
 * `proxy_list_unavailable` is one fetch of a third-party list URL failing —
 * if anything more transient than an exhausted pool. Treating it as permanent
 * abandoned a whole dimension, and the paid samples already collected with it,
 * over a blip on a host that is not even the provider's.
 */
describe('a proxy pool that cannot be refreshed is not a permanent failure', () => {
  test('keeps the dimension running when the proxy list is briefly unreachable', async () => {
    const fixtures = buildEvaluationFixtures().structuredOutput;
    const bodies = new Map(fixtures.map((item) => [JSON.stringify(item.payload), item.expectedResponse]));
    let calls = 0;
    const result = await runDimensionEvaluation({
      providerId: 'p', modelId: 'm', dimension: 'structuredOutput',
      scenarios: fixtures, credential: 'secret', now: () => '2026-08-20T00:00:00.000Z',
      transport: async (payload) => {
        calls++;
        if (calls === 10) {
          return { kind: 'provider_failure', status: null, attempts: 1, errorCode: 'proxy_list_unavailable' };
        }
        return { kind: 'success', attempts: 1, response: { status: 200, headers: {}, body: bodies.get(JSON.stringify(payload)) } };
      },
    });

    assert.equal(result.status, 'insufficient_evidence');
    assert.equal(result.samples.length, 60);
    assert.equal(calls, 63, 'a list blip does not stop the remaining samples');
  });
});
