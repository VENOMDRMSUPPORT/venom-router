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
