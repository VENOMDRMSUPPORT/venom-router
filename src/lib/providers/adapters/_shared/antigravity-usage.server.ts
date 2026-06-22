/* Antigravity usage helpers — subscription/plan metadata + quota parsing from live models. */

import {
  ANTIGRAVITY_BASE,
  ANTIGRAVITY_USER_AGENT,
  OAUTH_CLIENT_METADATA,
  loadCodeAssistBody,
} from "./antigravity-constants.server";
import { resolveAntigravityPlan, type LoadCodeAssistResponse } from "./antigravity-plan.server";
import type { LiveModelEntry } from "./antigravity-models.server";

const LOAD_CODE_ASSIST = `${ANTIGRAVITY_BASE}/v1internal:loadCodeAssist`;

export interface AntigravityModelQuota {
  used: number;
  total: number;
  remaining: number;
  remainingPercentage: number;
  resetAt?: string;
  unlimited: boolean;
  displayName?: string;
  isExhausted?: boolean;
}

export interface AntigravityUsageSnapshot {
  plan: string | null;
  projectId?: string;
  availablePromptCredits?: number;
  planInfo?: Record<string, unknown>;
  tierName?: string;
  quotas: Record<string, AntigravityModelQuota>;
  message?: string;
}

function antigravityHeaders(token: string) {
  return {
    Authorization: `Bearer ${token}`,
    "Content-Type": "application/json",
    "User-Agent": ANTIGRAVITY_USER_AGENT,
    "X-Goog-Api-Client": "google-cloud-sdk vscode_cloudshelleditor/0.1",
    "X-Client-Name": "antigravity",
    "X-Client-Version": "1.107.0",
    "x-request-source": "local",
    "Client-Metadata": JSON.stringify(OAUTH_CLIENT_METADATA),
  };
}

function parseResetTime(resetTime?: string): string | undefined {
  return resetTime || undefined;
}

function quotaFromFraction(
  remainingFraction: number,
  displayName?: string,
  isExhausted?: boolean,
): AntigravityModelQuota {
  const frac = Math.max(0, Math.min(1, remainingFraction));
  const total = 1000;
  const remaining = Math.round(total * frac);
  const used = Math.max(0, total - remaining);
  return {
    used,
    total,
    remaining,
    remainingPercentage: frac * 100,
    unlimited: false,
    displayName,
    isExhausted,
  };
}

export function buildAntigravityUsageQuotas(
  liveModels: Record<string, LiveModelEntry>,
): Record<string, AntigravityModelQuota> {
  const quotas: Record<string, AntigravityModelQuota> = {};
  for (const [modelKey, info] of Object.entries(liveModels)) {
    if (!info?.quotaInfo) continue;
    const remainingFraction = Number(info.quotaInfo.remainingFraction ?? 0);
    quotas[modelKey] = {
      ...quotaFromFraction(
        remainingFraction,
        info.displayName || modelKey,
        info.quotaInfo.isExhausted,
      ),
      resetAt: parseResetTime(info.quotaInfo.resetTime),
    };
  }
  return quotas;
}

export async function fetchAntigravitySubscription(
  token: string,
): Promise<LoadCodeAssistResponse | null> {
  try {
    const res = await fetch(LOAD_CODE_ASSIST, {
      method: "POST",
      headers: antigravityHeaders(token),
      body: JSON.stringify(loadCodeAssistBody()),
      signal: AbortSignal.timeout(10_000),
    });
    if (!res.ok) return null;
    return (await res.json()) as LoadCodeAssistResponse;
  } catch {
    return null;
  }
}

export async function fetchAntigravityUsage(
  token: string,
  projectId: string | undefined,
  liveModels: Record<string, LiveModelEntry>,
  subscriptionInfo?: LoadCodeAssistResponse | null,
): Promise<AntigravityUsageSnapshot> {
  const resolvedSubscription =
    subscriptionInfo !== undefined ? subscriptionInfo : await fetchAntigravitySubscription(token);
  const resolvedProjectId = projectId ?? resolvedSubscription?.cloudaicompanionProject ?? undefined;
  const plan = resolvedSubscription ? (resolveAntigravityPlan(resolvedSubscription) ?? null) : null;
  const quotas = buildAntigravityUsageQuotas(liveModels);

  const tier = resolvedSubscription?.currentTier;
  const paidTier = resolvedSubscription?.paidTier;

  return {
    plan,
    projectId: resolvedProjectId,
    availablePromptCredits: resolvedSubscription?.availablePromptCredits,
    planInfo: resolvedSubscription
      ? {
          currentTier: tier?.name,
          tierId: tier?.id,
          paidTierName: paidTier?.name,
          paidTierId: paidTier?.id,
          upgradeText: tier?.upgradeSubscriptionText,
          projectId: resolvedSubscription.cloudaicompanionProject,
        }
      : undefined,
    tierName: tier?.name,
    quotas,
  };
}
