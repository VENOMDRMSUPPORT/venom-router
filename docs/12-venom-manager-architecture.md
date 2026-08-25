# 12 — Venom Manager Architecture

## Status

This document defines the architecture for the rebuilt `venom.exe` desktop manager. It is an implementation contract for the manager runtime and its local control surface. It does not replace the Router architecture, Catalog architecture, or their respective data ownership rules.

## Product boundaries

Venom Manager is a local desktop orchestration product. It owns process lifecycle, environment profiles, readiness observation, diagnostics aggregation, and links to product dashboards. It does not own Router domain state and it does not own Catalog domain state.

| Product | Authoritative state | Manager access |
|---|---|---|
| Venom Router | Router control API and Router SQLite/keyring | HTTP health/control contracts only; never direct database access |
| Venom Catalog | Catalog API and Catalog-owned SQLite database | Catalog API only; never direct database access |
| Venom Manager | Local manager settings, operation state, and secret-free event journal | Owns its local state and process groups |

Catalog remains a standalone service. Its server remains the single database writer, and all Catalog facts, freshness, scores, lifecycle states, evidence, conflicts, and jobs remain server-owned. Manager may request a summary or an operation through a typed API, but it must not rederive or cache an alternative authoritative Catalog.

## Environment model

The manager exposes four explicit service groups:

| Group key | Product | Environment | Expected role |
|---|---|---|---|
| `router.production` | Router | Production | Embedded or packaged Router server and Router dashboard |
| `router.development` | Router | Development | Watched source backend and Vite dashboard |
| `catalog.production` | Catalog | Production | Catalog API and Catalog UI using the production profile |
| `catalog.development` | Catalog | Development | Catalog API and Catalog UI using a development profile or an explicit Catalog snapshot |

A group is a lifecycle boundary. A group may contain more than one process, but every process has a typed definition with its working directory, command, arguments, environment overlay, ports, health checks, readiness checks, log path, and shutdown policy.

Router production and Router development cannot run concurrently when both use the canonical Router data directory and control-plane port. The manager must represent this as an explicit mutual-exclusion rule, not as an implicit process failure. Catalog production and development must also have distinct data roots and ports when both are configured to run concurrently. A live Catalog database must never be copied or opened by the manager.

## Runtime components

The manager runtime is organized around one lifecycle engine:

- `ServiceDefinition` describes one managed process and its readiness contract.
- `ServiceGroup` describes a product/environment group and its dependency graph.
- `ProcessRunner` starts and supervises processes, captures output, and terminates complete process trees.
- `LifecycleCoordinator` serializes conflicting operations, makes Start/Stop/Restart idempotent, and returns an operation identifier without blocking the control UI.
- `ReadinessMonitor` observes ports, health endpoints, product APIs, and UI endpoints using bounded backoff and deadlines.
- `EventJournal` stores secret-free lifecycle events and startup timing markers.
- `EnvironmentStore` persists validated profiles and non-secret settings only.
- `RouterAdapter` and `CatalogAdapter` expose typed product-specific health and summary operations without duplicating product logic.

The same lifecycle engine must be used for Router and Catalog. Product-specific behavior is expressed by definitions and adapters, not by a second supervisor implementation or a switch on product names scattered through handlers.

## State contract

The public group state is one of the following values:

| State | Meaning |
|---|---|
| `stopped` | No process in the group is running |
| `preparing` | Local validation is running before process creation |
| `starting` | The process tree has been created but readiness is not complete |
| `waiting_readiness` | Processes exist and the manager is waiting for declared checks |
| `ready` | All required checks have passed |
| `degraded` | The group is partially available or an optional check has failed |
| `stopping` | A bounded shutdown is in progress |
| `error` | The group failed to start, exited unexpectedly, or exceeded a lifecycle deadline |

Every transition carries `group_id`, `operation_id`, `from`, `to`, `reason_code`, `occurred_at`, and a safe human detail. It must not carry credentials, authorization headers, raw provider responses, or arbitrary child output.

## Operation contract

Start, Stop, and Restart are asynchronous operations. The request returns an operation identifier immediately after validation and scheduling. The UI observes progress through a local event stream where available and falls back to bounded polling.

An operation is idempotent for the same target state. A second Start while a group is `starting`, `waiting_readiness`, or `ready` does not create a second process tree. A Stop while `stopped` is a no-op success. A conflicting operation is rejected with a typed error that names the conflicting group and current state.

The manager must apply group dependencies as follows:

1. Validate all definitions, paths, ports, data-root exclusivity, and required tools.
2. Stop conflicting groups before starting a mutually exclusive target.
3. Start independent service definitions concurrently.
4. Wait for process creation and readiness independently.
5. Mark the group `ready` only when all required checks pass.
6. On a required-process failure, stop the remaining processes in the same operation and retain the failure detail in the journal.

## Local security boundary

The manager control listener remains loopback-only and uses a per-startup random control token plus same-origin protection. The listener is only for the local manager UI and its own lifecycle operations. Router authentication and CSRF requirements remain enforced by Router; Manager cannot bypass them.

Manager settings are not a credential store. Provider credentials, OAuth values, Router API keys, Catalog secrets, and authorization headers must never be written to manager settings or lifecycle events. Sensitive values are passed only through the existing product-owned credential mechanisms.

## Catalog adapter contract

The manager must consume Catalog through an API adapter with typed responses for:

- service health and readiness;
- freshness and last successful sync;
- inventory counts and lifecycle partitions;
- open conflicts and review backlog;
- recent sync runs and durable jobs;
- supported maintenance operations that return a job identifier.

The adapter treats unknown or unavailable values as unknown. It must not turn a stale snapshot into a live result, lower unknown scores to zero, or recompute server-owned scores. If a required summary field is unavailable, the Catalog service must add a read-only endpoint and contract tests; the manager must not open `catalog.db`.

## Startup performance contract

The manager must separate local preparation from process readiness. Dependency installation, runtime downloads, and repair operations are not part of the normal Start path. They are explicit diagnostics or repair actions.

The normal Development Start path must:

- use a locally available, pinned watcher executable;
- validate the dependency fingerprint through a short-lived cached preflight;
- spawn independent frontend and backend processes concurrently;
- report `starting` immediately after process creation;
- use bounded readiness checks with a visible deadline;
- record timestamps for validation, spawn, first listener, API readiness, UI readiness, and final state.

A failed preflight must explain the missing tool or stale fingerprint and provide a separate Repair action. Start must not silently execute a network installation and continue to appear frozen.

## Windows process containment

Development and Catalog process trees must be created with no console window and placed in a kill-on-close Job Object on Windows. Stop waits for the process tree to exit up to a configured deadline, then reports a bounded timeout and continues safe cleanup. Closing the manager must trigger the same containment path before the manager exits.

## Compatibility and migration

The existing tray menu remains a behavioral reference during migration. Existing Router control-plane security and public API contracts are preserved unless a versioned migration is explicitly documented. The current `dist/venom.exe` is never treated as an architectural source of truth and is replaced only after the new manager passes packaging and manual acceptance checks.

## Acceptance evidence

The architecture is considered implemented only when the repository contains:

1. A service inventory and environment matrix with resolved ports and data-root ownership.
2. A lifecycle state-machine test suite covering idempotency, conflict prevention, deadlines, and unexpected exits.
3. A startup timing report containing measured cold and warm runs rather than estimates.
4. Catalog adapter contract tests proving that Manager does not access the Catalog database directly.
5. Windows process-containment evidence showing no orphaned child processes after Stop and manager exit.
6. A GUI-subsystem bundle produced only through `task bundle` and verified as PE Subsystem 2.

## References

- `docs/01-architecture.md`
- `docs/09-control-api.md`
- `docs/tray-dev-environment.md`
- `catalog/CLAUDE.md`
- `catalog/server/index.ts`
