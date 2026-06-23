import type { ChatMessage } from "@/lib/providers/adapters/types";
import type { Modality, RoutingCandidate, RoutingCondition } from "@/lib/routing/types";
import type { TierStrategyConfig } from "@/lib/routing/strategy.types";
import { getCostType } from "@/lib/routing/policy.server";

/**
 * Detects the modality of a request from its messages.
 */
export function detectModality(messages: ChatMessage[]): Modality {
  for (const msg of messages) {
    if (typeof msg.content !== "string") {
      const parts = msg.content as unknown as Array<{ type: string }>;
      if (!Array.isArray(parts)) continue;
      for (const part of parts) {
        if (part.type === "image_url" || part.type === "image") return "vision";
        if (part.type === "audio") return "audio";
        if (part.type === "file" || part.type === "document") return "documents";
      }
    }
  }
  return "text";
}

export function isQuotaExhausted(
  quota: { used: number; total: number | null; confidence: string } | null,
  thresholdPct: number,
): boolean {
  if (!quota) return false;
  if (quota.confidence !== "high") return false;
  if (quota.total === null || quota.total <= 0) return false;
  const remainingPct = ((quota.total - quota.used) / quota.total) * 100;
  return remainingPct < thresholdPct;
}

function isPremiumReserved(
  candidate: RoutingCandidate,
  quota: { used: number; total: number | null; confidence: string } | null,
  reservePct: number,
): boolean {
  const costType = candidate.costType ?? getCostType(candidate);
  if (costType !== "premium") return false;
  if (!quota || quota.confidence !== "high") return false;
  if (quota.total === null || quota.total <= 0) return false;
  const remainingPct = ((quota.total - quota.used) / quota.total) * 100;
  return remainingPct < reservePct;
}

function matchesCondition(condition: RoutingCondition | null, capabilities: string[]): boolean {
  if (!condition) return true;
  if (condition.requires?.length) {
    for (const cap of condition.requires) {
      if (!capabilities.includes(cap)) return false;
    }
  }
  return true;
}

export function getFilterReason(
  candidate: RoutingCandidate,
  modality: Modality,
  strategy: TierStrategyConfig,
): string | null {
  if (candidate.model.lifecycle !== "approved") return "lifecycle_not_approved";
  if (!candidate.model.enabled) return "model_disabled";
  if (candidate.account.status !== "healthy") return "account_unhealthy";

  const quota = candidate.account.quota;

  if (isPremiumReserved(candidate, quota, strategy.premium_reserve_pct)) return "premium_reserved";

  if (isQuotaExhausted(quota, strategy.quota_threshold_pct)) return "quota_exhausted";

  if (modality !== "text") {
    const caps = candidate.model.capabilities;
    if (!caps.includes(modality)) return `missing_capability:${modality}`;
  }

  if (!matchesCondition(candidate.condition, candidate.model.capabilities)) {
    const required = candidate.condition?.requires?.join(",") ?? "unknown";
    return `condition_requires:${required}`;
  }

  return null;
}

export interface FilterDiagnostics {
  eligible: RoutingCandidate[];
  rejected: Array<{ candidate: RoutingCandidate; reason: string }>;
}

export function filterCandidatesWithDiagnostics(
  candidates: RoutingCandidate[],
  modality: Modality,
  strategy: TierStrategyConfig,
): FilterDiagnostics {
  const eligible: RoutingCandidate[] = [];
  const rejected: Array<{ candidate: RoutingCandidate; reason: string }> = [];

  for (const c of candidates) {
    const reason = getFilterReason(c, modality, strategy);
    if (reason) {
      rejected.push({ candidate: c, reason });
    } else {
      eligible.push(c);
    }
  }

  return { eligible, rejected };
}

export function filterCandidates(
  candidates: RoutingCandidate[],
  modality: Modality,
  strategy: TierStrategyConfig,
): RoutingCandidate[] {
  return filterCandidatesWithDiagnostics(candidates, modality, strategy).eligible;
}
