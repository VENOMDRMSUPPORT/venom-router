/* Antigravity plan resolution — aligned with venom-router/lib/adapters/antigravity/onboarding.ts */

export interface LoadCodeAssistResponse {
  cloudaicompanionProject?: string | null;
  currentTier?: {
    id?: string;
    name?: string;
    description?: string;
    upgradeSubscriptionText?: string;
  };
  paidTier?: { id?: string; name?: string; description?: string } | null;
  allowedTiers?: Array<{
    id?: string;
    name?: string;
    description?: string;
    isDefault?: boolean;
  }>;
  availablePromptCredits?: number;
  planInfo?: unknown;
}

const TIER_LABEL_MAP: Array<{ pattern: RegExp; label: string }> = [
  { pattern: /ultra/i, label: "Ultra" },
  { pattern: /enterprise/i, label: "Enterprise" },
  { pattern: /pro/i, label: "Pro" },
  { pattern: /standard/i, label: "Standard" },
  { pattern: /free|zero/i, label: "Free" },
];

export function formatAntigravityPlan(tierStr?: string): string | undefined {
  if (!tierStr) return undefined;
  for (const { pattern, label } of TIER_LABEL_MAP) {
    if (pattern.test(tierStr)) return label;
  }
  return undefined;
}

function isFreeTierLabel(tierStr?: string): boolean {
  if (!tierStr) return false;
  return /free|zero/i.test(tierStr);
}

export function resolveOnboardTierId(loadResult: LoadCodeAssistResponse): string | undefined {
  const allowedTiers = loadResult.allowedTiers ?? [];
  return (
    loadResult.currentTier?.id ?? allowedTiers.find((t) => t.isDefault)?.id ?? allowedTiers[0]?.id
  );
}

/**
 * Resolve plan badge from loadCodeAssist response.
 * Priority: paidTier → currentTier → default allowedTier.
 */
export function resolveAntigravityPlan(loadResult: LoadCodeAssistResponse): string | undefined {
  const paidTier = loadResult.paidTier;
  if (paidTier?.id) {
    const paidLabel =
      formatAntigravityPlan(paidTier.id) ??
      formatAntigravityPlan(paidTier.name) ??
      formatAntigravityPlan(paidTier.description);
    if (paidLabel) return paidLabel;
    if (!isFreeTierLabel(paidTier.id) && !isFreeTierLabel(paidTier.name)) {
      return paidTier.name ?? paidTier.id;
    }
  }

  const current = loadResult.currentTier;
  const fromCurrent =
    formatAntigravityPlan(current?.id) ??
    formatAntigravityPlan(current?.name) ??
    formatAntigravityPlan(current?.description);
  if (fromCurrent) return fromCurrent;

  if (isFreeTierLabel(current?.id) || isFreeTierLabel(current?.name)) {
    return "Free";
  }

  const defaultTier = loadResult.allowedTiers?.find((t) => t.isDefault);
  if (defaultTier && isFreeTierLabel(defaultTier.id)) {
    return "Free";
  }

  return (
    formatAntigravityPlan(defaultTier?.id) ??
    formatAntigravityPlan(defaultTier?.name) ??
    formatAntigravityPlan(defaultTier?.description)
  );
}

export function buildAntigravityPlanInfo(load: LoadCodeAssistResponse): Record<string, unknown> {
  const tier = load.currentTier;
  const paidTier = load.paidTier;
  return {
    currentTier: tier?.name,
    tierId: tier?.id,
    paidTierName: paidTier?.name,
    paidTierId: paidTier?.id,
    upgradeText: tier?.upgradeSubscriptionText,
    projectId: load.cloudaicompanionProject,
  };
}
