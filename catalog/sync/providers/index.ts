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
import type { BillingModel } from '../enrich/resolvers.ts';

/**
 * How to read the ABSENCE of a published price for each provider.
 *
 * This is deliberately narrow. It never overrides a price the provider's own
 * feed publishes — that always wins, because it is the provider speaking about
 * itself. It only decides what a *missing* price means: for a subscription
 * provider the model is covered by the plan (`included`), while for a
 * per-token provider a missing price is genuinely `unknown`.
 *
 * Evidence, checked 2026-08-12:
 *   opencode-zen / opencode-go  models.dev prices every model; this never fires
 *   clinepass                   subscription per docs.cline.bot, but models.dev
 *                               DOES publish ClinePass's own rates for 11 of 12
 *                               models — and they are not copies of the vendor
 *                               list price (cline-pass/deepseek-v4-flash is
 *                               0.14/0.28 against the vendor's 0.07/0.14, an
 *                               exact 2x markup), so those 11 are correctly
 *                               per-token and only the unpriced one falls
 *                               through to `included`
 *   ollama-cloud                ollama.com/pricing gates usage volume and
 *                               concurrency, not models; models.dev publishes
 *                               no per-token cost for it at all
 */
export const BILLING: Record<string, BillingModel> = {
  'opencode-zen': 'per_token',
  'opencode-go': 'per_token',
  clinepass: 'subscription',
  'ollama-cloud': 'subscription',
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
