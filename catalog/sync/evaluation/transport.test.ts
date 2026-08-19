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
