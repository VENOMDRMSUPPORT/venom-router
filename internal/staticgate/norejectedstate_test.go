package staticgate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckNoRejectedState_FlagsStringLiteral(t *testing.T) {
	dir := t.TempDir()
	src := `package fixture

const s = "rejected"
`
	if err := os.WriteFile(filepath.Join(dir, "fixture.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	violations, err := CheckNoRejectedState(dir)
	if err != nil {
		t.Fatalf("CheckNoRejectedState: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("got %d violations, want exactly 1: %v", len(violations), violations)
	}
}

func TestCheckNoRejectedState_IgnoresCommentsAndIdentifiers(t *testing.T) {
	dir := t.TempDir()
	src := `package fixture

// the second insert is rejected by the index
var rejectedCount int
`
	if err := os.WriteFile(filepath.Join(dir, "fixture.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	violations, err := CheckNoRejectedState(dir)
	if err != nil {
		t.Fatalf("CheckNoRejectedState: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("got %d violations, want 0 (comment/identifier must not be flagged): %v", len(violations), violations)
	}
}

func TestCheckNoRejectedState_FlagsSQLCheck(t *testing.T) {
	dir := t.TempDir()
	src := "CREATE TABLE t (status TEXT NOT NULL CHECK (status IN ('rejected')));\n"
	if err := os.WriteFile(filepath.Join(dir, "0001_test.sql"), []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	violations, err := CheckNoRejectedState(dir)
	if err != nil {
		t.Fatalf("CheckNoRejectedState: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("got %d violations, want exactly 1: %v", len(violations), violations)
	}
}

func TestCheckNoRejectedState_RealRepoRoot(t *testing.T) {
	root := repoRootFromThisFile(t)
	violations, err := CheckNoRejectedState(root)
	if err != nil {
		t.Fatalf("CheckNoRejectedState: %v", err)
	}
	for _, v := range violations {
		t.Logf("violation: %s", v)
	}
	// ZERO, with no allowlist and no known exception. A static check whose
	// healthy state is "exactly one violation" cannot be used as a gate by
	// anything else, and the natural response to the next drift is to bump
	// the expected count — which is how these checks die. The one real
	// occurrence this check found (a quota-reservation audit outcome in
	// internal/storage/quota_lifecycle.go) was therefore fixed at the
	// source, renamed to "illegal_transition", rather than exempted here.
	if len(violations) != 0 {
		t.Fatalf("got %d violations, want 0 — no \"%s\" state may exist anywhere in code or schema (04 §5)", len(violations), "reject"+"ed")
	}
}
