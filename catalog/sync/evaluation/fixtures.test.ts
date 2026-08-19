import assert from 'node:assert/strict';
import { describe, test } from 'node:test';
import { QUALITY_DIMENSIONS } from './score.ts';
import { buildEvaluationFixtures, fixtureDigest } from './fixtures.ts';

describe('catalog evaluation fixtures', () => {
  test('ships 20 unique, five-criterion scenarios for every quality dimension', () => {
    const fixtures = buildEvaluationFixtures();
    assert.deepEqual(Object.keys(fixtures).sort(), [...QUALITY_DIMENSIONS].sort());
    for (const dimension of QUALITY_DIMENSIONS) {
      const scenarios = fixtures[dimension];
      assert.equal(scenarios.length, 20, dimension);
      assert.equal(new Set(scenarios.map((scenario) => scenario.id)).size, 20, dimension);
      for (const scenario of scenarios) {
        const graded = scenario.grade(scenario.expectedResponse);
        assert.deepEqual(graded, { weightedSuccesses: 5, weightedCriteria: 5 }, scenario.id);
        assert.equal(typeof scenario.payload, 'object', scenario.id);
      }
    }
  });

  test('produces a stable SHA-256 test-set digest', () => {
    const first = fixtureDigest(buildEvaluationFixtures());
    const second = fixtureDigest(buildEvaluationFixtures());
    assert.match(first, /^[a-f0-9]{64}$/);
    assert.equal(first, second);
  });

  test('does not require provider-specific response_format support to grade JSON quality', () => {
    const fixtures = buildEvaluationFixtures();
    for (const dimension of ['longContext', 'structuredOutput', 'vision'] as const) {
      for (const scenario of fixtures[dimension]) {
        assert.equal('response_format' in (scenario.payload as Record<string, unknown>), false, scenario.id);
      }
    }
  });

  test('does not force required tool_choice on reasoning providers', () => {
    const scenario = buildEvaluationFixtures().toolCalling[0];
    assert.equal('tool_choice' in (scenario.payload as Record<string, unknown>), false);
    assert.equal(Array.isArray((scenario.payload as Record<string, unknown>).tools), true);
  });

  test('grades a provider response that exposes the answer in reasoning when content is empty', () => {
    const scenario = buildEvaluationFixtures().reasoning[0];
    const response = {
      choices: [{ message: { content: '', reasoning: '(26 - 5) / 7 = 3' } }],
    };
    assert.deepEqual(scenario.grade(response), { weightedSuccesses: 5, weightedCriteria: 5 });
  });
});
