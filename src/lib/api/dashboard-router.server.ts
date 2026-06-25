import type { SupabaseClient } from "@supabase/supabase-js";
import { requireDashboardAuth } from "@/lib/dashboard-auth.server";
import { createLogger } from "@/lib/logger";
import { z } from "zod";

const log = createLogger("dashboard-api");

// ── helpers ───────────────────────────────────────────────────────────────────

function ok(data: unknown, status = 200): Response {
  return Response.json(data, { status });
}

function err(message: string, status = 500): Response {
  return Response.json({ error: message }, { status });
}

function parseBody(request: Request): Promise<unknown> {
  return request.json().catch(() => {
    throw Object.assign(new Error("Invalid JSON body"), { status: 400 });
  });
}

// ── validation schemas ────────────────────────────────────────────────────────

const updateVenomSchema = z.object({
  weight_cost: z.number().min(0).max(1).optional(),
  weight_speed: z.number().min(0).max(1).optional(),
  weight_quality: z.number().min(0).max(1).optional(),
  max_fallback_attempts: z.number().int().min(0).max(10).optional(),
  timeout_ms: z.number().int().min(1000).max(120000).optional(),
  strategy_config: z
    .object({
      quota_threshold_pct: z.number().min(0).max(100),
      premium_reserve_pct: z.number().min(0).max(100),
      auto_escalation: z.enum(["off", "on_failure", "on_quota", "on_complexity"]),
      account_rotation: z.enum(["off", "round_robin", "quota_weighted", "health_weighted"]),
      health_requirement: z.enum(["healthy_only", "allow_degraded"]),
      fallback_behavior: z.enum(["sequential", "skip_exhausted", "premium_last"]),
    })
    .partial()
    .optional(),
});

const routingConditionSchema = z
  .object({
    requires: z.array(z.string()).optional(),
    min_context_tokens: z.number().int().min(0).optional(),
    quota_risk: z.enum(["low", "medium", "high"]).optional(),
  })
  .optional();

const createRuleSchema = z.object({
  venom_slug: z.enum(["lite", "pro", "max"]),
  model_id: z.string().uuid(),
  account_id: z.string().uuid(),
  priority: z.number().int().min(0).max(9999),
  role: z.enum(["primary", "fallback"]).default("primary"),
  active: z.boolean().default(true),
  condition: routingConditionSchema,
});

const updateRuleSchema = z
  .object({
    priority: z.number().int().min(0).max(9999).optional(),
    role: z.enum(["primary", "fallback"]).optional(),
    active: z.boolean().optional(),
    condition: routingConditionSchema,
  })
  .refine((body) => Object.keys(body).length > 0, { message: "No fields to update" });

const toggleRuleSchema = z.object({ active: z.boolean() });

const createKeySchema = z.object({
  name: z.string().trim().min(1).max(80),
  allowed_models: z.array(z.enum(["lite", "pro", "max"])).min(1),
  rpm_limit: z.number().int().positive().nullable(),
  tpd_limit: z.number().int().positive().nullable(),
  monthly_cap_usd: z.number().positive().nullable(),
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

const playgroundChatSchema = z.object({
  venom_slug: z.enum(["lite", "pro", "max"]),
  messages: z
    .array(
      z.object({
        role: z.enum(["user", "assistant", "system"]),
        content: z.string().min(1),
      }),
    )
    .min(1),
  max_tokens: z.number().int().positive().optional(),
  temperature: z.number().min(0).max(2).optional(),
});

const usagePeriodSchema = z.enum(["7d", "30d"]);

// ── integrations schemas ──────────────────────────────────────────────────────

const categorySchema = z.enum(["oauth", "free"]);

const oauthStartSchema = z.object({
  provider_slug: z.enum(["claude-code", "antigravity"]),
  redirect_uri: z.string().url(),
  label: z.string().trim().min(1).max(80).optional(),
});

const oauthCompleteSchema = z.object({
  flow_id: z.string().uuid(),
  code: z.string().min(1),
  state: z.string().min(1),
});

const connectCredentialSchema = z.object({
  provider_slug: z.string().min(1),
  auth_type: z.literal("api_key"),
  credential: z.string().trim().min(4),
  label: z.string().trim().min(1).max(80).optional(),
});

const accountIdSchema = z.object({
  account_id: z.string().uuid(),
});

const toggleAccountSchema = z.object({
  account_id: z.string().uuid(),
  status: z.enum(["healthy", "degraded"]),
});

const testModelsSchema = z.object({
  account_id: z.string().uuid(),
  external_ids: z.array(z.string()).min(1),
});

const setModelsEnabledSchema = z.object({
  account_id: z.string().uuid(),
  enabled: z.record(z.string(), z.boolean()),
});

// ── metrics helper ────────────────────────────────────────────────────────────

const DAY_LABELS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"] as const;

function buildTraffic7d(records: { created_at: string }[]): { day: string; requests: number }[] {
  const buckets = new Map<string, number>();
  const now = new Date();
  for (let i = 6; i >= 0; i--) {
    const d = new Date(now);
    d.setDate(d.getDate() - i);
    d.setHours(0, 0, 0, 0);
    buckets.set(d.toISOString().slice(0, 10), 0);
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

async function handleGetMetrics(supabase: SupabaseClient): Promise<unknown> {
  const { ACCOUNT_MODELS_SELECT, mapJoinToCatalogRow } =
    await import("@/lib/providers/catalog-queries.server");
  const { aggregateCatalogModels } = await import("@/lib/providers/integrations.service");

  const sevenDaysAgo = new Date(Date.now() - 7 * 86400000).toISOString();

  const [
    accountModelRows,
    venomModels,
    routingRules,
    apiKeys,
    usage7d,
    auditLog,
    accounts,
    allAccounts,
  ] = await Promise.all([
    supabase.from("account_models").select(ACCOUNT_MODELS_SELECT),
    supabase.from("venom_models").select("id", { count: "exact", head: true }),
    supabase.from("routing_rules").select("id", { count: "exact", head: true }).eq("active", true),
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

  const catalog = aggregateCatalogModels(
    (accountModelRows.data ?? []).map((row: any) => mapJoinToCatalogRow(row)) as any,
  );
  const usageRecords = usage7d.data ?? [];
  const accountRows = accounts.data ?? [];

  const modelsEnabledByAccount: Record<string, number> = {};
  for (const row of accountModelRows.data ?? []) {
    const join = row as any;
    if (join.enabled)
      modelsEnabledByAccount[join.account_id] = (modelsEnabledByAccount[join.account_id] ?? 0) + 1;
  }

  const distributionMap = new Map<string, number>();
  for (const slug of ["lite", "pro", "max"]) distributionMap.set(slug, 0);
  for (const r of usageRecords) {
    const slug = r.venom_slug as string;
    if (distributionMap.has(slug)) distributionMap.set(slug, (distributionMap.get(slug) ?? 0) + 1);
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

  return {
    kpis: {
      provider_models: catalog.length,
      venom_models: venomModels.count ?? 0,
      routing_rules: routingRules.count ?? 0,
      api_keys: apiKeys.count ?? 0,
    },
    working_models: catalog.filter((m: any) => m.test_status === "working").length,
    traffic_7d: buildTraffic7d(usageRecords),
    distribution: [...distributionMap.entries()].map(([slug, requests]) => ({ slug, requests })),
    checklist: {
      owner_created: true,
      provider_connected: allAccountRows.length > 0,
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
    accounts_total: allAccountRows.length,
  };
}

// ── integrations helpers ──────────────────────────────────────────────────────

async function handleListIntegrations(supabase: SupabaseClient, category: "oauth" | "free") {
  const { data: providers, error } = await supabase
    .from("providers")
    .select(
      "id,slug,name,category,auth_type,description,homepage,base_url,is_builtin,created_at,accounts(id,label,email,plan,status,quota_used,quota_total,quota_unit,quota_extra,last_synced_at,last_health_check_at)",
    )
    .eq("category", category)
    .order("created_at", { ascending: true });
  if (error) throw new Error(error.message);

  const accountIds: string[] = (providers ?? []).flatMap((p: any) =>
    (p.accounts ?? []).map((a: any) => a.id),
  );
  const modelsByAccount: Record<string, { total: number; enabled: number }> = {};
  if (accountIds.length) {
    const { data: m } = await supabase
      .from("account_models")
      .select("account_id,enabled")
      .in("account_id", accountIds);
    for (const row of m ?? []) {
      const k = row.account_id as string;
      if (!modelsByAccount[k]) modelsByAccount[k] = { total: 0, enabled: 0 };
      modelsByAccount[k].total++;
      if (row.enabled) modelsByAccount[k].enabled++;
    }
  }

  return (providers ?? []).map((p: any) => ({
    ...p,
    accounts: (p.accounts ?? []).map((a: any) => ({
      ...a,
      modelsTotal: modelsByAccount[a.id]?.total ?? 0,
      modelsEnabled: modelsByAccount[a.id]?.enabled ?? 0,
    })),
  }));
}

async function handleStartOAuthFlow(
  supabase: SupabaseClient,
  body: { provider_slug: "claude-code" | "antigravity"; redirect_uri: string; label?: string },
) {
  const { data: provider, error: pErr } = await supabase
    .from("providers")
    .select("id,slug,category")
    .eq("slug", body.provider_slug)
    .single();
  if (pErr || !provider) throw new Error("Provider not found");
  if (provider.category !== "oauth") throw new Error("Not an OAuth provider");

  let flow: {
    authorize_url: string;
    redirect_uri: string;
    code_verifier?: string;
    state: string;
    client?: unknown;
  };
  if (body.provider_slug === "claude-code") {
    const a = await import("@/lib/providers/adapters/claude-code.server");
    flow = a.startFlow({ redirect_uri: body.redirect_uri });
  } else {
    const a = await import("@/lib/providers/adapters/antigravity.server");
    flow = a.startFlow({ redirect_uri: body.redirect_uri });
  }

  const { data: row, error } = await supabase
    .from("oauth_flows")
    .insert({
      provider_slug: body.provider_slug,
      code_verifier: flow.code_verifier ?? null,
      state: flow.state,
      redirect_uri: flow.redirect_uri,
      extra: { label: body.label, client: flow.client ?? null },
    })
    .select("id")
    .single();
  if (error) throw new Error(error.message);

  return { flow_id: row.id, authorize_url: flow.authorize_url };
}

async function handleCompleteOAuthFlow(
  supabase: SupabaseClient,
  body: { flow_id: string; code: string; state: string },
) {
  const { data: flow, error: fErr } = await supabase
    .from("oauth_flows")
    .select("*")
    .eq("id", body.flow_id)
    .single();
  if (fErr || !flow) throw new Error("OAuth flow not found or expired");

  const ageMs = Date.now() - new Date(flow.created_at).getTime();
  if (ageMs > 10 * 60_000) {
    await supabase.from("oauth_flows").delete().eq("id", flow.id);
    throw new Error("OAuth flow expired — please try again");
  }

  if (flow.state !== body.state) {
    throw new Error("OAuth state mismatch");
  }

  const { data: provider, error: pErr } = await supabase
    .from("providers")
    .select("id,name,slug")
    .eq("slug", flow.provider_slug)
    .single();
  if (pErr || !provider) throw new Error("Provider not found");

  const { packCredentials } = await import("@/lib/credentials.server");
  let creds: any;

  if (flow.provider_slug === "claude-code") {
    const a = await import("@/lib/providers/adapters/claude-code.server");
    creds = await a.completeFlow({
      code: body.code,
      code_verifier: flow.code_verifier!,
      state: flow.state,
      redirect_uri: flow.redirect_uri!,
    });
  } else if (flow.provider_slug === "antigravity") {
    const a = await import("@/lib/providers/adapters/antigravity.server");
    creds = await a.completeFlow({
      code: body.code,
      code_verifier: flow.code_verifier!,
      state: flow.state,
      redirect_uri: flow.redirect_uri!,
    });
  } else {
    throw new Error("Unsupported OAuth provider");
  }

  const packed = packCredentials(creds);
  const label = (flow.extra as any)?.label ?? provider.name;

  const { data: account, error: aErr } = await supabase
    .from("accounts")
    .insert({
      provider_id: provider.id,
      label,
      auth_type: "oauth2",
      credentials_enc: packed.credentials_enc,
      credentials_iv: packed.credentials_iv,
      credentials_tag: packed.credentials_tag,
      status: "healthy",
    })
    .select("id")
    .single();
  if (aErr) throw new Error(aErr.message);

  await supabase.from("oauth_flows").delete().eq("id", flow.id);

  const { syncAccountInternal, toSyncAccountWireResponse } =
    await import("@/lib/providers/integrations.service");

  let health: { ok: boolean; error?: string } = { ok: true };
  try {
    await syncAccountInternal(supabase, account.id);
  } catch (e: any) {
    health = { ok: false, error: e?.message ?? String(e) };
    await supabase
      .from("accounts")
      .update({
        status: "degraded",
        last_health_check_at: new Date().toISOString(),
        quota_extra: { health_error: health.error },
      })
      .eq("id", account.id);
  }

  return { ok: true, account_id: account.id, health };
}

async function handleConnectCredential(
  supabase: SupabaseClient,
  body: { provider_slug: string; auth_type: "api_key"; credential: string; label?: string },
) {
  const { data: provider, error: pErr } = await supabase
    .from("providers")
    .select("id,name,slug,category")
    .eq("slug", body.provider_slug)
    .single();
  if (pErr || !provider) throw new Error("Provider not found");
  if (provider.category === "oauth") {
    throw new Error(
      "OAuth providers must use the Connect button — paste API keys are not supported here",
    );
  }

  const { packCredentials } = await import("@/lib/credentials.server");
  const creds = {
    kind: "api_key" as const,
    api_key: body.credential,
    extra: { auth_type: body.auth_type, label: body.label },
  };
  const packed = packCredentials(creds);

  const { data: account, error: aErr } = await supabase
    .from("accounts")
    .insert({
      provider_id: provider.id,
      label: body.label ?? provider.name,
      auth_type: body.auth_type,
      credentials_enc: packed.credentials_enc,
      credentials_iv: packed.credentials_iv,
      credentials_tag: packed.credentials_tag,
      status: "healthy",
    })
    .select("id")
    .single();
  if (aErr) throw new Error(aErr.message);

  const { syncAccountInternal } = await import("@/lib/providers/integrations.service");

  let health: { ok: boolean; error?: string } = { ok: true };
  try {
    await syncAccountInternal(supabase, account.id);
  } catch (e: any) {
    health = { ok: false, error: e?.message ?? String(e) };
    await supabase
      .from("accounts")
      .update({
        status: "degraded",
        last_health_check_at: new Date().toISOString(),
        quota_extra: { health_error: health.error },
      })
      .eq("id", account.id);
  }
  return { ok: true, account_id: account.id, health };
}

async function handleFetchModels(supabase: SupabaseClient, accountId: string) {
  const { unpackCredentials } = await import("@/lib/credentials.server");
  const { runProviderModelSync } = await import("@/lib/providers/integrations.service");
  const { data: acct, error } = await supabase
    .from("accounts")
    .select("id,provider_id,credentials_enc,credentials_iv,credentials_tag,providers(slug)")
    .eq("id", accountId)
    .single();
  if (error || !acct) throw new Error("Account not found");

  const slug = ((acct.providers as { slug?: string } | null)?.slug ?? "") as string;

  if (slug === "antigravity") {
    const { runAntigravityLiveSnapshotFetch } =
      await import("@/lib/providers/integrations.service");
    const snapshot = await runAntigravityLiveSnapshotFetch(supabase, accountId);
    return {
      slug: "antigravity",
      count: snapshot.stats.rawFetchedCount,
      ideVisible: snapshot.stats.visibleCount,
      added: snapshot.stats.insertedNewCount,
      updated: snapshot.stats.updatedExistingCount,
      unchanged: snapshot.stats.unchangedCount,
      tested: snapshot.stats.testedAfterFetchCount,
      working: snapshot.stats.workingCount,
      failed: snapshot.stats.failedCount,
      selected: snapshot.stats.selectedForRoutingCount,
      duplicatePrevented: snapshot.stats.duplicatePreventedCount,
      removed: snapshot.stats.removedStaleCount,
    };
  }

  let creds = unpackCredentials({
    credentials_enc: acct.credentials_enc,
    credentials_iv: acct.credentials_iv,
    credentials_tag: acct.credentials_tag,
  });

  let liveModels: {
    external_id: string;
    display_name: string;
    capabilities: string[];
    context_window?: number;
    quality_rating?: number;
  }[] = [];
  if (slug === "claude-code") {
    const a = await import("@/lib/providers/adapters/claude-code.server");
    creds = (await a.fetchIdentity(creds)).creds;
    liveModels = await a.listModels(creds);
  } else if (slug === "opencode-zen") {
    const a = await import("@/lib/providers/adapters/opencode-zen.server");
    liveModels = await a.listModels(creds);
  } else {
    throw new Error("Fetch models not supported for this provider");
  }

  const stats = await runProviderModelSync(supabase, {
    accountId,
    providerId: acct.provider_id,
    providerSlug: slug,
    liveModels,
    creds,
  });
  return {
    slug,
    count: stats.count,
    added: stats.added.length,
    linked: stats.linked,
    removed: stats.removed.length,
    unchanged: stats.unchanged,
    tested: stats.tested,
    failed: stats.failed,
  };
}

async function handleLoadAntigravityStoredSnapshot(supabase: SupabaseClient, accountId: string) {
  const { ACCOUNT_MODELS_SELECT, mapJoinToCatalogRow } =
    await import("@/lib/providers/catalog-queries.server");
  const { buildAntigravitySnapshotFromDbRows } =
    await import("@/lib/providers/antigravity-live-snapshot");
  const { extractCapabilityList } = await import("@/lib/providers/integrations.service");

  const { data: acct, error: acctErr } = await supabase
    .from("accounts")
    .select("id,plan,last_synced_at,quota_extra,providers(slug)")
    .eq("id", accountId)
    .single();
  if (acctErr || !acct) throw new Error("Account not found");
  const providerSlug = (acct.providers as { slug?: string } | null)?.slug;
  if (providerSlug !== "antigravity") {
    throw new Error("loadAntigravityStoredSnapshot is only for Antigravity accounts");
  }

  const { data: rows, error } = await supabase
    .from("account_models")
    .select(ACCOUNT_MODELS_SELECT)
    .eq("account_id", accountId)
    .order("display_name", { foreignTable: "models" });
  if (error) throw new Error(error.message);

  const extra = (acct.quota_extra ?? null) as Record<string, unknown> | null;
  const snapshot = buildAntigravitySnapshotFromDbRows(
    (rows ?? []).map((row: any) => {
      const mapped = mapJoinToCatalogRow(row);
      return {
        id: mapped.id,
        external_id: mapped.external_id,
        display_name: mapped.display_name,
        capabilities: mapped.capabilities as Record<string, unknown> | null,
        test_status: mapped.test_status,
        enabled: mapped.enabled,
        last_test_error: null,
        last_tested_at: mapped.last_tested_at,
        latency_ms: mapped.latency_ms,
        updated_at: mapped.last_tested_at,
      };
    }),
    {
      projectId: typeof extra?.projectId === "string" ? extra.projectId : undefined,
      planTier: (acct.plan as string | null) ?? undefined,
      lastSyncedAt: acct.last_synced_at as string | null,
    },
  );

  return snapshot;
}

async function handleDiagnoseAntigravityFetch(supabase: SupabaseClient, accountId: string) {
  const { unpackCredentials } = await import("@/lib/credentials.server");
  const { data: acct, error } = await supabase
    .from("accounts")
    .select("id,credentials_enc,credentials_iv,credentials_tag,providers(slug)")
    .eq("id", accountId)
    .single();
  if (error || !acct) throw new Error("Account not found");
  if ((acct.providers as any)?.slug !== "antigravity") {
    throw new Error("diagnoseAntigravityFetch is only for Antigravity accounts");
  }
  const credsIn = unpackCredentials({
    credentials_enc: acct.credentials_enc,
    credentials_iv: acct.credentials_iv,
    credentials_tag: acct.credentials_tag,
  });
  const adapter = await import("@/lib/providers/adapters/antigravity.server");
  return adapter.diagnoseAntigravityFetch(credsIn);
}

async function handleListCatalogModels(supabase: SupabaseClient) {
  const { ACCOUNT_MODELS_SELECT, mapJoinToCatalogRow } =
    await import("@/lib/providers/catalog-queries.server");
  const { aggregateCatalogModels } = await import("@/lib/providers/integrations.service");

  const { data: rows, error } = await supabase
    .from("account_models")
    .select(ACCOUNT_MODELS_SELECT)
    .order("display_name", { foreignTable: "models" });
  if (error) throw new Error(error.message);
  return aggregateCatalogModels((rows ?? []).map((row: any) => mapJoinToCatalogRow(row)));
}

async function handleListAccountModels(supabase: SupabaseClient, accountId: string) {
  const { ACCOUNT_MODELS_SELECT, mapJoinToAccountModelView } =
    await import("@/lib/providers/catalog-queries.server");
  const { extractCapabilityList } = await import("@/lib/providers/integrations.service");

  const { data: rows, error } = await supabase
    .from("account_models")
    .select(`${ACCOUNT_MODELS_SELECT}`)
    .eq("account_id", accountId)
    .order("display_name", { foreignTable: "models" });
  if (error) throw new Error(error.message);
  return (rows ?? []).map((row: any) => {
    const mapped = mapJoinToAccountModelView(row);
    return {
      ...mapped,
      capabilities: extractCapabilityList(mapped.capabilities),
    };
  });
}

async function handleTestAccountModels(
  supabase: SupabaseClient,
  body: { account_id: string; external_ids: string[] },
) {
  const { unpackCredentials } = await import("@/lib/credentials.server");
  const { updateAccountModelTestResult } = await import("@/lib/providers/model-sync.server");

  const { data: acct } = await supabase
    .from("accounts")
    .select("credentials_enc,credentials_iv,credentials_tag,provider_id,providers(slug)")
    .eq("id", body.account_id)
    .single();
  if (!acct) throw new Error("Account not found");
  const slug = (acct as any).providers?.slug as string;
  const creds = unpackCredentials({
    credentials_enc: acct.credentials_enc,
    credentials_iv: acct.credentials_iv,
    credentials_tag: acct.credentials_tag,
  });
  const adapter =
    slug === "claude-code"
      ? await import("@/lib/providers/adapters/claude-code.server")
      : slug === "antigravity"
        ? await import("@/lib/providers/adapters/antigravity.server")
        : await import("@/lib/providers/adapters/opencode-zen.server");

  const results = await Promise.all(
    body.external_ids.map(async (ext) => {
      const r = await adapter.testModel(creds, ext);
      const { data: modelRow } = await supabase
        .from("models")
        .select("id")
        .eq("provider_id", acct.provider_id)
        .eq("external_id", ext)
        .maybeSingle();
      if (modelRow?.id) {
        await updateAccountModelTestResult(supabase, body.account_id, modelRow.id, r);
      }
      return r;
    }),
  );
  return results;
}

async function handleSetModelsEnabled(
  supabase: SupabaseClient,
  body: { account_id: string; enabled: Record<string, boolean> },
) {
  await Promise.all(
    Object.entries(body.enabled).map(([id, enabled]) =>
      supabase.from("account_models").update({ enabled }).eq("id", id),
    ),
  );
  return { ok: true };
}

async function handleDisconnectAccount(supabase: SupabaseClient, accountId: string) {
  const { unlinkAccountModels } = await import("@/lib/providers/model-sync.server");
  await unlinkAccountModels(supabase, accountId);
  const { error } = await supabase.from("accounts").delete().eq("id", accountId);
  if (error) throw new Error(error.message);
  return { ok: true };
}

async function handleToggleAccount(
  supabase: SupabaseClient,
  body: { account_id: string; status: "healthy" | "degraded" },
) {
  const { error } = await supabase
    .from("accounts")
    .update({ status: body.status })
    .eq("id", body.account_id);
  if (error) throw new Error(error.message);
  return { ok: true };
}

async function handlePlaygroundChat(body: z.infer<typeof playgroundChatSchema>) {
  const { routeRequest } = await import("@/lib/routing/engine.server");
  const { routingErrorMessage } = await import("@/components/routing/routing-constants");
  const t0 = Date.now();

  const result = await routeRequest({
    venomSlug: body.venom_slug,
    messages: body.messages as import("@/lib/providers/adapters/types").ChatMessage[],
    maxTokens: body.max_tokens,
    temperature: body.temperature,
    includeTrace: true,
  });

  const wireRequest = {
    venom_slug: body.venom_slug,
    messages: body.messages,
    ...(body.max_tokens != null ? { max_tokens: body.max_tokens } : {}),
    ...(body.temperature != null ? { temperature: body.temperature } : {}),
  };

  if (!result.success) {
    const errorCode = result.errorCode ?? "Routing failed";
    const errorMessage = routingErrorMessage(errorCode, body.venom_slug);
    throw Object.assign(new Error(errorMessage), {
      status: 422,
      payload: {
        error: errorCode,
        error_code: result.errorCode,
        error_message: errorMessage,
        trace: result.trace ?? null,
        request: wireRequest,
      },
    });
  }

  return {
    content: result.content ?? "",
    input_tokens: result.inputTokens,
    output_tokens: result.outputTokens,
    latency_ms: Date.now() - t0,
    provider_adapter: result.providerAdapter ?? null,
    fallback_used: result.fallbackUsed,
    fallback_count: result.fallbackCount,
    cost_usd: result.costUsd ?? 0,
    modality: result.modality,
    trace: result.trace ?? null,
    request: wireRequest,
  };
}

async function handleGetUsageAnalytics(
  supabase: SupabaseClient,
  period: z.infer<typeof usagePeriodSchema>,
) {
  const { getUsageAnalytics } = await import("@/lib/db/usage.server");
  const days = period === "30d" ? 30 : 7;
  return getUsageAnalytics(supabase, { days });
}

/**
 * Aggregate diagnostic signals: degraded/unreachable accounts, recent failed
 * routing traces, and health-check run stats. All queries run concurrently and
 * are read-only — diagnostics is a pure status view.
 */
async function handleGetDiagnostics(supabase: SupabaseClient) {
  const twentyFourHoursAgo = new Date(Date.now() - 24 * 86400000).toISOString();

  const [degradedAccounts, failedTraces, healthStats, recentHealthChecks] = await Promise.all([
    supabase
      .from("accounts")
      .select("id,label,email,status,last_health_check_at,quota_extra,providers(slug,name)")
      .in("status", ["degraded", "expired", "unreachable"])
      .order("last_health_check_at", { ascending: false, nullsFirst: false })
      .limit(50),
    supabase
      .from("routing_traces")
      .select(
        "id,request_id,venom_slug,reason,decision_reason,candidates_evaluated,candidates_filtered,fallback_attempts,created_at",
      )
      .eq("success", false)
      .gte("created_at", twentyFourHoursAgo)
      .order("created_at", { ascending: false })
      .limit(50),
    supabase
      .from("account_health_checks")
      .select("status", { count: "exact", head: true })
      .gte("checked_at", twentyFourHoursAgo),
    supabase.from("account_health_checks").select("status").gte("checked_at", twentyFourHoursAgo),
  ]);

  const recentRows = (recentHealthChecks.data ?? []) as Array<{ status: string }>;
  const counts = recentRows.reduce<Record<string, number>>(
    (acc, r) => {
      acc[r.status] = (acc[r.status] ?? 0) + 1;
      return acc;
    },
    { healthy: 0, degraded: 0, unreachable: 0 },
  );

  const degraded = ((degradedAccounts.data ?? []) as Array<any>).map((a) => ({
    id: a.id as string,
    label: (a.label as string | null) ?? null,
    email: (a.email as string | null) ?? null,
    status: a.status as string,
    provider_slug: ((a.providers as { slug?: string } | null)?.slug ?? "") as string,
    provider_name: ((a.providers as { name?: string } | null)?.name ?? "") as string,
    last_health_check_at: (a.last_health_check_at as string | null) ?? null,
    quota_extra: (a.quota_extra as Record<string, unknown> | null) ?? null,
  }));

  const traces = ((failedTraces.data ?? []) as Array<any>).map((t) => ({
    id: t.id as string,
    request_id: (t.request_id as string | null) ?? null,
    venom_slug: t.venom_slug as string,
    reason: (t.reason as string) ?? "",
    decision_reason: (t.decision_reason as string | null) ?? null,
    candidates_evaluated: (t.candidates_evaluated as number) ?? 0,
    candidates_filtered: (t.candidates_filtered as number) ?? 0,
    fallback_attempts: (t.fallback_attempts as number) ?? 0,
    created_at: t.created_at as string,
  }));

  return {
    degraded_accounts: degraded,
    failed_traces: traces,
    health_check_runs: {
      total: healthStats.count ?? counts.healthy + counts.degraded + counts.unreachable,
      healthy: counts.healthy,
      degraded: counts.degraded,
      unreachable: counts.unreachable,
    },
  };
}

// ── main handler ──────────────────────────────────────────────────────────────

export async function handleDashboardAPI(request: Request): Promise<Response | null> {
  const url = new URL(request.url);
  const path = url.pathname;

  if (!path.startsWith("/api/dashboard/")) return null;

  const method = request.method;

  // OPTIONS passthrough
  if (method === "OPTIONS") {
    return new Response(null, {
      status: 204,
      headers: { Allow: "GET, POST, PATCH, DELETE, OPTIONS" },
    });
  }

  // Auth
  let supabase: SupabaseClient;
  try {
    const auth = await requireDashboardAuth(request);
    supabase = auth.supabase;
  } catch (e: any) {
    return err(e.message ?? "Unauthorized", e.status ?? 401);
  }

  // Parse route: /api/dashboard/{resource}/{id?}/{sub?}/{subId?}
  const parts = path.slice("/api/dashboard/".length).split("/").filter(Boolean);
  const resource = parts[0];
  const id = parts[1];
  const sub = parts[2];
  const subId = parts[3];

  try {
    // ── GET /api/dashboard/metrics ────────────────────────────────────────────
    if (resource === "metrics" && !id && method === "GET") {
      return ok(await handleGetMetrics(supabase));
    }

    // ── GET /api/dashboard/providers ─────────────────────────────────────────
    if (resource === "providers" && !id && method === "GET") {
      const { data, error } = await supabase
        .from("providers")
        .select("id,name,kind,adapter,base_url,created_at,accounts(id,label,status,quota_strategy)")
        .order("created_at", { ascending: false });
      if (error) throw new Error(error.message);
      return ok(data ?? []);
    }

    // ── GET /api/dashboard/integrations?category=oauth|free ───────────────────
    if (resource === "integrations" && !id && method === "GET") {
      const category = categorySchema.parse(url.searchParams.get("category") ?? undefined);
      return ok(await handleListIntegrations(supabase, category));
    }

    // ── POST /api/dashboard/oauth/start ───────────────────────────────────────
    if (resource === "oauth" && id === "start" && method === "POST") {
      const body = oauthStartSchema.parse(await parseBody(request));
      return ok(await handleStartOAuthFlow(supabase, body));
    }

    // ── POST /api/dashboard/oauth/complete ────────────────────────────────────
    if (resource === "oauth" && id === "complete" && method === "POST") {
      const body = oauthCompleteSchema.parse(await parseBody(request));
      return ok(await handleCompleteOAuthFlow(supabase, body));
    }

    // ── POST /api/dashboard/credentials/connect ───────────────────────────────
    if (resource === "credentials" && id === "connect" && method === "POST") {
      const body = connectCredentialSchema.parse(await parseBody(request));
      return ok(await handleConnectCredential(supabase, body));
    }

    // ── venom-models ─────────────────────────────────────────────────────────
    if (resource === "venom-models") {
      if (!id && method === "GET") {
        const { listVenomModels } = await import("@/lib/db/venom.server");
        return ok(await listVenomModels(supabase));
      }
      if (id && method === "PATCH") {
        const validSlugs = ["lite", "pro", "max"];
        if (!validSlugs.includes(id)) return err("Invalid slug", 400);
        const body = updateVenomSchema.parse(await parseBody(request));
        if (Object.keys(body).length === 0) return err("No fields to update", 400);
        const { error } = await supabase.from("venom_models").update(body).eq("slug", id);
        if (error) throw new Error(error.message);
        return ok({ ok: true });
      }
    }

    // ── api-keys ─────────────────────────────────────────────────────────────
    if (resource === "api-keys") {
      if (!id) {
        if (method === "GET") {
          const { listApiKeys } = await import("@/lib/db/api-keys.server");
          return ok(await listApiKeys(supabase));
        }
        if (method === "POST") {
          const body = createKeySchema.parse(await parseBody(request));
          const { generateApiKey } = await import("@/lib/crypto.server");
          const { raw, prefix, hash } = generateApiKey();
          const { data: row, error } = await supabase
            .from("venom_api_keys")
            .insert({
              name: body.name,
              allowed_models: body.allowed_models,
              rpm_limit: body.rpm_limit,
              tpd_limit: body.tpd_limit,
              monthly_cap_usd: body.monthly_cap_usd,
              key_prefix: prefix,
              key_hash: hash,
            })
            .select("id")
            .single();
          if (error) throw new Error(error.message);
          return ok({ id: (row as any).id, raw, prefix }, 201);
        }
      } else {
        if (method === "PATCH") {
          // revoke
          const { error } = await supabase
            .from("venom_api_keys")
            .update({ revoked_at: new Date().toISOString() })
            .eq("id", id);
          if (error) throw new Error(error.message);
          return ok({ ok: true });
        }
        if (method === "DELETE") {
          const { error } = await supabase.from("venom_api_keys").delete().eq("id", id);
          if (error) throw new Error(error.message);
          return ok({ ok: true });
        }
      }
    }

    // ── routing-rules ─────────────────────────────────────────────────────────
    if (resource === "routing-rules") {
      if (!id) {
        if (method === "GET") {
          const { listRoutingRules } = await import("@/lib/db/venom.server");
          return ok(await listRoutingRules(supabase));
        }
        if (method === "POST") {
          const body = createRuleSchema.parse(await parseBody(request));
          const insert: Record<string, unknown> = {
            venom_slug: body.venom_slug,
            model_id: body.model_id,
            account_id: body.account_id,
            priority: body.priority,
            role: body.role,
            active: body.active,
          };
          if (body.condition) insert.condition = body.condition;
          const { error } = await supabase.from("routing_rules").insert(insert);
          if (error) throw new Error(error.message);
          return ok({ ok: true }, 201);
        }
      } else {
        if (method === "PATCH") {
          const raw = await parseBody(request);
          const parsed = updateRuleSchema.safeParse(raw);
          if (parsed.success) {
            const body = parsed.data;
            const patch: Record<string, unknown> = {};
            if (body.priority !== undefined) patch.priority = body.priority;
            if (body.role !== undefined) patch.role = body.role;
            if (body.active !== undefined) patch.active = body.active;
            if (body.condition !== undefined) patch.condition = body.condition;
            const { error } = await supabase.from("routing_rules").update(patch).eq("id", id);
            if (error) throw new Error(error.message);
            return ok({ ok: true });
          }
          const { active } = toggleRuleSchema.parse(raw);
          const { error } = await supabase.from("routing_rules").update({ active }).eq("id", id);
          if (error) throw new Error(error.message);
          return ok({ ok: true });
        }
        if (method === "DELETE") {
          const { error } = await supabase.from("routing_rules").delete().eq("id", id);
          if (error) throw new Error(error.message);
          return ok({ ok: true });
        }
      }
    }

    // ── account-models ────────────────────────────────────────────────────────
    if (resource === "account-models" && !id && method === "GET") {
      const { data, error } = await supabase
        .from("account_models")
        .select(
          "id,account_id,model_id,models!inner(external_id,display_name,providers!inner(slug,name)),accounts!inner(email,label)",
        )
        .eq("lifecycle", "approved")
        .order("created_at", { ascending: false });
      if (error) throw new Error(error.message);
      return ok(
        ((data ?? []) as any[]).map((row) => ({
          id: row.id as string,
          account_id: row.account_id as string,
          model_id: row.model_id as string,
          model_external_id: (row.models?.external_id ?? "") as string,
          model_display_name: (row.models?.display_name ?? "") as string,
          provider_slug: (row.models?.providers?.slug ?? "") as string,
          provider_name: (row.models?.providers?.name ?? "") as string,
          account_email: (row.accounts?.email ?? null) as string | null,
          account_label: (row.accounts?.label ?? null) as string | null,
        })),
      );
    }

    // ── catalog-models ────────────────────────────────────────────────────────
    if (resource === "catalog-models" && !id && method === "GET") {
      return ok(await handleListCatalogModels(supabase));
    }

    // ── account-scoped actions: /api/dashboard/accounts/{id}/{sub}/{subId?} ────
    if (resource === "accounts" && id) {
      // ── POST /api/dashboard/accounts/:id/sync
      if (sub === "sync" && method === "POST") {
        const { syncAccountInternal, toSyncAccountWireResponse } =
          await import("@/lib/providers/integrations.service");
        const result = await syncAccountInternal(supabase, id);
        return ok(toSyncAccountWireResponse(result));
      }

      // ── POST /api/dashboard/accounts/:id/fetch-models
      if (sub === "fetch-models" && method === "POST") {
        return ok(await handleFetchModels(supabase, id));
      }

      // ── POST /api/dashboard/accounts/:id/antigravity/live-snapshot
      if (sub === "antigravity" && subId === "live-snapshot" && method === "POST") {
        const { runAntigravityLiveSnapshotFetch } =
          await import("@/lib/providers/integrations.service");
        return ok(await runAntigravityLiveSnapshotFetch(supabase, id));
      }

      // ── GET /api/dashboard/accounts/:id/antigravity/stored-snapshot
      if (sub === "antigravity" && subId === "stored-snapshot" && method === "GET") {
        return ok(await handleLoadAntigravityStoredSnapshot(supabase, id));
      }

      // ── POST /api/dashboard/accounts/:id/antigravity/diagnose
      if (sub === "antigravity" && subId === "diagnose" && method === "POST") {
        return ok(await handleDiagnoseAntigravityFetch(supabase, id));
      }

      // ── GET /api/dashboard/accounts/:id/models
      if (sub === "models" && !subId && method === "GET") {
        return ok(await handleListAccountModels(supabase, id));
      }

      // ── POST /api/dashboard/accounts/:id/models/test
      if (sub === "models" && subId === "test" && method === "POST") {
        const body = testModelsSchema.parse(await parseBody(request));
        return ok(await handleTestAccountModels(supabase, body));
      }

      // ── POST /api/dashboard/accounts/:id/models/enabled
      if (sub === "models" && subId === "enabled" && method === "POST") {
        const body = setModelsEnabledSchema.parse(await parseBody(request));
        return ok(await handleSetModelsEnabled(supabase, body));
      }

      // ── POST /api/dashboard/accounts/:id/disconnect
      if (sub === "disconnect" && method === "POST") {
        return ok(await handleDisconnectAccount(supabase, id));
      }

      // ── POST /api/dashboard/accounts/:id/toggle
      if (sub === "toggle" && method === "POST") {
        const body = toggleAccountSchema.parse(await parseBody(request));
        return ok(await handleToggleAccount(supabase, body));
      }
    }

    // ── GET /api/dashboard/usage?period=7d|30d ────────────────────────────────
    if (resource === "usage" && !id && method === "GET") {
      const period = usagePeriodSchema.parse(url.searchParams.get("period") ?? "7d");
      return ok(await handleGetUsageAnalytics(supabase, period));
    }

    // ── GET /api/dashboard/diagnostics ────────────────────────────────────────
    if (resource === "diagnostics" && !id && method === "GET") {
      return ok(await handleGetDiagnostics(supabase));
    }

    // ── POST /api/dashboard/playground/chat ───────────────────────────────────
    if (resource === "playground" && id === "chat" && method === "POST") {
      const body = playgroundChatSchema.parse(await parseBody(request));
      return ok(await handlePlaygroundChat(body));
    }

    // ── quotas ────────────────────────────────────────────────────────────────
    if (resource === "quotas") {
      if (!id && method === "GET") {
        const { data: accts, error: acctErr } = await supabase
          .from("accounts")
          .select(
            "id,label,status,quota_strategy,provider_id,last_health_check_at,providers(name,kind)",
          )
          .order("created_at", { ascending: false });
        if (acctErr) throw new Error(acctErr.message);
        const ids = (accts ?? []).map((a) => a.id);
        const { data: quotas } = ids.length
          ? await supabase.from("quotas").select("*").in("account_id", ids)
          : { data: [] };
        const byId = new Map((quotas ?? []).map((q) => [q.account_id, q]));
        return ok((accts ?? []).map((a) => ({ ...a, quota: byId.get(a.id) ?? null })));
      }
      if (!id && method === "POST") {
        const body = quotaSchema.parse(await parseBody(request));
        const { error } = await supabase
          .from("quotas")
          .upsert({ ...body, updated_at: new Date().toISOString() }, { onConflict: "account_id" });
        if (error) throw new Error(error.message);
        return ok({ ok: true });
      }
    }

    return err("Not found", 404);
  } catch (e: any) {
    if (e instanceof z.ZodError) {
      return err(`Validation error: ${e.errors[0]?.message ?? "invalid input"}`, 400);
    }
    if (e.payload && typeof e.status === "number") {
      return Response.json(e.payload, { status: e.status });
    }
    const status = typeof e.status === "number" ? e.status : 500;
    if (status < 500) return err(e.message, status);
    log.error("unhandled dashboard error", {
      path,
      error: e instanceof Error ? e.message : String(e),
    });
    return err(e.message ?? "Internal error");
  }
}
