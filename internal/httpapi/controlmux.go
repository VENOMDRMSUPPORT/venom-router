package httpapi

import (
	"net/http"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// ControlMux builds the control-plane mux (01 §1/§3, P2a-UI-001): the
// same definitive /health liveness surface HealthMux serves, the
// unauthenticated owner-auth handshake (P2b-SEC-001/SEC-002, 09 §5.1/§5.2)
// under /api/control/v1/auth/*, plus the embedded dashboard SPA mounted
// at "/" as a catch-all — all behind the identical loopback +
// Host-allowlist network gate (01 §6a). This replaces HealthMux at the
// composition root now that there is a second surface to serve on the
// same bind; HealthMux itself is unchanged and still usable on its own.
//
// The /auth/* routes are deliberately NOT behind an owner-session
// requirement — they ARE the pre-auth handshake (09 §5: "every endpoint
// except the unauthenticated first-run/login handshake"). The
// owner-session auth middleware for every other /api/control/v1/* route
// is CAPI-001, later work.
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
	mux.Handle("/api/control/v1/auth/login", networkGate(allowedHost, http.HandlerFunc(auth.ServeLogin)))
	mux.Handle("/api/control/v1/auth/logout", networkGate(allowedHost, http.HandlerFunc(auth.ServeLogout)))
	mux.Handle("/api/control/v1/auth/session", networkGate(allowedHost, http.HandlerFunc(auth.ServeSession)))
	mux.Handle("/api/control/v1/auth/reverify", networkGate(allowedHost, http.HandlerFunc(auth.ServeReverify)))

	// The provider registry (P2b-PROV-001) is empty this phase — no
	// adapter registers until PROV-005/PROV-007 — so DerivedCapabilities
	// correctly reports zero capabilities for every catalog entry. It is
	// constructed here, at this composition point, rather than plumbed
	// through ControlMux's signature, to keep this unit's boot ripple to
	// zero call-site changes.
	providersHandler := NewProvidersHandler(providers.NewRegistry())
	mux.Handle("/api/control/v1/providers", networkGate(allowedHost, http.HandlerFunc(providersHandler.ServeList)))
	mux.Handle("/api/control/v1/providers/{id}", networkGate(allowedHost, http.HandlerFunc(providersHandler.ServeGet)))

	mux.Handle("/", networkGate(allowedHost, spa))
	return mux
}
