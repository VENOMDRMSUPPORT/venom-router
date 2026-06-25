import { describe, it, expect } from "vitest";
import { scoreCandidate } from "./scorer.server";
import type { RoutingCandidate } from "@/lib/routing/types";

function makeCandidate(overrides: {
  priority?: number;
  inputCostPerMtok?: number | null;
  outputCostPerMtok?: number | null;
  accountStatus?: string;
  quota?: { used: number; total: number | null; confidence: string } | null;
  costType?: import("@/lib/routing/types").CostType;
  qualityScore?: number;
  latencyMs?: number | null;
}): RoutingCandidate {
  return {
    ruleId: "r1",
    priority: overrides.priority ?? 5,
    role: "primary",
    condition: null,
    model: {
      id: "m1",
      externalId: "model-x",
      lifecycle: "approved",
      enabled: true,
      inputCostPerMtok: overrides.inputCostPerMtok ?? null,
      outputCostPerMtok: overrides.outputCostPerMtok ?? null,
      capabilities: [],
      latencyMs: overrides.latencyMs ?? null,
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
    qualityScore: overrides.qualityScore,
  };
}

describe("scoreCandidate", () => {
  it("returns a positive number", () => {
    const c = makeCandidate({});
    const score = scoreCandidate(c, "pro", "simple_chat", [c]);
    expect(score).toBeGreaterThan(0);
  });

  it("lite scores free model higher than premium model", () => {
    const free = makeCandidate({ costType: "free", inputCostPerMtok: 0 });
    const premium = makeCandidate({
      costType: "premium",
      inputCostPerMtok: 15,
      outputCostPerMtok: 60,
    });
    const all = [free, premium];
    expect(scoreCandidate(free, "lite", "simple_chat", all)).toBeGreaterThan(
      scoreCandidate(premium, "lite", "simple_chat", all),
    );
  });

  it("max scores high-quality model higher than low-quality for complex task", () => {
    const highQ = makeCandidate({
      priority: 1,
      qualityScore: 0.9,
      costType: "premium",
      inputCostPerMtok: 15,
    });
    const lowQ = makeCandidate({ priority: 10, qualityScore: 0.1, costType: "free" });
    const all = [highQ, lowQ];
    expect(scoreCandidate(highQ, "max", "reasoning_heavy", all)).toBeGreaterThan(
      scoreCandidate(lowQ, "max", "reasoning_heavy", all),
    );
  });

  it("premium model is penalized more for lite than max", () => {
    const premium = makeCandidate({
      costType: "premium",
      inputCostPerMtok: 15,
      outputCostPerMtok: 60,
      qualityScore: 0.9,
    });
    const litePenalty = scoreCandidate(premium, "lite", "simple_chat", [premium]);
    const maxPenalty = scoreCandidate(premium, "max", "simple_chat", [premium]);
    expect(maxPenalty).toBeGreaterThan(litePenalty);
  });

  it("candidate with more remaining quota scores higher", () => {
    const highQuota = makeCandidate({ quota: { used: 10, total: 100, confidence: "high" } });
    const lowQuota = makeCandidate({ quota: { used: 90, total: 100, confidence: "high" } });
    const all = [highQuota, lowQuota];
    expect(scoreCandidate(highQuota, "pro", "coding", all)).toBeGreaterThan(
      scoreCandidate(lowQuota, "pro", "coding", all),
    );
  });
});
