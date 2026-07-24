package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// setupOwner runs a real POST /auth/setup against mux with the given
// password, failing the test if it does not succeed. It returns the
// session cookie the setup call itself produced (unused by most callers,
// but handy for a couple of tests).
func setupOwner(t *testing.T, mux http.Handler, password string) *http.Cookie {
	t.Helper()

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, newAuthRequest(t, http.MethodPost, "/api/control/v1/auth/setup", setupRequestBody(password)))
	if rec.Code != http.StatusOK {
		t.Fatalf("setup failed: status = %d, body = %q", rec.Code, rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName {
			return c
		}
	}
	t.Fatalf("setup succeeded but did not set a %s cookie", sessionCookieName)
	return nil
}

func decodeSessionData(t *testing.T, body []byte) (idleExpiresAt, absoluteExpiresAt string) {
	t.Helper()
	var got struct {
		Data struct {
			Session struct {
				IdleExpiresAt     string `json:"idle_expires_at"`
				AbsoluteExpiresAt string `json:"absolute_expires_at"`
			} `json:"session"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode session response: %v; body = %q", err, body)
	}
	return got.Data.Session.IdleExpiresAt, got.Data.Session.AbsoluteExpiresAt
}

func decodeErrorCode(t *testing.T, body []byte) string {
	t.Helper()
	var got struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode error response: %v; body = %q", err, body)
	}
	return got.Error.Code
}

func TestAuthLogin_SucceedsAfterSetup(t *testing.T) {
	db := testControlDB(t)
	mux := ControlMux(testAllowedHost, fakeSPA(), db)
	setupOwner(t, mux, testSetupPassword)

	before := 0
	_ = db.Conn().QueryRow("SELECT COUNT(*) FROM owner_sessions").Scan(&before)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, newAuthRequest(t, http.MethodPost, "/api/control/v1/auth/login", setupRequestBody(testSetupPassword)))

	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want %d; body = %q", rec.Code, http.StatusOK, rec.Body.String())
	}

	idle, absolute := decodeSessionData(t, rec.Body.Bytes())
	if idle == "" || absolute == "" {
		t.Fatalf("login response missing session expiry fields: %q", rec.Body.String())
	}

	var after int
	if err := db.Conn().QueryRow("SELECT COUNT(*) FROM owner_sessions").Scan(&after); err != nil {
		t.Fatalf("count owner_sessions: %v", err)
	}
	if after != before+1 {
		t.Fatalf("owner_sessions row count = %d, want %d (setup's session + login's new session)", after, before+1)
	}

	resp := rec.Result()
	var sessionCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookieName {
			sessionCookie = c
		}
	}
	if sessionCookie == nil {
		t.Fatalf("login did not set a %s cookie", sessionCookieName)
	}
	if !sessionCookie.HttpOnly || sessionCookie.SameSite != http.SameSiteStrictMode || sessionCookie.Path != controlAPIPath {
		t.Fatalf("login session cookie flags = %+v, want HttpOnly+SameSite=Strict+Path=%s", sessionCookie, controlAPIPath)
	}

	// P2b-SEC-004: login now issues a session-bound CSRF token in the
	// response body and as a readable XSRF-TOKEN cookie.
	var csrfBody struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &csrfBody); err != nil {
		t.Fatalf("decode csrf_token: %v", err)
	}
	if csrfBody.CSRFToken == "" {
		t.Fatalf("login response missing csrf_token: %q", rec.Body.String())
	}
	var xsrfCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == csrfCookieName {
			xsrfCookie = c
		}
	}
	if xsrfCookie == nil {
		t.Fatalf("login did not set an %s cookie", csrfCookieName)
	}
	if xsrfCookie.HttpOnly {
		t.Fatalf("XSRF-TOKEN cookie HttpOnly = true, want false (client script must read it)")
	}
	if xsrfCookie.Value != csrfBody.CSRFToken {
		t.Fatalf("XSRF-TOKEN cookie value %q != response csrf_token %q", xsrfCookie.Value, csrfBody.CSRFToken)
	}
}

func TestAuthLogin_WrongPasswordRejectedGeneric(t *testing.T) {
	db := testControlDB(t)
	mux := ControlMux(testAllowedHost, fakeSPA(), db)
	setupOwner(t, mux, testSetupPassword)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, newAuthRequest(t, http.MethodPost, "/api/control/v1/auth/login", setupRequestBody("totally-the-wrong-password")))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d for wrong password", rec.Code, http.StatusUnauthorized)
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != "invalid_credentials" {
		t.Fatalf("error code = %q, want %q", code, "invalid_credentials")
	}
}

// TestAuthLogin_NoOwnerRow_SameGenericResponseAsWrongPassword is the
// core no-setup-state-leak proof: with no owner_auth row at all, login
// must fail with the exact same status/code/message as a wrong password
// against a real owner — never a distinguishable response.
func TestAuthLogin_NoOwnerRow_SameGenericResponseAsWrongPassword(t *testing.T) {
	// Case A: no owner_auth row exists at all.
	noOwnerDB := testControlDB(t)
	noOwnerMux := ControlMux(testAllowedHost, fakeSPA(), noOwnerDB)
	noOwnerRec := httptest.NewRecorder()
	noOwnerMux.ServeHTTP(noOwnerRec, newAuthRequest(t, http.MethodPost, "/api/control/v1/auth/login", setupRequestBody("whatever-password-value")))

	// Case B: owner exists, wrong password supplied.
	wrongPwDB := testControlDB(t)
	wrongPwMux := ControlMux(testAllowedHost, fakeSPA(), wrongPwDB)
	setupOwner(t, wrongPwMux, testSetupPassword)
	wrongPwRec := httptest.NewRecorder()
	wrongPwMux.ServeHTTP(wrongPwRec, newAuthRequest(t, http.MethodPost, "/api/control/v1/auth/login", setupRequestBody("whatever-password-value")))

	if noOwnerRec.Code != wrongPwRec.Code {
		t.Fatalf("status differs: no-owner=%d wrong-password=%d, want identical", noOwnerRec.Code, wrongPwRec.Code)
	}
	if noOwnerRec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", noOwnerRec.Code, http.StatusUnauthorized)
	}

	noOwnerCode := decodeErrorCode(t, noOwnerRec.Body.Bytes())
	wrongPwCode := decodeErrorCode(t, wrongPwRec.Body.Bytes())
	if noOwnerCode != wrongPwCode {
		t.Fatalf("error code differs: no-owner=%q wrong-password=%q, want identical", noOwnerCode, wrongPwCode)
	}
	if noOwnerCode != "invalid_credentials" {
		t.Fatalf("error code = %q, want %q", noOwnerCode, "invalid_credentials")
	}

	// Neither response set a session cookie.
	for _, c := range noOwnerRec.Result().Cookies() {
		if c.Name == sessionCookieName && c.Value != "" {
			t.Fatalf("no-owner login response unexpectedly set a session cookie")
		}
	}
}

func TestAuthLogout_RevokesSessionAndClearsCookie(t *testing.T) {
	db := testControlDB(t)
	mux := ControlMux(testAllowedHost, fakeSPA(), db)
	cookie := setupOwner(t, mux, testSetupPassword)

	// Confirm the session is valid before logout.
	sessionReq := newAuthRequest(t, http.MethodGet, "/api/control/v1/auth/session", nil)
	sessionReq.AddCookie(cookie)
	sessionRec := httptest.NewRecorder()
	mux.ServeHTTP(sessionRec, sessionReq)
	if sessionRec.Code != http.StatusOK {
		t.Fatalf("GET /auth/session before logout: status = %d, want %d", sessionRec.Code, http.StatusOK)
	}

	logoutReq := newAuthRequest(t, http.MethodPost, "/api/control/v1/auth/logout", nil)
	logoutReq.AddCookie(cookie)
	logoutRec := httptest.NewRecorder()
	mux.ServeHTTP(logoutRec, logoutReq)

	if logoutRec.Code != http.StatusOK {
		t.Fatalf("logout status = %d, want %d", logoutRec.Code, http.StatusOK)
	}

	var cleared *http.Cookie
	for _, c := range logoutRec.Result().Cookies() {
		if c.Name == sessionCookieName {
			cleared = c
		}
	}
	if cleared == nil || cleared.MaxAge >= 0 {
		t.Fatalf("logout did not clear the session cookie (MaxAge<0 expected), got %+v", cleared)
	}

	// The session is now revoked: GET /auth/session with the SAME (old)
	// cookie must be rejected, and must never be resurrected.
	afterReq := newAuthRequest(t, http.MethodGet, "/api/control/v1/auth/session", nil)
	afterReq.AddCookie(cookie)
	afterRec := httptest.NewRecorder()
	mux.ServeHTTP(afterRec, afterReq)
	if afterRec.Code != http.StatusUnauthorized {
		t.Fatalf("GET /auth/session after logout: status = %d, want %d (revoked session must never be resurrected)", afterRec.Code, http.StatusUnauthorized)
	}
}

func TestAuthLogout_NoOpWithoutValidCookie(t *testing.T) {
	mux := ControlMux(testAllowedHost, fakeSPA(), testControlDB(t))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, newAuthRequest(t, http.MethodPost, "/api/control/v1/auth/logout", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("logout without any cookie: status = %d, want %d (idempotent no-op)", rec.Code, http.StatusOK)
	}
}

func TestAuthSession_ValidReturnsExpiry(t *testing.T) {
	mux := ControlMux(testAllowedHost, fakeSPA(), testControlDB(t))
	cookie := setupOwner(t, mux, testSetupPassword)

	req := newAuthRequest(t, http.MethodGet, "/api/control/v1/auth/session", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %q", rec.Code, http.StatusOK, rec.Body.String())
	}
	idle, absolute := decodeSessionData(t, rec.Body.Bytes())
	if idle == "" || absolute == "" {
		t.Fatalf("missing session expiry fields: %q", rec.Body.String())
	}
}

func TestAuthSession_AbsentCookieRejected(t *testing.T) {
	mux := ControlMux(testAllowedHost, fakeSPA(), testControlDB(t))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, newAuthRequest(t, http.MethodGet, "/api/control/v1/auth/session", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d for no cookie", rec.Code, http.StatusUnauthorized)
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != "invalid_credentials" {
		t.Fatalf("error code = %q, want %q", code, "invalid_credentials")
	}
}

func TestAuthSession_UnknownCookieValueRejected(t *testing.T) {
	mux := ControlMux(testAllowedHost, fakeSPA(), testControlDB(t))
	setupOwner(t, mux, testSetupPassword)

	req := newAuthRequest(t, http.MethodGet, "/api/control/v1/auth/session", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "not-a-real-session-handle"})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d for an unrecognized session handle", rec.Code, http.StatusUnauthorized)
	}
}

// TestAuthCanary_LoginPasswordNeverLeaks pushes a distinctive canary
// password through the real POST /auth/login path (both the wrong-
// password branch and the no-owner-row branch) and asserts no fragment
// of it appears in the response body or any response header.
func TestAuthCanary_LoginPasswordNeverLeaks(t *testing.T) {
	const canaryPassword = "CANARY-SECRET-9f3Kx2Qw8pLm0Zt7Vb4Nr1Hy6Dc5Ea-login"

	db := testControlDB(t)
	mux := ControlMux(testAllowedHost, fakeSPA(), db)
	setupOwner(t, mux, testSetupPassword)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, newAuthRequest(t, http.MethodPost, "/api/control/v1/auth/login", setupRequestBody(canaryPassword)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	assertNoFragment(t, rec.Body.String(), canaryPassword, "login response body")
	for key, values := range rec.Result().Header {
		for _, v := range values {
			assertNoFragment(t, v, canaryPassword, "login response header "+key)
		}
	}

	// The no-owner-row branch, too.
	noOwnerMux := ControlMux(testAllowedHost, fakeSPA(), testControlDB(t))
	noOwnerRec := httptest.NewRecorder()
	noOwnerMux.ServeHTTP(noOwnerRec, newAuthRequest(t, http.MethodPost, "/api/control/v1/auth/login", setupRequestBody(canaryPassword)))
	assertNoFragment(t, noOwnerRec.Body.String(), canaryPassword, "no-owner login response body")
}
