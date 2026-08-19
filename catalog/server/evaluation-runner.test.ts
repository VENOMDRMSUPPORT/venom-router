import assert from 'node:assert/strict';
import { describe, test } from 'node:test';
import { openDb, type Db } from '../db/index.ts';
import { buildEvaluationFixtures, fixtureDigest } from '../sync/evaluation/fixtures.ts';
import { OVERALL_SCORE_POLICY } from '../sync/evaluation/score.ts';
import { EvaluationRunner, type EvaluationJobExecutor } from './evaluation-runner.ts';

const HASH = fixtureDigest(buildEvaluationFixtures());

function seed(models = ['m1', 'm2'], withIdentity = true): Db {
  const db = openDb(':memory:');
  db.prepare(`INSERT INTO providers (id,name,roster_url) VALUES ('p','P','https://p.test')`).run();
  for (const modelId of models) {
    db.prepare(`INSERT INTO models (provider_id,model_id,status,first_seen_at,last_seen_at)
      VALUES ('p',?,'active','2026-08-20','2026-08-20')`).run(modelId);
    if (withIdentity) {
      db.prepare(`INSERT INTO model_scores
        (provider_id,model_id,kind,value,evidence_level,source_model_id,methodology_ver,computed_at)
        VALUES ('p',?,'VQ',50,'measured',?,?,'2026-08-20T00:00:00.000Z')`)
        .run(modelId, `vendor/${modelId}`, OVERALL_SCORE_POLICY.methodologyVersion);
    }
  }
  return db;
}

function recordingExecutor(log: string[], hooks: { onDimension?: () => void } = {}): EvaluationJobExecutor {
  return {
    async runDimension({ modelId, dimension, onSample }) {
      log.push(`dimension:${modelId}:${dimension}`);
      onSample(1, 60);
      hooks.onDimension?.();
      return { status: 'complete', score: 90 };
    },
    async runSpeed({ modelId }) {
      log.push(`speed:${modelId}`);
      return { status: 'complete' };
    },
    recalculate() {
      log.push('recalculate');
    },
  };
}

describe('EvaluationRunner', () => {
  test('runs speed after every quality dimension, then recalculates once', async () => {
    const db = seed(['m1']);
    const log: string[] = [];
    const runner = new EvaluationRunner({ db, executor: recordingExecutor(log), testSetHash: HASH, hasCredential: () => true });
    runner.enqueue('p', 'm1');
    await runner.idle;
    assert.equal(log.filter((entry) => entry.startsWith('dimension:')).length, 6);
    assert.equal(log[log.length - 2], 'speed:m1');
    assert.equal(log[log.length - 1], 'recalculate');
    assert.equal(log.filter((entry) => entry === 'recalculate').length, 1);
    db.close();
  });

  test('runs queued jobs one at a time, in order', async () => {
    const db = seed();
    const log: string[] = [];
    let concurrent = 0;
    let maxConcurrent = 0;
    const executor: EvaluationJobExecutor = {
      async runDimension({ modelId, dimension }) {
        concurrent++;
        maxConcurrent = Math.max(maxConcurrent, concurrent);
        await new Promise((resolve) => setTimeout(resolve, 1));
        log.push(`${modelId}:${dimension}`);
        concurrent--;
        return { status: 'complete', score: 90 };
      },
      async runSpeed() { return { status: 'complete' }; },
      recalculate() {},
    };
    const runner = new EvaluationRunner({ db, executor, testSetHash: HASH, hasCredential: () => true });
    runner.enqueue('p', 'm1');
    runner.enqueue('p', 'm2');
    await runner.idle;
    assert.equal(maxConcurrent, 1, 'one worker, never two');
    assert.ok(log.indexOf('m1:vision') < log.indexOf('m2:coding'), 'the first job finishes before the second starts');
    db.close();
  });

  test('reports its position in the queue, counting the job in flight', async () => {
    const db = seed();
    const runner = new EvaluationRunner({ db, executor: recordingExecutor([]), testSetHash: HASH, hasCredential: () => true });
    const first = runner.enqueue('p', 'm1');
    const second = runner.enqueue('p', 'm2');
    assert.equal(first.accepted && first.position, 1);
    assert.equal(second.accepted && second.position, 2);
    // The worker keeps running after the assertions; closing the database under
    // it turns a passing test into an unhandled rejection.
    await runner.idle;
    db.close();
  });

  test('refuses to queue the same offer twice', () => {
    const db = seed();
    const runner = new EvaluationRunner({ db, executor: recordingExecutor([]), testSetHash: HASH, hasCredential: () => true });
    assert.equal(runner.enqueue('p', 'm1').accepted, true);
    const second = runner.enqueue('p', 'm1');
    assert.equal(second.accepted, false);
    assert.equal(second.accepted === false && second.reason, 'already_queued');
    db.close();
  });

  test('refuses a blocked plan without ever calling the executor', async () => {
    const db = seed(['m1'], false);
    const log: string[] = [];
    const runner = new EvaluationRunner({ db, executor: recordingExecutor(log), testSetHash: HASH, hasCredential: () => true });
    const outcome = runner.enqueue('p', 'm1');
    assert.equal(outcome.accepted, false);
    assert.equal(outcome.accepted === false && outcome.reason, 'blocked');
    assert.equal(outcome.plan.blocked, 'identity_unresolved');
    await runner.idle;
    assert.deepEqual(log, [], 'a refusal costs nothing');
    db.close();
  });

  test('stop halts between dimensions and empties the queue', async () => {
    const db = seed();
    const log: string[] = [];
    let runner!: EvaluationRunner;
    const executor = recordingExecutor(log, { onDimension: () => runner.stop() });
    runner = new EvaluationRunner({ db, executor, testSetHash: HASH, hasCredential: () => true });
    runner.enqueue('p', 'm1');
    runner.enqueue('p', 'm2');
    await runner.idle;

    assert.equal(
      log.filter((entry) => entry.startsWith('dimension:')).length, 1,
      'the dimension in flight finishes; the next one never starts',
    );
    assert.ok(!log.some((entry) => entry.startsWith('speed:')), 'a stopped job does not measure speed');
    assert.ok(!log.some((entry) => entry.includes('m2')), 'the queue is cleared');
    assert.equal(runner.state.state, 'idle');
    assert.deepEqual(runner.state.queue, []);
    assert.equal(runner.state.recent[0].outcome, 'stopped');
    db.close();
  });

  test('stopping when nothing runs is not an error', () => {
    const db = seed();
    const runner = new EvaluationRunner({ db, executor: recordingExecutor([]), testSetHash: HASH, hasCredential: () => true });
    assert.deepEqual(runner.stop(), { stopped: false, cleared: 0 });
    db.close();
  });

  test('reports live sample progress while a dimension runs', async () => {
    const db = seed(['m1']);
    const seen: Array<{ dimension: string | null; samplesCompleted: number; samplesTotal: number }> = [];
    let runner!: EvaluationRunner;
    const executor: EvaluationJobExecutor = {
      async runDimension({ onSample }) {
        onSample(17, 60);
        const current = runner.state.current!;
        seen.push({
          dimension: current.dimension,
          samplesCompleted: current.samplesCompleted,
          samplesTotal: current.samplesTotal,
        });
        return { status: 'complete', score: 90 };
      },
      async runSpeed() { return { status: 'complete' }; },
      recalculate() {},
    };
    runner = new EvaluationRunner({ db, executor, testSetHash: HASH, hasCredential: () => true });
    runner.enqueue('p', 'm1');
    await runner.idle;
    assert.deepEqual(seen[0], { dimension: 'coding', samplesCompleted: 17, samplesTotal: 60 });
    db.close();
  });

  test('keeps at most ten finished jobs in recent', async () => {
    const db = seed(Array.from({ length: 12 }, (_, index) => `m${index}`));
    const runner = new EvaluationRunner({ db, executor: recordingExecutor([]), testSetHash: HASH, hasCredential: () => true });
    for (let index = 0; index < 12; index++) runner.enqueue('p', `m${index}`);
    await runner.idle;
    assert.equal(runner.state.recent.length, 10);
    assert.equal(runner.state.recent[0].modelId, 'm11', 'newest first');
    db.close();
  });
});
