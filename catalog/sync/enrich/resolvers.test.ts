import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import {
  resolveContext, resolveMaxOutput, resolveModalities, resolveCapability, resolveCost,
} from './resolvers.ts';
import type { CanonicalRecord, IntrinsicFacts } from './resolvers.ts';

const CANON: CanonicalRecord = {
  id: 'qwen/qwen3.8-max',
  contextLength: 1_000_000,
  maxCompletionTokens: 131_072,
  inputModalities: ['text', 'image', 'video'],
  supportedParameters: ['max_tokens', 'reasoning', 'structured_outputs', 'tools'],
  refCostInPerM: 2,
  refCostOutPerM: 6,
};

describe('each field resolves independently', () => {
  test('context does NOT fall through to the canonical index', () => {
    // A context window is what THIS provider serves. OpenRouter reports what ITS
    // providers serve, and the two differ — Ollama serves nemotron-3-ultra at
    // 262144 while the model supports 512288. Unknown is the honest answer.
    assert.equal(resolveContext({ spec: null, intrinsic: null, canonical: CANON }), null);
  });

  test('the provider feed outranks the canonical index', () => {
    const r = resolveContext({ intrinsic: null, spec: { contextTokens: 256_000 }, canonical: CANON })!;
    assert.equal(r.value, 256_000);
    assert.equal(r.source, 'models.dev');
  });

  test('max output resolves separately from context, and never from the index', () => {
    // Two rules at once. A feed that publishes context but not output must not
    // have its context reused as an output limit — and the index cannot supply
    // one either: its field is literally `top_provider.max_completion_tokens`,
    // the cap of whichever provider OpenRouter ranks first.
    const input = { intrinsic: null, spec: { contextTokens: 256_000 }, canonical: CANON };
    assert.equal(resolveContext(input)!.value, 256_000);
    assert.equal(resolveMaxOutput(input), null);
  });

  test('one model can resolve its fields from different sources', () => {
    // Serving limits come from the seller; an intrinsic property may come from
    // the index. That split is the point — not that every field has one source.
    const input = { spec: { contextTokens: 256_000 }, intrinsic: null, canonical: CANON };
    assert.equal(resolveContext(input)!.source, 'models.dev');
    assert.equal(resolveModalities(input)!.source, 'openrouter');
    assert.equal(resolveModalities(input)!.state, 'index_confirmation');
  });

  test('nothing anywhere resolves to null, not to a default', () => {
    assert.equal(resolveContext({ spec: null, intrinsic: null, canonical: null }), null);
    assert.equal(resolveMaxOutput({ spec: null, intrinsic: null, canonical: null }), null);
    assert.equal(resolveModalities({ spec: null, intrinsic: null, canonical: null }), null);
  });
});

describe('unknown never becomes unsupported', () => {
  test('a capability absent from every source stays null', () => {
    const canon: CanonicalRecord = { id: 'x/y', supportedParameters: ['max_tokens'] };
    assert.equal(resolveCapability('tools', { spec: null, intrinsic: null, canonical: canon }), null);
  });

  test('a parameter list that omits a capability is not evidence of absence', () => {
    // OpenRouter lists what its endpoint accepts. Absence there says nothing
    // about the model, so it must not be reported as false.
    const canon: CanonicalRecord = { id: 'x/y', supportedParameters: ['max_tokens', 'tools'] };
    const structured = resolveCapability('structured', { spec: null, intrinsic: null, canonical: canon });
    assert.equal(structured, null, 'must be unknown, not false');
  });

  test('an explicit false from the provider feed IS recorded as false', () => {
    const r = resolveCapability('tools', { intrinsic: null, spec: { tools: false }, canonical: CANON })!;
    assert.equal(r.value, false);
    assert.equal(r.source, 'models.dev');
  });

  test('the canonical index can only ever confirm, never deny', () => {
    const r = resolveCapability('structured', { spec: null, intrinsic: null, canonical: CANON })!;
    assert.equal(r.value, true);
    assert.equal(r.source, 'openrouter');
  });
});

describe('cost semantics keep effective and reference apart', () => {
  test('a per-token provider reports its own price as effective', () => {
    const c = resolveCost({ intrinsic: null, spec: { costInPerM: 10, costOutPerM: 50 }, canonical: CANON }, { model: 'per_token', evidenceUrl: 'https://example.test/pricing', note: 'per_token policy' });
    assert.equal(c.kind, 'per_token');
    assert.equal(c.inPerM, 10);
    assert.equal(c.refInPerM, 2, 'the reference is carried alongside, not merged');
  });

  test('a zero price is free, and that is a real answer', () => {
    const c = resolveCost({ intrinsic: null, spec: { costInPerM: 0, costOutPerM: 0 }, canonical: CANON }, { model: 'per_token', evidenceUrl: 'https://example.test/pricing', note: 'per_token policy' });
    assert.equal(c.kind, 'free');
  });

  test('a subscription provider with no per-token price reports Included, not unknown', () => {
    const c = resolveCost({ spec: null, intrinsic: null, canonical: CANON }, { model: 'subscription', evidenceUrl: 'https://example.test/pricing', note: 'subscription policy' });
    assert.equal(c.kind, 'included');
    assert.equal(c.inPerM, null, 'Included must not carry a fabricated per-token figure');
  });

  test('the market price NEVER lands in the effective field', () => {
    // The failure this guards: showing "$2/M" for a subscription model, which
    // is a price from a different seller wearing this provider's label.
    const c = resolveCost({ spec: null, intrinsic: null, canonical: CANON }, { model: 'subscription', evidenceUrl: 'https://example.test/pricing', note: 'subscription policy' });
    assert.equal(c.inPerM, null);
    assert.equal(c.outPerM, null);
    assert.equal(c.refInPerM, 2);
    assert.equal(c.refOutPerM, 6);
  });

  test('a per-token provider that published nothing is unknown, not free', () => {
    const c = resolveCost({ spec: null, intrinsic: null, canonical: null }, { model: 'per_token', evidenceUrl: 'https://example.test/pricing', note: 'per_token policy' });
    assert.equal(c.kind, 'unknown');
    assert.equal(c.inPerM, null);
  });

  test('every row gets a cost semantic — there is no null kind', () => {
    for (const model of ['per_token', 'subscription'] as const) {
        const billing = { model, evidenceUrl: 'https://example.test/pricing', note: `${model} policy` };
      for (const spec of [null, { costOutPerM: 5 }, { costInPerM: 0, costOutPerM: 0 }]) {
        assert.ok(resolveCost({ spec, intrinsic: null, canonical: null }, billing).kind);
      }
    }
  });
});

describe('the billing declaration decides how a provider charges', () => {
  test("a subscription provider's published rate is a reference, not what you pay", () => {
    // This assertion is the reverse of the one it replaces, and the reversal is
    // evidence-driven, not a preference.
    //
    // The old reasoning, from 2026-08-12: models.dev publishes ClinePass's own
    // rates for 11 of its 12 models, and one carries an exact 2x markup over the
    // vendor list price — so they were read as ClinePass's own CHARGES, and the
    // subscription declaration was demoted to a fallback for the unpriced rest.
    // The markup is real. The conclusion drawn from it was not.
    //
    // ClinePass's own documentation, read 2026-08-18: "ClinePass is a flat
    // monthly subscription, so you are NOT charged the individual API prices
    // below. These reference prices show the underlying per-1M-token rates for
    // each model and can help you understand how usage is measured against your
    // ClinePass quota." The published table is the metering rate, and the
    // catalog was presenting it as the price a subscriber pays.
    const c = resolveCost({ intrinsic: null, spec: { costInPerM: 0.14, costOutPerM: 0.28 }, canonical: CANON }, { model: 'subscription', evidenceUrl: 'https://example.test/pricing', note: 'subscription policy' });
    assert.equal(c.kind, 'included');
    assert.equal(c.inPerM, null);
    assert.equal(c.refInPerM, 0.14, 'the published figure is kept — as the reference it is');
  });

  test('it decides what a MISSING price means too', () => {
    const sub = resolveCost({ spec: null, intrinsic: null, canonical: CANON }, { model: 'subscription', evidenceUrl: 'https://example.test/pricing', note: 'subscription policy' });
    const pay = resolveCost({ spec: null, intrinsic: null, canonical: CANON }, { model: 'per_token', evidenceUrl: 'https://example.test/pricing', note: 'per_token policy' });
    const freeq = resolveCost({ spec: null, intrinsic: null, canonical: CANON }, { model: 'free_quota', evidenceUrl: 'https://example.test/pricing', note: 'free_quota policy' });
    assert.equal(sub.kind, 'included');
    assert.equal(pay.kind, 'unknown');
    assert.equal(freeq.kind, 'free');
  });
});

describe('a free, quota-limited provider is free at point of use, not unknown', () => {
  const FREE_QUOTA = { model: 'free_quota' as const, evidenceUrl: 'https://example.test/pricing', note: 'free at point of use; usage gated by quota, not price' };

  test('no published price means Free — and it NEVER fabricates a $0 in a price field', () => {
    // The defect this guards: writing a derived 0 into the effective-price fields
    // makes the next enrich pass re-read it as a first-party models.dev price
    // (provenance laundering). Free here is a declared policy — the KIND says
    // free while the price stays null.
    const c = resolveCost({ spec: null, intrinsic: null, canonical: CANON }, FREE_QUOTA);
    assert.equal(c.kind, 'free');
    assert.equal(c.inPerM, null);
    assert.equal(c.outPerM, null);
  });

  test('the free kind is a declared policy, cited — not a first-party feed figure', () => {
    const c = resolveCost({ spec: null, intrinsic: null, canonical: CANON }, FREE_QUOTA);
    assert.equal(c.source, 'provider_billing');
    assert.equal(c.state, 'declared_policy');
    assert.equal(c.url, 'https://example.test/pricing');
  });

  test('a published rate does not convert a free-quota provider to per-token', () => {
    // Same correction as the subscription case, applied to the same shape: how a
    // provider charges is a fact about the provider. Letting one priced row flip
    // to per-token would split a single provider's table into two costing
    // semantics — and, because the cost dimension is renormalised out of one
    // half's VO and not the other's, put two incomparable scores in one ranking.
    //
    // Inert today — models.dev prices no Ollama Cloud model — which is exactly
    // why it needs a test rather than a comment.
    const c = resolveCost({ intrinsic: null, spec: { costInPerM: 3, costOutPerM: 9 }, canonical: CANON }, FREE_QUOTA);
    assert.equal(c.kind, 'free');
    assert.equal(c.inPerM, null);
    assert.equal(c.refInPerM, 3);
  });
});

describe('cross-provider facts: intrinsic only', () => {
  const POOL: IntrinsicFacts = {
  structured: true, tools: true, declaredBy: 'nano-gpt/qwen/qwen3.5-plus', conflicts: [],
};

  test('a capability another seller declares fills the gap, with its origin recorded', () => {
    // Measured 2026-08-12: models.dev declares structured_output for only 63% of
    // its 6280 entries, but the same model is often listed by several providers
    // and one of them declares it.
    const r = resolveCapability('structured', { spec: null, intrinsic: POOL, canonical: null })!;
    assert.equal(r.value, true);
    assert.equal(r.source, 'models.dev');
    assert.match(r.ref, /nano-gpt/);
  });

  test("our own provider's declaration still wins", () => {
    const r = resolveCapability('structured', { spec: { structured: false }, intrinsic: POOL, canonical: null })!;
    assert.equal(r.value, false);
    assert.equal(r.ref, 'structured');
  });

  test('a serving LIMIT is never taken from another provider', () => {
    // context and max output describe what one seller offers, not what the model
    // is: Ollama serves nemotron-3-ultra at 262144 while the model supports
    // 512288. Borrowing a ceiling would put one seller's limit under another's
    // name — the same error the pricing split prevents.
    const pool = { ...POOL, contextLength: 1_000_000, maxCompletionTokens: 999 } as never;
    assert.equal(resolveContext({ spec: null, intrinsic: pool, canonical: null }), null);
    assert.equal(resolveMaxOutput({ spec: null, intrinsic: pool, canonical: null }), null);
  });

  test('price is never taken from another provider either', () => {
    const c = resolveCost({ spec: null, intrinsic: POOL, canonical: null }, { model: 'per_token', evidenceUrl: 'https://example.test/pricing', note: 'per_token policy' });
    assert.equal(c.kind, 'unknown');
    assert.equal(c.inPerM, null);
  });

  test('the pool can say no as well as yes — it is a declaration, not a parameter list', () => {
    const r = resolveCapability('structured', { spec: null, intrinsic: { structured: false, declaredBy: 'x/y', conflicts: [] }, canonical: null })!;
    assert.equal(r.value, false);
  });
});

describe('a limit the model vendor publishes about its own model', () => {
  const DOC = 'https://docs.z.ai/devpack/overview';
  const FP = {
    vendor: 'z-ai',
    context: [{ value: 1_000_000, by: 'zai-coding-plan/glm-5.3', url: DOC }],
    maxOutput: [{ value: 131_072, by: 'zai-coding-plan/glm-5.3', url: DOC }],
  };

  test('fills a context window no seller published, labelled as the vendor default', () => {
    const r = resolveContext({ spec: null, intrinsic: null, canonical: null, firstParty: FP })!;
    assert.equal(r.value, 1_000_000);
    assert.equal(r.state, 'vendor_default');
    assert.equal(r.ref, 'zai-coding-plan/glm-5.3.limit.context');
    assert.equal(r.url, DOC, 'the URL must be the storefront doc the feed published, never one we keep by hand');
  });

  test('fills a max output the same way', () => {
    const r = resolveMaxOutput({ spec: null, intrinsic: null, canonical: null, firstParty: FP })!;
    assert.equal(r.value, 131_072);
    assert.equal(r.state, 'vendor_default');
  });

  test('never outranks the seller that actually serves the model', () => {
    // The host's own figure is what this deployment does. The vendor's is what
    // the model supports. When both exist the deployment wins, and the ranking
    // must not depend on which happened to be looked up first.
    const r = resolveContext({ spec: { contextTokens: 262_144 }, intrinsic: null, canonical: null, firstParty: FP })!;
    assert.equal(r.value, 262_144);
    assert.equal(r.state, 'first_party');
  });

  test('a vendor contradicting itself fills nothing', () => {
    const split = { ...FP, context: [{ value: 1_000_000, by: 'a/m', url: null }, { value: 200_000, by: 'b/m', url: null }] };
    assert.equal(resolveContext({ spec: null, intrinsic: null, canonical: null, firstParty: split }), null);
  });
});

describe('the provider decides how it bills, not whichever rows the feed priced', () => {
  const SUBSCRIPTION = { model: 'subscription' as const, evidenceUrl: 'https://docs.cline.bot/getting-started/clinepass', note: 'flat monthly subscription' };

  test('a subscription row with a published rate is still included, and the rate is a reference', () => {
    // ClinePass's own documentation, read 2026-08-18: "ClinePass is a flat
    // monthly subscription, so you are not charged the individual API prices
    // below. These reference prices show the underlying per-1M-token rates."
    // The feed publishes exactly that table, and the catalog was putting it in
    // the EFFECTIVE column — "this is what it costs you here" — which is the one
    // thing the provider says it is not.
    const c = resolveCost(
      { spec: { costInPerM: 1.4, costOutPerM: 4.4 }, intrinsic: null, canonical: CANON },
      SUBSCRIPTION,
    );

    assert.equal(c.kind, 'included');
    assert.equal(c.inPerM, null, 'a subscriber is not charged this');
    assert.equal(c.outPerM, null);
    assert.equal(c.refInPerM, 1.4, 'and the published rate is what it actually is: a reference');
    assert.equal(c.refOutPerM, 4.4);
  });

  test('two rows of the same subscription provider get the same semantics', () => {
    // The defect this closes. Cost semantics were decided per ROW by whether
    // models.dev happened to carry a price, so one provider's table split into
    // "costs $1.40" and "included" — and the two halves had their VO computed on
    // different bases, renormalised for one and not the other, while sitting in
    // the same ranking.
    const priced = resolveCost({ spec: { costInPerM: 1.4, costOutPerM: 4.4 }, intrinsic: null, canonical: CANON }, SUBSCRIPTION);
    const unpriced = resolveCost({ spec: null, intrinsic: null, canonical: CANON }, SUBSCRIPTION);

    assert.equal(priced.kind, unpriced.kind);
  });

  test('a free-quota row with a published rate is still free', () => {
    // Same shape, same fix: a declared policy is about the provider, so a
    // stray feed price must not silently convert one row to per-token.
    const c = resolveCost(
      { spec: { costInPerM: 0.5, costOutPerM: 2 }, intrinsic: null, canonical: CANON },
      { model: 'free_quota', evidenceUrl: 'https://ollama.com/pricing', note: 'free, quota-limited' },
    );

    assert.equal(c.kind, 'free');
    assert.equal(c.inPerM, null);
    assert.equal(c.refInPerM, 0.5);
  });

  test('a per-token provider is untouched: its feed price IS what you pay', () => {
    const c = resolveCost(
      { spec: { costInPerM: 10, costOutPerM: 50 }, intrinsic: null, canonical: CANON },
      { model: 'per_token', evidenceUrl: 'https://opencode.ai/zen', note: 'per-token' },
    );

    assert.equal(c.kind, 'per_token');
    assert.equal(c.inPerM, 10);
    assert.equal(c.outPerM, 50);
  });
});
