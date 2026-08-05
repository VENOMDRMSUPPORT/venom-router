# Fix Provider Account Toasts & Remove Inline Overlap Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove inline status notes that overflow over header elements in `AccountRow.tsx`, route all status notes to clean `toast` notifications, and add outcome toasts to every action button in account rows and provider toolbars.

**Architecture:** Update `AccountRow.tsx` to stop replacing `statusConfig.meta` span with long `actionNote` strings, triggering `toast.info()`, `toast.success()`, and `toast.danger()` on all action button completions. Update `ProviderRow.tsx` to emit toasts on toolbar actions.

**Tech Stack:** React 18, TypeScript, Vitest / Playwright, `@venom/design-system`.

## Global Constraints
- ALL code, comments, docstrings, and plan documents MUST strictly use ENGLISH ONLY.
- Ensure typecheck (`npx tsc --noEmit`) and dashboard unit tests pass cleanly after edits.

---

### Task 1: Update `AccountRow.tsx` to Route Status Notes to Toasts & Add Button Outcome Toasts

**Files:**
- Modify: `dashboard/src/fleet/AccountRow.tsx`

**Interfaces:**
- Consumes: `toast` from `@venom/design-system`
- Produces: Toast notifications for sync, discovery, pause/resume, disconnect, edit, and credential actions; clean metadata span without text overlap.

- [ ] **Step 1: Update `AccountRow.tsx` Metadata Span Rendering**

In `AccountRow.tsx` (around line 985):
Change:
```tsx
<span>{actionNote ? actionNote : (isClinePassOAuth ? statusConfig.meta : metaLabel)}</span>
```
To:
```tsx
<span>{isClinePassOAuth ? statusConfig.meta : metaLabel}</span>
```

- [ ] **Step 2: Add Outcome Toasts to `AccountRow.tsx` Handlers**

In `handleSync`:
```tsx
if (err instanceof AuthApiError && err.code === "quota_unsupported") {
  toast.info("Quota sync skipped — this provider has no quota capability. Health was refreshed.");
  return;
}
```
And on success:
```tsx
if (success) {
  toast.success("Health and quota refreshed", { detail: `Account: ${account.label || defaultName}` });
}
```

In `handleFetchModels`:
```tsx
if (success) {
  toast.success("Models discovered successfully", { detail: `Discovered models for ${account.label || defaultName}` });
}
```

In `handleStop` / `handleResume`:
```tsx
function handleStop() {
  void runLifecycleAction(() => stopAccount(account.id, csrfToken)).then((success) => {
    if (success) toast.success("Account paused", { detail: `Paused account "${account.label || defaultName}"` });
  });
}

function handleResume() {
  void runLifecycleAction(() => resumeAccount(account.id, csrfToken)).then((success) => {
    if (success) toast.success("Account resumed", { detail: `Resumed account "${account.label || defaultName}"` });
  });
}
```

In `runLifecycleAction`:
```tsx
catch (err) {
  if (isSessionExpired(err)) {
    onSessionExpired();
    return false;
  }
  const apiErr = toApiError(err);
  setActionError(apiErr);
  toast.danger("Account action failed", { detail: apiErr.message || (err instanceof Error ? err.message : String(err)) });
  return false;
}
```

- [ ] **Step 3: Commit Task 1 Changes**

```bash
git add dashboard/src/fleet/AccountRow.tsx
git commit -m "fix(fleet): route status notes to toasts and remove inline text overlap in AccountRow"
```

---

### Task 2: Add Outcome Toasts to `ProviderRow.tsx` Toolbar Actions

**Files:**
- Modify: `dashboard/src/fleet/ProviderRow.tsx`

**Interfaces:**
- Consumes: `toast` from `@venom/design-system`
- Produces: Toast notifications for Sync All and Refresh Models toolbar actions.

- [ ] **Step 1: Update `ProviderRow.tsx` Action Handlers**

In `handleSyncAll`:
```tsx
void runRowAction(async () => {
  try {
    await syncProvider(provider.id, csrfToken);
    toast.success("Synced all accounts", { detail: `Provider: ${provider.name || provider.id}` });
  } catch (err) {
    toast.danger("Sync all accounts failed", { detail: err instanceof Error ? err.message : String(err) });
    throw err;
  }
});
```

In `handleRefreshModels`:
```tsx
void runRowAction(async () => {
  try {
    for (const account of accounts) {
      const handle = await startDiscovery(account.id, csrfToken);
      const job = await pollJobToTerminal(handle.job_id);
      if (job.status !== "completed") {
        throw new AuthApiError(0, {
          code: job.error?.code ?? `job_${job.status}`,
          message: job.error?.message ?? `A discovery job is ${job.status}.`,
          request_id: "",
          retryable: true,
        });
      }
    }
    toast.success("Refreshed models for all accounts", { detail: `Provider: ${provider.name || provider.id}` });
  } catch (err) {
    toast.danger("Model refresh failed", { detail: err instanceof Error ? err.message : String(err) });
    throw err;
  }
});
```

- [ ] **Step 2: Commit Task 2 Changes**

```bash
git add dashboard/src/fleet/ProviderRow.tsx
git commit -m "feat(fleet): add outcome toasts to ProviderRow toolbar actions"
```

---

### Task 3: Run Verification Tests & Typechecks

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
git commit -m "test(dashboard): verify clean toast notifications and removal of inline text overlap"
```
