/* Patch React Query integrations cache from syncAccount response — avoids listIntegrations refetch. */

import type { QueryClient } from "@tanstack/react-query";
import type { SyncAccountResponse, SyncAccountResult } from "./sync-response.types";
import type { ProviderRow } from "@/components/providers/account-row";

/** Invalidate catalog + dashboard after model/provider mutations. */
export async function invalidateModelViews(qc: QueryClient) {
  await Promise.all([
    qc.invalidateQueries({ queryKey: ["catalog-models"] }),
    qc.invalidateQueries({ queryKey: ["dashboard-metrics"] }),
  ]);
}

/** syncAccount returns Response.json (x-tss-raw); unwrap for cache + toasts. */
export async function parseSyncResponse(
  raw: SyncAccountResult | Response,
): Promise<SyncAccountResult> {
  if (raw instanceof Response) {
    if (!raw.ok) throw new Error(await raw.text());
    return (await raw.json()) as SyncAccountResult;
  }
  return raw;
}

const QUOTA_SHORT_LABELS: Record<string, string> = {
  "Gemini Models": "GEM",
  "Claude and GPT Models": "OPT",
};

export function formatSyncToast(r: SyncAccountResponse): string {
  return [
    `${r.models.fetched} models`,
    r.account.plan,
    r.quota.synced ? `${r.quota.groups.length} quota groups` : null,
    r.health.ok ? `health ${r.health.latency_ms}ms` : null,
  ]
    .filter(Boolean)
    .join(" · ");
}

export function patchAccountInProviders(
  prev: ProviderRow[] | undefined,
  response: SyncAccountResponse,
): ProviderRow[] | undefined {
  if (!prev?.length) return prev;

  const { account_id, account: acct, models } = response;
  let patched = false;

  const next = prev.map((provider) => {
    const idx = provider.accounts.findIndex((a) => a.id === account_id);
    if (idx === -1) return provider;

    patched = true;
    const accounts = [...provider.accounts];
    accounts[idx] = {
      ...accounts[idx]!,
      label: acct.label,
      email: acct.email,
      plan: acct.plan,
      status: acct.status,
      quota_used: acct.quota_used,
      quota_total: acct.quota_total,
      quota_unit: acct.quota_unit,
      quota_extra: acct.quota_extra,
      last_synced_at: acct.last_synced_at,
      last_health_check_at: acct.last_health_check_at,
      modelsTotal: models.total || models.fetched,
      modelsEnabled: models.enabled || models.total || models.fetched,
    };
    return { ...provider, accounts };
  });

  return patched ? next : prev;
}

export function quotaGroupsFromExtra(
  quotaExtra: Record<string, unknown> | null | undefined,
): SyncAccountResponse["quota"]["groups"] {
  const raw =
    (quotaExtra?.groups as
      | Array<{
          name: string;
          modelIds?: string[];
          fiveHourQuota?: { remainingFraction?: number; resetTime?: string; isExhausted?: boolean };
        }>
      | undefined) ?? [];

  return raw.map((g) => ({
    name: g.name,
    short_label: QUOTA_SHORT_LABELS[g.name] ?? g.name.split(" ")[0] ?? g.name,
    model_count: g.modelIds?.length ?? 0,
    five_hour: g.fiveHourQuota?.resetTime
      ? {
          remaining_pct: Math.round((g.fiveHourQuota.remainingFraction ?? 0) * 100),
          reset_at: g.fiveHourQuota.resetTime,
          exhausted: Boolean(g.fiveHourQuota.isExhausted),
        }
      : undefined,
  }));
}
