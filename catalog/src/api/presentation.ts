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
    blurb: 'Ollama-hosted cloud models, reachable on every plan.',
    docsUrl: 'https://docs.ollama.com/cloud',
    invertInDark: true,
    note: 'Plans gate usage volume and concurrency, not which models you may call.',
  },
};

export const present = (id: string): ProviderPresentation =>
  PRESENTATION[id] ?? { logo: '', blurb: '', docsUrl: '' };
