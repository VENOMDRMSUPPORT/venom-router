package httpapi

import (
	"context"
	"net/http"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/application"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/execution"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/intelligence"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/observability"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
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
// ControlMuxOption tunes ControlMux without changing its (widely-called)
// positional signature — existing four-arg call sites compile unchanged.
type ControlMuxOption func(*controlMuxOptions)

type controlMuxOptions struct {
	omitPublicRoutes bool
	bind             string
	dataPlaneBind    string
	usabilityTrigger func(ctx context.Context, providerID, accountID string)
}

// WithUsabilityTrigger supplies the fast-lane hook (design 2026-08-05): fired
// once a discovery run completes successfully, so the account's freshly
// observed models get verified right away instead of waiting for the next
// scheduled sweep. Boot builds ONE UsabilityService and passes its
// VerifyAccount both here AND to the scheduler's Tick, so the fast lane and
// the periodic sweep always share the same composition root. Absent (nil,
// every pre-existing four-arg ControlMux call site), the discovery handler's
// fast lane stays disabled — exactly like a nil discoveryTrigger today.
func WithUsabilityTrigger(fn func(ctx context.Context, providerID, accountID string)) ControlMuxOption {
	return func(o *controlMuxOptions) { o.usabilityTrigger = fn }
}

// WithoutPublicRoutes tells ControlMux NOT to mount the vk-gated /v1/* public
// surface on the control listener. Boot passes this ONLY when a separate
// data-plane bind is configured, so the public /v1 API lives solely on that
// second, public-only listener and the control listener serves control routes
// alone (01 §6b: "each serves only its own surface"). In the default
// shared-listener case the option is absent and /v1/* is mounted here, behind
// the same loopback + Host-allowlist gate as every control route but gated by
// vk auth instead of the owner session.
func WithoutPublicRoutes() ControlMuxOption {
	return func(o *controlMuxOptions) { o.omitPublicRoutes = true }
}

// WithEffectiveBinds supplies the BOOT-RESOLVED listen addresses so
// GET /settings can report them read-only under `effective_config`
// (P6-CAPI-001, 01 §6b). Boot is the only place that knows them: they come
// from config.Load's default -> env -> flag precedence, and this package must
// never read the environment itself (forbidigo confines that to
// internal/config and internal/platform).
//
// When the option is absent, `bind` falls back to allowedHost — which IS
// cfg.Bind at the production call site — and `data_plane_bind` reports null,
// config.Config.DataPlaneBind's documented default meaning "the public /v1 API
// shares the control listener". Both fallbacks are the true values for a
// default install, so an omitted option reports honestly rather than
// fabricating.
func WithEffectiveBinds(bind, dataPlaneBind string) ControlMuxOption {
	return func(o *controlMuxOptions) {
		o.bind = bind
		o.dataPlaneBind = dataPlaneBind
	}
}

func ControlMux(allowedHost string, spa http.Handler, db *storage.DB, kr *secrets.Keyring, opts ...ControlMuxOption) http.Handler {
	var o controlMuxOptions
	for _, opt := range opts {
		opt(&o)
	}

	if o.bind == "" {
		o.bind = allowedHost
	}

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

	// The provider registry (P2b-PROV-001) is constructed here, at this
	// composition point, rather than plumbed through ControlMux's
	// signature, to keep boot ripple to zero call-site changes. It is
	// shared with the OAuth handler below (P2b-PROV-006) rather than each
	// building its own — one registry per request path, built from the
	// SAME registration list the background ticks use
	// (newProviderRegistry, provider_registry.go).
	reg := newProviderRegistry()

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
	settingsRepo := storage.NewSettingsRepo(db)
	// ops is the SINGLE resolver every owner-configurable operational knob is
	// read through (staleness window, probe caps). It is shared by the
	// accounts projection and the probe handler below rather than each
	// constructing its own, so there is exactly one definition of "the owner's
	// current operational settings" in the process.
	ops := newOperationalSettings(settingsRepo)
	settingsHandler := NewSettingsHandler(settingsRepo, audit, nil, newEffectiveConfig(o.bind, o.dataPlaneBind))
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
	// GET /callback is the REGISTERED OAuth redirect target — the
	// `{origin}/callback` shape every non-fixed-redirect provider's public
	// client allows (legacy 2026-08-03). The handler resolves the provider
	// from the `state` via the transaction row, so this one route serves
	// claude-code, clinepass, and antigravity alike. The provider-specific
	// path below is kept for the status/gating contract and direct
	// navigation; the provider's redirect itself always targets /callback.
	mux.Handle("/callback", networkGate(allowedHost, http.HandlerFunc(oauthHandler.ServeCallback)))
	mux.Handle("/api/control/v1/oauth/{provider}/callback", networkGate(allowedHost, http.HandlerFunc(oauthHandler.ServeCallback)))
	mux.Handle("/api/control/v1/oauth/{transaction_id}/status", networkGateJSON(allowedHost, http.HandlerFunc(oauthHandler.ServeStatus)))
	// POST /oauth/complete is the manual-code completion leg for providers
	// whose client never redirects back (providers.RequiresManualCode —
	// claude-code's hosted code page). It is owner-session + CSRF gated like
	// every mutating control route.
	mux.Handle("/api/control/v1/oauth/complete", gated(oauthHandler.ServeCompleteCode))
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
	enrollmentHandler := NewEnrollmentHandler(connectService, reg, fundingRepo, accountRepo, idem, audit)
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
	// quotaWindowRepo (P3b-CAPI-QUOTAREAD, enables P3b-UI-001) is shared
	// with the quota-refresh route further below rather than each building
	// its own — same one-repo-per-underlying-table pattern as accountRepo/
	// fundingRepo/credentialRepo above.
	quotaWindowRepo := storage.NewQuotaWindowRepo(db, nil, nil)
	// WithProviderRegistry wires the live HealthAdapter lookup into
	// POST /accounts/{id}/health — a registered provider (opencode-zen)
	// now gets a real probe instead of the P2b default-unknown placeholder.
	accountsHandler := NewAccountsHandler(accountRepo, credentialRepo, fundingRepo, quotaWindowRepo, credentialService, ops, audit, nil, newOAuthTransactionID).WithProviderRegistry(reg)
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
	mux.Handle("PATCH /api/control/v1/accounts/{id}", gated(accountsHandler.ServeSetLabel))
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
	// probeRunRepo is constructed here (rather than down by the probe route
	// below, where it used to live) so ModelsHandler can be wired with the
	// SAME instance via WithProbeRuns (task-5: capability provenance) —
	// there is exactly one probe_runs repo in this composition, shared by
	// both the read model and the probe/certification routes.
	probeRunRepo := storage.NewProbeRunRepo(db, nil, intelligence.DefaultProbeSafetyPolicy().ContextProbeCooldown)
	modelsHandler := NewModelsHandler(catalogRepo, nil).WithProbeRuns(probeRunRepo)
	mux.Handle("/api/control/v1/models", gated(modelsHandler.ServeModels))
	mux.Handle("/api/control/v1/offerings", gated(modelsHandler.ServeOfferings))

	// Canonical quality benchmark (P6-CAPI-001, 04 §3/§5, 09 §3.12): POST
	// /models/{id}/benchmark, async 202 + the canonical shared job surface.
	// The QualityIndex seam is nil here for the same reason the metadata
	// registry seam is: no unit has yet wired a real analysis-leaderboard HTTP
	// client (04 §2b's pipeline B is owner-enabled and still unwired). A nil
	// seam behaves as a leaderboard that always misses, so the endpoint
	// completes its job honestly WITHOUT writing a rating — never a fabricated
	// one. Wiring a real leaderboard client is a later unit's work and
	// requires no change here beyond passing it in.
	benchmarkHandler := NewBenchmarkHandler(
		catalogRepo, storage.NewJobRepo(db), storage.NewSettingsRepo(db),
		nil, audit, newOAuthTransactionID, nil,
	)
	mux.Handle("/api/control/v1/models/{id}/benchmark", gated(benchmarkHandler.ServeBenchmark))

	// Tier policy read (P6-CAPI-EXTRA, enables P6-UI-003, 05 §1/§8.4): GET
	// /routing/policy. It takes no repo at all — the three tier policies come
	// from internal/routing's own validated table, so there is nothing to wire
	// but the gate. READ-ONLY by design: 05 §8.4 defers owner weight tuning past
	// V1, so no PUT exists here or anywhere.
	mux.Handle("/api/control/v1/routing/policy", gated(NewRoutingPolicyHandler().ServePolicy))

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
	// Auto-discovery on connect: a successful enrollment fires a best-effort
	// background discovery through this same handler (wired here because the
	// DiscoveryHandler is constructed after the EnrollmentHandler above).
	enrollmentHandler.SetDiscoveryTrigger(discoveryHandler)
	oauthHandler.SetDiscoveryTrigger(discoveryHandler)
	// Fast-lane usability verification (design 2026-08-05): wired only when
	// the caller supplied one (boot always does; some tests deliberately
	// don't, to exercise the trigger-absent no-op path).
	if o.usabilityTrigger != nil {
		discoveryHandler.SetUsabilityTrigger(o.usabilityTrigger)
	}

	// Probe (P3c-DB-EXTRA/CAPI-001, 09 §3.8): POST /offerings/{id}/probe,
	// async 202 + the canonical shared job surface, exactly like
	// discovery/quota-refresh above. certRepo/probeRunRepo are shared with
	// the certification-read route (DiscoveryHandler.WithProbeRuns)
	// immediately below, rather than each building its own.
	//
	// Probe transport wiring (P3c-EXEC-001): probeTransports is a DATA
	// lookup (never a slug switch) from provider id to the
	// execution.InferenceTransport that serves it — today, only
	// opencode-zen (the one provider with both a base URL and a live
	// API-key credential adapter). probeBaseURLs carries the FULL base
	// each entry's transport needs (providers.OpenCodeZenBaseURL + "/v1":
	// this transport's fixed "/chat/completions" suffix convention needs
	// the version segment folded in, independent of whatever base_url the
	// providers table stores for that provider's OTHER adapters). Every
	// other provider is simply absent from both maps, so Available()
	// reports it unavailable and ServeProbe refuses 409 probe_unsupported
	// before any job row is ever created — fail-closed, never a
	// fabricated capability.
	// Both maps are DERIVED from the two single-source tables the request path
	// also composes from (liveTransportImpls + liveProviderBaseURLs in
	// chatcompletions.go) and from each provider's catalog-declared transport
	// KIND — never a second hand-written literal that could silently disagree
	// with the request path. Resolution is by typed capability
	// (Definition.Transport -> TransportType), never a slug switch.
	probeHTTPClient := &http.Client{Timeout: execution.DefaultOpenAICompatibleTimeout}
	probeImpls := liveTransportImpls(probeHTTPClient, reg)
	probeTransports := make(map[string]execution.InferenceTransport)
	probeBaseURLs := make(map[string]string)
	for id, base := range liveProviderBaseURLs() {
		def, registered := reg.Definition(id)
		if !registered {
			continue // not registered in this composition: absent from both maps
		}
		impl, wired := probeImpls[execution.TransportType(def.Transport)]
		if !wired {
			continue // no implementation for its declared kind: absent, fail closed
		}
		probeTransports[string(id)] = impl
		probeBaseURLs[string(id)] = base
	}
	certRepo := storage.NewCertificationRepo(db, nil)
	// probeRunRepo was already constructed earlier, alongside catalogRepo,
	// so it could be wired into modelsHandler via WithProbeRuns — reused
	// here rather than built a second time.
	certAuditor := newCertificationAuditorAdapter(audit)
	certDriver, _ := intelligence.NewCertificationDriver(certRepo, certAuditor, intelligence.DefaultProbeRetryBudget, nil)
	probeReserver := newProbeReserverAdapter(storage.NewQuotaReservationRepo(db, nil))
	probeTransportAdapterInstance := newProbeTransportAdapter(probeTransports, probeBaseURLs, credentialRepo, credentialService)
	probeHandler := buildProbeHandler(
		accountRepo, credentialRepo, catalogRepo, jobRepo, certRepo, probeRunRepo,
		probeReserver, probeTransportAdapterInstance, certDriver, discoveryRepo, ops, audit, idem,
	)
	mux.Handle("/api/control/v1/offerings/{id}/probe", gated(probeHandler.ServeProbe))

	// GET /offerings/{id}/certification additionally reports the
	// probe-execution dimension once probeRunRepo is wired in (see
	// DiscoveryHandler.WithProbeRuns's own doc comment for why this is a
	// copy-returning method rather than a NewDiscoveryHandler parameter).
	discoveryHandlerWithProbes := discoveryHandler.WithProbeRuns(probeRunRepo)
	mux.Handle("/api/control/v1/offerings/{id}/certification", gated(discoveryHandlerWithProbes.ServeCertification))

	// Routing-admission census (P6-CAPI-EXTRA, enables P6-UI-012, 04 §5): GET
	// /certifications/review — "the review count grouped by reason". It shares
	// certRepo with the probe/certification routes above rather than building its
	// own, and reports which admission reasons it could NOT evaluate rather than
	// counting them as zero (see reviewcensus.go's header).
	mux.Handle("/api/control/v1/certifications/review", gated(NewReviewCensusHandler(certRepo).ServeCensus))

	// Consumption read model (P6-CAPI-EXTRA-2, enables P6-UI-005, 05 §4/§7): GET
	// /usage over the M7 usage_records table the public data plane writes. Read-only
	// — the WRITE path stays in the chat-completions handler, which is the only
	// place that knows a request's terminal outcome. Every NULL metric is reported
	// as unknown with its own unknown-count rather than summed as 0; see
	// usageread.go's header.
	mux.Handle("/api/control/v1/usage", gated(NewUsageHandler(storage.NewUsageRecordRepo(db)).ServeUsage))

	// Quota refresh (P3b-CAPI-001, 09 §2 "Refresh quota snapshot"): POST
	// /accounts/{id}/quota, async (202 + the canonical shared job
	// surface) exactly like discovery above. quotaLifecycleRepo backs
	// reconciliationRepo's SyncQuotaWindows call (the ONLY write path this
	// handler drives); reconciliationRepo has no audit sink here (the
	// handler's own auditEmitter covers the refresh call itself, and
	// SyncQuotaWindows's transition-audit vocabulary belongs to the
	// worker paths in quotaworkers.go, not this synchronous trigger).
	quotaLifecycleRepo := storage.NewQuotaLifecycleRepo(db, nil, nil)
	reconciliationRepo := storage.NewReconciliationRepo(db, nil, quota.DefaultReconciliationPolicy(), quotaLifecycleRepo, nil)
	quotaHandler := NewQuotaHandler(accountRepo, credentialRepo, jobRepo, reconciliationRepo, reg, credentialService, audit, idem, newOAuthTransactionID, nil)
	mux.Handle("/api/control/v1/accounts/{id}/quota", gated(quotaHandler.ServeQuotaRefresh))
	oauthHandler.SetQuotaTrigger(quotaHandler)

	// Reconciliation diagnostics (P3b-CAPI-002, 09 §2 / 05 §4 "Manual
	// recovery"): GET /diagnostics/reconciliation (read model) and POST
	// /diagnostics/reconciliation/{reservation_id} (resync /
	// accept_estimate). Shares reconciliationRepo/quotaLifecycleRepo with
	// the quota-refresh route above — both are the same underlying
	// five-state reservation lifecycle.
	//
	// Route explanations (P6-CAPI-001, 09 §3.9 / 05 §7) join the SAME handler
	// via WithRoutes: GET /diagnostics/routes (paginated list) and GET
	// /diagnostics/routes/{request_id}. The reader is constructed over
	// db.Conn() exactly as chatcompletions.go constructs the RouteRecorder
	// that writes these same two tables — one connection pool, one table pair,
	// a writer and a reader.
	diagnosticsHandler := NewDiagnosticsHandler(reconciliationRepo, quotaLifecycleRepo, audit).
		WithRoutes(observability.NewRouteReader(db.Conn()))
	mux.Handle("/api/control/v1/diagnostics/reconciliation", gated(diagnosticsHandler.ServeList))
	mux.Handle("/api/control/v1/diagnostics/reconciliation/{reservation_id}", gated(diagnosticsHandler.ServeAction))
	mux.Handle("/api/control/v1/diagnostics/routes", gated(diagnosticsHandler.ServeRoutes))
	mux.Handle("/api/control/v1/diagnostics/routes/{request_id}", gated(diagnosticsHandler.ServeRouteExplanation))

	// Venom API-key management (P5-CAPI-001, 09 §3.11): POST/GET /keys and
	// DELETE /keys/{id}, owner-session + CSRF gated like every other mutating
	// control route, and Idempotency-Key aware on create via the shared `idem`
	// store. The DELETE is a method-specific pattern (like /accounts/{id}) so it
	// reaches ServeDelete rather than the method-less collection handler.
	keysHandler := NewKeysHandler(storage.NewAPIKeyRepo(db), idem, audit, nil, newOAuthTransactionID)
	mux.Handle("/api/control/v1/keys", gated(keysHandler.ServeCollection))
	mux.Handle("DELETE /api/control/v1/keys/{id}", gated(keysHandler.ServeDelete))

	// Public data-plane surface (P5-PAPI-001, 01 §6b): the vk-gated /v1/*
	// routes. In the default local-only case they share THIS control listener,
	// behind the identical loopback + Host-allowlist network gate (the `outer`
	// wrapper below) but authenticated by a Venom API key instead of the owner
	// session — never owner-session gated, and an owner session alone never
	// authenticates /v1. When Boot opens a separate data-plane listener it
	// passes WithoutPublicRoutes() here so /v1 lives only there. The vk
	// authenticator uses the wall clock in production; tests that need a
	// deterministic RPM clock exercise vkAuthenticator / PublicMux directly.
	if !o.omitPublicRoutes {
		vk := newVKAuthenticator(storage.NewAPIKeyRepo(db), nil)
		chat := buildChatCompletionsHandler(db, kr, reg)
		registerPublicRoutes(mux, func(h http.Handler) http.Handler { return networkGate(allowedHost, h) }, vk, chat)
	}

	mux.Handle("/", networkGate(allowedHost, spa))
	// Per-path per-IP ingress limiter (P5-PAPI-005, 05 §6) wraps the WHOLE mux,
	// so it runs before the network gate, auth, and any handler — an ingress
	// rejection never reaches the engine or quota. It is independent of the
	// per-key RPM enforced inside vk auth on /v1/*; a request must satisfy both.
	return newIngressLimiter(0, 0, nil).Middleware(mux)
}

// buildProbeHandler is the ONE place the probe handler's dependencies are
// assembled, extracted from ControlMux's body so the wiring itself is
// testable (P6-CAPI-001).
//
// The extraction is deliberate and load-bearing: the whole point of making
// the probe caps owner-configurable is that ops.probePolicy — not
// intelligence.DefaultProbeSafetyPolicy() — is what reaches the admission
// gate. While that expression lived inline in ControlMux, replacing it with
// the bare defaults left the entire package green, i.e. the setting could
// silently go inert. It now has a test of its own.
func buildProbeHandler(
	accounts *storage.AccountRepo,
	credentials *storage.AccountCredentialRepo,
	catalog *storage.CatalogRepo,
	jobs *storage.JobRepo,
	certs *storage.CertificationRepo,
	probeRuns *storage.ProbeRunRepo,
	reserver intelligence.ProbeReserver,
	transport probeTransport,
	driver *intelligence.CertificationDriver,
	discovery nativeContextWriter,
	ops *operationalSettings,
	audit *auditEmitter,
	idem *idempotencyStore,
) *ProbeHandler {
	return NewProbeHandler(
		accounts, credentials, catalog, jobs, certs, probeRuns,
		reserver, transport, driver, discovery, ops.probePolicy,
		audit, idem, newOAuthTransactionID, nil, nil,
	)
}
