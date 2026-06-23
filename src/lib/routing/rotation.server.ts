import type { ScoredCandidate } from "@/lib/routing/types";
import type { AccountRotation } from "@/lib/routing/strategy.types";

function remainingQuotaFraction(
  quota: { used: number; total: number | null; confidence: string } | null,
): number {
  if (!quota || quota.total === null || quota.total <= 0) return 0.5;
  return Math.max(0, (quota.total - quota.used) / quota.total);
}

/**
 * Interleaves candidates so no single account dominates the top of the list.
 * Within each "round", the best candidate from each account is picked.
 */
function interleaveByAccount(
  scored: ScoredCandidate[],
  accountOrder: (accountIds: string[]) => string[],
): ScoredCandidate[] {
  // Group by account
  const byAccount = new Map<string, ScoredCandidate[]>();
  for (const sc of scored) {
    const id = sc.candidate.account.id;
    if (!byAccount.has(id)) byAccount.set(id, []);
    byAccount.get(id)!.push(sc);
  }

  const sortedAccountIds = accountOrder([...byAccount.keys()]);
  const result: ScoredCandidate[] = [];

  let remaining = scored.length;
  while (remaining > 0) {
    for (const accountId of sortedAccountIds) {
      const bucket = byAccount.get(accountId);
      if (bucket && bucket.length > 0) {
        result.push(bucket.shift()!);
        remaining--;
      }
    }
  }

  return result;
}

export function applyAccountRotation(
  scored: ScoredCandidate[],
  strategy: AccountRotation,
): ScoredCandidate[] {
  if (scored.length === 0 || strategy === "off") return scored;

  switch (strategy) {
    case "quota_weighted": {
      // Sort accounts by descending remaining quota, then interleave
      const quotaByAccount = new Map<string, number>();
      for (const sc of scored) {
        const id = sc.candidate.account.id;
        if (!quotaByAccount.has(id)) {
          quotaByAccount.set(id, remainingQuotaFraction(sc.candidate.account.quota));
        }
      }
      return interleaveByAccount(scored, (ids) =>
        [...ids].sort((a, b) => (quotaByAccount.get(b) ?? 0) - (quotaByAccount.get(a) ?? 0)),
      );
    }

    case "health_weighted": {
      // Healthy accounts first, then by score
      const healthScore = (sc: ScoredCandidate) =>
        sc.candidate.account.status === "healthy" ? 1 : 0;
      const accountBestScore = new Map<string, number>();
      for (const sc of scored) {
        const id = sc.candidate.account.id;
        const existing = accountBestScore.get(id) ?? -Infinity;
        accountBestScore.set(id, Math.max(existing, healthScore(sc) * 10 + sc.score));
      }
      return interleaveByAccount(scored, (ids) =>
        [...ids].sort(
          (a, b) => (accountBestScore.get(b) ?? 0) - (accountBestScore.get(a) ?? 0),
        ),
      );
    }

    case "round_robin": {
      // Sort account IDs deterministically (by ID string), then interleave
      return interleaveByAccount(scored, (ids) => [...ids].sort());
    }

    default:
      return scored;
  }
}
