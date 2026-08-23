import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { loadSpecs } from './models-dev.ts';

/**
 * These exercise the REAL pooling code against a crafted feed.
 *
 * The enrichment tests hand `enrich` an `IntrinsicFacts` they built themselves,
 * which proves what enrich does with a conflict but says nothing about whether
 * the pooling ever produces one. That gap is the whole point of this file.
 */
const feedOf = (feed: unknown, vendors: Record<string, { label: string; storefronts: string[]; namespaces: string[] }> = {}) =>
  loadSpecs(async () => ({ status: 200, body: feed }), vendors);

describe('pooling intrinsic facts across sellers', () => {
  test('sellers who disagree produce a conflict carrying both values and both names', async () => {
    const specs = await feedOf({
      aihubmix: { models: { 'hy3-preview': { id: 'hy3-preview', structured_output: true } } },
      kilo: { models: { 'hy3-preview': { id: 'hy3-preview', structured_output: false } } },
    });

    const facts = specs.intrinsic('hy3-preview')!;
    assert.equal(facts.structured, undefined, 'a disputed field must not resolve to a value');
    assert.equal(facts.conflicts.length, 1);
    assert.equal(facts.conflicts[0].field, 'structured');
    assert.deepEqual(facts.conflicts[0].sides, [
      { value: true, by: 'aihubmix/hy3-preview' },
      { value: false, by: 'kilo/hy3-preview' },
    ]);
  });

  test('sellers who agree resolve the field and record no conflict', async () => {
    const specs = await feedOf({
      aihubmix: { models: { 'hy3-preview': { id: 'hy3-preview', tool_call: true } } },
      kilo: { models: { 'hy3-preview': { id: 'hy3-preview', tool_call: true } } },
    });

    const facts = specs.intrinsic('hy3-preview')!;
    assert.equal(facts.tools, true);
    assert.deepEqual(facts.conflicts, []);
  });

  test('each distinct value appears once, however many sellers repeat it', async () => {
    // Three sellers, two answers. The disagreement is two-sided; listing a side
    // once per seller would make a 2-1 split look like three-way chaos.
    const specs = await feedOf({
      a: { models: { m: { id: 'm', structured_output: true } } },
      b: { models: { m: { id: 'm', structured_output: false } } },
      c: { models: { m: { id: 'm', structured_output: false } } },
    });

    const sides = specs.intrinsic('m')!.conflicts[0].sides;
    assert.equal(sides.length, 2);
    assert.deepEqual(sides.map((s) => s.value), [true, false]);
  });

  test('a serving limit is never pooled, however many sellers publish one', async () => {
    // Context and price belong to a seller, not to the model. Ollama serving
    // nemotron at 262144 while the model supports 512288 is the measured proof.
    const specs = await feedOf({
      a: { models: { m: { id: 'm', limit: { context: 262_144 }, cost: { input: 1 } } } },
      b: { models: { m: { id: 'm', limit: { context: 512_288 }, cost: { input: 2 } } } },
    });

    const facts = specs.intrinsic('m');
    assert.equal(facts, null, 'nothing intrinsic was declared, so there is nothing to pool');
  });

  test('one seller alone is not a conflict', async () => {
    const specs = await feedOf({
      solo: { models: { m: { id: 'm', structured_output: true } } },
    });

    const facts = specs.intrinsic('m')!;
    assert.equal(facts.structured, true);
    assert.equal(facts.declaredBy, 'solo/m');
    assert.deepEqual(facts.conflicts, []);
  });
});

/**
 * First-party limits.
 *
 * A context window belongs to a SELLER, which is why the pool above refuses to
 * average one across resellers. But there is one seller whose figure is about
 * the model rather than about a deployment: the company that built it, selling
 * it from its own storefront. `cline-pass/glm-5.3` is the case that forced this
 * — ClinePass publishes `{id, name, description, tags}` and nothing else, and
 * models.dev has not listed the model under `cline-pass` yet, so the row sat in
 * "needs verification" with a context window that Z-AI itself publishes.
 *
 * Membership is read from the feed, not asserted: a model belongs to a vendor
 * when some seller lists it under that vendor's namespace. That is what keeps
 * `alibaba/glm-5.2` — Alibaba reselling a Z-AI model — from being mistaken for
 * a first-party GLM figure.
 */
describe('first-party limits from a vendor selling its own model', () => {
  const ZAI = {
    'z-ai': { label: 'Z.ai', storefronts: ['zai-coding-plan', 'zhipuai-coding-plan'], namespaces: ['zai-org', 'z-ai'] },
  };

  test('a vendor storefront listing its own model is first-party evidence', async () => {
    const specs = await feedOf({
      'nano-gpt': { models: { 'zai-org/glm-5.3': { id: 'zai-org/glm-5.3', limit: { context: 1_048_576, output: 131_072 } } } },
      'zai-coding-plan': { doc: 'https://docs.z.ai/devpack/overview', models: { 'glm-5.3': { id: 'glm-5.3', limit: { context: 1_000_000, output: 131_072 } } } },
    }, ZAI);

    const fp = specs.firstPartyLimits('cline-pass/glm-5.3')!;
    assert.equal(fp.vendor, 'z-ai');
    assert.deepEqual(fp.context, [{ value: 1_000_000, by: 'zai-coding-plan/glm-5.3', url: 'https://docs.z.ai/devpack/overview' }]);
    assert.deepEqual(fp.maxOutput, [{ value: 131_072, by: 'zai-coding-plan/glm-5.3', url: 'https://docs.z.ai/devpack/overview' }]);
  });

  test('a reseller that happens to be a vendor elsewhere is not first-party here', async () => {
    // Alibaba sells its own Qwen models AND resells GLM. Its GLM figure is a
    // deployment limit like any other reseller's; treating a storefront as
    // first-party for everything it stocks is how that mistake gets made.
    const specs = await feedOf({
      'nano-gpt': { models: { 'zai-org/glm-5.2': { id: 'zai-org/glm-5.2', limit: { context: 1_048_576 } } } },
      alibaba: { models: { 'glm-5.2': { id: 'glm-5.2', limit: { context: 262_144 } } } },
    }, { alibaba: { label: 'Alibaba', storefronts: ['alibaba'], namespaces: ['qwen'] } });

    assert.equal(specs.firstPartyLimits('cline-pass/glm-5.2'), null);
  });

  test('a model no vendor storefront lists has no first-party limit', async () => {
    const specs = await feedOf({
      'nano-gpt': { models: { 'zai-org/glm-5.3': { id: 'zai-org/glm-5.3', limit: { context: 1_048_576 } } } },
    }, ZAI);

    assert.equal(specs.firstPartyLimits('cline-pass/glm-5.3'), null);
  });

  test('every storefront that disagrees is reported, never reduced on sight', async () => {
    // The pooling above settles disagreements by refusing them. This lookup
    // does not decide at all: it reports what the vendor's own stores said and
    // leaves the adoption rule to one reviewable place.
    const specs = await feedOf({
      'nano-gpt': { models: { 'zai-org/glm-5.3': { id: 'zai-org/glm-5.3', limit: { context: 1_048_576 } } } },
      'zai-coding-plan': { models: { 'glm-5.3': { id: 'glm-5.3', limit: { context: 1_000_000 } } } },
      'zhipuai-coding-plan': { models: { 'glm-5.3': { id: 'glm-5.3', limit: { context: 200_000 } } } },
    }, ZAI);

    assert.deepEqual(specs.firstPartyLimits('cline-pass/glm-5.3')!.context, [
      { value: 1_000_000, by: 'zai-coding-plan/glm-5.3', url: null },
      { value: 200_000, by: 'zhipuai-coding-plan/glm-5.3', url: null },
    ]);
  });
});

/**
 * Vendor identity.
 *
 * A different question from `firstPartyLimits`, and from the canonical id the
 * scoring attaches. `canonicalId` on a row is the REFERENCE-INDEX entry a score
 * was taken from; this is *which model the row is*, which the feed can answer
 * even when the index carries no entry. Conflating them is why the page showed
 * `cline-pass/glm-5.3` with no identity at all while quoting a context window
 * read from Z.ai's own listing of that exact model.
 */
describe('the identity a vendor listing establishes', () => {
  const ZAI = {
    'z-ai': { label: 'Z.ai', storefronts: ['zai-coding-plan'], namespaces: ['zai-org', 'z-ai'], canonicalPrefix: 'z-ai' },
  };

  test('a namespaced listing names the model, in the index\'s own convention', async () => {
    // Sellers write `zai-org/glm-5.3`; the reference index writes `z-ai/...` for
    // this vendor. The registry declares which prefix this catalog canonicalises
    // to, so the id is normalised rather than copied from whichever seller was
    // read first.
    const specs = await feedOf({
      'nano-gpt': { models: { 'zai-org/glm-5.3': { id: 'zai-org/glm-5.3' } } },
    }, ZAI);

    const v = specs.vendorIdentity('cline-pass/glm-5.3')!;
    assert.equal(v.canonicalId, 'z-ai/glm-5.3');
    assert.equal(v.vendor, 'z-ai');
    assert.equal(v.declaredBy, 'nano-gpt/zai-org/glm-5.3');
  });

  test('a model no listing places with a vendor has no identity', async () => {
    const specs = await feedOf({ someone: { models: { 'mystery-1': { id: 'mystery-1' } } } }, ZAI);
    assert.equal(specs.vendorIdentity('someone/mystery-1'), null);
  });

  test('a vendor with no declared prefix yields nothing rather than a guess', async () => {
    // Constructing `${vendorId}/${slug}` would have produced `alibaba/qwen3.8-max`
    // where every index writes `qwen/qwen3.8-max`. A registry entry that has not
    // declared its prefix has not answered.
    const specs = await feedOf(
      { 'nano-gpt': { models: { 'zai-org/glm-5.3': { id: 'zai-org/glm-5.3' } } } },
      { 'z-ai': { label: 'Z.ai', storefronts: [], namespaces: ['zai-org'] } },
    );
    assert.equal(specs.vendorIdentity('cline-pass/glm-5.3'), null);
  });
});

describe('a vendor identity is built from the row, not from whichever seller was read first', () => {
  const ZAI = {
    'z-ai': { label: 'Z.ai', storefronts: [], namespaces: ['zai-org'], canonicalPrefix: 'z-ai' },
  };

  test("a seller's variant tag does not become part of the id", async () => {
    // Measured against the live feed: the first `zai-org/` listing of glm-5.3 is
    // nano-gpt's `zai-org/glm-5.3:thinking`, and the id came out as
    // `z-ai/glm-5.3:thinking` — a reasoning-mode variant presented as the
    // model's identity. The listing establishes WHICH VENDOR; the row already
    // says which model.
    const specs = await feedOf({
      'nano-gpt': { models: { 'zai-org/glm-5.3:thinking': { id: 'zai-org/glm-5.3:thinking' } } },
    }, ZAI);

    assert.equal(specs.vendorIdentity('cline-pass/glm-5.3')!.canonicalId, 'z-ai/glm-5.3');
  });

  test("a seller's capitalisation does not become part of the id either", async () => {
    // Same defect, quieter: `moonshotai/Kimi-K2.6` beside a canonical
    // `moonshotai/kimi-k2.6` on the next row.
    const specs = await feedOf({
      hf: { models: { 'zai-org/GLM-5.3': { id: 'zai-org/GLM-5.3' } } },
    }, ZAI);

    assert.equal(specs.vendorIdentity('cline-pass/glm-5.3')!.canonicalId, 'z-ai/glm-5.3');
  });

  test('the listing is still cited exactly as published', async () => {
    // The citation must stay verbatim — it is the thing a reader goes and checks.
    const specs = await feedOf({
      'nano-gpt': { models: { 'zai-org/glm-5.3:thinking': { id: 'zai-org/glm-5.3:thinking' } } },
    }, ZAI);

    assert.equal(specs.vendorIdentity('cline-pass/glm-5.3')!.declaredBy, 'nano-gpt/zai-org/glm-5.3:thinking');
  });
});

describe('a disagreement is answered by whoever has standing, or not at all', () => {
  test('the seller of the offering answers a dispute about its own offering', async () => {
    // The reported case: `ollama-cloud/gemma4:31b` showed "reasoning: true vs
    // false" with no value taken, while ollama-cloud — the seller actually
    // serving it — publishes `true` in the same feed. Five sellers said true and
    // one TEE deployment said false, and the panel showed it as an even split
    // because conflicts keep one entry per distinct value.
    const specs = await feedOf({
      'ollama-cloud': { models: { 'gemma4:31b': { id: 'gemma4:31b', reasoning: true } } },
      'nano-gpt': { models: { 'TEE/gemma4-31b': { id: 'TEE/gemma4-31b', reasoning: false } } },
    });

    const facts = specs.intrinsic('gemma4:31b', 'ollama-cloud')!;
    assert.equal(facts.reasoning, true);
    assert.equal(facts.standing?.reasoning, 'serving-seller');
    assert.equal(facts.conflicts.length, 0, 'answered, so nothing is left open');
  });

  test('the same feed still withholds it from a seller with no standing', async () => {
    // Standing is per offer. clinepass selling the same model gets no answer
    // from ollama-cloud's listing, so the dispute stays open for clinepass.
    const specs = await feedOf({
      'ollama-cloud': { models: { 'gemma4:31b': { id: 'gemma4:31b', reasoning: true } } },
      'nano-gpt': { models: { 'TEE/gemma4-31b': { id: 'TEE/gemma4-31b', reasoning: false } } },
    });

    const facts = specs.intrinsic('gemma4:31b', 'clinepass')!;
    assert.equal(facts.reasoning, undefined);
    assert.equal(facts.conflicts.length, 1);
  });

  test("a seller's own variant listing does not answer for its base offering", async () => {
    // `nano-gpt/qwen3.8-max:thinking` says reasoning true and
    // `nano-gpt/qwen3.8-max` says false. Same seller, same feed — but those are
    // two products, not a disagreement, and the thinking listing must not answer
    // for the base one.
    const specs = await feedOf({
      'nano-gpt': { models: {
        'qwen3.8-max': { id: 'qwen3.8-max', reasoning: false },
        'qwen3.8-max:thinking': { id: 'qwen3.8-max:thinking', reasoning: true },
      } },
    });

    const facts = specs.intrinsic('qwen3.8-max', 'nano-gpt')!;
    assert.equal(facts.reasoning, false, 'the base listing answers, not the mode');
    assert.equal(facts.standing?.reasoning, 'serving-seller');
  });

  test('the vendor answers when the seller did not publish the field', async () => {
    const specs = await feedOf({
      reseller: { models: { 'zai-org/glm-5.3': { id: 'zai-org/glm-5.3', structured_output: false } } },
      zai: { models: { 'zai-org/glm-5.3': { id: 'zai-org/glm-5.3', structured_output: true } } },
    }, { 'z-ai': { label: 'Z.ai', storefronts: ['zai'], namespaces: ['zai-org'] } });

    const facts = specs.intrinsic('glm-5.3', 'clinepass')!;
    assert.equal(facts.structured, true);
    assert.equal(facts.standing?.structured, 'vendor-storefront');
  });

  test('a reseller inside the vendor namespace does not become the vendor', async () => {
    // The guard the vendors overlay exists for: Alibaba reselling a Z-AI model
    // must not answer as a first-party GLM declaration.
    const specs = await feedOf({
      alibaba: { models: { 'zai-org/glm-5.2': { id: 'zai-org/glm-5.2', structured_output: false } } },
      hyper: { models: { 'zai-org/glm-5.2': { id: 'zai-org/glm-5.2', structured_output: true } } },
    }, {
      'z-ai': { label: 'Z.ai', storefronts: ['zai'], namespaces: ['zai-org'] },
      alibaba: { label: 'Alibaba', storefronts: ['alibaba'], namespaces: ['qwen'] },
    });

    const facts = specs.intrinsic('glm-5.2', 'clinepass')!;
    assert.equal(facts.structured, undefined, 'neither side has standing');
    assert.equal(facts.conflicts.length, 1);
  });

  test('unanimity still needs no authority and is recorded as such', async () => {
    const specs = await feedOf({
      a: { models: { m: { id: 'm', reasoning: true } } },
      b: { models: { m: { id: 'm', reasoning: true } } },
    });
    const facts = specs.intrinsic('m', 'zzz')!;
    assert.equal(facts.reasoning, true);
    assert.equal(facts.standing?.reasoning, 'unanimous');
  });
});
