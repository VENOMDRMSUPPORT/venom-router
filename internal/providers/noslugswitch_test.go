package providers

import (
	"os"
	"path/filepath"
	"testing"
)

// TestNoSlugSwitch runs CheckNoSlugSwitch against this package's own real
// source. It must report zero violations.
func TestNoSlugSwitch(t *testing.T) {
	violations, err := CheckNoSlugSwitch(".")
	if err != nil {
		t.Fatalf("CheckNoSlugSwitch: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("found %d slug-switch violation(s) in this package's own source:\n%v", len(violations), violations)
	}
}

// TestNoSlugSwitch_FiresOnStringLiteralSwitch proves the check actually
// bites: a fixture file with a switch dispatching on string-literal
// provider slugs must be flagged.
func TestNoSlugSwitch_FiresOnStringLiteralSwitch(t *testing.T) {
	dir := t.TempDir()
	src := `package fixture

func dispatch(providerSlug string) string {
	switch providerSlug {
	case "openai":
		return "a"
	case "anthropic":
		return "b"
	}
	return ""
}
`
	if err := os.WriteFile(filepath.Join(dir, "fixture.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	violations, err := CheckNoSlugSwitch(dir)
	if err != nil {
		t.Fatalf("CheckNoSlugSwitch: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("got %d violations, want exactly 1 (the fixture's string-literal switch)", len(violations))
	}
}

// TestNoSlugSwitch_IgnoresTypeSwitchAndMixedCases proves the check does
// not over-fire: a type switch and a switch with a non-string-literal
// case must both be left alone.
func TestNoSlugSwitch_IgnoresTypeSwitchAndMixedCases(t *testing.T) {
	dir := t.TempDir()
	src := `package fixture

func typeSwitch(v any) string {
	switch v.(type) {
	case string:
		return "s"
	case int:
		return "i"
	}
	return ""
}

func mixedSwitch(id string, other string) string {
	switch id {
	case "openai":
		return "a"
	case other:
		return "b"
	}
	return ""
}
`
	if err := os.WriteFile(filepath.Join(dir, "fixture.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	violations, err := CheckNoSlugSwitch(dir)
	if err != nil {
		t.Fatalf("CheckNoSlugSwitch: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("got %d violations, want 0 (a type switch and a mixed-case switch are not slug switches):\n%v", len(violations), violations)
	}
}
