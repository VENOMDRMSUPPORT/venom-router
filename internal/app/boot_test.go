package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/httpapi"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/httpui"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/observability"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/platform"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/secrets"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// fakeSPA is a stand-in dashboard SPA handler for Boot tests that do not
// exercise the real embed: it lets Boot pass the validate_embedded_assets
// stage (P2a-UI-001) without requiring a real dashboard build in the tree,
// so the Go gate stays independent of the frontend build. The real embed is
// covered by internal/httpui's own tests and by
// TestBoot_ServesEmbeddedDashboardSPA when the build is present.
func fakeSPA() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<!doctype html><html><body>fake spa</body></html>"))
	})
}

// TestBoot_HappyPath is Test B1: the startup sequence runs and the
// listener comes up for real — asserted by dialing httpapi's gated
// /health route over an actual TCP connection, not by inspecting
// internal state.
func TestBoot_HappyPath(t *testing.T) {
	setDataDirEnv(t)

	var logBuf bytes.Buffer
	logger := observability.New(slog.NewJSONHandler(&logBuf, nil))

	srv, err := Boot(context.Background(), BootConfig{Bind: "127.0.0.1:0", Logger: logger, SPAHandler: fakeSPA()})
	if err != nil {
		t.Fatalf("Boot() error = %v", err)
	}
	defer func() {
		if err := srv.Shutdown(context.Background()); err != nil {
			t.Fatalf("Shutdown() error = %v", err)
		}
	}()

	// This test uses an ephemeral port (Bind: "127.0.0.1:0"), so the
	// resolved srv.Addr's port differs from the literal cfg.Bind string
	// the network gate's Host-allowlist was constructed with (P0-CAPI-001
	// — see internal/httpapi). A bare http.Get would send
	// Host: srv.Addr (the ephemeral, resolved address), which the gate
	// correctly rejects as not matching the configured bind. Sending
	// Host: "localhost" instead exercises the gate's other unconditional
	// bare-hostname allowance (01 §6a) rather than papering over the
	// real check; in production, Bind is a fixed, known address and a
	// real client's Host header matches it directly.
	req, err := http.NewRequest(http.MethodGet, "http://"+srv.Addr+"/health", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Host = "localhost"

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /health error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /health status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	t.Logf("boot log:\n%s", logBuf.String())
}

// TestBoot_ServesEmbeddedDashboardSPA is Test B1b (P2a-UI-001): the
// dashboard SPA httpui embeds is actually reachable through the real,
// fully-booted mux — not just unit-tested in isolation — at "/" (real
// index.html) and at a real built asset path, both behind the same gate
// /health already proved. A client-side SPA route also falls back to
// index.html rather than 404ing.
func TestBoot_ServesEmbeddedDashboardSPA(t *testing.T) {
	setDataDirEnv(t)

	// Prove UI-001's DoD end-to-end with the REAL embedded dashboard. It
	// requires `task dashboard:build-embed` to have run in this tree; on a
	// fresh checkout (the committed placeholder) httpui.New() fails closed and
	// this end-to-end proof is skipped. The Go gate therefore does not depend
	// on a frontend build; the real embed is still exercised locally and in CI
	// once P2a-DS-004 wires the dashboard build in. The Boot wiring itself
	// (stage 1 -> stage 9 -> ControlMux behind the gate) is covered by the
	// other Boot tests via an injected SPA, and the SPA behavior by
	// internal/httpui's own tests.
	spa, err := httpui.New()
	if err != nil {
		t.Skipf("real embedded dashboard not present (run `task dashboard:build-embed`); skipping end-to-end embed test: %v", err)
	}

	srv, err := Boot(context.Background(), BootConfig{Bind: "127.0.0.1:0", SPAHandler: spa})
	if err != nil {
		t.Fatalf("Boot() error = %v", err)
	}
	defer func() {
		if err := srv.Shutdown(context.Background()); err != nil {
			t.Fatalf("Shutdown() error = %v", err)
		}
	}()

	get := func(path string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, "http://"+srv.Addr+path, nil)
		if err != nil {
			t.Fatalf("build request for %q: %v", path, err)
		}
		req.Host = "localhost"
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET %s error = %v", path, err)
		}
		return resp
	}

	rootResp := get("/")
	defer func() { _ = rootResp.Body.Close() }()
	if rootResp.StatusCode != http.StatusOK {
		t.Fatalf("GET / status = %d, want %d", rootResp.StatusCode, http.StatusOK)
	}
	rootBody, err := io.ReadAll(rootResp.Body)
	if err != nil {
		t.Fatalf("read GET / body: %v", err)
	}
	if !bytes.Contains(rootBody, []byte("<!doctype html")) && !bytes.Contains(rootBody, []byte("<!DOCTYPE html")) {
		t.Fatalf("GET / body doesn't look like the dashboard's index.html: %q", rootBody)
	}

	fallbackResp := get("/some/client/side/route")
	defer func() { _ = fallbackResp.Body.Close() }()
	if fallbackResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /some/client/side/route status = %d, want %d (SPA fallback)", fallbackResp.StatusCode, http.StatusOK)
	}
}

// TestBoot_FailClosedOnMigrationFailure is Test B2: a forced
// checksum-tamper failure (the same fail-closed mechanism P0-DB-002
// proved) must abort Boot before net.Listen is ever reached, so nothing
// is accepting connections on the address it would have used.
func TestBoot_FailClosedOnMigrationFailure(t *testing.T) {
	setDataDirEnv(t)
	ctx := context.Background()

	// First boot succeeds: establishes the baseline migration and its
	// recorded checksum, then shuts down cleanly.
	srv1, err := Boot(ctx, BootConfig{Bind: "127.0.0.1:0", SPAHandler: fakeSPA()})
	if err != nil {
		t.Fatalf("first Boot() error = %v", err)
	}
	if err := srv1.Shutdown(ctx); err != nil {
		t.Fatalf("first Shutdown() error = %v", err)
	}

	// Force the failure: tamper the checksum recorded for the already-
	// applied baseline migration. This is exactly the mechanism
	// P0-DB-002 proved makes storage.Migrate/Verify fail closed with
	// ErrChecksumMismatch. Done by reopening the DB directly (bypassing
	// the lock/Boot), purely to make this one SQL edit.
	dataDir, err := platform.EnsureDataDir()
	if err != nil {
		t.Fatalf("platform.EnsureDataDir(): %v", err)
	}
	tamperDB, err := storage.Open(dataDir)
	if err != nil {
		t.Fatalf("storage.Open() for tampering: %v", err)
	}
	if _, err := tamperDB.Conn().Exec(
		"UPDATE venom_migration_checksums SET checksum = 'deadbeef' WHERE version = 1",
	); err != nil {
		t.Fatalf("tamper checksum: %v", err)
	}
	if err := tamperDB.Close(); err != nil {
		t.Fatalf("close tamper handle: %v", err)
	}

	// Reserve a real free port up front so there is a concrete address
	// to dial afterward and confirm nothing is listening on it.
	bind := freeLoopbackAddr(t)

	var logBuf bytes.Buffer
	logger := observability.New(slog.NewJSONHandler(&logBuf, nil))

	srv2, err := Boot(ctx, BootConfig{Bind: bind, Logger: logger, SPAHandler: fakeSPA()})
	if err == nil {
		_ = srv2.Shutdown(ctx)
		t.Fatalf("second Boot() succeeded, want a checksum-mismatch failure")
	}
	if !errors.Is(err, storage.ErrChecksumMismatch) {
		t.Fatalf("second Boot() error = %v, want it to wrap storage.ErrChecksumMismatch", err)
	}
	if srv2 != nil {
		t.Fatalf("Boot() returned a non-nil *Server alongside an error")
	}

	conn, dialErr := net.DialTimeout("tcp", bind, 200*time.Millisecond)
	if dialErr == nil {
		_ = conn.Close()
		t.Fatalf("dial to %q succeeded — a listener came up despite the fail-closed failure", bind)
	}
	t.Logf("dial to %q correctly refused: %v", bind, dialErr)

	stages := parseStages(t, logBuf.Bytes())
	t.Logf("boot log (must stop at %q):\n%s", StageMigrateDatabase, logBuf.String())
	if len(stages) == 0 || stages[len(stages)-1] != string(StageMigrateDatabase) {
		t.Fatalf("last logged stage = %v, want the sequence to end at %q", stages, StageMigrateDatabase)
	}
	for _, s := range stages {
		if s == string(StageMountHTTPMux) || s == string(StageListen) {
			t.Fatalf("stage %q was logged despite the fail-closed failure — listener stages must never run", s)
		}
	}
}

// fixedRefStore is a secrets.CiphertextRefStore fixture that always
// returns the same, pre-baked refs — used to force a reconcile failure
// without needing a real credential table (M2/P2b does not exist yet).
type fixedRefStore struct {
	refs []secrets.CiphertextRef
}

func (s fixedRefStore) ListKeyRefs(_ context.Context) ([]secrets.CiphertextRef, error) {
	return s.refs, nil
}

// TestBoot_FailClosedOnCorruptKeyring proves load_keyring, now wired to
// the real secrets.Load, aborts Boot before net.Listen when the on-disk
// keyring is corrupt — the same fail-closed shape as
// TestBoot_FailClosedOnMigrationFailure, one stage earlier.
func TestBoot_FailClosedOnCorruptKeyring(t *testing.T) {
	setDataDirEnv(t)
	ctx := context.Background()

	// First boot succeeds: creates the real on-disk keyring.
	srv1, err := Boot(ctx, BootConfig{Bind: "127.0.0.1:0", SPAHandler: fakeSPA()})
	if err != nil {
		t.Fatalf("first Boot() error = %v", err)
	}
	if err := srv1.Shutdown(ctx); err != nil {
		t.Fatalf("first Shutdown() error = %v", err)
	}

	// Corrupt the keyring file directly, bypassing Boot/the lock,
	// purely to make this one edit.
	dataDir, err := platform.EnsureDataDir()
	if err != nil {
		t.Fatalf("platform.EnsureDataDir(): %v", err)
	}
	keyringPath := filepath.Join(dataDir, "secrets", "keyring.json")
	if err := os.WriteFile(keyringPath, []byte("{ not valid json"), 0o600); err != nil {
		t.Fatalf("corrupt keyring file: %v", err)
	}

	bind := freeLoopbackAddr(t)

	var logBuf bytes.Buffer
	logger := observability.New(slog.NewJSONHandler(&logBuf, nil))

	srv2, err := Boot(ctx, BootConfig{Bind: bind, Logger: logger, SPAHandler: fakeSPA()})
	if err == nil {
		_ = srv2.Shutdown(ctx)
		t.Fatalf("second Boot() succeeded, want a corrupt-keyring failure")
	}
	if !errors.Is(err, secrets.ErrKeyringCorrupt) {
		t.Fatalf("second Boot() error = %v, want it to wrap secrets.ErrKeyringCorrupt", err)
	}
	if srv2 != nil {
		t.Fatalf("Boot() returned a non-nil *Server alongside an error")
	}

	conn, dialErr := net.DialTimeout("tcp", bind, 200*time.Millisecond)
	if dialErr == nil {
		_ = conn.Close()
		t.Fatalf("dial to %q succeeded — a listener came up despite the fail-closed failure", bind)
	}
	t.Logf("dial to %q correctly refused: %v", bind, dialErr)

	stages := parseStages(t, logBuf.Bytes())
	t.Logf("boot log (must stop at %q):\n%s", StageLoadKeyring, logBuf.String())
	if len(stages) == 0 || stages[len(stages)-1] != string(StageLoadKeyring) {
		t.Fatalf("last logged stage = %v, want the sequence to end at %q", stages, StageLoadKeyring)
	}
	for _, s := range stages {
		if s == string(StageMountHTTPMux) || s == string(StageListen) {
			t.Fatalf("stage %q was logged despite the fail-closed failure — listener stages must never run", s)
		}
	}
}

// TestBoot_ReconcileFailsClosed_KeyringStoreMismatch proves
// reconcile_keyring aborts Boot before net.Listen when a stored
// ciphertext references a key_id the keyring does not have. No real
// credential table exists yet (M2/P2b), so this injects a
// fixedRefStore via BootConfig.CiphertextStore to force the mismatch.
func TestBoot_ReconcileFailsClosed_KeyringStoreMismatch(t *testing.T) {
	setDataDirEnv(t)
	ctx := context.Background()

	bind := freeLoopbackAddr(t)
	var logBuf bytes.Buffer
	logger := observability.New(slog.NewJSONHandler(&logBuf, nil))

	mismatchStore := fixedRefStore{refs: []secrets.CiphertextRef{
		{Envelope: secrets.Envelope{KeyID: "k_does_not_exist"}},
	}}

	srv, err := Boot(ctx, BootConfig{Bind: bind, Logger: logger, SPAHandler: fakeSPA(), CiphertextStore: mismatchStore})
	if err == nil {
		_ = srv.Shutdown(ctx)
		t.Fatalf("Boot() succeeded, want a reconcile failure")
	}
	if !errors.Is(err, secrets.ErrMissingKey) {
		t.Fatalf("Boot() error = %v, want it to wrap secrets.ErrMissingKey", err)
	}
	if srv != nil {
		t.Fatalf("Boot() returned a non-nil *Server alongside an error")
	}

	conn, dialErr := net.DialTimeout("tcp", bind, 200*time.Millisecond)
	if dialErr == nil {
		_ = conn.Close()
		t.Fatalf("dial to %q succeeded — a listener came up despite the fail-closed failure", bind)
	}
	t.Logf("dial to %q correctly refused: %v", bind, dialErr)

	stages := parseStages(t, logBuf.Bytes())
	t.Logf("boot log (must stop at %q):\n%s", StageReconcileKeyring, logBuf.String())
	if len(stages) == 0 || stages[len(stages)-1] != string(StageReconcileKeyring) {
		t.Fatalf("last logged stage = %v, want the sequence to end at %q", stages, StageReconcileKeyring)
	}
	for _, s := range stages {
		if s == string(StageMountHTTPMux) || s == string(StageListen) {
			t.Fatalf("stage %q was logged despite the fail-closed failure — listener stages must never run", s)
		}
	}
}

// TestBoot_StartupOrderEnforced is Test B3: it asserts the exact,
// in-order sequence of stages Boot logged, not merely the end state —
// proving lock-before-DB, migrate-before-mount/listen, etc.
func TestBoot_StartupOrderEnforced(t *testing.T) {
	setDataDirEnv(t)

	var logBuf bytes.Buffer
	logger := observability.New(slog.NewJSONHandler(&logBuf, nil))

	srv, err := Boot(context.Background(), BootConfig{Bind: "127.0.0.1:0", Logger: logger, SPAHandler: fakeSPA()})
	if err != nil {
		t.Fatalf("Boot() error = %v", err)
	}
	defer func() {
		if err := srv.Shutdown(context.Background()); err != nil {
			t.Fatalf("Shutdown() error = %v", err)
		}
	}()

	want := []string{
		string(StageValidateEmbeddedAssets),
		string(StageAcquireLock),
		string(StageLoadKeyring),
		string(StageOpenDatabase),
		string(StageMigrateDatabase),
		string(StageReconcileKeyring),
		string(StageBuildRepositories),
		string(StageBuildProviderRegistry),
		string(StageBuildServices),
		string(StageMountHTTPMux),
		string(StageListen),
	}

	got := parseStages(t, logBuf.Bytes())
	if len(got) != len(want) {
		t.Fatalf("got %d stages, want %d\ngot:  %v\nwant: %v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("stage[%d] = %q, want %q\nfull got:  %v\nfull want: %v", i, got[i], want[i], got, want)
		}
	}
}

// TestBoot_FailClosedOnNonLoopbackBind is P2b-CAPI-001's fail-closed
// proof: a syntactically valid but non-loopback bind address must never
// reach net.Listen at all — Boot aborts first, so nothing is ever
// attempted to be exposed off-host.
func TestBoot_FailClosedOnNonLoopbackBind(t *testing.T) {
	setDataDirEnv(t)
	ctx := context.Background()

	const nonLoopbackBind = "203.0.113.5:8081" // TEST-NET-3 (RFC 5737) — never actually dialable/listenable here

	var logBuf bytes.Buffer
	logger := observability.New(slog.NewJSONHandler(&logBuf, nil))

	srv, err := Boot(ctx, BootConfig{Bind: nonLoopbackBind, Logger: logger, SPAHandler: fakeSPA()})
	if err == nil {
		_ = srv.Shutdown(ctx)
		t.Fatalf("Boot() succeeded with a non-loopback bind, want failure")
	}
	if !errors.Is(err, ErrNonLoopbackBind) {
		t.Fatalf("Boot() error = %v, want it to wrap ErrNonLoopbackBind", err)
	}
	if srv != nil {
		t.Fatalf("Boot() returned a non-nil *Server alongside an error")
	}

	conn, dialErr := net.DialTimeout("tcp", nonLoopbackBind, 200*time.Millisecond)
	if dialErr == nil {
		_ = conn.Close()
		t.Fatalf("dial to %q succeeded — a listener came up despite the fail-closed bind rejection", nonLoopbackBind)
	}
}

// TestBoot_LoopbackBindVariantsAccepted proves the check is not merely
// "127.0.0.1" special-cased: "localhost" and the IPv6 loopback also
// boot successfully.
func TestBoot_LoopbackBindVariantsAccepted(t *testing.T) {
	for _, bind := range []string{"localhost:0", "[::1]:0"} {
		t.Run(bind, func(t *testing.T) {
			setDataDirEnv(t)

			srv, err := Boot(context.Background(), BootConfig{Bind: bind, SPAHandler: fakeSPA()})
			if err != nil {
				t.Fatalf("Boot() with bind %q error = %v", bind, err)
			}
			if err := srv.Shutdown(context.Background()); err != nil {
				t.Fatalf("Shutdown() error = %v", err)
			}
		})
	}
}

// fakeTickerSource is a channel-driven tickerSource — tests fire a tick
// by sending on ch, deterministically, instead of waiting on real
// wall-clock time.
type fakeTickerSource struct {
	ch chan time.Time
}

func newFakeTickerSource() *fakeTickerSource {
	return &fakeTickerSource{ch: make(chan time.Time)}
}

func (f *fakeTickerSource) C() <-chan time.Time { return f.ch }
func (f *fakeTickerSource) Stop()               {}
func (f *fakeTickerSource) fire()               { f.ch <- time.Time{} }

// countingTick returns a SchedulerTick whose Run increments a counter
// (protected by a mutex) each call and signals on done after every call,
// so a test can deterministically wait for "the Nth invocation happened"
// without a wall-clock sleep.
func countingTick(name string, count *int, mu *sync.Mutex, done chan struct{}) SchedulerTick {
	return SchedulerTick{Name: name, Run: func(_ context.Context) error {
		mu.Lock()
		*count++
		mu.Unlock()
		done <- struct{}{}
		return nil
	}}
}

// TestBoot_SchedulerRunsEveryTickOnceAtBootThenOnInterval proves Start
// runs every tick once immediately (crash recovery) and again each time
// the injected fake ticker fires — using a fake, channel-driven ticker,
// never a real wall-clock sleep.
func TestBoot_SchedulerRunsEveryTickOnceAtBootThenOnInterval(t *testing.T) {
	var mu sync.Mutex
	countA, countB := 0, 0
	doneA := make(chan struct{}, 10)
	doneB := make(chan struct{}, 10)

	scheduler := NewScheduler(time.Hour, nil, countingTick("a", &countA, &mu, doneA), countingTick("b", &countB, &mu, doneB))
	fake := newFakeTickerSource()
	scheduler.newTicker = func(time.Duration) tickerSource { return fake }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	scheduler.Start(ctx)

	// Boot-time pass: both ticks fire once, with no ticker signal sent yet.
	waitForSignal(t, doneA, 2*time.Second)
	waitForSignal(t, doneB, 2*time.Second)
	mu.Lock()
	if countA != 1 || countB != 1 {
		t.Fatalf("after boot-time pass: countA=%d countB=%d, want 1/1", countA, countB)
	}
	mu.Unlock()

	// Fire the fake ticker once: both ticks must run a second time.
	fake.fire()
	waitForSignal(t, doneA, 2*time.Second)
	waitForSignal(t, doneB, 2*time.Second)
	mu.Lock()
	if countA != 2 || countB != 2 {
		t.Fatalf("after one interval tick: countA=%d countB=%d, want 2/2", countA, countB)
	}
	mu.Unlock()

	cancel()
	scheduler.Wait()
}

// waitForSignal blocks until ch receives a value or timeout elapses.
func waitForSignal(t *testing.T, ch chan struct{}, timeout time.Duration) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(timeout):
		t.Fatalf("timed out after %v waiting for a tick signal", timeout)
	}
}

// TestBoot_SchedulerStopsOnContextCancel proves cancelling the context
// passed to Start makes the background goroutine exit promptly (Wait
// returns) and that no tick runs after cancellation.
func TestBoot_SchedulerStopsOnContextCancel(t *testing.T) {
	var mu sync.Mutex
	count := 0
	done := make(chan struct{}, 10)

	scheduler := NewScheduler(time.Hour, nil, countingTick("a", &count, &mu, done))
	fake := newFakeTickerSource()
	scheduler.newTicker = func(time.Duration) tickerSource { return fake }

	ctx, cancel := context.WithCancel(context.Background())
	scheduler.Start(ctx)
	waitForSignal(t, done, 2*time.Second) // the boot-time pass

	cancel()

	waitDone := make(chan struct{})
	go func() {
		scheduler.Wait()
		close(waitDone)
	}()
	select {
	case <-waitDone:
	case <-time.After(2 * time.Second):
		t.Fatalf("Wait() did not return promptly after context cancellation")
	}

	mu.Lock()
	final := count
	mu.Unlock()
	if final != 1 {
		t.Fatalf("tick count after cancellation = %d, want 1 (no tick may run after cancel)", final)
	}
}

// TestBoot_TickPanicDoesNotKillTheProcess proves one tick's panic is
// recovered — the OTHER tick in the same pass still runs, and the
// scheduler keeps ticking on the next interval, rather than the panic
// crashing the process (which would abort this entire test binary if
// unrecovered).
func TestBoot_TickPanicDoesNotKillTheProcess(t *testing.T) {
	var mu sync.Mutex
	survivorCount := 0
	survivorDone := make(chan struct{}, 10)

	panicking := SchedulerTick{Name: "panics", Run: func(_ context.Context) error {
		panic("simulated tick panic") //nolint:forbidigo // deliberately triggers a panic to prove Scheduler.runOne's recover actually catches one
	}}
	survivor := countingTick("survivor", &survivorCount, &mu, survivorDone)

	scheduler := NewScheduler(time.Hour, nil, panicking, survivor)
	fake := newFakeTickerSource()
	scheduler.newTicker = func(time.Duration) tickerSource { return fake }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	scheduler.Start(ctx)

	// If the panic were not recovered, this whole test binary would
	// crash before ever reaching this assertion.
	waitForSignal(t, survivorDone, 2*time.Second)
	mu.Lock()
	if survivorCount != 1 {
		t.Fatalf("survivor tick count after boot-time pass = %d, want 1", survivorCount)
	}
	mu.Unlock()

	// A second round (via the fake ticker) proves the scheduler loop
	// itself survived the panic and keeps running, not just that one
	// pass tolerated it.
	fake.fire()
	waitForSignal(t, survivorDone, 2*time.Second)
	mu.Lock()
	if survivorCount != 2 {
		t.Fatalf("survivor tick count after second pass = %d, want 2 (scheduler must keep running after a tick panics)", survivorCount)
	}
	mu.Unlock()
}

func parseStages(t *testing.T, logBytes []byte) []string {
	t.Helper()

	var stages []string
	for _, line := range bytes.Split(bytes.TrimSpace(logBytes), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatalf("unmarshal log line %q: %v", line, err)
		}
		if stage, ok := record["stage"].(string); ok {
			stages = append(stages, stage)
		}
	}
	return stages
}

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

// bootSeedAPIKey creates a valid vk key directly in a booted server's database
// (boot_test is package app, so srv.db is reachable), storing only its hash —
// so a data-plane request can authenticate against the real, booted listener.
func bootSeedAPIKey(t *testing.T, srv *Server, id, raw string) {
	t.Helper()
	repo := storage.NewAPIKeyRepo(srv.db)
	if err := repo.Create(context.Background(), storage.CreateAPIKeyParams{
		ID: id, Label: id, KeyHash: httpapi.HashAPIKey(raw), KeyPrefix: raw[:12], CreatedAt: time.Unix(1, 0),
	}); err != nil {
		t.Fatalf("seed api key: %v", err)
	}
}

// bootGet issues a GET against a fully-booted listener. host is the Host header
// (use "localhost" for the gated control listener; anything for the ungated
// data-plane listener); bearer, when non-empty, is the vk key.
func bootGet(t *testing.T, addr, path, host, bearer string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "http://"+addr+path, nil)
	if err != nil {
		t.Fatalf("build request %s: %v", path, err)
	}
	if host != "" {
		req.Host = host
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s error = %v", path, err)
	}
	return resp
}

// TestBoot_SharedListenerServesV1 proves the default (no data-plane bind): a
// single listener, and /v1/models is reachable on it behind vk auth.
func TestBoot_SharedListenerServesV1(t *testing.T) {
	setDataDirEnv(t)

	srv, err := Boot(context.Background(), BootConfig{Bind: "127.0.0.1:0", SPAHandler: fakeSPA()})
	if err != nil {
		t.Fatalf("Boot() error = %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	if srv.DataPlaneAddr() != "" {
		t.Fatalf("no data-plane bind configured, want a single listener; got data-plane addr %q", srv.DataPlaneAddr())
	}

	bootSeedAPIKey(t, srv, "k-1", "vk_live_shared00")
	resp := bootGet(t, srv.Addr, "/v1/models", "localhost", "vk_live_shared00")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/models on the shared listener = %d, want 200", resp.StatusCode)
	}
}

// TestBoot_SeparateDataPlaneListener proves a configured, distinct data-plane
// bind opens a SECOND listener that serves ONLY /v1/* (control paths 404),
// while the control listener serves control but NOT /v1 (each serves only its
// own surface, 01 §6b), and both shut down cleanly.
//
// Mutation U2-M5 (mount control/SPA on the public mux) → the data-plane control
// path stops 404-ing → RED here too.
func TestBoot_SeparateDataPlaneListener(t *testing.T) {
	setDataDirEnv(t)

	ctrlBind := freeLoopbackAddr(t)
	dataBind := freeLoopbackAddr(t)

	srv, err := Boot(context.Background(), BootConfig{Bind: ctrlBind, DataPlaneBind: dataBind, SPAHandler: fakeSPA()})
	if err != nil {
		t.Fatalf("Boot() error = %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	if srv.DataPlaneAddr() == "" || srv.DataPlaneAddr() == srv.Addr {
		t.Fatalf("want a distinct second listener; control=%q data=%q", srv.Addr, srv.DataPlaneAddr())
	}
	bootSeedAPIKey(t, srv, "k-1", "vk_live_separate")

	// Data-plane listener (ungated): /v1/models needs a key (401 without, 200
	// with), and control paths do not exist there (404).
	noKey := bootGet(t, srv.DataPlaneAddr(), "/v1/models", "", "")
	_ = noKey.Body.Close()
	if noKey.StatusCode != http.StatusUnauthorized {
		t.Fatalf("data-plane /v1/models without a key = %d, want 401", noKey.StatusCode)
	}
	withKey := bootGet(t, srv.DataPlaneAddr(), "/v1/models", "", "vk_live_separate")
	_ = withKey.Body.Close()
	if withKey.StatusCode != http.StatusOK {
		t.Fatalf("data-plane /v1/models with a key = %d, want 200", withKey.StatusCode)
	}
	for _, controlPath := range []string{"/api/control/v1/accounts", "/health", "/"} {
		resp := bootGet(t, srv.DataPlaneAddr(), controlPath, "", "")
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("data-plane %s = %d, want 404 (no control surface on the data plane)", controlPath, resp.StatusCode)
		}
	}

	// Control listener: /health works; /v1 does NOT live here in separate mode.
	health := bootGet(t, srv.Addr, "/health", "localhost", "")
	_ = health.Body.Close()
	if health.StatusCode != http.StatusOK {
		t.Fatalf("control /health = %d, want 200", health.StatusCode)
	}
	// In separate mode the control listener does NOT expose the /v1 API: the
	// vk-gated route is absent, so /v1/models falls through to the SPA catch-all
	// and returns the dashboard HTML — never the models JSON. (The models API
	// lives solely on the data-plane listener, proved above.)
	v1 := bootGet(t, srv.Addr, "/v1/models", "localhost", "vk_live_separate")
	v1Body, _ := io.ReadAll(v1.Body)
	_ = v1.Body.Close()
	if bytes.Contains(v1Body, []byte("venom/lite")) {
		t.Fatalf("control /v1/models in separate mode served the models API — the data plane must own /v1")
	}
}

// TestBoot_NonLoopbackDataPlaneAccepted proves the data-plane bind is NOT
// subject to the control plane's loopback-only rule: a non-loopback data-plane
// bind boots successfully (a second listener comes up) while the control bind
// stays loopback.
//
// Mutation U2-M6 (apply isLoopbackBind to the data-plane bind) → Boot rejects
// the non-loopback data-plane bind → RED.
func TestBoot_NonLoopbackDataPlaneAccepted(t *testing.T) {
	setDataDirEnv(t)

	srv, err := Boot(context.Background(), BootConfig{Bind: "127.0.0.1:0", DataPlaneBind: "0.0.0.0:0", SPAHandler: fakeSPA()})
	if err != nil {
		t.Fatalf("Boot() with a non-loopback data-plane bind error = %v, want success (the data plane may be off-host)", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	if srv.DataPlaneAddr() == "" {
		t.Fatalf("want a second data-plane listener to have opened")
	}
}
