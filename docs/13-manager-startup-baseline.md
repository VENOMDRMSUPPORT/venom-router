# 13 — Manager Startup Baseline

## Purpose

This document records the current startup path that the rebuilt manager must improve. It intentionally separates facts established by the repository from measurements that require a Windows execution run.

## Current service inventory

| Service group | Current implementation | Current endpoint or port | Current data ownership |
|---|---|---|---|
| Router Production | In-process Router server managed by the tray controller | Control plane default `127.0.0.1:8081`; embedded dashboard at `/` | Router data directory and keyring |
| Router Development backend | Go watcher started through `go run github.com/air-verse/air@latest` | `127.0.0.1:8081/health` | Same canonical Router data directory and lock as Production |
| Router Development frontend | Node bootstrap followed by Vite | `127.0.0.1:8088`; proxies API to `8081` | No database |
| Catalog Production | Standalone Catalog service | Catalog API default `127.0.0.1:8791/v1`; UI default `127.0.0.1:5173` | Catalog-owned database |
| Catalog Development | Standalone Catalog development profile to be defined by Manager | Dedicated ports and data root required | Catalog-owned development database or explicit snapshot |
| Manager control window | Tray-owned loopback server and browser app window | Ephemeral loopback port | Manager log and settings only |

## Current Development start sequence

The current tray Development action first stops Router Production and waits for the production lifecycle to report `stopped`. It then starts the Vite child and the Go backend watcher through the development supervisor. The supervisor later promotes each child from `starting` to `running` after an HTTP probe answers.

The frontend command runs `dashboard/scripts/dev-bootstrap.mjs`. That bootstrap validates the package manifest, lockfile, local Vite executable, and an installation fingerprint. If the installation is absent, incomplete, or stale, it executes `npm ci`. The backend command uses `go run ... air@latest`, which may resolve and build the watcher before Air can build and start the application.

These are separate concerns that must not remain hidden behind one `Starting` badge:

1. mutual exclusion and production shutdown;
2. local dependency validation;
3. process creation;
4. first listener availability;
5. Router API readiness;
6. Vite UI readiness;
7. final group readiness.

## Confirmed causes of perceived slowness

The repository confirms that Development startup can include frontend dependency repair, a first-use watcher resolution, a Go build/restart cycle, and two readiness probes. The existing control page polls every 1.5 seconds and exposes only a coarse combined status, so a legitimate build or repair looks like a stalled operation.

The current supervisor starts the frontend component and backend component from one synchronous Start call. Even where the child processes continue independently after creation, the user receives no phase timeline or progress event. The current frontend bootstrap also intentionally performs repair before Vite starts, which makes network and file-system work part of the visible Start path.

No absolute latency claim is made here until the baseline is measured on the target Windows machine. The following fields are mandatory in the next execution run:

| Run type | Preflight | Process spawn | First listener | API ready | UI ready | Final ready | Result |
|---|---:|---:|---:|---:|---:|---:|---|
| Cold Development start | pending | pending | pending | pending | pending | pending | pending |
| Warm Development start | pending | pending | pending | pending | pending | pending | pending |
| Cold Catalog start | pending | pending | pending | pending | pending | pending | pending |
| Warm Catalog start | pending | pending | pending | pending | pending | pending | pending |

Times must be recorded in milliseconds from a monotonic clock. Each run must state the commit or executable version, Node version, Go version, whether the dependency fingerprint was valid, whether the watcher was already available, and whether another group had to stop first.

## Target startup path

The rebuilt Manager uses the following normal path:

1. Validate the target group definition and mutual-exclusion rules.
2. Read a cached local preflight result for tools, paths, ports, and dependency fingerprints.
3. If the cache is invalid, stop and show a repair or configuration action; do not install dependencies implicitly.
4. Start independent child processes concurrently inside contained process groups.
5. Emit lifecycle events immediately after process creation.
6. Probe listener and product readiness with a bounded backoff schedule.
7. Mark the group `ready` only after all required checks pass.
8. Persist timing markers and the final safe result in the event journal.

## Measurement procedure

The performance harness must run from a fresh Manager launch and from a warm Manager launch. It must capture the manager event journal and the child log tail for every run. It must repeat each condition at least three times and report median and maximum values. A run is invalid if an implicit install or unrecorded repair occurs.

## Success criteria

The performance change is accepted when the normal warm path has no network installation or runtime download, independent services start concurrently, the UI receives an immediate operation event, and the measured final readiness is lower than the current baseline on the same machine. If cold startup remains expensive because of a first compilation, that cost must be visible as a named phase with a bounded deadline and a direct remediation path.
