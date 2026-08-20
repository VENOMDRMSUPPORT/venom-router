import { describe, test, expect } from 'vitest';
import assert from 'node:assert/strict';
import { present, vendorQualifier } from './presentation';

describe('presentation', () => {
  test('Ollama presentation does not claim plan-gated models are universally callable', () => {
    const ollama = present('ollama-cloud');
    expect(ollama.blurb).not.toMatch(/every plan/i);
    expect(ollama.note ?? '').not.toMatch(/not which models/i);
    expect(ollama.note ?? '').toMatch(/excluded/i);
  });
});

describe('the label a vendor puts on a model beyond its id', () => {
  test('surfaces a qualifier the id does not carry', () => {
    // models.dev serves the provider's own display name, and OpenCode uses it to
    // advertise: "Hy3 (8x usage)", "DeepSeek V4 Pro (New)", "GPT-5.6 Sol (50%
    // Off)". The catalog is not asserting the offer — it is reporting what the
    // provider calls the model, which is a fact with a source.
    assert.equal(vendorQualifier('hy3', 'Hy3 (8x usage)'), '8x usage');
    assert.equal(vendorQualifier('deepseek-v4-pro', 'DeepSeek V4 Pro (New)'), 'New');
    assert.equal(vendorQualifier('gpt-5.6-sol', 'GPT-5.6 Sol (50% Off)'), '50% Off');
  });

  test('says nothing when the display name is just the id made pretty', () => {
    // Showing "GLM-5.3" beside glm-5.3 is the duplication this page just had
    // removed from it.
    assert.equal(vendorQualifier('glm-5.3', 'GLM-5.3'), null);
    assert.equal(vendorQualifier('grok-4.5', 'Grok 4.5'), null);
    assert.equal(vendorQualifier('kimi-k2.7-code', 'Kimi K2.7 Code'), null);
    assert.equal(vendorQualifier('minimax-m3', 'MiniMax-M3'), null);
  });

  test('is quiet when there is no display name at all', () => {
    assert.equal(vendorQualifier('hy3', null), null);
    assert.equal(vendorQualifier('hy3', 'hy3'), null);
  });

  test('ignores a parenthetical that only repeats the id', () => {
    assert.equal(vendorQualifier('mimo-v2.5-free', 'MiMo-V2.5 (free)'), null);
  });
});
