package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
)

// reauthBeginResponse mirrors beginOAuthResponse's shape — ServeReauthBegin
// returns the identical beginOAuthJSON payload.
type reauthBeginResponse = beginOAuthResponse

// TestOAuthHandler_ReauthBegin_UnknownAccount404 proves ServeReauthBegin
// fails closed for an account id that does not exist — no transaction
// is created and nothing is bound in the reauth cache.
func TestOAuthHandler_ReauthBegin_UnknownAccount404(t *testing.T) {
	h, db := newTestOAuthHandler(t, "fake-oauth", newFakeHTTPOAuthAdapter())
	mux := newTestOAuthMux(h)

	req := httptest.NewRequest(http.MethodPost, "/api/control/v1/accounts/does-not-exist/reauth/begin", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %q", rec.Code, rec.Body.String())
	}
	assertCountHTTP(t, db, "oauth_transactions", 0)
}

// TestOAuthHandler_ReauthBegin_KnownAccountReturns202WithTransaction
// proves the happy path: a known account whose provider has a
// registered OAuth adapter gets a 202 with a transaction_id + authorize
// URL, exactly like the regular /oauth/begin flow.
func TestOAuthHandler_ReauthBegin_KnownAccountReturns202WithTransaction(t *testing.T) {
	h, db := newTestOAuthHandler(t, "fake-oauth", newFakeHTTPOAuthAdapter())
	mux := newTestOAuthMux(h)

	if _, err := db.Conn().Exec(
		`INSERT INTO accounts (id, provider_id, external_id, auth_type, created_at, updated_at) VALUES (?, ?, ?, 'oauth2', 0, 0)`,
		"acct-1", "fake-oauth", "ext-1",
	); err != nil {
		t.Fatalf("seed account: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/control/v1/accounts/acct-1/reauth/begin", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body = %q", rec.Code, rec.Body.String())
	}
	var resp reauthBeginResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Data.TransactionID == "" || resp.Data.AuthorizeURL == "" {
		t.Fatalf("reauth begin response missing transaction_id/authorize_url: %+v", resp.Data)
	}
}

// TestOAuthHandler_ReauthCallback_IdentityMismatchLeavesAccountUntouched
// proves the full wire-level flow (ServeReauthBegin -> ServeCallback):
// starting a reauth for a known account, then completing the callback
// with an adapter whose identity does NOT match that account, must NOT
// create/alter any account_credentials row for it — the
// account_identity_mismatch guard applies before anything is staged.
func TestOAuthHandler_ReauthCallback_IdentityMismatchLeavesAccountUntouched(t *testing.T) {
	adapter := newFakeHTTPOAuthAdapter() // identity.ExternalID == "http-ext-1"
	h, db := newTestOAuthHandler(t, "fake-oauth", adapter)
	mux := newTestOAuthMux(h)

	if _, err := db.Conn().Exec(
		`INSERT INTO accounts (id, provider_id, external_id, auth_type, created_at, updated_at) VALUES (?, ?, ?, 'oauth2', 0, 0)`,
		"acct-1", "fake-oauth", "some-other-external-id",
	); err != nil {
		t.Fatalf("seed account: %v", err)
	}

	beginRec := httptest.NewRecorder()
	mux.ServeHTTP(beginRec, httptest.NewRequest(http.MethodPost, "/api/control/v1/accounts/acct-1/reauth/begin", nil))
	if beginRec.Code != http.StatusAccepted {
		t.Fatalf("reauth begin status = %d, want 202; body = %q", beginRec.Code, beginRec.Body.String())
	}

	callbackReq := httptest.NewRequest(http.MethodGet, "/api/control/v1/oauth/fake-oauth/callback?code=any&state="+adapter.lastState, nil)
	callbackRec := httptest.NewRecorder()
	mux.ServeHTTP(callbackRec, callbackReq)

	// The callback page itself always renders 200 (success or failure —
	// see renderOAuthCallbackPage); the proof is in the DB state, not
	// the HTTP status of this particular response.
	if callbackRec.Code != http.StatusOK {
		t.Fatalf("callback status = %d, want 200 (a rendered page either way)", callbackRec.Code)
	}
	assertCountHTTP(t, db, "account_credentials", 0)

	var reauthInProgress int
	if err := db.Conn().QueryRow(`SELECT reauth_in_progress FROM accounts WHERE id = ?`, "acct-1").Scan(&reauthInProgress); err != nil {
		t.Fatalf("query account: %v", err)
	}
	if reauthInProgress != 0 {
		t.Fatalf("reauth_in_progress = %d, want 0 (nothing staged for a mismatched identity)", reauthInProgress)
	}
}

// TestOAuthHandler_ReauthCallback_MatchingIdentitySwapsCredential proves
// the full happy-path wire-level flow: reauth begin for an account whose
// external_id matches the adapter's identity results in exactly one
// active oauth2 credential after the callback completes.
func TestOAuthHandler_ReauthCallback_MatchingIdentitySwapsCredential(t *testing.T) {
	adapter := newFakeHTTPOAuthAdapter() // identity.ExternalID == "http-ext-1"
	h, db := newTestOAuthHandler(t, "fake-oauth", adapter)
	mux := newTestOAuthMux(h)

	if _, err := db.Conn().Exec(
		`INSERT INTO accounts (id, provider_id, external_id, auth_type, created_at, updated_at) VALUES (?, ?, ?, 'oauth2', 0, 0)`,
		"acct-1", "fake-oauth", "http-ext-1",
	); err != nil {
		t.Fatalf("seed account: %v", err)
	}

	beginRec := httptest.NewRecorder()
	mux.ServeHTTP(beginRec, httptest.NewRequest(http.MethodPost, "/api/control/v1/accounts/acct-1/reauth/begin", nil))
	if beginRec.Code != http.StatusAccepted {
		t.Fatalf("reauth begin status = %d, want 202; body = %q", beginRec.Code, beginRec.Body.String())
	}

	callbackReq := httptest.NewRequest(http.MethodGet, "/api/control/v1/oauth/fake-oauth/callback?code=any&state="+adapter.lastState, nil)
	callbackRec := httptest.NewRecorder()
	mux.ServeHTTP(callbackRec, callbackReq)
	if callbackRec.Code != http.StatusOK {
		t.Fatalf("callback status = %d, want 200; body = %q", callbackRec.Code, callbackRec.Body.String())
	}

	var activeCount int
	if err := db.Conn().QueryRow(
		`SELECT COUNT(*) FROM account_credentials WHERE account_id = 'acct-1' AND kind = 'oauth2' AND state = 'active'`,
	).Scan(&activeCount); err != nil {
		t.Fatalf("count active credentials: %v", err)
	}
	if activeCount != 1 {
		t.Fatalf("active oauth2 credentials = %d, want exactly 1", activeCount)
	}
}

var _ providers.OAuthAdapter = (*fakeHTTPOAuthAdapter)(nil)
