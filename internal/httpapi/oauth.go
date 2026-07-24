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
	allowedHost string
	cache       *oauthResultCache
	now         func() time.Time
}

// NewOAuthHandler builds the handler. allowedHost is the same
// loopback+Host-allowlist value ControlMux already threads through
// networkGate — it is reused here only to construct the deterministic
// callback redirect_uri Begin hands the adapter and Complete later
// re-derives; it performs no gating of its own (ControlMux's mux-level
// wiring does that).
func NewOAuthHandler(service *application.OAuthEnrollmentService, reg *providers.Registry, tx *storage.OAuthTransactionRepo, allowedHost string) *OAuthHandler {
	return &OAuthHandler{service: service, reg: reg, tx: tx, allowedHost: allowedHost, cache: newOAuthResultCache(), now: time.Now}
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
		writeAuthError(w, http.StatusNotFound, "not_found", "provider has no OAuth adapter registered", false)
		return
	}

	result, err := h.service.Begin(r.Context(), application.BeginOAuthParams{
		ProviderID:  id,
		Adapter:     adapter,
		RedirectURI: h.callbackURL(id),
	})
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "internal", "failed to begin OAuth enrollment", true)
		return
	}

	writeData(w, http.StatusAccepted, beginOAuthJSON{
		TransactionID: result.TransactionID,
		AuthorizeURL:  result.AuthorizeURL,
		ExpiresAt:     result.ExpiresAt.UTC().Format(time.RFC3339),
	})
}

// ServeCallback implements GET /api/control/v1/oauth/{provider}/callback:
// sits behind networkGate ONLY (no owner session/CSRF — the provider's
// redirect carries neither). It completes the transaction via
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
		renderOAuthCallbackPage(w, false)
		return
	}

	txID, account, err := h.service.Complete(r.Context(), application.CompleteOAuthParams{
		ProviderID:  providerID,
		Adapter:     adapter,
		RawState:    state,
		Code:        code,
		RedirectURI: h.callbackURL(providerID),
		FundingMode: catalogFundingModeFor(providerID),
	})
	if err != nil {
		if txID != "" {
			h.cache.storeFailed(txID, safeOAuthErrorCode(err))
		}
		renderOAuthCallbackPage(w, false)
		return
	}

	h.cache.storeCompleted(txID, account.ID)
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
	case errors.Is(err, application.ErrOAuthAccountAlreadyConnected):
		return "account_already_connected"
	case errors.Is(err, providers.ErrInvalidCredential):
		return "invalid_credential"
	case errors.Is(err, providers.ErrProviderUnavailable):
		return "provider_unavailable"
	default:
		return "internal_error"
	}
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
