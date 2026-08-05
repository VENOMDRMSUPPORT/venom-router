package httpapi

// benchmark_test.go exercises the P6-CAPI-001 canonical-quality endpoint:
// POST /models/{id}/benchmark (09 §3.12 async-job shape, 04 §3/§5 canonical
// quality). Plan 3 of the local-benchmark-rating design (2026-08-05) rewired
// this job to run a REAL fixed measurement suite against the model's own
// live offering (through the injected benchmarkStreamFn seam — a fake here,
// the real dispatch path in production) rather than reading an imported
// leaderboard. It never fabricates a rating: one is written only when every
// request in the suite succeeded.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

const (
	benchModelID   = "model-bench-1"
	benchProvider  = "prov-bench"
	benchProvModel = "vendor/great-model"
)

var benchNow = time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)

type benchmarkFixture struct {
	handler  *BenchmarkHandler
	db       *storage.DB
	jobs     *storage.JobRepo
	settings *storage.SettingsRepo
	catalog  *storage.CatalogRepo
	runs     *storage.BenchmarkRunRepo
}

// newBenchmarkFixture seeds a provider + canonical model + alias over a fresh
// migrated DB and wires a BenchmarkHandler with the supplied fake stream.
// enrichmentEnabled seeds the owner toggle this endpoint gates on. It seeds
// no live offering — tests that need one call seedLiveOffering themselves,
// so "no live offering" (the typed no_live_offering failure) is the fixture's
// own honest default rather than something each test has to arrange.
func newBenchmarkFixture(t *testing.T, enrichmentEnabled bool, stream benchmarkStreamFn) *benchmarkFixture {
	t.Helper()
	db := testControlDB(t)

	if _, err := db.Conn().Exec(
		`INSERT INTO providers (id, display_name, auth_mode, funding_mode, funding_locked, created_at, updated_at)
		 VALUES (?, ?, 'api_key', 'owner_policy', 0, 0, 0)`, benchProvider, benchProvider,
	); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if _, err := db.Conn().Exec(
		`INSERT INTO models (id, canonical_key_sha256, display_name, quality_rating, created_at, updated_at)
		 VALUES (?, ?, 'Great Model', NULL, 0, 0)`, benchModelID, "sha-"+benchModelID,
	); err != nil {
		t.Fatalf("seed model: %v", err)
	}
	if _, err := db.Conn().Exec(
		`INSERT INTO provider_model_aliases (provider_id, provider_model_id, model_id) VALUES (?, ?, ?)`,
		benchProvider, benchProvModel, benchModelID,
	); err != nil {
		t.Fatalf("seed alias: %v", err)
	}

	settings := storage.NewSettingsRepo(db)
	if err := settings.PutEnrichment(context.Background(), enrichmentEnabled, benchNow); err != nil {
		t.Fatalf("seed enrichment toggle: %v", err)
	}

	jobs := storage.NewJobRepo(db)
	catalog := storage.NewCatalogRepo(db)
	runs := storage.NewBenchmarkRunRepo(db, func() time.Time { return benchNow })
	handler := NewBenchmarkHandler(
		catalog, jobs, settings, runs, stream, newAuditEmitter(db, nil),
		newOAuthTransactionID, func() time.Time { return benchNow },
	)
	return &benchmarkFixture{handler: handler, db: db, jobs: jobs, settings: settings, catalog: catalog, runs: runs}
}

// seedLiveOffering makes benchModelID's one alias (benchProvider/
// benchProvModel) satisfy CatalogRepo.ListOfferings' LiveOnly gate: a
// connected+healthy account, an available offering, and a
// certified+supported chat offering_operation — exactly what Plan 1's
// honest gate requires before a model can be considered "live".
func seedLiveOffering(t *testing.T, db *storage.DB, accountID string) {
	t.Helper()
	if _, err := db.Conn().Exec(
		`INSERT INTO accounts (id, provider_id, external_id, auth_type, connection_state, health_state, reauth_in_progress, created_at, updated_at)
		 VALUES (?, ?, ?, 'api_key', 'connected', 'healthy', 0, 0, 0)`,
		accountID, benchProvider, accountID,
	); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := db.Conn().Exec(
		`INSERT INTO account_model_offerings (account_id, provider_id, provider_model_id, model_id, availability, first_seen_at, last_seen_at)
		 VALUES (?, ?, ?, ?, 'available', 0, 0)`,
		accountID, benchProvider, benchProvModel, benchModelID,
	); err != nil {
		t.Fatalf("seed offering: %v", err)
	}
	opID := "op-" + accountID + "-chat"
	if _, err := db.Conn().Exec(
		`INSERT INTO offering_operations (id, account_id, provider_id, provider_model_id, operation, created_at, updated_at)
		 VALUES (?, ?, ?, ?, 'chat', 0, 0)`,
		opID, accountID, benchProvider, benchProvModel,
	); err != nil {
		t.Fatalf("seed offering operation: %v", err)
	}
	if _, err := db.Conn().Exec(
		`INSERT INTO certifications (offering_operation_id, status, capability_truth, version, created_at, updated_at)
		 VALUES (?, 'certified', 'supported', 1, 0, 0)`,
		opID,
	); err != nil {
		t.Fatalf("seed certification: %v", err)
	}
}

func (f *benchmarkFixture) post(t *testing.T, modelID string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	return f.postCtx(t, modelID, nil)
}

// postCtx drives the handler with an optional request context, so a test can
// cancel the CLIENT request and still assert the accepted job completes.
func (f *benchmarkFixture) postCtx(t *testing.T, modelID string, ctx context.Context) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/control/v1/models/"+modelID+"/benchmark", nil)
	if ctx != nil {
		req = req.WithContext(ctx)
	}
	req.SetPathValue("id", modelID)
	rec := httptest.NewRecorder()
	f.handler.ServeBenchmark(rec, req)
	var body map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode %q: %v", rec.Body.String(), err)
		}
	}
	return rec, body
}

func (f *benchmarkFixture) jobCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.db.Conn().QueryRow(`SELECT COUNT(*) FROM jobs`).Scan(&n); err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	return n
}

// qualityRating reads models.quality_rating directly, so the assertion is on
// what is actually persisted rather than on what a projection reports.
func (f *benchmarkFixture) qualityRating(t *testing.T) *float64 {
	t.Helper()
	var v *float64
	if err := f.db.Conn().QueryRow(`SELECT quality_rating FROM models WHERE id = ?`, benchModelID).Scan(&v); err != nil {
		t.Fatalf("read quality_rating: %v", err)
	}
	return v
}

// benchmarkRunCount counts benchmark_runs rows for benchModelID — the
// "a row is inserted ALWAYS once the suite ran, never when it could not run
// at all" proof.
func (f *benchmarkFixture) benchmarkRunCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.db.Conn().QueryRow(`SELECT COUNT(*) FROM benchmark_runs WHERE model_id = ?`, benchModelID).Scan(&n); err != nil {
		t.Fatalf("count benchmark_runs: %v", err)
	}
	return n
}

// latestRun reads back benchModelID's most recent benchmark_runs row through
// the REAL repo read path (BenchmarkRunRepo.LatestForModel), never by
// re-deriving it from the aggregate the test already knows.
func (f *benchmarkFixture) latestRun(t *testing.T) storage.BenchmarkRun {
	t.Helper()
	run, ok, err := f.runs.LatestForModel(context.Background(), benchModelID)
	if err != nil {
		t.Fatalf("LatestForModel: %v", err)
	}
	if !ok {
		t.Fatalf("LatestForModel(%q): no row, want one", benchModelID)
	}
	return run
}

// awaitTerminalJob polls the REAL job row until it reaches a terminal state,
// which is how the detached background run reports itself.
func (f *benchmarkFixture) awaitTerminalJob(t *testing.T, jobID string) storage.JobRow {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		row, ok, err := f.jobs.GetByID(context.Background(), jobID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if ok && (row.Status == storage.JobCompleted || row.Status == storage.JobFailed) {
			return row
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("job %s never reached a terminal state", jobID)
	return storage.JobRow{}
}

// jobIDFrom pulls job_id out of a decoded 202 body.
func jobIDFrom(t *testing.T, body map[string]any) string {
	t.Helper()
	data, ok := body["data"].(map[string]any)
	if !ok {
		t.Fatalf("no data object: %#v", body)
	}
	id, _ := data["job_id"].(string)
	if id == "" {
		t.Fatalf("job_id = %#v, want a non-empty id", data["job_id"])
	}
	return id
}

// --- TDD Step 1 tests (task-5-brief.md): (a) live+all-success, (b) live+one
// failure, (c) no live offering, (d) enrichment disabled. ------------------

// TestBenchmark_LiveOfferingAllSuccess_PersistsRunAndWritesRating is Step
// 1(a): every request in the suite succeeds -> a benchmark_runs row exists,
// models.quality_rating is set to the formula's value ON THAT COLUMN'S OWN
// SCALE, and the job completes. Both expected numbers are computed BY HAND
// from localBenchmarkRating's own documented weights, never read from the
// implementation (no tautology):
//
//	speed   = min(40/80, 1)        = 0.5
//	latency = max(0, 1 - 100/2000) = 0.95
//	rating  = 0.5*0.5 + 0.5*0.95   = 0.725   (benchmark_runs.rating, 0..1)
//	column  = 0.725 * 100          = 72.5    (models.quality_rating, 0-100)
//
// The TWO scales are the point of this pair of assertions (whole-branch
// review, 2026-08-05, finding 1): benchmark_runs.rating keeps the raw 0..1
// measurement its migration documents, while models.quality_rating is the
// 0-100 column 04 §3 and models.QualityScore (which divides by 100) define.
// Pinning only the 0..1 value here is what let a perfect benchmark be
// persisted as a near-worst 0.01 ranking score.
func TestBenchmark_LiveOfferingAllSuccess_PersistsRunAndWritesRating(t *testing.T) {
	const wantRunRating = 0.725
	const wantColumnRating = 72.5
	sample := benchmarkSample{OK: true, TTFT: 100 * time.Millisecond, TokensPerSec: 40}
	stream, calls := scriptedStream(t, []scriptedStep{{sample: sample}, {sample: sample}, {sample: sample}})

	f := newBenchmarkFixture(t, true, stream)
	const accountID = "acct-bench-live"
	seedLiveOffering(t, f.db, accountID)

	_, body := f.post(t, benchModelID)
	jobID := jobIDFrom(t, body)
	row := f.awaitTerminalJob(t, jobID)
	if row.Status != storage.JobCompleted {
		t.Fatalf("job status = %q (err %+v), want completed", row.Status, row.Error)
	}

	if len(*calls) != benchmarkDefaultRequests {
		t.Fatalf("stream invoked %d times, want %d", len(*calls), benchmarkDefaultRequests)
	}

	got := f.qualityRating(t)
	if got == nil || !floatsClose(*got, wantColumnRating, 1e-9) {
		t.Fatalf("models.quality_rating = %v, want %v (the 0-100 column scale, 04 §3)", got, wantColumnRating)
	}

	run := f.latestRun(t)
	if run.Requests != benchmarkDefaultRequests || run.Successes != benchmarkDefaultRequests {
		t.Fatalf("run Requests/Successes = %d/%d, want %d/%d", run.Requests, run.Successes, benchmarkDefaultRequests, benchmarkDefaultRequests)
	}
	if run.Rating == nil || !floatsClose(*run.Rating, wantRunRating, 1e-9) {
		t.Fatalf("benchmark_runs.rating = %v, want %v (the raw 0..1 measurement)", run.Rating, wantRunRating)
	}
	if run.AccountID != accountID || run.ProviderID != benchProvider || run.ProviderModelID != benchProvModel {
		t.Fatalf("run target = %+v, want account=%s provider=%s model=%s", run, accountID, benchProvider, benchProvModel)
	}
	if run.ModelID != benchModelID {
		t.Fatalf("run.ModelID = %q, want %q", run.ModelID, benchModelID)
	}
}

// TestBenchmark_LiveOfferingAllSuccess_RecordsProvenance proves the rating
// write still goes through the audit path (04 §3: "always anchored to a
// documented source") — now stamped with the LOCAL-benchmark source, never
// the retired leaderboard-evidence shape.
func TestBenchmark_LiveOfferingAllSuccess_RecordsProvenance(t *testing.T) {
	sample := benchmarkSample{OK: true, TTFT: 50 * time.Millisecond, TokensPerSec: 80}
	stream, _ := scriptedStream(t, []scriptedStep{{sample: sample}, {sample: sample}, {sample: sample}})

	f := newBenchmarkFixture(t, true, stream)
	const accountID = "acct-bench-provenance"
	seedLiveOffering(t, f.db, accountID)

	_, body := f.post(t, benchModelID)
	jobID := jobIDFrom(t, body)
	row := f.awaitTerminalJob(t, jobID)
	if row.Status != storage.JobCompleted {
		t.Fatalf("job status = %q (err %+v), want completed", row.Status, row.Error)
	}

	var reason string
	if err := f.db.Conn().QueryRow(
		`SELECT reason_code FROM audit_events WHERE action = ? AND entity_id = ? ORDER BY id DESC LIMIT 1`,
		auditActionModelQualityRating, benchModelID,
	).Scan(&reason); err != nil {
		t.Fatalf("read provenance audit row: %v", err)
	}
	for _, want := range []string{"source=local_benchmark", "account_id=" + accountID, "requests=3", "successes=3"} {
		if !strings.Contains(reason, want) {
			t.Fatalf("provenance reason %q is missing %q", reason, want)
		}
	}
}

// TestBenchmark_LiveOfferingOneFailure_RecordsRunButWithholdsRating is Step
// 1(b): one request fails -> a benchmark_runs row exists with a NULL
// rating, models.quality_rating stays untouched, and the job still
// COMPLETES — the measurement is recorded, the rating honestly withheld.
func TestBenchmark_LiveOfferingOneFailure_RecordsRunButWithholdsRating(t *testing.T) {
	steps := []scriptedStep{
		{sample: benchmarkSample{OK: true, TTFT: 100 * time.Millisecond, TokensPerSec: 40}},
		{sample: benchmarkSample{OK: false}},
		{sample: benchmarkSample{OK: true, TTFT: 100 * time.Millisecond, TokensPerSec: 40}},
	}
	stream, _ := scriptedStream(t, steps)

	f := newBenchmarkFixture(t, true, stream)
	seedLiveOffering(t, f.db, "acct-bench-partial")

	_, body := f.post(t, benchModelID)
	jobID := jobIDFrom(t, body)
	row := f.awaitTerminalJob(t, jobID)
	if row.Status != storage.JobCompleted {
		t.Fatalf("job status = %q (err %+v), want completed — a partial failure is still a completed measurement", row.Status, row.Error)
	}

	if got := f.qualityRating(t); got != nil {
		t.Fatalf("quality_rating = %v, want NULL — one request failed, the success gate withholds the rating", *got)
	}

	run := f.latestRun(t)
	if run.Requests != 3 || run.Successes != 2 {
		t.Fatalf("run Requests/Successes = %d/%d, want 3/2", run.Requests, run.Successes)
	}
	if run.Rating != nil {
		t.Fatalf("run.Rating = %v, want nil", *run.Rating)
	}
	if run.TTFTMillis == nil || run.TokensPerSec == nil {
		t.Fatalf("run medians = %v/%v, want both non-nil — the 2 successful samples are still evidence", run.TTFTMillis, run.TokensPerSec)
	}
}

// TestBenchmark_LiveOfferingOneFailure_PreservesExistingRating is the
// never-fabricate proof for the suite-failure path: a PRE-EXISTING rating
// must survive a completed-but-unrated run untouched — not overwritten with
// NULL, not zeroed. (This is the honest replacement for the old
// leaderboard-miss test of the same shape — see task-5-report.md for why
// the leaderboard-based scenario this test used to cover no longer exists.)
func TestBenchmark_LiveOfferingOneFailure_PreservesExistingRating(t *testing.T) {
	steps := []scriptedStep{
		{sample: benchmarkSample{OK: false}},
		{sample: benchmarkSample{OK: true, TTFT: 100 * time.Millisecond, TokensPerSec: 40}},
		{sample: benchmarkSample{OK: true, TTFT: 100 * time.Millisecond, TokensPerSec: 40}},
	}
	stream, _ := scriptedStream(t, steps)

	f := newBenchmarkFixture(t, true, stream)
	seedLiveOffering(t, f.db, "acct-bench-preserve")

	// A value on models.quality_rating's own 0-100 scale (04 §3) — what a
	// PRIOR fully-successful benchmark would really have left behind.
	const preexisting = 64.0
	if _, err := f.db.Conn().Exec(`UPDATE models SET quality_rating = ? WHERE id = ?`, preexisting, benchModelID); err != nil {
		t.Fatalf("seed pre-existing rating: %v", err)
	}

	_, body := f.post(t, benchModelID)
	jobID := jobIDFrom(t, body)
	row := f.awaitTerminalJob(t, jobID)
	if row.Status != storage.JobCompleted {
		t.Fatalf("job status = %q (err %+v), want completed", row.Status, row.Error)
	}

	got := f.qualityRating(t)
	if got == nil {
		t.Fatalf("quality_rating became NULL; the pre-existing %v must be left alone", preexisting)
	}
	if !floatsClose(*got, preexisting, 1e-9) {
		t.Fatalf("quality_rating = %v, want the untouched %v (never an invented or zero rating)", *got, preexisting)
	}
}

// TestBenchmark_NoLiveOffering_FailsTypedAndCreatesNoRun is Step 1(c): a
// model with an alias but NO live offering must fail the job typed
// no_live_offering, and — unlike the suite-ran-but-failed case above —
// benchmark_runs must gain NO row at all, because nothing was ever measured.
func TestBenchmark_NoLiveOffering_FailsTypedAndCreatesNoRun(t *testing.T) {
	stream, calls := scriptedStream(t, nil)
	f := newBenchmarkFixture(t, true, stream)
	// Deliberately no seedLiveOffering call.

	_, body := f.post(t, benchModelID)
	jobID := jobIDFrom(t, body)
	row := f.awaitTerminalJob(t, jobID)
	if row.Status != storage.JobFailed {
		t.Fatalf("job status = %q (err %+v), want failed", row.Status, row.Error)
	}
	if row.Error == nil || row.Error.Code != benchmarkNoLiveOffering {
		t.Fatalf("job error = %+v, want code %q", row.Error, benchmarkNoLiveOffering)
	}
	if n := f.benchmarkRunCount(t); n != 0 {
		t.Fatalf("benchmark_runs rows = %d, want 0 — nothing was measured", n)
	}
	if len(*calls) != 0 {
		t.Fatalf("stream invoked %d times, want 0 — there was no target to measure", len(*calls))
	}
	if got := f.qualityRating(t); got != nil {
		t.Fatalf("quality_rating = %v, want unchanged (NULL)", *got)
	}
}

// TestBenchmark_DisabledIsRefusedAndCreatesNoJob is Step 1(d): the
// owner-enabled gate is UNCHANGED by this rewrite — it must still 409
// enrichment_disabled and create no job, before the seam is ever consulted.
func TestBenchmark_DisabledIsRefusedAndCreatesNoJob(t *testing.T) {
	stream, calls := scriptedStream(t, nil)
	f := newBenchmarkFixture(t, false, stream)
	before := f.jobCount(t)

	rec, body := f.post(t, benchModelID)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body %s)", rec.Code, rec.Body.String())
	}
	if code := body["error"].(map[string]any)["code"]; code != "enrichment_disabled" {
		t.Fatalf("error code = %#v, want enrichment_disabled", code)
	}
	if after := f.jobCount(t); after != before {
		t.Fatalf("job count went %d -> %d; a refusal must create no job", before, after)
	}
	if got := f.qualityRating(t); got != nil {
		t.Fatalf("quality_rating = %v, want unchanged (NULL)", *got)
	}
	if len(*calls) != 0 {
		t.Fatalf("stream invoked %d times, want 0 — the gate must refuse before the seam is ever touched", len(*calls))
	}
}

// --- Pre-existing coverage, updated for the new seam -----------------------

// TestBenchmark_AcceptedShapeAndJobResolves proves the 202 envelope and that
// the returned job id resolves through the canonical GET /jobs/{job_id}
// surface — looked up for real, never compared to itself. No live offering
// is seeded: this test only cares about the accept/job-resolution shape, not
// the terminal outcome (awaitTerminalJob accepts either terminal status).
func TestBenchmark_AcceptedShapeAndJobResolves(t *testing.T) {
	stream, _ := scriptedStream(t, nil)
	f := newBenchmarkFixture(t, true, stream)

	rec, body := f.post(t, benchModelID)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body %s)", rec.Code, rec.Body.String())
	}
	jobID := jobIDFrom(t, body)
	data := body["data"].(map[string]any)
	if data["status_url"] != "/api/control/v1/jobs/"+jobID {
		t.Fatalf("status_url = %#v, want the canonical shared job route", data["status_url"])
	}

	// Resolve the id through the REAL jobs handler, not by string comparison.
	jobsHandler := NewJobsHandler(f.db)
	jreq := httptest.NewRequest(http.MethodGet, "/api/control/v1/jobs/"+jobID, nil)
	jreq.SetPathValue("job_id", jobID)
	jrec := httptest.NewRecorder()
	jobsHandler.ServeGet(jrec, jreq)
	if jrec.Code != http.StatusOK {
		t.Fatalf("GET /jobs/%s status = %d, want 200", jobID, jrec.Code)
	}
	var jbody map[string]any
	if err := json.Unmarshal(jrec.Body.Bytes(), &jbody); err != nil {
		t.Fatalf("decode job: %v", err)
	}
	jdata := jbody["data"].(map[string]any)
	if jdata["kind"] != string(storage.JobKindBenchmark) {
		t.Fatalf("job kind = %#v, want %q", jdata["kind"], storage.JobKindBenchmark)
	}

	// The kind must also survive the storage-layer's own closed vocabulary.
	if _, err := storage.ParseJobKind(jdata["kind"].(string)); err != nil {
		t.Fatalf("ParseJobKind(%q): %v", jdata["kind"], err)
	}

	// Let the detached run finish before the fixture's DB is closed.
	f.awaitTerminalJob(t, jobID)
}

// TestBenchmark_UnknownModelIs404AndCreatesNoJob proves the model id is
// validated BEFORE any job row exists — a 404 must leave the database exactly
// as it was.
func TestBenchmark_UnknownModelIs404AndCreatesNoJob(t *testing.T) {
	stream, _ := scriptedStream(t, nil)
	f := newBenchmarkFixture(t, true, stream)
	before := f.jobCount(t)

	rec, body := f.post(t, "no-such-model")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
	if code := body["error"].(map[string]any)["code"]; code != "not_found" {
		t.Fatalf("error code = %#v, want not_found", code)
	}
	if after := f.jobCount(t); after != before {
		t.Fatalf("job count went %d -> %d; a 404 must create no job", before, after)
	}
}

// TestBenchmark_SurvivesClientCancellation proves the accepted job runs on a
// DETACHED context. Once the caller has been told 202 with a job id, a
// disconnecting client must not silently cancel the work that id refers to.
// The fake stream blocks on its FIRST call until the test has cancelled the
// client request and closed `release`, so the suite genuinely resumes after
// cancellation; every call (including the first, once released) returns an
// identical successful sample so the resulting rating is a single hand-
// computable value.
func TestBenchmark_SurvivesClientCancellation(t *testing.T) {
	sample := benchmarkSample{OK: true, TTFT: 50 * time.Millisecond, TokensPerSec: 40}
	// speed = min(40/80,1) = 0.5; latency = 1 - 50/2000 = 0.975
	// rating = 0.5*0.5 + 0.5*0.975 = 0.7375 (benchmark_runs, 0..1)
	// column = 0.7375 * 100        = 73.75  (models.quality_rating, 0-100)
	const wantColumnRating = 73.75

	release := make(chan struct{})
	var once sync.Once
	stream := func(ctx context.Context, accountID, providerID, providerModelID, prompt string, maxTokens int) (benchmarkSample, error) {
		once.Do(func() {
			<-release
		})
		return sample, nil
	}

	f := newBenchmarkFixture(t, true, stream)
	seedLiveOffering(t, f.db, "acct-bench-cancel")

	ctx, cancel := context.WithCancel(context.Background())
	_, body := f.postCtx(t, benchModelID, ctx)
	jobID := jobIDFrom(t, body)

	cancel()
	close(release)

	row := f.awaitTerminalJob(t, jobID)
	if row.Status != storage.JobCompleted {
		t.Fatalf("job status = %q (err %+v), want completed after client cancellation", row.Status, row.Error)
	}
	got := f.qualityRating(t)
	if got == nil || !floatsClose(*got, wantColumnRating, 1e-9) {
		t.Fatalf("quality_rating = %v, want %v — the detached run must still persist its result", got, wantColumnRating)
	}
}

// TestBenchmark_MethodNotAllowed proves the endpoint is POST-only.
func TestBenchmark_MethodNotAllowed(t *testing.T) {
	stream, _ := scriptedStream(t, nil)
	f := newBenchmarkFixture(t, true, stream)

	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/api/control/v1/models/"+benchModelID+"/benchmark", nil)
			req.SetPathValue("id", benchModelID)
			rec := httptest.NewRecorder()
			f.handler.ServeBenchmark(rec, req)
			if rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want 405", rec.Code)
			}
		})
	}
}

// TestBenchmark_EmitsExactlyOneAuditRowOnAccept pins the accept-path audit
// contract: one row, secret-free. No live offering is seeded — the job fails
// no_live_offering, which is irrelevant to what this test asserts (the
// ACCEPT audit event, emitted before the job even runs).
func TestBenchmark_EmitsExactlyOneAuditRowOnAccept(t *testing.T) {
	stream, _ := scriptedStream(t, nil)
	f := newBenchmarkFixture(t, true, stream)

	_, body := f.post(t, benchModelID)
	jobID := jobIDFrom(t, body)
	f.awaitTerminalJob(t, jobID)

	var n int
	if err := f.db.Conn().QueryRow(
		`SELECT COUNT(*) FROM audit_events WHERE action = ?`, auditActionModelBenchmark,
	).Scan(&n); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if n != 1 {
		t.Fatalf("accept audit rows = %d, want exactly 1", n)
	}
}

// TestBenchmark_IsOwnerGatedThroughTheRealMux proves the route is registered
// behind gated(...) in the real ControlMux, not merely reachable.
func TestBenchmark_IsOwnerGatedThroughTheRealMux(t *testing.T) {
	db := testControlDB(t)
	realMux := ControlMux(testAllowedHost, fakeSPA(), db, testKeyring(t))

	rec := httptest.NewRecorder()
	realMux.ServeHTTP(rec, newAuthRequest(t, http.MethodPost, "/api/control/v1/models/m1/benchmark", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401 (body %s)", rec.Code, rec.Body.String())
	}
}

// TestParseJobKind_AcceptsBenchmark pins the storage-layer vocabulary: 09
// §3.12 lists `benchmark` among the job kinds, so ParseJobKind must accept it
// and still reject anything outside the closed set.
func TestParseJobKind_AcceptsBenchmark(t *testing.T) {
	got, err := storage.ParseJobKind("benchmark")
	if err != nil {
		t.Fatalf("ParseJobKind(benchmark): %v", err)
	}
	if got != storage.JobKindBenchmark {
		t.Fatalf("got %q, want %q", got, storage.JobKindBenchmark)
	}
	if _, err := storage.ParseJobKind("benchmarks"); !errors.Is(err, storage.ErrUnknownJobKind) {
		t.Fatalf("ParseJobKind(benchmarks) err = %v, want ErrUnknownJobKind", err)
	}
}
