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
    return { id: `${group}-m${i}`, identity: `${group}-m${i}`, group, x, y: slope * x + intercept + rnd() * noise };
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
      identity: `m${i}`,
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
    const out = applyCalibration(c, 1200)!;
    assert.equal(out.uncertainty, c.looRmse);
    assert.ok(out.uncertainty > 0, 'a calibrated value must never claim zero uncertainty');
  });

  test('is a pure function of the fit', () => {
    const c = fitCalibration(linear(60, 0.11, -99, 2))!;
    assert.deepEqual(applyCalibration(c, 1204), applyCalibration(c, 1204));
  });
});

describe('plan twins must not be counted as independent observations', () => {
  /**
   * Found by audit on 2026-08-12: the live fit ran on 83 rows holding only 57
   * distinct models, because upstream publishes `X`, `X:batch` and `X:free`
   * with identical figures. Three separate things break when they are left in.
   */
  const base = linear(40, 0.11, -99, 3);
  const withTwins = [
    ...base,
    ...base.map((o) => ({ ...o, id: `${o.id}:batch` })), // same identity, same x/y
    ...base.map((o) => ({ ...o, id: `${o.id}:free` })),
  ];

  test('n counts distinct models, not rows', () => {
    assert.equal(fitCalibration(withTwins)!.n, 40);
  });

  test('a twin-inflated group cannot masquerade as a large sample', () => {
    const single = linear(1, 0.11, -99 - 40, 0, 'one-model');
    const inflated = [
      ...linear(40, 0.11, -99, 1, 'ok'),
      ...single,
      ...single.map((o) => ({ ...o, id: `${o.id}:batch` })),
      ...single.map((o) => ({ ...o, id: `${o.id}:free` })),
    ];
    // Without dedupe this looks like a group of 3 and would be excluded on the
    // strength of one data point.
    assert.ok(!fitCalibration(inflated)!.excludedGroups.includes('one-model'));
  });

  test('leave-one-out is not leaked into by an identical twin', () => {
    // With twins present, holding out a point leaves its clone in the training
    // set, so the reported error comes out optimistically small.
    const honest = fitCalibration(base)!.looRmse;
    const leaked = fitCalibration(withTwins)!.looRmse;
    assert.ok(Math.abs(honest - leaked) < 1e-9, `dedupe must make LOO identical: ${honest} vs ${leaked}`);
  });
});

describe('an exclusion must be statistically supported, not just large', () => {
  test('a large but scattered group bias does NOT exclude', () => {
    // mean is past the threshold, but the standard error is wide enough that
    // the bias is not established.
    const scattered = [
      ...linear(40, 0.11, -99, 1, 'ok'),
      { id: 'n1', identity: 'n1', group: 'noisy', x: 1100, y: 0.11 * 1100 - 99 - 30 },
      { id: 'n2', identity: 'n2', group: 'noisy', x: 1100, y: 0.11 * 1100 - 99 + 12 },
      { id: 'n3', identity: 'n3', group: 'noisy', x: 1100, y: 0.11 * 1100 - 99 - 15 },
    ];
    const c = fitCalibration(scattered)!;
    assert.ok(Math.abs(c.groupBias['noisy'].bias) > 10, 'precondition: the raw mean is past the threshold');
    assert.ok(!c.excludedGroups.includes('noisy'), 'a wide standard error must block the exclusion');
  });

  test('a large and tight group bias DOES exclude', () => {
    // The offset has to clear the threshold *after* the fit has been pulled
    // toward the biased group — residuals are measured against a line that
    // includes it, which shrinks the apparent bias. That makes exclusion
    // harder, not easier, which is the conservative direction: an offset of 14
    // shows up as only -9.9 and is correctly left alone.
    const tight = [
      ...linear(40, 0.11, -99, 1, 'ok'),
      ...linear(5, 0.11, -99 - 18, 0.5, 'consistent'),
    ];
    const c = fitCalibration(tight)!;
    assert.ok(c.groupBias['consistent'].bias < -10);
    assert.deepEqual(c.excludedGroups, ['consistent']);
  });

  test('a tight bias just under the threshold is left alone', () => {
    const mild = [...linear(40, 0.11, -99, 1, 'ok'), ...linear(5, 0.11, -99 - 14, 0.5, 'consistent')];
    assert.deepEqual(fitCalibration(mild)!.excludedGroups, []);
  });
});

describe('an excluded group must not receive a calibrated value', () => {
  const withBias = [
    ...linear(40, 0.11, -99, 1, 'ok'),
    ...linear(5, 0.11, -99 - 18, 0.5, 'skewed'),
  ];
  const c = fitCalibration(withBias)!;

  test('precondition: the group is excluded from the fit', () => {
    assert.deepEqual(c.excludedGroups, ['skewed']);
  });

  test('applying it to that group returns nothing, not a number', () => {
    assert.equal(applyCalibration(c, 1100, 'skewed'), null);
  });

  test('a represented group still gets a value', () => {
    assert.ok(applyCalibration(c, 1100, 'ok'));
  });

  test('uncertainty is smaller for a represented group than an unseen one', () => {
    const known = applyCalibration(c, 1100, 'ok')!;
    const unseen = applyCalibration(c, 1100, 'brand-new-vendor')!;
    assert.equal(known.uncertainty, c.looRmse);
    assert.equal(unseen.uncertainty, c.vendorHoldoutRmse);
    assert.ok(unseen.uncertainty >= known.uncertainty,
      'an unseen vendor must never be reported as more certain than a known one');
  });
});
