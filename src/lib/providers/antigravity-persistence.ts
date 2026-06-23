import {
  buildVisibleCatalogModels,
  extractRecommendedModelIds,
  type AntigravityLiveModelEntry,
} from "./antigravity-live-snapshot";
import type { ProviderModelInput } from "./models-persistence";

export type AntigravityUpsertModelInput = ProviderModelInput & {
  extra_capabilities?: Record<string, unknown>;
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
