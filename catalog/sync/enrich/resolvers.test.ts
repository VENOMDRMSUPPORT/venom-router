import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import {
  resolveContext, resolveMaxOutput, resolveModalities, resolveCapability, resolveCost,
} from './resolvers.ts';
import type { CanonicalRecord } from './resolvers.ts';

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
  test('context falls through to the canonical index when the feed is silent', () => {
    const r = resolveContext({ spec: null, canonical: CANON })!;
    assert.equal(r.value, 1_000_000);
    assert.equal(r.source, 'openrouter');
    assert.match(r.ref, /context_length/);
  });

  test('the provider feed outranks the canonical index', () => {
    const r = resolveContext({ spec: { contextTokens: 256_000 }, canonical: CANON })!;
    assert.equal(r.value, 256_000);
    assert.equal(r.source, 'models.dev');
  });

  test('max output resolves separately from context', () => {
    // A feed that publishes context but not output must not have its context
    // reused as an output limit.
    const r = resolveMaxOutput({ spec: { contextTokens: 256_000 }, canonical: CANON })!;
    assert.equal(r.value, 131_072);
    assert.equal(r.source, 'openrouter');
  });

  test('one model can resolve its fields from different sources', () => {
    const input = { spec: { contextTokens: 256_000 }, canonical: CANON };
    assert.equal(resolveContext(input)!.source, 'models.dev');
    assert.equal(resolveMaxOutput(input)!.source, 'openrouter');
  });

  test('nothing anywhere resolves to null, not to a default', () => {
    assert.equal(resolveContext({ spec: null, canonical: null }), null);
    assert.equal(resolveMaxOutput({ spec: null, canonical: null }), null);
    assert.equal(resolveModalities({ spec: null, canonical: null }), null);
  });
});

describe('unknown never becomes unsupported', () => {
  test('a capability absent from every source stays null', () => {
    const canon: CanonicalRecord = { id: 'x/y', supportedParameters: ['max_tokens'] };
    assert.equal(resolveCapability('tools', { spec: null, canonical: canon }), null);
  });

  test('a parameter list that omits a capability is not evidence of absence', () => {
    // OpenRouter lists what its endpoint accepts. Absence there says nothing
    // about the model, so it must not be reported as false.
    const canon: CanonicalRecord = { id: 'x/y', supportedParameters: ['max_tokens', 'tools'] };
    const structured = resolveCapability('structured', { spec: null, canonical: canon });
    assert.equal(structured, null, 'must be unknown, not false');
  });

  test('an explicit false from the provider feed IS recorded as false', () => {
    const r = resolveCapability('tools', { spec: { tools: false }, canonical: CANON })!;
    assert.equal(r.value, false);
    assert.equal(r.source, 'models.dev');
  });

  test('the canonical index can only ever confirm, never deny', () => {
    const r = resolveCapability('structured', { spec: null, canonical: CANON })!;
    assert.equal(r.value, true);
    assert.equal(r.source, 'openrouter');
  });
});

describe('cost semantics keep effective and reference apart', () => {
  test('a per-token provider reports its own price as effective', () => {
    const c = resolveCost({ spec: { costInPerM: 10, costOutPerM: 50 }, canonical: CANON }, 'per_token');
    assert.equal(c.kind, 'per_token');
    assert.equal(c.inPerM, 10);
    assert.equal(c.refInPerM, 2, 'the reference is carried alongside, not merged');
  });

  test('a zero price is free, and that is a real answer', () => {
    const c = resolveCost({ spec: { costInPerM: 0, costOutPerM: 0 }, canonical: CANON }, 'per_token');
    assert.equal(c.kind, 'free');
  });

  test('a subscription provider with no per-token price reports Included, not unknown', () => {
    const c = resolveCost({ spec: null, canonical: CANON }, 'subscription');
    assert.equal(c.kind, 'included');
    assert.equal(c.inPerM, null, 'Included must not carry a fabricated per-token figure');
  });

  test('the market price NEVER lands in the effective field', () => {
    // The failure this guards: showing "$2/M" for a subscription model, which
    // is a price from a different seller wearing this provider's label.
    const c = resolveCost({ spec: null, canonical: CANON }, 'subscription');
    assert.equal(c.inPerM, null);
    assert.equal(c.outPerM, null);
    assert.equal(c.refInPerM, 2);
    assert.equal(c.refOutPerM, 6);
  });

  test('a per-token provider that published nothing is unknown, not free', () => {
    const c = resolveCost({ spec: null, canonical: null }, 'per_token');
    assert.equal(c.kind, 'unknown');
    assert.equal(c.inPerM, null);
  });

  test('every row gets a cost semantic — there is no null kind', () => {
    for (const billing of ['per_token', 'subscription'] as const) {
      for (const spec of [null, { costOutPerM: 5 }, { costInPerM: 0, costOutPerM: 0 }]) {
        assert.ok(resolveCost({ spec, canonical: null }, billing).kind);
      }
    }
  });
});

describe('the billing declaration is a fallback, never an override', () => {
  test("a subscription provider's own published price still wins", () => {
    // Measured 2026-08-12: models.dev publishes ClinePass's OWN rates for 11 of
    // its 12 models, and they are not vendor list prices (one carries an exact
    // 2x markup). Treating "subscription" as an override would have replaced 11
    // real prices with the word Included.
    const c = resolveCost({ spec: { costInPerM: 0.14, costOutPerM: 0.28 }, canonical: CANON }, 'subscription');
    assert.equal(c.kind, 'per_token');
    assert.equal(c.inPerM, 0.14);
  });

  test('it only decides what a MISSING price means', () => {
    const sub = resolveCost({ spec: null, canonical: CANON }, 'subscription');
    const pay = resolveCost({ spec: null, canonical: CANON }, 'per_token');
    assert.equal(sub.kind, 'included');
    assert.equal(pay.kind, 'unknown');
  });
});
