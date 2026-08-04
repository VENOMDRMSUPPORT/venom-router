package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/application"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// OAuthHandler serves the P2b-PROV-006 OAuth enrollment HTTP surface:
// POST .../oauth/begin (owner-session + CSRF gated, via ControlMux's
// `gated`), and GET .../oauth/{provider}/callback + GET
// .../oauth/{transaction_id}/status (network-gated only — the provider's
// redirect and a status poller both carry no owner session/CSRF, exactly
// like the unauthenticated /auth/* handshake). The transaction_id itself
// is the unguessable capability token the status endpoint relies on —
// there is deliberately no additional auth on it.
type OAuthHandler struct {
	service     *application.OAuthEnrollmentService
	reg         *providers.Registry
	tx          *storage.OAuthTransactionRepo
	accounts    *storage.AccountRepo
	allowedHost string
	cache       *oauthResultCache
	reauth      *reauthBindingCache
	audit       *auditEmitter
	discovery   discoveryTrigger
	quota       quotaTrigger
	now         func() time.Time
}

// quotaTrigger fires a best-effort background quota refresh for a newly
// connected OAuth account (legacy sync-on-connect). *QuotaHandler implements
// it; a nil trigger disables auto-quota.
type quotaTrigger interface {
	TriggerBackgroundQuota(ctx context.Context, accountID string)
}

// NewOAuthHandler builds the handler. allowedHost is the same
// loopback+Host-allowlist value ControlMux already threads through
// networkGate — it is reused here only to construct the deterministic
// callback redirect_uri Begin hands the adapter and Complete later
// re-derives; it performs no gating of its own (ControlMux's mux-level
// wiring does that). accounts resolves the target account for a
// reauthentication begin (P2b-PROV-008). audit is the shared
// P2b-OBS-001 emitter every mutating route (and the network-gated
// callback's terminal outcome) records exactly one audit_event through.
func NewOAuthHandler(service *application.OAuthEnrollmentService, reg *providers.Registry, tx *storage.OAuthTransactionRepo, accounts *storage.AccountRepo, allowedHost string, audit *auditEmitter) *OAuthHandler {
	return &OAuthHandler{
		service: service, reg: reg, tx: tx, accounts: accounts, allowedHost: allowedHost,
		cache: newOAuthResultCache(), reauth: newReauthBindingCache(), audit: audit, now: time.Now,
	}
}

// SetDiscoveryTrigger wires auto model-discovery after a successful OAuth
// connect (mirrors EnrollmentHandler.SetDiscoveryTrigger).
func (h *OAuthHandler) SetDiscoveryTrigger(t discoveryTrigger) { h.discovery = t }

// SetQuotaTrigger wires auto quota refresh after a successful OAuth connect.
func (h *OAuthHandler) SetQuotaTrigger(t quotaTrigger) { h.quota = t }

func (h *OAuthHandler) triggerPostConnectSync(ctx context.Context, accountID string) {
	if h.discovery != nil {
		h.discovery.TriggerBackgroundDiscovery(ctx, accountID)
	}
	if h.quota != nil {
		h.quota.TriggerBackgroundQuota(ctx, accountID)
	}
}

// callbackURL builds the deterministic redirect_uri every OAuth begin hands
// its adapter: http://<allowedHost>/callback. This is the ONE shape the
// claude-code/clinepass/antigravity public clients have REGISTERED — the
// legacy implementation sent `${window.location.origin}/callback` for every
// non-fixed-redirect provider (verified against venom-router-legacy
// 2026-08-03), and claude.ai rejects any other path with "Redirect URI is not
// supported by client". It is provider-agnostic on purpose: the callback route
// is GET /callback and the handler resolves the provider from the `state`
// (the oauth_transactions row stores provider_id), never from the URL path.
// The same value is reconstructed at Complete (handed to the adapter again,
// unchanged, per the OAuth spec's requirement that the redirect_uri match
// across both legs of the flow). Fixed-redirect providers (Codex, xAI) are
// future scope and will override this per-provider when they land.
func (h *OAuthHandler) callbackURL() string {
	return "http://" + h.allowedHost + "/callback"
}

// beginOAuthJSON is POST .../oauth/begin's success payload.
type beginOAuthJSON struct {
	TransactionID string `json:"transaction_id"`
	AuthorizeURL  string `json:"authorize_url"`
	ExpiresAt     string `json:"expires_at"`
}

// ServeBegin implements POST /api/control/v1/providers/{id}/oauth/begin:
// looks up id's registered OAuthAdapter (not_found if none), starts a
// fresh transaction via OAuthEnrollmentService.Begin, and returns the
// transaction id + authorize URL for the caller to send the owner to.
func (h *OAuthHandler) ServeBegin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAuthError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false)
		return
	}

	id := r.PathValue("id")
	adapter, ok := h.reg.OAuthAdapter(providers.ProviderID(id))
	if !ok {
		h.audit.Emit(r.Context(), AuditActionOAuthBegin, AuditResultFailure, AuditResourceProvider, id, "not_found")
		writeAuthError(w, http.StatusNotFound, "not_found", "provider has no OAuth adapter registered", false)
		return
	}

	result, err := h.service.Begin(r.Context(), application.BeginOAuthParams{
		ProviderID:  id,
		Adapter:     adapter,
		RedirectURI: h.callbackURL(),
	})
	if err != nil {
		h.audit.Emit(r.Context(), AuditActionOAuthBegin, AuditResultFailure, AuditResourceProvider, id, "internal_error")
		writeAuthError(w, http.StatusInternalServerError, "internal", "failed to begin OAuth enrollment", true)
		return
	}

	h.audit.Emit(r.Context(), AuditActionOAuthBegin, AuditResultSuccess, AuditResourceProvider, id, "")
	writeData(w, http.StatusAccepted, beginOAuthJSON{
		TransactionID: result.TransactionID,
		AuthorizeURL:  result.AuthorizeURL,
		ExpiresAt:     result.ExpiresAt.UTC().Format(time.RFC3339),
	})
}

// ServeCallback implements the OAuth redirect target — GET /callback (the
// registered, provider-agnostic shape; the legacy provider-specific GET
// /api/control/v1/oauth/{provider}/callback also lands here and supplies the
// provider directly): sits behind networkGate ONLY (no owner session/CSRF — the
// provider's redirect carries neither). The provider is resolved from the
// `state` via the transaction row (which stores provider_id) when the URL path
// carries no provider. Before calling Complete, it peeks (via
// PeekTransactionIDByStateHash — a non-consuming, side-effect-free lookup)
// whether this state hashes to a transaction the reauth-binding cache marked as
// a TARGETED reauthentication (POST .../accounts/{id}/reauth/begin,
// P2b-PROV-008); if so, the bound account id is threaded into Complete as
// ReauthAccountID so the account_identity_mismatch guard applies BEFORE
// anything is staged/swapped — Complete cannot undo a swap after the fact, so
// this binding must be resolved ahead of the call, not after. It then completes
// the transaction via OAuthEnrollmentService.Complete, caches the terminal
// outcome (status + account id OR a safe error code — never the
// code/tokens/verifier) keyed by whatever transaction id Complete reports, and
// renders a minimal, secret-free HTML page telling the owner to return to the
// dashboard.
func (h *OAuthHandler) ServeCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")

	// Provider-agnostic redirect target: resolve the provider from the
	// transaction the state names (the row stores provider_id). A state that
	// names no row fails closed exactly like an unknown provider on the
	// legacy provider-specific path — no oracle, no guessed provider.
	//
	// For providers that omit `state` (e.g. clinepass), the callback still
	// carries `code`. We cannot safely complete server-side without a
	// binding, so we render a relay page: the dashboard opener (which holds
	// the unguessable transaction_id from Begin) finishes via
	// POST /oauth/complete — the proven legacy pattern.
	providerID := r.PathValue("provider")
	stateIsTransactionID := false
	if providerID == "" {
		// ClinePass omits state entirely: relay to the opener so it can
		// complete with the transaction_id from Begin. If state IS present
		// but unknown, fail closed — never treat a forged state as a relay.
		if state == "" {
			if code != "" {
				renderOAuthRelayPage(w)
				return
			}
			h.audit.Emit(r.Context(), AuditActionOAuthComplete, AuditResultFailure, AuditResourceProvider, "", "not_found")
			renderOAuthCallbackPage(w, false)
			return
		}
		if pid, ok, err := h.tx.ProviderIDByTransactionID(r.Context(), state); err == nil && ok {
			providerID = pid
			stateIsTransactionID = true
		} else {
			pid, ok, err := h.tx.ProviderIDByStateHash(r.Context(), application.HashOAuthState(state))
			if err != nil || !ok {
				h.audit.Emit(r.Context(), AuditActionOAuthComplete, AuditResultFailure, AuditResourceProvider, "", "not_found")
				renderOAuthCallbackPage(w, false)
				return
			}
			providerID = pid
		}
	}
	adapter, ok := h.reg.OAuthAdapter(providers.ProviderID(providerID))
	if !ok {
		h.audit.Emit(r.Context(), AuditActionOAuthComplete, AuditResultFailure, AuditResourceProvider, providerID, "not_found")
		renderOAuthCallbackPage(w, false)
		return
	}

	// Best-effort reauth-binding resolution: a peek failure (extremely
	// unlikely — same DB, no side effects) just means this specific
	// completion is treated as a non-targeted flow; it does NOT skip
	// Complete's own replay-safe consume/validate logic, and an identity
	// that happens to resolve to an existing account is still
	// reauthenticated regardless (see Complete's doc comment).
	var reauthAccountID string
	// For the state-less path, the reauth peek is keyed on the transaction
	// id itself (no state hash exists to peek with).
	var peekTxID string
	if stateIsTransactionID {
		peekTxID = state
	} else if peeked, ok, _ := h.tx.PeekTransactionIDByStateHash(r.Context(), application.HashOAuthState(state)); ok {
		peekTxID = peeked
	}
	if binding, bound := h.reauth.get(peekTxID); bound {
		reauthAccountID = binding.accountID
	}

	fundingFixed, fundingLocked := catalogFundingFixedFor(providerID)
	completeParams := application.CompleteOAuthParams{
		ProviderID:      providerID,
		Adapter:         adapter,
		RawState:        state,
		Code:            code,
		RedirectURI:     h.callbackURL(),
		FundingMode:     catalogFundingModeFor(providerID),
		FundingFixed:    fundingFixed,
		FundingLocked:   fundingLocked,
		ReauthAccountID: reauthAccountID,
	}
	if stateIsTransactionID {
		// Legacy recovery path: state query carried a transaction id.
		completeParams.RawState = ""
		completeParams.TransactionID = state
	}
	txID, account, err := h.service.Complete(r.Context(), completeParams)
	if err != nil {
		if txID != "" {
			h.cache.storeFailed(txID, safeOAuthErrorCode(err))
		}
		h.audit.Emit(r.Context(), AuditActionOAuthComplete, AuditResultFailure, AuditResourceProvider, providerID, safeOAuthErrorCode(err))
		renderOAuthCallbackPage(w, false)
		return
	}

	h.cache.storeCompleted(txID, account.ID)
	h.audit.Emit(r.Context(), AuditActionOAuthComplete, AuditResultSuccess, AuditResourceAccount, account.ID, "")
	h.triggerPostConnectSync(r.Context(), account.ID)
	renderOAuthCallbackPage(w, true)
}

// statusJSON is GET .../oauth/{transaction_id}/status's payload.
type statusJSON struct {
	Status    string `json:"status"`
	AccountID string `json:"account_id,omitempty"`
	Error     string `json:"error,omitempty"`
}

// ServeStatus implements GET /api/control/v1/oauth/{transaction_id}/status:
// sits behind networkGateJSON ONLY (no owner session/CSRF — the
// transaction_id, an unguessable server-minted value, is itself the
// capability token). It resolves the cached terminal result first; only
// when no cache entry exists does it derive pending/expired directly
// from the oauth_transactions row (consumed=0 && not yet expired ->
// pending; consumed=0 && expired -> expired; consumed=1 with no cache
// entry -> failed, the safe default for an outcome this process can no
// longer distinguish).
func (h *OAuthHandler) ServeStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAuthError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false)
		return
	}

	txID := r.PathValue("transaction_id")

	if entry, ok := h.cache.get(txID); ok {
		writeData(w, http.StatusOK, statusJSON{Status: entry.status, AccountID: entry.accountID, Error: entry.errorCode})
		return
	}

	consumed, expired, ok, err := h.tx.GetStatusByTransactionID(r.Context(), txID, h.now())
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}
	if !ok {
		writeAuthError(w, http.StatusNotFound, "not_found", "transaction not found", false)
		return
	}

	status := "pending"
	switch {
	case consumed:
		status = "failed"
	case expired:
		status = "expired"
	}
	writeData(w, http.StatusOK, statusJSON{Status: status})
}

// completeCodeJSON is POST .../oauth/complete's request payload.
type completeCodeJSON struct {
	// TransactionID is the id POST .../oauth/begin returned. It is the
	// unguessable capability token that binds the pasted code to exactly
	// one transaction (the same token the status endpoint uses).
	TransactionID string `json:"transaction_id"`
	// Code is the RAW string the owner copies from the provider's hosted
	// code page (claude-code's platform.claude.com page shows
	// `<auth_code>#<fragment>`; the fragment is the echoed state and is
	// preserved verbatim so the exchange can hand it back).
	Code string `json:"code"`
}

// ServeCompleteCode implements POST /api/control/v1/oauth/complete —
// owner-session + CSRF gated like every mutating control route. It is the
// opener-driven completion leg used when the provider omits `state` from the
// callback (clinepass) or when the dashboard already holds the transaction id
// from Begin (legacy pattern). The transaction is resolved by id, the provider
// from the transaction row, and the code is exchanged exactly as the browser
// callback would — replay-safe (consume-before-exchange), code never
// persisted, terminal outcome cached for the status endpoint.
func (h *OAuthHandler) ServeCompleteCode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAuthError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false)
		return
	}

	var req completeCodeJSON
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAuthError(w, http.StatusBadRequest, "validation_error", "invalid request body", false)
		return
	}
	if req.TransactionID == "" || req.Code == "" {
		writeAuthError(w, http.StatusBadRequest, "validation_error", "transaction_id and code are required", false)
		return
	}

	// Resolve the provider from the transaction row (never from the client).
	providerID, ok, err := h.tx.ProviderIDByTransactionID(r.Context(), req.TransactionID)
	if err != nil {
		h.audit.Emit(r.Context(), AuditActionOAuthComplete, AuditResultFailure, AuditResourceProvider, "", "internal_error")
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}
	if !ok {
		h.audit.Emit(r.Context(), AuditActionOAuthComplete, AuditResultFailure, AuditResourceProvider, "", "not_found")
		writeAuthError(w, http.StatusNotFound, "not_found", "transaction not found", false)
		return
	}

	adapter, ok := h.reg.OAuthAdapter(providers.ProviderID(providerID))
	if !ok {
		h.audit.Emit(r.Context(), AuditActionOAuthComplete, AuditResultFailure, AuditResourceProvider, providerID, "not_found")
		writeAuthError(w, http.StatusNotFound, "not_found", "provider has no OAuth adapter registered", false)
		return
	}

	// Reauth binding: if this transaction is a TARGETED reauthentication
	// (POST .../accounts/{id}/reauth/begin), the binding must be threaded
	// into Complete so the account_identity_mismatch guard applies before
	// anything is staged/swapped.
	var reauthAccountID string
	if binding, bound := h.reauth.get(req.TransactionID); bound {
		reauthAccountID = binding.accountID
	}

	fundingFixed, fundingLocked := catalogFundingFixedFor(providerID)
	txID, account, err := h.service.Complete(r.Context(), application.CompleteOAuthParams{
		ProviderID:      providerID,
		Adapter:         adapter,
		TransactionID:   req.TransactionID,
		Code:            req.Code,
		RedirectURI:     h.callbackURL(),
		FundingMode:     catalogFundingModeFor(providerID),
		FundingFixed:    fundingFixed,
		FundingLocked:   fundingLocked,
		ReauthAccountID: reauthAccountID,
	})
	if err != nil {
		if txID != "" {
			h.cache.storeFailed(txID, safeOAuthErrorCode(err))
		}
		h.audit.Emit(r.Context(), AuditActionOAuthComplete, AuditResultFailure, AuditResourceProvider, providerID, safeOAuthErrorCode(err))
		writeAuthError(w, http.StatusBadRequest, safeOAuthErrorCode(err), "the OAuth code could not be completed", false)
		return
	}

	h.cache.storeCompleted(txID, account.ID)
	h.audit.Emit(r.Context(), AuditActionOAuthComplete, AuditResultSuccess, AuditResourceAccount, account.ID, "")
	h.triggerPostConnectSync(r.Context(), account.ID)
	writeData(w, http.StatusOK, statusJSON{Status: "completed", AccountID: account.ID})
}

// safeOAuthErrorCode maps a Complete error to a small, fixed vocabulary
// of canary-safe codes — never the error's own message text, which may
// wrap adapter-supplied detail this package cannot vouch for.
func safeOAuthErrorCode(err error) string {
	switch {
	case errors.Is(err, application.ErrOAuthTransactionInvalid):
		return "invalid_or_expired"
	case errors.Is(err, application.ErrOAuthAccountIdentityMismatch):
		return "account_identity_mismatch"
	case errors.Is(err, domain.ErrReauthenticationInProgress):
		return "reauthentication_in_progress"
	case errors.Is(err, providers.ErrInvalidCredential):
		return "invalid_credential"
	case errors.Is(err, providers.ErrProviderUnavailable):
		return "provider_unavailable"
	default:
		return "internal_error"
	}
}

// ServeReauthBegin implements POST
// /api/control/v1/accounts/{id}/reauth/begin (P2b-PROV-008): looks up
// account id (404 if unknown or its provider has no registered OAuth
// adapter), starts a fresh OAuth transaction for that account's OWN
// provider by reusing PROV-006's Begin verbatim, and records
// transaction_id -> {accountID, account's current external_id} in the
// bounded reauth-binding cache so ServeCallback can later enforce the
// account_identity_mismatch guard before staging/swapping anything.
func (h *OAuthHandler) ServeReauthBegin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAuthError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false)
		return
	}

	id := r.PathValue("id")
	acct, ok, err := h.accounts.GetByID(r.Context(), id)
	if err != nil {
		h.audit.Emit(r.Context(), AuditActionReauthBegin, AuditResultFailure, AuditResourceAccount, id, "internal_error")
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}
	if !ok {
		h.audit.Emit(r.Context(), AuditActionReauthBegin, AuditResultFailure, AuditResourceAccount, id, "not_found")
		writeAuthError(w, http.StatusNotFound, "not_found", "account not found", false)
		return
	}

	adapter, ok := h.reg.OAuthAdapter(providers.ProviderID(acct.ProviderID))
	if !ok {
		h.audit.Emit(r.Context(), AuditActionReauthBegin, AuditResultFailure, AuditResourceAccount, id, "not_found")
		writeAuthError(w, http.StatusNotFound, "not_found", "provider has no OAuth adapter registered", false)
		return
	}

	result, err := h.service.Begin(r.Context(), application.BeginOAuthParams{
		ProviderID:  acct.ProviderID,
		Adapter:     adapter,
		RedirectURI: h.callbackURL(),
	})
	if err != nil {
		h.audit.Emit(r.Context(), AuditActionReauthBegin, AuditResultFailure, AuditResourceAccount, id, "internal_error")
		writeAuthError(w, http.StatusInternalServerError, "internal", "failed to begin reauthentication", true)
		return
	}

	h.reauth.bind(result.TransactionID, acct.ID, acct.ExternalID)

	h.audit.Emit(r.Context(), AuditActionReauthBegin, AuditResultSuccess, AuditResourceAccount, acct.ID, "")
	writeData(w, http.StatusAccepted, beginOAuthJSON{
		TransactionID: result.TransactionID,
		AuthorizeURL:  result.AuthorizeURL,
		ExpiresAt:     result.ExpiresAt.UTC().Format(time.RFC3339),
	})
}

// --- Reauth binding cache ---

// reauthBindingCacheMaxEntries bounds reauthBindingCache exactly like
// oauthResultCache's own FIFO cap, above.
const reauthBindingCacheMaxEntries = 4096

// reauthBinding is what ServeReauthBegin records for a transaction id:
// the target account and the external_id it had at begin time (used
// only as a defensive cross-check; Complete's own mismatch guard
// compares against the account's freshly reloaded external_id, not this
// cached snapshot).
type reauthBinding struct {
	accountID  string
	externalID string
}

// reauthBindingCache is a process-lifetime, in-memory, bounded cache of
// transaction_id -> {target account, its external_id at begin time},
// mirroring oauthResultCache's exact shape (a process restart naturally
// forgets it; the bound transaction's 10-minute TTL then just expires
// and the owner re-initiates).
type reauthBindingCache struct {
	mu      sync.Mutex
	entries map[string]reauthBinding
	order   []string
}

func newReauthBindingCache() *reauthBindingCache {
	return &reauthBindingCache{entries: make(map[string]reauthBinding)}
}

func (c *reauthBindingCache) bind(transactionID, accountID, externalID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.entries[transactionID]; !exists && len(c.entries) >= reauthBindingCacheMaxEntries {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.entries, oldest)
	}
	if _, exists := c.entries[transactionID]; !exists {
		c.order = append(c.order, transactionID)
	}
	c.entries[transactionID] = reauthBinding{accountID: accountID, externalID: externalID}
}

func (c *reauthBindingCache) get(transactionID string) (reauthBinding, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, ok := c.entries[transactionID]
	return b, ok
}

// renderOAuthCallbackPage writes a minimal, secret-free HTML page for the
// browser the provider redirected back to — no code/token/verifier value
// ever appears in it, only a fixed, static success/failure message.
func renderOAuthCallbackPage(w http.ResponseWriter, ok bool) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if ok {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<!doctype html><html><body>Connected. You may close this window.</body></html>"))
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("<!doctype html><html><body>Connection failed. You may close this window and try again.</body></html>"))
}

// renderOAuthRelayPage is the legacy-style callback page for providers that
// omit `state` (or whose state cannot be resolved): it relays code/state to
// the opener via postMessage + localStorage so the dashboard can finish via
// POST /oauth/complete with the transaction_id it already holds. The auth
// code appears only in the URL the provider already redirected to — never in
// a Venom API log line from this handler.
func renderOAuthRelayPage(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`<!doctype html><html><body>
<p>Sign-in complete. Returning to Venom…</p>
<script>
(function () {
  var p = new URLSearchParams(location.search);
  var data = {
    code: p.get("code"),
    state: p.get("state"),
    error: p.get("error"),
    error_description: p.get("error_description")
  };
  var msg = { type: "venom_oauth_callback", data: data };
  if (window.opener) {
    try { window.opener.postMessage(msg, "*"); } catch (e) {}
  }
  try {
    localStorage.setItem("venom_oauth_callback", JSON.stringify(Object.assign({}, data, { timestamp: Date.now() })));
  } catch (e) {}
  setTimeout(function () { try { window.close(); } catch (e) {} }, 400);
})();
</script>
</body></html>`))
}

// catalogFundingModeFor translates providerID's catalog FundingPolicy.Mode
// (internal/providers's own enum, since providers must not import
// accounts/domain) into the accounts/domain.FundingMode
// OAuthEnrollmentService.Complete expects. An id with no catalog entry
// (should not happen for a registered adapter, but fails closed rather
// than assuming) defaults to evidence_required — the same "cannot
// classify" default the catalog itself uses for its own unclassifiable
// entries.
// catalogFundingFixedFor returns the catalog's declared FIXED funding value
// and lock flag for id — meaningful only when catalogFundingModeFor(id) is
// FundingModeFixed (e.g. clinepass: paid + locked). Unknown ids and
// non-fixed modes report unknown/unlocked, which StampFirstEvidence then
// treats honestly.
func catalogFundingFixedFor(id string) (domain.Funding, bool) {
	for _, e := range oauthCatalogEntries() {
		if string(e.ID) != id {
			continue
		}
		switch e.Funding.Fixed {
		case providers.FundingFree:
			return domain.FundingFree, e.Funding.Locked
		case providers.FundingPaid:
			return domain.FundingPaid, e.Funding.Locked
		}
		return domain.FundingUnknown, e.Funding.Locked
	}
	return domain.FundingUnknown, false
}

func catalogFundingModeFor(id string) domain.FundingMode {
	for _, e := range oauthCatalogEntries() {
		if string(e.ID) != id {
			continue
		}
		switch e.Funding.Mode {
		case providers.FundingModeFixed:
			return domain.FundingModeFixed
		case providers.FundingModeOwnerPolicy:
			return domain.FundingModeOwnerPolicy
		case providers.FundingModeProviderEvidence:
			return domain.FundingModeProviderEvidence
		}
		return domain.FundingModeEvidenceRequired
	}
	return domain.FundingModeEvidenceRequired
}

func oauthCatalogEntries() []providers.CatalogEntry {
	entries := append([]providers.CatalogEntry{}, providers.BuiltinCatalog()...)
	return append(entries, providers.CustomPathDescriptor())
}

// newOAuthTransactionID mints a fresh, high-entropy, unguessable
// transaction id — the application.IDGenerator this package's OAuth
// service composition uses. It is deliberately NOT a sequential or
// otherwise predictable value, since the transaction id doubles as the
// status endpoint's capability token.
func newOAuthTransactionID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand.Read is documented to never fail on this module's
		// supported platforms; this fallback only avoids a panic in the
		// theoretical case it ever does, at the cost of an id that is
		// merely unique-enough-for-this-process rather than
		// cryptographically unguessable.
		return fmt.Sprintf("oauth-fallback-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// --- Result cache ---

// oauthResultCacheMaxEntries bounds oauthResultCache exactly like
// idempotencyStore's own FIFO cap (idempotency.go) — a client (or a
// misbehaving provider) hammering distinct transaction ids cannot grow
// this store unboundedly for the life of the process.
const oauthResultCacheMaxEntries = 4096

// oauthResultEntry is one cached terminal OAuth outcome. It stores ONLY
// a status, an account id, and a safe error code — never the
// code/tokens/verifier, never a raw adapter error message.
type oauthResultEntry struct {
	status    string // "completed" | "failed"
	accountID string
	errorCode string
}

// oauthResultCache is a process-lifetime, in-memory, bounded cache of
// transaction_id -> terminal outcome, exactly mirroring idempotencyStore's
// shape (idempotency.go) for the same reason: not persisted, so a
// process restart naturally forgets it, and a poller falls back to
// deriving pending/expired/failed straight from the oauth_transactions
// row in that case (see ServeStatus).
type oauthResultCache struct {
	mu      sync.Mutex
	entries map[string]oauthResultEntry
	order   []string
}

func newOAuthResultCache() *oauthResultCache {
	return &oauthResultCache{entries: make(map[string]oauthResultEntry)}
}

func (c *oauthResultCache) storeCompleted(transactionID, accountID string) {
	c.store(transactionID, oauthResultEntry{status: "completed", accountID: accountID})
}

func (c *oauthResultCache) storeFailed(transactionID, errorCode string) {
	c.store(transactionID, oauthResultEntry{status: "failed", errorCode: errorCode})
}

func (c *oauthResultCache) store(k string, entry oauthResultEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.entries[k]; !exists && len(c.entries) >= oauthResultCacheMaxEntries {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.entries, oldest)
	}
	if _, exists := c.entries[k]; !exists {
		c.order = append(c.order, k)
	}
	c.entries[k] = entry
}

func (c *oauthResultCache) get(k string) (oauthResultEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[k]
	return entry, ok
}
