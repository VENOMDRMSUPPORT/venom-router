import { describe, it, expect } from "bun:test";
import { listUsageRecords, getMetricsSummary, getTraffic7d } from "./usage.server";

function makeSupabaseMock(
  tableResponses: Record<string, { data: unknown; error: null | { message: string } }>,
) {
  return {
    from(table: string) {
      const resp = tableResponses[table] ?? {
        data: null,
        error: { message: `no mock for table: ${table}` },
      };
      const chain: any = {};
      for (const m of ["select", "eq", "in", "gte", "is", "neq", "limit", "order"]) {
        chain[m] = () => chain;
      }
      chain.single = () => Promise.resolve(resp);
      chain.maybeSingle = () => Promise.resolve(resp);
      chain.then = (resolve: (v: any) => any, reject?: (e: any) => any) =>
        Promise.resolve(resp).then(resolve, reject);
      return chain;
    },
  } as any;
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
    });
    const result = await listUsageRecords(supabase);
    expect(result).toHaveLength(1);
    expect(result[0]!.venom_slug).toBe("pro");
    expect(result[0]!.success).toBe(true);
    expect(result[0]!.fallback_used).toBe(false);
  });

  it("returns empty array when no records", async () => {
    const supabase = makeSupabaseMock({ usage_records: { data: [], error: null } });
    expect(await listUsageRecords(supabase)).toEqual([]);
  });

  it("throws on DB error", async () => {
    const supabase = makeSupabaseMock({
      usage_records: { data: null, error: { message: "connection refused" } },
    });
    await expect(listUsageRecords(supabase)).rejects.toThrow(
      "listUsageRecords: connection refused",
    );
  });
});

describe("getMetricsSummary", () => {
  it("computes correct totals and rates", async () => {
    const supabase = makeSupabaseMock({
      usage_records: {
        data: [
          {
            success: true,
            fallback_used: false,
            cost_usd: 0.01,
            input_tokens: 100,
            output_tokens: 50,
          },
          {
            success: true,
            fallback_used: true,
            cost_usd: 0.02,
            input_tokens: 200,
            output_tokens: 100,
          },
          {
            success: false,
            fallback_used: false,
            cost_usd: null,
            input_tokens: null,
            output_tokens: null,
          },
        ],
        error: null,
      },
    });
    const result = await getMetricsSummary(supabase);
    expect(result.total_requests).toBe(3);
    expect(result.total_tokens).toBe(450);
    expect(result.total_cost_usd).toBeCloseTo(0.03);
    expect(result.success_rate).toBeCloseTo(2 / 3);
    expect(result.fallback_rate).toBeCloseTo(1 / 3);
  });

  it("returns zero values when no records", async () => {
    const supabase = makeSupabaseMock({ usage_records: { data: [], error: null } });
    const result = await getMetricsSummary(supabase);
    expect(result.total_requests).toBe(0);
    expect(result.total_tokens).toBe(0);
    expect(result.total_cost_usd).toBe(0);
    expect(result.success_rate).toBe(0);
    expect(result.fallback_rate).toBe(0);
  });
});

describe("getTraffic7d", () => {
  it("returns exactly 7 day buckets", async () => {
    const supabase = makeSupabaseMock({
      usage_records: { data: [], error: null },
    });
    const result = await getTraffic7d(supabase);
    expect(result).toHaveLength(7);
    expect(
      result.every((r) => ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"].includes(r.day)),
    ).toBe(true);
    expect(result.every((r) => typeof r.requests === "number")).toBe(true);
  });

  it("counts requests into the correct day buckets", async () => {
    const today = new Date();
    today.setHours(12, 0, 0, 0);
    const supabase = makeSupabaseMock({
      usage_records: {
        data: [{ created_at: today.toISOString() }, { created_at: today.toISOString() }],
        error: null,
      },
    });
    const result = await getTraffic7d(supabase);
    const total = result.reduce((s, r) => s + r.requests, 0);
    expect(total).toBe(2);
  });
});
