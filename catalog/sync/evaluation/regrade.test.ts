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
  /**
   * The whole stored choice, when a test needs a shape `answer` cannot express.
   * `finish_reason` lives here, not on the message, which is why this option
   * takes the choice rather than the message.
   */
  choice?: (index: number) => Record<string, unknown>;
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
        response: written <= keep
          ? { choices: [options.choice?.(index) ?? { message: { content: options.answer(index) } }] }
          : undefined,
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

function scoreOf(db: Db, identityId: string): { score: number | null; evidence: string } {
  const row = db.prepare(`SELECT score, evidence_json FROM model_identity_scores
    WHERE identity_id=? AND dimension='reasoning'`).get(identityId) as unknown as
    { score: number | null; evidence_json: string };
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
    assert.ok((after.score ?? 0) > 20, `expected the repaired grader to raise 20, got ${after.score}`);
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
      {
        identityId: 'vendor/partial', dimension: 'reasoning', retained: 59, samples: 60,
        reason: 'responses_not_retained', demoted: false,
      },
    ]);
    assert.equal(scoreOf(db, 'vendor/partial').score, 20, 'the stored score is left exactly as it was');
    db.close();
  });

  test('a wrong answer stays wrong: this corrects the reading, not the result', () => {
    const db = openDb(':memory:');
    seedRun(db, { identityId: 'vendor/wrong', answer: () => 'I am not sure.', storedScore: 90 });

    regradeFromRetainedResponses({ db, now, dimension: 'reasoning' });

    assert.ok((scoreOf(db, 'vendor/wrong').score ?? 100) < 10,
      'replaying evidence must not flatter a model that did not answer');
    db.close();
  });

  test('refuses a corpus the provider never finished answering', () => {
    const db = openDb(':memory:');
    // What `opencode-go/hy3` actually returned 174 times out of 180: the whole
    // output budget spent inside the trace, `finish_reason: 'length'`, and no
    // answer. Replaying that would republish a truncation as a measurement.
    seedRun(db, {
      identityId: 'vendor/truncated', answer: latexAnswer, storedScore: 1.99,
      choice: () => ({
        finish_reason: 'length',
        message: { role: 'assistant', content: '', reasoning_content: 'The user wants me to' },
      }),
    });

    const summary = regradeFromRetainedResponses({ db, now, dimension: 'reasoning' });

    assert.equal(summary.rescored.length, 0);
    assert.deepEqual(summary.unreplayable, [
      {
        identityId: 'vendor/truncated', dimension: 'reasoning', retained: 60, samples: 60,
        reason: 'answer_truncated', demoted: true,
      },
    ]);
    assert.equal(scoreOf(db, 'vendor/truncated').score, null,
      'a number derived from a response nobody finished is withdrawn, not corrected');
    assert.match(scoreOf(db, 'vendor/truncated').evidence, /withdrawn:answer-truncated/,
      'and the row says so, rather than disappearing');
    db.close();
  });

  test('a withdrawal is not repeated, and does not report a second time', () => {
    const db = openDb(':memory:');
    seedRun(db, {
      identityId: 'vendor/truncated', answer: latexAnswer, storedScore: 1.99,
      choice: () => ({
        finish_reason: 'length',
        message: { role: 'assistant', content: '', reasoning_content: 'The user wants me to' },
      }),
    });

    regradeFromRetainedResponses({ db, now, dimension: 'reasoning' });
    const second = regradeFromRetainedResponses({ db, now, dimension: 'reasoning' });

    assert.equal(second.unreplayable.length, 1, 'it is still reported as unreplayable');
    assert.equal(second.unreplayable[0].demoted, false, 'but nothing was withdrawn the second time');
    db.close();
  });

  test('a dry run reports the withdrawal it would make without making it', () => {
    const db = openDb(':memory:');
    seedRun(db, {
      identityId: 'vendor/truncated', answer: latexAnswer, storedScore: 1.99,
      choice: () => ({
        finish_reason: 'length',
        message: { role: 'assistant', content: '', reasoning_content: 'The user wants me to' },
      }),
    });

    const summary = regradeFromRetainedResponses({ db, now, dimension: 'reasoning', dryRun: true });

    assert.equal(summary.unreplayable[0].demoted, true, 'it reports what it would do');
    assert.equal(scoreOf(db, 'vendor/truncated').score, 1.99, 'and the stored score is untouched');
    db.close();
  });

  test('refuses a partial answer the provider was cut off mid-way through', () => {
    const db = openDb(':memory:');
    // `gpt-oss:120b` began its answer and was cut at the cap. `content` is not
    // empty, so the older check passed it straight to the grader, which scored
    // the fragment as a wrong answer.
    seedRun(db, {
      identityId: 'vendor/partial-answer', answer: latexAnswer, storedScore: 30,
      choice: () => ({
        finish_reason: 'length',
        message: { role: 'assistant', content: '{ "first": "alpha-01", "sec' },
      }),
    });

    const summary = regradeFromRetainedResponses({ db, now, dimension: 'reasoning' });

    assert.equal(summary.rescored.length, 0);
    assert.equal(summary.unreplayable[0].reason, 'answer_truncated');
    assert.equal(scoreOf(db, 'vendor/partial-answer').score, null, 'withdrawn, not re-scored from a fragment');
    db.close();
  });

  test('a cut-off answer that still scored full marks is evidence', () => {
    const db = openDb(':memory:');
    // Cut off AFTER answering: the interruption cost nothing, so refusing it
    // would throw away a paid measurement for no gain.
    seedRun(db, {
      identityId: 'vendor/cut-after-answer', answer: latexAnswer, storedScore: 20,
      choice: (index) => ({
        finish_reason: 'length',
        message: { role: 'assistant', content: `${latexAnswer(index)}

And here is some extra prose that ran` },
      }),
    });

    const summary = regradeFromRetainedResponses({ db, now, dimension: 'reasoning' });

    assert.equal(summary.unreplayable.length, 0);
    assert.ok((scoreOf(db, 'vendor/cut-after-answer').score ?? 0) > 20);
    db.close();
  });

  test('replays a trace the gateway filed under its own name', () => {
    const db = openDb(':memory:');
    // A finished answer plus a trace under `reasoning_content`. 1,800 retained
    // samples look like this, and the grader could not read a single one.
    seedRun(db, {
      identityId: 'vendor/trace', answer: latexAnswer, storedScore: 20,
      choice: (index) => ({
        finish_reason: 'stop',
        message: { role: 'assistant', content: '', reasoning_content: latexAnswer(index) },
      }),
    });

    const summary = regradeFromRetainedResponses({ db, now, dimension: 'reasoning' });

    assert.equal(summary.unreplayable.length, 0, 'a worked answer in the trace is still an answer');
    assert.ok((scoreOf(db, 'vendor/trace').score ?? 0) > 20);
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
