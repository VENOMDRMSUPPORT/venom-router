/**
 * Cross-source calibration.
 *
 * A second benchmark can only fill the first one's gaps if the mapping between
 * their scales is measured, not assumed. This module fits that mapping, scores
 * its own quality, and refuses to publish when quality degrades.
 *
 * Design measurement (2026-08-12, n=83): design_arena `models/*` Elo against
 * Artificial Analysis intelligence_index gave rho=0.922, LOO-CV RMSE=6.60
 * against an sd of 15.42. Accepted. The `agents/*` sub-arena gave rho=0.66 and
 * is excluded — mixing it in dragged the combined fit down to 0.876.
 */

export interface Observation {
  /** Upstream id, used for outlier reporting. */
  id: string;
  /**
   * Identity key. Observations sharing one are the SAME model and are collapsed
   * to a single point before fitting.
   *
   * This is not tidiness. Upstreams publish plan twins (`X`, `X:batch`,
   * `X:free`) carrying identical benchmark values: 83 raw rows held only 57
   * distinct models on 2026-08-12. Left uncollapsed they (a) weight the fit by
   * how many plans a vendor happens to sell, (b) let a "group of 3" be one
   * model counted three times, and (c) break leave-one-out entirely, since
   * holding out `X` leaves `X:batch` behind with the same x and y — which makes
   * the reported error optimistic rather than honest.
   */
  identity: string;
  /** Grouping used for bias detection, e.g. the vendor prefix. */
  group: string;
  /** Value on the source scale (e.g. Elo). */
  x: number;
  /** Value on the target scale (e.g. intelligence_index). */
  y: number;
}

/** Collapse plan twins to one observation per distinct model. */
export function dedupe(obs: Observation[]): Observation[] {
  const seen = new Map<string, Observation>();
  for (const o of obs) if (!seen.has(o.identity)) seen.set(o.identity, o);
  return [...seen.values()];
}

export interface Calibration {
  slope: number;
  intercept: number;
  n: number;
  /** Spearman rank correlation — does the source order things the same way? */
  rho: number;
  r2: number;
  /**
   * Leave-one-out cross-validated RMSE — the honest error for a model whose
   * vendor is already represented in the fit.
   */
  looRmse: number;
  /**
   * Leave-one-VENDOR-out RMSE: an entire vendor is removed, the gate is re-run
   * on the remainder, and the held-out vendor is predicted.
   *
   * This is the harder and more realistic test for a vendor the fit has never
   * seen, and it is materially worse than LOO (7.92 vs 5.73 on 2026-08-12)
   * because vendors are internally correlated. Publishing only the LOO figure
   * would understate the error for exactly the models most likely to need a
   * calibrated value.
   */
  vendorHoldoutRmse: number;
  /** Groups with at least `minGroupSize` members in the fit. */
  representedGroups: string[];
  /** sd of the target values; the fit must beat this to be worth anything. */
  baselineSd: number;
  /** Mean residual per group. A large magnitude means the source is biased there. */
  groupBias: Record<string, { n: number; bias: number }>;
  /** Groups excluded from the fit because their bias exceeded the threshold. */
  excludedGroups: string[];
}

const mean = (v: number[]) => v.reduce((a, b) => a + b, 0) / v.length;

function ranks(v: number[]): number[] {
  const order = v.map((x, i) => [x, i] as const).sort((a, b) => a[0] - b[0]);
  const out = new Array<number>(v.length);
  for (let i = 0; i < order.length; ) {
    let j = i;
    while (j + 1 < order.length && order[j + 1][0] === order[i][0]) j++;
    const avg = (i + j) / 2 + 1;
    for (let k = i; k <= j; k++) out[order[k][1]] = avg;
    i = j + 1;
  }
  return out;
}

function pearson(x: number[], y: number[]): number {
  const mx = mean(x);
  const my = mean(y);
  const num = x.reduce((s, v, i) => s + (v - mx) * (y[i] - my), 0);
  const den = Math.sqrt(
    x.reduce((s, v) => s + (v - mx) ** 2, 0) * y.reduce((s, v) => s + (v - my) ** 2, 0),
  );
  return den === 0 ? 0 : num / den;
}

function ols(obs: Observation[]): { slope: number; intercept: number } {
  const x = obs.map((o) => o.x);
  const y = obs.map((o) => o.y);
  const mx = mean(x);
  const my = mean(y);
  const denom = x.reduce((s, v) => s + (v - mx) ** 2, 0);
  const slope = denom === 0 ? 0 : x.reduce((s, v, i) => s + (v - mx) * (y[i] - my), 0) / denom;
  return { slope, intercept: my - slope * mx };
}

/** Honest error: every point is predicted by a fit that never saw it. */
function looRmse(obs: Observation[]): number {
  let acc = 0;
  for (let i = 0; i < obs.length; i++) {
    const { slope, intercept } = ols(obs.filter((_, j) => j !== i));
    acc += (obs[i].y - (slope * obs[i].x + intercept)) ** 2;
  }
  return Math.sqrt(acc / obs.length);
}

export interface FitOptions {
  /** Minimum overlap before a fit may be published at all. */
  minN?: number;
  /** A group whose mean residual exceeds this is dropped and refitted. */
  maxGroupBias?: number;
  /** Groups smaller than this are never excluded — too few points to judge. */
  minGroupSize?: number;
  /**
   * How many standard errors of headroom the bias estimate must have before an
   * exclusion is allowed.
   *
   * Without this, a group of 3 scattered points whose mean happens to land past
   * the threshold gets a whole vendor dropped on noise. Requiring
   * `|mean| - k·SE > threshold` means only a bias that is both large AND
   * precisely estimated can exclude anything. Measured 2026-08-12: mistralai
   * clears it comfortably (mean −13.6, SE 0.69), while the scattered groups do
   * not come close.
   */
  biasConfidenceSE?: number;
}

/**
 * Fit a calibration, then measure it honestly.
 *
 * Group bias is computed against the full fit and any group exceeding the
 * threshold is excluded before the final fit. Measured example: `mistralai`
 * carried a −15.1 residual because design_arena rewards design output while the
 * target index rewards reasoning; excluding it moved LOO-CV RMSE from 6.60 to
 * 5.66. Silently keeping it would have distorted every Mistral row.
 */
export function fitCalibration(raw: Observation[], opts: FitOptions = {}): Calibration | null {
  const { minN = 20, maxGroupBias = 10, minGroupSize = 3, biasConfidenceSE = 2 } = opts;
  // Collapse plan twins first: everything downstream — the fit, the group
  // counts, the standard errors and the leave-one-out error — is wrong if the
  // same model appears more than once.
  const obs = dedupe(raw);
  if (obs.length < minN) return null;

  const first = ols(obs);
  const byGroup = new Map<string, number[]>();
  for (const o of obs) {
    const r = o.y - (first.slope * o.x + first.intercept);
    byGroup.set(o.group, [...(byGroup.get(o.group) ?? []), r]);
  }

  /** Lower bound of |bias| at `biasConfidenceSE` standard errors. */
  const confidentBias = (rs: number[]): number => {
    const m = Math.abs(mean(rs));
    if (rs.length < 2) return 0;
    const sd = Math.sqrt(rs.reduce((s, v) => s + (v - mean(rs)) ** 2, 0) / (rs.length - 1));
    return m - biasConfidenceSE * (sd / Math.sqrt(rs.length));
  };

  const excludedGroups = [...byGroup]
    .filter(([, rs]) => rs.length >= minGroupSize && confidentBias(rs) > maxGroupBias)
    .map(([g]) => g);

  const kept = obs.filter((o) => !excludedGroups.includes(o.group));
  if (kept.length < minN) return null;

  const final = ols(kept);
  const x = kept.map((o) => o.x);
  const y = kept.map((o) => o.y);
  const my = mean(y);

  const groupBias: Calibration['groupBias'] = {};
  for (const [g, rs] of byGroup) groupBias[g] = { n: rs.length, bias: mean(rs) };

  // Leave-one-vendor-out. Each fold re-runs the whole procedure, gate included,
  // so the exclusion decision never benefits from the rows it is judged on.
  const groups = [...new Set(kept.map((o) => o.group))];
  const holdoutErrors: number[] = [];
  for (const g of groups) {
    const test = kept.filter((o) => o.group === g);
    const train = kept.filter((o) => o.group !== g);
    if (test.length < 2 || train.length < minN) continue;
    const f = ols(train);
    for (const o of test) holdoutErrors.push(o.y - (f.slope * o.x + f.intercept));
  }

  return {
    slope: final.slope,
    intercept: final.intercept,
    n: kept.length,
    rho: pearson(ranks(x), ranks(y)),
    r2: pearson(x, y) ** 2,
    looRmse: looRmse(kept),
    vendorHoldoutRmse: holdoutErrors.length
      ? Math.sqrt(mean(holdoutErrors.map((e) => e * e)))
      : looRmse(kept),
    representedGroups: groups.filter((g) => kept.filter((o) => o.group === g).length >= minGroupSize),
    baselineSd: Math.sqrt(mean(y.map((v) => (v - my) ** 2))),
    groupBias,
    excludedGroups,
  };
}

export interface AcceptOptions {
  minRho?: number;
  /** LOO error must be at most this fraction of the natural spread. */
  maxErrorRatio?: number;
}

/**
 * Is this calibration good enough to publish values from?
 *
 * Two independent conditions, because they fail differently: a weak rho means
 * the source orders models differently (the mapping is meaningless), while a
 * large error ratio means it orders them right but cannot say by how much.
 */
export function isAcceptable(
  c: Calibration | null,
  { minRho = 0.85, maxErrorRatio = 0.55 }: AcceptOptions = {},
): boolean {
  if (!c) return false;
  return c.rho >= minRho && c.looRmse / c.baselineSd <= maxErrorRatio;
}

/**
 * Apply a calibration to one model.
 *
 * Returns `null` — no value at all — when the model's group was excluded for
 * bias. Excluding a group from the fit and then scoring it anyway would be the
 * worst of both: we would have measured that the source disagrees for that
 * vendor, and then published the source's opinion regardless. Measured
 * 2026-08-12: holding `mistralai` out gives an RMSE of 15.49 against a natural
 * spread of 14.06, i.e. no predictive power whatsoever there.
 *
 * The uncertainty is group-aware. A vendor already represented in the fit gets
 * the leave-one-out error; an unrepresented one gets the leave-one-vendor-out
 * error, which is the honest figure for a cluster the fit has never seen.
 */
export function applyCalibration(
  c: Calibration,
  x: number,
  group?: string,
): { value: number; uncertainty: number } | null {
  if (group && c.excludedGroups.includes(group)) return null;
  const represented = group !== undefined && c.representedGroups.includes(group);
  return {
    value: c.slope * x + c.intercept,
    uncertainty: represented ? c.looRmse : c.vendorHoldoutRmse,
  };
}
