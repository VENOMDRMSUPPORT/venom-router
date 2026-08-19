import assert from 'node:assert/strict';
import { describe, test } from 'node:test';
import { openDb } from '../../db/index.ts';
import { createEvaluationRepository } from './repository.ts';
import { projectOfferOperationalEvidence, recalculateOfferOverall, recalculatePublishedOffers } from './recalculate.ts';
import { QUALITY_DIMENSIONS } from './score.ts';

function seeded() {
  const db = openDb(':memory:');
  db.prepare(`INSERT INTO providers (id,name,roster_url) VALUES ('p1','P1','https://p1.test')`).run();
  db.prepare(`INSERT INTO models (provider_id,model_id,status,first_seen_at,last_seen_at) VALUES ('p1','m1','active','2026-08-19','2026-08-19')`).run();
  db.prepare(`INSERT INTO model_facts (provider_id,model_id,field,value,source,resolved_at) VALUES ('p1','m1','vendorIdentity','"vendor/m1"','models.dev','2026-08-19')`).run();
  return { db, repo: createEvaluationRepository(db) };
}

describe('overall score recalculation', () => {
  test('projects applicability and free cost from exact facts without inventing task quality', () => {
    const { db, repo } = seeded();
    db.prepare(`UPDATE models SET tools=1, structured=0, attachment=0,
      input_modalities='["text"]', cost_kind='free' WHERE provider_id='p1' AND model_id='m1'`).run();

    projectOfferOperationalEvidence(db, repo, '2026-08-19T00:00:00.000Z');

    const offer = new Map(repo.offerDimensions('p1', 'm1').map((row) => [row.dimension, row]));
    assert.equal(offer.get('toolCalling')?.status, 'supported');
    assert.equal(offer.get('structuredOutput')?.status, 'unsupported');
    assert.equal(offer.get('vision')?.status, 'unsupported');
    assert.equal(offer.get('costEfficiency')?.status, 'scored');
    assert.ok((offer.get('costEfficiency')?.score ?? 100) < 100);
    assert.equal(repo.identityDimensions('vendor/m1').length, 0);
  });

  test('offer applicability excludes an unsupported dimension but preserves an identity score when supported', () => {
    const { db, repo } = seeded();
    for (const modelId of ['supported', 'unsupported']) {
      db.prepare(`INSERT INTO models (provider_id,model_id,status,tools,first_seen_at,last_seen_at)
        VALUES ('p1',?, 'active', ?, '2026-08-19','2026-08-19')`).run(modelId, modelId === 'supported' ? 1 : 0);
      db.prepare(`INSERT INTO model_facts (provider_id,model_id,field,value,source,resolved_at)
        VALUES ('p1',?,'vendorIdentity','"vendor/shared"','models.dev','2026-08-19')`).run(modelId);
    }
    repo.saveIdentityDimension({
      identityId: 'vendor/shared', dimension: 'toolCalling', score: 75, rawRate: 0.75,
      uncertainty: 2, confidence: 0.98, sampleCount: 300, status: 'scored',
      rubricVersion: 'catalog-rubrics-v1', testSetHash: 'hash', evidence: ['run:1'],
      evaluatedAt: '2026-08-19', methodologyVersion: 'overall-score-v1',
    });

    projectOfferOperationalEvidence(db, repo, '2026-08-19T00:00:00.000Z');

    const supported = recalculateOfferOverall(repo, {
      providerId: 'p1', modelId: 'supported', identityId: 'vendor/shared', computedAt: '2026-08-19',
    });
    const unsupported = recalculateOfferOverall(repo, {
      providerId: 'p1', modelId: 'unsupported', identityId: 'vendor/shared', computedAt: '2026-08-19',
    });
    assert.ok(supported.reasons.every((reason) => reason !== 'missing_toolCalling_evaluation'));
    assert.ok(unsupported.excludedDimensions.includes('toolCalling'));
  });

  test('unknown price remains unknown and never becomes a zero-cost score', () => {
    const { db, repo } = seeded();
    db.prepare(`UPDATE models SET cost_kind='unknown' WHERE provider_id='p1' AND model_id='m1'`).run();

    projectOfferOperationalEvidence(db, repo, '2026-08-19T00:00:00.000Z');

    const cost = repo.offerDimensions('p1', 'm1').find((row) => row.dimension === 'costEfficiency');
    assert.equal(cost?.status, 'unknown');
    assert.equal(cost?.score, null);
  });

  test('persists a complete 70/30 projection from exact identity and offer dimensions', () => {
    const { repo } = seeded();
    for (const dimension of QUALITY_DIMENSIONS) repo.saveIdentityDimension({
      identityId: 'vendor/m1', dimension, score: 80, rawRate: 0.8, uncertainty: 1,
      confidence: 0.99, sampleCount: 60, status: dimension === 'vision' ? 'unsupported' : 'scored',
      rubricVersion: 'catalog-rubrics-v1', testSetHash: 'hash', evidence: [],
      evaluatedAt: '2026-08-19', methodologyVersion: 'overall-score-v1',
    });
    for (const [dimension, score] of [['speed', 60], ['costEfficiency', 40]] as const) repo.saveOfferDimension({
      providerId: 'p1', modelId: 'm1', dimension, score, rawRate: score / 100,
      uncertainty: 1, confidence: 0.99, sampleCount: 60, status: 'scored', evidence: [],
      evaluatedAt: '2026-08-19', methodologyVersion: 'overall-score-v1',
    });
    const result = recalculateOfferOverall(repo, { providerId: 'p1', modelId: 'm1', identityId: 'vendor/m1', computedAt: '2026-08-19' });
    assert.equal(result.status, 'complete');
    assert.equal(result.value, 71);
    assert.equal(repo.overall('p1', 'm1')?.value, 71);
  });

  test('missing identity evidence remains insufficient instead of inheriting a family score', () => {
    const { repo } = seeded();
    repo.saveOfferDimension({
      providerId: 'p1', modelId: 'm1', dimension: 'costEfficiency', score: 90,
      rawRate: 0.9, uncertainty: 1, confidence: 0.99, sampleCount: 100,
      status: 'scored', evidence: ['pricing'], evaluatedAt: '2026-08-19',
      methodologyVersion: 'overall-score-v1',
    });
    const result = recalculateOfferOverall(repo, { providerId: 'p1', modelId: 'm1', identityId: null, computedAt: '2026-08-19' });
    assert.equal(result.status, 'insufficient_evidence');
    assert.equal(result.value, null);
    assert.ok(result.reasons.includes('identity_unresolved'));
    assert.ok(result.includedDimensions.includes('costEfficiency'));
    assert.equal(result.overallCoverage.scored, 1);
  });

  test('projects every published offer without inventing evidence', () => {
    const { db, repo } = seeded();
    const summary = recalculatePublishedOffers(db, '2026-08-19');
    assert.deepEqual(summary, { complete: 0, incomplete: 1, total: 1 });
    assert.equal(repo.overall('p1', 'm1')?.status, 'insufficient_evidence');
  });
});
