import { describe, expect, it } from "vitest";
import { extractAntigravityResponseText } from "./antigravity-sse.server";

describe("extractAntigravityResponseText", () => {
  it("collects text across SSE chunks with thoughtSignature-only prelude", () => {
    const body = [
      'data: {"response":{"candidates":[{"content":{"role":"model","parts":[{"thoughtSignature":"sig1"}]}}]}}',
      'data: {"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]}}]}}',
    ].join("\n");
    expect(extractAntigravityResponseText(body)).toBe("ok");
  });

  it("reads text from non-wrapped candidates", () => {
    const body = 'data: {"candidates":[{"content":{"parts":[{"text":"hello ok"}]}}]}';
    expect(extractAntigravityResponseText(body)).toBe("hello ok");
  });

  it("returns raw body when no text parts exist", () => {
    const body =
      'data: {"response":{"candidates":[{"content":{"parts":[{"thoughtSignature":"only"}]}}]}}';
    expect(extractAntigravityResponseText(body)).toBe(body);
  });
});
