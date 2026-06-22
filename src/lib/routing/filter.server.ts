import type { ChatMessage } from "@/lib/providers/adapters/types";
import type { Modality, RoutingCandidate, RoutingCondition } from "@/lib/routing/types";

/**
 * Detects the modality of a request from its messages.
 * Checks content arrays for image_url (vision), audio (audio), or file/document (documents).
 */
export function detectModality(messages: ChatMessage[]): Modality {
  for (const msg of messages) {
    if (typeof msg.content !== "string") {
      // content is an array of content parts (multimodal)
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

function isQuotaExhausted(
  quota: { used: number; total: number | null; confidence: string } | null,
): boolean {
  if (!quota) return false;
  if (quota.confidence !== "high") return false;
  if (quota.total === null || quota.total <= 0) return false;
  const remaining = quota.total - quota.used;
  return remaining / quota.total < 0.05;
}

function matchesCondition(
  condition: RoutingCondition | null,
  capabilities: string[],
  modality: Modality,
): boolean {
  if (!condition) return true;

  if (condition.requires?.length) {
    for (const cap of condition.requires) {
      if (!capabilities.includes(cap)) return false;
    }
  }

  return true;
}

/**
 * Filters candidates by: lifecycle, enabled, account health, quota, modality, condition.
 * Returns only eligible candidates.
 */
export function filterCandidates(
  candidates: RoutingCandidate[],
  modality: Modality,
): RoutingCandidate[] {
  return candidates.filter((c) => {
    if (c.model.lifecycle !== "approved") return false;
    if (!c.model.enabled) return false;
    if (c.account.status !== "healthy") return false;
    if (isQuotaExhausted(c.account.quota)) return false;

    // Modality capability check
    if (modality !== "text") {
      const caps = c.model.capabilities;
      if (!caps.includes(modality)) return false;
    }

    if (!matchesCondition(c.condition, c.model.capabilities, modality)) return false;

    return true;
  });
}
