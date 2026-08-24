import { test, describe, beforeEach } from 'node:test';
import assert from 'node:assert/strict';
import { openDb, type Db } from '../db/index.ts';
import { route, health, type AppDeps } from './app.ts';
import { SyncRunner } from './sync-runner.ts';
import { EvaluationRunner, type EvaluationJobExecutor } from './evaluation-runner.ts';
import { buildEvaluationFixtures, fixtureDigest } from '../sync/evaluation/fixtures.ts';
import { resolveIdentity } from '../sync/evaluation/plan.ts';
import { syncProvider, type ProviderAdapter, type SpecLookup } from '../sync/engine.ts';
import { scoreAll } from '../sync/score/pipeline.ts';
import { enrich, canonicalFromBenchmarks } from '../sync/enrich/enrich.ts';
import { buildIndex } from '../sync/identity.ts';
import type { BenchmarkSource } from '../sync/sources/openrouter.ts';
import type { ScoreProfile } from '../sync/score/venom-score.ts';
import { beginResolutionWindow, finishResolutionAttempt } from '../sync/resolution-jobs.ts';

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

/**
 * The feed, as one function.
 *
 * Production hands `specs.lookup` to BOTH the roster engine and `enrich`, so a
 * fixture that gives the two different answers is testing a wiring that does
 * not exist.
 */
const SPEC: SpecLookup = (_k, modelId) => ({
  contextTokens: modelId.includes('big') ? 1_000_000 : 128_000,
  outputTokens: 32_000, tools: true, reasoning: true, structured: true,
  attachment: false, inputModalities: ['text'], costInPerM: 1, costOutPerM: modelId.includes('free') ? 0 : 5,
});

const adapter = (id: string): ProviderAdapter => ({
  id, name: id.toUpperCase(), rosterUrl: `https://${id}.test/v1/models`, feedKey: id,
  parseRoster: (b) => (b as { data: { id: string }[] }).data.map((m) => m.id),
});

let db: Db;
let clock = 0;
const now = () => new Date(Date.UTC(2026, 7, 12, 0, 0, clock++)).toISOString();

async function seed(rosters: Record<string, string[]>) {
  for (const [id, ids] of Object.entries(rosters)) {
    await syncProvider(adapter(id), {
      db, now,
      fetchJson: async () => ({ status: 200, body: { data: ids.map((x) => ({ id: x })) } }),
      lookupSpec: SPEC,
    });
  }
  // The real pipeline always enriches before scoring, so the fixture does too —
  // otherwise cost semantics would be unset and every row would look incomplete
  // for a reason production never produces.
  const bm = benchmarks();
  enrich({
    db, lookupSpec: SPEC, canonical: canonicalFromBenchmarks(bm), overlay: {}, billing: {
      acme: { model: 'per_token', evidenceUrl: 'https://acme.test/pricing', note: 'per-token' },
      other: { model: 'per_token', evidenceUrl: 'https://other.test/pricing', note: 'per-token' },
    },
    intrinsic: () => null, now,
  });
  scoreAll({
    db, benchmarks: bm, overlay: {}, profile: PROFILE,
    methodologyVersion: 'venom-score-v1', sourceFetchedAt: '2026-08-12T00:00:00.000Z', now,
  });
}

const runner = () => new SyncRunner({ db, profile: PROFILE, methodologyVersion: 'venom-score-v1', identityOverlay: {} });
/** Never touches a provider: every job resolves instantly and records nothing. */
const inertExecutor: EvaluationJobExecutor = {
  async runDimension() { return { status: 'complete', score: 90 }; },
  async runSpeed() { return { status: 'complete' }; },
  recalculate() {},
};

/**
 * Deps for a route call, built in one place so a new service dependency is
 * added once rather than at every call site.
 */
function deps(extra: Partial<AppDeps> = {}): AppDeps {
  const database = extra.db ?? db;
  return {
    db: database,
    runner: runner(),
    evaluations: new EvaluationRunner({
      db: database,
      executor: inertExecutor,
      testSetHash: fixtureDigest(buildEvaluationFixtures()),
      hasCredential: () => true,
    }),
    ...extra,
  };
}

const get = (path: string) => route(deps({ now: () => new Date(Date.UTC(2026, 7, 12, 1)) }), new URL(`http://127.0.0.1${path}`), 'GET') as { status: number; body: any };

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

  test('provider filtering does not turn catalog metadata or ranks into provider-local values', () => {
    const global = get('/v1/models').body;
    const providerId = global.models[0].providerId;
    const filtered = get(`/v1/models?provider=${providerId}`).body;

    assert.ok(filtered.models.length < global.models.length);
    assert.equal(filtered.meta.liveModels, global.meta.liveModels);
    for (const model of filtered.models) {
      const globalModel = global.models.find(
        (candidate: any) => candidate.providerId === model.providerId && candidate.modelId === model.modelId,
      );
      assert.equal(model.modelRank, globalModel.modelRank);
    }
  });

  test('the identity partition sums to liveModels', () => {
    const m = get('/v1/models').body.meta;
    const i = m.identity;
    assert.equal(i.resolved + i.identityReview + i.unresolved, m.liveModels);
  });

  test('identity rules are reported on their own axis, not as a partition of scored rows', () => {
    // A rule can sit on a row that resolved but carries no benchmark, so these
    // sum to resolved rows — never to qualityScored. Documented, not hidden.
    const m = get('/v1/models').body.meta;
    const ruleTotal = Object.values(m.identityRules).reduce((s: number, n: any) => s + n, 0);
    assert.equal(ruleTotal, m.identity.resolved);
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

describe('model-score-v1 is projected once by the service', () => {
  test('publishes the hand-calculated 70/30 score and policy', () => {
    const body = get('/v1/models').body;
    const model = body.models.find((m: any) => m.providerId === 'acme' && m.modelId === 'measured-1');
    const expected = model.vq.value * 0.7 + Math.round(model.vo.value) * 0.3;

    assert.equal(model.modelScore.value, expected);
    assert.equal(model.modelScore.display, `${expected.toFixed(1)}%`);
    assert.equal(model.modelScore.methodologyVersion, 'model-score-v1');
    assert.deepEqual(body.meta.scoringPolicy, {
      methodologyVersion: 'model-score-v1',
      qualityWeight: 0.7,
      operationalWeight: 0.3,
      operationalPrecision: 0,
    });
    assert.equal(body.meta.sortContracts.modelScore.field, 'modelScore.value');
  });

  test('keeps a model without VQ outside the composite ranking', () => {
    const model = get('/v1/models').body.models.find((m: any) => m.modelId === 'nobench-1');

    assert.equal(model.vo.value > 0, true);
    assert.equal(model.modelScore.value, null);
    assert.equal(model.modelScore.reason, 'missing_vq');
    assert.equal(model.modelRank, null);
    assert.equal(model.tiedAtModelRank, false);
  });

  test('gives identical provider offerings the same dense global rank', () => {
    const rows = get('/v1/models').body.models.filter((m: any) => m.modelId === 'measured-1');

    assert.equal(rows.length, 2);
    assert.equal(rows[0].modelScore.value, rows[1].modelScore.value);
    assert.equal(rows[0].modelRank, rows[1].modelRank);
    assert.equal(rows[0].tiedAtModelRank, true);
    assert.equal(rows[1].tiedAtModelRank, true);
  });

  test('provider and catalog score coverage counts composite model scores', () => {
    const body = get('/v1/models').body;
    const provider = get('/v1/providers').body.providers.find((p: any) => p.id === 'acme');
    const scored = body.models.filter((m: any) => m.providerId === 'acme' && m.modelScore.value !== null).length;
    assert.equal(provider.modelScoreScored, scored);
    assert.equal(body.meta.modelScoreScored, body.models.filter((m: any) => m.modelScore.value !== null).length);
  });
});

describe('overall-score-v1 coexists with the legacy composite', () => {
  test('publishes the stored overall result and server-owned global rank', () => {
    db.prepare(`
      INSERT INTO overall_model_scores (
        provider_id, model_id, overall_score, quality_score, operational_score,
        quality_coverage_json, overall_coverage_json, included_dimensions_json,
        excluded_dimensions_json, status, uncertainty, reasons_json,
        methodology_ver, computed_at
      ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
    `).run(
      'acme', 'measured-1', 65.79, 67.5, 61.8,
      JSON.stringify({ scored: 5, applicable: 5, percent: 100 }),
      JSON.stringify({ scored: 7, applicable: 7, percent: 100 }),
      JSON.stringify(['coding', 'reasoning', 'longContext', 'toolCalling', 'structuredOutput', 'speed', 'costEfficiency']),
      JSON.stringify(['vision']), 'complete', 1.25, '[]',
      'overall-score-v1', '2026-08-19T10:00:00.000Z',
    );

    const body = get('/v1/models').body;
    const model = body.models.find((item: any) => item.providerId === 'acme' && item.modelId === 'measured-1');
    assert.equal(model.modelScore.methodologyVersion, 'model-score-v1');
    assert.equal(model.overallScore.value, 65.79);
    assert.equal(model.overallScore.display, '65.8%');
    assert.equal(model.overallScore.methodologyVersion, 'overall-score-v1');
    assert.equal(model.overallRank, 1);
    assert.equal(body.meta.overallScoreScored, 1);
  });

  test('exposes sanitized evaluation diagnostics without credentials or raw provider responses', () => {
    db.prepare(`INSERT INTO model_identity_scores (
      identity_id, dimension, score, raw_rate, uncertainty, confidence, sample_count,
      status, rubric_version, test_set_hash, evidence_json, evaluated_at, methodology_ver
    ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`).run(
      'acme/measured-1', 'coding', 72, 0.72, 2, 0.98, 60,
      'scored', 'catalog-rubrics-v1', 'sha256:test', JSON.stringify(['run:1']),
      '2026-08-19T10:00:00.000Z', 'overall-score-v1',
    );
    const response = get('/v1/models/acme/measured-1/evaluation');
    assert.equal(response.status, 200);
    assert.equal(response.body.identityDimensions[0].dimension, 'coding');
    assert.equal(response.body.identityDimensions[0].score, 72);
    assert.equal(response.body.providerId, 'acme');
    assert.equal(response.body.modelId, 'measured-1');
    const serialized = JSON.stringify(response.body);
    assert.equal(serialized.includes('raw_response'), false);
    // What must never appear is a credential VALUE. Scanning for the WORD was a
    // weaker proxy that also forbade the API from explaining itself: the plan
    // reports `missing_credentials` as a typed reason, which is exactly the kind
    // of accountable answer this service is supposed to give.
    assert.ok(!/sk-[A-Za-z0-9_-]{8,}/.test(serialized), 'no API-key shaped value may appear');
    assert.ok(!/"[A-Za-z0-9_-]{32,}"/.test(serialized), 'no opaque secret-length token may appear');
    assert.equal(response.body.plan.blocked, 'missing_credentials',
      'the only mention of a credential is this typed reason');
  });

  test('returns an accountable unknown result when no overall evidence was stored', () => {
    const model = get('/v1/models').body.models.find((item: any) => item.modelId === 'nobench-1');
    assert.deepEqual(model.overallScore.reasons, ['not_evaluated']);
    assert.equal(model.overallScore.status, 'unknown');
    assert.equal(model.overallScore.value, null);
    assert.equal(model.overallRank, null);
  });
});

describe('model resolution is projected by the service', () => {
  test('publishes processing and then awaiting benchmark without inventing a score', () => {
    beginResolutionWindow(db, '2026-08-19T10:00:00.000Z');
    let model = get('/v1/models').body.models.find((m: any) => m.providerId === 'acme' && m.modelId === 'nobench-1');
    assert.equal(model.resolution.state, 'processing');
    assert.ok(model.resolution.reasons.includes('missing_vq'));
    assert.equal(model.modelScore.value, null);

    finishResolutionAttempt(db, 'acme', 'nobench-1', '2026-08-19T10:05:00.000Z');
    model = get('/v1/models').body.models.find((m: any) => m.providerId === 'acme' && m.modelId === 'nobench-1');
    assert.equal(model.resolution.state, 'awaiting_external_benchmark');
    assert.equal(model.resolution.nextAttemptAt, null);
  });

  test('a complete scored model reports complete without a job', () => {
    const model = get('/v1/models').body.models.find((m: any) => m.providerId === 'acme' && m.modelId === 'measured-1');
    assert.deepEqual(model.resolution, {
      state: 'complete', reasons: [], firstDetectedAt: null, lastAttemptAt: null, nextAttemptAt: null,
    });
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
    const r = route(deps(), new URL('http://127.0.0.1/v1/models/acme/calibrated-1/provenance'), 'GET') as any;
    assert.equal(r.status, 200);
    assert.equal(typeof r.body.rawValue, 'number');
    assert.match(r.body.transformation, /^y = /);
    assert.ok(r.body.calibrationVersion);
  });

  test('a calibrated value is re-derivable from its own provenance', () => {
    const r = route(deps(), new URL('http://127.0.0.1/v1/models/acme/calibrated-1/provenance'), 'GET') as any;
    const [, slope, intercept] = /y = (-?[\d.e-]+) \* x \+ (-?[\d.e-]+)/.exec(r.body.transformation)!;
    const stored = get('/v1/models').body.models.find((m: any) => m.modelId === 'calibrated-1').vq.value;
    assert.ok(Math.abs(Number(slope) * r.body.rawValue + Number(intercept) - stored) < 1e-9);
  });

  test('an unrated model has no provenance to offer, and says so', () => {
    const r = route(deps(), new URL('http://127.0.0.1/v1/models/acme/nobench-1/provenance'), 'GET') as any;
    assert.equal(r.status, 404);
  });
});

describe('AC7 — a failed provider refresh preserves prior valid data', () => {
  test('an unreachable provider leaves its models and the global count intact', async () => {
    const before = get('/v1/models').body.models.length;
    await syncProvider(adapter('acme'), {
      db, now, lookupSpec: () => null,
      fetchJson: async () => { throw new Error('upstream down'); },
    });
    assert.equal(get('/v1/models').body.models.length, before);
  });

  test('freshness follows the last SUCCESS, not the last attempt', async () => {
    await syncProvider(adapter('acme'), {
      db, now, lookupSpec: () => null,
      fetchJson: async () => { throw new Error('upstream down'); },
    });
    const p = get('/v1/providers').body.providers.find((x: any) => x.id === 'acme');
    assert.equal(p.lastOutcome, 'failed');
    assert.ok(p.lastAttemptedSyncAt > p.lastSuccessfulSyncAt, 'the attempt is newer than the success');
    assert.ok(p.liveModels > 0, 'the catalog did not disappear');
  });

  test('health reports the failure without claiming the service is broken', async () => {
    await syncProvider(adapter('acme'), {
      db, now, lookupSpec: () => null,
      fetchJson: async () => { throw new Error('upstream down'); },
    });
    const h = health({ db, runner: runner(), now: () => new Date(Date.UTC(2026, 7, 12, 1)) }) as { status: number; body: any };
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
    const res = (await route(deps({ runner: r }), new URL('http://127.0.0.1/v1/sync'), 'POST')) as any;
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
    const h = health({ db, runner: runner(), now: () => new Date(Date.UTC(2026, 7, 12, 1)) }) as { status: number; body: any };
    assert.equal(h.body.service.status, 'up');
    assert.ok('status' in h.body.catalog);
    assert.notEqual(h.body.service.status, h.body.catalog.status, 'the two must be distinct fields, not one value');
  });

  test('a stale catalog is not reported as 200 healthy', () => {
    const h = health({ db, runner: runner(), now: () => new Date(Date.UTC(2026, 8, 30)) }) as { status: number; body: any };
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
    await syncProvider(adapter('acme'), {
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
    await syncProvider(adapter('acme'), {
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
    assert.equal((route(deps(), new URL(`http://127.0.0.1/v1/changes?since=${cursor}`), 'GET') as any).body.total, 0);
  });

  test('the same query twice returns the same result', () => {
    assert.deepEqual(get('/v1/changes').body, get('/v1/changes').body);
  });

  test('a publish-policy exclusion is surfaced, with the reason that caused it', () => {
    // 73 exclusions were recorded and none reached a reader: `classify` had no
    // case for the kind, so every one of them fell through to null. A row
    // vanishing from the catalog because it could not be proven free is exactly
    // what this endpoint exists to report.
    db.prepare(`INSERT INTO model_events (provider_id, model_id, kind, field, old_value, new_value, reason, at)
      VALUES ('acme','measured-1','excluded','status','active','excluded','not_proven_free','2126-01-01T00:00:00.000Z')`).run();

    const excluded = get('/v1/changes').body.changes.find((c: any) => c.class === 'excluded');
    assert.ok(excluded, 'expected an excluded entry');
    assert.equal(excluded.modelId, 'measured-1');
    assert.equal(excluded.note, 'not_proven_free');
  });

  test('limit=0 answers with the cursor alone', () => {
    // What the SPA polls to decide whether to refetch: the row query becomes
    // LIMIT 0 while the cursor is still MAX(at) over the whole event table, so
    // "has anything changed" needs no second endpoint.
    const probe = (route(deps(), new URL('http://127.0.0.1/v1/changes?limit=0'), 'GET') as any).body;
    assert.equal(probe.total, 0);
    assert.deepEqual(probe.changes, []);
    assert.equal(probe.cursor, get('/v1/changes').body.cursor);
    assert.ok(probe.cursor, 'a catalog with recorded events must report a cursor');
  });

  test('a huge limit is clamped to the public maximum', () => {
    const insert = db.prepare(`
      INSERT INTO model_events (provider_id, model_id, kind, field, old_value, new_value, reason, at)
      VALUES ('probe', ?, 'changed', 'context', '1', '2', 'context', '2026-08-12T00:00:00.000Z')
    `);
    for (let i = 0; i < 600; i++) insert.run(`model-${i}`);

    const result = get('/v1/changes?limit=1000000').body;
    assert.equal(result.total, 500);
    assert.equal(result.changes.length, 500);
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

describe('a recorded source disagreement is visible through the API', () => {
  /** A conflict as the enrichment pass records one. */
  const recordConflict = (modelId: string, field: string, sides: { value: unknown; by: string }[]) =>
    db
      .prepare(
        `INSERT INTO model_conflicts (provider_id, model_id, field, sides_json, conflict_type, detected_at)
         VALUES ('acme', ?, ?, ?, 'source_disagreement', '2026-08-13T00:00:00.000Z')`,
      )
      .run(modelId, field, JSON.stringify(sides));

  const rowFor = (modelId: string) =>
    get('/v1/models').body.models.find((x: any) => x.modelId === modelId && x.providerId === 'acme');

  test('the disputed field, both values and both sources reach the client', () => {
    recordConflict('measured-1', 'structured', [
      { value: true, by: 'aihubmix/measured-1' },
      { value: false, by: 'kilo/measured-1' },
    ]);

    const conflicts = rowFor('measured-1').conflicts;
    assert.equal(conflicts.length, 1, 'expected the conflict on the model row');
    assert.equal(conflicts[0].field, 'structured');
    assert.equal(conflicts[0].status, 'open');
    assert.deepEqual(conflicts[0].sides, [
      { value: true, by: 'aihubmix/measured-1' },
      { value: false, by: 'kilo/measured-1' },
    ]);
  });

  test('a model with no disagreement carries an empty list, not a missing field', () => {
    // An absent key and "no conflicts" are different claims to a consumer.
    assert.deepEqual(rowFor('calibrated-1').conflicts, []);
  });

  test('a conflict is attributed to its own provider offering only', () => {
    recordConflict('measured-1', 'structured', [{ value: true, by: 'a/m' }, { value: false, by: 'b/m' }]);

    const other = get('/v1/models').body.models.find((x: any) => x.providerId === 'other');
    assert.deepEqual(other.conflicts, [], 'another seller of the same model must not inherit it');
  });

  test('meta counts the models a disagreement is withholding a field from', () => {
    recordConflict('measured-1', 'structured', [{ value: true, by: 'a/m' }, { value: false, by: 'b/m' }]);
    recordConflict('measured-1', 'attachment', [{ value: true, by: 'a/m' }, { value: false, by: 'b/m' }]);
    recordConflict('calibrated-1', 'structured', [{ value: true, by: 'a/m' }, { value: false, by: 'b/m' }]);

    const meta = get('/v1/models').body.meta;
    assert.equal(meta.conflictedModels, 2, 'two models, not three conflicts');
    assert.deepEqual(meta.conflictsByField, { attachment: 1, structured: 2 });
  });
});

describe('rejected identity candidates reach the HTTP surface', () => {
  test('/v1/models carries the rejection evidence and the identity state', () => {
    // Proves the serialisation boundary, not just the read model: a field the
    // route drops is invisible to the SPA no matter how well it is stored.
    db.prepare(
      `INSERT INTO identity_rejections (provider_id, model_id, rejected_candidate, verdict, reason,
                                        evidence_json, source, source_ref, source_url, evidence_state,
                                        resolver_version, candidate_meta_json, reviewed_at, recorded_at)
       VALUES ('acme','big-unknown','up/refused','candidate_rejected','the reason',
               '["line one","line two"]','identity_overlay','big-unknown','https://src.test',
               'declared_policy','identity-rejections-v1','{"contextTokens":42}','2026-08-13','2026-08-13T00:00:00.000Z')`,
    ).run();

    const body = get('/v1/models').body;
    const m = body.models.find((x: any) => x.providerId === 'acme' && x.modelId === 'big-unknown');

    assert.equal(m.identityState, 'identity_review', 'an investigated row is not merely unresolved');
    assert.equal(m.rejectedCandidates.length, 1);
    const r = m.rejectedCandidates[0];
    assert.equal(r.candidate, 'up/refused');
    assert.equal(r.verdict, 'candidate_rejected');
    assert.equal(r.why, 'the reason');
    assert.deepEqual(r.evidence, ['line one', 'line two']);
    assert.equal(r.sourceUrl, 'https://src.test');
    assert.equal(r.evidenceState, 'declared_policy');
    assert.equal(r.resolverVersion, 'identity-rejections-v1');
    assert.deepEqual(r.candidateMeta, { contextTokens: 42 });
    assert.equal(body.meta.identityDetail.rejectedCandidates, 1);
  });

  test('a row with no rejections still carries the field, as an empty list', () => {
    const m = get('/v1/models').body.models.find((x: any) => x.modelId === 'measured-1');
    assert.deepEqual(m.rejectedCandidates, []);
    assert.equal(m.identityState, 'resolved');
  });
});

/**
 * The invariant the qwen3.8-max defect broke, asserted at the API surface.
 *
 * One Evidence panel reported "Context window — not published by any source we
 * consult" directly above "Context window | 1000000 | openrouter". Both came
 * from the same payload, so no amount of care in the component could have
 * reconciled them: `missingFacts` and `provenanceByField` disagreed at source.
 *
 * Asserted here rather than only in the enrichment tests because this is the
 * contract a consumer actually sees, and it is the shape a reader calls a lie.
 */
describe('a field is never both missing and provenanced', () => {
  /**
   * Re-run enrichment the way the sync does, against whatever the feed now
   * publishes.
   *
   * A source going quiet is expressed through the LOOKUP rather than by nulling
   * a column by hand: the column is also where enrich's own output lands, so
   * emptying it says "this run resolved nothing", which is a different event
   * from "the provider stopped publishing it" and no longer simulates one.
   */
  const reEnrich = (spec: SpecLookup = SPEC) => {
    const bm = benchmarks();
    enrich({
      db, lookupSpec: spec, canonical: canonicalFromBenchmarks(bm), overlay: {},
      billing: {
        acme: { model: 'per_token', evidenceUrl: 'https://acme.test/pricing', note: 'per-token' },
        other: { model: 'per_token', evidenceUrl: 'https://other.test/pricing', note: 'per-token' },
      },
      intrinsic: () => null, now,
    });
  };

  test('a fact that stops resolving leaves no provenance for the consumer to trip over', () => {
    const model = () =>
      (get('/v1/models').body.models as any[]).find(
        (m) => m.providerId === 'acme' && m.modelId === 'big-unknown',
      );

    assert.ok(model().provenanceByField.context, 'precondition: the field had provenance');

    // Exactly the production shape: the provider stops publishing the limit,
    // and a serving limit never travels between sellers, so nothing can prove
    // it again.
    reEnrich((k, id) => (id === 'big-unknown' ? { ...SPEC(k, id)!, contextTokens: undefined } : SPEC(k, id)));

    const m = model();
    assert.ok(m.missingFacts.includes('context'), 'the field is reported as a gap');
    assert.equal(
      m.provenanceByField.context,
      undefined,
      'so it must carry no provenance — a gap with a source is a contradiction, not extra detail',
    );
  });

  test('no model in the payload contradicts itself on any field', () => {
    // The general form, so a future field cannot reintroduce the shape one
    // field at a time.
    //
    // The gate's `cost` maps to `effectivePrice` ALONE, and the two facts it
    // does NOT map to are the interesting part:
    //
    //   billingKind    survives a cost gap because it records WHY the price is
    //                  unknown — `value: "unknown"`, `sourceRef: "no price
    //                  published"`, `evidenceState: declared_policy`. A recorded
    //                  reason for not knowing is the opposite of a claimed value.
    //   referencePrice is a different question — the list price ELSEWHERE. It is
    //                  rendered as `ref` and is never what this provider charges.
    //
    // Only `effectivePrice` asserts "this is what it costs here", which is the
    // one claim a `cost` gap contradicts.
    const GATE_TO_FACT: Record<string, string[]> = { cost: ['effectivePrice'] };

    // The feed stops publishing the limits, and stops publishing a price for
    // one row — a real cost gap, so the `cost` branch above is actually
    // exercised. Without it every fixture row had a price and the mapping was
    // never reached, which is how a wrong mapping passes for the wrong reason.
    reEnrich((k, id) => {
      const base = { ...SPEC(k, id)!, contextTokens: undefined, outputTokens: undefined };
      return id === 'big-unknown' ? { ...base, costInPerM: undefined, costOutPerM: undefined } : base;
    });

    const models = get('/v1/models').body.models as any[];
    const gaps = models.flatMap((m) => m.missingFacts as string[]);
    assert.ok(gaps.includes('cost'), 'the fixture must produce a cost gap or this test proves nothing about cost');
    assert.ok(gaps.includes('context'), 'and a context gap, likewise');

    for (const m of models) {
      for (const gap of m.missingFacts as string[]) {
        for (const field of GATE_TO_FACT[gap] ?? [gap]) {
          assert.equal(
            m.provenanceByField[field],
            undefined,
            `${m.providerId}/${m.modelId}: "${gap}" is reported missing but still carries provenance for "${field}"`,
          );
        }
      }
    }
  });
});

describe('a row states which model it is, even with no index entry to bind to', () => {
  test('the vendor identity is served alongside the canonical id, never instead of it', () => {
    // `canonicalId` answers "which reference-index entry was this score taken
    // from" and must stay null when none was. But the page rendered that field
    // as the row's identity, so a model no index lists showed none at all —
    // beside a context window read from a listing of that exact model. The two
    // travel as two fields because they are two questions.
    db.prepare(
      `INSERT INTO model_facts (provider_id, model_id, field, value, source, source_ref, source_url,
                                evidence_state, raw_value, resolver_version, probe_version, resolved_at)
       VALUES ('acme','big-unknown','vendorIdentity','"z-ai/glm-5.3"','models.dev','nano-gpt/zai-org/glm-5.3',
               'https://models.dev/api.json','vendor_default','null','v1',NULL,'2026-08-12T00:00:00.000Z')`,
    ).run();

    const m = (get('/v1/models').body.models as any[]).find((x) => x.modelId === 'big-unknown');
    assert.equal(m.vendorModelId, 'z-ai/glm-5.3');
    assert.equal(m.canonicalId, null, 'no score was attached, so no canonical id');
  });

  test('a row with no vendor listing reports null, not an empty string', () => {
    const m = (get('/v1/models').body.models as any[]).find((x) => x.modelId === 'measured-1');
    assert.equal(m.vendorModelId, null);
  });
});

describe('evaluation control routes', () => {
  /** One queue shared across the calls in a test, so state carries between them. */
  const queue = () => new EvaluationRunner({
    db,
    executor: inertExecutor,
    testSetHash: fixtureDigest(buildEvaluationFixtures()),
    hasCredential: () => true,
  });
  const post = (evaluations: EvaluationRunner, body: unknown) =>
    route(deps({ evaluations }), new URL('http://127.0.0.1/v1/evaluations'), 'POST', body) as { status: number; body: any };

  test('accepts a model onto the queue and answers with the plan it will spend', () => {
    const result = post(queue(), { providerId: 'acme', modelId: 'measured-1' });
    assert.equal(result.status, 202);
    assert.equal(result.body.position, 1);
    assert.ok(result.body.plan.estimatedRequests > 0);
    assert.equal(result.body.plan.blocked, null);
  });

  test('is a conflict when the same offer is already queued', () => {
    const evaluations = queue();
    assert.equal(post(evaluations, { providerId: 'acme', modelId: 'measured-1' }).status, 202);
    const second = post(evaluations, { providerId: 'acme', modelId: 'measured-1' });
    assert.equal(second.status, 409);
  });

  test('is 404 for a model the catalog does not have', () => {
    const result = post(queue(), { providerId: 'acme', modelId: 'no-such-model' });
    assert.equal(result.status, 404);
  });

  test('is 400 when the body does not name an offer', () => {
    assert.equal(post(queue(), {}).status, 400);
    assert.equal(post(queue(), { providerId: 'acme' }).status, 400);
  });

  test('reports the queue, and DELETE empties it', () => {
    const evaluations = queue();
    post(evaluations, { providerId: 'acme', modelId: 'measured-1' });
    const state = route(deps({ evaluations }), new URL('http://127.0.0.1/v1/evaluations'), 'GET') as { status: number; body: any };
    assert.equal(state.status, 200);
    assert.ok(['idle', 'running'].includes(state.body.state));
    const cleared = route(deps({ evaluations }), new URL('http://127.0.0.1/v1/evaluations'), 'DELETE') as { status: number; body: any };
    assert.equal(cleared.status, 200);
    assert.equal(typeof cleared.body.cleared, 'number');
  });

  test('refuses a method it does not implement', () => {
    const result = route(deps(), new URL('http://127.0.0.1/v1/evaluations'), 'PATCH') as { status: number };
    assert.equal(result.status, 405);
  });

  const regrade = (evaluations: EvaluationRunner, body: unknown) =>
    route(deps({ evaluations }), new URL('http://127.0.0.1/v1/evaluations/regrade'), 'POST', body) as
      { status: number; body: any };

  test('re-reads stored evidence for one offer, and reports what it found', () => {
    // Zero provider requests, so this route needs no cost preview and no queue.
    // It exists because the free repair was a terminal script guarded against
    // running while the service is up — unavailable exactly when it is wanted.
    const result = regrade(queue(), { providerId: 'acme', modelId: 'measured-1' });
    assert.equal(result.status, 200);
    assert.ok(Array.isArray(result.body.rescored));
    assert.ok(Array.isArray(result.body.unreplayable));
    assert.equal(typeof result.body.withdrawn, 'number');
    assert.equal(result.body.dryRun, false);
  });

  test('a re-read is refused while an evaluation is running', () => {
    const evaluations = queue();
    post(evaluations, { providerId: 'acme', modelId: 'measured-1' });
    const result = regrade(evaluations, { providerId: 'acme', modelId: 'measured-1' });
    // Re-scoring a dimension a job is measuring would publish half a run.
    assert.equal(result.status, 409);
  });

  test('a re-read needs an offer, and an offer with an identity', () => {
    assert.equal(regrade(queue(), {}).status, 400);
    assert.equal(regrade(queue(), { providerId: 'acme', modelId: 'no-such-model' }).status, 404);
  });

  test('a re-read refuses a method it does not implement', () => {
    const result = route(deps(), new URL('http://127.0.0.1/v1/evaluations/regrade'), 'GET') as { status: number };
    assert.equal(result.status, 405);
  });

  test('the diagnostics answer under the same identity the planner uses', () => {
    // Two resolutions used to exist: the planner weighs canonical id, vendor
    // identity AND the reviewed `evaluationIdentity` override, while this route
    // read only the first two. For the offers carrying an override they
    // disagreed, so the route reported no dimensions at all and the Evaluate
    // dialog showed an empty evidence panel for a model with stored scores.
    const detail = get('/v1/models/acme/measured-1/evaluation');
    assert.equal(detail.status, 200);
    assert.equal(
      detail.body.identityId,
      resolveIdentity(db, 'acme', 'measured-1'),
      'one question, one answer',
    );
  });

  test('the diagnostics route carries the plan, so the modal needs no second endpoint', () => {
    const result = get('/v1/models/acme/measured-1/evaluation');
    assert.equal(result.status, 200);
    assert.ok(Array.isArray(result.body.plan.dimensions));
    assert.equal(typeof result.body.plan.estimatedRequests, 'number');
  });
});

describe('HTTP server payload limit guard', () => {
  test('rejects payload exceeding MAX_BODY_BYTES with HTTP 413', async () => {
    const { createApp, MAX_BODY_BYTES } = await import('./index.ts');
    const app = createApp(0, ':memory:');
    await new Promise<void>((resolve) => app.server.listen(0, '127.0.0.1', () => resolve()));
    const address = app.server.address() as { port: number };

    try {
      // Send an oversized body exceeding MAX_BODY_BYTES
      const oversized = Buffer.alloc(MAX_BODY_BYTES + 1024, 'a');
      const res = await fetch(`http://127.0.0.1:${address.port}/v1/sync`, {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: oversized,
      });

      assert.equal(res.status, 413);
      const data = await res.json() as { error?: string };
      assert.equal(data.error, 'payload too large');
    } finally {
      await new Promise<void>((resolve) => app.server.close(() => resolve()));
      app.scheduler.stop();
    }
  });
});


describe('Database Browser — bounded read-only contract', () => {
  const dbRequest = (path: string, method: string, body?: unknown) =>
    route(deps(), new URL(`http://127.0.0.1${path}`), method, body) as { status: number; body: any };

  test('lists known tables and returns a validated schema', () => {
    const tables = dbRequest('/v1/db/tables', 'GET');
    assert.equal(tables.status, 200);
    assert.ok(tables.body.tables.some((table: any) => table.name === 'models'));

    const schema = dbRequest('/v1/db/schema?table=models', 'GET');
    assert.equal(schema.status, 200);
    assert.equal(schema.body.table, 'models');
    assert.ok(schema.body.columns.some((column: any) => column.name === 'model_id'));
  });

  test('rejects unknown and malicious schema identifiers without executing them', () => {
    assert.equal(dbRequest('/v1/db/schema?table=not_a_table', 'GET').status, 404);
    assert.equal(dbRequest('/v1/db/schema?table=models%22%29%3B%20DROP%20TABLE%20models%3B--', 'GET').status, 404);
    assert.ok(db.prepare("SELECT name FROM sqlite_master WHERE type='table' AND name='models'").get());
  });

  test('runs SELECT, read-only CTEs, literals, and comments', () => {
    const select = dbRequest('/v1/db/query', 'POST', { sql: 'SELECT model_id FROM models ORDER BY model_id', limit: 2 });
    assert.equal(select.status, 200);
    assert.equal(select.body.columns[0], 'model_id');
    assert.equal(select.body.rows.length, 2);
    assert.equal(select.body.rowCount, 2);
    assert.equal(select.body.limit, 2);
    assert.equal(select.body.truncated, true);

    const cte = dbRequest('/v1/db/query', 'POST', { sql: 'WITH picked AS (SELECT model_id FROM models LIMIT 1) SELECT model_id FROM picked' });
    assert.equal(cte.status, 200);
    assert.equal(cte.body.rowCount, 1);

    const literal = dbRequest('/v1/db/query', 'POST', { sql: "SELECT 'DELETE UPDATE INSERT' AS text -- DELETE\n" });
    assert.equal(literal.status, 200);
    assert.equal(literal.body.rows[0].values[0], 'DELETE UPDATE INSERT');
  });

  test('rejects writes, schema changes, pragmas, attachments, and transaction statements', () => {
    for (const sql of [
      'INSERT INTO models (provider_id, model_id, status) VALUES (\'x\', \'y\', \'active\')',
      'UPDATE models SET status=\'active\'',
      'DELETE FROM models',
      'DROP TABLE models',
      'CREATE TABLE unsafe_table (id INTEGER)',
      'ALTER TABLE models ADD COLUMN unsafe TEXT',
      'PRAGMA user_version',
      "ATTACH DATABASE ':memory:' AS other",
      'DETACH DATABASE other',
      'VACUUM',
      'ANALYZE',
      'BEGIN',
      'WITH changed AS (INSERT INTO models (provider_id, model_id, status) VALUES (\'x\', \'z\', \'active\') RETURNING model_id) SELECT * FROM changed',
    ]) {
      assert.equal(dbRequest('/v1/db/query', 'POST', { sql }).status, 400, sql);
    }
  });

  test('rejects multiple statements and invalid limits at the boundary', () => {
    for (const sql of ['SELECT 1; DELETE FROM models', 'SELECT 1; SELECT 2', '/* comment */ UPDATE models SET status=\'active\'']) {
      const result = dbRequest('/v1/db/query', 'POST', { sql });
      assert.equal(result.status, 400, sql);
    }
    for (const limit of [0, -1, 1001, 1.5, '10', null]) {
      assert.equal(dbRequest('/v1/db/query', 'POST', { sql: 'SELECT 1', limit }).status, 400, String(limit));
    }
  });

  test('reads at most limit plus one row and reports returned count and truncation', () => {
    const result = dbRequest('/v1/db/query', 'POST', { sql: 'SELECT model_id FROM models ORDER BY model_id', limit: 1 });
    assert.equal(result.status, 200);
    assert.equal(result.body.rows.length, 1);
    assert.equal(result.body.rowCount, 1);
    assert.equal(result.body.truncated, true);
  });

  test('serializes BigInt and Blob values without JSON failures', () => {
    db.exec('CREATE TABLE db_browser_types (big INTEGER, payload BLOB)');
    db.prepare('INSERT INTO db_browser_types (big, payload) VALUES (?, ?)').run(9223372036854775807n, new Uint8Array([0, 1, 255]));
    const result = dbRequest('/v1/db/query', 'POST', { sql: 'SELECT big, payload FROM db_browser_types' });
    assert.equal(result.status, 200);
    assert.deepEqual(result.body.rows[0].values, [
      { type: 'bigint', value: '9223372036854775807' },
      { type: 'blob', value: 'AAH/', bytes: 3 },
    ]);
    assert.doesNotThrow(() => JSON.stringify(result.body));
  });

  test('does not leave the service writer protected after a rejected query', () => {
    db.exec('CREATE TABLE db_browser_writer (id INTEGER)');
    const rejected = dbRequest('/v1/db/query', 'POST', { sql: 'DELETE FROM db_browser_writer' });
    assert.equal(rejected.status, 400);
    assert.doesNotThrow(() => db.prepare('INSERT INTO db_browser_writer VALUES (1)').run());
    assert.equal((db.prepare('SELECT COUNT(*) AS count FROM db_browser_writer').get() as { count: number }).count, 1);
  });

  test('returns generic errors instead of SQLite internals', () => {
    const result = dbRequest('/v1/db/query', 'POST', { sql: 'SELECT * FROM missing_table' });
    assert.equal(result.status, 400);
    assert.equal(result.body.code, 'query_failed');
    assert.equal(result.body.error, 'The read-only query could not be executed.');
    assert.equal(JSON.stringify(result.body).includes('missing_table'), false);
  });
});
