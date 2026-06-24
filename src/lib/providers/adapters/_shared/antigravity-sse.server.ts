/** Parse Antigravity streamGenerateContent SSE bodies. Server-only. */

type AntigravityPart = { text?: string; thoughtSignature?: string };
type AntigravityCandidate = { content?: { parts?: AntigravityPart[] } };
type AntigravityEvent = {
  response?: { candidates?: AntigravityCandidate[] };
  candidates?: AntigravityCandidate[];
};

function collectTextFromCandidates(candidates: AntigravityCandidate[]): string[] {
  const chunks: string[] = [];
  for (const c of candidates) {
    for (const part of c.content?.parts ?? []) {
      if (part.text) chunks.push(part.text);
    }
  }
  return chunks;
}

/** Minimum output budget for model health checks (thinking models need room beyond 16). */
export const MODEL_TEST_MAX_OUTPUT_TOKENS = 256;

export function extractAntigravityResponseText(bodyText: string): string {
  const chunks: string[] = [];
  for (const line of bodyText.split(/\r?\n/)) {
    const trimmed = line.trim();
    if (!trimmed.startsWith("data:")) continue;
    const payload = trimmed.slice(5).trim();
    if (!payload || payload === "[DONE]") continue;
    try {
      const j = JSON.parse(payload) as AntigravityEvent;
      const candidates = j.response?.candidates ?? j.candidates ?? [];
      chunks.push(...collectTextFromCandidates(candidates));
    } catch {
      /* non-JSON SSE line */
    }
  }
  if (chunks.length) return chunks.join("");
  try {
    const j = JSON.parse(bodyText) as AntigravityEvent;
    const candidates = j.response?.candidates ?? j.candidates ?? [];
    const text = collectTextFromCandidates(candidates).join("");
    return text || bodyText;
  } catch {
    return bodyText;
  }
}
