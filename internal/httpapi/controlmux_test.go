package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

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

// TestControlMux_SPABehindGate_LoopbackAllowedHostReturns200 is Test C1:
// a loopback client with an allowed Host reaches the SPA handler mounted
// at "/".
func TestControlMux_SPABehindGate_LoopbackAllowedHostReturns200(t *testing.T) {
	mux := ControlMux(testAllowedHost, fakeSPA())

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
	mux := ControlMux(testAllowedHost, fakeSPA())

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
	mux := ControlMux(testAllowedHost, fakeSPA())

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
	mux := ControlMux(testAllowedHost, fakeSPA())

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
	mux := ControlMux(testAllowedHost, fakeSPA())

	req := httptest.NewRequest(http.MethodGet, "/providers/42/edit", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Host = testAllowedHost

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /providers/42/edit status = %d, want %d", rec.Code, http.StatusOK)
	}
}
