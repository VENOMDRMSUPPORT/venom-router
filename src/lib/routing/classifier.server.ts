import type { ChatMessage } from "@/lib/providers/adapters/types";
import type { TaskClass } from "@/lib/routing/types";

const LONG_CONTEXT_CHAR_THRESHOLD = 10_000;

function extractTextContent(messages: ChatMessage[]): string {
  return messages
    .map((m) => {
      if (typeof m.content === "string") return m.content;
      if (Array.isArray(m.content)) {
        return (m.content as Array<{ type: string; text?: string }>)
          .filter((p) => p.type === "text")
          .map((p) => p.text ?? "")
          .join(" ");
      }
      return "";
    })
    .join(" ");
}

function hasImageContent(messages: ChatMessage[]): boolean {
  for (const m of messages) {
    if (Array.isArray(m.content)) {
      const parts = m.content as Array<{ type: string }>;
      if (parts.some((p) => p.type === "image_url" || p.type === "image")) return true;
    }
  }
  return false;
}

export function classifyTask(messages: ChatMessage[]): TaskClass {
  if (hasImageContent(messages)) return "vision";

  const text = extractTextContent(messages);

  if (text.length > LONG_CONTEXT_CHAR_THRESHOLD) return "long_context";

  // Tool calling: explicit mention of tools or function calls
  if (/\b(use the .+ tool|call the|function call|tool_call|tool use)\b/i.test(text)) {
    return "tool_calling";
  }

  // Agentic: multi-step workflows
  if (
    /\b(step[- ]by[- ]step|multi[- ]step|first .+ then .+|agent|complete this task)\b/i.test(text)
  ) {
    return "agentic_task";
  }

  // Critical: high-stakes situations requiring the best model
  if (
    /\b(critical|production|urgent|security vulnerability|data loss|breaking change|final decision)\b/i.test(
      text,
    )
  ) {
    return "critical_task";
  }

  // Coding: code blocks or programming keywords
  if (
    /```|\bfunction\b|\bclass\b|\bconst\b|\bdef\b|\bimport\b|\bfix (this|the) (code|bug|error)\b/i.test(
      text,
    )
  ) {
    return "coding";
  }

  // Reasoning: deep explanation requests
  if (
    /\b(explain in depth|why is|reason(ing)?|analyze|compare|evaluate|pros and cons)\b/i.test(text)
  ) {
    return "reasoning_heavy";
  }

  return "simple_chat";
}
