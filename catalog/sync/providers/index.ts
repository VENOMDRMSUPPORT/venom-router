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
  // prices most of them, so only the models proven free are published; the rest
  // are excluded as paid / not-proven-free. Conservative on purpose — a model
  // whose price we cannot prove is zero is NOT published.
  //
  // Proof of free is the provider's OWN lineup below, not the price feed: read
  // from the official Zen documentation inside the provider's public repository
  // (packages/web/src/content/docs/zen.mdx, endpoints + pricing tables, where
  // every id below is priced "Free" in every column). The roster endpoint
  // carries no prices, so a zero transcribed by models.dev for an id this list
  // does not name (laguna-s-2.1-free and deepseek-v4-flash-free at review time)
  // is a claim about an offer the provider has not made — its own app does not
  // show them as free. Re-check against the source when refreshing this list.
  publishPolicy: 'free_only',
  officialFreeList: {
    ids: [
      'big-pickle',
      'x-preview-f-free',
      'mimo-v2.5-free',
      'hy3-free',
      'nemotron-3-ultra-free',
      'nemotron-3.5-lightning-free',
      'muse-spark-1.2-contributor-free',
    ],
    reviewedAt: '2026-08-21',
    sourceUrl: 'https://github.com/anomalyco/opencode/blob/dev/packages/web/src/content/docs/zen.mdx',
  },
};

export const OPENCODE_GO: ProviderAdapter = {
  id: 'opencode-go',
  name: 'OpenCode Go',
  rosterUrl: 'https://opencode.ai/zen/go/v1/models',
  feedKey: 'opencode-go',
  parseRoster: parseOpenAiList,
  // Withheld after probing every rostered id on 2026-08-20. Twenty-two answered
  // and stay published; these five cannot be called, for three different reasons
  // that are worth keeping apart:
  //
  //   * `provider_unsupported` — the provider itself says it cannot serve the
  //     id. "Model is unavailable", "Unsupported model mimo-v2", "Model
  //     muse-spark-1.2 is not supported". Nothing on our side unlocks these.
  //
  //   * `consent_required` — muse-spark-1.2-contributor is callable only by
  //     opting in to the model collecting data to improve its own quality. That
  //     would send evaluation prompts upstream for training, which is a decision
  //     for the owner and not one to take by default.
  //
  // `grok-4.5` is deliberately NOT here. It answers 503 "Endpoint is
  // unavailable" — three times running, so not a blip, but an outage is not a
  // permanent condition and withholding it would record the wrong reason. It
  // stays published and simply has no measurement until the endpoint returns.
  //
  // One warning for whoever re-runs this sweep: probing with `max_tokens: 1`
  // made gpt-5.6-luna look broken — HTTP 400 around an otherwise valid empty
  // completion. At 16 tokens it answers normally. A probe that starves the model
  // tests the probe, not the model.
  publishExclusions: {
    'hy3-preview': 'provider_unsupported',
    'mimo-v2-omni': 'provider_unsupported',
    'mimo-v2-pro': 'provider_unsupported',
    'muse-spark-1.2': 'provider_unsupported',
    'muse-spark-1.2-contributor': 'consent_required',
  },
};

export const OLLAMA_CLOUD: ProviderAdapter = {
  id: 'ollama-cloud',
  name: 'Ollama Cloud',
  rosterUrl: 'https://ollama.com/v1/models',
  feedKey: 'ollama-cloud',
  parseRoster: parseOpenAiList,
  // Withheld because the configured Ollama Cloud access cannot call them. A
  // roster listing is not evidence of access, and publishing a model this
  // account cannot reach makes the catalog claim availability it does not have.
  //
  // Established by sending one minimal completion per rostered id and reading
  // the error back. Two DIFFERENT refusals came back, and the difference decides
  // what an upgrade would buy:
  //
  //   * eleven ids — "this model requires a subscription, upgrade for access".
  //     A plan would unlock these.
  //
  //   * `kimi-k3` — "requires both a Pro, Max, or Team plan AND extra usage (it
  //     does not use included plan usage)". A plan alone does NOT unlock this
  //     one; it is metered separately on top, so it can never be part of a free
  //     offering. Confirmed 2026-08-19 and again 2026-08-20.
  //
  // Both are `plan_required` because the catalog's question is only "can this
  // account call it", but the distinction is recorded here rather than flattened
  // away: it is the difference between "buy the plan" and "this will never be
  // free".
  //
  // Seven ids answered 200 and stay published: gemma4:31b, gpt-oss:120b,
  // gpt-oss:20b, minimax-m3, nemotron-3-nano:30b, nemotron-3-super,
  // nemotron-3-ultra.
  //
  // Hand-maintained on purpose: this records what one account could reach on one
  // day, which no roster or price feed reports. Dated, and re-checkable.
  publishExclusions: {
    'deepseek-v4-flash:0731': 'plan_required',
    'deepseek-v4-flash:preview': 'plan_required',
    'deepseek-v4-pro:0813': 'plan_required',
    'deepseek-v4-pro:preview': 'plan_required',
    'glm-5.1': 'plan_required',
    'glm-5.2': 'plan_required',
    'kimi-k2.6': 'plan_required',
    'kimi-k2.7-code': 'plan_required',
    // Subscription AND extra usage — see above; not merely un-subscribed.
    'kimi-k3': 'plan_required',
    'minimax-m2.7': 'plan_required',
    'mistral-large-3:675b': 'plan_required',
    'qwen3.5:397b': 'plan_required',
  },

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
