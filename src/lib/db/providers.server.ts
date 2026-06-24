import type { SupabaseClient } from "@supabase/supabase-js";

// ── Types ──────────────────────────────────────────────────────────────────────

export type AccountStatus = "healthy" | "degraded" | "expired";

export type AccountInfo = {
  id: string;
  email: string | null;
  label: string | null;
  plan: string | null;
  status: AccountStatus;
  provider_slug: string;
  provider_name: string;
  auth_type: string;
  last_synced_at: string | null;
  last_health_check_at: string | null;
};

export type QuotaGroup = {
  name: string;
  short_label: string;
  model_count: number;
  five_hour?: {
    remaining_pct: number;
    reset_at: string;
    exhausted: boolean;
  };
};

export type AccountQuota = {
  account_id: string;
  used: number | null;
  total: number | null;
  unit: string | null;
  groups: QuotaGroup[];
  resets_at: string | null;
  confidence: "high" | "medium" | "low" | null;
};

export type AccountModel = {
  id: string;
  external_id: string;
  display_name: string;
  capabilities: string[];
  enabled: boolean;
  test_status: "working" | "failed" | "untested";
  latency_ms: number | null;
  last_tested_at: string | null;
  lifecycle: string;
};

export type ModelCheckResult = {
  external_id: string;
  ok: boolean;
  latency_ms: number;
  error?: string;
};

export type ProviderHealth = {
  provider_slug: string;
  provider_name: string;
  accounts_total: number;
  accounts_healthy: number;
  accounts_degraded: number;
  accounts_expired: number;
  is_healthy: boolean;
};

// ── Internal helpers ───────────────────────────────────────────────────────────

const QUOTA_SHORT_LABELS: Record<string, string> = {
  "Gemini Models": "GEM",
  "Claude and GPT Models": "OPT",
};

function extractQuotaGroups(extra: Record<string, unknown> | null): QuotaGroup[] {
  const raw =
    (extra?.groups as
      | Array<{
          name: string;
          modelIds?: string[];
          fiveHourQuota?: {
            remainingFraction?: number;
            resetTime?: string;
            isExhausted?: boolean;
          };
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

function extractCapabilities(caps: Record<string, unknown> | null): string[] {
  if (!caps) return [];
  if (Array.isArray(caps.list)) return caps.list as string[];
  return Object.entries(caps)
    .filter(([k]) => /^\d+$/.test(k))
    .sort(([a], [b]) => Number(a) - Number(b))
    .map(([, v]) => String(v));
}

// ── Exported functions ─────────────────────────────────────────────────────────

export async function getAccountStatus(
  supabase: SupabaseClient,
  accountId: string,
): Promise<AccountStatus> {
  const { data, error } = await supabase
    .from("accounts")
    .select("status")
    .eq("id", accountId)
    .single();
  if (error || !data) throw new Error(`getAccountStatus: ${error?.message ?? "not found"}`);
  return (data as any).status as AccountStatus;
}

export async function getAccountInfo(
  supabase: SupabaseClient,
  accountId: string,
): Promise<AccountInfo> {
  const { data, error } = await supabase
    .from("accounts")
    .select(
      "id,email,label,plan,status,auth_type,last_synced_at,last_health_check_at,providers(slug,name)",
    )
    .eq("id", accountId)
    .single();
  if (error || !data) throw new Error(`getAccountInfo: ${error?.message ?? "not found"}`);
  const row = data as any;
  const p = row.providers as { slug: string; name: string } | null;
  return {
    id: row.id,
    email: row.email,
    label: row.label,
    plan: row.plan,
    status: row.status as AccountStatus,
    provider_slug: p?.slug ?? "",
    provider_name: p?.name ?? "",
    auth_type: row.auth_type,
    last_synced_at: row.last_synced_at,
    last_health_check_at: row.last_health_check_at,
  };
}

export async function getAccountQuota(
  supabase: SupabaseClient,
  accountId: string,
): Promise<AccountQuota> {
  const { data: acct, error: acctErr } = await supabase
    .from("accounts")
    .select("id,quota_used,quota_total,quota_unit,quota_extra")
    .eq("id", accountId)
    .single();
  if (acctErr || !acct) throw new Error(`getAccountQuota: ${acctErr?.message ?? "not found"}`);

  const { data: quotaRow } = await supabase
    .from("quotas")
    .select("confidence,resets_at")
    .eq("account_id", accountId)
    .maybeSingle();

  const extra = ((acct as any).quota_extra ?? null) as Record<string, unknown> | null;
  return {
    account_id: accountId,
    used: (acct as any).quota_used ?? null,
    total: (acct as any).quota_total ?? null,
    unit: (acct as any).quota_unit ?? null,
    groups: extractQuotaGroups(extra),
    resets_at: (quotaRow as any)?.resets_at ?? null,
    confidence: ((quotaRow as any)?.confidence ?? null) as "high" | "medium" | "low" | null,
  };
}

export async function getAccountModels(
  supabase: SupabaseClient,
  accountId: string,
  opts?: { enabledOnly?: boolean; lifecycle?: string },
): Promise<AccountModel[]> {
  let q = supabase
    .from("account_models")
    .select(
      "id,enabled,test_status,lifecycle,latency_ms,last_tested_at,models!inner(external_id,display_name,capabilities)",
    )
    .eq("account_id", accountId);
  if (opts?.enabledOnly) q = (q as any).eq("enabled", true);
  if (opts?.lifecycle) q = (q as any).eq("lifecycle", opts.lifecycle);

  const { data, error } = await q;
  if (error) throw new Error(`getAccountModels: ${error.message}`);

  return ((data ?? []) as any[]).map((row) => {
    const model = row.models;
    return {
      id: row.id,
      external_id: model?.external_id ?? "",
      display_name: model?.display_name ?? "",
      capabilities: extractCapabilities(model?.capabilities ?? null),
      enabled: row.enabled,
      test_status: (row.test_status ?? "untested") as "working" | "failed" | "untested",
      latency_ms: row.latency_ms ?? null,
      last_tested_at: row.last_tested_at ?? null,
      lifecycle: row.lifecycle,
    };
  });
}

// Note: checkAccountModels performs live provider API calls (not unit tested — integration concern)
export async function checkAccountModels(
  supabase: SupabaseClient,
  accountId: string,
  externalIds?: string[],
): Promise<ModelCheckResult[]> {
  const { data: acct, error } = await supabase
    .from("accounts")
    .select("credentials_enc,credentials_iv,credentials_tag,providers(slug)")
    .eq("id", accountId)
    .single();
  if (error || !acct) throw new Error(`checkAccountModels: ${error?.message ?? "not found"}`);

  const slug = ((acct as any).providers as { slug?: string } | null)?.slug ?? "";
  const { unpackCredentials } = await import("@/lib/credentials.server");
  const creds = unpackCredentials(acct as any);

  let targets = externalIds;
  if (!targets) {
    const models = await getAccountModels(supabase, accountId, { enabledOnly: true });
    targets = models.map((m) => m.external_id);
  }

  if (!["claude-code", "antigravity", "opencode-zen"].includes(slug)) {
    throw new Error(`checkAccountModels: unknown provider slug "${slug}"`);
  }

  const adapter =
    slug === "claude-code"
      ? await import("@/lib/providers/adapters/claude-code.server")
      : slug === "antigravity"
        ? await import("@/lib/providers/adapters/antigravity.server")
        : await import("@/lib/providers/adapters/opencode-zen.server");

  return Promise.all(
    targets.map(async (ext) => {
      const r = await adapter.testModel(creds, ext);
      return { external_id: ext, ok: r.ok, latency_ms: r.latency_ms ?? 0, error: r.error };
    }),
  );
}

export async function getProviderHealth(
  supabase: SupabaseClient,
  opts?: { providerSlug?: string },
): Promise<ProviderHealth[]> {
  let q = supabase.from("providers").select("slug,name,accounts(status)");
  if (opts?.providerSlug) q = (q as any).eq("slug", opts.providerSlug);

  const { data, error } = await q;
  if (error) throw new Error(`getProviderHealth: ${error.message}`);

  return ((data ?? []) as any[]).map((p) => {
    const accounts = (p.accounts ?? []) as Array<{ status: string }>;
    const healthy = accounts.filter((a) => a.status === "healthy").length;
    const degraded = accounts.filter((a) => a.status === "degraded").length;
    const expired = accounts.filter((a) => a.status === "expired").length;
    return {
      provider_slug: p.slug,
      provider_name: p.name,
      accounts_total: accounts.length,
      accounts_healthy: healthy,
      accounts_degraded: degraded,
      accounts_expired: expired,
      is_healthy: healthy > 0,
    };
  });
}

export async function listAccounts(
  supabase: SupabaseClient,
  opts?: { status?: AccountStatus | AccountStatus[] },
): Promise<AccountInfo[]> {
  let q = supabase
    .from("accounts")
    .select(
      "id,email,label,plan,status,auth_type,last_synced_at,last_health_check_at,providers(slug,name)",
    )
    .order("created_at", { ascending: false });

  if (opts?.status) {
    const statuses = Array.isArray(opts.status) ? opts.status : [opts.status];
    q =
      statuses.length === 1
        ? (q as any).eq("status", statuses[0])
        : (q as any).in("status", statuses);
  }

  const { data, error } = await q;
  if (error) throw new Error(`listAccounts: ${error.message}`);

  return ((data ?? []) as any[]).map((row) => {
    const p = row.providers as { slug: string; name: string } | null;
    return {
      id: row.id,
      email: row.email,
      label: row.label,
      plan: row.plan,
      status: row.status as AccountStatus,
      provider_slug: p?.slug ?? "",
      provider_name: p?.name ?? "",
      auth_type: row.auth_type,
      last_synced_at: row.last_synced_at,
      last_health_check_at: row.last_health_check_at,
    };
  });
}
