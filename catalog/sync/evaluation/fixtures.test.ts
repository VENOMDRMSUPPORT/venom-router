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

describe('grading whether the model solved it, not how it wrote it', () => {
  const fixtures = buildEvaluationFixtures();
  const answer = (content: string): unknown => ({ choices: [{ message: { content } }] });

  // reasoning-01: 7x + 5 = 26, original 3. Captured verbatim from evaluation
  // runs, where each of these scored 4/5 on its answer alone — the fifth
  // criterion required a division sign, which none of them needs to write.
  const CORRECT = [
    'Equation: $7x + 5 = 26$\n\nAnswer: $3$',
    '**Equation and solution**\n\n\[\n7x + 5 = 26 \quad\Longrightarrow\quad x = 3\n\]\n\nSo the original number is **3**.',
    'Equation: 7x + 5 = 26\nAnswer: 3',
    '(26 - 5) / 7 = 3',
  ];

  test('accepts a correct solution however it is written', () => {
    for (const content of CORRECT) {
      assert.deepEqual(
        fixtures.reasoning[0].grade(answer(content)),
        { weightedSuccesses: 5, weightedCriteria: 5 },
        `scored short: ${JSON.stringify(content)}`,
      );
    }
  });

  test('still refuses an answer that is simply wrong', () => {
    // The right equation with the wrong root, and a bare number with no working.
    assert.ok(fixtures.reasoning[0].grade(answer('7x + 5 = 26, so x = 9')).weightedSuccesses < 5);
    assert.ok(fixtures.reasoning[0].grade(answer('3')).weightedSuccesses < 5);
  });

  test('reads the worked answer whichever name the gateway filed it under', () => {
    // Same trace, two spellings. `reasoning` is what the Responses path folds it
    // into; `reasoning_content` is what every OpenAI-compatible reasoning gateway
    // here actually sends, and reading only the first left 1,800 retained samples
    // looking empty. Both must grade identically or a replay changes the answer.
    const trace = 'so 7x + 5 = 26, which gives x = 3';
    const graded = (message: Record<string, unknown>) =>
      fixtures.reasoning[0].grade({ choices: [{ message }] });

    assert.deepEqual(graded({ content: '', reasoning: trace }), graded({ content: '', reasoning_content: trace }));
    assert.equal(graded({ content: '', reasoning_content: trace }).weightedSuccesses, 5);
  });

  test('a provider spelling never overrides the name already normalised', () => {
    // The Responses path fills `reasoning` itself. If both arrive, the folded one
    // wins: it is the answer this transport already committed to.
    const graded = fixtures.reasoning[0].grade({
      choices: [{ message: { content: '', reasoning: '7x + 5 = 26 so x = 3', reasoning_content: 'nonsense' } }],
    });
    assert.equal(graded.weightedSuccesses, 5);
  });

  test('leaves the test-set digest untouched, so no stored score is invalidated', () => {
    // Grading is outside the digest by design. Repairing a criterion corrects
    // what future evidence means without silently discarding what was paid for.
    assert.equal(
      fixtureDigest(buildEvaluationFixtures()),
      'cbb5148e0fe42d3f7bee86aefe87d875e7ce263d682950e5bf15d2c6acc1e223',
    );
  });
});
