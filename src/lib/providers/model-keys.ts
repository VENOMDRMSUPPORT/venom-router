/** Model external_id helpers and spec inference. */

export function providerExternalId(
  dbExternalId: string,
  capabilities?: Record<string, unknown> | null,
): string {
  const fromCaps = capabilities?.provider_external_id;
  if (typeof fromCaps === "string" && fromCaps.length > 0) return fromCaps;
  if (dbExternalId.startsWith("acct:")) {
    const rest = dbExternalId.slice("acct:".length);
    const sep = rest.indexOf(":");
    if (sep > 0) return rest.slice(sep + 1);
  }
  return dbExternalId;
}

function inferQualityRating(externalId: string): number {
  const id = externalId.toLowerCase();
  if (/opus|gpt-5|o3-pro|gemini-2\.5-pro/.test(id)) return 92;
  if (/sonnet|gpt-4o|gpt-4\.1|gemini-2\.5/.test(id)) return 82;
  if (/haiku|flash|mini|lite|gemini-flash/.test(id)) return 68;
  if (/pro|gpt-4/.test(id)) return 88;
  return 50;
}

export function resolveModelSpecs(
  externalId: string,
  _providerSlug: string,
  caps: Record<string, unknown> | null,
  dbContextWindow: number | null | undefined,
  dbQualityRating: number | null | undefined,
): { context_window: number | null; quality_rating: number } {
  let context_window = dbContextWindow ?? null;
  let quality_rating = dbQualityRating ?? 50;

  if (!context_window && caps) {
    const raw = caps.antigravity_raw as { maxTokens?: number } | undefined;
    if (typeof raw?.maxTokens === "number") context_window = raw.maxTokens;
  }

  if (!dbQualityRating || dbQualityRating === 50) {
    quality_rating = inferQualityRating(externalId);
  }

  return { context_window, quality_rating };
}
