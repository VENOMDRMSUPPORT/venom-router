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
import { route } from './app.ts';
import { writeSnapshot } from './snapshot.ts';
import type { ScoreProfile } from '../sync/score/venom-score.ts';
import type { RejectionOverlay } from '../sync/identity-rejections.ts';

const HERE = dirname(fileURLToPath(import.meta.url));
export const BIND_HOST = '127.0.0.1';
export const DEFAULT_PORT = 8791;

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
  const runner = new SyncRunner({
    db,
    profile: profiles.find((p) => p.id === 'balanced')!,
    methodologyVersion,
    identityOverlay: loadIdentityOverlay(),
    identityRejections: loadIdentityRejections(),
    onSnapshot: writeSnapshot,
  });
  const scheduler = startScheduler(runner);
  const startedAt = new Date().toISOString();

  const server = createServer(async (req, res) => {
    const url = new URL(req.url ?? '/', `http://${BIND_HOST}:${port}`);
    let result;
    try {
      result = await route({ db, runner, scheduler, startedAt }, url, req.method ?? 'GET');
    } catch (err) {
      result = { status: 500, body: { error: err instanceof Error ? err.message : String(err) } };
    }
    const payload = JSON.stringify(result.body, null, 1);
    res.writeHead(result.status, {
      'content-type': 'application/json; charset=utf-8',
      // The SPA is served from a different port in development.
      'access-control-allow-origin': '*',
      ...result.headers,
    });
    res.end(payload);
  });

  return { server, db, runner, scheduler, port };
}

if (import.meta.filename === process.argv[1]) {
  const port = Number(process.argv.find((a) => a.startsWith('--port='))?.split('=')[1] ?? DEFAULT_PORT);
  const app = createApp(port);
  app.server.listen(port, BIND_HOST, () => {
    console.log(`catalog service on http://${BIND_HOST}:${port} (loopback only)`);
    console.log(`  GET  /v1/health     GET /v1/providers   GET /v1/models`);
    console.log(`  GET  /v1/changes    POST /v1/sync`);
    console.log(`  scheduler: every ${app.scheduler.intervalMs / 3_600_000}h, next ${app.scheduler.nextRunAt()}`);
  });
}
