package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

func TestJobsGet_UnauthenticatedRejected(t *testing.T) {
	mux := ControlMux(testAllowedHost, fakeSPA(), testControlDB(t), testKeyring(t))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, newAuthRequest(t, http.MethodGet, "/api/control/v1/jobs/some-job", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d without a session (P2b-CAPI-001 gate)", rec.Code, http.StatusUnauthorized)
	}
}

func TestJobsGet_UnknownID404(t *testing.T) {
	mux := ControlMux(testAllowedHost, fakeSPA(), testControlDB(t), testKeyring(t))
	cookie := setupOwner(t, mux, testSetupPassword)

	rec := getGated(t, mux, "/api/control/v1/jobs/does-not-exist", cookie)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != "not_found" {
		t.Fatalf("error code = %q, want not_found", code)
	}
}

func TestJobsGet_KnownIDReturnsDocumentedBody(t *testing.T) {
	db := testControlDB(t)
	mux := ControlMux(testAllowedHost, fakeSPA(), db, testKeyring(t))
	cookie := setupOwner(t, mux, testSetupPassword)

	jobs := storage.NewJobRepo(db)
	now := time.Now().UTC().Truncate(time.Second)
	if err := jobs.Create(t.Context(), "job-xyz", "discovery", now); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := jobs.MarkRunning(t.Context(), "job-xyz", now.Add(time.Second)); err != nil {
		t.Fatalf("MarkRunning: %v", err)
	}

	rec := getGated(t, mux, "/api/control/v1/jobs/job-xyz", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %q", rec.Code, http.StatusOK, rec.Body.String())
	}

	var got struct {
		Data jobJSON `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Data.JobID != "job-xyz" || got.Data.Kind != "discovery" || got.Data.Status != "running" {
		t.Fatalf("job = %+v, want {job-xyz discovery running ...}", got.Data)
	}
	if got.Data.StartedAt == nil {
		t.Fatalf("started_at missing for a running job")
	}
	if got.Data.FinishedAt != nil {
		t.Fatalf("finished_at present for a still-running job")
	}
}

func TestJobsGet_RepollDoesNotMutate(t *testing.T) {
	db := testControlDB(t)
	mux := ControlMux(testAllowedHost, fakeSPA(), db, testKeyring(t))
	cookie := setupOwner(t, mux, testSetupPassword)

	jobs := storage.NewJobRepo(db)
	now := time.Now().UTC().Truncate(time.Second)
	if err := jobs.Create(t.Context(), "job-poll", "probe", now); err != nil {
		t.Fatalf("Create: %v", err)
	}

	first := getGated(t, mux, "/api/control/v1/jobs/job-poll", cookie)
	second := getGated(t, mux, "/api/control/v1/jobs/job-poll", cookie)

	if first.Body.String() != second.Body.String() {
		t.Fatalf("re-polling changed the response: first=%q second=%q", first.Body.String(), second.Body.String())
	}

	row, ok, err := jobs.GetByID(t.Context(), "job-poll")
	if err != nil || !ok {
		t.Fatalf("GetByID: ok=%v err=%v", ok, err)
	}
	if row.Status != storage.JobPending {
		t.Fatalf("Status = %q after re-polling, want unchanged pending", row.Status)
	}
}

// TestJobsGet_ErrorFieldDoesNotLeakBeyondItself is JOBS-001's canary: a
// distinctive value placed in a job's error.message must appear ONLY
// under data.error.message in the response — never duplicated into any
// other field (id/kind/status/result_ref) via an encoding mistake.
func TestJobsGet_ErrorFieldDoesNotLeakBeyondItself(t *testing.T) {
	const canary = "CANARY-9f3Kx2Qw8pLm0Zt7Vb4Nr1Hy6Dc5Ea-joberror"

	db := testControlDB(t)
	mux := ControlMux(testAllowedHost, fakeSPA(), db, testKeyring(t))
	cookie := setupOwner(t, mux, testSetupPassword)

	jobs := storage.NewJobRepo(db)
	now := time.Now().UTC().Truncate(time.Second)
	if err := jobs.Create(t.Context(), "job-err", "probe", now); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := jobs.MarkTerminal(t.Context(), "job-err", storage.JobFailed, now, "", &storage.JobError{Code: "probe_failed", Message: canary}, storage.DefaultJobRetention); err != nil {
		t.Fatalf("MarkTerminal: %v", err)
	}

	rec := getGated(t, mux, "/api/control/v1/jobs/job-err", cookie)
	var got struct {
		Data jobJSON `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got.Data.Error == nil || got.Data.Error.Message != canary {
		t.Fatalf("error.message = %v, want the canary value present exactly there", got.Data.Error)
	}
	if got.Data.JobID != "job-err" || got.Data.Kind != "probe" || got.Data.Status != "failed" || got.Data.ResultRef != "" {
		t.Fatalf("the canary leaked outside error.message: %+v", got.Data)
	}
}

// TestJobs_OnlyOneCanonicalStatusSurface probes a handful of plausible
// competing "per-resource status" URL shapes and confirms none of them
// serves a job-shaped response — GET /jobs/{job_id} is the only surface.
func TestJobs_OnlyOneCanonicalStatusSurface(t *testing.T) {
	mux := ControlMux(testAllowedHost, fakeSPA(), testControlDB(t), testKeyring(t))
	cookie := setupOwner(t, mux, testSetupPassword)

	for _, path := range []string{
		"/api/control/v1/jobs/abc/status",
		"/api/control/v1/accounts/abc/job",
		"/api/control/v1/accounts/abc/status",
		"/api/control/v1/job-status/abc",
	} {
		rec := getGated(t, mux, path, cookie)
		if rec.Code != http.StatusOK {
			continue // 401/403/404 — not a competing surface
		}
		var probe struct {
			Data struct {
				JobID string `json:"job_id"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &probe); err == nil && probe.Data.JobID != "" {
			t.Fatalf("path %q unexpectedly served a job-status-shaped response — a competing surface exists", path)
		}
	}
}
