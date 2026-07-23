package execution

import "testing"

// TestNoSlugSwitch runs CheckNoSlugSwitch against this package's own real
// source. It must report zero violations. This same test, re-run with a
// temporary offending file present (see the unit's report for the
// fires-proof transcript), is what proves the check actually fires
// rather than merely existing.
func TestNoSlugSwitch(t *testing.T) {
	violations, err := CheckNoSlugSwitch(".")
	if err != nil {
		t.Fatalf("CheckNoSlugSwitch: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("found %d slug-switch violation(s):\n%v", len(violations), violations)
	}
}
