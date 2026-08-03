package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/application"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// --- A deterministic, in-memory fake OAuthAdapter (no real network call,
// no real provider) — the only kind of adapter these tests ever use. ---

type fakeHTTPOAuthAdapter struct {
	identity providers.IdentityResult
	creds    providers.StoredCredentials

	lastState, lastChallenge, lastRedirectURI string
	lastCode, lastVerifier                    string
	completeCalls                             int
}

func newFakeHTTPOAuthAdapter() *fakeHTTPOAuthAdapter {
	return &fakeHTTPOAuthAdapter{
		identity: providers.IdentityResult{ExternalID: "http-ext-1", Email: "owner@example.com", Plan: "pro"},
		creds:    providers.StoredCredentials{Value: "fake-http-oauth-token"},
	}
}

func (f *fakeHTTPOAuthAdapter) BeginOAuth(_ context.Context, redirectURI, state, pkceChallenge string) (string, error) {
	f.lastState, f.lastChallenge, f.lastRedirectURI = state, pkceChallenge, redirectURI
	return "https://fake-provider.example/authorize?state=" + state, nil
}

func (f *fakeHTTPOAuthAdapter) CompleteOAuth(_ context.Context, code, pkceVerifier, _ string) (providers.IdentityResult, providers.StoredCredentials, error) {
	f.completeCalls++
	f.lastCode, f.lastVerifier = code, pkceVerifier
	return f.identity, f.creds, nil
}

func (f *fakeHTTPOAuthAdapter) RefreshCredentials(_ context.Context, creds providers.StoredCredentials) (providers.StoredCredentials, error) {
	return creds, nil
}

// newTestOAuthHandler builds an OAuthHandler wired over a fresh migrated
// DB + keyring, with adapter registered under providerID in a fresh,
// test-local Registry — deliberately NOT ControlMux's own (always empty)
// registry, so these tests can exercise the real begin/callback/status
// behavior against a working fake adapter.
func newTestOAuthHandler(t *testing.T, providerID string, adapter providers.OAuthAdapter) (*OAuthHandler, *storage.DB) {
	t.Helper()
	db := testControlDB(t)
	if _, err := db.Conn().Exec(
		`INSERT INTO providers (id, display_name, auth_mode, funding_mode, created_at, updated_at) VALUES (?, ?, 'oauth2', 'owner_policy', 0, 0)`,
		providerID, providerID,
	); err != nil {
		t.Fatalf("seed provider: %v", err)
	}

	reg := providers.NewRegistry()
	if err := reg.Register(providers.Definition{ID: providers.ProviderID(providerID), AuthMode: providers.AuthModeOAuth, Transport: providers.TransportKindNativeOAuth, WireSchema: providers.WireSchemaGoogleGenerateContent, OAuth: adapter}); err != nil {
		t.Fatalf("register fake adapter: %v", err)
	}

	txRepo := storage.NewOAuthTransactionRepo(db)
	accountRepo := storage.NewAccountRepo(db)
	svc := application.NewOAuthEnrollmentService(
		txRepo, storage.NewEnrollmentRepo(db), accountRepo,
		storage.NewAccountCredentialRepo(db), storage.NewReauthRepo(db),
		testKeyring(t), newOAuthTransactionID, nil,
	)
	return NewOAuthHandler(svc, reg, txRepo, accountRepo, testAllowedHost, newAuditEmitter(db, nil)), db
}

// newTestOAuthMux wires h's routes on a bare http.ServeMux — the same
// route patterns ControlMux registers (including the provider-agnostic
// GET /callback redirect target) — WITHOUT any gate, so these tests
// exercise OAuthHandler's own behavior against a real fake adapter in
// isolation from the (separately tested, see oauth_gating_test.go)
// networkGate/ownerSessionGate wiring.
func newTestOAuthMux(h *OAuthHandler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/control/v1/providers/{id}/oauth/begin", h.ServeBegin)
	mux.HandleFunc("/api/control/v1/accounts/{id}/reauth/begin", h.ServeReauthBegin)
	mux.HandleFunc("/callback", h.ServeCallback)
	mux.HandleFunc("/api/control/v1/oauth/{provider}/callback", h.ServeCallback)
	mux.HandleFunc("/api/control/v1/oauth/{transaction_id}/status", h.ServeStatus)
	mux.HandleFunc("/api/control/v1/oauth/complete", h.ServeCompleteCode)
	return mux
}

type beginOAuthResponse struct {
	Data struct {
		TransactionID string `json:"transaction_id"`
		AuthorizeURL  string `json:"authorize_url"`
		ExpiresAt     string `json:"expires_at"`
	} `json:"data"`
}

type statusOAuthResponse struct {
	Data struct {
		Status    string `json:"status"`
		AccountID string `json:"account_id"`
		Error     string `json:"error"`
	} `json:"data"`
}

// TestOAuthHandler_FullFlow_BeginCallbackStatus proves the whole
// begin -> provider redirect -> callback -> status arc works end to end
// against a fake, deterministic adapter: Begin returns a transaction id
// and authorize URL (handing the adapter the REGISTERED
// `http://<host>/callback` redirect_uri — the only shape the claude-code/
// clinepass/antigravity public clients allow, legacy 2026-08-03), the
// provider redirects to the provider-AGNOSTIC /callback (which resolves
// the provider from the `state` via the transaction row), completes the
// enrollment and creates an account, and the status endpoint reports
// "completed" with that account's id.
func TestOAuthHandler_FullFlow_BeginCallbackStatus(t *testing.T) {
	adapter := newFakeHTTPOAuthAdapter()
	h, db := newTestOAuthHandler(t, "fake-oauth", adapter)
	mux := newTestOAuthMux(h)

	beginReq := httptest.NewRequest(http.MethodPost, "/api/control/v1/providers/fake-oauth/oauth/begin", nil)
	beginRec := httptest.NewRecorder()
	mux.ServeHTTP(beginRec, beginReq)
	if beginRec.Code != http.StatusAccepted {
		t.Fatalf("POST begin status = %d, want 202; body = %q", beginRec.Code, beginRec.Body.String())
	}
	var beginResp beginOAuthResponse
	if err := json.Unmarshal(beginRec.Body.Bytes(), &beginResp); err != nil {
		t.Fatalf("decode begin response: %v", err)
	}
	if beginResp.Data.TransactionID == "" || beginResp.Data.AuthorizeURL == "" {
		t.Fatalf("begin response missing transaction_id/authorize_url: %+v", beginResp.Data)
	}
	// The adapter must receive the REGISTERED redirect_uri shape — not a
	// provider-specific path, which claude.ai rejects with "Redirect URI is
	// not supported by client".
	if adapter.lastRedirectURI != "http://"+testAllowedHost+"/callback" {
		t.Fatalf("redirect_uri = %q, want the registered %q shape", adapter.lastRedirectURI, "http://"+testAllowedHost+"/callback")
	}

	// The provider redirects to the provider-agnostic /callback. The handler
	// must resolve the provider from the `state` alone.
	callbackURL := "/callback?code=fake-code&state=" + adapter.lastState
	callbackReq := httptest.NewRequest(http.MethodGet, callbackURL, nil)
	callbackRec := httptest.NewRecorder()
	mux.ServeHTTP(callbackRec, callbackReq)
	if callbackRec.Code != http.StatusOK {
		t.Fatalf("GET callback status = %d, want 200; body = %q", callbackRec.Code, callbackRec.Body.String())
	}
	if !strings.Contains(callbackRec.Body.String(), "Connected") {
		t.Fatalf("callback page = %q, want a success message", callbackRec.Body.String())
	}
	assertCountHTTP(t, db, "accounts", 1)

	statusReq := httptest.NewRequest(http.MethodGet, "/api/control/v1/oauth/"+beginResp.Data.TransactionID+"/status", nil)
	statusRec := httptest.NewRecorder()
	mux.ServeHTTP(statusRec, statusReq)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200; body = %q", statusRec.Code, statusRec.Body.String())
	}
	var statusResp statusOAuthResponse
	if err := json.Unmarshal(statusRec.Body.Bytes(), &statusResp); err != nil {
		t.Fatalf("decode status response: %v", err)
	}
	if statusResp.Data.Status != "completed" {
		t.Fatalf("status = %q, want completed", statusResp.Data.Status)
	}
	if statusResp.Data.AccountID == "" {
		t.Fatalf("status response missing account_id on completion")
	}
}

// TestOAuthHandler_Begin_UnknownProviderNotFound proves an id with no
// registered OAuth adapter is rejected before any transaction is created.
func TestOAuthHandler_Begin_UnknownProviderNotFound(t *testing.T) {
	h, db := newTestOAuthHandler(t, "fake-oauth", newFakeHTTPOAuthAdapter())
	mux := newTestOAuthMux(h)

	req := httptest.NewRequest(http.MethodPost, "/api/control/v1/providers/does-not-exist/oauth/begin", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	assertCountHTTP(t, db, "oauth_transactions", 0)
}

// TestOAuthHandler_Callback_UnknownStateReturnsFailurePageNoAccount
// proves a callback with a state that matches no transaction renders a
// generic failure page and creates no account.
func TestOAuthHandler_Callback_UnknownStateReturnsFailurePageNoAccount(t *testing.T) {
	adapter := newFakeHTTPOAuthAdapter()
	h, db := newTestOAuthHandler(t, "fake-oauth", adapter)
	mux := newTestOAuthMux(h)

	req := httptest.NewRequest(http.MethodGet, "/api/control/v1/oauth/fake-oauth/callback?code=any&state=never-began", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (a generic failure page, not an error status)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "failed") {
		t.Fatalf("callback page = %q, want a failure message", rec.Body.String())
	}
	if adapter.completeCalls != 0 {
		t.Fatalf("adapter.CompleteOAuth called %d times, want 0 for an unknown state", adapter.completeCalls)
	}
	assertCountHTTP(t, db, "accounts", 0)
}

// TestOAuthHandler_Callback_AgnosticUnknownStateFailsClosed proves the
// provider-agnostic /callback route (the registered redirect target) fails
// closed on a state that names no transaction: generic failure page, no
// account, and the adapter is never called.
func TestOAuthHandler_Callback_AgnosticUnknownStateFailsClosed(t *testing.T) {
	adapter := newFakeHTTPOAuthAdapter()
	h, db := newTestOAuthHandler(t, "fake-oauth", adapter)
	mux := newTestOAuthMux(h)

	req := httptest.NewRequest(http.MethodGet, "/callback?code=any&state=never-began", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (a generic failure page, not an error status)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "failed") {
		t.Fatalf("callback page = %q, want a failure message", rec.Body.String())
	}
	if adapter.completeCalls != 0 {
		t.Fatalf("adapter.CompleteOAuth called %d times, want 0 for an unknown state", adapter.completeCalls)
	}
	assertCountHTTP(t, db, "accounts", 0)
}

// TestOAuthHandler_CompleteCode_PasteFlow proves the manual-code completion
// leg (POST /oauth/complete) for providers whose client never redirects back
// (providers.RequiresManualCode — claude-code): begin -> the owner pastes the
// code the hosted page displayed -> complete exchanges it and creates the
// account. The transaction is resolved by id, the provider from the
// transaction row (never from the client), and the response mirrors the
// status payload.
func TestOAuthHandler_CompleteCode_PasteFlow(t *testing.T) {
	adapter := newFakeHTTPOAuthAdapter()
	h, db := newTestOAuthHandler(t, "fake-oauth", adapter)
	mux := newTestOAuthMux(h)

	beginRec := httptest.NewRecorder()
	mux.ServeHTTP(beginRec, httptest.NewRequest(http.MethodPost, "/api/control/v1/providers/fake-oauth/oauth/begin", nil))
	if beginRec.Code != http.StatusAccepted {
		t.Fatalf("begin status = %d, want 202; body = %q", beginRec.Code, beginRec.Body.String())
	}
	var beginResp beginOAuthResponse
	if err := json.Unmarshal(beginRec.Body.Bytes(), &beginResp); err != nil {
		t.Fatalf("decode begin: %v", err)
	}

	body := strings.NewReader(`{"transaction_id":"` + beginResp.Data.TransactionID + `","code":"pasted-code#fragment"}`)
	completeReq := httptest.NewRequest(http.MethodPost, "/api/control/v1/oauth/complete", body)
	completeRec := httptest.NewRecorder()
	mux.ServeHTTP(completeRec, completeReq)
	if completeRec.Code != http.StatusOK {
		t.Fatalf("complete status = %d, want 200; body = %q", completeRec.Code, completeRec.Body.String())
	}
	var completeResp statusOAuthResponse
	if err := json.Unmarshal(completeRec.Body.Bytes(), &completeResp); err != nil {
		t.Fatalf("decode complete: %v", err)
	}
	if completeResp.Data.Status != "completed" || completeResp.Data.AccountID == "" {
		t.Fatalf("complete = %+v, want completed + account_id", completeResp.Data)
	}
	assertCountHTTP(t, db, "accounts", 1)
	// The raw pasted string (with fragment) must reach the adapter, which
	// strips the fragment per its own contract.
	if adapter.lastCode != "pasted-code#fragment" {
		t.Fatalf("adapter code = %q, want the raw pasted code with fragment preserved", adapter.lastCode)
	}
}

// TestOAuthHandler_CompleteCode_UnknownTransaction404 proves the manual-code
// endpoint fails closed on a transaction id it has never seen.
func TestOAuthHandler_CompleteCode_UnknownTransaction404(t *testing.T) {
	adapter := newFakeHTTPOAuthAdapter()
	h, _ := newTestOAuthHandler(t, "fake-oauth", adapter)
	mux := newTestOAuthMux(h)

	body := strings.NewReader(`{"transaction_id":"does-not-exist","code":"whatever"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/control/v1/oauth/complete", body)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %q", rec.Code, rec.Body.String())
	}
	if adapter.completeCalls != 0 {
		t.Fatalf("adapter.CompleteOAuth called %d times, want 0", adapter.completeCalls)
	}
}

// TestOAuthHandler_Status_UnknownTransactionID404 proves the status
// endpoint fails closed (404) for a transaction id it has never seen,
// rather than guessing a default status.
func TestOAuthHandler_Status_UnknownTransactionID404(t *testing.T) {
	h, _ := newTestOAuthHandler(t, "fake-oauth", newFakeHTTPOAuthAdapter())
	mux := newTestOAuthMux(h)

	req := httptest.NewRequest(http.MethodGet, "/api/control/v1/oauth/does-not-exist/status", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %q", rec.Code, rec.Body.String())
	}
}

// TestOAuthHandler_Status_PendingBeforeCallback proves an unfinished
// transaction reports "pending" (derived straight from the DB row —
// consumed=0, not yet expired) when polled before any callback arrives.
func TestOAuthHandler_Status_PendingBeforeCallback(t *testing.T) {
	h, _ := newTestOAuthHandler(t, "fake-oauth", newFakeHTTPOAuthAdapter())
	mux := newTestOAuthMux(h)

	beginRec := httptest.NewRecorder()
	mux.ServeHTTP(beginRec, httptest.NewRequest(http.MethodPost, "/api/control/v1/providers/fake-oauth/oauth/begin", nil))
	var beginResp beginOAuthResponse
	if err := json.Unmarshal(beginRec.Body.Bytes(), &beginResp); err != nil {
		t.Fatalf("decode begin response: %v", err)
	}

	statusRec := httptest.NewRecorder()
	mux.ServeHTTP(statusRec, httptest.NewRequest(http.MethodGet, "/api/control/v1/oauth/"+beginResp.Data.TransactionID+"/status", nil))
	var statusResp statusOAuthResponse
	if err := json.Unmarshal(statusRec.Body.Bytes(), &statusResp); err != nil {
		t.Fatalf("decode status response: %v", err)
	}
	if statusResp.Data.Status != "pending" {
		t.Fatalf("status = %q, want pending", statusResp.Data.Status)
	}
}

func assertCountHTTP(t *testing.T, db *storage.DB, table string, want int) {
	t.Helper()
	var got int
	if err := db.Conn().QueryRow("SELECT COUNT(*) FROM " + table).Scan(&got); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if got != want {
		t.Fatalf("row count in %s = %d, want %d", table, got, want)
	}
}
