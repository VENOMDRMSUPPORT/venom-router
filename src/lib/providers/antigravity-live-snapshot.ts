/* Antigravity live fetch snapshot — client-safe parser and UI builders. */

import type { QuotaGroup, QuotaPeriod } from "./adapters/_shared/quota-types";
import { providerExternalId } from "./model-keys";

export type AntigravityLiveModelEntry = {
  displayName?: string;
  maxTokens?: number;
  maxOutputTokens?: number;
  supportsImages?: boolean;
  supportsThinking?: boolean;
  isInternal?: boolean;
  recommended?: boolean;
  apiProvider?: string;
  modelProvider?: string;
  quotaInfo?: {
    remainingFraction?: number | string;
    resetTime?: string;
    isExhausted?: boolean;
  };
  [key: string]: unknown;
};

export type AntigravityFetchedModel = {
  id: string;
  displayName: string;
  displayNameSource: "backend" | "fallback-to-id";
  raw: unknown;
  capabilities: string[];
  quota?: {
    remainingFraction?: number;
    remainingPercentage?: number;
    usedPercentage?: number;
    resetTime?: string;
    isExhausted?: boolean;
  };
  test: {
    status: "untested" | "testing" | "working" | "failed";
    testedAt?: string;
    latencyMs?: number;
    error?: string;
  };
  routing: {
    selected: boolean;
    eligible: boolean;
    reason?: string;
    dbRowId?: string;
  };
};

export type AntigravityLiveFetchSnapshot = {
  provider: "antigravity";
  source: "fetchAvailableModels";
  projectId?: string;
  planTier?: string;
  fetchedAt: string;
  rawResponse: unknown;
  rawCatalog: {
    count: number;
    models: AntigravityFetchedModel[];
  };
  visibleCatalog: {
    source: "agentModelSorts.Recommended" | "missing-recommended-sort";
    count: number;
    modelIds: string[];
    models: AntigravityFetchedModel[];
    missingModelIds: string[];
  };
  stats: {
    rawFetchedCount: number;
    visibleCount: number;
    insertedNewCount: number;
    updatedExistingCount: number;
    unchangedCount: number;
    duplicatePreventedCount: number;
    removedStaleCount: number;
    testedAfterFetchCount: number;
    selectedForRoutingCount: number;
    workingCount: number;
    failedCount: number;
    untestedCount: number;
    exhaustedCount: number;
  };
  diagnostics: {
    dbMixedIntoModal: false;
    hardcodedFiltersActive: false;
    recommendedSortFound: boolean;
    rawCatalogShownAsMainList: false;
    loadCodeAssistUsed: boolean;
    loadedFromDb?: boolean;
  };
};

export type DbModelOverlayRow = {
  id: string;
  external_id: string;
  display_name?: string | null;
  capabilities?: Record<string, unknown> | null;
  test_status?: string | null;
  enabled?: boolean | null;
  last_test_error?: string | null;
  last_tested_at?: string | null;
  latency_ms?: number | null;
};

function extractIdsFromGroups(groups: unknown): string[] {
  if (!Array.isArray(groups)) return [];
  const ids: string[] = [];
  for (const group of groups) {
    if (!group || typeof group !== "object") continue;
    const modelIds = (group as Record<string, unknown>).modelIds;
    if (!Array.isArray(modelIds)) continue;
    for (const id of modelIds) {
      if (typeof id === "string" && id.length > 0) ids.push(id);
    }
  }
  return ids;
}

/** Parse Recommended model IDs from agentModelSorts (array or object form). */
export function extractRecommendedModelIds(raw: unknown): string[] {
  if (!raw || typeof raw !== "object") return [];
  const root = raw as Record<string, unknown>;
  const sorts = root.agentModelSorts;
  if (!sorts || typeof sorts !== "object") return [];

  if (!Array.isArray(sorts)) {
    const rec =
      (sorts as Record<string, unknown>).Recommended ??
      (sorts as Record<string, unknown>).recommended;
    if (rec && typeof rec === "object") {
      return extractIdsFromGroups((rec as Record<string, unknown>).groups);
    }
    return [];
  }

  const ids: string[] = [];
  for (const sort of sorts) {
    if (!sort || typeof sort !== "object") continue;
    const s = sort as Record<string, unknown>;
    const name = typeof s.displayName === "string" ? s.displayName.trim().toLowerCase() : "";
    if (name !== "recommended") continue;
    ids.push(...extractIdsFromGroups(s.groups));
  }
  return ids;
}

/** Quota UI groups (GEM / OPT) from agentModelSorts — excludes Recommended. */
export function buildAntigravityQuotaGroups(
  rawResponse: unknown,
  liveModels: Record<string, AntigravityLiveModelEntry>,
): QuotaGroup[] {
  if (!rawResponse || typeof rawResponse !== "object") return [];
  const sorts = (rawResponse as Record<string, unknown>).agentModelSorts;
  const groups: QuotaGroup[] = [];

  function pushGroup(displayName: string, modelIds: string[]) {
    if (!displayName || displayName.trim().toLowerCase() === "recommended") return;
    if (!modelIds.length) return;
    groups.push({
      name: displayName,
      modelIds,
      fiveHourQuota: quotaPeriodForModelIds(modelIds, liveModels),
    });
  }

  function consumeGroups(displayName: string, sortGroups: unknown) {
    if (!Array.isArray(sortGroups)) return;
    for (const group of sortGroups) {
      if (!group || typeof group !== "object") continue;
      const modelIds = (group as Record<string, unknown>).modelIds;
      if (!Array.isArray(modelIds)) continue;
      pushGroup(
        displayName,
        modelIds.filter((id): id is string => typeof id === "string" && id.length > 0),
      );
    }
  }

  if (Array.isArray(sorts)) {
    for (const sort of sorts) {
      if (!sort || typeof sort !== "object") continue;
      const s = sort as Record<string, unknown>;
      const name = typeof s.displayName === "string" ? s.displayName.trim() : "";
      consumeGroups(name, s.groups);
    }
  } else if (sorts && typeof sorts === "object") {
    for (const [key, val] of Object.entries(sorts as Record<string, unknown>)) {
      if (key.toLowerCase() === "recommended") continue;
      if (!val || typeof val !== "object") continue;
      const v = val as Record<string, unknown>;
      const name =
        typeof v.displayName === "string" && v.displayName.trim()
          ? v.displayName.trim()
          : key.replace(/([a-z])([A-Z])/g, "$1 $2");
      consumeGroups(name, v.groups);
    }
  }

  const recommendedIds = extractRecommendedModelIds(rawResponse);
  const catalogGroups = buildQuotaGroupsFromModelCatalog(liveModels, recommendedIds);
  return mergeGemOptQuotaGroups(groups, catalogGroups);
}

export type AntigravityQuotaBucket = "gemini" | "claude_gpt";

const GEMINI_GROUP_NAME = "Gemini Models";
const CLAUDE_GPT_GROUP_NAME = "Claude and GPT Models";

/** Classify a model into GEM vs OPT quota pool from API metadata (not hardcoded model list). */
export function classifyAntigravityQuotaBucket(
  modelId: string,
  entry?: AntigravityLiveModelEntry | null,
): AntigravityQuotaBucket | null {
  const text = [modelId, entry?.displayName, entry?.apiProvider, entry?.modelProvider]
    .filter((v): v is string => typeof v === "string" && v.length > 0)
    .join(" ")
    .toLowerCase();

  if (/\bgemini\b|\bgoogle\b/.test(text)) return "gemini";
  if (
    /\bclaude\b|\banthropic\b|\bgpt\b|\bopenai\b|\bopus\b|\bsonnet\b|\bhaiku\b|gpt-oss/.test(text)
  ) {
    return "claude_gpt";
  }
  return null;
}

function quotaPeriodForModelIds(
  modelIds: string[],
  liveModels: Record<string, AntigravityLiveModelEntry>,
): QuotaPeriod | undefined {
  for (const id of modelIds) {
    const quota = parseModelQuota(liveModels[id]);
    if (!quota?.resetTime) continue;
    return {
      remainingFraction: quota.remainingFraction ?? 0,
      resetTime: quota.resetTime,
      isExhausted: Boolean(quota.isExhausted),
    };
  }
  return undefined;
}

/** Build GEM + OPT bars from IDE-visible / recommended models when sorts omit quota groups. */
export function buildQuotaGroupsFromModelCatalog(
  liveModels: Record<string, AntigravityLiveModelEntry>,
  modelIds?: string[],
): QuotaGroup[] {
  const ids = modelIds && modelIds.length > 0 ? modelIds : Object.keys(liveModels ?? {});
  const geminiIds: string[] = [];
  const claudeGptIds: string[] = [];

  for (const id of ids) {
    const bucket = classifyAntigravityQuotaBucket(id, liveModels[id]);
    if (bucket === "gemini") geminiIds.push(id);
    else if (bucket === "claude_gpt") claudeGptIds.push(id);
  }

  const groups: QuotaGroup[] = [];
  if (geminiIds.length) {
    groups.push({
      name: GEMINI_GROUP_NAME,
      modelIds: geminiIds,
      fiveHourQuota: quotaPeriodForModelIds(geminiIds, liveModels),
    });
  }
  if (claudeGptIds.length) {
    groups.push({
      name: CLAUDE_GPT_GROUP_NAME,
      modelIds: claudeGptIds,
      fiveHourQuota: quotaPeriodForModelIds(claudeGptIds, liveModels),
    });
  }
  return groups;
}

function mergeGemOptQuotaGroups(primary: QuotaGroup[], fallback: QuotaGroup[]): QuotaGroup[] {
  const byName = new Map<string, QuotaGroup>();
  for (const g of [...primary, ...fallback]) {
    if (g.name !== GEMINI_GROUP_NAME && g.name !== CLAUDE_GPT_GROUP_NAME) continue;
    const prev = byName.get(g.name);
    if (!prev || (!prev.fiveHourQuota?.resetTime && g.fiveHourQuota?.resetTime)) {
      byName.set(g.name, g);
    }
  }
  const merged: QuotaGroup[] = [];
  const gem = byName.get(GEMINI_GROUP_NAME);
  const opt = byName.get(CLAUDE_GPT_GROUP_NAME);
  if (gem) merged.push(gem);
  if (opt) merged.push(opt);
  return merged;
}

/** Account-row display: always prefer GEM + OPT when available. */
export function resolveAntigravityDisplayQuotaGroups(
  extra: Record<string, unknown> | null | undefined,
): QuotaGroup[] {
  const stored = Array.isArray(extra?.groups) ? (extra!.groups as QuotaGroup[]) : [];
  const merged = mergeGemOptQuotaGroups(stored, []);
  if (merged.length >= 2) return merged;
  if (merged.length === 1 && stored.length) return merged;

  const modelsMap = extra?.models;
  if (!modelsMap || typeof modelsMap !== "object") return merged;

  const liveModels: Record<string, AntigravityLiveModelEntry> = {};
  for (const [id, info] of Object.entries(modelsMap as Record<string, unknown>)) {
    const q = info as { resetTime?: string; remainingFraction?: number };
    liveModels[id] = {
      displayName: id,
      quotaInfo: {
        remainingFraction: q.remainingFraction,
        resetTime: q.resetTime,
      },
    };
  }

  const usageQuotas = extra?.usageQuotas;
  if (usageQuotas && typeof usageQuotas === "object") {
    for (const [id, info] of Object.entries(
      usageQuotas as Record<string, { displayName?: string }>,
    )) {
      liveModels[id] = {
        ...(liveModels[id] ?? { displayName: id }),
        displayName: info.displayName ?? liveModels[id]?.displayName ?? id,
      };
    }
  }

  return mergeGemOptQuotaGroups(merged, buildQuotaGroupsFromModelCatalog(liveModels));
}

export function parseRemainingFraction(value: number | string | undefined): number | undefined {
  if (value === undefined || value === null || value === "") return undefined;
  const n = Number(value);
  if (!Number.isFinite(n)) return undefined;
  return Math.max(0, Math.min(1, n));
}

export function parseModelQuota(
  entry: AntigravityLiveModelEntry,
): AntigravityFetchedModel["quota"] | undefined {
  const q = entry?.quotaInfo;
  if (!q) return undefined;
  const remainingFraction = parseRemainingFraction(q.remainingFraction);
  const isExhausted =
    Boolean(q.isExhausted) || (remainingFraction !== undefined && remainingFraction <= 0);
  const result: AntigravityFetchedModel["quota"] = {
    resetTime: q.resetTime,
    isExhausted,
  };
  if (remainingFraction !== undefined) {
    result.remainingFraction = remainingFraction;
    result.remainingPercentage = remainingFraction * 100;
    result.usedPercentage = (1 - remainingFraction) * 100;
  }
  return result;
}

export function inferCapabilities(entry: AntigravityLiveModelEntry): string[] {
  const caps = ["chat", "tools"];
  if (entry.supportsImages) caps.push("vision");
  if (entry.supportsThinking) caps.push("reasoning");
  return caps;
}

export function parseSingleFetchedModel(
  id: string,
  entry: AntigravityLiveModelEntry,
): AntigravityFetchedModel {
  const hasDisplayName =
    typeof entry.displayName === "string" && entry.displayName.trim().length > 0;
  const quota = parseModelQuota(entry);
  const exhausted = Boolean(quota?.isExhausted);
  return {
    id,
    displayName: hasDisplayName ? entry.displayName!.trim() : id,
    displayNameSource: hasDisplayName ? "backend" : "fallback-to-id",
    raw: entry,
    capabilities: inferCapabilities(entry),
    quota,
    test: { status: "untested" },
    routing: {
      selected: false,
      eligible: !exhausted,
      reason: exhausted ? "Quota exhausted" : undefined,
    },
  };
}

/** Parse every model key from response.models — raw catalog only. */
export function parseAntigravityFetchedModels(
  liveModels: Record<string, AntigravityLiveModelEntry>,
): AntigravityFetchedModel[] {
  return Object.entries(liveModels ?? {}).map(([id, entry]) => parseSingleFetchedModel(id, entry));
}

export function buildVisibleCatalogModels(
  recommendedIds: string[],
  liveModels: Record<string, AntigravityLiveModelEntry>,
): { models: AntigravityFetchedModel[]; missingModelIds: string[] } {
  const models: AntigravityFetchedModel[] = [];
  const missingModelIds: string[] = [];
  const seen = new Set<string>();

  for (const modelId of recommendedIds) {
    if (seen.has(modelId)) continue;
    seen.add(modelId);
    const entry = liveModels[modelId];
    if (!entry) {
      missingModelIds.push(modelId);
      continue;
    }
    models.push(parseSingleFetchedModel(modelId, entry));
  }

  return { models, missingModelIds };
}

export function computeSnapshotTestStats(models: AntigravityFetchedModel[]) {
  let workingCount = 0;
  let failedCount = 0;
  let untestedCount = 0;
  let exhaustedCount = 0;
  for (const m of models) {
    if (m.test.status === "working") workingCount++;
    else if (m.test.status === "failed") failedCount++;
    else if (m.test.status === "untested") untestedCount++;
    if (m.quota?.isExhausted) exhaustedCount++;
  }
  return { workingCount, failedCount, untestedCount, exhaustedCount };
}

export type AntigravityStoredModelRow = {
  id: string;
  external_id: string;
  display_name: string;
  capabilities: Record<string, unknown> | null;
  test_status?: string | null;
  enabled?: boolean | null;
  last_test_error?: string | null;
  last_tested_at?: string | null;
  latency_ms?: number | null;
  updated_at?: string | null;
};

function isStaleStoredModel(caps: Record<string, unknown> | null | undefined): boolean {
  return Boolean(caps?.stale);
}

export function dbRowToAntigravityFetchedModel(
  row: AntigravityStoredModelRow,
): AntigravityFetchedModel {
  const caps = row.capabilities ?? {};
  const id = providerExternalId(row.external_id, caps);
  const raw = (caps.antigravity_raw as AntigravityLiveModelEntry | undefined) ?? {};
  const storedQuota = caps.quota as AntigravityFetchedModel["quota"] | undefined;

  const base = parseSingleFetchedModel(id, {
    ...raw,
    displayName:
      row.display_name?.trim() ||
      (typeof raw.displayName === "string" ? raw.displayName : undefined) ||
      id,
  });

  const quota = storedQuota ?? base.quota;
  const testStatus =
    row.test_status === "working"
      ? ("working" as const)
      : row.test_status === "failed"
        ? ("failed" as const)
        : ("untested" as const);
  const eligible = testStatus === "working" && !quota?.isExhausted;

  return {
    ...base,
    quota,
    test: {
      status: testStatus,
      testedAt: row.last_tested_at ?? undefined,
      latencyMs: row.latency_ms ?? undefined,
      error: row.last_test_error ?? undefined,
    },
    routing: {
      selected: Boolean(row.enabled) && eligible,
      eligible,
      dbRowId: row.id,
      reason:
        testStatus === "failed"
          ? (row.last_test_error ?? "Test failed")
          : quota?.isExhausted
            ? "Quota exhausted"
            : undefined,
    },
  };
}

/** Build modal snapshot from persisted IDE-visible models for an account. */
export function buildAntigravitySnapshotFromDbRows(
  rows: AntigravityStoredModelRow[],
  opts?: {
    projectId?: string;
    planTier?: string;
    fetchedAt?: string;
    lastSyncedAt?: string | null;
  },
): AntigravityLiveFetchSnapshot | null {
  const active = rows.filter((row) => !isStaleStoredModel(row.capabilities));
  if (!active.length) return null;

  const models = active.map(dbRowToAntigravityFetchedModel);
  const testStats = computeSnapshotTestStats(models);

  return {
    provider: "antigravity",
    source: "fetchAvailableModels",
    projectId: opts?.projectId,
    planTier: opts?.planTier,
    fetchedAt: opts?.fetchedAt ?? opts?.lastSyncedAt ?? new Date().toISOString(),
    rawResponse: { _source: "database", storedModelCount: models.length },
    rawCatalog: { count: 0, models: [] },
    visibleCatalog: {
      source: "agentModelSorts.Recommended",
      count: models.length,
      modelIds: models.map((m) => m.id),
      models,
      missingModelIds: [],
    },
    stats: {
      rawFetchedCount: 0,
      visibleCount: models.length,
      insertedNewCount: 0,
      updatedExistingCount: 0,
      unchangedCount: models.length,
      duplicatePreventedCount: 0,
      removedStaleCount: 0,
      testedAfterFetchCount: 0,
      selectedForRoutingCount: models.filter((m) => m.routing.selected).length,
      ...testStats,
    },
    diagnostics: {
      dbMixedIntoModal: false,
      hardcodedFiltersActive: false,
      recommendedSortFound: true,
      rawCatalogShownAsMainList: false,
      loadCodeAssistUsed: false,
      loadedFromDb: true,
    },
  };
}

export function mergeDbOverlay(
  models: AntigravityFetchedModel[],
  dbRows: DbModelOverlayRow[],
  providerExternalId: (dbExternalId: string, caps?: Record<string, unknown> | null) => string,
): AntigravityFetchedModel[] {
  const byProviderId = new Map<string, DbModelOverlayRow>();
  for (const row of dbRows) {
    byProviderId.set(providerExternalId(row.external_id, row.capabilities), row);
  }

  return models.map((m) => {
    const db = byProviderId.get(m.id);
    if (!db) return m;

    const testStatus =
      db.test_status === "working"
        ? ("working" as const)
        : db.test_status === "failed"
          ? ("failed" as const)
          : m.test.status;

    const eligible =
      testStatus === "working" && !m.quota?.isExhausted
        ? true
        : testStatus === "failed"
          ? false
          : m.routing.eligible;

    return {
      ...m,
      test: {
        status: testStatus,
        testedAt: db.last_tested_at ?? undefined,
        latencyMs: db.latency_ms ?? undefined,
        error: db.last_test_error ?? undefined,
      },
      routing: {
        ...m.routing,
        dbRowId: db.id,
        selected: Boolean(db.enabled) && eligible,
        eligible,
        reason:
          testStatus === "failed"
            ? (db.last_test_error ?? "Test failed")
            : m.quota?.isExhausted
              ? "Quota exhausted"
              : m.routing.reason,
      },
    };
  });
}

export function buildAntigravityLiveFetchSnapshot(input: {
  rawResponse: unknown;
  projectId?: string;
  planTier?: string;
  fetchedAt?: string;
  persistenceStats: {
    insertedNewCount: number;
    updatedExistingCount: number;
    unchangedCount: number;
    duplicatePreventedCount?: number;
    removedStaleCount?: number;
  };
  autoTestStats?: {
    testedAfterFetchCount: number;
    selectedForRoutingCount: number;
  };
  loadCodeAssistUsed?: boolean;
}): AntigravityLiveFetchSnapshot {
  const response =
    input.rawResponse && typeof input.rawResponse === "object"
      ? (input.rawResponse as { models?: Record<string, AntigravityLiveModelEntry> })
      : {};
  const liveModels = response.models ?? {};
  const rawCatalogModels = parseAntigravityFetchedModels(liveModels);
  const recommendedIds = extractRecommendedModelIds(input.rawResponse);
  const { models: visibleModels, missingModelIds } = buildVisibleCatalogModels(
    recommendedIds,
    liveModels,
  );
  const visibleSource =
    recommendedIds.length > 0
      ? ("agentModelSorts.Recommended" as const)
      : "missing-recommended-sort";
  const testStats = computeSnapshotTestStats(visibleModels);

  return {
    provider: "antigravity",
    source: "fetchAvailableModels",
    projectId: input.projectId,
    planTier: input.planTier,
    fetchedAt: input.fetchedAt ?? new Date().toISOString(),
    rawResponse: input.rawResponse,
    rawCatalog: {
      count: rawCatalogModels.length,
      models: rawCatalogModels,
    },
    visibleCatalog: {
      source: visibleSource,
      count: visibleModels.length,
      modelIds: recommendedIds,
      models: visibleModels,
      missingModelIds,
    },
    stats: {
      rawFetchedCount: rawCatalogModels.length,
      visibleCount: visibleModels.length,
      insertedNewCount: input.persistenceStats.insertedNewCount,
      updatedExistingCount: input.persistenceStats.updatedExistingCount,
      unchangedCount: input.persistenceStats.unchangedCount,
      duplicatePreventedCount: input.persistenceStats.duplicatePreventedCount ?? 0,
      removedStaleCount: input.persistenceStats.removedStaleCount ?? 0,
      testedAfterFetchCount: input.autoTestStats?.testedAfterFetchCount ?? 0,
      selectedForRoutingCount: input.autoTestStats?.selectedForRoutingCount ?? 0,
      ...testStats,
    },
    diagnostics: {
      dbMixedIntoModal: false,
      hardcodedFiltersActive: false,
      recommendedSortFound: recommendedIds.length > 0,
      rawCatalogShownAsMainList: false,
      loadCodeAssistUsed: input.loadCodeAssistUsed ?? false,
    },
  };
}

export function formatAntigravityFetchToast(stats: { visibleCount: number }): string {
  return `${stats.visibleCount} Models Fetched`;
}

export function applyTestResultsToSnapshot(
  snapshot: AntigravityLiveFetchSnapshot,
  results: Array<{ external_id: string; ok: boolean; latency_ms?: number; error?: string }>,
  autoSelectRouting: boolean,
): AntigravityLiveFetchSnapshot {
  let next = snapshot;
  for (const r of results) {
    next = patchModelTestResult(next, r.external_id, r);
  }
  if (!autoSelectRouting) return next;

  const models = next.visibleCatalog.models.map((m) => {
    const eligible = m.test.status === "working" && !m.quota?.isExhausted;
    return {
      ...m,
      routing: {
        ...m.routing,
        eligible,
        selected: eligible,
        reason: eligible
          ? undefined
          : m.quota?.isExhausted
            ? "Quota exhausted"
            : (m.test.error ?? "Test failed or not run"),
      },
    };
  });

  const testStats = computeSnapshotTestStats(models);
  const selectedForRoutingCount = models.filter((m) => m.routing.selected).length;

  return {
    ...next,
    visibleCatalog: {
      ...next.visibleCatalog,
      models,
      count: models.length,
    },
    stats: {
      ...next.stats,
      ...testStats,
      testedAfterFetchCount: results.length,
      selectedForRoutingCount,
    },
  };
}

export function isEligibleForRouting(model: AntigravityFetchedModel): boolean {
  return model.test.status === "working" && !model.quota?.isExhausted;
}

function patchVisibleModels(
  snapshot: AntigravityLiveFetchSnapshot,
  mapper: (m: AntigravityFetchedModel) => AntigravityFetchedModel,
): AntigravityLiveFetchSnapshot {
  const models = snapshot.visibleCatalog.models.map(mapper);
  const testStats = computeSnapshotTestStats(models);
  return {
    ...snapshot,
    visibleCatalog: {
      ...snapshot.visibleCatalog,
      count: models.length,
      models,
    },
    stats: { ...snapshot.stats, ...testStats },
  };
}

export type SnapshotFilters = {
  search?: string;
  status?: "all" | "working" | "failed" | "untested" | "exhausted";
  capability?: "all" | "chat" | "tools" | "vision" | "reasoning";
  quota?: "all" | "available" | "exhausted" | "unknown";
};

export function filterSnapshotModels(
  models: AntigravityFetchedModel[],
  filters: SnapshotFilters,
): AntigravityFetchedModel[] {
  let result = models;

  if (filters.search?.trim()) {
    const q = filters.search.trim().toLowerCase();
    result = result.filter(
      (m) => m.displayName.toLowerCase().includes(q) || m.id.toLowerCase().includes(q),
    );
  }

  if (filters.status && filters.status !== "all") {
    result = result.filter((m) => {
      if (filters.status === "exhausted") return Boolean(m.quota?.isExhausted);
      if (filters.status === "untested") return m.test.status === "untested";
      return m.test.status === filters.status;
    });
  }

  if (filters.capability && filters.capability !== "all") {
    result = result.filter((m) => m.capabilities.includes(filters.capability!));
  }

  if (filters.quota && filters.quota !== "all") {
    result = result.filter((m) => {
      if (filters.quota === "unknown") return !m.quota;
      if (filters.quota === "exhausted") return Boolean(m.quota?.isExhausted);
      return m.quota && !m.quota.isExhausted;
    });
  }

  return result;
}

export function patchModelTestResult(
  snapshot: AntigravityLiveFetchSnapshot,
  modelId: string,
  result: { ok: boolean; latency_ms?: number; error?: string },
): AntigravityLiveFetchSnapshot {
  return patchVisibleModels(snapshot, (m) => {
    if (m.id !== modelId) return m;
    const status = result.ok ? ("working" as const) : ("failed" as const);
    const eligible = result.ok && !m.quota?.isExhausted;
    return {
      ...m,
      test: {
        status,
        testedAt: new Date().toISOString(),
        latencyMs: result.latency_ms,
        error: result.ok ? undefined : result.error,
      },
      routing: {
        ...m.routing,
        eligible,
        selected: eligible,
        reason: result.ok ? undefined : (result.error ?? "Test failed"),
      },
    };
  });
}

export function setModelTesting(
  snapshot: AntigravityLiveFetchSnapshot,
  modelId: string,
): AntigravityLiveFetchSnapshot {
  return patchVisibleModels(snapshot, (m) =>
    m.id === modelId ? { ...m, test: { ...m.test, status: "testing" as const } } : m,
  );
}
