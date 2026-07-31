# Tray Development Environment

How the venom tray app splits Production from Development, which ports belong
to what, and how the dev children are isolated and contained. Accurate to the
code as of 2026-08-01 (`internal/tray/devsupervisor.go` and friends).

## Two tray sections

- **Production** — the in-process server serving the **embedded** dashboard at
  `http://127.0.0.1:8081`. Menu items: Open Production Dashboard,
  Start/Stop/Restart Production.
- **Development** — live-reload development. Menu items: Open Development
  Dashboard, Start/Stop/Restart Development, plus a `Dev Status:` info line
  (Stopped / Starting / Running / Error per component).

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
| 8081 | Production server + embedded dashboard | default `VENOM_BIND` (`internal/config`) |
| 8088 | Dev frontend (vite, hot reload) | `--strictPort`: vite fails loudly if 8088 is taken — it never silently hops to another port. Bound to `--host 127.0.0.1` |
| 8082 | Dev backend | `go run ./cmd/venom serve -bind 127.0.0.1:8082` |

## Dev repo root resolution

The Development section needs the repo root to run `npm run dev` (in
`<root>/dashboard`) and `go run ./cmd/venom` (in `<root>`). Resolution order
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

## Isolation: dev state never touches production state

The dev backend is spawned with `VENOM_DATA_DIR=<dataDir>\dev`, giving it its
own single-instance lock, database, and keyring — fully separate from
production.

- Production data dir on Windows: `%LOCALAPPDATA%\venom-router`.
- Dev backend data dir: `%LOCALAPPDATA%\venom-router\dev`.
- `%LOCALAPPDATA%\VenomRouter` (no hyphen) belongs to a **separate legacy
  install** (`G:\Venom-Router`) and is never read or written by this project.
  For the same reason the autostart Run-key value this app manages is named
  `venom-router` — a legacy `VenomRouter` Run-key entry is deliberately left
  alone.

## Containment: no orphaned dev processes

On Windows both dev children are spawned inside their own Job Object
configured `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`
(`internal/tray/devprocess_windows.go`). Stop Development — or the tray
process exiting or dying, which closes the job handles — terminates the
**entire** process tree: npm/vite's node children and `go run`'s compiled
child. Children are also spawned with `CREATE_NO_WINDOW`, so no consoles pop.

## The vite `/api` proxy

`dashboard/vite.config.ts` proxies `/api` to the control plane. The target is
`VENOM_DEV_API_TARGET` when set; the tray's Development section sets it to
`http://127.0.0.1:8082`, so dev traffic hits the isolated dev backend. A
manual `npm run dev` (without the tray) defaults to `http://127.0.0.1:8081` —
the production/standalone bind.

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
