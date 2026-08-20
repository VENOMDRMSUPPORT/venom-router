import assert from 'node:assert/strict';
import { describe, test } from 'node:test';
import { callWithPolicy, redactSecrets, type TransportResponse } from './transport.ts';

const response = (status: number): TransportResponse => ({ status, body: {}, headers: {} });

describe('evaluation transport policy', () => {
  test('waits for Retry-After before retrying a rate-limited request', async () => {
    const statuses = [
      { ...response(429), headers: { 'retry-after': '1' } },
      response(200),
    ];
    const waits: number[] = [];
    const result = await callWithPolicy(async () => statuses.shift()!, {
      timeoutMs: 100,
      transientRetries: 1,
      sleep: async (milliseconds) => { waits.push(milliseconds); },
      random: () => 0.5,
      now: () => 0,
    });
    assert.equal(result.kind, 'success');
    assert.deepEqual(waits, [1_000]);
  });

  test('retries 429 and 5xx at most three times', async () => {
    const statuses = [429, 500, 502, 200];
    let calls = 0;
    const result = await callWithPolicy(async () => {
      calls++;
      return response(statuses.shift()!);
    }, { timeoutMs: 100, transientRetries: 3 });
    assert.equal(result.kind, 'success');
    assert.equal(calls, 4);
  });

  test('does not retry a declared 4xx model rejection', async () => {
    let calls = 0;
    const result = await callWithPolicy(async () => { calls++; return response(400); }, { timeoutMs: 100, transientRetries: 3 });
    assert.equal(result.kind, 'model_failure');
    assert.equal(calls, 1);
  });

  test('redacts every known secret from nested evidence', () => {
    const secret = 'VENOM_CATALOG_SECRET_CANARY';
    assert.deepEqual(redactSecrets({ error: secret, nested: [secret, 'ok'] }, [secret]), {
      error: '[REDACTED]', nested: ['[REDACTED]', 'ok'],
    });
  });
});

describe('a provider that says come back much later', () => {
  test('does not sleep for hours holding the only worker', async () => {
    // OpenCode Zen answers a free-usage 429 with Retry-After: 41922 — eleven and
    // a half hours. Honouring that literally parks the single evaluation worker
    // mid-dimension, with the queue behind it and a progress modal frozen at
    // whatever it last reported. A wait that long is not a retry; it is a
    // refusal with a time on it.
    const slept: number[] = [];
    const outcome = await callWithPolicy(
      async () => ({ status: 429, headers: { 'retry-after': '41922' }, body: null }),
      {
        timeoutMs: 1000,
        transientRetries: 3,
        sleep: async (ms) => { slept.push(ms); },
        random: () => 0.5,
        now: () => 0,
        maxBackoffMs: 30_000,
      },
    );

    assert.equal(outcome.kind, 'provider_failure');
    assert.equal(outcome.kind === 'provider_failure' && outcome.errorCode, 'retry_after_too_long');
    assert.deepEqual(slept, [], 'nothing was waited on');
    assert.equal(outcome.attempts, 1, 'and nothing was retried into the same wall');
  });

  test('still honours a short Retry-After, which is what the header is for', async () => {
    const slept: number[] = [];
    let calls = 0;
    const outcome = await callWithPolicy(
      async (): Promise<TransportResponse> => {
        calls++;
        if (calls === 1) return { status: 429, headers: { 'retry-after': '2' }, body: null };
        return { status: 200, headers: {}, body: { ok: true } };
      },
      {
        timeoutMs: 1000,
        transientRetries: 3,
        sleep: async (ms) => { slept.push(ms); },
        random: () => 0.5,
        now: () => 0,
        maxBackoffMs: 30_000,
      },
    );

    assert.equal(outcome.kind, 'success');
    assert.deepEqual(slept, [2000]);
  });
});
