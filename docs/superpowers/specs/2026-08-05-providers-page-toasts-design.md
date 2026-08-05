# Design Specification: Providers Page Toast Notifications

## Overview
This specification details the integration of outcome toast notifications (`success`, `danger`, `warning`, `info`) across all interactive action completion handlers on the Providers page (`dashboard/src/fleet/`). In accordance with user guidance, loading/in-progress toasts are omitted; toasts are shown strictly upon operation completion or failure.

## Target Components & Action Outcomes

### 1. Account Connection (`dashboard/src/fleet/ConnectDialog.tsx` & `useOAuthRelay.ts`)
- **API Key Connection**:
  - `success`: `toast.success("Account connected successfully", { detail: `Connected account "${label}" for ${providerId}` })`
  - `danger`: `toast.danger("Failed to connect account", { detail: error.message })`
- **OAuth Connection**:
  - `success`: `toast.success("OAuth account connected", { detail: `Successfully authenticated with ${providerId}` })`
  - `danger`: `toast.danger("OAuth connection failed", { detail: error.message })`
- **OAuth Re-authentication**:
  - `success`: `toast.success("Re-authentication successful", { detail: `Updated credentials for ${accountLabel}` })`
  - `danger`: `toast.danger("Re-authentication failed", { detail: error.message })`

### 2. Account Management (`dashboard/src/fleet/AccountRow.tsx`)
- **Update Account Label**:
  - `success`: `toast.success("Account label updated", { detail: `Renamed to "${newLabel}"` })`
  - `danger`: `toast.danger("Failed to update account label", { detail: error.message })`
- **Reveal & Copy Credential**:
  - `success`: `toast.success("Credential copied to clipboard")`
  - `danger`: `toast.danger("Failed to reveal credential", { detail: error.message })`
- **Update Funding Balance / Status**:
  - `success`: `toast.success("Funding details updated")`
  - `danger`: `toast.danger("Failed to update funding", { detail: error.message })`
- **Disconnect Account**:
  - `success`: `toast.success("Account disconnected", { detail: `Removed ${accountLabel}` })`
  - `danger`: `toast.danger("Failed to disconnect account", { detail: error.message })`

### 3. Provider Discovery & Sync (`dashboard/src/fleet/FleetOverview.tsx` & `ProviderRow.tsx`)
- **Start Model Discovery**:
  - `success`: `toast.success("Discovery completed", { detail: `Discovered models for ${providerId}` })`
  - `danger`: `toast.danger("Discovery failed", { detail: error.message })`
- **Refresh Quota**:
  - `success`: `toast.success("Quota limits refreshed")`
  - `danger`: `toast.danger("Failed to refresh quota", { detail: error.message })`
- **Sync Provider**:
  - `success`: `toast.success("Provider configuration synced")`
  - `danger`: `toast.danger("Provider sync failed", { detail: error.message })`
- **Fleet Burst Refresh**:
  - `info`: `toast.info("Fleet status refreshed")`

### 4. Probing & Benchmarking (`dashboard/src/fleet/ModelTestReport.tsx`)
- **Capability Probe**:
  - `success`: `toast.success("Probe completed successfully", { detail: `Model ${modelId} is operational` })`
  - `danger`: `toast.danger("Probe failed", { detail: error.message })`
- **Start Benchmark Job**:
  - `success`: `toast.success("Benchmark job started", { detail: `Job ID: ${jobId}` })`
  - `danger`: `toast.danger("Failed to start benchmark", { detail: error.message })`

## Implementation Guidelines
- Import `toast` from `@venom/design-system` or `@venom/design-system/primitives`.
- Trigger toasts inside `try / catch` or `async` handler completion blocks.
- Ensure all detail strings use safe fallback error messages (`err instanceof Error ? err.message : String(err)`).
