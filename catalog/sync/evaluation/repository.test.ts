import assert from 'node:assert/strict';
import { describe, test } from 'node:test';
import { openDb } from '../../db/index.ts';
import { createEvaluationRepository } from './repository.ts';

describe('evaluation schema', () => {
  test('creates every overall-score-v1 evidence table', () => {
    const db = openDb(':memory:');
    const names = new Set(
      (db.prepare(`SELECT name FROM sqlite_master WHERE type='table'`).all() as unknown as { name: string }[])
        .map((row) => row.name),
    );
    for (const name of [
      'model_identity_scores',
      'provider_model_scores',
      'overall_model_scores',
      'evaluation_runs',
      'evaluation_samples',
      'provider_quality_overrides',
    ]) {
      assert.ok(names.has(name), `missing evaluation table: ${name}`);
    }
  });
});

describe('evaluation repository grain', () => {
  test('shares quality only through exact identity and keeps operations offer-scoped', () => {
    const db = openDb(':memory:');
    db.prepare(`INSERT INTO providers (id,name,roster_url) VALUES ('p1','P1','https://p1.test'),('p2','P2','https://p2.test')`).run();
    for (const providerId of ['p1', 'p2']) {
      db.prepare(`INSERT INTO models (provider_id,model_id,status,first_seen_at,last_seen_at) VALUES (?,?,'active','2026-08-19','2026-08-19')`)
        .run(providerId, 'glm-5.3');
    }
    const repo = createEvaluationRepository(db);
    repo.saveIdentityDimension({
      identityId: 'z-ai/glm-5.3', dimension: 'coding', score: 72, rawRate: 0.72,
      uncertainty: 4, confidence: 0.96, sampleCount: 60, status: 'scored',
      rubricVersion: 'catalog-rubrics-v1', testSetHash: 'sha256:abc', evidence: ['run:1'],
      evaluatedAt: '2026-08-19T00:00:00.000Z', methodologyVersion: 'overall-score-v1',
    });
    repo.saveOfferDimension({
      providerId: 'p1', modelId: 'glm-5.3', dimension: 'speed', score: 61,
      rawRate: 0.61, uncertainty: 5, confidence: 0.95, sampleCount: 20,
      status: 'scored', evidence: ['run:2'], evaluatedAt: '2026-08-19T00:00:00.000Z',
      methodologyVersion: 'overall-score-v1',
    });

    assert.equal(repo.identityDimensions('z-ai/glm-5.3')[0].score, 72);
    assert.equal(repo.offerDimensions('p1', 'glm-5.3')[0].score, 61);
    assert.deepEqual(repo.offerDimensions('p2', 'glm-5.3'), []);
  });

  test('persists an overall projection with structured coverage and reasons', () => {
    const db = openDb(':memory:');
    db.prepare(`INSERT INTO providers (id,name,roster_url) VALUES ('p1','P1','https://p1.test')`).run();
    db.prepare(`INSERT INTO models (provider_id,model_id,status,first_seen_at,last_seen_at) VALUES ('p1','m1','active','2026-08-19','2026-08-19')`).run();
    const repo = createEvaluationRepository(db);
    repo.saveOverall({
      providerId: 'p1', modelId: 'm1', value: null, qualityScore: null,
      operationalScore: null, qualityCoverage: { scored: 0, applicable: 5, percent: 0 },
      overallCoverage: { scored: 1, applicable: 7, percent: 100 / 7 },
      includedDimensions: ['costEfficiency'], excludedDimensions: ['vision'],
      status: 'insufficient_evidence', uncertainty: null,
      reasons: ['missing_coding_evaluation'], methodologyVersion: 'overall-score-v1',
      computedAt: '2026-08-19T00:00:00.000Z',
    });

    assert.deepEqual(repo.overall('p1', 'm1')?.reasons, ['missing_coding_evaluation']);
    assert.equal(repo.overall('p1', 'm1')?.overallCoverage.applicable, 7);
  });
});
