import { describe, it, expect } from "vitest";
import {
  buildIdeVisibleUpsertInput,
  upsertAntigravityIdeVisibleModelsStore,
  createModelStore,
  dedupeAccountModelRowsStore,
  type AntigravityUpsertModelInput,
} from "./antigravity-persistence";
import { providerExternalId } from "./model-keys";
import type { AntigravityLiveModelEntry } from "./antigravity-live-snapshot";

const ACCOUNT = "acff5076-b850-4c9c-9776-f9a8531c6e03";
const PROVIDER = "provider-antigravity";

const RECOMMENDED_IDS = ["gemini-a", "gemini-b"];
const ALL_MODELS: Record<string, AntigravityLiveModelEntry> = {
  "gemini-a": { displayName: "Gemini A" },
  "gemini-b": { displayName: "Gemini B" },
  chat_20706: { displayName: "chat_20706" },
  tab_jump_x: { displayName: "Tab Jump" },
};

function rawResponse(ids: string[]) {
  return {
    models: ALL_MODELS,
    agentModelSorts: { Recommended: { groups: [{ modelIds: ids }] } },
  };
}

describe("antigravity-persistence", () => {
  it("5. upsert inserts only IDE-visible models, not raw catalog", () => {
    const input = buildIdeVisibleUpsertInput(rawResponse(RECOMMENDED_IDS));
    expect(input).toHaveLength(2);
    expect(input.map((m) => m.external_id)).toEqual(RECOMMENDED_IDS);
    expect(input.some((m) => m.external_id === "chat_20706")).toBe(false);
  });

  it("6. repeated fetch does not create duplicates", () => {
    const store = createModelStore(true);
    const stats1 = upsertAntigravityIdeVisibleModelsStore(
      store,
      ACCOUNT,
      PROVIDER,
      rawResponse(RECOMMENDED_IDS),
    );
    const stats2 = upsertAntigravityIdeVisibleModelsStore(
      store,
      ACCOUNT,
      PROVIDER,
      rawResponse(RECOMMENDED_IDS),
    );
    expect(stats1.insertedNewCount).toBe(2);
    expect(stats2.insertedNewCount).toBe(0);
    expect(store.listByAccount(ACCOUNT)).toHaveLength(2);
  });

  it("7. existing model row is updated, not duplicated", () => {
    const store = createModelStore(true);
    upsertAntigravityIdeVisibleModelsStore(store, ACCOUNT, PROVIDER, rawResponse(RECOMMENDED_IDS));
    const stats = upsertAntigravityIdeVisibleModelsStore(
      store,
      ACCOUNT,
      PROVIDER,
      rawResponse(RECOMMENDED_IDS),
    );
    expect(stats.updatedExistingCount + stats.unchangedCount).toBe(2);
    expect(store.listByAccount(ACCOUNT)).toHaveLength(2);
  });

  it("8. new Recommended model is inserted automatically", () => {
    const store = createModelStore(true);
    const withC = {
      ...ALL_MODELS,
      "gemini-c": { displayName: "Gemini C" },
    };
    const resp = (ids: string[]) => ({
      models: withC,
      agentModelSorts: { Recommended: { groups: [{ modelIds: ids }] } },
    });
    upsertAntigravityIdeVisibleModelsStore(store, ACCOUNT, PROVIDER, resp(RECOMMENDED_IDS));
    const stats = upsertAntigravityIdeVisibleModelsStore(
      store,
      ACCOUNT,
      PROVIDER,
      resp([...RECOMMENDED_IDS, "gemini-c"]),
    );
    expect(stats.insertedNewCount).toBe(1);
    const active = store
      .listByAccount(ACCOUNT)
      .filter((r) => !(r.capabilities as Record<string, unknown>)?.stale);
    expect(active).toHaveLength(3);
  });

  it("9. removed Recommended model becomes stale/disabled", () => {
    const store = createModelStore(true);
    upsertAntigravityIdeVisibleModelsStore(store, ACCOUNT, PROVIDER, rawResponse(RECOMMENDED_IDS));
    const stats = upsertAntigravityIdeVisibleModelsStore(
      store,
      ACCOUNT,
      PROVIDER,
      rawResponse(["gemini-a"]),
    );
    expect(stats.removedStaleCount).toBe(1);
    const stale = store.listByAccount(ACCOUNT).find((r) => r.capabilities?.stale);
    expect(stale?.enabled).toBe(false);
    expect(providerExternalId(stale!.external_id, stale!.capabilities)).toBe("gemini-b");
  });

  it("dedupe merges legacy bare external_id rows", () => {
    const store = createModelStore(true);
    store.insert({
      provider_id: PROVIDER,
      account_id: ACCOUNT,
      external_id: "chat_20706",
      display_name: "legacy",
      capabilities: { provider_external_id: "chat_20706", list: ["chat"] },
      enabled: true,
      lifecycle: "discovered",
    });
    store.insert({
      provider_id: PROVIDER,
      account_id: ACCOUNT,
      external_id: `acct:${ACCOUNT}:chat_20706`,
      display_name: "canonical",
      capabilities: { provider_external_id: "chat_20706", list: ["chat"] },
      enabled: true,
      lifecycle: "discovered",
    });
    const removed = dedupeAccountModelRowsStore(store, ACCOUNT);
    expect(removed).toBe(1);
    expect(store.listByAccount(ACCOUNT)).toHaveLength(1);
    expect(store.listByAccount(ACCOUNT)[0]?.external_id).toBe(`acct:${ACCOUNT}:chat_20706`);
  });
});

describe("auto-test model ID selection", () => {
  it("10. auto-test receives only visibleCatalog model IDs", () => {
    const input = buildIdeVisibleUpsertInput(rawResponse(RECOMMENDED_IDS));
    const ids = input.map((m: AntigravityUpsertModelInput) => m.external_id);
    expect(ids).not.toContain("chat_20706");
    expect(ids).not.toContain("tab_jump_x");
    expect(ids).toEqual(RECOMMENDED_IDS);
  });
});
