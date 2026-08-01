package httpapi

// benchmark_test.go exercises the P6-CAPI-001 canonical-quality endpoint:
// POST /models/{id}/benchmark (09 §3.12 async-job shape, 04 §3/§5 canonical
// quality). The endpoint reads an analysis leaderboard through the injected
// intelligence.QualityIndex seam and resolves the result through the SAME
// precedence engine every other fact goes through — it never runs inference
// and never invents a rating.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/intelligence"
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
}

// newBenchmarkFixture seeds a provider + canonical model + alias over a fresh
// migrated DB and wires a BenchmarkHandler with the supplied leaderboard seam.
// enrichmentEnabled seeds the owner toggle this endpoint gates on.
func newBenchmarkFixture(t *testing.T, enrichmentEnabled bool, quality intelligence.QualityIndex) *benchmarkFixture {
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
	handler := NewBenchmarkHandler(
		catalog, jobs, settings, quality, newAuditEmitter(db, nil),
		newOAuthTransactionID, func() time.Time { return benchNow },
	)
	return &benchmarkFixture{handler: handler, db: db, jobs: jobs, settings: settings, catalog: catalog}
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

func ratingIndex(rating float64, exact bool) intelligence.QualityIndex {
	return func(_ context.Context, _, _ string) (intelligence.QualityEntry, bool, error) {
		return intelligence.QualityEntry{
			Rating: &rating, SourceRef: "analysis-leaderboard/v3",
			ExactIdentityMatch: exact, DatasetVersion: "2026-07",
		}, true, nil
	}
}

// noSignalIndex is a leaderboard that simply has no entry for this model — a
// LEGITIMATE outcome, not an error.
func noSignalIndex() intelligence.QualityIndex {
	return func(_ context.Context, _, _ string) (intelligence.QualityEntry, bool, error) {
		return intelligence.QualityEntry{}, false, nil
	}
}

// TestBenchmark_AcceptedShapeAndJobResolves proves the 202 envelope and that
// the returned job id resolves through the canonical GET /jobs/{job_id}
// surface — looked up for real, never compared to itself.
func TestBenchmark_AcceptedShapeAndJobResolves(t *testing.T) {
	f := newBenchmarkFixture(t, true, ratingIndex(78, true))

	rec, body := f.post(t, benchModelID)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body %s)", rec.Code, rec.Body.String())
	}
	data, ok := body["data"].(map[string]any)
	if !ok {
		t.Fatalf("no data object: %#v", body)
	}
	jobID, _ := data["job_id"].(string)
	if jobID == "" {
		t.Fatalf("job_id = %#v, want a non-empty id", data["job_id"])
	}
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
	f := newBenchmarkFixture(t, true, ratingIndex(78, true))
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

// TestBenchmark_DisabledIsRefusedAndCreatesNoJob proves the owner-enabled gate.
//
// The gate is `enrichment_enabled` (PUT /settings, PUT /settings/enrichment):
// 04 §2b classes the analysis leaderboard as pipeline B ("Metadata
// enrichment... Off by default; owner-enabled"), which is exactly the pipeline
// this endpoint drives.
func TestBenchmark_DisabledIsRefusedAndCreatesNoJob(t *testing.T) {
	f := newBenchmarkFixture(t, false, ratingIndex(78, true))
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
}

// TestBenchmark_NoSignalCompletesWithoutWritingARating is the never-fabricate
// proof. It seeds a PRE-EXISTING rating so the assertion also catches the
// "helpfully overwrite it with 0" failure, not just "wrote something to NULL".
func TestBenchmark_NoSignalCompletesWithoutWritingARating(t *testing.T) {
	f := newBenchmarkFixture(t, true, noSignalIndex())

	const preexisting = 64.5
	if _, err := f.db.Conn().Exec(`UPDATE models SET quality_rating = ? WHERE id = ?`, preexisting, benchModelID); err != nil {
		t.Fatalf("seed pre-existing rating: %v", err)
	}

	rec, body := f.post(t, benchModelID)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}
	jobID := body["data"].(map[string]any)["job_id"].(string)

	row := f.awaitTerminalJob(t, jobID)
	if row.Status != storage.JobCompleted {
		t.Fatalf("job status = %q, want completed — a leaderboard with no signal is a legitimate outcome, not a failure", row.Status)
	}

	got := f.qualityRating(t)
	if got == nil {
		t.Fatalf("quality_rating became NULL; the pre-existing %v must be left alone", preexisting)
	}
	if *got != preexisting {
		t.Fatalf("quality_rating = %v, want the untouched %v (never an invented or zero rating)", *got, preexisting)
	}
}

// TestBenchmark_SignalPersistsRatingWithProvenance proves the happy path
// writes through the precedence engine and records where the value came from.
func TestBenchmark_SignalPersistsRatingWithProvenance(t *testing.T) {
	const rating = 81.5
	f := newBenchmarkFixture(t, true, ratingIndex(rating, true))

	_, body := f.post(t, benchModelID)
	jobID := body["data"].(map[string]any)["job_id"].(string)
	row := f.awaitTerminalJob(t, jobID)
	if row.Status != storage.JobCompleted {
		t.Fatalf("job status = %q (err %+v), want completed", row.Status, row.Error)
	}

	got := f.qualityRating(t)
	if got == nil || *got != rating {
		t.Fatalf("quality_rating = %v, want %v", got, rating)
	}

	// Provenance: an audit row records the winning evidence's source and
	// dataset version. models has no provenance column (00006 is frozen and
	// this unit adds no migration), so the audit trail is where it lands.
	var reason string
	if err := f.db.Conn().QueryRow(
		`SELECT reason_code FROM audit_events WHERE action = ? AND entity_id = ? ORDER BY id DESC LIMIT 1`,
		auditActionModelQualityRating, benchModelID,
	).Scan(&reason); err != nil {
		t.Fatalf("read provenance audit row: %v", err)
	}
	for _, want := range []string{"source=external_registry", "dataset=2026-07", "exact_identity_match=true"} {
		if !strings.Contains(reason, want) {
			t.Fatalf("provenance reason %q is missing %q", reason, want)
		}
	}
}

// TestBenchmark_NonExactMatchNeverCertifies proves a name/family match cannot
// write a canonical rating: 04 §4 downgrades heuristic-sourced evidence to
// probe_suggested, which is NOT a resolved value.
func TestBenchmark_NonExactMatchNeverCertifies(t *testing.T) {
	f := newBenchmarkFixture(t, true, ratingIndex(90, false))

	_, body := f.post(t, benchModelID)
	jobID := body["data"].(map[string]any)["job_id"].(string)
	row := f.awaitTerminalJob(t, jobID)
	if row.Status != storage.JobCompleted {
		t.Fatalf("job status = %q, want completed", row.Status)
	}
	if got := f.qualityRating(t); got != nil {
		t.Fatalf("quality_rating = %v, want NULL — a non-exact identity match can only suggest a probe, never certify (04 §4)", *got)
	}
}

// TestBenchmark_SurvivesClientCancellation proves the accepted job runs on a
// DETACHED context. Once the caller has been told 202 with a job id, a
// disconnecting client must not silently cancel the work that id refers to.
func TestBenchmark_SurvivesClientCancellation(t *testing.T) {
	const rating = 55.0
	release := make(chan struct{})
	f := newBenchmarkFixture(t, true, func(ctx context.Context, _, _ string) (intelligence.QualityEntry, bool, error) {
		// Block until the test has cancelled the client request, so the
		// leaderboard read genuinely happens after cancellation.
		<-release
		if err := ctx.Err(); err != nil {
			return intelligence.QualityEntry{}, false, err
		}
		r := rating
		return intelligence.QualityEntry{
			Rating: &r, SourceRef: "analysis-leaderboard/v3",
			ExactIdentityMatch: true, DatasetVersion: "2026-07",
		}, true, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	_, body := f.postCtx(t, benchModelID, ctx)
	jobID := body["data"].(map[string]any)["job_id"].(string)

	cancel()
	close(release)

	row := f.awaitTerminalJob(t, jobID)
	if row.Status != storage.JobCompleted {
		t.Fatalf("job status = %q (err %+v), want completed after client cancellation", row.Status, row.Error)
	}
	got := f.qualityRating(t)
	if got == nil || *got != rating {
		t.Fatalf("quality_rating = %v, want %v — the detached run must still persist its result", got, rating)
	}
}

// TestBenchmark_MethodNotAllowed proves the endpoint is POST-only.
func TestBenchmark_MethodNotAllowed(t *testing.T) {
	f := newBenchmarkFixture(t, true, ratingIndex(78, true))

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
// contract: one row, secret-free.
func TestBenchmark_EmitsExactlyOneAuditRowOnAccept(t *testing.T) {
	f := newBenchmarkFixture(t, true, noSignalIndex())

	_, body := f.post(t, benchModelID)
	jobID := body["data"].(map[string]any)["job_id"].(string)
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
