package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/secrets"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// testKeyring builds a fresh, ready-to-use keyring for a ControlMux test
// (P2b-PROV-006) — ControlMux now needs a *secrets.Keyring to wire the
// OAuth enrollment service, even for tests that never exercise
// /api/control/v1/providers/*/oauth/* or /api/control/v1/oauth/*.
func testKeyring(t *testing.T) *secrets.Keyring {
	t.Helper()
	kr, err := secrets.Load(t.TempDir(), "", false)
	if err != nil {
		t.Fatalf("secrets.Load: %v", err)
	}
	return kr
}

// fakeSPA is a minimal stand-in for httpui's real SPA handler — ControlMux
// must compose whatever http.Handler it's given behind the gate without
// caring what's inside it (httpui is deliberately not imported here).
func fakeSPA() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<!doctype html><html><body>fake dashboard</body></html>"))
	})
}

// testControlDB opens and migrates a fresh temp-dir database for a
// ControlMux test — ControlMux now needs a *storage.DB to build the
// P2b-SEC-001 auth handlers, even for tests that never exercise
// /api/control/v1/auth/*.
func testControlDB(t *testing.T) *storage.DB {
	t.Helper()

	dir := t.TempDir()
	db, err := storage.Open(dir)
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := storage.Migrate(context.Background(), db); err != nil {
		t.Fatalf("storage.Migrate: %v", err)
	}
	return db
}

// TestControlMux_SPABehindGate_LoopbackAllowedHostReturns200 is Test C1:
// a loopback client with an allowed Host reaches the SPA handler mounted
// at "/".
func TestControlMux_SPABehindGate_LoopbackAllowedHostReturns200(t *testing.T) {
	mux := ControlMux(testAllowedHost, fakeSPA(), testControlDB(t), testKeyring(t))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Host = testAllowedHost

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want %d; body = %q", rec.Code, http.StatusOK, rec.Body.String())
	}
	if got := rec.Body.String(); got != "<!doctype html><html><body>fake dashboard</body></html>" {
		t.Fatalf("GET / body = %q, want the SPA handler's output", got)
	}
}

// TestControlMux_HealthStillWorksAlongsideSPA is Test C2: /health keeps
// serving exactly as HealthMux alone does, even with the SPA now mounted
// on the same mux at "/".
func TestControlMux_HealthStillWorksAlongsideSPA(t *testing.T) {
	mux := ControlMux(testAllowedHost, fakeSPA(), testControlDB(t), testKeyring(t))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Host = testAllowedHost

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /health status = %d, want %d; body = %q", rec.Code, http.StatusOK, rec.Body.String())
	}
	if rec.Body.String() != `{"status":"ok"}` {
		t.Fatalf("GET /health body = %q, want the liveness payload", rec.Body.String())
	}
}

// TestControlMux_DisallowedHostRejected_BothRoutes is Test C3: a
// disallowed Host header is rejected with 403 before either the SPA or
// /health ever runs.
func TestControlMux_DisallowedHostRejected_BothRoutes(t *testing.T) {
	mux := ControlMux(testAllowedHost, fakeSPA(), testControlDB(t), testKeyring(t))

	for _, p := range []string{"/", "/health"} {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		req.RemoteAddr = "127.0.0.1:54321" // loopback — only Host should fail
		req.Host = "evil.example.com"

		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("GET %s status = %d, want %d for disallowed Host", p, rec.Code, http.StatusForbidden)
		}
	}
}

// TestControlMux_NonLoopbackRejected_BothRoutes is Test C4: a
// non-loopback RemoteAddr is rejected for both the SPA and /health, and
// a spoofed X-Forwarded-For claiming loopback is never consulted.
func TestControlMux_NonLoopbackRejected_BothRoutes(t *testing.T) {
	mux := ControlMux(testAllowedHost, fakeSPA(), testControlDB(t), testKeyring(t))

	for _, p := range []string{"/", "/health"} {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		req.RemoteAddr = "203.0.113.5:54321"
		req.Host = testAllowedHost
		req.Header.Set("X-Forwarded-For", "127.0.0.1")

		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("GET %s status = %d, want %d for non-loopback RemoteAddr", p, rec.Code, http.StatusForbidden)
		}
	}
}

// TestControlMux_SPAFallbackRouteBehindGate is Test C5: an arbitrary
// non-asset, non-/health path (a client-side SPA route) still reaches
// the SPA handler behind the gate — ControlMux mounts the SPA at "/" as
// a catch-all, not an exact match.
func TestControlMux_SPAFallbackRouteBehindGate(t *testing.T) {
	mux := ControlMux(testAllowedHost, fakeSPA(), testControlDB(t), testKeyring(t))

	req := httptest.NewRequest(http.MethodGet, "/providers/42/edit", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Host = testAllowedHost

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /providers/42/edit status = %d, want %d", rec.Code, http.StatusOK)
	}
}

// TestControlMux_ReviewCensusRouteIsGone proves GET
// /api/control/v1/certifications/review is NOT registered on the mux.
//
// The certification-review census (P6-CAPI-EXTRA / P6-UI-012) was deleted on
// 2026-08-07: model qualification became fully automatic (qualification.go's
// 30s tick), so no certification waits on a human review any more and the
// census counted a queue that does not exist. A caller-less HTTP endpoint is a
// named smell in this repo, so the route went with the UI.
//
// WHY NOT A 404 ASSERTION: ControlMux mounts the SPA at "/" as a catch-all
// (see TestControlMux_SPAFallbackRouteBehindGate), so an unregistered path is
// answered by the SPA handler, never by a 404. "Unregistered" is therefore
// proved the only way it can be — the request reaches the SPA catch-all
// instead of a JSON handler:
//
//   - registered + gated, unauthenticated -> 401 JSON  (what it used to do)
//   - registered + gated, authenticated   -> 200 census JSON
//   - unregistered (now)                  -> the SPA handler's own output
//
// Re-adding the mux.Handle line in controlmux.go turns every check below RED.
func TestControlMux_ReviewCensusRouteIsGone(t *testing.T) {
	const path = "/api/control/v1/certifications/review"
	mux := ControlMux(testAllowedHost, fakeSPA(), testControlDB(t), testKeyring(t))

	// 1. Unauthenticated: a gated route answers 401. The SPA does not.
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, newAuthRequest(t, http.MethodGet, path, nil))
	if rec.Code == http.StatusUnauthorized {
		t.Fatalf("GET %s returned 401 — the route is still registered behind `gated`", path)
	}

	// 2. Authenticated: the only way to catch a re-registration that answers
	// the owner rather than the gate.
	cookie, csrfToken := setupOwnerWithCSRF(t, mux)
	req := newAuthRequest(t, http.MethodGet, path, nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrfToken)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	body := rec.Body.String()
	for _, marker := range []string{"evaluated_reasons", "not_evaluated_reasons", "by_reason", "capability_not_certified"} {
		if strings.Contains(body, marker) {
			t.Fatalf("GET %s served a census payload (found %q) — the endpoint is back: %s", path, marker, body)
		}
	}

	// 3. And it lands on the SPA catch-all, which is what "unregistered" means
	// on this mux — not merely "returned something that isn't a census".
	if !strings.Contains(body, "fake dashboard") {
		t.Fatalf("GET %s status = %d, body = %q — want the SPA catch-all's output, i.e. no route registered for this path", path, rec.Code, body)
	}
}

// TestControlMux_AccountDelete_RoutesToDisconnect proves DELETE
// /accounts/{id} is wired THROUGH THE MUX to ServeDisconnect — a
// regression guard for a real gap: the account detail route is registered
// method-lessly to ServeGet, so before a method-specific DELETE pattern was
// added a DELETE fell through to ServeGet and returned 405, leaving the
// soft-disconnect handler unreachable. This asserts the composed mux, not
// the handler in isolation (accounts_test.go covers the disconnect logic
// directly). An unknown account id is used deliberately: ServeDisconnect
// answers 404 not_found for it, whereas the old dead routing answered 405 —
// so 404 (not 405) is exactly the proof the route reaches ServeDisconnect.
// Removing the DELETE registration in ControlMux turns this RED (405).
func TestControlMux_AccountDelete_RoutesToDisconnect(t *testing.T) {
	mux := ControlMux(testAllowedHost, fakeSPA(), testControlDB(t), testKeyring(t))
	cookie, csrfToken := setupOwnerWithCSRF(t, mux)

	req := newAuthRequest(t, http.MethodDelete, "/api/control/v1/accounts/does-not-exist", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrfToken)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code == http.StatusMethodNotAllowed {
		t.Fatalf("DELETE /accounts/{id} returned 405 — the route is not wired to ServeDisconnect (regression)")
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("DELETE /accounts/{id} status = %d, want 404 (ServeDisconnect for an unknown account); body = %q", rec.Code, rec.Body.String())
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != "not_found" {
		t.Fatalf("error code = %q, want not_found", code)
	}
}
