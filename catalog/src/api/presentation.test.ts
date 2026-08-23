import { describe, test, expect } from 'vitest';
import assert from 'node:assert/strict';
import { present, splitVendorName } from './presentation';

describe('presentation', () => {
  test('Ollama presentation does not claim plan-gated models are universally callable', () => {
    const ollama = present('ollama-cloud');
    expect(ollama.blurb).not.toMatch(/every plan/i);
    expect(ollama.note ?? '').not.toMatch(/not which models/i);
    expect(ollama.note ?? '').toMatch(/excluded/i);
  });
});

const qualifierOf = (modelId: string, displayName: string | null) =>
  splitVendorName(modelId, displayName).qualifier;

describe('the label a vendor puts on a model beyond its id', () => {
  test('surfaces a qualifier the id does not carry', () => {
    // models.dev serves the provider's own display name, and OpenCode uses it to
    // advertise: "Hy3 (8x usage)", "DeepSeek V4 Pro (New)", "GPT-5.6 Sol (50%
    // Off)". The catalog is not asserting the offer — it is reporting what the
    // provider calls the model, which is a fact with a source.
    assert.equal(qualifierOf('hy3', 'Hy3 (8x usage)'), '8x usage');
    assert.equal(qualifierOf('deepseek-v4-pro', 'DeepSeek V4 Pro (New)'), 'New');
    assert.equal(qualifierOf('gpt-5.6-sol', 'GPT-5.6 Sol (50% Off)'), '50% Off');
  });

  test('says nothing when the display name is just the id made pretty', () => {
    // Showing "GLM-5.3" beside glm-5.3 is the duplication this page just had
    // removed from it.
    assert.equal(qualifierOf('glm-5.3', 'GLM-5.3'), null);
    assert.equal(qualifierOf('grok-4.5', 'Grok 4.5'), null);
    assert.equal(qualifierOf('kimi-k2.7-code', 'Kimi K2.7 Code'), null);
    assert.equal(qualifierOf('minimax-m3', 'MiniMax-M3'), null);
  });

  test('is quiet when there is no display name at all', () => {
    assert.equal(qualifierOf('hy3', null), null);
    assert.equal(qualifierOf('hy3', 'hy3'), null);
  });

  test('ignores a parenthetical that only repeats the id', () => {
    assert.equal(qualifierOf('mimo-v2.5-free', 'MiMo-V2.5 (free)'), null);
  });
});

/** The two halves under test here; `restatesId` has its own block below. */
const nameParts = (modelId: string, displayName: string | null) => {
  const { base, qualifier } = splitVendorName(modelId, displayName);
  return { base, qualifier };
};

describe('splitting a vendor name into what the row prints and what the badge lifts', () => {
  test('the name no longer carries the qualifier the badge shows', () => {
    // The bug this replaced: the row printed "DeepSeek V4 Pro (New)" and then a
    // "New" pill beside it. The parenthetical is lifted, not copied — one fact,
    // one place. Every case here is a real row in the live catalog.
    assert.deepEqual(nameParts('deepseek-v4-pro', 'DeepSeek V4 Pro (New)'), {
      base: 'DeepSeek V4 Pro',
      qualifier: 'New',
    });
    assert.deepEqual(nameParts('hy3', 'Hy3 (8x usage)'), { base: 'Hy3', qualifier: '8x usage' });
    assert.deepEqual(nameParts('gpt-5.6-sol', 'GPT-5.6 Sol (50% Off)'), {
      base: 'GPT-5.6 Sol',
      qualifier: '50% Off',
    });
    assert.deepEqual(nameParts('x-preview-f-free', 'Ox Alpha Free (Unlimited)'), {
      base: 'Ox Alpha Free',
      qualifier: 'Unlimited',
    });
  });

  test('a name with nothing to lift is printed whole', () => {
    // No badge will render, so stripping anything here would delete the only
    // copy of the fact.
    assert.deepEqual(nameParts('glm-5.3', 'GLM-5.3'), { base: 'GLM-5.3', qualifier: null });
    assert.deepEqual(nameParts('grok-4.5', 'Grok 4.5'), { base: 'Grok 4.5', qualifier: null });
  });

  test('a parenthetical that only repeats the id stays in the name', () => {
    // vendorQualifier refuses to badge it, so the name must keep it: lifting
    // into a badge that never renders would silently drop text the provider
    // published.
    assert.deepEqual(nameParts('mimo-v2.5-free', 'MiMo-V2.5 (free)'), {
      base: 'MiMo-V2.5 (free)',
      qualifier: null,
    });
  });

  test('falls back to the id when the provider published no name', () => {
    // The row still has to print something, and the id is the API call.
    assert.deepEqual(nameParts('hy3', null), { base: 'hy3', qualifier: null });
    assert.deepEqual(nameParts('hy3', ''), { base: 'hy3', qualifier: null });
    assert.deepEqual(nameParts('hy3', '   '), { base: 'hy3', qualifier: null });
  });

  test('a name that is nothing but a qualifier keeps it rather than emptying the cell', () => {
    // Stripping would leave the name column blank, which is worse than the
    // duplication this function exists to remove.
    assert.deepEqual(nameParts('some-promo', '(New)'), { base: 'some-promo', qualifier: 'New' });
  });
});

describe('whether the name already says what the model id says', () => {
  // `restatesId` is what lets the row stop printing the bare id twice. It is
  // measured against the STRIPPED base, not the raw display name: "DeepSeek V4
  // Pro (New)" does not look like `deepseek-v4-pro` until the qualifier is
  // lifted off it, which is the whole reason the two live in one function.
  test('true when the name is the id made pretty', () => {
    assert.equal(splitVendorName('deepseek-v4-pro', 'DeepSeek V4 Pro (New)').restatesId, true);
    assert.equal(splitVendorName('hy3', 'Hy3 (8x usage)').restatesId, true);
    assert.equal(splitVendorName('glm-5.3', 'GLM-5.3').restatesId, true);
    assert.equal(splitVendorName('minimax-m3', 'MiniMax-M3').restatesId, true);
    assert.equal(splitVendorName('mimo-v2.5-free', 'MiMo-V2.5 Free').restatesId, true);
  });

  test('false when the name carries something the id does not', () => {
    // `x-preview-f-free` is served as "Ox Alpha Free": nothing about the name
    // tells you the id, so the row has to keep printing it.
    assert.equal(splitVendorName('x-preview-f-free', 'Ox Alpha Free (Unlimited)').restatesId, false);
    // A reseller prefix is part of the id and absent from the name.
    assert.equal(splitVendorName('cline-pass/glm-5.3', 'GLM-5.3').restatesId, false);
    // The id says `-free` and the name does not — a different offer.
    assert.equal(splitVendorName('hy3-free', 'Hy3').restatesId, false);
  });

  test('true when there is no published name, because the id is the name', () => {
    assert.equal(splitVendorName('hy3', null).restatesId, true);
    assert.equal(splitVendorName('gemma4:31b', 'gemma4:31b').restatesId, true);
  });
});
