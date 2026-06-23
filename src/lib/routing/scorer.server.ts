import type { RoutingCandidate } from "@/lib/routing/types";
import type { TaskClass } from "@/lib/routing/types";
import type { VenomTier } from "@/lib/routing/strategy.types";
import { TIER_SCORING_WEIGHTS } from "@/lib/routing/strategy.types";
import { getCostType, getQualityScore } from "@/lib/routing/policy.server";

/** Compute what fraction of this account's usage relative to all candidates (0–1). */
function accountOveruseFraction(candidate: RoutingCandidate, all: RoutingCandidate[]): number {
  const totalUsed = all.reduce((sum, c) => sum + (c.account.quota?.used ?? 0), 0);
  if (totalUsed === 0) return 0;
  const thisUsed = candidate.account.quota?.used ?? 0;
  return thisUsed / totalUsed;
}

/** Remaining quota fraction (0–1). Returns 0.5 when quota unknown. */
function quotaRemainingFraction(
  quota: { used: number; total: number | null; confidence: string } | null,
): number {
  if (!quota || quota.total === null || quota.total <= 0) return 0.5;
  return Math.max(0, (quota.total - quota.used) / quota.total);
}

/** Task fit bonus: coding tasks benefit from coding-capable models. */
function taskFitScore(candidate: RoutingCandidate, taskClass: TaskClass): number {
  const caps = candidate.model.capabilities;
  switch (taskClass) {
    case "coding":
      return caps.includes("coding") ? 1 : 0.4;
    case "vision":
      return caps.includes("vision") ? 1 : 0;
    case "tool_calling":
      return caps.includes("tools") ? 1 : 0.3;
    case "long_context":
      return caps.includes("long_context") ? 1 : 0.5;
    case "reasoning_heavy":
      return caps.includes("reasoning") ? 1 : 0.6;
    default:
      return 0.7;
  }
}

/**
 * Multi-factor scorer.
 *
 * score = quality×w.quality + quotaRem×w.quota + (1-overuse)×w.accountBalance
 *       + taskFit×w.taskFit + healthBonus×w.health
 *       - costNorm×w.costPenalty - premiumPenalty×w.premiumPressure
 *       - overuse×w.overuse
 */
export function scoreCandidate(
  candidate: RoutingCandidate,
  tier: VenomTier,
  taskClass: TaskClass,
  allCandidates: RoutingCandidate[],
): number {
  const w = TIER_SCORING_WEIGHTS[tier];

  const qualityScore = candidate.qualityScore ?? getQualityScore(candidate);

  const quota = candidate.account.quota;
  const quotaScore = quotaRemainingFraction(quota);

  const overuse = accountOveruseFraction(candidate, allCandidates);
  const accountBalanceScore = 1 - overuse;

  const tFit = taskFitScore(candidate, taskClass);

  const healthScore = candidate.account.status === "healthy" ? 1 : 0.3;

  const costType = candidate.costType ?? getCostType(candidate);
  const costRank: Record<string, number> = { free: 0, cheap: 0.2, balanced: 0.5, premium: 1 };
  const costNorm = costRank[costType] ?? 0.5;

  const premiumPenalty = costType === "premium" ? 1 : 0;

  return (
    qualityScore * w.quality +
    quotaScore * w.quota +
    accountBalanceScore * w.accountBalance +
    tFit * w.taskFit +
    healthScore * w.health -
    costNorm * w.costPenalty -
    premiumPenalty * w.premiumPressure -
    overuse * w.overuse
  );
}
