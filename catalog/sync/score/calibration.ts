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
  /** Grouping used for bias detection, e.g. the vendor prefix. */
  group: string;
  /** Value on the source scale (e.g. Elo). */
  x: number;
  /** Value on the target scale (e.g. intelligence_index). */
  y: number;
}

export interface Calibration {
  slope: number;
  intercept: number;
  n: number;
  /** Spearman rank correlation — does the source order things the same way? */
  rho: number;
  r2: number;
  /** Leave-one-out cross-validated RMSE. This is the published uncertainty. */
  looRmse: number;
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
export function fitCalibration(obs: Observation[], opts: FitOptions = {}): Calibration | null {
  const { minN = 20, maxGroupBias = 10, minGroupSize = 3 } = opts;
  if (obs.length < minN) return null;

  const first = ols(obs);
  const byGroup = new Map<string, number[]>();
  for (const o of obs) {
    const r = o.y - (first.slope * o.x + first.intercept);
    byGroup.set(o.group, [...(byGroup.get(o.group) ?? []), r]);
  }

  const excludedGroups = [...byGroup]
    .filter(([, rs]) => rs.length >= minGroupSize && Math.abs(mean(rs)) > maxGroupBias)
    .map(([g]) => g);

  const kept = obs.filter((o) => !excludedGroups.includes(o.group));
  if (kept.length < minN) return null;

  const final = ols(kept);
  const x = kept.map((o) => o.x);
  const y = kept.map((o) => o.y);
  const my = mean(y);

  const groupBias: Calibration['groupBias'] = {};
  for (const [g, rs] of byGroup) groupBias[g] = { n: rs.length, bias: mean(rs) };

  return {
    slope: final.slope,
    intercept: final.intercept,
    n: kept.length,
    rho: pearson(ranks(x), ranks(y)),
    r2: pearson(x, y) ** 2,
    looRmse: looRmse(kept),
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

/** Apply a calibration. Returns the value and the uncertainty that goes with it. */
export function applyCalibration(c: Calibration, x: number): { value: number; uncertainty: number } {
  return { value: c.slope * x + c.intercept, uncertainty: c.looRmse };
}
