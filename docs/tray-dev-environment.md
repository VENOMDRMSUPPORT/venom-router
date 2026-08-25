# Tray Development Environment

How the venom tray app splits Production from Development, which ports belong
to what, and how the dev child is contained. Accurate to the code as of
2026-08-07 (`internal/tray/devsupervisor.go` and friends).

## Two tray sections

The tray now opens a Manager control center rather than a compact three-card
window. The Manager has separate cards for Router Production, Router
Development, Catalog Production, and Catalog Development, plus system overview,
activity, and diagnostics-oriented status details.

- **Router Production** — the in-process server serving the **embedded** dashboard at
  `http://127.0.0.1:8081`. Menu items: Open Production Dashboard,
  Start/Stop/Restart Production.
- **Router Development** — live-reload development. The Manager stops the
  embedded production server before starting the Vite frontend and watched source
  backend. The backend remains on 8081 and keeps using the single canonical database.
- **Catalog Production / Development** — independent Catalog service groups. The
  Manager controls them through process lifecycle and Catalog HTTP health only;
  it never opens the Catalog database. Production uses API 8791/UI 5173, while
  Development uses API 8792/UI 5174 and a separate data root.

The legacy manual Development path uses a dependency-free bootstrap that
validates the dashboard lockfile and local Vite executable. The Manager's normal
path now invokes the locally installed Vite entrypoint directly after a valid
local installation is present; dependency repair is not hidden inside Start.
A missing or stale installation must be repaired explicitly before retrying.

## The key fact: edits under `dashboard/` do NOT appear on 8081

The dashboard served on `http://127.0.0.1:8081` is embedded into the binary at
**build time** (`go:embed` of `internal/httpui/dist/`, populated by
`task bundle` / `task dashboard:build-embed`). Editing files under
`dashboard/` changes nothing there until you rebuild and rerun the bundle.

Live development happens at `http://127.0.0.1:8088`: click **Start
Development** in the tray, wait for `Dev Status: Running`, then open
`http://127.0.0.1:8088` (or click Open Development Dashboard). Vite hot-reloads
edits under `dashboard/` immediately.

## Port map

| Port | What | Notes |
| ---- | ---- | ----- |
| 8081 | Production server + embedded dashboard + the single database | default `VENOM_BIND` (`internal/config`); serves both prod and dev API |
| 8088 | Router Development frontend (vite, hot reload); proxies `/api` -> 8081 | `--strictPort`: vite fails loudly if 8088 is taken — it never silently hops to another port. Bound to `--host 127.0.0.1` |
| 8791 | Catalog Production API | Standalone Catalog service, loopback only |
| 5173 | Catalog Production UI | Standalone Catalog UI |
| 8792 | Catalog Development API | Independent Catalog development service, loopback only |
| 5174 | Catalog Development UI | Independent Catalog development UI |

## Dev repo root resolution

The Development section needs the repo root to run `npm run dev` (in
`<root>/dashboard`). Resolution order
(`ResolveDevRoot` in `internal/tray/devsupervisor.go`):

1. The `VENOM_DEV_ROOT` environment variable, when set and non-empty
   (an explicit override — taken as-is, no marker check).
2. Otherwise the first of the following directories containing both `go.mod`
   and `dashboard/package.json`:
   1. the tray process's current working directory;
   2. the directory holding `venom.exe` itself;
   3. that directory's parent — this covers the shipped layout
      `<repo>\dist\venom.exe`, so a double-clicked bundle finds its repo
      automatically.
3. Otherwise the section is disabled and the menu shows
   `Dev Status: unavailable`.

Because of step 2, `VENOM_DEV_ROOT` is now **optional**: it is only needed
when the exe lives outside the repo (and its parent). To set it once (new
processes pick it up after a re-login or new shell):

```
setx VENOM_DEV_ROOT "C:\Users\hamee\Desktop\venom-router"
```

The tray logs the resolved value at boot as `tray: dev root` in
`%LOCALAPPDATA%\venom-router\logs\venom.log`.

## One shared database: dev uses production state

Development replaces the embedded production process with a watched source
backend on the same 8081 bind. The Vite frontend proxies `/api` to that backend,
so development reads and writes the **single** production database, keyring,
and single-instance lock. There is no separate dev database, keyring, or lock,
and no `<dataDir>\dev` data dir. The owner account created through the production
dashboard is therefore the same account the dev dashboard logs into.

- Production (and now dev) data dir on Windows: `%LOCALAPPDATA%\venom-router`.
- `%LOCALAPPDATA%\VenomRouter` (no hyphen) belongs to a **separate legacy
  install** (`G:\Venom-Router`) and is never read or written by this project.
  For the same reason the autostart Run-key value this app manages is named
  `venom-router` — a legacy `VenomRouter` Run-key entry is deliberately left
  alone.

## Containment: no orphaned dev processes

On Windows each dev child is spawned inside its own Job Object configured
`JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`
(`internal/tray/devprocess_windows.go`). Stop Development — or the tray
process exiting or dying, which closes the job handle — terminates the
**entire** process tree, including npm/vite's node children. The child is also
spawned with `CREATE_NO_WINDOW`, so no console pops.

## Dependency repair and frontend diagnostics

The tray starts `dashboard/scripts/dev-bootstrap.mjs`, not Vite directly. A
valid install requires both `node_modules/.bin/vite.cmd` and
`node_modules/.venom-dev-install.sha256` matching the SHA-256 of
`dashboard/package-lock.json`. When either is missing or stale, the bootstrap
runs `npm ci --prefer-offline --no-audit --no-fund` and writes the stamp only
after Vite is present.

`@venom/design-system` is a Windows junction from dashboard `node_modules` to
the protected source directory. Immediately before a repair the bootstrap
unlinks that junction entry without following it, then verifies the source
`Design_System/package.json` still exists. This keeps npm's destructive
`node_modules` cleanup outside the source tree.

Frontend and watched-backend stdout/stderr are appended to
`%LOCALAPPDATA%\venom-router\logs\development.log`. Unexpected child exits
retain a short actionable detail in the Development card, whose **View
Development Log** button opens the full child log. The main structured Venom
log remains separate.

## The vite `/api` proxy

`dashboard/vite.config.ts` proxies `/api` to the control plane. The target is
`VENOM_DEV_API_TARGET` when set; the tray's Development section sets it to
`http://127.0.0.1:8081`, so dev traffic hits the watched source backend and its
single database. A manual `npm run dev` (without the tray) also defaults to
`http://127.0.0.1:8081` — the same production/standalone bind. When Vite is
started manually instead of through the tray, a backend must already be
listening there for the frontend's `/api` calls to succeed.

## Build and icon

- `task bundle` builds the dashboard, copies it into `internal/httpui/dist/`
  for `go:embed`, and produces the silent (`-H windowsgui`) `dist\venom.exe`
  with the dashboard embedded. Plain `go build ./cmd/venom` stays
  console-subsystem for dev/CI.
- Icon regeneration (whenever the icon changes), from the repo root:

  ```
  go run ./tools/genicon
  go run github.com/akavel/rsrc@v0.10.2 -ico internal/tray/assets/venom.ico -o cmd/venom/rsrc_windows_amd64.syso
  ```

## Boot-failure UX

Bare tray mode has no console, so a failed boot (for example a corrupt
keyring) surfaces as an error **MessageBox** on Windows instead of the process
dying silently before the tray icon appears (`notifyStartupFailure` in
`internal/cli/cli.go`). `venom serve` intentionally skips this — it runs in a
terminal where the error is already visible; in the `windowsgui` bundle, CLI
modes still print to an existing terminal via the parent-console attach in
`cmd/venom/console_windows.go`.
