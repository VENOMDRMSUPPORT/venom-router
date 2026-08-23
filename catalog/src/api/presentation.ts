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
 * Comparable form of a name or an id: case, spaces, and punctuation removed.
 *
 * "MiniMax-M3" and `minimax-m3` are the same fact spelled two ways, and the page
 * has to be able to say so. Kept at module scope because two different questions
 * need the same normalization and answering them with two copies is how they
 * start to disagree.
 */
const flatten = (value: string) => value.toLowerCase().replace(/[^a-z0-9]/g, '');

/**
 * The vendor's name split into the part the row prints and the part a badge lifts.
 *
 * models.dev serves each provider's own display name, and OpenCode uses it to
 * advertise: "Hy3 (8x usage)", "DeepSeek V4 Pro (New)", "GPT-5.6 Sol (50% Off)".
 * That is worth showing — but once, as a typed badge. The row used to print the
 * name whole AND badge the parenthetical, so every promoted model read
 * "DeepSeek V4 Pro (New)" with a "New" pill beside it: the same fact twice.
 *
 * One regex owns both halves on purpose. A separate strip-it and extract-it pair
 * is the same defect at one remove — the moment the two disagree about what is
 * liftable, either the badge duplicates the name again, or text the provider
 * published disappears from the page with nothing left carrying it.
 *
 * Only a trailing parenthetical can be lifted, and only when it says something
 * the id does not: "GLM-5.3" beside `glm-5.3` is the duplication this page was
 * cleared of. A parenthetical that fails that test is not lifted, so it stays in
 * the name — there would be no badge to receive it.
 *
 * The catalog is not claiming the offer is real or current. It is reporting what
 * the provider calls the model, which is a fact with a source and a fetch date.
 */
export interface VendorName {
  /** What the name column prints. Never empty — falls back to the model id. */
  base: string;
  /** What the badge shows, or null when there is nothing the id does not say. */
  qualifier: string | null;
  /**
   * True when `base` is the model id made pretty, so a row printing both says
   * one thing twice.
   *
   * It does NOT mean the id is redundant: "GLM-5.3" does not tell you whether to
   * call `glm-5.3` or `glm5.3`, and a pretty name is not an api argument. It
   * means only that the row should look for the id somewhere else before
   * printing it a second time. Measured against `base`, not the raw display
   * name — "DeepSeek V4 Pro (New)" does not look like `deepseek-v4-pro` until
   * the qualifier is lifted off it.
   */
  restatesId: boolean;
}

export function splitVendorName(modelId: string, displayName: string | null | undefined): VendorName {
  const restatesId = (base: string) => flatten(base) === flatten(modelId);
  /** Nothing was lifted, so the name is printed exactly as the provider serves it. */
  const whole = (base: string): VendorName => ({ base, qualifier: null, restatesId: restatesId(base) });

  const name = (displayName ?? '').trim();
  if (!name) return whole(modelId);

  const match = /\s*\(([^)]+)\)\s*$/.exec(name);
  const qualifier = match?.[1].trim();
  if (!match || !qualifier) return whole(name);

  // A parenthetical that only repeats part of the id is not extra information.
  // `includes` and not equality: a qualifier is a PART of the id — "free" inside
  // `mimo-v2.5-free`. The name is compared with equality instead, because a
  // substring match there would call `hy3-free` a restatement of "Hy3".
  if (flatten(modelId).includes(flatten(qualifier))) return whole(name);

  // A name that was nothing but its qualifier would leave the column blank.
  const base = name.slice(0, match.index).trim() || modelId;
  return { base, qualifier, restatesId: restatesId(base) };
}
