# Catalog Live Evaluation Control Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the owner evaluate one model from the Venom Catalog dashboard, watch every dimension progress live, and stop it — instead of running a blind terminal script that also writes to a database the service already owns.

**Architecture:** The rule that decides which dimensions to run moves out of `scripts/run-overall-evaluations.ts` into one pure unit both the script and the service call. The service gains a single-worker FIFO job runner shaped after the existing `SyncRunner`, three additive routes, and a polled progress modal. Provider credentials stay server-side.

**Tech Stack:** TypeScript, Node 22 `node:sqlite`, `node --test`, React 19, Vitest, Testing Library, Vite.

**Spec:** `docs/superpowers/specs/2026-08-20-catalog-live-evaluation-control-design.md`

## Global Constraints

- Scope is `catalog/` only. Do not edit Venom Router, Venom Lite, Venom Pro, Venom Max, or `Design_System/`.
- Every repository file is English-only. No Arabic in code, comments, tests, fixtures, or commit messages.
- All production behaviour is introduced through a failing test first.
- No test may contact a real provider. The transport and the job executor are injected in every test.
- Do not modify `sync/evaluation/fixtures.ts` payloads. `fixtureDigest` feeds `testSetHash`, and changing it invalidates every stored score. The pinned-digest test in `sync/evaluation/fixtures.test.ts` guards this.
- Do not change `OVERALL_SCORE_POLICY.scenarioCount` (20), `repetitions` (3), `warmupRequests` (3), `speedProviderConcurrency` (2), or any score anchor.
- Speed runs last in a job and never while a quality dimension is in flight. This is a measurement condition.
- `task gate` does not cover `catalog/`. Verify with `npm test` and `npm run typecheck` inside `catalog/`, and never claim gate-verified.
- Fail closed: an unresolved identity or an absent credential is a typed rejection that contacts no provider.

---

## File Structure

| File | Responsibility |
|---|---|
| `catalog/sync/evaluation/plan.ts` (create) | Pure: given a db and a provider/model, decide what to run and what it costs. Sole owner of that rule. |
| `catalog/sync/evaluation/plan.test.ts` (create) | Tests for the above. |
| `catalog/scripts/run-overall-evaluations.ts` (modify) | Calls `planEvaluation` instead of its inline copy; refuses to start while the service listens. |
| `catalog/server/evaluation-runner.ts` (create) | FIFO queue, one worker, cooperative stop, progress snapshot. |
| `catalog/server/evaluation-runner.test.ts` (create) | Tests for the above. |
| `catalog/server/app.ts` (modify) | Three routes; `plan` added to the existing evaluation diagnostics route. |
| `catalog/server/app.test.ts` (modify) | Route contract tests. |
| `catalog/server/read-model.ts` (modify) | `loadEvaluationDiagnostics` returns `plan`. |
| `catalog/server/index.ts` (modify) | Construct the runner and pass it in `AppDeps`. |
| `catalog/src/api/client.ts` (modify) | `startEvaluation`, `fetchEvaluationState`, `stopEvaluations`. |
| `catalog/src/api/client.test.ts` (modify) | Tests for the above. |
| `catalog/src/components/EvaluateModal/EvaluateModal.tsx` (create) | Preview / running / finished. |
| `catalog/src/components/EvaluateModal/EvaluateModal.module.css` (create) | Styles. |
| `catalog/src/components/EvaluateModal/EvaluateModal.test.tsx` (create) | Tests for the above. |
| `catalog/src/pages/ProviderPage/ProviderPage.tsx` (modify) | Per-row Evaluate button that opens the modal. |

---

### Task 1: The plan unit

**Files:**
- Create: `catalog/sync/evaluation/plan.ts`
- Create: `catalog/sync/evaluation/plan.test.ts`

**Interfaces:**
- Consumes: `createEvaluationRepository` from `./repository.ts`; `OVERALL_SCORE_POLICY`, `QUALITY_DIMENSIONS`, `type QualityDimension` from `./score.ts`; `resolveEvaluationCredential` from `./provider-transport.ts`; `type Db` from `../../db/index.ts`.
- Produces:
  ```ts
  export interface EvaluationPlan {
    providerId: string;
    modelId: string;
    identityId: string | null;
    dimensions: QualityDimension[];
    skipped: Array<{ dimension: QualityDimension; reason: 'already_scored' | 'unsupported' }>;
    speed: 'missing' | 'scored';
    blocked: null | 'model_not_found' | 'identity_unresolved' | 'missing_credentials';
    estimatedRequests: number;
  }
  export function planEvaluation(
    db: Db,
    input: { providerId: string; modelId: string; testSetHash: string; force?: boolean;
             hasCredential?: (providerId: string) => boolean },
  ): EvaluationPlan;
  ```

- [ ] **Step 1: Write the failing test**

Create `catalog/sync/evaluation/plan.test.ts`:

```ts
import assert from 'node:assert/strict';
import { describe, test } from 'node:test';
import { openDb } from '../../db/index.ts';
import { buildEvaluationFixtures, fixtureDigest } from './fixtures.ts';
import { createEvaluationRepository } from './repository.ts';
import { OVERALL_SCORE_POLICY } from './score.ts';
import { planEvaluation } from './plan.ts';

const HASH = fixtureDigest(buildEvaluationFixtures());
const yes = () => true;
const no = () => false;

function seed(): ReturnType<typeof openDb> {
  const db = openDb(':memory:');
  db.prepare(`INSERT INTO providers (id,name,roster_url) VALUES ('p','P','https://p.test')`).run();
  db.prepare(`INSERT INTO models (provider_id,model_id,status,first_seen_at,last_seen_at)
    VALUES ('p','m','active','2026-08-20','2026-08-20')`).run();
  db.prepare(`INSERT INTO model_scores (provider_id,model_id,kind,source_model_id,value,computed_at)
    VALUES ('p','m','VQ','vendor/model',50,'2026-08-20')`).run();
  return db;
}

describe('planEvaluation', () => {
  test('plans every quality dimension plus speed for an untouched model', () => {
    const db = seed();
    const plan = planEvaluation(db, { providerId: 'p', modelId: 'm', testSetHash: HASH, hasCredential: yes });
    assert.equal(plan.identityId, 'vendor/model');
    assert.equal(plan.blocked, null);
    assert.equal(plan.dimensions.length, 6);
    assert.equal(plan.speed, 'missing');
    const perDimension = OVERALL_SCORE_POLICY.scenarioCount * OVERALL_SCORE_POLICY.repetitions
      + OVERALL_SCORE_POLICY.warmupRequests;
    const speed = OVERALL_SCORE_POLICY.scenarioCount + OVERALL_SCORE_POLICY.warmupRequests;
    assert.equal(plan.estimatedRequests, 6 * perDimension + speed);
    db.close();
  });

  test('skips a dimension already scored against the current test set', () => {
    const db = seed();
    createEvaluationRepository(db).saveIdentityDimension({
      identityId: 'vendor/model', dimension: 'coding', score: 90, rawRate: 0.9, uncertainty: 1,
      confidence: 0.99, sampleCount: 300, status: 'scored', rubricVersion: OVERALL_SCORE_POLICY.rubricVersion,
      testSetHash: HASH, evidence: [], evaluatedAt: '2026-08-20', methodologyVersion: OVERALL_SCORE_POLICY.methodologyVersion,
    });
    const plan = planEvaluation(db, { providerId: 'p', modelId: 'm', testSetHash: HASH, hasCredential: yes });
    assert.ok(!plan.dimensions.includes('coding'));
    assert.deepEqual(plan.skipped.find((s) => s.dimension === 'coding'), { dimension: 'coding', reason: 'already_scored' });
    db.close();
  });

  test('force re-plans a scored dimension', () => {
    const db = seed();
    createEvaluationRepository(db).saveIdentityDimension({
      identityId: 'vendor/model', dimension: 'coding', score: 90, rawRate: 0.9, uncertainty: 1,
      confidence: 0.99, sampleCount: 300, status: 'scored', rubricVersion: OVERALL_SCORE_POLICY.rubricVersion,
      testSetHash: HASH, evidence: [], evaluatedAt: '2026-08-20', methodologyVersion: OVERALL_SCORE_POLICY.methodologyVersion,
    });
    const plan = planEvaluation(db, { providerId: 'p', modelId: 'm', testSetHash: HASH, force: true, hasCredential: yes });
    assert.ok(plan.dimensions.includes('coding'));
    db.close();
  });

  test('excludes a dimension the offer reports as unsupported', () => {
    const db = seed();
    db.prepare(`INSERT INTO provider_model_scores
      (provider_id,model_id,dimension,score,status,methodology_ver,computed_at)
      VALUES ('p','m','vision',NULL,'unsupported',?, '2026-08-20')`)
      .run(OVERALL_SCORE_POLICY.methodologyVersion);
    const plan = planEvaluation(db, { providerId: 'p', modelId: 'm', testSetHash: HASH, hasCredential: yes });
    assert.ok(!plan.dimensions.includes('vision'));
    assert.deepEqual(plan.skipped.find((s) => s.dimension === 'vision'), { dimension: 'vision', reason: 'unsupported' });
    db.close();
  });

  test('blocks, and costs nothing, when the model is unknown', () => {
    const db = seed();
    const plan = planEvaluation(db, { providerId: 'p', modelId: 'absent', testSetHash: HASH, hasCredential: yes });
    assert.equal(plan.blocked, 'model_not_found');
    assert.equal(plan.estimatedRequests, 0);
    db.close();
  });

  test('blocks when no identity resolves', () => {
    const db = openDb(':memory:');
    db.prepare(`INSERT INTO providers (id,name,roster_url) VALUES ('p','P','https://p.test')`).run();
    db.prepare(`INSERT INTO models (provider_id,model_id,status,first_seen_at,last_seen_at)
      VALUES ('p','m','active','2026-08-20','2026-08-20')`).run();
    const plan = planEvaluation(db, { providerId: 'p', modelId: 'm', testSetHash: HASH, hasCredential: yes });
    assert.equal(plan.blocked, 'identity_unresolved');
    assert.equal(plan.estimatedRequests, 0);
    db.close();
  });

  test('blocks when the provider has no credential', () => {
    const db = seed();
    const plan = planEvaluation(db, { providerId: 'p', modelId: 'm', testSetHash: HASH, hasCredential: no });
    assert.equal(plan.blocked, 'missing_credentials');
    assert.equal(plan.estimatedRequests, 0);
    db.close();
  });
});
```

- [ ] **Step 2: Run the test and watch it fail**

Run: `cd catalog && node --test "sync/evaluation/plan.test.ts"`
Expected: FAIL — `Cannot find module './plan.ts'`.

- [ ] **Step 3: Write the implementation**

Create `catalog/sync/evaluation/plan.ts`:

```ts
/**
 * What to evaluate for one offer, and what it will cost.
 *
 * This rule has exactly one home. It used to live inline in the terminal
 * script; the service needs the same decision, and a second copy of a core
 * mechanism is a defect in this repository rather than a variation. Both
 * callers import this.
 *
 * Pure: it reads the database and returns a decision. It never contacts a
 * provider and never writes.
 */
import type { Db } from '../../db/index.ts';
import { createEvaluationRepository } from './repository.ts';
import { resolveEvaluationCredential } from './provider-transport.ts';
import { OVERALL_SCORE_POLICY, QUALITY_DIMENSIONS, type QualityDimension } from './score.ts';

export interface EvaluationPlan {
  providerId: string;
  modelId: string;
  identityId: string | null;
  dimensions: QualityDimension[];
  skipped: Array<{ dimension: QualityDimension; reason: 'already_scored' | 'unsupported' }>;
  speed: 'missing' | 'scored';
  blocked: null | 'model_not_found' | 'identity_unresolved' | 'missing_credentials';
  estimatedRequests: number;
}

export interface PlanEvaluationInput {
  providerId: string;
  modelId: string;
  testSetHash: string;
  force?: boolean;
  /** Injected so tests never depend on the ambient environment. */
  hasCredential?: (providerId: string) => boolean;
}

const REQUESTS_PER_DIMENSION =
  OVERALL_SCORE_POLICY.scenarioCount * OVERALL_SCORE_POLICY.repetitions + OVERALL_SCORE_POLICY.warmupRequests;
const REQUESTS_PER_SPEED_RUN = OVERALL_SCORE_POLICY.scenarioCount + OVERALL_SCORE_POLICY.warmupRequests;

function blocked(input: PlanEvaluationInput, reason: NonNullable<EvaluationPlan['blocked']>, identityId: string | null): EvaluationPlan {
  return {
    providerId: input.providerId, modelId: input.modelId, identityId,
    dimensions: [], skipped: [], speed: 'missing', blocked: reason, estimatedRequests: 0,
  };
}

/** The offer's identity: the VQ source id, else the recorded vendor identity. */
export function resolveIdentity(db: Db, providerId: string, modelId: string): string | null {
  const row = db.prepare(`
    SELECT (SELECT source_model_id FROM model_scores s
             WHERE s.provider_id=? AND s.model_id=? AND s.kind='VQ') canonical_id,
           (SELECT value FROM model_facts f
             WHERE f.provider_id=? AND f.model_id=? AND f.field='vendorIdentity') vendor_identity_json
  `).get(providerId, modelId, providerId, modelId) as unknown as
    { canonical_id: string | null; vendor_identity_json: string | null } | undefined;
  if (!row) return null;
  if (row.canonical_id) return row.canonical_id;
  if (!row.vendor_identity_json) return null;
  try {
    const parsed = JSON.parse(row.vendor_identity_json) as unknown;
    return typeof parsed === 'string' ? parsed : null;
  } catch {
    return null;
  }
}

export function planEvaluation(db: Db, input: PlanEvaluationInput): EvaluationPlan {
  const exists = db.prepare(`SELECT 1 FROM models WHERE provider_id=? AND model_id=? AND status IN ('active','missing')`)
    .get(input.providerId, input.modelId);
  if (!exists) return blocked(input, 'model_not_found', null);

  const identityId = resolveIdentity(db, input.providerId, input.modelId);
  if (!identityId) return blocked(input, 'identity_unresolved', null);

  const hasCredential = input.hasCredential ?? ((id: string) => resolveEvaluationCredential(id) !== null);
  if (!hasCredential(input.providerId)) return blocked(input, 'missing_credentials', identityId);

  const repository = createEvaluationRepository(db);
  const scored = new Set(repository.identityDimensions(identityId)
    .filter((row) => row.status === 'scored' && row.testSetHash === input.testSetHash)
    .map((row) => row.dimension));
  const applicability = new Map(repository.offerDimensions(input.providerId, input.modelId)
    .map((row) => [row.dimension, row.status]));

  const dimensions: QualityDimension[] = [];
  const skipped: EvaluationPlan['skipped'] = [];
  for (const dimension of QUALITY_DIMENSIONS) {
    if (applicability.get(dimension) === 'unsupported') {
      skipped.push({ dimension, reason: 'unsupported' });
      continue;
    }
    if (!input.force && scored.has(dimension)) {
      skipped.push({ dimension, reason: 'already_scored' });
      continue;
    }
    dimensions.push(dimension);
  }

  const speed: EvaluationPlan['speed'] =
    !input.force && applicability.get('speed') === 'scored' ? 'scored' : 'missing';

  return {
    providerId: input.providerId, modelId: input.modelId, identityId, dimensions, skipped, speed,
    blocked: null,
    estimatedRequests: dimensions.length * REQUESTS_PER_DIMENSION
      + (speed === 'missing' ? REQUESTS_PER_SPEED_RUN : 0),
  };
}
```

- [ ] **Step 4: Run the test and watch it pass**

Run: `cd catalog && node --test "sync/evaluation/plan.test.ts"`
Expected: PASS, 7 tests.

- [ ] **Step 5: Commit**

```bash
git add catalog/sync/evaluation/plan.ts catalog/sync/evaluation/plan.test.ts
git commit -m "feat(catalog): give evaluation selection one home"
```

---

### Task 2: The script uses the plan, and refuses a second writer

**Files:**
- Modify: `catalog/scripts/run-overall-evaluations.ts`
- Create: `catalog/scripts/service-guard.ts`
- Create: `catalog/scripts/service-guard.test.ts`

**Interfaces:**
- Consumes: `planEvaluation` from Task 1.
- Produces: `export async function assertServiceNotListening(probe?: (port: number) => Promise<boolean>): Promise<void>` — throws `Error('service_is_listening')` when the catalog service holds `DEFAULT_PORT`.

- [ ] **Step 1: Write the failing test**

Create `catalog/scripts/service-guard.test.ts`:

```ts
import assert from 'node:assert/strict';
import { describe, test } from 'node:test';
import { assertServiceNotListening } from './service-guard.ts';

describe('terminal script service guard', () => {
  test('refuses to run while the service holds the port', async () => {
    await assert.rejects(
      () => assertServiceNotListening(async () => true),
      /service_is_listening/,
    );
  });

  test('allows the run when the port is free', async () => {
    await assert.doesNotReject(() => assertServiceNotListening(async () => false));
  });
});
```

- [ ] **Step 2: Run the test and watch it fail**

Run: `cd catalog && node --test "scripts/service-guard.test.ts"`
Expected: FAIL — `Cannot find module './service-guard.ts'`.

- [ ] **Step 3: Write the implementation**

Create `catalog/scripts/service-guard.ts`:

```ts
/**
 * One writer, enforced rather than remembered.
 *
 * The service owns `data/catalog.db`. A terminal batch opening its own
 * connection while the service runs is a second writer, and a guarantee that
 * depends on someone remembering is not a guarantee.
 */
import { connect } from 'node:net';
import { BIND_HOST, DEFAULT_PORT } from '../server/index.ts';

export function portIsListening(port: number): Promise<boolean> {
  return new Promise((resolve) => {
    const socket = connect({ host: BIND_HOST, port });
    const settle = (listening: boolean) => { socket.destroy(); resolve(listening); };
    socket.setTimeout(500);
    socket.once('connect', () => settle(true));
    socket.once('timeout', () => settle(false));
    socket.once('error', () => settle(false));
  });
}

export async function assertServiceNotListening(
  probe: (port: number) => Promise<boolean> = portIsListening,
): Promise<void> {
  if (await probe(DEFAULT_PORT)) {
    throw new Error(
      `service_is_listening: the catalog service holds ${BIND_HOST}:${DEFAULT_PORT} and owns the database. `
      + 'Stop it, or evaluate from the dashboard.',
    );
  }
}
```

- [ ] **Step 4: Run the test and watch it pass**

Run: `cd catalog && node --test "scripts/service-guard.test.ts"`
Expected: PASS, 2 tests.

- [ ] **Step 5: Rewire the script onto the shared plan**

In `catalog/scripts/run-overall-evaluations.ts`:

1. Add at the top of the imports:
   ```ts
   import { planEvaluation } from '../sync/evaluation/plan.ts';
   import { assertServiceNotListening } from './service-guard.ts';
   ```
2. Immediately after the argument parsing and before `const db = openDb(...)`, add:
   ```ts
   await assertServiceNotListening();
   ```
3. Replace the per-offer identity resolution, the `repository.identityDimensions` skip check, and the `applicability` lookup with a single call. Inside the `for (const offer of selected)` loop, delete the `identityOf(offer)`, `resolveEvaluationCredential`, `existing`, and `applicability` blocks and put in their place:
   ```ts
   const plan = planEvaluation(db, {
     providerId: offer.provider_id, modelId: offer.model_id, testSetHash, force,
   });
   if (plan.blocked) {
     console.log(JSON.stringify({ event: 'skip', providerId: offer.provider_id, modelId: offer.model_id, reason: plan.blocked }));
     skipped++;
     continue;
   }
   const identityId = plan.identityId!;
   if (seenIdentities.has(identityId)) continue;
   seenIdentities.add(identityId);
   for (const entry of plan.skipped) {
     console.log(JSON.stringify({ event: 'skip', identityId, dimension: entry.dimension, reason: entry.reason }));
     skipped++;
   }
   ```
4. Change the inner dimension loop header to iterate the plan, intersected with `--dimensions`:
   ```ts
   for (const dimension of plan.dimensions.filter((d) => dimensionFilter.includes(d))) {
   ```
   and delete the now-dead `shouldSkipDimension` and `applicability.get(dimension) === 'unsupported'` guards inside it.
5. Delete the now-unused `identityOf` function and the `shouldSkipDimension` import.
6. `credential` is still needed for the transport. Replace its former definition with:
   ```ts
   const credential = resolveEvaluationCredential(offer.provider_id)!;
   ```
   placed after the `plan.blocked` guard — the plan already proved it exists.

- [ ] **Step 6: Delete the superseded selection unit and its test**

`scripts/evaluation-selection.ts` and `scripts/evaluation-selection.test.ts` are now a second copy of a rule that has one home. Remove both.

```bash
git rm catalog/scripts/evaluation-selection.ts catalog/scripts/evaluation-selection.test.ts
```

- [ ] **Step 7: Run the full backend suite**

Run: `cd catalog && npm run test:backend`
Expected: PASS, with the two `evaluation-selection` tests gone and the nine new ones present.

- [ ] **Step 8: Commit**

```bash
git add catalog/scripts catalog/sync
git commit -m "refactor(catalog): the terminal batch shares one selection rule and refuses a second writer"
```

---

### Task 3: The job runner

**Files:**
- Create: `catalog/server/evaluation-runner.ts`
- Create: `catalog/server/evaluation-runner.test.ts`

**Interfaces:**
- Consumes: `planEvaluation`, `type EvaluationPlan` from Task 1.
- Produces:
  ```ts
  export interface EvaluationJobExecutor {
    runDimension(input: { providerId: string; modelId: string; identityId: string;
                          dimension: QualityDimension;
                          onSample: (completed: number, total: number) => void })
      : Promise<{ status: 'complete' | 'insufficient_evidence'; score: number | null }>;
    runSpeed(input: { providerId: string; modelId: string }): Promise<{ status: 'complete' | 'insufficient_evidence' }>;
    recalculate(): void;
  }
  export interface EvaluationState { /* the GET /v1/evaluations body, see spec §5 */ }
  export class EvaluationRunner {
    constructor(config: { db: Db; executor: EvaluationJobExecutor; testSetHash: string; now?: () => Date });
    enqueue(providerId: string, modelId: string): { accepted: true; position: number; plan: EvaluationPlan }
                                                | { accepted: false; reason: 'already_queued' | 'blocked'; plan: EvaluationPlan };
    stop(): { stopped: boolean; cleared: number };
    get state(): EvaluationState;
    /** Resolves when the queue drains. Tests await it; the service does not. */
    get idle(): Promise<void>;
  }
  ```

- [ ] **Step 1: Write the failing test**

Create `catalog/server/evaluation-runner.test.ts`:

```ts
import assert from 'node:assert/strict';
import { describe, test } from 'node:test';
import { openDb } from '../db/index.ts';
import { buildEvaluationFixtures, fixtureDigest } from '../sync/evaluation/fixtures.ts';
import { EvaluationRunner, type EvaluationJobExecutor } from './evaluation-runner.ts';

const HASH = fixtureDigest(buildEvaluationFixtures());

function seed() {
  const db = openDb(':memory:');
  db.prepare(`INSERT INTO providers (id,name,roster_url) VALUES ('p','P','https://p.test')`).run();
  for (const modelId of ['m1', 'm2']) {
    db.prepare(`INSERT INTO models (provider_id,model_id,status,first_seen_at,last_seen_at)
      VALUES ('p',?, 'active','2026-08-20','2026-08-20')`).run(modelId);
    db.prepare(`INSERT INTO model_scores (provider_id,model_id,kind,source_model_id,value,computed_at)
      VALUES ('p',?, 'VQ',?,50,'2026-08-20')`).run(modelId, `vendor/${modelId}`);
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
    recalculate() { log.push('recalculate'); },
  };
}

describe('EvaluationRunner', () => {
  test('runs speed after every quality dimension of the same job', async () => {
    const db = seed();
    const log: string[] = [];
    const runner = new EvaluationRunner({ db, executor: recordingExecutor(log), testSetHash: HASH });
    runner.enqueue('p', 'm1');
    await runner.idle;
    assert.equal(log.filter((entry) => entry.startsWith('dimension:')).length, 6);
    assert.equal(log[log.length - 2], 'speed:m1');
    assert.equal(log[log.length - 1], 'recalculate');
    db.close();
  });

  test('runs queued jobs one at a time, in order', async () => {
    const db = seed();
    const log: string[] = [];
    let concurrent = 0;
    let maxConcurrent = 0;
    const executor: EvaluationJobExecutor = {
      async runDimension({ modelId, dimension }) {
        concurrent++; maxConcurrent = Math.max(maxConcurrent, concurrent);
        await new Promise((resolve) => setTimeout(resolve, 1));
        log.push(`${modelId}:${dimension}`);
        concurrent--;
        return { status: 'complete', score: 90 };
      },
      async runSpeed() { return { status: 'complete' }; },
      recalculate() {},
    };
    const runner = new EvaluationRunner({ db, executor, testSetHash: HASH });
    runner.enqueue('p', 'm1');
    runner.enqueue('p', 'm2');
    await runner.idle;
    assert.equal(maxConcurrent, 1);
    assert.ok(log.indexOf('m1:vision') < log.indexOf('m2:coding'));
    db.close();
  });

  test('refuses to queue the same offer twice', () => {
    const db = seed();
    const runner = new EvaluationRunner({ db, executor: recordingExecutor([]), testSetHash: HASH });
    assert.equal(runner.enqueue('p', 'm1').accepted, true);
    const second = runner.enqueue('p', 'm1');
    assert.equal(second.accepted, false);
    assert.equal(second.accepted === false && second.reason, 'already_queued');
    db.close();
  });

  test('refuses a blocked plan without ever calling the executor', async () => {
    const db = openDb(':memory:');
    db.prepare(`INSERT INTO providers (id,name,roster_url) VALUES ('p','P','https://p.test')`).run();
    db.prepare(`INSERT INTO models (provider_id,model_id,status,first_seen_at,last_seen_at)
      VALUES ('p','m1','active','2026-08-20','2026-08-20')`).run();
    const log: string[] = [];
    const runner = new EvaluationRunner({ db, executor: recordingExecutor(log), testSetHash: HASH });
    const outcome = runner.enqueue('p', 'm1');
    assert.equal(outcome.accepted, false);
    assert.equal(outcome.accepted === false && outcome.reason, 'blocked');
    assert.equal(outcome.plan.blocked, 'identity_unresolved');
    assert.deepEqual(log, []);
    db.close();
  });

  test('stop halts between dimensions and empties the queue', async () => {
    const db = seed();
    const log: string[] = [];
    let runner!: EvaluationRunner;
    const executor = recordingExecutor(log, { onDimension: () => runner.stop() });
    runner = new EvaluationRunner({ db, executor, testSetHash: HASH });
    runner.enqueue('p', 'm1');
    runner.enqueue('p', 'm2');
    await runner.idle;
    assert.equal(log.filter((entry) => entry.startsWith('dimension:')).length, 1,
      'the in-flight dimension finishes, the next one never starts');
    assert.ok(!log.some((entry) => entry.startsWith('speed:')), 'a stopped job does not measure speed');
    assert.ok(!log.some((entry) => entry.includes('m2')), 'the queue is cleared');
    assert.equal(runner.state.state, 'idle');
    assert.deepEqual(runner.state.queue, []);
    db.close();
  });

  test('reports live progress while a dimension runs', async () => {
    const db = seed();
    const seen: Array<{ dimension: string; samplesCompleted: number }> = [];
    const executor: EvaluationJobExecutor = {
      async runDimension({ dimension, onSample }) {
        onSample(17, 60);
        seen.push({ dimension, samplesCompleted: runner.state.current!.samplesCompleted });
        return { status: 'complete', score: 90 };
      },
      async runSpeed() { return { status: 'complete' }; },
      recalculate() {},
    };
    const runner = new EvaluationRunner({ db, executor, testSetHash: HASH });
    runner.enqueue('p', 'm1');
    await runner.idle;
    assert.equal(seen[0].samplesCompleted, 17);
    assert.equal(seen[0].dimension, 'coding');
    db.close();
  });
});
```

- [ ] **Step 2: Run the test and watch it fail**

Run: `cd catalog && node --test "server/evaluation-runner.test.ts"`
Expected: FAIL — `Cannot find module './evaluation-runner.ts'`.

- [ ] **Step 3: Write the implementation**

Create `catalog/server/evaluation-runner.ts`:

```ts
/**
 * The service's evaluation queue.
 *
 * Shaped after SyncRunner deliberately: the service should own one concurrency
 * pattern, not two. One worker, a FIFO queue held in memory, and a cooperative
 * stop.
 *
 * The queue is not persisted. A restart mid-job loses the queue but no
 * evidence: samples are written as they complete, so the next job for that
 * model resumes the run. A persistent queue would add a schema and a recovery
 * path for a case that costs one click to redo.
 */
import type { Db } from '../db/index.ts';
import { planEvaluation, type EvaluationPlan } from '../sync/evaluation/plan.ts';
import type { QualityDimension } from '../sync/evaluation/score.ts';

export interface EvaluationJobExecutor {
  runDimension(input: {
    providerId: string; modelId: string; identityId: string; dimension: QualityDimension;
    onSample: (completed: number, total: number) => void;
  }): Promise<{ status: 'complete' | 'insufficient_evidence'; score: number | null }>;
  runSpeed(input: { providerId: string; modelId: string }): Promise<{ status: 'complete' | 'insufficient_evidence' }>;
  recalculate(): void;
}

export interface EvaluationCurrent {
  providerId: string;
  modelId: string;
  identityId: string;
  dimension: QualityDimension | 'speed' | null;
  samplesCompleted: number;
  samplesTotal: number;
  dimensionsCompleted: Array<{ dimension: string; score: number | null; status: string }>;
  dimensionsRemaining: string[];
  startedAt: string;
}

export interface EvaluationState {
  state: 'idle' | 'running' | 'stopping';
  current: EvaluationCurrent | null;
  queue: Array<{ providerId: string; modelId: string }>;
  recent: Array<{ providerId: string; modelId: string; finishedAt: string; outcome: string }>;
}

const RECENT_LIMIT = 10;

export class EvaluationRunner {
  private readonly db: Db;
  private readonly executor: EvaluationJobExecutor;
  private readonly testSetHash: string;
  private readonly clock: () => Date;
  private queue: Array<{ providerId: string; modelId: string }> = [];
  private current: EvaluationCurrent | null = null;
  private recent: EvaluationState['recent'] = [];
  private stopping = false;
  private working: Promise<void> | null = null;

  constructor(config: { db: Db; executor: EvaluationJobExecutor; testSetHash: string; now?: () => Date }) {
    this.db = config.db;
    this.executor = config.executor;
    this.testSetHash = config.testSetHash;
    this.clock = config.now ?? (() => new Date());
  }

  get state(): EvaluationState {
    return {
      state: this.stopping ? 'stopping' : this.current ? 'running' : 'idle',
      current: this.current,
      queue: [...this.queue],
      recent: [...this.recent],
    };
  }

  get idle(): Promise<void> {
    return this.working ?? Promise.resolve();
  }

  enqueue(providerId: string, modelId: string):
    | { accepted: true; position: number; plan: EvaluationPlan }
    | { accepted: false; reason: 'already_queued' | 'blocked'; plan: EvaluationPlan } {
    const plan = planEvaluation(this.db, { providerId, modelId, testSetHash: this.testSetHash });
    if (plan.blocked) return { accepted: false, reason: 'blocked', plan };
    const queued = this.queue.some((job) => job.providerId === providerId && job.modelId === modelId);
    const active = this.current?.providerId === providerId && this.current?.modelId === modelId;
    if (queued || active) return { accepted: false, reason: 'already_queued', plan };

    this.queue.push({ providerId, modelId });
    if (!this.working) this.working = this.drain().finally(() => { this.working = null; });
    return { accepted: true, position: this.queue.length, plan };
  }

  stop(): { stopped: boolean; cleared: number } {
    const cleared = this.queue.length;
    this.queue = [];
    const stopped = this.current !== null;
    if (stopped) this.stopping = true;
    return { stopped, cleared };
  }

  private async drain(): Promise<void> {
    while (this.queue.length > 0) {
      const job = this.queue.shift()!;
      await this.runJob(job.providerId, job.modelId);
      if (this.stopping) break;
    }
    this.stopping = false;
    this.current = null;
  }

  private async runJob(providerId: string, modelId: string): Promise<void> {
    // Re-plan at start: a sibling offer sharing this identity may have been
    // scored while this job waited in the queue.
    const plan = planEvaluation(this.db, { providerId, modelId, testSetHash: this.testSetHash });
    const startedAt = this.clock().toISOString();
    if (plan.blocked) {
      this.remember(providerId, modelId, plan.blocked);
      return;
    }
    this.current = {
      providerId, modelId, identityId: plan.identityId!, dimension: null,
      samplesCompleted: 0, samplesTotal: 0,
      dimensionsCompleted: [],
      dimensionsRemaining: [...plan.dimensions, ...(plan.speed === 'missing' ? ['speed'] : [])],
      startedAt,
    };

    for (const dimension of plan.dimensions) {
      if (this.stopping) { this.remember(providerId, modelId, 'stopped'); return; }
      this.current.dimension = dimension;
      this.current.samplesCompleted = 0;
      this.current.samplesTotal = 0;
      const result = await this.executor.runDimension({
        providerId, modelId, identityId: plan.identityId!, dimension,
        onSample: (completed, total) => {
          if (!this.current) return;
          this.current.samplesCompleted = completed;
          this.current.samplesTotal = total;
        },
      });
      this.current.dimensionsCompleted.push({ dimension, score: result.score, status: result.status });
      this.current.dimensionsRemaining = this.current.dimensionsRemaining.filter((entry) => entry !== dimension);
    }

    if (this.stopping) { this.remember(providerId, modelId, 'stopped'); return; }

    if (plan.speed === 'missing') {
      // Last, and alone. Speed measures latency and throughput, so it must not
      // share the connection with quality traffic.
      this.current.dimension = 'speed';
      const result = await this.executor.runSpeed({ providerId, modelId });
      this.current.dimensionsCompleted.push({ dimension: 'speed', score: null, status: result.status });
      this.current.dimensionsRemaining = this.current.dimensionsRemaining.filter((entry) => entry !== 'speed');
    }

    this.executor.recalculate();
    this.remember(providerId, modelId, 'complete');
  }

  private remember(providerId: string, modelId: string, outcome: string): void {
    this.recent.unshift({ providerId, modelId, finishedAt: this.clock().toISOString(), outcome });
    this.recent = this.recent.slice(0, RECENT_LIMIT);
    this.current = null;
  }
}
```

- [ ] **Step 4: Run the test and watch it pass**

Run: `cd catalog && node --test "server/evaluation-runner.test.ts"`
Expected: PASS, 6 tests.

- [ ] **Step 5: Commit**

```bash
git add catalog/server/evaluation-runner.ts catalog/server/evaluation-runner.test.ts
git commit -m "feat(catalog): queue evaluations in the service, one worker, stoppable"
```

---

### Task 4: The real executor

**Files:**
- Create: `catalog/server/evaluation-executor.ts`
- Create: `catalog/server/evaluation-executor.test.ts`

**Interfaces:**
- Consumes: `EvaluationJobExecutor` from Task 3; `persistDimensionEvaluation` from `../sync/evaluation/runner.ts`; `persistSpeedEvaluation` from `../sync/evaluation/speed-runner.ts`; `createEvaluationTransport`, `resolveEvaluationCredential` from `../sync/evaluation/provider-transport.ts`; `createStreamingSpeedProbe` from `../sync/evaluation/speed-probe.ts`; `recalculatePublishedOffers` from `../sync/evaluation/recalculate.ts`; `buildEvaluationFixtures`, `fixtureDigest` from `../sync/evaluation/fixtures.ts`.
- Produces: `export function createEvaluationExecutor(db: Db): EvaluationJobExecutor`.

- [ ] **Step 1: Write the failing test**

Create `catalog/server/evaluation-executor.test.ts`:

```ts
import assert from 'node:assert/strict';
import { describe, test } from 'node:test';
import { openDb } from '../db/index.ts';
import { buildEvaluationFixtures, fixtureDigest } from '../sync/evaluation/fixtures.ts';
import { createEvaluationExecutor } from './evaluation-executor.ts';

describe('the service-side evaluation executor', () => {
  test('persists a scored dimension and reports sample progress as it goes', async () => {
    const db = openDb(':memory:');
    db.prepare(`INSERT INTO providers (id,name,roster_url) VALUES ('p','P','https://p.test')`).run();
    db.prepare(`INSERT INTO models (provider_id,model_id,status,first_seen_at,last_seen_at)
      VALUES ('p','m','active','2026-08-20','2026-08-20')`).run();
    const fixtures = buildEvaluationFixtures().structuredOutput;
    const bodies = new Map(fixtures.map((item) => [JSON.stringify(item.payload), item.expectedResponse]));
    const executor = createEvaluationExecutor(db, {
      credential: () => 'secret',
      transport: () => async (payload: unknown) => ({
        kind: 'success' as const, attempts: 1,
        response: { status: 200, headers: {}, body: bodies.get(JSON.stringify(payload)) },
      }),
    });

    const progress: number[] = [];
    const result = await executor.runDimension({
      providerId: 'p', modelId: 'm', identityId: 'vendor/model', dimension: 'structuredOutput',
      onSample: (completed) => progress.push(completed),
    });

    assert.equal(result.status, 'complete');
    assert.ok(result.score !== null && result.score > 99);
    assert.equal(progress.length, 60, 'one progress callback per sample');
    assert.deepEqual(progress.slice(0, 3), [1, 2, 3]);
    const stored = db.prepare(`SELECT status, test_set_hash FROM model_identity_scores
      WHERE identity_id='vendor/model' AND dimension='structuredOutput'`).get() as unknown as
      { status: string; test_set_hash: string };
    assert.equal(stored.status, 'scored');
    assert.equal(stored.test_set_hash, fixtureDigest(buildEvaluationFixtures()));
    db.close();
  });
});
```

- [ ] **Step 2: Run the test and watch it fail**

Run: `cd catalog && node --test "server/evaluation-executor.test.ts"`
Expected: FAIL — `Cannot find module './evaluation-executor.ts'`.

- [ ] **Step 3: Write the implementation**

Create `catalog/server/evaluation-executor.ts`:

```ts
/**
 * The executor the queue drives: the same persistence path the terminal batch
 * uses, wrapped so progress can be reported per sample.
 *
 * The transport and the credential lookup are injectable so tests never touch a
 * provider and never read the ambient environment.
 */
import type { Db } from '../db/index.ts';
import { buildEvaluationFixtures, fixtureDigest } from '../sync/evaluation/fixtures.ts';
import { createEvaluationTransport, resolveEvaluationCredential } from '../sync/evaluation/provider-transport.ts';
import { recalculatePublishedOffers } from '../sync/evaluation/recalculate.ts';
import { persistDimensionEvaluation } from '../sync/evaluation/runner.ts';
import { createStreamingSpeedProbe } from '../sync/evaluation/speed-probe.ts';
import { persistSpeedEvaluation } from '../sync/evaluation/speed-runner.ts';
import type { EvaluationTransport } from '../sync/evaluation/transport.ts';
import type { EvaluationJobExecutor } from './evaluation-runner.ts';

export interface ExecutorOverrides {
  credential?: (providerId: string) => string | null;
  transport?: (input: { providerId: string; modelId: string; credential: string }) => EvaluationTransport;
  now?: () => string;
}

export function createEvaluationExecutor(db: Db, overrides: ExecutorOverrides = {}): EvaluationJobExecutor {
  const fixtures = buildEvaluationFixtures();
  const testSetHash = fixtureDigest(fixtures);
  const now = overrides.now ?? (() => new Date().toISOString());
  const credentialFor = overrides.credential ?? resolveEvaluationCredential;
  const transportFor = overrides.transport ?? createEvaluationTransport;

  return {
    async runDimension({ providerId, modelId, identityId, dimension, onSample }) {
      const credential = credentialFor(providerId);
      if (!credential) return { status: 'insufficient_evidence', score: null };
      const scenarios = fixtures[dimension];
      const total = scenarios.length * 3;
      let completed = 0;
      const transport = transportFor({ providerId, modelId, credential });
      const counting: EvaluationTransport = async (payload, secret) => {
        const outcome = await transport(payload, secret);
        onSample(++completed, total);
        return outcome;
      };
      const result = await persistDimensionEvaluation({
        db, providerId, modelId, identityId, dimension, scenarios,
        transport: counting, credential, testSetHash, now,
      });
      return { status: result.status, score: result.status === 'complete' ? result.score.score : null };
    },

    async runSpeed({ providerId, modelId }) {
      const credential = credentialFor(providerId);
      if (!credential) return { status: 'insufficient_evidence' };
      const result = await persistSpeedEvaluation({
        db, providerId, modelId,
        probe: createStreamingSpeedProbe({ providerId, modelId, credential }),
        now,
      });
      return { status: result.status };
    },

    recalculate() {
      recalculatePublishedOffers(db, now());
    },
  };
}
```

Note on the counting transport: the warmup requests also pass through it, so
`completed` can exceed `total` by the warmup count on the first samples. Clamp
in the callback rather than in the runner — change the counting wrapper's call
to `onSample(Math.min(++completed, total), total)`.

- [ ] **Step 4: Run the test and watch it pass**

Run: `cd catalog && node --test "server/evaluation-executor.test.ts"`
Expected: PASS. If `progress.length` is 63 rather than 60, the warmups are being
counted — apply the clamp above and count only after the warmups by resetting
`completed = 0` when `persistDimensionEvaluation` reports its first graded
sample. The assertion is the specification; make it true.

- [ ] **Step 5: Commit**

```bash
git add catalog/server/evaluation-executor.ts catalog/server/evaluation-executor.test.ts
git commit -m "feat(catalog): drive persisted evaluation from the service with per-sample progress"
```

---

### Task 5: The routes

**Files:**
- Modify: `catalog/server/app.ts`
- Modify: `catalog/server/app.test.ts`
- Modify: `catalog/server/read-model.ts`
- Modify: `catalog/server/index.ts`

**Interfaces:**
- Consumes: `EvaluationRunner` from Task 3, `createEvaluationExecutor` from Task 4, `planEvaluation` from Task 1.
- Produces: `AppDeps` gains `evaluations: EvaluationRunner`.

- [ ] **Step 1: Write the failing test**

Append to `catalog/server/app.test.ts`:

```ts
describe('evaluation control routes', () => {
  test('POST accepts a model onto the queue and answers with its plan', async () => {
    const { deps } = evaluationHarness();
    const result = await route(deps, new URL('http://x/v1/evaluations'), 'POST', { providerId: 'p', modelId: 'm' });
    assert.equal(result.status, 202);
    const body = result.body as { position: number; plan: { estimatedRequests: number } };
    assert.equal(body.position, 1);
    assert.ok(body.plan.estimatedRequests > 0);
  });

  test('POST is a conflict when the offer is already queued', async () => {
    const { deps } = evaluationHarness();
    await route(deps, new URL('http://x/v1/evaluations'), 'POST', { providerId: 'p', modelId: 'm' });
    const result = await route(deps, new URL('http://x/v1/evaluations'), 'POST', { providerId: 'p', modelId: 'm' });
    assert.equal(result.status, 409);
  });

  test('POST is 404 for an unknown model and 422 for a blocked plan', async () => {
    const { deps } = evaluationHarness();
    const missing = await route(deps, new URL('http://x/v1/evaluations'), 'POST', { providerId: 'p', modelId: 'absent' });
    assert.equal(missing.status, 404);
    const blocked = await route(deps, new URL('http://x/v1/evaluations'), 'POST', { providerId: 'p', modelId: 'unresolved' });
    assert.equal(blocked.status, 422);
    assert.equal((blocked.body as { reason: string }).reason, 'identity_unresolved');
  });

  test('GET reports the queue and DELETE clears it', async () => {
    const { deps } = evaluationHarness();
    await route(deps, new URL('http://x/v1/evaluations'), 'POST', { providerId: 'p', modelId: 'm' });
    const state = await route(deps, new URL('http://x/v1/evaluations'), 'GET');
    assert.ok(['idle', 'running'].includes((state.body as { state: string }).state));
    const cleared = await route(deps, new URL('http://x/v1/evaluations'), 'DELETE');
    assert.equal(cleared.status, 200);
    assert.ok('cleared' in (cleared.body as object));
  });

  test('the diagnostics route carries the plan the button will spend', async () => {
    const { deps } = evaluationHarness();
    const result = await route(deps, new URL('http://x/v1/models/p/m/evaluation'), 'GET');
    assert.equal(result.status, 200);
    const body = result.body as { plan: { dimensions: string[]; estimatedRequests: number } };
    assert.ok(Array.isArray(body.plan.dimensions));
    assert.ok(body.plan.estimatedRequests > 0);
  });
});
```

Add the harness above that describe block, in the same file:

```ts
function evaluationHarness() {
  const db = openDb(':memory:');
  db.prepare(`INSERT INTO providers (id,name,roster_url) VALUES ('p','P','https://p.test')`).run();
  for (const modelId of ['m', 'unresolved']) {
    db.prepare(`INSERT INTO models (provider_id,model_id,status,first_seen_at,last_seen_at)
      VALUES ('p',?, 'active','2026-08-20','2026-08-20')`).run(modelId);
  }
  db.prepare(`INSERT INTO model_scores (provider_id,model_id,kind,source_model_id,value,computed_at)
    VALUES ('p','m','VQ','vendor/m',50,'2026-08-20')`).run();
  const evaluations = new EvaluationRunner({
    db,
    testSetHash: fixtureDigest(buildEvaluationFixtures()),
    executor: {
      async runDimension() { return { status: 'complete', score: 90 }; },
      async runSpeed() { return { status: 'complete' }; },
      recalculate() {},
    },
  });
  return { db, deps: { db, runner: fakeSyncRunner(), evaluations } as unknown as AppDeps };
}
```

Reuse whatever `app.test.ts` already uses to build a `SyncRunner` stand-in for
`fakeSyncRunner()`; if it builds `deps` inline, extract that into a helper of
this name in the same edit.

- [ ] **Step 2: Run the test and watch it fail**

Run: `cd catalog && node --test "server/app.test.ts"`
Expected: FAIL — `route` takes three arguments, and `/v1/evaluations` is 404.

- [ ] **Step 3: Write the implementation**

In `catalog/server/app.ts`:

1. Extend the deps and the signature:
   ```ts
   import { EvaluationRunner } from './evaluation-runner.ts';
   import { planEvaluation } from '../sync/evaluation/plan.ts';
   import { buildEvaluationFixtures, fixtureDigest } from '../sync/evaluation/fixtures.ts';

   export interface AppDeps {
     db: Db;
     runner: SyncRunner;
     evaluations: EvaluationRunner;
     scheduler?: SchedulerHandle;
     now?: () => Date;
     startedAt?: string;
   }

   export function route(deps: AppDeps, url: URL, method: string, body?: unknown): HttpResult | Promise<HttpResult> {
   ```
2. Immediately before the final `return { status: 404, ... }`, add:
   ```ts
   if (path === '/v1/evaluations') {
     if (method === 'GET') return { status: 200, body: deps.evaluations.state };
     if (method === 'DELETE') return { status: 200, body: deps.evaluations.stop() };
     if (method === 'POST') {
       const input = (body ?? {}) as { providerId?: unknown; modelId?: unknown };
       if (typeof input.providerId !== 'string' || typeof input.modelId !== 'string') {
         return { status: 400, body: { error: 'providerId and modelId are required' } };
       }
       const outcome = deps.evaluations.enqueue(input.providerId, input.modelId);
       if (outcome.accepted) return { status: 202, body: { position: outcome.position, plan: outcome.plan } };
       if (outcome.reason === 'already_queued') {
         return { status: 409, body: { error: 'already queued', state: deps.evaluations.state.state } };
       }
       if (outcome.plan.blocked === 'model_not_found') {
         return { status: 404, body: { error: 'model not found', providerId: input.providerId, modelId: input.modelId } };
       }
       return { status: 422, body: { error: 'cannot evaluate', reason: outcome.plan.blocked } };
     }
     return { status: 405, body: { error: 'method not allowed', path } };
   }
   ```
3. In the existing `/v1/models/{p}/{m}/evaluation` handler, attach the plan:
   ```ts
   const detail = loadEvaluationDiagnostics(db, providerId, modelId);
   return detail
     ? { status: 200, body: { ...detail, plan: planEvaluation(db, {
         providerId, modelId, testSetHash: fixtureDigest(buildEvaluationFixtures()),
       }) } }
     : { status: 404, body: { error: 'model not found', providerId, modelId } };
   ```

In `catalog/server/index.ts`, construct the runner and pass it into every
`route(...)` call, and read the POST body:

```ts
import { EvaluationRunner } from './evaluation-runner.ts';
import { createEvaluationExecutor } from './evaluation-executor.ts';
import { buildEvaluationFixtures, fixtureDigest } from '../sync/evaluation/fixtures.ts';

const evaluations = new EvaluationRunner({
  db,
  executor: createEvaluationExecutor(db),
  testSetHash: fixtureDigest(buildEvaluationFixtures()),
});
```

In the request handler, buffer the body for POST before routing:

```ts
const chunks: Buffer[] = [];
for await (const chunk of req) chunks.push(chunk as Buffer);
let parsed: unknown;
if (chunks.length > 0) {
  try { parsed = JSON.parse(Buffer.concat(chunks).toString('utf8')); }
  catch { res.writeHead(400, { 'content-type': 'application/json' }); res.end(JSON.stringify({ error: 'invalid json' })); return; }
}
const result = await route({ db, runner, evaluations, scheduler }, url, req.method ?? 'GET', parsed);
```

- [ ] **Step 4: Run the tests and watch them pass**

Run: `cd catalog && npm run test:backend`
Expected: PASS. Every existing `route(deps, ...)` call site compiles because
`body` is optional.

- [ ] **Step 5: Typecheck**

Run: `cd catalog && npm run typecheck`
Expected: exit 0.

- [ ] **Step 6: Commit**

```bash
git add catalog/server
git commit -m "feat(catalog): expose the evaluation queue over the control routes"
```

---

### Task 6: The SPA client

**Files:**
- Modify: `catalog/src/api/client.ts`
- Modify: `catalog/src/api/client.test.ts`

**Interfaces:**
- Produces:
  ```ts
  export interface EvaluationPlanView {
    dimensions: string[];
    skipped: Array<{ dimension: string; reason: string }>;
    speed: 'missing' | 'scored';
    blocked: string | null;
    estimatedRequests: number;
  }
  export interface EvaluationStateView {
    state: 'idle' | 'running' | 'stopping';
    current: null | { providerId: string; modelId: string; dimension: string | null;
                      samplesCompleted: number; samplesTotal: number;
                      dimensionsCompleted: Array<{ dimension: string; score: number | null; status: string }>;
                      dimensionsRemaining: string[] };
    queue: Array<{ providerId: string; modelId: string }>;
  }
  export function fetchEvaluationPlan(providerId: string, modelId: string): Promise<EvaluationPlanView>;
  export function startEvaluation(providerId: string, modelId: string): Promise<{ ok: true } | { ok: false; status: number; reason: string }>;
  export function fetchEvaluationState(): Promise<EvaluationStateView>;
  export function stopEvaluations(): Promise<void>;
  ```

- [ ] **Step 1: Write the failing test**

Append to `catalog/src/api/client.test.ts`:

```ts
describe('evaluation control client', () => {
  test('startEvaluation reports a rejection instead of throwing', async () => {
    const original = globalThis.fetch;
    globalThis.fetch = (async () => new Response(
      JSON.stringify({ error: 'cannot evaluate', reason: 'missing_credentials' }),
      { status: 422, headers: { 'content-type': 'application/json' } },
    )) as typeof fetch;
    try {
      const outcome = await startEvaluation('p', 'm');
      assert.equal(outcome.ok, false);
      assert.equal(outcome.ok === false && outcome.reason, 'missing_credentials');
      assert.equal(outcome.ok === false && outcome.status, 422);
    } finally {
      globalThis.fetch = original;
    }
  });

  test('fetchEvaluationState returns the queue snapshot', async () => {
    const original = globalThis.fetch;
    globalThis.fetch = (async () => new Response(
      JSON.stringify({ state: 'running', current: null, queue: [{ providerId: 'p', modelId: 'm' }], recent: [] }),
      { status: 200, headers: { 'content-type': 'application/json' } },
    )) as typeof fetch;
    try {
      const state = await fetchEvaluationState();
      assert.equal(state.state, 'running');
      assert.equal(state.queue.length, 1);
    } finally {
      globalThis.fetch = original;
    }
  });
});
```

Match the assertion style already used in `client.test.ts` (Vitest `expect` if
that is what the file uses — do not introduce a second assertion library).

- [ ] **Step 2: Run the test and watch it fail**

Run: `cd catalog && npx vitest run src/api/client.test.ts`
Expected: FAIL — `startEvaluation is not exported`.

- [ ] **Step 3: Write the implementation**

Append to `catalog/src/api/client.ts`, below the existing `fetchHealth`:

```ts
export interface EvaluationPlanView {
  dimensions: string[];
  skipped: Array<{ dimension: string; reason: string }>;
  speed: 'missing' | 'scored';
  blocked: string | null;
  estimatedRequests: number;
}

export interface EvaluationStateView {
  state: 'idle' | 'running' | 'stopping';
  current: null | {
    providerId: string; modelId: string; dimension: string | null;
    samplesCompleted: number; samplesTotal: number;
    dimensionsCompleted: Array<{ dimension: string; score: number | null; status: string }>;
    dimensionsRemaining: string[];
  };
  queue: Array<{ providerId: string; modelId: string }>;
}

export async function fetchEvaluationPlan(providerId: string, modelId: string): Promise<EvaluationPlanView> {
  const res = await fetch(`${BASE}/models/${encodeURIComponent(providerId)}/${encodeURIComponent(modelId)}/evaluation`);
  if (!res.ok) throw new Error(`evaluation plan unavailable (${res.status})`);
  return ((await res.json()) as { plan: EvaluationPlanView }).plan;
}

/**
 * Rejections are values, not exceptions. A missing credential or an unresolved
 * identity is something the modal must show, not something it should crash on.
 */
export async function startEvaluation(
  providerId: string, modelId: string,
): Promise<{ ok: true } | { ok: false; status: number; reason: string }> {
  const res = await fetch(`${BASE}/evaluations`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ providerId, modelId }),
  });
  if (res.ok) return { ok: true };
  const body = (await res.json().catch(() => ({}))) as { reason?: string; error?: string };
  return { ok: false, status: res.status, reason: body.reason ?? body.error ?? `http_${res.status}` };
}

export async function fetchEvaluationState(): Promise<EvaluationStateView> {
  const res = await fetch(`${BASE}/evaluations`);
  if (!res.ok) throw new Error(`evaluation state unavailable (${res.status})`);
  return (await res.json()) as EvaluationStateView;
}

export async function stopEvaluations(): Promise<void> {
  await fetch(`${BASE}/evaluations`, { method: 'DELETE' });
}
```

- [ ] **Step 4: Run the test and watch it pass**

Run: `cd catalog && npx vitest run src/api/client.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add catalog/src/api
git commit -m "feat(catalog): add the evaluation control client"
```

---

### Task 7: The modal and the row button

**Files:**
- Create: `catalog/src/components/EvaluateModal/EvaluateModal.tsx`
- Create: `catalog/src/components/EvaluateModal/EvaluateModal.module.css`
- Create: `catalog/src/components/EvaluateModal/EvaluateModal.test.tsx`
- Modify: `catalog/src/pages/ProviderPage/ProviderPage.tsx`

**Interfaces:**
- Consumes: `fetchEvaluationPlan`, `startEvaluation`, `fetchEvaluationState`, `stopEvaluations` from Task 6; `useCatalog`'s `reload` from `../../hooks/useCatalog`.
- Produces: `export function EvaluateModal({ model, onClose }: { model: ApiModel; onClose: () => void })`.

- [ ] **Step 1: Write the failing test**

Create `catalog/src/components/EvaluateModal/EvaluateModal.test.tsx`:

```tsx
import { describe, expect, test, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { EvaluateModal } from './EvaluateModal';
import type { ApiModel } from '../../api/client';

vi.mock('../../api/client', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/client')>()),
  fetchEvaluationPlan: vi.fn(),
  startEvaluation: vi.fn(),
  fetchEvaluationState: vi.fn(),
  stopEvaluations: vi.fn(),
}));

vi.mock('../../hooks/useCatalog', () => ({
  useCatalog: () => ({ data: null, error: null, loading: false, reload: vi.fn() }),
}));

import { fetchEvaluationPlan, startEvaluation, fetchEvaluationState, stopEvaluations } from '../../api/client';

const model = { providerId: 'p', modelId: 'm' } as unknown as ApiModel;

beforeEach(() => { vi.clearAllMocks(); });
afterEach(() => { vi.useRealTimers(); });

describe('EvaluateModal', () => {
  test('shows what the click will cost before it spends anything', async () => {
    vi.mocked(fetchEvaluationPlan).mockResolvedValue({
      dimensions: ['coding', 'vision'], skipped: [], speed: 'missing', blocked: null, estimatedRequests: 149,
    });
    vi.mocked(fetchEvaluationState).mockResolvedValue({ state: 'idle', current: null, queue: [] });
    render(<EvaluateModal model={model} onClose={() => {}} />);
    expect(await screen.findByText(/149/)).toBeInTheDocument();
    expect(startEvaluation).not.toHaveBeenCalled();
  });

  test('refuses to offer a start button when the plan is blocked', async () => {
    vi.mocked(fetchEvaluationPlan).mockResolvedValue({
      dimensions: [], skipped: [], speed: 'missing', blocked: 'missing_credentials', estimatedRequests: 0,
    });
    vi.mocked(fetchEvaluationState).mockResolvedValue({ state: 'idle', current: null, queue: [] });
    render(<EvaluateModal model={model} onClose={() => {}} />);
    expect(await screen.findByText(/missing_credentials/)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /start/i })).not.toBeInTheDocument();
  });

  test('shows live sample progress for the dimension in flight', async () => {
    vi.mocked(fetchEvaluationPlan).mockResolvedValue({
      dimensions: ['coding'], skipped: [], speed: 'scored', blocked: null, estimatedRequests: 63,
    });
    vi.mocked(fetchEvaluationState).mockResolvedValue({
      state: 'running',
      current: {
        providerId: 'p', modelId: 'm', dimension: 'coding',
        samplesCompleted: 24, samplesTotal: 60,
        dimensionsCompleted: [], dimensionsRemaining: ['coding'],
      },
      queue: [],
    });
    render(<EvaluateModal model={model} onClose={() => {}} />);
    expect(await screen.findByText(/24\s*\/\s*60/)).toBeInTheDocument();
  });

  test('stop asks the service to stop', async () => {
    vi.mocked(fetchEvaluationPlan).mockResolvedValue({
      dimensions: ['coding'], skipped: [], speed: 'scored', blocked: null, estimatedRequests: 63,
    });
    vi.mocked(fetchEvaluationState).mockResolvedValue({
      state: 'running',
      current: { providerId: 'p', modelId: 'm', dimension: 'coding', samplesCompleted: 1, samplesTotal: 60,
                 dimensionsCompleted: [], dimensionsRemaining: ['coding'] },
      queue: [],
    });
    render(<EvaluateModal model={model} onClose={() => {}} />);
    await userEvent.click(await screen.findByRole('button', { name: /stop/i }));
    await waitFor(() => expect(stopEvaluations).toHaveBeenCalled());
  });
});
```

- [ ] **Step 2: Run the test and watch it fail**

Run: `cd catalog && npx vitest run src/components/EvaluateModal`
Expected: FAIL — the module does not exist.

- [ ] **Step 3: Write the implementation**

Create `catalog/src/components/EvaluateModal/EvaluateModal.tsx`. Model its
markup, focus handling and escape-to-close on the existing
`src/components/SearchModal/SearchModal.tsx`, which is this repository's modal
precedent — do not invent a second modal pattern. The component:

- On mount, calls `fetchEvaluationPlan(model.providerId, model.modelId)` and
  `fetchEvaluationState()`.
- Renders **preview** when the state has no `current` for this model: the
  dimension list, `estimatedRequests`, and a Start button. When
  `plan.blocked` is set, it renders the reason and no Start button.
- Renders **running** when `state.current` matches this model: one row per
  dimension, `samplesCompleted / samplesTotal` for the one in flight, the score
  for each completed one, and a Stop button.
- Polls `fetchEvaluationState()` every 1500 ms via `setInterval`, started only
  while the component is mounted and `state.state !== 'idle'`, and cleared in the
  effect's cleanup. It must never poll after unmount.
- On transition from running to idle, calls `reload()` from `useCatalog` so the
  table behind the modal shows the new values.

Create the CSS module alongside it, using the design tokens the sibling modules
already use. Do not introduce raw colour values.

- [ ] **Step 4: Run the test and watch it pass**

Run: `cd catalog && npx vitest run src/components/EvaluateModal`
Expected: PASS, 4 tests.

- [ ] **Step 5: Add the row button**

In `catalog/src/pages/ProviderPage/ProviderPage.tsx`:

1. `import { EvaluateModal } from '../../components/EvaluateModal/EvaluateModal';`
2. Add `const [evaluating, setEvaluating] = useState<ApiModel | null>(null);` to
   both the table view component and the grid card component that render rows.
3. In the row's action cell — beside the existing evidence toggle, which carries
   `data-testid={`evidence-toggle-${model.modelId}`}` — add:
   ```tsx
   <button
     type="button"
     className={styles.evaluateButton}
     data-testid={`evaluate-${m.modelId}`}
     aria-label={`Evaluate ${m.modelId}`}
     onClick={() => setEvaluating(m)}
   >
     Evaluate
   </button>
   ```
4. Render the modal once per view, outside the row loop:
   ```tsx
   {evaluating && <EvaluateModal model={evaluating} onClose={() => setEvaluating(null)} />}
   ```
5. Add `.evaluateButton` to `ProviderPage.module.css` following the existing
   button rules in that file.

- [ ] **Step 6: Run the SPA suite and typecheck**

Run: `cd catalog && npx vitest run && npm run typecheck`
Expected: PASS, exit 0.

- [ ] **Step 7: Commit**

```bash
git add catalog/src
git commit -m "feat(catalog): evaluate a model from the dashboard with live progress"
```

---

### Task 8: Browser acceptance and documentation

**Files:**
- Modify: `docs/superpowers/specs/2026-08-20-catalog-live-evaluation-control-design.md` (status line only)

- [ ] **Step 1: Run the whole suite**

Run: `cd catalog && npm test && npm run typecheck`
Expected: all backend and SPA tests pass, typecheck exits 0.

- [ ] **Step 2: Prepare an isolated instance**

With the live service stopped, refresh the verification copy and start it:

```bash
cd catalog
cp data/catalog.db data/verify.db
cp data/catalog.db-wal data/verify.db-wal
cp data/catalog.db-shm data/verify.db-shm
npm run serve:verify
```

In a second shell: `CATALOG_API=http://127.0.0.1:8792 PORT=5174 npx vite`

- [ ] **Step 3: Verify in a real browser**

Open `http://localhost:5174`, go to a provider page, and check all of:

- The console is clean. A green Vitest suite has passed in this repository while
  the application was blank in a browser; the suite is not the acceptance.
- The Evaluate button appears on every model row.
- Opening it shows the missing dimensions and a request count before any start.
- A model whose plan is blocked shows the reason and offers no Start button.
- Starting one model, then a second, leaves the second queued.
- Stop halts the run and the modal returns to a resting state.
- Closing the modal after a run leaves the table showing the new scores without
  a page reload.

- [ ] **Step 4: Confirm the single-writer guard**

With the service still listening, run:

```bash
cd catalog && node --env-file=.env scripts/run-overall-evaluations.ts --providers=clinepass
```

Expected: it exits with `service_is_listening` and contacts no provider.

- [ ] **Step 5: Mark the spec delivered and commit**

Change the spec's `**Status:**` line to `delivered 2026-08-20` and commit:

```bash
git add docs/superpowers/specs/2026-08-20-catalog-live-evaluation-control-design.md
git commit -m "docs(catalog): mark the live evaluation control design delivered"
```

---

## Self-Review

**Spec coverage.** §4.1 one selection rule → Task 1 and Task 2. §4.2 job runner →
Task 3. §4.3 stopping → Task 3 Step 1 stop test. §4.4 restart behaviour →
documented in the runner's header comment; nothing to build. §4.5 single writer →
Task 2 guard, verified in Task 8 Step 4. §5 HTTP contract → Task 5. §6 modal →
Task 7. §7 testing → the test step of every task plus Task 8. §8 acceptance →
Task 8.

**Type consistency.** `EvaluationPlan` is produced in Task 1 and consumed under
that name in Tasks 3 and 5. `EvaluationJobExecutor` is declared in Task 3 and
implemented in Task 4. `EvaluationStateView` in Task 6 mirrors `EvaluationState`
from Task 3 minus `recent`, which the modal does not use — deliberate, and the
route still returns it.

**Known rough edge, deliberately left in Task 4.** The counting transport also
sees the three warmup requests, so the first progress callbacks can overcount.
Step 4 of that task names the symptom, the assertion that catches it, and the
fix, rather than pretending the first draft is right.
