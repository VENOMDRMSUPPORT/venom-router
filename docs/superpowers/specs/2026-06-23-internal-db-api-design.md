# Internal DB API — Design Spec

**Date:** 2026-06-23  
**Status:** Approved  
**Scope:** Phase 1 — Core layer + Providers domain (other domains follow same pattern)

---

## Problem

Backend logic is currently spread across three disconnected layers:

- `createServerFn` files — UI-coupled, require user session middleware
- Worker files (`health-check.server.ts`, `quota-snapshot.server.ts`) — call Supabase directly, ad-hoc
- HTTP API (`chat-completions.server.ts`) — calls Supabase via `supabaseAdmin`

Each layer duplicates Supabase queries independently. Adding a new operation means touching multiple files. Logic like account sync exists in different forms in `integrations.functions.ts` and `health-check.server.ts`.

---

## Solution

Introduce a **Core Internal API** at `src/lib/db/` — a collection of typed TypeScript functions that serve as the single source of truth for all backend data access.

All three existing layers become thin consumers of this core.

```
┌─────────────────────────────────────────┐
│         src/lib/db/  (Core layer)       │
│   typed functions + SupabaseClient      │
└──────────┬──────────┬───────────┬───────┘
           │          │           │
    createServerFn  workers   HTTP /api
    (UI layer)   (cron/bg)  (external)
```

---

## Design Principles

1. **First argument is always `supabase: SupabaseClient`** — works with user-scoped client or `supabaseAdmin`, caller decides
2. **Returns typed result directly** — never raw Supabase response objects
3. **Throws on DB error** — caller handles errors at their level
4. **No middleware, no session, no framework coupling** — pure TypeScript functions
5. **Files are `.server.ts`** — enforces server-only boundary, consistent with codebase convention

---

## File Structure

```
src/lib/db/
  providers.server.ts   ← accounts, quota, models, provider health
  venom.server.ts       ← venom models, routing rules
  usage.server.ts       ← usage records, metrics, traffic
  api-keys.server.ts    ← API key queries
```

---

## Domain: Providers (`providers.server.ts`)

### Types

```ts
type AccountStatus = "healthy" | "degraded" | "expired"

type AccountInfo = {
  id: string
  email: string | null
  label: string | null
  plan: string | null
  status: AccountStatus
  provider_slug: string
  provider_name: string
  auth_type: string
  last_synced_at: string | null
  last_health_check_at: string | null
}

type QuotaGroup = {
  label: string
  used: number
  total: number | null
  unit: string
  resets_at: string | null
}

type AccountQuota = {
  account_id: string
  used: number | null
  total: number | null
  unit: string | null
  groups: QuotaGroup[]
  resets_at: string | null
  confidence: "high" | "medium" | "low" | null
}

type AccountModel = {
  id: string
  external_id: string
  display_name: string
  capabilities: string[]
  enabled: boolean
  test_status: "working" | "failed" | "untested"
  latency_ms: number | null
  last_tested_at: string | null
  lifecycle: string
}

type ModelCheckResult = {
  external_id: string
  ok: boolean
  latency_ms: number
  error?: string
}

type ProviderHealth = {
  provider_slug: string
  provider_name: string
  accounts_total: number
  accounts_healthy: number
  accounts_degraded: number
  accounts_expired: number
  is_healthy: boolean  // true if at least one healthy account exists
}
```

### Functions

```ts
// Returns just the status string — lightweight check
getAccountStatus(supabase, accountId: string): Promise<AccountStatus>

// Full account info including provider details
getAccountInfo(supabase, accountId: string): Promise<AccountInfo>

// Quota from accounts table + quotas table groups
getAccountQuota(supabase, accountId: string): Promise<AccountQuota>

// Models for an account with optional filters
getAccountModels(supabase, accountId: string, opts?: {
  enabledOnly?: boolean
  lifecycle?: string
}): Promise<AccountModel[]>

// Test models via provider adapter — runs actual API calls
// If externalIds omitted: tests all enabled models for the account
checkAccountModels(supabase, accountId: string, externalIds?: string[]): Promise<ModelCheckResult[]>

// Provider health aggregated across all accounts per provider
// If providerSlug omitted: returns all providers
getProviderHealth(supabase, opts?: {
  providerSlug?: string
}): Promise<ProviderHealth[]>

// List accounts with optional status/provider filter
listAccounts(supabase, opts?: {
  status?: AccountStatus | AccountStatus[]
  providerSlug?: string
}): Promise<AccountInfo[]>
```

---

## Domain: Venom Models (`venom.server.ts`)

```ts
type VenomModel = {
  id: string
  slug: "lite" | "pro" | "max"
  weight_cost: number
  weight_speed: number
  weight_quality: number
  max_fallback_attempts: number
  timeout_ms: number
}

type RoutingRule = {
  id: string
  venom_slug: "lite" | "pro" | "max"
  account_model_id: string
  priority: number
  active: boolean
  model_external_id: string
  provider_slug: string
}

getVenomModel(supabase, slug: "lite" | "pro" | "max"): Promise<VenomModel>
listVenomModels(supabase): Promise<VenomModel[]>
listRoutingRules(supabase, opts?: {
  venomSlug?: "lite" | "pro" | "max"
  activeOnly?: boolean
}): Promise<RoutingRule[]>
```

---

## Domain: Usage (`usage.server.ts`)

```ts
type UsageRecord = {
  id: string
  venom_slug: string
  cost_usd: number | null
  input_tokens: number | null
  output_tokens: number | null
  success: boolean
  fallback_used: boolean
  created_at: string
}

type MetricsSummary = {
  total_requests: number
  total_tokens: number
  total_cost_usd: number
  success_rate: number
  fallback_rate: number
}

listUsageRecords(supabase, opts?: {
  since?: string        // ISO date string
  venomSlug?: string
  limit?: number
}): Promise<UsageRecord[]>

getMetricsSummary(supabase, opts?: {
  since?: string
}): Promise<MetricsSummary>

getTraffic7d(supabase): Promise<{ day: string; requests: number }[]>
```

---

## Domain: API Keys (`api-keys.server.ts`)

```ts
type ApiKey = {
  id: string
  name: string
  key_prefix: string
  allowed_models: string[]
  rpm_limit: number | null
  tpd_limit: number | null
  monthly_cap_usd: number | null
  revoked_at: string | null
  last_used_at: string | null
  created_at: string
}

listApiKeys(supabase, opts?: { activeOnly?: boolean }): Promise<ApiKey[]>
getApiKey(supabase, id: string): Promise<ApiKey>
```

---

## No Barrel Export

Each domain file is imported directly:

```ts
import { getAccountInfo } from "@/lib/db/providers.server"
import { listVenomModels } from "@/lib/db/venom.server"
import { getTraffic7d } from "@/lib/db/usage.server"
import { listApiKeys } from "@/lib/db/api-keys.server"
```

A barrel `index.ts` (without `.server` suffix) would bypass the Vite server-only boundary check — client code could accidentally import server functions without a build-time error. Direct imports keep the boundary explicit.

---

## Integration with Existing Layers

### createServerFn (UI layer)

```ts
// Before — inline query in integrations.functions.ts
const { data } = await supabase.from("accounts").select("...").eq("id", id)

// After — delegate to core
import { getAccountInfo } from "@/lib/db/providers.server"
const account = await getAccountInfo(context.supabase, id)
```

### Workers / Cron

```ts
// Before — ad-hoc query in health-check.server.ts
const { data: accounts } = await supabase.from("accounts").select("...").neq("status", "expired")

// After
import { listAccounts } from "@/lib/db/providers.server"
const accounts = await listAccounts(supabaseAdmin, { status: ["healthy", "degraded"] })
```

### New code (background jobs, cron, future workers)

Starts directly from the core — no justification for writing raw queries.

---

## Migration Strategy

1. **Build `src/lib/db/`** — all 4 files + index. No changes to existing code yet.
2. **Migrate workers first** — `health-check.server.ts` and `quota-snapshot.server.ts` are the most painful duplication points.
3. **Migrate `createServerFn` files incrementally** — when a file is touched for another reason, refactor its queries to use the core.
4. **New features always use the core** — no new raw Supabase queries outside `src/lib/db/`.

---

## Out of Scope (Phase 1)

- Mutation functions (insert/update/delete) — read queries only for now
- Caching layer — not needed yet
- HTTP wrapper endpoints — internal module only
- Auth/RLS logic — caller's responsibility
