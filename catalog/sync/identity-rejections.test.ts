import { test, describe, beforeEach } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { openDb, type Db } from '../db/index.ts';
import { parseRejections, ingestRejections, type RejectionOverlay } from './identity-rejections.ts';
import { loadModels, loadMeta } from '../server/read-model.ts';

const HERE = dirname(fileURLToPath(import.meta.url));
const OVERLAY_PATH = join(HERE, '..', 'overlays', 'identity.json');

let db: Db;
const now = () => '2026-08-13T00:00:00.000Z';

function seedModel(providerId: string, modelId: string): void {
  db.prepare(`INSERT OR IGNORE INTO providers (id, name, roster_url) VALUES (?, ?, 'https://example.test')`)
    .run(providerId, providerId);
  db.prepare(
    `INSERT INTO models (provider_id, model_id, status, first_seen_at, last_seen_at)
     VALUES (?, ?, 'active', ?, ?)`,
  ).run(providerId, modelId, now(), now());
}

const rejectionsFor = (providerId: string, modelId: string) =>
  db
    .prepare(
      `SELECT * FROM identity_rejections WHERE provider_id=? AND model_id=?
       ORDER BY rejected_candidate`,
    )
    .all(providerId, modelId) as unknown as Record<string, string | null>[];

beforeEach(() => {
  db = openDb(':memory:');
});

describe('parsing the rejected overlay', () => {
  test('each rejected candidate becomes its own record', () => {
    // Rule 4: two rejected candidates are two facts. Collapsing them into one
    // string would make "which candidate, and why that one" unanswerable.
    const overlay: RejectionOverlay = {
      entries: {
        'model-a': {
          verdict: 'identity_review',
          candidates: [
            { candidate: 'up/one', why: 'first reason', evidence: ['e1'], sourceUrl: 'https://a.test' },
            { candidate: 'up/two', why: 'second reason', evidence: ['e2'], sourceUrl: 'https://b.test' },
          ],
        },
      },
    };

    const parsed = parseRejections(overlay);

    assert.equal(parsed.length, 2);
    assert.deepEqual(parsed.map((r) => r.candidate), ['up/one', 'up/two']);
    assert.deepEqual(parsed.map((r) => r.why), ['first reason', 'second reason']);
    assert.equal(parsed[0].verdict, 'candidate_rejected');
  });

  test('a null candidate is recorded as "no candidate exists", not as a rejection of nothing', () => {
    // Different claim: "we examined X and refused it" versus "the search
    // established there is nothing to examine".
    const parsed = parseRejections({
      entries: {
        'model-b': {
          verdict: 'no_candidate_exists',
          candidates: [{ candidate: null, why: 'nothing upstream matches', evidence: [] }],
        },
      },
    });

    assert.equal(parsed.length, 1);
    assert.equal(parsed[0].candidate, null);
    assert.equal(parsed[0].verdict, 'no_candidate_exists');
  });
});

describe('ingesting rejections into the catalog', () => {
  test('a rejection fans out to every provider serving that model id', () => {
    // The overlay is keyed by catalog model id, and two providers sell
    // `qwen3.5-plus`. A record attached to only one of them would leave the
    // other looking un-investigated.
    seedModel('p1', 'shared-model');
    seedModel('p2', 'shared-model');

    ingestRejections(db, {
      entries: {
        'shared-model': {
          verdict: 'identity_review',
          candidates: [{ candidate: 'up/x', why: 'because', evidence: ['e'] }],
        },
      },
    }, now);

    assert.equal(rejectionsFor('p1', 'shared-model').length, 1);
    assert.equal(rejectionsFor('p2', 'shared-model').length, 1);
  });

  test('the complete record survives — reason, evidence, source, url, versions, metadata', () => {
    seedModel('p1', 'full-record');

    ingestRejections(db, {
      reviewedAt: '2026-08-13',
      entries: {
        'full-record': {
          verdict: 'identity_review',
          candidates: [
            {
              candidate: 'up/y',
              why: 'the stated reason',
              evidence: ['first piece', 'second piece'],
              sourceUrl: 'https://source.test/models',
              candidateMeta: { contextTokens: 1000 },
            },
          ],
        },
      },
    }, now);

    const [r] = rejectionsFor('p1', 'full-record');
    assert.equal(r.rejected_candidate, 'up/y');
    assert.equal(r.reason, 'the stated reason');
    assert.deepEqual(JSON.parse(r.evidence_json as string), ['first piece', 'second piece']);
    assert.equal(r.source, 'identity_overlay');
    assert.equal(r.source_ref, 'full-record');
    assert.equal(r.source_url, 'https://source.test/models');
    assert.equal(r.reviewed_at, '2026-08-13');
    assert.ok(r.resolver_version, 'the resolver version must be stamped');
    assert.ok(r.evidence_state, 'the provenance contract requires an evidence state');
    assert.deepEqual(JSON.parse(r.candidate_meta_json as string), { contextTokens: 1000 });
  });

  test('re-ingesting updates in place instead of duplicating', () => {
    seedModel('p1', 'twice');
    const overlay: RejectionOverlay = {
      entries: { twice: { verdict: 'identity_review', candidates: [{ candidate: 'up/z', why: 'r', evidence: [] }] } },
    };

    ingestRejections(db, overlay, now);
    ingestRejections(db, overlay, now);

    assert.equal(rejectionsFor('p1', 'twice').length, 1);
  });

  test('a model id nobody serves is ignored rather than orphaned', () => {
    ingestRejections(db, {
      entries: { 'nobody-sells-this': { verdict: 'identity_review', candidates: [{ candidate: 'up/q', why: 'r', evidence: [] }] } },
    }, now);

    assert.equal((db.prepare('SELECT COUNT(*) n FROM identity_rejections').get() as { n: number }).n, 0);
  });

  test('an id that is BOTH mapped and rejected is refused, not silently preferred', () => {
    // Rule 1, enforced rather than trusted. A resolved mapping and a rejection
    // for the same id are contradictory, and picking either one quietly is how a
    // rejected candidate would become a resolved identity.
    seedModel('p1', 'contradiction');

    assert.throws(
      () =>
        ingestRejections(
          db,
          {
            entries: {
              contradiction: { verdict: 'identity_review', candidates: [{ candidate: 'up/c', why: 'r', evidence: [] }] },
            },
          },
          now,
          { mappings: { contradiction: 'up/c' } },
        ),
      /both mapped and rejected/i,
    );
  });
});

describe('a rejected candidate never becomes a resolved identity', () => {
  test('the model keeps its unresolved identity state after ingestion', () => {
    seedModel('p1', 'still-unresolved');

    ingestRejections(db, {
      entries: {
        'still-unresolved': {
          verdict: 'identity_review',
          candidates: [{ candidate: 'up/tempting', why: 'refused', evidence: [] }],
        },
      },
    }, now);

    const m = loadModels(db).find((x) => x.modelId === 'still-unresolved')!;
    assert.equal(m.canonicalId, null, 'a rejected candidate must not populate the canonical identity');
    assert.notEqual(m.identityState, 'resolved');
    assert.equal(m.identityState, 'identity_review');
    assert.equal(m.rejectedCandidates.length, 1);
    assert.equal(m.rejectedCandidates[0].candidate, 'up/tempting');
  });

  test('no rejected candidate appears anywhere as a canonical id', () => {
    seedModel('p1', 'guard');
    ingestRejections(db, {
      entries: { guard: { verdict: 'identity_review', candidates: [{ candidate: 'up/never', why: 'r', evidence: [] }] } },
    }, now);

    const canonicals = loadModels(db).map((m) => m.canonicalId).filter(Boolean);
    assert.ok(!canonicals.includes('up/never'));
  });

  test('a model with no rejections reports an empty list, not a missing field', () => {
    seedModel('p1', 'clean');
    const m = loadModels(db).find((x) => x.modelId === 'clean')!;
    assert.deepEqual(m.rejectedCandidates, []);
    assert.equal(m.identityState, 'unresolved');
  });
});

describe('root composition: the real overlay file reaches the API model', () => {
  test('every rejected entry in overlays/identity.json is represented for every provider serving it', () => {
    // Not a hand-built object: the actual committed file, through the actual
    // ingestion, out through the actual read model. A test that only ever sees
    // an injected fixture cannot prove the wiring exists.
    const file = JSON.parse(readFileSync(OVERLAY_PATH, 'utf8')) as {
      mappings: Record<string, string>;
      rejected: RejectionOverlay;
    };
    const ids = Object.keys(file.rejected.entries);
    assert.ok(ids.length > 0, 'the overlay must actually contain rejected entries');

    // One provider per id is enough to prove the path; the fan-out is covered above.
    for (const id of ids) seedModel('p1', id);

    ingestRejections(db, file.rejected, now, { mappings: file.mappings });

    const models = loadModels(db);
    for (const id of ids) {
      const m = models.find((x) => x.modelId === id)!;
      const expected = file.rejected.entries[id].candidates.length;
      assert.equal(
        m.rejectedCandidates.length,
        expected,
        `${id}: expected ${expected} rejection record(s) to reach the API, got ${m.rejectedCandidates.length}`,
      );
      for (const r of m.rejectedCandidates) {
        assert.ok(r.why, `${id}: a rejection reached the API with no reason`);
        assert.ok(r.evidence.length > 0 || r.candidate === null, `${id}: no evidence carried`);
        assert.equal(r.source, 'identity_overlay');
      }
      assert.equal(m.canonicalId, null, `${id}: a rejected id must never resolve`);
    }

    assert.equal(
      loadMeta(db, models).identityDetail.rejectedCandidates,
      ids.reduce((n, id) => n + file.rejected.entries[id].candidates.length, 0),
    );
  });

  test('no rejected candidate in the real file is also a resolved mapping', () => {
    const file = JSON.parse(readFileSync(OVERLAY_PATH, 'utf8')) as {
      mappings: Record<string, string>;
      rejected: RejectionOverlay;
    };
    for (const id of Object.keys(file.rejected.entries)) {
      assert.ok(!(id in file.mappings), `${id} is both mapped and rejected in the committed overlay`);
    }
  });
});

describe('the identity partition stays exclusive', () => {
  test('a candidate rejected for ONE offering is still resolvable for another', () => {
    // The invariant is per-offering, not global. deepseek/deepseek-v4-flash is
    // the wrong identity for the April `:preview` snapshot and the RIGHT identity
    // for every provider serving the current release. A global ban would be a
    // different bug, so this pins the correct scope.
    seedModel('p1', 'preview-row');
    seedModel('p2', 'stable-row');
    db.prepare(
      `INSERT INTO model_scores (provider_id, model_id, kind, evidence_level, source_model_id,
                                 methodology_ver, computed_at, precision_dp, value)
       VALUES ('p2','stable-row','VQ','measured','up/shared','venom-score-v2','2026-08-13T00:00:00.000Z',1,50)`,
    ).run();

    ingestRejections(db, {
      entries: {
        'preview-row': {
          verdict: 'identity_review',
          candidates: [{ candidate: 'up/shared', why: 'wrong snapshot', evidence: ['e'] }],
        },
      },
    }, now);

    const models = loadModels(db);
    const preview = models.find((m) => m.modelId === 'preview-row')!;
    const stable = models.find((m) => m.modelId === 'stable-row')!;
    assert.equal(preview.canonicalId, null, 'the offering that rejected it must not resolve to it');
    assert.equal(stable.canonicalId, 'up/shared', 'another offering keeps its legitimate identity');
  });

  test('no offering lists its own resolved identity among its rejections', () => {
    // The real invariant, stated per offering. If this ever fails, a row is
    // simultaneously claiming a candidate is correct and that it was refused.
    seedModel('p1', 'row');
    db.prepare(
      `INSERT INTO model_scores (provider_id, model_id, kind, evidence_level, source_model_id,
                                 methodology_ver, computed_at, precision_dp, value)
       VALUES ('p1','row','VQ','measured','up/good','venom-score-v2','2026-08-13T00:00:00.000Z',1,50)`,
    ).run();

    ingestRejections(db, {
      entries: { row: { verdict: 'identity_review', candidates: [{ candidate: 'up/bad', why: 'r', evidence: [] }] } },
    }, now);

    for (const m of loadModels(db)) {
      const own = m.rejectedCandidates.map((r) => r.candidate);
      assert.ok(
        m.canonicalId === null || !own.includes(m.canonicalId),
        `${m.providerId}/${m.modelId} both resolves to and rejects ${m.canonicalId}`,
      );
    }
  });
});

describe('the identity contract the SPA will depend on', () => {
  /** Seed a resolved row (VQ carries the canonical id), optionally benchmarked. */
  function seedResolved(modelId: string, benchmarked: boolean): void {
    seedModel('p1', modelId);
    db.prepare(
      `INSERT INTO model_scores (provider_id, model_id, kind, evidence_level, source_model_id,
                                 methodology_ver, computed_at, precision_dp, value)
       VALUES ('p1', ?, 'VQ', ?, ?, 'venom-score-v2', '2026-08-13T00:00:00.000Z', 1, ?)`,
    ).run(modelId, benchmarked ? 'measured' : 'unrated', `up/${modelId}`, benchmarked ? 50 : null);
  }

  test('the three identity states are exclusive and sum to liveModels', () => {
    // The contract a dashboard chart depends on. Four buckets where one of them
    // was really a QUALITY count is how a report came to claim 102 + 6 + 6 + 2.
    seedResolved('resolved-scored', true);
    seedResolved('resolved-unscored', false);
    seedModel('p1', 'in-review');
    seedModel('p1', 'never-matched');
    ingestRejections(db, {
      entries: { 'in-review': { verdict: 'identity_review', candidates: [{ candidate: 'up/x', why: 'r', evidence: [] }] } },
    }, now);

    const { identity, liveModels } = loadMeta(db, loadModels(db));

    assert.equal(identity.resolved, 2, 'both resolved rows count as resolved, benchmarked or not');
    assert.equal(identity.identityReview, 1);
    assert.equal(identity.unresolved, 1);
    assert.equal(identity.resolved + identity.identityReview + identity.unresolved, liveModels);
    assert.equal(liveModels, 4);
  });

  test('no model falls into two identity states at once', () => {
    seedResolved('r', true);
    seedModel('p1', 'ir');
    seedModel('p1', 'u');
    ingestRejections(db, {
      entries: { ir: { verdict: 'identity_review', candidates: [{ candidate: 'up/y', why: 'r', evidence: [] }] } },
    }, now);

    const counts: Record<string, number> = {};
    for (const m of loadModels(db)) counts[m.identityState] = (counts[m.identityState] ?? 0) + 1;
    const { identity } = loadMeta(db, loadModels(db));
    assert.deepEqual(
      { resolved: counts.resolved ?? 0, identityReview: counts.identity_review ?? 0, unresolved: counts.unresolved ?? 0 },
      { resolved: identity.resolved, identityReview: identity.identityReview, unresolved: identity.unresolved },
      'the meta counters must be exactly the per-row states, not a second derivation',
    );
  });

  test('quality is NOT an identity bucket: a resolved row with no benchmark is still resolved', () => {
    // The specific confusion being ruled out. `qualityScored` asks "does a
    // benchmark exist"; identity asks "which model is this". A row can answer the
    // second and not the first, and it is fully resolved.
    seedResolved('resolved-unscored', false);

    const models = loadModels(db);
    const meta = loadMeta(db, models);
    const m = models[0];

    assert.equal(m.identityState, 'resolved');
    assert.equal(m.vq.value, null);
    assert.equal(meta.identity.resolved, 1);
    assert.equal(meta.qualityScored, 0, 'unbenchmarked, so it is not quality-scored');
    assert.ok(!('resolvedWithEvidence' in meta.identity), 'no quality count may live inside the identity partition');
  });

  test('a resolver-detected ambiguity puts the row in review too, not merely unresolved', () => {
    // The `identity_review` TABLE holds machine-detected ambiguity and the
    // identity STATE is named the same thing. They must mean the same thing, or
    // one row is ambiguous in the table and unresolved in the API.
    seedModel('p1', 'ambiguous-row');
    db.prepare(
      `INSERT INTO identity_review (provider_id, model_id, candidates_json, status, first_seen_at)
       VALUES ('p1','ambiguous-row','["up/a","up/b"]','open','2026-08-13T00:00:00.000Z')`,
    ).run();

    const models = loadModels(db);
    const m = models.find((x) => x.modelId === 'ambiguous-row')!;
    assert.equal(m.identityState, 'identity_review');
    const { identity } = loadMeta(db, models);
    assert.equal(identity.identityReview, 1);
    assert.equal(identity.unresolved, 0);
  });
});
