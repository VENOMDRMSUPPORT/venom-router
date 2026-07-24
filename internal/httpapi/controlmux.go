package httpapi

import (
	"net/http"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// ControlMux builds the control-plane mux (01 §1/§3, P2a-UI-001): the
// same definitive /health liveness surface HealthMux serves, the
// unauthenticated first-run owner-setup handshake (P2b-SEC-001, 09 §5.1)
// under /api/control/v1/auth/*, plus the embedded dashboard SPA mounted
// at "/" as a catch-all — all behind the identical loopback +
// Host-allowlist network gate (01 §6a). This replaces HealthMux at the
// composition root now that there is a second surface to serve on the
// same bind; HealthMux itself is unchanged and still usable on its own.
//
// The /auth/setup and /auth/status routes are deliberately NOT behind an
// owner-session requirement — they ARE the pre-auth handshake (09 §5:
// "every endpoint except the unauthenticated first-run/login handshake").
// The owner-session auth middleware for every other /api/control/v1/*
// route is CAPI-001, later work.
//
// spa is httpui's SPA handler (embedded assets + SPA fallback). This
// package does not import httpui — it only composes whatever handler
// it's given behind the gate, keeping httpui a pure, gate-agnostic
// handler package.
func ControlMux(allowedHost string, spa http.Handler, db *storage.DB) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/health", networkGate(allowedHost, http.HandlerFunc(healthHandler)))

	auth := NewAuthHandlers(db)
	mux.Handle("/api/control/v1/auth/setup", networkGate(allowedHost, http.HandlerFunc(auth.ServeSetup)))
	mux.Handle("/api/control/v1/auth/status", networkGate(allowedHost, http.HandlerFunc(auth.ServeStatus)))

	mux.Handle("/", networkGate(allowedHost, spa))
	return mux
}
