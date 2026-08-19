package tray

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// This file is the platform-neutral core of the tray's app-window launcher:
// choosing which installed browser to open the control page with in chromeless
// "app" mode, recognising a control window that is already open, and deciding
// what a single tray left-click should therefore do. The OS-specific executable
// resolution, window enumeration and process spawn live in
// appwindow_windows.go / winapi_windows.go.

// browserCandidate is a browser we may launch in app-window mode. Path is the
// resolved executable path, or "" when that browser is not installed.
type browserCandidate struct {
	Name string
	Path string
}

// appWindowWidth / appWindowHeight keep the control window compact enough to
// avoid the big dead margins that Chromium's default app-window sizing can
// leave around the actual controls.
const (
	appWindowWidth  = 560
	appWindowHeight = 440
)

var appWindowLaunchArgs = func(url string) []string {
	return []string{
		"--app=" + url,
		fmt.Sprintf("--window-size=%d,%d", appWindowWidth, appWindowHeight),
	}
}

// resolveAppWindowCommand returns the executable and args to open url in a
// chromeless app-window using the first present candidate, or ok=false when
// none is installed (the caller then falls back to a normal browser tab).
func resolveAppWindowCommand(candidates []browserCandidate, url string) (name string, args []string, ok bool) {
	for _, c := range candidates {
		if c.Path == "" {
			continue
		}
		return c.Path, appWindowLaunchArgs(url), true
	}
	return "", nil, false
}

// replaceControlWindow retires any Chromium app-window inherited from an
// earlier venom.exe process before opening this process's control URL.
func replaceControlWindow(retire func(), open func() error) error {
	retire()
	return open()
}

// controlWindowTitle is the control page's document title, which Chromium uses
// verbatim as the app-window's caption. It is the handle the tray identifies its
// own already-open window by, so it must stay in lock-step with the <title> in
// controlpage.html (TestControlPageTitleMatchesConstant guards that) and must
// not be a prefix of any other Venom window title — the dashboard's is
// "Venom Router — Dashboard".
const controlWindowTitle = "Venom Router Control"

// chromiumWindowClassPrefix is the window class Chromium gives its top-level
// windows; the trailing digit is a class generation ("Chrome_WidgetWin_1"), so
// only the prefix is stable.
const chromiumWindowClassPrefix = "Chrome_WidgetWin_"

// nativeWebViewWindowClass is the top-level class used by go-webview2.
const nativeWebViewWindowClass = "webview"

// appWindowSpawnCooldown is how long after a spawn a tap that still cannot see
// the window is assumed to be racing that spawn rather than reporting a closed
// window. Generous enough for a cold Chromium start, short enough that closing
// the window and immediately clicking again feels responsive.
const appWindowSpawnCooldown = 3 * time.Second

// isControlWindow reports whether a top-level window is the tray's own control
// app-window. Class, exact title and visibility must all agree: title-only
// matching would let a tap raise an unrelated browser window showing a
// same-titled page and then treat it as the control window.
func isControlWindow(class, title string, visible bool) bool {
	return visible &&
		(strings.HasPrefix(class, chromiumWindowClassPrefix) || strings.EqualFold(class, nativeWebViewWindowClass)) &&
		title == controlWindowTitle
}

// tapOutcome is what one tray left-click did, for logging and tests.
type tapOutcome int

const (
	tapFocused tapOutcome = iota // an existing control window was raised
	tapSpawned                   // a new control window was launched
	tapDropped                   // ignored: a spawn is still in flight
)

func (o tapOutcome) String() string {
	switch o {
	case tapFocused:
		return "focused"
	case tapSpawned:
		return "spawned"
	case tapDropped:
		return "dropped"
	}
	return "unknown"
}

// appWindowGate serialises tray taps and remembers the last spawn, so that the
// window is opened at most once no matter how often the icon is clicked.
type appWindowGate struct {
	mu             sync.Mutex
	lastSpawn      time.Time // zero until a spawn has succeeded
	sessionStarted bool      // false until this venom process owns a window
}

// resolveTap decides and performs what a single tray left-click does: raise the
// existing control window if focus finds one, else spawn a new one, unless a
// spawn from the last appWindowSpawnCooldown has not had time to produce an
// enumerable window yet — in which case the tap is dropped.
//
// A spawn that fails deliberately does NOT arm the cooldown: it produced no
// window, so the owner's next click must be free to try again.
func (g *appWindowGate) resolveTap(now time.Time, focus func() bool, spawn func() error) (tapOutcome, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	// A same-titled Chromium app-window can outlive the venom.exe process that
	// created it. The first tap of a new tray session must therefore never focus
	// an inherited window; its URL points at the previous ephemeral control port.
	// The Windows spawn closure retires that stale window before opening ours.
	if !g.sessionStarted {
		if err := spawn(); err != nil {
			return tapSpawned, err
		}
		g.sessionStarted = true
		g.lastSpawn = now
		return tapSpawned, nil
	}

	if focus() {
		return tapFocused, nil
	}
	if !g.lastSpawn.IsZero() && now.Sub(g.lastSpawn) < appWindowSpawnCooldown {
		return tapDropped, nil
	}
	if err := spawn(); err != nil {
		return tapSpawned, err
	}
	g.lastSpawn = now
	return tapSpawned, nil
}
