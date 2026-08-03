# Tray GUI window (Part B) — design

**Date:** 2026-08-03
**Status:** approved (owner)
**Depends on:** Part A tray live-reload dev backend (`0b104d5`) — `EnterDevMode`/`ExitDevMode`.

## Goal

The app is a tray app. Today its only surface is the systray right-click menu.
The owner wants a small options **window** that opens on a **left-click** of the
tray icon, exposes the same actions as buttons, and — when closed — leaves the
app running in the tray. No cgo (the machine has no C compiler), so the window
is an **app-window of a web control page** that reuses the existing web stack,
not a native (walk/webview) window.

## Constraints

- **No cgo.** Rules out webview_go and most native GUI toolkits.
- **The window must drive the tray across every state**, including while
  production is stopped. So it cannot depend on the production app server (that
  server is *down* when prod is stopped — you could never press "Start
  Production" from a page it hosts). The window talks to a **tray-owned control
  listener** that is always up while the tray runs.
- **One shared database, lifecycle safety.** All dev/prod transitions route
  through the existing, already-WAL-safe primitives (`Controller`,
  `EnterDevMode`/`ExitDevMode`). Part B adds **no new database code paths**.
- **Public repo + a control surface that can Start/Stop/Quit** ⇒ the listener
  must resist localhost CSRF / DNS-rebinding from any web page the user visits.

## Architecture

Four units, each independently testable.

### 1. Tray control server — `internal/tray/controlserver.go` (package `tray`, platform-neutral)

A `net/http.Server` bound to `127.0.0.1:0` (OS-assigned ephemeral port), started
when the tray boots and shut down when it exits. It depends only on a
`TrayControls` interface (below) and an injected token, so it is fully testable
with `net/http/httptest` and a fake.

Routes:

| Method + path        | Effect                                                    |
|----------------------|-----------------------------------------------------------|
| `GET  /`             | Serve the embedded control page (token baked in)          |
| `GET  /state`        | JSON snapshot: prod state, dev state, autostart           |
| `POST /prod/start`   | `TrayControls.StartProd()`                                |
| `POST /prod/stop`    | `TrayControls.StopProd()`                                 |
| `POST /prod/open`    | `TrayControls.OpenProdDashboard()`                        |
| `POST /dev/start`    | `TrayControls.StartDev()` (→ `EnterDevMode`)              |
| `POST /dev/stop`     | `TrayControls.StopDev()` (→ `ExitDevMode`)                |
| `POST /dev/open`     | `TrayControls.OpenDevDashboard()`                         |
| `POST /logs`         | `TrayControls.OpenLogs()`                                 |
| `POST /autostart`    | `TrayControls.SetAutostart(enabled)` from JSON body       |
| `POST /quit`         | `TrayControls.Quit()`                                     |

All handlers are non-blocking from the UI's perspective: lifecycle calls that
may block (prod stop, dev teardown) are run in their own goroutine inside the
`TrayControls` adapter, so an HTTP handler returns promptly and the page polls
`/state` for the result.

`URL()` returns `http://127.0.0.1:<port>/` for the launcher.

### 2. Security model

The control server can start, stop, and **quit** the app, so it is hardened
against a malicious web page the user happens to have open:

- **Loopback bind only** (`127.0.0.1`), never `0.0.0.0`.
- **Ephemeral random port** — an attacker page cannot know the port a priori.
- **Per-startup random token** (32 bytes, crypto/rand, hex). It is baked into
  the served HTML. **Every request except the `GET /` bootstrap** (i.e.
  `GET /state` and all `POST`s) must present it in a **custom header**
  `X-Venom-Control-Token`. A custom header forces a CORS preflight for any
  cross-origin caller, which the server never approves; and same-origin policy
  keeps the baked-in token unreadable to any other origin's JavaScript.
- **Origin allowlist.** Any request carrying an `Origin` header that is not the
  server's own origin is rejected `403`, before any side effect.
- `GET /` is intentionally token-free (it is the top-level navigation from
  `msedge --app`, which cannot set a custom header). It only returns the page;
  cross-origin readers get an opaque response and cannot read the token.

Failure mode: a request missing/!matching the token, or with a foreign Origin,
gets `403 Forbidden` and performs no action.

### 3. `TrayControls` interface + adapter

```go
type ControlState struct {
    Prod      string // "running" | "stopped" | "error"
    Dev       DevStatusView
    Autostart bool
}

type TrayControls interface {
    State() ControlState
    StartProd()
    StopProd()
    StartDev()
    StopDev()
    OpenProdDashboard()
    OpenDevDashboard()
    OpenLogs()
    SetAutostart(enabled bool) error
    Quit()
}
```

The concrete adapter (`trayControlsAdapter`) is a thin closure-holder over the
root `ctx`, the `*Controller`, the `*DevSupervisor`, the autostart funcs, and
the root `cancel`. Its bodies are one-liners:

- `StartProd` → `go c.Start(ctx)`; `StopProd` → `go c.Stop()`
- `StartDev` → `go EnterDevMode(ctx, c, dev)`; `StopDev` → `go ExitDevMode(ctx, c, dev)`
- `OpenProdDashboard` → `c.OpenDashboard()`; `OpenDevDashboard` → `c.OpenURL(dev.DashboardURL())`
- `OpenLogs` → `c.OpenLogs()`
- `SetAutostart` → `enableAutostart()`/`disableAutostart()`
- `Quit` → `cancel()` (funnels into the single `ctx.Done()` → `ShutdownAndExit`)

The adapter is trivial glue; the routing, JSON shape, and security are what the
tests pin (against a fake `TrayControls`).

### 4. App-window launcher — `internal/tray/appwindow_windows.go`

- `resolveAppWindowCommand(candidates, url) (name string, args []string, ok bool)`
  is **pure and platform-neutral** (own file, no build tag) so it runs on both
  CI jobs. Given an ordered list of browser candidates (Edge, then Chrome) with
  their resolved executable paths, it returns the first present browser and the
  app-window args (`--app=<url>`, a modest `--window-size`), or `ok=false` when
  none is present.
- The Windows-only exec wrapper `openControlWindow(url)` resolves Edge/Chrome
  paths (well-known `%ProgramFiles%`/`%LOCALAPPDATA%` locations + `PATH`), calls
  the resolver, and launches it; on `ok=false` it falls back to the existing
  `shellOpen(url)` (opens the page as a normal browser tab — still fully
  functional, just not a chromeless window).

### 5. Wiring — `internal/tray/tray_windows.go`

In `RunNativeUI`, after the controller/dev are ready and before/around
`systray.Run`:

- Build `trayControlsAdapter`, generate the token, construct the control server,
  `Start()` it (goroutine), capture its `URL()`.
- `systray.SetOnTapped(func() { go openControlWindow(server.URL()) })` — left
  click opens the window. Right-click is left untouched, so the existing context
  menu still appears (redundant, safe fallback).
- On teardown (the existing `ctx.Done()` path / `onExit`), call
  `server.Shutdown(context.Background())` so the listener closes cleanly.

The control server lifetime is tied to the tray process; closing the window does
nothing to it (separate browser process) — satisfying "closing the window keeps
the app in the tray".

## Control page (`internal/tray/controlpage.html`, `go:embed`)

A single self-contained HTML file (inline CSS + vanilla JS, no build step, no
external assets) reusing the dashboard's visual tokens where practical. It:

- Polls `GET /state` every ~1.5s and renders prod + dev status lines.
- Shows the action buttons; button **enablement mirrors the menu**
  (`prodEnablement`/`devEnablement` semantics), computed in JS from `/state`.
  Notably "Start Production" is disabled while dev is active (both take 8081 +
  the one-DB lock), which closes the Part A footgun from the window.
- Sends every action as `fetch(POST, {headers: {'X-Venom-Control-Token': …}})`.
- `Quit` asks for a confirm() before POSTing `/quit`.

## Testing (strict TDD)

- **controlserver_test.go** (`httptest`, fake `TrayControls`):
  - each `POST` route invokes exactly the matching `TrayControls` method;
  - `GET /state` returns the expected JSON for a given `ControlState`;
  - a token-gated request (e.g. `POST /prod/stop`, `GET /state`) with a
    missing/wrong token → `403`, no method called;
  - a token-gated request with a foreign `Origin` → `403`, no method called;
  - `GET /` returns 200 HTML containing the token;
  - `/autostart` parses the `enabled` bool and calls `SetAutostart`.
- **appwindow_test.go** (platform-neutral): `resolveAppWindowCommand` table —
  Edge present → Edge `--app`; only Chrome → Chrome; neither → `ok=false`.
- The adapter and wiring are thin glue over Part-A-tested primitives; the
  `EnterDevMode`/`ExitDevMode` ordering + WAL-safe teardown are already covered.

## Out of scope / non-goals

- No authentication beyond the loopback+token+Origin model (single-user
  desktop). No TLS (loopback only).
- No packaging of a bundled browser; we use an installed Edge/Chrome or fall
  back to the default browser tab.
- No change to production or dev lifecycle semantics; Part B is purely an
  additional surface over existing operations.

## Security note recap

The one genuinely new attack surface is the control listener. It is mitigated by
loopback bind + ephemeral port + per-startup token (custom header) + Origin
allowlist, so no third-party web page can drive it. This is the standard
hardening for a local control server and is proportionate to its power
(Start/Stop/Quit).
