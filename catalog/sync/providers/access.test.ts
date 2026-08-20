import assert from 'node:assert/strict';
import { describe, test } from 'node:test';
import { OLLAMA_CLOUD } from './index.ts';

/**
 * The reachability of a provider's roster is evidence, not configuration.
 *
 * Ollama Cloud rosters models this account cannot call: the provider answers
 * HTTP 403, "this model requires a subscription, upgrade for access". A roster
 * listing is not proof of access, and publishing a model the account cannot
 * reach makes the catalog claim availability it does not have.
 *
 * These lists were established by sending one minimal completion per rostered id
 * and recording the status. Pinning them here means a future edit that drops an
 * id has to explain itself, rather than quietly re-publishing something nobody
 * can call.
 */
const REQUIRES_SUBSCRIPTION = [
  'deepseek-v4-flash:0731',
  'deepseek-v4-flash:preview',
  'deepseek-v4-pro:0813',
  'deepseek-v4-pro:preview',
  'glm-5.1',
  'glm-5.2',
  'kimi-k2.6',
  'kimi-k2.7-code',
  'kimi-k3',
  'minimax-m2.7',
  'mistral-large-3:675b',
  'qwen3.5:397b',
];

/** Answered HTTP 200 on the same sweep, so they stay published. */
const REACHABLE = [
  'gemma4:31b',
  'gpt-oss:120b',
  'gpt-oss:20b',
  'minimax-m3',
  'nemotron-3-nano:30b',
  'nemotron-3-super',
  'nemotron-3-ultra',
];

describe('Ollama Cloud publish access', () => {
  test('withholds every model the account cannot call, with a reason', () => {
    const exclusions = OLLAMA_CLOUD.publishExclusions ?? {};
    for (const modelId of REQUIRES_SUBSCRIPTION) {
      assert.equal(exclusions[modelId], 'plan_required', `${modelId} must be withheld`);
    }
  });

  test('does not withhold a model that answered', () => {
    const exclusions = OLLAMA_CLOUD.publishExclusions ?? {};
    for (const modelId of REACHABLE) {
      assert.equal(exclusions[modelId], undefined, `${modelId} answered 200 and must stay published`);
    }
  });

  test('withholds nothing beyond what was actually tested', () => {
    // A reason nobody can point at evidence for is a guess. If an id is added
    // here it should arrive with its own probe result, and this assertion is what
    // forces that conversation.
    assert.deepEqual(
      Object.keys(OLLAMA_CLOUD.publishExclusions ?? {}).sort(),
      [...REQUIRES_SUBSCRIPTION].sort(),
    );
  });
});
