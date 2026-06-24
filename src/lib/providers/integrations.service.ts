/* Provider integration service — internal helpers used by dashboard-router.server.ts. */
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
  let models: {
    external_id: string;
    display_name: string;
    capabilities: string[];
    context_window?: number;
    quality_rating?: number;
  }[] = [];
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
