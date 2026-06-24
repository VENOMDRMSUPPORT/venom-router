/** Shared model test prompt and response validation. Server-only. */

export const MODEL_TEST_PROMPT = 'Reply with exactly the word "ok" and nothing else.';

export function responseContainsOk(text: string): boolean {
  return /\bok\b/i.test(text.trim());
}

export function validateModelTestResponse(
  text: string,
): { ok: true } | { ok: false; error: string } {
  const trimmed = text.trim();
  if (!trimmed) {
    return { ok: false, error: "Empty response body" };
  }
  if (!responseContainsOk(trimmed)) {
    return { ok: false, error: `Response did not contain "ok": ${trimmed.slice(0, 120)}` };
  }
  return { ok: true };
}
