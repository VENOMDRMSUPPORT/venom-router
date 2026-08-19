# Venom Catalog Live Evaluation Control Design

**Status:** delivered 2026-08-20
**Date:** 2026-08-20
**Scope:** Venom Catalog only (`catalog/`). No Venom Router, Venom Lite, Venom
Pro, Venom Max, or `Design_System/` change.

## 1. Why

Evaluations are driven today by `catalog/scripts/run-overall-evaluations.ts`, a
terminal script. That has three costs the owner has now paid for directly.

**It is blind.** A run takes hours and the only signal is a JSON line per
dimension in a terminal. On 2026-08-19 a run wrote systematically wrong scores
for three dimensions for roughly an hour before anyone could see it, because
nothing surfaced the numbers while they were being produced.

**It is a second writer.** The script opens its own connection to
`data/catalog.db` while the service holds the same file. SQLite locking kept the
database intact — `PRAGMA integrity_check` came back `ok` — but the arrangement
has no guarantee that a scheduled sync will not interleave with an evaluation
write, and the project's own operating rule is that the service is the single
writer.

**It cannot be stopped safely.** Stopping the bad run meant finding the process
and killing it by PID. Nothing in the product could do it.

This design moves evaluation execution into the service that already owns the
database, and puts a control surface on it.

## 2. Decisions locked by the owner

- One button per model. Clicking it evaluates **only the dimensions that are
  missing** for that model, plus Speed when Speed is missing.
- A second click while a job runs **queues** the model. Jobs run one at a time.
- A running job can be **stopped** from the modal, and stopping also clears the
  queue.
- Progress reaches the browser by **polling a job-state endpoint**, not by
  server-sent events.
- Provider credentials never reach the browser. Evaluation runs server-side.

## 3. Non-goals

Deliberately not built, and not to be added without a new decision:

- Server-sent events or websockets.
- More than one concurrent worker.
- A queue that survives a service restart.
- Manual per-dimension selection in the UI.
- Cancelling an individual in-flight request.

## 4. Architecture

### 4.1 One selection rule, one implementation

Choosing what to run — resolving a model to its identity, skipping dimensions
already scored against the current test-set hash, skipping dimensions the offer
reports as `unsupported`, and refusing an offer with no resolved identity or no
credential — currently lives inline inside `scripts/run-overall-evaluations.ts`.

The service needs exactly that rule. Copying it would create a second copy of a
core mechanism, which this repository treats as a defect rather than a
variation. The rule therefore moves into a single unit under
`sync/evaluation/`, and **both** the script and the service call it. Neither
owns it.

The unit is pure: it takes a database handle plus a provider/model pair and
returns a plan. It performs no I/O against a provider and no writes.

```
planEvaluation(db, { providerId, modelId, force }) -> {
  identityId: string | null,
  dimensions: Array<{ dimension, reason: 'missing' | 'forced' }>,
  skipped: Array<{ dimension, reason: 'already_scored' | 'unsupported' }>,
  speed: 'missing' | 'scored',
  blocked: null | 'identity_unresolved' | 'missing_credentials',
  estimatedRequests: number,
}
```

`estimatedRequests` is computed from the policy constants, not hard-coded:
`dimensions.length * (scenarioCount * repetitions + warmupRequests)` plus
`scenarioCount + warmupRequests` when Speed is included.

### 4.2 The job runner

`server/evaluation-runner.ts`, shaped after the existing `server/sync-runner.ts`
so the service gains no second concurrency pattern.

- A FIFO queue of `{ providerId, modelId }` held in memory.
- Exactly one worker. State is `idle`, `running`, or `stopping`.
- Enqueuing a pair already in the queue, or already running, is rejected rather
  than duplicated.

A job executes in a fixed order:

1. Compute the plan (§4.1). A blocked plan finishes the job immediately with the
   typed reason and never contacts a provider.
2. Run each missing quality dimension. Quality belongs to the model identity, so
   a dimension completed here also completes it for every other offer that
   resolves to the same identity.
3. Run Speed **last, and only when no other evaluation traffic is in flight**.
   Speed measures time-to-first-token and output tokens per second, so it is
   measured under a fixed load. This ordering is a measurement condition, not a
   scheduling preference.
4. Recalculate published offers once, at the end of the job.

### 4.3 Stopping

Stopping is cooperative, and it is cheap by construction.

A `stopping` flag is checked between samples. The request already in flight is
allowed to finish rather than being torn down. Samples are already persisted one
at a time as they complete, so a stopped run keeps every sample it paid for and
stays resumable: a later job for the same model resumes that run and re-executes
only what is missing.

Stopping never marks a partial dimension as scored. An interrupted dimension
stays `insufficient_evidence`, which is the honest state.

### 4.4 Restart behaviour

The queue is in memory. If the service restarts mid-job, the queue is lost and
the in-flight dimension is left unfinished. No evidence is lost, because samples
are persisted as they complete and the next job for that model resumes the run.
This is documented rather than engineered around: a persistent queue would add a
schema and a recovery path for a case that costs one click to redo.

### 4.5 Single writer

With the service running the evaluations, the service is the only process
writing to `data/catalog.db`.

`scripts/run-overall-evaluations.ts` remains — it is how a long unattended batch
is run — but it must refuse to start when the service is listening, and say why.
A guarantee that depends on remembering is not a guarantee.

## 5. HTTP contract

All routes are additive and follow the existing `route()` shape, which returns a
complete `{ status, body }`. No streaming primitive is introduced.

### `POST /v1/evaluations`

Body: `{ providerId, modelId }`.

There is no `force` on this route. Re-evaluating an already-scored dimension is
a corpus-wide decision with a real bill attached, so it stays on the terminal
script where it is deliberate. `planEvaluation` still accepts `force` because
the script needs it.

| Status | Meaning | Body |
|---|---|---|
| 202 | Accepted onto the queue | `{ position, plan }` |
| 409 | Already queued or already running | `{ error, state }` |
| 404 | No such provider/model | `{ error, providerId, modelId }` |
| 422 | Plan is blocked | `{ error, reason }` where reason is `identity_unresolved` or `missing_credentials` |

Fail closed: an unknown or unresolvable request is rejected with a typed reason
and buys nothing from the provider.

### `GET /v1/evaluations`

```
{
  state: 'idle' | 'running' | 'stopping',
  current: null | {
    providerId, modelId, identityId,
    dimension,                        // the dimension in flight
    samplesCompleted, samplesTotal,   // within that dimension
    dimensionsCompleted: [{ dimension, score, status }],
    dimensionsRemaining: [dimension],
    startedAt,
  },
  queue: [{ providerId, modelId }],
  recent: [{ providerId, modelId, finishedAt, outcome }],   // the last 10, oldest dropped
}
```

### `DELETE /v1/evaluations`

Stops the running job after the in-flight request and clears the queue. Returns
`{ stopped: boolean, cleared: number }`. Stopping when idle is not an error.

### `GET /v1/models/{providerId}/{modelId}/evaluation` (existing)

Extended with a `plan` field carrying §4.1's output. The modal's cost preview
needs no new endpoint, and the plan the preview shows is produced by the same
code that will execute it.

## 6. The modal

Opened from a button on each model row. Three states.

**Preview.** Lists the missing dimensions and the estimated request count, e.g.
"3 dimensions missing — about 189 requests". A click spends money, so it is
never a blind click. A blocked plan shows the typed reason and offers no start
button.

**Running.** A progress row per dimension: the one in flight shows
`samplesCompleted / samplesTotal`, completed ones show their score as it lands,
remaining ones are pending. A Stop button is present throughout. The modal polls
`GET /v1/evaluations` every 1.5 seconds **only while it is open and a job is
active** — never in the background, never when idle.

**Finished.** Final scores, resulting coverage, and any dimension that ended
`insufficient_evidence` with its reason. Closing invalidates the catalog query
so the table behind the modal reflects the new values.

Opening the modal for a model whose job is already running enters the running
state directly.

## 7. Testing

No test contacts a real provider. The transport is injected in every case.

**Backend** (`node --test`):

- The plan unit: missing versus scored versus unsupported, unresolved identity,
  absent credential, and that `estimatedRequests` is derived from the policy
  constants rather than restated.
- The script and the service produce an identical plan for the same input —
  the test that keeps §4.1 honest.
- Queue behaviour: FIFO order, one worker, duplicate enqueue rejected.
- Speed runs last, and never while a quality dimension is in flight.
- Stop: leaves persisted samples intact, leaves the run resumable, and does not
  mark a partial dimension scored.
- Route contract: each status code above, including that a 422 performs no
  provider call.

**SPA** (Vitest + Testing Library): the three modal states against a stubbed
fetch, the poll starting and stopping with the modal, and the Stop action.

**Browser**: the page is opened against a copy of the live database and the
console is read. A green SPA suite has passed in this repository while the
application was blank in a browser; the suite is not the acceptance.

## 8. Acceptance

- A model with missing dimensions can be evaluated end to end from the
  dashboard, and its row updates without a reload.
- A second model queues, and runs after the first.
- Stop halts within one request and loses no completed sample.
- The terminal script refuses to run while the service is listening.
- `npm test` and `npm run typecheck` are green in `catalog/`. `task gate` does
  not cover `catalog/`, so it is not evidence here.
