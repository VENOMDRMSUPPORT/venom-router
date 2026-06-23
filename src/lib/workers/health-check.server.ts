// src/lib/workers/health-check.server.ts
import type { SupabaseClient } from "@supabase/supabase-js";
import { unpackCredentials, packCredentials } from "@/lib/credentials.server";
import type { StoredCredentials } from "@/lib/providers/adapters/types";

export interface AccountHealthCheckResult {
  account_id: string;
  provider_slug: string;
  ok: boolean;
  latency_ms: number;
  error?: string;
  new_status: "healthy" | "degraded" | "expired";
  quota_used: number | null;
  quota_total: number | null;
  quota_unit: string | null;
  quota_extra: Record<string, unknown> | null;
}

function isClaudeAuthError(slug: string, e: unknown): boolean {
  if (slug !== "claude-code") return false;
  const err = e as { name?: string; message?: string } | null;
  return (
    err?.name === "ClaudeAuthError" ||
    String(err?.message ?? "").includes("re-login required")
  );
}

export async function runHealthChecks(
  supabase: SupabaseClient,
): Promise<AccountHealthCheckResult[]> {
  const { data: accounts, error } = await supabase
    .from("accounts")
    .select(
      "id,credentials_enc,credentials_iv,credentials_tag,quota_extra,providers(slug)",
    )
    .neq("status", "expired");

  if (error) throw new Error(`Health check: failed to fetch accounts: ${error.message}`);

  const results: AccountHealthCheckResult[] = [];
  const checkedAt = new Date().toISOString();

  for (const acct of accounts ?? []) {
    const slug = (acct.providers as { slug?: string } | null)?.slug ?? "";
    let result: AccountHealthCheckResult = {
      account_id: acct.id as string,
      provider_slug: slug,
      ok: false,
      latency_ms: 0,
      new_status: "degraded",
      quota_used: null,
      quota_total: null,
      quota_unit: null,
      quota_extra: null,
    };

    let refreshedCreds: StoredCredentials | null = null;

    // Skip providers with no health check method
    if (slug === "opencode-zen") {
      console.log(`[health-check] ${slug}/${acct.id} → skipped (no health check)`);
      continue;
    }

    try {
      const credsIn = unpackCredentials({
        credentials_enc: acct.credentials_enc,
        credentials_iv: acct.credentials_iv,
        credentials_tag: acct.credentials_tag,
      });
      const t0 = Date.now();

      if (slug === "claude-code") {
        const adapter = await import("@/lib/providers/adapters/claude-code.server");
        const r = await adapter.fetchIdentity(credsIn);
        refreshedCreds = r.creds;
        result = {
          ...result,
          ok: r.health.ok,
          latency_ms: Date.now() - t0,
          error: r.health.error,
          new_status: r.health.ok ? "healthy" : "degraded",
          quota_used: r.identity.quota_used,
          quota_total: r.identity.quota_total,
          quota_unit: r.identity.quota_unit,
          quota_extra: (r.identity.quota_extra as Record<string, unknown>) ?? null,
        };
      } else if (slug === "antigravity") {
        const adapter = await import("@/lib/providers/adapters/antigravity.server");
        const r = await adapter.syncAntigravityAccount(credsIn);
        refreshedCreds = r.creds;
        result = {
          ...result,
          ok: r.health.ok,
          latency_ms: r.health.latency_ms,
          error: r.health.error,
          new_status: r.health.ok ? "healthy" : "degraded",
          quota_used: r.identity.quota_used,
          quota_total: r.identity.quota_total,
          quota_unit: r.identity.quota_unit,
          quota_extra: (r.identity.quota_extra as Record<string, unknown>) ?? null,
        };
      }
      // opencode-zen: no health check method — skip silently
    } catch (e: unknown) {
      result.ok = false;
      result.error = String((e as { message?: string } | null)?.message ?? e).slice(0, 500);
      result.new_status = isClaudeAuthError(slug, e) ? "expired" : "degraded";
    }

    // Build account update patch
    const patch: Record<string, unknown> = {
      status: result.new_status,
      last_health_check_at: checkedAt,
    };
    if (result.quota_used !== null) patch.quota_used = result.quota_used;
    if (result.quota_total !== null) patch.quota_total = result.quota_total;
    if (result.quota_unit !== null) patch.quota_unit = result.quota_unit;
    if (result.quota_extra !== null) patch.quota_extra = result.quota_extra;

    if (refreshedCreds) {
      const packed = packCredentials(refreshedCreds);
      patch.credentials_enc = packed.credentials_enc;
      patch.credentials_iv = packed.credentials_iv;
      patch.credentials_tag = packed.credentials_tag;
    }

    const { error: updateErr } = await supabase
      .from("accounts")
      .update(patch)
      .eq("id", acct.id);
    if (updateErr) {
      console.error(`[health-check] failed to update account ${acct.id}:`, updateErr.message);
    }

    await supabase.from("account_health_checks").insert({
      account_id: acct.id,
      checked_at: checkedAt,
      status: result.new_status,
      latency_ms: result.latency_ms,
      error_code: result.ok ? null : "check_failed",
      error_message: result.error ?? null,
    });

    results.push(result);
    console.log(
      `[health-check] ${slug}/${acct.id} → ${result.new_status} (${result.latency_ms}ms)${result.error ? ` err=${result.error.slice(0, 80)}` : ""}`,
    );
  }

  return results;
}
