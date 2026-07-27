package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/intelligence"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// probeRoute is the fixed route key idempotencyStore.Execute keys replays
// under (mirrors discoverRoute in discovery.go).
const probeRoute = "POST /offerings/{id}/probe"

// probeRunTimeout bounds the detached background probe run this handler
// spawns after responding 202 — mirrors discoveryRunTimeout's role for
// discovery, sized generously for one probe attempt.
const probeRunTimeout = 2 * time.Minute

// probeableOperations is the fixed four-value subset of the broader
// seven-value models.Operation vocabulary this endpoint may ever probe
// (09 §3.8, 04 §5): chat/streaming are certified by other means (actual
// successful use, not a deliberate probe) and image_generation is
// reserved future scope (04 §5) — none of the three is ever accepted
// here.
var probeableOperations = map[models.Operation]bool{
	models.OperationContextWindow:    true,
	models.OperationTools:            true,
	models.OperationStructuredOutput: true,
	models.OperationVision:           true,
}

// errProbeOperationValidation is the shared sentinel every operation-
// resolution failure in resolveProbeOperation wraps.
var errProbeOperationValidation = errors.New("httpapi: invalid probe operation")

// resolveProbeOperation resolves the single operation to probe for one
// offering-operation.
//
// RESOLVED AMBIGUITY (09 §3.8 cites a PLURAL `operations` request field,
// but this endpoint's own path segment `{id}` is one offering_operation_id
// — the same identity GET /offerings/{id}/certification already uses,
// per discovery.go's ServeCertification, which this batch does not
// change). Since one offering_operation_id already carries exactly one
// fixed `operation` value, and no storage-level concept of "this id's
// sibling operations under the same offering" exists yet, `operations`
// is accepted as, at most, a ONE-element confirmation of the addressed
// row's own operation: omitted defaults to that operation; supplied, it
// must both be one of the four probeable values AND equal the row's own
// operation, or this call is rejected as a validation error. A future
// unit that gives "offering" (as opposed to "offering-operation") its own
// storage identity can widen this to true multi-operation bundling
// without breaking this contract.
func resolveProbeOperation(rowOperation string, requested []string) (models.Operation, error) {
	rowOp, err := models.ParseOperation(rowOperation)
	if err != nil {
		return "", fmt.Errorf("%w: this offering-operation has an unrecognized operation %q: %v", errProbeOperationValidation, rowOperation, err)
	}
	if !probeableOperations[rowOp] {
		return "", fmt.Errorf("%w: operation %q is not one of the four probeable operations (context_window, tools, structured_output, vision)", errProbeOperationValidation, rowOp)
	}
	if len(requested) == 0 {
		return rowOp, nil
	}
	if len(requested) != 1 {
		return "", fmt.Errorf("%w: exactly one operation may be requested per call, matching this offering-operation's own operation %q", errProbeOperationValidation, rowOp)
	}
	reqOp, err := models.ParseOperation(requested[0])
	if err != nil {
		return "", fmt.Errorf("%w: %v", errProbeOperationValidation, err)
	}
	if !probeableOperations[reqOp] {
		return "", fmt.Errorf("%w: operation %q is not one of the four probeable operations (context_window, tools, structured_output, vision)", errProbeOperationValidation, reqOp)
	}
	if reqOp != rowOp {
		return "", fmt.Errorf("%w: requested operation %q does not match this offering-operation's own operation %q", errProbeOperationValidation, reqOp, rowOp)
	}
	return rowOp, nil
}

// probeTransport is the local composite port ProbeHandler consults: every
// intelligence.ProbeTransport implementation this package wires must ALSO
// report, synchronously and per-provider, whether it can run at all —
// kept separate from intelligence.ProbeTransport itself (whose only
// method is Probe) so a stub can honestly answer "unavailable" without
// ever being asked to attempt a probe.
type probeTransport interface {
	intelligence.ProbeTransport
	Available(providerID string) bool
}

// noCooldownReader is an intelligence.ProbeCooldownReader that never
// reports a cooldown — the ONE thing `force: true` bypasses (09 §3.8);
// every other gate (cost caps, account caps, concurrency, the reservation
// itself) still goes through the real ports.
type noCooldownReader struct{}

func (noCooldownReader) ProbeCooldownUntil(_ context.Context, _ string) (*time.Time, error) {
	return nil, nil
}

// inFlightExcluding adapts ProbeRunRepo to intelligence.ProbeInFlightReader
// for one specific run: the run has already inserted its own 'running' row
// (so that the row exists ACROSS the transport call, which is the only way
// the per-provider cap in 04 §2 can actually bound anything), and must not
// then count itself. With the default cap of 1, a self-counting reader
// would make every probe refuse itself.
type inFlightExcluding struct {
	runs       *storage.ProbeRunRepo
	excludeRun string
}

func (f inFlightExcluding) InFlightProbes(ctx context.Context, providerID string) (int, error) {
	return f.runs.InFlightProbesExcluding(ctx, providerID, f.excludeRun)
}

// ProbeHandler serves the P3c-CAPI-001 probe-trigger surface: POST
// /offerings/{id}/probe (async, 202 + job). Owner-session + CSRF gated
// via ControlMux's `gated`.
type ProbeHandler struct {
	accounts    *storage.AccountRepo
	credentials *storage.AccountCredentialRepo
	catalog     *storage.CatalogRepo
	jobs        *storage.JobRepo
	certs       *storage.CertificationRepo
	probeRuns   *storage.ProbeRunRepo
	reserver    intelligence.ProbeReserver
	transport   probeTransport
	driver      *intelligence.CertificationDriver
	policy      intelligence.ProbeSafetyPolicy
	audit       *auditEmitter
	idem        *idempotencyStore
	newID       func() string
	now         func() time.Time
}

// NewProbeHandler builds the handler over every repo/service it needs.
// idem is the SAME shared idempotencyStore ControlMux already constructs
// for enrollment/discovery (never a second instance). newID/now default
// to newOAuthTransactionID/time.Now when nil.
func NewProbeHandler(
	accounts *storage.AccountRepo,
	credentials *storage.AccountCredentialRepo,
	catalog *storage.CatalogRepo,
	jobs *storage.JobRepo,
	certs *storage.CertificationRepo,
	probeRuns *storage.ProbeRunRepo,
	reserver intelligence.ProbeReserver,
	transport probeTransport,
	driver *intelligence.CertificationDriver,
	policy intelligence.ProbeSafetyPolicy,
	audit *auditEmitter,
	idem *idempotencyStore,
	newID func() string,
	now func() time.Time,
) *ProbeHandler {
	if newID == nil {
		newID = newOAuthTransactionID
	}
	if now == nil {
		now = time.Now
	}
	return &ProbeHandler{
		accounts: accounts, credentials: credentials, catalog: catalog, jobs: jobs,
		certs: certs, probeRuns: probeRuns, reserver: reserver, transport: transport,
		driver: driver, policy: policy, audit: audit, idem: idem, newID: newID, now: now,
	}
}

// certificationResultRef is the ONE reference shape a probe job's
// result_ref ever takes (09 §3.8: "result reports capability truth and
// probe-execution state separately") — the certification-read route for
// the SAME offering-operation, carrying no inline content whatsoever.
func certificationResultRef(offeringOperationID string) string {
	return "/api/control/v1/offerings/" + url.PathEscape(offeringOperationID) + "/certification"
}

// probeRequestJSON is POST .../probe's request body (09 §3.8). Both
// fields are optional; an empty/absent body is a fully valid request
// (probe this offering-operation's own operation, cooldown enforced).
type probeRequestJSON struct {
	Operations []string `json:"operations,omitempty"`
	Force      bool     `json:"force,omitempty"`
}

// probeResponseJSON is POST .../probe's 202 success payload — the same
// shape as discoverResponseJSON (09 §3.7/§3.8/§3.12): a job id and the
// ONE canonical shared status route.
type probeResponseJSON struct {
	JobID     string `json:"job_id"`
	StatusURL string `json:"status_url"`
}

// ServeProbe implements POST /api/control/v1/offerings/{id}/probe.
func (h *ProbeHandler) ServeProbe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAuthError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false)
		return
	}
	h.idem.Execute(w, r, probeRoute, h.serveProbe)
}

// serveProbe is ServeProbe's actual body, run at most once per (route,
// Idempotency-Key) pair by idem.Execute. Every precondition below runs
// BEFORE any job row (or any other side effect) is created — a rejection
// at any of these steps leaves the database exactly as it was.
func (h *ProbeHandler) serveProbe(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")

	// 1. Unknown offering-operation id -> 404. Nothing created.
	op, ok, err := h.catalog.GetOperationCertification(ctx, id)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}
	if !ok {
		h.audit.Emit(ctx, auditActionOfferingProbe, AuditResultFailure, AuditResourceOffering, id, "not_found")
		writeAuthError(w, http.StatusNotFound, "not_found", "offering operation not found", false)
		return
	}

	// 2. Decode the (fully optional) request body, then validate the
	// requested operation(s) -> 422 validation_error. Nothing created.
	var body probeRequestJSON
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			h.audit.Emit(ctx, auditActionOfferingProbe, AuditResultFailure, AuditResourceOffering, id, "validation_error")
			writeErrorDetails(w, http.StatusUnprocessableEntity, "validation_error", "invalid request body", false, nil)
			return
		}
	}
	effectiveOp, err := resolveProbeOperation(op.Operation, body.Operations)
	if err != nil {
		h.audit.Emit(ctx, auditActionOfferingProbe, AuditResultFailure, AuditResourceOffering, id, "validation_error")
		writeErrorDetails(w, http.StatusUnprocessableEntity, "validation_error", err.Error(), false, nil)
		return
	}

	// 3. The account must have an active credential; nothing created
	// otherwise (mirrors discovery.go's own precondition ordering).
	if _, ok := activeCredentialIDFor(ctx, h.credentials, op.AccountID); !ok {
		h.audit.Emit(ctx, auditActionOfferingProbe, AuditResultFailure, AuditResourceOffering, id, "credential_unavailable")
		writeErrorDetails(w, http.StatusConflict, "credential_unavailable", "account has no active credential", false, nil)
		return
	}

	// 4. Resolve the account for its ProviderID.
	account, ok, err := h.accounts.GetByID(ctx, op.AccountID)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}
	if !ok {
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}

	// 5. No probe transport available for this provider -> 409
	// probe_unsupported. Nothing created.
	if !h.transport.Available(account.ProviderID) {
		h.audit.Emit(ctx, auditActionOfferingProbe, AuditResultFailure, AuditResourceOffering, id, "probe_unsupported")
		writeErrorDetails(w, http.StatusConflict, "probe_unsupported", "no probe transport is available for this provider", false, nil)
		return
	}

	// 6. Every precondition passed: mint the job, respond 202, and THEN
	// run the actual probe on a detached background context — mirrors
	// discovery.go's ServeDiscover exactly.
	jobID := h.newID()
	now := h.now()
	if err := h.jobs.Create(ctx, jobID, string(storage.JobKindProbe), now); err != nil {
		h.audit.Emit(ctx, auditActionOfferingProbe, AuditResultFailure, AuditResourceOffering, id, "internal")
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}

	h.audit.Emit(ctx, auditActionOfferingProbe, AuditResultSuccess, AuditResourceOffering, id, "")
	writeData(w, http.StatusAccepted, probeResponseJSON{
		JobID:     jobID,
		StatusURL: "/api/control/v1/jobs/" + jobID,
	})

	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), probeRunTimeout)
	go func() {
		defer cancel()
		h.runProbe(runCtx, jobID, id, op.AccountID, account.ProviderID, op.ProviderModelID, effectiveOp, body.Force)
	}()
}

// ensureProbing drives the certification into `probing` if it is not
// already there, using the legal edges 04 §5 provides for exactly that
// purpose: observed -> probing (StartProbe) or suspended/expired ->
// probing (ReProbe). `probing` itself needs no transition. `discovered`
// (edge 1, first evidence, never yet driven by any unit in this project
// — see this batch's report) and `certified` (which must pass through
// suspended or expired first, 04 §5's own invalid-transition list) are
// NOT eligible entry states for this endpoint and are rejected.
func (h *ProbeHandler) ensureProbing(ctx context.Context, offeringOperationID string, state models.CertificationState) error {
	switch state {
	case models.CertProbing:
		return nil
	case models.CertObserved:
		_, err := h.driver.StartProbe(ctx, offeringOperationID)
		return err
	case models.CertSuspended, models.CertExpired:
		_, err := h.driver.ReProbe(ctx, offeringOperationID)
		return err
	default:
		return fmt.Errorf("certification state %q is not eligible for probing (must be observed, probing, suspended, or expired)", state)
	}
}

// probeJobErrorCode maps a probe run's error onto one of this endpoint's
// fixed job-error codes (09 §3.8 / this batch's spec): probe_unsupported,
// probe_capped, probe_cooling_down, probe_concurrency,
// probe_opt_in_required, or the generic probe_failed fallback.
// RefusalAccountCapped and RefusalQuotaRejected both map to probe_capped
// (a rolling-window cap and a reservation-rejected-for-insufficient-
// headroom refusal are both, fundamentally, a budget/cap concern — the
// fixed code list this batch specifies has no separate bucket for
// either); RefusalSafetyUnavailable (an internal port I/O failure) maps
// to the generic probe_failed, alongside any transport error that is not
// the known "unavailable" stub sentinel.
func probeJobErrorCode(err error) string {
	if refusal, ok := intelligence.RefusalOf(err); ok {
		switch refusal {
		case intelligence.RefusalOptInRequired:
			return "probe_opt_in_required"
		case intelligence.RefusalCapped, intelligence.RefusalAccountCapped, intelligence.RefusalQuotaRejected:
			return "probe_capped"
		case intelligence.RefusalConcurrency:
			return "probe_concurrency"
		case intelligence.RefusalCoolingDown:
			return "probe_cooling_down"
		default:
			return "probe_failed"
		}
	}
	if errors.Is(err, ErrProbeTransportUnavailable) {
		return "probe_unsupported"
	}
	return "probe_failed"
}

// failJob marks jobID terminally failed with the given typed code,
// mirroring runDiscovery/runQuotaRefresh's own MarkTerminal-on-background-
// context convention (the job's own timeout may already be past its
// deadline by the time this runs).
func (h *ProbeHandler) failJob(jobID, code, message string) {
	_ = h.jobs.MarkTerminal(context.Background(), jobID, storage.JobFailed, h.now(), "",
		&storage.JobError{Code: code, Message: message}, storage.DefaultJobRetention)
}

// probeEstimateAllocations reconstructs, independently, the SAME
// EstimateInput ContextProbe.Run / CapabilityProbe.Run compute internally
// when they call ProbeGuard.Admit — using only exported constants/
// functions (intelligence.ContextProbeInputTokens/ContextProbeMaxOutputTokens,
// intelligence.CapabilityFixture's returned maxOutput) — so probe_runs_costs
// can honestly record what was actually reserved, without this package
// re-deriving or duplicating the frozen probe/guard decision logic
// itself. A mismatch here would only ever affect BOOKKEEPING (the
// per-account rolling spend total ProbeSpendSince reports), never the
// admission decision itself, which intelligence.ProbeGuard.Admit already
// made correctly using its own internal computation before this function
// is ever called.
func probeEstimateAllocations(op models.Operation) []quota.Allocation {
	var input, maxOutput int
	switch op {
	case models.OperationContextWindow:
		input = intelligence.ContextProbeInputTokens
		maxOutput = intelligence.ContextProbeMaxOutputTokens
	default:
		_, fixtureMaxOutput, err := intelligence.CapabilityFixture(op)
		if err != nil {
			return nil
		}
		input = intelligence.CapabilityProbeDeclaredInputTokens
		maxOutput = fixtureMaxOutput
	}
	allocations, err := quota.Estimate(quota.EstimateInput{InputTokens: &input, MaxOutputTokens: &maxOutput}, quota.DefaultEstimatePolicy())
	if err != nil {
		return nil
	}
	return allocations
}

// runProbe executes one probe attempt end to end and terminates jobID
// accordingly. It is panic-safe: a recovered panic marks the job failed
// with a generic internal code rather than crashing the process
// (mirrors runDiscovery/QuotaWorkers' own top-of-function recover).
func (h *ProbeHandler) runProbe(ctx context.Context, jobID, offeringOperationID, accountID, providerID, providerModelID string, op models.Operation, force bool) {
	defer func() {
		if rec := recover(); rec != nil {
			h.failJob(jobID, "internal", "probe run failed unexpectedly")
		}
	}()

	startedAt := h.now()
	if err := h.jobs.MarkRunning(ctx, jobID, startedAt); err != nil {
		h.failJob(jobID, "internal", "probe run failed to start")
		return
	}

	cert, err := h.certs.Load(ctx, offeringOperationID)
	if err != nil {
		h.failJob(jobID, "probe_failed", "certification lookup failed")
		return
	}
	if err := h.ensureProbing(ctx, offeringOperationID, cert.State); err != nil {
		h.failJob(jobID, "probe_failed", err.Error())
		return
	}

	attempts, err := h.probeRuns.CountAttempts(ctx, offeringOperationID)
	if err != nil {
		h.failJob(jobID, "internal", "attempt count lookup failed")
		return
	}
	attempts++ // this attempt

	var cooldown intelligence.ProbeCooldownReader = h.probeRuns
	if force {
		// force:true bypasses ONLY the context-probe cooldown (09 §3.8) —
		// every other port (spend/in-flight/reservation) below is still
		// the real one, so cost caps and concurrency remain fully
		// enforced regardless of force.
		cooldown = noCooldownReader{}
	}
	// Claim the in-flight slot BEFORE admission and the transport call, not
	// after: InFlightProbes counts unfinished probe_runs rows, so a row
	// written only once the probe has already returned would mean the
	// per-provider cap (04 §2) never bounds anything — two concurrent
	// probes to one provider would each read zero and both proceed. The
	// guard's in-flight reader excludes this run so it cannot refuse
	// itself, and the deferred Finish below always frees the slot.
	runID := h.newID()
	class := intelligence.ProbeStandard
	if op == models.OperationContextWindow {
		class = intelligence.ProbeExpensive
	}
	if err := h.probeRuns.Start(ctx, storage.ProbeRunParams{
		ID: runID, OfferingOperationID: offeringOperationID, AccountID: accountID, ProviderID: providerID,
		Operation: string(op), Class: class, Allocations: probeEstimateAllocations(op), StartedAt: h.now(),
	}); err != nil {
		h.failJob(jobID, "internal", "probe run recording failed")
		return
	}
	runExecution := intelligence.ProbeTerminalFailure
	defer func() {
		// Whatever path this function leaves by — success, refusal, or a
		// recovered panic — the slot is released exactly once.
		_ = h.probeRuns.Finish(context.WithoutCancel(ctx), runID, runExecution, h.now())
	}()

	guard, err := intelligence.NewProbeGuard(h.policy, h.reserver, h.probeRuns, inFlightExcluding{runs: h.probeRuns, excludeRun: runID}, cooldown, h.now)
	if err != nil {
		h.failJob(jobID, "internal", "probe guard construction failed")
		return
	}

	req := intelligence.ProbeRequest{
		AccountID: accountID, ProviderID: providerID, ProviderModelID: providerModelID,
		OfferingOperationID: offeringOperationID, Operation: op,
	}

	var outcome intelligence.ProbeOutcome
	var reservationID string
	var runErr error

	if op == models.OperationContextWindow {
		cp, cpErr := intelligence.NewContextProbe(h.transport, guard, nil, h.now)
		if cpErr != nil {
			h.failJob(jobID, "internal", "context probe construction failed")
			return
		}
		report, rerr := cp.Run(ctx, req)
		outcome, reservationID, runErr = report.Outcome, report.ReservationID, rerr
	} else {
		cp, cpErr := intelligence.NewCapabilityProbe(h.transport, guard, h.now)
		if cpErr != nil {
			h.failJob(jobID, "internal", "capability probe construction failed")
			return
		}
		report, rerr := cp.Run(ctx, req)
		outcome, reservationID, runErr = report.Outcome, report.ReservationID, rerr
	}

	if runErr != nil {
		h.failJob(jobID, probeJobErrorCode(runErr), "probe refused")
		return
	}

	// The attempt reached the transport and produced an outcome: stamp the
	// reservation it obtained onto the already-open run row, and let the
	// deferred Finish close it with this outcome's execution.
	runExecution = outcome.Execution
	if reservationID != "" {
		_ = h.probeRuns.AttachReservation(ctx, runID, reservationID)
	}

	if _, err := h.driver.RecordAttempt(ctx, offeringOperationID, outcome, attempts); err != nil {
		h.failJob(jobID, "probe_failed", "certification transition failed")
		return
	}

	_ = h.jobs.MarkTerminal(context.Background(), jobID, storage.JobCompleted, h.now(),
		certificationResultRef(offeringOperationID), nil, storage.DefaultJobRetention)
}
