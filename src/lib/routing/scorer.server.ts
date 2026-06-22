import type { RoutingCandidate, VenomWeights } from "@/lib/routing/types";

/**
 * Scores a routing candidate.
 *
 * score = roleBonus×10 + costWeight×costScore + speedWeight×speedScore + qualityWeight×priorityScore
 *
 * roleBonus     = 1 if role="primary" else 0
 * costScore     = 1 / (avgCost×1000 + 1)  where avgCost = (input + output×3) / 4
 * speedScore    = 1000 / latencyMs  (default 0.5 if no data)
 * priorityScore = 1 / (priority + 1)
 */
export function scoreCandidate(
  candidate: RoutingCandidate,
  weights: VenomWeights,
): number {
  const roleBonus = candidate.role === "primary" ? 1 : 0;
  const priorityScore = 1 / (candidate.priority + 1);

  const inputCost = candidate.model.inputCostPerMtok ?? 0.001;
  const outputCost = candidate.model.outputCostPerMtok ?? inputCost * 3;
  const avgCost = (inputCost + outputCost * 3) / 4;
  const costScore = avgCost > 0 ? 1 / (avgCost * 1000 + 1) : 0.5;

  const latency = candidate.model.latencyMs;
  const speedScore =
    typeof latency === "number" && latency > 0 ? 1000 / latency : 0.5;

  return (
    roleBonus * 10 +
    weights.costWeight * costScore +
    weights.speedWeight * speedScore +
    weights.qualityWeight * priorityScore
  );
}
