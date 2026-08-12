#!/usr/bin/env node
/**
 * Catalog service entry point.
 *
 * Binds to 127.0.0.1 only. The bind address is not configurable: an accidental
 * `0.0.0.0` would put an unauthenticated service on the network, and the safest
 * way to prevent that is to leave no switch to flip.
 */

import { createServer } from 'node:http';
import { readFileSync, writeFileSync, mkdirSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { openDb, type Db } from '../db/index.ts';
import { SyncRunner, startScheduler } from './sync-runner.ts';
import { route } from './app.ts';
import type { ScoreProfile } from '../sync/score/venom-score.ts';

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

/** Deterministic JSON mirror of the database. Also the SPA's offline fallback. */
export function writeSnapshot(db: Db): void {
  const dir = join(HERE, '..', 'data', 'snapshot');
  mkdirSync(dir, { recursive: true });
  const models = db
    .prepare(
      `SELECT m.*, vq.value vq_value, vq.uncertainty vq_uncertainty, vq.evidence_level vq_level,
              vq.source_model_id vq_canonical, vq.precision_dp vq_precision, vo.value vo_value
       FROM models m
       LEFT JOIN model_scores vq ON vq.provider_id=m.provider_id AND vq.model_id=m.model_id AND vq.kind='VQ'
       LEFT JOIN model_scores vo ON vo.provider_id=m.provider_id AND vo.model_id=m.model_id AND vo.kind='VO'
       WHERE m.status != 'retired' ORDER BY m.provider_id, m.model_id`,
    )
    .all();
  writeFileSync(
    join(dir, 'catalog.json'),
    JSON.stringify({ generatedAt: new Date().toISOString(), providers: db.prepare('SELECT * FROM providers ORDER BY id').all(), models }, null, 1),
  );
}

export function createApp(port = DEFAULT_PORT) {
  const db = openDb();
  const { methodologyVersion, profiles } = loadProfiles();
  const runner = new SyncRunner({
    db,
    profile: profiles.find((p) => p.id === 'balanced')!,
    methodologyVersion,
    identityOverlay: loadIdentityOverlay(),
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
