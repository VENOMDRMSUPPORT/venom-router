# Venom Catalog Overall Score v1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the reproducible `overall-score-v1` evaluation subsystem to Venom Catalog while preserving legacy `model-score-v1` and publishing honest incomplete states when evidence or credentials are absent.

**Architecture:** A pure scoring core calculates criterion, component, coverage, uncertainty, and overall results from persisted evaluation evidence. SQLite repositories own identity-level quality results, provider-offer operational results, runs/samples, and conformance overrides; server projection and React consume the server-owned `overallScore` and rank. A credential-injected runtime shell records reproducible samples but never fabricates results when it cannot call an offer.

**Tech Stack:** TypeScript, Node 22 `node:sqlite`, Node test runner, React 19, Vitest, Testing Library, Vite.

**Spec:** `docs/superpowers/specs/2026-08-19-catalog-fair-model-evaluation-design.md`

## Implementation Status (2026-08-19)

- Complete: schema/repository, pure math, rubric manifest validation, external-benchmark gate, credential-injected runtime core, retry/timeout/concurrency policy, conformance guard, recalculation, sync projection, API/snapshot/client contracts, diagnostics endpoint, global rank, and Catalog UI migration.
- Verified: 356 backend tests, 98 SPA tests, TypeScript checks, production build, fresh in-memory migration, isolated browser acceptance, and live 5174 acceptance.
- Live evidence state: 0/65 complete overall scores (`clinepass` 0/13, `ollama-cloud` 0/19, `opencode-go` 0/26, `opencode-zen` 0/7). Every row is published once as `insufficient_evidence`; no legacy VQ/VO or metadata score was relabelled as `overallScore`.
- External decision gate still required for numeric publication: reviewed prompt/expected-output fixtures matching `catalog-rubrics-v1`, plus provider credential resolvers/transports or benchmark records that pass the five acceptance checks. The runtime shell accepts these dependencies, but this repository does not contain them and therefore must not claim numeric completion.
- Intentional lifecycle boundary: evaluation incompleteness is persisted in `overall_model_scores` and evaluation evidence tables, not mixed into the five-minute `resolution_jobs` source-fact queue. The existing full/targeted pipeline recalculates overall projections after evidence changes without adding a second roster scheduler.
- Runtime continuation (2026-08-19): exact offer-level applicability and Cost Efficiency projection now populate `provider_model_scores` without creating task-quality scores. The live database has 64/65 cost dimensions scored; the remaining unknown price stays incomplete.
- The versioned `catalog-benchmark-crosswalk-v1` maps Coding, Reasoning, Long Context, Tool Calling, and Vision to pinned `inspect-evals` tasks, while Structured Output remains a deterministic Catalog-owned fixture. The importer rejects any result that is not exactly 20 samples x 3 epochs and persists all 60 outcomes separately before publishing an identity score.
- Provider preflight: OpenCode Go and ClinePass completed live inference smoke requests. Ollama Cloud authentication succeeds but the tested offer is blocked by the account plan/extra-usage requirement. OpenCode Zen accepts the corrected `-free` model id but exhausts its available quota with HTTP 429. These are provider/account outcomes, not model failures.
- Scale gate: the current 35 resolved identities require 186 applicable quality-dimension runs (11,718 requests including warmups, before offer-level Speed runs). A full GPQA 20x3 run on `opencode-go/glm-5.3` was stopped after 6/60 samples when measured throughput projected roughly 100 minutes for that single dimension. The partial log was not imported and created no score.

## Global Constraints

- Scope is `catalog/` plus the two approved docs only; do not edit Venom Router, Venom Lite, Venom Pro, Venom Max, or `Design_System/`.
- Preserve existing local changes. Do not create a branch, commit, or push.
- Keep `modelScore`/`model-score-v1`, VQ, VO, and their ranks unchanged for compatibility.
- Add `overallScore`/`overall-score-v1` beside legacy fields; new Catalog Score/rank/coverage presentation uses `overallScore`.
- Never derive task quality from metadata, model-family similarity, another provider, or an inexact identity.
- Unsupported dimensions are excluded; unknown or supported-but-unmeasured dimensions block final publication.
- Missing credentials and unknown price produce `insufficient_evidence`, never estimated values.
- No finite published dimension score may equal 0 or 100.
- All production behavior is introduced through a failing test first.

---

### Task 1: Persist Evaluation Evidence at the Correct Grain

**Files:**
- Modify: `catalog/db/schema.sql`
- Modify: `catalog/db/index.ts`
- Create: `catalog/sync/evaluation/repository.ts`
- Create: `catalog/sync/evaluation/repository.test.ts`

**Interfaces:**
- Produces `EvaluationRepository`, `IdentityDimensionRow`, `OfferDimensionRow`, `OverallScoreRow`, `EvaluationRunRow`, and `EvaluationSampleRow`.
- Every identity-quality key is `(identityId, dimension, methodologyVersion)`; every offer key is `(providerId, modelId, dimension, methodologyVersion)`.

- [ ] **Step 1: Write failing schema/repository tests**

```ts
test('quality rows are shared only by exact identity and operational rows stay offer-scoped', () => {
  const db = openDb(':memory:');
  const repo = createEvaluationRepository(db);
  repo.saveIdentityDimension(identityResult('z-ai/glm-5.3', 'coding', 72));
  repo.saveOfferDimension(offerResult('p1', 'glm-5.3', 'speed', 61));
  assert.equal(repo.identityDimensions('z-ai/glm-5.3')[0].score, 72);
  assert.equal(repo.offerDimensions('p2', 'glm-5.3').length, 0);
});
```

- [ ] **Step 2: Run the focused test and confirm missing-table/repository failure**

Run: `node --test sync/evaluation/repository.test.ts`

- [ ] **Step 3: Add tables and migration-safe indexes**

Add `model_identity_scores`, `provider_model_scores`, `overall_model_scores`, `evaluation_runs`, `evaluation_samples`, and `provider_quality_overrides`. Store included/excluded dimensions and evidence as JSON, use foreign keys for offer rows, and never store credentials or raw secrets.

- [ ] **Step 4: Implement the repository with prepared statements and JSON parsing at one boundary**

```ts
export interface EvaluationRepository {
  saveIdentityDimension(row: IdentityDimensionRow): void;
  saveOfferDimension(row: OfferDimensionRow): void;
  saveOverall(row: OverallScoreRow): void;
  identityDimensions(identityId: string): IdentityDimensionRow[];
  offerDimensions(providerId: string, modelId: string): OfferDimensionRow[];
  overall(providerId: string, modelId: string): OverallScoreRow | null;
  createRun(row: EvaluationRunRow): number;
  appendSample(row: EvaluationSampleRow): void;
}
```

- [ ] **Step 5: Run repository tests and existing DB tests**

Run: `node --test sync/evaluation/repository.test.ts server/app.test.ts`

---

### Task 2: Implement the Pure `overall-score-v1` Math

**Files:**
- Create: `catalog/sync/evaluation/policy.ts`
- Create: `catalog/sync/evaluation/score.ts`
- Create: `catalog/sync/evaluation/score.test.ts`

**Interfaces:**
- Produces `OVERALL_SCORE_POLICY`, `smoothCriterionScore`, `scoreSpeed`, `scoreCostEfficiency`, `aggregateOverallScore`, and `OverallScoreResult`.

- [ ] **Step 1: Write failing tests for smoothing, anchors, coverage, and aggregation**

```ts
test('finite perfect evidence is below 100', () => {
  assert.equal(smoothCriterionScore(60, 60).score, 98.38709677419355);
});

test('unsupported vision is excluded while unknown vision blocks completion', () => {
  assert.equal(aggregateOverallScore(completeWithoutVision()).status, 'complete');
  assert.equal(aggregateOverallScore(unknownVision()).status, 'insufficient_evidence');
});

test('overall uses 70/30 full precision', () => {
  const out = aggregateOverallScore(completeDimensions({ quality: 80, speed: 60, cost: 40 }));
  assert.equal(out.value, 71);
});
```

- [ ] **Step 2: Run and confirm imports/functions are missing**

Run: `node --test sync/evaluation/score.test.ts`

- [ ] **Step 3: Implement immutable policy constants**

```ts
export const OVERALL_SCORE_POLICY = {
  methodologyVersion: 'overall-score-v1',
  evaluatorVersion: 'catalog-eval-v1',
  rubricVersion: 'catalog-rubrics-v1',
  testSetVersion: 'catalog-testset-v1',
  qualityWeight: 0.70,
  operationalWeight: 0.30,
  warmupRequests: 3,
  scenarioCount: 20,
  repetitions: 3,
  requestTimeoutMs: 120_000,
  providerConcurrency: 2,
  transientRetries: 3,
} as const;
```

- [ ] **Step 4: Implement smoothing and uncertainty exactly from the Decision Gate**

```ts
export function smoothCriterionScore(successes: number, criteria: number): CriterionScore {
  const rawRate = successes / criteria;
  const p = (successes + 1) / (criteria + 2);
  const uncertainty = 196 * Math.sqrt((p * (1 - p)) / (criteria + 4));
  return { rawRate, score: p * 100, uncertainty, confidence: clamp(1 - uncertainty / 100, 0, 1), sampleCount: criteria };
}
```

- [ ] **Step 5: Implement Speed/Cost anchor mapping and 70/30 aggregation**

Speed uses four equal mapped metrics; cost uses the 800k/200k reference workload and `$0 -> 100`, `$50 -> 0`. Aggregate only completed applicable dimensions and propagate uncertainty by normalized root-sum-square.

- [ ] **Step 6: Run focused and all scoring tests**

Run: `node --test sync/evaluation/score.test.ts "sync/score/*.test.ts"`

---

### Task 3: Validate Rubric Manifests and External Benchmark Eligibility

**Files:**
- Create: `catalog/sync/evaluation/rubrics.ts`
- Create: `catalog/sync/evaluation/rubrics.test.ts`
- Create: `catalog/sync/evaluation/external-benchmark.ts`
- Create: `catalog/sync/evaluation/external-benchmark.test.ts`
- Create: `catalog/overlays/evaluation-rubrics.json`

**Interfaces:**
- Produces `loadRubrics`, `rubricDigest`, `assessExternalBenchmark`, and `ExternalBenchmarkDecision`.
- Does not import provider slugs or infer quality from OpenRouter/models.dev metadata.

- [ ] **Step 1: Write failing validation tests for six dimensions, 20 scenarios, five equal criteria, and digest stability**

```ts
test('v1 manifest contains exactly 20 scenarios per quality dimension', () => {
  const rubrics = loadRubrics(fixture);
  for (const dimension of QUALITY_DIMENSIONS) assert.equal(rubrics[dimension].scenarios.length, 20);
});
```

- [ ] **Step 2: Run and confirm manifest loader is missing**

Run: `node --test sync/evaluation/rubrics.test.ts`

- [ ] **Step 3: Add a canonical manifest containing scenario ids and criterion ids only**

The committed v1 manifest defines stable scenario identifiers and criterion weights. Runtime prompt/answer fixtures are loaded from reviewed evaluator assets and their SHA-256 is validated against the canonical manifest digest; the catalog does not claim a score until those reviewed assets exist.

- [ ] **Step 4: Implement strict validation and SHA-256 canonical serialization**

Reject missing/extra dimensions, unequal weights, duplicate ids, non-20 scenario counts, and changed content under the same version.

- [ ] **Step 5: Write failing external evidence acceptance tests**

Cover exact identity, compatible crosswalk, published methodology/range, sample count or CI, 180-day freshness, zero-width CI rejection, and provenance-only fallback.

- [ ] **Step 6: Implement acceptance and effective-criteria inference**

```ts
effectiveCriteria = Math.max(1, 3.8416 * p * (1 - p) / (u * u) - 4);
```

- [ ] **Step 7: Run both focused suites**

Run: `node --test sync/evaluation/rubrics.test.ts sync/evaluation/external-benchmark.test.ts`

---

### Task 4: Build the Credential-Injected Runtime Evaluation Core

**Files:**
- Create: `catalog/sync/evaluation/runtime.ts`
- Create: `catalog/sync/evaluation/runtime.test.ts`
- Create: `catalog/sync/evaluation/transport.ts`
- Create: `catalog/sync/evaluation/transport.test.ts`

**Interfaces:**
- Produces `runDimensionEvaluation`, `runSpeedEvaluation`, `EvaluationTransport`, `CredentialResolver`, and sanitized sample records.
- Consumes Task 1 repository and Task 2 policy/math.

- [ ] **Step 1: Write failing tests for warmups, 20x3 samples, concurrency 2, timeout, and retries**

```ts
test('retries only 429 and 5xx three times', async () => {
  const statuses = [429, 500, 502, 200];
  const result = await callWithPolicy(() => response(statuses.shift()!));
  assert.equal(result.status, 200);
});
```

- [ ] **Step 2: Verify focused tests fail because runtime interfaces are absent**

Run: `node --test sync/evaluation/runtime.test.ts sync/evaluation/transport.test.ts`

- [ ] **Step 3: Implement the generic transport boundary**

Use injected request construction and credential resolution; never switch on provider/model inside the evaluator core. Return typed `model_failure`, `provider_failure`, `evaluator_failure`, and `missing_credentials` outcomes.

- [ ] **Step 4: Implement deterministic scheduling and sample persistence**

Exclude three warmups, retain each repetition, cap each offer at concurrency 2, apply 120s timeout, and store artifact hashes/references only.

- [ ] **Step 5: Implement speed statistics from retained requests**

Compute median/p95 TTFT, tokens/sec, end-to-end, and success rate; exhausted provider failures affect speed success rate but not task answers.

- [ ] **Step 6: Add a secret-canary test**

Inject `VENOM_CATALOG_SECRET_CANARY` and assert it appears in no DB field, returned error, evidence JSON, artifact reference, or captured logger output.

- [ ] **Step 7: Run runtime suites**

Run: `node --test sync/evaluation/runtime.test.ts sync/evaluation/transport.test.ts`

---

### Task 5: Add Provider Conformance and Overall Recalculation

**Files:**
- Create: `catalog/sync/evaluation/conformance.ts`
- Create: `catalog/sync/evaluation/conformance.test.ts`
- Create: `catalog/sync/evaluation/recalculate.ts`
- Create: `catalog/sync/evaluation/recalculate.test.ts`

**Interfaces:**
- Produces `assessConformance`, `recalculateOfferOverall`, and `recalculateChangedOffers`.
- Consumes exact canonical identity from existing identity evidence, never fuzzy ids.

- [ ] **Step 1: Write failing tests for provisional, override, and exact identity isolation**

```ts
test('one divergent run is provisional and two independent non-overlapping runs create override', () => {
  assert.equal(assessConformance([divergentRun(1)]).state, 'provisional');
  assert.equal(assessConformance([divergentRun(1), divergentRun(2)]).state, 'override');
});
```

- [ ] **Step 2: Run and confirm conformance module is missing**

Run: `node --test sync/evaluation/conformance.test.ts`

- [ ] **Step 3: Implement the two-run, >8 point, non-overlapping-CI rule plus contract-break override**

Persist override evidence separately and leave identity scores unchanged.

- [ ] **Step 4: Write failing recalculation tests for complete/incomplete/unknown price/missing credentials**

- [ ] **Step 5: Implement offer projection and change detection**

Recalculate only when dimension score, uncertainty, applicability, conformance, or methodology changes. Emit an event when value/status changes.

- [ ] **Step 6: Run conformance and recalculation suites**

Run: `node --test sync/evaluation/conformance.test.ts sync/evaluation/recalculate.test.ts`

---

### Task 6: Integrate Evaluation Lifecycle Without Duplicating Sync

**Files:**
- Modify: `catalog/sync/pipeline.ts`
- Modify: `catalog/sync/resolution-jobs.ts`
- Modify: `catalog/sync/resolution-jobs.test.ts`
- Modify: `catalog/server/sync-runner.ts`
- Modify: `catalog/server/sync-runner.test.ts`

**Interfaces:**
- Extends the existing single full/targeted pipeline and lock; does not create a second roster or scheduler path.
- Produces durable evaluation-needed reasons and overall recalculation after evidence changes.

- [ ] **Step 1: Write failing lifecycle tests**

Assert missing credentials and unknown pricing become `insufficient_evidence`, dormant jobs do not spin, full sync wins the shared lock, and targeted passes do not fetch rosters.

- [ ] **Step 2: Run focused lifecycle tests and confirm expected failures**

Run: `node --test sync/resolution-jobs.test.ts server/sync-runner.test.ts`

- [ ] **Step 3: Extend reason vocabulary and trigger rules**

Add explicit `missing_evaluation_credentials`, `missing_cost_evidence`, `missing_<dimension>_evaluation`, and `conformance_pending` reasons while preserving existing five-minute source-resolution behavior.

- [ ] **Step 4: Wire evaluation/recalculation into the single pipeline**

Use injected evaluator dependencies. A normal sync with no credentials records eligibility and incomplete state but performs no estimated evaluation.

- [ ] **Step 5: Run lifecycle and pipeline suites**

Run: `node --test sync/resolution-jobs.test.ts server/sync-runner.test.ts sync/pipeline.test.ts`

---

### Task 7: Publish `overallScore`, Coverage, Diagnostics, and Global Rank

**Files:**
- Modify: `catalog/server/read-model.ts`
- Modify: `catalog/server/app.ts`
- Modify: `catalog/server/app.test.ts`
- Modify: `catalog/server/snapshot.ts`
- Modify: `catalog/server/snapshot.test.ts`
- Modify: `catalog/src/api/client.ts`
- Modify: `catalog/src/api/client.test.ts`

**Interfaces:**
- Adds `ApiOverallScore`, `overallRank`, `tiedAtOverallRank`, and coverage counts beside legacy fields.
- Provides a diagnostics endpoint returning dimension/evidence records without credentials or private raw responses.

- [ ] **Step 1: Write failing API tests for complete, incomplete, stale payload, and legacy coexistence**

```ts
assert.equal(model.modelScore.methodologyVersion, 'model-score-v1');
assert.equal(model.overallScore.methodologyVersion, 'overall-score-v1');
assert.equal(model.overallRank, expectedServerRank);
```

- [ ] **Step 2: Run focused server/client tests and confirm missing fields**

Run: `node --test server/app.test.ts server/snapshot.test.ts && npm run test:spa -- --run src/api/client.test.ts`

- [ ] **Step 3: Implement server projection and dense global rank**

Rank only complete numeric results by full precision; tie overlapping uncertainty intervals; unplaced rows remain null and alphabetical in provider presentation.

- [ ] **Step 4: Add diagnostics response and stale-client normalization**

Absent `overallScore` normalizes to `{ value: null, display: '—', status: 'unknown', ... }` and never reuses `modelScore`.

- [ ] **Step 5: Run focused API and snapshot suites**

Run: `node --test server/app.test.ts server/snapshot.test.ts && npm run test:spa -- --run src/api/client.test.ts`

---

### Task 8: Switch the Catalog Presentation to Overall Score

**Files:**
- Modify: `catalog/src/components/ScoreCell/ScoreCell.tsx`
- Modify: `catalog/src/components/ScoreCell/ScoreCell.test.tsx`
- Modify: `catalog/src/components/EvidencePanel/EvidencePanel.tsx`
- Modify: `catalog/src/components/EvidencePanel/EvidencePanel.test.tsx`
- Modify: `catalog/src/pages/ProviderPage/ProviderPage.tsx`
- Modify: `catalog/src/pages/ProviderPage/ProviderPage.test.tsx`
- Modify: `catalog/src/pages/DashboardPage/DashboardPage.tsx`
- Modify: `catalog/src/pages/DashboardPage/DashboardPage.test.tsx`

**Interfaces:**
- Consumes only `overallScore` and `overallRank` for default Score/rank display.
- Keeps specialized scores in Why/diagnostics and legacy fields in the API only.

- [ ] **Step 1: Write failing UI tests for complete/evaluating/incomplete/unknown and coverage**

Assert one table, one Score cell, no VQ/VO dimension columns, no secondary headings, server rank use, percentage plus coverage, and `Unrated` without 0 for stale payloads.

- [ ] **Step 2: Run focused SPA tests and confirm old `modelScore` display fails new assertions**

Run: `npm run test:spa -- --run src/components/ScoreCell/ScoreCell.test.tsx src/pages/ProviderPage/ProviderPage.test.tsx src/pages/DashboardPage/DashboardPage.test.tsx`

- [ ] **Step 3: Implement OverallScoreCell and evidence details**

Show `overallScore.display`, coverage, status, uncertainty, methodology, included/excluded dimensions, and missing evidence reasons. Do not add specialized dimension columns.

- [ ] **Step 4: Change provider ordering and coverage tiles to overall contracts**

Use `overallRank` from the server; sort null ranks alphabetically. Preserve one unified table/grid.

- [ ] **Step 5: Run all focused UI suites**

Run: `npm run test:spa -- --run src/components/ScoreCell/ScoreCell.test.tsx src/components/EvidencePanel/EvidencePanel.test.tsx src/pages/ProviderPage/ProviderPage.test.tsx src/pages/DashboardPage/DashboardPage.test.tsx`

---

### Task 9: Full Verification and Live Acceptance

**Files:**
- Modify: `docs/superpowers/specs/2026-08-19-catalog-fair-model-evaluation-design.md` only if implementation details require a clarification that does not change owner policy.
- Modify: `docs/superpowers/plans/2026-08-19-catalog-overall-score-v1.md` checkbox statuses.

**Interfaces:**
- Produces verified repository and browser evidence; no commit/push.

- [ ] **Step 1: Run full automated verification**

Run: `npm run typecheck`

Run: `npm test`

Run: `npm run build`

- [ ] **Step 2: Verify migration on a fresh isolated SQLite database**

Create a temp DB through `openDb`, inspect all evaluation tables/indexes, seed completed/incomplete fixtures, and verify API serialization. Do not write the service-owned live DB directly.

- [ ] **Step 3: Restart the Catalog API and Vite development surface silently**

Use unused ports if 8791/5174 are occupied by another process. Verify HTTP status and page title before browser acceptance.

- [ ] **Step 4: Browser-verify desktop and mobile provider routes**

Check Ollama Cloud, OpenCode Go, OpenCode Zen, and ClinePass: one table/grid, roster row count, honest incomplete state, coverage, server rank, Why details, and no secondary headings or overlap.

- [ ] **Step 5: Report the exact scored/incomplete counts and credential boundary**

Distinguish framework completion from actual runtime evidence. Any model without accepted external evidence and runtime credentials must remain `insufficient_evidence`; do not call it scored.
