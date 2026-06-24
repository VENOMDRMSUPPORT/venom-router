/** OpenAI-compatible chat completion response helpers. Server-only. */

type OpenAiContentPart = { type?: string; text?: string };
type OpenAiMessage = {
  content?: string | null | OpenAiContentPart[];
  reasoning_content?: string | null;
};

/** Enough budget for reasoning models to emit a short final answer. */
export const MODEL_TEST_MAX_TOKENS = 256;

export function extractOpenAiMessageText(
  message?: OpenAiMessage | null,
  opts?: { includeReasoningFallback?: boolean },
): string {
  if (!message) return "";

  const content = message.content;
  if (typeof content === "string" && content.trim()) return content;

  if (Array.isArray(content)) {
    const text = content
      .map((part) => part.text ?? "")
      .join("")
      .trim();
    if (text) return text;
  }

  if (opts?.includeReasoningFallback && message.reasoning_content?.trim()) {
    return message.reasoning_content;
  }

  return "";
}
