package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/app"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/platform"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/secrets"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// TestMain injects a fake dashboard SPA into every boot the cli dispatch
// tests trigger, so the real serve/bare run loop is exercised without
// requiring a frontend build (P2a-UI-001's real embed is covered by
// internal/httpui and internal/app tests). version/help modes don't boot
// and are unaffected.
func TestMain(m *testing.M) {
	realBoot := bootFunc
	bootFunc = func(ctx context.Context, cfg app.BootConfig) (*app.Server, error) {
		if cfg.SPAHandler == nil {
			cfg.SPAHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = w.Write([]byte("<!doctype html><html><body>fake spa</body></html>"))
			})
		}
		return realBoot(ctx, cfg)
	}
	os.Exit(m.Run())
}

// setTestDataDir points platform's data-dir resolution (used by
// app.Boot's lock/DB) at a fresh temp directory for the duration of the
// test, so real-boot tests never touch the real user profile.
func setTestDataDir(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", dir)
	t.Setenv("XDG_DATA_HOME", dir)
}

// freeLoopbackAddr reserves a real free port via a throwaway listener,
// closes it immediately, and returns the address — used so tests that
// need to know the exact bind address up front (to dial it later) don't
// have to hardcode a port that might already be in use.
func freeLoopbackAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve free port: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("close temp listener: %v", err)
	}
	return addr
}

// waitForListener polls addr until something accepts a TCP connection
// there (or the deadline elapses), since app.Boot does real work (lock,
// DB open, migrate) before its listener comes up.
func waitForListener(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for a listener at %q", addr)
}

func TestResetOwner_ClearsOwnerAuthRevokesSessionsKeepsKeyringUntouched(t *testing.T) {
	setTestDataDir(t)
	dataDir, err := platform.EnsureDataDir()
	if err != nil {
		t.Fatalf("EnsureDataDir: %v", err)
	}

	// Seed a keyring, mirroring what a real first boot would have
	// created, so this test can prove reset-owner leaves it untouched.
	if _, err := secrets.Load(dataDir, "", false); err != nil {
		t.Fatalf("secrets.Load: %v", err)
	}
	keyringPath := filepath.Join(dataDir, "secrets", "keyring.json")
	before, err := os.ReadFile(keyringPath)
	if err != nil {
		t.Fatalf("read keyring before reset: %v", err)
	}

	db, err := storage.Open(dataDir)
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	if _, err := storage.Migrate(context.Background(), db); err != nil {
		t.Fatalf("storage.Migrate: %v", err)
	}

	hash, err := secrets.DeriveOwnerPasswordHash("a-long-enough-owner-password-1")
	if err != nil {
		t.Fatalf("DeriveOwnerPasswordHash: %v", err)
	}
	ownerAuth := storage.NewOwnerAuthRepo(db)
	if err := ownerAuth.Create(context.Background(), storage.OwnerAuthRow{
		PasswordHash: hash.Hash, Salt: hash.Salt, KDFTime: hash.Time, KDFMemKiB: hash.MemKiB, KDFThreads: hash.Threads, KDFKeyLen: hash.KeyLen,
	}); err != nil {
		t.Fatalf("OwnerAuthRepo.Create: %v", err)
	}

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	sessionTokenHash := []byte("a-session-token-hash")
	sessions := storage.NewOwnerSessionRepo(db)
	if err := sessions.Create(context.Background(), storage.OwnerSessionRow{
		TokenHash:         sessionTokenHash,
		CreatedAt:         now,
		LastSeenAt:        now,
		IdleExpiresAt:     now.Add(30 * time.Minute),
		AbsoluteExpiresAt: now.Add(12 * time.Hour),
	}); err != nil {
		t.Fatalf("OwnerSessionRepo.Create: %v", err)
	}

	if err := db.Close(); err != nil {
		t.Fatalf("close seed db: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if err := Dispatch(context.Background(), []string{"reset-owner"}, &stdout, &stderr); err != nil {
		t.Fatalf("Dispatch(reset-owner) error = %v; stderr = %q", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "reset") {
		t.Fatalf("stdout = %q, want a reset confirmation", stdout.String())
	}

	after, err := os.ReadFile(keyringPath)
	if err != nil {
		t.Fatalf("read keyring after reset: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("keyring file changed by reset-owner — credentials must remain untouched")
	}

	db2, err := storage.Open(dataDir)
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	defer func() { _ = db2.Close() }()

	exists, err := storage.NewOwnerAuthRepo(db2).Exists(context.Background())
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if exists {
		t.Fatalf("owner_auth row still exists after reset-owner")
	}

	row, ok, err := storage.NewOwnerSessionRepo(db2).GetByTokenHash(context.Background(), sessionTokenHash)
	if err != nil || !ok {
		t.Fatalf("GetByTokenHash: ok=%v err=%v", ok, err)
	}
	if row.RevokedAt == nil {
		t.Fatalf("session not revoked after reset-owner")
	}
}

func TestResetOwner_RefusesWhileServerRunning(t *testing.T) {
	setTestDataDir(t)

	lock, err := app.AcquireLock()
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	defer func() { _ = lock.Release() }()

	var stdout, stderr bytes.Buffer
	err = Dispatch(context.Background(), []string{"reset-owner"}, &stdout, &stderr)
	if err == nil {
		t.Fatalf("Dispatch(reset-owner) succeeded while locked, want refusal")
	}
	if !errors.Is(err, app.ErrAlreadyRunning) {
		t.Fatalf("error = %v, want app.ErrAlreadyRunning", err)
	}
	if stderr.Len() == 0 {
		t.Fatalf("stderr empty, want a refusal message")
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty on refusal", stdout.String())
	}
}

func TestDispatch_Version(t *testing.T) {
	var stdout, stderr bytes.Buffer

	err := Dispatch(context.Background(), []string{"version"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != version {
		t.Fatalf("stdout = %q, want %q", got, version)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestDispatch_Help(t *testing.T) {
	var stdout, stderr bytes.Buffer

	err := Dispatch(context.Background(), []string{"help"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "venom serve") {
		t.Fatalf("stdout = %q, want usage text mentioning modes", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestDispatch_Unrecognized(t *testing.T) {
	var stdout, stderr bytes.Buffer

	err := Dispatch(context.Background(), []string{"bogus"}, &stdout, &stderr)
	if !errors.Is(err, ErrUnrecognizedMode) {
		t.Fatalf("Dispatch() error = %v, want ErrUnrecognizedMode", err)
	}
	if stderr.Len() == 0 {
		t.Fatalf("stderr is empty, want an error message")
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty (unrecognized mode must not run any known mode)", stdout.String())
	}
}

func TestDispatch_Serve_BlocksThenShutsDownWithinBound(t *testing.T) {
	setTestDataDir(t)

	// A known, pre-reserved address (not ":0") so the test can
	// synchronize on the listener actually being up before cancelling,
	// instead of racing a fixed sleep against app.Boot's lock/DB/migrate
	// work. A fixed sleep here is exactly what caused this test to flake
	// on a slow CI Windows runner: migration was still in progress when
	// the sleep elapsed and cancel() fired, aborting Boot mid-migration.
	bind := freeLoopbackAddr(t)

	ctx, cancel := context.WithCancel(context.Background())
	var stdout, stderr bytes.Buffer

	done := make(chan error, 1)
	go func() {
		done <- Dispatch(ctx, []string{"serve", "-bind", bind}, &stdout, &stderr)
	}()

	waitForListener(t, bind)

	select {
	case err := <-done:
		t.Fatalf("Dispatch() returned before shutdown was requested, err = %v", err)
	default:
		// still running, as expected — the listener came up, so Boot
		// succeeded and Dispatch is now blocked waiting for ctx.Done()
	}

	begin := time.Now()
	cancel()

	select {
	case err := <-done:
		elapsed := time.Since(begin)
		if err != nil {
			t.Fatalf("Dispatch() error = %v", err)
		}
		if elapsed > app.ShutdownTimeout {
			t.Fatalf("shutdown took %v, want <= %v", elapsed, app.ShutdownTimeout)
		}
		t.Logf("serve mode shut down in %v (bound %v)", elapsed, app.ShutdownTimeout)
	case <-time.After(app.ShutdownTimeout + time.Second):
		t.Fatal("Dispatch() did not return within bound after cancel")
	}

	if !strings.Contains(stdout.String(), "shutdown complete") {
		t.Fatalf("stdout = %q, want shutdown-complete message", stdout.String())
	}
}

// Owner condition 3: the tray log is append-only — reopening never truncates.
func TestOpenAppendLog_AppendsNeverTruncates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "venom.log")

	f1, err := openAppendLog(path)
	if err != nil {
		t.Fatalf("openAppendLog #1: %v", err)
	}
	if _, err := f1.WriteString("first\n"); err != nil {
		t.Fatalf("write #1: %v", err)
	}
	_ = f1.Close()

	f2, err := openAppendLog(path)
	if err != nil {
		t.Fatalf("openAppendLog #2: %v", err)
	}
	if _, err := f2.WriteString("second\n"); err != nil {
		t.Fatalf("write #2: %v", err)
	}
	_ = f2.Close()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "first\nsecond\n" {
		t.Fatalf("append-only violated: got %q, want %q", got, "first\nsecond\n")
	}
}

// Bare mode routes to tray mode, not serve. The tray loop is indirected via
// runTrayLoopFn so this asserts routing without entering the real UI/os.Exit path.
func TestDispatch_BareMode_RoutesToTrayNotServe(t *testing.T) {
	prev := runTrayLoopFn
	t.Cleanup(func() { runTrayLoopFn = prev })
	called := false
	runTrayLoopFn = func(_ context.Context, _ io.Writer) error {
		called = true
		return nil
	}
	var stdout, stderr strings.Builder
	if err := Dispatch(context.Background(), nil, &stdout, &stderr); err != nil {
		t.Fatalf("Dispatch bare mode: %v", err)
	}
	if !called {
		t.Fatal("bare mode did not route to the tray loop")
	}
}

// TestDispatch_BareMode_BootFailureNotifiesVisibly pins the owner-facing
// fix for silent tray death: when bare tray mode's boot fails (e.g. a
// corrupt production keyring), a double-click user previously saw NOTHING
// — the process died before the tray icon appeared. runTrayLoop must call
// the platform notifier (a MessageBox on Windows) with the boot error
// before returning it.
//
// This exercises the REAL runTrayLoop (bare mode routes through
// runTrayLoopFn, which is not overridden here). A boot failure is
// guaranteed by holding the single-instance lock, exactly like
// TestResetOwner_RefusesWhileServerRunning: ServerAdapter.Boot calls
// app.Boot directly (NOT this package's bootFunc), and app.Boot cannot
// succeed while the lock is held. The test deliberately does NOT pin
// WHICH boot failure fires: app.Boot validates embedded dashboard assets
// BEFORE the lock, so a fresh CI checkout (placeholder .buildmarker only)
// fails there, while a working tree holding a real dashboard build
// reaches the lock and fails with ErrAlreadyRunning. The invariant this
// test's name claims is "a boot failure notifies visibly" — true in both.
func TestDispatch_BareMode_BootFailureNotifiesVisibly(t *testing.T) {
	setTestDataDir(t)
	// runTrayLoop's config.Load(nil) reads the host environment; pin both
	// keys so the test cannot depend on (or collide with) real VENOM_* env.
	// Empty VENOM_DATA_PLANE_BIND is treated as unset by config.Load.
	t.Setenv("VENOM_BIND", freeLoopbackAddr(t))
	t.Setenv("VENOM_DATA_PLANE_BIND", "")

	lock, err := app.AcquireLock()
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	defer func() { _ = lock.Release() }()

	var recorded []string
	prevNotify := notifyStartupFailure
	t.Cleanup(func() { notifyStartupFailure = prevNotify })
	notifyStartupFailure = func(detail string) { recorded = append(recorded, detail) }

	var stdout, stderr bytes.Buffer
	err = Dispatch(context.Background(), nil, &stdout, &stderr)
	if err == nil {
		t.Fatal("Dispatch(bare) succeeded while the lock was held, want a boot failure")
	}
	if len(recorded) != 1 {
		t.Fatalf("notifier called %d times, want exactly 1 (boot-failure path only)", len(recorded))
	}
	// The notified detail must be the boot error itself: runTrayLoop wraps
	// the inner error as "cli: boot: <inner>" and notifies with the inner
	// text, so the returned error must contain exactly what was notified.
	if recorded[0] == "" || !strings.Contains(err.Error(), recorded[0]) {
		t.Fatalf("notified detail = %q, want the boot error text (returned error %q)", recorded[0], err.Error())
	}
}

// TestDispatch_Serve_EndToEnd_BootsHealthAndShutsDownCleanly is the P0
// gate's deliverable proof: `venom serve` genuinely boots the real
// composition root (not the old NoopDrainer stub), a real HTTP client
// gets a 200 from the gated /health handler, and a shutdown signal
// (context cancellation, standing in for real SIGINT/SIGTERM — see
// app.NotifyContext) brings it down cleanly within app.ShutdownTimeout,
// after which the listener is genuinely gone.
func TestDispatch_Serve_EndToEnd_BootsHealthAndShutsDownCleanly(t *testing.T) {
	setTestDataDir(t)

	// A real, pre-reserved free port (not the hardcoded default, and not
	// bare ":0" — this test needs to know the exact address up front so
	// it can dial /health once Boot's listener comes up).
	bind := freeLoopbackAddr(t)

	ctx, cancel := context.WithCancel(context.Background())
	var stdout, stderr bytes.Buffer

	done := make(chan error, 1)
	go func() {
		done <- Dispatch(ctx, []string{"serve", "-bind", bind}, &stdout, &stderr)
	}()

	waitForListener(t, bind)

	req, err := http.NewRequest(http.MethodGet, "http://"+bind+"/health", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Host = bind // exact configured bind — the network gate's Host-allowlist accepts this literally

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /health error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /health status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	begin := time.Now()
	cancel() // simulated shutdown signal

	select {
	case err := <-done:
		elapsed := time.Since(begin)
		if err != nil {
			t.Fatalf("Dispatch() error = %v", err)
		}
		if elapsed > app.ShutdownTimeout {
			t.Fatalf("shutdown took %v, want <= %v", elapsed, app.ShutdownTimeout)
		}
		t.Logf("end-to-end serve shut down in %v (bound %v)", elapsed, app.ShutdownTimeout)
	case <-time.After(app.ShutdownTimeout + 2*time.Second):
		t.Fatal("Dispatch() did not return within bound after cancel")
	}

	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty on a clean run", stderr.String())
	}
	if !strings.Contains(stdout.String(), "shutdown complete") {
		t.Fatalf("stdout = %q, want shutdown-complete message", stdout.String())
	}

	if conn, dialErr := net.DialTimeout("tcp", bind, 200*time.Millisecond); dialErr == nil {
		_ = conn.Close()
		t.Fatalf("dial to %q succeeded after shutdown — listener should be closed", bind)
	}
}

// TestServe_ThreadsDataPlaneBindIntoBootConfig pins the wiring that makes the
// optional public data-plane bind real in PRODUCTION (P5-PAPI-001, 01 §6b).
// config.Load parses and validates VENOM_DATA_PLANE_BIND / -data-plane-bind, and
// app.Boot honors BootConfig.DataPlaneBind — but if runServeLoop does not pass
// the one to the other, the whole feature is INERT: a configured data-plane bind
// would silently keep sharing the control listener. Nothing else in the tree
// catches that, because both ends are individually correct and tested.
//
// Mutation: drop DataPlaneBind from the BootConfig literal in runServeLoop → the
// captured value is "" → this test RED.
func TestServe_ThreadsDataPlaneBindIntoBootConfig(t *testing.T) {
	setTestDataDir(t)

	var captured app.BootConfig
	saved := bootFunc
	bootFunc = func(_ context.Context, cfg app.BootConfig) (*app.Server, error) {
		captured = cfg
		return nil, errors.New("boot stopped by test")
	}
	t.Cleanup(func() { bootFunc = saved })

	const dataBind = "127.0.0.1:18099"
	var stdout, stderr bytes.Buffer
	err := Dispatch(context.Background(), []string{"serve", "-bind", "127.0.0.1:18098", "-data-plane-bind", dataBind}, &stdout, &stderr)
	if err == nil {
		t.Fatalf("expected the stubbed boot error")
	}

	if captured.Bind != "127.0.0.1:18098" {
		t.Fatalf("BootConfig.Bind = %q, want the configured control bind", captured.Bind)
	}
	if captured.DataPlaneBind != dataBind {
		t.Fatalf("BootConfig.DataPlaneBind = %q, want %q — the configured data-plane bind must reach Boot, or the flag is inert", captured.DataPlaneBind, dataBind)
	}
}
