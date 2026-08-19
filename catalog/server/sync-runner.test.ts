/**
 * `SyncRunner.run()` — reached by `POST /v1/sync` and the six-hour scheduler —
 * against the exact defect this file exists to close: it used to run
 * `enrich()` once, from the free shared sources only, and NEVER asked a
 * provider's own detail endpoint. `sync/run.ts` (the CLI) always did, in a
 * second pass. Both now call the single `runSyncPipeline()` in
 * `sync/pipeline.ts`, and these tests prove that in the one way that matters:
 * by running `SyncRunner.run()` for real — network-free, via its injectable
 * `fetchJson`/`detailFetchers` — and checking that a detail-sourced fact
 * actually lands, then diffing the result against calling the CLI's own path
 * (`runSyncPipeline` directly) with equivalent inputs.
 */

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { openDb, type Db } from '../db/index.ts';
import { SyncRunner } from './sync-runner.ts';
import { runSyncPipeline } from '../sync/pipeline.ts';
import { loadSpecs } from '../sync/sources/models-dev.ts';
import { loadBenchmarks } from '../sync/sources/openrouter.ts';
import { ADAPTERS, BILLING } from '../sync/providers/index.ts';
import type { FetchJson } from '../sync/http.ts';
import type { ScoreProfile } from '../sync/score/venom-score.ts';
import type { ProviderDetail } from '../sync/sources/provider-detail.ts';
import { loadResolution } from '../sync/resolution-jobs.ts';

const PROFILE: ScoreProfile = {
  id: 'balanced', label: 'Balanced',
  weights: { context: 0.3, output: 0.2, capabilities: 0.3, cost: 0.2 },
};

const MODELS_DEV_URL = 'https://models.dev/api.json';
const OPENROUTER_URL = 'https://openrouter.ai/api/v1/models';

/**
 * Answers every real `ADAPTERS` roster URL and both shared-source URLs, with
 * `ollama-cloud` the only roster carrying a model — one that publishes nothing
 * on models.dev, so it is exactly the shape that needs a detail call: a row
 * only the provider's own endpoint can complete.
 */
const fakeFetchJson: FetchJson = async (url: string) => {
  if (url === MODELS_DEV_URL) return { status: 200, body: {} };
  if (url === OPENROUTER_URL) return { status: 200, body: { data: [] } };
  if (url === 'https://ollama.com/v1/models') return { status: 200, body: { data: [{ id: 'detail-needed' }] } };
  if (url === 'https://opencode.ai/zen/v1/models') return { status: 200, body: { data: [] } };
  if (url === 'https://opencode.ai/zen/go/v1/models') return { status: 200, body: { data: [] } };
  if (url === 'https://api.cline.bot/api/v1/ai/cline/recommended-models') return { status: 200, body: { clinePass: [] } };
  throw new Error(`fakeFetchJson: unexpected url ${url}`);
};

/** Stands in for `fetchOllamaDetail`, without a real POST to ollama.com. */
const fakeDetailFetchers = {
  'ollama-cloud': async (modelId: string): Promise<ProviderDetail | null> =>
    modelId === 'detail-needed' ? { contextTokens: 999_999, ref: 'fake-detail', url: 'https://ollama.test/show' } : null,
};

function makeDb(): Db {
  return openDb(':memory:');
}

describe('the service actually consults provider detail now', () => {
  test('a fact only detail can prove is established through SyncRunner.run(), the method POST /v1/sync calls', async () => {
    const db = makeDb();
    const runner = new SyncRunner({
      db, profile: PROFILE, methodologyVersion: 'venom-score-v1', identityOverlay: {},
      fetchJson: fakeFetchJson, detailFetchers: fakeDetailFetchers,
    });

    const outcome = await runner.run();

    assert.ok(outcome, 'the run must not be refused');
    assert.equal(outcome!.aborted, undefined, 'the fake shared sources are valid; this must not abort');
    assert.equal(outcome!.providers.length, ADAPTERS.length, 'every real adapter is still synced');
    // The other three fakes deliberately answer an empty roster, which layer 3
    // correctly treats as suspicious rather than "no models" — a pre-existing
    // gate, and not what this test is about. Only `ollama-cloud`, the one
    // provider carrying the row that needs detail, has to succeed here.
    const ollama = outcome!.providers.find((p) => p.provider === 'ollama-cloud');
    assert.equal(ollama?.outcome, 'ok');

    const fact = db
      .prepare(`SELECT value, source FROM model_facts WHERE provider_id='ollama-cloud' AND model_id='detail-needed' AND field='context'`)
      .get() as unknown as { value: string; source: string } | undefined;

    assert.ok(fact, 'before this fix, SyncRunner.run() never asked provider detail at all, so this row would have stayed missing');
    assert.equal(fact!.source, 'provider_api');
    assert.equal(JSON.parse(fact!.value), 999_999);
  });

  test('CLI and service produce the identical fact from the identical inputs', async () => {
    // The CLI's own shape: fetch shared sources, then call `runSyncPipeline`
    // directly — see `sync/run.ts`. Run on its own fresh database.
    const cliDb = makeDb();
    const [specs, benchmarks] = await Promise.all([loadSpecs(fakeFetchJson), loadBenchmarks(fakeFetchJson)]);
    const cliResult = await runSyncPipeline({
      db: cliDb, fetchJson: fakeFetchJson, adapters: ADAPTERS, specs, benchmarks, billing: BILLING,
      overlay: {}, profile: PROFILE, methodologyVersion: 'venom-score-v1',
      sourceFetchedAt: '2026-08-13T00:00:00.000Z', now: () => '2026-08-13T00:00:00.000Z',
      detailFetchers: fakeDetailFetchers,
    });

    // The service's shape: `SyncRunner.run()`, on a SEPARATE fresh database,
    // with the same fake inputs.
    const serviceDb = makeDb();
    const runner = new SyncRunner({
      db: serviceDb, profile: PROFILE, methodologyVersion: 'venom-score-v1', identityOverlay: {},
      fetchJson: fakeFetchJson, detailFetchers: fakeDetailFetchers,
    });
    const serviceOutcome = await runner.run();

    const factFrom = (db: Db) =>
      db
        .prepare(`SELECT value, source FROM model_facts WHERE provider_id='ollama-cloud' AND model_id='detail-needed' AND field='context'`)
        .get() as unknown as { value: string; source: string } | undefined;

    const cliFact = factFrom(cliDb);
    const serviceFact = factFrom(serviceDb);

    assert.ok(cliFact, 'precondition: the CLI path resolves the fact');
    assert.deepEqual(
      serviceFact,
      cliFact,
      'the two orchestration paths must land on the identical value and provenance for a detail-only fact',
    );

    // The comparison generalises past this one field: both paths ran the same
    // pipeline over the same rosters, so their scoring totals agree too.
    assert.deepEqual(serviceOutcome!.scoring?.levels, cliResult.scoring.levels);
    assert.equal(serviceOutcome!.providers.length, cliResult.providers.length);
  });
});

describe('targeted resolution follow-up', () => {
  test('a full sync creates a durable processing job for an unresolved published model', async () => {
    let at = new Date('2026-08-19T10:00:00.000Z');
    const db = makeDb();
    const runner = new SyncRunner({
      db, profile: PROFILE, methodologyVersion: 'venom-score-v1', identityOverlay: {},
      fetchJson: fakeFetchJson, detailFetchers: fakeDetailFetchers, now: () => at,
    });

    await runner.run();

    const resolution = loadResolution(db, 'ollama-cloud', 'detail-needed');
    assert.equal(resolution?.state, 'processing');
    assert.equal(resolution?.lastAttemptAt, '2026-08-19T10:00:00.000Z');
    assert.equal(resolution?.nextAttemptAt, '2026-08-19T10:01:00.000Z');
  });

  test('the due follow-up refreshes shared sources and detail without fetching any roster', async () => {
    let at = new Date('2026-08-19T10:00:00.000Z');
    const calls: string[] = [];
    const fetchJson: FetchJson = async (url, init) => {
      calls.push(url);
      return fakeFetchJson(url, init);
    };
    const db = makeDb();
    const runner = new SyncRunner({
      db, profile: PROFILE, methodologyVersion: 'venom-score-v1', identityOverlay: {},
      fetchJson, detailFetchers: fakeDetailFetchers, now: () => at, resolutionLockWaitMs: 0,
    });
    await runner.run();
    calls.length = 0;
    at = new Date('2026-08-19T10:01:00.000Z');

    const outcome = await runner.runResolutionPass();

    assert.equal(outcome?.attempted, 1);
    assert.deepEqual(calls.sort(), [MODELS_DEV_URL, OPENROUTER_URL].sort());
    assert.ok(!calls.includes('https://ollama.com/v1/models'), 'a targeted pass must not fetch provider rosters');
    assert.equal(loadResolution(db, 'ollama-cloud', 'detail-needed')?.nextAttemptAt, '2026-08-19T10:05:00.000Z');

    at = new Date('2026-08-19T10:05:00.000Z');
    await runner.runResolutionPass();
    assert.equal(loadResolution(db, 'ollama-cloud', 'detail-needed')?.state, 'source_incomplete');
    assert.equal(loadResolution(db, 'ollama-cloud', 'detail-needed')?.nextAttemptAt, null);
  });
});
