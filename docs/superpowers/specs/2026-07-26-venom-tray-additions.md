# Design addendum — Tray additions (2026-07-26)

Follows `2026-07-25-venom-tray-p6-fnd-001-design.md` (r5). Owner requested three
additions after confirming the base tray works. Single-process/single-binary
and all Global Constraints from the base spec still hold. Two increments:

## Increment 1 — Stop/Start menu items + "Start with Windows" (internal/tray)

Both touch `tray_windows.go`'s menu → ONE implementer, sequential.

### 1a. Stop / Start
- `Controller.Stop()`: a **re-runnable** bounded `ServerLifecycle.Shutdown`
  (`ShutdownTimeout`), NOT the `sync.Once`-guarded `Quit`/`ShutdownAndExit`. On
  success → `StateStopped`; on timeout/error → `StateError`. **Never exits the
  process** (this is what distinguishes it from Quit). No-op if already stopped.
  Note: `Server.Shutdown` releases the single-instance lock, so after Stop the
  lock is free; Start re-acquires it via `Boot`.
- `Controller.Start(ctx)`: if not already Running, `ServerLifecycle.Boot`;
  success → `StateRunning`, failure → `StateError`. No-op if Running.
- `Restart(ctx)` is refactored to `Stop()` then (only if not `StateError`)
  `Start(ctx)` — preserves the owner-condition-2 rule (a dirty/failed Stop skips
  the re-Boot and stays `Error`). Guard all three with the existing lifecycle
  mutex (rename `restartMu` → `lifecycleMu`).
- Menu (`tray_windows.go` onReady): add `Start` and `Stop` items alongside
  `Restart`/`Quit`. The ~2s status ticker enables/disables them by state:
  Running → Stop enabled, Start disabled; Stopped/Error → Start enabled, Stop
  disabled. Clicks run in a goroutine (as Restart already does).
- Tests (Controller-level, no systray, injected fake lifecycle + `Exit` fake):
  Stop → server shut down + `StateStopped` + Exit NOT called; Start → Boot +
  `StateRunning`; Stop-then-Start round-trip; Stop with a shutdown error →
  `StateError`, no exit, and a following Start still works.

### 1b. Start with Windows (Windows-only)
- `autostart_windows.go` (`//go:build windows`): `autostartEnabled() bool`,
  `enableAutostart() error`, `disableAutostart() error` over
  `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`, value name `VenomRouter`,
  data = `os.Executable()`. Use `golang.org/x/sys/windows/registry` (already in
  the `golang.org/x/sys` module). No admin rights, no COM/shortcut.
- `autostart_other.go` (`//go:build !windows`): stubs (`autostartEnabled`
  returns false; enable/disable return nil) — the menu item is only built in the
  Windows adapter regardless.
- Menu: a **checkable** item "Start with Windows" (systray
  `AddMenuItemCheckbox`), initial checked = `autostartEnabled()`; on click →
  toggle enable/disable, then reflect the new state on the item; log on error
  (never panic).
- Test (Windows-only): enable → `autostartEnabled()` true → disable → false, with
  guaranteed cleanup. To avoid clobbering the user's real Run entry during tests,
  the helpers read the value name from an unexported package var so the test can
  point at a throwaway name (e.g. `VenomRouterTest`) and delete it in `t.Cleanup`.

## Increment 2 — Vite dev proxy (dashboard, live HMR)

Dashboard-only; boundary-disjoint from Go. Separate implementer.

- `dashboard/vite.config.ts`: add
  ```ts
  server: {
    proxy: {
      "/api": { target: "http://127.0.0.1:8081", changeOrigin: true },
    },
  },
  ```
  `changeOrigin: true` rewrites the proxied request's Host to `127.0.0.1:8081`,
  which is exactly what the control plane's network gate allowlists — so
  `/api/control/v1/*` (and `/api/control/v1/auth/*`) calls from the Vite dev
  server (`npm run dev`, :5173) reach a locally-running `venom` and pass the gate.
  Session cookie + `XSRF-TOKEN` flow through the proxy for the dev loop.
- No unit test (dev-only server config; `vite build` ignores `server`). The
  production `go:embed` snapshot is unaffected. Add a one-line note to the
  dashboard README/dev docs: "`npm run dev` proxies `/api` → 127.0.0.1:8081; run
  `venom` (or `venom serve`) alongside for a live dashboard."

## Out of scope / unchanged
Menu order otherwise unchanged; `Quit` still = graceful stop + exit; the
bounded-exit machine, CGO isolation, and append-only log are untouched.

## Acceptance
- `task gate` green (both OSes via CI); the base tray behavior unchanged.
- New Controller Stop/Start/round-trip tests pass; Windows autostart round-trip
  test passes with no residue.
- `task bundle` rebuilt so the owner's `dist/venom.exe` gains Start/Stop + the
  "Start with Windows" toggle; owner re-launches to confirm visually.
- Vite proxy: manual — `npm run dev` + running `venom` → dashboard edits hot-
  reload while the API works.
