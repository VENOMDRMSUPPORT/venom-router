package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/observability"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/platform"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// TestBoot_HappyPath is Test B1: the startup sequence runs and the
// listener comes up for real — asserted by dialing httpapi's gated
// /health route over an actual TCP connection, not by inspecting
// internal state.
func TestBoot_HappyPath(t *testing.T) {
	setDataDirEnv(t)

	var logBuf bytes.Buffer
	logger := observability.New(slog.NewJSONHandler(&logBuf, nil))

	srv, err := Boot(context.Background(), BootConfig{Bind: "127.0.0.1:0", Logger: logger})
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

// TestBoot_FailClosedOnMigrationFailure is Test B2: a forced
// checksum-tamper failure (the same fail-closed mechanism P0-DB-002
// proved) must abort Boot before net.Listen is ever reached, so nothing
// is accepting connections on the address it would have used.
func TestBoot_FailClosedOnMigrationFailure(t *testing.T) {
	setDataDirEnv(t)
	ctx := context.Background()

	// First boot succeeds: establishes the baseline migration and its
	// recorded checksum, then shuts down cleanly.
	srv1, err := Boot(ctx, BootConfig{Bind: "127.0.0.1:0"})
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

	srv2, err := Boot(ctx, BootConfig{Bind: bind, Logger: logger})
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

// TestBoot_StartupOrderEnforced is Test B3: it asserts the exact,
// in-order sequence of stages Boot logged, not merely the end state —
// proving lock-before-DB, migrate-before-mount/listen, etc.
func TestBoot_StartupOrderEnforced(t *testing.T) {
	setDataDirEnv(t)

	var logBuf bytes.Buffer
	logger := observability.New(slog.NewJSONHandler(&logBuf, nil))

	srv, err := Boot(context.Background(), BootConfig{Bind: "127.0.0.1:0", Logger: logger})
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
