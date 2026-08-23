/**
 * What a sync does about the models it just discovered.
 *
 * A roster addition used to be the end of the story: the offer appeared, and a
 * human had to notice it and click Evaluate. The gap was measured, not guessed —
 * `deepseek-v4-flash-vision-exp` was added at 2026-08-22T14:36 and first scored
 * at 2026-08-23T04:50, fourteen hours later, by hand.
 *
 * This closes it, under a budget. Evaluation spends real provider requests, so
 * the decision this module encodes is not "measure everything" but "measure as
 * much as one run is allowed to buy, and say out loud what it could not".
 *
 * The budget is denominated in requests rather than currency deliberately: that
 * is the unit `planEvaluation` already computes and the unit the Evaluate modal
 * already shows an owner before a click. A second cost vocabulary would be a
 * second thing to keep in agreement with the first.
 *
 * Pure with respect to the network: it plans and enqueues. Nothing here contacts
 * a provider.
 */
import type { EvaluationPlan } from '../sync/evaluation/plan.ts';
import type { EvaluationRunner } from './evaluation-runner.ts';

export interface AutoEvaluationConfig {
  enabled: boolean;
  /**
   * Ceiling on provider requests one sync run may commit to.
   *
   * A brand-new identity with every dimension unmeasured plans 6 dimensions at
   * 63 requests plus a 23-request speed run — 401. The default therefore buys
   * roughly three new identities per run and refuses the fourth rather than
   * discovering the bill afterwards.
   */
  maxRequestsPerRun: number;
}

export const DEFAULT_MAX_REQUESTS_PER_RUN = 1_200;
export const MAX_REQUESTS_CEILING = 20_000;

export function autoEvaluationConfig(env: NodeJS.ProcessEnv = process.env): AutoEvaluationConfig {
  const raw = Number(env.CATALOG_AUTO_EVALUATION_MAX_REQUESTS ?? DEFAULT_MAX_REQUESTS_PER_RUN);
  const maxRequestsPerRun = Number.isFinite(raw)
    ? Math.min(Math.max(Math.trunc(raw), 0), MAX_REQUESTS_CEILING)
    : DEFAULT_MAX_REQUESTS_PER_RUN;
  return {
    // Opt-out rather than opt-in: an owner who wants automatic measurement
    // should not have to discover a flag, and a budget of 0 is the honest way
    // to turn the spending off without turning the reporting off with it.
    enabled: env.CATALOG_AUTO_EVALUATION !== 'false',
    maxRequestsPerRun,
  };
}

export interface AutoEvaluationOffer {
  providerId: string;
  modelId: string;
}

/** Why one discovered offer was not enqueued. Every case is named; none is silent. */
export type AutoEvaluationSkipReason =
  | NonNullable<EvaluationPlan['blocked']>
  | 'already_covered'
  | 'already_queued'
  | 'over_budget';

export interface AutoEvaluationReport {
  enabled: boolean;
  maxRequestsPerRun: number;
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
 * Plan every newly discovered offer, then buy what the budget covers.
 *
 * Cheapest first. A budget spent on the single most expensive candidate leaves
 * the other five unmeasured; spent cheapest-first it buys the most coverage the
 * same money can. The ordering is stable — equal estimates fall back to the
 * offer's identity — so two runs over the same roster make the same choices.
 */
export function autoEvaluate(
  deps: AutoEvaluationDeps,
  offers: AutoEvaluationOffer[],
): AutoEvaluationReport {
  const config = deps.config ?? autoEvaluationConfig();
  const log = deps.log ?? ((message: string) => console.log(`[auto-evaluation] ${message}`));
  const report = EMPTY_REPORT(config);
  if (!config.enabled || offers.length === 0) return report;

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
      // Not a failure: quality is measured per identity, so an offer of an
      // already-measured model arrives fully covered and costs nothing.
      report.skipped.push({ ...entry.offer, reason: 'already_covered', estimatedRequests: 0 });
      continue;
    }
    candidates.push(entry);
  }

  candidates.sort((a, b) =>
    a.plan.estimatedRequests - b.plan.estimatedRequests
    || `${a.offer.providerId}/${a.offer.modelId}`.localeCompare(`${b.offer.providerId}/${b.offer.modelId}`),
  );

  for (const { offer, plan } of candidates) {
    if (report.committedRequests + plan.estimatedRequests > config.maxRequestsPerRun) {
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

  // A cap that truncates coverage silently reads, afterwards, as "everything was
  // measured". Say what was bought and what was refused, every run.
  if (report.enqueued.length > 0) {
    log(`queued ${report.enqueued.length} evaluation(s) for ${report.committedRequests} of ${config.maxRequestsPerRun} budgeted requests`);
  }
  const deferred = report.skipped.filter((entry) => entry.reason === 'over_budget');
  if (deferred.length > 0) {
    log(`budget exhausted: ${deferred.length} offer(s) deferred to the next run — ${deferred.map((entry) => `${entry.providerId}/${entry.modelId} (${entry.estimatedRequests} requests)`).join(', ')}`);
  }
  const blocked = report.skipped.filter((entry) => entry.reason !== 'over_budget' && entry.reason !== 'already_covered');
  if (blocked.length > 0) {
    log(`not evaluable: ${blocked.map((entry) => `${entry.providerId}/${entry.modelId} (${entry.reason})`).join(', ')}`);
  }

  return report;
}
