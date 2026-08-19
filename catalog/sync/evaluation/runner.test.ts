import assert from 'node:assert/strict';
import { describe, test } from 'node:test';
import { openDb } from '../../db/index.ts';
import { createEvaluationRepository } from './repository.ts';
import { persistDimensionEvaluation } from './runner.ts';
import { buildEvaluationFixtures, fixtureDigest } from './fixtures.ts';
import type { EvaluationTransport } from './transport.ts';

describe('persisted quality evaluation runner', () => {
  test('resumes only failed provider samples in the same dimension run', async () => {
    const db = openDb(':memory:');
    db.prepare(`INSERT INTO providers (id,name,roster_url) VALUES ('p','P','https://p.test')`).run();
    db.prepare(`INSERT INTO models (provider_id,model_id,status,first_seen_at,last_seen_at)
      VALUES ('p','m','active','2026-08-19','2026-08-19')`).run();
    const fixtures = buildEvaluationFixtures().reasoning;
    const responses = new Map(fixtures.map((item) => [JSON.stringify(item.payload), item.expectedResponse]));
    let calls = 0;
    let failedOnce = false;
    const transport: EvaluationTransport = async (payload) => {
      calls++;
      if (!failedOnce && calls === 62) {
        failedOnce = true;
        return { kind: 'provider_failure', status: 429, attempts: 4, errorCode: 'http_429' };
      }
      return { kind: 'success', response: { status: 200, body: responses.get(JSON.stringify(payload)), headers: {} }, attempts: 1 };
    };
    const input = {
      db, providerId: 'p', modelId: 'm', identityId: 'vendor/model', dimension: 'reasoning' as const,
      scenarios: fixtures, transport, credential: 'secret', testSetHash: fixtureDigest(buildEvaluationFixtures()),
      now: () => '2026-08-19T00:00:00.000Z',
    };

    const first = await persistDimensionEvaluation(input);
    const callsAfterFirstRun = calls;
    const second = await persistDimensionEvaluation(input);

    assert.equal(first.status, 'insufficient_evidence');
    assert.equal(second.status, 'complete');
    assert.equal(calls - callsAfterFirstRun, 4, 'three warmups plus the single failed sample');
    assert.equal((db.prepare('SELECT COUNT(*) n FROM evaluation_runs').get() as unknown as { n: number }).n, 1);
    assert.equal((db.prepare('SELECT COUNT(*) n FROM evaluation_samples').get() as unknown as { n: number }).n, 60);
  });

  test('persists one run, 60 samples and an identity score only after completion', async () => {
    const db = openDb(':memory:');
    db.prepare(`INSERT INTO providers (id,name,roster_url) VALUES ('p','P','https://p.test')`).run();
    db.prepare(`INSERT INTO models (provider_id,model_id,status,first_seen_at,last_seen_at)
      VALUES ('p','m','active','2026-08-19','2026-08-19')`).run();
    const fixtures = buildEvaluationFixtures().reasoning;
    const responses = new Map(fixtures.map((item) => [JSON.stringify(item.payload), item.expectedResponse]));
    const transport: EvaluationTransport = async (payload) => ({
      kind: 'success', response: { status: 200, body: responses.get(JSON.stringify(payload)), headers: {} }, attempts: 1,
    });

    const result = await persistDimensionEvaluation({
      db, providerId: 'p', modelId: 'm', identityId: 'vendor/model', dimension: 'reasoning',
      scenarios: fixtures, transport, credential: 'secret', testSetHash: fixtureDigest(buildEvaluationFixtures()),
      now: () => '2026-08-19T00:00:00.000Z',
    });

    assert.equal(result.status, 'complete');
    assert.equal((db.prepare('SELECT COUNT(*) n FROM evaluation_runs').get() as unknown as { n: number }).n, 1);
    assert.equal((db.prepare('SELECT COUNT(*) n FROM evaluation_samples').get() as unknown as { n: number }).n, 60);
    const score = createEvaluationRepository(db).identityDimensions('vendor/model')[0];
    assert.equal(score.dimension, 'reasoning');
    assert.equal(score.sampleCount, 300);
    assert.ok(score.score !== null && score.score < 100);
  });
});

describe('re-evaluating a dimension after a grader repair', () => {
  test('a fresh run refuses to inherit samples that a superseded grader scored', async () => {
    const db = openDb(':memory:');
    db.prepare(`INSERT INTO providers (id,name,roster_url) VALUES ('p','P','https://p.test')`).run();
    db.prepare(`INSERT INTO models (provider_id,model_id,status,first_seen_at,last_seen_at)
      VALUES ('p','m','active','2026-08-19','2026-08-19')`).run();
    const fixtures = buildEvaluationFixtures().reasoning;
    const responses = new Map(fixtures.map((item) => [JSON.stringify(item.payload), item.expectedResponse]));
    let calls = 0;
    let poisoned = true;
    const transport: EvaluationTransport = async (payload) => {
      calls++;
      // The first run ends short of a score, so it stays resumable.
      if (poisoned && calls === 62) return { kind: 'provider_failure', status: 500, attempts: 4, errorCode: 'http_500' };
      return { kind: 'success', response: { status: 200, body: responses.get(JSON.stringify(payload)), headers: {} }, attempts: 1 };
    };
    const input = {
      db, providerId: 'p', modelId: 'm', identityId: 'vendor/model', dimension: 'reasoning' as const,
      scenarios: fixtures, transport, credential: 'secret', testSetHash: fixtureDigest(buildEvaluationFixtures()),
      now: () => '2026-08-19T00:00:00.000Z',
    };

    assert.equal((await persistDimensionEvaluation(input)).status, 'insufficient_evidence');
    poisoned = false;
    const callsBeforeFresh = calls;

    const fresh = await persistDimensionEvaluation({ ...input, fresh: true });

    assert.equal(fresh.status, 'complete');
    // Every sample re-executed: 3 warmups + 20 scenarios x 3 repetitions. Reusing
    // even one stored sample would carry a grade the repaired grader never produced.
    assert.equal(calls - callsBeforeFresh, 63);
    assert.equal((db.prepare('SELECT COUNT(*) n FROM evaluation_runs').get() as unknown as { n: number }).n, 2);
  });

  test('a later resume attaches to the fresh run, not to the superseded one', async () => {
    const db = openDb(':memory:');
    db.prepare(`INSERT INTO providers (id,name,roster_url) VALUES ('p','P','https://p.test')`).run();
    db.prepare(`INSERT INTO models (provider_id,model_id,status,first_seen_at,last_seen_at)
      VALUES ('p','m','active','2026-08-19','2026-08-19')`).run();
    const fixtures = buildEvaluationFixtures().reasoning;
    const responses = new Map(fixtures.map((item) => [JSON.stringify(item.payload), item.expectedResponse]));
    let calls = 0;
    let failAt: number | null = 62;
    const transport: EvaluationTransport = async (payload) => {
      calls++;
      if (failAt !== null && calls === failAt) return { kind: 'provider_failure', status: 500, attempts: 4, errorCode: 'http_500' };
      return { kind: 'success', response: { status: 200, body: responses.get(JSON.stringify(payload)), headers: {} }, attempts: 1 };
    };
    const input = {
      db, providerId: 'p', modelId: 'm', identityId: 'vendor/model', dimension: 'reasoning' as const,
      scenarios: fixtures, transport, credential: 'secret', testSetHash: fixtureDigest(buildEvaluationFixtures()),
      now: () => '2026-08-19T00:00:00.000Z',
    };

    await persistDimensionEvaluation(input);
    failAt = 125;
    await persistDimensionEvaluation({ ...input, fresh: true });
    failAt = null;
    const callsBeforeResume = calls;

    const resumed = await persistDimensionEvaluation(input);

    assert.equal(resumed.status, 'complete');
    assert.equal(calls - callsBeforeResume, 4, 'three warmups plus the one sample the fresh run lost');
    assert.equal((db.prepare('SELECT COUNT(*) n FROM evaluation_runs').get() as unknown as { n: number }).n, 2);
  });
});

describe('retaining what the provider actually answered', () => {
  test('stores the redacted response body so a grader repair never costs a second paid run', async () => {
    const db = openDb(':memory:');
    db.prepare(`INSERT INTO providers (id,name,roster_url) VALUES ('p','P','https://p.test')`).run();
    db.prepare(`INSERT INTO models (provider_id,model_id,status,first_seen_at,last_seen_at)
      VALUES ('p','m','active','2026-08-19','2026-08-19')`).run();
    const fixtures = buildEvaluationFixtures().structuredOutput;
    const transport: EvaluationTransport = async () => ({
      kind: 'success',
      attempts: 1,
      response: {
        status: 200,
        headers: {},
        body: { choices: [{ message: { content: '{"recordId": 1, "approved": true, "label": "catalog-1"}' } }], key: 'secret' },
      },
    });

    await persistDimensionEvaluation({
      db, providerId: 'p', modelId: 'm', identityId: 'vendor/model', dimension: 'structuredOutput',
      scenarios: fixtures, transport, credential: 'secret', testSetHash: fixtureDigest(buildEvaluationFixtures()),
      now: () => '2026-08-19T00:00:00.000Z',
    });

    const stored = db.prepare(`SELECT response_json FROM evaluation_samples
      WHERE scenario_id='structured-output-01' AND repetition=1`).get() as unknown as { response_json: string | null };
    assert.ok(stored.response_json, 'the answer must be retained, not just its verdict');
    const body = JSON.parse(stored.response_json);
    // Re-gradable offline: this is exactly what the grader reads.
    assert.deepEqual(
      fixtures[0].grade(body),
      { weightedSuccesses: 5, weightedCriteria: 5 },
    );
    assert.equal(body.key, '[REDACTED]', 'the credential must never reach a stored row');
  });
});
