import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { rankByVQ, orderingIsHonest, sortContract } from './ranking.ts';
import type { ScoredModel } from './ranking.ts';
import type { VQ, VO } from './venom-score.ts';

const vq = (value: number | null, uncertainty: number | null, level: VQ['level']): VQ => ({
  kind: 'VQ', value, uncertainty, bound: null, level,
  source: null, sourceModelId: null, identityRule: null,
  rawValue: null, rawField: null, transformation: null,
  precision: level === 'measured' ? 1 : 0,
});
const vo = (value: number): VO => ({
  kind: 'VO', value, dimensions: { context: value, output: value, capabilities: value, cost: value },
  missing: [], profileId: 'balanced',
});
const m = (modelId: string, q: VQ, o = vo(50)): ScoredModel => ({ providerId: 'p', modelId, vq: q, vo: o });

describe('a quality ranking uses quality evidence only', () => {
  test('a perfect VO cannot lift a model with no VQ into the ranking', () => {
    const cheapLongContext = m('cheap', vq(null, null, 'unrated'), vo(100));
    const measuredMediocre = m('measured', vq(20, 0.05, 'measured'), vo(1));
    const { ranked, unplaced } = rankByVQ([cheapLongContext, measuredMediocre]);
    assert.deepEqual(ranked.map((g) => g.members.map((x) => x.modelId)), [['measured']]);
    assert.deepEqual(unplaced.map((x) => x.modelId), ['cheap']);
  });

  test('an unrated model never outranks a measured one, whatever its VO', () => {
    const { ranked } = rankByVQ([
      m('unrated-but-perfect-vo', vq(null, null, 'unrated'), vo(100)),
      m('measured-low', vq(5, 0.05, 'measured'), vo(0)),
    ]);
    assert.equal(ranked[0].members[0].modelId, 'measured-low');
  });

  test('unplaced models are a separate section, not the tail of the ranking', () => {
    // Appending them would read as "measured, and found worst".
    const { ranked, unplaced } = rankByVQ([
      m('a', vq(60, 0.05, 'measured')),
      m('b', vq(null, null, 'unrated')),
    ]);
    assert.equal(ranked.flatMap((g) => g.members).length, 1);
    assert.equal(unplaced.length, 1);
  });
});

describe('overlapping uncertainty is a tie, not an ordering', () => {
  test('40 +/-5.66 and 43 +/-5.66 are tied', () => {
    const { ranked } = rankByVQ([
      m('A', vq(40, 5.66, 'calibrated')),
      m('B', vq(43, 5.66, 'calibrated')),
    ]);
    assert.equal(ranked.length, 1, 'the evidence cannot separate these');
    assert.equal(ranked[0].members.length, 2);
    assert.equal(ranked[0].tiedByUncertainty, true);
  });

  test('40 +/-5.66 and 60 +/-5.66 are genuinely ordered', () => {
    const { ranked } = rankByVQ([
      m('A', vq(40, 5.66, 'calibrated')),
      m('B', vq(60, 5.66, 'calibrated')),
    ]);
    assert.equal(ranked.length, 2);
    assert.equal(ranked[0].members[0].modelId, 'B');
  });

  test('two measured values a point apart ARE ordered — small error, real gap', () => {
    const { ranked } = rankByVQ([
      m('A', vq(62.1, 0.05, 'measured')),
      m('B', vq(63.1, 0.05, 'measured')),
    ]);
    assert.equal(ranked.length, 2);
  });

  test('a measured and a calibrated value tie when the wider interval swallows the gap', () => {
    const { ranked } = rankByVQ([
      m('measured', vq(45, 0.05, 'measured')),
      m('calibrated', vq(48, 5.66, 'calibrated')),
    ]);
    assert.equal(ranked.length, 1);
  });

  test('a tie group of identical values is not flagged as uncertainty-tied', () => {
    const { ranked } = rankByVQ([
      m('A', vq(40, 5.66, 'calibrated')),
      m('B', vq(40, 5.66, 'calibrated')),
    ]);
    assert.equal(ranked[0].tiedByUncertainty, false);
  });
});

describe('the sort contract is explicit and unblended', () => {
  test('a VQ sort names only the VQ field', () => {
    assert.equal(sortContract('vq').field, 'vq.value');
  });

  test('a VO sort names only the VO field', () => {
    assert.equal(sortContract('vo').field, 'vo.value');
  });

  test('each key carries its own tie rule', () => {
    assert.notEqual(sortContract('vq').tieRule, sortContract('vo').tieRule);
  });

  test('unplaced rows are labelled rather than silently ranked', () => {
    assert.match(sortContract('vq').unplacedLabel, /no quality evidence/i);
  });
});

describe('orderingIsHonest', () => {
  test('rejects a list that interleaves an unrated row above a rated one', () => {
    assert.equal(orderingIsHonest([
      m('unrated', vq(null, null, 'unrated')),
      m('rated', vq(50, 0.05, 'measured')),
    ]), false);
  });

  test('accepts rated rows followed by unrated ones', () => {
    assert.equal(orderingIsHonest([
      m('rated', vq(50, 0.05, 'measured')),
      m('unrated', vq(null, null, 'unrated')),
    ]), true);
  });
});
