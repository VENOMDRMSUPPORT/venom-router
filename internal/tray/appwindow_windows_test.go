//go:build windows

package tray

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/platform"
)

// TestBrowserPathsFromInstallRoots pins the two halves of browser location:
// which well-known paths each root contributes and in what order, and that a
// root reported as "" contributes NOTHING. The empty-root case matters
// because filepath.Join("", "Microsoft", …) yields a RELATIVE path, which
// exec.Command would resolve against the process working directory — the tray
// would then launch whatever unrelated msedge.exe happened to sit there.
func TestBrowserPathsFromInstallRoots(t *testing.T) {
	roots := platform.InstallRoots{
		ProgramFiles:    `C:\PF`,
		ProgramFilesX86: `C:\PFx86`,
		LocalAppData:    `C:\LAD`,
	}

	t.Run("edge prefers the x86 root, then ProgramFiles", func(t *testing.T) {
		got := rootDerived(edgePaths(roots))
		want := []string{
			filepath.Join(`C:\PFx86`, "Microsoft", "Edge", "Application", "msedge.exe"),
			filepath.Join(`C:\PF`, "Microsoft", "Edge", "Application", "msedge.exe"),
		}
		assertPrefix(t, got, want)
	})

	t.Run("chrome covers ProgramFiles, x86 and the per-user root", func(t *testing.T) {
		got := rootDerived(chromePaths(roots))
		want := []string{
			filepath.Join(`C:\PF`, "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(`C:\PFx86`, "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(`C:\LAD`, "Google", "Chrome", "Application", "chrome.exe"),
		}
		assertPrefix(t, got, want)
	})

	t.Run("no root set -> no relative candidate is ever produced", func(t *testing.T) {
		none := platform.InstallRoots{}
		for name, paths := range map[string][]string{
			"edge":   edgePaths(none),
			"chrome": chromePaths(none),
		} {
			for _, p := range paths {
				// Anything left comes from exec.LookPath (an absolute path on
				// a machine that has the browser on PATH), never from a join
				// onto an empty root.
				if !filepath.IsAbs(p) {
					t.Errorf("%s: candidate %q is not absolute — an empty install root leaked into a join", name, p)
				}
			}
		}
	})
}

// rootDerived drops any PATH-resolved candidate (exec.LookPath finds a real
// browser on a developer machine and finds nothing on a bare runner), leaving
// only the deterministic root-derived entries the test asserts on.
func rootDerived(paths []string) []string {
	var out []string
	for _, p := range paths {
		if strings.HasPrefix(p, `C:\PF`) || strings.HasPrefix(p, `C:\LAD`) {
			out = append(out, p)
		}
	}
	return out
}

func assertPrefix(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("root-derived candidates = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("candidate[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
