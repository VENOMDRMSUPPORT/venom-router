/** The provider slugs that ship an official logo PNG at
 * `public/providers/<slug>.png` (copied verbatim from the providers'
 * official brand assets; the embedded build ships them as static files).
 * Exported so the asset-presence test can assert this manifest and the
 * shipped files never drift apart, in either direction.
 *
 * The generic `custom` OpenAI-compatible path is deliberately absent and
 * falls back to the deterministic letter mark. */
export const PROVIDER_LOGO_SLUGS = [
  "agnes-ai",
  "antigravity",
  "claude-code",
  "clinepass",
  "codex",
  "gemini-cli",
  "github-copilot",
  "nvidia-nim",
  "ollama-cloud",
  "opencode-zen",
  "xai",
] as const;

const LOGO_SLUGS: ReadonlySet<string> = new Set(PROVIDER_LOGO_SLUGS);

/** The static URL of a provider's official logo PNG, or null when no
 * logo asset ships for the slug (callers then render the letter mark —
 * never a broken image). */
export function providerLogoSrc(slug: string): string | null {
  if (!LOGO_SLUGS.has(slug)) return null;
  return `${import.meta.env.BASE_URL}providers/${slug}.png`;
}
