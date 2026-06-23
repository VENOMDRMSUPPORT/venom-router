import { describe, it, expect } from "bun:test";
import {
  isQuotaExhausted,
  getFilterReason,
  filterCandidatesWithDiagnostics,
} from "./filter.server";
import type { RoutingCandidate } from "@/lib/routing/types";
import { TIER_STRATEGY_PRESETS } from "@/lib/routing/strategy.types";

function makeCandidate(overrides: {
  lifecycle?: string;
  enabled?: boolean;
  accountStatus?: string;
  quota?: { used: number; total: number | null; confidence: string } | null;
  capabilities?: string[];
  costType?: import("@/lib/routing/types").CostType;
  priority?: number;
}): RoutingCandidate {
  return {
    ruleId: "r1",
    priority: overrides.priority ?? 1,
    role: "primary",
    condition: null,
    model: {
      id: "m1",
      externalId: "model-x",
      lifecycle: overrides.lifecycle ?? "approved",
      enabled: overrides.enabled ?? true,
      inputCostPerMtok: null,
      outputCostPerMtok: null,
      capabilities: overrides.capabilities ?? [],
      latencyMs: null,
      provider: { adapter: "test", baseUrl: null },
    },
    account: {
      id: "a1",
      status: overrides.accountStatus ?? "healthy",
      credentialsEnc: null,
      credentialsIv: null,
      credentialsTag: null,
      quota: overrides.quota !== undefined ? overrides.quota : null,
    },
    costType: overrides.costType,
  };
}

describe("isQuotaExhausted", () => {
  it("returns false when quota is null", () => {
    expect(isQuotaExhausted(null, 15)).toBe(false);
  });

  it("returns false when confidence is not high", () => {
    expect(isQuotaExhausted({ used: 95, total: 100, confidence: "low" }, 15)).toBe(false);
  });

  it("returns true when remaining < threshold", () => {
    // remaining = 5/100 = 5%, threshold = 15% → exhausted
    expect(isQuotaExhausted({ used: 95, total: 100, confidence: "high" }, 15)).toBe(true);
  });

  it("returns false when remaining >= threshold", () => {
    // remaining = 20/100 = 20%, threshold = 15% → not exhausted
    expect(isQuotaExhausted({ used: 80, total: 100, confidence: "high" }, 15)).toBe(false);
  });

  it("returns false when total is null", () => {
    expect(isQuotaExhausted({ used: 95, total: null, confidence: "high" }, 15)).toBe(false);
  });
});

describe("getFilterReason with strategy", () => {
  const liteStrategy = TIER_STRATEGY_PRESETS.lite;
  const maxStrategy = TIER_STRATEGY_PRESETS.max;

  it("returns lifecycle_not_approved for unapproved model", () => {
    expect(getFilterReason(makeCandidate({ lifecycle: "discovered" }), "text", liteStrategy)).toBe(
      "lifecycle_not_approved",
    );
  });

  it("returns model_disabled when disabled", () => {
    expect(getFilterReason(makeCandidate({ enabled: false }), "text", liteStrategy)).toBe(
      "model_disabled",
    );
  });

  it("returns account_unhealthy for unhealthy account", () => {
    expect(
      getFilterReason(makeCandidate({ accountStatus: "suspended" }), "text", liteStrategy),
    ).toBe("account_unhealthy");
  });

  it("returns quota_exhausted using lite threshold (15%)", () => {
    // 92% used = 8% remaining, below 15% threshold
    const quota = { used: 92, total: 100, confidence: "high" };
    expect(getFilterReason(makeCandidate({ quota }), "text", liteStrategy)).toBe("quota_exhausted");
  });

  it("does NOT exhaust at 8% remaining for max threshold (5%)", () => {
    // 92% used = 8% remaining, above 5% threshold
    const quota = { used: 92, total: 100, confidence: "high" };
    expect(getFilterReason(makeCandidate({ quota }), "text", maxStrategy)).toBeNull();
  });

  it("returns premium_reserved when premium model within reserve", () => {
    // lite reserve = 5%, so 3% remaining triggers reserve
    const quota = { used: 97, total: 100, confidence: "high" };
    const c = makeCandidate({ quota, costType: "premium" });
    expect(getFilterReason(c, "text", liteStrategy)).toBe("premium_reserved");
  });

  it("does not reserve premium when outside reserve window", () => {
    // lite reserve = 5%, 10% remaining is outside reserve
    const quota = { used: 90, total: 100, confidence: "high" };
    const c = makeCandidate({ quota, costType: "premium" });
    // Should be null (not filtered) since 10% > 5% reserve AND 10% > 15% threshold?
    // Actually 10% < 15% lite threshold, so it's quota_exhausted. Use a different quota.
    const quota2 = { used: 70, total: 100, confidence: "high" };
    const c2 = makeCandidate({ quota: quota2, costType: "premium" });
    expect(getFilterReason(c2, "text", liteStrategy)).toBeNull();
  });

  it("returns missing_capability for vision request without vision capability", () => {
    expect(getFilterReason(makeCandidate({ capabilities: [] }), "vision", liteStrategy)).toBe(
      "missing_capability:vision",
    );
  });

  it("passes candidate with vision capability for vision request", () => {
    expect(
      getFilterReason(makeCandidate({ capabilities: ["vision"] }), "vision", liteStrategy),
    ).toBeNull();
  });
});

describe("filterCandidatesWithDiagnostics", () => {
  it("separates eligible from rejected", () => {
    const strategy = TIER_STRATEGY_PRESETS.pro;
    const eligible = makeCandidate({});
    const rejected = makeCandidate({ lifecycle: "discovered" });
    const { eligible: e, rejected: r } = filterCandidatesWithDiagnostics(
      [eligible, rejected],
      "text",
      strategy,
    );
    expect(e).toHaveLength(1);
    expect(r).toHaveLength(1);
    expect(r[0].reason).toBe("lifecycle_not_approved");
  });
});
