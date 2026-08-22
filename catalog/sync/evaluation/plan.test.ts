import assert from 'node:assert/strict';
import { describe, test } from 'node:test';
import { openDb, type Db } from '../../db/index.ts';
import { buildEvaluationFixtures, fixtureDigest } from './fixtures.ts';
import { createEvaluationRepository } from './repository.ts';
import { OVERALL_SCORE_POLICY } from './score.ts';
import { planEvaluation } from './plan.ts';

const HASH = fixtureDigest(buildEvaluationFixtures());
const yes = () => true;
const no = () => false;

const REQUESTS_PER_DIMENSION =
  OVERALL_SCORE_POLICY.scenarioCount * OVERALL_SCORE_POLICY.repetitions + OVERALL_SCORE_POLICY.warmupRequests;
const REQUESTS_PER_SPEED_RUN = OVERALL_SCORE_POLICY.scenarioCount + OVERALL_SCORE_POLICY.warmupRequests;

function seedModel(withIdentity = true): Db {
  const db = openDb(':memory:');
  db.prepare(`INSERT INTO providers (id,name,roster_url) VALUES ('p','P','https://p.test')`).run();
  db.prepare(`INSERT INTO models (provider_id,model_id,status,first_seen_at,last_seen_at)
    VALUES ('p','m','active','2026-08-20','2026-08-20')`).run();
  if (withIdentity) {
    db.prepare(`INSERT INTO model_scores
      (provider_id,model_id,kind,value,evidence_level,source_model_id,methodology_ver,computed_at)
      VALUES ('p','m','VQ',50,'measured','vendor/model',?,'2026-08-20T00:00:00.000Z')`)
      .run(OVERALL_SCORE_POLICY.methodologyVersion);
  }
  return db;
}

function markScored(db: Db, dimension: string, testSetHash: string): void {
  createEvaluationRepository(db).saveIdentityDimension({
    identityId: 'vendor/model',
    dimension,
    score: 90,
    rawRate: 0.9,
    uncertainty: 1,
    confidence: 0.99,
    sampleCount: 300,
    status: 'scored',
    rubricVersion: OVERALL_SCORE_POLICY.rubricVersion,
    testSetHash,
    evidence: [],
    evaluatedAt: '2026-08-20T00:00:00.000Z',
    methodologyVersion: OVERALL_SCORE_POLICY.methodologyVersion,
  });
}

describe('planEvaluation', () => {
  test('plans every quality dimension plus speed for an untouched model', () => {
    const db = seedModel();
    const plan = planEvaluation(db, { providerId: 'p', modelId: 'm', testSetHash: HASH, hasCredential: yes });
    assert.equal(plan.identityId, 'vendor/model');
    assert.equal(plan.blocked, null);
    assert.equal(plan.dimensions.length, 6);
    assert.equal(plan.speed, 'missing');
    assert.equal(plan.estimatedRequests, 6 * REQUESTS_PER_DIMENSION + REQUESTS_PER_SPEED_RUN);
    db.close();
  });

  test('skips a dimension already scored against the current test set', () => {
    const db = seedModel();
    markScored(db, 'coding', HASH);
    const plan = planEvaluation(db, { providerId: 'p', modelId: 'm', testSetHash: HASH, hasCredential: yes });
    assert.ok(!plan.dimensions.includes('coding'));
    assert.deepEqual(
      plan.skipped.find((entry) => entry.dimension === 'coding'),
      { dimension: 'coding', reason: 'already_scored' },
    );
    assert.equal(plan.estimatedRequests, 5 * REQUESTS_PER_DIMENSION + REQUESTS_PER_SPEED_RUN);
    db.close();
  });

  test('re-plans a dimension scored against a superseded test set', () => {
    const db = seedModel();
    markScored(db, 'coding', 'a-digest-from-an-older-corpus');
    const plan = planEvaluation(db, { providerId: 'p', modelId: 'm', testSetHash: HASH, hasCredential: yes });
    assert.ok(plan.dimensions.includes('coding'), 'evidence from another corpus is not evidence for this one');
    db.close();
  });

  test('force re-plans a scored dimension', () => {
    const db = seedModel();
    markScored(db, 'coding', HASH);
    const plan = planEvaluation(db, {
      providerId: 'p', modelId: 'm', testSetHash: HASH, force: true, hasCredential: yes,
    });
    assert.ok(plan.dimensions.includes('coding'));
    db.close();
  });

  test('excludes a dimension the offer reports as unsupported', () => {
    const db = seedModel();
    db.prepare(`INSERT INTO provider_model_scores (provider_id,model_id,dimension,status,methodology_ver)
      VALUES ('p','m','vision','unsupported',?)`).run(OVERALL_SCORE_POLICY.methodologyVersion);
    const plan = planEvaluation(db, { providerId: 'p', modelId: 'm', testSetHash: HASH, hasCredential: yes });
    assert.ok(!plan.dimensions.includes('vision'));
    assert.deepEqual(
      plan.skipped.find((entry) => entry.dimension === 'vision'),
      { dimension: 'vision', reason: 'unsupported' },
    );
    db.close();
  });

  test('unsupported outranks force: a capability the offer lacks is never planned', () => {
    const db = seedModel();
    db.prepare(`INSERT INTO provider_model_scores (provider_id,model_id,dimension,status,methodology_ver)
      VALUES ('p','m','vision','unsupported',?)`).run(OVERALL_SCORE_POLICY.methodologyVersion);
    const plan = planEvaluation(db, {
      providerId: 'p', modelId: 'm', testSetHash: HASH, force: true, hasCredential: yes,
    });
    assert.ok(!plan.dimensions.includes('vision'));
    db.close();
  });

  test('leaves speed out once it is scored', () => {
    const db = seedModel();
    db.prepare(`INSERT INTO provider_model_scores (provider_id,model_id,dimension,score,status,methodology_ver)
      VALUES ('p','m','speed',70,'scored',?)`).run(OVERALL_SCORE_POLICY.methodologyVersion);
    const plan = planEvaluation(db, { providerId: 'p', modelId: 'm', testSetHash: HASH, hasCredential: yes });
    assert.equal(plan.speed, 'scored');
    assert.equal(plan.estimatedRequests, 6 * REQUESTS_PER_DIMENSION);
    db.close();
  });

  test('blocks, and costs nothing, when the model is unknown', () => {
    const db = seedModel();
    const plan = planEvaluation(db, { providerId: 'p', modelId: 'absent', testSetHash: HASH, hasCredential: yes });
    assert.equal(plan.blocked, 'model_not_found');
    assert.equal(plan.estimatedRequests, 0);
    assert.deepEqual(plan.dimensions, []);
    db.close();
  });

  test('blocks when no identity resolves', () => {
    const db = seedModel(false);
    const plan = planEvaluation(db, { providerId: 'p', modelId: 'm', testSetHash: HASH, hasCredential: yes });
    assert.equal(plan.blocked, 'identity_unresolved');
    assert.equal(plan.estimatedRequests, 0);
    db.close();
  });

  test('queues a provider-scoped identity without inventing a benchmark id', () => {
    const db = seedModel(false);
    db.prepare(`INSERT INTO model_facts (provider_id,model_id,field,value,source,resolved_at)
      VALUES ('p','m','evaluationIdentity',?,'reviewed_source','2026-08-20')`)
      .run(JSON.stringify({ id: 'p/m', kind: 'provider_scoped', consent: 'not_required' }));
    const plan = planEvaluation(db, { providerId: 'p', modelId: 'm', testSetHash: HASH, hasCredential: yes });
    assert.equal(plan.identityId, 'p/m');
    assert.equal(plan.blocked, null);
    db.close();
  });

  test('blocks a contributor identity until reviewed consent is granted', () => {
    const db = seedModel(false);
    db.prepare(`INSERT INTO model_facts (provider_id,model_id,field,value,source,resolved_at)
      VALUES ('p','m','evaluationIdentity',?,'reviewed_source','2026-08-20')`)
      .run(JSON.stringify({ id: 'meta/muse-spark-1.2', kind: 'benchmark', consent: 'required' }));
    const plan = planEvaluation(db, { providerId: 'p', modelId: 'm', testSetHash: HASH, hasCredential: yes });
    assert.equal(plan.blocked, 'consent_required');
    assert.equal(plan.estimatedRequests, 0);
    db.close();
  });

  test('blocks when the provider has no credential', () => {
    const db = seedModel();
    const plan = planEvaluation(db, { providerId: 'p', modelId: 'm', testSetHash: HASH, hasCredential: no });
    assert.equal(plan.blocked, 'missing_credentials');
    assert.equal(plan.estimatedRequests, 0);
    db.close();
  });

  test('pins the cost of a full plan to a concrete number', () => {
    const db = seedModel();
    const plan = planEvaluation(db, { providerId: 'p', modelId: 'm', testSetHash: HASH, hasCredential: yes });
    // Deliberately a literal. Re-deriving it from the same constants production
    // uses would pass even if the formula itself were wrong; 6 dimensions of
    // 20 x 3 plus 3 warmups, then a speed run of 20 plus 3 warmups, is 401.
    assert.equal(plan.estimatedRequests, 401);
    db.close();
  });
});
