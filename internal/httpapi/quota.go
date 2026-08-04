package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/application"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// quotaRefreshRoute is the fixed route key idempotencyStore.Execute keys
// replays under (mirrors discoverRoute in discovery.go).
const quotaRefreshRoute = "POST /accounts/{id}/quota"

// quotaSyncTimeout bounds the detached background quota-sync run this
// handler spawns after responding 202 — mirrors discoveryRunTimeout's
// rationale: a generous ceiling for a single account-scoped provider
// call, never the request's own (already-returned) context.
const quotaSyncTimeout = 3 * time.Minute

// Typed, fixed, secret-free job-error codes for every failure path
// runQuotaRefresh can reach. The provider adapter's own error text is
// NEVER used as either a code or a message — only these constants are.
const (
	quotaErrCredentialUnavailable = "credential_unavailable"
	quotaErrFetchFailed           = "quota_fetch_failed"
	quotaErrMappingFailed         = "quota_mapping_failed"
	quotaErrSyncFailed            = "quota_sync_failed"
)

// QuotaHandler serves the P3b-CAPI-001 quota-refresh trigger:
// POST /accounts/{id}/quota (async, 202 + job, mirroring
// POST /accounts/{id}/discover exactly). Owner-session + CSRF gated via
// ControlMux's `gated`.
type QuotaHandler struct {
	accounts       *storage.AccountRepo
	credentials    *storage.AccountCredentialRepo
	jobs           *storage.JobRepo
	reconciliation *storage.ReconciliationRepo
	reg            *providers.Registry
	credService    *application.CredentialService
	audit          *auditEmitter
	idem           *idempotencyStore
	newID          func() string
	now            func() time.Time
}

// NewQuotaHandler builds the handler over every repo/service it needs.
// idem is the SAME shared idempotencyStore ControlMux already constructs
// for enrollment/discovery (never a second instance). now/newID default
// to time.Now / newOAuthTransactionID when nil, exactly like every other
// injectable clock/id-minter in this package.
func NewQuotaHandler(
	accounts *storage.AccountRepo,
	credentials *storage.AccountCredentialRepo,
	jobs *storage.JobRepo,
	reconciliation *storage.ReconciliationRepo,
	reg *providers.Registry,
	credService *application.CredentialService,
	audit *auditEmitter,
	idem *idempotencyStore,
	newID func() string,
	now func() time.Time,
) *QuotaHandler {
	if newID == nil {
		newID = newOAuthTransactionID
	}
	if now == nil {
		now = time.Now
	}
	return &QuotaHandler{
		accounts:       accounts,
		credentials:    credentials,
		jobs:           jobs,
		reconciliation: reconciliation,
		reg:            reg,
		credService:    credService,
		audit:          audit,
		idem:           idem,
		newID:          newID,
		now:            now,
	}
}

// quotaRefreshResponseJSON is POST .../quota's 202 success payload (09
// §2/§3.12): a job id and the ONE canonical shared status route — never
// any inline quota snapshot.
type quotaRefreshResponseJSON struct {
	JobID     string `json:"job_id"`
	StatusURL string `json:"status_url"`
}

// ServeQuotaRefresh implements POST /api/control/v1/accounts/{id}/quota
// (09 §2 "Refresh quota snapshot"): validates the account/provider/
// credential preconditions, creates a tracked "quota_sync" job, responds
// 202 {job_id, status_url}, and THEN runs the actual refresh on a
// detached background context. The method check happens outside
// idem.Execute so a wrong-method request is never captured as a
// replayable response (mirrors ServeDiscover).
func (h *QuotaHandler) ServeQuotaRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAuthError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false)
		return
	}
	h.idem.Execute(w, r, quotaRefreshRoute, h.serveQuotaRefresh)
}

// serveQuotaRefresh is ServeQuotaRefresh's actual body, run at most once
// per (route, Idempotency-Key) pair by idem.Execute.
func (h *QuotaHandler) serveQuotaRefresh(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")

	// 1. Load the account; unknown -> 404 + failure audit. Nothing created.
	account, ok, err := h.accounts.GetByID(ctx, id)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}
	if !ok {
		h.audit.Emit(ctx, AuditActionAccountQuotaRefresh, AuditResultFailure, AuditResourceAccount, id, "not_found")
		writeAuthError(w, http.StatusNotFound, "not_found", "account not found", false)
		return
	}

	// 2. The provider must have a registered quota adapter; nothing
	// created otherwise (mirrors ServeDiscover's discovery_unsupported
	// precedent, discovery.go:152-155).
	adapter, ok := h.reg.QuotaAdapter(providers.ProviderID(account.ProviderID))
	if !ok {
		h.audit.Emit(ctx, AuditActionAccountQuotaRefresh, AuditResultFailure, AuditResourceAccount, id, "quota_unsupported")
		writeErrorDetails(w, http.StatusConflict, "quota_unsupported", "this provider has no quota capability", false, nil)
		return
	}

	// 3. The account must have an active credential to lease; nothing
	// created otherwise.
	credentialID, ok := activeCredentialIDFor(ctx, h.credentials, account.ID)
	if !ok {
		h.audit.Emit(ctx, AuditActionAccountQuotaRefresh, AuditResultFailure, AuditResourceAccount, id, "credential_unavailable")
		writeErrorDetails(w, http.StatusConflict, "credential_unavailable", "account has no active credential", false, nil)
		return
	}

	// 4. Mint a job id and create the tracked job row.
	jobID := h.newID()
	now := h.now()
	if err := h.jobs.Create(ctx, jobID, string(storage.JobKindQuotaSync), now); err != nil {
		h.audit.Emit(ctx, AuditActionAccountQuotaRefresh, AuditResultFailure, AuditResourceAccount, id, "internal")
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}

	// 5. Respond 202 with the canonical shared job surface, and audit
	// success — BEFORE running the actual refresh.
	h.audit.Emit(ctx, AuditActionAccountQuotaRefresh, AuditResultSuccess, AuditResourceAccount, id, "")
	writeData(w, http.StatusAccepted, quotaRefreshResponseJSON{
		JobID:     jobID,
		StatusURL: "/api/control/v1/jobs/" + jobID,
	})

	// 6. Run the refresh on a DETACHED context: the request's own context
	// is cancelled the instant this handler returns (the response has
	// already been written), so using r.Context() directly would abort
	// the run. context.WithoutCancel strips the parent's cancellation/
	// deadline while keeping its values; a bounded timeout is layered on
	// top so a stuck provider call cannot run forever.
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), quotaSyncTimeout)
	go func() {
		defer cancel()
		h.runQuotaRefresh(runCtx, jobID, account.ID, credentialID, adapter)
	}()
}

// runQuotaRefresh executes the actual quota fetch + sync and terminates
// jobID accordingly. It is panic-safe: a recovered panic marks the job
// failed with a generic internal code rather than crashing the process.
// FetchQuota runs INSIDE CredentialService.Use's callback (the plaintext
// credential never escapes that scope) and, critically, OUTSIDE any
// write transaction (05 §2 Step 4) — SyncQuotaWindows is called
// afterward, over the already-fetched result, never from inside the
// lease callback's own transaction (there is none) and never while a
// transaction from a prior step is still open.
func (h *QuotaHandler) runQuotaRefresh(ctx context.Context, jobID, accountID, credentialID string, adapter providers.QuotaAdapter) {
	defer func() {
		if rec := recover(); rec != nil {
			_ = h.jobs.MarkTerminal(context.Background(), jobID, storage.JobFailed, h.now(), "",
				&storage.JobError{Code: "internal", Message: "quota refresh failed unexpectedly"},
				storage.DefaultJobRetention)
		}
	}()

	startedAt := h.now()
	if err := h.jobs.MarkRunning(ctx, jobID, startedAt); err != nil {
		_ = h.jobs.MarkTerminal(context.Background(), jobID, storage.JobFailed, h.now(), "",
			&storage.JobError{Code: "internal", Message: "quota refresh failed to start"},
			storage.DefaultJobRetention)
		return
	}

	var (
		result   providers.QuotaResult
		fetchErr error
	)
	leaseErr := h.credService.Use(ctx, credentialID, func(plaintext []byte) error {
		creds := providers.StoredCredentials{Value: string(plaintext)}
		result, fetchErr = adapter.FetchQuota(ctx, creds)
		return nil
	})

	if leaseErr != nil {
		_ = h.jobs.MarkTerminal(context.Background(), jobID, storage.JobFailed, h.now(), "",
			&storage.JobError{Code: quotaErrCredentialUnavailable, Message: "quota refresh failed"},
			storage.DefaultJobRetention)
		return
	}
	// fetchErr is a provider adapter error: its own text is discarded
	// below, never propagated into the job's typed error — only the
	// fixed constant is ever used as the message.
	if fetchErr != nil {
		_ = h.jobs.MarkTerminal(context.Background(), jobID, storage.JobFailed, h.now(), "",
			&storage.JobError{Code: quotaErrFetchFailed, Message: "quota refresh failed"},
			storage.DefaultJobRetention)
		return
	}

	specs, mapErr := quota.WindowsFromProviderResult(result, h.now())
	if mapErr != nil {
		_ = h.jobs.MarkTerminal(context.Background(), jobID, storage.JobFailed, h.now(), "",
			&storage.JobError{Code: quotaErrMappingFailed, Message: "quota refresh failed"},
			storage.DefaultJobRetention)
		return
	}

	// No cooldown trigger from this endpoint yet (nothing here classifies
	// a 429 — that mapping is P4-ROUTE-014).
	if err := h.reconciliation.SyncQuotaWindows(ctx, accountID, specs, nil); err != nil {
		_ = h.jobs.MarkTerminal(context.Background(), jobID, storage.JobFailed, h.now(), "",
			&storage.JobError{Code: quotaErrSyncFailed, Message: "quota refresh failed"},
			storage.DefaultJobRetention)
		return
	}

	// resultRef is the account id — ids only, per 09 §3.12, never a
	// provider payload, window count, or content of any kind.
	_ = h.jobs.MarkTerminal(context.Background(), jobID, storage.JobCompleted, h.now(), accountID, nil, storage.DefaultJobRetention)
}

// TriggerBackgroundQuota fires a best-effort quota refresh for accountID after
// OAuth connect (legacy sync-on-connect). Setup failures are swallowed so the
// connect path is never disturbed — mirrors DiscoveryHandler.TriggerBackgroundDiscovery.
func (h *QuotaHandler) TriggerBackgroundQuota(ctx context.Context, accountID string) {
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), quotaSyncTimeout)
	go func() {
		defer cancel()

		account, ok, err := h.accounts.GetByID(runCtx, accountID)
		if err != nil || !ok {
			return
		}
		adapter, ok := h.reg.QuotaAdapter(providers.ProviderID(account.ProviderID))
		if !ok {
			return
		}
		credentialID, ok := activeCredentialIDFor(runCtx, h.credentials, account.ID)
		if !ok {
			return
		}
		jobID := h.newID()
		if err := h.jobs.Create(runCtx, jobID, string(storage.JobKindQuotaSync), h.now()); err != nil {
			return
		}
		h.runQuotaRefresh(runCtx, jobID, account.ID, credentialID, adapter)
	}()
}
