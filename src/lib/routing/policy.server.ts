import type { CostType, RoutingCandidate } from "@/lib/routing/types";
import type { VenomTier } from "@/lib/routing/strategy.types";

/**
 * Thresholds (input cost per million tokens) for cost classification.
 * free:     both costs null or 0
 * cheap:    avg ≤ 0.5
 * balanced: avg ≤ 5
 * premium:  avg > 5
 */
const COST_CHEAP_THRESHOLD = 0.5;
const COST_BALANCED_THRESHOLD = 5;

/**
 * Simple average of input and output cost per million tokens.
 * If output cost is missing, falls back to 3× input as an estimate.
 */
function avgCostPerMtok(c: RoutingCandidate): number {
  const input = c.model.inputCostPerMtok;
  const output = c.model.outputCostPerMtok;
  if ((input === null || input === 0) && (output === null || output === 0)) return 0;
  const i = input ?? 0;
  const o = output ?? i * 3;
  return (i + o) / 2;
}

export function getCostType(c: RoutingCandidate): CostType {
  const avg = avgCostPerMtok(c);
  if (avg === 0) return "free";
  if (avg <= COST_CHEAP_THRESHOLD) return "cheap";
  if (avg <= COST_BALANCED_THRESHOLD) return "balanced";
  return "premium";
}

export function getQualityScore(c: RoutingCandidate): number {
  // priority 1 → score near 1.0; priority 10 → score near 0.18
  // 1 / (priority + 1) * 2 capped at 1
  return Math.min(1, 2 / (c.priority + 1));
}

export function isPremium(c: RoutingCandidate): boolean {
  return getCostType(c) === "premium";
}

export function enrichCandidate(c: RoutingCandidate): RoutingCandidate {
  return {
    ...c,
    costType: getCostType(c),
    qualityScore: getQualityScore(c),
  };
}

export interface EscalationStage {
  /** Cost types allowed in this stage. */
  allowedCostTypes: CostType[];
  /** If true, only candidates with qualityScore >= 0.6 pass. Used in Max stages 1 and 4. */
  requireHighQuality?: boolean;
}

const ESCALATION_STAGES: Record<VenomTier, EscalationStage[]> = {
  lite: [
    { allowedCostTypes: ["free"] },
    { allowedCostTypes: ["free", "cheap"] },
    { allowedCostTypes: ["free", "cheap", "balanced"] },
  ],
  pro: [
    { allowedCostTypes: ["free", "cheap"] },
    { allowedCostTypes: ["free", "cheap", "balanced"] },
    { allowedCostTypes: ["free", "cheap", "balanced", "premium"] },
  ],
  max: [
    { allowedCostTypes: ["free"], requireHighQuality: true },
    { allowedCostTypes: ["free", "cheap", "balanced"] },
    { allowedCostTypes: ["free", "cheap", "balanced", "premium"] },
    { allowedCostTypes: ["free", "cheap", "balanced", "premium"], requireHighQuality: true },
  ],
};

export function getEscalationStages(tier: VenomTier): EscalationStage[] {
  return ESCALATION_STAGES[tier];
}
