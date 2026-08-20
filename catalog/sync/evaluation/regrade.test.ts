import assert from 'node:assert/strict';
import { describe, test } from 'node:test';
import { openDb, type Db } from '../../db/index.ts';
import { buildEvaluationFixtures, fixtureDigest } from './fixtures.ts';
import { createEvaluationRepository } from './repository.ts';
import { OVERALL_SCORE_POLICY } from './score.ts';
import { regradeFromRetainedResponses } from './regrade.ts';

const HASH = fixtureDigest(buildEvaluationFixtures());
const now = () => '2026-08-20T12:00:00.000Z';

/**
 * A scored reasoning run whose responses were retained.
 *
 * `answer` is what every sample's `content` said. The point of the fixture is
 * that the stored VERDICT and the stored RESPONSE can disagree — which is
 * exactly the state a grader repair leaves behind.
 */
/** The correct answer to scenario `index`, written in LaTeX the old check failed. */
const latexAnswer = (index: number) => {
  const multiplier = index + 7;
  const original = index + 3;
  const added = index + 5;
  const result = multiplier * original + added;
  return `Equation: $${multiplier}x + ${added} = ${result}$

Answer: $${original}$`;
};

function seedRun(db: Db, options: {
  identityId: string;
  /** Built per scenario, because every scenario asks about different numbers. */
  answer: (index: number) => string;
  storedScore: number;
  retainedSamples?: number;
}): void {
  const scenarios = buildEvaluationFixtures().reasoning;
  db.prepare("INSERT OR IGNORE INTO providers (id,name,roster_url) VALUES ('p','P','https://p.test')").run();
  db.prepare(`INSERT OR IGNORE INTO models (provider_id,model_id,status,first_seen_at,last_seen_at)
    VALUES ('p','m','active','2026-08-20','2026-08-20')`).run();
  const repository = createEvaluationRepository(db);
  const runId = repository.createRun({
    providerId: 'p', modelId: 'm', identityId: options.identityId, dimension: 'reasoning',
    runKind: 'runtime', status: 'complete', evaluatorVersion: OVERALL_SCORE_POLICY.evaluatorVersion,
    rubricVersion: OVERALL_SCORE_POLICY.rubricVersion, testSetVersion: OVERALL_SCORE_POLICY.testSetVersion,
    testSetHash: HASH, methodologyVersion: OVERALL_SCORE_POLICY.methodologyVersion,
    region: OVERALL_SCORE_POLICY.region, independentRunKey: `k-${options.identityId}`,
    errorCode: null, startedAt: now(), finishedAt: now(),
  });

  const keep = options.retainedSamples ?? scenarios.length * OVERALL_SCORE_POLICY.repetitions;
  let written = 0;
  for (const [index, scenario] of scenarios.entries()) {
    for (let repetition = 1; repetition <= OVERALL_SCORE_POLICY.repetitions; repetition++) {
      written++;
      repository.upsertSample({
        runId, scenarioId: scenario.id, repetition, outcome: 'passed',
        weightedSuccesses: 1, weightedCriteria: 5, metrics: null,
        artifactRef: `fixture:${HASH}#${scenario.id}`,
        response: written <= keep ? { choices: [{ message: { content: options.answer(index) } }] } : undefined,
        errorCode: null, recordedAt: now(),
      });
    }
  }

  repository.saveIdentityDimension({
    identityId: options.identityId, dimension: 'reasoning', score: options.storedScore,
    rawRate: options.storedScore / 100, uncertainty: 1, confidence: 0.99, sampleCount: 300,
    status: 'scored', rubricVersion: OVERALL_SCORE_POLICY.rubricVersion, testSetHash: HASH,
    evidence: [`run:${runId}`], evaluatedAt: now(),
    methodologyVersion: OVERALL_SCORE_POLICY.methodologyVersion,
  });
}

function scoreOf(db: Db, identityId: string): { score: number; evidence: string } {
  const row = db.prepare(`SELECT score, evidence_json FROM model_identity_scores
    WHERE identity_id=? AND dimension='reasoning'`).get(identityId) as unknown as
    { score: number; evidence_json: string };
  return { score: row.score, evidence: row.evidence_json };
}

describe('re-scoring from evidence already bought', () => {
  test('replays retained responses instead of asking the provider again', () => {
    const db = openDb(':memory:');
    // reasoning-01 is "7x + 5 = 26"; this answers it correctly, in LaTeX, which
    // the repaired criterion accepts and the old one did not.
    seedRun(db, { identityId: 'vendor/latex', answer: latexAnswer, storedScore: 20 });

    const summary = regradeFromRetainedResponses({ db, now, dimension: 'reasoning' });

    assert.equal(summary.rescored.length, 1);
    assert.equal(summary.unreplayable.length, 0);
    const after = scoreOf(db, 'vendor/latex');
    assert.ok(after.score > 20, `expected the repaired grader to raise 20, got ${after.score}`);
    assert.match(after.evidence, /regraded:retained-responses/,
      'the trail must say this was re-derived, not re-measured');
    db.close();
  });

  test('refuses a run it cannot replay in full, and names it', () => {
    const db = openDb(':memory:');
    // One sample short of complete retention: replaying 59 of 60 would publish a
    // different measurement under the same name.
    seedRun(db, {
      identityId: 'vendor/partial', answer: latexAnswer,
      storedScore: 20, retainedSamples: 59,
    });

    const summary = regradeFromRetainedResponses({ db, now, dimension: 'reasoning' });

    assert.equal(summary.rescored.length, 0);
    assert.deepEqual(summary.unreplayable, [
      { identityId: 'vendor/partial', dimension: 'reasoning', retained: 59, samples: 60 },
    ]);
    assert.equal(scoreOf(db, 'vendor/partial').score, 20, 'the stored score is left exactly as it was');
    db.close();
  });

  test('a wrong answer stays wrong: this corrects the reading, not the result', () => {
    const db = openDb(':memory:');
    seedRun(db, { identityId: 'vendor/wrong', answer: () => 'I am not sure.', storedScore: 90 });

    regradeFromRetainedResponses({ db, now, dimension: 'reasoning' });

    assert.ok(scoreOf(db, 'vendor/wrong').score < 10,
      'replaying evidence must not flatter a model that did not answer');
    db.close();
  });

  test('is idempotent, so running it twice is not a second opinion', () => {
    const db = openDb(':memory:');
    seedRun(db, { identityId: 'vendor/latex', answer: latexAnswer, storedScore: 20 });

    regradeFromRetainedResponses({ db, now, dimension: 'reasoning' });
    const first = scoreOf(db, 'vendor/latex').score;
    regradeFromRetainedResponses({ db, now, dimension: 'reasoning' });

    assert.equal(scoreOf(db, 'vendor/latex').score, first);
    db.close();
  });
});

describe('previewing a re-score', () => {
  test('a dry run reports the outcome and writes nothing', () => {
    const db = openDb(':memory:');
    seedRun(db, { identityId: 'vendor/latex', answer: latexAnswer, storedScore: 20 });

    const summary = regradeFromRetainedResponses({ db, now, dimension: 'reasoning', dryRun: true });

    assert.equal(summary.rescored.length, 1);
    assert.ok(summary.rescored[0].after > 20, 'it still reports what would happen');
    assert.equal(scoreOf(db, 'vendor/latex').score, 20, 'and the stored score is untouched');
    db.close();
  });
});
