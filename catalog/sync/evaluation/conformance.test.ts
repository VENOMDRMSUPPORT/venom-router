import assert from 'node:assert/strict';
import { describe, test } from 'node:test';
import { assessConformance, type ConformanceRun } from './conformance.ts';

const run = (key: string): ConformanceRun => ({
  independentRunKey: key, identityScore: 80, offerScore: 68,
  identityInterval95: { lower: 78, upper: 82 }, offerInterval95: { lower: 66, upper: 70 },
  contractBreak: false, runId: Number(key),
});

describe('provider conformance guard', () => {
  test('one divergent run is provisional and two independent runs create an override', () => {
    assert.equal(assessConformance([run('1')]).state, 'provisional');
    assert.equal(assessConformance([run('1'), run('2')]).state, 'override');
  });

  test('an operational contract break creates an override immediately', () => {
    assert.equal(assessConformance([{ ...run('1'), contractBreak: true }]).state, 'override');
  });

  test('overlapping intervals never create a divergence override', () => {
    const close = { ...run('1'), offerScore: 79, offerInterval95: { lower: 76, upper: 79 } };
    assert.equal(assessConformance([close, { ...close, independentRunKey: '2', runId: 2 }]).state, 'conformant');
  });
});
