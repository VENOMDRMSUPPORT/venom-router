#!/usr/bin/env node
/**
 * Catalog service entry point.
 *
 * Binds to 127.0.0.1 only. The bind address is not configurable: an accidental
 * `0.0.0.0` would put an unauthenticated service on the network, and the safest
 * way to prevent that is to leave no switch to flip.
 */

import { createServer } from 'node:http';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { openDb } from '../db/index.ts';
import { SyncRunner, startScheduler } from './sync-runner.ts';
import { EvaluationRunner } from './evaluation-runner.ts';
import { createEvaluationExecutor } from './evaluation-executor.ts';
import { buildEvaluationFixtures, fixtureDigest } from '../sync/evaluation/fixtures.ts';
import { evaluationCredentialReport } from '../sync/evaluation/provider-transport.ts';
import { reconcileCatalogNotificationLedger, route } from './app.ts';
import { writeSnapshot } from './snapshot.ts';
import { deliverDueNotifications, notificationConfig } from './notifications.ts';
import { autoEvaluate, autoEvaluationConfig, lastAttemptReader } from './auto-evaluation.ts';
import type { ScoreProfile } from '../sync/score/venom-score.ts';
import type { RejectionOverlay } from '../sync/identity-rejections.ts';
import { CATALOG_API_PORT, CATALOG_BIND_HOST } from '../config/ports.ts';
import { CATALOG_API_CONTRACT_HEADER, CATALOG_API_CONTRACT_VERSION } from '../config/api-contract.ts';

const HERE = dirname(fileURLToPath(import.meta.url));
export const BIND_HOST = CATALOG_BIND_HOST;
export const DEFAULT_PORT = CATALOG_API_PORT;
export const MAX_BODY_BYTES = 10 * 1024 * 1024; // 10MB limit to prevent memory exhaustion

/**
 * How often the service reconciles its own alert ledger.
 *
 * Matches the SPA's poll so a browser and an unattended service see the same
 * ledger. The tick is what makes the ledger independent of a browser at all:
 * reconciliation used to happen only inside `GET /v1/alerts`, so a service
 * nobody was watching raised no alert and queued no webhook.
 */
export const NOTIFICATION_RECONCILE_MS = 30_000;

export function loadProfiles(): { methodologyVersion: string; profiles: ScoreProfile[] } {
  const raw = JSON.parse(readFileSync(join(HERE, '..', 'overlays', 'score-profiles.json'), 'utf8'));
  return { methodologyVersion: raw.methodologyVersion, profiles: raw.profiles };
}

export function loadIdentityOverlay(): Record<string, string> {
  return JSON.parse(readFileSync(join(HERE, '..', 'overlays', 'identity.json'), 'utf8')).mappings ?? {};
}

/**
 * The overlay's refused-candidate records.
 *
 * Loaded alongside the mappings rather than folded into them, because they are
 * opposite claims: one decision resolved an identity, the other refused a
 * candidate. The ingestion needs both together so an id claimed by each fails
 * the run instead of resolving to whichever was read first.
 */
export function loadIdentityRejections(): RejectionOverlay {
  const raw = JSON.parse(readFileSync(join(HERE, '..', 'overlays', 'identity.json'), 'utf8'));
  return raw.rejected ?? { entries: {} };
}

/**
 * @param dbPath override the database file.
 *
 * Exists so a verification or review instance can run against a COPY. Two
 * writers on one SQLite file is the one way to actually corrupt it, and the live
 * service already holds the default path — so inspecting a change must never
 * mean contending for that file.
 */
export function createApp(port = DEFAULT_PORT, dbPath = process.env.CATALOG_DB) {
  const db = dbPath ? openDb(dbPath) : openDb();
  const { methodologyVersion, profiles } = loadProfiles();
  const testSetHash = fixtureDigest(buildEvaluationFixtures());
  // The service runs evaluations itself, which is what makes it the single
  // writer: a terminal batch opening a second connection is refused by
  // scripts/service-guard.ts.
  //
  // Built before the sync runner, not after, because the runner now hands it the
  // offers each run discovered. Constructing the runner first would have meant
  // assigning the queue onto it afterwards, and a dependency wired by mutation
  // is one a future caller can forget to wire at all.
  const evaluations = new EvaluationRunner({
    db,
    executor: createEvaluationExecutor(db),
    testSetHash,
  });
  const autoEvaluation = autoEvaluationConfig();
  const runner = new SyncRunner({
    db,
    profile: profiles.find((p) => p.id === 'balanced')!,
    methodologyVersion,
    identityOverlay: loadIdentityOverlay(),
    identityRejections: loadIdentityRejections(),
    // Evaluation identities are deliberately NOT frozen here: like bounds and
    // reviewed facts, the overlay is re-read from disk on every run, so a
    // reviewed declaration takes effect on the next sync without a restart.
    onSnapshot: writeSnapshot,
    // Every active offer is re-planned after a sync and measured if anything is
    // still missing, under the policy in `auto-evaluation.ts`. Whatever it does
    // not queue — covered, blocked, cooling down, over a ceiling — is reported
    // rather than dropped quietly.
    onOffersSettled: (offers) => autoEvaluate(
      { evaluations, config: autoEvaluation, lastAttemptAt: lastAttemptReader(db) },
      offers,
    ),
  });
  const scheduler = startScheduler(runner);
  const startedAt = new Date().toISOString();

  // Reconciliation belongs to the service, not to whoever happens to be looking
  // at it. `unref` so this timer never keeps the process alive on its own.
  const notificationTimer = setInterval(() => {
    try {
      reconcileCatalogNotificationLedger({ db, runner, scheduler, startedAt }, new Date());
    } catch (error) {
      console.error('[notifications] reconcile tick failed:', error);
    }
  }, NOTIFICATION_RECONCILE_MS);
  notificationTimer.unref?.();

  const server = createServer(async (req, res) => {
    const url = new URL(req.url ?? '/', `http://${BIND_HOST}:${port}`);
    let result;
    try {
      // Buffered rather than streamed: every request this service accepts is a
      // small control message, and a body is only read when one was sent.
      let body: unknown;
      if (req.method === 'POST' || req.method === 'PUT' || req.method === 'PATCH') {
        const chunks: Buffer[] = [];
        let totalBytes = 0;
        for await (const chunk of req) {
          const buf = chunk as Buffer;
          totalBytes += buf.length;
          if (totalBytes > MAX_BODY_BYTES) {
            res.writeHead(413, { 'content-type': 'application/json; charset=utf-8' });
            res.end(JSON.stringify({ error: 'payload too large' }));
            return;
          }
          chunks.push(buf);
        }
        const raw = Buffer.concat(chunks).toString('utf8').trim();
        if (raw.length > 0) {
          try {
            body = JSON.parse(raw);
          } catch {
            res.writeHead(400, { 'content-type': 'application/json; charset=utf-8' });
            res.end(JSON.stringify({ error: 'invalid json body' }));
            return;
          }
        }
      }
      result = await route({ db, runner, evaluations, scheduler, startedAt }, url, req.method ?? 'GET', body);
    } catch (err) {
      // The response says nothing: an exception message can carry a path, a
      // query or an upstream payload, and the caller is not entitled to any of
      // it. The log says everything, because this service holds no secrets —
      // the same property that lets its logs go unsanitised. A 500 that leaves
      // no trace anywhere would be the worse bug.
      console.error('[http] unhandled request failure:', err);
      result = { status: 500, body: { error: 'internal server error' } };
    }
    const payload = JSON.stringify(result.body, null, 1);
    res.writeHead(result.status, {
      'content-type': 'application/json; charset=utf-8',
      [CATALOG_API_CONTRACT_HEADER]: CATALOG_API_CONTRACT_VERSION,
      ...result.headers,
    });
    res.end(payload);
  });

  server.on('close', () => clearInterval(notificationTimer));

  return { server, db, runner, evaluations, scheduler, port, autoEvaluation };
}

if (import.meta.filename === process.argv[1]) {
  const port = Number(process.argv.find((a) => a.startsWith('--port='))?.split('=')[1] ?? DEFAULT_PORT);
  const app = createApp(port);
  const deliveryConfig = notificationConfig();
  const notificationTimer = deliveryConfig.enabled
    ? setInterval(() => { deliverDueNotifications(app.db, deliveryConfig).catch((error) => console.error('[notifications] delivery loop failed:', error)); }, 5_000)
    : null;
  notificationTimer?.unref();
  app.server.on('close', () => { if (notificationTimer) clearInterval(notificationTimer); });

  app.server.listen(port, BIND_HOST, () => {
    console.log(`catalog service on http://${BIND_HOST}:${port} (loopback only)`);
    console.log(`  GET  /v1/health     GET /v1/providers   GET /v1/models`);
    console.log(`  GET  /v1/changes    POST /v1/sync`);
    console.log(`  scheduler: every ${app.scheduler.intervalMs / 3_600_000}h, next ${app.scheduler.nextRunAt()}`);
    console.log(`  notifications: ${deliveryConfig.enabled ? `webhook enabled (${deliveryConfig.webhookUrl})` : 'disabled'}`);
    console.log(`  auto-evaluation: ${app.autoEvaluation.enabled
      ? `on, ${app.autoEvaluation.maxRequestsPerRun === null ? 'no request ceiling' : `up to ${app.autoEvaluation.maxRequestsPerRun} requests per sync`}`
        + `, ${app.autoEvaluation.retryCooldownMs / 3_600_000}h retry cooldown`
      : 'off'}`);
    // Said at startup, not on the first click. What the evaluation path can see
    // is a property of THIS process's environment, and the only thing that used
    // to report it was a modal sentence naming no variable.
    const credentials = evaluationCredentialReport();
    const readable = credentials.filter((row) => row.state === 'present').length;
    console.log(`  credentials: ${readable}/${credentials.length} readable`
      + (readable === credentials.length ? '' : ' — `npm run env:check` names each one'));
  });
}
