/**
 * What a sync does about the models it can still measure.
 *
 * A roster addition used to be the end of the story: the offer appeared, and a
 * human had to notice it and click Evaluate. The gap was measured, not guessed —
 * `deepseek-v4-flash-vision-exp` was added at 2026-08-22T14:36 and first scored
 * at 2026-08-23T04:50, fourteen hours later, by hand.
 *
 * This sweeps EVERY active offer, not only the ones a run just discovered.
 * Restricting it to new arrivals left an already-published offer with an
 * unmeasured dimension unmeasured forever, because nothing ever asked about it
 * again — four such dimensions were sitting in the live catalog. The plan is the
 * filter: an offer with nothing left to measure costs nothing and reports
 * `already_covered`, so a sweep over a complete catalog is pure database reads.
 *
 * Coverage, not a target number. A dimension the offer does not support is
 * excluded from coverage rather than counted as unsatisfied, so "complete" means
 * every APPLICABLE dimension has a verdict. 22 of 38 identities in the live
 * catalog lack a `vision` or `structuredOutput` score for exactly that reason:
 * the models have no such capability, and testing one would produce a number
 * with no meaning.
 *
 * Pure with respect to the network: it plans and enqueues. Nothing here contacts
 * a provider.
 */
import type { Db } from '../db/index.ts';
import type { EvaluationPlan } from '../sync/evaluation/plan.ts';
import type { EvaluationRunner } from './evaluation-runner.ts';

export interface AutoEvaluationConfig {
  enabled: boolean;
  /**
   * Ceiling on provider requests one sync run may commit to, or `null` for none.
   *
   * `null` is the default, by owner instruction: the goal is a complete catalog,
   * and a ceiling that stops short of it just defers the same spend to the next
   * run. A number still caps it for anyone who wants one — 0 spends nothing
   * while leaving the reporting intact.
   *
   * For scale: a brand-new identity with every dimension unmeasured plans 6
   * dimensions at 63 requests plus a 23-request speed run, so 401.
   */
  maxRequestsPerRun: number | null;
  /**
   * How long to leave an identity alone after measuring it.
   *
   * The anti-runaway guard, and it is not hypothetical. `x-ai/grok-4.5`/vision
   * was attempted on 2026-08-23 at 09:45 and returned
   * `insufficient_evidence: incomplete_valid_scenarios`;
   * `xiaomi/mimo-v2.5-pro`/vision did the same at 10:21. Those plans stay
   * incomplete, so an uncapped sweep on a six-hourly schedule would re-buy them
   * four times a day, forever, for a verdict that asking again immediately does
   * not produce. A dimension that failed gets one more chance per window, not
   * one per sync.
   */
  retryCooldownMs: number;
}

export const DEFAULT_RETRY_COOLDOWN_HOURS = 24;
export const MAX_REQUESTS_CEILING = 1_000_000;

function positiveInt(raw: string | undefined, fallback: number, ceiling: number): number {
  const value = Number(raw ?? fallback);
  return Number.isFinite(value) ? Math.min(Math.max(Math.trunc(value), 0), ceiling) : fallback;
}

export function autoEvaluationConfig(env: NodeJS.ProcessEnv = process.env): AutoEvaluationConfig {
  const rawMax = env.CATALOG_AUTO_EVALUATION_MAX_REQUESTS?.trim();
  return {
    // Opt-out rather than opt-in: an owner who wants automatic measurement
    // should not have to discover a flag.
    enabled: env.CATALOG_AUTO_EVALUATION !== 'false',
    // Absent means no ceiling. A number that does not parse is treated as absent
    // too — guessing a cap from a typo would silently stop short of a complete
    // catalog, which is the one outcome this is supposed to prevent.
    maxRequestsPerRun: rawMax === undefined || rawMax === '' || !Number.isFinite(Number(rawMax))
      ? null
      : Math.min(Math.max(Math.trunc(Number(rawMax)), 0), MAX_REQUESTS_CEILING),
    retryCooldownMs: positiveInt(
      env.CATALOG_AUTO_EVALUATION_RETRY_HOURS, DEFAULT_RETRY_COOLDOWN_HOURS, 24 * 365,
    ) * 60 * 60 * 1000,
  };
}

/**
 * The most recent measurement attempt per identity, for the retry cooldown.
 *
 * Reads `evaluation_runs`, which records every attempt with its outcome — so the
 * cooldown is anchored to what the service actually did, not to a counter this
 * module would have to keep in agreement with it.
 */
export function lastAttemptReader(db: Db): (identityId: string) => string | null {
  const statement = db.prepare(
    `SELECT MAX(COALESCE(finished_at, started_at)) at FROM evaluation_runs WHERE identity_id = ?`,
  );
  return (identityId: string) => {
    const row = statement.get(identityId) as unknown as { at: string | null } | undefined;
    return row?.at ?? null;
  };
}

export interface AutoEvaluationOffer {
  providerId: string;
  modelId: string;
}

/** Why one offer was not enqueued. Every case is named; none is silent. */
export type AutoEvaluationSkipReason =
  | NonNullable<EvaluationPlan['blocked']>
  | 'already_covered'
  | 'already_queued'
  | 'retry_cooldown'
  | 'over_budget';

export interface AutoEvaluationReport {
  enabled: boolean;
  maxRequestsPerRun: number | null;
  /** Requests committed by the jobs this run actually queued. */
  committedRequests: number;
  enqueued: Array<AutoEvaluationOffer & { estimatedRequests: number }>;
  skipped: Array<AutoEvaluationOffer & { reason: AutoEvaluationSkipReason; estimatedRequests: number }>;
}

export interface AutoEvaluationDeps {
  /**
   * The queue, used for BOTH planning and enqueueing.
   *
   * Planning through the runner rather than calling `planEvaluation` here is
   * what keeps one credential view: a separate call would not carry the
   * runner's injected `hasCredential`, so this module could refuse an offer the
   * queue would have accepted, or budget for one the queue then rejects.
   */
  evaluations: Pick<EvaluationRunner, 'enqueue' | 'plan'>;
  config?: AutoEvaluationConfig;
  /**
   * When this identity was last measured, or null if never.
   *
   * Injected rather than read here so this stays testable without a database,
   * and so the cooldown can be exercised directly. Omitted means no cooldown —
   * a caller that cannot answer the question must not have its offers silently
   * held back by a guard it did not opt into.
   */
  lastAttemptAt?: (identityId: string) => string | null;
  /** Injectable clock; the cooldown is the only thing that reads it. */
  now?: () => Date;
  /** Injected so a test asserts on what was reported rather than on stdout. */
  log?: (message: string) => void;
}

const EMPTY_REPORT = (config: AutoEvaluationConfig): AutoEvaluationReport => ({
  enabled: config.enabled,
  maxRequestsPerRun: config.maxRequestsPerRun,
  committedRequests: 0,
  enqueued: [],
  skipped: [],
});

/**
 * Plan every offer, then buy everything the budget allows.
 *
 * Cheapest first. With no ceiling the order does not change what gets bought,
 * but it changes what a ceiling buys when one is set, and it decides which jobs
 * reach the provider first — so a run interrupted halfway has still closed as
 * many dimensions as it could. The ordering is stable, equal estimates falling
 * back to the offer's identity, so two runs over the same roster make the same
 * choices.
 */
export function autoEvaluate(
  deps: AutoEvaluationDeps,
  offers: AutoEvaluationOffer[],
): AutoEvaluationReport {
  const config = deps.config ?? autoEvaluationConfig();
  const log = deps.log ?? ((message: string) => console.log(`[auto-evaluation] ${message}`));
  const report = EMPTY_REPORT(config);
  if (!config.enabled || offers.length === 0) return report;

  const now = (deps.now?.() ?? new Date()).getTime();
  /**
   * True when this identity was measured too recently to ask again.
   *
   * Keyed on the identity, not the offer, because that is what the evidence
   * belongs to: two sellers of one model share both the measurement and the
   * reason it is not worth re-buying yet.
   */
  const cooledDown = (identityId: string | null): boolean => {
    if (!deps.lastAttemptAt || identityId === null || config.retryCooldownMs <= 0) return false;
    const last = deps.lastAttemptAt(identityId);
    if (last === null) return false;
    const at = Date.parse(last);
    return Number.isFinite(at) && now - at < config.retryCooldownMs;
  };

  const planned = offers.map((offer) => ({
    offer,
    plan: deps.evaluations.plan(offer.providerId, offer.modelId),
  }));

  const candidates: Array<{ offer: AutoEvaluationOffer; plan: EvaluationPlan }> = [];
  for (const entry of planned) {
    if (entry.plan.blocked) {
      // Fail closed with the typed reason. Nothing was sent to a provider, and
      // the reason is the same one the manual route would have reported.
      report.skipped.push({ ...entry.offer, reason: entry.plan.blocked, estimatedRequests: 0 });
      continue;
    }
    if (entry.plan.estimatedRequests === 0) {
      // Not a failure, and the common case for a sweep: quality is measured per
      // identity, so a complete offer costs nothing to look at.
      report.skipped.push({ ...entry.offer, reason: 'already_covered', estimatedRequests: 0 });
      continue;
    }
    if (cooledDown(entry.plan.identityId)) {
      report.skipped.push({ ...entry.offer, reason: 'retry_cooldown', estimatedRequests: entry.plan.estimatedRequests });
      continue;
    }
    candidates.push(entry);
  }

  candidates.sort((a, b) =>
    a.plan.estimatedRequests - b.plan.estimatedRequests
    || `${a.offer.providerId}/${a.offer.modelId}`.localeCompare(`${b.offer.providerId}/${b.offer.modelId}`),
  );

  for (const { offer, plan } of candidates) {
    if (config.maxRequestsPerRun !== null
      && report.committedRequests + plan.estimatedRequests > config.maxRequestsPerRun) {
      report.skipped.push({ ...offer, reason: 'over_budget', estimatedRequests: plan.estimatedRequests });
      continue;
    }
    const outcome = deps.evaluations.enqueue(offer.providerId, offer.modelId);
    if (!outcome.accepted) {
      report.skipped.push({
        ...offer,
        reason: outcome.reason === 'already_queued' ? 'already_queued' : (outcome.plan.blocked ?? 'already_queued'),
        estimatedRequests: plan.estimatedRequests,
      });
      continue;
    }
    report.committedRequests += plan.estimatedRequests;
    report.enqueued.push({ ...offer, estimatedRequests: plan.estimatedRequests });
  }

  // Every outcome is reported. A cap that truncates coverage silently, or a
  // cooldown that quietly holds something back, both read afterwards as
  // "everything was measured".
  if (report.enqueued.length > 0) {
    const ceiling = config.maxRequestsPerRun === null ? 'no ceiling' : `a ceiling of ${config.maxRequestsPerRun}`;
    log(`queued ${report.enqueued.length} evaluation(s) for ${report.committedRequests} requests against ${ceiling}`);
  }
  const reasonCounts = new Map<AutoEvaluationSkipReason, number>();
  for (const entry of report.skipped) reasonCounts.set(entry.reason, (reasonCounts.get(entry.reason) ?? 0) + 1);

  const named = (reason: AutoEvaluationSkipReason) => report.skipped
    .filter((entry) => entry.reason === reason)
    .map((entry) => `${entry.providerId}/${entry.modelId}`)
    .join(', ');

  const deferred = reasonCounts.get('over_budget') ?? 0;
  if (deferred > 0) log(`budget exhausted: ${deferred} offer(s) deferred to the next run — ${named('over_budget')}`);

  const waiting = reasonCounts.get('retry_cooldown') ?? 0;
  if (waiting > 0) {
    log(`cooling down: ${waiting} offer(s) measured too recently to re-buy — ${named('retry_cooldown')}`);
  }

  for (const reason of reasonCounts.keys()) {
    if (reason === 'over_budget' || reason === 'retry_cooldown' || reason === 'already_covered') continue;
    log(`not evaluable (${reason}): ${named(reason)}`);
  }

  const covered = reasonCounts.get('already_covered') ?? 0;
  if (covered > 0 && report.enqueued.length === 0 && report.skipped.length === covered) {
    log(`nothing to measure: all ${covered} offer(s) already cover every applicable dimension`);
  }

  return report;
}
