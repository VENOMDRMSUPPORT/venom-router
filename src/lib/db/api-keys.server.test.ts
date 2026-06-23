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
