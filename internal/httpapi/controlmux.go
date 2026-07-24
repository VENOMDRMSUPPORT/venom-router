package httpapi

import (
	"net/http"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/application"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/secrets"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// ControlMux builds the control-plane mux (01 §1/§3, P2a-UI-001): the
// same definitive /health liveness surface HealthMux serves, the
// unauthenticated owner-auth handshake (P2b-SEC-001/SEC-002, 09 §5.1/§5.2)
// under /api/control/v1/auth/*, the embedded dashboard SPA mounted at "/"
// as a catch-all, and every other /api/control/v1/* route behind the
// owner-session + CSRF middleware (P2b-CAPI-001) — all behind the
// identical loopback + Host-allowlist network gate (01 §6a) first. This
// replaces HealthMux at the composition root now that there is a second
// surface to serve on the same bind; HealthMux itself is unchanged and
// still usable on its own.
//
// Gating model (exact, per P2b-CAPI-001):
//   - /health and "/" (the SPA): networkGate only (plain-text 403 on
//     rejection) — unchanged from before this unit.
//   - /api/control/v1/auth/*: networkGate only, unchanged — this IS the
//     pre-auth handshake (09 §5: "every endpoint except the
//     unauthenticated first-run/login handshake"), so it self-manages
//     auth per endpoint rather than going through ownerSessionGate.
//   - every other /api/control/v1/* route: networkGateJSON (typed 403
//     envelope) wrapping auth.ownerSessionGate (session + CSRF), so a
//     handler never runs without both a valid owner session and, for
//     mutations, a valid CSRF token.
//
// spa is httpui's SPA handler (embedded assets + SPA fallback). This
// package does not import httpui — it only composes whatever handler
// it's given behind the gate, keeping httpui a pure, gate-agnostic
// handler package.
//
// kr is the process's active keyring (P2b-PROV-006), needed here to wire
// OAuthEnrollmentService's PKCE-verifier and credential envelope
// encryption — the same keyring internal/app.Boot already loads at its
// load_keyring stage.
func ControlMux(allowedHost string, spa http.Handler, db *storage.DB, kr *secrets.Keyring) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/health", networkGate(allowedHost, http.HandlerFunc(healthHandler)))

	auth := NewAuthHandlers(db)
	mux.Handle("/api/control/v1/auth/setup", networkGate(allowedHost, http.HandlerFunc(auth.ServeSetup)))
	mux.Handle("/api/control/v1/auth/status", networkGate(allowedHost, http.HandlerFunc(auth.ServeStatus)))
	mux.Handle("/api/control/v1/auth/login", networkGate(allowedHost, http.HandlerFunc(auth.ServeLogin)))
	mux.Handle("/api/control/v1/auth/logout", networkGate(allowedHost, http.HandlerFunc(auth.ServeLogout)))
	mux.Handle("/api/control/v1/auth/session", networkGate(allowedHost, http.HandlerFunc(auth.ServeSession)))
	mux.Handle("/api/control/v1/auth/reverify", networkGate(allowedHost, http.HandlerFunc(auth.ServeReverify)))

	// gated wraps handler in the CAPI-001 owner-session + CSRF middleware
	// behind the typed-envelope network gate — the composition every
	// authenticated control route below uses.
	gated := func(handler http.HandlerFunc) http.Handler {
		return networkGateJSON(allowedHost, auth.ownerSessionGate(handler))
	}

	// The provider registry (P2b-PROV-001) is empty this phase — no
	// adapter registers until PROV-005/PROV-007 — so DerivedCapabilities
	// correctly reports zero capabilities for every catalog entry. It is
	// constructed here, at this composition point, rather than plumbed
	// through ControlMux's signature, to keep this unit's boot ripple to
	// zero call-site changes. It is shared with the OAuth handler below
	// (P2b-PROV-006) rather than each building its own — one registry
	// per process, exactly like every other composition-root singleton.
	reg := providers.NewRegistry()
	// antigravity (P2b-PROV-007) is the first live OAuth adapter this
	// registry can hold — registered only when its confidential-client
	// env vars are both configured; see registerAntigravityIfConfigured's
	// doc comment for why its error is safely discardable here.
	_ = registerAntigravityIfConfigured(reg)
	providersHandler := NewProvidersHandler(reg)
	mux.Handle("/api/control/v1/providers", gated(providersHandler.ServeList))
	mux.Handle("/api/control/v1/providers/{id}", gated(providersHandler.ServeGet))

	// GET /jobs/{job_id} (P2b-JOBS-001, 09 §3.12) is the single canonical
	// async-job status surface — no per-resource status route exists or
	// may be added alongside it.
	jobsHandler := NewJobsHandler(db)
	mux.Handle("/api/control/v1/jobs/{job_id}", gated(jobsHandler.ServeGet))

	// OAuth enrollment framework (P2b-PROV-006) + reauthentication
	// staging (P2b-PROV-008): begin/reauth-begin are owner-session +
	// CSRF gated like every other mutating control route; callback and
	// status are network-gated ONLY — the provider's redirect and a
	// status poller both carry no owner session/CSRF, and the
	// transaction_id itself is the unguessable capability token the
	// status endpoint relies on. reg gains a live antigravity OAuth
	// adapter above when configured (P2b-PROV-007); every OTHER route
	// here is exercised in tests against a fake adapter registered
	// directly into a Registry, not through this shared one.
	oauthTxRepo := storage.NewOAuthTransactionRepo(db)
	accountRepo := storage.NewAccountRepo(db)
	oauthService := application.NewOAuthEnrollmentService(
		oauthTxRepo, storage.NewEnrollmentRepo(db), accountRepo,
		storage.NewAccountCredentialRepo(db), storage.NewReauthRepo(db),
		kr, newOAuthTransactionID, nil,
	)
	oauthHandler := NewOAuthHandler(oauthService, reg, oauthTxRepo, accountRepo, allowedHost)
	mux.Handle("/api/control/v1/providers/{id}/oauth/begin", gated(oauthHandler.ServeBegin))
	mux.Handle("/api/control/v1/oauth/{provider}/callback", networkGate(allowedHost, http.HandlerFunc(oauthHandler.ServeCallback)))
	mux.Handle("/api/control/v1/oauth/{transaction_id}/status", networkGateJSON(allowedHost, http.HandlerFunc(oauthHandler.ServeStatus)))
	mux.Handle("/api/control/v1/accounts/{id}/reauth/begin", gated(oauthHandler.ServeReauthBegin))

	mux.Handle("/", networkGate(allowedHost, spa))
	return mux
}
