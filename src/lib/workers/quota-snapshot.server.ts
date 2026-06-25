import type { SupabaseClient } from "@supabase/supabase-js";
import type { AccountHealthCheckResult } from "./health-check.server";
import { createLogger } from "@/lib/logger";

const log = createLogger("quota-snapshot");

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
    log.error("insert failed", { error: error.message });
  } else {
    log.info("snapshots inserted", { count: withQuota.length });
  }
}
