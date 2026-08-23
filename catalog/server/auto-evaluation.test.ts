import assert from 'node:assert/strict';
import { describe, test } from 'node:test';
import type { EvaluationPlan } from '../sync/evaluation/plan.ts';
import {
  DEFAULT_MAX_REQUESTS_PER_RUN,
  MAX_REQUESTS_CEILING,
  autoEvaluate,
  autoEvaluationConfig,
  type AutoEvaluationConfig,
  type AutoEvaluationDeps,
} from './auto-evaluation.ts';

function plan(overrides: Partial<EvaluationPlan> = {}): EvaluationPlan {
  return {
    providerId: 'p',
    modelId: 'm',
    identityId: 'vendor/m',
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
  ({ enabled: true, maxRequestsPerRun: 1_000, ...overrides });

const offers = (...modelIds: string[]) => modelIds.map((modelId) => ({ providerId: 'p', modelId }));

describe('autoEvaluationConfig', () => {
  test('automatic measurement is on unless it is explicitly turned off', () => {
    assert.equal(autoEvaluationConfig({}).enabled, true);
    assert.equal(autoEvaluationConfig({ CATALOG_AUTO_EVALUATION: 'false' }).enabled, false);
    // Anything that is not the literal opt-out leaves it on, rather than
    // treating a typo as a silent instruction to stop measuring.
    assert.equal(autoEvaluationConfig({ CATALOG_AUTO_EVALUATION: 'yes' }).enabled, true);
  });

  test('the budget is clamped, and nonsense falls back to the default rather than to zero', () => {
    assert.equal(autoEvaluationConfig({}).maxRequestsPerRun, DEFAULT_MAX_REQUESTS_PER_RUN);
    assert.equal(autoEvaluationConfig({ CATALOG_AUTO_EVALUATION_MAX_REQUESTS: '500' }).maxRequestsPerRun, 500);
    assert.equal(autoEvaluationConfig({ CATALOG_AUTO_EVALUATION_MAX_REQUESTS: '-5' }).maxRequestsPerRun, 0);
    assert.equal(autoEvaluationConfig({ CATALOG_AUTO_EVALUATION_MAX_REQUESTS: '999999' }).maxRequestsPerRun, MAX_REQUESTS_CEILING);
    assert.equal(autoEvaluationConfig({ CATALOG_AUTO_EVALUATION_MAX_REQUESTS: 'lots' }).maxRequestsPerRun, DEFAULT_MAX_REQUESTS_PER_RUN);
  });

  test('a zero budget stops the spending without turning the reporting off', () => {
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
