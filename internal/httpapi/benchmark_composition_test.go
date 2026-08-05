package httpapi

// benchmark_composition_test.go proves the ONE thing benchmark_test.go's
// direct-fixture tests cannot: that the benchmark job's stream seam, as
// ControlMux actually wires it in production (buildBenchmarkStreamFn,
// benchmark.go), really does drive a live offering through the REAL
// dispatch path — the real provider registry, the real execution.Dispatcher,
// a real OpenAICompatibleTransport, and a real encrypted credential lease —
// rather than depending on a test-owned fake seam that could silently
// diverge from what controlmux.go actually constructs. This is the
// "test-owned-fixture hole" class of defect this package has hit twice
// before (see the governor MEMORY note on mutating the composition ROOT):
// every other benchmark test in this package calls NewBenchmarkHandler
// directly with its OWN fake stream, so none of them would notice if
// controlmux.go's `buildBenchmarkStreamFn(reg, credentialRepo,
// credentialService)` call were replaced with nil or a stub. This test
// calls buildBenchmarkStreamFn itself — the exact function controlmux.go
// calls — so mutating what THAT function builds is what the mutation proof
// (task-5-report.md) exercises.
//
// The only fake here is the outbound HTTP transport: http.DefaultTransport
// is swapped process-wide for the duration of the test (the same technique
// usability_trigger_controlmux_test.go already uses for the identical
// problem — a fixed production base URL with no injection seam — safe
// because this package runs serially, no t.Parallel()).

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/application"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/secrets"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

const (
	benchCompAccountID  = "acct-bench-composition"
	benchCompModelID    = "model-bench-composition"
	benchCompProvModel  = "zen/bench-composition-model"
	benchCompProviderID = string(providers.OpenCodeZenID)
)

// benchCompFakeZenTransport answers ONLY the exact chat/completions POST the
// benchmark's fixed fixture drives (the same base URL liveProviderBaseURLs()
// resolves for opencode-zen), with a well-formed two-chunk SSE completion —
// anything else fails the test loudly rather than silently falling through
// to a real socket.
type benchCompFakeZenTransport struct {
	t *testing.T
}

func (rt benchCompFakeZenTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	wantURL := providers.OpenCodeZenBaseURL + "/v1/chat/completions"
	if req.Method != http.MethodPost || req.URL.String() != wantURL {
		rt.t.Fatalf("unexpected outbound HTTP request during the benchmark composition test: %s %s", req.Method, req.URL.String())
		return nil, fmt.Errorf("unexpected request: %s %s", req.Method, req.URL.String())
	}
	body := "data: {\"choices\":[{\"delta\":{\"content\":\"one\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"two\"},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Request:    req,
	}, nil
}

// seedBenchmarkCompositionFixture seeds a REAL opencode-zen provider,
// connected+healthy account with a REAL encrypted active credential, and a
// canonical model whose one alias carries a LIVE offering (available,
// certified+supported chat) — everything ControlMux's own composition needs
// to run a real benchmark job end to end.
func seedBenchmarkCompositionFixture(t *testing.T, db *storage.DB, kr *secrets.Keyring) {
	t.Helper()

	if _, err := db.Conn().Exec(
		`INSERT OR IGNORE INTO providers (id, display_name, auth_mode, funding_mode, funding_locked, created_at, updated_at)
		 VALUES (?, ?, 'api_key', 'owner_policy', 0, 0, 0)`, benchCompProviderID, benchCompProviderID,
	); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if _, err := db.Conn().Exec(
		`INSERT INTO accounts (id, provider_id, external_id, auth_type, connection_state, health_state, reauth_in_progress, created_at, updated_at)
		 VALUES (?, ?, ?, 'api_key', 'connected', 'healthy', 0, 0, 0)`,
		benchCompAccountID, benchCompProviderID, benchCompAccountID,
	); err != nil {
		t.Fatalf("seed account: %v", err)
	}

	credentialRepo := storage.NewAccountCredentialRepo(db)
	credentialService := application.NewCredentialService(credentialRepo, kr, nil)
	if _, err := credentialService.Store(context.Background(), application.StoreCredentialParams{
		ID:           "cred-" + benchCompAccountID,
		AccountID:    benchCompAccountID,
		ProviderID:   benchCompProviderID,
		Kind:         domain.CredentialKindAPIKey,
		Active:       true,
		PlaintextKey: "fake-key-never-sent-to-a-real-host",
	}); err != nil {
		t.Fatalf("store credential: %v", err)
	}

	if _, err := db.Conn().Exec(
		`INSERT INTO models (id, canonical_key_sha256, display_name, quality_rating, created_at, updated_at)
		 VALUES (?, ?, 'Bench Composition Model', NULL, 0, 0)`,
		benchCompModelID, "sha-"+benchCompModelID,
	); err != nil {
		t.Fatalf("seed model: %v", err)
	}
	if _, err := db.Conn().Exec(
		`INSERT INTO provider_model_aliases (provider_id, provider_model_id, model_id) VALUES (?, ?, ?)`,
		benchCompProviderID, benchCompProvModel, benchCompModelID,
	); err != nil {
		t.Fatalf("seed alias: %v", err)
	}
	if _, err := db.Conn().Exec(
		`INSERT INTO account_model_offerings (account_id, provider_id, provider_model_id, model_id, availability, first_seen_at, last_seen_at)
		 VALUES (?, ?, ?, ?, 'available', 0, 0)`,
		benchCompAccountID, benchCompProviderID, benchCompProvModel, benchCompModelID,
	); err != nil {
		t.Fatalf("seed offering: %v", err)
	}
	opID := "op-" + benchCompAccountID + "-chat"
	if _, err := db.Conn().Exec(
		`INSERT INTO offering_operations (id, account_id, provider_id, provider_model_id, operation, created_at, updated_at)
		 VALUES (?, ?, ?, ?, 'chat', 0, 0)`,
		opID, benchCompAccountID, benchCompProviderID, benchCompProvModel,
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

// TestBenchmark_ProductionStreamComposition_DrivesRealDispatchOverFakeTransport
// is the composition-root proof: it calls buildBenchmarkStreamFn directly —
// the exact function ControlMux's own construction calls — against the real
// provider registry and a real encrypted credential, with only the outbound
// HTTP transport faked. See task-5-report.md's "Mutation proof" section for
// the paired manual mutation (buildBenchmarkStreamFn temporarily broken,
// this test observed RED, then restored byte-identical).
func TestBenchmark_ProductionStreamComposition_DrivesRealDispatchOverFakeTransport(t *testing.T) {
	// GUARD: process-wide global swap — see usability_trigger_controlmux_test.go's
	// identical guard; safe only because this package never calls t.Parallel().
	originalTransport := http.DefaultTransport
	http.DefaultTransport = benchCompFakeZenTransport{t: t}
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	db := testControlDB(t)
	kr := testKeyring(t)
	seedBenchmarkCompositionFixture(t, db, kr)

	settings := storage.NewSettingsRepo(db)
	if err := settings.PutEnrichment(context.Background(), true, benchNow); err != nil {
		t.Fatalf("seed enrichment toggle: %v", err)
	}

	reg := newProviderRegistry()
	credentialRepo := storage.NewAccountCredentialRepo(db)
	credentialService := application.NewCredentialService(credentialRepo, kr, nil)
	catalog := storage.NewCatalogRepo(db)
	jobs := storage.NewJobRepo(db)
	runs := storage.NewBenchmarkRunRepo(db, nil)

	// The exact call controlmux.go makes.
	stream := buildBenchmarkStreamFn(reg, credentialRepo, credentialService)
	handler := NewBenchmarkHandler(catalog, jobs, settings, runs, stream, newAuditEmitter(db, nil), newOAuthTransactionID, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/control/v1/models/"+benchCompModelID+"/benchmark", nil)
	req.SetPathValue("id", benchCompModelID)
	rec := httptest.NewRecorder()
	handler.ServeBenchmark(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body %s)", rec.Code, rec.Body.String())
	}

	var jobID string
	{
		var body struct {
			Data struct {
				JobID string `json:"job_id"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		jobID = body.Data.JobID
	}
	if jobID == "" {
		t.Fatalf("job_id is empty; body = %s", rec.Body.String())
	}

	deadline := time.Now().Add(10 * time.Second)
	var row storage.JobRow
	for {
		got, ok, err := jobs.GetByID(context.Background(), jobID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if ok && (got.Status == storage.JobCompleted || got.Status == storage.JobFailed) {
			row = got
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s never reached a terminal state", jobID)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if row.Status != storage.JobCompleted {
		t.Fatalf("job status = %q (err %+v), want completed — the real dispatch path must have driven a successful suite over the fake transport", row.Status, row.Error)
	}

	run, ok, err := runs.LatestForModel(context.Background(), benchCompModelID)
	if err != nil || !ok {
		t.Fatalf("LatestForModel: ok=%v err=%v", ok, err)
	}
	if run.Requests != benchmarkDefaultRequests || run.Successes != benchmarkDefaultRequests {
		t.Fatalf("run Requests/Successes = %d/%d, want %d/%d (every real dispatch call must have succeeded against the fake transport)",
			run.Requests, run.Successes, benchmarkDefaultRequests, benchmarkDefaultRequests)
	}
	if run.Rating == nil {
		t.Fatal("run.Rating = nil, want non-nil — every request succeeded")
	}
	if *run.Rating < 0 || *run.Rating > 1 {
		t.Fatalf("run.Rating = %v, want in [0,1]", *run.Rating)
	}

	var gotRating *float64
	if err := db.Conn().QueryRow(`SELECT quality_rating FROM models WHERE id = ?`, benchCompModelID).Scan(&gotRating); err != nil {
		t.Fatalf("read quality_rating: %v", err)
	}
	// The column is 0-100 (04 §3), the run's rating is 0..1 — so the
	// persisted value is the run's rating SCALED, never the raw measurement
	// (whole-branch review, 2026-08-05, finding 1).
	wantColumn := *run.Rating * 100
	if gotRating == nil || !floatsClose(*gotRating, wantColumn, 1e-9) {
		t.Fatalf("models.quality_rating = %v, want %v (the run's own rating on the column's 0-100 scale)", gotRating, wantColumn)
	}
}
