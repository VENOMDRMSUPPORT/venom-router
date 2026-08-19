import { describe, test } from 'node:test';
import assert from 'node:assert/strict';
import { openDb, type Db } from '../db/index.ts';
import { enrich } from './enrich/enrich.ts';
import { buildIndex } from './identity.ts';
import type { ReviewedFacts } from './reviewed-facts.ts';

const AT = '2026-08-19T10:00:00.000Z';
const fact = <T>(value: T) => ({
  value,
  ref: 'official.model.field',
  sourceUrl: 'https://official.example/model',
  evidence: ['Official documentation states this value.'],
  reviewedAt: '2026-08-19',
});

function seed(db: Db) {
  for (const provider of ['a', 'b']) {
    db.prepare(`INSERT INTO providers (id, name, roster_url) VALUES (?, ?, ?)`)
      .run(provider, provider, `https://${provider}.test/models`);
    db.prepare(`INSERT INTO models (
      provider_id, model_id, display_name, status, first_seen_at, last_seen_at, miss_count
    ) VALUES (?, 'shared-model', 'shared-model', 'active', ?, ?, 0)`).run(provider, AT, AT);
  }
}

function run(db: Db, reviewedFacts: ReviewedFacts, details = new Map()) {
  return enrich({
    db,
    canonical: { index: buildIndex([]), byId: new Map() },
    overlay: {},
    billing: {
      a: { model: 'free_quota', evidenceUrl: 'https://a.test/pricing', note: 'free' },
      b: { model: 'free_quota', evidenceUrl: 'https://b.test/pricing', note: 'free' },
    },
    lookupSpec: () => null,
    intrinsic: () => null,
    reviewedFacts,
    details,
    now: () => AT,
  });
}

describe('reviewed facts enrichment', () => {
  test('fills every supported field only on the exact provider/model offering', () => {
    const db = openDb(':memory:');
    seed(db);
    run(db, {
      'a/shared-model': {
        context: fact(1_000_000),
        maxOutput: fact(128_000),
        inputModalities: fact(['text', 'image']),
        tools: fact(true),
        reasoning: fact(true),
        structured: fact(false),
        attachment: fact(true),
      },
    });

    const a = db.prepare(`SELECT * FROM models WHERE provider_id='a' AND model_id='shared-model'`).get() as any;
    const b = db.prepare(`SELECT * FROM models WHERE provider_id='b' AND model_id='shared-model'`).get() as any;
    assert.equal(a.context_tokens, 1_000_000);
    assert.equal(a.output_tokens, 128_000);
    assert.equal(a.input_modalities, '["text","image"]');
    assert.equal(a.tools, 1);
    assert.equal(a.reasoning, 1);
    assert.equal(a.structured, 0);
    assert.equal(a.attachment, 1);
    assert.equal(b.context_tokens, null, 'a reviewed fact must never leak to another provider');
    assert.equal(b.structured, null);

    const sources = db.prepare(`SELECT DISTINCT source FROM model_facts WHERE provider_id='a'`).all() as any[];
    assert.ok(sources.some((row) => row.source === 'reviewed_source'));
  });

  test('withholds a value when provider detail contradicts a reviewed official source', () => {
    const db = openDb(':memory:');
    seed(db);
    run(
      db,
      { 'a/shared-model': { context: fact(1_000_000) } },
      new Map([['a/shared-model', { contextTokens: 128_000, ref: 'provider-detail', url: 'https://a.test/model' }]]),
    );

    const model = db.prepare(`SELECT context_tokens FROM models WHERE provider_id='a' AND model_id='shared-model'`).get() as any;
    const conflict = db.prepare(`SELECT * FROM model_conflicts WHERE provider_id='a' AND model_id='shared-model' AND field='context'`).get() as any;
    assert.equal(model.context_tokens, null);
    assert.equal(conflict.conflict_type, 'official_source_disagreement');
    assert.deepEqual(JSON.parse(conflict.sides_json).map((side: any) => side.value), [128_000, 1_000_000]);
  });
});
