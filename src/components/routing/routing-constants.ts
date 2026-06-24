import type { VenomTier } from "@/lib/routing/strategy.types";

export const TIERS = ["lite", "pro", "max"] as const;
export type Tier = (typeof TIERS)[number];

export const TIER_META: Record<
  Tier,
  { label: string; subtitle: string; color: string; accent: string; gradient: string }
> = {
  lite: {
    label: "venom/lite",
    subtitle: "Cost-first · free-heavy · premium-protected",
    color: "text-sky-400",
    accent: "border-sky-500/20 bg-sky-500/5",
    gradient: "from-sky-500/10 via-sky-500/5 to-transparent",
  },
  pro: {
    label: "venom/pro",
    subtitle: "Balanced quality · quota-smart · controlled premium usage",
    color: "text-violet-400",
    accent: "border-violet-500/20 bg-violet-500/5",
    gradient: "from-violet-500/10 via-violet-500/5 to-transparent",
  },
  max: {
    label: "venom/max",
    subtitle: "Quality-first · quota-aware · premium reserve",
    color: "text-amber-400",
    accent: "border-amber-500/20 bg-amber-500/5",
    gradient: "from-amber-500/10 via-amber-500/5 to-transparent",
  },
};

export const TIER_STRATEGY_COPY: Record<Tier, { title: string; bullets: string[] }> = {
  lite: {
    title: "Cost-first universal router",
    bullets: [
      "Prefer free healthy certified providers.",
      "Prefer accounts with high remaining quota.",
      "Use cheap paid providers only when free providers fail or cannot satisfy the required capability.",
      "Avoid premium expensive models except emergency fallback.",
      "Optimize for cost, speed, and quota preservation.",
    ],
  },
  pro: {
    title: "Balanced professional universal router",
    bullets: [
      "Use strong free certified models first when quality is enough.",
      "Use balanced paid providers for coding, agents, vision, tools, and long-context tasks.",
      "Use premium providers only when task complexity or previous failures justify it.",
      "Rotate across provider accounts based on health, remaining quota, and current pressure.",
      "Optimize for professional quality without burning premium quota.",
    ],
  },
  max: {
    title: "Quality-first protected universal router",
    bullets: [
      "Do not blindly use the most expensive provider for every request.",
      "Start with strong free or balanced paid models if they can satisfy max-quality requirements.",
      "Escalate to premium providers only for complex reasoning, hard coding, vision reasoning, long context, tool reliability, or failed lower routes.",
      "Keep premium reserve for critical tasks.",
      "Optimize for highest confidence while preserving scarce premium quota.",
    ],
  },
};

export const UNIVERSAL_CAPABILITIES = [
  "chat",
  "streaming",
  "vision",
  "code",
  "reasoning",
  "tools",
  "agents",
  "long_context",
] as const;

export const CAPABILITY_FILTER_OPTIONS = [
  "chat",
  "streaming",
  "vision",
  "code",
  "reasoning",
  "tools",
] as const;

export const CAPABILITY_LABELS: Record<string, string> = {
  chat: "Chat",
  streaming: "Streaming",
  vision: "Vision",
  code: "Code",
  reasoning: "Reasoning",
  tools: "Tools",
  agents: "Agents",
  long_context: "Long context",
};

export const ROLE_LABELS: Record<string, string> = {
  primary: "Primary",
  fallback: "Fallback",
};

export const AUTO_ESCALATION_OPTIONS = [
  { value: "off", label: "Off" },
  { value: "on_failure", label: "On failure" },
  { value: "on_quota", label: "On quota pressure" },
  { value: "on_complexity", label: "On task complexity" },
] as const;

export const ACCOUNT_ROTATION_OPTIONS = [
  { value: "off", label: "Off" },
  { value: "round_robin", label: "Round robin" },
  { value: "quota_weighted", label: "Quota-weighted" },
  { value: "health_weighted", label: "Health-weighted" },
] as const;

export const HEALTH_REQUIREMENT_OPTIONS = [
  { value: "healthy_only", label: "Healthy accounts only" },
  { value: "allow_degraded", label: "Allow degraded accounts" },
] as const;

export const FALLBACK_BEHAVIOR_OPTIONS = [
  { value: "sequential", label: "Sequential fallback chain" },
  { value: "skip_exhausted", label: "Skip exhausted accounts" },
  { value: "premium_last", label: "Premium last" },
] as const;

export const EMPTY_TIER_MESSAGE =
  "No routing rules yet. Add at least one rule so the gateway can route requests for this tier. Without rules, this tier returns 503.";

export const GLOBAL_EMPTY_MESSAGE =
  "No routing rules configured yet. Add at least one rule per tier you want to serve. Without rules the gateway returns 503 for every request to that tier.";

export type ApprovedModel = {
  id: string;
  account_id: string;
  model_id: string;
  model_external_id: string;
  model_display_name: string;
  provider_slug: string;
  provider_name: string;
  account_email: string | null;
  account_label: string | null;
};

export function modelKey(m: Pick<ApprovedModel, "account_id" | "model_id">): string {
  return `${m.account_id}::${m.model_id}`;
}

export function tierFromSlug(slug: VenomTier): Tier {
  return slug;
}

export function routingErrorMessage(code: string, tier?: Tier): string {
  switch (code) {
    case "NO_ROUTING_RULES":
      return tier
        ? `No active routing rules for ${TIER_META[tier].label}. Add rules on the Routing page.`
        : EMPTY_TIER_MESSAGE;
    case "NO_ELIGIBLE_CANDIDATES":
      return "Rules exist but none passed filters (disabled model, quota, capabilities). Check the routing trace for details.";
    case "VENOM_MODEL_NOT_FOUND":
      return "Unknown venom tier.";
    default:
      return code || "Routing failed";
  }
}
