import { test, describe, beforeEach } from 'node:test';
import assert from 'node:assert/strict';
import { openDb, type Db } from '../db/index.ts';
import { applyPublishPolicy } from './publish-policy.ts';
import { loadModels } from '../server/read-model.ts';
import type { ProviderAdapter, ModelSpec, SpecLookup } from './engine.ts';

let db: Db;
const now = () => '2026-08-13T00:00:00.000Z';

/** A free-only provider, exactly as OpenCode Zen is declared. */
const ZEN: ProviderAdapter = {
  id: 'opencode-zen',
  name: 'OpenCode Zen',
  rosterUrl: 'https://example.test/zen',
  feedKey: 'opencode',
  parseRoster: () => [],
  publishPolicy: 'free_only',
};

/** A prices map keyed by model id, standing in for models.dev. */
function lookupFrom(prices: Record<string, ModelSpec | null>): SpecLookup {
  return (_feedKey, modelId) => prices[modelId] ?? null;
}

/** Store a rostered model exactly as the engine leaves it: active, no reason. */
function seed(modelId: string): void {
  db.prepare(
    `INSERT INTO models (provider_id, model_id, status, first_seen_at, last_seen_at)
     VALUES ('opencode-zen', ?, 'active', ?, ?)`,
  ).run(modelId, now(), now());
}

const statusOf = (modelId: string) => {
  const r = db.prepare(`SELECT status, exclusion_reason FROM models WHERE model_id=?`).get(modelId) as unknown as
    | { status: string; exclusion_reason: string | null }
    | undefined;
  // node:sqlite returns null-prototype rows; rebuild as a plain object so
  // assert.deepEqual against a literal compares by value, not by prototype.
  return r ? { status: r.status, exclusion_reason: r.exclusion_reason } : undefined;
};

const events = () =>
  db.prepare(`SELECT model_id, kind, reason FROM model_events ORDER BY id`).all() as unknown as
    { model_id: string; kind: string; reason: string | null }[];

const apply = (prices: Record<string, ModelSpec | null>) =>
  applyPublishPolicy({ db, adapters: [ZEN], lookupSpec: lookupFrom(prices), now });

beforeEach(() => {
  db = openDb(':memory:');
  db.prepare(`INSERT INTO providers (id, name, roster_url) VALUES ('opencode-zen', 'OpenCode Zen', 'https://example.test/zen')`).run();
});

describe('a free-only provider publishes only models proven free', () => {
  test('a paid model is excluded with reason paid, and never reaches the published roster', () => {
    seed('gpt-5.5');
    apply({ 'gpt-5.5': { costInPerM: 1.25, costOutPerM: 10 } });

    assert.deepEqual(statusOf('gpt-5.5'), { status: 'excluded', exclusion_reason: 'paid' });
    const published = loadModels(db).map((m) => m.modelId);
    assert.ok(!published.includes('gpt-5.5'), 'a paid model must not be in the active published roster');
  });

  test('a proven-free model (a published zero price) stays active and published', () => {
    seed('deepseek-v4-flash-free');
    apply({ 'deepseek-v4-flash-free': { costInPerM: 0, costOutPerM: 0 } });

    assert.deepEqual(statusOf('deepseek-v4-flash-free'), { status: 'active', exclusion_reason: null });
    assert.ok(loadModels(db).map((m) => m.modelId).includes('deepseek-v4-flash-free'));
  });

  test('a free model the provider no longer serves (missing) is withheld as not_served', () => {
    // A free tier must not advertise a model nobody can call. Even a proven-free
    // model that has dropped out of the roster is withheld — deterministically,
    // not left to age out over three syncs.
    db.prepare(
      `INSERT INTO models (provider_id, model_id, status, first_seen_at, last_seen_at, miss_count)
       VALUES ('opencode-zen', 'ling-tiny-free', 'missing', ?, ?, 2)`,
    ).run(now(), now());
    apply({ 'ling-tiny-free': { costInPerM: 0, costOutPerM: 0 } });

    assert.deepEqual(statusOf('ling-tiny-free'), { status: 'excluded', exclusion_reason: 'not_served' });
    assert.ok(!loadModels(db).map((m) => m.modelId).includes('ling-tiny-free'), 'a dropped model is not in the published roster');
  });

  test('a model with no published price is excluded conservatively as not_proven_free', () => {
    seed('mystery');
    apply({ mystery: null });
    assert.deepEqual(statusOf('mystery'), { status: 'excluded', exclusion_reason: 'not_proven_free' });
  });

  test('a partial price (only one side, or a zero paired with an absent) is not proven free', () => {
    seed('half-priced');
    apply({ 'half-priced': { costInPerM: 0 } }); // costOutPerM absent
    assert.deepEqual(statusOf('half-priced'), { status: 'excluded', exclusion_reason: 'not_proven_free' });
  });

  test('only the free ones survive when a whole roster is mixed', () => {
    const roster: Record<string, ModelSpec | null> = {
      'free-a': { costInPerM: 0, costOutPerM: 0 },
      'free-b': { costInPerM: 0, costOutPerM: 0 },
      'paid-a': { costInPerM: 3, costOutPerM: 9 },
      'paid-b': { costInPerM: 0.5, costOutPerM: 1.5 },
      'unpriced-a': null,
    };
    for (const id of Object.keys(roster)) seed(id);
    apply(roster);

    const published = loadModels(db).map((m) => m.modelId).sort();
    assert.deepEqual(published, ['free-a', 'free-b'], 'only proven-free models are published');
  });
});

describe('exclusion preserves history and never re-enters the published roster', () => {
  test('an excluded row is not deleted — it survives in the models table', () => {
    seed('paid');
    apply({ paid: { costInPerM: 2, costOutPerM: 8 } });
    const row = db.prepare(`SELECT model_id, status FROM models WHERE model_id='paid'`).get() as unknown as
      { model_id: string; status: string };
    assert.equal(row.model_id, 'paid');
    assert.equal(row.status, 'excluded');
  });

  test('a later sync re-asserts the exclusion silently — no re-entry, no duplicate event', () => {
    seed('paid');
    apply({ paid: { costInPerM: 2, costOutPerM: 8 } });
    // Simulate the engine reactivating the rostered model before the next policy pass.
    db.prepare(`UPDATE models SET status='active' WHERE model_id='paid'`).run();
    apply({ paid: { costInPerM: 2, costOutPerM: 8 } });

    assert.deepEqual(statusOf('paid'), { status: 'excluded', exclusion_reason: 'paid' });
    const excl = events().filter((e) => e.kind === 'excluded');
    assert.equal(excl.length, 1, 'the exclusion is recorded once, not on every sync');
    assert.ok(!loadModels(db).map((m) => m.modelId).includes('paid'));
  });

  test('a model that becomes free is restored to the published roster', () => {
    seed('was-paid');
    apply({ 'was-paid': { costInPerM: 2, costOutPerM: 8 } });
    assert.equal(statusOf('was-paid')!.status, 'excluded');

    apply({ 'was-paid': { costInPerM: 0, costOutPerM: 0 } });
    assert.deepEqual(statusOf('was-paid'), { status: 'active', exclusion_reason: null });
    assert.ok(loadModels(db).map((m) => m.modelId).includes('was-paid'));
    assert.ok(events().some((e) => e.kind === 'readded'));
  });
});

describe('the policy only touches free-only providers', () => {
  test('a provider with no publish policy keeps every model, priced or not', () => {
    db.prepare(`INSERT INTO providers (id, name, roster_url) VALUES ('opencode-go', 'OpenCode Go', 'https://example.test/go')`).run();
    db.prepare(
      `INSERT INTO models (provider_id, model_id, status, first_seen_at, last_seen_at)
       VALUES ('opencode-go', 'paid-go', 'active', ?, ?)`,
    ).run(now(), now());

    const go: ProviderAdapter = { id: 'opencode-go', name: 'OpenCode Go', rosterUrl: 'x', feedKey: 'opencode-go', parseRoster: () => [] };
    applyPublishPolicy({ db, adapters: [go], lookupSpec: lookupFrom({ 'paid-go': { costInPerM: 2, costOutPerM: 8 } }), now });

    assert.equal(statusOf('paid-go')!.status, 'active', 'a paid model at a normal provider stays published');
  });

  test('an explicitly plan-gated Ollama offer is excluded while other free offers remain published', () => {
    db.prepare(`INSERT INTO providers (id, name, roster_url) VALUES ('ollama-cloud', 'Ollama Cloud', 'https://ollama.com/v1/models')`).run();
    for (const modelId of ['kimi-k3', 'gpt-oss:20b']) {
      db.prepare(
        `INSERT INTO models (provider_id, model_id, status, first_seen_at, last_seen_at)
         VALUES ('ollama-cloud', ?, 'active', ?, ?)`,
      ).run(modelId, now(), now());
    }

    const ollama: ProviderAdapter = {
      id: 'ollama-cloud', name: 'Ollama Cloud', rosterUrl: 'https://ollama.com/v1/models',
      feedKey: 'ollama-cloud', parseRoster: () => [],
      publishExclusions: { 'kimi-k3': 'plan_required' },
    };
    applyPublishPolicy({ db, adapters: [ollama], lookupSpec: lookupFrom({}), now });

    const rows = db.prepare(
      `SELECT model_id, status, exclusion_reason FROM models WHERE provider_id='ollama-cloud' ORDER BY model_id`,
    ).all() as unknown as { model_id: string; status: string; exclusion_reason: string | null }[];
    assert.deepEqual(rows.map((row) => ({ ...row })), [
      { model_id: 'gpt-oss:20b', status: 'active', exclusion_reason: null },
      { model_id: 'kimi-k3', status: 'excluded', exclusion_reason: 'plan_required' },
    ]);
    assert.deepEqual(loadModels(db).filter((model) => model.providerId === 'ollama-cloud').map((model) => model.modelId), ['gpt-oss:20b']);
  });
});

describe("a provider's own free listing is the proof when configured", () => {
  const ZEN_OFFICIAL: ProviderAdapter = {
    ...ZEN,
    officialFreeList: {
      ids: ['listed-free'],
      reviewedAt: '2026-08-21',
      sourceUrl: 'https://official.example/docs',
    },
  };
  const applyOfficial = (prices: Record<string, ModelSpec | null>) =>
    applyPublishPolicy({ db, adapters: [ZEN_OFFICIAL], lookupSpec: lookupFrom(prices), now });

  test('an id the provider lists as free is published even when the feed disagrees', () => {
    seed('listed-free');
    applyOfficial({ 'listed-free': { costInPerM: 0.5, costOutPerM: 2 } });
    assert.deepEqual(statusOf('listed-free'), { status: 'active', exclusion_reason: null });
  });

  test('an id the provider does not list is withheld even at a transcribed zero price', () => {
    seed('unlisted-zero');
    applyOfficial({ 'unlisted-zero': { costInPerM: 0, costOutPerM: 0 } });
    assert.deepEqual(statusOf('unlisted-zero'), { status: 'excluded', exclusion_reason: 'not_proven_free' });
    assert.ok(!loadModels(db).map((m) => m.modelId).includes('unlisted-zero'));
  });

  test('naming the id later restores it to the published roster', () => {
    seed('unlisted-zero');
    applyOfficial({ 'unlisted-zero': { costInPerM: 0, costOutPerM: 0 } });
    const restored = applyPublishPolicy({
      db,
      adapters: [{
        ...ZEN_OFFICIAL,
        officialFreeList: { ids: ['listed-free', 'unlisted-zero'], reviewedAt: '2026-08-21', sourceUrl: 'https://official.example/docs' },
      }],
      lookupSpec: lookupFrom({ 'unlisted-zero': { costInPerM: 0, costOutPerM: 0 } }),
      now,
    });
    assert.deepEqual(statusOf('unlisted-zero'), { status: 'active', exclusion_reason: null });
    assert.equal(restored.restored, 1);
  });
});
