import { describe, it, expect, vi, beforeEach } from "vitest";

// Usage rows returned by the mocked supabaseAdmin query in each test.
let usageRows: Array<{
  input_tokens: number;
  output_tokens: number;
  cost_usd: number | string;
  created_at: string;
}> = [];

vi.mock("@/integrations/supabase/client.server", () => ({
  supabaseAdmin: {
    from(table: string) {
      if (table !== "usage_records") throw new Error(`unexpected table: ${table}`);
      const api = {
        select() {
          return api;
        },
        eq() {
          return api;
        },
        gte() {
          return api;
        },
        then(resolve: (v: unknown) => void) {
          resolve({ data: usageRows, error: null });
        },
      };
      return api;
    },
  },
}));

const { checkKeyLimits } = await import("./api-key-auth.server");
type ValidatedApiKey = Parameters<typeof checkKeyLimits>[0];

function nowIso(): string {
  return new Date().toISOString();
}

describe("checkKeyLimits", () => {
  beforeEach(() => {
    usageRows = [];
  });

  it("passes when no limits are configured", async () => {
    const result = await checkKeyLimits({
      id: "k1",
      allowedModels: [],
      rpmLimit: null,
      tpdLimit: null,
      monthlyCupUsd: null,
    } as ValidatedApiKey);
    expect(result).toEqual({ ok: true });
  });

  it("rejects when tokens-per-day exceeds the cap", async () => {
    usageRows = [
      { input_tokens: 4000, output_tokens: 1500, cost_usd: 0.01, created_at: nowIso() },
      { input_tokens: 4000, output_tokens: 1000, cost_usd: 0.02, created_at: nowIso() },
    ];
    const result = await checkKeyLimits({
      id: "k1",
      allowedModels: [],
      rpmLimit: null,
      tpdLimit: 10_000, // total used = 10500
      monthlyCupUsd: null,
    } as ValidatedApiKey);
    expect(result.ok).toBe(false);
    if (!result.ok) expect(result.errorCode).toBe("TPD_EXCEEDED");
  });

  it("rejects when monthly spend exceeds the cap", async () => {
    usageRows = [
      { input_tokens: 100, output_tokens: 100, cost_usd: 5, created_at: nowIso() },
      { input_tokens: 100, output_tokens: 100, cost_usd: 6, created_at: nowIso() },
    ];
    const result = await checkKeyLimits({
      id: "k1",
      allowedModels: [],
      rpmLimit: null,
      tpdLimit: null,
      monthlyCupUsd: 10, // spent = 11
    } as ValidatedApiKey);
    expect(result.ok).toBe(false);
    if (!result.ok) expect(result.errorCode).toBe("MONTHLY_CAP_EXCEEDED");
  });

  it("passes when usage is within both caps", async () => {
    usageRows = [{ input_tokens: 1000, output_tokens: 500, cost_usd: 0.5, created_at: nowIso() }];
    const result = await checkKeyLimits({
      id: "k1",
      allowedModels: [],
      rpmLimit: null,
      tpdLimit: 10_000,
      monthlyCupUsd: 10,
    } as ValidatedApiKey);
    expect(result).toEqual({ ok: true });
  });
});
