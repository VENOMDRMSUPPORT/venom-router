/**
 * The offline fallback is the last live answer, or it is nothing.
 *
 * The snapshot used to be a raw dump of database ROWS, and the SPA rebuilt an
 * API payload out of them by hand. That second derivation is what put "MISSING"
 * on three dashboard tiles and — worse — a fabricated `catalogReady = every
 * model` and `needsVerification = 0` on two others: the row dump carries no
 * identity, no conflicts and no completeness verdict, so the reconstruction had
 * to either admit ignorance or invent an answer, and it did both.
 *
 * These tests pin the replacement invariant: what gets written to disk is
 * exactly what `/v1/models` and `/v1/providers` served, so the offline page can
 * be stale but never different.
 */

import { test, describe, beforeEach } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, readFileSync, existsSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { openDb, type Db } from '../db/index.ts';
import { route } from './app.ts';
import { buildSnapshot, writeSnapshot } from './snapshot.ts';
import { syncProvider, type ProviderAdapter, type SpecLookup } from '../sync/engine.ts';
import { scoreAll } from '../sync/score/pipeline.ts';
import { enrich, canonicalFromBenchmarks } from '../sync/enrich/enrich.ts';
import { buildIndex } from '../sync/identity.ts';
import { SyncRunner } from './sync-runner.ts';
import type { BenchmarkSource } from '../sync/sources/openrouter.ts';
import type { ScoreProfile } from '../sync/score/venom-score.ts';

const PROFILE: ScoreProfile = {
  id: 'balanced', label: 'Balanced',
  weights: { context: 0.3, output: 0.2, capabilities: 0.3, cost: 0.2 },
};

function benchmarks(): BenchmarkSource {
  const records = [];
  for (let i = 0; i < 40; i++) {
    const elo = 900 + i * 12;
    records.push({
      id: `vendor${i % 5}/model-${i}`, vendor: `vendor${i % 5}`,
      intelligence: 0.11 * elo - 99 + ((i * 37) % 7) - 3,
      designElo: elo, costOutPerM: i,
    });
  }
  records.push({ id: 'acme/measured-1', vendor: 'acme', intelligence: 62.1, designElo: 1300 });
  records.push({ id: 'acme/calibrated-1', vendor: 'acme', designElo: 1200 });
  return { index: buildIndex(records), byId: new Map(records.map((r) => [r.id, r])), count: records.length } as BenchmarkSource;
}

/**
 * The feed. `nobench-1` gets no entry at all, so it fails the completeness gate
 * — the fixture needs a genuinely incomplete row, because a catalog where every
 * model is ready cannot tell a REPORTED `catalogReady` from an assumed one.
 * Handed to the roster engine and to `enrich` alike, as production does.
 */
const SPEC: SpecLookup = (_k, modelId) =>
  modelId.includes('nobench')
    ? null
    : {
        contextTokens: 128_000, outputTokens: 32_000, tools: true, reasoning: true,
        structured: true, inputModalities: ['text'], costInPerM: 1, costOutPerM: 5,
      };

const adapter = (id: string): ProviderAdapter => ({
  id, name: id.toUpperCase(), rosterUrl: `https://${id}.test/v1/models`, feedKey: id,
  parseRoster: (b) => (b as { data: { id: string }[] }).data.map((m) => m.id),
});

let db: Db;
let clock = 0;
const now = () => new Date(Date.UTC(2026, 7, 12, 0, 0, clock++)).toISOString();

async function seed() {
  await syncProvider(adapter('acme'), {
    db, now,
    fetchJson: async () => ({ status: 200, body: { data: [{ id: 'measured-1' }, { id: 'calibrated-1' }, { id: 'nobench-1' }] } }),
    lookupSpec: SPEC,
  });
  const bm = benchmarks();
  enrich({
    db, lookupSpec: SPEC, canonical: canonicalFromBenchmarks(bm), overlay: {},
    billing: { acme: { model: 'per_token', evidenceUrl: 'https://acme.test/pricing', note: 'per-token' } },
    intrinsic: () => null, now,
  });
  scoreAll({
    db, benchmarks: bm, overlay: {}, profile: PROFILE,
    methodologyVersion: 'venom-score-v1', sourceFetchedAt: '2026-08-12T00:00:00.000Z', now,
  });
}

const runner = () => new SyncRunner({ db, profile: PROFILE, methodologyVersion: 'venom-score-v1', identityOverlay: {} });
/** The one clock both sides read, so provider freshness is a comparison and not a race. */
const AT = () => new Date(Date.UTC(2026, 7, 12, 1));
const get = (path: string) => route({ db, runner: runner(), now: AT }, new URL(`http://127.0.0.1${path}`), 'GET') as { status: number; body: any };

beforeEach(async () => {
  db = openDb(':memory:');
  clock = 0;
  await seed();
});

describe('the snapshot is the API answer, not a second derivation of it', () => {
  test('its models are the rows /v1/models served', () => {
    assert.deepEqual(buildSnapshot(db, AT).models, get('/v1/models').body.models);
  });

  test('its providers are the rows /v1/providers served', () => {
    assert.deepEqual(buildSnapshot(db, AT).providers, get('/v1/providers').body.providers);
  });

  test('it carries the whole meta block, so the offline page can state every figure', () => {
    // The three tiles that read MISSING, and the two that read a fabricated
    // number. All five come from meta; a snapshot without it cannot answer.
    const meta = buildSnapshot(db, AT).meta;
    assert.deepEqual(meta, get('/v1/models').body.meta);
    assert.equal(typeof meta.conflictedModels, 'number');
    assert.equal(typeof meta.identity.resolved, 'number');
    assert.equal(typeof meta.identityDetail.rejectedCandidates, 'number');
  });

  test('completeness is reported, never assumed', () => {
    // The fabrication being killed: the old fallback published
    // `catalogReady = every model` and `needsVerification = 0` regardless of
    // what the catalog actually found. This fixture HAS an incomplete row, so
    // the assertion cannot pass by the numbers coincidentally agreeing.
    const meta = buildSnapshot(db, AT).meta;
    assert.ok(meta.needsVerification > 0, 'fixture must contain a row that fails the completeness gate');
    assert.equal(meta.catalogReady + meta.needsVerification, meta.liveModels);
    assert.notEqual(meta.catalogReady, meta.liveModels);
  });

  test('it stamps when it was generated, from the injected clock', () => {
    assert.equal(buildSnapshot(db, () => new Date(Date.UTC(2026, 7, 18, 9))).generatedAt, '2026-08-18T09:00:00.000Z');
  });
});

describe('writeSnapshot puts that payload where the SPA reads it', () => {
  test('it writes catalog.json into the given directory', () => {
    const dir = mkdtempSync(join(tmpdir(), 'catalog-snap-'));
    writeSnapshot(db, dir, AT);
    const file = join(dir, 'catalog.json');
    assert.ok(existsSync(file));
    const written = JSON.parse(readFileSync(file, 'utf8'));
    assert.deepEqual(written.models, get('/v1/models').body.models);
    assert.deepEqual(written.meta, get('/v1/models').body.meta);
  });
});
