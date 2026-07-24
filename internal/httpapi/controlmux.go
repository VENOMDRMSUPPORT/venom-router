package httpapi

import "net/http"

// ControlMux builds the control-plane mux (01 §1/§3, P2a-UI-001): the
// same definitive /health liveness surface HealthMux serves, plus the
// embedded dashboard SPA mounted at "/" as a catch-all — both behind the
// identical loopback + Host-allowlist network gate (01 §6a). This
// replaces HealthMux at the composition root now that there is a second
// surface to serve on the same bind; HealthMux itself is unchanged and
// still usable on its own.
//
// spa is httpui's SPA handler (embedded assets + SPA fallback). This
// package does not import httpui — it only composes whatever handler
// it's given behind the gate, keeping httpui a pure, gate-agnostic
// handler package.
func ControlMux(allowedHost string, spa http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/health", networkGate(allowedHost, http.HandlerFunc(healthHandler)))
	mux.Handle("/", networkGate(allowedHost, spa))
	return mux
}
