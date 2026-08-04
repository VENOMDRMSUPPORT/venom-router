# Live Model Lifecycle Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `Live Models` an operationally truthful surface that contains only models currently backed by healthy connected accounts, while automatically purging dead catalog data and repopulating it when an account recovers.

**Architecture:** Keep discovery as the only writer that creates model catalog rows. Add a dedicated storage lifecycle repository that deletes unavailable or unhealthy-account offerings, unused aliases, and orphan canonical models in one transaction. Run that cleanup after every account-maintenance sweep, guard the control-plane model read with a live-only query, and trigger discovery when health transitions back to healthy.

**Tech Stack:** Go 1.26, SQLite, React 18, TypeScript, Vitest, Testing Library, Vite.

## Global Constraints

- The sidebar and page title are `Live Models`; the URL remains `/models` for compatibility.
- A live offering requires `availability = available`, `connection_state = connected`, `health_state = healthy`, and `reauth_in_progress = 0`.
- Inactive offerings, unused provider aliases, and orphan canonical models are deleted, not displayed as historical inventory.
- A provider with no healthy account therefore retains no model catalog rows.
- When an account changes from non-healthy to healthy, discovery runs automatically so its catalog is rebuilt.
- Existing unrelated work is not protected: remove debug instrumentation and unrelated formatting churn; retain only behavior that is justified and verified.
- Do not create a branch, commit, or push without a separate explicit instruction.
- Verify the actual development surface at `http://127.0.0.1:8088/` against the shared backend/database.

---

### Task 1: Catalog lifecycle cleanup

**Files:**
- Create: `internal/storage/model_lifecycle.go`
- Create: `internal/storage/model_lifecycle_test.go`

**Interfaces:**
- Produces: `NewModelLifecycleRepo(db *DB) *ModelLifecycleRepo`
- Produces: `PurgeInactive(ctx context.Context) (ModelLifecyclePurgeResult, error)`

- [ ] **Step 1: Write a failing storage test**

Seed a healthy account, an expired account, an available offering for each, a withdrawn offering, shared and orphan canonical models, aliases, operations, and certifications. Assert that cleanup keeps only the healthy available offering and its dependency graph.

- [ ] **Step 2: Run the focused test and verify RED**

Run: `go test ./internal/storage -run TestModelLifecycleRepo_PurgeInactive -count=1`

Expected: compilation failure because `ModelLifecycleRepo` does not exist.

- [ ] **Step 3: Implement the transactional purge**

In one transaction:

```sql
DELETE FROM account_model_offerings
WHERE availability <> 'available'
   OR NOT EXISTS (
       SELECT 1 FROM accounts a
       WHERE a.id = account_model_offerings.account_id
         AND a.connection_state = 'connected'
         AND a.health_state = 'healthy'
         AND a.reauth_in_progress = 0
   );

DELETE FROM provider_model_aliases
WHERE NOT EXISTS (
    SELECT 1 FROM account_model_offerings amo
    WHERE amo.provider_id = provider_model_aliases.provider_id
      AND amo.provider_model_id = provider_model_aliases.provider_model_id
);

DELETE FROM models
WHERE NOT EXISTS (SELECT 1 FROM account_model_offerings amo WHERE amo.model_id = models.id)
  AND NOT EXISTS (SELECT 1 FROM provider_model_aliases pma WHERE pma.model_id = models.id);
```

Return the affected counts for observability and testing.

- [ ] **Step 4: Run the focused storage tests and verify GREEN**

Run: `go test ./internal/storage -run 'TestModelLifecycleRepo_' -count=1`

---

### Task 2: Live-only read contract

**Files:**
- Modify: `internal/storage/catalog.go`
- Modify: `internal/storage/catalog_test.go`
- Modify: `internal/httpapi/models.go`
- Modify: `internal/httpapi/models_test.go`

**Interfaces:**
- Extend: `CatalogListParams` with `LiveOnly bool`.
- `ModelsHandler` passes `LiveOnly: true` for `/models` and `/offerings`.

- [ ] **Step 1: Write failing repository and HTTP tests**

Prove that a live-only list excludes withdrawn offerings and offerings attached to degraded, expired, unavailable, unknown, stopped, or reauthenticating accounts while retaining healthy connected offerings.

- [ ] **Step 2: Run the focused tests and verify RED**

Run: `go test ./internal/storage ./internal/httpapi -run 'LiveOnly|ServeModels.*Healthy' -count=1`

- [ ] **Step 3: Add the live SQL predicate and wire the handler**

Join `accounts` only for `LiveOnly` reads and apply the exact live invariant from Global Constraints. Keep the default repository behavior available to internal lifecycle/routing code that explicitly needs raw rows.

- [ ] **Step 4: Run the focused tests and verify GREEN**

Run: `go test ./internal/storage ./internal/httpapi -run 'CatalogRepo|ModelsHandler|ServeModels|ServeOfferings' -count=1`

---

### Task 3: Automatic purge and recovery discovery

**Files:**
- Modify: `internal/httpapi/account_maintenance_tick.go`
- Modify: `internal/httpapi/account_maintenance_tick_test.go`

**Interfaces:**
- `probeHealth` reports the newly persisted `domain.HealthState`.
- Add `purgeInactiveModels func(context.Context) error`.
- Add `discoverModels func(context.Context, accountMaintenanceTarget) error`.

- [ ] **Step 1: Write failing scheduler tests**

Prove cleanup runs once after every sweep, including an empty/expired-only sweep, and discovery runs once when a non-healthy account becomes healthy but not on an already-healthy no-op.

- [ ] **Step 2: Run the scheduler tests and verify RED**

Run: `go test ./internal/httpapi -run 'TestAccountMaintenanceTick_.*(Purge|Recovery)' -count=1`

- [ ] **Step 3: Implement recovery and cleanup wiring**

Use `intelligence.DiscoveryService` with the existing registry, credential lease, and `DiscoveryRepo` when health recovers. Run `ModelLifecycleRepo.PurgeInactive` after all probes so a newly healthy account is not purged.

- [ ] **Step 4: Run scheduler and discovery tests and verify GREEN**

Run: `go test ./internal/httpapi ./internal/intelligence ./internal/storage -run 'Maintenance|Discovery|ModelLifecycle' -count=1`

---

### Task 4: Rename and truthful empty state

**Files:**
- Modify: `dashboard/src/shell/nav.ts`
- Modify: `dashboard/src/shell/AppShell.test.tsx`
- Modify: `dashboard/src/models/ModelsSurface.tsx`
- Modify: `dashboard/src/models/ModelsSurface.test.tsx`

**Interfaces:**
- Navigation label, page title, and breadcrumb: `Live Models`.
- Empty state: `No live models` with guidance that models appear automatically when a healthy account is available.

- [ ] **Step 1: Write failing navigation and empty-state tests**

Assert the sidebar link and breadcrumb use `Live Models`, `/models` remains the route, and an empty response renders `No live models` without historical/discovered wording.

- [ ] **Step 2: Run the Vitest files and verify RED**

Run: `npm.cmd test -- src/shell/AppShell.test.tsx src/models/ModelsSurface.test.tsx`

- [ ] **Step 3: Implement the copy changes**

Update the single navigation source of truth and the Models surface loading/error/empty copy to describe the live operational contract.

- [ ] **Step 4: Run the Vitest files and verify GREEN**

Run: `npm.cmd test -- src/shell/AppShell.test.tsx src/models/ModelsSurface.test.tsx`

---

### Task 5: Remove session debris and verify the live system

**Files:**
- Modify: `internal/httpapi/oauth.go`
- Delete if present: `debug-db56d2.log`
- Review all currently modified/untracked ClinePass, Providers, maintenance, and Models files.

**Interfaces:**
- No absolute-path debug writes, token/code prefixes, agent-log regions, or temporary diagnostics remain.

- [ ] **Step 1: Remove unsafe debug instrumentation**

Delete every `#region agent log` block and the hard-coded `debug-db56d2.log` writer. Remove the generated log file if it exists.

- [ ] **Step 2: Audit prior-session changes**

Classify each relevant diff as required, defective, or unrelated churn. Fix required behavior, delete defective code, and restore unrelated formatting-only edits after verifying they carry no semantic change.

- [ ] **Step 3: Run fresh verification**

Run:

```text
go test ./internal/storage ./internal/httpapi ./internal/accounts/application ./internal/providers -count=1
npm.cmd test -- src/shell/AppShell.test.tsx src/models/ModelsSurface.test.tsx src/fleet/FleetOverview.test.tsx
npm.cmd run typecheck
npm.cmd run lint
npm.cmd run build
go test ./...
```

- [ ] **Step 4: Verify the live development surface**

On `http://127.0.0.1:8088/models`, confirm the sidebar/title/breadcrumb say `Live Models`, the current zero-healthy ClinePass provider exposes zero model cards, and the API/database contain no inactive offerings, unused aliases, or orphan models.

- [ ] **Step 5: Report exact remaining issues**

List any failure or unverified external behavior explicitly, especially the full OAuth refresh cycle that still requires a fresh login to observe.
