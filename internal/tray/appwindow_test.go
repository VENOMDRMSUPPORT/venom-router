package tray

import (
	"errors"
	"strings"
	"testing"
	"time"
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

// TestIsControlWindow pins how the tray recognises its own already-open control
// window among every top-level window on the desktop. The rejections carry the
// weight: matching on the title alone would let a tray tap raise (and then
// consider "already open") an unrelated browser window that merely happens to
// display a page with the same title — including the dashboard, whose own title
// starts with the same two words.
func TestIsControlWindow(t *testing.T) {
	const chromiumClass = "Chrome_WidgetWin_1"

	cases := []struct {
		name    string
		class   string
		title   string
		visible bool
		want    bool
	}{
		{"the control app-window", chromiumClass, controlWindowTitle, true, true},
		{"another chromium class generation", "Chrome_WidgetWin_2", controlWindowTitle, true, true},
		{"not a chromium window", "Notepad", controlWindowTitle, true, false},
		{"the dashboard window", chromiumClass, "Venom Router — Dashboard", true, false},
		{"the pre-fix bare title", chromiumClass, "Venom Router", true, false},
		{"title merely contains ours", chromiumClass, controlWindowTitle + " - Profile 1", true, false},
		{"hidden helper window", chromiumClass, controlWindowTitle, false, false},
		{"empty everything", "", "", false, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isControlWindow(c.class, c.title, c.visible); got != c.want {
				t.Errorf("isControlWindow(%q, %q, %v) = %v, want %v",
					c.class, c.title, c.visible, got, c.want)
			}
		})
	}
}

// TestResolveTapReusesOneWindow is the single-window contract: a tray left-click
// raises the existing control window instead of spawning a second one, and a
// click that lands while a spawn is still in flight is dropped rather than
// stacking another window (a Chromium app-window needs ~1-2s to become
// enumerable, so "focus found nothing" is NOT proof that none is coming).
func TestResolveTapReusesOneWindow(t *testing.T) {
	t0 := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)

	t.Run("the first tap belongs to this tray session, never an inherited window", func(t *testing.T) {
		g := &appWindowGate{}
		focusCalls := 0
		spawns := 0
		got, err := g.resolveTap(t0, func() bool { focusCalls++; return true }, func() error { spawns++; return nil })
		if err != nil || got != tapSpawned {
			t.Fatalf("outcome = %v err = %v, want tapSpawned nil", got, err)
		}
		if focusCalls != 0 {
			t.Errorf("focus calls = %d, want 0 — a same-titled window inherited from an exited tray is stale", focusCalls)
		}
		if spawns != 1 {
			t.Errorf("spawns = %d, want 1 current-session window", spawns)
		}
	})

	t.Run("an existing window is focused, never duplicated", func(t *testing.T) {
		g := &appWindowGate{sessionStarted: true}
		spawns := 0
		got, err := g.resolveTap(t0, func() bool { return true }, func() error { spawns++; return nil })
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if got != tapFocused {
			t.Errorf("outcome = %v, want tapFocused", got)
		}
		if spawns != 0 {
			t.Errorf("spawns = %d, want 0 — the existing window must be reused", spawns)
		}
	})

	t.Run("no window yet -> spawn once", func(t *testing.T) {
		g := &appWindowGate{}
		spawns := 0
		got, err := g.resolveTap(t0, func() bool { return false }, func() error { spawns++; return nil })
		if err != nil || got != tapSpawned || spawns != 1 {
			t.Fatalf("outcome = %v spawns = %d err = %v, want tapSpawned 1 nil", got, spawns, err)
		}
	})

	t.Run("a tap during the spawn cooldown is dropped", func(t *testing.T) {
		g := &appWindowGate{}
		spawns := 0
		spawn := func() error { spawns++; return nil }
		noWindow := func() bool { return false }

		if _, err := g.resolveTap(t0, noWindow, spawn); err != nil {
			t.Fatalf("first tap: %v", err)
		}
		got, err := g.resolveTap(t0.Add(appWindowSpawnCooldown-time.Millisecond), noWindow, spawn)
		if err != nil {
			t.Fatalf("second tap: %v", err)
		}
		if got != tapDropped {
			t.Errorf("outcome = %v, want tapDropped", got)
		}
		if spawns != 1 {
			t.Errorf("spawns = %d, want 1 — the in-flight window must not be duplicated", spawns)
		}
	})

	t.Run("the window appearing wins over the cooldown", func(t *testing.T) {
		g := &appWindowGate{}
		spawn := func() error { return nil }
		if _, err := g.resolveTap(t0, func() bool { return false }, spawn); err != nil {
			t.Fatalf("first tap: %v", err)
		}
		got, err := g.resolveTap(t0.Add(time.Second), func() bool { return true }, spawn)
		if err != nil || got != tapFocused {
			t.Errorf("outcome = %v err = %v, want tapFocused nil", got, err)
		}
	})

	t.Run("after the cooldown a closed window is reopened", func(t *testing.T) {
		g := &appWindowGate{}
		spawns := 0
		spawn := func() error { spawns++; return nil }
		noWindow := func() bool { return false }

		if _, err := g.resolveTap(t0, noWindow, spawn); err != nil {
			t.Fatalf("first tap: %v", err)
		}
		got, err := g.resolveTap(t0.Add(appWindowSpawnCooldown), noWindow, spawn)
		if err != nil || got != tapSpawned || spawns != 2 {
			t.Errorf("outcome = %v spawns = %d err = %v, want tapSpawned 2 nil", got, spawns, err)
		}
	})

	// A failed spawn produced no window, so arming the cooldown would swallow
	// the owner's next click for three seconds and look like a dead tray icon.
	t.Run("a failed spawn does not arm the cooldown", func(t *testing.T) {
		g := &appWindowGate{}
		boom := errors.New("browser missing")
		spawns := 0
		noWindow := func() bool { return false }

		if _, err := g.resolveTap(t0, noWindow, func() error { spawns++; return boom }); !errors.Is(err, boom) {
			t.Fatalf("err = %v, want %v", err, boom)
		}
		got, err := g.resolveTap(t0.Add(time.Millisecond), noWindow, func() error { spawns++; return nil })
		if err != nil || got != tapSpawned || spawns != 2 {
			t.Errorf("retry: outcome = %v spawns = %d err = %v, want tapSpawned 2 nil", got, spawns, err)
		}
	})
}

// TestReplaceControlWindowRetiresStaleBeforeOpening guards the cross-process
// boundary: Chromium can keep the old app-window alive after venom.exe exits,
// so opening the new ephemeral URL before retiring that window lets Chromium
// reuse the dead page and makes every control button appear unresponsive.
func TestReplaceControlWindowRetiresStaleBeforeOpening(t *testing.T) {
	var events []string
	err := replaceControlWindow(
		func() { events = append(events, "retire") },
		func() error { events = append(events, "open"); return nil },
	)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got := strings.Join(events, ","); got != "retire,open" {
		t.Errorf("events = %q, want retire,open", got)
	}
}

// TestControlPageTitleMatchesConstant guards the one coupling the focus path
// cannot express in code: isControlWindow matches on an exact title string, so
// the served page's <title> and controlWindowTitle must never drift apart —
// editing either one alone silently breaks window reuse.
func TestControlPageTitleMatchesConstant(t *testing.T) {
	want := "<title>" + controlWindowTitle + "</title>"
	if !strings.Contains(controlPageTemplate, want) {
		t.Errorf("controlpage.html does not contain %q — the focus path would never find the window", want)
	}
}

func containsArg(args []string, want string) bool {
	for _, a := range args {
		if strings.Contains(a, want) {
			return true
		}
	}
	return false
}
