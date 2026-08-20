/**
 * Brand presentation only: logos and one-line blurbs.
 *
 * This file holds NO model facts and no counts. Every number on the page comes
 * from the API — the whole point of M4 is that there is one data path, and a
 * "helpful" hardcoded model count here is exactly how the old catalog drifted to
 * claiming 58 while the providers served 116.
 *
 * A provider missing from this map still renders; it just uses its API name and
 * no logo.
 */

export interface ProviderPresentation {
  logo: string;
  blurb: string;
  docsUrl: string;
  /** Dark-on-light artwork that needs inverting in dark mode. */
  invertInDark?: boolean;
  /** Operational caveat about the plan, not about any model's data. */
  note?: string;
}

export const PRESENTATION: Record<string, ProviderPresentation> = {
  'opencode-zen': {
    logo: '/assets/opencode-zen.png',
    blurb: 'Coding models served through the OpenCode Zen gateway.',
    docsUrl: 'https://opencode.ai/docs/zen/',
    note: 'Several models are limited-time offers while OpenCode collects feedback; the roster changes often.',
  },
  'opencode-go': {
    logo: '/assets/opencode-zen.png',
    blurb: 'The paid OpenCode Go tier, with higher rate limits.',
    docsUrl: 'https://opencode.ai/docs/zen/',
  },
  clinepass: {
    logo: '/assets/clinepass.png',
    blurb: 'Curated open-weight models via the Cline extension.',
    docsUrl: 'https://docs.cline.bot/getting-started/clinepass',
    note: 'Only the clinePass group is listed. The recommended and free groups Cline also returns are billed separately and are not part of this subscription.',
  },
  'ollama-cloud': {
    logo: '/assets/ollama-cloud.png',
    blurb: 'Ollama-hosted models available to the configured free catalog access.',
    docsUrl: 'https://docs.ollama.com/cloud',
    invertInDark: true,
    note: 'Offers requiring Pro, Max, or Team plus Extra Usage are excluded from this free catalog.',
  },
};

export const present = (id: string): ProviderPresentation =>
  PRESENTATION[id] ?? { logo: '', blurb: '', docsUrl: '' };

/**
 * The part of a vendor's display name that the model id does not already say.
 *
 * models.dev serves each provider's own display name, and OpenCode uses it to
 * advertise: "Hy3 (8x usage)", "DeepSeek V4 Pro (New)", "GPT-5.6 Sol (50% Off)".
 * That is worth showing — but only the part that adds something. "GLM-5.3"
 * beside `glm-5.3` is the same fact twice, which is the duplication this page
 * was just cleared of.
 *
 * The catalog is not claiming the offer is real or current. It is reporting what
 * the provider calls the model, which is a fact with a source and a fetch date.
 */
export function vendorQualifier(modelId: string, displayName: string | null | undefined): string | null {
  if (!displayName) return null;
  const match = /\(([^)]+)\)\s*$/.exec(displayName.trim());
  if (!match) return null;
  const qualifier = match[1].trim();
  if (!qualifier) return null;
  // A parenthetical that only repeats part of the id is not extra information.
  const flatten = (value: string) => value.toLowerCase().replace(/[^a-z0-9]/g, '');
  return flatten(modelId).includes(flatten(qualifier)) ? null : qualifier;
}
