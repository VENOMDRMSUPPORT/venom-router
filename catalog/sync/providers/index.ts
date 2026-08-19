/**
 * Provider adapters.
 *
 * Each adapter declares three things: where its live roster is, how to read
 * that response, and which models.dev key carries its specs. It contains no
 * fetching, retrying, diffing, gating or transaction logic — all of that lives
 * once in the engine, so a new provider inherits every failure layer on the day
 * it is added.
 *
 * All four rosters are unauthenticated. The service therefore holds no secrets,
 * which is a property worth protecting: it is why the catalog can run anywhere
 * and why no sanitiser is needed on its logs.
 */

import type { ProviderAdapter } from '../engine.ts';
import type { BillingPolicy } from '../enrich/resolvers.ts';

/**
 * How to read the ABSENCE of a published price for each provider.
 *
 * This is deliberately narrow. It never overrides a price the provider's own
 * feed publishes — that always wins, because it is the provider speaking about
 * itself. It only decides what a *missing* price means: for a subscription
 * provider the model is covered by the plan (`included`), for a per-token
 * provider a missing price is genuinely `unknown`, and for a free, quota-limited
 * provider it is `free` (with no fabricated number — see BillingModel).
 *
 * Evidence, checked 2026-08-13:
 *   opencode-zen                free per the owner; models.dev prices most of its
 *                               models, so ONLY the ones models.dev prices at
 *                               zero are published — the rest are excluded as
 *                               paid/not-proven-free at the roster stage, so this
 *                               billing branch only ever sees free models
 *   opencode-go                 models.dev prices every model; this never fires
 *   clinepass                   a flat $9.99/month subscription. models.dev
 *                               publishes a per-token table for it, and every
 *                               figure in it is a REFERENCE rate, not a charge:
 *                               "ClinePass is a flat monthly subscription, so
 *                               you are not charged the individual API prices
 *                               below. These reference prices show the
 *                               underlying per-1M-token rates ... how usage is
 *                               measured against your ClinePass quota"
 *                               (docs.cline.bot/getting-started/clinepass, read
 *                               2026-08-18). An earlier reading took the 2x
 *                               markup over vendor list as proof these were
 *                               ClinePass's own charges; the markup is real, the
 *                               conclusion was not — it is the metering rate
 *                               that is marked up. So all 13 rows are `included`
 *                               with those figures as the reference, and the
 *                               table no longer splits by which rows models.dev
 *                               happened to price
 *   ollama-cloud                free, quota-limited for callable offers.
 *                               Offers proven to require a paid plan plus Extra
 *                               Usage are withheld by `publishExclusions` even
 *                               when the public roster still lists them.
 */
export const BILLING: Record<string, BillingPolicy> = {
  'opencode-zen': {
    model: 'per_token',
    evidenceUrl: 'https://opencode.ai/zen',
    note: 'models.dev prices every opencode-zen model; the absent-price branch never fires',
  },
  'opencode-go': {
    model: 'per_token',
    evidenceUrl: 'https://opencode.ai/zen',
    note: 'models.dev prices every opencode-go model; the absent-price branch never fires',
  },
  clinepass: {
    model: 'subscription',
    evidenceUrl: 'https://docs.cline.bot/getting-started/clinepass',
    note: 'ClinePass is a flat $9.99/month subscription; the per-token table it publishes is a reference rate for quota metering, not a charge',
  },
  'ollama-cloud': {
    model: 'free_quota',
    evidenceUrl: 'https://ollama.com/pricing',
    note: 'Published Ollama Cloud offers are free at point of use; offers proven to require a paid plan plus Extra Usage are withheld from this free-provider catalog',
  },
};

/** The OpenAI-compatible `/v1/models` shape: `{ object, data: [{ id }] }`. */
function parseOpenAiList(body: unknown): string[] {
  const b = body as { data?: { id?: unknown }[] };
  if (!Array.isArray(b?.data)) throw new Error('expected an OpenAI-style {data:[...]} list');
  return b.data.map((m) => {
    if (typeof m?.id !== 'string') throw new Error('a roster entry has no string id');
    return m.id;
  });
}

export const OPENCODE_ZEN: ProviderAdapter = {
  id: 'opencode-zen',
  name: 'OpenCode Zen',
  rosterUrl: 'https://opencode.ai/zen/v1/models',
  feedKey: 'opencode',
  parseRoster: parseOpenAiList,
  // Owner decision (2026-08-13): OpenCode Zen is a free tier, kept distinct from
  // the paid OpenCode Go. Its roster mixes free and paid ids and models.dev
  // prices most of them, so only the models proven free (a published zero price)
  // are published; the rest are excluded as paid / not-proven-free. Conservative
  // on purpose — a model whose price we cannot prove is zero is NOT published.
  publishPolicy: 'free_only',
};

export const OPENCODE_GO: ProviderAdapter = {
  id: 'opencode-go',
  name: 'OpenCode Go',
  rosterUrl: 'https://opencode.ai/zen/go/v1/models',
  feedKey: 'opencode-go',
  parseRoster: parseOpenAiList,
};

export const OLLAMA_CLOUD: ProviderAdapter = {
  id: 'ollama-cloud',
  name: 'Ollama Cloud',
  rosterUrl: 'https://ollama.com/v1/models',
  feedKey: 'ollama-cloud',
  parseRoster: parseOpenAiList,
  // Verified against the configured Ollama Cloud access on 2026-08-19. The
  // provider returned HTTP 403 and explicitly required Pro/Max/Team plus Extra
  // Usage. A roster listing is not evidence that this free offering can call it.
  publishExclusions: { 'kimi-k3': 'plan_required' },
};

/**
 * ClinePass publishes four named groups, of which only `clinePass` is the
 * subscription's own lineup; `recommended` and `free` are billed elsewhere.
 *
 * Cline's OpenAI-compatible endpoints (`/api/v1/models`, `/v1/models`) both
 * return 401 and need a Cline account, so this grouped endpoint is the only
 * open route — checked 2026-08-12.
 */
export const CLINEPASS: ProviderAdapter = {
  id: 'clinepass',
  name: 'ClinePass',
  rosterUrl: 'https://api.cline.bot/api/v1/ai/cline/recommended-models',
  feedKey: 'cline-pass',
  parseRoster(body) {
    const b = body as { clinePass?: { id?: unknown }[] };
    if (!Array.isArray(b?.clinePass)) throw new Error('expected a {clinePass:[...]} group');
    return b.clinePass.map((m) => {
      if (typeof m?.id !== 'string') throw new Error('a clinePass entry has no string id');
      return m.id;
    });
  },
};

export const ADAPTERS: ProviderAdapter[] = [OPENCODE_ZEN, OPENCODE_GO, OLLAMA_CLOUD, CLINEPASS];
