package staticgate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckNoStaticModelList_FlagsThreeElementList(t *testing.T) {
	dir := t.TempDir()
	src := `package fixture

var models = []string{"gpt-4o", "claude-3-opus", "gemini-1.5-pro"}
`
	if err := os.WriteFile(filepath.Join(dir, "fixture.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	violations, err := CheckNoStaticModelList(dir)
	if err != nil {
		t.Fatalf("CheckNoStaticModelList: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("got %d violations, want exactly 1: %v", len(violations), violations)
	}
}

func TestCheckNoStaticModelList_IgnoresBenignLists(t *testing.T) {
	dir := t.TempDir()
	src := `package fixture

var twoModels = []string{"gpt-4o", "claude-3-opus"}
var operations = []string{"chat", "streaming", "tools"}
// Hyphenated but digit-free: proves the digit requirement, not just the
// hyphen/slash requirement, is load-bearing.
var locales = []string{"en-us", "en-gb", "pt-br"}
`
	if err := os.WriteFile(filepath.Join(dir, "fixture.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	testSrc := `package fixture

var testModels = []string{"gpt-4o", "claude-3-opus", "gemini-1.5-pro"}
`
	if err := os.WriteFile(filepath.Join(dir, "fixture_test.go"), []byte(testSrc), 0o644); err != nil {
		t.Fatalf("write test fixture: %v", err)
	}

	violations, err := CheckNoStaticModelList(dir)
	if err != nil {
		t.Fatalf("CheckNoStaticModelList: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("got %d violations, want 0 (two-element list, non-model list, and a test-file model list must all be ignored): %v", len(violations), violations)
	}
}

func TestCheckNoStaticModelList_RealRepoRoot(t *testing.T) {
	root := repoRootFromThisFile(t)
	violations, err := CheckNoStaticModelList(root)
	if err != nil {
		t.Fatalf("CheckNoStaticModelList: %v", err)
	}
	for _, v := range violations {
		t.Logf("violation: %s", v)
	}
	if len(violations) != 0 {
		t.Fatalf("got %d violations in the real repo, want 0", len(violations))
	}
}
