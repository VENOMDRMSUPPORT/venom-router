# Local Benchmark Rating Implementation Plan (Plan 3 of 3)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The Benchmark button runs real inference on the owner's own account — measuring success, time-to-first-token, and generation speed — and writes a local quality rating that replaces "Not rated — unknown" on Live Models.

**Architecture:** A benchmark engine that sends N streamed chat completions through the production request path (route resolution + wire codecs — so the measurement reflects what routing actually delivers), persists per-run aggregates to a new `benchmark_runs` table, derives a documented 0..1 performance score, and writes it through the existing `SetQualityRating` + audit path. The existing job pipeline (`internal/httpapi/benchmark.go`) is kept; only its no-op quality resolution is replaced. The leaderboard `QualityIndex` seam stays nil — no imported scores (spec D4). Spec: `docs/superpowers/specs/2026-08-05-honest-model-verification-design.md` §3.F.

**Tech Stack:** Go (SSE streaming measurement, goose migration, real-sqlite tests), React+TypeScript (vitest).

## Global Constraints

- Same global constraints as Plan 1 (`2026-08-05-universal-probes-and-honest-gate.md`): English-only files, strict TDD, no live network in tests (stream against `httptest`), composition-root mutation proofs, no tautologies, full gate, commit per green step.
- **Depends on Plan 1** (the honest gate defines "best live offering"; the specs map provides provider coverage). Independent of Plan 2.
- Benchmarks SPEND real credits: manual trigger only (the existing button); never called from any tick, sweep, or discovery path. Grep-proof it in the final task.
- The benchmark measures through the PRODUCTION dispatch path — never a hand-rolled per-provider HTTP client (that would measure a parallel implementation, not the product).
- `settings.EnrichmentEnabled` remains the gate (`benchmark.go:133-139`) — unchanged.

---

### Task 1: `benchmark_runs` migration

**Files:**
- Create: `internal/storage/migrations/NNNNN_benchmark_runs.sql` (NNNNN = next number — `ls internal/storage/migrations/` first)
- Test: `internal/storage/migrations_test.go` (follow the existing per-migration up/down test pattern — read the 00015/00016 tests first; the down must be a faithful inverse, not a table reconstruction — the 00016 lesson)

**Interfaces:**
- Produces: the table Task 2's repo reads/writes.

```sql
-- +goose Up
-- +goose StatementBegin
CREATE TABLE benchmark_runs (
    id                TEXT PRIMARY KEY,
    model_id          TEXT NOT NULL REFERENCES models(id) ON DELETE CASCADE,
    account_id        TEXT NOT NULL,
    provider_id       TEXT NOT NULL,
    provider_model_id TEXT NOT NULL,
    requests          INTEGER NOT NULL,
    successes         INTEGER NOT NULL,
    ttft_ms           INTEGER,          -- median across successful requests; NULL if none succeeded
    tokens_per_sec    REAL,             -- median across successful requests; NULL if none succeeded
    rating            REAL,             -- the derived 0..1 score; NULL when the success gate failed
    started_at        INTEGER NOT NULL,
    finished_at       INTEGER NOT NULL,
    created_at        INTEGER NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_benchmark_runs_model ON benchmark_runs(model_id, finished_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX idx_benchmark_runs_model;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE benchmark_runs;
-- +goose StatementEnd
```

- [ ] **Step 1: Write the failing migration test** (UpTo/DownTo the new number, assert table + index exist after up and are gone after down).
- [ ] **Step 2: Run to verify failure; add the migration; green.**
- [ ] **Step 3: Commit** — `git commit -m "feat(storage): benchmark_runs migration"`

---### Task 2: BenchmarkRunRepo

**Files:**
- Create: `internal/storage/benchmarkruns.go`
- Test: `internal/storage/benchmarkruns_test.go` (real-sqlite)

**Interfaces:**
- Produces:

```go
type BenchmarkRun struct {
	ID, ModelID, AccountID, ProviderID, ProviderModelID string
	Requests, Successes                                 int
	TTFTMillis                                          *int64
	TokensPerSec                                        *float64
	Rating                                              *float64
	StartedAt, FinishedAt                               time.Time
}
func NewBenchmarkRunRepo(db *DB, now func() time.Time) *BenchmarkRunRepo
func (r *BenchmarkRunRepo) Insert(ctx context.Context, run BenchmarkRun) error
func (r *BenchmarkRunRepo) LatestForModel(ctx context.Context, modelID string) (BenchmarkRun, bool, error)
```

- [ ] **Step 1: Write the failing tests** — insert + LatestForModel round-trip (nullable fields survive nil and non-nil); LatestForModel returns the newest by finished_at; `(BenchmarkRun{}, false, nil)` for an unknown model.
- [ ] **Step 2: Run to verify failure; implement; green.**
- [ ] **Step 3: Commit** — `git commit -m "feat(storage): benchmark run repository"`

---

### Task 3: The measurement engine

**Files:**
- Create: `internal/httpapi/benchmark_engine.go`
- Test: `internal/httpapi/benchmark_engine_test.go`

**Interfaces:**
- Consumes: the production streamed-dispatch seam. **Step 0 (read first, decide once):** read `internal/httpapi/chatcompletions.go` end-to-end and identify the narrowest production seam that (a) resolves a route for a model, (b) executes a STREAMED completion through the wire codecs. If the handler exposes an internal dispatch function, take it as a constructor dependency; if it does not, assemble an `httptest.Server` over the production handler built from `liveTransportImpls` (the composition-root tables) and stream over loopback. Either way the engine's dependency is injected, so tests fake it — but the PRODUCTION wiring (Task 5) must hand it the production path, proven by mutation.
- Produces:

```go
// benchmarkSample is one streamed completion's measurement.
type benchmarkSample struct {
	OK           bool
	TTFT         time.Duration // first content token latency
	TokensPerSec float64       // output tokens / (last-token time - first-token time); 0 if <2 tokens
}
// benchmarkStreamFn runs ONE streamed completion for the offering and reports
// the sample. Injected; the production impl drives the real dispatch path.
type benchmarkStreamFn func(ctx context.Context, accountID, providerID, providerModelID, prompt string, maxTokens int) (benchmarkSample, error)

type benchmarkAggregate struct {
	Requests, Successes int
	TTFTMillis          *int64    // median of successful samples
	TokensPerSec        *float64  // median of successful samples
	Rating              *float64  // nil unless every request succeeded
}
func runBenchmarkSuite(ctx context.Context, stream benchmarkStreamFn, accountID, providerID, providerModelID string, requests int) benchmarkAggregate
```

Fixed fixture: prompt `"Write the numbers one to twenty as words, separated by spaces."`, `maxTokens: 64`, default `requests: 3`, sequential (never parallel — parallel requests contend and corrupt the latency measurement).

**The rating formula (documented, deterministic, pinned by table tests):**

```go
// localBenchmarkRating maps measured performance to a 0..1 score. It is a
// LOCAL heuristic (relative performance on the owner's own account), not a
// universal quality metric: 50% generation speed (saturating at 80 tok/s),
// 50% first-token latency (1.0 at 0ms, 0.0 at >=2000ms). Only defined when
// every request in the suite succeeded.
func localBenchmarkRating(ttft time.Duration, tokensPerSec float64) float64 {
	speed := math.Min(tokensPerSec/80.0, 1.0)
	latency := math.Max(0, 1.0-float64(ttft.Milliseconds())/2000.0)
	return 0.5*speed + 0.5*latency
}
```

- [ ] **Step 1: Write the failing formula tests** — pinned literals: `(0ms, 80tps) → 1.0`, `(2000ms, 0tps) → 0.0`, `(1000ms, 40tps) → 0.5`, `(500ms, 160tps) → 0.875` (speed saturates at 1.0).
- [ ] **Step 2: Write the failing suite tests** — fake stream: 3 OKs with known samples → aggregate has medians + rating; 2 OK + 1 failure → `Successes: 2`, `Rating: nil`, medians still reported from the 2; a transport error from the fake counts as a failed request, never a panic; requests are sequential (the fake asserts no overlap via an in-flight flag).
- [ ] **Step 3: Run to verify failure; implement; green.**
- [ ] **Step 4: Commit** — `git commit -m "feat(benchmark): measurement suite + documented local rating formula"`

---

### Task 4: The production stream implementation

**Files:**
- Create: `internal/httpapi/benchmark_stream.go` (the real `benchmarkStreamFn`)
- Test: `internal/httpapi/benchmark_stream_test.go`

**Interfaces:**
- Consumes: the seam chosen in Task 3 Step 0 (production dispatch or loopback over the production handler); the credential lease (`credentialLeaser` — same seam the usability verifier uses, `usability_assembler.go:26-28`).
- Produces: `newBenchmarkStreamFn(...deps...) benchmarkStreamFn` measuring a real SSE stream: TTFT = time to the first chunk carrying non-empty content delta (reasoning_content counts — the big-pickle rule); TokensPerSec = chunk-count-based approximation documented in a comment (chunks ≈ tokens for these providers; if the terminal SSE frame carries a `usage` block, prefer its `completion_tokens`).

- [ ] **Step 1: Write the failing test** — `httptest` SSE server emitting 3 content chunks at fake-clock-visible intervals (real small sleeps are acceptable here — keep them ≤50ms and assert ORDER-of-magnitude, not exact values: TTFT ≥ first-delay, tokensPerSec > 0, OK=true); a non-2xx response → `OK=false, err=nil` (a provider rejection is a failed sample, not a transport error); a connection drop mid-stream → `err != nil`.
- [ ] **Step 2: Run to verify failure; implement; green.**
- [ ] **Step 3: Commit** — `git commit -m "feat(benchmark): production streamed measurement over the real dispatch path"`

---

### Task 5: Wire the engine into the benchmark job

**Files:**
- Modify: `internal/httpapi/benchmark.go:189-240` (the run body: pick the target offering, run the suite, persist, rate)
- Modify: `internal/httpapi/controlmux.go` (construct the engine deps; the `QualityIndex` seam REMAINS nil)
- Test: `internal/httpapi/benchmark_test.go`

**Interfaces:**
- Consumes: Tasks 2-4; `CatalogRepo.ListOfferings` with `LiveOnly: true` filtered to the model (the target = the model's first LIVE offering — Plan 1's gate guarantees it has verified chat; if the model has NO live offering the job fails typed `no_live_offering`).
- Produces: a benchmark job that (1) inserts a `benchmark_runs` row ALWAYS (even when the success gate fails — the measurement is evidence), (2) calls `catalog.SetQualityRating(ctx, modelID, rating)` (`catalog.go:548-551`) ONLY when the aggregate's Rating is non-nil, (3) completes with a result ref exposing the aggregate.

- [ ] **Step 1: Write the failing job tests** (real-sqlite + fake stream): (a) model with a live offering + all-success fake → benchmark_runs row exists, `models.quality_rating` set to the formula value (pin the literal), job completed; (b) fake with one failure → row exists with `rating NULL`, `quality_rating` stays NULL, job completed (measurement recorded, rating honestly withheld); (c) model with no live offering → job fails `no_live_offering`, no row; (d) `EnrichmentEnabled=false` → 409 `enrichment_disabled` (existing behavior — assert it still holds, the seam replacement must not bypass it).
- [ ] **Step 2: Run to verify failure.**
- [ ] **Step 3: Implement** — replace the `resolveQuality` no-op path with the engine; keep the job/audit scaffolding; the leaderboard precedence code stays for a future source but is unreachable with a nil seam (delete only if it obstructs — prefer leaving the tested precedence engine intact).
- [ ] **Step 4: Mutation proof (composition root)** — in PRODUCTION controlmux construction, replace the engine dependency with nil/no-op; test (a) must go RED (proving the test drives the production assembly, not a test-owned one).
- [ ] **Step 5: Manual-only grep-proof** — `grep -rn "runBenchmark\|BenchmarkSuite\|benchmarkStream" internal/ | grep -v _test.go` and confirm the ONLY non-test caller chain roots at the HTTP handler (no tick, no sweep). Paste the output in the report.
- [ ] **Step 6: Run the package; commit** — `git commit -m "feat(benchmark): real local benchmark writes measured quality rating"`

---

### Task 6: UI — the rating becomes real

**Files:**
- Modify: `dashboard/src/models/ModelsSurface.tsx:443-446` (the "Completed with no rating — no canonical quality source is wired yet" copy is now WRONG — the completion toast/message states the measured outcome: rated vs "measurement recorded; rating withheld (a request failed)")
- Modify: the rating badge (`ModelsSurface.tsx:590-599` group-level, `:240-253` offering-level) — when `quality_known`, label the value's provenance "Local benchmark"
- Test: `dashboard/src/models/ModelsSurface.test.tsx`

- [ ] **Step 1: Write the failing tests** — (1) a group with `quality_rating: 0.87` renders the rating with title/label containing "Local benchmark"; (2) the completed-benchmark message no longer claims "no canonical quality source is wired yet".
- [ ] **Step 2: Run to verify failure; implement; green.**
- [ ] **Step 3: Run dashboard suites + e2e (copy changed).**
- [ ] **Step 4: Commit** — `git commit -m "feat(models): benchmark completion reports the measured rating with local-benchmark provenance"`

---

### Task 7: Full gate + evidence

- [ ] **Step 1:** `task gate` on Windows; `go test -race ./internal/httpapi/ ./internal/storage/`.
- [ ] **Step 2:** dashboard: typecheck, lint, vitest, build, e2e.
- [ ] **Step 3:** Report: Task 5 mutation evidence + the manual-only grep output + which dispatch seam Task 3 Step 0 selected and why.

---

## Self-Review (done at write time)

- **Spec coverage:** §3.F engine → Tasks 3-4; persistence → Tasks 1-2; rating via SetQualityRating → Task 5; manual-only → Task 5 Step 5 + Global Constraints; UI provenance → Task 6; "success rate gates it; latency and speed scale it" → the formula + nil-Rating gate (Task 3); leaderboard stays nil → Task 5.
- **Placeholder scan:** the one open implementation choice (dispatch seam vs loopback) is a bounded read-first decision with both options specified and the invariant (production path, proven by mutation) fixed — not a TBD.
- **Type consistency:** `benchmarkSample`/`benchmarkStreamFn`/`benchmarkAggregate` (Task 3) consumed by Tasks 4-5; `BenchmarkRun` fields match the migration's columns nullable-for-nullable.
