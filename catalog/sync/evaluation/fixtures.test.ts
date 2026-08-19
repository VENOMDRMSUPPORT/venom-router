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

describe('grading a reasoning model', () => {
  const fixtures = buildEvaluationFixtures();
  const reply = (content: string, reasoning: string): unknown => ({
    choices: [{ message: { role: 'assistant', content, reasoning } }],
  });
  // The exact trace shape ClinePass returns: the answer lives in `content`, and
  // `reasoning` carries a long think-aloud that quotes the answer, the prompt,
  // and sometimes fenced JSON. Concatenating the two made JSON.parse throw, so
  // a perfect answer scored zero on every sample of three dimensions.
  const TRACE = 'Thinking Process:\n1. The prompt says "Return JSON only".\n'
    + '```json\n{"scratch": true}\n```\nLet me reconsider. Done.';

  test('scores a correct JSON answer that arrives beside a reasoning trace', () => {
    const cases = [
      { dimension: 'structuredOutput' as const, content: '{"recordId": 1, "approved": true, "label": "catalog-1"}' },
      { dimension: 'longContext' as const, content: '{"first":"alpha-01","second":"omega-20","combined":"alpha-01:omega-20"}' },
      { dimension: 'vision' as const, content: '{"digit":"1","color":"black","background":"white"}' },
    ];
    for (const { dimension, content } of cases) {
      const scenario = fixtures[dimension][0];
      assert.deepEqual(
        scenario.grade(reply(content, TRACE)),
        { weightedSuccesses: 5, weightedCriteria: 5 },
        `${scenario.id} must be graded on its answer, not on its thinking`,
      );
    }
  });

  test('accepts a fenced JSON answer, which is what most models actually emit', () => {
    const scenario = fixtures.structuredOutput[0];
    const fenced = '```json\n{"recordId": 1, "approved": true, "label": "catalog-1"}\n```';
    assert.deepEqual(scenario.grade(reply(fenced, TRACE)), { weightedSuccesses: 5, weightedCriteria: 5 });
  });

  test('reads a vision digit whether the model typed it as a number or a string', () => {
    const scenario = fixtures.vision[0];
    // Observed verbatim from ClinePass/kimi-k2.6: the image was read correctly
    // and the digit came back as a JSON number. Nothing in the prompt asks for a
    // string, so rejecting it scored a right answer 3/5.
    const numeric = '{"digit":1,"color":"black","background":"white"}';
    assert.deepEqual(scenario.grade(reply(numeric, TRACE)), { weightedSuccesses: 5, weightedCriteria: 5 });
  });

  test('still refuses a wrong answer, and still catches a filler echo', () => {
    const structured = fixtures.structuredOutput[0];
    assert.equal(structured.grade(reply('{"recordId": 2, "approved": false, "label": "nope"}', TRACE)).weightedSuccesses, 2);
    assert.equal(structured.grade(reply('no json here at all', TRACE)).weightedSuccesses, 0);

    const longContext = fixtures.longContext[0];
    const echoed = '{"first":"alpha-01","second":"omega-20","combined":"alpha-01:omega-20"}'
      + ' Record 1: routine inventory entry with no authorization.';
    assert.equal(longContext.grade(reply(echoed, TRACE)).weightedSuccesses, 4);
  });

  test('pins the test-set digest, because changing it re-runs the whole paid corpus', () => {
    // Grading logic is deliberately NOT part of this digest, so a grader repair
    // keeps every correctly-scored dimension. Touching a payload does change it
    // and invalidates every stored score — this test makes that deliberate.
    assert.equal(
      fixtureDigest(buildEvaluationFixtures()),
      'cbb5148e0fe42d3f7bee86aefe87d875e7ce263d682950e5bf15d2c6acc1e223',
    );
  });
});
