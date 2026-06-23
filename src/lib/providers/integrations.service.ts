/* Provider integration server functions. Owner-only. */
import { createServerFn } from "@tanstack/react-start";
import { requireSupabaseAuth } from "@/integrations/supabase/auth-middleware";
import { z } from "zod";
import type { SyncAccountResponse, SyncAccountStatus } from "./sync-response.types";
import { quotaGroupsFromExtra } from "./sync-cache";
import { providerExternalId, resolveModelSpecs } from "./model-keys";
import {
  syncModelsForAccount,
  countAccountModels,
  unlinkAccountModels,
  updateAccountModelTestResult,
  type LiveModelInput,
} from "./model-sync.server";
import { buildIdeVisibleUpsertInput } from "./antigravity-persistence";
import {
  ACCOUNT_MODELS_SELECT,
  mapJoinToAccountModelView,
  mapJoinToCatalogRow,
  type AccountModelJoinRow,
} from "./catalog-queries.server";
import {
  buildAntigravityLiveFetchSnapshot,
  mergeDbOverlay,
  applyTestResultsToSnapshot,
  isEligibleForRouting,
  buildAntigravityQuotaGroups,
  buildAntigravitySnapshotFromDbRows,
  computeSnapshotTestStats,
  type AntigravityLiveFetchSnapshot,
  type AntigravityLiveModelEntry,
} from "./antigravity-live-snapshot";

export type { SyncAccountResponse } from "./sync-response.types";

const OAUTH_SLUGS = ["claude-code", "antigravity"] as const;

export const listIntegrations = createServerFn({ method: "GET" })
  .middleware([requireSupabaseAuth])
  .inputValidator((data: unknown) => z.object({ category: z.enum(["oauth", "free"]) }).parse(data))
  .handler(async ({ data, context }) => {
    const { supabase } = context as any;
    const { data: providers, error } = await supabase
      .from("providers")
      .select(
        "id,slug,name,category,auth_type,description,homepage,base_url,is_builtin,created_at,accounts(id,label,email,plan,status,quota_used,quota_total,quota_unit,quota_extra,last_synced_at,last_health_check_at)",
      )
      .eq("category", data.category)
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
  });

// ───── OAuth flow (popup + /callback) ─────
export const startOAuthFlow = createServerFn({ method: "POST" })
  .middleware([requireSupabaseAuth])
  .inputValidator((d: unknown) =>
    z
      .object({
        provider_slug: z.enum(OAUTH_SLUGS),
        redirect_uri: z.string().url(),
        label: z.string().trim().min(1).max(80).optional(),
      })
      .parse(d),
  )
  .handler(async ({ data, context }) => {
    const { supabase } = context as any;
    const { data: provider, error: pErr } = await supabase
      .from("providers")
      .select("id,slug,category")
      .eq("slug", data.provider_slug)
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
    if (data.provider_slug === "claude-code") {
      const a = await import("./adapters/claude-code.server");
      flow = a.startFlow({ redirect_uri: data.redirect_uri });
    } else {
      const a = await import("./adapters/antigravity.server");
      flow = a.startFlow({ redirect_uri: data.redirect_uri });
    }

    const { data: row, error } = await supabase
      .from("oauth_flows")
      .insert({
        provider_slug: data.provider_slug,
        code_verifier: flow.code_verifier ?? null,
        state: flow.state,
        redirect_uri: flow.redirect_uri,
        extra: { label: data.label, client: flow.client ?? null },
      })
      .select("id")
      .single();
    if (error) throw new Error(error.message);

    return { flow_id: row.id, authorize_url: flow.authorize_url };
  });

export const completeOAuthFlow = createServerFn({ method: "POST" })
  .middleware([requireSupabaseAuth])
  .inputValidator((d: unknown) =>
    z
      .object({
        flow_id: z.string().uuid(),
        code: z.string().min(1),
        state: z.string().min(1),
      })
      .parse(d),
  )
  .handler(async ({ data, context }) => {
    const { supabase } = context as any;
    const { data: flow, error: fErr } = await supabase
      .from("oauth_flows")
      .select("*")
      .eq("id", data.flow_id)
      .single();
    if (fErr || !flow) throw new Error("OAuth flow not found or expired");

    const ageMs = Date.now() - new Date(flow.created_at).getTime();
    if (ageMs > 10 * 60_000) {
      await supabase.from("oauth_flows").delete().eq("id", flow.id);
      throw new Error("OAuth flow expired — please try again");
    }

    if (flow.state !== data.state) {
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
      const a = await import("./adapters/claude-code.server");
      creds = await a.completeFlow({
        code: data.code,
        code_verifier: flow.code_verifier!,
        state: flow.state,
        redirect_uri: flow.redirect_uri!,
      });
    } else if (flow.provider_slug === "antigravity") {
      const a = await import("./adapters/antigravity.server");
      creds = await a.completeFlow({
        code: data.code,
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
  });

// ───── API key connect (free providers only) ─────
export const connectCredential = createServerFn({ method: "POST" })
  .middleware([requireSupabaseAuth])
  .inputValidator((d: unknown) =>
    z
      .object({
        provider_slug: z.string().min(1),
        auth_type: z.literal("api_key"),
        credential: z.string().trim().min(4),
        label: z.string().trim().min(1).max(80).optional(),
      })
      .parse(d),
  )
  .handler(async ({ data, context }) => {
    const { supabase } = context as any;
    const { data: provider, error: pErr } = await supabase
      .from("providers")
      .select("id,name,slug,category")
      .eq("slug", data.provider_slug)
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
      api_key: data.credential,
      extra: { auth_type: data.auth_type, label: data.label },
    };
    const packed = packCredentials(creds);

    const { data: account, error: aErr } = await supabase
      .from("accounts")
      .insert({
        provider_id: provider.id,
        label: data.label ?? provider.name,
        auth_type: data.auth_type,
        credentials_enc: packed.credentials_enc,
        credentials_iv: packed.credentials_iv,
        credentials_tag: packed.credentials_tag,
        status: "healthy",
      })
      .select("id")
      .single();
    if (aErr) throw new Error(aErr.message);

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
  });

export async function runProviderModelSync(
  supabase: any,
  opts: {
    accountId: string;
    providerId: string;
    providerSlug: string;
    liveModels: LiveModelInput[];
    creds: import("./adapters/types").StoredCredentials;
  },
) {
  const adapter =
    opts.providerSlug === "claude-code"
      ? await import("./adapters/claude-code.server")
      : opts.providerSlug === "antigravity"
        ? await import("./adapters/antigravity.server")
        : await import("./adapters/opencode-zen.server");

  return syncModelsForAccount(supabase, {
    ...opts,
    testModel: (creds, externalId) => adapter.testModel(creds, externalId),
  });
}

export async function syncAccountInternal(
  supabase: any,
  accountId: string,
): Promise<SyncAccountResponse> {
  const startedAt = Date.now();
  const syncedAt = new Date().toISOString();
  const dbWrites: string[] = [];

  const { unpackCredentials, packCredentials } = await import("@/lib/credentials.server");
  const { data: acct, error } = await supabase
    .from("accounts")
    .select("id,provider_id,credentials_enc,credentials_iv,credentials_tag,providers(slug)")
    .eq("id", accountId)
    .single();
  if (error || !acct) throw new Error("Account not found");
  const slug = acct.providers?.slug as string;
  let creds = unpackCredentials({
    credentials_enc: acct.credentials_enc,
    credentials_iv: acct.credentials_iv,
    credentials_tag: acct.credentials_tag,
  });

  let identity: any = {
    email: null,
    plan: null,
    quota_used: null,
    quota_total: null,
    quota_unit: null,
  };
  let models: { external_id: string; display_name: string; capabilities: string[] }[] = [];
  let antigravityRawResponse: unknown = null;
  let health: SyncAccountResponse["health"] = {
    ok: true,
    latency_ms: 0,
    checked_at: syncedAt,
  };
  let providerCalls: string[] = [];

  try {
    if (slug === "claude-code") {
      const a = await import("./adapters/claude-code.server");
      providerCalls.push("fetchIdentity", "listModels");
      const r = await a.fetchIdentity(creds);
      creds = r.creds;
      identity = r.identity;
      models = await a.listModels(creds);
      health = {
        ok: r.health.ok,
        latency_ms: Date.now() - startedAt,
        checked_at: syncedAt,
        error: r.health.error,
      };
    } else if (slug === "antigravity") {
      const a = await import("./adapters/antigravity.server");
      const r = await a.syncAntigravityAccount(creds);
      creds = r.creds;
      identity = r.identity;
      models = r.models;
      antigravityRawResponse = r.rawResponse;
      health = {
        ok: r.health.ok,
        latency_ms: r.health.latency_ms,
        checked_at: syncedAt,
        error: r.health.error,
      };
      providerCalls = r.provider_calls;
    } else if (slug === "opencode-zen") {
      const a = await import("./adapters/opencode-zen.server");
      const r = await a.syncOpenCodeZenAccount(creds);
      identity = r.identity;
      models = r.models;
      health = {
        ok: r.health.ok,
        latency_ms: r.health.latency_ms,
        checked_at: syncedAt,
        error: r.health.error,
      };
      providerCalls = r.provider_calls;
    }
  } catch (e: any) {
    const isClaudeAuth =
      slug === "claude-code" &&
      (e?.name === "ClaudeAuthError" || String(e?.message ?? "").includes("re-login required"));
    const isOpenCodeHealth = slug === "opencode-zen" && e?.health && typeof e.health === "object";
    if (isOpenCodeHealth) {
      health = {
        ok: false,
        latency_ms: e.health.latency_ms ?? Date.now() - startedAt,
        checked_at: syncedAt,
        error: e.health.error ?? e?.message,
      };
    }
    await supabase
      .from("accounts")
      .update({
        status: isClaudeAuth
          ? "expired"
          : slug === "claude-code" || slug === "opencode-zen"
            ? "degraded"
            : "expired",
        last_health_check_at: syncedAt,
        ...(slug === "claude-code" && !isClaudeAuth
          ? { quota_extra: { health_error: e?.message ?? String(e) } }
          : slug === "opencode-zen"
            ? { quota_extra: { health_error: e?.message ?? String(e) } }
            : {}),
      })
      .eq("id", accountId);
    throw e;
  }

  const accountStatus: SyncAccountStatus =
    slug === "claude-code" && health.ok
      ? "healthy"
      : slug === "claude-code" && creds.access_token
        ? "degraded"
        : health.ok
          ? "healthy"
          : "degraded";
  const label = identity.email ?? "Connected account";
  const quotaExtra = (identity.quota_extra ?? null) as Record<string, unknown> | null;
  const quotaGroups = quotaGroupsFromExtra(quotaExtra);

  const packed = packCredentials(creds);
  await supabase
    .from("accounts")
    .update({
      credentials_enc: packed.credentials_enc,
      credentials_iv: packed.credentials_iv,
      credentials_tag: packed.credentials_tag,
      email: identity.email,
      plan: identity.plan,
      quota_used: identity.quota_used,
      quota_total: identity.quota_total,
      quota_unit: identity.quota_unit,
      quota_extra: quotaExtra,
      label,
      last_synced_at: syncedAt,
      last_health_check_at: syncedAt,
      status: accountStatus,
    })
    .eq("id", accountId);
  dbWrites.push("accounts");

  let modelStats = {
    count: 0,
    added: 0,
    updated: 0,
    removed: 0,
    unchanged: 0,
    ideVisible: 0,
    rawFetched: 0,
  };

  let liveModels: LiveModelInput[] = [];
  if (slug === "antigravity" && antigravityRawResponse) {
    liveModels = buildIdeVisibleUpsertInput(antigravityRawResponse);
    modelStats.rawFetched = Object.keys(
      (antigravityRawResponse as { models?: Record<string, unknown> }).models ?? {},
    ).length;
    modelStats.ideVisible = liveModels.length;
  } else {
    liveModels = models;
    modelStats.rawFetched = models.length;
    modelStats.ideVisible = models.length;
  }

  if (liveModels.length) {
    const syncStats = await runProviderModelSync(supabase, {
      accountId,
      providerId: acct.provider_id,
      providerSlug: slug,
      liveModels,
      creds,
    });
    modelStats = {
      ...modelStats,
      count: syncStats.count,
      added: syncStats.added.length,
      updated: syncStats.linked,
      removed: syncStats.removed.length,
      unchanged: syncStats.unchanged,
    };
    dbWrites.push("models", "account_models");
  }

  const modelCounts = await countAccountModels(supabase, accountId);
  const modelsTotal = modelCounts.total || models.length;
  const modelsEnabled = modelCounts.enabled || modelsTotal;

  return {
    ok: true,
    account_id: accountId,
    provider_slug: slug,
    synced_at: syncedAt,
    account: {
      email: identity.email,
      label,
      plan: identity.plan,
      status: accountStatus,
      last_synced_at: syncedAt,
      last_health_check_at: syncedAt,
      quota_used: identity.quota_used,
      quota_total: identity.quota_total,
      quota_unit: identity.quota_unit,
      quota_extra: quotaExtra,
    },
    health,
    models: {
      fetched: models.length,
      added: modelStats.added,
      updated: modelStats.updated,
      removed: modelStats.removed,
      enabled: modelsEnabled,
      total: modelsTotal,
    },
    quota: {
      synced: quotaGroups.length > 0,
      used: identity.quota_used,
      total: identity.quota_total,
      unit: identity.quota_unit,
      groups: quotaGroups,
    },
    meta: {
      provider_calls: providerCalls,
      db_writes: dbWrites,
      duration_ms: Date.now() - startedAt,
    },
  };
}

function slimQuotaExtraForWire(
  extra: Record<string, unknown> | null,
): Record<string, unknown> | null {
  if (!extra) return null;
  const {
    projectId,
    tierId,
    tierName,
    displayName,
    availablePromptCredits,
    planInfo,
    groups,
    health,
    fetchedAt,
  } = extra;
  return {
    projectId,
    tierId,
    tierName,
    displayName,
    availablePromptCredits,
    planInfo,
    groups,
    health,
    fetchedAt,
  };
}

export function toSyncAccountWireResponse(result: SyncAccountResponse): SyncAccountResponse {
  return {
    ...result,
    account: {
      ...result.account,
      quota_extra: slimQuotaExtraForWire(result.account.quota_extra),
    },
  };
}

export const syncAccount = createServerFn({ method: "POST" })
  .middleware([requireSupabaseAuth])
  .inputValidator((d: unknown) => z.object({ account_id: z.string().uuid() }).parse(d))
  .handler(async ({ data, context }) => {
    const result = await syncAccountInternal((context as any).supabase, data.account_id);
    return Response.json(toSyncAccountWireResponse(result));
  });

async function testAntigravityModelsConcurrent(
  supabase: any,
  accountId: string,
  creds: import("./adapters/types").StoredCredentials,
  externalIds: string[],
  quotaById: Map<string, boolean>,
  concurrency = 3,
): Promise<Array<{ external_id: string; ok: boolean; latency_ms?: number; error?: string }>> {
  const adapter = await import("./adapters/antigravity.server");
  const results: Array<{
    external_id: string;
    ok: boolean;
    latency_ms?: number;
    error?: string;
  }> = [];
  const queue = [...externalIds];

  const { data: catalogRows } = await supabase
    .from("models")
    .select("id, external_id")
    .in("external_id", externalIds);
  const modelIdByExt = new Map(
    (catalogRows ?? []).map((r: { id: string; external_id: string }) => [r.external_id, r.id]),
  );

  async function worker() {
    while (queue.length) {
      const ext = queue.shift();
      if (!ext) return;
      const r = await adapter.testModel(creds, ext);
      const exhausted = quotaById.get(ext) ?? false;
      const eligible = r.ok && !exhausted;
      const modelId = modelIdByExt.get(ext);
      if (modelId) {
        await updateAccountModelTestResult(supabase, accountId, modelId, r, {
          enabled: eligible,
        });
      }
      results.push({ external_id: ext, ok: r.ok, latency_ms: r.latency_ms, error: r.error });
    }
  }

  await Promise.all(Array.from({ length: Math.min(concurrency, externalIds.length) }, worker));
  return results;
}

async function persistAntigravityQuotaToAccount(
  supabase: any,
  accountId: string,
  rawResponse: unknown,
  existingExtra: Record<string, unknown> | null | undefined,
) {
  const liveModels =
    rawResponse && typeof rawResponse === "object"
      ? (((rawResponse as { models?: Record<string, AntigravityLiveModelEntry> }).models ??
          {}) as Record<string, AntigravityLiveModelEntry>)
      : {};
  const groups = buildAntigravityQuotaGroups(rawResponse, liveModels);
  const quotaMap: Record<
    string,
    { remainingFraction: number; resetTime: string; isExhausted: boolean }
  > = {};
  for (const [id, entry] of Object.entries(liveModels)) {
    const q = entry?.quotaInfo;
    if (!q?.resetTime) continue;
    const remainingFraction = Number(q.remainingFraction ?? 0);
    quotaMap[id] = {
      remainingFraction: Math.max(0, Math.min(1, remainingFraction)),
      resetTime: q.resetTime,
      isExhausted: Boolean(q.isExhausted) || remainingFraction <= 0,
    };
  }

  const fractions = groups
    .map((g) => g.fiveHourQuota?.remainingFraction)
    .filter((n): n is number => typeof n === "number");

  const patch: Record<string, unknown> = {
    quota_extra: {
      ...(existingExtra ?? {}),
      groups,
      models: quotaMap,
      fetchedAt: new Date().toISOString(),
    },
  };

  if (fractions.length) {
    const avgRemaining = fractions.reduce((s, n) => s + n, 0) / fractions.length;
    patch.quota_used = Math.round((1 - avgRemaining) * 100);
    patch.quota_total = 100;
    patch.quota_unit = "%";
  }

  await supabase.from("accounts").update(patch).eq("id", accountId);
}

export async function runAntigravityLiveSnapshotFetch(
  supabase: any,
  accountId: string,
  opts: { autoTest?: boolean } = {},
): Promise<AntigravityLiveFetchSnapshot> {
  const autoTest = opts.autoTest ?? true;
  const { unpackCredentials, packCredentials } = await import("@/lib/credentials.server");
  const { data: acct, error } = await supabase
    .from("accounts")
    .select(
      "id,provider_id,credentials_enc,credentials_iv,credentials_tag,quota_extra,providers(slug)",
    )
    .eq("id", accountId)
    .single();
  if (error || !acct) throw new Error("Account not found");
  if ((acct.providers as any)?.slug !== "antigravity") {
    throw new Error("runAntigravityLiveSnapshotFetch is only for Antigravity accounts");
  }

  const credsIn = unpackCredentials({
    credentials_enc: acct.credentials_enc,
    credentials_iv: acct.credentials_iv,
    credentials_tag: acct.credentials_tag,
  });

  const adapter = await import("./adapters/antigravity.server");
  const live = await adapter.fetchAntigravityLiveRaw(credsIn);

  await persistAntigravityQuotaToAccount(
    supabase,
    accountId,
    live.rawResponse,
    acct.quota_extra as Record<string, unknown> | null,
  );

  if (live.creds.project_id !== credsIn.project_id || live.creds.extra !== credsIn.extra) {
    const packed = packCredentials(live.creds);
    await supabase
      .from("accounts")
      .update({
        credentials_enc: packed.credentials_enc,
        credentials_iv: packed.credentials_iv,
        credentials_tag: packed.credentials_tag,
      })
      .eq("id", accountId);
  }

  const upsertStats = await runProviderModelSync(supabase, {
    accountId,
    providerId: acct.provider_id,
    providerSlug: "antigravity",
    liveModels: buildIdeVisibleUpsertInput(live.rawResponse),
    creds: live.creds,
  });

  const snapshotBase = buildAntigravityLiveFetchSnapshot({
    rawResponse: live.rawResponse,
    projectId: live.projectId,
    planTier: live.planTier,
    loadCodeAssistUsed: live.loadCodeAssistUsed,
    persistenceStats: {
      insertedNewCount: upsertStats.added.length,
      updatedExistingCount: upsertStats.linked,
      unchangedCount: upsertStats.unchanged,
      duplicatePreventedCount: 0,
      removedStaleCount: upsertStats.removed.length,
    },
  });

  const ideVisibleIds = snapshotBase.visibleCatalog.models.map((m) => m.id);
  const quotaById = new Map(
    snapshotBase.visibleCatalog.models.map((m) => [m.id, Boolean(m.quota?.isExhausted)]),
  );

  let testResults: Array<{
    external_id: string;
    ok: boolean;
    latency_ms?: number;
    error?: string;
  }> = [];

  if (autoTest && upsertStats.added.length > 0) {
    testResults = await testAntigravityModelsConcurrent(
      supabase,
      accountId,
      live.creds,
      upsertStats.added,
      quotaById,
      3,
    );
  }

  const snapshotWithTests = applyTestResultsToSnapshot(snapshotBase, testResults, autoTest);

  const snapshotIds = new Set(snapshotWithTests.visibleCatalog.models.map((m) => m.id));
  const { data: dbRows } = await supabase
    .from("account_models")
    .select(ACCOUNT_MODELS_SELECT)
    .eq("account_id", accountId);

  const overlayRows = (dbRows ?? [])
    .map((row: AccountModelJoinRow) => mapJoinToCatalogRow(row))
    .filter((row) => snapshotIds.has(row.external_id))
    .map((row) => ({
      id: row.id,
      external_id: row.external_id,
      display_name: row.display_name,
      capabilities: row.capabilities,
      test_status: row.test_status,
      enabled: row.enabled,
      last_test_error: null,
      last_tested_at: row.last_tested_at,
      latency_ms: row.latency_ms,
      updated_at: row.last_tested_at,
    }));

  const visibleModels = mergeDbOverlay(
    snapshotWithTests.visibleCatalog.models,
    overlayRows,
    providerExternalId,
  );

  if (autoTest) {
    for (const m of visibleModels) {
      const eligible = isEligibleForRouting(m);
      if (m.routing.dbRowId) {
        await supabase
          .from("account_models")
          .update({ enabled: eligible && m.routing.selected })
          .eq("id", m.routing.dbRowId);
      }
    }
  }

  const testStats = computeSnapshotTestStats(visibleModels);
  const selectedForRoutingCount = visibleModels.filter((m) => m.routing.selected).length;

  return {
    ...snapshotWithTests,
    visibleCatalog: {
      ...snapshotWithTests.visibleCatalog,
      count: visibleModels.length,
      models: visibleModels,
    },
    stats: {
      ...snapshotWithTests.stats,
      ...testStats,
      testedAfterFetchCount: testResults.length,
      selectedForRoutingCount,
    },
  };
}

export const fetchModels = createServerFn({ method: "POST" })
  .middleware([requireSupabaseAuth])
  .inputValidator((d: unknown) => z.object({ account_id: z.string().uuid() }).parse(d))
  .handler(async ({ data, context }) => {
    const { supabase } = context as any;
    const { unpackCredentials } = await import("@/lib/credentials.server");
    const { data: acct, error } = await supabase
      .from("accounts")
      .select("id,provider_id,credentials_enc,credentials_iv,credentials_tag,providers(slug)")
      .eq("id", data.account_id)
      .single();
    if (error || !acct) throw new Error("Account not found");

    const slug = acct.providers?.slug as string;

    if (slug === "antigravity") {
      const snapshot = await runAntigravityLiveSnapshotFetch(supabase, data.account_id);
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

    let liveModels: LiveModelInput[] = [];
    if (slug === "claude-code") {
      const a = await import("./adapters/claude-code.server");
      creds = (await a.fetchIdentity(creds)).creds;
      liveModels = await a.listModels(creds);
    } else if (slug === "opencode-zen") {
      const a = await import("./adapters/opencode-zen.server");
      liveModels = await a.listModels(creds);
    } else {
      throw new Error("Fetch models not supported for this provider");
    }

    const stats = await runProviderModelSync(supabase, {
      accountId: data.account_id,
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
  });

export const fetchAntigravityLiveSnapshot = createServerFn({ method: "POST" })
  .middleware([requireSupabaseAuth])
  .inputValidator((d: unknown) => z.object({ account_id: z.string().uuid() }).parse(d))
  .handler(async ({ data, context }) => {
    return runAntigravityLiveSnapshotFetch((context as any).supabase, data.account_id);
  });

export const loadAntigravityStoredSnapshot = createServerFn({ method: "GET" })
  .middleware([requireSupabaseAuth])
  .inputValidator((d: unknown) => z.object({ account_id: z.string().uuid() }).parse(d))
  .handler(async ({ data, context }) => {
    const { supabase } = context as any;
    const { data: acct, error: acctErr } = await supabase
      .from("accounts")
      .select("id,plan,last_synced_at,quota_extra,providers(slug)")
      .eq("id", data.account_id)
      .single();
    if (acctErr || !acct) throw new Error("Account not found");
    if ((acct.providers as { slug?: string } | null)?.slug !== "antigravity") {
      throw new Error("loadAntigravityStoredSnapshot is only for Antigravity accounts");
    }

    const { data: rows, error } = await supabase
      .from("account_models")
      .select(ACCOUNT_MODELS_SELECT)
      .eq("account_id", data.account_id)
      .order("display_name", { foreignTable: "models" });
    if (error) throw new Error(error.message);

    const extra = (acct.quota_extra ?? null) as Record<string, unknown> | null;
    const snapshot = buildAntigravitySnapshotFromDbRows(
      (rows ?? []).map((row: AccountModelJoinRow) => {
        const mapped = mapJoinToCatalogRow(row);
        return {
          id: mapped.id,
          external_id: mapped.external_id,
          display_name: mapped.display_name,
          capabilities: mapped.capabilities,
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
  });

export const diagnoseAntigravityFetch = createServerFn({ method: "POST" })
  .middleware([requireSupabaseAuth])
  .inputValidator((d: unknown) => z.object({ account_id: z.string().uuid() }).parse(d))
  .handler(async ({ data, context }) => {
    const { supabase } = context as any;
    const { unpackCredentials } = await import("@/lib/credentials.server");
    const { data: acct, error } = await supabase
      .from("accounts")
      .select("id,credentials_enc,credentials_iv,credentials_tag,providers(slug)")
      .eq("id", data.account_id)
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
    const adapter = await import("./adapters/antigravity.server");
    return adapter.diagnoseAntigravityFetch(credsIn);
  });

export function extractCapabilityList(caps: Record<string, unknown> | null): string[] {
  if (!caps) return [];
  // New format: { list: string[], provider_external_id: string }
  if (Array.isArray(caps.list)) return caps.list as string[];
  // Old format: array was spread into numeric keys { "0": "chat", "1": "tools", ... }
  return Object.entries(caps)
    .filter(([k]) => /^\d+$/.test(k))
    .sort(([a], [b]) => Number(a) - Number(b))
    .map(([, v]) => String(v));
}

export type CatalogModel = {
  key: string;
  external_id: string;
  display_name: string;
  provider_slug: string;
  provider_name: string;
  capabilities: string[];
  quality_rating: number;
  context_window: number | null;
  input_cost_per_mtok: number | null;
  output_cost_per_mtok: number | null;
  test_status: "working" | "failed" | "untested";
  latency_ms: number | null;
  last_tested_at: string | null;
  lifecycle: "discovered" | "tested" | "approved" | "blocked";
  accounts: { id: string; email: string | null; label: string | null; status: string }[];
  account_rows: { id: string; account_id: string; enabled: boolean }[];
  enabled_account_count: number;
  total_account_count: number;
};

type CatalogRowInput = {
  id: string;
  model_id?: string;
  account_id?: string;
  external_id: string;
  display_name: string;
  capabilities: Record<string, unknown> | null;
  quality_rating: number | null;
  context_window: number | null;
  input_cost_per_mtok: number | null;
  output_cost_per_mtok: number | null;
  test_status: string | null;
  latency_ms: number | null;
  last_tested_at: string | null;
  lifecycle: string;
  enabled: boolean;
  accounts: { id: string; email: string | null; label: string | null; status: string } | null;
  providers: { slug: string; name: string } | null;
};

const TEST_STATUS_RANK: Record<string, number> = { working: 3, failed: 2, untested: 1 };
const LIFECYCLE_RANK: Record<string, number> = {
  approved: 4,
  tested: 3,
  discovered: 2,
  blocked: 1,
};

function normalizeTestStatus(s: string | null): "working" | "failed" | "untested" {
  if (s === "working" || s === "failed") return s;
  return "untested";
}

function normalizeLifecycle(s: string): CatalogModel["lifecycle"] {
  if (s === "approved" || s === "tested" || s === "blocked") return s;
  return "discovered";
}

function rowScore(row: CatalogRowInput): number {
  let score = 0;
  score += (TEST_STATUS_RANK[row.test_status ?? "untested"] ?? 0) * 100;
  score += (LIFECYCLE_RANK[row.lifecycle] ?? 0) * 10;
  if (row.enabled) score += 5;
  if (row.latency_ms != null) score += Math.max(0, 1000 - row.latency_ms) / 100;
  return score;
}

export function aggregateCatalogModels(rows: CatalogRowInput[]): CatalogModel[] {
  const groups = new Map<string, CatalogRowInput[]>();

  for (const row of rows) {
    const caps = row.capabilities;
    if (!row.enabled && row.lifecycle === "blocked") continue;

    const providerSlug = row.providers?.slug ?? "unknown";
    const ext = providerExternalId(row.external_id, caps);
    const key = `${providerSlug}:${ext}`;
    const list = groups.get(key) ?? [];
    list.push(row);
    groups.set(key, list);
  }

  const catalog: CatalogModel[] = [];

  for (const [key, group] of groups) {
    const best = [...group].sort((a, b) => rowScore(b) - rowScore(a))[0]!;
    const providerSlug = best.providers?.slug ?? "unknown";
    const ext = providerExternalId(best.external_id, best.capabilities);
    const caps = best.capabilities;
    const specs = resolveModelSpecs(
      ext,
      providerSlug,
      caps,
      best.context_window,
      best.quality_rating,
    );

    const accountsMap = new Map<
      string,
      { id: string; email: string | null; label: string | null; status: string }
    >();
    const accountRows: CatalogModel["account_rows"] = [];
    for (const r of group) {
      const acct = r.accounts;
      if (acct) accountsMap.set(acct.id, acct);
      accountRows.push({
        id: r.id,
        account_id: acct?.id ?? r.account_id ?? "",
        enabled: r.enabled,
      });
    }

    let testStatus: "working" | "failed" | "untested" = "untested";
    for (const r of group) {
      const t = normalizeTestStatus(r.test_status);
      if ((TEST_STATUS_RANK[t] ?? 0) > (TEST_STATUS_RANK[testStatus] ?? 0)) testStatus = t;
    }

    let lifecycle: CatalogModel["lifecycle"] = "discovered";
    for (const r of group) {
      const l = normalizeLifecycle(r.lifecycle);
      if ((LIFECYCLE_RANK[l] ?? 0) > (LIFECYCLE_RANK[lifecycle] ?? 0)) lifecycle = l;
    }

    const workingLatencies = group
      .filter((r) => r.test_status === "working" && r.latency_ms != null)
      .map((r) => r.latency_ms as number);
    const latency_ms = workingLatencies.length ? Math.min(...workingLatencies) : null;

    const last_tested_at =
      group
        .map((r) => r.last_tested_at)
        .filter(Boolean)
        .sort()
        .reverse()[0] ?? null;

    catalog.push({
      key,
      external_id: ext,
      display_name: best.display_name,
      provider_slug: providerSlug,
      provider_name: best.providers?.name ?? providerSlug,
      capabilities: extractCapabilityList(caps),
      quality_rating: specs.quality_rating,
      context_window: specs.context_window,
      input_cost_per_mtok: best.input_cost_per_mtok,
      output_cost_per_mtok: best.output_cost_per_mtok,
      test_status: testStatus,
      latency_ms,
      last_tested_at,
      lifecycle,
      accounts: [...accountsMap.values()],
      account_rows: accountRows,
      enabled_account_count: group.filter((r) => r.enabled).length,
      total_account_count: group.length,
    });
  }

  return catalog.sort((a, b) => {
    const pn = a.provider_name.localeCompare(b.provider_name);
    return pn !== 0 ? pn : a.display_name.localeCompare(b.display_name);
  });
}

export const listCatalogModels = createServerFn({ method: "GET" })
  .middleware([requireSupabaseAuth])
  .handler(async ({ context }) => {
    const { supabase } = context as any;
    const { data: rows, error } = await supabase
      .from("account_models")
      .select(ACCOUNT_MODELS_SELECT)
      .order("display_name", { foreignTable: "models" });
    if (error) throw new Error(error.message);
    return aggregateCatalogModels(
      (rows ?? []).map((row: AccountModelJoinRow) => mapJoinToCatalogRow(row)) as CatalogRowInput[],
    );
  });

export const listAccountModels = createServerFn({ method: "GET" })
  .middleware([requireSupabaseAuth])
  .inputValidator((d: unknown) => z.object({ account_id: z.string().uuid() }).parse(d))
  .handler(async ({ data, context }) => {
    const { supabase } = context as any;
    const { data: rows, error } = await supabase
      .from("account_models")
      .select(`${ACCOUNT_MODELS_SELECT}`)
      .eq("account_id", data.account_id)
      .order("display_name", { foreignTable: "models" });
    if (error) throw new Error(error.message);
    return (rows ?? []).map((row: AccountModelJoinRow) => {
      const mapped = mapJoinToAccountModelView(row);
      return {
        ...mapped,
        capabilities: extractCapabilityList(mapped.capabilities),
      };
    });
  });

export const testAccountModels = createServerFn({ method: "POST" })
  .middleware([requireSupabaseAuth])
  .inputValidator((d: unknown) =>
    z
      .object({
        account_id: z.string().uuid(),
        external_ids: z.array(z.string()).min(1),
      })
      .parse(d),
  )
  .handler(async ({ data, context }) => {
    const { supabase } = context as any;
    const { unpackCredentials } = await import("@/lib/credentials.server");
    const { data: acct } = await supabase
      .from("accounts")
      .select("credentials_enc,credentials_iv,credentials_tag,provider_id,providers(slug)")
      .eq("id", data.account_id)
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
        ? await import("./adapters/claude-code.server")
        : slug === "antigravity"
          ? await import("./adapters/antigravity.server")
          : await import("./adapters/opencode-zen.server");

    const results = await Promise.all(
      data.external_ids.map(async (ext) => {
        const r = await adapter.testModel(creds, ext);
        const { data: modelRow } = await supabase
          .from("models")
          .select("id")
          .eq("provider_id", acct.provider_id)
          .eq("external_id", ext)
          .maybeSingle();
        if (modelRow?.id) {
          await updateAccountModelTestResult(supabase, data.account_id, modelRow.id, r);
        }
        return r;
      }),
    );
    return results;
  });

export const setModelsEnabled = createServerFn({ method: "POST" })
  .middleware([requireSupabaseAuth])
  .inputValidator((d: unknown) =>
    z
      .object({
        account_id: z.string().uuid(),
        enabled: z.record(z.string(), z.boolean()),
      })
      .parse(d),
  )
  .handler(async ({ data, context }) => {
    const { supabase } = context as any;
    await Promise.all(
      Object.entries(data.enabled).map(([id, enabled]) =>
        supabase.from("account_models").update({ enabled }).eq("id", id),
      ),
    );
    return { ok: true };
  });

export const disconnectAccount = createServerFn({ method: "POST" })
  .middleware([requireSupabaseAuth])
  .inputValidator((d: unknown) => z.object({ account_id: z.string().uuid() }).parse(d))
  .handler(async ({ data, context }) => {
    const { supabase } = context as any;
    await unlinkAccountModels(supabase, data.account_id);
    const { error } = await supabase.from("accounts").delete().eq("id", data.account_id);
    if (error) throw new Error(error.message);
    return { ok: true };
  });

export const toggleAccount = createServerFn({ method: "POST" })
  .middleware([requireSupabaseAuth])
  .inputValidator((d: unknown) =>
    z.object({ account_id: z.string().uuid(), status: z.enum(["healthy", "degraded"]) }).parse(d),
  )
  .handler(async ({ data, context }) => {
    const { supabase } = context as any;
    const { error } = await supabase
      .from("accounts")
      .update({ status: data.status })
      .eq("id", data.account_id);
    if (error) throw new Error(error.message);
    return { ok: true };
  });
