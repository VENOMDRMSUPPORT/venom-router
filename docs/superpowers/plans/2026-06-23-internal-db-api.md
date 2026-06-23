# Internal DB API Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `src/lib/db/` — four typed, framework-free TypeScript domain files that serve as the single source of truth for all Supabase queries in the project.

**Architecture:** Each domain file exports async functions that accept `SupabaseClient` as first argument and return typed results directly. No barrel export — callers import from the specific domain file. TDD throughout — tests mock the Supabase query builder.

**Tech Stack:** TypeScript, `@supabase/supabase-js` (SupabaseClient type), `bun:test` test runner.

## Global Constraints

- All files are `.server.ts` — never import from browser-only modules (React, React Query, Radix, component files)
- Do NOT import from `src/lib/providers/sync-cache.ts` — it imports React Query and component types (browser-only)
- First argument of every exported function must be `supabase: SupabaseClient`
- Return typed results directly — never return raw Supabase response objects (`{ data, error }`)
- Throw `Error` on DB errors with the format: `throw new Error(\`functionName: \${error.message}\`)`
- No barrel `index.ts` — callers import from the specific domain file e.g. `@/lib/db/providers.server`
- Test files use `bun:test`: `import { describe, it, expect } from "bun:test"`
- Run a single test file: `bun test src/lib/db/<filename>.test.ts`
- Run all DB tests: `bun test src/lib/db/`

---

## File Structure

```
src/lib/db/
  providers.server.ts        CREATE
  providers.server.test.ts   CREATE
  venom.server.ts            CREATE
  venom.server.test.ts       CREATE
  usage.server.ts            CREATE
  usage.server.test.ts       CREATE
  api-keys.server.ts         CREATE
  api-keys.server.test.ts    CREATE
```

---

## Shared test helper (copy into every test file)

Every test file uses this exact mock factory. It intercepts all Supabase chaining calls and returns controlled data per table name.

```ts
function makeSupabaseMock(
  tableResponses: Record<string, { data: unknown; error: null | { message: string } }>,
) {
  return {
    from(table: string) {
      const resp = tableResponses[table] ?? {
        data: null,
        error: { message: `no mock for table: ${table}` },
      }
      const chain: any = {}
      for (const m of ["select", "eq", "in", "gte", "is", "neq", "limit", "order"]) {
        chain[m] = () => chain
      }
      chain.single = () => Promise.resolve(resp)
      chain.maybeSingle = () => Promise.resolve(resp)
      chain.then = (resolve: (v: any) => any, reject?: (e: any) => any) =>
        Promise.resolve(resp).then(resolve, reject)
      return chain
    },
  } as any
}
```

---

### Task 1: providers.server.ts

**Files:**
- Create: `src/lib/db/providers.server.ts`
- Create: `src/lib/db/providers.server.test.ts`

**Interfaces:**
- Consumes: `@supabase/supabase-js`, `@/lib/credentials.server`, provider adapters (dynamic import inside `checkAccountModels`)
- Produces: `AccountStatus`, `AccountInfo`, `AccountQuota`, `QuotaGroup`, `AccountModel`, `ModelCheckResult`, `ProviderHealth` types + 7 exported functions

**Key schema facts (from `src/integrations/supabase/types.ts`):**
- `accounts`: id, email, label, plan, status, auth_type, last_synced_at, last_health_check_at, quota_used, quota_total, quota_unit, quota_extra, provider_id
- `providers`: id, slug, name
- `quotas`: account_id, used, total, unit, confidence, resets_at, source
- `account_models`: id, account_id, model_id, enabled, test_status, lifecycle, latency_ms, last_tested_at
- `models`: id, external_id, display_name, capabilities

---

- [ ] **Step 1: Write failing tests**

Create `src/lib/db/providers.server.test.ts`:

```ts
import { describe, it, expect } from "bun:test"
import {
  getAccountStatus,
  getAccountInfo,
  getAccountQuota,
  getAccountModels,
  getProviderHealth,
  listAccounts,
} from "./providers.server"

function makeSupabaseMock(
  tableResponses: Record<string, { data: unknown; error: null | { message: string } }>,
) {
  return {
    from(table: string) {
      const resp = tableResponses[table] ?? {
        data: null,
        error: { message: `no mock for table: ${table}` },
      }
      const chain: any = {}
      for (const m of ["select", "eq", "in", "gte", "is", "neq", "limit", "order"]) {
        chain[m] = () => chain
      }
      chain.single = () => Promise.resolve(resp)
      chain.maybeSingle = () => Promise.resolve(resp)
      chain.then = (resolve: (v: any) => any, reject?: (e: any) => any) =>
        Promise.resolve(resp).then(resolve, reject)
      return chain
    },
  } as any
}

// ── getAccountStatus ──────────────────────────────────────────────────────────

describe("getAccountStatus", () => {
  it("returns the status string", async () => {
    const supabase = makeSupabaseMock({
      accounts: { data: { status: "healthy" }, error: null },
    })
    expect(await getAccountStatus(supabase, "acct-1")).toBe("healthy")
  })

  it("throws when account not found", async () => {
    const supabase = makeSupabaseMock({
      accounts: { data: null, error: { message: "PGRST116" } },
    })
    await expect(getAccountStatus(supabase, "bad-id")).rejects.toThrow("PGRST116")
  })
})

// ── getAccountInfo ────────────────────────────────────────────────────────────

describe("getAccountInfo", () => {
  it("returns mapped account info with provider details", async () => {
    const supabase = makeSupabaseMock({
      accounts: {
        data: {
          id: "acct-1",
          email: "user@example.com",
          label: "My Account",
          plan: "pro",
          status: "healthy",
          auth_type: "oauth2",
          last_synced_at: "2026-06-23T00:00:00Z",
          last_health_check_at: null,
          providers: { slug: "antigravity", name: "Antigravity" },
        },
        error: null,
      },
    })
    const result = await getAccountInfo(supabase, "acct-1")
    expect(result.email).toBe("user@example.com")
    expect(result.provider_slug).toBe("antigravity")
    expect(result.provider_name).toBe("Antigravity")
    expect(result.status).toBe("healthy")
  })

  it("handles null providers gracefully", async () => {
    const supabase = makeSupabaseMock({
      accounts: {
        data: {
          id: "acct-2",
          email: null,
          label: "L",
          plan: null,
          status: "degraded",
          auth_type: "api_key",
          last_synced_at: null,
          last_health_check_at: null,
          providers: null,
        },
        error: null,
      },
    })
    const result = await getAccountInfo(supabase, "acct-2")
    expect(result.provider_slug).toBe("")
    expect(result.provider_name).toBe("")
  })

  it("throws when account not found", async () => {
    const supabase = makeSupabaseMock({
      accounts: { data: null, error: { message: "not found" } },
    })
    await expect(getAccountInfo(supabase, "bad")).rejects.toThrow("not found")
  })
})

// ── getAccountQuota ───────────────────────────────────────────────────────────

describe("getAccountQuota", () => {
  it("returns quota with empty groups when quota_extra is null", async () => {
    const supabase = makeSupabaseMock({
      accounts: {
        data: { id: "acct-1", quota_used: 40, quota_total: 100, quota_unit: "%", quota_extra: null },
        error: null,
      },
      quotas: { data: { resets_at: null, confidence: "high" }, error: null },
    })
    const result = await getAccountQuota(supabase, "acct-1")
    expect(result.used).toBe(40)
    expect(result.total).toBe(100)
    expect(result.unit).toBe("%")
    expect(result.groups).toEqual([])
    expect(result.confidence).toBe("high")
  })

  it("extracts quota groups from quota_extra.groups", async () => {
    const supabase = makeSupabaseMock({
      accounts: {
        data: {
          id: "acct-1",
          quota_used: 60,
          quota_total: 100,
          quota_unit: "%",
          quota_extra: {
            groups: [
              { name: "Gemini Models", modelIds: ["m1", "m2"], fiveHourQuota: { remainingFraction: 0.5, resetTime: "2026-06-23T06:00:00Z", isExhausted: false } },
            ],
          },
        },
        error: null,
      },
      quotas: { data: null, error: null },
    })
    const result = await getAccountQuota(supabase, "acct-1")
    expect(result.groups).toHaveLength(1)
    expect(result.groups[0]!.name).toBe("Gemini Models")
    expect(result.groups[0]!.model_count).toBe(2)
    expect(result.groups[0]!.five_hour?.remaining_pct).toBe(50)
  })

  it("throws when account not found", async () => {
    const supabase = makeSupabaseMock({
      accounts: { data: null, error: { message: "not found" } },
    })
    await expect(getAccountQuota(supabase, "bad")).rejects.toThrow("not found")
  })
})

// ── getAccountModels ──────────────────────────────────────────────────────────

describe("getAccountModels", () => {
  it("maps model rows to AccountModel shape", async () => {
    const supabase = makeSupabaseMock({
      account_models: {
        data: [
          {
            id: "am-1",
            enabled: true,
            test_status: "working",
            lifecycle: "approved",
            latency_ms: 200,
            last_tested_at: "2026-06-23T00:00:00Z",
            models: {
              external_id: "claude-sonnet-4-6",
              display_name: "Claude Sonnet 4.6",
              capabilities: { list: ["chat", "tools"] },
            },
          },
        ],
        error: null,
      },
    })
    const result = await getAccountModels(supabase, "acct-1")
    expect(result).toHaveLength(1)
    expect(result[0]!.external_id).toBe("claude-sonnet-4-6")
    expect(result[0]!.capabilities).toEqual(["chat", "tools"])
    expect(result[0]!.test_status).toBe("working")
    expect(result[0]!.enabled).toBe(true)
  })

  it("handles old capabilities format (numeric keys)", async () => {
    const supabase = makeSupabaseMock({
      account_models: {
        data: [
          {
            id: "am-2",
            enabled: true,
            test_status: null,
            lifecycle: "discovered",
            latency_ms: null,
            last_tested_at: null,
            models: {
              external_id: "gpt-4",
              display_name: "GPT-4",
              capabilities: { "0": "chat", "1": "tools" },
            },
          },
        ],
        error: null,
      },
    })
    const result = await getAccountModels(supabase, "acct-1")
    expect(result[0]!.capabilities).toEqual(["chat", "tools"])
    expect(result[0]!.test_status).toBe("untested")
  })

  it("returns empty array when no models", async () => {
    const supabase = makeSupabaseMock({
      account_models: { data: [], error: null },
    })
    expect(await getAccountModels(supabase, "acct-1")).toEqual([])
  })
})

// ── getProviderHealth ─────────────────────────────────────────────────────────

describe("getProviderHealth", () => {
  it("aggregates account statuses per provider", async () => {
    const supabase = makeSupabaseMock({
      providers: {
        data: [
          {
            slug: "antigravity",
            name: "Antigravity",
            accounts: [{ status: "healthy" }, { status: "degraded" }],
          },
        ],
        error: null,
      },
    })
    const result = await getProviderHealth(supabase)
    expect(result).toHaveLength(1)
    expect(result[0]!.accounts_healthy).toBe(1)
    expect(result[0]!.accounts_degraded).toBe(1)
    expect(result[0]!.accounts_expired).toBe(0)
    expect(result[0]!.is_healthy).toBe(true)
  })

  it("marks provider as unhealthy when no healthy accounts exist", async () => {
    const supabase = makeSupabaseMock({
      providers: {
        data: [{ slug: "antigravity", name: "Antigravity", accounts: [{ status: "expired" }] }],
        error: null,
      },
    })
    const result = await getProviderHealth(supabase)
    expect(result[0]!.is_healthy).toBe(false)
  })

  it("returns empty array for provider with no accounts", async () => {
    const supabase = makeSupabaseMock({
      providers: {
        data: [{ slug: "opencode-zen", name: "OpenCode Zen", accounts: [] }],
        error: null,
      },
    })
    const result = await getProviderHealth(supabase)
    expect(result[0]!.accounts_total).toBe(0)
    expect(result[0]!.is_healthy).toBe(false)
  })
})

// ── listAccounts ──────────────────────────────────────────────────────────────

describe("listAccounts", () => {
  it("returns mapped accounts list", async () => {
    const supabase = makeSupabaseMock({
      accounts: {
        data: [
          {
            id: "acct-1",
            email: "a@x.com",
            label: "A",
            plan: null,
            status: "healthy",
            auth_type: "api_key",
            last_synced_at: null,
            last_health_check_at: null,
            providers: { slug: "opencode-zen", name: "OpenCode Zen" },
          },
        ],
        error: null,
      },
    })
    const result = await listAccounts(supabase)
    expect(result).toHaveLength(1)
    expect(result[0]!.provider_slug).toBe("opencode-zen")
    expect(result[0]!.status).toBe("healthy")
  })

  it("returns empty array when no accounts match", async () => {
    const supabase = makeSupabaseMock({ accounts: { data: [], error: null } })
    expect(await listAccounts(supabase)).toEqual([])
  })
})
```

- [ ] **Step 2: Run tests to confirm they fail**

```
bun test src/lib/db/providers.server.test.ts
```

Expected: `Cannot find module './providers.server'`

- [ ] **Step 3: Implement `providers.server.ts`**

Create `src/lib/db/providers.server.ts`:

```ts
import type { SupabaseClient } from "@supabase/supabase-js"

// ── Types ──────────────────────────────────────────────────────────────────────

export type AccountStatus = "healthy" | "degraded" | "expired"

export type AccountInfo = {
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

export type QuotaGroup = {
  name: string
  short_label: string
  model_count: number
  five_hour?: {
    remaining_pct: number
    reset_at: string
    exhausted: boolean
  }
}

export type AccountQuota = {
  account_id: string
  used: number | null
  total: number | null
  unit: string | null
  groups: QuotaGroup[]
  resets_at: string | null
  confidence: "high" | "medium" | "low" | null
}

export type AccountModel = {
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

export type ModelCheckResult = {
  external_id: string
  ok: boolean
  latency_ms: number
  error?: string
}

export type ProviderHealth = {
  provider_slug: string
  provider_name: string
  accounts_total: number
  accounts_healthy: number
  accounts_degraded: number
  accounts_expired: number
  is_healthy: boolean
}

// ── Internal helpers ───────────────────────────────────────────────────────────

const QUOTA_SHORT_LABELS: Record<string, string> = {
  "Gemini Models": "GEM",
  "Claude and GPT Models": "OPT",
}

function extractQuotaGroups(extra: Record<string, unknown> | null): QuotaGroup[] {
  const raw =
    (extra?.groups as
      | Array<{
          name: string
          modelIds?: string[]
          fiveHourQuota?: {
            remainingFraction?: number
            resetTime?: string
            isExhausted?: boolean
          }
        }>
      | undefined) ?? []
  return raw.map((g) => ({
    name: g.name,
    short_label: QUOTA_SHORT_LABELS[g.name] ?? (g.name.split(" ")[0] ?? g.name),
    model_count: g.modelIds?.length ?? 0,
    five_hour: g.fiveHourQuota?.resetTime
      ? {
          remaining_pct: Math.round((g.fiveHourQuota.remainingFraction ?? 0) * 100),
          reset_at: g.fiveHourQuota.resetTime,
          exhausted: Boolean(g.fiveHourQuota.isExhausted),
        }
      : undefined,
  }))
}

function extractCapabilities(caps: Record<string, unknown> | null): string[] {
  if (!caps) return []
  if (Array.isArray(caps.list)) return caps.list as string[]
  return Object.entries(caps)
    .filter(([k]) => /^\d+$/.test(k))
    .sort(([a], [b]) => Number(a) - Number(b))
    .map(([, v]) => String(v))
}

// ── Exported functions ─────────────────────────────────────────────────────────

export async function getAccountStatus(
  supabase: SupabaseClient,
  accountId: string,
): Promise<AccountStatus> {
  const { data, error } = await supabase
    .from("accounts")
    .select("status")
    .eq("id", accountId)
    .single()
  if (error || !data) throw new Error(`getAccountStatus: ${error?.message ?? "not found"}`)
  return (data as any).status as AccountStatus
}

export async function getAccountInfo(
  supabase: SupabaseClient,
  accountId: string,
): Promise<AccountInfo> {
  const { data, error } = await supabase
    .from("accounts")
    .select(
      "id,email,label,plan,status,auth_type,last_synced_at,last_health_check_at,providers(slug,name)",
    )
    .eq("id", accountId)
    .single()
  if (error || !data) throw new Error(`getAccountInfo: ${error?.message ?? "not found"}`)
  const row = data as any
  const p = row.providers as { slug: string; name: string } | null
  return {
    id: row.id,
    email: row.email,
    label: row.label,
    plan: row.plan,
    status: row.status as AccountStatus,
    provider_slug: p?.slug ?? "",
    provider_name: p?.name ?? "",
    auth_type: row.auth_type,
    last_synced_at: row.last_synced_at,
    last_health_check_at: row.last_health_check_at,
  }
}

export async function getAccountQuota(
  supabase: SupabaseClient,
  accountId: string,
): Promise<AccountQuota> {
  const { data: acct, error: acctErr } = await supabase
    .from("accounts")
    .select("id,quota_used,quota_total,quota_unit,quota_extra")
    .eq("id", accountId)
    .single()
  if (acctErr || !acct) throw new Error(`getAccountQuota: ${acctErr?.message ?? "not found"}`)

  const { data: quotaRow } = await supabase
    .from("quotas")
    .select("confidence,resets_at")
    .eq("account_id", accountId)
    .maybeSingle()

  const extra = ((acct as any).quota_extra ?? null) as Record<string, unknown> | null
  return {
    account_id: accountId,
    used: (acct as any).quota_used ?? null,
    total: (acct as any).quota_total ?? null,
    unit: (acct as any).quota_unit ?? null,
    groups: extractQuotaGroups(extra),
    resets_at: (quotaRow as any)?.resets_at ?? null,
    confidence: ((quotaRow as any)?.confidence ?? null) as "high" | "medium" | "low" | null,
  }
}

export async function getAccountModels(
  supabase: SupabaseClient,
  accountId: string,
  opts?: { enabledOnly?: boolean; lifecycle?: string },
): Promise<AccountModel[]> {
  let q = supabase
    .from("account_models")
    .select(
      "id,enabled,test_status,lifecycle,latency_ms,last_tested_at,models!inner(external_id,display_name,capabilities)",
    )
    .eq("account_id", accountId)
  if (opts?.enabledOnly) q = (q as any).eq("enabled", true)
  if (opts?.lifecycle) q = (q as any).eq("lifecycle", opts.lifecycle)

  const { data, error } = await q
  if (error) throw new Error(`getAccountModels: ${error.message}`)

  return ((data ?? []) as any[]).map((row) => {
    const model = row.models
    return {
      id: row.id,
      external_id: model?.external_id ?? "",
      display_name: model?.display_name ?? "",
      capabilities: extractCapabilities(model?.capabilities ?? null),
      enabled: row.enabled,
      test_status: (row.test_status ?? "untested") as "working" | "failed" | "untested",
      latency_ms: row.latency_ms ?? null,
      last_tested_at: row.last_tested_at ?? null,
      lifecycle: row.lifecycle,
    }
  })
}

// Note: checkAccountModels performs live provider API calls (not unit tested — integration concern)
export async function checkAccountModels(
  supabase: SupabaseClient,
  accountId: string,
  externalIds?: string[],
): Promise<ModelCheckResult[]> {
  const { data: acct, error } = await supabase
    .from("accounts")
    .select("credentials_enc,credentials_iv,credentials_tag,providers(slug)")
    .eq("id", accountId)
    .single()
  if (error || !acct) throw new Error(`checkAccountModels: ${error?.message ?? "not found"}`)

  const slug = ((acct as any).providers as { slug?: string } | null)?.slug ?? ""
  const { unpackCredentials } = await import("@/lib/credentials.server")
  const creds = unpackCredentials(acct as any)

  let targets = externalIds
  if (!targets) {
    const models = await getAccountModels(supabase, accountId, { enabledOnly: true })
    targets = models.map((m) => m.external_id)
  }

  const adapter =
    slug === "claude-code"
      ? await import("@/lib/providers/adapters/claude-code.server")
      : slug === "antigravity"
        ? await import("@/lib/providers/adapters/antigravity.server")
        : await import("@/lib/providers/adapters/opencode-zen.server")

  return Promise.all(
    targets.map(async (ext) => {
      const r = await adapter.testModel(creds, ext)
      return { external_id: ext, ok: r.ok, latency_ms: r.latency_ms ?? 0, error: r.error }
    }),
  )
}

export async function getProviderHealth(
  supabase: SupabaseClient,
  opts?: { providerSlug?: string },
): Promise<ProviderHealth[]> {
  let q = supabase.from("providers").select("slug,name,accounts(status)")
  if (opts?.providerSlug) q = (q as any).eq("slug", opts.providerSlug)

  const { data, error } = await q
  if (error) throw new Error(`getProviderHealth: ${error.message}`)

  return ((data ?? []) as any[]).map((p) => {
    const accounts = (p.accounts ?? []) as Array<{ status: string }>
    const healthy = accounts.filter((a) => a.status === "healthy").length
    const degraded = accounts.filter((a) => a.status === "degraded").length
    const expired = accounts.filter((a) => a.status === "expired").length
    return {
      provider_slug: p.slug,
      provider_name: p.name,
      accounts_total: accounts.length,
      accounts_healthy: healthy,
      accounts_degraded: degraded,
      accounts_expired: expired,
      is_healthy: healthy > 0,
    }
  })
}

export async function listAccounts(
  supabase: SupabaseClient,
  opts?: { status?: AccountStatus | AccountStatus[]; providerSlug?: string },
): Promise<AccountInfo[]> {
  let q = supabase
    .from("accounts")
    .select(
      "id,email,label,plan,status,auth_type,last_synced_at,last_health_check_at,providers(slug,name)",
    )
    .order("created_at", { ascending: false })

  if (opts?.status) {
    const statuses = Array.isArray(opts.status) ? opts.status : [opts.status]
    q =
      statuses.length === 1
        ? (q as any).eq("status", statuses[0])
        : (q as any).in("status", statuses)
  }

  const { data, error } = await q
  if (error) throw new Error(`listAccounts: ${error.message}`)

  return ((data ?? []) as any[]).map((row) => {
    const p = row.providers as { slug: string; name: string } | null
    return {
      id: row.id,
      email: row.email,
      label: row.label,
      plan: row.plan,
      status: row.status as AccountStatus,
      provider_slug: p?.slug ?? "",
      provider_name: p?.name ?? "",
      auth_type: row.auth_type,
      last_synced_at: row.last_synced_at,
      last_health_check_at: row.last_health_check_at,
    }
  })
}
```

- [ ] **Step 4: Run tests to confirm they pass**

```
bun test src/lib/db/providers.server.test.ts
```

Expected: all 13 assertions pass, 0 failures.

- [ ] **Step 5: Commit**

```bash
git add src/lib/db/providers.server.ts src/lib/db/providers.server.test.ts
git commit -m "feat(db): add providers domain — account status/info/quota/models/health"
```

---

### Task 2: venom.server.ts

**Files:**
- Create: `src/lib/db/venom.server.ts`
- Create: `src/lib/db/venom.server.test.ts`

**Interfaces:**
- Consumes: `@supabase/supabase-js`
- Produces: `VenomModel`, `RoutingRule`, `getVenomModel`, `listVenomModels`, `listRoutingRules`

**Key schema facts (from `src/integrations/supabase/types.ts`):**
- `venom_models`: id, slug, weight_cost, weight_speed, weight_quality, max_fallback_attempts, timeout_ms
- `routing_rules`: id, venom_slug, model_id (FK → models.id), account_id (FK → accounts.id), priority, active, role, conditions
- `routing_rules` joins: `models!inner(external_id, providers!inner(slug))`

---

- [ ] **Step 1: Write failing tests**

Create `src/lib/db/venom.server.test.ts`:

```ts
import { describe, it, expect } from "bun:test"
import { getVenomModel, listVenomModels, listRoutingRules } from "./venom.server"

function makeSupabaseMock(
  tableResponses: Record<string, { data: unknown; error: null | { message: string } }>,
) {
  return {
    from(table: string) {
      const resp = tableResponses[table] ?? {
        data: null,
        error: { message: `no mock for table: ${table}` },
      }
      const chain: any = {}
      for (const m of ["select", "eq", "in", "gte", "is", "neq", "limit", "order"]) {
        chain[m] = () => chain
      }
      chain.single = () => Promise.resolve(resp)
      chain.maybeSingle = () => Promise.resolve(resp)
      chain.then = (resolve: (v: any) => any, reject?: (e: any) => any) =>
        Promise.resolve(resp).then(resolve, reject)
      return chain
    },
  } as any
}

describe("getVenomModel", () => {
  it("returns the venom model for the given slug", async () => {
    const supabase = makeSupabaseMock({
      venom_models: {
        data: {
          id: "vm-1",
          slug: "pro",
          weight_cost: 0.3,
          weight_speed: 0.3,
          weight_quality: 0.4,
          max_fallback_attempts: 3,
          timeout_ms: 30000,
        },
        error: null,
      },
    })
    const result = await getVenomModel(supabase, "pro")
    expect(result.slug).toBe("pro")
    expect(result.weight_quality).toBe(0.4)
    expect(result.timeout_ms).toBe(30000)
  })

  it("throws when venom model not found", async () => {
    const supabase = makeSupabaseMock({
      venom_models: { data: null, error: { message: "not found" } },
    })
    await expect(getVenomModel(supabase, "lite")).rejects.toThrow("getVenomModel: not found")
  })
})

describe("listVenomModels", () => {
  it("returns all venom models", async () => {
    const supabase = makeSupabaseMock({
      venom_models: {
        data: [
          { id: "1", slug: "lite", weight_cost: 0.5, weight_speed: 0.3, weight_quality: 0.2, max_fallback_attempts: 2, timeout_ms: 15000 },
          { id: "2", slug: "pro", weight_cost: 0.3, weight_speed: 0.3, weight_quality: 0.4, max_fallback_attempts: 3, timeout_ms: 30000 },
          { id: "3", slug: "max", weight_cost: 0.2, weight_speed: 0.2, weight_quality: 0.6, max_fallback_attempts: 5, timeout_ms: 60000 },
        ],
        error: null,
      },
    })
    const result = await listVenomModels(supabase)
    expect(result).toHaveLength(3)
    expect(result.map((m) => m.slug)).toEqual(["lite", "pro", "max"])
  })
})

describe("listRoutingRules", () => {
  it("maps routing rule rows with joined model info", async () => {
    const supabase = makeSupabaseMock({
      routing_rules: {
        data: [
          {
            id: "rr-1",
            venom_slug: "pro",
            model_id: "model-1",
            account_id: "acct-1",
            priority: 10,
            active: true,
            role: "primary",
            models: { external_id: "claude-sonnet-4-6", providers: { slug: "antigravity" } },
          },
        ],
        error: null,
      },
    })
    const result = await listRoutingRules(supabase)
    expect(result).toHaveLength(1)
    expect(result[0]!.model_external_id).toBe("claude-sonnet-4-6")
    expect(result[0]!.provider_slug).toBe("antigravity")
    expect(result[0]!.venom_slug).toBe("pro")
    expect(result[0]!.active).toBe(true)
  })

  it("returns empty array when no rules exist", async () => {
    const supabase = makeSupabaseMock({
      routing_rules: { data: [], error: null },
    })
    expect(await listRoutingRules(supabase)).toEqual([])
  })

  it("handles missing model join gracefully", async () => {
    const supabase = makeSupabaseMock({
      routing_rules: {
        data: [
          {
            id: "rr-2",
            venom_slug: "lite",
            model_id: "model-2",
            account_id: "acct-2",
            priority: 5,
            active: false,
            role: "fallback",
            models: null,
          },
        ],
        error: null,
      },
    })
    const result = await listRoutingRules(supabase)
    expect(result[0]!.model_external_id).toBe("")
    expect(result[0]!.provider_slug).toBe("")
  })
})
```

- [ ] **Step 2: Run tests to confirm they fail**

```
bun test src/lib/db/venom.server.test.ts
```

Expected: `Cannot find module './venom.server'`

- [ ] **Step 3: Implement `venom.server.ts`**

Create `src/lib/db/venom.server.ts`:

```ts
import type { SupabaseClient } from "@supabase/supabase-js"

// ── Types ──────────────────────────────────────────────────────────────────────

export type VenomModel = {
  id: string
  slug: "lite" | "pro" | "max"
  weight_cost: number
  weight_speed: number
  weight_quality: number
  max_fallback_attempts: number
  timeout_ms: number
}

export type RoutingRule = {
  id: string
  venom_slug: "lite" | "pro" | "max"
  model_id: string
  account_id: string
  priority: number
  active: boolean
  role: string
  model_external_id: string
  provider_slug: string
}

// ── Exported functions ─────────────────────────────────────────────────────────

export async function getVenomModel(
  supabase: SupabaseClient,
  slug: "lite" | "pro" | "max",
): Promise<VenomModel> {
  const { data, error } = await supabase
    .from("venom_models")
    .select("id,slug,weight_cost,weight_speed,weight_quality,max_fallback_attempts,timeout_ms")
    .eq("slug", slug)
    .single()
  if (error || !data) throw new Error(`getVenomModel: ${error?.message ?? "not found"}`)
  return data as unknown as VenomModel
}

export async function listVenomModels(supabase: SupabaseClient): Promise<VenomModel[]> {
  const { data, error } = await supabase
    .from("venom_models")
    .select("id,slug,weight_cost,weight_speed,weight_quality,max_fallback_attempts,timeout_ms")
    .order("slug")
  if (error) throw new Error(`listVenomModels: ${error.message}`)
  return (data ?? []) as unknown as VenomModel[]
}

export async function listRoutingRules(
  supabase: SupabaseClient,
  opts?: { venomSlug?: "lite" | "pro" | "max"; activeOnly?: boolean },
): Promise<RoutingRule[]> {
  let q = supabase
    .from("routing_rules")
    .select(
      "id,venom_slug,model_id,account_id,priority,active,role,models!inner(external_id,providers!inner(slug))",
    )
    .order("priority", { ascending: false })
  if (opts?.venomSlug) q = (q as any).eq("venom_slug", opts.venomSlug)
  if (opts?.activeOnly) q = (q as any).eq("active", true)

  const { data, error } = await q
  if (error) throw new Error(`listRoutingRules: ${error.message}`)

  return ((data ?? []) as any[]).map((row) => ({
    id: row.id,
    venom_slug: row.venom_slug as "lite" | "pro" | "max",
    model_id: row.model_id,
    account_id: row.account_id,
    priority: row.priority,
    active: row.active,
    role: row.role ?? "",
    model_external_id: row.models?.external_id ?? "",
    provider_slug: row.models?.providers?.slug ?? "",
  }))
}
```

- [ ] **Step 4: Run tests to confirm they pass**

```
bun test src/lib/db/venom.server.test.ts
```

Expected: all 8 assertions pass, 0 failures.

- [ ] **Step 5: Commit**

```bash
git add src/lib/db/venom.server.ts src/lib/db/venom.server.test.ts
git commit -m "feat(db): add venom domain — venom models and routing rules"
```

---

### Task 3: usage.server.ts

**Files:**
- Create: `src/lib/db/usage.server.ts`
- Create: `src/lib/db/usage.server.test.ts`

**Interfaces:**
- Consumes: `@supabase/supabase-js`
- Produces: `UsageRecord`, `MetricsSummary`, `listUsageRecords`, `getMetricsSummary`, `getTraffic7d`

**Key schema facts:**
- `usage_records`: id, venom_slug, cost_usd, input_tokens, output_tokens, success, fallback_used, created_at

---

- [ ] **Step 1: Write failing tests**

Create `src/lib/db/usage.server.test.ts`:

```ts
import { describe, it, expect } from "bun:test"
import { listUsageRecords, getMetricsSummary, getTraffic7d } from "./usage.server"

function makeSupabaseMock(
  tableResponses: Record<string, { data: unknown; error: null | { message: string } }>,
) {
  return {
    from(table: string) {
      const resp = tableResponses[table] ?? {
        data: null,
        error: { message: `no mock for table: ${table}` },
      }
      const chain: any = {}
      for (const m of ["select", "eq", "in", "gte", "is", "neq", "limit", "order"]) {
        chain[m] = () => chain
      }
      chain.single = () => Promise.resolve(resp)
      chain.maybeSingle = () => Promise.resolve(resp)
      chain.then = (resolve: (v: any) => any, reject?: (e: any) => any) =>
        Promise.resolve(resp).then(resolve, reject)
      return chain
    },
  } as any
}

describe("listUsageRecords", () => {
  it("returns usage records", async () => {
    const supabase = makeSupabaseMock({
      usage_records: {
        data: [
          {
            id: "ur-1",
            venom_slug: "pro",
            cost_usd: 0.01,
            input_tokens: 100,
            output_tokens: 200,
            success: true,
            fallback_used: false,
            created_at: "2026-06-23T00:00:00Z",
          },
        ],
        error: null,
      },
    })
    const result = await listUsageRecords(supabase)
    expect(result).toHaveLength(1)
    expect(result[0]!.venom_slug).toBe("pro")
    expect(result[0]!.success).toBe(true)
    expect(result[0]!.fallback_used).toBe(false)
  })

  it("returns empty array when no records", async () => {
    const supabase = makeSupabaseMock({ usage_records: { data: [], error: null } })
    expect(await listUsageRecords(supabase)).toEqual([])
  })

  it("throws on DB error", async () => {
    const supabase = makeSupabaseMock({
      usage_records: { data: null, error: { message: "connection refused" } },
    })
    await expect(listUsageRecords(supabase)).rejects.toThrow("listUsageRecords: connection refused")
  })
})

describe("getMetricsSummary", () => {
  it("computes correct totals and rates", async () => {
    const supabase = makeSupabaseMock({
      usage_records: {
        data: [
          { success: true, fallback_used: false, cost_usd: 0.01, input_tokens: 100, output_tokens: 50 },
          { success: true, fallback_used: true, cost_usd: 0.02, input_tokens: 200, output_tokens: 100 },
          { success: false, fallback_used: false, cost_usd: null, input_tokens: null, output_tokens: null },
        ],
        error: null,
      },
    })
    const result = await getMetricsSummary(supabase)
    expect(result.total_requests).toBe(3)
    expect(result.total_tokens).toBe(450)
    expect(result.total_cost_usd).toBeCloseTo(0.03)
    expect(result.success_rate).toBeCloseTo(2 / 3)
    expect(result.fallback_rate).toBeCloseTo(1 / 3)
  })

  it("returns zero values when no records", async () => {
    const supabase = makeSupabaseMock({ usage_records: { data: [], error: null } })
    const result = await getMetricsSummary(supabase)
    expect(result.total_requests).toBe(0)
    expect(result.total_tokens).toBe(0)
    expect(result.total_cost_usd).toBe(0)
    expect(result.success_rate).toBe(0)
    expect(result.fallback_rate).toBe(0)
  })
})

describe("getTraffic7d", () => {
  it("returns exactly 7 day buckets", async () => {
    const supabase = makeSupabaseMock({
      usage_records: { data: [], error: null },
    })
    const result = await getTraffic7d(supabase)
    expect(result).toHaveLength(7)
    expect(result.every((r) => ["Sun","Mon","Tue","Wed","Thu","Fri","Sat"].includes(r.day))).toBe(true)
    expect(result.every((r) => typeof r.requests === "number")).toBe(true)
  })

  it("counts requests into the correct day buckets", async () => {
    const today = new Date()
    today.setHours(12, 0, 0, 0)
    const supabase = makeSupabaseMock({
      usage_records: {
        data: [{ created_at: today.toISOString() }, { created_at: today.toISOString() }],
        error: null,
      },
    })
    const result = await getTraffic7d(supabase)
    const total = result.reduce((s, r) => s + r.requests, 0)
    expect(total).toBe(2)
  })
})
```

- [ ] **Step 2: Run tests to confirm they fail**

```
bun test src/lib/db/usage.server.test.ts
```

Expected: `Cannot find module './usage.server'`

- [ ] **Step 3: Implement `usage.server.ts`**

Create `src/lib/db/usage.server.ts`:

```ts
import type { SupabaseClient } from "@supabase/supabase-js"

// ── Types ──────────────────────────────────────────────────────────────────────

export type UsageRecord = {
  id: string
  venom_slug: string
  cost_usd: number | null
  input_tokens: number | null
  output_tokens: number | null
  success: boolean
  fallback_used: boolean
  created_at: string
}

export type MetricsSummary = {
  total_requests: number
  total_tokens: number
  total_cost_usd: number
  success_rate: number
  fallback_rate: number
}

// ── Internal helpers ───────────────────────────────────────────────────────────

const DAY_LABELS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"] as const

// ── Exported functions ─────────────────────────────────────────────────────────

export async function listUsageRecords(
  supabase: SupabaseClient,
  opts?: { since?: string; venomSlug?: string; limit?: number },
): Promise<UsageRecord[]> {
  let q = supabase
    .from("usage_records")
    .select("id,venom_slug,cost_usd,input_tokens,output_tokens,success,fallback_used,created_at")
    .order("created_at", { ascending: false })
  if (opts?.since) q = (q as any).gte("created_at", opts.since)
  if (opts?.venomSlug) q = (q as any).eq("venom_slug", opts.venomSlug)
  if (opts?.limit) q = (q as any).limit(opts.limit)

  const { data, error } = await q
  if (error) throw new Error(`listUsageRecords: ${error.message}`)
  return (data ?? []) as unknown as UsageRecord[]
}

export async function getMetricsSummary(
  supabase: SupabaseClient,
  opts?: { since?: string },
): Promise<MetricsSummary> {
  let q = supabase
    .from("usage_records")
    .select("success,fallback_used,cost_usd,input_tokens,output_tokens")
  if (opts?.since) q = (q as any).gte("created_at", opts.since)

  const { data, error } = await q
  if (error) throw new Error(`getMetricsSummary: ${error.message}`)

  const records = (data ?? []) as Array<{
    success: boolean
    fallback_used: boolean
    cost_usd: number | null
    input_tokens: number | null
    output_tokens: number | null
  }>

  const total_requests = records.length
  const total_tokens = records.reduce(
    (s, r) => s + (r.input_tokens ?? 0) + (r.output_tokens ?? 0),
    0,
  )
  const total_cost_usd = records.reduce((s, r) => s + Number(r.cost_usd ?? 0), 0)
  const successes = records.filter((r) => r.success !== false).length
  const fallbacks = records.filter((r) => r.fallback_used).length

  return {
    total_requests,
    total_tokens,
    total_cost_usd,
    success_rate: total_requests ? successes / total_requests : 0,
    fallback_rate: total_requests ? fallbacks / total_requests : 0,
  }
}

export async function getTraffic7d(
  supabase: SupabaseClient,
): Promise<{ day: string; requests: number }[]> {
  const since = new Date(Date.now() - 7 * 86400000).toISOString()
  const { data, error } = await supabase
    .from("usage_records")
    .select("created_at")
    .gte("created_at", since)
  if (error) throw new Error(`getTraffic7d: ${error.message}`)

  const buckets = new Map<string, number>()
  const now = new Date()
  for (let i = 6; i >= 0; i--) {
    const d = new Date(now)
    d.setDate(d.getDate() - i)
    d.setHours(0, 0, 0, 0)
    buckets.set(d.toISOString().slice(0, 10), 0)
  }
  for (const r of (data ?? []) as Array<{ created_at: string }>) {
    const key = new Date(r.created_at).toISOString().slice(0, 10)
    if (buckets.has(key)) buckets.set(key, (buckets.get(key) ?? 0) + 1)
  }
  return [...buckets.entries()].map(([key, requests]) => {
    const d = new Date(key + "T12:00:00")
    return { day: DAY_LABELS[d.getDay()]!, requests }
  })
}
```

- [ ] **Step 4: Run tests to confirm they pass**

```
bun test src/lib/db/usage.server.test.ts
```

Expected: all 9 assertions pass, 0 failures.

- [ ] **Step 5: Commit**

```bash
git add src/lib/db/usage.server.ts src/lib/db/usage.server.test.ts
git commit -m "feat(db): add usage domain — records, metrics summary, 7-day traffic"
```

---

### Task 4: api-keys.server.ts

**Files:**
- Create: `src/lib/db/api-keys.server.ts`
- Create: `src/lib/db/api-keys.server.test.ts`

**Interfaces:**
- Consumes: `@supabase/supabase-js`
- Produces: `ApiKey`, `listApiKeys`, `getApiKey`

**Key schema facts:**
- `venom_api_keys`: id, name, key_prefix, allowed_models, rpm_limit, tpd_limit, monthly_cap_usd, revoked_at, last_used_at, created_at, key_hash (key_hash is never returned — not in select)

---

- [ ] **Step 1: Write failing tests**

Create `src/lib/db/api-keys.server.test.ts`:

```ts
import { describe, it, expect } from "bun:test"
import { listApiKeys, getApiKey } from "./api-keys.server"

function makeSupabaseMock(
  tableResponses: Record<string, { data: unknown; error: null | { message: string } }>,
) {
  return {
    from(table: string) {
      const resp = tableResponses[table] ?? {
        data: null,
        error: { message: `no mock for table: ${table}` },
      }
      const chain: any = {}
      for (const m of ["select", "eq", "in", "gte", "is", "neq", "limit", "order"]) {
        chain[m] = () => chain
      }
      chain.single = () => Promise.resolve(resp)
      chain.maybeSingle = () => Promise.resolve(resp)
      chain.then = (resolve: (v: any) => any, reject?: (e: any) => any) =>
        Promise.resolve(resp).then(resolve, reject)
      return chain
    },
  } as any
}

const SAMPLE_KEY = {
  id: "key-1",
  name: "My Key",
  key_prefix: "vk_live_abc",
  allowed_models: ["lite", "pro"],
  rpm_limit: 60,
  tpd_limit: null,
  monthly_cap_usd: 10,
  revoked_at: null,
  last_used_at: null,
  created_at: "2026-06-23T00:00:00Z",
}

describe("listApiKeys", () => {
  it("returns all API keys", async () => {
    const supabase = makeSupabaseMock({
      venom_api_keys: { data: [SAMPLE_KEY], error: null },
    })
    const result = await listApiKeys(supabase)
    expect(result).toHaveLength(1)
    expect(result[0]!.name).toBe("My Key")
    expect(result[0]!.allowed_models).toEqual(["lite", "pro"])
    expect(result[0]!.rpm_limit).toBe(60)
  })

  it("returns empty array when no keys", async () => {
    const supabase = makeSupabaseMock({ venom_api_keys: { data: [], error: null } })
    expect(await listApiKeys(supabase)).toEqual([])
  })

  it("does not expose key_hash in returned objects", async () => {
    const supabase = makeSupabaseMock({
      venom_api_keys: { data: [{ ...SAMPLE_KEY, key_hash: "secret" }], error: null },
    })
    const result = await listApiKeys(supabase)
    expect("key_hash" in (result[0] ?? {})).toBe(false)
  })

  it("throws on DB error", async () => {
    const supabase = makeSupabaseMock({
      venom_api_keys: { data: null, error: { message: "permission denied" } },
    })
    await expect(listApiKeys(supabase)).rejects.toThrow("listApiKeys: permission denied")
  })
})

describe("getApiKey", () => {
  it("returns the requested key by id", async () => {
    const supabase = makeSupabaseMock({
      venom_api_keys: { data: SAMPLE_KEY, error: null },
    })
    const result = await getApiKey(supabase, "key-1")
    expect(result.id).toBe("key-1")
    expect(result.key_prefix).toBe("vk_live_abc")
    expect(result.revoked_at).toBeNull()
  })

  it("throws when key not found", async () => {
    const supabase = makeSupabaseMock({
      venom_api_keys: { data: null, error: { message: "PGRST116" } },
    })
    await expect(getApiKey(supabase, "bad-id")).rejects.toThrow("getApiKey: PGRST116")
  })
})
```

- [ ] **Step 2: Run tests to confirm they fail**

```
bun test src/lib/db/api-keys.server.test.ts
```

Expected: `Cannot find module './api-keys.server'`

- [ ] **Step 3: Implement `api-keys.server.ts`**

Create `src/lib/db/api-keys.server.ts`:

```ts
import type { SupabaseClient } from "@supabase/supabase-js"

// ── Types ──────────────────────────────────────────────────────────────────────

export type ApiKey = {
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

// key_hash intentionally excluded — never returned to callers
const KEY_SELECT =
  "id,name,key_prefix,allowed_models,rpm_limit,tpd_limit,monthly_cap_usd,revoked_at,last_used_at,created_at"

// ── Exported functions ─────────────────────────────────────────────────────────

export async function listApiKeys(
  supabase: SupabaseClient,
  opts?: { activeOnly?: boolean },
): Promise<ApiKey[]> {
  let q = supabase
    .from("venom_api_keys")
    .select(KEY_SELECT)
    .order("created_at", { ascending: false })
  if (opts?.activeOnly) q = (q as any).is("revoked_at", null)

  const { data, error } = await q
  if (error) throw new Error(`listApiKeys: ${error.message}`)
  return (data ?? []) as unknown as ApiKey[]
}

export async function getApiKey(supabase: SupabaseClient, id: string): Promise<ApiKey> {
  const { data, error } = await supabase
    .from("venom_api_keys")
    .select(KEY_SELECT)
    .eq("id", id)
    .single()
  if (error || !data) throw new Error(`getApiKey: ${error?.message ?? "not found"}`)
  return data as unknown as ApiKey
}
```

- [ ] **Step 4: Run tests to confirm they pass**

```
bun test src/lib/db/api-keys.server.test.ts
```

Expected: all 7 assertions pass, 0 failures.

- [ ] **Step 5: Run the full DB test suite**

```
bun test src/lib/db/
```

Expected: all tests across all 4 domain files pass, 0 failures.

- [ ] **Step 6: Commit**

```bash
git add src/lib/db/api-keys.server.ts src/lib/db/api-keys.server.test.ts
git commit -m "feat(db): add api-keys domain — list and get API keys"
```
