/** Unified model catalog sync — test-before-insert, skip existing, orphan cleanup. Server-only. */

import type { ModelTestResult, StoredCredentials } from "./adapters/types";
import { resolveModelSpecs } from "./model-keys";

export type LiveModelInput = {
  external_id: string;
  display_name: string;
  capabilities: string[];
  extra_capabilities?: Record<string, unknown>;
  context_window?: number;
  quality_rating?: number;
};

export type ModelSyncStats = {
  count: number;
  added: string[];
  removed: string[];
  unchanged: number;
  updated: number;
  linked: number;
  tested: number;
  failed: number;
};

type CatalogRow = {
  id: string;
  external_id: string;
  display_name: string;
  capabilities: Record<string, unknown> | null;
};

type AccountLinkRow = {
  id: string;
  model_id: string;
  enabled: boolean;
  test_status: string;
  lifecycle: string;
  models: CatalogRow | CatalogRow[];
};

function buildCapabilities(m: LiveModelInput) {
  return {
    list: m.capabilities,
    provider_external_id: m.external_id,
    ...(m.extra_capabilities ?? {}),
  };
}

function catalogFromJoin(row: AccountLinkRow): CatalogRow {
  const m = row.models;
  return Array.isArray(m) ? m[0]! : m;
}

export async function deleteModelIfOrphaned(supabase: any, modelId: string): Promise<boolean> {
  const { count, error } = await supabase
    .from("account_models")
    .select("id", { count: "exact", head: true })
    .eq("model_id", modelId);
  if (error) throw new Error(error.message);
  if ((count ?? 0) > 0) return false;
  const { error: delErr } = await supabase.from("models").delete().eq("id", modelId);
  if (delErr) throw new Error(delErr.message);
  return true;
}

export async function unlinkAccountModels(supabase: any, accountId: string): Promise<void> {
  const { data: links, error } = await supabase
    .from("account_models")
    .select("id, model_id")
    .eq("account_id", accountId);
  if (error) throw new Error(error.message);

  const modelIds = [...new Set((links ?? []).map((l: { model_id: string }) => l.model_id))];

  const { error: delLinksErr } = await supabase
    .from("account_models")
    .delete()
    .eq("account_id", accountId);
  if (delLinksErr) throw new Error(delLinksErr.message);

  for (const modelId of modelIds) {
    await deleteModelIfOrphaned(supabase, modelId);
  }
}

export async function syncModelsForAccount(
  supabase: any,
  opts: {
    accountId: string;
    providerId: string;
    providerSlug: string;
    liveModels: LiveModelInput[];
    creds: StoredCredentials;
    testModel: (creds: StoredCredentials, externalId: string) => Promise<ModelTestResult>;
  },
): Promise<ModelSyncStats> {
  const { accountId, providerId, providerSlug, liveModels, creds, testModel } = opts;
  const stats: ModelSyncStats = {
    count: liveModels.length,
    added: [],
    removed: [],
    unchanged: 0,
    updated: 0,
    linked: 0,
    tested: 0,
    failed: 0,
  };

  const liveByExt = new Map(liveModels.map((m) => [m.external_id, m]));
  const liveIds = new Set(liveModels.map((m) => m.external_id));

  const { data: catalogRows, error: catErr } = await supabase
    .from("models")
    .select("id, external_id, display_name, capabilities")
    .eq("provider_id", providerId);
  if (catErr) throw new Error(catErr.message);

  const catalogByExt = new Map<string, CatalogRow>(
    (catalogRows ?? []).map((r: CatalogRow) => [r.external_id, r]),
  );

  const { data: accountLinks, error: linkErr } = await supabase
    .from("account_models")
    .select("id, model_id, enabled, test_status, lifecycle, models(id, external_id, display_name, capabilities)")
    .eq("account_id", accountId);
  if (linkErr) throw new Error(linkErr.message);

  const linkByExt = new Map<string, AccountLinkRow>();
  for (const row of accountLinks ?? []) {
    const cat = catalogFromJoin(row as AccountLinkRow);
    linkByExt.set(cat.external_id, row as AccountLinkRow);
  }

  // Remove models no longer returned by provider for this account
  for (const [ext, link] of linkByExt) {
    if (liveIds.has(ext)) continue;
    const cat = catalogFromJoin(link);
    const { error } = await supabase.from("account_models").delete().eq("id", link.id);
    if (error) throw new Error(`Unlink failed (${ext}): ${error.message}`);
    stats.removed.push(ext);
    linkByExt.delete(ext);
    await deleteModelIfOrphaned(supabase, cat.id);
  }

  for (const m of liveModels) {
    const existingLink = linkByExt.get(m.external_id);
    if (existingLink) {
      stats.unchanged++;
      continue;
    }

    const catalogRow = catalogByExt.get(m.external_id);
    if (catalogRow) {
      const { error } = await supabase.from("account_models").insert({
        account_id: accountId,
        model_id: catalogRow.id,
        enabled: true,
        test_status: "untested",
        lifecycle: "discovered",
      });
      if (error) throw new Error(`Link failed (${m.external_id}): ${error.message}`);
      stats.linked++;
      continue;
    }

    stats.tested++;
    const test = await testModel(creds, m.external_id);
    if (!test.ok) {
      stats.failed++;
      continue;
    }

    const capabilities = buildCapabilities(m);
    const specs =
      m.context_window != null && m.quality_rating != null
        ? { context_window: m.context_window, quality_rating: m.quality_rating }
        : resolveModelSpecs(m.external_id, providerSlug, capabilities, null, null);

    const { data: inserted, error: insErr } = await supabase
      .from("models")
      .insert({
        provider_id: providerId,
        external_id: m.external_id,
        display_name: m.display_name,
        capabilities,
        lifecycle: "discovered",
        context_window: specs.context_window,
        quality_rating: specs.quality_rating,
      })
      .select("id")
      .single();
    if (insErr) throw new Error(`Model insert failed (${m.external_id}): ${insErr.message}`);

    const { error: linkInsErr } = await supabase.from("account_models").insert({
      account_id: accountId,
      model_id: inserted.id,
      enabled: true,
      test_status: "working",
      lifecycle: "approved",
      latency_ms: test.latency_ms,
      last_tested_at: new Date().toISOString(),
      last_test_error: null,
    });
    if (linkInsErr) throw new Error(`Account link failed (${m.external_id}): ${linkInsErr.message}`);

    catalogByExt.set(m.external_id, {
      id: inserted.id,
      external_id: m.external_id,
      display_name: m.display_name,
      capabilities,
    });
    stats.added.push(m.external_id);
  }

  return stats;
}

export async function countAccountModels(
  supabase: any,
  accountId: string,
): Promise<{ total: number; enabled: number }> {
  const { data: rows, error } = await supabase
    .from("account_models")
    .select("enabled")
    .eq("account_id", accountId);
  if (error) throw new Error(error.message);
  const list = rows ?? [];
  return {
    total: list.length,
    enabled: list.filter((r: { enabled?: boolean }) => r.enabled).length,
  };
}

export async function updateAccountModelTestResult(
  supabase: any,
  accountId: string,
  modelId: string,
  result: ModelTestResult,
  opts?: { enabled?: boolean },
): Promise<void> {
  const { error } = await supabase
    .from("account_models")
    .update({
      test_status: result.ok ? "working" : "failed",
      latency_ms: result.latency_ms,
      last_test_error: result.ok ? null : (result.error ?? null),
      last_tested_at: new Date().toISOString(),
      lifecycle: result.ok ? "approved" : "blocked",
      ...(opts?.enabled !== undefined ? { enabled: opts.enabled } : { enabled: result.ok }),
    })
    .eq("account_id", accountId)
    .eq("model_id", modelId);
  if (error) throw new Error(error.message);
}
