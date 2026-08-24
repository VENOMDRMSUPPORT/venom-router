import { test, describe, beforeEach } from 'node:test';
import assert from 'node:assert/strict';
import { openDb } from '../db/index.ts';
import { syncProvider } from './engine.ts';
import { beginResolutionWindow } from './resolution-jobs.ts';
import type { ProviderAdapter, SyncDeps } from './engine.ts';
import { FetchFailure } from './http.ts';
import type { Db } from '../db/index.ts';

const ADAPTER: ProviderAdapter = {
  id: 'test-provider',
  name: 'Test Provider',
  rosterUrl: 'https://example.test/v1/models',
  feedKey: 'test',
  parseRoster(body) {
    const b = body as { data?: { id: string }[] };
    if (!Array.isArray(b?.data)) throw new Error('expected {data:[...]}');
    return b.data.map((m) => m.id);
  },
};

let db: Db;
let clock = 0;
const now = () => new Date(Date.UTC(2026, 0, 1 + clock++)).toISOString();

const deps = (roster: string[] | Error, over: Partial<SyncDeps> = {}): SyncDeps => ({
  db,
  now,
  lookupSpec: () => ({ contextTokens: 128_000, outputTokens: 32_000, tools: true }),
  fetchJson: async () => {
    if (roster instanceof Error) throw roster;
    return { status: 200, body: { data: roster.map((id) => ({ id })) } };
  },
  ...over,
});

const activeIds = () =>
  (db.prepare(`SELECT model_id FROM models WHERE status='active' ORDER BY model_id`).all() as unknown as { model_id: string }[]).map((r) => r.model_id);
const statusOf = (id: string) =>
  (db.prepare('SELECT status, miss_count FROM models WHERE model_id = ?').get(id) as unknown as { status: string; miss_count: number } | undefined);

beforeEach(() => {
  db = openDb(':memory:');
  clock = 0;
});

describe('happy path', () => {
  test('inserts the roster and records an ok run', async () => {
    const r = await syncProvider(ADAPTER, deps(['a', 'b', 'c']));
    assert.equal(r.outcome, 'ok');
    assert.deepEqual(activeIds(), ['a', 'b', 'c']);
    assert.deepEqual(r.added, ['a', 'b', 'c']);
  });

  test('an addition is never gated — a provider launching models is not a failure', async () => {
    await syncProvider(ADAPTER, deps(['a']));
    const r = await syncProvider(ADAPTER, deps(['a', 'b', 'c', 'd', 'e', 'f', 'g', 'h']));
    assert.equal(r.outcome, 'ok');
    assert.equal(activeIds().length, 8);
  });

  test('records an added event per new model', async () => {
    await syncProvider(ADAPTER, deps(['a', 'b']));
    const n = db.prepare(`SELECT COUNT(*) c FROM model_events WHERE kind='added'`).get() as unknown as { c: number };
    assert.equal(n.c, 2);
  });
});

describe('layer 2/3 — a failed or malformed fetch writes nothing', () => {
  test('a fetch failure leaves the catalog untouched', async () => {
    await syncProvider(ADAPTER, deps(['a', 'b', 'c']));
    const before = activeIds();
    const beforeStatus = statusOf('a');
    const r = await syncProvider(ADAPTER, deps(new FetchFailure('boom', 503, 3)));
    assert.equal(r.outcome, 'failed');
    assert.deepEqual(activeIds(), before, 'a provider outage must not change stored data');
    assert.deepEqual(statusOf('a'), beforeStatus, 'a failed fetch must not count as absence');
  });

  test('a shape the parser does not recognise is rejected whole', async () => {
    await syncProvider(ADAPTER, deps(['a', 'b']));
    const r = await syncProvider(ADAPTER, deps([], { fetchJson: async () => ({ status: 200, body: { items: [] } }) }));
    assert.equal(r.outcome, 'failed');
    assert.deepEqual(activeIds(), ['a', 'b']);
  });

  test('a failed run is recorded rather than swallowed', async () => {
    await syncProvider(ADAPTER, deps(new FetchFailure('boom', 500, 3)));
    const run = db.prepare(`SELECT outcome, error FROM sync_runs ORDER BY id DESC LIMIT 1`).get() as unknown as { outcome: string; error: string };
    assert.equal(run.outcome, 'failed');
    assert.match(run.error, /boom/);
  });
});

describe('layer 4 — the delta gate', () => {
  test('an HTTP 200 with an empty roster is rejected, not obeyed', async () => {
    await syncProvider(ADAPTER, deps(['a', 'b', 'c']));
    const r = await syncProvider(ADAPTER, deps([], { fetchJson: async () => ({ status: 200, body: { data: [] } }) }));
    assert.equal(r.outcome, 'failed'); // an empty roster fails validation before the gate
    assert.deepEqual(activeIds(), ['a', 'b', 'c']);
  });

  test('a mass deletion is quarantined and not applied', async () => {
    await syncProvider(ADAPTER, deps(['a', 'b', 'c', 'd', 'e', 'f', 'g', 'h', 'i', 'j']));
    const r = await syncProvider(ADAPTER, deps(['a']));
    assert.equal(r.outcome, 'quarantined');
    assert.match(r.quarantineReason!, /would remove 9\/10/);
    assert.equal(activeIds().length, 10, 'quarantined runs must change nothing');
  });

  test('a quarantined run is recorded with its reason', async () => {
    await syncProvider(ADAPTER, deps(['a', 'b', 'c', 'd', 'e', 'f', 'g', 'h', 'i', 'j']));
    await syncProvider(ADAPTER, deps(['a']));
    const run = db.prepare(`SELECT outcome, error FROM sync_runs ORDER BY id DESC LIMIT 1`).get() as unknown as { outcome: string; error: string };
    assert.equal(run.outcome, 'quarantined');
    assert.match(run.error, /gate/);
  });

  test('a small removal passes the gate', async () => {
    await syncProvider(ADAPTER, deps(['a', 'b', 'c', 'd', 'e', 'f', 'g', 'h', 'i', 'j']));
    const r = await syncProvider(ADAPTER, deps(['a', 'b', 'c', 'd', 'e', 'f', 'g', 'h', 'i']));
    assert.equal(r.outcome, 'ok');
  });
});

describe('layer 5 — first-miss retirement', () => {
  const ten = ['a', 'b', 'c', 'd', 'e', 'f', 'g', 'h', 'i', 'j'];

  test('one absence retires immediately on a successful roster', async () => {
    await syncProvider(ADAPTER, deps(ten));
    await syncProvider(ADAPTER, deps(ten.filter((x) => x !== 'j')));
    assert.equal(statusOf('j')!.status, 'retired');
    assert.equal(statusOf('j')!.miss_count, 1);
  });

  test('a reintroduced model becomes active again with a fresh existence event', async () => {
    await syncProvider(ADAPTER, deps(ten));
    await syncProvider(ADAPTER, deps(ten.filter((x) => x !== 'j')));
    await syncProvider(ADAPTER, deps(ten));
    const j = statusOf('j')!;
    assert.equal(j.status, 'active');
    assert.equal(j.miss_count, 0);
    const n = db.prepare(`SELECT COUNT(*) c FROM model_events WHERE model_id='j' AND kind='added'`).get() as unknown as { c: number };
    assert.equal(n.c, 2, 'the reintroduced roster listing is a new existence declaration');
  });

  test('the configured multi-miss path still parks in missing and still records a readded event', async () => {
    // `retireAfterMisses` is still a supported option, so `missing` and the
    // `readded` event are still live code — just off the default path. Deleting
    // the coverage with the default would leave that branch unpinned, and it is
    // the branch a future operator turns on when a provider's roster flaps.
    const strikes = { options: { retireAfterMisses: 3 } };
    await syncProvider(ADAPTER, deps(ten, strikes));
    await syncProvider(ADAPTER, deps(ten.filter((x) => x !== 'j'), strikes));
    assert.equal(statusOf('j')!.status, 'missing', 'one absence of three is not a retirement');

    await syncProvider(ADAPTER, deps(ten, strikes));
    assert.equal(statusOf('j')!.status, 'active');
    const n = db.prepare(`SELECT COUNT(*) c FROM model_events WHERE model_id='j' AND kind='readded'`).get() as unknown as { c: number };
    assert.equal(n.c, 1, 'missing -> active is a reappearance, not a fresh arrival');
  });

  test('retirement terminalizes a processing resolution job in the same run', async () => {
    await syncProvider(ADAPTER, deps(ten));
    beginResolutionWindow(db, '2026-01-02T00:00:00.000Z');
    await syncProvider(ADAPTER, deps(ten.filter((x) => x !== 'j')));

    const job = db.prepare(`
      SELECT status, reasons_json, next_attempt_at
      FROM resolution_jobs WHERE provider_id='test-provider' AND model_id='j'
    `).get() as unknown as { status: string; reasons_json: string; next_attempt_at: string | null };
    assert.equal(job.status, 'complete');
    assert.equal(job.reasons_json, '[]');
    assert.equal(job.next_attempt_at, null);
  });
});

describe('layer 7 — nothing is ever physically deleted', () => {
  test('a retired model keeps its row and its history', async () => {
    const ten = ['a', 'b', 'c', 'd', 'e', 'f', 'g', 'h', 'i', 'j'];
    const without = ten.filter((x) => x !== 'j');
    await syncProvider(ADAPTER, deps(ten));
    await syncProvider(ADAPTER, deps(without));
    const row = db.prepare('SELECT status, first_seen_at FROM models WHERE model_id = ?').get('j') as unknown as { status: string; first_seen_at: string };
    assert.equal(row.status, 'retired');
    assert.ok(row.first_seen_at, 'first_seen_at survives retirement');
  });
});

describe('layer 1 — providers are isolated', () => {
  test("one provider's outage does not touch another's data", async () => {
    const other: ProviderAdapter = { ...ADAPTER, id: 'other', name: 'Other' };
    await syncProvider(ADAPTER, deps(['a', 'b']));
    await syncProvider(other, deps(['x', 'y']));
    await syncProvider(ADAPTER, deps(new FetchFailure('down', 503, 3)));
    const otherRows = db.prepare(`SELECT COUNT(*) c FROM models WHERE provider_id='other' AND status='active'`).get() as unknown as { c: number };
    assert.equal(otherRows.c, 2);
  });
});

describe('display names', () => {
  test('a reviewed name wins over the spec feed transcription', async () => {
    await syncProvider(ADAPTER, deps(['a'], {
      lookupSpec: () => ({ displayName: 'Feed Spelling' }),
      displayNameFor: (_providerId, modelId) => (modelId === 'a' ? 'Provider Spelling' : undefined),
    }));
    const row = db.prepare('SELECT display_name FROM models WHERE model_id = ?').get('a') as unknown as { display_name: string };
    assert.equal(row.display_name, 'Provider Spelling');
  });

  test('without a review the feed name stands, and without a feed the id stands', async () => {
    await syncProvider(ADAPTER, deps(['a', 'b'], {
      lookupSpec: (_feedKey, modelId) => (modelId === 'a' ? { displayName: 'Feed Spelling' } : null),
    }));
    const names = Object.fromEntries(
      (db.prepare('SELECT model_id, display_name FROM models').all() as unknown as { model_id: string; display_name: string }[])
        .map((r) => [r.model_id, r.display_name]),
    );
    assert.equal(names.a, 'Feed Spelling');
    assert.equal(names.b, 'b');
  });
});

/**
 * The change ledger's whole promise is "a sync that finds nothing different adds
 * nothing here". It was broken for every field a LATER stage owns: the diff read
 * the same mutable column enrich rewrites, so the feed's value was compared
 * against the resolved value and re-reported as a change on every single run.
 * Live evidence at the time of the fix: `cost_out_per_m null -> 1.6` recorded 13
 * times for one ClinePass row, one distinct from/to pair, and 298 of 459 events
 * in the feed were this. The diff has to compare the feed against WHAT THE FEED
 * LAST SAID, never against a column somebody else is authoritative over.
 */
describe('the change ledger only reports what actually changed upstream', () => {
  const priced = (over: Partial<SyncDeps> = {}) =>
    deps(['a'], { lookupSpec: () => ({ contextTokens: 128_000, outputTokens: 32_000, costInPerM: 0.8, costOutPerM: 1.6 }), ...over });
  const priceEvents = () =>
    (db.prepare(`SELECT field, old_value, new_value FROM model_events WHERE kind='changed' AND reason='price'`)
      .all() as unknown as { field: string; old_value: string | null; new_value: string | null }[])
      // node:sqlite rows have a null prototype, which deepEqual will not match
      // against a literal. Re-shaped so the assertion is about the values.
      .map((r) => ({ field: r.field, from: r.old_value, to: r.new_value }));

  test('an unchanged feed produces no event even after enrich rewrites the effective price', async () => {
    await syncProvider(ADAPTER, priced());
    // What enrich does to a subscription provider: the per-token number is not
    // what this provider charges, so it moves to the reference columns and the
    // effective ones become NULL.
    db.prepare(`UPDATE models SET cost_in_per_m = NULL, cost_out_per_m = NULL,
                                  ref_cost_in_per_m = 0.8, ref_cost_out_per_m = 1.6, cost_kind = 'included'`).run();

    await syncProvider(ADAPTER, priced());

    assert.deepEqual(priceEvents(), []);
  });

  test('an unchanged feed produces no event after enrich adopts a reviewed output limit', async () => {
    await syncProvider(ADAPTER, deps(['a'], { lookupSpec: () => ({ contextTokens: 128_000, outputTokens: 1_048_576 }) }));
    // A reviewed fact overriding the feed's limit — the ollama-cloud case.
    db.prepare(`UPDATE models SET output_tokens = 384000`).run();

    await syncProvider(ADAPTER, deps(['a'], { lookupSpec: () => ({ contextTokens: 128_000, outputTokens: 1_048_576 }) }));

    const fields = (db.prepare(`SELECT field FROM model_events WHERE kind='changed' AND reason='context'`)
      .all() as unknown as { field: string }[]).map((r) => r.field);
    assert.deepEqual(fields, []);
  });

  test('a real upstream price change is still reported, once', async () => {
    await syncProvider(ADAPTER, priced());
    db.prepare(`UPDATE models SET cost_in_per_m = NULL, cost_out_per_m = NULL, cost_kind = 'included'`).run();

    await syncProvider(ADAPTER, deps(['a'], { lookupSpec: () => ({ contextTokens: 128_000, outputTokens: 32_000, costInPerM: 0.8, costOutPerM: 2.4 }) }));

    assert.deepEqual(priceEvents(), [{ field: 'cost_out_per_m', from: '1.6', to: '2.4' }]);
  });
});
