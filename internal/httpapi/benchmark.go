package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/application"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/execution"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// benchmark.go serves P6-CAPI-001's third endpoint: POST
// /models/{id}/benchmark — the owner-triggered refresh of a canonical
// model's QUALITY RATING (04 §3/§5, 09 §3.12).
//
// Plan 3 of the local-benchmark-rating design (2026-08-05) replaced this
// job's original no-op quality resolution with a REAL measurement: a small
// fixed suite of streamed chat completions run against the model's own best
// LIVE offering, on the owner's own account. There is no imported
// leaderboard number anywhere in this file any more (spec D4: "No imported
// leaderboard numbers" — the honest-model-verification design's local
// benchmark is "the one genuinely new unit"). The rating this job writes is
// therefore a LOCAL, on-account measurement, not a universal quality score.
//
// OWNER-ENABLED GATE. The benchmark spends the owner's own account
// credits/quota running real inference, so — exactly like the metadata
// enrichment pipeline it replaced — it stays behind the existing
// enrichment_enabled owner toggle (04 §2b's pipeline-B framing) rather than
// a new flag. The refusal is 409, not 403: the caller is an authenticated,
// fully authorized owner, and the request conflicts with current STATE (a
// pipeline they have switched off), which is the same shape as probe.go's
// 409 probe_unsupported. A 403 would wrongly imply the owner lacks
// permission to do this at all.

// benchmarkRunTimeout bounds the detached background run, mirroring
// probeRunTimeout's role for a probe. It must comfortably fit
// benchmarkDefaultRequests sequential streamed completions.
const benchmarkRunTimeout = 2 * time.Minute

// auditActionModelBenchmark records the accept of a benchmark request;
// auditActionModelQualityRating records the rating write itself, carrying the
// winning evidence's PROVENANCE (04 §3 requires a rating be "always anchored
// to a documented source"). models has no provenance column and this unit
// adds no migration, so the audit trail is where that provenance lives.
const (
	auditActionModelBenchmark     = "model_benchmark"
	auditActionModelQualityRating = "model_quality_rating"
	auditResourceModel            = "model"
)

// benchmarkResultRef is the ONE reference a benchmark job's result_ref takes
// (09 §3.12: a reference, never inline content) — the read model that now
// reports the rating.
const benchmarkResultRef = "/api/control/v1/models"

// benchmarkNoLiveOffering is the typed job-failure code for a model with no
// LIVE offering (CatalogRepo.ListOfferings' LiveOnly gate: available,
// account connected/healthy/not-reauthenticating, AND a certified+supported
// chat offering_operation — Plan 1's honest gate). There is nothing this job
// could safely dispatch real inference against, so it fails rather than
// fabricating a target or silently completing with nothing measured.
const benchmarkNoLiveOffering = "no_live_offering"

// BenchmarkHandler serves POST /models/{id}/benchmark. Owner-session + CSRF
// gated via ControlMux's `gated`.
type BenchmarkHandler struct {
	catalog  *storage.CatalogRepo
	jobs     *storage.JobRepo
	settings *storage.SettingsRepo
	// runs persists every attempt's measurement (benchmark_runs,
	// 00017_benchmark_runs.sql) — ALWAYS, even when the suite's success gate
	// fails, because the measurement itself is evidence.
	runs *storage.BenchmarkRunRepo
	// stream runs ONE streamed completion for a given offering. Production
	// wiring (buildBenchmarkStreamFn, this file) drives the real dispatch
	// path; tests inject a fake to control exactly which samples the suite
	// sees without any network I/O.
	stream benchmarkStreamFn
	audit  *auditEmitter
	newID  func() string
	now    func() time.Time
}

// NewBenchmarkHandler builds the handler. newID/now default to
// newOAuthTransactionID/time.Now when nil, like every other handler here.
func NewBenchmarkHandler(
	catalog *storage.CatalogRepo,
	jobs *storage.JobRepo,
	settings *storage.SettingsRepo,
	runs *storage.BenchmarkRunRepo,
	stream benchmarkStreamFn,
	audit *auditEmitter,
	newID func() string,
	now func() time.Time,
) *BenchmarkHandler {
	if newID == nil {
		newID = newOAuthTransactionID
	}
	if now == nil {
		now = time.Now
	}
	return &BenchmarkHandler{
		catalog: catalog, jobs: jobs, settings: settings,
		runs: runs, stream: stream, audit: audit, newID: newID, now: now,
	}
}

// buildBenchmarkStreamFn composes the PRODUCTION benchmarkStreamFn from the
// SAME tables the request path uses (liveTransportImpls +
// liveProviderBaseURLs, chatcompletions.go) plus the account credential
// repo/service ControlMux already builds for account reveal. It is a
// separate, directly-callable function — rather than inlined into
// ControlMux — specifically so a test can drive the EXACT composition
// production wires and prove that mutating it (not some test-owned
// equivalent) is what the test's assertions depend on
// (benchmark_composition_test.go's mutation-proof test) — the same
// composition-root mutation discipline this package already applies to
// BuildInferenceDispatcher.
func buildBenchmarkStreamFn(
	reg *providers.Registry,
	credentialRepo *storage.AccountCredentialRepo,
	credentialService *application.CredentialService,
) benchmarkStreamFn {
	httpClient := &http.Client{Timeout: execution.DefaultOpenAICompatibleTimeout}
	impls := liveTransportImpls(httpClient, reg)
	dispatcher := BuildInferenceDispatcher(reg, impls)
	baseURLs := liveProviderBaseURLs()
	baseURLFor := func(providerID string) string { return baseURLs[providers.ProviderID(providerID)] }
	return newBenchmarkStreamFn(dispatcher, baseURLFor, credentialRepo, credentialService)
}

// benchmarkResponseJSON is the 202 payload — the same {job_id, status_url}
// shape discovery/probe return (09 §3.7/§3.8/§3.12).
type benchmarkResponseJSON struct {
	JobID     string `json:"job_id"`
	StatusURL string `json:"status_url"`
}

// ServeBenchmark implements POST /api/control/v1/models/{id}/benchmark.
//
// Every precondition runs BEFORE any job row exists, so a rejection at any
// step leaves the database exactly as it was (probe.go's ordering discipline).
func (h *BenchmarkHandler) ServeBenchmark(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAuthError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false)
		return
	}

	ctx := r.Context()
	modelID := r.PathValue("id")

	// 1. Unknown model -> 404. Nothing created.
	model, ok, err := h.catalog.GetCanonicalModel(ctx, modelID)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}
	if !ok {
		h.audit.Emit(ctx, auditActionModelBenchmark, AuditResultFailure, auditResourceModel, modelID, "not_found")
		writeAuthError(w, http.StatusNotFound, "not_found", "model not found", false)
		return
	}

	// 2. Owner-enabled gate (04 §2b pipeline B) -> 409. Nothing created.
	settings, err := h.settings.Get(ctx)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}
	if !settings.EnrichmentEnabled {
		h.audit.Emit(ctx, auditActionModelBenchmark, AuditResultFailure, auditResourceModel, modelID, "enrichment_disabled")
		writeErrorDetails(w, http.StatusConflict, "enrichment_disabled",
			"the local benchmark runs real inference on the owner's own account, which is part of the owner-enabled metadata-enrichment pipeline; enable enrichment_enabled via PUT /settings first",
			false, nil)
		return
	}

	// 3. Mint the job, respond 202, then run detached.
	jobID := h.newID()
	if err := h.jobs.Create(ctx, jobID, string(storage.JobKindBenchmark), h.now()); err != nil {
		h.audit.Emit(ctx, auditActionModelBenchmark, AuditResultFailure, auditResourceModel, modelID, "internal")
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}

	h.audit.Emit(ctx, auditActionModelBenchmark, AuditResultSuccess, auditResourceModel, modelID, "")
	writeData(w, http.StatusAccepted, benchmarkResponseJSON{
		JobID:     jobID,
		StatusURL: "/api/control/v1/jobs/" + jobID,
	})

	// context.WithoutCancel: the caller has already been handed a job id and
	// told 202. If the run inherited r.Context(), a client that disconnects
	// (or simply a handler that has returned) would cancel work the owner can
	// still see a job row for — the job would sit `running` forever, or fail
	// for a reason that has nothing to do with the benchmark.
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), benchmarkRunTimeout)
	go func() {
		defer cancel()
		h.runBenchmark(runCtx, jobID, model)
	}()
}

// failJob marks jobID terminally failed, on context.Background() because the
// run's own deadline may already have passed (probe.go's convention).
func (h *BenchmarkHandler) failJob(jobID, code, message string) {
	_ = h.jobs.MarkTerminal(context.Background(), jobID, storage.JobFailed, h.now(), "",
		&storage.JobError{Code: code, Message: message}, storage.DefaultJobRetention)
}

// runBenchmark resolves the model's one live offering, runs the fixed
// measurement suite against it, and persists what it measured.
//
// The three job outcomes:
//   - No live offering: the job FAILS typed no_live_offering. No
//     benchmark_runs row is written — there was nothing to measure.
//   - A live offering exists: a benchmark_runs row is inserted ALWAYS once
//     the suite has run, even when every request failed — the measurement
//     itself is evidence, never discarded because the news was bad.
//   - models.quality_rating is written ONLY when the aggregate's Rating is
//     non-nil (runBenchmarkSuite's own gate: every request in the suite
//     succeeded). A suite with any failure measures reliability as much as
//     speed, and writing a rating anyway would hide that.
func (h *BenchmarkHandler) runBenchmark(ctx context.Context, jobID string, model storage.CanonicalModelRow) {
	defer func() {
		if rec := recover(); rec != nil {
			h.failJob(jobID, "internal", "benchmark run failed unexpectedly")
		}
	}()

	startedAt := h.now()
	if err := h.jobs.MarkRunning(ctx, jobID, startedAt); err != nil {
		h.failJob(jobID, "internal", "benchmark run failed to start")
		return
	}

	offering, ok, err := h.targetOffering(ctx, model)
	if err != nil {
		h.failJob(jobID, "internal", "benchmark run failed to resolve a live offering")
		return
	}
	if !ok {
		h.failJob(jobID, benchmarkNoLiveOffering, "the model has no live (verified chat) offering to benchmark")
		return
	}

	aggregate := runBenchmarkSuite(ctx, h.stream, offering.AccountID, offering.ProviderID, offering.ProviderModelID, benchmarkDefaultRequests)
	finishedAt := h.now()

	run := storage.BenchmarkRun{
		ID:              h.newID(),
		ModelID:         model.ModelID,
		AccountID:       offering.AccountID,
		ProviderID:      offering.ProviderID,
		ProviderModelID: offering.ProviderModelID,
		Requests:        aggregate.Requests,
		Successes:       aggregate.Successes,
		TTFTMillis:      aggregate.TTFTMillis,
		TokensPerSec:    aggregate.TokensPerSec,
		Rating:          aggregate.Rating,
		StartedAt:       startedAt,
		FinishedAt:      finishedAt,
	}
	if err := h.runs.Insert(ctx, run); err != nil {
		h.failJob(jobID, "internal", "benchmark run failed to persist its measurement")
		return
	}

	if aggregate.Rating != nil {
		if err := h.catalog.SetQualityRating(ctx, model.ModelID, *aggregate.Rating, h.now()); err != nil {
			h.failJob(jobID, "benchmark_failed", "quality rating write failed")
			return
		}
		// Provenance (04 §3: a rating is "always anchored to a documented
		// source, observed date, and confidence value"). Codes and ids only
		// — never a response body.
		h.audit.Emit(context.Background(), auditActionModelQualityRating, AuditResultSuccess,
			auditResourceModel, model.ModelID, benchmarkRunProvenanceReason(run))
	}

	_ = h.jobs.MarkTerminal(context.Background(), jobID, storage.JobCompleted, h.now(),
		benchmarkResultRef, nil, storage.DefaultJobRetention)
}

// targetOffering resolves model's benchmark target: its first LIVE offering
// (CatalogRepo.ListOfferings with LiveOnly: true — availability=available,
// account connected/healthy/not-reauthenticating, AND a certified+supported
// chat offering_operation — Plan 1's honest gate), ordered deterministically
// by (account_id, provider_model_id) exactly as ListOfferings itself orders.
// It walks every page rather than assuming the model's one live offering
// sits on page one.
func (h *BenchmarkHandler) targetOffering(ctx context.Context, model storage.CanonicalModelRow) (storage.CatalogOfferingRow, bool, error) {
	cursor := ""
	for {
		rows, next, err := h.catalog.ListOfferings(ctx, storage.CatalogListParams{LiveOnly: true, Cursor: cursor})
		if err != nil {
			return storage.CatalogOfferingRow{}, false, err
		}
		for _, row := range rows {
			if row.ModelID == model.ModelID {
				return row, true, nil
			}
		}
		if next == "" {
			return storage.CatalogOfferingRow{}, false, nil
		}
		cursor = next
	}
}

// benchmarkRunProvenanceReason renders one persisted benchmark_runs row as a
// short, secret-free audit reason — 04 §3's "always anchored to a documented
// source" for the LOCAL-benchmark source (spec D4): which run, account, and
// offering was measured, how many requests succeeded, and the resulting
// rating. Only ever called when run.Rating is non-nil.
func benchmarkRunProvenanceReason(run storage.BenchmarkRun) string {
	return fmt.Sprintf("source=local_benchmark,run_id=%s,account_id=%s,provider_model_id=%s,requests=%d,successes=%d,rating=%.4f",
		run.ID, run.AccountID, run.ProviderModelID, run.Requests, run.Successes, *run.Rating)
}
