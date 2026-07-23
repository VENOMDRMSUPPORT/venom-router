// Package httpapi implements the HTTP surfaces described in 01 §6. This
// unit (P0-CAPI-001) implements only the /health liveness surface (01
// §6d); the authenticated /api/control/v1/* control plane is later work
// (P2b+).
package httpapi

import "net/http"

// HealthMux builds the definitive /health liveness surface (01 §6d):
// unauthenticated, outside /api/control/v1, minimal (process up,
// listener accepting) — no owner data, no session, no CSRF. It is the
// single liveness surface; per 01 §6d, a distinct, optional,
// owner-session-gated readiness endpoint (/api/control/v1/health) may be
// added later, but that is explicitly not V1 and is not implemented
// here.
//
// allowedHost is the composition root's configured bind host:port (e.g.
// "127.0.0.1:8081") — the network gate accepts only that value, plus the
// bare loopback hostnames, as the request's Host header.
func HealthMux(allowedHost string) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/health", networkGate(allowedHost, http.HandlerFunc(healthHandler)))
	return mux
}

// healthHandler returns a minimal liveness signal only: process up,
// listener accepting. No DB/keyring/readiness detail and no owner data.
func healthHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}
