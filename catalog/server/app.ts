/**
 * HTTP routing, separated from the listener so tests can drive it without
 * opening a socket.
 */

import type { Db } from '../db/index.ts';
import { loadModels, loadProviders, loadMeta, loadProvenance, loadEvaluationDiagnostics, STALE_AFTER_HOURS } from './read-model.ts';
import { clampChangesLimit, loadChanges } from './changes.ts';
import type { SyncRunner, SchedulerHandle } from './sync-runner.ts';
import type { EvaluationRunner } from './evaluation-runner.ts';
import { planEvaluation, resolveIdentity } from '../sync/evaluation/plan.ts';
import { buildEvaluationFixtures, fixtureDigest } from '../sync/evaluation/fixtures.ts';
import { regradeFromRetainedResponses } from '../sync/evaluation/regrade.ts';
import { recalculatePublishedOffers } from '../sync/evaluation/recalculate.ts';
import {
  catalogNotificationSummary,
  clampNotificationLimit,
  listCatalogNotifications,
  markCatalogNotificationsRead,
  MAX_MARK_READ_IDS,
  reconcileCatalogNotifications,
} from './model-notifications.ts';
import { isDbError, listDatabaseTables, loadDatabaseSchema, runDatabaseQuery } from './database-browser.ts';

export interface AppDeps {
  db: Db;
  /** Optional file-backed read-only connection used by Database Browser queries. */
  readonlyDb?: Db;
  runner: SyncRunner;
  /**
   * Required, not optional: the service always owns an evaluation queue, and an
   * optional dependency would give the routes a silent "feature missing" state
   * that production can never actually be in.
   */
  evaluations: EvaluationRunner;
  scheduler?: SchedulerHandle;
  now?: () => Date;
  startedAt?: string;
}

/** The corpus the plan is measured against. Computed once; it never varies at runtime. */
const TEST_SET_HASH = fixtureDigest(buildEvaluationFixtures());

export interface HttpResult {
  status: number;
  body: unknown;
  headers?: Record<string, string>;
}

export type Handler = (url: URL, method: string) => HttpResult;

/**
 * Health, in two independent parts.
 *
 * `service` is "am I running and can I read my own data". `catalog` is "is what
 * I would serve actually current". Collapsing them would let a service with a
 * week-old catalog report healthy because its socket answered — which is the
 * failure this endpoint exists to make visible.
 */
/**
 * Health asks for exactly what it reads. Widening this to the full AppDeps
 * would make every caller build an evaluation queue to answer "are you up".
 */
export type HealthDeps = Pick<AppDeps, 'db' | 'runner' | 'scheduler' | 'now' | 'startedAt'>;

export function health(deps: HealthDeps): HttpResult {
  const { db, runner, scheduler } = deps;
  const now = deps.now?.() ?? new Date();

  let dbReadable = true;
  let liveModels = 0;
  try {
    liveModels = (db.prepare(`SELECT COUNT(*) n FROM models WHERE status IN ('active','missing')`).get() as { n: number }).n;
  } catch {
    dbReadable = false;
  }

  const models = dbReadable ? loadModels(db) : [];
  const providers = dbReadable ? loadProviders(db, models, now) : [];
  const meta = dbReadable ? loadMeta(db, models) : null;
  const stale = providers.filter((p) => p.freshness !== 'fresh');

  const serviceOk = dbReadable && liveModels > 0;
  const catalogOk = providers.length > 0 && stale.length === 0;

  return {
    // 503 when the catalog is not current: a caller that only checks the status
    // code must not be told everything is fine while serving week-old data.
    status: serviceOk && catalogOk ? 200 : serviceOk ? 503 : 500,
    body: {
      api: { contractVersion: 'catalog-api-v2' },
      service: {
        status: serviceOk ? 'up' : 'degraded',
        databaseReadable: dbReadable,
        startedAt: deps.startedAt ?? null,
        syncInFlight: runner.isRunning,
        currentRunStartedAt: runner.currentRunStartedAt,
        schedulerEnabled: Boolean(scheduler),
        nextScheduledRunAt: scheduler?.nextRunAt() ?? null,
      },
      catalog: {
        status: catalogOk ? 'current' : 'stale',
        liveModels,
        methodologyVersion: meta?.methodologyVersion ?? null,
        staleAfterHours: STALE_AFTER_HOURS,
        staleProviders: stale.map((p) => ({ id: p.id, freshness: p.freshness, lastSuccessfulSyncAt: p.lastSuccessfulSyncAt, lastOutcome: p.lastOutcome })),
        providers: providers.map((p) => ({
          id: p.id, liveModels: p.liveModels, freshness: p.freshness,
          lastSuccessfulSyncAt: p.lastSuccessfulSyncAt, lastAttemptedSyncAt: p.lastAttemptedSyncAt,
          lastOutcome: p.lastOutcome, hoursSinceSuccess: p.hoursSinceSuccess,
        })),
      },
      lastSync: runner.lastOutcome
        ? {
            startedAt: runner.lastOutcome.startedAt,
            finishedAt: runner.lastOutcome.finishedAt,
            aborted: runner.lastOutcome.aborted ?? null,
            providers: runner.lastOutcome.providers.map((p) => ({ provider: p.provider, outcome: p.outcome, error: p.error ?? null })),
          }
        : null,
    },
  };
}

/**
 * Bring the alert ledger in line with current health, from the one code path.
 *
 * Exported because two callers need it and must not drift: the `/v1/alerts`
 * route, and the service's own reconcile tick. Before the tick existed the
 * ledger only advanced when a browser polled — so with no tab open, no alert was
 * ever raised and no webhook was ever queued. Two copies of this three-line
 * sequence would be two definitions of what the ledger means.
 */
export function reconcileCatalogNotificationLedger(deps: HealthDeps, now: Date) {
  reconcileCatalogNotifications(deps.db, now.toISOString());
  return listCatalogNotifications(deps.db);
}

export function route(deps: AppDeps, url: URL, method: string, body?: unknown): HttpResult | Promise<HttpResult> {
  const { db } = deps;
  const now = deps.now?.() ?? new Date();
  const path = url.pathname.replace(/\/+$/, '') || '/';

  if (path === '/v1/health') return health(deps);

  if (path === '/v1/alerts' && method === 'GET') {
    return {
      status: 410,
      body: {
        error: 'The alerts contract was replaced by notification history.',
        replacement: '/v1/notifications',
        contractVersion: 'catalog-api-v2',
      },
    };
  }

  if (path === '/v1/providers' && method === 'GET') {
    const models = loadModels(db);
    return { status: 200, body: { providers: loadProviders(db, models, now), meta: loadMeta(db, models) } };
  }

  if (path === '/v1/models' && method === 'GET') {
    const includeRetired = url.searchParams.get('includeRetired') === 'true';
    const allModels = loadModels(db, { includeRetired });
    let models = allModels;
    const provider = url.searchParams.get('provider');
    if (provider) models = models.filter((m) => m.providerId === provider);
    const level = url.searchParams.get('evidence');
    if (level) models = models.filter((m) => m.vq.evidenceLevel === level);
    return { status: 200, body: { models, meta: loadMeta(db, allModels) } };
  }

  const provenance = /^\/v1\/models\/([^/]+)\/([^]+)\/provenance$/.exec(path);
  if (provenance && method === 'GET') {
    const detail = loadProvenance(db, decodeURIComponent(provenance[1]), decodeURIComponent(provenance[2]));
    return detail
      ? { status: 200, body: detail }
      : { status: 404, body: { error: 'no scored value for that model', providerId: provenance[1], modelId: provenance[2] } };
  }

  const evaluation = /^\/v1\/models\/([^/]+)\/([^]+)\/evaluation$/.exec(path);
  if (evaluation && method === 'GET') {
    const providerId = decodeURIComponent(evaluation[1]);
    const modelId = decodeURIComponent(evaluation[2]);
    const detail = loadEvaluationDiagnostics(db, providerId, modelId);
    // The plan travels with the diagnostics so the modal can show what a click
    // will spend without a second endpoint, and so the figure it shows is
    // produced by the same code that will execute it.
    return detail
      ? { status: 200, body: { ...detail, plan: planEvaluation(db, { providerId, modelId, testSetHash: TEST_SET_HASH }) } }
      : { status: 404, body: { error: 'model not found', providerId, modelId } };
  }

  if (path === '/v1/changes' && method === 'GET') {
    const since = url.searchParams.get('since') ?? undefined;
    const limit = clampChangesLimit(Number(url.searchParams.get('limit') ?? 500));
    return { status: 200, body: loadChanges(db, { since, limit }) };
  }

  if (path === '/v1/notifications' && method === 'GET') {
    reconcileCatalogNotifications(db, now.toISOString());
    const providerId = url.searchParams.get('provider') ?? undefined;
    const rawLimit = url.searchParams.has('limit') ? Number(url.searchParams.get('limit')) : undefined;
    const limit = clampNotificationLimit(rawLimit);
    const notifications = listCatalogNotifications(db, { providerId, limit });
    return {
      status: 200,
      body: {
        notifications,
        summary: catalogNotificationSummary(db, providerId),
        // The applied ceiling, reported so a caller can tell "this is all of it"
        // from "this is one page of it" without knowing the service's constants.
        limit,
        generatedAt: now.toISOString(),
      },
    };
  }

  if (path === '/v1/notifications/read' && method === 'PATCH') {
    const requested = (body ?? {}) as { ids?: unknown; provider?: unknown };
    if (requested.ids !== undefined && (!Array.isArray(requested.ids) || requested.ids.some((id) => typeof id !== 'string'))) {
      return { status: 400, body: { error: 'ids must be an array of notification ids', code: 'invalid_notification_ids' } };
    }
    if (Array.isArray(requested.ids) && requested.ids.length > MAX_MARK_READ_IDS) {
      return { status: 400, body: { error: `ids must contain at most ${MAX_MARK_READ_IDS} notification ids`, code: 'notification_ids_limit', limit: MAX_MARK_READ_IDS } };
    }
    if (requested.provider !== undefined && (typeof requested.provider !== 'string' || requested.provider.length === 0)) {
      return { status: 400, body: { error: 'provider must be a non-empty string when supplied' } };
    }
    const updated = markCatalogNotificationsRead(db,
      requested.ids === undefined ? null : requested.ids,
      { providerId: requested.provider, now: now.toISOString() });
    return { status: 200, body: { updated } };
  }

  if (path === '/v1/db/query' && method === 'POST') {
    const result = runDatabaseQuery({ db, readonlyDb: deps.readonlyDb }, (body ?? {}) as { sql?: unknown; limit?: unknown });
    if (isDbError(result)) return { status: 400, body: result };
    return { status: 200, body: result };
  }

  if (path === '/v1/db/tables' && method === 'GET') {
    const result = listDatabaseTables({ db: deps.readonlyDb ?? db });
    return isDbError(result) ? { status: 500, body: result } : { status: 200, body: result };
  }

  if (path === '/v1/db/schema' && method === 'GET') {
    const result = loadDatabaseSchema({ db: deps.readonlyDb ?? db }, url.searchParams.get('table'));
    if (!isDbError(result)) return { status: 200, body: result };
    return {
      status: result.code === 'table_not_found' ? 404 : result.code === 'schema_failed' ? 500 : 400,
      body: result,
    };
  }

  if (path === '/v1/sync' && method === 'POST') {
    return deps.runner.run().then((outcome) =>
      outcome === null
        ? {
            status: 409,
            body: { error: 'a sync is already running', startedAt: deps.runner.currentRunStartedAt },
          }
        : { status: 200, body: outcome },
    );
  }

  /**
   * Re-read one model's stored evidence with today's grader. Zero requests.
   *
   * This is deliberately NOT `force`: it buys nothing from a provider, so the
   * reason `POST /v1/evaluations` has no force flag — a real bill that should
   * stay deliberate — does not apply. What it does apply to is the other half
   * of the same problem, which was that the free repair existed only as a
   * terminal script guarded against running while the service is up. It was
   * therefore unavailable in exactly the situation where it is wanted.
   *
   * Refused while the runner is busy. Re-scoring a dimension a job is in the
   * middle of measuring would publish a number from half a run.
   */
  if (path === '/v1/evaluations/regrade') {
    if (method !== 'POST') return { status: 405, body: { error: 'method not allowed', path } };
    const input = (body ?? {}) as { providerId?: unknown; modelId?: unknown; dryRun?: unknown };
    if (typeof input.providerId !== 'string' || typeof input.modelId !== 'string') {
      return { status: 400, body: { error: 'providerId and modelId are required' } };
    }
    if (deps.evaluations.state.state !== 'idle') {
      return { status: 409, body: { error: 'an evaluation is running', state: deps.evaluations.state.state } };
    }
    const identityId = resolveIdentity(db, input.providerId, input.modelId);
    if (!identityId) {
      return {
        status: 404,
        body: { error: 'no resolved identity to re-read', providerId: input.providerId, modelId: input.modelId },
      };
    }
    const dryRun = input.dryRun === true;
    const summary = regradeFromRetainedResponses({
      db, identityId, dryRun, now: () => now.toISOString(),
    });
    // The overall score is derived, so a re-read that changed nothing downstream
    // would leave the published number disagreeing with its own evidence.
    if (!dryRun) recalculatePublishedOffers(db, now.toISOString());
    return {
      status: 200,
      body: {
        identityId,
        dryRun,
        rescored: summary.rescored,
        unreplayable: summary.unreplayable,
        withdrawn: summary.unreplayable.filter((row) => row.demoted).length,
      },
    };
  }

  if (path === '/v1/evaluations') {
    if (method === 'GET') return { status: 200, body: deps.evaluations.state };
    if (method === 'DELETE') return { status: 200, body: deps.evaluations.stop() };
    if (method === 'POST') {
      const input = (body ?? {}) as { providerId?: unknown; modelId?: unknown };
      if (typeof input.providerId !== 'string' || typeof input.modelId !== 'string') {
        return { status: 400, body: { error: 'providerId and modelId are required' } };
      }
      const outcome = deps.evaluations.enqueue(input.providerId, input.modelId);
      if (outcome.accepted) return { status: 202, body: { position: outcome.position, plan: outcome.plan } };
      if (outcome.reason === 'already_queued') {
        return { status: 409, body: { error: 'already queued', state: deps.evaluations.state.state } };
      }
      // Fail closed with the typed reason: nothing was sent to a provider.
      if (outcome.plan.blocked === 'model_not_found') {
        return { status: 404, body: { error: 'model not found', providerId: input.providerId, modelId: input.modelId } };
      }
      return { status: 422, body: { error: 'cannot evaluate', reason: outcome.plan.blocked } };
    }
    return { status: 405, body: { error: 'method not allowed', path } };
  }

  return { status: 404, body: { error: 'not found', path } };
}
