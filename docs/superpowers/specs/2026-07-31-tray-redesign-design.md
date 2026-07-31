# Tray Redesign: Production/Development Split — Design

**Date:** 2026-07-31
**Status:** Approved by owner
**Scope:** Standalone task. NOT part of the 179-task roadmap or the tracker.

## Goal

Make the Windows tray app match two owner-provided screenshots 1:1:

1. **Icon**: an upward-pointing green/teal triangle on a dark rounded square.
2. **Menu**: two sections — Production (the existing in-process server) and
   Development (a supervised `vite` frontend + a separate dev backend) — each
   with its own dashboard link, status line, and Start/Stop/Restart, followed
   by the existing View Logs / Start with Windows / Quit items.

## Target menu layout

```
Open Production Dashboard
Status: <Running|Stopped|Error>                      (disabled info line)
Start Production
Stop Production
Restart Production
──────────────
Open Development Dashboard
Dev Status: <overall> · Frontend <s> · Backend <s>   (disabled info line)
Start Development
Stop Development
Restart Development
──────────────
View Logs
Start with Windows                                   (checkbox, unchanged)
Quit
```

Dynamic enablement produces the greyed look in the screenshot:

- Production: `Start` disabled while Running; `Stop`/`Restart`/`Open …
  Dashboard` disabled while not Running.
- Development: same rule against the dev supervisor's overall state; ALL dev
  items disabled (and `Dev Status: unavailable`) when no dev repo is found.

## Architecture (approved approach)

The tray itself supervises the development environment as two external child
processes, each with a small state machine:

- **Frontend**: `npm run dev -- --port 5173 --strictPort` in `<devRoot>/dashboard`.
  Health = HTTP GET `http://127.0.0.1:5173/` returns any response.
- **Backend**: `go run ./cmd/venom serve -bind 127.0.0.1:8082` in `<devRoot>`,
  with env `VENOM_DATA_DIR=<prod data dir>\dev` so its single-instance lock,
  SQLite DB, logs, and keyring files are fully isolated from production.
  Health = HTTP GET `http://127.0.0.1:8082/health` (same probe the production
  lifecycle uses).

Per-component states: `Stopped → Starting → Running`, plus `Error` (process
exited unexpectedly, or failed to spawn). Overall dev state = Running if both
Running; Error if either Error; Starting if either Starting; else Stopped.

**Process containment (Windows):** children are placed in a Windows Job Object
created with `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`. Stop closes the job handle
(killing the full process tree, including vite's node children); if the tray
process dies for any reason, the OS kills the job too — no orphaned processes.

**Dev repo discovery:** `VENOM_DEV_ROOT` env var if set; otherwise the current
working directory if it contains both `go.mod` and `dashboard/package.json`.
No walking up the directory tree. If unresolved, the Development section is
disabled as described above. Resolution happens once at tray startup.

**Open Development Dashboard** opens `http://127.0.0.1:5173/`.

## Code changes

| Location | Change |
|---|---|
| `internal/tray/devsupervisor.go` (+ tests) | Platform-neutral supervisor core: state machines, health polling, start/stop/restart orchestration, status-line formatting. Depends on a `ProcessRunner` port — no syscalls, no `os/exec`. |
| `internal/tray/devprocess_windows.go` (+ tests where hermetic) | Windows `ProcessRunner`: spawn via `os/exec` (npm through `cmd /c npm.cmd`), Job Object creation/assignment, kill-on-close teardown. |
| `internal/tray/tray_other.go` | Headless fallback: dev supervisor APIs compile but the native menu does not exist (unchanged behavior). |
| `internal/tray/tray_windows.go` | New menu layout, wiring dev items to the supervisor, renamed production items, per-section enablement sync, status-line ticker updates. |
| `internal/tray/icon_windows.go` assets | Replace `assets/venom.ico` with the generated icon (16/20/24/32/48/64 PNG-compressed ICO): upward green/teal triangle on a dark rounded square, matching the screenshot. |
| `internal/platform` | `VENOM_DATA_DIR` env override checked before the OS-specific default in `DataDir()` (both OSes), documented as a dev/ops override. |
| `dashboard/vite.config.ts` | Proxy target from `process.env.VENOM_DEV_API_TARGET`, defaulting to the current `http://127.0.0.1:8081`. The tray sets it to `http://127.0.0.1:8082` when spawning vite. |

Out of scope: any change to `internal/app`, the roadmap tracker, CI, or the
production server lifecycle (`Controller` semantics stay exactly as they are).

## Error handling

- Spawn failure (npm/go missing, port taken) → component `Error`, detail kept
  for the status line; other component unaffected; Start re-enabled.
- Unexpected child exit → `Error` (not `Stopped`), so the owner can tell a
  crash from an intentional stop.
- `Stop Development` always tears down BOTH components (job close), then both
  report `Stopped`.
- Quit: the job's kill-on-close guarantees dev children die with the tray;
  the existing bounded-exit watchdog machinery is untouched.

## Testing

- Supervisor state machine fully covered with a fake `ProcessRunner` +
  injected health probe: start/stop/restart, spawn failure, crash-while-
  running, restart-after-error, both-components aggregation, status-line text.
- Enablement logic and status formatting as pure-function tests.
- `VENOM_DATA_DIR` override: `platform.DataDir()` honors it; a second lock in
  an overridden dir does not collide with the default dir's lock.
- Existing tray/controller/exit tests must remain green and untouched in
  semantics (menu-item renames may require test-string updates only).
- Icon: a test asserting the embedded ICO parses and contains the expected
  image count/sizes.

## Constraints verified against the codebase

- `venom.lock` lives in the data dir (`internal/app/lock.go:49`) — hence the
  `VENOM_DATA_DIR` isolation requirement for the dev backend.
- Production health probe is `GET /health` (`internal/tray/lifecycle.go:55`).
- Vite proxy currently hardcodes `127.0.0.1:8081` (`dashboard/vite.config.ts`).
- Entry point is `cmd/venom/main.go`; `go run ./cmd/venom serve -bind …` is a
  valid dev backend invocation.
