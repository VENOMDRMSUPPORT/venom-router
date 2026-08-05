package httpapi

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/intelligence"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// discoverRoute is the fixed route key idempotencyStore.Execute keys
// replays under (mirrors enrollmentRoute in enrollment.go).
const discoverRoute = "POST /accounts/{id}/discover"

// discoveryRunTimeout bounds the detached background discovery run this
// handler spawns after responding 202 — a generous ceiling for a single
// account-scoped provider call, never the request's own (already-returned)
// context.
const discoveryRunTimeout = 3 * time.Minute

// usabilityFastLaneTimeout bounds the detached fast-lane usability
// verification fired after a discovery run succeeds (design 2026-08-05) —
// generous enough for one account's full model catalog to be probed (it
// mirrors the scheduled sweep's own per-lane usabilitySweepBudget). It gets
// its OWN context.WithoutCancel + timeout rather than reusing runDiscovery's
// ctx: that ctx's cancel fires the instant runDiscovery returns to its
// caller, which would otherwise abort the fast lane before it ever runs.
const usabilityFastLaneTimeout = 30 * time.Second

// discoveryResultRef is the ONE reference shape a discovery job's
// result_ref ever takes (09 §3.12: "a reference... e.g. the affected
// account_id + the read-model route"): the /models route filtered to the
// account whose discovery just ran. It NEVER contains a model id, count,
// credential, or provider error — those never leave this function's scope.
func discoveryResultRef(accountID string) string {
	return "/api/control/v1/models?account_id=" + url.QueryEscape(accountID)
}

// DiscoveryHandler serves the P3a-CAPI-002 discovery-trigger and
// certification-read surface: POST /accounts/{id}/discover (async, 202 +
// job) and GET /offerings/{id}/certification. Owner-session + CSRF gated
// via ControlMux's `gated`.
type DiscoveryHandler struct {
	accounts    *storage.AccountRepo
	credentials *storage.AccountCredentialRepo
	catalog     *storage.CatalogRepo
	jobs        *storage.JobRepo
	discovery   *storage.DiscoveryRepo
	reg         *providers.Registry
	leaser      intelligence.CredentialLeaser
	audit       *auditEmitter
	idem        *idempotencyStore
	newID       func() string
	now         func() time.Time

	// probeRuns is P3c-CAPI-001's ADDITIVE dependency for the
	// certification read's probe-execution dimension — nil unless
	// WithProbeRuns is called. Adding it as a constructor parameter would
	// break every existing NewDiscoveryHandler call site across this
	// package's test files (none of which is in this batch's touchable
	// list), so it is wired via the WithProbeRuns method below instead;
	// every pre-existing caller/test is completely unaffected.
	probeRuns *storage.ProbeRunRepo

	// usabilityTrigger is the fast-lane hook (design 2026-08-05): fired ONLY
	// after a discovery run completes successfully, so the account's freshly
	// observed models get verified immediately instead of waiting for the
	// next scheduled sweep. UsabilityService.VerifyAccount satisfies this
	// signature directly. nil (every pre-existing construction site) disables
	// the fast lane — a discovery run behaves exactly as it did before this
	// field existed.
	usabilityTrigger func(ctx context.Context, providerID, accountID string)
}

// SetUsabilityTrigger wires the fast-lane usability-verification hook fired
// after a successful discovery run. Optional (nil disables it, the default);
// ControlMux sets it once the usability composition root exists, mirroring
// SetDiscoveryTrigger's own injection pattern.
func (h *DiscoveryHandler) SetUsabilityTrigger(fn func(ctx context.Context, providerID, accountID string)) {
	h.usabilityTrigger = fn
}

// NewDiscoveryHandler builds the handler over every repo/service it needs.
// idem is the SAME shared idempotencyStore ControlMux already constructs
// for enrollment (never a second instance). now/newID default to time.Now /
// newOAuthTransactionID when nil, exactly like every other injectable
// clock/id-minter in this package.
func NewDiscoveryHandler(
	accounts *storage.AccountRepo,
	credentials *storage.AccountCredentialRepo,
	catalog *storage.CatalogRepo,
	jobs *storage.JobRepo,
	discovery *storage.DiscoveryRepo,
	reg *providers.Registry,
	leaser intelligence.CredentialLeaser,
	audit *auditEmitter,
	idem *idempotencyStore,
	newID func() string,
	now func() time.Time,
) *DiscoveryHandler {
	if newID == nil {
		newID = newOAuthTransactionID
	}
	if now == nil {
		now = time.Now
	}
	return &DiscoveryHandler{
		accounts:    accounts,
		credentials: credentials,
		catalog:     catalog,
		jobs:        jobs,
		discovery:   discovery,
		reg:         reg,
		leaser:      leaser,
		audit:       audit,
		idem:        idem,
		newID:       newID,
		now:         now,
	}
}

// WithProbeRuns returns a shallow copy of h with probeRuns wired in, so
// ServeCertification can additionally report the probe-execution
// dimension (P3c-CAPI-001) — see the field's own doc comment for why
// this is a copy-returning method rather than a constructor parameter.
func (h *DiscoveryHandler) WithProbeRuns(probeRuns *storage.ProbeRunRepo) *DiscoveryHandler {
	clone := *h
	clone.probeRuns = probeRuns
	return &clone
}

// activeCredentialIDFor returns the id of accountID's one active
// credential, or ok=false if it has none — mirrors
// AccountsHandler.activeCredentialID (accounts.go), duplicated here rather
// than exported from that unrelated handler, since both are small,
// independent lookups over the same repo.
func activeCredentialIDFor(ctx context.Context, credentials *storage.AccountCredentialRepo, accountID string) (string, bool) {
	creds, err := credentials.ListForAccount(ctx, accountID)
	if err != nil {
		return "", false
	}
	for _, c := range creds {
		if c.State == domain.CredentialActive {
			return c.ID, true
		}
	}
	return "", false
}

// discoverResponseJSON is POST .../discover's 202 success payload (09
// §3.7/§3.12): a job id and the ONE canonical shared status route — never
// a per-resource status endpoint, and never any inline discovery result.
type discoverResponseJSON struct {
	JobID     string `json:"job_id"`
	StatusURL string `json:"status_url"`
}

// ServeDiscover implements POST /api/control/v1/accounts/{id}/discover (09
// §3.7): validates the account/provider/credential preconditions, creates a
// tracked "discovery" job, responds 202 {job_id, status_url}, and THEN runs
// the actual discovery on a detached background context. The method check
// happens outside idem.Execute so a wrong-method request is never captured
// as a replayable response (mirrors enrollment.go's ServeConnect).
func (h *DiscoveryHandler) ServeDiscover(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAuthError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false)
		return
	}
	h.idem.Execute(w, r, discoverRoute, h.serveDiscover)
}

// serveDiscover is ServeDiscover's actual body, run at most once per
// (route, Idempotency-Key) pair by idem.Execute.
func (h *DiscoveryHandler) serveDiscover(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")

	// 1. Load the account; unknown -> 404 + failure audit. Nothing created.
	account, ok, err := h.accounts.GetByID(ctx, id)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}
	if !ok {
		h.audit.Emit(ctx, AuditActionAccountDiscover, AuditResultFailure, AuditResourceAccount, id, "not_found")
		writeAuthError(w, http.StatusNotFound, "not_found", "account not found", false)
		return
	}

	// 2. The provider must have a registered discovery adapter; nothing
	// created otherwise.
	adapter, ok := h.reg.ModelDiscoveryAdapter(providers.ProviderID(account.ProviderID))
	if !ok {
		h.audit.Emit(ctx, AuditActionAccountDiscover, AuditResultFailure, AuditResourceAccount, id, "discovery_unsupported")
		writeErrorDetails(w, http.StatusConflict, "discovery_unsupported", "this provider has no discovery capability", false, nil)
		return
	}

	// 3. The account must have an active credential to lease; nothing
	// created otherwise.
	credentialID, ok := activeCredentialIDFor(ctx, h.credentials, account.ID)
	if !ok {
		h.audit.Emit(ctx, AuditActionAccountDiscover, AuditResultFailure, AuditResourceAccount, id, "credential_unavailable")
		writeErrorDetails(w, http.StatusConflict, "credential_unavailable", "account has no active credential", false, nil)
		return
	}

	// 4. Mint job/run ids and create the tracked job row.
	jobID := h.newID()
	runID := h.newID()
	now := h.now()
	if err := h.jobs.Create(ctx, jobID, string(storage.JobKindDiscovery), now); err != nil {
		h.audit.Emit(ctx, AuditActionAccountDiscover, AuditResultFailure, AuditResourceAccount, id, "internal")
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}

	// 5. Respond 202 with the canonical shared job surface, and audit
	// success — BEFORE running the actual discovery.
	h.audit.Emit(ctx, AuditActionAccountDiscover, AuditResultSuccess, AuditResourceAccount, id, "")
	writeData(w, http.StatusAccepted, discoverResponseJSON{
		JobID:     jobID,
		StatusURL: "/api/control/v1/jobs/" + jobID,
	})

	// 6. Run discovery on a DETACHED context: the request's own context is
	// cancelled the instant this handler returns (the response has already
	// been written), so using r.Context() directly would abort every run.
	// context.WithoutCancel strips the parent's cancellation/deadline while
	// keeping its values; a bounded timeout is layered on top so a stuck
	// provider call cannot run forever.
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), discoveryRunTimeout)
	go func() {
		defer cancel()
		h.runDiscovery(runCtx, jobID, runID, account.ID, account.ProviderID, credentialID, adapter)
	}()
}

// TriggerBackgroundDiscovery fires a best-effort model discovery for accountID
// — the auto-discovery-on-connect path (design 2026-08-03). Unlike
// ServeDiscover it writes no HTTP response and SWALLOWS every setup failure (a
// missing discovery adapter, no active credential, or a job-row insert error
// simply means no discovery runs): it must never disturb the connect that
// called it. All of it — the resolution AND the run — happens on a detached,
// timeout-bounded context in its own goroutine, so the connect response returns
// immediately. The account was just created by that same connect, so GetByID
// finds it. Reuses the exact runDiscovery the manual path is tested against.
func (h *DiscoveryHandler) TriggerBackgroundDiscovery(ctx context.Context, accountID string) {
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), discoveryRunTimeout)
	go func() {
		defer cancel()

		account, ok, err := h.accounts.GetByID(runCtx, accountID)
		if err != nil || !ok {
			return
		}
		adapter, ok := h.reg.ModelDiscoveryAdapter(providers.ProviderID(account.ProviderID))
		if !ok {
			return
		}
		credentialID, ok := activeCredentialIDFor(runCtx, h.credentials, account.ID)
		if !ok {
			return
		}
		jobID := h.newID()
		runID := h.newID()
		if err := h.jobs.Create(runCtx, jobID, string(storage.JobKindDiscovery), h.now()); err != nil {
			return
		}
		h.runDiscovery(runCtx, jobID, runID, account.ID, account.ProviderID, credentialID, adapter)
	}()
}

// runDiscovery executes the actual discovery run and terminates jobID
// accordingly. It is panic-safe: a recovered panic marks the job failed
// with a generic internal code rather than crashing the process.
func (h *DiscoveryHandler) runDiscovery(ctx context.Context, jobID, runID, accountID, providerID, credentialID string, adapter providers.ModelDiscoveryAdapter) {
	defer func() {
		if rec := recover(); rec != nil {
			_ = h.jobs.MarkTerminal(context.Background(), jobID, storage.JobFailed, h.now(), "",
				&storage.JobError{Code: "internal", Message: "discovery run failed unexpectedly"},
				storage.DefaultJobRetention)
		}
	}()

	startedAt := h.now()
	if err := h.jobs.MarkRunning(ctx, jobID, startedAt); err != nil {
		_ = h.jobs.MarkTerminal(context.Background(), jobID, storage.JobFailed, h.now(), "",
			&storage.JobError{Code: "internal", Message: "discovery run failed to start"},
			storage.DefaultJobRetention)
		return
	}

	svc := intelligence.NewDiscoveryService(adapter, h.leaser, h.discovery, h.discovery, h.now)
	result, err := svc.Run(ctx, intelligence.RunParams{
		AccountID:    accountID,
		ProviderID:   providerID,
		CredentialID: credentialID,
		RunID:        runID,
	})
	finishedAt := h.now()

	if err != nil {
		_ = h.jobs.MarkTerminal(context.Background(), jobID, storage.JobFailed, finishedAt, "",
			&storage.JobError{Code: "internal", Message: "discovery run failed"},
			storage.DefaultJobRetention)
		return
	}

	if result.Outcome == intelligence.OutcomeFailed {
		reasonCode := result.ReasonCode
		if reasonCode == "" {
			reasonCode = "internal"
		}
		// result.ReasonCode is already one of intelligence's typed, safe
		// reason codes (never a raw provider error, credential, or model
		// content) — it is used verbatim as the job's typed error code.
		_ = h.jobs.MarkTerminal(context.Background(), jobID, storage.JobFailed, finishedAt, "",
			&storage.JobError{Code: reasonCode, Message: "discovery run failed"},
			storage.DefaultJobRetention)
		return
	}

	_ = h.jobs.MarkTerminal(context.Background(), jobID, storage.JobCompleted, finishedAt,
		discoveryResultRef(accountID), nil, storage.DefaultJobRetention)

	// Fast lane (design 2026-08-05): a successful discovery run's freshly
	// observed models get verified right away rather than waiting for the
	// next scheduled sweep. THIS is runDiscovery's one success point — both
	// the manual (ServeDiscover) and background (TriggerBackgroundDiscovery)
	// paths funnel through here, so both get the fast lane. Detached exactly
	// like the discovery run itself: ctx is cancelled by the caller's
	// deferred cancel() the instant this function returns, so the fast lane
	// needs its own context.WithoutCancel + timeout, not ctx directly.
	if h.usabilityTrigger != nil {
		triggerCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), usabilityFastLaneTimeout)
		go func() {
			defer cancel()
			h.usabilityTrigger(triggerCtx, providerID, accountID)
		}()
	}
}

// --- GET /offerings/{id}/certification ---

// certificationJSON is GET /offerings/{id}/certification's success payload
// (09 §2 / 04 §5). {id} is an offering_operations.id — certification is
// per offering-operation, and the frozen M4 certifications table's own
// primary key IS offering_operation_id.
// CertifiedAndSupported is models.Routable(state, truth) — the
// CERTIFICATION-LAYER predicate ONLY (04 §5: certified AND supported). It is
// deliberately NOT named "routable": end-to-end routability additionally
// requires the capability to be EFFECTIVE (native support AND provider
// exposure AND transport support, 04 §3), which this per-operation
// certification read has no access to. `GET /offerings` is the single
// surface that answers routability, on its capability objects' `routable`
// field; a consumer that treats this field as admission would route to an
// offering whose capability was never proven effective.
type certificationJSON struct {
	OfferingOperationID   string  `json:"offering_operation_id"`
	AccountID             string  `json:"account_id"`
	ProviderModelID       string  `json:"provider_model_id"`
	Operation             string  `json:"operation"`
	State                 string  `json:"state"`
	CapabilityTruth       string  `json:"capability_truth"`
	Version               int     `json:"version"`
	CertifiedAt           *string `json:"certified_at"`
	EvidenceRef           string  `json:"evidence_ref,omitempty"`
	CertifiedAndSupported bool    `json:"certified_and_supported"`
	// ProbeExecution (P3c-CAPI-001, 04 §2's "probe execution" layer,
	// deliberately reported as a dimension SEPARATE from State/
	// CapabilityTruth above) is nil when unknown — no probe has ever run
	// for this offering-operation, or this handler was built without
	// WithProbeRuns (every pre-existing test/call site).
	ProbeExecution *string `json:"probe_execution,omitempty"`
	// ReviewReasons (P3c-CAPI-001 GOVERNOR DECISION) reports ONLY the
	// reasons this read surface actually computes from the inputs it
	// owns (certification state + capability truth) — today that is
	// capability_not_certified, and NOTHING else. Funding, health, quota,
	// and cooldown reasons need inputs this endpoint has no access to;
	// fabricating them here would be dishonest. This array grows once a
	// later phase (P4) supplies those inputs to this read. Always
	// present (never omitted, never null) — an empty array for a
	// routable row, never a missing key.
	ReviewReasons []string `json:"review_reasons"`
}

// ServeCertification implements GET /api/control/v1/offerings/{id}/certification
// (09 §2 / 04 §5): certification state + capability truth for one
// offering-operation. Unknown id -> 404. This is a read — no audit event.
func (h *DiscoveryHandler) ServeCertification(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAuthError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false)
		return
	}

	id := r.PathValue("id")
	op, ok, err := h.catalog.GetOperationCertification(r.Context(), id)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}
	if !ok {
		writeAuthError(w, http.StatusNotFound, "not_found", "offering operation not found", false)
		return
	}

	state, err := models.ParseCertificationState(op.CertificationStatus)
	if err != nil {
		state = models.CertDiscovered
	}
	truth, err := models.ParseCapabilityTruth(op.CapabilityTruth)
	if err != nil {
		truth = models.TruthUnknown
	}

	resp := certificationJSON{
		OfferingOperationID:   op.ID,
		AccountID:             op.AccountID,
		ProviderModelID:       op.ProviderModelID,
		Operation:             op.Operation,
		State:                 string(state),
		CapabilityTruth:       string(truth),
		Version:               op.CertificationVersion,
		EvidenceRef:           op.EvidenceRef,
		CertifiedAndSupported: models.Routable(state, truth),
		ReviewReasons:         certificationReviewReasons(state, truth),
	}
	if op.CertifiedAt != nil {
		s := op.CertifiedAt.Format(time.RFC3339)
		resp.CertifiedAt = &s
	}
	if h.probeRuns != nil {
		if execution, ok, err := h.probeRuns.LatestExecution(r.Context(), op.ID); err == nil && ok {
			e := string(execution)
			resp.ProbeExecution = &e
		}
	}
	writeData(w, http.StatusOK, resp)
}

// certificationReviewReasons computes GET .../certification's
// review_reasons array (P3c-CAPI-001 GOVERNOR DECISION): every
// non-certification admission gate is fed as passing, so
// intelligence.Admit's conjunction can only ever surface the ONE reason
// this endpoint actually owns the inputs for —
// intelligence.AdmissionCapabilityNotCertified — never funding_unknown,
// no_healthy_account, quota_exhausted, quota_insufficient, or
// cooling_down, none of which this read has any basis to assert. Always
// returns a non-nil slice (possibly empty), so the JSON field is never
// null.
func certificationReviewReasons(state models.CertificationState, truth models.CapabilityTruth) []string {
	verdict := intelligence.Admit(intelligence.AdmissionInput{
		State:            state,
		Truth:            truth,
		IdentityResolved: true,
		ContextVerified:  true,
		FundingKnown:     true,
		HealthyAccount:   true,
	})
	out := make([]string, 0, len(verdict.Reasons))
	for _, reason := range verdict.Reasons {
		out = append(out, string(reason))
	}
	return out
}
