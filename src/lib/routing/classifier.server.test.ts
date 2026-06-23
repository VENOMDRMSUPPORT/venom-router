import { describe, it, expect } from "bun:test";
import { classifyTask } from "./classifier.server";
import type { ChatMessage } from "@/lib/providers/adapters/types";

function textMsg(text: string): ChatMessage {
  return { role: "user", content: text };
}

function visionMsg(): ChatMessage {
  return {
    role: "user",
    content: [
      { type: "image_url", image_url: { url: "data:image/png;base64,abc" } },
      { type: "text", text: "What is in this image?" },
    ] as unknown as string,
  };
}

describe("classifyTask", () => {
  it("classifies image message as vision", () => {
    expect(classifyTask([visionMsg()])).toBe("vision");
  });

  it("classifies coding keywords as coding", () => {
    expect(classifyTask([textMsg("Fix this TypeScript function: function foo() { return 1; }")])).toBe("coding");
  });

  it("classifies code block as coding", () => {
    expect(classifyTask([textMsg("Review this code:\n```python\ndef main(): pass\n```")])).toBe("coding");
  });

  it("classifies tool_calls mention as tool_calling", () => {
    expect(classifyTask([textMsg("Use the search tool to find relevant docs")])).toBe("tool_calling");
  });

  it("classifies long messages as long_context", () => {
    const longText = "word ".repeat(3000); // ~15000 chars
    expect(classifyTask([textMsg(longText)])).toBe("long_context");
  });

  it("classifies agent/step keywords as agentic_task", () => {
    expect(classifyTask([textMsg("Complete this multi-step task: first search, then summarize, then write")])).toBe("agentic_task");
  });

  it("classifies short generic messages as simple_chat", () => {
    expect(classifyTask([textMsg("Hello, how are you?")])).toBe("simple_chat");
  });

  it("classifies 'why' / 'explain' / 'reason' as reasoning_heavy", () => {
    expect(classifyTask([textMsg("Explain in depth why functional programming is better")])).toBe("reasoning_heavy");
  });

  it("classifies critical/urgent keywords as critical_task", () => {
    expect(classifyTask([textMsg("This is a critical production issue causing data loss")])).toBe("critical_task");
  });

  it("prioritizes vision over coding", () => {
    const mixed: ChatMessage = {
      role: "user",
      content: [
        { type: "image_url", image_url: { url: "data:image/png;base64,abc" } },
        { type: "text", text: "Fix the bug in this code ```js const x = 1```" },
      ] as unknown as string,
    };
    expect(classifyTask([mixed])).toBe("vision");
  });
});
