# Providers Page Toast Integration Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Integrate outcome toast notifications (`success`, `danger`, `info`) across all action completion handlers on the Providers page, omitting in-progress/loading toasts as requested.

**Architecture:** Update async event handlers in `ConnectDialog.tsx`, `useOAuthRelay.ts`, `AccountRow.tsx`, `FleetOverview.tsx`, `ProviderRow.tsx`, and `ModelTestReport.tsx` to invoke `toast.success()`, `toast.danger()`, and `toast.info()` upon completion.

**Tech Stack:** React 18, TypeScript, Vitest / Playwright, `@venom/design-system`.

## Global Constraints
- ALL code, comments, docstrings, and plan documents MUST strictly use ENGLISH ONLY.
- NO `toast.loading()` calls during in-progress execution; notify ONLY on success/failure outcomes.
- Ensure typecheck (`npx tsc --noEmit`) and dashboard unit tests pass cleanly after edits.

---

### Task 1: Integrate Toast Notifications in `ConnectDialog` & OAuth Relay

**Files:**
- Modify: `dashboard/src/fleet/ConnectDialog.tsx`
- Modify: `dashboard/src/fleet/useOAuthRelay.ts`

**Interfaces:**
- Consumes: `toast` from `@venom/design-system`
- Produces: Toast notifications for API Key connection, OAuth completion, and OAuth failure.

- [ ] **Step 1: Update `ConnectDialog.tsx` to Trigger Toasts on Connection Completion**

In `ConnectDialog.tsx`:
Import `toast` from `@venom/design-system`:
```tsx
import { toast } from "@venom/design-system";
```
In `handleConnectApiKey`:
- On success: `toast.success("Account connected successfully", { detail: `Connected account "${label || "default"}"` });`
- On error catch: `toast.danger("Failed to connect account", { detail: err instanceof Error ? err.message : String(err) });`

- [ ] **Step 2: Update `useOAuthRelay.ts` to Trigger Toasts on OAuth Completion/Error**

In `useOAuthRelay.ts`:
Import `toast` from `@venom/design-system`:
```tsx
import { toast } from "@venom/design-system";
```
- On success: `toast.success("OAuth account connected", { detail: `Authenticated with provider` });`
- On error: `toast.danger("OAuth connection failed", { detail: err instanceof Error ? err.message : String(err) });`

- [ ] **Step 3: Commit Task 1 Changes**

```bash
git add dashboard/src/fleet/ConnectDialog.tsx dashboard/src/fleet/useOAuthRelay.ts
git commit -m "feat(fleet): add toast notifications for API key and OAuth account connection"
```

---

### Task 2: Integrate Toast Notifications in `AccountRow`

**Files:**
- Modify: `dashboard/src/fleet/AccountRow.tsx`

**Interfaces:**
- Consumes: `toast` from `@venom/design-system`
- Produces: Toast notifications for label rename, credential copy, funding update, and disconnect account actions.

- [ ] **Step 1: Update `AccountRow.tsx` Handlers**

Import `toast` from `@venom/design-system`:
```tsx
import { toast } from "@venom/design-system";
```

Update action handlers:
1. `handleSaveLabel`:
   - Success: `toast.success("Account label updated", { detail: `Renamed to "${editingLabel}"` });`
   - Error: `toast.danger("Failed to update account label", { detail: err instanceof Error ? err.message : String(err) });`
2. `handleCopyCredential`:
   - Success: `toast.success("Credential copied to clipboard");`
   - Error: `toast.danger("Failed to reveal credential", { detail: err instanceof Error ? err.message : String(err) });`
3. `handleSaveFunding`:
   - Success: `toast.success("Funding details updated");`
   - Error: `toast.danger("Failed to update funding details", { detail: err instanceof Error ? err.message : String(err) });`
4. `handleDisconnect`:
   - Success: `toast.success("Account disconnected", { detail: `Removed account "${account.label || account.id}"` });`
   - Error: `toast.danger("Failed to disconnect account", { detail: err instanceof Error ? err.message : String(err) });`

- [ ] **Step 2: Commit Task 2 Changes**

```bash
git add dashboard/src/fleet/AccountRow.tsx
git commit -m "feat(fleet): add toast notifications for account label, credential, funding, and disconnect actions"
```

---

### Task 3: Integrate Toast Notifications in `FleetOverview`, `ProviderRow` & `ModelTestReport`

**Files:**
- Modify: `dashboard/src/fleet/FleetOverview.tsx`
- Modify: `dashboard/src/fleet/ProviderRow.tsx`
- Modify: `dashboard/src/fleet/ModelTestReport.tsx`

**Interfaces:**
- Consumes: `toast` from `@venom/design-system`
- Produces: Toast notifications for discovery, quota refresh, provider sync, probe execution, and benchmark jobs.

- [ ] **Step 1: Update `FleetOverview.tsx` & `ProviderRow.tsx` Handlers**

Import `toast` from `@venom/design-system`:
```tsx
import { toast } from "@venom/design-system";
```

Update handlers:
- `handleStartDiscovery`:
  - Success: `toast.success("Discovery completed", { detail: `Discovered models for ${providerId}` });`
  - Error: `toast.danger("Discovery failed", { detail: err instanceof Error ? err.message : String(err) });`
- `handleRefreshQuota`:
  - Success: `toast.success("Quota limits refreshed");`
  - Error: `toast.danger("Failed to refresh quota", { detail: err instanceof Error ? err.message : String(err) });`
- `handleSyncProvider`:
  - Success: `toast.success("Provider configuration synced");`
  - Error: `toast.danger("Provider sync failed", { detail: err instanceof Error ? err.message : String(err) });`
- `handleBurstRefresh`:
  - Info: `toast.info("Fleet status refreshed");`

- [ ] **Step 2: Update `ModelTestReport.tsx` Handlers**

Import `toast` from `@venom/design-system`:
```tsx
import { toast } from "@venom/design-system";
```

Update handlers:
- `handleRunProbe`:
  - Success: `toast.success("Model probe completed", { detail: `Probe for ${modelId} passed` });`
  - Error: `toast.danger("Model probe failed", { detail: err instanceof Error ? err.message : String(err) });`
- `handleStartBenchmark`:
  - Success: `toast.success("Benchmark job started", { detail: `Job handle created` });`
  - Error: `toast.danger("Failed to start benchmark", { detail: err instanceof Error ? err.message : String(err) });`

- [ ] **Step 3: Commit Task 3 Changes**

```bash
git add dashboard/src/fleet/FleetOverview.tsx dashboard/src/fleet/ProviderRow.tsx dashboard/src/fleet/ModelTestReport.tsx
git commit -m "feat(fleet): add toast notifications for discovery, quota, sync, probe, and benchmark actions"
```

---

### Task 4: Run Verification Tests & Typechecks

**Files:**
- Test: `dashboard/tests/` and `dashboard/src/fleet/*.test.tsx`

- [ ] **Step 1: Run Dashboard Typecheck**

Run: `npm run typecheck` inside `dashboard/`
Expected: 0 TypeScript errors.

- [ ] **Step 2: Run Dashboard Unit Test Suite**

Run: `npm test` inside `dashboard/`
Expected: All unit test suites pass cleanly.

- [ ] **Step 3: Commit Verification**

```bash
git add dashboard/
git commit -m "test(dashboard): verify toast integration across fleet provider actions"
```
