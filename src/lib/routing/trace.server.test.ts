import { describe, it, expect, vi, beforeEach } from "vitest";

const usageInsert = vi.fn<(row: unknown) => Promise<{ data: { id: string }; error: null }>>((row) =>
  Promise.resolve({ data: { id: "usage-1" }, error: null }),
);
const traceInsert = vi.fn<
  (payload: Record<string, unknown>) => Promise<{ data: null; error: null }>
>((payload) => Promise.resolve({ data: null, error: null }));

vi.mock("@/integrations/supabase/client.server", () => ({
  supabaseAdmin: {
    from(table: string) {
      if (table === "usage_records") {
        return {
          insert: () => ({
            select: () => ({
              single: usageInsert,
            }),
          }),
        };
      }
      if (table === "routing_traces") {
        return {
          insert: traceInsert,
        };
      }
      throw new Error(`unexpected table: ${table}`);
    },
  },
}));

const { persistUsageAndTrace } = await import("./trace.server");

describe("persistUsageAndTrace", () => {
  beforeEach(() => {
    usageInsert.mockClear();
    traceInsert.mockClear();
  });

  it("inserts usage and trace with required NOT NULL fields", async () => {
    await persistUsageAndTrace({
      venomSlug: "pro",
      ruleId: "rule-1",
      accountId: "acct-1",
      modelId: "model-1",
      apiKeyId: undefined,
      inputTokens: 10,
      outputTokens: 20,
      costUsd: 0.001,
      latencyMs: 500,
      success: true,
      fallbackUsed: false,
      fallbackCount: 0,
      candidatesEvaluated: 3,
      candidatesFiltered: 1,
      selectedRuleId: "rule-1",
      decisionReason: "Primary: selected rule rule-1",
      modality: "text",
      requestId: "req-uuid-123",
      candidates: [
        {
          rule_id: "rule-1",
          external_id: "gpt-4",
          adapter: "opencode-zen",
          priority: 1,
          role: "primary",
          score: 0.85,
          status: "selected",
        },
      ],
      fallbackChain: [],
    });

    expect(usageInsert).toHaveBeenCalled();
    expect(traceInsert).toHaveBeenCalled();

    const tracePayload = traceInsert.mock.calls[0]![0];
    expect(tracePayload.request_id).toBe("req-uuid-123");
    expect(tracePayload.venom_slug).toBe("pro");
    expect(tracePayload.success).toBe(true);
    expect(tracePayload.reason).toBe("Primary: selected rule rule-1");
    expect(Array.isArray(tracePayload.candidates)).toBe(true);
    expect(Array.isArray(tracePayload.fallback_chain)).toBe(true);
    expect(tracePayload.candidates_evaluated).toBe(3);
    expect(tracePayload.candidates_filtered).toBe(1);
  });
});
