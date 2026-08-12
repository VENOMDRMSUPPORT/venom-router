import { test, describe, beforeEach } from 'node:test';
import assert from 'node:assert/strict';
import { openDb, type Db } from '../../db/index.ts';
import { buildIndex } from '../identity.ts';
import { enrich, type EnrichDeps } from './enrich.ts';
import type { IntrinsicFacts } from './resolvers.ts';

let db: Db;
const now = () => '2026-08-13T00:00:00.000Z';

/** A stored row, as the roster engine would have left it. */
function seed(row: {
  modelId: string;
  attachment?: number | null;
  structured?: number | null;
  contextTokens?: number | null;
  outputTokens?: number | null;
}): void {
  db.prepare(
    `INSERT INTO models (provider_id, model_id, context_tokens, output_tokens,
       tools, reasoning, structured, attachment, status, first_seen_at, last_seen_at)
     VALUES ('p', ?, ?, ?, 1, 1, ?, ?, 'active', ?, ?)`,
  ).run(
    row.modelId,
    row.contextTokens ?? 128_000,
    row.outputTokens ?? 32_000,
    row.structured ?? null,
    row.attachment ?? null,
    now(),
    now(),
  );
}

const deps = (over: Partial<EnrichDeps> = {}): EnrichDeps => ({
  db,
  intrinsic: () => null,
  canonical: { index: buildIndex([]), byId: new Map() },
  overlay: {},
  billing: { p: 'per_token' },
  now,
  ...over,
});

const factFor = (modelId: string, field: string) =>
  db
    .prepare('SELECT value, source, source_ref FROM model_facts WHERE provider_id=? AND model_id=? AND field=?')
    .get('p', modelId, field) as unknown as { value: string; source: string; source_ref: string } | undefined;

const storedAttachment = (modelId: string) =>
  (db.prepare('SELECT attachment FROM models WHERE model_id=?').get(modelId) as unknown as { attachment: number | null })
    .attachment;

beforeEach(() => {
  db = openDb(':memory:');
  db.prepare(`INSERT INTO providers (id, name, roster_url) VALUES ('p', 'Provider', 'https://example.test')`).run();
});

describe('attachment is resolved and recorded like every other capability', () => {
  test('a value the provider feed published gets a provenance row', () => {
    // The engine already wrote `attachment` from the feed. Without a fact row
    // the number is displayed and traceable to nothing.
    seed({ modelId: 'from-feed', attachment: 1 });

    enrich(deps());

    const fact = factFor('from-feed', 'attachment');
    assert.ok(fact, 'expected a model_facts row for attachment');
    assert.equal(fact.value, 'true');
    assert.equal(fact.source, 'models.dev');
  });

  test('a unanimous pooled declaration settles a null, including a negative', () => {
    // The hy3-preview case: this provider publishes no entry, one other seller
    // declares attachment=false and nobody contradicts it. Explicit negative
    // evidence is allowed to produce false.
    seed({ modelId: 'pooled-false', attachment: null });
    const intrinsic: IntrinsicFacts = {
      attachment: false,
      declaredBy: 'other-seller/pooled-false',
      conflicts: [],
    };

    enrich(deps({ intrinsic: () => intrinsic }));

    assert.equal(storedAttachment('pooled-false'), 0);
    const fact = factFor('pooled-false', 'attachment');
    assert.ok(fact, 'expected a model_facts row for attachment');
    assert.equal(fact.value, 'false');
    assert.match(fact.source_ref, /other-seller\/pooled-false/);
  });

  test('a seller disagreement leaves attachment unknown, never guessed', () => {
    seed({ modelId: 'conflicted', attachment: null });
    const intrinsic: IntrinsicFacts = { declaredBy: '', conflicts: ['attachment'] };

    enrich(deps({ intrinsic: () => intrinsic }));

    assert.equal(storedAttachment('conflicted'), null);
    assert.equal(factFor('conflicted', 'attachment'), undefined);
  });

  test('a modality list never stands in for attachment', () => {
    // `modalities` and `attachment` are different questions. An index that says
    // the model accepts images has not said it accepts file attachments.
    seed({ modelId: 'image-model', attachment: null });
    const canonical = {
      index: buildIndex([{ id: 'up/image-model' }]),
      byId: new Map([
        ['up/image-model', { id: 'up/image-model', inputModalities: ['text', 'image'], supportedParameters: ['tools'] }],
      ]),
    };

    enrich(deps({ canonical }));

    assert.equal(storedAttachment('image-model'), null);
    assert.equal(factFor('image-model', 'attachment'), undefined);
  });
});
