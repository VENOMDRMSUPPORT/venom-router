import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { normalizeId, buildIndex, resolveIdentity } from './identity.ts';

/**
 * The upstream shapes below are abridged from a real OpenRouter fetch
 * (2026-08-12). The dangerous pairs are kept verbatim: every one of them was
 * mis-bound by a prefix-similarity matcher during design.
 */
const UPSTREAM = buildIndex([
  { id: 'openai/gpt-oss-20b' },
  { id: 'openai/gpt-oss-20b:free' },
  { id: 'openai/gpt-oss-120b' },
  { id: 'openai/gpt-oss-safeguard-20b' },
  { id: 'qwen/qwen3.5-9b' },
  { id: 'qwen/qwen3.5-397b-a17b' },
  { id: 'qwen/qwen3.5-plus-20260420' },
  { id: 'google/gemma-4-31b-it' },
  { id: 'google/gemma-4-31b-it:free' },
  { id: 'google/gemma-4-26b-a4b-it' },
  { id: 'nvidia/nemotron-3-nano-30b-a3b' },
  { id: 'nvidia/nemotron-3-nano-omni-30b-a3b-reasoning' },
  { id: 'nvidia/nemotron-3-ultra-550b-a55b' },
  { id: 'anthropic/claude-opus-4.8' },
  { id: 'deepseek/deepseek-v4-flash' },
  { id: 'tencent/hy3' },
  { id: 'mistralai/mistral-large-2512' },
]);

describe('normalizeId', () => {
  test('drops the vendor prefix', () => {
    assert.equal(normalizeId('openai/gpt-oss-20b'), 'gpt-oss-20b');
  });

  test('treats . - _ : as one separator class', () => {
    assert.equal(normalizeId('claude-opus-4-8'), normalizeId('anthropic/claude-opus-4.8'));
    assert.equal(normalizeId('deepseek-v4-flash:0731'), 'deepseek-v4-flash-0731');
  });

  test('drops plan variants, which carry no identity', () => {
    assert.equal(normalizeId('openai/gpt-oss-20b:free'), normalizeId('openai/gpt-oss-20b'));
    assert.equal(normalizeId('openai/gpt-5-codex:batch'), normalizeId('openai/gpt-5-codex'));
  });

  test('KEEPS size tokens — they are identity, not decoration', () => {
    assert.notEqual(normalizeId('gpt-oss-20b'), normalizeId('gpt-oss-120b'));
    assert.notEqual(normalizeId('qwen3.5-9b'), normalizeId('qwen3.5-397b-a17b'));
  });
});

describe('R1 exact', () => {
  test('binds an unambiguous exact match', () => {
    const r = resolveIdentity('gpt-oss:120b', UPSTREAM);
    assert.equal(r.status, 'resolved');
    assert.equal(r.status === 'resolved' && r.target, 'openai/gpt-oss-120b');
    assert.equal(r.status === 'resolved' && r.rule, 'exact');
  });

  test('a plan-variant twin is one identity, not an ambiguity', () => {
    // gpt-oss-20b and gpt-oss-20b:free must collapse, or every free model
    // would land in the review queue forever.
    const r = resolveIdentity('gpt-oss:20b', UPSTREAM);
    assert.equal(r.status, 'resolved');
    assert.equal(r.status === 'resolved' && r.target, 'openai/gpt-oss-20b');
  });
});

describe('R2 free-variant', () => {
  test('resolves a -free model to its base', () => {
    const r = resolveIdentity('hy3-free', UPSTREAM);
    assert.equal(r.status, 'resolved');
    assert.equal(r.status === 'resolved' && r.target, 'tencent/hy3');
    assert.equal(r.status === 'resolved' && r.rule, 'free-variant');
  });

  test('does not invent a base that does not exist', () => {
    assert.equal(resolveIdentity('big-pickle-free', UPSTREAM).status, 'unresolved');
  });
});

describe('R3 exact-size — the rule that prevents the catastrophic bind', () => {
  test('binds only the exactly-sized counterpart', () => {
    const r = resolveIdentity('qwen3.5:397b', UPSTREAM);
    assert.equal(r.status, 'resolved');
    assert.equal(r.status === 'resolved' && r.target, 'qwen/qwen3.5-397b-a17b');
  });

  test('never binds a differently-sized sibling', () => {
    // The failure this whole module exists to prevent: 397b must not become 9b.
    const r = resolveIdentity('qwen3.5:397b', UPSTREAM);
    assert.notEqual(r.status === 'resolved' && r.target, 'qwen/qwen3.5-9b');
  });

  test('binds gemma4:31b to the 31b build, not the 26b one', () => {
    const r = resolveIdentity('gemma4:31b', UPSTREAM);
    assert.equal(r.status, 'resolved');
    assert.equal(r.status === 'resolved' && r.target, 'google/gemma-4-31b-it');
  });

  test('declines when no exactly-sized counterpart exists', () => {
    // mistral-large-3:675b has no 675b counterpart upstream. Silence is correct.
    assert.equal(resolveIdentity('mistral-large-3:675b', UPSTREAM).status, 'unresolved');
  });
});

describe('ambiguity is a decision, not a fallthrough', () => {
  test('two distinct same-size candidates produce ambiguous, never a pick', () => {
    const r = resolveIdentity('nemotron-3-nano:30b', UPSTREAM);
    assert.equal(r.status, 'ambiguous');
    assert.equal(r.candidates.length, 2);
  });

  test('an ambiguous result carries no target to score from', () => {
    const r = resolveIdentity('nemotron-3-nano:30b', UPSTREAM);
    assert.ok(!('target' in r));
  });
});

describe('R4 overlay', () => {
  test('a reviewed mapping outranks every inferred rule', () => {
    // Ollama's bare "nemotron-3-ultra" has no size token, so no rule can bind
    // it. A human confirms it once; from then on it is deterministic.
    const r = resolveIdentity('nemotron-3-ultra', UPSTREAM, {
      'nemotron-3-ultra': 'nvidia/nemotron-3-ultra-550b-a55b',
    });
    assert.equal(r.status, 'resolved');
    assert.equal(r.status === 'resolved' && r.rule, 'overlay');
  });

  test('an overlay pointing at a vanished model resolves to nothing, not to a guess', () => {
    const r = resolveIdentity('nemotron-3-ultra', UPSTREAM, {
      'nemotron-3-ultra': 'nvidia/model-that-left-the-index',
    });
    assert.equal(r.status, 'unresolved');
  });
});

describe('no fuzzy matching exists', () => {
  test('a name with no exact counterpart stays unresolved however similar', () => {
    // qwen3.5-plus vs qwen3.5-plus-20260420: plausible to a human, and a dated
    // snapshot probably IS the same model — but that is a human's call, not a
    // matcher's. Until someone records it in the overlay, no score.
    assert.equal(resolveIdentity('qwen3.5-plus', UPSTREAM).status, 'unresolved');
  });

  test('a nearby version is never substituted', () => {
    assert.equal(resolveIdentity('mimo-v2-pro', UPSTREAM).status, 'unresolved');
  });
});
