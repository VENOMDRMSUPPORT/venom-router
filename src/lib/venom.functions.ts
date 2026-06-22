import { createServerFn } from "@tanstack/react-start";
import { requireSupabaseAuth } from "@/integrations/supabase/auth-middleware";
import { z } from "zod";
import { aggregateCatalogModels } from "@/lib/providers/integrations.functions";

type VenomSlug = "lite" | "pro" | "max";

const DAY_LABELS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"] as const;

function buildTraffic7d(records: { created_at: string }[]): { day: string; requests: number }[] {
  const buckets = new Map<string, number>();
  const now = new Date();
  for (let i = 6; i >= 0; i--) {
    const d = new Date(now);
    d.setDate(d.getDate() - i);
    d.setHours(0, 0, 0, 0);
    const key = d.toISOString().slice(0, 10);
    buckets.set(key, 0);
  }
  for (const r of records) {
    const key = new Date(r.created_at).toISOString().slice(0, 10);
    if (buckets.has(key)) buckets.set(key, (buckets.get(key) ?? 0) + 1);
  }
  return [...buckets.entries()].map(([key, requests]) => {
    const d = new Date(key + "T12:00:00");
    return { day: DAY_LABELS[d.getDay()]!, requests };
  });
}

export const getDashboardMetrics = createServerFn({ method: "GET" })
  .middleware([requireSupabaseAuth])
  .handler(async ({ context }) => {
    const { supabase } = context;
    const sevenDaysAgo = new Date(Date.now() - 7 * 86400000).toISOString();

    const [
      modelRows,
      venomModels,
      routingRules,
      apiKeys,
      usage7d,
      auditLog,
      accounts,
      allAccounts,
    ] = await Promise.all([
      supabase
        .from("models")
        .select(
          "id,external_id,display_name,capabilities,quality_rating,context_window,input_cost_per_mtok,output_cost_per_mtok,test_status,latency_ms,last_tested_at,lifecycle,enabled,accounts(id,email,label,status),providers(slug,name)",
        ),
      supabase.from("venom_models").select("id", { count: "exact", head: true }),
      supabase
        .from("routing_rules")
        .select("id", { count: "exact", head: true })
        .eq("active", true),
      supabase
        .from("venom_api_keys")
        .select("id", { count: "exact", head: true })
        .is("revoked_at", null),
      supabase
        .from("usage_records")
        .select("id,venom_slug,created_at,success,cost_usd,input_tokens,output_tokens")
        .gte("created_at", sevenDaysAgo)
        .order("created_at", { ascending: false }),
      supabase
        .from("audit_log")
        .select("id,action,target_type,metadata,created_at")
        .order("created_at", { ascending: false })
        .limit(5),
      supabase
        .from("accounts")
        .select("id,email,label,status,last_synced_at,quota_used,quota_unit,providers(name,slug)")
        .order("last_synced_at", { ascending: false, nullsFirst: false })
        .limit(6),
      supabase.from("accounts").select("status"),
    ]);

    const catalog = aggregateCatalogModels((modelRows.data ?? []) as any);
    const usageRecords = usage7d.data ?? [];
    const accountRows = accounts.data ?? [];

    const modelsEnabledByAccount: Record<string, number> = {};
    for (const row of modelRows.data ?? []) {
      const caps = row.capabilities as Record<string, unknown> | null;
      if (caps?.stale) continue;
      const acctId = (row as any).accounts?.id as string | undefined;
      if (!acctId) continue;
      if (row.enabled) modelsEnabledByAccount[acctId] = (modelsEnabledByAccount[acctId] ?? 0) + 1;
    }

    const distributionMap = new Map<string, number>();
    for (const slug of ["lite", "pro", "max"]) distributionMap.set(slug, 0);
    for (const r of usageRecords) {
      const slug = r.venom_slug as string;
      if (distributionMap.has(slug))
        distributionMap.set(slug, (distributionMap.get(slug) ?? 0) + 1);
    }

    const recentFromUsage = usageRecords.slice(0, 5).map((r) => ({
      id: r.id as string,
      kind: "request" as const,
      title: `Request · venom/${r.venom_slug}`,
      detail:
        r.input_tokens != null ? `${(r.input_tokens ?? 0) + (r.output_tokens ?? 0)} tokens` : null,
      status: (r.success === false ? "failure" : "success") as "success" | "failure",
      created_at: r.created_at as string,
    }));

    const recentFromAudit = (auditLog.data ?? []).map((a) => ({
      id: a.id as string,
      kind: "sync" as const,
      title: a.action as string,
      detail: ((a.metadata as Record<string, unknown> | null)?.detail as string | null) ?? null,
      status: "success" as const,
      created_at: a.created_at as string,
    }));

    const recent_activity = [...recentFromUsage, ...recentFromAudit]
      .sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime())
      .slice(0, 8);

    const allAccountRows = allAccounts.data ?? [];

    const [{ count: totalUsage }] = await Promise.all([
      supabase.from("usage_records").select("id", { count: "exact", head: true }),
    ]);

    const accountCount = allAccountRows.length;

    return {
      kpis: {
        provider_models: catalog.length,
        venom_models: venomModels.count ?? 0,
        routing_rules: routingRules.count ?? 0,
        api_keys: apiKeys.count ?? 0,
      },
      working_models: catalog.filter((m) => m.test_status === "working").length,
      traffic_7d: buildTraffic7d(usageRecords),
      distribution: [...distributionMap.entries()].map(([slug, requests]) => ({ slug, requests })),
      checklist: {
        owner_created: true,
        provider_connected: (accountCount ?? 0) > 0,
        routing_configured: (routingRules.count ?? 0) > 0,
        api_key_issued: (apiKeys.count ?? 0) > 0,
        first_request_sent: (totalUsage ?? 0) > 0,
      },
      recent_activity,
      provider_health: accountRows.map((a) => ({
        account_id: a.id,
        provider_name: (a.providers as { name?: string } | null)?.name ?? "Provider",
        provider_slug: (a.providers as { slug?: string } | null)?.slug ?? "",
        email: a.email,
        label: a.label,
        status: a.status,
        last_synced_at: a.last_synced_at,
        models_enabled: modelsEnabledByAccount[a.id] ?? 0,
        quota_used: a.quota_used,
        quota_unit: a.quota_unit,
      })),
      accounts_healthy: allAccountRows.filter((a) => a.status === "healthy").length,
      accounts_total: accountCount,
    };
  });

export const getOverviewStats = createServerFn({ method: "GET" })
  .middleware([requireSupabaseAuth])
  .handler(async ({ context }) => {
    const { supabase } = context;
    const [providers, accounts, models, rules, traces] = await Promise.all([
      supabase.from("providers").select("id", { count: "exact", head: true }),
      supabase.from("accounts").select("id,status", { count: "exact" }),
      supabase.from("models").select("id,lifecycle", { count: "exact" }),
      supabase.from("routing_rules").select("id,venom_slug,active"),
      supabase
        .from("usage_records")
        .select("venom_slug,cost_usd,input_tokens,output_tokens,fallback_used,created_at")
        .gte("created_at", new Date(Date.now() - 30 * 86400000).toISOString()),
    ]);

    const accountRows = accounts.data ?? [];
    const modelRows = models.data ?? [];
    const ruleRows = rules.data ?? [];
    const traceRows = traces.data ?? [];

    const totalTokens = traceRows.reduce(
      (s, r) => s + (r.input_tokens ?? 0) + (r.output_tokens ?? 0),
      0,
    );
    const totalCost = traceRows.reduce((s, r) => s + Number(r.cost_usd ?? 0), 0);

    const perVenom = (["lite", "pro", "max"] as const).map((slug) => {
      const activeRoutes = ruleRows.filter((r) => r.venom_slug === slug && r.active).length;
      const today = new Date();
      today.setHours(0, 0, 0, 0);
      const todayRecs = traceRows.filter(
        (r) => r.venom_slug === slug && new Date(r.created_at) >= today,
      );
      const fallbacks = todayRecs.filter((r) => r.fallback_used).length;
      return {
        slug,
        activeRoutes,
        requestsToday: todayRecs.length,
        fallbackRate: todayRecs.length ? Math.round((fallbacks / todayRecs.length) * 100) : 0,
      };
    });

    return {
      providers: providers.count ?? 0,
      accountsHealthy: accountRows.filter((a) => a.status === "healthy").length,
      accountsTotal: accountRows.length,
      modelsApproved: modelRows.filter((m) => m.lifecycle === "approved").length,
      modelsTotal: modelRows.length,
      totalTokens,
      totalCost,
      perVenom,
    };
  });

export const listProviders = createServerFn({ method: "GET" })
  .middleware([requireSupabaseAuth])
  .handler(async ({ context }) => {
    const { data, error } = await context.supabase
      .from("providers")
      .select("id,name,kind,adapter,base_url,created_at,accounts(id,label,status,quota_strategy)")
      .order("created_at", { ascending: false });
    if (error) throw new Error(error.message);
    return data ?? [];
  });

export const listVenomModels = createServerFn({ method: "GET" })
  .middleware([requireSupabaseAuth])
  .handler(async ({ context }) => {
    const { data, error } = await context.supabase.from("venom_models").select("*").order("slug");
    if (error) throw new Error(error.message);
    return data ?? [];
  });

export const listApiKeys = createServerFn({ method: "GET" })
  .middleware([requireSupabaseAuth])
  .handler(async ({ context }) => {
    const { data, error } = await context.supabase
      .from("venom_api_keys")
      .select(
        "id,name,key_prefix,allowed_models,rpm_limit,tpd_limit,monthly_cap_usd,revoked_at,last_used_at,created_at",
      )
      .order("created_at", { ascending: false });
    if (error) throw new Error(error.message);
    return data ?? [];
  });

// ───── Venom Models: update weights, timeout, fallback attempts ─────
const updateVenomSchema = z.object({
  slug: z.enum(["lite", "pro", "max"]),
  weight_cost: z.number().min(0).max(1).optional(),
  weight_speed: z.number().min(0).max(1).optional(),
  weight_quality: z.number().min(0).max(1).optional(),
  max_fallback_attempts: z.number().int().min(0).max(10).optional(),
  timeout_ms: z.number().int().min(1000).max(120000).optional(),
});
export const updateVenomModel = createServerFn({ method: "POST" })
  .middleware([requireSupabaseAuth])
  .inputValidator((data: unknown) => updateVenomSchema.parse(data))
  .handler(async ({ data, context }) => {
    const { slug, ...patch } = data;
    const { error } = await context.supabase.from("venom_models").update(patch).eq("slug", slug);
    if (error) throw new Error(error.message);
    return { ok: true };
  });

// ───── Quotas: list accounts + quota, upsert quota ─────
export const listAccountQuotas = createServerFn({ method: "GET" })
  .middleware([requireSupabaseAuth])
  .handler(async ({ context }) => {
    const { data: accounts, error } = await context.supabase
      .from("accounts")
      .select(
        "id,label,status,quota_strategy,provider_id,last_health_check_at,providers(name,kind)",
      )
      .order("created_at", { ascending: false });
    if (error) throw new Error(error.message);
    const ids = (accounts ?? []).map((a) => a.id);
    const { data: quotas } = ids.length
      ? await context.supabase.from("quotas").select("*").in("account_id", ids)
      : {
          data: [] as Array<{
            account_id: string;
            used: number;
            total: number | null;
            unit: string;
            confidence: "high" | "medium" | "low";
            source: string;
            resets_at: string | null;
            updated_at: string;
          }>,
        };
    const byId = new Map((quotas ?? []).map((q) => [q.account_id, q]));
    return (accounts ?? []).map((a) => ({ ...a, quota: byId.get(a.id) ?? null }));
  });

const quotaSchema = z.object({
  account_id: z.string().uuid(),
  used: z.number().min(0),
  total: z.number().min(0).nullable(),
  unit: z.string().min(1).max(32),
  confidence: z.enum(["high", "medium", "low"]),
  source: z.string().min(1).max(64),
  resets_at: z.string().nullable(),
});
export const upsertQuota = createServerFn({ method: "POST" })
  .middleware([requireSupabaseAuth])
  .inputValidator((data: unknown) => quotaSchema.parse(data))
  .handler(async ({ data, context }) => {
    const { error } = await context.supabase
      .from("quotas")
      .upsert({ ...data, updated_at: new Date().toISOString() }, { onConflict: "account_id" });
    if (error) throw new Error(error.message);
    return { ok: true };
  });

// ───── API Keys: create / revoke / delete ─────
const createKeySchema = z.object({
  name: z.string().trim().min(1).max(80),
  allowed_models: z.array(z.enum(["lite", "pro", "max"])).min(1),
  rpm_limit: z.number().int().positive().nullable(),
  tpd_limit: z.number().int().positive().nullable(),
  monthly_cap_usd: z.number().positive().nullable(),
});
export const createApiKey = createServerFn({ method: "POST" })
  .middleware([requireSupabaseAuth])
  .inputValidator((data: unknown) => createKeySchema.parse(data))
  .handler(async ({ data, context }) => {
    const { generateApiKey } = await import("@/lib/crypto.server");
    const { raw, prefix, hash } = generateApiKey();
    const allowed: VenomSlug[] = data.allowed_models;
    const { data: row, error } = await context.supabase
      .from("venom_api_keys")
      .insert({
        name: data.name,
        allowed_models: allowed,
        rpm_limit: data.rpm_limit,
        tpd_limit: data.tpd_limit,
        monthly_cap_usd: data.monthly_cap_usd,
        key_prefix: prefix,
        key_hash: hash,
      })
      .select("id")
      .single();
    if (error) throw new Error(error.message);
    return { id: row.id, raw, prefix };
  });

export const revokeApiKey = createServerFn({ method: "POST" })
  .middleware([requireSupabaseAuth])
  .inputValidator((data: unknown) => z.object({ id: z.string().uuid() }).parse(data))
  .handler(async ({ data, context }) => {
    const { error } = await context.supabase
      .from("venom_api_keys")
      .update({ revoked_at: new Date().toISOString() })
      .eq("id", data.id);
    if (error) throw new Error(error.message);
    return { ok: true };
  });

export const deleteApiKey = createServerFn({ method: "POST" })
  .middleware([requireSupabaseAuth])
  .inputValidator((data: unknown) => z.object({ id: z.string().uuid() }).parse(data))
  .handler(async ({ data, context }) => {
    const { error } = await context.supabase.from("venom_api_keys").delete().eq("id", data.id);
    if (error) throw new Error(error.message);
    return { ok: true };
  });
