import { describe, it, expect } from "bun:test";
import {
  getCostType,
  getQualityScore,
  isPremium,
  enrichCandidate,
  getEscalationStages,
} from "./policy.server";
import type { RoutingCandidate } from "@/lib/routing/types";

function makeCandidate(
  overrides: Partial<RoutingCandidate["model"]> = {},
  priority = 5,
): RoutingCandidate {
  return {
    ruleId: "r1",
    priority,
    role: "primary",
    condition: null,
    model: {
      id: "m1",
      externalId: "model-x",
      lifecycle: "approved",
      enabled: true,
      inputCostPerMtok: null,
      outputCostPerMtok: null,
      capabilities: [],
      latencyMs: null,
      provider: { adapter: "test", baseUrl: null },
      ...overrides,
    },
    account: {
      id: "a1",
      status: "healthy",
      credentialsEnc: null,
      credentialsIv: null,
      credentialsTag: null,
      quota: null,
    },
  };
}

describe("getCostType", () => {
  it("returns free when both costs are null", () => {
    expect(getCostType(makeCandidate())).toBe("free");
  });

  it("returns free when both costs are 0", () => {
    expect(getCostType(makeCandidate({ inputCostPerMtok: 0, outputCostPerMtok: 0 }))).toBe("free");
  });

  it("returns cheap when cost is 0.3/mtok", () => {
    expect(getCostType(makeCandidate({ inputCostPerMtok: 0.3, outputCostPerMtok: 0.6 }))).toBe(
      "cheap",
    );
  });

  it("returns balanced when cost is 2/mtok", () => {
    expect(getCostType(makeCandidate({ inputCostPerMtok: 2, outputCostPerMtok: 6 }))).toBe(
      "balanced",
    );
  });

  it("returns premium when cost is 15/mtok", () => {
    expect(getCostType(makeCandidate({ inputCostPerMtok: 15, outputCostPerMtok: 60 }))).toBe(
      "premium",
    );
  });
});

describe("getQualityScore", () => {
  it("returns a value between 0 and 1", () => {
    const score = getQualityScore(makeCandidate({}, 5));
    expect(score).toBeGreaterThan(0);
    expect(score).toBeLessThanOrEqual(1);
  });

  it("lower priority number = higher quality score", () => {
    const high = getQualityScore(makeCandidate({}, 1));
    const low = getQualityScore(makeCandidate({}, 10));
    expect(high).toBeGreaterThan(low);
  });
});

describe("isPremium", () => {
  it("returns true for premium cost type", () => {
    expect(isPremium(makeCandidate({ inputCostPerMtok: 15, outputCostPerMtok: 60 }))).toBe(true);
  });

  it("returns false for free model", () => {
    expect(isPremium(makeCandidate())).toBe(false);
  });
});

describe("enrichCandidate", () => {
  it("adds costType and qualityScore to candidate", () => {
    const c = makeCandidate({ inputCostPerMtok: 0.3, outputCostPerMtok: 0.6 }, 3);
    const enriched = enrichCandidate(c);
    expect(enriched.costType).toBe("cheap");
    expect(typeof enriched.qualityScore).toBe("number");
  });
});

describe("getEscalationStages", () => {
  it("lite has 3 stages and no premium in any stage", () => {
    const stages = getEscalationStages("lite");
    expect(stages).toHaveLength(3);
    for (const stage of stages) {
      expect(stage.allowedCostTypes).not.toContain("premium");
    }
  });

  it("pro has 3 stages; last stage includes premium", () => {
    const stages = getEscalationStages("pro");
    expect(stages).toHaveLength(3);
    expect(stages[2].allowedCostTypes).toContain("premium");
  });

  it("max has 4 stages; last stage is premium with requireHighQuality", () => {
    const stages = getEscalationStages("max");
    expect(stages).toHaveLength(4);
    expect(stages[3].allowedCostTypes).toContain("premium");
    expect(stages[3].requireHighQuality).toBe(true);
  });

  it("each stage expands or equals previous allowed cost types", () => {
    for (const tier of ["lite", "pro", "max"] as const) {
      const stages = getEscalationStages(tier);
      for (let i = 1; i < stages.length; i++) {
        // Each stage's allowed set must include everything from the prior
        const prev = new Set(stages[i - 1].allowedCostTypes);
        for (const ct of prev) {
          expect(stages[i].allowedCostTypes).toContain(ct);
        }
      }
    }
  });
});
