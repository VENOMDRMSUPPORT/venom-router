import { describe, test } from 'node:test';
import assert from 'node:assert/strict';
import { parseReviewedFacts } from './reviewed-facts.ts';

const evidence = (value: unknown) => ({
  value,
  ref: 'official.model.field',
  sourceUrl: 'https://official.example/model',
  evidence: ['The official model card states this value.'],
  reviewedAt: '2026-08-19',
});

describe('reviewed facts contract', () => {
  test('accepts every operational field with its exact value type', () => {
    const parsed = parseReviewedFacts({
      facts: {
        'provider/model': {
          context: evidence(1_000_000),
          maxOutput: evidence(128_000),
          inputModalities: evidence(['text', 'image']),
          tools: evidence(true),
          reasoning: evidence(false),
          structured: evidence(true),
          attachment: evidence(false),
        },
      },
    });

    assert.equal(parsed['provider/model'].context?.value, 1_000_000);
    assert.deepEqual(parsed['provider/model'].inputModalities?.value, ['text', 'image']);
    assert.equal(parsed['provider/model'].attachment?.value, false);
  });

  test('rejects a field with the wrong value type', () => {
    assert.throws(
      () => parseReviewedFacts({ facts: { 'provider/model': { context: evidence('1M') } } }),
      /context.*number/i,
    );
  });

  test('rejects evidence without a verifiable source URL', () => {
    const fact = evidence(true);
    fact.sourceUrl = '';
    assert.throws(
      () => parseReviewedFacts({ facts: { 'provider/model': { tools: fact } } }),
      /sourceUrl/i,
    );
  });
});
