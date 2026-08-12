import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { fitCalibration, isAcceptable, applyCalibration } from './calibration.ts';
import type { Observation } from './calibration.ts';

/** Deterministic pseudo-noise: tests must not flake on a random seed. */
function lcg(seed: number) {
  let s = seed;
  return () => ((s = (s * 1103515245 + 12345) % 2147483648) / 2147483648 - 0.5) * 2;
}

function linear(n: number, slope: number, intercept: number, noise: number, group = 'g'): Observation[] {
  const rnd = lcg(42);
  return Array.from({ length: n }, (_, i) => {
    const x = 1000 + i * 5;
    return { id: `m${i}`, group, x, y: slope * x + intercept + rnd() * noise };
  });
}

describe('fitCalibration', () => {
  test('recovers a known linear relation', () => {
    const c = fitCalibration(linear(60, 0.11, -99, 1));
    assert.ok(c);
    assert.ok(Math.abs(c.slope - 0.11) < 0.01, `slope ${c.slope}`);
    assert.ok(Math.abs(c.intercept + 99) < 5, `intercept ${c.intercept}`);
  });

  test('refuses to fit below the minimum overlap', () => {
    assert.equal(fitCalibration(linear(10, 0.11, -99, 1)), null);
  });

  test('LOO error grows with noise — it is not a formality', () => {
    const clean = fitCalibration(linear(60, 0.11, -99, 0.5))!;
    const noisy = fitCalibration(linear(60, 0.11, -99, 12))!;
    assert.ok(noisy.looRmse > clean.looRmse * 3, `${clean.looRmse} vs ${noisy.looRmse}`);
  });
});

describe('group bias', () => {
  /** One group is shifted far off the shared line — the measured Mistral case. */
  const biased: Observation[] = [
    ...linear(40, 0.11, -99, 1, 'ok-vendor'),
    ...linear(6, 0.11, -99 - 18, 1, 'skewed-vendor'),
  ];

  test('detects and reports the skewed group', () => {
    const c = fitCalibration(biased)!;
    assert.ok(c.groupBias['skewed-vendor'].bias < -10);
  });

  test('excludes it from the published fit', () => {
    const c = fitCalibration(biased)!;
    assert.deepEqual(c.excludedGroups, ['skewed-vendor']);
    assert.equal(c.n, 40);
  });

  test('excluding it improves the honest error', () => {
    const withBias = fitCalibration(biased, { maxGroupBias: 999 })!;
    const without = fitCalibration(biased)!;
    assert.ok(without.looRmse < withBias.looRmse, `${without.looRmse} !< ${withBias.looRmse}`);
  });

  test('a small group is never excluded — too few points to judge', () => {
    const c = fitCalibration(
      [...linear(40, 0.11, -99, 1, 'ok'), ...linear(2, 0.11, -140, 1, 'tiny')],
      { minGroupSize: 3 },
    )!;
    assert.ok(!c.excludedGroups.includes('tiny'));
  });
});

describe('isAcceptable — the gate that withholds bad calibration', () => {
  test('accepts a tight fit', () => {
    assert.equal(isAcceptable(fitCalibration(linear(60, 0.11, -99, 1))), true);
  });

  test('rejects when the source cannot say by how much', () => {
    // Same ordering, far too much scatter to publish a number from.
    const c = fitCalibration(linear(60, 0.11, -99, 40))!;
    assert.ok(c.looRmse / c.baselineSd > 0.55);
    assert.equal(isAcceptable(c), false);
  });

  test('rejects a null fit rather than throwing', () => {
    assert.equal(isAcceptable(null), false);
  });

  test('rejects when rank order disagrees', () => {
    const rnd = lcg(7);
    const scrambled: Observation[] = Array.from({ length: 60 }, (_, i) => ({
      id: `m${i}`,
      group: 'g',
      x: 1000 + i * 5,
      y: 30 + rnd() * 30,
    }));
    assert.equal(isAcceptable(fitCalibration(scrambled)), false);
  });
});

describe('applyCalibration', () => {
  test('carries the LOO error as the value uncertainty', () => {
    const c = fitCalibration(linear(60, 0.11, -99, 6))!;
    const out = applyCalibration(c, 1200);
    assert.equal(out.uncertainty, c.looRmse);
    assert.ok(out.uncertainty > 0, 'a calibrated value must never claim zero uncertainty');
  });

  test('is a pure function of the fit', () => {
    const c = fitCalibration(linear(60, 0.11, -99, 2))!;
    assert.deepEqual(applyCalibration(c, 1204), applyCalibration(c, 1204));
  });
});
