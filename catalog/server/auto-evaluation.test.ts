import assert from 'node:assert/strict';
import { describe, test } from 'node:test';
import { openDb } from '../db/index.ts';
import type { EvaluationPlan } from '../sync/evaluation/plan.ts';
import {
  DEFAULT_RETRY_COOLDOWN_HOURS,
  lastAttemptReader,
  MAX_REQUESTS_CEILING,
  autoEvaluate,
  autoEvaluationConfig,
  type AutoEvaluationConfig,
  type AutoEvaluationDeps,
} from './auto-evaluation.ts';

const HOUR = 60 * 60 * 1000;

function plan(overrides: Partial<EvaluationPlan> = {}): EvaluationPlan {
  const modelId = overrides.modelId ?? 'm';
  return {
    providerId: 'p',
    modelId,
    identityId: overrides.identityId ?? `vendor/${modelId}`,
    dimensions: [],
    skipped: [],
    speed: 'scored',
    blocked: null,
    estimatedRequests: 0,
    ...overrides,
  };
}

/**
 * A queue that records what it was asked to do.
 *
 * `plan` answers from a table keyed by model id, which is the point of the
 * refactor this exercises: the module under test plans through the queue, so a
 * test controls the plan without reaching for a database or the environment.
 */
function queue(plans: Record<string, EvaluationPlan>, refuse: Set<string> = new Set()) {
  const enqueued: string[] = [];
  const deps: AutoEvaluationDeps['evaluations'] = {
    plan: (providerId, modelId) => plans[modelId] ?? plan({ providerId, modelId, blocked: 'model_not_found' }),
    enqueue: (providerId, modelId) => {
      const p = plans[modelId] ?? plan({ providerId, modelId });
      if (refuse.has(modelId)) return { accepted: false, reason: 'already_queued', plan: p };
      enqueued.push(modelId);
      return { accepted: true, position: enqueued.length, plan: p };
    },
  };
  return { deps, enqueued };
}

const config = (overrides: Partial<AutoEvaluationConfig> = {}): AutoEvaluationConfig =>
  ({ enabled: true, maxRequestsPerRun: null, retryCooldownMs: 0, ...overrides });

const offers = (...modelIds: string[]) => modelIds.map((modelId) => ({ providerId: 'p', modelId }));

describe('autoEvaluationConfig', () => {
  test('automatic measurement is on unless it is explicitly turned off', () => {
    assert.equal(autoEvaluationConfig({}).enabled, true);
    assert.equal(autoEvaluationConfig({ CATALOG_AUTO_EVALUATION: 'false' }).enabled, false);
    // Anything that is not the literal opt-out leaves it on, rather than
    // treating a typo as a silent instruction to stop measuring.
    assert.equal(autoEvaluationConfig({ CATALOG_AUTO_EVALUATION: 'yes' }).enabled, true);
  });

  test('there is no request ceiling unless one is asked for', () => {
    // The default the owner asked for: the goal is a complete catalog, and a
    // ceiling that stops short of it only defers the same spend to the next run.
    assert.equal(autoEvaluationConfig({}).maxRequestsPerRun, null);
    assert.equal(autoEvaluationConfig({ CATALOG_AUTO_EVALUATION_MAX_REQUESTS: '' }).maxRequestsPerRun, null);
    // A value that does not parse is treated as absent, not as a guessed cap:
    // inventing one from a typo would stop short of a complete catalog silently.
    assert.equal(autoEvaluationConfig({ CATALOG_AUTO_EVALUATION_MAX_REQUESTS: 'lots' }).maxRequestsPerRun, null);
    assert.equal(autoEvaluationConfig({ CATALOG_AUTO_EVALUATION_MAX_REQUESTS: '500' }).maxRequestsPerRun, 500);
    assert.equal(autoEvaluationConfig({ CATALOG_AUTO_EVALUATION_MAX_REQUESTS: '0' }).maxRequestsPerRun, 0);
    assert.equal(autoEvaluationConfig({ CATALOG_AUTO_EVALUATION_MAX_REQUESTS: '-5' }).maxRequestsPerRun, 0);
    assert.equal(autoEvaluationConfig({ CATALOG_AUTO_EVALUATION_MAX_REQUESTS: '99999999' }).maxRequestsPerRun, MAX_REQUESTS_CEILING);
  });

  test('the retry cooldown defaults to a day and is tunable in hours', () => {
    assert.equal(autoEvaluationConfig({}).retryCooldownMs, DEFAULT_RETRY_COOLDOWN_HOURS * HOUR);
    assert.equal(autoEvaluationConfig({ CATALOG_AUTO_EVALUATION_RETRY_HOURS: '6' }).retryCooldownMs, 6 * HOUR);
    // Zero is a legitimate instruction: retry on every sync, no guard at all.
    assert.equal(autoEvaluationConfig({ CATALOG_AUTO_EVALUATION_RETRY_HOURS: '0' }).retryCooldownMs, 0);
  });

  test('an explicit zero budget stops the spending without turning the reporting off', () => {
    const { deps, enqueued } = queue({ a: plan({ modelId: 'a', estimatedRequests: 63 }) });
    const report = autoEvaluate({ evaluations: deps, config: config({ maxRequestsPerRun: 0 }), log: () => {} }, offers('a'));

    assert.deepEqual(enqueued, []);
    assert.equal(report.enabled, true);
    assert.deepEqual(report.skipped.map((entry) => entry.reason), ['over_budget']);
  });
});

describe('autoEvaluate', () => {
  test('the opt-out spends nothing and plans nothing', () => {
    let planned = 0;
    const report = autoEvaluate({
      evaluations: {
        plan: (providerId, modelId) => { planned += 1; return plan({ providerId, modelId }); },
        enqueue: () => assert.fail('a disabled policy must not enqueue'),
      },
      config: config({ enabled: false }),
      log: () => {},
    }, offers('a', 'b'));

    assert.equal(planned, 0);
    assert.equal(report.enabled, false);
    assert.deepEqual(report.enqueued, []);
    assert.deepEqual(report.skipped, []);
  });

  test('a blocked offer is refused with its typed reason and never queued', () => {
    const { deps, enqueued } = queue({
      secret: plan({ modelId: 'secret', blocked: 'missing_credentials', estimatedRequests: 0 }),
      consent: plan({ modelId: 'consent', blocked: 'consent_required', estimatedRequests: 0 }),
    });
    const report = autoEvaluate({ evaluations: deps, config: config(), log: () => {} }, offers('secret', 'consent'));

    assert.deepEqual(enqueued, []);
    assert.equal(report.committedRequests, 0);
    assert.deepEqual(
      report.skipped.map((entry) => [entry.modelId, entry.reason]),
      [['secret', 'missing_credentials'], ['consent', 'consent_required']],
    );
  });

  test('an offer of an already-measured identity is covered, not failed', () => {
    // The common case, and the one worth naming separately: quality belongs to
    // an identity, so a second seller listing a measured model costs nothing.
    const { deps, enqueued } = queue({ sibling: plan({ modelId: 'sibling', estimatedRequests: 0 }) });
    const report = autoEvaluate({ evaluations: deps, config: config(), log: () => {} }, offers('sibling'));

    assert.deepEqual(enqueued, []);
    assert.deepEqual(report.skipped.map((entry) => entry.reason), ['already_covered']);
  });

  test('only one newly added offer for the same canonical identity can auto-queue work', () => {
    const { deps, enqueued } = queue({
      first: plan({ modelId: 'first', identityId: 'vendor/shared', estimatedRequests: 63 }),
      sibling: plan({ modelId: 'sibling', identityId: 'vendor/shared', estimatedRequests: 63 }),
    });
    const report = autoEvaluate({ evaluations: deps, config: config(), log: () => {} }, offers('first', 'sibling'));

    assert.deepEqual(enqueued, ['first']);
    assert.deepEqual(report.skipped.map((entry) => [entry.modelId, entry.reason]), [['sibling', 'duplicate_identity']]);
  });

  test('the budget buys the cheapest offers first, and names what it could not buy', () => {
    const { deps, enqueued } = queue({
      whole: plan({ modelId: 'whole', estimatedRequests: 401 }),
      speedOnly: plan({ modelId: 'speedOnly', estimatedRequests: 23 }),
      half: plan({ modelId: 'half', estimatedRequests: 126 }),
    });
    const logged: string[] = [];
    const report = autoEvaluate(
      { evaluations: deps, config: config({ maxRequestsPerRun: 200 }), log: (message) => logged.push(message) },
      offers('whole', 'speedOnly', 'half'),
    );

    // 23 + 126 = 149 fits; the 401-request job does not and waits for the next
    // run rather than consuming the whole budget on its own.
    assert.deepEqual(enqueued, ['speedOnly', 'half']);
    assert.equal(report.committedRequests, 149);
    assert.deepEqual(report.skipped.map((entry) => [entry.modelId, entry.reason]), [['whole', 'over_budget']]);
    // A silent cap reads afterwards as "everything was measured".
    assert.ok(logged.some((message) => message.includes('budget exhausted') && message.includes('whole')));
  });

  test('a cheaper offer still fits after an expensive one was deferred', () => {
    const { deps, enqueued } = queue({
      big: plan({ modelId: 'big', estimatedRequests: 90 }),
      small: plan({ modelId: 'small', estimatedRequests: 10 }),
      medium: plan({ modelId: 'medium', estimatedRequests: 50 }),
    });
    const report = autoEvaluate({ evaluations: deps, config: config({ maxRequestsPerRun: 100 }), log: () => {} }, offers('big', 'small', 'medium'));

    assert.deepEqual(enqueued, ['small', 'medium']);
    assert.equal(report.committedRequests, 60);
    assert.deepEqual(report.skipped.map((entry) => entry.modelId), ['big']);
  });

  test('equal estimates are ordered by identity, so two runs make the same choices', () => {
    const same = (modelId: string) => plan({ modelId, estimatedRequests: 63 });
    const first = queue({ zebra: same('zebra'), apple: same('apple'), mango: same('mango') });
    autoEvaluate({ evaluations: first.deps, config: config({ maxRequestsPerRun: 126 }), log: () => {} }, offers('zebra', 'apple', 'mango'));
    const second = queue({ zebra: same('zebra'), apple: same('apple'), mango: same('mango') });
    autoEvaluate({ evaluations: second.deps, config: config({ maxRequestsPerRun: 126 }), log: () => {} }, offers('mango', 'zebra', 'apple'));

    assert.deepEqual(first.enqueued, ['apple', 'mango']);
    assert.deepEqual(first.enqueued, second.enqueued, 'input order must not change what a budget buys');
  });

  test('a queue that refuses the job does not have its cost charged to the budget', () => {
    const { deps, enqueued } = queue(
      {
        taken: plan({ modelId: 'taken', estimatedRequests: 63 }),
        fresh: plan({ modelId: 'fresh', estimatedRequests: 63 }),
      },
      new Set(['taken']),
    );
    // A budget that covers both, so the refusal is what is under test rather
    // than the cap: with room for only one, the cheaper offer would take the
    // slot and `taken` would never reach the queue at all.
    const report = autoEvaluate({ evaluations: deps, config: config({ maxRequestsPerRun: 200 }), log: () => {} }, offers('taken', 'fresh'));

    // The refusal must leave the budget intact, or an offer the queue never
    // started would cost a later one its slot.
    assert.deepEqual(enqueued, ['fresh']);
    assert.equal(report.committedRequests, 63);
    assert.deepEqual(report.skipped.map((entry) => [entry.modelId, entry.reason]), [['taken', 'already_queued']]);
  });

  test('a run that discovered nothing reports nothing and says nothing', () => {
    const logged: string[] = [];
    const { deps } = queue({});
    const report = autoEvaluate({ evaluations: deps, config: config(), log: (message) => logged.push(message) }, []);

    assert.deepEqual(report.enqueued, []);
    assert.deepEqual(report.skipped, []);
    assert.deepEqual(logged, []);
  });
});

describe('the retry cooldown', () => {
  const NOW = '2026-08-23T12:00:00.000Z';
  const clock = () => new Date(NOW);
  const ago = (hours: number) => new Date(Date.parse(NOW) - hours * HOUR).toISOString();

  test('an identity measured inside the window is left alone', () => {
    /**
     * Not hypothetical, and the reason this guard exists at all. With no request
     * ceiling, a sweep re-plans every offer on every sync — and
     * `x-ai/grok-4.5`/vision was attempted on 2026-08-23 at 09:45, returning
     * `insufficient_evidence: incomplete_valid_scenarios`, which leaves its plan
     * incomplete. Nothing else would stop a six-hourly schedule from re-buying
     * that dimension four times a day, forever, for a verdict that asking again
     * immediately does not produce.
     */
    const { deps, enqueued } = queue({ grok: plan({ modelId: 'grok', identityId: 'x-ai/grok-4.5', estimatedRequests: 63 }) });
    const logged: string[] = [];
    const report = autoEvaluate({
      evaluations: deps,
      config: config({ retryCooldownMs: 24 * HOUR }),
      lastAttemptAt: () => ago(2),
      now: clock,
      log: (message) => logged.push(message),
    }, offers('grok'));

    assert.deepEqual(enqueued, []);
    assert.equal(report.committedRequests, 0);
    assert.deepEqual(report.skipped.map((entry) => entry.reason), ['retry_cooldown']);
    // Held back, but never silently: a quiet hold reads as "already measured".
    assert.ok(logged.some((message) => message.includes('cooling down') && message.includes('grok')));
  });

  test('the same identity is retried once the window has passed', () => {
    const { deps, enqueued } = queue({ grok: plan({ modelId: 'grok', identityId: 'x-ai/grok-4.5', estimatedRequests: 63 }) });
    const report = autoEvaluate({
      evaluations: deps,
      config: config({ retryCooldownMs: 24 * HOUR }),
      lastAttemptAt: () => ago(25),
      now: clock,
      log: () => {},
    }, offers('grok'));

    assert.deepEqual(enqueued, ['grok']);
    assert.equal(report.committedRequests, 63);
  });

  test('an identity never measured is never held back', () => {
    const { deps, enqueued } = queue({ fresh: plan({ modelId: 'fresh', identityId: 'vendor/fresh', estimatedRequests: 401 }) });
    autoEvaluate({
      evaluations: deps,
      config: config({ retryCooldownMs: 24 * HOUR }),
      lastAttemptAt: () => null,
      now: clock,
      log: () => {},
    }, offers('fresh'));

    assert.deepEqual(enqueued, ['fresh']);
  });

  test('a zero cooldown retries every sync, and a caller that cannot answer is not blocked', () => {
    const build = () => queue({ a: plan({ modelId: 'a', identityId: 'vendor/a', estimatedRequests: 63 }) });

    const zero = build();
    autoEvaluate({ evaluations: zero.deps, config: config({ retryCooldownMs: 0 }), lastAttemptAt: () => ago(0.1), now: clock, log: () => {} }, offers('a'));
    assert.deepEqual(zero.enqueued, ['a'], 'a zero cooldown means no guard');

    const unanswerable = build();
    autoEvaluate({ evaluations: unanswerable.deps, config: config({ retryCooldownMs: 24 * HOUR }), now: clock, log: () => {} }, offers('a'));
    assert.deepEqual(unanswerable.enqueued, ['a'], 'no reader means no cooldown, not a silent hold');
  });
});

describe('sweeping a whole catalog', () => {
  test('a complete catalog costs nothing and says so', () => {
    // The steady state once this has caught up: every offer planned, nothing to
    // buy. It has to be cheap, and it must not read as a failure.
    const covered = ['a', 'b', 'c'].reduce((all, modelId) => ({ ...all, [modelId]: plan({ modelId, estimatedRequests: 0 }) }), {});
    const { deps, enqueued } = queue(covered);
    const logged: string[] = [];
    const report = autoEvaluate({ evaluations: deps, config: config(), log: (message) => logged.push(message) }, offers('a', 'b', 'c'));

    assert.deepEqual(enqueued, []);
    assert.equal(report.committedRequests, 0);
    assert.equal(report.skipped.filter((entry) => entry.reason === 'already_covered').length, 3);
    assert.ok(logged.some((message) => message.includes('nothing to measure')));
  });

  test('with no ceiling every incomplete offer is queued, however many there are', () => {
    const plans = Object.fromEntries(
      Array.from({ length: 12 }, (_, index) => [`m${index}`, plan({ modelId: `m${index}`, identityId: `vendor/m${index}`, estimatedRequests: 401 })]),
    );
    const { deps, enqueued } = queue(plans);
    const report = autoEvaluate({ evaluations: deps, config: config(), log: () => {} }, offers(...Object.keys(plans)));

    assert.equal(enqueued.length, 12);
    assert.equal(report.committedRequests, 12 * 401);
    assert.deepEqual(report.skipped, [], 'nothing may be deferred when there is no ceiling');
  });
});

describe('lastAttemptReader', () => {
  test('reads the newest attempt for the identity from evaluation_runs', () => {
    // Anchored to what the service actually did, rather than to a counter this
    // module would have to keep in agreement with it.
    const db = openDb(':memory:');
    try {
      // provider_id and model_id stay NULL: the table carries a composite
      // foreign key into `models`, and the reader keys on the identity anyway.
      const insert = (identityId: string, dimension: string, startedAt: string, finishedAt: string | null, status: string) =>
        db.prepare(`INSERT INTO evaluation_runs
          (identity_id, dimension, run_kind, status, evaluator_version, rubric_version,
           test_set_version, methodology_ver, region, independent_run_key, started_at, finished_at)
          VALUES (?,?,'runtime',?,'v','r','t','m','region',?,?,?)`)
          .run(identityId, dimension, status, `${identityId}:${dimension}:${startedAt}`, startedAt, finishedAt);

      insert('x-ai/grok-4.5', 'coding', '2026-08-20T03:00:00.000Z', '2026-08-20T03:10:00.000Z', 'complete');
      insert('x-ai/grok-4.5', 'vision', '2026-08-23T09:45:00.000Z', '2026-08-23T09:52:00.000Z', 'insufficient_evidence');
      insert('other/model', 'coding', '2026-08-23T11:00:00.000Z', '2026-08-23T11:05:00.000Z', 'complete');

      const read = lastAttemptReader(db);
      // A failed attempt still counts as an attempt: the cooldown exists for
      // exactly the dimension that keeps not producing a verdict.
      assert.equal(read('x-ai/grok-4.5'), '2026-08-23T09:52:00.000Z');
      assert.equal(read('other/model'), '2026-08-23T11:05:00.000Z');
      assert.equal(read('never/measured'), null);
    } finally {
      db.close();
    }
  });

  test('a run that never finished still counts, by its start time', () => {
    // A service killed mid-evaluation leaves `finished_at` null. Ignoring the
    // row would let the very next sync re-buy the dimension it was measuring.
    const db = openDb(':memory:');
    try {
      db.prepare(`INSERT INTO evaluation_runs
        (identity_id, dimension, run_kind, status, evaluator_version, rubric_version,
         test_set_version, methodology_ver, region, independent_run_key, started_at, finished_at)
        VALUES ('stranded/one','coding','runtime','running','v','r','t','m','region','stranded','2026-08-23T11:30:00.000Z',NULL)`).run();

      assert.equal(lastAttemptReader(db)('stranded/one'), '2026-08-23T11:30:00.000Z');
    } finally {
      db.close();
    }
  });
});
