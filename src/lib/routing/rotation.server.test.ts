import { describe, it, expect } from "vitest";
import { applyAccountRotation } from "./rotation.server";
import type { ScoredCandidate } from "@/lib/routing/types";

function makeScoredCandidate(
  accountId: string,
  score: number,
  used = 0,
  total: number | null = 100,
): ScoredCandidate {
  return {
    score,
    candidate: {
      ruleId: `r-${accountId}-${score}`,
      priority: 1,
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
      },
      account: {
        id: accountId,
        status: "healthy",
        credentialsEnc: null,
        credentialsIv: null,
        credentialsTag: null,
        quota: total !== null ? { used, total, confidence: "high" } : null,
      },
    },
  };
}

describe("applyAccountRotation", () => {
  it("off: returns candidates in original order", () => {
    const a = makeScoredCandidate("a1", 0.9);
    const b = makeScoredCandidate("a2", 0.8);
    const c = makeScoredCandidate("a1", 0.7);
    const result = applyAccountRotation([a, b, c], "off");
    expect(result.map((r) => r.candidate.ruleId)).toEqual([a, b, c].map((x) => x.candidate.ruleId));
  });

  it("quota_weighted: interleaves accounts by remaining quota", () => {
    // a1 low quota (80 used/100), a2 high quota (20 used/100)
    const a1_1 = makeScoredCandidate("a1", 0.9, 80);
    const a1_2 = makeScoredCandidate("a1", 0.8, 80);
    const a2_1 = makeScoredCandidate("a2", 0.7, 20);
    // After rotation: a2 (more quota) should appear first for its slot
    const result = applyAccountRotation([a1_1, a1_2, a2_1], "quota_weighted");
    expect(result).toHaveLength(3);
    // The first candidate should be from a2 (higher remaining quota)
    expect(result[0].candidate.account.id).toBe("a2");
  });

  it("round_robin: interleaves candidates from different accounts", () => {
    const a1 = makeScoredCandidate("a1", 0.9);
    const a2 = makeScoredCandidate("a2", 0.8);
    const a3 = makeScoredCandidate("a1", 0.7);
    const result = applyAccountRotation([a1, a2, a3], "round_robin");
    expect(result).toHaveLength(3);
    // First two should be from different accounts
    expect(result[0].candidate.account.id).not.toBe(result[1].candidate.account.id);
  });

  it("health_weighted: healthy accounts preferred", () => {
    const healthy = makeScoredCandidate("a1", 0.5);
    const degraded = { ...makeScoredCandidate("a2", 0.9), score: 0.9 };
    degraded.candidate = {
      ...degraded.candidate,
      account: { ...degraded.candidate.account, status: "degraded" },
    };
    const result = applyAccountRotation([degraded, healthy], "health_weighted");
    expect(result[0].candidate.account.id).toBe("a1");
  });
});
