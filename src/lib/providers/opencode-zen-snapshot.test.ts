import { describe, it, expect } from "vitest";
import {
  isZeroCost,
  isCatalogEntryAvailable,
  buildOpenCodeZenFreeCatalog,
  type OpenCodeZenCatalogEntry,
} from "./opencode-zen-snapshot";

const catalog: Record<string, OpenCodeZenCatalogEntry> = {
  "big-pickle": {
    name: "Big Pickle",
    cost: { input: 0, output: 0 },
    limit: { context: 200000, output: 32000 },
    reasoning: true,
    tool_call: true,
    structured_output: true,
  },
  "deepseek-v4-flash-free": {
    name: "DeepSeek V4 Flash Free",
    cost: { input: 0, output: 0 },
    limit: { context: 200000, output: 128000 },
    reasoning: true,
    tool_call: true,
    structured_output: true,
  },
  "gpt-5.2": { name: "GPT 5.2", cost: { input: 1.75, output: 14 } },
  "claude-opus-4-8": { name: "Claude Opus 4.8", cost: { input: 5, output: 25 } },
  "catalog-only-free": { name: "Catalog Only", cost: { input: 0, output: 0 } },
  "minimax-m3-free": {
    name: "MiniMax M3 Free",
    status: "deprecated",
    cost: { input: 0, output: 0 },
  },
  "qwen3.6-plus-free": {
    name: "Qwen3.6 Plus Free",
    status: "deprecated",
    cost: { input: 0, output: 0 },
  },
};

describe("opencode-zen-snapshot", () => {
  it("isZeroCost accepts only zero input and output", () => {
    expect(isZeroCost({ input: 0, output: 0 })).toBe(true);
    expect(isZeroCost({ input: 0, output: 1 })).toBe(false);
    expect(isZeroCost({ input: 1, output: 0 })).toBe(false);
    expect(isZeroCost(undefined)).toBe(false);
  });

  it("includes big-pickle and free-tier models with zero cost", () => {
    const live = ["big-pickle", "deepseek-v4-flash-free", "gpt-5.2"];
    const result = buildOpenCodeZenFreeCatalog(live, catalog);
    expect(result.map((m) => m.id).sort()).toEqual(["big-pickle", "deepseek-v4-flash-free"]);
  });

  it("excludes paid models even when present in live catalog", () => {
    const live = ["gpt-5.2", "claude-opus-4-8"];
    expect(buildOpenCodeZenFreeCatalog(live, catalog)).toEqual([]);
  });

  it("excludes catalog-free models not returned by live Zen API", () => {
    const live = ["deepseek-v4-flash-free"];
    const result = buildOpenCodeZenFreeCatalog(live, catalog);
    expect(result.some((m) => m.id === "catalog-only-free")).toBe(false);
  });

  it("excludes live models missing from pricing catalog", () => {
    const live = ["unknown-live-model", "deepseek-v4-flash-free"];
    const result = buildOpenCodeZenFreeCatalog(live, catalog);
    expect(result).toHaveLength(1);
    expect(result[0].id).toBe("deepseek-v4-flash-free");
    expect(result[0].displayName).toBe("DeepSeek V4 Flash Free");
  });

  it("isCatalogEntryAvailable rejects deprecated zero-cost models", () => {
    expect(
      isCatalogEntryAvailable({
        name: "MiniMax M3 Free",
        status: "deprecated",
        cost: { input: 0, output: 0 },
      }),
    ).toBe(false);
    expect(isCatalogEntryAvailable({ cost: { input: 0, output: 0 } })).toBe(true);
  });

  it("excludes deprecated free models from live Zen catalog", () => {
    const live = ["deepseek-v4-flash-free", "minimax-m3-free", "qwen3.6-plus-free", "big-pickle"];
    const result = buildOpenCodeZenFreeCatalog(live, catalog);
    expect(result.map((m) => m.id).sort()).toEqual(["big-pickle", "deepseek-v4-flash-free"]);
  });

  it("parses limit.context as contextWindow", () => {
    const live = ["big-pickle"];
    const result = buildOpenCodeZenFreeCatalog(live, catalog);
    expect(result[0].contextWindow).toBe(200000);
  });

  it("parses reasoning, tool_call, structured_output flags", () => {
    const live = ["big-pickle"];
    const result = buildOpenCodeZenFreeCatalog(live, catalog);
    expect(result[0].reasoning).toBe(true);
    expect(result[0].toolCall).toBe(true);
    expect(result[0].structuredOutput).toBe(true);
  });

  it("omits undefined flags when catalog entry lacks them", () => {
    const live = ["catalog-only-free"];
    const result = buildOpenCodeZenFreeCatalog(live, catalog);
    expect(result[0].reasoning).toBeUndefined();
    expect(result[0].toolCall).toBeUndefined();
    expect(result[0].structuredOutput).toBeUndefined();
    expect(result[0].contextWindow).toBeUndefined();
  });
});
