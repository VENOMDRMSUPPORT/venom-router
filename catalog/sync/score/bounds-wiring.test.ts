/**
 * A reviewed bound reaches the database, or it is decoration.
 *
 * `computeVQ` has carried a `bounded` branch since the start and nothing ever
 * populated `evidence.bound`, so the feature existed in the type system and
 * nowhere else. That is the failure this file exists to catch: the unit test for
 * the branch passes either way.
 */

import { test, describe, beforeEach } from 'node:test';
import assert from 'node:assert/strict';
import { openDb, type Db } from '../../db/index.ts';
import { scoreAll } from './pipeline.ts';
import { buildIndex } from '../identity.ts';
import type { BenchmarkSource } from '../sources/openrouter.ts';
import type { ScoreProfile } from './venom-score.ts';

const PROFILE: ScoreProfile = {
  id: 'balanced', label: 'Balanced',
  weights: { context: 0.3, output: 0.2, capabilities: 0.3, cost: 0.2 },
};

/** An index that does NOT carry the model under test — the whole premise. */
function benchmarks(): BenchmarkSource {
  const records = [{ id: 'z-ai/glm-5.2', vendor: 'z-ai', intelligence: 52.6, designElo: 1250 }];
  return { index: buildIndex(records), byId: new Map(records.map((r) => [r.id, r])), count: records.length } as BenchmarkSource;
}

let db: Db;
const now = () => '2026-08-18T00:00:00.000Z';

beforeEach(() => {
  db = openDb(':memory:');
  db.prepare(
    `INSERT INTO providers (id, name, roster_url, feed_key) VALUES ('clinepass','ClinePass','https://x.invalid','cline-pass')`,
  ).run();
  db.prepare(
    `INSERT INTO models (provider_id, model_id, context_tokens, output_tokens, tools, reasoning,
       structured, attachment, cost_kind, status, first_seen_at, last_seen_at)
     VALUES ('clinepass','cline-pass/glm-5.3',1000000,131072,1,1,1,0,'included','active',?,?)`,
  ).run(now(), now());
});

const score = (bounds = {}) =>
  scoreAll({
    db, benchmarks: benchmarks(), overlay: {}, profile: PROFILE, bounds,
    methodologyVersion: 'venom-score-v2', sourceFetchedAt: now(), now,
  });

const vqRow = () =>
  db.prepare(`SELECT value, bound, evidence_level, source, source_model_id, unrated_reason
              FROM model_scores WHERE model_id='cline-pass/glm-5.3' AND kind='VQ'`).get() as unknown as
    { value: number | null; bound: string | null; evidence_level: string; source: string | null; source_model_id: string | null; unrated_reason: string | null };

describe('a reviewed bound reaches the stored score', () => {
  test('without one, the row is unrated — the state this catalog was in', () => {
    score();
    const r = vqRow();
    assert.equal(r.value, null);
    assert.equal(r.evidence_level, 'unrated');
    assert.equal(r.unrated_reason, 'identity_unresolved');
  });

  test('with one, the row carries the figure as a bound', () => {
    score({
      'cline-pass/glm-5.3': {
        value: 52.6, side: 'lower', referenceModel: 'z-ai/glm-5.2',
        reason: 'successor to glm-5.2, which is measured at 52.6', evidence: ['x'],
      },
    });
    const r = vqRow();
    assert.equal(r.value, 52.6);
    assert.equal(r.bound, 'lower');
    assert.equal(r.evidence_level, 'bounded');
    assert.match(r.source!, /relation/);
    assert.equal(r.unrated_reason, null);
  });

  test('and it still does not become an identity', () => {
    // `read-model.ts` reads `canonicalId` off this column. A bound that filled
    // it would publish "this row IS z-ai/glm-5.2" — the claim the whole
    // mechanism exists to avoid having to make.
    score({
      'cline-pass/glm-5.3': {
        value: 52.6, side: 'lower', referenceModel: 'z-ai/glm-5.2',
        reason: 'successor to glm-5.2, which is measured at 52.6', evidence: ['x'],
      },
    });
    assert.equal(vqRow().source_model_id, null);
  });

  test('a bound for a model nobody serves changes nothing', () => {
    score({ 'not/served': { value: 90, side: 'lower', referenceModel: 'a/b', reason: 'r', evidence: [] } });
    assert.equal(vqRow().value, null);
  });
});
