package tray

import (
	"strings"
	"testing"
)

// TestResolveAppWindowCommand pins the launcher's browser selection: the first
// present candidate wins and is launched in chromeless app-window mode; when
// none is present the caller is told to fall back (ok=false).
func TestResolveAppWindowCommand(t *testing.T) {
	edge := browserCandidate{Name: "edge", Path: `C:\Edge\msedge.exe`}
	chrome := browserCandidate{Name: "chrome", Path: `C:\Chrome\chrome.exe`}
	missing := browserCandidate{Name: "edge", Path: ""}
	url := "http://127.0.0.1:5555/"

	t.Run("first present candidate (edge) wins", func(t *testing.T) {
		name, args, ok := resolveAppWindowCommand([]browserCandidate{edge, chrome}, url)
		if !ok {
			t.Fatal("ok = false, want true")
		}
		if name != edge.Path {
			t.Errorf("name = %q, want %q", name, edge.Path)
		}
		if !containsArg(args, "--app="+url) {
			t.Errorf("args = %v, want an --app=%s entry", args, url)
		}
	})

	t.Run("falls through to chrome when edge is absent", func(t *testing.T) {
		name, _, ok := resolveAppWindowCommand([]browserCandidate{missing, chrome}, url)
		if !ok || name != chrome.Path {
			t.Errorf("name = %q ok = %v, want %q true", name, ok, chrome.Path)
		}
	})

	t.Run("no browser present -> caller must fall back", func(t *testing.T) {
		_, _, ok := resolveAppWindowCommand([]browserCandidate{missing}, url)
		if ok {
			t.Error("ok = true, want false when no browser is present")
		}
	})
}

func containsArg(args []string, want string) bool {
	for _, a := range args {
		if strings.Contains(a, want) {
			return true
		}
	}
	return false
}
