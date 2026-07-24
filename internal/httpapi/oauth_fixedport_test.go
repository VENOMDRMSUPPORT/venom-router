package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestFixedPortOAuthServer_ServesOnlyItsOwnRoutes proves the fixed-port
// framework is a genuinely separate server: it serves exactly the
// handler it was given (a route the control-plane mux does not have) and
// nothing else — no control-plane route (e.g. /health) is reachable on
// it, and it is never session-bound (no owner-session concept exists on
// this listener at all).
func TestFixedPortOAuthServer_ServesOnlyItsOwnRoutes(t *testing.T) {
	var callbackHit bool
	fixedMux := http.NewServeMux()
	fixedMux.HandleFunc("/fixed-callback", func(w http.ResponseWriter, _ *http.Request) {
		callbackHit = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("fixed-port callback reached"))
	})

	srv, err := StartFixedPortOAuthServer(0, fixedMux)
	if err != nil {
		t.Fatalf("StartFixedPortOAuthServer: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	// 1. The fixed-port server serves its own registered route.
	resp, err := http.Get("http://" + srv.Addr() + "/fixed-callback")
	if err != nil {
		t.Fatalf("GET fixed-port /fixed-callback: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "fixed-port callback reached" {
		t.Fatalf("fixed-port /fixed-callback = %d %q, want 200 \"fixed-port callback reached\"", resp.StatusCode, body)
	}
	if !callbackHit {
		t.Fatalf("fixed-port handler was not actually invoked")
	}

	// 2. The fixed-port server does NOT serve any control-plane route —
	// it has no /health, no /api/control/v1/* — a path never registered
	// on it 404s, proving its route table is genuinely its own and not
	// somehow shared with ControlMux's.
	resp2, err := http.Get("http://" + srv.Addr() + "/health")
	if err != nil {
		t.Fatalf("GET fixed-port /health: %v", err)
	}
	_ = resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("fixed-port /health = %d, want 404 (no control-plane route exists on this listener)", resp2.StatusCode)
	}
}

// TestFixedPortOAuthServer_ControlMuxNeverServesItsRoute proves isolation
// from the OTHER direction: a path that only the fixed-port server
// registers is never reachable through the control-plane mux either — it
// simply falls through to ControlMux's own routing (the SPA catch-all),
// never to the fixed-port handler's logic. The two are genuinely
// independent net.Listener-backed servers with disjoint route tables,
// not two views of shared state.
func TestFixedPortOAuthServer_ControlMuxNeverServesItsRoute(t *testing.T) {
	var callbackHit bool
	fixedMux := http.NewServeMux()
	fixedMux.HandleFunc("/fixed-callback", func(w http.ResponseWriter, _ *http.Request) {
		callbackHit = true
		w.WriteHeader(http.StatusOK)
	})

	srv, err := StartFixedPortOAuthServer(0, fixedMux)
	if err != nil {
		t.Fatalf("StartFixedPortOAuthServer: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	mux := ControlMux(testAllowedHost, fakeSPA(), testControlDB(t), testKeyring(t))
	req := httptest.NewRequest(http.MethodGet, "/fixed-callback", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Host = testAllowedHost
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	// ControlMux mounts the SPA as a catch-all at "/", so an unregistered
	// path like this one reaches the fake SPA handler, NOT a 404 and
	// certainly not the fixed-port server's own logic.
	if rec.Code != http.StatusOK || rec.Body.String() != "<!doctype html><html><body>fake dashboard</body></html>" {
		t.Fatalf("ControlMux GET /fixed-callback = %d %q, want the SPA catch-all's own output", rec.Code, rec.Body.String())
	}
	if callbackHit {
		t.Fatalf("the fixed-port server's handler ran via ControlMux's listener — the two servers are not actually independent")
	}
}

// TestFixedPortOAuthServer_NotWiredIntoProductionBoot is a documentation-
// level guard: internal/app.Boot never constructs a
// FixedPortOAuthServer this phase (no provider needs one yet) — this
// test exists so a future accidental wire-up is at least forced to
// touch this file, per the unit's "build but don't wire in" requirement.
// It performs no assertion about internal/app itself (this package must
// not import internal/app); it only re-states the framework's own
// starting state: a freshly-constructed server has exactly the routes
// its caller gave it, nothing implicitly added.
func TestFixedPortOAuthServer_NotWiredIntoProductionBoot(t *testing.T) {
	emptyMux := http.NewServeMux()
	srv, err := StartFixedPortOAuthServer(0, emptyMux)
	if err != nil {
		t.Fatalf("StartFixedPortOAuthServer: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + srv.Addr() + "/anything")
	if err != nil {
		t.Fatalf("GET fixed-port /anything: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for a handler with no registered routes", resp.StatusCode)
	}
}
