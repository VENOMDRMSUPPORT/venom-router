package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// setupOwnerWithCSRF runs a real POST /auth/setup against mux, returning
// both the session cookie AND the CSRF token the setup response itself
// carries — everything a caller needs to reach a gated mutation.
func setupOwnerWithCSRF(t *testing.T, mux http.Handler) (*http.Cookie, string) {
	t.Helper()

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, newAuthRequest(t, http.MethodPost, "/api/control/v1/auth/setup", setupRequestBody(testSetupPassword)))
	if rec.Code != http.StatusOK {
		t.Fatalf("setup failed: status = %d, body = %q", rec.Code, rec.Body.String())
	}

	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatalf("setup succeeded but did not set a %s cookie", sessionCookieName)
	}

	var body struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode setup response: %v; body = %q", err, rec.Body.String())
	}
	if body.CSRFToken == "" {
		t.Fatalf("setup response missing csrf_token: %q", rec.Body.String())
	}
	return cookie, body.CSRFToken
}

// --- Gating: POST .../oauth/begin is owner-session + CSRF gated ---

// TestControlMux_OAuthBegin_UnauthenticatedRejected proves begin behaves
// exactly like every other mutating control route: no session at all is
// rejected 401 before OAuthHandler.ServeBegin ever runs (the empty
// composition-root registry would otherwise 404 on any provider id, so a
// 401 here can only come from the gate).
func TestControlMux_OAuthBegin_UnauthenticatedRejected(t *testing.T) {
	mux := ControlMux(testAllowedHost, fakeSPA(), testControlDB(t), testKeyring(t))

	req := newAuthRequest(t, http.MethodPost, "/api/control/v1/providers/opencode-zen/oauth/begin", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 without any session", rec.Code)
	}
}

// TestControlMux_OAuthBegin_SessionWithoutCSRFRejected proves a mutating
// begin call with a valid session but no CSRF token is rejected 403
// before the handler runs.
func TestControlMux_OAuthBegin_SessionWithoutCSRFRejected(t *testing.T) {
	mux := ControlMux(testAllowedHost, fakeSPA(), testControlDB(t), testKeyring(t))
	cookie, _ := setupOwnerWithCSRF(t, mux)

	req := newAuthRequest(t, http.MethodPost, "/api/control/v1/providers/opencode-zen/oauth/begin", nil)
	req.AddCookie(cookie) // no X-CSRF-Token
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for a mutation with no CSRF token", rec.Code)
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != "csrf_failed" {
		t.Fatalf("error code = %q, want csrf_failed", code)
	}
}

// TestControlMux_OAuthBegin_ValidSessionAndCSRFReachesHandler proves a
// fully authenticated + CSRF'd begin call passes the gate and reaches
// OAuthHandler.ServeBegin (which then 404s — not_found — because the
// composition-root registry has no OAuth adapter registered this phase;
// that 404, rather than a 401/403, is exactly the proof the gate let it
// through).
func TestControlMux_OAuthBegin_ValidSessionAndCSRFReachesHandler(t *testing.T) {
	mux := ControlMux(testAllowedHost, fakeSPA(), testControlDB(t), testKeyring(t))
	cookie, csrfToken := setupOwnerWithCSRF(t, mux)

	req := newAuthRequest(t, http.MethodPost, "/api/control/v1/providers/opencode-zen/oauth/begin", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrfToken)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
		t.Fatalf("status = %d, want the gate to pass (not 401/403) once session+CSRF are valid; body = %q", rec.Code, rec.Body.String())
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 not_found from OAuthHandler.ServeBegin (empty registry); body = %q", rec.Code, rec.Body.String())
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != "not_found" {
		t.Fatalf("error code = %q, want not_found", code)
	}
}

// --- Gating: GET .../oauth/{provider}/callback is networkGate ONLY ---

// TestControlMux_OAuthCallback_ReachableWithoutAnySession proves the
// callback route needs no owner session/CSRF at all: a loopback request
// with an allowed Host reaches OAuthHandler.ServeCallback (which renders
// its generic failure page for an unknown provider) with no session
// cookie present whatsoever.
func TestControlMux_OAuthCallback_ReachableWithoutAnySession(t *testing.T) {
	mux := ControlMux(testAllowedHost, fakeSPA(), testControlDB(t), testKeyring(t))

	req := httptest.NewRequest(http.MethodGet, "/api/control/v1/oauth/opencode-zen/callback?code=x&state=y", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Host = testAllowedHost
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
		t.Fatalf("status = %d, want the callback route to be reachable without a session; body = %q", rec.Code, rec.Body.String())
	}
}

// TestControlMux_OAuthCallback_RejectedOffLoopback proves the callback
// route still enforces the loopback check, exactly like every other
// control-plane route.
func TestControlMux_OAuthCallback_RejectedOffLoopback(t *testing.T) {
	mux := ControlMux(testAllowedHost, fakeSPA(), testControlDB(t), testKeyring(t))

	req := httptest.NewRequest(http.MethodGet, "/api/control/v1/oauth/opencode-zen/callback?code=x&state=y", nil)
	req.RemoteAddr = "203.0.113.5:54321"
	req.Host = testAllowedHost
	req.Header.Set("X-Forwarded-For", "127.0.0.1")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for a non-loopback socket", rec.Code)
	}
}

// TestControlMux_OAuthCallback_RejectedBadHost proves the callback route
// still enforces the Host allowlist.
func TestControlMux_OAuthCallback_RejectedBadHost(t *testing.T) {
	mux := ControlMux(testAllowedHost, fakeSPA(), testControlDB(t), testKeyring(t))

	req := httptest.NewRequest(http.MethodGet, "/api/control/v1/oauth/opencode-zen/callback?code=x&state=y", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Host = "evil.example.com"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for a disallowed Host", rec.Code)
	}
}

// --- Gating: GET .../oauth/{transaction_id}/status is networkGate ONLY ---

// TestControlMux_OAuthStatus_ReachableWithoutAnySession proves the
// status route needs no owner session/CSRF either — the transaction_id
// itself is the capability token.
func TestControlMux_OAuthStatus_ReachableWithoutAnySession(t *testing.T) {
	mux := ControlMux(testAllowedHost, fakeSPA(), testControlDB(t), testKeyring(t))

	req := httptest.NewRequest(http.MethodGet, "/api/control/v1/oauth/does-not-exist/status", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Host = testAllowedHost
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
		t.Fatalf("status = %d, want the status route to be reachable without a session; body = %q", rec.Code, rec.Body.String())
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 not_found for an unknown transaction id; body = %q", rec.Code, rec.Body.String())
	}
}

// TestControlMux_OAuthStatus_RejectedOffLoopbackOrBadHost proves the
// status route still enforces both the loopback check and the Host
// allowlist, via the JSON-typed network gate.
func TestControlMux_OAuthStatus_RejectedOffLoopbackOrBadHost(t *testing.T) {
	mux := ControlMux(testAllowedHost, fakeSPA(), testControlDB(t), testKeyring(t))

	nonLoopback := httptest.NewRequest(http.MethodGet, "/api/control/v1/oauth/whatever/status", nil)
	nonLoopback.RemoteAddr = "203.0.113.5:54321"
	nonLoopback.Host = testAllowedHost
	rec1 := httptest.NewRecorder()
	mux.ServeHTTP(rec1, nonLoopback)
	if rec1.Code != http.StatusForbidden {
		t.Fatalf("non-loopback status = %d, want 403", rec1.Code)
	}
	if code := decodeErrorCode(t, rec1.Body.Bytes()); code != "not_loopback" {
		t.Fatalf("non-loopback error code = %q, want not_loopback", code)
	}

	badHost := httptest.NewRequest(http.MethodGet, "/api/control/v1/oauth/whatever/status", nil)
	badHost.RemoteAddr = "127.0.0.1:54321"
	badHost.Host = "evil.example.com"
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, badHost)
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("bad-host status = %d, want 403", rec2.Code)
	}
	if code := decodeErrorCode(t, rec2.Body.Bytes()); code != "host_not_allowed" {
		t.Fatalf("bad-host error code = %q, want host_not_allowed", code)
	}
}
