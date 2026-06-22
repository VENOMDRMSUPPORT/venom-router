import { describe, it, expect } from "vitest";
import {
  findStringsInRawResponse,
  extractAgentModelSortIds,
  findModelMapCandidates,
} from "./antigravity-fetch-diagnostics";

describe("antigravity-fetch-diagnostics", () => {
  it("findStringsInRawResponse returns JSON paths for nested matches", () => {
    const raw = {
      models: {
        "gemini-3.5-flash-medium": { displayName: "Gemini 3.5 Flash (Medium) - Fast" },
        chat_20706: { quotaInfo: { remainingFraction: 1 } },
      },
      agentModelSorts: [
        {
          displayName: "Recommended",
          groups: [{ modelIds: ["gemini-3.5-flash-medium", "claude-sonnet-4.6-thinking"] }],
        },
      ],
    };
    const matches = findStringsInRawResponse(raw, ["Gemini 3.5 Flash", "chat_20706"]);
    expect(matches.some((m) => m.path.includes("displayName"))).toBe(true);
    expect(matches.some((m) => m.value.includes("Gemini 3.5 Flash"))).toBe(true);
  });

  it("extractAgentModelSortIds reads Recommended group modelIds", () => {
    const raw = {
      agentModelSorts: [
        { displayName: "Recommended", groups: [{ modelIds: ["a", "b"] }] },
        { displayName: "Other", groups: [{ modelIds: ["c"] }] },
      ],
    };
    const r = extractAgentModelSortIds(raw);
    expect(r.recommendedModelIds).toEqual(["a", "b"]);
    expect(r.allReferencedIds).toEqual(["a", "b", "c"]);
  });

  it("findModelMapCandidates locates $.models map", () => {
    const raw = {
      models: {
        a: { displayName: "A" },
        b: {},
        c: { displayName: "C" },
      },
    };
    const maps = findModelMapCandidates(raw);
    expect(maps.some((m) => m.path === '$["models"]' || m.path.endsWith('["models"]'))).toBe(true);
  });
});
