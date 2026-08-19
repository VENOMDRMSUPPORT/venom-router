import assert from 'node:assert/strict';
import { describe, test } from 'node:test';
import { openDb, type Db } from '../db/index.ts';
import { buildEvaluationFixtures, fixtureDigest } from '../sync/evaluation/fixtures.ts';
import { OVERALL_SCORE_POLICY } from '../sync/evaluation/score.ts';
import type { EvaluationTransport } from '../sync/evaluation/transport.ts';
import { createEvaluationExecutor } from './evaluation-executor.ts';

const REQUESTS_PER_DIMENSION =
  OVERALL_SCORE_POLICY.warmupRequests + OVERALL_SCORE_POLICY.scenarioCount * OVERALL_SCORE_POLICY.repetitions;

function seed(): Db {
  const db = openDb(':memory:');
  db.prepare(`INSERT INTO providers (id,name,roster_url) VALUES ('p','P','https://p.test')`).run();
  db.prepare(`INSERT INTO models (provider_id,model_id,status,first_seen_at,last_seen_at)
    VALUES ('p','m','active','2026-08-20','2026-08-20')`).run();
  return db;
}

/** Answers every scenario with the response its own fixture declares correct. */
function perfectTransport(dimension: 'structuredOutput'): EvaluationTransport {
  const bodies = new Map(buildEvaluationFixtures()[dimension]
    .map((item) => [JSON.stringify(item.payload), item.expectedResponse]));
  return async (payload) => ({
    kind: 'success',
    attempts: 1,
    response: { status: 200, headers: {}, body: bodies.get(JSON.stringify(payload)) },
  });
}

describe('the service-side evaluation executor', () => {
  test('persists a scored dimension and reports progress as requests are issued', async () => {
    const db = seed();
    const executor = createEvaluationExecutor(db, {
      credential: () => 'secret',
      transport: () => perfectTransport('structuredOutput'),
      now: () => '2026-08-20T00:00:00.000Z',
    });

    const progress: Array<{ completed: number; total: number }> = [];
    const result = await executor.runDimension({
      providerId: 'p',
      modelId: 'm',
      identityId: 'vendor/model',
      dimension: 'structuredOutput',
      onSample: (completed, total) => progress.push({ completed, total }),
    });

    assert.equal(result.status, 'complete');
    assert.ok(result.score !== null && result.score > 99, `expected a near-perfect score, got ${result.score}`);

    assert.equal(progress.length, REQUESTS_PER_DIMENSION, 'one report per request, warmups included');
    assert.deepEqual(progress[0], { completed: 1, total: REQUESTS_PER_DIMENSION });
    assert.deepEqual(progress[progress.length - 1], {
      completed: REQUESTS_PER_DIMENSION, total: REQUESTS_PER_DIMENSION,
    });
    // Monotonic: a bar that goes backwards is worse than no bar.
    for (let index = 1; index < progress.length; index++) {
      assert.ok(progress[index].completed >= progress[index - 1].completed);
    }

    const stored = db.prepare(`SELECT status, test_set_hash FROM model_identity_scores
      WHERE identity_id='vendor/model' AND dimension='structuredOutput'`).get() as unknown as
      { status: string; test_set_hash: string };
    assert.equal(stored.status, 'scored');
    assert.equal(stored.test_set_hash, fixtureDigest(buildEvaluationFixtures()));
    db.close();
  });

  test('reports an incomplete dimension rather than scoring a partial one', async () => {
    const db = seed();
    let calls = 0;
    const executor = createEvaluationExecutor(db, {
      credential: () => 'secret',
      transport: () => async (payload, secret) => {
        calls++;
        if (calls === 20) return { kind: 'provider_failure', status: 429, attempts: 4, errorCode: 'http_429' };
        return perfectTransport('structuredOutput')(payload, secret);
      },
      now: () => '2026-08-20T00:00:00.000Z',
    });

    const result = await executor.runDimension({
      providerId: 'p', modelId: 'm', identityId: 'vendor/model', dimension: 'structuredOutput',
      onSample: () => {},
    });

    assert.equal(result.status, 'insufficient_evidence');
    assert.equal(result.score, null);
    const stored = db.prepare(`SELECT COUNT(*) n FROM model_identity_scores
      WHERE identity_id='vendor/model' AND dimension='structuredOutput' AND status='scored'`)
      .get() as unknown as { n: number };
    assert.equal(stored.n, 0, 'a partial dimension is never published as scored');
    db.close();
  });

  test('refuses to call a provider it has no credential for', async () => {
    const db = seed();
    let called = false;
    const executor = createEvaluationExecutor(db, {
      credential: () => null,
      transport: () => async () => {
        called = true;
        throw new Error('the transport must never be reached');
      },
    });
    const result = await executor.runDimension({
      providerId: 'p', modelId: 'm', identityId: 'vendor/model', dimension: 'structuredOutput',
      onSample: () => {},
    });
    assert.equal(result.status, 'insufficient_evidence');
    assert.equal(called, false);
    db.close();
  });
});
