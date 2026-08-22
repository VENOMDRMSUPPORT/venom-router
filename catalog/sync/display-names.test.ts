import { describe, test } from 'node:test';
import assert from 'node:assert/strict';
import { loadDisplayNames, parseDisplayNames } from './display-names.ts';

const entry = (value: unknown) => ({
  value,
  ref: 'official docs row',
  sourceUrl: 'https://official.example/docs',
  evidence: ['The provider documents this exact spelling.'],
  reviewedAt: '2026-08-21',
});

describe('display names contract', () => {
  test('accepts a reviewed correction keyed by provider/model', () => {
    const parsed = parseDisplayNames({
      names: { 'opencode-zen/mimo-v2.5-free': entry('MiMo-V2.5 Free') },
    });
    assert.equal(parsed['opencode-zen/mimo-v2.5-free'].value, 'MiMo-V2.5 Free');
    assert.equal(parsed['opencode-zen/mimo-v2.5-free'].sourceUrl, 'https://official.example/docs');
  });

  test('rejects a key without a provider/model separator', () => {
    assert.throws(() => parseDisplayNames({ names: { 'just-a-model': entry('Name') } }), /provider\/model/);
  });

  test('rejects an empty value — an override must say something', () => {
    assert.throws(() => parseDisplayNames({ names: { 'p/m': entry('  ') } }), /value/i);
  });

  test('rejects evidence without a verifiable source URL', () => {
    const e = entry('Name');
    e.sourceUrl = 'not-a-url';
    assert.throws(() => parseDisplayNames({ names: { 'p/m': e } }), /sourceUrl/i);
  });

  test('rejects an entry with no evidence lines', () => {
    const e = entry('Name');
    e.evidence = [];
    assert.throws(() => parseDisplayNames({ names: { 'p/m': e } }), /evidence/i);
  });

  test('the shipped overlay parses and names only offerings that need correcting', () => {
    const shipped = loadDisplayNames();
    for (const [key, fact] of Object.entries(shipped)) {
      assert.match(key, /^[^/]+\/.+$/);
      assert.ok(fact.value.trim(), `${key} must carry a non-empty name`);
    }
  });
});
