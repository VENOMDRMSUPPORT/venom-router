import { supabaseAdmin } from "@/integrations/supabase/client.server";
import { runHealthChecks } from "./health-check.server";
import { runQuotaSnapshots } from "./quota-snapshot.server";
import { createLogger } from "@/lib/logger";

const log = createLogger("workers");

export async function runScheduled(_cron: string): Promise<void> {
  const t0 = Date.now();
  log.info("scheduled run starting");

  try {
    const results = await runHealthChecks(supabaseAdmin);
    const healthy = results.filter((r) => r.ok).length;
    log.info("health checks complete", { accounts: results.length, healthy });

    await runQuotaSnapshots(supabaseAdmin, results);
    log.info("quota snapshots done");
  } catch (e) {
    log.error("scheduled run failed", { error: e instanceof Error ? e.message : String(e) });
  }

  log.info("scheduled run complete", { durationMs: Date.now() - t0 });
}
