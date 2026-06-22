/** DB unique is (provider_id, external_id) — scope rows per account without a migration. */

const PREFIX = "acct:";

export function toDbExternalId(accountId: string, providerExternalId: string): string {
  return `${PREFIX}${accountId}:${providerExternalId}`;
}

export function providerExternalId(
  dbExternalId: string,
  capabilities?: Record<string, unknown> | null,
): string {
  const fromCaps = capabilities?.provider_external_id;
  if (typeof fromCaps === "string" && fromCaps.length > 0) return fromCaps;
  if (dbExternalId.startsWith(PREFIX)) {
    const rest = dbExternalId.slice(PREFIX.length);
    const sep = rest.indexOf(":");
    if (sep > 0) return rest.slice(sep + 1);
  }
  return dbExternalId;
}

export function modelRowLookupKeys(accountId: string, providerExternalId: string): string[] {
  return [toDbExternalId(accountId, providerExternalId), providerExternalId];
}

function inferQualityRating(externalId: string): number {
  const id = externalId.toLowerCase();
  if (/opus|gpt-5|o3-pro|gemini-2\.5-pro/.test(id)) return 92;
  if (/sonnet|gpt-4o|gpt-4\.1|gemini-2\.5/.test(id)) return 82;
  if (/haiku|flash|mini|lite|gemini-flash/.test(id)) return 68;
  if (/pro|gpt-4/.test(id)) return 88;
  return 50;
}

const CLAUDE_MODEL_SPECS: Record<string, { context_window: number; quality_rating: number }> = {
  "claude-opus-4-8": { context_window: 200_000, quality_rating: 96 },
  "claude-opus-4-7": { context_window: 200_000, quality_rating: 95 },
  "claude-opus-4-6": { context_window: 200_000, quality_rating: 94 },
  "claude-sonnet-4-6": { context_window: 200_000, quality_rating: 85 },
  "claude-opus-4-5-20251101": { context_window: 200_000, quality_rating: 93 },
  "claude-sonnet-4-5-20250929": { context_window: 200_000, quality_rating: 84 },
  "claude-haiku-4-5-20251001": { context_window: 200_000, quality_rating: 72 },
};

export function resolveModelSpecs(
  externalId: string,
  providerSlug: string,
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

  if (providerSlug === "claude-code") {
    const spec = CLAUDE_MODEL_SPECS[externalId];
    if (spec) {
      if (!context_window) context_window = spec.context_window;
      if (!dbQualityRating || dbQualityRating === 50) quality_rating = spec.quality_rating;
    }
  } else if (!dbQualityRating || dbQualityRating === 50) {
    quality_rating = inferQualityRating(externalId);
  }

  return { context_window, quality_rating };
}
