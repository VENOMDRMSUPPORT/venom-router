import { describe, expect, it } from "bun:test";
import { extractOpenAiMessageText } from "./openai-chat.server";

describe("extractOpenAiMessageText", () => {
  it("reads string content", () => {
    expect(extractOpenAiMessageText({ content: "ok" })).toBe("ok");
  });

  it("reads array content blocks", () => {
    expect(
      extractOpenAiMessageText({
        content: [{ type: "text", text: "hello ok" }],
      }),
    ).toBe("hello ok");
  });

  it("falls back to reasoning_content for health checks", () => {
    expect(
      extractOpenAiMessageText(
        { content: "", reasoning_content: "The user wants ok, so: ok" },
        { includeReasoningFallback: true },
      ),
    ).toBe("The user wants ok, so: ok");
  });

  it("ignores reasoning_content for normal chat extraction", () => {
    expect(
      extractOpenAiMessageText({
        content: "",
        reasoning_content: "thinking only",
      }),
    ).toBe("");
  });
});
