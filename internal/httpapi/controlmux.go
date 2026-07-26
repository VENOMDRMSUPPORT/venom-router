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
	// opencode-zen (P2b-PROV-005/CAPI-003) is the first live API-key
	// adapter registered into this shared registry, over the real HTTP
	// seams built in opencode_zen_seams.go — always registered
	// unconditionally (unlike antigravity, opencode-zen needs no
	// confidential-client env vars); see registerOpenCodeZen's doc
	// comment for why its error is safely discardable here.
	_ = registerOpenCodeZen(reg)

	// audit is the shared P2b-OBS-001 emitter every mutating control
	// route below records exactly one audit_event through (log is nil
	// here, so it defaults to observability.Default() — a future unit
	// may thread a boot-level logger through ControlMux's own signature
	// instead).
	audit := newAuditEmitter(db, nil)
	providersHandler := NewProvidersHandler(reg)
	mux.Handle("/api/control/v1/providers", gated(providersHandler.ServeList))
	mux.Handle("/api/control/v1/providers/{id}", gated(providersHandler.ServeGet))

	// GET /jobs/{job_id} (P2b-JOBS-001, 09 §3.12) is the single canonical
	// async-job status surface — no per-resource status route exists or
	// may be added alongside it.
	jobsHandler := NewJobsHandler(db)
	mux.Handle("/api/control/v1/jobs/{job_id}", gated(jobsHandler.ServeGet))

	// Owner settings (P2b-CAPI-005, 07 §2.3/§3): server-side theme/density
	// persistence — the single GET/PUT surface the app shell (UI-001)
	// restores from at boot. Config only (no secret, no per-provider
	// state); wired httpapi<->storage directly like /jobs, with NO
	// accounts/application port (settings are configuration, not account-
	// domain orchestration). Owner-session + CSRF gated like every other
	// authenticated control route.
	settingsHandler := NewSettingsHandler(storage.NewSettingsRepo(db), audit, nil)
	mux.Handle("/api/control/v1/settings", gated(settingsHandler.ServeSettings))
	// PUT /settings/enrichment (P3a-CAPI-003): a distinct, more-specific
	// pattern than the method-less "/settings" above — Go 1.22's ServeMux
	// matches the more specific literal path first, so this does not
	// shadow (or get shadowed by) the settings route above.
	mux.Handle("/api/control/v1/settings/enrichment", gated(settingsHandler.ServeEnrichment))

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
	oauthHandler := NewOAuthHandler(oauthService, reg, oauthTxRepo, accountRepo, allowedHost, audit)
	mux.Handle("/api/control/v1/providers/{id}/oauth/begin", gated(oauthHandler.ServeBegin))
	mux.Handle("/api/control/v1/oauth/{provider}/callback", networkGate(allowedHost, http.HandlerFunc(oauthHandler.ServeCallback)))
	mux.Handle("/api/control/v1/oauth/{transaction_id}/status", networkGateJSON(allowedHost, http.HandlerFunc(oauthHandler.ServeStatus)))
	mux.Handle("/api/control/v1/accounts/{id}/reauth/begin", gated(oauthHandler.ServeReauthBegin))

	// API-key enrollment (P2b-CAPI-003): POST /providers/{id}/accounts,
	// owner-session + CSRF gated and Idempotency-Key aware like every
	// other mutating control route. newOAuthTransactionID is reused as
	// ConnectService's IDGenerator — it is already a generic,
	// high-entropy random-id minter with no OAuth-specific behavior,
	// despite its name (see its own doc comment).
	connectService := application.NewConnectService(storage.NewEnrollmentRepo(db), accountRepo, kr, newOAuthTransactionID, nil)
	fundingRepo := storage.NewFundingEvidenceRepo(db)
	idem := newIdempotencyStore()
	enrollmentHandler := NewEnrollmentHandler(connectService, reg, fundingRepo, idem, audit)
	mux.Handle("/api/control/v1/providers/{id}/accounts", gated(enrollmentHandler.ServeConnect))

	// Account lifecycle (P2b-CAPI-004): the GET list/detail projections,
	// the credential reveal (the only plaintext-returning endpoint, behind
	// the reverify-freshness gate), the funding owner override, and the
	// connection/health lifecycle mutations. credentialRepo is shared with
	// the OAuth path above (already constructed); credentialService is the
	// ONE decrypt-for-reveal seam (CredentialService.Use), wired here for
	// the first time. Every route below is owner-session + CSRF gated via
	// `gated`, exactly like every other authenticated control route.
	credentialRepo := storage.NewAccountCredentialRepo(db)
	credentialService := application.NewCredentialService(credentialRepo, kr, nil)
	accountsHandler := NewAccountsHandler(accountRepo, credentialRepo, fundingRepo, credentialService, audit, nil, newOAuthTransactionID)
	mux.Handle("/api/control/v1/accounts", gated(accountsHandler.ServeList))
	mux.Handle("/api/control/v1/accounts/{id}", gated(accountsHandler.ServeGet))
	// DELETE /accounts/{id} is the soft-disconnect route (09 §2). It is
	// registered as a method-specific pattern so it reaches ServeDisconnect;
	// the method-less /accounts/{id} above only ever serves GET (it 405s any
	// other method), so without this line a DELETE fell through to ServeGet
	// and 405'd — ServeDisconnect was unreachable dead code. Go 1.22's
	// ServeMux treats "DELETE /accounts/{id}" as more specific than the
	// method-less pattern, so the two do not conflict.
	mux.Handle("DELETE /api/control/v1/accounts/{id}", gated(accountsHandler.ServeDisconnect))
	mux.Handle("/api/control/v1/accounts/{id}/reveal", gated(accountsHandler.ServeReveal))
	mux.Handle("/api/control/v1/accounts/{id}/funding", gated(accountsHandler.ServeFunding))
	mux.Handle("/api/control/v1/accounts/{id}/stop", gated(accountsHandler.ServeStop))
	mux.Handle("/api/control/v1/accounts/{id}/resume", gated(accountsHandler.ServeResume))
	mux.Handle("/api/control/v1/accounts/{id}/health", gated(accountsHandler.ServeHealth))
	// /providers/{id}/sync is a provider-scoped best-effort refresh; it is
	// registered alongside the provider routes but served by the same
	// accounts handler (it iterates a provider's accounts). It does NOT
	// collide with /providers/{id} because ServeMux matches the more
	// specific /providers/{id}/sync path first.
	mux.Handle("/api/control/v1/providers/{id}/sync", gated(accountsHandler.ServeProviderSync))

	// Effective-offering read model (P3a-CAPI-001, 09 §2 / 04 §3):
	// GET /models and GET /offerings both read storage.CatalogRepo and
	// render intelligence.Project's ONE shared projection — reads, so no
	// audit event is emitted (mirrors GET /accounts and GET /settings).
	// catalogRepo is shared with the discovery/certification routes below
	// rather than each building its own.
	catalogRepo := storage.NewCatalogRepo(db)
	modelsHandler := NewModelsHandler(catalogRepo, nil)
	mux.Handle("/api/control/v1/models", gated(modelsHandler.ServeModels))
	mux.Handle("/api/control/v1/offerings", gated(modelsHandler.ServeOfferings))

	// Discovery + certification read (P3a-CAPI-002): POST
	// /accounts/{id}/discover (async, 202 + the canonical shared job
	// surface) and GET /offerings/{id}/certification. discoveryRepo
	// implements BOTH intelligence.GenerationAllocator and
	// intelligence.SnapshotApplier structurally; credentialService (already
	// constructed above for reveal) is reused as the
	// intelligence.CredentialLeaser via its Use method; idem is the SAME
	// shared idempotency store enrollment already uses.
	discoveryRepo := storage.NewDiscoveryRepo(db, newOAuthTransactionID)
	jobRepo := storage.NewJobRepo(db)
	discoveryHandler := NewDiscoveryHandler(accountRepo, credentialRepo, catalogRepo, jobRepo, discoveryRepo, reg, credentialService, audit, idem, newOAuthTransactionID, nil)
	mux.Handle("/api/control/v1/accounts/{id}/discover", gated(discoveryHandler.ServeDiscover))
	mux.Handle("/api/control/v1/offerings/{id}/certification", gated(discoveryHandler.ServeCertification))

	mux.Handle("/", networkGate(allowedHost, spa))
	return mux
}
