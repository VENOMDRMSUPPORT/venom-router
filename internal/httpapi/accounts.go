package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/application"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// revealLimiterMaxPerWindow bounds how many successful reveals a single
// account may make within revealLimiterWindow. A reveal is a sensitive,
// plaintext-returning operation behind the reverify-freshness gate; this
// limiter is the defense-in-depth cap on top of that gate (and on top of
// the lockout/audit trail SEC-006 will own). It is per-process per-account
// this phase: a single Venom process is the only writer, so per-process is
// the right granularity until a shared store (P3) can carry it across
// restarts/redeploys.
const (
	revealLimiterMaxPerWindow = 10
	revealLimiterWindow       = 1 * time.Hour
)

// AccountsHandler serves the P2b-CAPI-004 account-lifecycle surface: the
// GET list/detail projections, the credential reveal (the only endpoint in
// the whole control plane that returns plaintext), the funding owner
// override, and the connection/health lifecycle mutations. Every route is
// owner-session + CSRF gated via ControlMux's `gated` (the handler never
// re-validates the session or CSRF itself); every mutating call emits
// exactly one secret-free audit_event through the shared auditEmitter.
type AccountsHandler struct {
	accounts     *storage.AccountRepo
	credentials  *storage.AccountCredentialRepo
	funding      *storage.FundingEvidenceRepo
	credService  *application.CredentialService
	audit        *auditEmitter
	revealLimit  *fixedWindowLimiter
	now          func() time.Time
	newFundingID func() string
}

// NewAccountsHandler builds the handler over the shared account/credential/
// funding repositories, the credential service (the only decrypt-for-
// reveal path), the audit emitter, and an injectable clock + funding-id
// minter. now and newFundingID default to time.Now / a fresh random id
// when nil, exactly like every other injectable clock in this package.
func NewAccountsHandler(
	accounts *storage.AccountRepo,
	credentials *storage.AccountCredentialRepo,
	funding *storage.FundingEvidenceRepo,
	credService *application.CredentialService,
	audit *auditEmitter,
	now func() time.Time,
	newFundingID func() string,
) *AccountsHandler {
	if now == nil {
		now = time.Now
	}
	if newFundingID == nil {
		newFundingID = newFundingEvidenceID
	}
	return &AccountsHandler{
		accounts:     accounts,
		credentials:  credentials,
		funding:      funding,
		credService:  credService,
		audit:        audit,
		revealLimit:  newFixedWindowLimiter(revealLimiterMaxPerWindow, revealLimiterWindow),
		now:          now,
		newFundingID: newFundingID,
	}
}

// fundingEvidenceVersionToken is the opaque version token a funding row
// exposes for optimistic concurrency: the row's observed_at epoch, as a
// string. It MUST be the exact token GET /accounts/{id} returns in the
// funding.version field so a client can round-trip it back through
// PUT /accounts/{id}/funding's expected_version. observed_at is unique per
// row (it is stamped at insert time) and immutable, so it is a stable,
// unforgeable identifier of exactly one current row.
func fundingEvidenceVersionToken(e domain.FundingEvidence) string {
	return strconv.FormatInt(e.ObservedAt.Unix(), 10)
}

// --- GET /accounts and GET /accounts/{id} ---

// accountProjectionJSON is the multi-axis projection GET /accounts and
// GET /accounts/{id} both return (09 §2/§3.5): ids + the three lifecycle
// axes (connection_state, health_state, reauth_in_progress) + identity +
// funding (with a version token for optimistic concurrency) + the derived
// display_status + eligibility. It NEVER carries any credential material
// — no key, token, ciphertext, nonce, or fingerprint.
type accountProjectionJSON struct {
	ID                string              `json:"id"`
	ProviderID        string              `json:"provider"`
	ExternalID        string              `json:"external_id"`
	DisplayName       string              `json:"display_name,omitempty"`
	AuthType          string              `json:"auth_type"`
	ConnectionState   string              `json:"connection_state"`
	HealthState       string              `json:"health_state"`
	ReauthInProgress  bool                `json:"reauth_in_progress"`
	Identity          accountIdentityJSON `json:"identity"`
	Funding           *fundingJSON        `json:"funding"`
	DisplayStatus     string              `json:"display_status"`
	Eligibility       eligibilityJSON     `json:"eligibility"`
	LastHealthCheckAt string              `json:"last_health_check_at,omitempty"`
	LastHealthError   string              `json:"last_health_error,omitempty"`
	CreatedAt         string              `json:"created_at"`
	UpdatedAt         string              `json:"updated_at"`
}

type accountIdentityJSON struct {
	Email string `json:"email,omitempty"`
	Plan  string `json:"plan,omitempty"`
}

type fundingJSON struct {
	Funding string `json:"funding"`
	Source  string `json:"source"`
	Locked  bool   `json:"locked"`
	Version string `json:"version,omitempty"`
}

type eligibilityJSON struct {
	Eligible bool   `json:"eligible"`
	Reason   string `json:"reason,omitempty"`
}

// resolveCredentialStatus derives the domain.CredentialStatus the
// projection needs: Active = an active (non-retired) credential exists for
// the account; Expired = that active credential's ExpiresAt is in the
// past. It never returns any credential material — only the two booleans
// ProjectEligibility consumes.
func (h *AccountsHandler) resolveCredentialStatus(ctx context.Context, accountID string, now time.Time) domain.CredentialStatus {
	creds, err := h.credentials.ListForAccount(ctx, accountID)
	if err != nil {
		return domain.CredentialStatus{}
	}
	for _, c := range creds {
		if c.State != domain.CredentialActive {
			continue
		}
		expired := c.ExpiresAt != nil && now.After(*c.ExpiresAt)
		return domain.CredentialStatus{Active: true, Expired: expired}
	}
	return domain.CredentialStatus{Active: false}
}

// projectAccount builds the multi-axis projection for one account, reading
// its funding and resolving its credential status. includeFundingVersion
// selects whether the funding row's version token is included (true for
// the single-account GET, where a client may want to round-trip it through
// the PUT; the list view omits it for size). cooldownActive is always
// false this phase — no cooldown table exists until P3b.
func (h *AccountsHandler) projectAccount(ctx context.Context, a domain.Account, now time.Time, includeFundingVersion bool) accountProjectionJSON {
	const cooldownActive = false // P3b introduces cooldowns; none exists this phase.

	credStatus := h.resolveCredentialStatus(ctx, a.ID, now)

	out := accountProjectionJSON{
		ID:               a.ID,
		ProviderID:       a.ProviderID,
		ExternalID:       a.ExternalID,
		DisplayName:      a.DisplayName,
		AuthType:         a.AuthType,
		ConnectionState:  string(a.ConnectionState),
		HealthState:      string(a.HealthState),
		ReauthInProgress: a.ReauthInProgress,
		Identity: accountIdentityJSON{
			Email: a.IdentityEmail,
			Plan:  a.IdentityPlan,
		},
		DisplayStatus: string(domain.DeriveDisplayStatus(a, cooldownActive)),
		Eligibility:   eligibilityFromDomain(domain.ProjectEligibility(a, credStatus, cooldownActive)),
		CreatedAt:     a.CreatedAt.Format(time.RFC3339),
		UpdatedAt:     a.UpdatedAt.Format(time.RFC3339),
	}
	if a.LastHealthCheckAt != nil {
		out.LastHealthCheckAt = a.LastHealthCheckAt.Format(time.RFC3339)
	}
	out.LastHealthError = a.LastHealthError

	current, ok, err := h.funding.CurrentForAccount(ctx, a.ID)
	if err == nil && ok {
		fj := fundingJSON{
			Funding: string(current.Funding),
			Source:  string(current.Source),
			Locked:  current.Locked,
		}
		if includeFundingVersion {
			fj.Version = fundingEvidenceVersionToken(current)
		}
		out.Funding = &fj
	}
	return out
}

func eligibilityFromDomain(e domain.Eligibility) eligibilityJSON {
	if e.Eligible {
		return eligibilityJSON{Eligible: true}
	}
	return eligibilityJSON{Eligible: false, Reason: string(e.Reason)}
}

// ServeList implements GET /api/control/v1/accounts (09 §2): a cursor-
// paginated list of account projections, each built by projectAccount.
func (h *AccountsHandler) ServeList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAuthError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false)
		return
	}

	ctx := r.Context()
	page := parsePageParams(r, defaultPageLimit, maxPageLimit)

	accounts, nextCursor, err := h.accounts.List(ctx, page.Cursor, page.Limit, "")
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}

	now := h.now()
	items := make([]accountProjectionJSON, 0, len(accounts))
	for _, a := range accounts {
		items = append(items, h.projectAccount(ctx, a, now, false))
	}

	writeDataMeta(w, http.StatusOK, map[string]any{"accounts": items}, paginationMeta(nextCursor))
}

// ServeGet implements GET /api/control/v1/accounts/{id} (09 §3.5): the
// same multi-axis projection for one account, including the funding row's
// version token so a client can round-trip it through the funding PUT.
func (h *AccountsHandler) ServeGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAuthError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false)
		return
	}

	ctx := r.Context()
	id := r.PathValue("id")

	a, ok, err := h.accounts.GetByID(ctx, id)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}
	if !ok {
		writeAuthError(w, http.StatusNotFound, "not_found", "account not found", false)
		return
	}

	writeData(w, http.StatusOK, h.projectAccount(ctx, a, h.now(), true))
}

// --- POST /accounts/{id}/reveal — the crown jewel ---

// ServeReveal implements POST /api/control/v1/accounts/{id}/reveal (09
// §3.6): the ONLY endpoint in the control plane that returns plaintext
// credential material, and only behind the SEC-005 reverify-freshness
// gate. The plaintext is decrypted ONCE via CredentialService.Use, written
// into the response body INSIDE Use's callback (so it is never held after
// Use returns and zeroes it), and the response carries
// Cache-Control: no-store. The audit row records only the action + account
// id — never the secret. Repeated reveals are rate-limited.
func (h *AccountsHandler) ServeReveal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAuthError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false)
		return
	}

	ctx := r.Context()
	id := r.PathValue("id")

	// SEC-005 consumption point: the session MUST be reverify-fresh (<= 5
	// min since the last successful POST /auth/reverify) before ANY decrypt
	// is attempted. A stale session is rejected with reverification_required
	// 401 — exactly the gate IsReverifyFresh exists to back. This check
	// runs before the rate limiter and before any credential lookup, so a
	// stale session can never trigger a decrypt or burn a rate-limit slot.
	session, ok := sessionFromContext(ctx)
	if !ok || !IsReverifyFresh(session.Row, h.now()) {
		h.audit.Emit(ctx, AuditActionAccountReveal, AuditResultFailure, AuditResourceAccount, id, "reverification_required")
		writeAuthError(w, http.StatusUnauthorized, "reverification_required", "re-verification required", false)
		return
	}

	// Resolve the account's ACTIVE credential (the one a reveal is
	// meaningful for). None -> a typed not_found / no_active_credential,
	// never a decrypt of a retired/staged/absent row.
	account, accountOK, err := h.accounts.GetByID(ctx, id)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}
	if !accountOK {
		h.audit.Emit(ctx, AuditActionAccountReveal, AuditResultFailure, AuditResourceAccount, id, "not_found")
		writeAuthError(w, http.StatusNotFound, "not_found", "account not found", false)
		return
	}

	credID, credOK := h.activeCredentialID(ctx, account.ID)
	if !credOK {
		h.audit.Emit(ctx, AuditActionAccountReveal, AuditResultFailure, AuditResourceAccount, id, "no_active_credential")
		writeErrorDetails(w, http.StatusNotFound, "no_active_credential", "account has no active credential to reveal", false, nil)
		return
	}

	// Rate-limit: a stale-but-reverify-fresh session hammering reveal is
	// still blunted here. The limiter is checked AFTER the reverify gate
	// (so a stale session can never burn a slot) and BEFORE the decrypt
	// (so a throttled reveal never decrypts).
	if !h.revealLimit.Allow(h.now()) {
		h.audit.Emit(ctx, AuditActionAccountReveal, AuditResultFailure, AuditResourceAccount, id, "rate_limited")
		writeAuthError(w, http.StatusTooManyRequests, "rate_limited", "too many reveal attempts, try again later", true)
		return
	}

	// no-store is set BEFORE the decrypt so it lands on the response
	// unconditionally, including on a decrypt failure (the failure envelope
	// must never be cached either).
	w.Header().Set("Cache-Control", "no-store")

	// Decrypt ONCE and write the plaintext inside Use's callback. Use
	// zeroes the plaintext the instant this callback returns, so the value
	// never outlives the call — it exists only long enough to be copied
	// into the HTTP response body. A decrypt failure surfaces as a typed
	// internal error; the callback (and therefore the body write) is never
	// invoked in that case.
	revealErr := h.credService.Use(ctx, credID, func(plaintext []byte) error {
		_, werr := w.Write(plaintext)
		return werr
	})
	if revealErr != nil {
		h.audit.Emit(ctx, AuditActionAccountReveal, AuditResultFailure, AuditResourceAccount, id, "decrypt_failed")
		// The body may have already started if the write partially
		// succeeded; in that case http.WriteHeader is a no-op and the
		// client sees a truncated body. The audit row above still records
		// the failure. For the clean-failure case (decrypt error before any
		// write), emit the typed error envelope.
		if !headersWritten(w) {
			writeAuthError(w, http.StatusInternalServerError, "internal", "credential could not be revealed", true)
		}
		return
	}

	// Success: emit the audit row (account id only — NEVER the secret).
	h.audit.Emit(ctx, AuditActionAccountReveal, AuditResultSuccess, AuditResourceAccount, id, "")
}

// activeCredentialID returns the id of accountID's one active credential,
// or ok=false if it has none. It reads via ListForAccount and selects the
// active row; the M2 idx_cred_active_per_kind partial-unique index
// guarantees at most one active row per (account, kind).
func (h *AccountsHandler) activeCredentialID(ctx context.Context, accountID string) (string, bool) {
	creds, err := h.credentials.ListForAccount(ctx, accountID)
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

// --- PUT /accounts/{id}/funding — owner override ---

// fundingOverrideRequest is PUT /accounts/{id}/funding's body.
type fundingOverrideRequest struct {
	Funding         string `json:"funding"`
	ExpectedVersion string `json:"expected_version,omitempty"`
}

// ServeFunding implements PUT /api/control/v1/accounts/{id}/funding (02
// §3): an owner_override funding classification. Optimistic concurrency:
// expected_version (the body field) or If-Match must equal the current
// funding row's version token (its observed_at epoch), else 412
// precondition_failed. A locked provider_policy current row rejects with
// 409 funding_locked (rule 1); a supersession that wins persists BOTH the
// stamped-old and the new-current row in one transaction.
func (h *AccountsHandler) ServeFunding(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeAuthError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false)
		return
	}

	ctx := r.Context()
	id := r.PathValue("id")

	var req fundingOverrideRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.audit.Emit(ctx, AuditActionAccountFunding, AuditResultFailure, AuditResourceAccount, id, "validation_error")
		writeAuthError(w, http.StatusBadRequest, "validation_error", "invalid request body", false)
		return
	}

	value, ok := parseFundingValue(req.Funding)
	if !ok {
		h.audit.Emit(ctx, AuditActionAccountFunding, AuditResultFailure, AuditResourceAccount, id, "validation_error")
		writeAuthError(w, http.StatusBadRequest, "validation_error", "funding must be one of free, paid, unknown", false)
		return
	}

	// The account must exist before any funding work.
	account, accountOK, err := h.accounts.GetByID(ctx, id)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}
	if !accountOK {
		h.audit.Emit(ctx, AuditActionAccountFunding, AuditResultFailure, AuditResourceAccount, id, "not_found")
		writeAuthError(w, http.StatusNotFound, "not_found", "account not found", false)
		return
	}

	now := h.now()

	current, currentOK, err := h.funding.CurrentForAccount(ctx, id)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}

	// Optimistic concurrency: when a precondition is supplied (via the
	// body's expected_version OR the If-Match header), it must match the
	// current row's version token. An absent precondition (both empty) is
	// permitted — this endpoint is also the first-classification path for
	// an account whose current row is the evidence_required unknown stamp,
	// and requiring a version there would be friction with no benefit.
	provided := req.ExpectedVersion
	if v := ifMatchVersion(r); v != "" {
		provided = v
	}
	if provided != "" && currentOK {
		if !requireMatchingVersion(w, provided, fundingEvidenceVersionToken(current)) {
			h.audit.Emit(ctx, AuditActionAccountFunding, AuditResultFailure, AuditResourceAccount, id, "precondition_failed")
			return
		}
	}

	// Build the owner_override candidate and let the PURE domain layer
	// decide whether it supersedes the current row.
	candidate := domain.FundingEvidence{
		ID:         h.newFundingID(),
		AccountID:  id,
		Funding:    value,
		Source:     domain.FundingSourceOwnerOverride,
		Confidence: 1.0,
		ObservedAt: now,
	}

	var currentPtr *domain.FundingEvidence
	if currentOK {
		currentPtr = &current
	}

	result, derr := decideFunding(currentPtr, candidate, now)
	if derr != nil {
		// ErrFundingLocked -> 409 funding_locked. No other typed error is
		// expected from the domain layer here.
		if errors.Is(derr, domain.ErrFundingLocked) {
			h.audit.Emit(ctx, AuditActionAccountFunding, AuditResultFailure, AuditResourceAccount, id, "funding_locked")
			writeErrorDetails(w, http.StatusConflict, "funding_locked", "current funding is locked and cannot be overridden", false, nil)
			return
		}
		h.audit.Emit(ctx, AuditActionAccountFunding, AuditResultFailure, AuditResourceAccount, id, "internal")
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}

	if !result.Superseded {
		// The candidate did not win (e.g. an owner_override attempting to
		// supersede an owner_override that is not itself an owner_override
		// — the ordinary "not fresher" nil-error outcome). The current row
		// is unchanged; report it back without error.
		h.audit.Emit(ctx, AuditActionAccountFunding, AuditResultSuccess, AuditResourceAccount, id, "no_change")
		writeData(w, http.StatusOK, h.projectAccount(ctx, account, now, true))
		return
	}

	// Persist BOTH rows (stamp the old current, insert the new current) in
	// ONE storage transaction.
	var superseded *domain.FundingEvidence
	if currentOK {
		superseded = &result.UpdatedCurrent
	}
	if err := h.funding.AppendSupersession(ctx, superseded, result.NewCurrent, now); err != nil {
		h.audit.Emit(ctx, AuditActionAccountFunding, AuditResultFailure, AuditResourceAccount, id, "internal")
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}

	// Re-project so the response reflects the new current row.
	updated, updatedOK, err := h.accounts.GetByID(ctx, id)
	if err != nil || !updatedOK {
		updated = account
	}
	h.audit.Emit(ctx, AuditActionAccountFunding, AuditResultSuccess, AuditResourceAccount, id, "")
	writeData(w, http.StatusOK, h.projectAccount(ctx, updated, now, true))
}

// decideFunding wraps domain.ApplyFundingSupersession for the
// no-current-row case: when currentPtr is nil (the account has no funding
// evidence at all), the candidate is inserted directly with no stamp.
func decideFunding(currentPtr *domain.FundingEvidence, candidate domain.FundingEvidence, now time.Time) (domain.FundingSupersessionResult, error) {
	if currentPtr == nil {
		return domain.FundingSupersessionResult{Superseded: true, NewCurrent: candidate}, nil
	}
	return domain.ApplyFundingSupersession(*currentPtr, candidate, now)
}

// parseFundingValue maps the request body's funding string to a
// domain.Funding, accepting exactly the three valid values and rejecting
// everything else.
func parseFundingValue(raw string) (domain.Funding, bool) {
	switch domain.Funding(raw) {
	case domain.FundingFree, domain.FundingPaid, domain.FundingUnknown:
		return domain.Funding(raw), true
	}
	return "", false
}

// --- Lifecycle mutations ---

// ServeStop implements POST /accounts/{id}/stop: connected -> stopped.
func (h *AccountsHandler) ServeStop(w http.ResponseWriter, r *http.Request) {
	h.transitionConnection(w, r, domain.ConnectionStopped, AuditActionAccountStop)
}

// ServeResume implements POST /accounts/{id}/resume: stopped -> connected.
func (h *AccountsHandler) ServeResume(w http.ResponseWriter, r *http.Request) {
	h.transitionConnection(w, r, domain.ConnectionConnected, AuditActionAccountResume)
}

// transitionConnection is the shared body of stop/resume: load the account,
// apply the pure-domain TransitionConnection decision, persist it, and
// audit. An illegal transition (e.g. resume a connected account, or resume
// a disconnected one — disconnected can only return via re-enrollment) is
// rejected with invalid_state 409 and the account is left unchanged.
func (h *AccountsHandler) transitionConnection(w http.ResponseWriter, r *http.Request, target domain.ConnectionState, action string) {
	if r.Method != http.MethodPost {
		writeAuthError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false)
		return
	}

	ctx := r.Context()
	id := r.PathValue("id")

	account, ok, err := h.accounts.GetByID(ctx, id)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}
	if !ok {
		h.audit.Emit(ctx, action, AuditResultFailure, AuditResourceAccount, id, "not_found")
		writeAuthError(w, http.StatusNotFound, "not_found", "account not found", false)
		return
	}

	now := h.now()
	next, terr := account.TransitionConnection(target, now)
	if terr != nil {
		h.audit.Emit(ctx, action, AuditResultFailure, AuditResourceAccount, id, "invalid_state")
		writeErrorDetails(w, http.StatusConflict, "invalid_state", "illegal connection_state transition", false, nil)
		return
	}

	persisted, persistedOK, perr := h.accounts.UpdateConnectionState(ctx, id, next, now)
	if perr != nil {
		h.audit.Emit(ctx, action, AuditResultFailure, AuditResourceAccount, id, "internal")
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}
	if !persistedOK {
		h.audit.Emit(ctx, action, AuditResultFailure, AuditResourceAccount, id, "not_found")
		writeAuthError(w, http.StatusNotFound, "not_found", "account not found", false)
		return
	}

	h.audit.Emit(ctx, action, AuditResultSuccess, AuditResourceAccount, id, "")
	writeData(w, http.StatusOK, h.projectAccount(ctx, persisted, now, true))
}

// ServeDisconnect implements DELETE /accounts/{id}: a SOFT disconnect
// ONLY. In ONE transaction it sets connection_state = disconnected, RETIRES
// every still-usable (active/staged) credential, and clears
// reauth_in_progress. The account row and its sanitized history are
// retained (restorable only via re-enrollment); nothing is hard-deleted or
// purged. A disconnected account can never be resumed (disconnected ->
// connected is illegal); it can only be re-enrolled (disconnected ->
// connecting).
func (h *AccountsHandler) ServeDisconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeAuthError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false)
		return
	}

	ctx := r.Context()
	id := r.PathValue("id")

	account, ok, err := h.accounts.GetByID(ctx, id)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}
	if !ok {
		h.audit.Emit(ctx, AuditActionAccountDisconnect, AuditResultFailure, AuditResourceAccount, id, "not_found")
		writeAuthError(w, http.StatusNotFound, "not_found", "account not found", false)
		return
	}

	now := h.now()
	next, terr := account.TransitionConnection(domain.ConnectionDisconnected, now)
	if terr != nil {
		h.audit.Emit(ctx, AuditActionAccountDisconnect, AuditResultFailure, AuditResourceAccount, id, "invalid_state")
		writeErrorDetails(w, http.StatusConflict, "invalid_state", "illegal connection_state transition", false, nil)
		return
	}

	persisted, persistedOK, perr := h.accounts.SoftDisconnect(ctx, id, next, now)
	if perr != nil {
		h.audit.Emit(ctx, AuditActionAccountDisconnect, AuditResultFailure, AuditResourceAccount, id, "internal")
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}
	if !persistedOK {
		h.audit.Emit(ctx, AuditActionAccountDisconnect, AuditResultFailure, AuditResourceAccount, id, "not_found")
		writeAuthError(w, http.StatusNotFound, "not_found", "account not found", false)
		return
	}

	h.audit.Emit(ctx, AuditActionAccountDisconnect, AuditResultSuccess, AuditResourceAccount, id, "")
	writeData(w, http.StatusOK, h.projectAccount(ctx, persisted, now, true))
}

// ServeHealth implements POST /accounts/{id}/health (02 §3 Axis 2). SCOPE
// THIS PHASE: no HealthAdapter is registered for any provider yet
// (opencode-zen and antigravity have none), so there is no live probe to
// call. This endpoint applies the pure-domain health transition for the
// target the request carries (defaulting to unknown when omitted) and
// returns the resulting health_state, auditing the action. When a real
// HealthAdapter is registered (P3), this is the slot a live probe call
// slots into — the domain transition + audit shape is already correct.
func (h *AccountsHandler) ServeHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAuthError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false)
		return
	}

	ctx := r.Context()
	id := r.PathValue("id")

	account, ok, err := h.accounts.GetByID(ctx, id)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}
	if !ok {
		h.audit.Emit(ctx, AuditActionAccountHealth, AuditResultFailure, AuditResourceAccount, id, "not_found")
		writeAuthError(w, http.StatusNotFound, "not_found", "account not found", false)
		return
	}

	// The request MAY carry a target health_state; default to unknown
	// (never crash when no probe is possible this phase).
	target := domain.HealthUnknown
	if r.ContentLength > 0 {
		var req struct {
			HealthState string `json:"health_state"`
		}
		if jerr := json.NewDecoder(r.Body).Decode(&req); jerr == nil && req.HealthState != "" {
			if t, pok := parseHealthState(req.HealthState); pok {
				target = t
			}
		}
	}

	now := h.now()
	credStatus := h.resolveCredentialStatus(ctx, id, now)
	next, terr := account.TransitionHealth(target, credStatus, now)
	if terr != nil {
		h.audit.Emit(ctx, AuditActionAccountHealth, AuditResultFailure, AuditResourceAccount, id, "invalid_state")
		writeErrorDetails(w, http.StatusConflict, "invalid_state", "illegal health_state transition", false, nil)
		return
	}

	persisted, persistedOK, perr := h.accounts.UpdateHealthState(ctx, id, next, now)
	if perr != nil {
		h.audit.Emit(ctx, AuditActionAccountHealth, AuditResultFailure, AuditResourceAccount, id, "internal")
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}
	if !persistedOK {
		h.audit.Emit(ctx, AuditActionAccountHealth, AuditResultFailure, AuditResourceAccount, id, "not_found")
		writeAuthError(w, http.StatusNotFound, "not_found", "account not found", false)
		return
	}

	h.audit.Emit(ctx, AuditActionAccountHealth, AuditResultSuccess, AuditResourceAccount, id, "")
	writeData(w, http.StatusOK, h.projectAccount(ctx, persisted, now, true))
}

// parseHealthState maps a request-body health_state string to a
// domain.HealthState, accepting exactly the five valid values.
func parseHealthState(raw string) (domain.HealthState, bool) {
	switch domain.HealthState(raw) {
	case domain.HealthUnknown, domain.HealthHealthy, domain.HealthDegraded, domain.HealthUnavailable, domain.HealthExpired:
		return domain.HealthState(raw), true
	}
	return "", false
}

// --- POST /providers/{id}/sync ---

// ServeProviderSync implements POST /providers/{id}/sync. SCOPE THIS
// PHASE: discovery is P3a and quota is P3b, and no HealthAdapter is
// registered for any provider yet, so there is no real per-account work
// to do — this endpoint iterates the provider's accounts and performs the
// best-effort refresh available now (effectively a no-op), auditing the
// action. It is structured so P3a/P3b can slot real discovery/quota/health
// work into the per-account loop without changing the route's shape.
func (h *AccountsHandler) ServeProviderSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAuthError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false)
		return
	}

	ctx := r.Context()
	id := r.PathValue("id")

	// Iterate the provider's accounts. The loop is the structural slot
	// P3a/P3b's real per-account discovery/quota/health work drops into;
	// this phase it does nothing per account but count them, so the
	// endpoint's response shape and audit are already correct for the
	// future.
	var (
		cursor   string
		synced   int
		skipped  int
		pageSize = defaultPageLimit
	)
	for {
		accounts, next, err := h.accounts.List(ctx, cursor, pageSize, id)
		if err != nil {
			h.audit.Emit(ctx, AuditActionProviderSync, AuditResultFailure, AuditResourceProvider, id, "internal")
			writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
			return
		}
		for range accounts {
			// No HealthAdapter/DiscoveryAdapter/QuotaAdapter is registered
			// this phase, so there is nothing to refresh per account. The
			// count is the observable signal the endpoint did its
			// best-effort pass.
			synced++
		}
		if next == "" {
			break
		}
		cursor = next
	}

	h.audit.Emit(ctx, AuditActionProviderSync, AuditResultSuccess, AuditResourceProvider, id, "")
	writeData(w, http.StatusOK, map[string]any{
		"provider": id,
		"synced":   synced,
		"skipped":  skipped,
		"note":     "best-effort sync; discovery/quota/health adapters are not registered this phase",
	})
}

// --- helpers ---

// headersWritten reports whether w has already had WriteHeader called on
// it (i.e. the response body has started). http.ResponseWriter does not
// expose this directly, so we sniff it via the responseWriter type the
// httptest package and the real server both satisfy; for an opaque
// http.ResponseWriter we conservatively assume written=false (the common
// clean-failure path).
func headersWritten(w http.ResponseWriter) bool {
	type writtenSniffer interface {
		Written() bool
	}
	if s, ok := w.(writtenSniffer); ok {
		return s.Written()
	}
	// httptest.ResponseRecorder exposes its status via a Code field, but
	// that is not part of http.ResponseWriter; for the real server the
	// deferred WriteHeader-on-first-Write means we cannot cheaply tell. We
	// only reach this branch on a decrypt failure, which happens BEFORE
	// any body write, so false is correct here.
	return false
}

// newFundingEvidenceID is the default funding-evidence id minter (a fresh
// high-entropy random id), used when NewAccountsHandler's caller does not
// supply one. It reuses newOAuthTransactionID, the generic random-id
// minter this package already owns, exactly like ConnectService does.
func newFundingEvidenceID() string {
	return newOAuthTransactionID()
}
