package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/httpapi"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/httpui"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/observability"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/platform"
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
}

// Server represents a successfully booted process: a mounted HTTP mux
// listening on a real address, plus everything the fail-closed startup
// sequence built. Boot returns a non-nil *Server only on full success —
// a failure at any earlier stage returns (nil, error) and never reaches
// the point of opening a listener.
type Server struct {
	Addr string

	db      *storage.DB
	lock    *Lock
	http    *http.Server
	ln      net.Listener
	keyring *secrets.Keyring
}

// Shutdown gracefully stops the listener and releases what Boot
// acquired (the database handle, then the single-instance lock), in
// reverse order of acquisition.
func (s *Server) Shutdown(ctx context.Context) error {
	var errs []error
	if err := s.http.Shutdown(ctx); err != nil {
		errs = append(errs, fmt.Errorf("http shutdown: %w", err))
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

	// 6-8. Repositories / provider registry / services: all stubs until
	// P1/P2b+. Deliberately NOT internal/execution's dispatcher — wiring
	// the real execution path is separate, later work, not this unit's.
	logStage(StageBuildRepositories)
	buildRepositoriesStub()
	logStage(StageBuildProviderRegistry)
	buildProviderRegistryStub()
	logStage(StageBuildServices)
	buildServicesStub()

	// 9. Mount the HTTP mux. /health is httpapi's definitive liveness
	// surface (01 §6d): unauthenticated, outside /api/control/v1, behind
	// the loopback + Host-allowlist network gate. This replaces
	// P0-FND-007's original placeholder handler — there is exactly one
	// liveness surface. The dashboard SPA built at stage 1 joins it on
	// the same mux, behind the identical gate (P2a-UI-001, 01 §1/§3).
	logStage(StageMountHTTPMux)
	mux := httpapi.ControlMux(cfg.Bind, spa, db)

	// 10. Listen.
	logStage(StageListen)
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

	return &Server{
		Addr:    ln.Addr().String(),
		db:      db,
		lock:    lock,
		http:    httpServer,
		ln:      ln,
		keyring: kr,
	}, nil
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
// implement: repositories, provider registry, and services are P2b+.
// Each is a clearly-marked no-op — none is wired to internal/execution's
// dispatcher. validate_embedded_assets is no longer a stub as of
// P2a-UI-001 — it calls httpui.New() directly, above.

func buildRepositoriesStub()     {}
func buildProviderRegistryStub() {}
func buildServicesStub()         {}
