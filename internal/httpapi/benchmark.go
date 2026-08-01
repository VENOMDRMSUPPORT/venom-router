package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/intelligence"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// benchmark.go serves P6-CAPI-001's third endpoint: POST
// /models/{id}/benchmark — the owner-triggered refresh of a canonical
// model's QUALITY RATING (04 §3/§5, 09 §3.12).
//
// WHAT THIS IS NOT. It runs no inference, contacts no provider, and creates
// no table. 04 §5 defines canonical quality as "one reproducible rating per
// exact model version (never re-run per provider)", sourced from "a public
// benchmark (exact match, with a versioned calibration to a 0-100 scale)";
// 04 §2 names that source "an analysis leaderboard for quality signals". So
// the work here is exactly one leaderboard read (through the existing
// intelligence.QualityIndex seam), resolved through the SAME precedence
// engine every other fact goes through, landing in the frozen 00006
// models.quality_rating column.
//
// OWNER-ENABLED GATE. 04 §2b classifies the analysis leaderboard as pipeline
// B ("Metadata enrichment... Default state: Off by default; owner-enabled"),
// as opposed to pipeline A's always-on free-safety resolution. This endpoint
// drives pipeline B and nothing else, so its gate IS the existing
// enrichment_enabled owner toggle rather than a new flag. The refusal is 409,
// not 403: the caller is an authenticated, fully authorized owner, and the
// request conflicts with current STATE (a pipeline they have switched off),
// which is the same shape as probe.go's 409 probe_unsupported. A 403 would
// wrongly imply the owner lacks permission to do this at all.

// benchmarkRunTimeout bounds the detached background run, mirroring
// probeRunTimeout's role for a probe.
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

// BenchmarkHandler serves POST /models/{id}/benchmark. Owner-session + CSRF
// gated via ControlMux's `gated`.
type BenchmarkHandler struct {
	catalog  *storage.CatalogRepo
	jobs     *storage.JobRepo
	settings *storage.SettingsRepo
	// quality is the injected analysis-leaderboard read. A nil seam is a
	// leaderboard that always misses — which completes the job honestly
	// without a rating, never a fabricated one.
	quality intelligence.QualityIndex
	audit   *auditEmitter
	newID   func() string
	now     func() time.Time
}

// NewBenchmarkHandler builds the handler. newID/now default to
// newOAuthTransactionID/time.Now when nil, like every other handler here.
func NewBenchmarkHandler(
	catalog *storage.CatalogRepo,
	jobs *storage.JobRepo,
	settings *storage.SettingsRepo,
	quality intelligence.QualityIndex,
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
		quality: quality, audit: audit, newID: newID, now: now,
	}
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
			"canonical quality benchmarking reads an external analysis leaderboard, which is part of the owner-enabled metadata-enrichment pipeline; enable enrichment_enabled via PUT /settings first",
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

// runBenchmark performs the one leaderboard read and resolves it.
//
// The three outcomes, and why "no rating written" is a SUCCESS in two of them:
//
//   - The leaderboard has no entry, or the entry carries a nil Rating: 04 §3's
//     "NULL means 'no quality signal available'". The job completes and the
//     stored rating is left exactly as it was. Writing 0 here would be a
//     fabricated fact, and overwriting an existing rating with "we found
//     nothing this time" would destroy a real one.
//   - The entry is a non-exact (name/family) match: enrichmentProvenance
//     stamps it SourceHeuristic, and 04 §4 downgrades a heuristic winner to
//     probe_suggested — "heuristics may schedule a probe but can never
//     certify". Not a resolved value, so nothing is written.
//   - The entry is an exact identity match: SourceExternalRegistry evidence
//     resolves to a known value, which is persisted with its provenance.
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

	resolution, ok := h.resolveQuality(ctx, model)
	if !ok {
		// No signal, or a signal that cannot certify. A legitimate outcome:
		// the job completed, and the model's rating is untouched.
		_ = h.jobs.MarkTerminal(context.Background(), jobID, storage.JobCompleted, h.now(),
			benchmarkResultRef, nil, storage.DefaultJobRetention)
		return
	}

	rating, isFloat := resolution.Value.(float64)
	if !isFloat {
		// The precedence engine carries Value as `any`; a non-numeric winner
		// is a malformed source, not a rating. Fail closed rather than
		// coercing it.
		h.failJob(jobID, "benchmark_failed", "resolved quality value was not a number")
		return
	}

	if err := h.catalog.SetQualityRating(ctx, model.ModelID, rating, h.now()); err != nil {
		h.failJob(jobID, "benchmark_failed", "quality rating write failed")
		return
	}

	// Provenance (04 §3: a rating is "always anchored to a documented source,
	// observed date, and confidence value"). Codes and a dataset version only
	// — never a response body.
	h.audit.Emit(context.Background(), auditActionModelQualityRating, AuditResultSuccess,
		auditResourceModel, model.ModelID, benchmarkProvenanceReason(resolution.Winner))

	_ = h.jobs.MarkTerminal(context.Background(), jobID, storage.JobCompleted, h.now(),
		benchmarkResultRef, nil, storage.DefaultJobRetention)
}

// resolveQuality performs the leaderboard read and runs the result through
// the precedence engine. ok=false means "no rating may be written".
func (h *BenchmarkHandler) resolveQuality(ctx context.Context, model storage.CanonicalModelRow) (intelligence.Resolution, bool) {
	if h.quality == nil {
		return intelligence.Resolution{}, false
	}

	entry, hit, err := h.quality(ctx, model.ProviderID, model.ProviderModelID)
	if err != nil || !hit {
		return intelligence.Resolution{}, false
	}

	// The EnrichmentService seam and this endpoint agree on one shared
	// Evidence shape: intelligence.QualityEvidence is the single place a
	// QualityEntry becomes precedence-engine Evidence, so the two callers can
	// never stamp different provenance for the same leaderboard row.
	scope := intelligence.Scope{ProviderModelID: model.ProviderModelID}
	now := h.now()
	evidence := intelligence.QualityEvidence(scope, entry, now)
	if len(evidence) == 0 {
		// A hit whose Rating is nil: the leaderboard knows the model but has
		// no score for it.
		return intelligence.Resolution{}, false
	}

	resolution := intelligence.Resolve(intelligence.FieldQualityRating, evidence, now)
	if resolution.Kind != intelligence.ResolutionKnown {
		return intelligence.Resolution{}, false
	}
	return resolution, true
}

// benchmarkProvenanceReason renders the winning evidence's provenance as a
// short, secret-free audit reason.
func benchmarkProvenanceReason(w intelligence.Evidence) string {
	return fmt.Sprintf("source=%s,verification=%s,confidence=%.2f,dataset=%s,exact_identity_match=%t",
		w.Source, w.Verification, w.Confidence, w.DatasetVersion, w.ExactIdentityMatch)
}
