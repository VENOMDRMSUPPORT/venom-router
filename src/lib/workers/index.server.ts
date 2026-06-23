import { supabaseAdmin } from "@/integrations/supabase/client.server";
import { runHealthChecks } from "./health-check.server";
import { runQuotaSnapshots } from "./quota-snapshot.server";

export async function runScheduled(_cron: string): Promise<void> {
  const t0 = Date.now();
  console.log("[workers] scheduled run starting");

  try {
    const results = await runHealthChecks(supabaseAdmin);
    const healthy = results.filter((r) => r.ok).length;
    console.log(
      `[workers] health checks: ${results.length} accounts, ${healthy} healthy`,
    );

    await runQuotaSnapshots(supabaseAdmin, results);
    console.log("[workers] quota snapshots done");
  } catch (e) {
    console.error("[workers] scheduled run failed:", e);
  }

  console.log(`[workers] complete in ${Date.now() - t0}ms`);
}
