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
 * and reading the error back — the STATUS alone was not enough, because two
 * different refusals both arrive as 403 and they do not mean the same thing.
 * Pinning them here means a future edit that drops an id has to explain itself,
 * rather than quietly re-publishing something nobody can call.
 */
/**
 * "A subscription would unlock this." Eleven ids answered exactly that.
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
  'minimax-m2.7',
  'mistral-large-3:675b',
  'qwen3.5:397b',
];

/**
 * A different refusal, and a more permanent one: "requires both a Pro, Max, or
 * Team plan AND extra usage (it does not use included plan usage)". A plan alone
 * does not unlock it, so it can never belong to a free offering. Kept apart from
 * the list above because the two answer different questions about an upgrade.
 */
const REQUIRES_PLAN_AND_EXTRA_USAGE = ['kimi-k3'];

const WITHHELD = [...REQUIRES_SUBSCRIPTION, ...REQUIRES_PLAN_AND_EXTRA_USAGE];

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
    for (const modelId of WITHHELD) {
      assert.equal(exclusions[modelId], 'plan_required', `${modelId} must be withheld`);
    }
  });

  test('does not withhold a model that answered', () => {
    const exclusions = OLLAMA_CLOUD.publishExclusions ?? {};
    for (const modelId of REACHABLE) {
      assert.equal(exclusions[modelId], undefined, `${modelId} answered 200 and must stay published`);
    }
  });

  test('keeps the two refusals distinct, because they mean different things', () => {
    // Flattening them would lose the only operational fact here: buying a plan
    // brings eleven of these back and leaves kimi-k3 exactly where it is.
    assert.ok(!REQUIRES_SUBSCRIPTION.includes('kimi-k3'));
    assert.deepEqual(REQUIRES_PLAN_AND_EXTRA_USAGE, ['kimi-k3']);
    assert.equal(WITHHELD.length, REQUIRES_SUBSCRIPTION.length + 1);
  });

  test('withholds nothing beyond what was actually tested', () => {
    // A reason nobody can point at evidence for is a guess. If an id is added
    // here it should arrive with its own probe result, and this assertion is what
    // forces that conversation.
    assert.deepEqual(
      Object.keys(OLLAMA_CLOUD.publishExclusions ?? {}).sort(),
      [...WITHHELD].sort(),
    );
  });
});
