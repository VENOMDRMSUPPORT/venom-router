package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// gatedTestMutation is a minimal handler wired behind ownerSessionGate
// with an observable side effect, mirroring csrf_test.go's
// csrfGuardedMutation — used to prove the gate's CSRF half without
// depending on any real mutating control route (none exists yet in
// this batch besides the auth handshake, which is deliberately exempt).
func gatedTestMutation(t *testing.T, sideEffects *int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := sessionFromContext(r.Context()); !ok {
			t.Errorf("sessionFromContext: no session present despite passing ownerSessionGate")
		}
		*sideEffects++
		writeAuthJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"ok": true}})
	}
}

func TestOwnerSessionGate_UnauthenticatedRequestRejectedHandlerNeverRuns(t *testing.T) {
	h := NewAuthHandlers(testControlDB(t))
	var sideEffects int
	gated := h.ownerSessionGate(gatedTestMutation(t, &sideEffects))

	req := newAuthRequest(t, http.MethodGet, "/api/control/v1/test/mutate", nil)
	rec := httptest.NewRecorder()
	gated.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 without any session cookie; body = %q", rec.Code, rec.Body.String())
	}
	if sideEffects != 0 {
		t.Fatalf("sideEffects = %d, want 0 (handler must never run)", sideEffects)
	}
}

func TestOwnerSessionGate_ExpiredSessionRejected(t *testing.T) {
	clock := fixedClockAt2026()
	h, _ := newTestAuthHandlersWithDB(t, &clock)
	cookie := sessionCookieFrom(t, setupOwnerDirect(t, h, testSetupPassword))

	var sideEffects int
	gated := h.ownerSessionGate(gatedTestMutation(t, &sideEffects))

	clock = clock.Add(13 * time.Hour) // past the 12h absolute cap

	req := newAuthRequest(t, http.MethodGet, "/api/control/v1/test/mutate", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	gated.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for an expired session; body = %q", rec.Code, rec.Body.String())
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != "session_expired" {
		t.Fatalf("error code = %q, want session_expired", code)
	}
	if sideEffects != 0 {
		t.Fatalf("sideEffects = %d, want 0", sideEffects)
	}
}

func TestOwnerSessionGate_MutationWithoutCSRFRejectedBeforeSideEffect(t *testing.T) {
	clock := fixedClockAt2026()
	h, _ := newTestAuthHandlersWithDB(t, &clock)
	cookie := sessionCookieFrom(t, setupOwnerDirect(t, h, testSetupPassword))

	var sideEffects int
	gated := h.ownerSessionGate(gatedTestMutation(t, &sideEffects))

	req := newAuthRequest(t, http.MethodPost, "/api/control/v1/test/mutate", nil)
	req.AddCookie(cookie) // no X-CSRF-Token
	rec := httptest.NewRecorder()
	gated.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for a mutation with no CSRF token; body = %q", rec.Code, rec.Body.String())
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != "csrf_failed" {
		t.Fatalf("error code = %q, want csrf_failed", code)
	}
	if sideEffects != 0 {
		t.Fatalf("sideEffects = %d, want 0", sideEffects)
	}
}

func TestOwnerSessionGate_ValidSessionAndCSRFReachesHandlerWithSessionInContext(t *testing.T) {
	clock := fixedClockAt2026()
	h, _ := newTestAuthHandlersWithDB(t, &clock)
	setupRec := setupOwnerDirect(t, h, testSetupPassword)
	cookie := sessionCookieFrom(t, setupRec)
	var setupBody struct {
		CSRFToken string `json:"csrf_token"`
	}
	decodeJSON(t, setupRec.Body.Bytes(), &setupBody)

	var sideEffects int
	gated := h.ownerSessionGate(gatedTestMutation(t, &sideEffects))

	req := newAuthRequest(t, http.MethodPost, "/api/control/v1/test/mutate", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", setupBody.CSRFToken)
	rec := httptest.NewRecorder()
	gated.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %q", rec.Code, rec.Body.String())
	}
	if sideEffects != 1 {
		t.Fatalf("sideEffects = %d, want 1", sideEffects)
	}
}

func TestOwnerSessionGate_GetNeedsNoCSRF(t *testing.T) {
	clock := fixedClockAt2026()
	h, _ := newTestAuthHandlersWithDB(t, &clock)
	cookie := sessionCookieFrom(t, setupOwnerDirect(t, h, testSetupPassword))

	var sideEffects int
	gated := h.ownerSessionGate(gatedTestMutation(t, &sideEffects))

	req := newAuthRequest(t, http.MethodGet, "/api/control/v1/test/mutate", nil)
	req.AddCookie(cookie) // no X-CSRF-Token — fine for GET
	rec := httptest.NewRecorder()
	gated.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for GET with no CSRF token; body = %q", rec.Code, rec.Body.String())
	}
}

// TestControlMux_GatedRoutesBehindNetworkGateJSON proves the FULL
// composition end-to-end through the real ControlMux (not just the
// gate in isolation): non-loopback / bad-Host requests to a gated
// route get the typed envelope, and header-spoofed loopback claims
// don't bypass it.
func TestControlMux_GatedRoutesBehindNetworkGateJSON_NonLoopbackRejected(t *testing.T) {
	mux := ControlMux(testAllowedHost, fakeSPA(), testControlDB(t))

	req := httptest.NewRequest(http.MethodGet, "/api/control/v1/providers", nil)
	req.RemoteAddr = "203.0.113.5:54321"
	req.Host = testAllowedHost
	req.Header.Set("X-Forwarded-For", "127.0.0.1")

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for a non-loopback socket (even with spoofed XFF)", rec.Code)
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != "not_loopback" {
		t.Fatalf("error code = %q, want not_loopback", code)
	}
}

func TestControlMux_GatedRoutesBehindNetworkGateJSON_BadHostRejected(t *testing.T) {
	mux := ControlMux(testAllowedHost, fakeSPA(), testControlDB(t))

	req := httptest.NewRequest(http.MethodGet, "/api/control/v1/providers", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Host = "evil.example.com"

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for a disallowed Host", rec.Code)
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != "host_not_allowed" {
		t.Fatalf("error code = %q, want host_not_allowed", code)
	}
}
