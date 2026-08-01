package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/httpapi"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/httpui"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/observability"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/platform"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/secrets"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// StartupStage names one step of the mandated fail-closed startup order
// (01 §2). Boot logs each stage, in order, immediately before performing
// it. Tests assert ordering from that log rather than from internal
// state — the same way a real boot's ordering would be audited.
type StartupStage string

const (
	StageValidateEmbeddedAssets StartupStage = "validate_embedded_assets"
	StageAcquireLock            StartupStage = "acquire_lock"
	StageLoadKeyring            StartupStage = "load_keyring"
	StageOpenDatabase           StartupStage = "open_database"
	StageMigrateDatabase        StartupStage = "migrate_database"
	StageReconcileKeyring       StartupStage = "reconcile_keyring"
	StageBuildRepositories      StartupStage = "build_repositories"      // stub until P2b+
	StageBuildProviderRegistry  StartupStage = "build_provider_registry" // stub until P2b+
	StageBuildServices          StartupStage = "build_services"          // stub until P2b+
	StageMountHTTPMux           StartupStage = "mount_http_mux"
	StageListen                 StartupStage = "listen"
)

// BootConfig bundles Boot's inputs.
type BootConfig struct {
	// Bind is the TCP address to listen on (e.g. from config.Config.Bind).
	Bind string
	// DataPlaneBind is the OPTIONAL public data-plane bind (config.Config.
	// DataPlaneBind, 01 §6b). Empty, or equal to Bind, means the public /v1
	// API shares the control listener. When set and different from Bind, Boot
	// opens a SECOND, public-only listener there serving only /v1/*. Unlike
	// Bind it MAY be non-loopback — it is the one surface the owner may expose
	// off-host, so isLoopbackBind is deliberately NOT applied to it.
	DataPlaneBind string
	// Logger receives one "startup stage" record per stage, in order. If
	// nil, observability.Default() is used.
	Logger *observability.Logger
	// CiphertextStore is consulted at the reconcile_keyring stage
	// (P1-SEC-004). If nil, an empty store is used: no stored
	// ciphertext exists yet, since M2's credential table is P2b, so
	// reconciliation trivially passes with zero rows to check. Exposed
	// here (rather than hardcoded) so tests can inject a store with a
	// deliberately mismatched key_id reference to prove the fail-closed
	// path without needing real credential rows.
	CiphertextStore secrets.CiphertextRefStore
	// SPAHandler is the dashboard SPA served on the control plane at "/"
	// (P2a-UI-001). If nil, Boot builds it from internal/httpui's embedded
	// dashboard via httpui.New(), which fails closed when the embed is the
	// un-built placeholder rather than a real dashboard build. Exposed here
	// (rather than always calling httpui.New()) so tests can inject a fake
	// handler and not depend on a real frontend build — the real embed is
	// covered by internal/httpui's own tests and the build+embed pipeline,
	// and exercised in CI once the dashboard build is wired in (P2a-DS-004).
	// Production (cmd/venom) leaves this nil, so behavior there is unchanged.
	SPAHandler http.Handler
	// SchedulerInterval is the background scheduler's tick interval
	// (P3c-JOBS-001 GOVERNOR DECISION: "a constructor parameter with a
	// documented default is the whole contract" — no config-file surface).
	// <= 0 uses DefaultSchedulerInterval.
	SchedulerInterval time.Duration
}

// DefaultSchedulerInterval is the background scheduler's default tick
// interval (P3c-JOBS-001 GOVERNOR DECISION).
const DefaultSchedulerInterval = 30 * time.Second

// SchedulerTick is one named background sweep the Scheduler drives.
type SchedulerTick struct {
	Name string
	Run  func(ctx context.Context) error
}

// tickerSource abstracts time.NewTicker so tests can inject a fake,
// channel-driven ticker instead of depending on real wall-clock sleeps.
type tickerSource interface {
	C() <-chan time.Time
	Stop()
}

type realTicker struct{ t *time.Ticker }

func (r realTicker) C() <-chan time.Time { return r.t.C }
func (r realTicker) Stop()               { r.t.Stop() }

func newRealTicker(d time.Duration) tickerSource { return realTicker{t: time.NewTicker(d)} }

// Scheduler runs a fixed set of named background ticks: once immediately
// (crash recovery — any tick left mid-work by a prior crash gets a fresh
// pass right away) and then on a fixed interval, until its context is
// cancelled. A single tick's error is logged and never aborts the other
// ticks in the same pass; a tick that panics is recovered and logged
// rather than crashing the process.
type Scheduler struct {
	ticks     []SchedulerTick
	interval  time.Duration
	logger    *observability.Logger
	newTicker func(time.Duration) tickerSource
	wg        sync.WaitGroup
}

// NewScheduler builds a Scheduler. interval <= 0 uses
// DefaultSchedulerInterval; logger nil uses observability.Default().
func NewScheduler(interval time.Duration, logger *observability.Logger, ticks ...SchedulerTick) *Scheduler {
	if interval <= 0 {
		interval = DefaultSchedulerInterval
	}
	if logger == nil {
		logger = observability.Default()
	}
	return &Scheduler{ticks: ticks, interval: interval, logger: logger, newTicker: newRealTicker}
}

// Start runs every tick once immediately, then launches a background
// goroutine that re-runs every tick on s.interval until ctx is
// cancelled. Start itself never blocks on the boot-time pass — it runs
// inside the same background goroutine, so a slow tick never delays the
// rest of startup.
func (s *Scheduler) Start(ctx context.Context) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.runAllOnce(ctx)

		ticker := s.newTicker(s.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C():
				s.runAllOnce(ctx)
			}
		}
	}()
}

// Wait blocks until the background goroutine Start launched has fully
// exited. Callers cancel the context passed to Start first.
func (s *Scheduler) Wait() {
	s.wg.Wait()
}

func (s *Scheduler) runAllOnce(ctx context.Context) {
	for _, tick := range s.ticks {
		s.runOne(ctx, tick)
	}
}

func (s *Scheduler) runOne(ctx context.Context, tick SchedulerTick) {
	defer func() {
		if rec := recover(); rec != nil {
			s.logger.Error("scheduler tick panicked",
				observability.String("tick", tick.Name),
				observability.String("panic", fmt.Sprintf("%v", rec)),
			)
		}
	}()
	if err := tick.Run(ctx); err != nil {
		s.logger.Error("scheduler tick failed",
			observability.String("tick", tick.Name),
			observability.Err(err),
		)
	}
}

// Server represents a successfully booted process: a mounted HTTP mux
// listening on a real address, plus everything the fail-closed startup
// sequence built. Boot returns a non-nil *Server only on full success —
// a failure at any earlier stage returns (nil, error) and never reaches
// the point of opening a listener.
type Server struct {
	Addr string

	db              *storage.DB
	lock            *Lock
	http            *http.Server
	ln              net.Listener
	dataHTTP        *http.Server // the separate public data-plane server, nil when /v1 shares the control listener
	dataLn          net.Listener
	keyring         *secrets.Keyring
	scheduler       *Scheduler
	cancelScheduler context.CancelFunc
}

// DataPlaneAddr is the resolved address of the separate public data-plane
// listener, or "" when the public /v1 API shares the control listener.
func (s *Server) DataPlaneAddr() string {
	if s.dataLn == nil {
		return ""
	}
	return s.dataLn.Addr().String()
}

// Shutdown gracefully stops the listener, stops the background
// scheduler (cancel, then wait for its in-flight tick to finish — this
// MUST happen before db.Close(), or a still-running tick would touch a
// closed *sql.DB), and releases what Boot acquired (the database handle,
// then the single-instance lock), in reverse order of acquisition.
func (s *Server) Shutdown(ctx context.Context) error {
	var errs []error
	if err := s.http.Shutdown(ctx); err != nil {
		errs = append(errs, fmt.Errorf("http shutdown: %w", err))
	}
	if s.dataHTTP != nil {
		if err := s.dataHTTP.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("data-plane http shutdown: %w", err))
		}
	}
	if s.cancelScheduler != nil {
		s.cancelScheduler()
		s.scheduler.Wait()
	}
	if err := s.db.Close(); err != nil {
		errs = append(errs, fmt.Errorf("db close: %w", err))
	}
	if err := s.lock.Release(); err != nil {
		errs = append(errs, fmt.Errorf("release lock: %w", err))
	}
	return errors.Join(errs...)
}

// Boot performs the composition root: the mandated fail-closed startup
// order from 01 §2, wiring already-approved pieces (platform's data-dir
// resolution, this package's single-instance lock, storage's SQLite open
// + migrate/integrity-verify, the observability logger, httpui's embedded
// dashboard SPA) together, then mounts the control-plane HTTP mux and
// starts listening.
//
// Any failure — including a migration/integrity failure from
// storage.Migrate — aborts before net.Listen is ever called, so no
// listener opens on a half-initialized process. Provider outages or
// empty tier pools are deliberately NOT modeled as startup failures here
// (01 §2) — they are runtime states, and this phase has no real provider
// registry yet regardless (see the stub stages below).
//
// internal/execution's dispatcher is intentionally NOT wired here:
// "build services" is a stub, exactly like "build provider registry" —
// wiring the real execution path is separate, later work.
func Boot(ctx context.Context, cfg BootConfig) (*Server, error) {
	logger := cfg.Logger
	if logger == nil {
		logger = observability.Default()
	}
	logStage := func(s StartupStage) {
		logger.Info("startup stage", observability.String("stage", string(s)))
	}

	// 1. Validate embedded assets: build the dashboard SPA handler from
	// internal/httpui's go:embed (P2a-UI-001). httpui.New() fails closed
	// if the embedded dist is the un-built placeholder rather than a
	// real dashboard build (`task dashboard:build-embed`) — no listener
	// may ever open behind a broken/empty SPA. The returned handler is
	// mounted later, at stage 9 (mount_http_mux).
	logStage(StageValidateEmbeddedAssets)
	spa := cfg.SPAHandler
	if spa == nil {
		built, buildErr := httpui.New()
		if buildErr != nil {
			return nil, fmt.Errorf("app: validate embedded assets: %w", buildErr)
		}
		spa = built
	}

	// 2. Acquire the single-instance lock before any keyring/DB creation.
	logStage(StageAcquireLock)
	lock, err := AcquireLock()
	if err != nil {
		return nil, fmt.Errorf("app: acquire lock: %w", err)
	}
	// From here on, every early-return path must release the lock so a
	// failed boot leaves no half-state.
	release := func() { _ = lock.Release() }

	// dataDir is resolved once, here, because load_keyring (stage 3, just
	// below) needs it — the keyring lives at <dataDir>/secrets/keyring.json
	// — and open_database (stage 4) reuses the same value rather than
	// re-resolving it.
	dataDir, err := platform.EnsureDataDir()
	if err != nil {
		release()
		return nil, fmt.Errorf("app: resolve data dir: %w", err)
	}

	// 3. Load/create the master keyring in memory (P1-SEC-001), sourcing
	// VENOM_ENCRYPTION_KEY via platform.EncryptionKeyOverride — this
	// package never reads the environment directly (forbidigo). A
	// missing/corrupt keyring is fail-closed: no listener may ever open
	// without a usable master key.
	logStage(StageLoadKeyring)
	envValue, envPresent := platform.EncryptionKeyOverride()
	kr, err := secrets.Load(dataDir, envValue, envPresent)
	if err != nil {
		release()
		return nil, fmt.Errorf("app: load keyring: %w", err)
	}
	// TODO(follow-up unit, out of scope for P1-SEC-004): if
	// kr.PendingRotation != nil here, a P1-SEC-003 rotation was
	// interrupted before its ciphertext re-wrap completed. Wiring
	// secrets.KeyringHolder.Resume into startup to finish it
	// automatically is deliberately not done by this unit — it lands in
	// a dedicated later step. In the meantime kr.Keys already holds both
	// the old and new key material, so Reconcile below still validates
	// whatever ciphertext already exists correctly; there is no
	// reconciliation gap.

	// 4. Open SQLite.
	logStage(StageOpenDatabase)
	db, err := storage.Open(dataDir)
	if err != nil {
		release()
		return nil, fmt.Errorf("app: open database: %w", err)
	}
	closeDB := func() { _ = db.Close() }

	// 4b. Run migrations, which includes P0-DB-002's PRAGMA
	// integrity_check and checksum-tamper guard. Any failure here aborts
	// before a listener ever opens — this is the fail-closed boundary
	// 01 §2 mandates ("any integrity failure aborts startup").
	logStage(StageMigrateDatabase)
	if _, err := storage.Migrate(ctx, db); err != nil {
		closeDB()
		release()
		return nil, fmt.Errorf("app: migrate database: %w", err)
	}

	// 5. Reconcile the keyring with the DB: validate every stored
	// ciphertext's key_id against kr BEFORE any listener may open
	// (P1-SEC-004, 01 §2/§8). No credential table exists yet (M2 is
	// P2b), so cfg.CiphertextStore defaults to an empty store and this
	// trivially passes with zero rows to check; tests can override it to
	// prove the fail-closed path.
	logStage(StageReconcileKeyring)
	ciphertextStore := cfg.CiphertextStore
	if ciphertextStore == nil {
		ciphertextStore = noCiphertextStore{}
	}
	if err := secrets.Reconcile(ctx, kr, ciphertextStore); err != nil {
		closeDB()
		release()
		return nil, fmt.Errorf("app: reconcile keyring: %w", err)
	}

	// 6-8. Repositories / provider registry / services. Repositories and
	// services remain stubs until P2b+ wires the execution path — this is
	// deliberately NOT internal/execution's dispatcher. The provider
	// registry stage now seeds the M2 providers table from the frozen
	// built-in catalog (P2b-PROV-002) so GET /providers has FK-consistent
	// rows to read; it is fail-closed like every other stage — a seed
	// error aborts boot before any listener opens.
	logStage(StageBuildRepositories)
	buildRepositoriesStub()
	logStage(StageBuildProviderRegistry)
	if err := storage.SeedProviders(ctx, db, providers.BuiltinCatalog(), time.Now()); err != nil {
		closeDB()
		release()
		return nil, fmt.Errorf("app: seed providers: %w", err)
	}
	logStage(StageBuildServices)
	buildServicesStub()

	// 9. Mount the HTTP mux. /health is httpapi's definitive liveness
	// surface (01 §6d): unauthenticated, outside /api/control/v1, behind
	// the loopback + Host-allowlist network gate. This replaces
	// P0-FND-007's original placeholder handler — there is exactly one
	// liveness surface. The dashboard SPA built at stage 1 joins it on
	// the same mux, behind the identical gate (P2a-UI-001, 01 §1/§3).
	logStage(StageMountHTTPMux)
	// separateDataPlane is true only when a distinct data-plane bind is
	// configured. In that case the public /v1 surface moves to its own
	// listener (below) and the control mux omits it, so each listener serves
	// only its own surface (01 §6b).
	separateDataPlane := cfg.DataPlaneBind != "" && cfg.DataPlaneBind != cfg.Bind
	var muxOpts []httpapi.ControlMuxOption
	if separateDataPlane {
		muxOpts = append(muxOpts, httpapi.WithoutPublicRoutes())
	}
	// Boot is the ONLY place that knows the resolved listen addresses (they
	// come from config.Load's default -> env -> flag precedence), so it hands
	// them to the mux for GET /settings to report read-only under
	// `effective_config` (P6-CAPI-001, 01 §6b). Without this the endpoint
	// would report a fallback rather than what the process is actually
	// listening on.
	muxOpts = append(muxOpts, httpapi.WithEffectiveBinds(cfg.Bind, cfg.DataPlaneBind))
	mux := httpapi.ControlMux(cfg.Bind, spa, db, kr, muxOpts...)

	// 10. Listen. The control plane must never bind off-host (01 §6a/§8,
	// P2b-CAPI-001): the network gate's loopback check only ever sees
	// requests that already reached a loopback-bound listener, so THIS
	// check is what actually keeps a misconfigured bind-all address
	// (e.g. ":8081" or a routable IP) from exposing the control plane at
	// all — fail closed before net.Listen, with no override flag.
	logStage(StageListen)
	if !isLoopbackBind(cfg.Bind) {
		closeDB()
		release()
		return nil, fmt.Errorf("app: listen on %q: %w", cfg.Bind, ErrNonLoopbackBind)
	}
	ln, err := net.Listen("tcp", cfg.Bind)
	if err != nil {
		closeDB()
		release()
		return nil, fmt.Errorf("app: listen on %q: %w", cfg.Bind, err)
	}
	httpServer := &http.Server{Handler: mux}
	go func() {
		_ = httpServer.Serve(ln)
	}()

	// 10b. Open the separate public data-plane listener when configured
	// (P5-PAPI-001, 01 §6b). It serves the PUBLIC-ONLY mux — /v1/* and nothing
	// else — and is the ONE listener permitted off-host, so isLoopbackBind is
	// deliberately NOT applied to it (the control bind above still is, with no
	// escape hatch). A failure to open it aborts boot like every other stage:
	// no half-open process where the control plane is up but the data plane
	// silently failed to bind.
	var dataHTTP *http.Server
	var dataLn net.Listener
	if separateDataPlane {
		dataLn, err = net.Listen("tcp", cfg.DataPlaneBind)
		if err != nil {
			_ = httpServer.Close()
			closeDB()
			release()
			return nil, fmt.Errorf("app: listen on data-plane %q: %w", cfg.DataPlaneBind, err)
		}
		dataHTTP = &http.Server{Handler: httpapi.PublicMux(db, kr, nil, nil)}
		go func() {
			_ = dataHTTP.Serve(dataLn)
		}()
	}

	// 11. Start the background scheduler (P3c-JOBS-001 GOVERNOR DECISION):
	// P3b's ReconcileTick/JanitorTick and P3c's DrainTick/RecertifyTick/
	// ReclaimTick, all previously fully tested but never scheduled by
	// anything — this closes that gap. Started after the mux is
	// composed, as decided; a fresh crash-recovery pass runs immediately,
	// then every cfg.SchedulerInterval (default 30s). This log line
	// deliberately has no "stage" field — it is not one of the fail-
	// closed startup-order stages parseStages/TestBoot_StartupOrderEnforced
	// assert on, since it is a background concern, not a boot
	// precondition.
	quotaWorkers, probeWorkers, err := httpapi.BuildSchedulerWorkers(db, "scheduler", time.Now, nil)
	if err != nil {
		_ = httpServer.Close()
		if dataHTTP != nil {
			_ = dataHTTP.Close()
		}
		closeDB()
		release()
		return nil, fmt.Errorf("app: build scheduler workers: %w", err)
	}
	schedulerCtx, cancelScheduler := context.WithCancel(context.Background())
	scheduler := NewScheduler(cfg.SchedulerInterval, logger,
		SchedulerTick{Name: "quota_reconcile", Run: func(ctx context.Context) error { _, err := quotaWorkers.ReconcileTick(ctx); return err }},
		SchedulerTick{Name: "quota_janitor", Run: func(ctx context.Context) error { _, err := quotaWorkers.JanitorTick(ctx); return err }},
		SchedulerTick{Name: "probe_drain", Run: func(ctx context.Context) error { _, err := probeWorkers.DrainTick(ctx); return err }},
		SchedulerTick{Name: "probe_recertify", Run: func(ctx context.Context) error { _, err := probeWorkers.RecertifyTick(ctx); return err }},
		SchedulerTick{Name: "probe_reclaim", Run: func(ctx context.Context) error { _, err := probeWorkers.ReclaimTick(ctx); return err }},
	)
	logger.Info("background scheduler started", observability.String("interval", scheduler.interval.String()))
	scheduler.Start(schedulerCtx)

	return &Server{
		Addr:            ln.Addr().String(),
		db:              db,
		lock:            lock,
		http:            httpServer,
		ln:              ln,
		dataHTTP:        dataHTTP,
		dataLn:          dataLn,
		keyring:         kr,
		scheduler:       scheduler,
		cancelScheduler: cancelScheduler,
	}, nil
}

// ErrNonLoopbackBind is returned by Boot when cfg.Bind's host is not a
// loopback address (P2b-CAPI-001, 01 §6a/§8). There is no configuration
// escape hatch for this — the control plane is loopback-only by
// construction.
var ErrNonLoopbackBind = errors.New("app: control plane bind must be loopback-only (127.0.0.0/8, ::1, or localhost)")

// isLoopbackBind reports whether bind's host component refers only to
// the local host: "localhost", or an IP in 127.0.0.0/8 or ::1. An empty
// host (e.g. ":8081", which binds every interface) is deliberately NOT
// loopback — that is exactly the off-host exposure this check exists to
// reject.
func isLoopbackBind(bind string) bool {
	host, _, err := net.SplitHostPort(bind)
	if err != nil {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// noCiphertextStore is the default, empty secrets.CiphertextRefStore
// used when BootConfig.CiphertextStore is nil: no credential table
// exists yet (M2 is P2b), so there is nothing to enumerate and
// secrets.Reconcile trivially passes.
type noCiphertextStore struct{}

func (noCiphertextStore) ListKeyRefs(_ context.Context) ([]secrets.CiphertextRef, error) {
	return nil, nil
}

// The following are explicit stubs for stages this phase does not yet
// implement: repositories and services are still P2b+ no-ops — neither
// is wired to internal/execution's dispatcher. validate_embedded_assets
// stopped being a stub at P2a-UI-001 (calls httpui.New() directly,
// above), and build_provider_registry stopped being one at P2b-PROV-002
// (seeds the providers table directly in Boot, above).

func buildRepositoriesStub() {}
func buildServicesStub()     {}
