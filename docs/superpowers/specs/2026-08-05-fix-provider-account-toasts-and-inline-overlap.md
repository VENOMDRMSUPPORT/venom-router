# Design Specification: Provider & Account Action Toasts and Inline Overlap Removal

## Overview
This specification addresses user feedback regarding the Providers page:
1. **Remove Ugly Inline Text Overlap**: Remove inline status note text rendering inside `<div className="vnd-account-meta">` (e.g. `"Quota sync skipped — this provider has no quota capability. Health was refreshed."`) which currently overflows over header elements ("Live updates active · Usage available"). Route all such status notes directly into clean `toast.info()` / `toast.success()` notifications instead.
2. **Add Complete Toast Coverage for All Account & Provider Action Buttons**: Ensure every button in `AccountRow.tsx` (sync, discovery download, pause/resume, delete, edit, reveal/copy credential) and `ProviderRow.tsx` (sync all, refresh models) triggers outcome toasts (`success`, `danger`, `info`) upon operation completion.

## Detailed Changes

### 1. `dashboard/src/fleet/AccountRow.tsx`
- **Eliminate Inline Metadata Overlap**:
  - Update `vnd-account-meta` span to render `statusConfig.meta` or `metaLabel` cleanly without being overridden by long `actionNote` strings.
  - When `actionNote` is set (e.g. `"Quota sync skipped — this provider has no quota capability. Health was refreshed."`, `"Account reauthenticated. Refreshing live status…"`), trigger `toast.info(note)` or `toast.success(note)` so the notification is delivered via the Toast system.
- **Button Outcome Toast Handlers**:
  - `handleSync`:
    - Quota unsupported: `toast.info("Quota sync skipped — this provider has no quota capability. Health was refreshed.")`
    - Success: `toast.success("Health and quota refreshed", { detail: `Account: ${account.label || defaultName}` })`
    - Error: `toast.danger("Health sync failed", { detail: err.message })`
  - `handleFetchModels`:
    - Success: `toast.success("Models discovered successfully", { detail: `Discovered models for ${account.label || defaultName}` })`
    - Error: `toast.danger("Model discovery failed", { detail: err.message })`
  - `handleStop` / `handleResume`:
    - Stop Success: `toast.success("Account paused", { detail: `Paused account "${account.label || defaultName}"` })`
    - Resume Success: `toast.success("Account resumed", { detail: `Resumed account "${account.label || defaultName}"` })`
    - Error: `toast.danger("Action failed", { detail: err.message })`
  - `handleDisconnectConfirmed`:
    - Success: `toast.success("Account disconnected", { detail: `Removed account "${account.label || defaultName}"` })`
    - Error: `toast.danger("Failed to disconnect account", { detail: err.message })`
  - `handleEditSave`:
    - Label Success: `toast.success("Account label updated", { detail: `Renamed to "${nextLabel}"` })`
    - Funding Success: `toast.success("Funding details updated")`
    - Error: `toast.danger("Failed to update account", { detail: err.message })`
  - `runLifecycleAction`:
    - On catch error: `toast.danger("Account action failed", { detail: apiErr.message })`

### 2. `dashboard/src/fleet/ProviderRow.tsx`
- `handleSyncAll`:
  - Success: `toast.success("Synced all accounts", { detail: `Provider: ${provider.name || provider.id}` })`
  - Error: `toast.danger("Sync all accounts failed", { detail: err.message })`
- `handleRefreshModels`:
  - Success: `toast.success("Refreshed models for all accounts", { detail: `Provider: ${provider.name || provider.id}` })`
  - Error: `toast.danger("Model refresh failed", { detail: err.message })`

## Verification Strategy
- Run `npm run typecheck` in `dashboard/`.
- Run `npm test` in `dashboard/` (including `FleetOverview.test.tsx`).
