# Unify the dev and production environment (one database)

Date: 2026-08-02
Status: approved by owner

## Problem

The tray's Development section spawns an **isolated** dev backend
(`go run ./cmd/venom serve -bind 127.0.0.1:8082`) with
`VENOM_DATA_DIR=<dataDir>/dev`, giving it its own database
(`<dataDir>/dev/venom.db`), keyring, and single-instance lock. The dev
frontend (vite, `127.0.0.1:8088`) proxies `/api` to that dev backend via
`VENOM_DEV_API_TARGET=http://127.0.0.1:8082`.

Consequence: the owner account created through the production dashboard
(`venom.db`) does not exist in the dev database, so the owner cannot log into
the dev dashboard — login is rejected because the dev DB has no such account,
not because the password is wrong. The two-database split is confusing and
unwanted at this stage of the project.

## Decision

Collapse development onto the production backend and database. There is **one**
database. The Development section runs **only** the vite frontend; it proxies
`/api` to the production backend on `127.0.0.1:8081`. No dev backend, no port
8082, no `<dataDir>/dev` data dir, no second database.

Owner chose the "frontend-only" scope: live reload is for the dashboard.
Backend (Go) changes still require rebuilding/restarting production, which was
always true.

## Target shape

| Port | What | Notes |
| ---- | ---- | ----- |
| 8081 | Production backend + embedded dashboard + the single database | fixed; serves both prod and dev API |
| 8088 | Dev frontend (vite, hot reload); proxies `/api` -> 8081 | fixed (`--strictPort`) |

Removed: dev backend, port 8082, `<dataDir>/dev`, `dev/venom.db`.

## Changes

1. `internal/tray/devsupervisor.go`
   - Remove the backend component entirely: `backendSpec()`, the `backend`
     `devComponent`, the `DataDir` option, and all backend start/stop/watch/
     refresh wiring.
   - `DevSupervisor` manages a single component (the vite frontend).
   - Frontend spec keeps `cmd /c npm run dev -- --port 8088 --strictPort
     --host 127.0.0.1`, with `ExtraEnv:
     VENOM_DEV_API_TARGET=http://127.0.0.1:8081` (point at production).
   - `DevStatusView` drops the `Backend` field. Status line, single component:
     - available: `Dev Status: Stopped|Starting|Running|Error`
     - unavailable: `Dev Status: unavailable`
   - Drop now-unused helpers/constants (`devBackendBind`, `devBackendHealth`,
     `overallDevState`, and the old two-component status rendering).

2. `internal/cli/cli.go`
   - Drop `DataDir` from `NewDevSupervisor` options. Remove the now-unused
     `dataDir` computation in the tray-run path if the compiler flags it.

3. Tests (`internal/tray/*_test.go`, `internal/cli/*_test.go`)
   - Update every assertion that referenced the dev backend, the two-component
     status line, or the `DataDir`/`dev` data dir. The P6 gate test
     (`internal/httpapi/p6gate_realoperate_test.go`) does NOT depend on any of
     this and must remain untouched and green.

4. `docs/tray-dev-environment.md`
   - Port map: remove the 8082 row; state 8088 proxies `/api` -> 8081.
   - Replace the "isolation / dev state never touches production" section with a
     short note that dev shares the production data dir and database; there is
     no separate dev database.
   - Update the vite `/api` proxy section: the target is always 8081.

## Out of scope (owner action, not a repo change)

Deleting the existing on-disk `%LOCALAPPDATA%\venom-router\dev` folder (the
stale dev database) is done by the owner on their machine.

## Verification

- `go build ./...` clean.
- `go test ./internal/tray/... ./internal/cli/... ./internal/httpapi/...`
  green.
- `gofmt` clean on touched files.
