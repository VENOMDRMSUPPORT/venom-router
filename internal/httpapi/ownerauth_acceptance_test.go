package httpapi

// ownerauth_acceptance_test.go is P2b-TEST-001: a single cohesive
// acceptance suite that mechanizes 09 §5.9's "Testable acceptance
// criteria" end-to-end through the REAL composed ControlMux — not
// per-handler. Every criterion below is already covered by a narrower
// unit test elsewhere in this package (auth_test.go, authsession_test.go,
// oauth_gating_test.go, csrf_test.go, reverify_test.go, lockout_test.go,
// sessionlifecycle_test.go, ownersessiongate_test.go, accounts_test.go);
// this suite still re-proves each one driving the whole gated mux, which
// is the added value those narrower tests don't give: a regression in
// how ControlMux WIRES the pieces together (not just in one piece's own
// logic) would only show up here.
//
// Assembly: each test builds its own `db := testControlDB(t); mux :=
// ControlMux(testAllowedHost, fakeSPA(), db, testKeyring(t))` once, per
// the assignment's exact recipe.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/application"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/secrets"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// bytesBuffer wraps b as the *bytes.Buffer newAuthRequest expects — a
// tiny local adapter so this file's PUT-body construction can stay
// inline rather than threading a *bytes.Buffer through every call site.
func bytesBuffer(b []byte) *bytes.Buffer {
	return bytes.NewBuffer(b)
}

// acceptanceTimestampLayout mirrors internal/storage's own private
// timestampLayout (2006-01-02T15:04:05.000Z, the ISO-8601-with-
// milliseconds format every owner_sessions/owner_auth/auth_events
// timestamp is stored in — see 00002_owner_auth.sql's
// strftime('%Y-%m-%dT%H:%M:%fZ','now') default). storage.formatTimestamp
// is unexported, so this suite (package httpapi, driving the DB only via
// direct SQL for back-dating) reimplements the identical format rather
// than reaching into internal/storage's internals.
const acceptanceTimestampLayout = "2006-01-02T15:04:05.000Z"

func acceptanceTimestamp(t time.Time) string {
	return t.UTC().Format(acceptanceTimestampLayout)
}

// --- 1. Setup-once ---

// TestAcceptance_Setup_OnceThenSecondRejected is 09 §5.9's first bullet:
// "First run with no owner_auth requires setup; a second setup is
// rejected." (also covered narrowly by TestAuthSetup_SecondAttemptRejected
// in auth_test.go — reproved here through the full mux for completeness).
func TestAcceptance_Setup_OnceThenSecondRejected(t *testing.T) {
	db := testControlDB(t)
	mux := ControlMux(testAllowedHost, fakeSPA(), db, testKeyring(t))

	first := httptest.NewRecorder()
	mux.ServeHTTP(first, newAuthRequest(t, http.MethodPost, "/api/control/v1/auth/setup", setupRequestBody(testSetupPassword)))
	if first.Code != http.StatusOK {
		t.Fatalf("first setup status = %d, want 200; body = %q", first.Code, first.Body.String())
	}

	second := httptest.NewRecorder()
	mux.ServeHTTP(second, newAuthRequest(t, http.MethodPost, "/api/control/v1/auth/setup", setupRequestBody("a-different-long-enough-password")))
	if second.Code != http.StatusConflict {
		t.Fatalf("second setup status = %d, want 409; body = %q", second.Code, second.Body.String())
	}
	if code := decodeErrorCode(t, second.Body.Bytes()); code != "setup_already_complete" {
		t.Fatalf("error code = %q, want setup_already_complete", code)
	}
}

// --- 2. Generic invalid_credentials, no setup-state leak ---

// TestAcceptance_Login_GenericInvalidCredentials_NoSetupStateLeak is 09
// §5.9's second bullet: wrong password (after setup) and login before any
// setup exists both fail with the exact same 401 invalid_credentials —
// proving there is no way to distinguish "wrong password" from "not set
// up yet" from the response (also covered narrowly by
// TestAuthLogin_NoOwnerRow_SameGenericResponseAsWrongPassword in
// authsession_test.go).
func TestAcceptance_Login_GenericInvalidCredentials_NoSetupStateLeak(t *testing.T) {
	t.Run("wrong password after setup", func(t *testing.T) {
		db := testControlDB(t)
		mux := ControlMux(testAllowedHost, fakeSPA(), db, testKeyring(t))
		setupOwner(t, mux, testSetupPassword)

		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, newAuthRequest(t, http.MethodPost, "/api/control/v1/auth/login", setupRequestBody("wrong-password-entirely")))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401; body = %q", rec.Code, rec.Body.String())
		}
		if code := decodeErrorCode(t, rec.Body.Bytes()); code != "invalid_credentials" {
			t.Fatalf("error code = %q, want invalid_credentials", code)
		}
	})

	t.Run("login before any setup", func(t *testing.T) {
		mux := ControlMux(testAllowedHost, fakeSPA(), testControlDB(t), testKeyring(t))

		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, newAuthRequest(t, http.MethodPost, "/api/control/v1/auth/login", setupRequestBody("whatever-password")))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401; body = %q", rec.Code, rec.Body.String())
		}
		if code := decodeErrorCode(t, rec.Body.Bytes()); code != "invalid_credentials" {
			t.Fatalf("error code = %q, want invalid_credentials", code)
		}
	})

	// The identical-response proof: run both cases side by side and
	// diff status/code/body shape directly, rather than trusting two
	// separately-asserted "invalid_credentials" strings are really the
	// same shape.
	t.Run("identical response shape", func(t *testing.T) {
		wrongPwDB := testControlDB(t)
		wrongPwMux := ControlMux(testAllowedHost, fakeSPA(), wrongPwDB, testKeyring(t))
		setupOwner(t, wrongPwMux, testSetupPassword)
		wrongPwRec := httptest.NewRecorder()
		wrongPwMux.ServeHTTP(wrongPwRec, newAuthRequest(t, http.MethodPost, "/api/control/v1/auth/login", setupRequestBody("whatever-password")))

		noOwnerMux := ControlMux(testAllowedHost, fakeSPA(), testControlDB(t), testKeyring(t))
		noOwnerRec := httptest.NewRecorder()
		noOwnerMux.ServeHTTP(noOwnerRec, newAuthRequest(t, http.MethodPost, "/api/control/v1/auth/login", setupRequestBody("whatever-password")))

		if wrongPwRec.Code != noOwnerRec.Code {
			t.Fatalf("status differs: wrong-password=%d no-owner=%d, want identical", wrongPwRec.Code, noOwnerRec.Code)
		}

		// Compare shape field-by-field rather than raw bytes: request_id
		// is a fresh random hex value per response by design (newRequestID)
		// and would never match byte-for-byte even for two truly identical
		// responses, so it is excluded — every OTHER field must match
		// exactly, including retryable and the full message text.
		var wrongPwBody, noOwnerBody struct {
			Error struct {
				Code      string `json:"code"`
				Message   string `json:"message"`
				Retryable bool   `json:"retryable"`
			} `json:"error"`
		}
		if err := json.Unmarshal(wrongPwRec.Body.Bytes(), &wrongPwBody); err != nil {
			t.Fatalf("decode wrong-password body: %v", err)
		}
		if err := json.Unmarshal(noOwnerRec.Body.Bytes(), &noOwnerBody); err != nil {
			t.Fatalf("decode no-owner body: %v", err)
		}
		if wrongPwBody != noOwnerBody {
			t.Fatalf("error shape differs: wrong-password=%+v no-owner=%+v, want identical (modulo request_id)", wrongPwBody, noOwnerBody)
		}

		// Neither response set a session cookie either.
		for _, c := range wrongPwRec.Result().Cookies() {
			if c.Name == sessionCookieName {
				t.Fatalf("wrong-password login response unexpectedly set a session cookie")
			}
		}
		for _, c := range noOwnerRec.Result().Cookies() {
			if c.Name == sessionCookieName {
				t.Fatalf("no-owner login response unexpectedly set a session cookie")
			}
		}
	})
}

// --- 3. Idle (30m) + absolute (12h) expiry, revocation as a side effect ---

// acceptanceLoginSession logs in against mux (owner already set up) and
// returns the resulting session cookie plus its token_hash, for tests
// that need to back-date the persisted row directly via SQL.
func acceptanceLoginSession(t *testing.T, mux http.Handler) (*http.Cookie, []byte) {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, newAuthRequest(t, http.MethodPost, "/api/control/v1/auth/login", setupRequestBody(testSetupPassword)))
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200; body = %q", rec.Code, rec.Body.String())
	}
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatalf("login succeeded but set no %s cookie", sessionCookieName)
	}
	return cookie, secrets.HashSessionHandle(cookie.Value)
}

// gatedGETAccounts issues an authenticated GET /accounts through mux — an
// arbitrary "any gated request" per the assignment, used purely to prove
// the session gate rejects/accepts, never to assert anything about
// accounts themselves.
func gatedGETAccounts(mux http.Handler, cookie *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/control/v1/accounts", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Host = testAllowedHost
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// TestAcceptance_SessionExpiry_Idle_RejectsAndRevokes back-dates ONLY
// idle_expires_at into the past (absolute_expires_at stays in the
// future) via direct SQL against the real owner_sessions row, then
// proves a gated request rejects with session_expired AND the row is
// now revoked. No sleeping and no clock injected through ControlMux —
// back-dating the persisted expiry is the intended seam (ControlMux
// exposes no clock hook).
func TestAcceptance_SessionExpiry_Idle_RejectsAndRevokes(t *testing.T) {
	db := testControlDB(t)
	mux := ControlMux(testAllowedHost, fakeSPA(), db, testKeyring(t))
	setupOwner(t, mux, testSetupPassword)
	cookie, tokenHash := acceptanceLoginSession(t, mux)

	past := acceptanceTimestamp(time.Now().Add(-1 * time.Hour))
	if _, err := db.Conn().Exec(
		`UPDATE owner_sessions SET idle_expires_at = ? WHERE token_hash = ?`, past, tokenHash,
	); err != nil {
		t.Fatalf("back-date idle_expires_at: %v", err)
	}

	rec := gatedGETAccounts(mux, cookie)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for an idle-expired session; body = %q", rec.Code, rec.Body.String())
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != "session_expired" {
		t.Fatalf("error code = %q, want session_expired", code)
	}

	var revokedAt *string
	if err := db.Conn().QueryRow(`SELECT revoked_at FROM owner_sessions WHERE token_hash = ?`, tokenHash).Scan(&revokedAt); err != nil {
		t.Fatalf("read back revoked_at: %v", err)
	}
	if revokedAt == nil {
		t.Fatalf("revoked_at is NULL after idle-expiry rejection, want it set (session must never be resurrected)")
	}
}

// TestAcceptance_SessionExpiry_Absolute_RejectsAndRevokes back-dates ONLY
// absolute_expires_at into the past (idle_expires_at stays in the
// future, proving the absolute cap rejects regardless of idle activity),
// and confirms the row is revoked afterward — a separate case from the
// idle test above, per the assignment.
func TestAcceptance_SessionExpiry_Absolute_RejectsAndRevokes(t *testing.T) {
	db := testControlDB(t)
	mux := ControlMux(testAllowedHost, fakeSPA(), db, testKeyring(t))
	setupOwner(t, mux, testSetupPassword)
	cookie, tokenHash := acceptanceLoginSession(t, mux)

	past := acceptanceTimestamp(time.Now().Add(-1 * time.Hour))
	if _, err := db.Conn().Exec(
		`UPDATE owner_sessions SET absolute_expires_at = ? WHERE token_hash = ?`, past, tokenHash,
	); err != nil {
		t.Fatalf("back-date absolute_expires_at: %v", err)
	}

	rec := gatedGETAccounts(mux, cookie)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for an absolute-expired session; body = %q", rec.Code, rec.Body.String())
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != "session_expired" {
		t.Fatalf("error code = %q, want session_expired", code)
	}

	var revokedAt *string
	if err := db.Conn().QueryRow(`SELECT revoked_at FROM owner_sessions WHERE token_hash = ?`, tokenHash).Scan(&revokedAt); err != nil {
		t.Fatalf("read back revoked_at: %v", err)
	}
	if revokedAt == nil {
		t.Fatalf("revoked_at is NULL after absolute-expiry rejection, want it set")
	}
}

// --- 4. CSRF rejected before side effect (+ forged/cross-session) ---

func getSettingsTheme(t *testing.T, mux http.Handler, cookie *http.Cookie) string {
	t.Helper()
	req := newAuthRequest(t, http.MethodGet, "/api/control/v1/settings", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /settings status = %d, want 200; body = %q", rec.Code, rec.Body.String())
	}
	var body struct {
		Data struct {
			Theme string `json:"theme"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode settings response: %v; body = %q", err, rec.Body.String())
	}
	return body.Data.Theme
}

// TestAcceptance_CSRF_RejectedBeforeSideEffect proves an authenticated
// mutating request (PUT /settings) with a valid session cookie but no
// X-CSRF-Token header is rejected 403 csrf_failed, AND the side effect
// did not happen — a follow-up GET /settings still shows the prior
// value, proving no partial mutation.
func TestAcceptance_CSRF_RejectedBeforeSideEffect(t *testing.T) {
	mux := ControlMux(testAllowedHost, fakeSPA(), testControlDB(t), testKeyring(t))
	cookie, _ := setupOwnerWithCSRF(t, mux)

	before := getSettingsTheme(t, mux, cookie)
	if before != storage.DefaultTheme {
		t.Fatalf("theme before mutation = %q, want the fresh-DB default %q", before, storage.DefaultTheme)
	}

	putBody, _ := json.Marshal(map[string]string{"theme": "venom-light", "density": "compact"})
	req := newAuthRequest(t, http.MethodPut, "/api/control/v1/settings", bytesBuffer(putBody))
	req.AddCookie(cookie) // deliberately no X-CSRF-Token
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("PUT /settings without CSRF: status = %d, want 403; body = %q", rec.Code, rec.Body.String())
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != "csrf_failed" {
		t.Fatalf("error code = %q, want csrf_failed", code)
	}

	after := getSettingsTheme(t, mux, cookie)
	if after != before {
		t.Fatalf("theme after rejected mutation = %q, want unchanged %q (no partial mutation)", after, before)
	}
}

// TestAcceptance_CSRF_ForgedCrossSessionTokenRejected proves session A's
// cookie combined with session B's (a DIFFERENT, independently-created
// session for the same single owner) CSRF token is rejected 403 — the
// core CSRF-forgery proof, through the full mux and a real mutating
// route (PUT /settings) rather than a test-local handler.
func TestAcceptance_CSRF_ForgedCrossSessionTokenRejected(t *testing.T) {
	mux := ControlMux(testAllowedHost, fakeSPA(), testControlDB(t), testKeyring(t))
	cookieA, _ := setupOwnerWithCSRF(t, mux)

	loginRec := httptest.NewRecorder()
	mux.ServeHTTP(loginRec, newAuthRequest(t, http.MethodPost, "/api/control/v1/auth/login", setupRequestBody(testSetupPassword)))
	if loginRec.Code != http.StatusOK {
		t.Fatalf("second login status = %d, want 200; body = %q", loginRec.Code, loginRec.Body.String())
	}
	var loginBody struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.Unmarshal(loginRec.Body.Bytes(), &loginBody); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	tokenB := loginBody.CSRFToken
	if tokenB == "" {
		t.Fatalf("session B login did not return a csrf_token")
	}

	putBody, _ := json.Marshal(map[string]string{"theme": "venom-light", "density": "compact"})
	req := newAuthRequest(t, http.MethodPut, "/api/control/v1/settings", bytesBuffer(putBody))
	req.AddCookie(cookieA)                 // session A's cookie...
	req.Header.Set("X-CSRF-Token", tokenB) // ...with session B's token
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for a cross-session CSRF token; body = %q", rec.Code, rec.Body.String())
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != "csrf_failed" {
		t.Fatalf("error code = %q, want csrf_failed", code)
	}
}

// --- 5. Reveal reverify-gate (<=5 min), then success with no-store ---

// seedAcceptanceRevealAccount seeds one provider + connected account +
// one ACTIVE api_key credential directly into db, using the SAME kr
// ControlMux itself was built with, so the real ServeReveal path can
// decrypt it. Mirrors accounts_test.go's newTestAccountsHandlerV2 seeding
// pattern (reused here rather than duplicated, since that helper builds
// its own private AccountsHandler/db/keyring rather than sharing
// ControlMux's).
func seedAcceptanceRevealAccount(t *testing.T, db *storage.DB, kr *secrets.Keyring, now time.Time, canaryKey string) (accountID string) {
	t.Helper()

	if _, err := db.Conn().Exec(
		`INSERT INTO providers (id, display_name, auth_mode, funding_mode, funding_locked, created_at, updated_at) VALUES (?, ?, 'api_key', 'owner_policy', 0, 0, 0)`,
		"acc-prov-a", "acc-prov-a",
	); err != nil {
		t.Fatalf("seed provider: %v", err)
	}

	const accID = "acc-acceptance-1"
	const credID = "cred-acceptance-1"
	if _, err := db.Conn().Exec(
		`INSERT INTO accounts (id, provider_id, external_id, auth_type, connection_state, health_state, identity_email, identity_plan, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		accID, "acc-prov-a", "ext-acceptance-1", "api_key", string(domain.ConnectionConnected), string(domain.HealthHealthy),
		"owner@example.com", "free", now.Unix(), now.Unix(),
	); err != nil {
		t.Fatalf("seed account row: %v", err)
	}

	credRepo := storage.NewAccountCredentialRepo(db)
	credSvc := application.NewCredentialService(credRepo, kr, func() time.Time { return now })
	if _, err := credSvc.Store(context.Background(), application.StoreCredentialParams{
		ID:           credID,
		AccountID:    accID,
		ProviderID:   "acc-prov-a",
		Kind:         domain.CredentialKindAPIKey,
		Active:       true,
		PlaintextKey: canaryKey,
	}); err != nil {
		t.Fatalf("store seed credential: %v", err)
	}

	if _, err := db.Conn().Exec(
		`INSERT INTO account_funding_evidence (id, account_id, funding, source, locked, confidence, observed_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"acc-fund-1", accID, string(domain.FundingFree), string(domain.FundingSourceOwnerPolicy), 0, 1.0, now.Unix(),
	); err != nil {
		t.Fatalf("seed funding row: %v", err)
	}

	return accID
}

// TestAcceptance_Reveal_ReverifyGateRunsBeforeAccountLookup proves the
// reverify-freshness gate runs BEFORE any account lookup: an
// authenticated but not-reverify-fresh POST /accounts/{id}/reveal against
// an arbitrary, never-seeded account id still returns
// reverification_required (not not_found) — needing no seeded account at
// all.
func TestAcceptance_Reveal_ReverifyGateRunsBeforeAccountLookup(t *testing.T) {
	mux := ControlMux(testAllowedHost, fakeSPA(), testControlDB(t), testKeyring(t))
	cookie, csrfToken := setupOwnerWithCSRF(t, mux)

	req := newAuthRequest(t, http.MethodPost, "/api/control/v1/accounts/this-account-does-not-exist/reveal", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrfToken)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 reverification_required (gate must run before account lookup); body = %q", rec.Code, rec.Body.String())
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != "reverification_required" {
		t.Fatalf("error code = %q, want reverification_required (NOT not_found — proves the gate runs first)", code)
	}
}

// TestAcceptance_Reveal_FreshReverifyThenSuccessWithNoStore is the
// positive-path proof: seed a real account + active credential, POST
// /auth/reverify with the owner password to stamp freshness, then POST
// /accounts/{id}/reveal succeeds 200 with Cache-Control: no-store and
// the plaintext body.
func TestAcceptance_Reveal_FreshReverifyThenSuccessWithNoStore(t *testing.T) {
	const canaryKey = "sk-acceptance-canary-reveal-9f3Kx2Qw8pLm0Zt7Vb"

	db := testControlDB(t)
	kr := testKeyring(t)
	mux := ControlMux(testAllowedHost, fakeSPA(), db, kr)
	cookie, csrfToken := setupOwnerWithCSRF(t, mux)

	accountID := seedAcceptanceRevealAccount(t, db, kr, time.Now(), canaryKey)

	reverifyReq := newAuthRequest(t, http.MethodPost, "/api/control/v1/auth/reverify", setupRequestBody(testSetupPassword))
	reverifyReq.AddCookie(cookie)
	reverifyReq.Header.Set("X-CSRF-Token", csrfToken)
	reverifyRec := httptest.NewRecorder()
	mux.ServeHTTP(reverifyRec, reverifyReq)
	if reverifyRec.Code != http.StatusOK {
		t.Fatalf("POST /auth/reverify status = %d, want 200; body = %q", reverifyRec.Code, reverifyRec.Body.String())
	}

	revealReq := newAuthRequest(t, http.MethodPost, "/api/control/v1/accounts/"+accountID+"/reveal", nil)
	revealReq.AddCookie(cookie)
	revealReq.Header.Set("X-CSRF-Token", csrfToken)
	revealRec := httptest.NewRecorder()
	mux.ServeHTTP(revealRec, revealReq)

	if revealRec.Code != http.StatusOK {
		t.Fatalf("POST /accounts/{id}/reveal status = %d, want 200; body = %q", revealRec.Code, revealRec.Body.String())
	}
	if got := revealRec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want %q", got, "no-store")
	}
	if got := revealRec.Body.String(); got != canaryKey {
		t.Fatalf("reveal body = %q, want the plaintext %q verbatim", got, canaryKey)
	}
}

// TestAcceptance_Reveal_ReverifyExpiryReuseRejected back-dates
// reverify_fresh_until into the past via direct SQL (simulating the
// 5-minute window having elapsed since a prior POST /auth/reverify), and
// proves a subsequent reveal is rejected reverification_required again —
// freshness does not persist past its window.
func TestAcceptance_Reveal_ReverifyExpiryReuseRejected(t *testing.T) {
	const canaryKey = "sk-acceptance-canary-reuse-2Qw8pLm0Zt7Vb4Nr1"

	db := testControlDB(t)
	kr := testKeyring(t)
	mux := ControlMux(testAllowedHost, fakeSPA(), db, kr)
	cookie, csrfToken := setupOwnerWithCSRF(t, mux)
	accountID := seedAcceptanceRevealAccount(t, db, kr, time.Now(), canaryKey)

	reverifyReq := newAuthRequest(t, http.MethodPost, "/api/control/v1/auth/reverify", setupRequestBody(testSetupPassword))
	reverifyReq.AddCookie(cookie)
	reverifyReq.Header.Set("X-CSRF-Token", csrfToken)
	reverifyRec := httptest.NewRecorder()
	mux.ServeHTTP(reverifyRec, reverifyReq)
	if reverifyRec.Code != http.StatusOK {
		t.Fatalf("POST /auth/reverify status = %d, want 200; body = %q", reverifyRec.Code, reverifyRec.Body.String())
	}

	tokenHash := secrets.HashSessionHandle(cookie.Value)
	past := acceptanceTimestamp(time.Now().Add(-1 * time.Minute))
	if _, err := db.Conn().Exec(
		`UPDATE owner_sessions SET reverify_fresh_until = ? WHERE token_hash = ?`, past, tokenHash,
	); err != nil {
		t.Fatalf("back-date reverify_fresh_until: %v", err)
	}

	revealReq := newAuthRequest(t, http.MethodPost, "/api/control/v1/accounts/"+accountID+"/reveal", nil)
	revealReq.AddCookie(cookie)
	revealReq.Header.Set("X-CSRF-Token", csrfToken)
	revealRec := httptest.NewRecorder()
	mux.ServeHTTP(revealRec, revealReq)

	if revealRec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 reverification_required for an expired-freshness session; body = %q", revealRec.Code, revealRec.Body.String())
	}
	if code := decodeErrorCode(t, revealRec.Body.Bytes()); code != "reverification_required" {
		t.Fatalf("error code = %q, want reverification_required", code)
	}
	assertNoFragment(t, revealRec.Body.String(), canaryKey, "stale-reverify-reuse reveal response body")
}

// --- 6. Lockout + audit-without-secret canary (+ replay-during-lockout) ---

// TestAcceptance_Lockout_ThresholdThenAuditAndCanary drives 5 failed
// logins through the real ControlMux, confirms the 6th (429 locked_out
// with retry_after present), and confirms every attempt wrote exactly
// one auth_events row whose columns never contain the attempted (wrong)
// password.
func TestAcceptance_Lockout_ThresholdThenAuditAndCanary(t *testing.T) {
	const canaryPassword = "CANARY-ACCEPTANCE-9f3Kx2Qw8pLm0Zt7Vb4Nr1Hy6Dc5Ea"

	db := testControlDB(t)
	mux := ControlMux(testAllowedHost, fakeSPA(), db, testKeyring(t))
	setupOwner(t, mux, testSetupPassword)

	for i := 0; i < lockoutThreshold; i++ {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, newAuthRequest(t, http.MethodPost, "/api/control/v1/auth/login", setupRequestBody(canaryPassword)))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("failure #%d: status = %d, want 401; body = %q", i+1, rec.Code, rec.Body.String())
		}
	}

	sixth := httptest.NewRecorder()
	mux.ServeHTTP(sixth, newAuthRequest(t, http.MethodPost, "/api/control/v1/auth/login", setupRequestBody(testSetupPassword)))
	if sixth.Code != http.StatusTooManyRequests {
		t.Fatalf("6th attempt: status = %d, want 429; body = %q", sixth.Code, sixth.Body.String())
	}
	if code := decodeErrorCode(t, sixth.Body.Bytes()); code != "locked_out" {
		t.Fatalf("error code = %q, want locked_out", code)
	}
	var sixthBody struct {
		Error struct {
			RetryAfter int64 `json:"retry_after"`
		} `json:"error"`
	}
	if err := json.Unmarshal(sixth.Body.Bytes(), &sixthBody); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if sixthBody.Error.RetryAfter <= 0 {
		t.Fatalf("retry_after = %d, want positive", sixthBody.Error.RetryAfter)
	}

	// Every one of the 6 attempts wrote exactly one auth_events row: 5
	// failures + 1 locked_out (the 6th attempt is itself recorded as a
	// failure with reason_code=locked_out — see recordAuthEvent's call
	// sites — so 6 rows total, never fewer, never doubled).
	rows, err := db.Conn().QueryContext(context.Background(), `SELECT action, result, reason_code FROM auth_events ORDER BY id`)
	if err != nil {
		t.Fatalf("query auth_events: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var count int
	for rows.Next() {
		var action, result string
		var reasonCode *string
		if err := rows.Scan(&action, &result, &reasonCode); err != nil {
			t.Fatalf("scan: %v", err)
		}
		count++
		assertNoFragment(t, action, canaryPassword, "auth_events.action")
		assertNoFragment(t, result, canaryPassword, "auth_events.result")
		if reasonCode != nil {
			assertNoFragment(t, *reasonCode, canaryPassword, "auth_events.reason_code")
		}
	}
	if count != lockoutThreshold+1 {
		t.Fatalf("auth_events row count = %d, want %d (one per attempt)", count, lockoutThreshold+1)
	}
}

// TestAcceptance_Lockout_ReplayDuringLockoutDoesNotExtendWindow proves a
// further attempt while already locked out stays 429 AND does not push
// the lockout window further out — the replay's retry_after must never
// exceed the 6th attempt's (it may only shrink, as real wall-clock time
// elapses between the two requests; extension is what this asserts
// against).
func TestAcceptance_Lockout_ReplayDuringLockoutDoesNotExtendWindow(t *testing.T) {
	db := testControlDB(t)
	mux := ControlMux(testAllowedHost, fakeSPA(), db, testKeyring(t))
	setupOwner(t, mux, testSetupPassword)

	for i := 0; i < lockoutThreshold; i++ {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, newAuthRequest(t, http.MethodPost, "/api/control/v1/auth/login", setupRequestBody("the-wrong-password")))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("failure #%d: status = %d, want 401", i+1, rec.Code)
		}
	}

	sixth := httptest.NewRecorder()
	mux.ServeHTTP(sixth, newAuthRequest(t, http.MethodPost, "/api/control/v1/auth/login", setupRequestBody(testSetupPassword)))
	if sixth.Code != http.StatusTooManyRequests {
		t.Fatalf("6th attempt: status = %d, want 429", sixth.Code)
	}
	var sixthBody struct {
		Error struct {
			RetryAfter int64 `json:"retry_after"`
		} `json:"error"`
	}
	if err := json.Unmarshal(sixth.Body.Bytes(), &sixthBody); err != nil {
		t.Fatalf("decode 6th body: %v", err)
	}

	// A 7th attempt (the replay) while still locked out.
	seventh := httptest.NewRecorder()
	mux.ServeHTTP(seventh, newAuthRequest(t, http.MethodPost, "/api/control/v1/auth/login", setupRequestBody(testSetupPassword)))
	if seventh.Code != http.StatusTooManyRequests {
		t.Fatalf("replay during lockout: status = %d, want 429", seventh.Code)
	}
	if code := decodeErrorCode(t, seventh.Body.Bytes()); code != "locked_out" {
		t.Fatalf("replay error code = %q, want locked_out", code)
	}
	var seventhBody struct {
		Error struct {
			RetryAfter int64 `json:"retry_after"`
		} `json:"error"`
	}
	if err := json.Unmarshal(seventh.Body.Bytes(), &seventhBody); err != nil {
		t.Fatalf("decode 7th body: %v", err)
	}

	if seventhBody.Error.RetryAfter > sixthBody.Error.RetryAfter {
		t.Fatalf("replay retry_after (%d) > original lockout retry_after (%d): the replay EXTENDED the lockout window, want it never to grow", seventhBody.Error.RetryAfter, sixthBody.Error.RetryAfter)
	}
}

// --- 7. Password-change revokes all sessions (no HTTP endpoint exists) ---

// TestAcceptance_PasswordChangeRevokesAllSessions_StorageSeam proves 09
// §5.9's "changing the password revokes all existing sessions" guarantee
// at the one seam that actually exists in P2b: there is NO password-
// change HTTP endpoint yet (confirmed by reading 09-control-api.md §2's
// endpoint catalog and internal/httpapi/controlmux.go's route table —
// neither lists one) — internal/storage/owner_sessions.go's RevokeAll
// doc comment says so explicitly: "the password-change endpoint itself
// is a later unit; this is the storage primitive it will call." This
// test creates two live sessions (two logins), calls RevokeAll directly
// (the exact primitive a future password-change endpoint will invoke),
// and asserts BOTH are now rejected session_expired through the real
// gated mux — proving the guarantee holds at the layer that exists,
// without inventing an endpoint that doesn't.
func TestAcceptance_PasswordChangeRevokesAllSessions_StorageSeam(t *testing.T) {
	db := testControlDB(t)
	mux := ControlMux(testAllowedHost, fakeSPA(), db, testKeyring(t))

	cookieA := setupOwner(t, mux, testSetupPassword)
	cookieB, _ := acceptanceLoginSession(t, mux)

	// Both sessions work before the "password change".
	if rec := gatedGETAccounts(mux, cookieA); rec.Code != http.StatusOK {
		t.Fatalf("session A before RevokeAll: status = %d, want 200; body = %q", rec.Code, rec.Body.String())
	}
	if rec := gatedGETAccounts(mux, cookieB); rec.Code != http.StatusOK {
		t.Fatalf("session B before RevokeAll: status = %d, want 200; body = %q", rec.Code, rec.Body.String())
	}

	if err := storage.NewOwnerSessionRepo(db).RevokeAll(context.Background(), time.Now()); err != nil {
		t.Fatalf("RevokeAll: %v", err)
	}

	for name, cookie := range map[string]*http.Cookie{"A": cookieA, "B": cookieB} {
		rec := gatedGETAccounts(mux, cookie)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("session %s after RevokeAll: status = %d, want 401; body = %q", name, rec.Code, rec.Body.String())
		}
		if code := decodeErrorCode(t, rec.Body.Bytes()); code != "session_expired" {
			t.Fatalf("session %s error code = %q, want session_expired", name, code)
		}
	}
}

// --- 8. Negative matrix (each its own subtest, several share setup code
// with a criterion proved above but are named and asserted independently
// here) ---

func TestAcceptance_NegativeMatrix(t *testing.T) {
	t.Run("expired/revoked cookie", func(t *testing.T) {
		db := testControlDB(t)
		mux := ControlMux(testAllowedHost, fakeSPA(), db, testKeyring(t))
		cookie := setupOwner(t, mux, testSetupPassword)

		logoutReq := newAuthRequest(t, http.MethodPost, "/api/control/v1/auth/logout", nil)
		logoutReq.AddCookie(cookie)
		logoutRec := httptest.NewRecorder()
		mux.ServeHTTP(logoutRec, logoutReq)
		if logoutRec.Code != http.StatusOK {
			t.Fatalf("logout status = %d, want 200", logoutRec.Code)
		}

		rec := gatedGETAccounts(mux, cookie)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401 for a revoked cookie; body = %q", rec.Code, rec.Body.String())
		}
	})

	t.Run("forged/cross-session CSRF", func(t *testing.T) {
		mux := ControlMux(testAllowedHost, fakeSPA(), testControlDB(t), testKeyring(t))
		cookieA, _ := setupOwnerWithCSRF(t, mux)

		loginRec := httptest.NewRecorder()
		mux.ServeHTTP(loginRec, newAuthRequest(t, http.MethodPost, "/api/control/v1/auth/login", setupRequestBody(testSetupPassword)))
		var loginBody struct {
			CSRFToken string `json:"csrf_token"`
		}
		if err := json.Unmarshal(loginRec.Body.Bytes(), &loginBody); err != nil {
			t.Fatalf("decode: %v", err)
		}

		req := newAuthRequest(t, http.MethodPut, "/api/control/v1/settings", bytesBuffer(mustJSON(t, map[string]string{"theme": "venom-light", "density": "compact"})))
		req.AddCookie(cookieA)
		req.Header.Set("X-CSRF-Token", loginBody.CSRFToken)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 for a cross-session CSRF token; body = %q", rec.Code, rec.Body.String())
		}
		if code := decodeErrorCode(t, rec.Body.Bytes()); code != "csrf_failed" {
			t.Fatalf("error code = %q, want csrf_failed", code)
		}
	})

	t.Run("replayed login after lockout", func(t *testing.T) {
		mux := ControlMux(testAllowedHost, fakeSPA(), testControlDB(t), testKeyring(t))
		setupOwner(t, mux, testSetupPassword)

		for i := 0; i < lockoutThreshold; i++ {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, newAuthRequest(t, http.MethodPost, "/api/control/v1/auth/login", setupRequestBody("nope")))
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("failure #%d: status = %d, want 401", i+1, rec.Code)
			}
		}
		lockedRec := httptest.NewRecorder()
		mux.ServeHTTP(lockedRec, newAuthRequest(t, http.MethodPost, "/api/control/v1/auth/login", setupRequestBody(testSetupPassword)))
		if lockedRec.Code != http.StatusTooManyRequests {
			t.Fatalf("status = %d, want 429 after threshold", lockedRec.Code)
		}
		// The replay, with the CORRECT password, must still be rejected.
		replayRec := httptest.NewRecorder()
		mux.ServeHTTP(replayRec, newAuthRequest(t, http.MethodPost, "/api/control/v1/auth/login", setupRequestBody(testSetupPassword)))
		if replayRec.Code != http.StatusTooManyRequests {
			t.Fatalf("replayed correct-password login during lockout: status = %d, want 429", replayRec.Code)
		}
		if code := decodeErrorCode(t, replayRec.Body.Bytes()); code != "locked_out" {
			t.Fatalf("error code = %q, want locked_out", code)
		}
	})

	t.Run("reverify reuse past 5 minutes", func(t *testing.T) {
		db := testControlDB(t)
		kr := testKeyring(t)
		mux := ControlMux(testAllowedHost, fakeSPA(), db, kr)
		cookie, csrfToken := setupOwnerWithCSRF(t, mux)
		accountID := seedAcceptanceRevealAccount(t, db, kr, time.Now(), "sk-negmatrix-canary-Zt7Vb4Nr1Hy6Dc5")

		reverifyReq := newAuthRequest(t, http.MethodPost, "/api/control/v1/auth/reverify", setupRequestBody(testSetupPassword))
		reverifyReq.AddCookie(cookie)
		reverifyReq.Header.Set("X-CSRF-Token", csrfToken)
		reverifyRec := httptest.NewRecorder()
		mux.ServeHTTP(reverifyRec, reverifyReq)
		if reverifyRec.Code != http.StatusOK {
			t.Fatalf("reverify status = %d, want 200", reverifyRec.Code)
		}

		tokenHash := secrets.HashSessionHandle(cookie.Value)
		past := acceptanceTimestamp(time.Now().Add(-10 * time.Minute))
		if _, err := db.Conn().Exec(`UPDATE owner_sessions SET reverify_fresh_until = ? WHERE token_hash = ?`, past, tokenHash); err != nil {
			t.Fatalf("back-date: %v", err)
		}

		revealReq := newAuthRequest(t, http.MethodPost, "/api/control/v1/accounts/"+accountID+"/reveal", nil)
		revealReq.AddCookie(cookie)
		revealReq.Header.Set("X-CSRF-Token", csrfToken)
		revealRec := httptest.NewRecorder()
		mux.ServeHTTP(revealRec, revealReq)
		if revealRec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401 for reverify-reuse past window; body = %q", revealRec.Code, revealRec.Body.String())
		}
		if code := decodeErrorCode(t, revealRec.Body.Bytes()); code != "reverification_required" {
			t.Fatalf("error code = %q, want reverification_required", code)
		}
	})

	t.Run("setup after completion", func(t *testing.T) {
		mux := ControlMux(testAllowedHost, fakeSPA(), testControlDB(t), testKeyring(t))
		setupOwner(t, mux, testSetupPassword)

		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, newAuthRequest(t, http.MethodPost, "/api/control/v1/auth/setup", setupRequestBody("another-long-enough-password")))
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409; body = %q", rec.Code, rec.Body.String())
		}
		if code := decodeErrorCode(t, rec.Body.Bytes()); code != "setup_already_complete" {
			t.Fatalf("error code = %q, want setup_already_complete", code)
		}
	})
}

// mustJSON marshals v or fails the test — a tiny local helper so the
// negative-matrix subtests can build PUT bodies inline.
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}
