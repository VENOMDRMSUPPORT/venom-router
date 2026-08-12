import { test, describe, beforeEach } from 'node:test';
import assert from 'node:assert/strict';
import { openDb, type Db } from '../db/index.ts';
import { route, health } from './app.ts';
import { SyncRunner } from './sync-runner.ts';
import { loadModels, loadProviders, loadMeta } from './read-model.ts';
import { syncProvider, type ProviderAdapter } from '../sync/engine.ts';
import { scoreAll } from '../sync/score/pipeline.ts';
import { enrich, canonicalFromBenchmarks } from '../sync/enrich/enrich.ts';
import { buildIndex } from '../sync/identity.ts';
import type { BenchmarkSource } from '../sync/sources/openrouter.ts';
import type { ScoreProfile } from '../sync/score/venom-score.ts';

const PROFILE: ScoreProfile = {
  id: 'balanced', label: 'Balanced',
  weights: { context: 0.3, output: 0.2, capabilities: 0.3, cost: 0.2 },
};

/** A benchmark set with enough spread to fit an acceptable calibration. */
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
  // Two catalog-facing models: one with a direct figure, one only calibratable.
  records.push({ id: 'acme/measured-1', vendor: 'acme', intelligence: 62.1, designElo: 1300 });
  records.push({ id: 'acme/calibrated-1', vendor: 'acme', designElo: 1200 });
  records.push({ id: 'acme/nobench-1', vendor: 'acme' });
  return { index: buildIndex(records), byId: new Map(records.map((r) => [r.id, r])), count: records.length } as BenchmarkSource;
}

const adapter = (id: string, ids: string[]): ProviderAdapter => ({
  id, name: id.toUpperCase(), rosterUrl: `https://${id}.test/v1/models`, feedKey: id,
  parseRoster: (b) => (b as { data: { id: string }[] }).data.map((m) => m.id),
});

let db: Db;
let clock = 0;
const now = () => new Date(Date.UTC(2026, 7, 12, 0, 0, clock++)).toISOString();

async function seed(rosters: Record<string, string[]>) {
  for (const [id, ids] of Object.entries(rosters)) {
    await syncProvider(adapter(id, ids), {
      db, now,
      fetchJson: async () => ({ status: 200, body: { data: ids.map((x) => ({ id: x })) } }),
      lookupSpec: (_k, modelId) => ({
        contextTokens: modelId.includes('big') ? 1_000_000 : 128_000,
        outputTokens: 32_000, tools: true, reasoning: true, structured: true,
        inputModalities: ['text'], costInPerM: 1, costOutPerM: modelId.includes('free') ? 0 : 5,
      }),
    });
  }
  // The real pipeline always enriches before scoring, so the fixture does too —
  // otherwise cost semantics would be unset and every row would look incomplete
  // for a reason production never produces.
  const bm = benchmarks();
  enrich({
    db, canonical: canonicalFromBenchmarks(bm), overlay: {}, billing: { acme: 'per_token', other: 'per_token' },
    intrinsic: () => null, now,
  });
  scoreAll({
    db, benchmarks: bm, overlay: {}, profile: PROFILE,
    methodologyVersion: 'venom-score-v1', sourceFetchedAt: '2026-08-12T00:00:00.000Z', now,
  });
}

const runner = () => new SyncRunner({ db, profile: PROFILE, methodologyVersion: 'venom-score-v1', identityOverlay: {} });
const get = (path: string) => route({ db, runner: runner(), now: () => new Date(Date.UTC(2026, 7, 12, 1)) }, new URL(`http://127.0.0.1${path}`), 'GET') as { status: number; body: any };

beforeEach(async () => {
  db = openDb(':memory:');
  clock = 0;
  await seed({ acme: ['measured-1', 'calibrated-1', 'nobench-1', 'big-unknown'], other: ['measured-1'] });
});

describe('AC1 — the service returns the same inventory as the database', () => {
  test('model count matches a direct query', () => {
    const dbCount = (db.prepare(`SELECT COUNT(*) n FROM models WHERE status IN ('active','missing')`).get() as { n: number }).n;
    assert.equal(get('/v1/models').body.models.length, dbCount);
  });

  test('meta.liveModels agrees with the returned rows', () => {
    const r = get('/v1/models').body;
    assert.equal(r.meta.liveModels, r.models.length);
  });

  test('retired models are excluded unless asked for', () => {
    db.exec(`UPDATE models SET status='retired' WHERE model_id='nobench-1'`);
    assert.equal(get('/v1/models').body.models.length, 4);
    assert.equal(get('/v1/models?includeRetired=true').body.models.length, 5);
  });
});

describe('AC2 — provider counts reconcile with the global total', () => {
  test('the sum of provider liveModels equals the global count', () => {
    const providers = get('/v1/providers').body.providers;
    const total = providers.reduce((s: number, p: any) => s + p.liveModels, 0);
    assert.equal(total, get('/v1/models').body.models.length);
  });

  test('each provider count equals the rows filtered to it', () => {
    for (const p of get('/v1/providers').body.providers) {
      assert.equal(get(`/v1/models?provider=${p.id}`).body.models.length, p.liveModels);
    }
  });

  test('the identity partition sums to liveModels', () => {
    const m = get('/v1/models').body.meta;
    const i = m.identity;
    assert.equal(i.resolvedWithEvidence + i.resolvedWithoutEvidence + i.unresolved, m.liveModels);
  });

  test('identity rules are reported on their own axis, not as a partition of scored rows', () => {
    // A rule can sit on a row that resolved but carries no benchmark, so these
    // sum to resolved rows — never to qualityScored. Documented, not hidden.
    const m = get('/v1/models').body.meta;
    const ruleTotal = Object.values(m.identityRules).reduce((s: number, n: any) => s + n, 0);
    assert.equal(ruleTotal, m.identity.resolvedWithEvidence + m.identity.resolvedWithoutEvidence);
  });
});

describe('AC3 — no duplicate canonical rows from provider variants', () => {
  test('one row per (provider, model)', () => {
    const models = get('/v1/models').body.models;
    const keys = models.map((m: any) => `${m.providerId}/${m.modelId}`);
    assert.equal(new Set(keys).size, keys.length);
  });

  test('the same model at two providers shares a canonical id and a VQ', () => {
    const rows = get('/v1/models').body.models.filter((m: any) => m.modelId === 'measured-1');
    assert.equal(rows.length, 2);
    assert.equal(rows[0].canonicalId, rows[1].canonicalId);
    assert.equal(rows[0].vq.value, rows[1].vq.value);
  });
});

describe('AC4/AC5 — evidence and uncertainty survive serialization', () => {
  const json = (v: unknown) => JSON.parse(JSON.stringify(v));

  test('evidence levels are preserved exactly', () => {
    const wire = json(get('/v1/models').body.models);
    const levels = Object.fromEntries(wire.map((m: any) => [m.modelId, m.vq.evidenceLevel]));
    assert.equal(levels['measured-1'], 'measured');
    assert.equal(levels['calibrated-1'], 'calibrated');
    assert.equal(levels['nobench-1'], 'unrated');
    assert.equal(levels['big-unknown'], 'unrated');
  });

  test('uncertainty is a number on the wire, not lost or rounded away', () => {
    const wire = json(get('/v1/models').body.models);
    const cal = wire.find((m: any) => m.modelId === 'calibrated-1');
    const row = db.prepare(`SELECT uncertainty FROM model_scores WHERE model_id='calibrated-1' AND kind='VQ'`).get() as { uncertainty: number };
    assert.equal(cal.vq.uncertainty, row.uncertainty);
    assert.ok(cal.vq.uncertainty > 0);
  });

  test('an unrated model carries a null value and a dash, never a zero', () => {
    const wire = json(get('/v1/models').body.models).find((m: any) => m.modelId === 'nobench-1');
    assert.equal(wire.vq.value, null);
    assert.equal(wire.vq.display, '—');
  });

  test('precision is declared and the display honours it', () => {
    const wire = json(get('/v1/models').body.models);
    const cal = wire.find((m: any) => m.modelId === 'calibrated-1');
    assert.equal(cal.vq.precision, 0);
    assert.ok(!cal.vq.display.includes('.'), `calibrated rendered as ${cal.vq.display}`);
    assert.equal(wire.find((m: any) => m.modelId === 'measured-1').vq.precision, 1);
  });

  test('unrated models are unplaced in the ranking, not ranked last', () => {
    const wire = json(get('/v1/models').body.models);
    for (const m of wire) {
      if (m.vq.value === null) assert.equal(m.qualityRank, null, `${m.modelId} must be unplaced`);
      else assert.ok(m.qualityRank! >= 1);
    }
  });

  test('a high VO cannot buy a quality rank', () => {
    const wire = json(get('/v1/models').body.models);
    const unrated = wire.filter((m: any) => m.vq.value === null);
    assert.ok(unrated.length > 0);
    assert.ok(unrated.every((m: any) => m.qualityRank === null));
    assert.ok(unrated.some((m: any) => m.vo.value > 0), 'they still carry an operational score');
  });
});

describe('AC6 — provenance is sufficient for reconstruction', () => {
  test('every scored row carries a compact provenance summary', () => {
    for (const m of get('/v1/models').body.models) {
      if (m.vq.value === null) continue;
      assert.ok(m.vq.provenance, `${m.modelId} has no provenance`);
      assert.ok(m.vq.provenance.source);
      assert.ok(m.vq.provenance.sourceModelId);
      assert.ok(m.vq.provenance.identityRule);
      assert.equal(m.vq.provenance.methodologyVersion, 'venom-score-v1');
      assert.ok(m.vq.provenance.sourceFetchedAt);
    }
  });

  test('the detail endpoint returns raw value and transformation', () => {
    const r = route({ db, runner: runner() }, new URL('http://127.0.0.1/v1/models/acme/calibrated-1/provenance'), 'GET') as any;
    assert.equal(r.status, 200);
    assert.equal(typeof r.body.rawValue, 'number');
    assert.match(r.body.transformation, /^y = /);
    assert.ok(r.body.calibrationVersion);
  });

  test('a calibrated value is re-derivable from its own provenance', () => {
    const r = route({ db, runner: runner() }, new URL('http://127.0.0.1/v1/models/acme/calibrated-1/provenance'), 'GET') as any;
    const [, slope, intercept] = /y = (-?[\d.e-]+) \* x \+ (-?[\d.e-]+)/.exec(r.body.transformation)!;
    const stored = get('/v1/models').body.models.find((m: any) => m.modelId === 'calibrated-1').vq.value;
    assert.ok(Math.abs(Number(slope) * r.body.rawValue + Number(intercept) - stored) < 1e-9);
  });

  test('an unrated model has no provenance to offer, and says so', () => {
    const r = route({ db, runner: runner() }, new URL('http://127.0.0.1/v1/models/acme/nobench-1/provenance'), 'GET') as any;
    assert.equal(r.status, 404);
  });
});

describe('AC7 — a failed provider refresh preserves prior valid data', () => {
  test('an unreachable provider leaves its models and the global count intact', async () => {
    const before = get('/v1/models').body.models.length;
    await syncProvider(adapter('acme', []), {
      db, now, lookupSpec: () => null,
      fetchJson: async () => { throw new Error('upstream down'); },
    });
    assert.equal(get('/v1/models').body.models.length, before);
  });

  test('freshness follows the last SUCCESS, not the last attempt', async () => {
    await syncProvider(adapter('acme', []), {
      db, now, lookupSpec: () => null,
      fetchJson: async () => { throw new Error('upstream down'); },
    });
    const p = get('/v1/providers').body.providers.find((x: any) => x.id === 'acme');
    assert.equal(p.lastOutcome, 'failed');
    assert.ok(p.lastAttemptedSyncAt > p.lastSuccessfulSyncAt, 'the attempt is newer than the success');
    assert.ok(p.liveModels > 0, 'the catalog did not disappear');
  });

  test('health reports the failure without claiming the service is broken', async () => {
    await syncProvider(adapter('acme', []), {
      db, now, lookupSpec: () => null,
      fetchJson: async () => { throw new Error('upstream down'); },
    });
    const h = health({ db, runner: runner(), now: () => new Date(Date.UTC(2026, 7, 12, 1)) });
    assert.equal(h.body.service.status, 'up');
    assert.equal(h.body.catalog.liveModels, 5);
  });
});

describe('AC8 — concurrent syncs cannot corrupt state', () => {
  test('a second run is refused while one is in flight', async () => {
    const r = new SyncRunner({ db, profile: PROFILE, methodologyVersion: 'venom-score-v1', identityOverlay: {} });
    let release!: () => void;
    const gate = new Promise<void>((res) => (release = res));
    // Occupy the lock with a run that cannot finish until we let it.
    (r as unknown as { running: boolean }).running = true;
    const second = await r.run();
    assert.equal(second, null, 'the second run must be refused, not queued');
    (r as unknown as { running: boolean }).running = false;
    release();
    await gate;
  });

  test('POST /v1/sync answers 409 rather than starting a parallel run', async () => {
    const r = new SyncRunner({ db, profile: PROFILE, methodologyVersion: 'venom-score-v1', identityOverlay: {} });
    (r as unknown as { running: boolean }).running = true;
    const res = (await route({ db, runner: r }, new URL('http://127.0.0.1/v1/sync'), 'POST')) as any;
    assert.equal(res.status, 409);
    assert.match(res.body.error, /already running/);
  });
});

describe('AC9 — the service exposes only the intended interface', () => {
  test('an unknown path is 404, not a directory listing or a stack trace', () => {
    const r = get('/');
    assert.equal(r.status, 404);
    assert.equal(typeof r.body.error, 'string');
  });

  test('there is no route that returns the database file or arbitrary SQL', () => {
    for (const p of ['/v1/query?sql=SELECT+1', '/db', '/data/catalog.db', '/v1/models/../../etc/passwd']) {
      assert.equal(get(p).status, 404, `${p} must not be served`);
    }
  });

  test('health separates service liveness from catalog freshness', () => {
    const h = health({ db, runner: runner(), now: () => new Date(Date.UTC(2026, 7, 12, 1)) });
    assert.equal(h.body.service.status, 'up');
    assert.ok('status' in h.body.catalog);
    assert.notEqual(h.body.service.status, h.body.catalog.status, 'the two must be distinct fields, not one value');
  });

  test('a stale catalog is not reported as 200 healthy', () => {
    const h = health({ db, runner: runner(), now: () => new Date(Date.UTC(2026, 8, 30)) });
    assert.equal(h.status, 503);
    assert.equal(h.body.catalog.status, 'stale');
    assert.equal(h.body.service.status, 'up');
    assert.ok(h.body.catalog.staleProviders.length > 0);
  });
});

describe('AC10 — /v1/changes produces deterministic, meaningful diffs', () => {
  test('a first sync reports additions', () => {
    const r = get('/v1/changes').body;
    assert.equal(r.byClass.added, 5);
  });

  test('re-syncing identical data adds no change events', async () => {
    const before = get('/v1/changes').body.total;
    await seed({ acme: ['measured-1', 'calibrated-1', 'nobench-1', 'big-unknown'], other: ['measured-1'] });
    assert.equal(get('/v1/changes').body.total, before, 'an unchanged refetch must be silent');
  });

  test('a price change is reported as a price change', async () => {
    // The full roster is resent: shrinking it would trip the removal gate and
    // quarantine the run, so no change would be applied at all.
    const roster = ['measured-1', 'calibrated-1', 'nobench-1', 'big-unknown'];
    await syncProvider(adapter('acme', roster), {
      db, now,
      fetchJson: async () => ({ status: 200, body: { data: roster.map((id) => ({ id })) } }),
      lookupSpec: () => ({ contextTokens: 128_000, outputTokens: 32_000, tools: true, costInPerM: 1, costOutPerM: 99 }),
    });
    const changes = get('/v1/changes').body.changes;
    const price = changes.find((c: any) => c.class === 'price_changed');
    assert.ok(price, 'expected a price_changed entry');
    assert.equal(price.from, '5');
    assert.equal(price.to, '99');
  });

  test('a context change is classified separately from a price change', async () => {
    const roster = ['measured-1', 'calibrated-1', 'nobench-1', 'big-unknown'];
    await syncProvider(adapter('acme', roster), {
      db, now,
      fetchJson: async () => ({ status: 200, body: { data: roster.map((id) => ({ id })) } }),
      // context moves, price stays put
      lookupSpec: () => ({ contextTokens: 2_000_000, outputTokens: 32_000, tools: true, costInPerM: 1, costOutPerM: 5 }),
    });
    const classes = get('/v1/changes').body.changes.map((c: any) => c.class);
    assert.ok(classes.includes('context_changed'));
    assert.ok(!classes.includes('price_changed'));
  });

  test('since= filters to newer events only', () => {
    const all = get('/v1/changes').body;
    const cursor = all.cursor;
    assert.equal((route({ db, runner: runner() }, new URL(`http://127.0.0.1/v1/changes?since=${cursor}`), 'GET') as any).body.total, 0);
  });

  test('the same query twice returns the same result', () => {
    assert.deepEqual(get('/v1/changes').body, get('/v1/changes').body);
  });
});

describe('the completeness gate', () => {
  test('a row missing an operational fact is not catalog-ready, and says which', () => {
    db.exec(`UPDATE models SET structured = NULL WHERE model_id='measured-1' AND provider_id='acme'`);
    const m = get('/v1/models').body.models.find((x: any) => x.providerId === 'acme' && x.modelId === 'measured-1');
    assert.equal(m.catalogReady, false);
    assert.deepEqual(m.missingFacts, ['structured']);
  });

  test('a not-ready row is still SERVED, not hidden or deleted', () => {
    const before = get('/v1/models').body.models.length;
    db.exec(`UPDATE models SET structured = NULL`);
    const after = get('/v1/models').body;
    assert.equal(after.models.length, before, 'the inventory must stay whole');
    assert.equal(after.meta.catalogReady, 0);
    assert.equal(after.meta.needsVerification, before);
  });

  test('an unrated VQ does NOT make a row incomplete', () => {
    // Missing quality is a statement about the world; missing operational facts
    // are a gap in our data. Only the second one holds a row back.
    const m = get('/v1/models').body.models.find((x: any) => x.modelId === 'nobench-1');
    assert.equal(m.vq.value, null);
    assert.equal(m.catalogReady, true);
  });

  test('ready + needsVerification partitions the live rows', () => {
    const r = get('/v1/models').body;
    assert.equal(r.meta.catalogReady + r.meta.needsVerification, r.meta.liveModels);
  });
});
