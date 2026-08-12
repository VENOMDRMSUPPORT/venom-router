import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { computeVQ, computeVO, comparable, formatVQ, percentile } from './venom-score.ts';
import type { ScoreProfile } from './venom-score.ts';
import { fitCalibration } from './calibration.ts';
import type { Resolution } from '../identity.ts';

const RESOLVED: Resolution = {
  status: 'resolved',
  rule: 'exact',
  target: 'openai/gpt-oss-20b',
  candidates: ['openai/gpt-oss-20b'],
};
const AMBIGUOUS: Resolution = { status: 'ambiguous', candidates: ['a/x', 'b/x'] };
const UNRESOLVED: Resolution = { status: 'unresolved', candidates: [] };

function lcg(seed: number) {
  let s = seed;
  return () => ((s = (s * 1103515245 + 12345) % 2147483648) / 2147483648 - 0.5) * 2;
}
const rnd = lcg(9);
const CAL = fitCalibration(
  Array.from({ length: 60 }, (_, i) => {
    const x = 1000 + i * 5;
    return { id: `m${i}`, identity: `m${i}`, group: 'g', x, y: 0.11 * x - 99 + rnd() * 4 };
  }),
)!;

describe('computeVQ — identity gates everything', () => {
  test('an ambiguous identity yields unrated, never a value', () => {
    const vq = computeVQ(AMBIGUOUS, { direct: 42 }, CAL);
    assert.equal(vq.level, 'unrated');
    assert.equal(vq.value, null);
  });

  test('an unresolved identity yields unrated even with evidence present', () => {
    const vq = computeVQ(UNRESOLVED, { direct: 42, calibratable: 1200 }, CAL);
    assert.equal(vq.value, null);
  });

  test('a measured value records its source and upstream id', () => {
    const vq = computeVQ(RESOLVED, { direct: 15.2 }, CAL);
    assert.equal(vq.level, 'measured');
    assert.equal(vq.value, 15.2);
    assert.equal(vq.source, 'artificial_analysis');
    assert.equal(vq.sourceModelId, 'openai/gpt-oss-20b');
    assert.equal(vq.identityRule, 'exact');
  });

  test('direct evidence outranks calibratable evidence', () => {
    const vq = computeVQ(RESOLVED, { direct: 15.2, calibratable: 1300 }, CAL);
    assert.equal(vq.level, 'measured');
    assert.equal(vq.value, 15.2);
  });
});

describe('computeVQ — calibrated values carry their real uncertainty', () => {
  test('uses the calibration and its LOO error', () => {
    const vq = computeVQ(RESOLVED, { calibratable: 1200 }, CAL);
    assert.equal(vq.level, 'calibrated');
    assert.equal(vq.uncertainty, CAL.looRmse);
    assert.ok(vq.uncertainty! > 0);
  });

  test('withholds the value when the calibration is unusable', () => {
    const junk = fitCalibration(
      Array.from({ length: 60 }, (_, i) => ({ id: `j${i}`, identity: `j${i}`, group: 'g', x: 1000 + i, y: 30 + rnd() * 40 })),
    );
    const vq = computeVQ(RESOLVED, { calibratable: 1200 }, junk);
    assert.equal(vq.level, 'unrated');
  });

  test('withholds when there is no calibration at all', () => {
    assert.equal(computeVQ(RESOLVED, { calibratable: 1200 }, null).level, 'unrated');
  });
});

describe('precision follows evidence', () => {
  test('a measured value keeps one decimal', () => {
    assert.equal(formatVQ(computeVQ(RESOLVED, { direct: 15.24 }, CAL)), '15.2');
  });

  test('a calibrated value never shows a decimal it has not earned', () => {
    const s = formatVQ(computeVQ(RESOLVED, { calibratable: 1204 }, CAL));
    assert.ok(!s.includes('.'), `calibrated rendered as "${s}"`);
  });

  test('unrated renders as a dash, never as zero', () => {
    assert.equal(formatVQ(computeVQ(UNRESOLVED, {}, CAL)), '—');
  });

  test('a bound renders as a bound', () => {
    const vq = computeVQ(RESOLVED, { bound: { value: 56, side: 'lower', reason: 'base gpt-5.5 measured' } }, CAL);
    assert.equal(vq.level, 'bounded');
    assert.equal(formatVQ(vq), '≥ 56');
  });
});

describe('comparable — overlapping intervals are not a ranking', () => {
  test('two calibrated values a fraction apart are NOT comparable', () => {
    const a = computeVQ(RESOLVED, { calibratable: 1200 }, CAL);
    const b = computeVQ(RESOLVED, { calibratable: 1203 }, CAL);
    assert.equal(comparable(a, b), false);
  });

  test('two calibrated values far apart ARE comparable', () => {
    const a = computeVQ(RESOLVED, { calibratable: 1000 }, CAL);
    const b = computeVQ(RESOLVED, { calibratable: 1400 }, CAL);
    assert.equal(comparable(a, b), true);
  });

  test('an unrated value is comparable to nothing', () => {
    const a = computeVQ(RESOLVED, { direct: 60 }, CAL);
    assert.equal(comparable(a, computeVQ(UNRESOLVED, {}, CAL)), false);
  });
});

describe('computeVO', () => {
  const profile: ScoreProfile = {
    id: 'balanced',
    label: 'Balanced',
    weights: { context: 0.3, output: 0.2, capabilities: 0.3, cost: 0.2 },
  };
  const pop = {
    context: [Math.log(8000), Math.log(128000), Math.log(1000000)],
    output: [Math.log(4000), Math.log(32000), Math.log(128000)],
    cost: [0, 2, 10, 50],
  };

  test('a free model is scored as cheapest, not as missing', () => {
    const vo = computeVO({ costOutputPerM: 0 }, pop, profile);
    assert.ok(vo.dimensions.cost! > 80, `free scored ${vo.dimensions.cost}`);
    assert.ok(!vo.missing.includes('cost'));
  });

  test('an absent price is missing, and is not silently treated as free', () => {
    const vo = computeVO({ costOutputPerM: null }, pop, profile);
    assert.equal(vo.dimensions.cost, null);
    assert.ok(vo.missing.includes('cost'));
  });

  test('missing dimensions are declared, and weights renormalise over the rest', () => {
    const full = computeVO(
      { contextTokens: 1000000, maxOutputTokens: 128000, tools: true, reasoning: true, inputModalities: ['text'], costOutputPerM: 0 },
      pop, profile,
    );
    const partial = computeVO({ contextTokens: 1000000 }, pop, profile);
    assert.deepEqual(partial.missing.sort(), ['capabilities', 'cost', 'output']);
    assert.equal(full.missing.length, 0);
    assert.ok(partial.value > 0, 'a partial score still produces a value from what is known');
  });

  test('every dimension is reported, so any score can be explained', () => {
    const vo = computeVO({ contextTokens: 128000, maxOutputTokens: 32000, tools: true, inputModalities: ['text', 'image'], costOutputPerM: 2 }, pop, profile);
    assert.deepEqual(Object.keys(vo.dimensions).sort(), ['capabilities', 'context', 'cost', 'output']);
  });

  test('a bigger context never lowers the context dimension', () => {
    const small = computeVO({ contextTokens: 8000 }, pop, profile);
    const big = computeVO({ contextTokens: 1000000 }, pop, profile);
    assert.ok(big.dimensions.context! > small.dimensions.context!);
  });

  test('profile weights actually change the result', () => {
    const facts = { contextTokens: 1000000, maxOutputTokens: 4000, tools: false, inputModalities: ['text'], costOutputPerM: 50 };
    const ctxHeavy = computeVO(facts, pop, { ...profile, id: 'ctx', weights: { context: 1, output: 0, capabilities: 0, cost: 0 } });
    const costHeavy = computeVO(facts, pop, { ...profile, id: 'cost', weights: { context: 0, output: 0, capabilities: 0, cost: 1 } });
    assert.notEqual(ctxHeavy.value, costHeavy.value);
  });
});

describe('percentile', () => {
  test('the largest value in a population lands at the top', () => {
    assert.ok(percentile(100, [1, 2, 3, 100]) > 80);
  });
  test('an empty population is neutral rather than an exception', () => {
    assert.equal(percentile(5, []), 50);
  });
});
