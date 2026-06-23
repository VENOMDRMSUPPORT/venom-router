import type { RoutingCondition } from "@/lib/routing/types";

export type AutoEscalation = "off" | "on_failure" | "on_quota" | "on_complexity";

export type AccountRotation = "off" | "round_robin" | "quota_weighted" | "health_weighted";

export type HealthRequirement = "healthy_only" | "allow_degraded";

export type FallbackBehavior = "sequential" | "skip_exhausted" | "premium_last";

export type TierStrategyConfig = {
  quota_threshold_pct: number;
  premium_reserve_pct: number;
  auto_escalation: AutoEscalation;
  account_rotation: AccountRotation;
  health_requirement: HealthRequirement;
  fallback_behavior: FallbackBehavior;
};

export type VenomTier = "lite" | "pro" | "max";

export const TIER_STRATEGY_PRESETS: Record<VenomTier, TierStrategyConfig> = {
  lite: {
    quota_threshold_pct: 15,
    premium_reserve_pct: 5,
    auto_escalation: "on_failure",
    account_rotation: "quota_weighted",
    health_requirement: "healthy_only",
    fallback_behavior: "premium_last",
  },
  pro: {
    quota_threshold_pct: 10,
    premium_reserve_pct: 15,
    auto_escalation: "on_complexity",
    account_rotation: "health_weighted",
    health_requirement: "healthy_only",
    fallback_behavior: "sequential",
  },
  max: {
    quota_threshold_pct: 5,
    premium_reserve_pct: 25,
    auto_escalation: "on_failure",
    account_rotation: "quota_weighted",
    health_requirement: "healthy_only",
    fallback_behavior: "premium_last",
  },
};

export function mergeStrategyConfig(
  tier: VenomTier,
  partial: Partial<TierStrategyConfig> | null | undefined,
): TierStrategyConfig {
  return { ...TIER_STRATEGY_PRESETS[tier], ...(partial ?? {}) };
}

/** Per-factor scoring weights for each tier. All values 0–1. */
export type TierScoringWeights = {
  quality: number;
  quota: number;
  accountBalance: number;
  taskFit: number;
  health: number;
  costPenalty: number;
  premiumPressure: number;
  overuse: number;
};

export const TIER_SCORING_WEIGHTS: Record<VenomTier, TierScoringWeights> = {
  lite: {
    quality: 0.3,
    quota: 0.6,
    accountBalance: 0.5,
    taskFit: 0.3,
    health: 0.5,
    costPenalty: 0.8,
    premiumPressure: 0.9,
    overuse: 0.6,
  },
  pro: {
    quality: 0.6,
    quota: 0.5,
    accountBalance: 0.6,
    taskFit: 0.5,
    health: 0.6,
    costPenalty: 0.4,
    premiumPressure: 0.6,
    overuse: 0.4,
  },
  max: {
    quality: 0.9,
    quota: 0.4,
    accountBalance: 0.4,
    taskFit: 0.8,
    health: 0.5,
    costPenalty: 0.1,
    premiumPressure: 0.3,
    overuse: 0.3,
  },
};

export type { RoutingCondition };
