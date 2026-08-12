/**
 * HTTP routing, separated from the listener so tests can drive it without
 * opening a socket.
 */

import type { Db } from '../db/index.ts';
import { loadModels, loadProviders, loadMeta, loadProvenance, STALE_AFTER_HOURS } from './read-model.ts';
import { loadChanges } from './changes.ts';
import type { SyncRunner, SchedulerHandle } from './sync-runner.ts';

export interface AppDeps {
  db: Db;
  runner: SyncRunner;
  scheduler?: SchedulerHandle;
  now?: () => Date;
  startedAt?: string;
}

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
export function health(deps: AppDeps): HttpResult {
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

export function route(deps: AppDeps, url: URL, method: string): HttpResult | Promise<HttpResult> {
  const { db } = deps;
  const now = deps.now?.() ?? new Date();
  const path = url.pathname.replace(/\/+$/, '') || '/';

  if (path === '/v1/health') return health(deps);

  if (path === '/v1/providers' && method === 'GET') {
    const models = loadModels(db);
    return { status: 200, body: { providers: loadProviders(db, models, now), meta: loadMeta(db, models) } };
  }

  if (path === '/v1/models' && method === 'GET') {
    const includeRetired = url.searchParams.get('includeRetired') === 'true';
    let models = loadModels(db, { includeRetired });
    const provider = url.searchParams.get('provider');
    if (provider) models = models.filter((m) => m.providerId === provider);
    const level = url.searchParams.get('evidence');
    if (level) models = models.filter((m) => m.vq.evidenceLevel === level);
    return { status: 200, body: { models, meta: loadMeta(db, loadModels(db, { includeRetired })) } };
  }

  const provenance = /^\/v1\/models\/([^/]+)\/([^]+)\/provenance$/.exec(path);
  if (provenance && method === 'GET') {
    const detail = loadProvenance(db, decodeURIComponent(provenance[1]), decodeURIComponent(provenance[2]));
    return detail
      ? { status: 200, body: detail }
      : { status: 404, body: { error: 'no scored value for that model', providerId: provenance[1], modelId: provenance[2] } };
  }

  if (path === '/v1/changes' && method === 'GET') {
    const since = url.searchParams.get('since') ?? undefined;
    const limit = Number(url.searchParams.get('limit') ?? 500);
    return { status: 200, body: loadChanges(db, { since, limit: Number.isFinite(limit) ? limit : 500 }) };
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

  return { status: 404, body: { error: 'not found', path } };
}
