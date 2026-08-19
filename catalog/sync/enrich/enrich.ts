/**
 * The enrichment pass.
 *
 * Runs after the roster sync, over what is already stored. The engine writes
 * what the provider feed publishes; this pass fills what the feed omitted from
 * lower-priority sources, and records the source of EVERY field — including the
 * ones the engine filled, so "where did this number come from" is answerable for
 * every cell, not only for the ones that needed help.
 *
 * It is a pure function of (stored rows + canonical index + provider billing),
 * so re-running it reproduces the same result and never accumulates drift.
 */

import type { Db } from '../../db/index.ts';
import { transaction } from '../../db/index.ts';
import { resolveIdentity, type IdentityIndex } from '../identity.ts';
import type { BenchmarkSource } from '../sources/openrouter.ts';
import {
  resolveContext, resolveMaxOutput, resolveModalities, resolveCapability, resolveCost,
  SOURCE_URL, type CanonicalRecord, type BillingPolicy,
} from './resolvers.ts';
import type { SpecLookup } from '../engine.ts';
import type { ProviderDetail } from '../sources/provider-detail.ts';
import type { ReviewedFacts } from '../reviewed-facts.ts';

export interface EnrichDeps {
  db: Db;
  /**
   * The provider's own entry in the spec feed.
   *
   * Read from the FEED, never from the model row. The row's spec columns are
   * where this function's own output lands, so a second enrichment pass — and
   * the pipeline always runs one — used to read a value back and re-credit it
   * to the provider's models.dev entry. The figure survived; the provenance did
   * not, and a row would end up asserting that a provider published a limit it
   * has never published. Required rather than optional for the reason in
   * `sync/pipeline.ts`: a source wired into one entry point and not the other
   * is a difference nothing fails on.
   */
  lookupSpec: SpecLookup;
  /** Intrinsic model properties pooled across every provider in the spec feed. */
  intrinsic: (modelId: string) => import('./resolvers.ts').IntrinsicFacts | null;
  /**
   * What the model's own vendor publishes about it, from its own storefront.
   *
   * Optional because a run without a vendor registry configured is a run with
   * no first-party evidence available — which must leave a limit missing, not
   * silently reach for a different seller's.
   */
  firstPartyLimits?: (modelId: string) => import('../sources/models-dev.ts').FirstPartyLimits | null;
  /**
   * Which model a row IS, from a vendor-namespaced listing.
   *
   * Recorded as a fact with provenance like everything else, and deliberately
   * NOT written to `model_scores.source_model_id`: that column is the reference
   * index entry a score was taken from, and read-model turns it into
   * `canonicalId`. An identity established by a listing has no score attached to
   * it, and must never look as though it does.
   */
  vendorIdentity?: (modelId: string) => import('../sources/models-dev.ts').VendorIdentity | null;
  canonical: { index: IdentityIndex; byId: Map<string, CanonicalRecord> };
  overlay: Record<string, string>;
  /** How each provider charges. Declared, because no feed publishes it. */
  billing: Record<string, BillingPolicy>;
  /**
   * The provider's own per-model record, when it publishes one. Fetched only
   * for rows a cheaper source could not settle, so a full run does not make
   * hundreds of extra calls to answer questions already answered.
   */
  details?: Map<string, ProviderDetail>;
  /** Source-backed, human-reviewed facts for fields omitted by provider vocabularies. */
  reviewedFacts?: ReviewedFacts;
  /** Optional exact offering keys for a targeted resolution pass. */
  targets?: ReadonlySet<string>;
  now: () => string;
}

export interface EnrichSummary {
  rows: number;
  /** Fields newly filled from a fallback source, by field. */
  filled: Record<string, number>;
  /** Fields still unresolved after every source, by field. */
  stillMissing: Record<string, number>;
  costKinds: Record<string, number>;
  /**
   * Fields withheld because sellers disagreed, by field.
   *
   * Reported separately from `stillMissing` on purpose: one is "nobody said",
   * the other is "we were told two different things", and collapsing them is
   * how a disagreement becomes invisible.
   */
  conflicts: Record<string, number>;
  /**
   * Rows removed because this run could no longer prove them, by field.
   *
   * Reported rather than deleted quietly: a disappearing fact is exactly the
   * kind of drift that becomes undiagnosable when nothing counts it.
   */
  retired: Record<string, number>;
  /**
   * Stale facts kept rather than retired, because this run could not consult
   * the source that had proven them — the provider's own detail endpoint,
   * `provider_api`, was either not queried this run or the call failed.
   *
   * "We didn't ask" is not "the answer changed", and retiring on the strength
   * of silence is exactly the mistake `retired` exists to prevent everywhere
   * else. Reported for the same reason: a kept-despite-not-reproving fact is
   * information a caller may need (it means this run's picture of that field
   * is incomplete, not confirmed), and it must not look identical to a field
   * this run genuinely verified.
   */
  protectedStale: Record<string, number>;
}

interface Row {
  provider_id: string; model_id: string;
  context_tokens: number | null; output_tokens: number | null; input_modalities: string | null;
  tools: number | null; reasoning: number | null; structured: number | null; attachment: number | null;
  cost_in_per_m: number | null; cost_out_per_m: number | null;
}

/**
 * Version of the resolution logic, stamped on every fact it produces.
 *
 * Bump it whenever a resolver's ordering, its source list, or what it will and
 * will not infer changes. Without it, facts resolved under different rules are
 * indistinguishable in the table, and "why does this row disagree with that one"
 * has no answer. `v2` removed the two cross-provider serving-limit fallbacks.
 */
export const RESOLVER_VERSION = 'resolver-v2';

/**
 * What an unlisted provider's missing price means.
 *
 * `unknown`, not `included` — assuming a subscription for a provider nobody
 * declared would invent a cost semantic out of an omission in our own config.
 */
const DEFAULT_BILLING: BillingPolicy = {
  model: 'per_token',
  evidenceUrl: '',
  note: 'no billing policy declared for this provider',
};


/**
 * Rebuild the feed's view of a row from what the engine stored.
 *
 * The engine has already written the provider feed's values, so the stored row
 * IS the feed's answer for those fields. Re-reading them here keeps the resolver
 * ordering honest without holding the whole feed in memory a second time.
 */
export function enrich(deps: EnrichDeps): EnrichSummary {
  const { db, canonical, overlay, billing, now } = deps;
  // The feed is keyed by the provider's feed name, the rows by its id.
  const feedKeys = new Map(
    (db.prepare('SELECT id, feed_key FROM providers').all() as unknown as { id: string; feed_key: string | null }[])
      .map((p) => [p.id, p.feed_key ?? undefined]),
  );
  const rows = (db
    .prepare(`SELECT provider_id, model_id, context_tokens, output_tokens, input_modalities,
                     tools, reasoning, structured, attachment, cost_in_per_m, cost_out_per_m
              FROM models WHERE status IN ('active','missing')`)
    .all() as unknown as Row[]).filter(
      (row) => !deps.targets || deps.targets.has(`${row.provider_id}/${row.model_id}`),
    );

  const filled: Record<string, number> = {};
  const stillMissing: Record<string, number> = {};
  const costKinds: Record<string, number> = {};
  const conflicts: Record<string, number> = {};
  const retired: Record<string, number> = {};
  const protectedStale: Record<string, number> = {};
  const at = now();

  transaction(db, () => {
    const fact = db.prepare(
      `INSERT INTO model_facts (provider_id, model_id, field, value, source, source_ref,
                                source_url, evidence_state, raw_value, resolver_version,
                                probe_version, resolved_at)
       VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
       ON CONFLICT(provider_id, model_id, field) DO UPDATE SET
         value = excluded.value, source = excluded.source,
         source_ref = excluded.source_ref, source_url = excluded.source_url,
         evidence_state = excluded.evidence_state, raw_value = excluded.raw_value,
         resolver_version = excluded.resolver_version, probe_version = excluded.probe_version,
         resolved_at = excluded.resolved_at`,
    );
    /**
     * Fields the run has proved for the row being processed right now.
     *
     * Cleared at the top of each iteration and added to by `writeFact` /  the
     * conflict loop, so the prune at the bottom knows exactly what this run
     * stands behind — and therefore what it does not.
     */
    const provenFacts = new Set<string>();
    const provenConflicts = new Set<string>();

    /** Write one fact with the full provenance contract. */
    const writeFact = (
      modelKey: { provider_id: string; model_id: string },
      field: string,
      f: { value: unknown; source: string; ref: string; url: string | null; state: string; raw: unknown },
    ) => {
      provenFacts.add(field);
      return fact.run(
        modelKey.provider_id, modelKey.model_id, field,
        JSON.stringify(f.value), f.source, f.ref, f.url, f.state,
        JSON.stringify(f.raw ?? null), RESOLVER_VERSION,
        // Null until a probe produces a fact. Written explicitly rather than
        // left to a default, so "not probed" is a recorded answer.
        null,
        at,
      );
    };
    // Recorded with the same upsert discipline as a fact: re-running the pass
    // refreshes a conflict, it never stacks a second copy of it.
    const conflict = db.prepare(
      `INSERT INTO model_conflicts (provider_id, model_id, field, sides_json, conflict_type, detected_at)
       VALUES (?,?,?,?,?,?)
       ON CONFLICT(provider_id, model_id, field) DO UPDATE SET
         sides_json = excluded.sides_json, conflict_type = excluded.conflict_type,
         detected_at = excluded.detected_at`,
    );
    const update = db.prepare(
      `UPDATE models SET context_tokens = ?, output_tokens = ?, input_modalities = ?,
                         tools = ?, reasoning = ?, structured = ?, attachment = ?,
                         cost_in_per_m = ?, cost_out_per_m = ?,
                         ref_cost_in_per_m = ?, ref_cost_out_per_m = ?, cost_kind = ?
       WHERE provider_id = ? AND model_id = ?`,
    );

    // The prune half of the rebuild.
    //
    // Read AFTER the writes and filtered against what the run proved, which is
    // why this is not a delete-then-insert: `model_conflicts.status` and
    // `resolved_to` are a person's verdict, and re-detecting a conflict is not
    // grounds for discarding it. Scoped to one row at a time so a field
    // disappearing from one model cannot take a neighbour's provenance with it.
    const heldFacts = db.prepare(`SELECT field, source FROM model_facts WHERE provider_id = ? AND model_id = ?`);
    const heldConflictFields = db.prepare(`SELECT field FROM model_conflicts WHERE provider_id = ? AND model_id = ?`);
    const dropFact = db.prepare(`DELETE FROM model_facts WHERE provider_id = ? AND model_id = ? AND field = ?`);
    const dropConflict = db.prepare(`DELETE FROM model_conflicts WHERE provider_id = ? AND model_id = ? AND field = ?`);

    for (const r of rows) {
      provenFacts.clear();
      provenConflicts.clear();
      const res = resolveIdentity(r.model_id, canonical.index, overlay);
      const canon = res.status === 'resolved' ? canonical.byId.get(res.target) ?? null : null;
      const spec = deps.lookupSpec(feedKeys.get(r.provider_id), r.model_id);
      const input = {
        detail: deps.details?.get(`${r.provider_id}/${r.model_id}`) ?? null,
        spec, intrinsic: deps.intrinsic(r.model_id), canonical: canon,
        firstParty: deps.firstPartyLimits?.(r.model_id) ?? null,
      };

      // Recorded before any field resolves, so a disputed field is visible
      // whether or not some later source happened to settle it anyway.
      for (const c of input.intrinsic?.conflicts ?? []) {
        provenConflicts.add(c.field);
        conflict.run(r.provider_id, r.model_id, c.field, JSON.stringify(c.sides), 'source_disagreement', at);
        conflicts[c.field] = (conflicts[c.field] ?? 0) + 1;
      }

      // Identity first: it is a fact about the row like any other, and the
      // completeness gate deliberately does not require it — a model can be
      // fully usable while no index has listed it.
      const vid = deps.vendorIdentity?.(r.model_id) ?? null;
      if (vid) {
        writeFact(r, 'vendorIdentity', {
          value: vid.canonicalId,
          source: 'models.dev',
          ref: vid.declaredBy,
          url: SOURCE_URL['models.dev'],
          state: 'vendor_default',
          raw: vid.declaredBy,
        });
      }

      // Exact offering key only. Falling back to `model_id` would let a fact
      // reviewed for one seller silently cross into every other seller carrying
      // the same id.
      const reviewed = deps.reviewedFacts?.[`${r.provider_id}/${r.model_id}`];
      type Resolved = {
        value: unknown; source: string; ref: string; url: string | null; state: string; raw: unknown;
      };
      const reviewedValue = (entry: { value: unknown; ref: string; sourceUrl: string; evidence: string[]; reviewedAt: string } | undefined): Resolved | null =>
        entry
          ? {
              value: entry.value,
              source: 'reviewed_source',
              ref: entry.ref,
              url: entry.sourceUrl,
              state: 'reviewed',
              raw: { evidence: entry.evidence, reviewedAt: entry.reviewedAt },
            }
          : null;
      const mergeOfficial = (field: string, base: Resolved | null, reviewedEntry: Resolved | null): Resolved | null => {
        if (!reviewedEntry) return base;
        if (base?.source === 'provider_api' && JSON.stringify(base.value) !== JSON.stringify(reviewedEntry.value)) {
          provenConflicts.add(field);
          conflict.run(
            r.provider_id,
            r.model_id,
            field,
            JSON.stringify([
              { value: base.value, by: base.ref, sourceUrl: base.url },
              { value: reviewedEntry.value, by: reviewedEntry.ref, sourceUrl: reviewedEntry.url },
            ]),
            'official_source_disagreement',
            at,
          );
          conflicts[field] = (conflicts[field] ?? 0) + 1;
          return null;
        }
        return reviewedEntry;
      };

      const context = mergeOfficial('context', resolveContext(input), reviewedValue(reviewed?.context));
      const output = mergeOfficial('maxOutput', resolveMaxOutput(input), reviewedValue(reviewed?.maxOutput));
      const modalities = mergeOfficial('modalities', resolveModalities(input), reviewedValue(reviewed?.inputModalities));
      const tools = mergeOfficial('tools', resolveCapability('tools', input), reviewedValue(reviewed?.tools));
      const reasoning = mergeOfficial('reasoning', resolveCapability('reasoning', input), reviewedValue(reviewed?.reasoning));
      const structured = mergeOfficial('structured', resolveCapability('structured', input), reviewedValue(reviewed?.structured));
      const attachment = mergeOfficial('attachment', resolveCapability('attachment', input), reviewedValue(reviewed?.attachment));
      const cost = resolveCost(input, billing[r.provider_id] ?? DEFAULT_BILLING);

      const record = (
        field: string,
        v: { value: unknown; source: string; ref: string; url: string | null; state: string; raw: unknown } | null,
        wasNull: boolean,
      ) => {
        if (!v) {
          stillMissing[field] = (stillMissing[field] ?? 0) + 1;
          return;
        }
        if (wasNull && v.source !== 'models.dev') filled[field] = (filled[field] ?? 0) + 1;
        writeFact(r, field, v);
      };

      record('context', context, r.context_tokens === null);
      record('maxOutput', output, r.output_tokens === null);
      record('modalities', modalities, r.input_modalities === null);
      record('tools', tools, r.tools === null);
      record('reasoning', reasoning, r.reasoning === null);
      record('structured', structured, r.structured === null);
      record('attachment', attachment, r.attachment === null);
      // Cost is three facts with three different sources, not one blob. Written
      // as one value under a single label, the reference price silently
      // inherited the effective price's provenance — a market rate wearing this
      // provider's label is the error the split exists to prevent.
      writeFact(r, 'billingKind', {
        value: cost.kind, source: cost.source, ref: cost.ref, url: cost.url, state: cost.state, raw: cost.kind,
      });
      if (cost.inPerM !== null || cost.outPerM !== null) {
        writeFact(r, 'effectivePrice', {
          value: { inPerM: cost.inPerM, outPerM: cost.outPerM },
          source: 'models.dev', ref: 'cost', url: SOURCE_URL['models.dev'], state: 'first_party',
          raw: { input: cost.inPerM, output: cost.outPerM },
        });
      }
      // Absent when the index has no entry — and never written for a provider
      // that publishes no price, because "what it lists for elsewhere" is not an
      // answer to "what does it cost here".
      if (cost.refInPerM !== null || cost.refOutPerM !== null) {
        writeFact(r, 'referencePrice', {
          value: { inPerM: cost.refInPerM, outPerM: cost.refOutPerM },
          source: 'openrouter', ref: `${canon?.id ?? 'unknown'}.pricing`,
          url: SOURCE_URL.openrouter, state: 'index_confirmation',
          raw: { input: cost.refInPerM, output: cost.refOutPerM },
        });
      }
      costKinds[cost.kind] = (costKinds[cost.kind] ?? 0) + 1;

      update.run(
        context ? Number(context.value) : null,
        output ? Number(output.value) : null,
        modalities ? JSON.stringify(modalities.value) : null,
        tools === null ? null : Number(tools.value),
        reasoning === null ? null : Number(reasoning.value),
        structured === null ? null : Number(structured.value),
        attachment === null ? null : Number(attachment.value),
        cost.inPerM, cost.outPerM, cost.refInPerM, cost.refOutPerM, cost.kind,
        r.provider_id, r.model_id,
      );

      // Whatever the row still carries that this run did not prove is evidence
      // that outlived its source. Left in place it produces the contradiction
      // this prune exists to end: a field reported as "published by nobody"
      // sitting above its own stale provenance row.
      //
      // One source is an exception: `provider_api` is optional per orchestration
      // path (only the CLI's second pass — or, since the sync-runner fix, the
      // service's own second pass — asks it) and can fail per row even when it
      // IS asked. `input.detail` is exactly the answer to "was it consulted,
      // successfully, THIS run, for THIS row" — `deps.details?.get(key) ?? null`,
      // computed above. Null covers both "not asked" and "asked and failed";
      // either way, silence from an optional source is not proof it withdrew a
      // fact a run that DID ask it once established.
      for (const f of heldFacts.all(r.provider_id, r.model_id) as unknown as { field: string; source: string }[]) {
        if (provenFacts.has(f.field)) continue;
        if (f.source === 'provider_api' && input.detail === null) {
          protectedStale[f.field] = (protectedStale[f.field] ?? 0) + 1;
          continue;
        }
        dropFact.run(r.provider_id, r.model_id, f.field);
        retired[f.field] = (retired[f.field] ?? 0) + 1;
      }
      for (const c of heldConflictFields.all(r.provider_id, r.model_id) as unknown as { field: string }[]) {
        if (provenConflicts.has(c.field)) continue;
        dropConflict.run(r.provider_id, r.model_id, c.field);
        retired[`conflict:${c.field}`] = (retired[`conflict:${c.field}`] ?? 0) + 1;
      }
    }
  });

  return { rows: rows.length, filled, stillMissing, costKinds, conflicts, retired, protectedStale };
}

/**
 * Which rows are worth a provider detail call.
 *
 * Only rows still missing something after the free sources. A detail endpoint is
 * a per-model HTTP request, so asking it about 116 models to answer 4 questions
 * would be rude to the provider and slow for no gain.
 */
export function rowsNeedingDetail(db: Db, providers: string[]): { providerId: string; modelId: string }[] {
  const placeholders = providers.map(() => '?').join(',');
  return db
    .prepare(
      `SELECT provider_id AS providerId, model_id AS modelId FROM models
       WHERE status IN ('active','missing') AND provider_id IN (${placeholders})
         AND (context_tokens IS NULL OR output_tokens IS NULL OR input_modalities IS NULL
              OR tools IS NULL OR reasoning IS NULL OR structured IS NULL)`,
    )
    .all(...providers) as unknown as { providerId: string; modelId: string }[];
}

/** Project the OpenRouter payload into the shape the resolvers consume. */
export function canonicalFromBenchmarks(b: BenchmarkSource): { index: IdentityIndex; byId: Map<string, CanonicalRecord> } {
  const byId = new Map<string, CanonicalRecord>();
  for (const [id, rec] of b.byId) {
    const raw = rec as unknown as {
      contextLength?: number; maxCompletionTokens?: number; inputModalities?: string[];
      supportedParameters?: string[]; costOutPerM?: number; costInPerM?: number;
    };
    byId.set(id, {
      id,
      contextLength: raw.contextLength,
      maxCompletionTokens: raw.maxCompletionTokens,
      inputModalities: raw.inputModalities,
      supportedParameters: raw.supportedParameters,
      refCostInPerM: raw.costInPerM,
      refCostOutPerM: raw.costOutPerM,
    });
  }
  return { index: b.index, byId };
}
