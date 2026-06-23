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
          { slug: "lite", weight_cost: 0.5, weight_speed: 0.3, weight_quality: 0.2, max_fallback_attempts: 2, timeout_ms: 15000 },
          { slug: "pro", weight_cost: 0.3, weight_speed: 0.3, weight_quality: 0.4, max_fallback_attempts: 3, timeout_ms: 30000 },
          { slug: "max", weight_cost: 0.2, weight_speed: 0.2, weight_quality: 0.6, max_fallback_attempts: 5, timeout_ms: 60000 },
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
