# 14 — Venom Manager Rebuild Report

## Executive result

The control layer of `venom.exe` was rebuilt into a unified local management
center, replacing the small tray window that relied on nested start-up logic.
The application now drives Router Production, Router Development, Catalog
Production, and Catalog Development from a single surface, while Catalog remains
a fully standalone service that keeps ownership of its database and of its
freshness, scores, and sync logic.

The final build is produced at `dist/venom.exe` on the Windows GUI subsystem,
and launching it was verified not to open a console window. No Catalog rule was
replaced and the Manager never opens `catalog.db`; the connection is made over
`/v1/health` only.

## What changed

| Area | Implementation |
|---|---|
| Lifecycle | Added `internal/manager` with a typed Coordinator engine that drives Start/Stop/Restart asynchronously through the states `preparing`, `starting`, `waiting_readiness`, `ready`, `degraded`, `stopping`, and `error`. |
| Startup | Development frontend and backend now start in parallel, and a normal Start no longer installs dependencies or downloads a runtime. The active path invokes the locally installed Vite directly. |
| Readiness | Added bounded retries and backoff to readiness checks so a process start-up race no longer fails the run. |
| Security | Preserved the loopback-only control server, the per-startup token, and same-origin protection. |
| Catalog | Added a typed `CatalogAdapter` that consumes the `/v1/health` contract, accepts HTTP 503 when the body is well-formed, and surfaces freshness as a stale state rather than hiding it. |
| Catalog lifecycle | Added a `CatalogSupervisor` on the same Coordinator, with Production API on 8791 and UI on 5173, Development API on 8792 and UI on 5174, and a separate Development data root. |
| Interface | Replaced the interface entirely with a Manager dashboard carrying overview metrics, four environment cards, health/freshness, live polling, an activity timeline, and independent start, stop, restart, and open-dashboard actions. |
| Documentation | Updated the Tray and Catalog documentation and added this architecture document, the startup baseline, and this implementation report. |

## Addressing the cause of the slowness

The previous path conflated starting an environment with preparation work that
could be slow, started the Development components sequentially, and left the
outcome dependent on the watcher and Vite completing before anything was shown
to the user. The new path separates **issuing the start command** from
**reaching readiness**: independent processes are created in parallel, the state
is displayed immediately as `starting` or `waiting_readiness`, and readiness
checks continue in the background within an explicit timeout.

A normal Start also no longer triggers dependency repair or a network install.
If the local Vite is absent, the component fails with a diagnosable message
instead of the application appearing frozen during an invisible installation.
That removes the unbounded delay, but it means dependency repair and
provisioning must become an explicit, separate operation in a later stage.

## Verification performed

| Check | Result |
|---|---|
| `go test ./...` on Windows | Passing for every Go package, including Manager, Tray, and CLI. |
| `go vet ./...` | Passing. |
| Manager tests | Passing, covering readiness failure, concurrent start, conflict prevention, stop containment, and the Catalog adapter. |
| Tray tests | Passing after updating process-ordering assumptions to match parallel start-up. |
| Catalog `npm run typecheck` | Passing, including the docs, CSS-module, and repository-output checks. |
| Catalog `npm run test:backend` | Passing: 666 tests, 0 failures. |
| Catalog `npm run test:spa` | Passing: 26 test files, 287 tests. |
| Dashboard build | Passing after updating the Design System file dependency. |
| Windows bundle verification | Passing: `dist/venom.exe` is PE GUI subsystem 2, and the temporary test file was removed. |

## Principal files

Added `internal/manager/contracts.go`, `coordinator.go`, `catalog.go`, and
`validation.go` together with their tests. Added
`internal/tray/catalogsupervisor.go` and its test, redesigned
`internal/tray/controlpage.html`, and wired it to the new Catalog routes in
`controlserver.go` and `traycontrols.go`.

Updated `internal/tray/devsupervisor.go` and `internal/cli/cli.go` to enable
parallel start-up and the direct Vite path, and updated
`internal/tray/appwindow.go` to use the `Venom Manager` title that matches the
new HTML title.

## Measurement note

The current baseline and the startup path are documented in
`docs/13-manager-startup-baseline.md`. Exact timing-improvement numbers are not
recorded as final figures in this report because cold start and warm start are
affected by the Windows machine, Windows Defender, the Node cache state, and the
database state. Manual acceptance should record two separate durations — from
pressing Start to `starting`, and from `starting` to `ready` — for both a cold
and a warm run, then compare them against the previous build.

## Suggested manual run

After closing the previous build, run `dist/venom.exe` from the repository. Open
the Manager from the tray, then confirm that Router Development moves from
`stopped` to `starting` immediately and that the Backend and Frontend cards
change independently. Start Catalog Production and Development one at a time and
confirm that each environment uses its own API, UI, and directory, and that the
Catalog state reports `current` or `stale` as decided by Catalog itself.
