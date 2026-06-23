import { describe, expect, it } from "bun:test";
import { buildIdeVisibleUpsertInput } from "./antigravity-persistence";

const RECOMMENDED_IDS = [
  "gemini-pro-agent",
  "claude-sonnet-4-6",
  "claude-opus-4-6-thinking",
];

function rawResponse(recommendedIds: string[]) {
  const models = Object.fromEntries(
    recommendedIds.map((id) => [id, { displayName: id, quotaInfo: { remainingFraction: 1 } }]),
  );
  return {
    models,
    agentModelSorts: [{ displayName: "Recommended", groups: [{ modelIds: recommendedIds }] }],
  };
}

describe("buildIdeVisibleUpsertInput", () => {
  it("returns IDE-visible recommended models only", () => {
    const input = buildIdeVisibleUpsertInput(rawResponse(RECOMMENDED_IDS));
    expect(input.map((m) => m.external_id).sort()).toEqual([...RECOMMENDED_IDS].sort());
    expect(input[0]?.extra_capabilities?.ide_visible).toBe(true);
  });
});
