import assert from 'node:assert/strict';
import { describe, test } from 'node:test';
import { openDb } from '../../db/index.ts';
import { createEvaluationRepository } from './repository.ts';
import { persistDimensionEvaluation } from './runner.ts';
import { buildEvaluationFixtures, fixtureDigest } from './fixtures.ts';
import type { EvaluationTransport } from './transport.ts';

describe('persisted quality evaluation runner', () => {
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
