import { describe, it, expect } from "vitest";
import {
  parseAntigravityFetchedModels,
  parseModelQuota,
  parseRemainingFraction,
  buildAntigravityLiveFetchSnapshot,
  mergeDbOverlay,
  extractRecommendedModelIds,
  buildVisibleCatalogModels,
  applyTestResultsToSnapshot,
  isEligibleForRouting,
  buildAntigravityQuotaGroups,
  buildQuotaGroupsFromModelCatalog,
  buildAntigravitySnapshotFromDbRows,
  resolveAntigravityDisplayQuotaGroups,
  formatAntigravityFetchToast,
  type AntigravityLiveModelEntry,
} from "./antigravity-live-snapshot";
import { providerExternalId } from "./model-keys";

const RECOMMENDED_IDS = [
  "gemini-3.5-flash-medium",
  "gemini-3.5-flash-high",
  "claude-sonnet-4.6-thinking",
];

const RECOMMENDED_MODELS: Record<string, AntigravityLiveModelEntry> = {
  "gemini-3.5-flash-medium": {
    displayName: "Gemini 3.5 Flash (Medium) - Fast",
    supportsImages: true,
    quotaInfo: { remainingFraction: 0.8, resetTime: "2026-06-22T12:00:00Z" },
  },
  "gemini-3.5-flash-high": {
    displayName: "Gemini 3.5 Flash (High) - Fast",
    quotaInfo: { remainingFraction: 0.65 },
  },
  "claude-sonnet-4.6-thinking": {
    displayName: "Claude Sonnet 4.6 (Thinking)",
    supportsThinking: true,
    quotaInfo: { remainingFraction: "0.5" },
  },
};

const RAW_ONLY_MODELS: Record<string, AntigravityLiveModelEntry> = {
  chat_20706: { displayName: "chat_20706" },
  chat_23310: { displayName: "chat_23310" },
  tab_jump_flash_lite_preview: { displayName: "Tab Jump Flash Lite Preview" },
  "no-display-name-model": { supportsImages: false },
};

const FULL_RAW_MODELS = { ...RECOMMENDED_MODELS, ...RAW_ONLY_MODELS };

function buildRecommendedResponse(
  models: Record<string, AntigravityLiveModelEntry>,
  recommendedIds: string[],
  form: "object" | "array" = "object",
) {
  if (form === "object") {
    return {
      models,
      agentModelSorts: {
        Recommended: { groups: [{ modelIds: recommendedIds }] },
      },
    };
  }
  return {
    models,
    agentModelSorts: [{ displayName: "Recommended", groups: [{ modelIds: recommendedIds }] }],
  };
}

describe("antigravity live snapshot — raw vs visible catalog", () => {
  it("1. raw catalog includes all response.models keys", () => {
    const snapshot = buildAntigravityLiveFetchSnapshot({
      rawResponse: buildRecommendedResponse(FULL_RAW_MODELS, RECOMMENDED_IDS),
      persistenceStats: { insertedNewCount: 0, updatedExistingCount: 7, unchangedCount: 0 },
    });
    expect(snapshot.rawCatalog.count).toBe(Object.keys(FULL_RAW_MODELS).length);
    expect(snapshot.rawCatalog.models.map((m) => m.id).sort()).toEqual(
      Object.keys(FULL_RAW_MODELS).sort(),
    );
  });

  it("2. visible catalog uses only agentModelSorts.Recommended.groups[].modelIds", () => {
    const snapshot = buildAntigravityLiveFetchSnapshot({
      rawResponse: buildRecommendedResponse(FULL_RAW_MODELS, RECOMMENDED_IDS),
      persistenceStats: { insertedNewCount: 0, updatedExistingCount: 7, unchangedCount: 0 },
    });
    expect(snapshot.visibleCatalog.source).toBe("agentModelSorts.Recommended");
    expect(snapshot.visibleCatalog.modelIds).toEqual(RECOMMENDED_IDS);
    expect(snapshot.visibleCatalog.models.map((m) => m.id)).toEqual(RECOMMENDED_IDS);
    expect(snapshot.visibleCatalog.count).toBe(RECOMMENDED_IDS.length);
  });

  it("3. chat_* raw models do not appear in visible catalog unless listed in Recommended", () => {
    const snapshot = buildAntigravityLiveFetchSnapshot({
      rawResponse: buildRecommendedResponse(FULL_RAW_MODELS, RECOMMENDED_IDS),
      persistenceStats: { insertedNewCount: 0, updatedExistingCount: 7, unchangedCount: 0 },
    });
    const visibleIds = snapshot.visibleCatalog.models.map((m) => m.id);
    expect(visibleIds).not.toContain("chat_20706");
    expect(visibleIds).not.toContain("chat_23310");
    expect(snapshot.rawCatalog.models.some((m) => m.id === "chat_20706")).toBe(true);
  });

  it("4. tab_jump_* raw models do not appear in visible catalog unless listed in Recommended", () => {
    const snapshot = buildAntigravityLiveFetchSnapshot({
      rawResponse: buildRecommendedResponse(FULL_RAW_MODELS, RECOMMENDED_IDS),
      persistenceStats: { insertedNewCount: 0, updatedExistingCount: 7, unchangedCount: 0 },
    });
    expect(snapshot.visibleCatalog.models.some((m) => m.id.startsWith("tab_jump_"))).toBe(false);
    expect(snapshot.rawCatalog.models.some((m) => m.id === "tab_jump_flash_lite_preview")).toBe(
      true,
    );
  });

  it("5. visible model displayName is resolved from response.models[id].displayName", () => {
    const snapshot = buildAntigravityLiveFetchSnapshot({
      rawResponse: buildRecommendedResponse(FULL_RAW_MODELS, RECOMMENDED_IDS),
      persistenceStats: { insertedNewCount: 0, updatedExistingCount: 7, unchangedCount: 0 },
    });
    const flash = snapshot.visibleCatalog.models.find((m) => m.id === "gemini-3.5-flash-medium");
    expect(flash?.displayName).toBe("Gemini 3.5 Flash (Medium) - Fast");
    expect(flash?.displayNameSource).toBe("backend");
  });

  it("6. missing Recommended sort does not hardcode fallback IDE names", () => {
    const snapshot = buildAntigravityLiveFetchSnapshot({
      rawResponse: { models: FULL_RAW_MODELS },
      persistenceStats: { insertedNewCount: 0, updatedExistingCount: 7, unchangedCount: 0 },
    });
    expect(snapshot.visibleCatalog.source).toBe("missing-recommended-sort");
    expect(snapshot.visibleCatalog.count).toBe(0);
    expect(snapshot.visibleCatalog.models).toEqual([]);
    expect(snapshot.diagnostics.recommendedSortFound).toBe(false);
    expect(snapshot.stats.rawFetchedCount).toBe(7);
    expect(snapshot.stats.visibleCount).toBe(0);
  });

  it("7. missing model ID referenced by Recommended is reported in missingModelIds", () => {
    const ids = [...RECOMMENDED_IDS, "missing-from-catalog"];
    const snapshot = buildAntigravityLiveFetchSnapshot({
      rawResponse: buildRecommendedResponse(FULL_RAW_MODELS, ids),
      persistenceStats: { insertedNewCount: 0, updatedExistingCount: 7, unchangedCount: 0 },
    });
    expect(snapshot.visibleCatalog.missingModelIds).toEqual(["missing-from-catalog"]);
    expect(snapshot.visibleCatalog.count).toBe(RECOMMENDED_IDS.length);
  });

  it("8. main UI data builder uses visibleCatalog.models (not raw catalog)", () => {
    const snapshot = buildAntigravityLiveFetchSnapshot({
      rawResponse: buildRecommendedResponse(FULL_RAW_MODELS, RECOMMENDED_IDS),
      persistenceStats: { insertedNewCount: 0, updatedExistingCount: 7, unchangedCount: 0 },
    });
    const mainList = snapshot.visibleCatalog.models;
    const debugRawList = snapshot.rawCatalog.models;
    expect(mainList).toHaveLength(RECOMMENDED_IDS.length);
    expect(debugRawList).toHaveLength(Object.keys(FULL_RAW_MODELS).length);
    expect(mainList.some((m) => m.id.startsWith("chat_"))).toBe(false);
  });

  it("9. raw drawer still has all raw catalog models", () => {
    const snapshot = buildAntigravityLiveFetchSnapshot({
      rawResponse: buildRecommendedResponse(FULL_RAW_MODELS, RECOMMENDED_IDS),
      persistenceStats: { insertedNewCount: 0, updatedExistingCount: 7, unchangedCount: 0 },
    });
    expect(snapshot.rawCatalog.models).toHaveLength(7);
    expect(snapshot.rawCatalog.models.map((m) => m.id).sort()).toEqual(
      Object.keys(FULL_RAW_MODELS).sort(),
    );
  });

  it("10. external fetch toast shows IDE-visible count only", () => {
    const snapshot = buildAntigravityLiveFetchSnapshot({
      rawResponse: buildRecommendedResponse(FULL_RAW_MODELS, RECOMMENDED_IDS),
      persistenceStats: { insertedNewCount: 1, updatedExistingCount: 5, unchangedCount: 0 },
    });
    expect(formatAntigravityFetchToast(snapshot.stats)).toBe("3 Models Fetched");
  });

  it("11. failed models are not auto-selected for routing", () => {
    const snapshot = buildAntigravityLiveFetchSnapshot({
      rawResponse: buildRecommendedResponse(FULL_RAW_MODELS, RECOMMENDED_IDS),
      persistenceStats: { insertedNewCount: 0, updatedExistingCount: 7, unchangedCount: 0 },
    });
    const next = applyTestResultsToSnapshot(
      snapshot,
      [{ external_id: RECOMMENDED_IDS[0]!, ok: false, error: "rate limited" }],
      true,
    );
    const failed = next.visibleCatalog.models.find((m) => m.id === RECOMMENDED_IDS[0]);
    expect(failed?.routing.selected).toBe(false);
    expect(failed?.routing.eligible).toBe(false);
  });

  it("12. exhausted models are not auto-selected for routing", () => {
    const exhaustedModels = {
      ...RECOMMENDED_MODELS,
      "exhausted-only": {
        displayName: "Exhausted",
        quotaInfo: { remainingFraction: 0, isExhausted: true },
      },
    };
    const ids = [...RECOMMENDED_IDS, "exhausted-only"];
    const snapshot = buildAntigravityLiveFetchSnapshot({
      rawResponse: buildRecommendedResponse(exhaustedModels, ids),
      persistenceStats: { insertedNewCount: 0, updatedExistingCount: 0, unchangedCount: 0 },
    });
    const next = applyTestResultsToSnapshot(
      snapshot,
      [{ external_id: "exhausted-only", ok: true, latency_ms: 100 }],
      true,
    );
    const m = next.visibleCatalog.models.find((x) => x.id === "exhausted-only");
    expect(m?.routing.selected).toBe(false);
    expect(isEligibleForRouting(m!)).toBe(false);
  });
});

describe("buildAntigravityQuotaGroups", () => {
  it("builds GEM and OPT from recommended models when sorts omit quota groups", () => {
    const models = {
      "gemini-3.5-flash-medium": {
        displayName: "Gemini 3.5 Flash (Medium) - Fast",
        apiProvider: "google",
        quotaInfo: { remainingFraction: 1, resetTime: "2026-06-22T10:41:00Z" },
      },
      "claude-sonnet-4.6-thinking": {
        displayName: "Claude Sonnet 4.6 (Thinking)",
        modelProvider: "anthropic",
        quotaInfo: { remainingFraction: 1, resetTime: "2026-06-22T08:30:00Z" },
      },
    };
    const groups = buildAntigravityQuotaGroups(
      {
        models,
        agentModelSorts: [
          {
            displayName: "Recommended",
            groups: [{ modelIds: Object.keys(models) }],
          },
        ],
      },
      models,
    );
    expect(groups.map((g) => g.name)).toEqual(["Gemini Models", "Claude and GPT Models"]);
    expect(groups[0]?.fiveHourQuota?.resetTime).toContain("10:41");
    expect(groups[1]?.fiveHourQuota?.resetTime).toContain("08:30");
  });

  it("resolveAntigravityDisplayQuotaGroups rebuilds GEM/OPT from stored models map", () => {
    const groups = resolveAntigravityDisplayQuotaGroups({
      groups: [],
      models: {
        "gemini-3.5-flash-high": {
          remainingFraction: 0.5,
          resetTime: "2026-06-22T10:41:00Z",
          isExhausted: false,
        },
        "claude-opus-4.6-thinking": {
          remainingFraction: 0.25,
          resetTime: "2026-06-22T08:30:00Z",
          isExhausted: false,
        },
      },
    });
    expect(groups).toHaveLength(2);
    expect(groups.map((g) => g.name)).toEqual(["Gemini Models", "Claude and GPT Models"]);
  });
});

describe("antigravity live snapshot — parser helpers", () => {
  it("extractRecommendedModelIds supports array form", () => {
    const ids = extractRecommendedModelIds(
      buildRecommendedResponse(FULL_RAW_MODELS, RECOMMENDED_IDS, "array"),
    );
    expect(ids).toEqual(RECOMMENDED_IDS);
  });

  it("falls back to model ID when displayName is missing on a visible entry", () => {
    const models = {
      "no-display-name-model": { supportsImages: false },
    };
    const { models: visible } = buildVisibleCatalogModels(["no-display-name-model"], models);
    expect(visible[0]?.displayName).toBe("no-display-name-model");
    expect(visible[0]?.displayNameSource).toBe("fallback-to-id");
  });

  it("parses numeric string remainingFraction", () => {
    expect(parseRemainingFraction("0.5")).toBe(0.5);
    const q = parseModelQuota(RECOMMENDED_MODELS["claude-sonnet-4.6-thinking"]!);
    expect(q?.remainingFraction).toBe(0.5);
  });

  it("parseAntigravityFetchedModels includes every raw key", () => {
    const models = parseAntigravityFetchedModels(FULL_RAW_MODELS);
    expect(models).toHaveLength(7);
  });

  it("buildAntigravitySnapshotFromDbRows restores saved IDE-visible models", () => {
    const snapshot = buildAntigravitySnapshotFromDbRows([
      {
        id: "row-1",
        external_id: "acct:acc:gemini-3.5-flash-medium",
        display_name: "Gemini 3.5 Flash (Medium) - Fast",
        capabilities: {
          provider_external_id: "gemini-3.5-flash-medium",
          list: ["chat", "tools"],
          antigravity_raw: { displayName: "Gemini 3.5 Flash (Medium) - Fast" },
          quota: { remainingFraction: 0.8, resetTime: "2026-06-22T10:00:00Z" },
        },
        test_status: "working",
        enabled: true,
      },
      {
        id: "row-2",
        external_id: "chat_20706",
        display_name: "chat_20706",
        capabilities: { provider_external_id: "chat_20706", stale: true },
        test_status: "working",
        enabled: true,
      },
    ]);
    expect(snapshot?.visibleCatalog.count).toBe(1);
    expect(snapshot?.visibleCatalog.models[0]?.id).toBe("gemini-3.5-flash-medium");
    expect(snapshot?.diagnostics.loadedFromDb).toBe(true);
  });
});

describe("mergeDbOverlay", () => {
  const accountId = "acff5076-b850-4c9c-9776-f9a8531c6e03";

  it("does not add DB models absent from visible catalog", () => {
    const snapshot = buildAntigravityLiveFetchSnapshot({
      rawResponse: buildRecommendedResponse(FULL_RAW_MODELS, RECOMMENDED_IDS),
      persistenceStats: { insertedNewCount: 0, updatedExistingCount: 7, unchangedCount: 0 },
    });
    const dbRows = [
      {
        id: "db-only-1",
        external_id: "chat_20706",
        display_name: "chat_20706",
        test_status: "working",
        enabled: true,
      },
      {
        id: "db-only-2",
        external_id: `acct:${accountId}:gemini-3.5-flash-medium`,
        display_name: "Gemini 3.5 Flash (Medium) - Fast",
        capabilities: { provider_external_id: "gemini-3.5-flash-medium", list: ["chat"] },
        test_status: "working",
        enabled: true,
      },
    ];
    const merged = mergeDbOverlay(snapshot.visibleCatalog.models, dbRows, providerExternalId);
    expect(merged).toHaveLength(RECOMMENDED_IDS.length);
    expect(merged.some((m) => m.id === "chat_20706")).toBe(false);
    const enriched = merged.find((m) => m.id === "gemini-3.5-flash-medium");
    expect(enriched?.routing.dbRowId).toBe("db-only-2");
  });
});
