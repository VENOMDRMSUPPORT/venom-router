package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/application"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// enrollmentRoute is the fixed route key idempotencyStore.Execute keys
// replays under for this handler — matching route+Idempotency-Key
// pairs, not the request path (which varies per provider id).
const enrollmentRoute = "POST /providers/{id}/accounts"

// EnrollmentHandler serves the P2b-CAPI-003 API-key enrollment surface:
// POST /api/control/v1/providers/{id}/accounts. Owner-session + CSRF
// gated (via ControlMux's `gated`) exactly like every other mutating
// control route, and Idempotency-Key aware via idem.Execute so a
// retried request with the same key never re-runs ConnectAPIKeyAccount.
type EnrollmentHandler struct {
	connect  *application.ConnectService
	reg      *providers.Registry
	funding  *storage.FundingEvidenceRepo
	accounts *storage.AccountRepo
	idem     *idempotencyStore
	audit    *auditEmitter
	now      func() time.Time
}

// NewEnrollmentHandler builds the handler over the shared provider
// registry, connect service, funding-evidence reader (used only to
// report the persisted funding classification back in the success
// response), idempotency store, and audit emitter.
func NewEnrollmentHandler(connect *application.ConnectService, reg *providers.Registry, funding *storage.FundingEvidenceRepo, accounts *storage.AccountRepo, idem *idempotencyStore, audit *auditEmitter) *EnrollmentHandler {
	return &EnrollmentHandler{connect: connect, reg: reg, funding: funding, accounts: accounts, idem: idem, audit: audit, now: time.Now}
}

// connectAPIKeyRequest is POST .../accounts' request body.
type connectAPIKeyRequest struct {
	APIKey  string `json:"api_key"`
	Funding string `json:"funding,omitempty"`
	Label   string `json:"label,omitempty"`
}

// accountJSON is POST .../accounts' success payload projection — never
// the submitted key or any credential material.
type accountJSON struct {
	ID              string `json:"id"`
	ProviderID      string `json:"provider"`
	ExternalID      string `json:"external_id"`
	ConnectionState string `json:"connection_state"`
	HealthState     string `json:"health_state"`
	Funding         string `json:"funding"`
	DisplayStatus   string `json:"display_status"`
}

func toAccountJSON(a domain.Account, funding domain.Funding) accountJSON {
	return accountJSON{
		ID:              a.ID,
		ProviderID:      a.ProviderID,
		ExternalID:      a.ExternalID,
		ConnectionState: string(a.ConnectionState),
		HealthState:     string(a.HealthState),
		Funding:         string(funding),
		// No cooldown signal exists for a just-connected account (there
		// is nothing yet to have cooled down from) — false is always
		// correct here, unlike a general account-status projection that
		// would need the caller's own cooldown lookup.
		DisplayStatus: string(domain.DeriveDisplayStatus(a, false)),
	}
}

// ServeConnect implements POST /api/control/v1/providers/{id}/accounts
// (P2b-CAPI-003). The method check happens outside idem.Execute so a
// wrong-method request is never captured as a replayable response.
func (h *EnrollmentHandler) ServeConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAuthError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false)
		return
	}
	h.idem.Execute(w, r, enrollmentRoute, h.serveConnect)
}

// serveConnect is ServeConnect's actual body, run at most once per
// (route, Idempotency-Key) pair by idem.Execute.
func (h *EnrollmentHandler) serveConnect(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")

	adapter, ok := h.reg.APIKeyAdapter(providers.ProviderID(id))
	if !ok {
		h.audit.Emit(ctx, AuditActionAccountConnect, AuditResultFailure, AuditResourceProvider, id, "not_found")
		writeAuthError(w, http.StatusNotFound, "not_found", "provider has no API-key adapter registered", false)
		return
	}

	var req connectAPIKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.audit.Emit(ctx, AuditActionAccountConnect, AuditResultFailure, AuditResourceProvider, id, "validation_error")
		writeAuthError(w, http.StatusBadRequest, "validation_error", "invalid request body", false)
		return
	}
	if req.APIKey == "" {
		h.audit.Emit(ctx, AuditActionAccountConnect, AuditResultFailure, AuditResourceProvider, id, "validation_error")
		writeAuthError(w, http.StatusBadRequest, "validation_error", "api_key is required", false)
		return
	}

	ownerFunding, ok := parseOwnerFunding(req.Funding)
	if !ok {
		h.audit.Emit(ctx, AuditActionAccountConnect, AuditResultFailure, AuditResourceProvider, id, "validation_error")
		writeAuthError(w, http.StatusBadRequest, "validation_error", "funding must be one of free, paid, unknown", false)
		return
	}

	account, err := h.connect.ConnectAPIKeyAccount(ctx, application.ConnectAPIKeyAccountParams{
		ProviderID:   id,
		Adapter:      adapter,
		PlaintextKey: req.APIKey,
		FundingMode:  catalogFundingModeFor(id),
		OwnerFunding: ownerFunding,
	})
	if err != nil {
		code, status, retryable := connectErrorResponse(err)
		h.audit.Emit(ctx, AuditActionAccountConnect, AuditResultFailure, AuditResourceProvider, id, code)
		writeAuthError(w, status, code, "account enrollment failed", retryable)
		return
	}

	fundingEvidence, ok, err := h.funding.CurrentForAccount(ctx, account.ID)
	fundingValue := domain.FundingUnknown
	if err == nil && ok {
		fundingValue = fundingEvidence.Funding
	}

	if req.Label != "" {
		_, _ = h.accounts.UpdateLabel(ctx, account.ID, req.Label, h.now())
	}

	h.audit.Emit(ctx, AuditActionAccountConnect, AuditResultSuccess, AuditResourceAccount, account.ID, "")
	writeData(w, http.StatusCreated, toAccountJSON(account, fundingValue))
}

// parseOwnerFunding maps the request body's optional funding string to
// an owner-override *domain.Funding: "" means no override (ok=true,
// nil pointer — the catalog's FundingMode decides instead); any of the
// three valid Funding values is accepted; anything else is rejected
// (ok=false).
func parseOwnerFunding(raw string) (*domain.Funding, bool) {
	switch raw {
	case "":
		return nil, true
	case string(domain.FundingFree), string(domain.FundingPaid), string(domain.FundingUnknown):
		f := domain.Funding(raw)
		return &f, true
	default:
		return nil, false
	}
}

// connectErrorResponse maps a ConnectAPIKeyAccount error to a small,
// fixed vocabulary of canary-safe (code, status, retryable) triples —
// never the error's own message text, which may wrap adapter-supplied
// detail this package cannot vouch for.
//
//   - ErrConnectInvalidCredential -> invalid_credential, 422, not
//     retryable: the credential itself is genuinely bad.
//   - ErrConnectProviderUnavailable -> provider_unavailable, 502,
//     retryable: the key's validity is unknown, try again later.
//   - ErrConnectAccountAlreadyConnected -> account_already_connected,
//     409, not retryable: a friendly, typed conflict — never a bare/raw
//     409 or an untyped 500 from the underlying UNIQUE constraint.
//   - anything else -> internal, 500, retryable (this package's usual
//     safe default for an unclassified failure).
func connectErrorResponse(err error) (code string, status int, retryable bool) {
	switch {
	case errors.Is(err, application.ErrConnectInvalidCredential):
		return "invalid_credential", http.StatusUnprocessableEntity, false
	case errors.Is(err, application.ErrConnectProviderUnavailable):
		return "provider_unavailable", http.StatusBadGateway, true
	case errors.Is(err, application.ErrConnectAccountAlreadyConnected):
		return "account_already_connected", http.StatusConflict, false
	default:
		return "internal", http.StatusInternalServerError, true
	}
}
