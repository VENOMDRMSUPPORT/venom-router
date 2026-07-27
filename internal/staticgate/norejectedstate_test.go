package staticgate

import (
	"os"
	"path/filepath"
	"strings"
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
	// internal/storage/quota_lifecycle.go stamps a QUOTA RESERVATION audit
	// outcome as "rejected" (quota.ReservationState is a wholly different
	// vocabulary from models.CertificationState — see
	// internal/quota/lifecycle.go). It is not a certification "rejected"
	// state (04 §5's rule is about CertificationState specifically), and
	// internal/storage is frozen this batch so it cannot be renamed here.
	// This is the one known, reviewed, non-certification exception to an
	// otherwise-zero repo; ANY OTHER violation is a real regression and
	// fails this test.
	if len(violations) != 1 {
		t.Fatalf("got %d violations, want exactly 1 (the known quota-lifecycle exception)", len(violations))
	}
	v := violations[0]
	if !strings.HasSuffix(filepath.ToSlash(v.File), "internal/storage/quota_lifecycle.go") {
		t.Fatalf("unexpected violation location: %+v", v)
	}
}
