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

  test('a declared-free model with no per-token figure still scores cheapest — not missing, not N/A', () => {
    // Ollama Cloud: billingKind is free (declared policy), but no per-token price
    // is published. That absence is NOT a gap and NOT a subscription — it is $0
    // at point of use, so the cost dimension is scored as the cheapest, exactly
    // like a feed-published zero, without any fabricated number.
    const vo = computeVO({ costOutputPerM: null, billingKind: 'free' }, pop, profile);
    assert.ok(vo.dimensions.cost! > 80, `declared-free scored ${vo.dimensions.cost}`);
    assert.ok(!vo.missing.includes('cost'));
    assert.ok(!vo.notApplicable.includes('cost'));
  });

  test('missing dimensions are declared, and weights renormalise over the rest', () => {
    const full = computeVO(
      { contextTokens: 1000000, maxOutputTokens: 128000, tools: true, reasoning: true, inputModalities: ['text'], costOutputPerM: 0 },
      pop, profile,
    );
    const partial = computeVO({ contextTokens: 1000000 }, pop, profile);
    assert.deepEqual(partial.missing.sort(), ['capabilities', 'cost', 'output']);
    assert.equal(full.missing.length, 0);
    assert.ok(partial.value !== null && partial.value > 0, 'a partial score still produces a value from what is known');
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

describe('VO withholds a value when nothing is published', () => {
  const profile: ScoreProfile = {
    id: 'balanced', label: 'Balanced',
    weights: { context: 0.3, output: 0.2, capabilities: 0.3, cost: 0.2 },
  };
  const pop = { context: [Math.log(8000)], output: [Math.log(4000)], cost: [0, 5] };

  test('a model with no operational facts scores null, not zero', () => {
    // Zero would sort as "worst operational fit"; the truth is "not published".
    const vo = computeVO({}, pop, profile);
    assert.equal(vo.value, null);
    assert.equal(vo.missing.length, 4);
  });

  test('one known dimension is still enough for a value', () => {
    assert.ok(computeVO({ contextTokens: 128_000 }, pop, profile).value !== null);
  });
});

describe('a subscription cost is a known semantic, not a gap', () => {
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
  const facts = { contextTokens: 128_000, maxOutputTokens: 32_000, tools: true };

  test('an included model reports cost as not applicable, not as missing', () => {
    // The provider charges a subscription. There is no per-token price to know,
    // so calling it a data gap misreports a fact we hold as one we lack.
    const vo = computeVO({ ...facts, billingKind: 'included' }, pop, profile);
    assert.deepEqual(vo.notApplicable, ['cost']);
    assert.ok(!vo.missing.includes('cost'), 'included must not be counted as missing');
    assert.equal(vo.dimensions.cost, null, 'and it must still not carry a number');
  });

  test('an included model is never scored as if it were free', () => {
    // The failure this guards: treating "covered by the plan" as $0 would hand
    // every subscription model the best possible cost score.
    const included = computeVO({ ...facts, billingKind: 'included' }, pop, profile);
    const free = computeVO({ ...facts, billingKind: 'free', costOutputPerM: 0 }, pop, profile);
    assert.equal(included.dimensions.cost, null);
    assert.ok(free.dimensions.cost! > 80);
    assert.notEqual(included.value, free.value);
  });

  test('the remaining weights renormalise, so a subscription is not penalised', () => {
    // Excluding a dimension must not drag the score down. The same facts with
    // cost genuinely unknown produce the same value — what differs is only how
    // the absence is REPORTED.
    const included = computeVO({ ...facts, billingKind: 'included' }, pop, profile);
    const threeDims = computeVO({ ...facts, billingKind: 'unknown' }, pop, profile);
    assert.equal(included.value, threeDims.value);
  });

  test('an unknown billing kind still reports cost as missing — that IS a gap', () => {
    const vo = computeVO({ ...facts, billingKind: 'unknown' }, pop, profile);
    assert.ok(vo.missing.includes('cost'));
    assert.deepEqual(vo.notApplicable, []);
  });

  test('a per-token model with a published price has neither state', () => {
    const vo = computeVO({ ...facts, billingKind: 'per_token', costOutputPerM: 5 }, pop, profile);
    assert.deepEqual(vo.notApplicable, []);
    assert.ok(!vo.missing.includes('cost'));
    assert.ok(vo.dimensions.cost !== null);
  });
});

describe('an unrated VQ records WHY it is unrated', () => {
  test('an unresolved identity says so', () => {
    // The distinction that matters operationally: this one can never be fixed by
    // gathering more benchmarks, only by resolving the identity.
    const vq = computeVQ(UNRESOLVED, {}, CAL);
    assert.equal(vq.level, 'unrated');
    assert.equal(vq.unratedReason, 'identity_unresolved');
  });

  test('an ambiguous identity is distinguished from an unresolved one', () => {
    // Ambiguous means candidates exist and a human must choose; unresolved means
    // nothing matched at all. Different queues, different work.
    assert.equal(computeVQ(AMBIGUOUS, {}, CAL).unratedReason, 'identity_ambiguous');
  });

  test('a resolved identity nobody benchmarked says THAT instead', () => {
    // The six OpenAI codex/pro rows: the upstream record exists and every
    // benchmark field is empty. More identity work would not help.
    const vq = computeVQ(RESOLVED, {}, CAL);
    assert.equal(vq.level, 'unrated');
    assert.equal(vq.unratedReason, 'no_published_benchmark');
  });

  test('a vendor the calibration was measured to be useless for says that', () => {
    // mistral-large-3: excluded from the fit because a held-out mistralai scores
    // RMSE 15.49 against a natural spread of 14.06 — no predictive power. An Elo
    // exists; applying the calibration to it would publish a number the evidence
    // says is wrong.
    const vq = computeVQ(
      { status: 'resolved', rule: 'exact', target: 'mistralai/x', candidates: ['mistralai/x'] },
      { calibratable: 1200, group: 'mistralai' },
      { ...CAL!, excludedGroups: ['mistralai'] },
    );
    assert.equal(vq.level, 'unrated');
    assert.equal(vq.unratedReason, 'calibration_group_excluded');
  });

  test('a rated VQ carries no reason', () => {
    assert.equal(computeVQ(RESOLVED, { direct: 60 }, CAL).unratedReason, null);
  });
});

describe('a reviewed bound puts a figure on a row no index has measured', () => {
  const BOUND = { value: 52.6, side: 'lower' as const, reason: "Z-AI's stated successor to glm-5.2, which is measured at 52.6" };

  test('it survives an unresolved identity, which is the only case it exists for', () => {
    // The identity guard above returns unrated before anything else is read,
    // and that is right for a DERIVED figure: a measurement attached to the
    // wrong model is indistinguishable from a right one. A reviewed bound is
    // not derived — it is a human's claim about this row, made in full view of
    // the fact that the index does not carry the model. `cline-pass/glm-5.3` is
    // the case: no benchmark index lists GLM-5.3 at all.
    const vq = computeVQ(UNRESOLVED, { bound: BOUND }, CAL);

    assert.equal(vq.value, 52.6);
    assert.equal(vq.bound, 'lower');
    assert.equal(vq.level, 'bounded');
    assert.equal(vq.unratedReason, null);
  });

  test('it never becomes an identity', () => {
    // `read-model.ts` derives `canonicalId` from `sourceModelId`. Naming the
    // reference model there would make the row assert that it IS glm-5.2 —
    // the exact bind the bound exists to avoid having to make. The reference
    // belongs in the reason, where a reader sees it as a relation.
    const vq = computeVQ(UNRESOLVED, { bound: BOUND }, CAL);

    assert.equal(vq.sourceModelId, null);
    assert.equal(vq.identityRule, null);
    assert.match(vq.source!, /relation/);
  });

  test('a measured figure still outranks it', () => {
    // Order matters: a bound is the last resort, not a shortcut past evidence.
    const vq = computeVQ(RESOLVED, { direct: 61.4, bound: BOUND }, CAL);

    assert.equal(vq.value, 61.4);
    assert.equal(vq.level, 'measured');
  });

  test('without a bound an unresolved identity is still unrated', () => {
    assert.equal(computeVQ(UNRESOLVED, {}, CAL).level, 'unrated');
    assert.equal(computeVQ(UNRESOLVED, {}, CAL).unratedReason, 'identity_unresolved');
  });
});

describe('a bound is never rounded in the direction that strengthens it', () => {
  test('it keeps the precision of the figure it is derived from', () => {
    // Shipped defect, seen on the ClinePass page: glm-5.2 measured at 52.6
    // rendered "52.6", and glm-5.3's bound — the SAME 52.6, taken from that very
    // measurement — rendered "≥ 53". The same number, twice, differently, on
    // adjacent rows. The bounded branch hardcoded precision 0, a rule written
    // for calibrated values whose ±5-8 points cannot justify a decimal; a bound
    // copied from a measured figure carries that figure's precision.
    const vq = computeVQ(UNRESOLVED, { bound: { value: 52.6, side: 'lower', reason: 'successor to a model measured at 52.6' } }, CAL);

    assert.equal(vq.value, 52.6);
    assert.equal(vq.precision, 1, 'a bound of 52.6 must not be published as 53');
  });

  test('rounding up a lower bound would claim more than the evidence', () => {
    // The direction is the point. 52.6 -> "≥ 53" asserts at-least-53 where the
    // evidence supports at-least-52.6, so the display invents a point of
    // headroom. An upper bound rounded the other way would do the same in
    // reverse.
    assert.equal(formatVQ(computeVQ(UNRESOLVED, { bound: { value: 52.6, side: 'lower', reason: 'r' } }, CAL)), '≥ 52.6');
  });

  test('a whole-number bound stays whole', () => {
    const vq = computeVQ(UNRESOLVED, { bound: { value: 47, side: 'lower', reason: 'r' } }, CAL);
    assert.equal(vq.precision, 0);
    assert.equal(formatVQ(vq), '≥ 47');
  });
});
