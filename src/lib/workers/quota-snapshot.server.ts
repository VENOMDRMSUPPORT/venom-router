import type { SupabaseClient } from "@supabase/supabase-js";
import type { AccountHealthCheckResult } from "./health-check.server";

export async function runQuotaSnapshots(
  supabase: SupabaseClient,
  results: AccountHealthCheckResult[],
): Promise<void> {
  const withQuota = results.filter((r) => r.quota_used !== null && r.quota_total !== null);

  if (!withQuota.length) return;

  const snappedAt = new Date().toISOString();

  const { error } = await supabase.from("quota_snapshots").insert(
    withQuota.map((r) => ({
      account_id: r.account_id,
      snapped_at: snappedAt,
      quota_type: "tokens",
      period: "rolling",
      used: r.quota_used,
      total: r.quota_total,
      remaining:
        r.quota_total !== null && r.quota_used !== null ? r.quota_total - r.quota_used : null,
      quota_source: "provider_reported",
      confidence: "high",
    })),
  );

  if (error) {
    console.error("[quota-snapshot] insert failed:", error.message);
  } else {
    console.log(`[quota-snapshot] inserted ${withQuota.length} snapshot(s)`);
  }
}
