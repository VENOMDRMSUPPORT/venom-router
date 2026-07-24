package httpapi

import (
	"crypto/rand"
	"encoding/hex"
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
	now         func() time.Time
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

// callbackURL builds the deterministic redirect_uri for providerID's
// callback route on this control-plane bind — the same value is used at
// Begin (handed to the adapter) and reconstructed at Complete (handed to
// the adapter again, unchanged, per the OAuth spec's requirement that
// the redirect_uri match across both legs of the flow).
func (h *OAuthHandler) callbackURL(providerID string) string {
	return "http://" + h.allowedHost + "/api/control/v1/oauth/" + providerID + "/callback"
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
		RedirectURI: h.callbackURL(id),
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

// ServeCallback implements GET /api/control/v1/oauth/{provider}/callback:
// sits behind networkGate ONLY (no owner session/CSRF — the provider's
// redirect carries neither). Before calling Complete, it peeks (via
// PeekTransactionIDByStateHash — a non-consuming, side-effect-free
// lookup) whether this state hashes to a transaction the reauth-binding
// cache marked as a TARGETED reauthentication (POST .../accounts/{id}/
// reauth/begin, P2b-PROV-008); if so, the bound account id is threaded
// into Complete as ReauthAccountID so the account_identity_mismatch
// guard applies BEFORE anything is staged/swapped — Complete cannot
// undo a swap after the fact, so this binding must be resolved ahead of
// the call, not after. It then completes the transaction via
// OAuthEnrollmentService.Complete, caches the terminal outcome (status +
// account id OR a safe error code — never the code/tokens/verifier) keyed
// by whatever transaction id Complete reports, and renders a minimal,
// secret-free HTML page telling the owner to return to the dashboard.
func (h *OAuthHandler) ServeCallback(w http.ResponseWriter, r *http.Request) {
	providerID := r.PathValue("provider")
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")

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
	if peekedTxID, ok, _ := h.tx.PeekTransactionIDByStateHash(r.Context(), application.HashOAuthState(state)); ok {
		if binding, bound := h.reauth.get(peekedTxID); bound {
			reauthAccountID = binding.accountID
		}
	}

	txID, account, err := h.service.Complete(r.Context(), application.CompleteOAuthParams{
		ProviderID:      providerID,
		Adapter:         adapter,
		RawState:        state,
		Code:            code,
		RedirectURI:     h.callbackURL(providerID),
		FundingMode:     catalogFundingModeFor(providerID),
		ReauthAccountID: reauthAccountID,
	})
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
		RedirectURI: h.callbackURL(acct.ProviderID),
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

// catalogFundingModeFor translates providerID's catalog FundingPolicy.Mode
// (internal/providers's own enum, since providers must not import
// accounts/domain) into the accounts/domain.FundingMode
// OAuthEnrollmentService.Complete expects. An id with no catalog entry
// (should not happen for a registered adapter, but fails closed rather
// than assuming) defaults to evidence_required — the same "cannot
// classify" default the catalog itself uses for its own unclassifiable
// entries.
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
