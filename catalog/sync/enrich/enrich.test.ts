import { test, describe, beforeEach } from 'node:test';
import assert from 'node:assert/strict';
import { openDb, type Db } from '../../db/index.ts';
import { rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { buildIndex } from '../identity.ts';
import { enrich, type EnrichDeps } from './enrich.ts';
import type { IntrinsicFacts } from '../sources/models-dev.ts';

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

/**
 * The feed, reconstructed from the row a test seeded.
 *
 * These tests express "the provider's feed published X" by seeding a row with X
 * in it, which is what the roster engine does in production. `enrich` no longer
 * reads the row for that — the row is also where its own output lands, so a
 * second pass was re-crediting a derived value to the provider's feed entry —
 * so the fixture supplies the same information through the same door production
 * uses. A test that nulls a column to simulate a source going quiet still does
 * exactly that: the lookup stops reporting the field.
 */
const specFromSeededRow = (modelId: string) => {
  const r = db
    .prepare(`SELECT context_tokens, output_tokens, input_modalities, tools, reasoning,
                     structured, attachment, cost_in_per_m, cost_out_per_m
              FROM models WHERE model_id = ?`)
    .get(modelId) as Record<string, number | string | null> | undefined;
  if (!r) return null;
  const bool = (v: unknown) => (v === null || v === undefined ? undefined : Boolean(v));
  return {
    contextTokens: (r.context_tokens as number) ?? undefined,
    outputTokens: (r.output_tokens as number) ?? undefined,
    inputModalities: r.input_modalities ? (JSON.parse(r.input_modalities as string) as string[]) : undefined,
    tools: bool(r.tools),
    reasoning: bool(r.reasoning),
    structured: bool(r.structured),
    attachment: bool(r.attachment),
    costInPerM: (r.cost_in_per_m as number) ?? undefined,
    costOutPerM: (r.cost_out_per_m as number) ?? undefined,
  };
};

const deps = (over: Partial<EnrichDeps> = {}): EnrichDeps => ({
  db,
  lookupSpec: (_k, modelId) => specFromSeededRow(modelId),
  intrinsic: () => null,
  canonical: { index: buildIndex([]), byId: new Map() },
  overlay: {},
  billing: { p: { model: 'per_token', evidenceUrl: 'https://example.test/pricing', note: 'per-token' } },
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
    const intrinsic: IntrinsicFacts = {
      declaredBy: '',
      conflicts: [{ field: 'attachment', sides: [{ value: true, by: 'a/x' }, { value: false, by: 'b/x' }] }],
    };

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

const conflictsFor = (modelId: string) =>
  db
    .prepare(
      `SELECT field, sides_json, conflict_type, status, resolved_to, detected_at
       FROM model_conflicts WHERE provider_id=? AND model_id=? ORDER BY field`,
    )
    .all('p', modelId) as unknown as {
    field: string; sides_json: string; conflict_type: string; status: string;
    resolved_to: string | null; detected_at: string;
  }[];

describe('a seller disagreement is recorded, not merely dropped', () => {
  test('both sides survive, with the value and the source that declared it', () => {
    // The hy3-preview shape: aihubmix says structured_output=true, kilo says
    // false. Resolving to unknown is right; losing the fact that anyone
    // disagreed makes the resulting dash indistinguishable from silence.
    seed({ modelId: 'disputed', structured: null });
    const intrinsic: IntrinsicFacts = {
      declaredBy: '',
      conflicts: [
        {
          field: 'structured',
          sides: [
            { value: true, by: 'aihubmix/hy3-preview' },
            { value: false, by: 'kilo/hy3-preview' },
          ],
        },
      ],
    };

    enrich(deps({ intrinsic: () => intrinsic }));

    const rows = conflictsFor('disputed');
    assert.equal(rows.length, 1, 'expected one recorded conflict');
    assert.equal(rows[0].field, 'structured');
    assert.equal(rows[0].conflict_type, 'source_disagreement');
    assert.equal(rows[0].status, 'open');
    assert.equal(rows[0].resolved_to, null);

    const sides = JSON.parse(rows[0].sides_json) as { value: unknown; by: string }[];
    assert.deepEqual(sides, [
      { value: true, by: 'aihubmix/hy3-preview' },
      { value: false, by: 'kilo/hy3-preview' },
    ]);
  });

  test('a recorded conflict never becomes a value', () => {
    seed({ modelId: 'disputed-2', structured: null });
    const intrinsic: IntrinsicFacts = {
      declaredBy: '',
      conflicts: [{ field: 'structured', sides: [{ value: true, by: 'a/m' }, { value: false, by: 'b/m' }] }],
    };

    enrich(deps({ intrinsic: () => intrinsic }));

    const stored = db.prepare('SELECT structured FROM models WHERE model_id=?').get('disputed-2') as unknown as {
      structured: number | null;
    };
    assert.equal(stored.structured, null, 'a disputed field must stay unknown');
    assert.equal(factFor('disputed-2', 'structured'), undefined, 'and must not gain a provenance row');
  });

  test('sellers that agree produce no conflict row', () => {
    seed({ modelId: 'agreed', structured: null });
    const intrinsic: IntrinsicFacts = { structured: true, declaredBy: 'a/agreed', conflicts: [] };

    enrich(deps({ intrinsic: () => intrinsic }));

    assert.equal(conflictsFor('agreed').length, 0);
  });

  test('re-running the pass updates the conflict instead of duplicating it', () => {
    // enrich is documented as reproducible: running it twice must not
    // accumulate drift, and a primary key is the only thing that guarantees it.
    seed({ modelId: 'twice', structured: null });
    const intrinsic: IntrinsicFacts = {
      declaredBy: '',
      conflicts: [{ field: 'structured', sides: [{ value: true, by: 'a/m' }, { value: false, by: 'b/m' }] }],
    };

    enrich(deps({ intrinsic: () => intrinsic }));
    enrich(deps({ intrinsic: () => intrinsic }));

    assert.equal(conflictsFor('twice').length, 1);
  });
});

const allFactsFor = (modelId: string) =>
  db
    .prepare(
      `SELECT field, value, source, source_ref, source_url, evidence_state,
              resolver_version, probe_version, raw_value
       FROM model_facts WHERE provider_id='p' AND model_id=? ORDER BY field`,
    )
    .all(modelId) as unknown as {
    field: string; value: string; source: string; source_ref: string; source_url: string | null;
    evidence_state: string; resolver_version: string; probe_version: string | null; raw_value: string | null;
  }[];

describe('every resolved field carries the full provenance contract', () => {
  test('no fact is recorded without a resolver version and an evidence state', () => {
    seed({ modelId: 'traced' });

    enrich(deps());

    const facts = allFactsFor('traced');
    assert.ok(facts.length > 0, 'expected facts to be recorded');
    for (const f of facts) {
      assert.ok(f.resolver_version, `${f.field} has no resolver_version`);
      assert.ok(f.evidence_state, `${f.field} has no evidence_state`);
      assert.equal(f.probe_version, null, `${f.field} was not probed, so probe_version must be null`);
    }
  });

  test("a value from the provider's own feed cites that feed's URL", () => {
    seed({ modelId: 'from-feed-url' });

    enrich(deps());

    const ctx = allFactsFor('from-feed-url').find((f) => f.field === 'context')!;
    assert.equal(ctx.source, 'models.dev');
    assert.equal(ctx.source_url, 'https://models.dev/api.json');
    assert.equal(ctx.evidence_state, 'first_party');
    assert.equal(ctx.raw_value, '128000', 'the untransformed upstream figure must be kept');
  });

  test('a pooled declaration is marked as a third party, not as first party', () => {
    // The distinction a consumer needs: this provider did not say this. Another
    // seller of the same model did.
    seed({ modelId: 'pooled', structured: null });

    enrich(deps({ intrinsic: () => ({ structured: true, declaredBy: 'other/pooled', conflicts: [] }) }));

    const f = allFactsFor('pooled').find((x) => x.field === 'structured')!;
    assert.equal(f.evidence_state, 'pooled_third_party');
    assert.equal(f.source_url, 'https://models.dev/api.json');
  });

  test('an index confirmation is marked as such — it can only ever say yes', () => {
    seed({ modelId: 'confirmed', structured: null });
    const canonical = {
      index: buildIndex([{ id: 'up/confirmed' }]),
      byId: new Map([['up/confirmed', { id: 'up/confirmed', supportedParameters: ['structured_outputs'] }]]),
    };

    enrich(deps({ canonical }));

    const f = allFactsFor('confirmed').find((x) => x.field === 'structured')!;
    assert.equal(f.source, 'openrouter');
    assert.equal(f.evidence_state, 'index_confirmation');
    assert.equal(f.source_url, 'https://openrouter.ai/api/v1/models');
  });
});

describe('cost is three facts, not one blob', () => {
  test('billing kind, effective price and reference price are recorded separately', () => {
    seed({ modelId: 'priced' });
    db.prepare(`UPDATE models SET cost_in_per_m=1, cost_out_per_m=5 WHERE model_id='priced'`).run();
    const canonical = {
      index: buildIndex([{ id: 'up/priced' }]),
      byId: new Map([['up/priced', { id: 'up/priced', refCostInPerM: 9, refCostOutPerM: 20 }]]),
    };

    enrich(deps({ canonical }));

    const fields = allFactsFor('priced').map((f) => f.field);
    assert.ok(fields.includes('billingKind'), 'billingKind must be its own fact');
    assert.ok(fields.includes('effectivePrice'), 'effectivePrice must be its own fact');
    assert.ok(fields.includes('referencePrice'), 'referencePrice must be its own fact');
    assert.ok(!fields.includes('cost'), 'the single cost blob must be gone');
  });

  test('the reference price cites the index, never the provider', () => {
    // The whole point of the split: a market rate must never be able to appear
    // under the provider's own label.
    seed({ modelId: 'ref-vs-eff' });
    db.prepare(`UPDATE models SET cost_in_per_m=1, cost_out_per_m=5 WHERE model_id='ref-vs-eff'`).run();
    const canonical = {
      index: buildIndex([{ id: 'up/ref-vs-eff' }]),
      byId: new Map([['up/ref-vs-eff', { id: 'up/ref-vs-eff', refCostInPerM: 9, refCostOutPerM: 20 }]]),
    };

    enrich(deps({ canonical }));

    const facts = allFactsFor('ref-vs-eff');
    const eff = facts.find((f) => f.field === 'effectivePrice')!;
    const ref = facts.find((f) => f.field === 'referencePrice')!;
    assert.equal(eff.source, 'models.dev');
    assert.equal(eff.evidence_state, 'first_party');
    assert.equal(ref.source, 'openrouter');
    assert.equal(ref.evidence_state, 'index_confirmation');
    assert.notEqual(eff.value, ref.value);
  });

  test('a subscription model records a billing kind and no effective price at all', () => {
    // `included` is an answer, not a hole — but it is also not a price, so there
    // must be no effectivePrice fact claiming one.
    seed({ modelId: 'subscribed' });
    db.prepare(`UPDATE models SET cost_in_per_m=NULL, cost_out_per_m=NULL WHERE model_id='subscribed'`).run();

    enrich(
      deps({
        billing: {
          p: { model: 'subscription', evidenceUrl: 'https://example.test/pricing', note: 'plan covers all models' },
        },
      }),
    );

    const facts = allFactsFor('subscribed');
    const kind = facts.find((f) => f.field === 'billingKind')!;
    assert.equal(JSON.parse(kind.value), 'included');
    assert.equal(kind.evidence_state, 'declared_policy');
    assert.ok(kind.source_url, 'the billing declaration must cite its own evidence');
    assert.equal(facts.find((f) => f.field === 'effectivePrice'), undefined);
  });

  test('a free, quota-limited provider survives a SECOND enrich pass without laundering', () => {
    // The exact defect end-to-end verification caught: a free-quota model with no
    // feed price must NOT get a derived $0 written into its price columns, because
    // the second enrich pass would then re-read that 0 from the models table and
    // relabel it a first-party models.dev price. Two passes, like the pipeline.
    seed({ modelId: 'free-quota-model' });
    db.prepare(`UPDATE models SET cost_in_per_m=NULL, cost_out_per_m=NULL WHERE model_id='free-quota-model'`).run();
    const freeQuota = deps({
      billing: { p: { model: 'free_quota', evidenceUrl: 'https://ollama.com/pricing', note: 'free, quota-limited' } },
    });

    enrich(freeQuota);
    enrich(freeQuota);

    const facts = allFactsFor('free-quota-model');
    const kind = facts.find((f) => f.field === 'billingKind')!;
    assert.equal(JSON.parse(kind.value), 'free', 'kind must be free, never included');
    assert.equal(kind.source, 'provider_billing', 'free must be a declared policy, not a feed figure');
    assert.equal(kind.evidence_state, 'declared_policy');
    assert.equal(
      facts.find((f) => f.field === 'effectivePrice'),
      undefined,
      'no fabricated $0 effective price — that is what the 2nd pass would launder to first_party',
    );
    const m = db
      .prepare(`SELECT cost_kind, cost_out_per_m FROM models WHERE model_id='free-quota-model'`)
      .get() as unknown as { cost_kind: string; cost_out_per_m: number | null };
    assert.equal(m.cost_kind, 'free');
    assert.equal(m.cost_out_per_m, null, 'the price column stays null across both passes');
  });
});

describe('a superseded fact field is retired, not left provenance-less', () => {
  test('an old `cost` blob row is gone after the database is opened', () => {
    // Uses a real file, because an in-memory database is per-connection: opening
    // ':memory:' twice yields two empty databases, so a migration asserted that
    // way passes without the migration existing at all.
    const file = join(tmpdir(), `venom-catalog-migrate-${process.pid}.db`);
    rmSync(file, { force: true });
    try {
      const first = openDb(file);
      first.prepare(
        `INSERT INTO providers (id, name, roster_url) VALUES ('p','Provider','https://example.test')`,
      ).run();
      first.prepare(
        `INSERT INTO model_facts (provider_id, model_id, field, value, source, source_ref, resolved_at)
         VALUES ('p','legacy','cost','{"kind":"per_token"}','models.dev','cost','2026-08-12T00:00:00.000Z')`,
      ).run();
      // The legacy row carries no source_url, evidence_state or resolver_version.
      assert.equal(
        (first.prepare(`SELECT COUNT(*) n FROM model_facts WHERE field='cost'`).get() as { n: number }).n,
        1,
        'precondition: the legacy row is really there',
      );
      first.close();

      const reopened = openDb(file);
      assert.equal(
        (reopened.prepare(`SELECT COUNT(*) n FROM model_facts WHERE field='cost'`).get() as { n: number }).n,
        0,
        'reopening must retire the superseded field',
      );
      reopened.close();
    } finally {
      rmSync(file, { force: true });
      rmSync(`${file}-wal`, { force: true });
      rmSync(`${file}-shm`, { force: true });
    }
  });

  test('the fields still in use are never retired', () => {
    seed({ modelId: 'kept' });
    enrich(deps());
    const fields = allFactsFor('kept').map((f) => f.field);
    for (const f of ['billingKind', 'context', 'maxOutput', 'tools']) {
      assert.ok(fields.includes(f), `${f} must survive the migration`);
    }
  });
});

/**
 * The pass is documented as "a pure function of (stored rows + canonical index +
 * provider billing), so re-running it reproduces the same result and never
 * accumulates drift". Upserting satisfies that only for facts the run still
 * produces. A field that STOPS resolving was writing nothing and deleting
 * nothing, so the previous run's row outlived the evidence for it — and the
 * catalog then reported the field as missing while still serving its old
 * provenance. One panel said both "no source published this" and
 * "1000000, from openrouter" about the same field.
 *
 * The rule these pin: after a run, the derived tables hold exactly what THIS run
 * proved. Not a superset carried over from a luckier one.
 */
describe('derived state is rebuilt by every run, never accumulated', () => {
  test('a fact the resolver can no longer prove is deleted, not left behind', () => {
    seed({ modelId: 'was-known', contextTokens: 500_000 });
    enrich(deps());
    assert.equal(JSON.parse(factFor('was-known', 'context')!.value), 500_000, 'precondition: the fact was resolved once');

    // The evidence goes away: nothing in the row, no detail, no intrinsic. This
    // is the real qwen3.8-max shape — the provider publishes no limit and the
    // cross-provider fallback was deliberately removed, so nothing can prove it.
    db.prepare(`UPDATE models SET context_tokens = NULL WHERE model_id = 'was-known'`).run();

    enrich(deps());

    const stored = db.prepare('SELECT context_tokens FROM models WHERE model_id=?').get('was-known') as unknown as {
      context_tokens: number | null;
    };
    assert.equal(stored.context_tokens, null, 'the value must become unknown');
    assert.equal(
      factFor('was-known', 'context'),
      undefined,
      'and its provenance must go with it — a fact row for a field we report as missing is a contradiction',
    );
  });

  test('a fact that is still proven survives the rebuild', () => {
    // The companion guard: pruning must remove only what stopped resolving. If
    // this fails the fix is deleting live provenance, which is worse than the
    // defect it replaces.
    seed({ modelId: 'still-known', contextTokens: 256_000 });
    enrich(deps());
    enrich(deps());

    const fact = factFor('still-known', 'context');
    assert.ok(fact, 'a field that still resolves keeps its provenance');
    assert.equal(JSON.parse(fact!.value), 256_000);
  });

  test('a price fact stops being served once the price is gone', () => {
    // `effectivePrice` and `referencePrice` are written conditionally, so they
    // had the same hole as context: no price this run meant the previous run's
    // price kept its row.
    seed({ modelId: 'was-priced' });
    db.prepare(`UPDATE models SET cost_in_per_m = 3.0, cost_out_per_m = 15.0 WHERE model_id='was-priced'`).run();
    enrich(deps());
    assert.ok(factFor('was-priced', 'effectivePrice'), 'precondition: a price was resolved once');

    db.prepare(`UPDATE models SET cost_in_per_m = NULL, cost_out_per_m = NULL WHERE model_id='was-priced'`).run();
    enrich(deps());

    assert.equal(factFor('was-priced', 'effectivePrice'), undefined, 'a withdrawn price leaves no provenance behind');
  });

  test('a conflict the current run no longer produces does not survive it', () => {
    seed({ modelId: 'was-disputed', structured: null });
    const disputed: IntrinsicFacts = {
      declaredBy: '',
      conflicts: [{ field: 'structured', sides: [{ value: true, by: 'a/m' }, { value: false, by: 'b/m' }] }],
    };
    enrich(deps({ intrinsic: () => disputed }));
    assert.equal(conflictsFor('was-disputed').length, 1, 'precondition: the disagreement was recorded');

    // The sellers now agree — the disagreement is over.
    const agreed: IntrinsicFacts = { structured: true, declaredBy: 'a/m', conflicts: [] };
    enrich(deps({ intrinsic: () => agreed }));

    assert.equal(
      conflictsFor('was-disputed').length,
      0,
      'a settled disagreement must not keep being reported as open',
    );
  });

  test('a conflict that persists keeps a human resolution decision', () => {
    // The guard that stops the prune from being a blunt DELETE-then-INSERT:
    // `status` and `resolved_to` are owned by a person, not by the run, and
    // rebuilding the row from the sources must not overwrite their decision.
    seed({ modelId: 'judged', structured: null });
    const disputed: IntrinsicFacts = {
      declaredBy: '',
      conflicts: [{ field: 'structured', sides: [{ value: true, by: 'a/m' }, { value: false, by: 'b/m' }] }],
    };
    enrich(deps({ intrinsic: () => disputed }));
    db.prepare(
      `UPDATE model_conflicts SET status='resolved', resolved_to='true'
       WHERE provider_id='p' AND model_id='judged' AND field='structured'`,
    ).run();

    enrich(deps({ intrinsic: () => disputed }));

    const rows = conflictsFor('judged');
    assert.equal(rows.length, 1);
    assert.equal(rows[0].status, 'resolved', 'a human verdict outlives the run that re-detected the conflict');
    assert.equal(rows[0].resolved_to, 'true');
  });

  test('one model losing a fact does not disturb another model', () => {
    // The prune is scoped per row. A single shared DELETE would have taken the
    // neighbour's provenance with it.
    seed({ modelId: 'loser', contextTokens: 400_000 });
    seed({ modelId: 'keeper', contextTokens: 300_000 });
    enrich(deps());
    db.prepare(`UPDATE models SET context_tokens = NULL WHERE model_id='loser'`).run();

    enrich(deps());

    assert.equal(factFor('loser', 'context'), undefined);
    assert.equal(JSON.parse(factFor('keeper', 'context')!.value), 300_000, "the neighbour's provenance is untouched");
  });
});

/**
 * The gap the two orchestration paths opened between them.
 *
 * `sync/run.ts` (the CLI) runs enrich twice — once from the free shared
 * sources, then again after asking the provider's own detail endpoint for
 * whatever is still open. `server/sync-runner.ts` (reached by `POST /v1/sync`
 * and the scheduler) used to run enrich only once, never consulting detail at
 * all. Against the rebuild-on-every-run rule just added above, that is not a
 * missing feature — it is active data loss: a fact ONLY the detail endpoint
 * can prove looks, to a run that never asks it, identical to a fact the
 * provider withdrew. "We didn't ask" is not "the answer changed".
 *
 * These tests exercise `enrich()` directly, which is where the fix belongs —
 * it is the single place both orchestration paths ultimately call, so a guard
 * here protects a fact regardless of which path forgets to ask, now or later.
 */
/**
 * `seed()`'s `row.contextTokens ?? 128_000` treats an explicit `null` as
 * "not given" and fills the default — so it cannot express "models.dev has
 * nothing for this row". These tests need exactly that (detail as the ONLY
 * source), so they seed normally and null the column with a direct write,
 * the same idiom the "derived state is rebuilt" tests above already use.
 */
function clearContext(modelId: string): void {
  db.prepare(`UPDATE models SET context_tokens = NULL WHERE model_id = ?`).run(modelId);
}

describe('a fact only the provider detail endpoint can prove is not mistaken for one it withdrew', () => {
  test('it survives a run that never consults provider detail at all', () => {
    // Round 1: the CLI shape — detail supplies context, models.dev has none.
    seed({ modelId: 'detail-only' });
    clearContext('detail-only');
    const detailSaysContext = new Map([
      ['p/detail-only', { contextTokens: 900_000, ref: 'ollama.com/api/show(detail-only)', url: 'https://ollama.com/api/show' }],
    ]);
    enrich(deps({ intrinsic: () => null, details: detailSaysContext }));

    const first = factFor('detail-only', 'context');
    assert.ok(first, 'precondition: detail actually proved the fact');
    assert.equal(first!.source, 'provider_api');

    // Round 2: the service shape — no `details` map at all, exactly what
    // `SyncRunner.run()` passed before this fix. `clearContext` here stands
    // in for the roster resync that always precedes a real enrich() call:
    // `engine.ts` rewrites `context_tokens` fresh from models.dev on every
    // sync, so a real run never re-reads back a value enrich() itself wrote
    // — models.dev genuinely still has nothing for this row.
    clearContext('detail-only');
    enrich(deps({ intrinsic: () => null }));

    const after = factFor('detail-only', 'context');
    assert.ok(after, 'a run that never asked detail must not conclude detail withdrew the fact');
    assert.equal(JSON.parse(after!.value), 900_000);
    assert.equal(after!.source, 'provider_api');
  });

  test('it survives a run where the detail call for THIS row failed, even while detail is asked of other rows', () => {
    // The failure mode inside the CLI path itself: `fetchOllamaDetail` returns
    // `null` on a network error, and `open`-but-unanswered rows are simply
    // absent from `details` — indistinguishable, on purpose, from "not asked".
    seed({ modelId: 'flaky' });
    clearContext('flaky');
    const flakyGotDetail = new Map([['p/flaky', { contextTokens: 700_000, ref: 'r', url: 'u' }]]);
    enrich(deps({ intrinsic: () => null, details: flakyGotDetail }));
    assert.ok(factFor('flaky', 'context'), 'precondition');

    // This round's detail map has no entry for `flaky` — the call failed —
    // but DOES have one for a different row, so the run is not simply
    // skipping detail wholesale. `clearContext('flaky')` stands in for the
    // roster resync that precedes every real enrich() call — see the first
    // test's comment for why that matters here.
    clearContext('flaky');
    seed({ modelId: 'healthy' });
    clearContext('healthy');
    const onlyHealthyAnswered = new Map([['p/healthy', { contextTokens: 500_000, ref: 'r2', url: 'u2' }]]);
    enrich(deps({ intrinsic: () => null, details: onlyHealthyAnswered }));

    const flaky = factFor('flaky', 'context');
    assert.ok(flaky, "one row's failed call must not retire a fact belonging to a DIFFERENT row's success");
    assert.equal(JSON.parse(flaky!.value), 700_000);
  });

  test('it is retired once detail is actually consulted again and genuinely no longer reports it', () => {
    // The other half of the rule: "asked, and the answer changed" IS grounds
    // to retire. Without this the guard would just be a permanent shield that
    // never lets a real withdrawal through.
    seed({ modelId: 'withdrawn' });
    clearContext('withdrawn');
    const detailOnceSaidContext = new Map([['p/withdrawn', { contextTokens: 800_000, ref: 'r', url: 'u' }]]);
    enrich(deps({ intrinsic: () => null, details: detailOnceSaidContext }));
    assert.ok(factFor('withdrawn', 'context'), 'precondition');

    // Detail responds again this round — a real, successful call — but this
    // time its payload has no `contextTokens` at all. Resync first: models.dev
    // genuinely has nothing either, and without this the row's own column would
    // still carry round one's detail-derived value from enrich's own UPDATE.
    clearContext('withdrawn');
    const detailNowSilentOnContext = new Map([['p/withdrawn', { ref: 'r', url: 'u' }]]);
    enrich(deps({ intrinsic: () => null, details: detailNowSilentOnContext }));

    assert.equal(
      factFor('withdrawn', 'context'),
      undefined,
      'a source that was actually asked again and genuinely stopped reporting the field must still be allowed to retire it',
    );
  });

  test('the protection is reported, not silent', () => {
    seed({ modelId: 'counted' });
    clearContext('counted');
    const detailSaysContext = new Map([['p/counted', { contextTokens: 900_000, ref: 'r', url: 'u' }]]);
    enrich(deps({ intrinsic: () => null, details: detailSaysContext }));

    clearContext('counted');
    const summary = enrich(deps({ intrinsic: () => null }));

    assert.equal(summary.protectedStale.context, 1, 'a fact kept despite this run not reproving it must show up somewhere');
  });
});

describe('a fallback value must not be relaundered as the provider\'s own figure', () => {
  test('a second enrichment keeps the vendor-default provenance it recorded on the first', () => {
    // The pipeline enriches twice — once from the shared sources, once more
    // after the provider detail calls. `enrich` writes what it resolved back
    // into `models.context_tokens`, and the second pass used to read that same
    // column back as though it were this provider's own models.dev entry. The
    // value survived; the story of where it came from did not. A row would end
    // up asserting that ClinePass published a 1,000,000-token window, which is
    // the precise claim the vendor fallback exists to avoid making.
    // Inserted directly: `seed` fills an omitted limit with a default, and this
    // case needs a row that genuinely has none — the condition ClinePass's two
    // newest models are actually in.
    db.prepare(
      `INSERT INTO models (provider_id, model_id, context_tokens, output_tokens,
         tools, reasoning, structured, attachment, status, first_seen_at, last_seen_at)
       VALUES ('p', 'glm-5.3', NULL, NULL, 1, 1, 1, 1, 'active', ?, ?)`,
    ).run(now(), now());
    const d = deps({
      // models.dev carries no `cline-pass` entry for this model — verified
      // 2026-08-18 — so the feed answers nothing on every pass, which is the
      // condition that made the row reach the vendor fallback at all.
      lookupSpec: () => null,
      firstPartyLimits: () => ({
        vendor: 'z-ai',
        context: [{ value: 1_000_000, by: 'zai-coding-plan/glm-5.3', url: 'https://docs.z.ai/devpack/overview' }],
        maxOutput: [{ value: 131_072, by: 'zai-coding-plan/glm-5.3', url: 'https://docs.z.ai/devpack/overview' }],
      }),
    });

    enrich(d);
    const first = db.prepare(`SELECT evidence_state s, source_ref r FROM model_facts WHERE model_id='glm-5.3' AND field='context'`).get() as { s: string; r: string };
    assert.equal(first.s, 'vendor_default', 'the first pass must record where the figure actually came from');

    enrich(d);
    const second = db.prepare(`SELECT evidence_state s, source_ref r FROM model_facts WHERE model_id='glm-5.3' AND field='context'`).get() as { s: string; r: string };
    assert.equal(second.s, 'vendor_default');
    assert.equal(second.r, first.r, 'and the second pass must not re-credit it to a source that never published it');
  });
});

describe('a model the reference index never listed still knows what it is', () => {
  test('a vendor listing gives the row an identity, recorded like any other fact', () => {
    // The row this exists for: `cline-pass/glm-5.3` displayed no identity at all
    // while quoting a 1M context window read from Z.ai's own listing of that
    // exact model. The listing that answered one question can answer the other.
    db.prepare(
      `INSERT INTO models (provider_id, model_id, context_tokens, output_tokens,
         tools, reasoning, structured, attachment, status, first_seen_at, last_seen_at)
       VALUES ('p', 'glm-5.3', 1000000, 131072, 1, 1, 1, 0, 'active', ?, ?)`,
    ).run(now(), now());

    enrich(deps({
      lookupSpec: () => null,
      vendorIdentity: () => ({ vendor: 'z-ai', canonicalId: 'z-ai/glm-5.3', declaredBy: 'nano-gpt/zai-org/glm-5.3' }),
    }));

    const f = db.prepare(`SELECT value, source, source_ref, evidence_state FROM model_facts WHERE model_id='glm-5.3' AND field='vendorIdentity'`).get() as
      { value: string; source: string; source_ref: string; evidence_state: string };
    assert.equal(JSON.parse(f.value), 'z-ai/glm-5.3');
    assert.equal(f.evidence_state, 'vendor_default');
    assert.equal(f.source_ref, 'nano-gpt/zai-org/glm-5.3');
  });

  test('no vendor listing records nothing rather than an invented id', () => {
    db.prepare(
      `INSERT INTO models (provider_id, model_id, context_tokens, output_tokens,
         tools, reasoning, structured, attachment, status, first_seen_at, last_seen_at)
       VALUES ('p', 'mystery-1', 1000, 100, 1, 1, 1, 0, 'active', ?, ?)`,
    ).run(now(), now());

    enrich(deps({ lookupSpec: () => null, vendorIdentity: () => null }));

    const count = db.prepare(`SELECT COUNT(*) n FROM model_facts WHERE model_id='mystery-1' AND field='vendorIdentity'`).get() as
      | { n: number }
      | undefined;
    assert.equal(count?.n ?? 0, 0);
  });
});
