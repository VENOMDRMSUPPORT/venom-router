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
  type CanonicalRecord, type BillingModel,
} from './resolvers.ts';
import type { ModelSpec } from '../engine.ts';

export interface EnrichDeps {
  db: Db;
  /** Intrinsic model properties pooled across every provider in the spec feed. */
  intrinsic: (modelId: string) => import('./resolvers.ts').IntrinsicFacts | null;
  canonical: { index: IdentityIndex; byId: Map<string, CanonicalRecord> };
  overlay: Record<string, string>;
  /** How each provider charges. Declared, because no feed publishes it. */
  billing: Record<string, BillingModel>;
  now: () => string;
}

export interface EnrichSummary {
  rows: number;
  /** Fields newly filled from a fallback source, by field. */
  filled: Record<string, number>;
  /** Fields still unresolved after every source, by field. */
  stillMissing: Record<string, number>;
  costKinds: Record<string, number>;
}

interface Row {
  provider_id: string; model_id: string;
  context_tokens: number | null; output_tokens: number | null; input_modalities: string | null;
  tools: number | null; reasoning: number | null; structured: number | null;
  cost_in_per_m: number | null; cost_out_per_m: number | null;
}

const bool = (v: number | null): boolean | undefined => (v === null ? undefined : Boolean(v));

/**
 * Rebuild the feed's view of a row from what the engine stored.
 *
 * The engine has already written the provider feed's values, so the stored row
 * IS the feed's answer for those fields. Re-reading them here keeps the resolver
 * ordering honest without holding the whole feed in memory a second time.
 */
function specFromRow(r: Row): ModelSpec {
  return {
    contextTokens: r.context_tokens ?? undefined,
    outputTokens: r.output_tokens ?? undefined,
    inputModalities: r.input_modalities ? (JSON.parse(r.input_modalities) as string[]) : undefined,
    tools: bool(r.tools),
    reasoning: bool(r.reasoning),
    structured: bool(r.structured),
    costInPerM: r.cost_in_per_m ?? undefined,
    costOutPerM: r.cost_out_per_m ?? undefined,
  };
}

export function enrich(deps: EnrichDeps): EnrichSummary {
  const { db, canonical, overlay, billing, now } = deps;
  const rows = db
    .prepare(`SELECT provider_id, model_id, context_tokens, output_tokens, input_modalities,
                     tools, reasoning, structured, cost_in_per_m, cost_out_per_m
              FROM models WHERE status IN ('active','missing')`)
    .all() as unknown as Row[];

  const filled: Record<string, number> = {};
  const stillMissing: Record<string, number> = {};
  const costKinds: Record<string, number> = {};
  const at = now();

  transaction(db, () => {
    const fact = db.prepare(
      `INSERT INTO model_facts (provider_id, model_id, field, value, source, source_ref, resolved_at)
       VALUES (?,?,?,?,?,?,?)
       ON CONFLICT(provider_id, model_id, field) DO UPDATE SET
         value = excluded.value, source = excluded.source,
         source_ref = excluded.source_ref, resolved_at = excluded.resolved_at`,
    );
    const update = db.prepare(
      `UPDATE models SET context_tokens = ?, output_tokens = ?, input_modalities = ?,
                         tools = ?, reasoning = ?, structured = ?,
                         cost_in_per_m = ?, cost_out_per_m = ?,
                         ref_cost_in_per_m = ?, ref_cost_out_per_m = ?, cost_kind = ?
       WHERE provider_id = ? AND model_id = ?`,
    );

    for (const r of rows) {
      const res = resolveIdentity(r.model_id, canonical.index, overlay);
      const canon = res.status === 'resolved' ? canonical.byId.get(res.target) ?? null : null;
      const spec = specFromRow(r);
      const input = { spec, intrinsic: deps.intrinsic(r.model_id), canonical: canon };

      const context = resolveContext(input);
      const output = resolveMaxOutput(input);
      const modalities = resolveModalities(input);
      const tools = resolveCapability('tools', input);
      const reasoning = resolveCapability('reasoning', input);
      const structured = resolveCapability('structured', input);
      const cost = resolveCost(input, billing[r.provider_id] ?? 'per_token');

      const record = (field: string, v: { value: unknown; source: string; ref: string } | null, wasNull: boolean) => {
        if (!v) {
          stillMissing[field] = (stillMissing[field] ?? 0) + 1;
          return;
        }
        if (wasNull && v.source !== 'models.dev') filled[field] = (filled[field] ?? 0) + 1;
        fact.run(r.provider_id, r.model_id, field, JSON.stringify(v.value), v.source, v.ref, at);
      };

      record('context', context, r.context_tokens === null);
      record('maxOutput', output, r.output_tokens === null);
      record('modalities', modalities, r.input_modalities === null);
      record('tools', tools, r.tools === null);
      record('reasoning', reasoning, r.reasoning === null);
      record('structured', structured, r.structured === null);
      fact.run(r.provider_id, r.model_id, 'cost', JSON.stringify({ kind: cost.kind, inPerM: cost.inPerM, outPerM: cost.outPerM }), cost.source, cost.ref, at);
      costKinds[cost.kind] = (costKinds[cost.kind] ?? 0) + 1;

      update.run(
        context?.value ?? null,
        output?.value ?? null,
        modalities ? JSON.stringify(modalities.value) : null,
        tools === null ? null : Number(tools.value),
        reasoning === null ? null : Number(reasoning.value),
        structured === null ? null : Number(structured.value),
        cost.inPerM, cost.outPerM, cost.refInPerM, cost.refOutPerM, cost.kind,
        r.provider_id, r.model_id,
      );
    }
  });

  return { rows: rows.length, filled, stillMissing, costKinds };
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
