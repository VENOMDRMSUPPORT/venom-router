import {
  buildVisibleCatalogModels,
  extractRecommendedModelIds,
  inferCapabilities,
  parseModelQuota,
  type AntigravityLiveModelEntry,
} from "./antigravity-live-snapshot";
import {
  createModelStore,
  type ModelStore,
  type ProviderModelInput,
  upsertModelsForAccountStore,
} from "./models-persistence";
import { providerExternalId, toDbExternalId, resolveModelSpecs } from "./model-keys";

export type AntigravityUpsertStats = {
  ideVisibleFetchedCount: number;
  rawFetchedCount: number;
  insertedNewCount: number;
  updatedExistingCount: number;
  unchangedCount: number;
  duplicatePreventedCount: number;
  removedStaleCount: number;
};

export type AntigravityUpsertModelInput = ProviderModelInput & {
  extra_capabilities?: Record<string, unknown>;
};

type ExistingRow = {
  id: string;
  external_id: string;
  display_name: string;
  capabilities: Record<string, unknown>;
  enabled?: boolean;
  test_status?: string | null;
  last_tested_at?: string | null;
};

function liveModelsFromRaw(rawResponse: unknown): Record<string, AntigravityLiveModelEntry> {
  if (!rawResponse || typeof rawResponse !== "object") return {};
  const models = (rawResponse as { models?: Record<string, AntigravityLiveModelEntry> }).models;
  return models && typeof models === "object" ? models : {};
}

export function rawCatalogCount(rawResponse: unknown): number {
  return Object.keys(liveModelsFromRaw(rawResponse)).length;
}

/** Build upsert payload for IDE-visible Recommended models only. */
export function buildIdeVisibleUpsertInput(rawResponse: unknown): AntigravityUpsertModelInput[] {
  const liveModels = liveModelsFromRaw(rawResponse);
  const recommendedIds = extractRecommendedModelIds(rawResponse);
  const { models } = buildVisibleCatalogModels(recommendedIds, liveModels);

  return models.map((m) => ({
    external_id: m.id,
    display_name: m.displayName,
    capabilities: m.capabilities,
    extra_capabilities: {
      antigravity_raw: m.raw,
      quota: m.quota,
      source: "fetchAvailableModels",
      ide_visible: true,
    },
  }));
}

function scoreRow(row: ExistingRow): number {
  let score = 0;
  if (row.external_id.startsWith("acct:")) score += 100;
  if (row.test_status === "working") score += 50;
  if (row.enabled) score += 10;
  if (row.last_tested_at) score += 5;
  return score;
}

export function pickCanonicalModelRow(rows: ExistingRow[], accountId: string): ExistingRow {
  const sorted = [...rows].sort((a, b) => scoreRow(b) - scoreRow(a));
  const keep = sorted[0]!;
  const ext = providerExternalId(keep.external_id, keep.capabilities);
  const canonicalExternalId = toDbExternalId(accountId, ext);
  if (keep.external_id !== canonicalExternalId) {
    return { ...keep, external_id: canonicalExternalId };
  }
  return keep;
}

export function dedupeAccountModelRowsStore(store: ModelStore, accountId: string): number {
  const rows = store.listByAccount(accountId);
  const groups = new Map<string, ExistingRow[]>();

  for (const row of rows) {
    const ext = providerExternalId(row.external_id, row.capabilities);
    const list = groups.get(ext) ?? [];
    list.push({
      id: row.id,
      external_id: row.external_id,
      display_name: row.display_name,
      capabilities: row.capabilities as Record<string, unknown>,
      enabled: row.enabled,
    });
    groups.set(ext, list);
  }

  let duplicatePreventedCount = 0;
  for (const [, dupes] of groups) {
    if (dupes.length <= 1) {
      const only = dupes[0];
      if (!only) continue;
      const ext = providerExternalId(only.external_id, only.capabilities);
      const canonical = toDbExternalId(accountId, ext);
      if (only.external_id !== canonical) {
        store.updateById(only.id, { external_id: canonical });
      }
      continue;
    }

    const keep = pickCanonicalModelRow(dupes, accountId);
    store.updateById(keep.id, {
      external_id: keep.external_id,
      display_name: keep.display_name,
      capabilities: keep.capabilities,
      enabled: keep.enabled ?? true,
    });

    for (const row of dupes) {
      if (row.id === keep.id) continue;
      store.deleteById(row.id);
      duplicatePreventedCount++;
    }
  }

  return duplicatePreventedCount;
}

export function markStaleModelsStore(
  store: ModelStore,
  accountId: string,
  keepProviderIds: Set<string>,
): number {
  let removedStaleCount = 0;
  for (const row of store.listByAccount(accountId)) {
    const ext = providerExternalId(row.external_id, row.capabilities);
    if (keepProviderIds.has(ext)) continue;
    store.updateById(row.id, {
      enabled: false,
      lifecycle: "blocked",
      capabilities: {
        ...(row.capabilities as Record<string, unknown>),
        stale: true,
        stale_reason: "removed_from_recommended",
      },
    });
    removedStaleCount++;
  }
  return removedStaleCount;
}

export function upsertAntigravityIdeVisibleModelsStore(
  store: ModelStore,
  accountId: string,
  providerId: string,
  rawResponse: unknown,
): AntigravityUpsertStats {
  const duplicatePreventedCount = dedupeAccountModelRowsStore(store, accountId);
  const models = buildIdeVisibleUpsertInput(rawResponse);
  const keepIds = new Set(models.map((m) => m.external_id));

  const upsertStats = upsertModelsForAccountStoreWithUnchanged(
    store,
    accountId,
    providerId,
    models,
  );
  const removedStaleCount = markStaleModelsStore(store, accountId, keepIds);

  return {
    ideVisibleFetchedCount: models.length,
    rawFetchedCount: rawCatalogCount(rawResponse),
    insertedNewCount: upsertStats.added,
    updatedExistingCount: upsertStats.updated,
    unchangedCount: upsertStats.unchanged,
    duplicatePreventedCount,
    removedStaleCount,
  };
}

function upsertModelsForAccountStoreWithUnchanged(
  store: ModelStore,
  accountId: string,
  providerId: string,
  models: AntigravityUpsertModelInput[],
) {
  const existing = store.selectExisting(accountId);
  const byProviderExt = new Map(
    existing.map((m) => [
      providerExternalId(m.external_id, m.capabilities as Record<string, unknown>),
      m,
    ]),
  );
  let added = 0;
  let updated = 0;
  let unchanged = 0;

  for (const m of models) {
    const capabilities = {
      list: m.capabilities,
      provider_external_id: m.external_id,
      ...(m.extra_capabilities ?? {}),
    };
    const prev = byProviderExt.get(m.external_id);
    if (prev) {
      const fullPrev = store.listByAccount(accountId).find((r) => r.id === prev.id);
      const same =
        fullPrev &&
        fullPrev.display_name === m.display_name &&
        JSON.stringify(fullPrev.capabilities) === JSON.stringify(capabilities);
      if (same) {
        unchanged++;
        continue;
      }
      const err = store.updateById(prev.id, {
        display_name: m.display_name,
        capabilities,
        enabled: true,
        lifecycle: "discovered",
      });
      if (err.error)
        throw new Error(`Model update failed (${m.external_id}): ${err.error.message}`);
      updated++;
      continue;
    }

    const err = store.insert({
      provider_id: providerId,
      account_id: accountId,
      external_id: toDbExternalId(accountId, m.external_id),
      display_name: m.display_name,
      capabilities,
      enabled: true,
      lifecycle: "discovered",
    });
    if (err.error) throw new Error(`Model insert failed (${m.external_id}): ${err.error.message}`);
    added++;
  }

  return { added, updated, unchanged };
}

export { createModelStore };

export async function dedupeAccountModelRowsSupabase(
  supabase: any,
  accountId: string,
): Promise<number> {
  const { data: rows } = await supabase
    .from("models")
    .select("id,external_id,display_name,capabilities,enabled,test_status,last_tested_at")
    .eq("account_id", accountId);

  const groups = new Map<string, ExistingRow[]>();
  for (const row of rows ?? []) {
    const ext = providerExternalId(row.external_id, row.capabilities);
    const list = groups.get(ext) ?? [];
    list.push(row);
    groups.set(ext, list);
  }

  let duplicatePreventedCount = 0;
  for (const [, dupes] of groups) {
    if (dupes.length <= 1) {
      const only = dupes[0];
      if (!only) continue;
      const ext = providerExternalId(only.external_id, only.capabilities);
      const canonical = toDbExternalId(accountId, ext);
      if (only.external_id !== canonical) {
        const { error } = await supabase
          .from("models")
          .update({ external_id: canonical })
          .eq("id", only.id);
        if (error) throw new Error(`Normalize external_id failed: ${error.message}`);
      }
      continue;
    }

    const keep = pickCanonicalModelRow(dupes, accountId);
    const { error: keepErr } = await supabase
      .from("models")
      .update({
        external_id: keep.external_id,
        display_name: keep.display_name,
        capabilities: keep.capabilities,
        enabled: keep.enabled ?? true,
      })
      .eq("id", keep.id);
    if (keepErr) throw new Error(`Merge duplicate failed: ${keepErr.message}`);

    for (const row of dupes) {
      if (row.id === keep.id) continue;
      const { error } = await supabase.from("models").delete().eq("id", row.id);
      if (error) throw new Error(`Delete duplicate failed: ${error.message}`);
      duplicatePreventedCount++;
    }
  }

  return duplicatePreventedCount;
}

export async function markStaleModelsSupabase(
  supabase: any,
  accountId: string,
  keepProviderIds: Set<string>,
): Promise<number> {
  const { data: rows } = await supabase
    .from("models")
    .select("id,external_id,capabilities")
    .eq("account_id", accountId);

  let removedStaleCount = 0;
  for (const row of rows ?? []) {
    const ext = providerExternalId(row.external_id, row.capabilities);
    if (keepProviderIds.has(ext)) continue;
    const { error } = await supabase
      .from("models")
      .update({
        enabled: false,
        lifecycle: "blocked",
        capabilities: {
          ...(row.capabilities ?? {}),
          stale: true,
          stale_reason: "removed_from_recommended",
        },
      })
      .eq("id", row.id);
    if (error) throw new Error(`Mark stale failed: ${error.message}`);
    removedStaleCount++;
  }
  return removedStaleCount;
}

export async function upsertAntigravityIdeVisibleModelsSupabase(
  supabase: any,
  accountId: string,
  providerId: string,
  rawResponse: unknown,
): Promise<AntigravityUpsertStats> {
  const duplicatePreventedCount = await dedupeAccountModelRowsSupabase(supabase, accountId);
  const models = buildIdeVisibleUpsertInput(rawResponse);
  const keepIds = new Set(models.map((m) => m.external_id));

  const { data: existing } = await supabase
    .from("models")
    .select("id,external_id,capabilities,display_name")
    .eq("account_id", accountId);

  const byProviderExt = new Map(
    (existing ?? []).map((m) => [
      providerExternalId(m.external_id, m.capabilities),
      { id: m.id as string, display_name: m.display_name as string, capabilities: m.capabilities },
    ]),
  );

  let insertedNewCount = 0;
  let updatedExistingCount = 0;
  let unchangedCount = 0;

  for (const m of models) {
    const capabilities = {
      list: m.capabilities,
      provider_external_id: m.external_id,
      ...(m.extra_capabilities ?? {}),
    };
    const specs = resolveModelSpecs(m.external_id, "antigravity", capabilities, null, null);
    const prev = byProviderExt.get(m.external_id);
    if (prev) {
      const same =
        prev.display_name === m.display_name &&
        JSON.stringify(prev.capabilities) === JSON.stringify(capabilities);
      if (same) {
        unchangedCount++;
        continue;
      }
      const { error } = await supabase
        .from("models")
        .update({
          display_name: m.display_name,
          capabilities,
          enabled: true,
          lifecycle: "discovered",
          context_window: specs.context_window,
          quality_rating: specs.quality_rating,
        })
        .eq("id", prev.id);
      if (error) throw new Error(`Model update failed (${m.external_id}): ${error.message}`);
      updatedExistingCount++;
      continue;
    }

    const { error } = await supabase.from("models").insert({
      provider_id: providerId,
      account_id: accountId,
      external_id: toDbExternalId(accountId, m.external_id),
      display_name: m.display_name,
      capabilities,
      lifecycle: "discovered",
      enabled: true,
      context_window: specs.context_window,
      quality_rating: specs.quality_rating,
    });
    if (error) throw new Error(`Model insert failed (${m.external_id}): ${error.message}`);
    insertedNewCount++;
  }

  const removedStaleCount = await markStaleModelsSupabase(supabase, accountId, keepIds);

  return {
    ideVisibleFetchedCount: models.length,
    rawFetchedCount: rawCatalogCount(rawResponse),
    insertedNewCount,
    updatedExistingCount,
    unchangedCount,
    duplicatePreventedCount,
    removedStaleCount,
  };
}
