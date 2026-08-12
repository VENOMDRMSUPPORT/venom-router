/**
 * The one sync pipeline. Every provider runs through this; a provider adapter
 * declares what to fetch and how to read it, and contributes no control flow.
 *
 * A second copy of this logic per provider would be a defect under CLAUDE.md §7,
 * not a variation — and it would mean a new provider silently missing the
 * failure layers below.
 *
 * Layers implemented here: 1 isolation, 3 contract validation, 4 delta gate,
 * 5 two-strike retirement, 6 atomicity, 7 no physical delete.
 * Layer 2 lives in http.ts; layer 8 is a property of the read path.
 */

import type { Db } from '../db/index.ts';
import { transaction } from '../db/index.ts';
import type { FetchJson } from './http.ts';
import { FetchFailure } from './http.ts';

export interface ProviderAdapter {
  id: string;
  name: string;
  rosterUrl: string;
  /** models.dev provider key, when the feed carries one. */
  feedKey?: string;
  /**
   * Read the roster response into model ids. Throws on a shape it does not
   * recognise — that throw is the contract check, and it must not be softened
   * into a best-effort parse.
   */
  parseRoster(body: unknown): string[];
}

/** Per-model facts from the spec feed. All optional: absent means "not published". */
export interface ModelSpec {
  displayName?: string;
  contextTokens?: number;
  outputTokens?: number;
  inputModalities?: string[];
  tools?: boolean;
  reasoning?: boolean;
  structured?: boolean;
  attachment?: boolean;
  costInPerM?: number;
  costOutPerM?: number;
}

export type SpecLookup = (feedKey: string | undefined, modelId: string) => ModelSpec | null;

export interface EngineOptions {
  /** Layer 4: a run removing more than this fraction is quarantined. */
  maxRemovalRatio: number;
  /** Layer 4: ...or more than this many models, whichever triggers first. */
  maxRemovalCount: number;
  /** Layer 5: consecutive absences before a model is retired. */
  retireAfterMisses: number;
}

export const DEFAULT_OPTIONS: EngineOptions = {
  maxRemovalRatio: 0.3,
  maxRemovalCount: 5,
  retireAfterMisses: 3,
};

export interface SyncDeps {
  db: Db;
  fetchJson: FetchJson;
  now: () => string;
  lookupSpec: SpecLookup;
  options?: Partial<EngineOptions>;
}

export type Outcome = 'ok' | 'failed' | 'quarantined';

export interface RunResult {
  provider: string;
  outcome: Outcome;
  rosterCount: number;
  added: string[];
  removed: string[];
  changed: number;
  error?: string;
  /** Set when layer 4 fired, so the caller can explain the refusal. */
  quarantineReason?: string;
}

/** Layer 3. A roster that fails any of these is rejected whole. */
function validateRoster(ids: unknown): string[] {
  if (!Array.isArray(ids)) throw new Error('roster parser did not return an array');
  const out: string[] = [];
  const seen = new Set<string>();
  for (const id of ids) {
    if (typeof id !== 'string' || id.trim() === '') throw new Error(`roster contains a non-string id: ${JSON.stringify(id)}`);
    if (seen.has(id)) continue;
    seen.add(id);
    out.push(id);
  }
  if (out.length === 0) throw new Error('roster is empty');
  return out;
}

interface StoredModel {
  model_id: string;
  status: string;
  miss_count: number;
  context_tokens: number | null;
  output_tokens: number | null;
}

function specChanges(before: StoredModel | undefined, spec: ModelSpec | null): number {
  if (!before || !spec) return 0;
  let n = 0;
  if (spec.contextTokens !== undefined && spec.contextTokens !== before.context_tokens) n++;
  if (spec.outputTokens !== undefined && spec.outputTokens !== before.output_tokens) n++;
  return n;
}

/**
 * Sync one provider. Never throws for an upstream problem: an unreachable
 * provider is a recorded `failed` run, not an exception that takes down a batch
 * (layer 1).
 */
export async function syncProvider(adapter: ProviderAdapter, deps: SyncDeps): Promise<RunResult> {
  const opts = { ...DEFAULT_OPTIONS, ...deps.options };
  const { db, now } = deps;
  const startedAt = now();

  db.prepare(
    `INSERT INTO providers (id, name, roster_url, feed_key)
     VALUES (?, ?, ?, ?)
     ON CONFLICT(id) DO UPDATE SET name = excluded.name, roster_url = excluded.roster_url, feed_key = excluded.feed_key`,
  ).run(adapter.id, adapter.name, adapter.rosterUrl, adapter.feedKey ?? null);

  const runId = Number(
    db.prepare('INSERT INTO sync_runs (provider_id, started_at) VALUES (?, ?) RETURNING id').get(adapter.id, startedAt)!.id,
  );

  const fail = (error: string, status: number | null): RunResult => {
    db.prepare('UPDATE sync_runs SET finished_at = ?, outcome = ?, error = ?, http_status = ? WHERE id = ?')
      .run(now(), 'failed', error, status, runId);
    db.prepare('UPDATE providers SET last_sync_at = ?, last_outcome = ? WHERE id = ?').run(now(), 'failed', adapter.id);
    return { provider: adapter.id, outcome: 'failed', rosterCount: 0, added: [], removed: [], changed: 0, error };
  };

  let roster: string[];
  let httpStatus: number | null = null;
  try {
    const res = await deps.fetchJson(adapter.rosterUrl);
    httpStatus = res.status;
    roster = validateRoster(adapter.parseRoster(res.body));
  } catch (err) {
    const status = err instanceof FetchFailure ? err.status : httpStatus;
    return fail(err instanceof Error ? err.message : String(err), status);
  }

  const stored = db
    .prepare(`SELECT model_id, status, miss_count, context_tokens, output_tokens FROM models WHERE provider_id = ? AND status != 'retired'`)
    .all(adapter.id) as unknown as StoredModel[];
  const storedById = new Map(stored.map((m) => [m.model_id, m]));
  const live = new Set(roster);

  const added = roster.filter((id) => !storedById.has(id));
  const absent = stored.filter((m) => !live.has(m.model_id));

  // Layer 4. Removal is the destructive direction, so it is the gated one.
  // Additions are never gated: a provider launching models is not a failure.
  const denominator = stored.length || roster.length;
  const ratio = denominator === 0 ? 0 : absent.length / denominator;
  if (absent.length > 0 && (ratio > opts.maxRemovalRatio || absent.length > opts.maxRemovalCount)) {
    const reason = `would remove ${absent.length}/${denominator} models (${Math.round(ratio * 100)}%) — over the ${Math.round(opts.maxRemovalRatio * 100)}% / ${opts.maxRemovalCount} gate`;
    db.prepare('UPDATE sync_runs SET finished_at = ?, outcome = ?, roster_count = ?, removed = ?, error = ?, http_status = ? WHERE id = ?')
      .run(now(), 'quarantined', roster.length, absent.length, reason, httpStatus, runId);
    db.prepare('UPDATE providers SET last_sync_at = ?, last_outcome = ? WHERE id = ?').run(now(), 'quarantined', adapter.id);
    return {
      provider: adapter.id, outcome: 'quarantined', rosterCount: roster.length,
      added: [], removed: [], changed: 0, quarantineReason: reason,
    };
  }

  const retired: string[] = [];
  let changed = 0;

  // Layer 6: one transaction for the whole provider.
  transaction(db, () => {
    const at = now();
    const event = db.prepare(
      'INSERT INTO model_events (run_id, provider_id, model_id, kind, field, old_value, new_value, reason, at) VALUES (?,?,?,?,?,?,?,?,?)',
    );

    for (const id of roster) {
      const spec = deps.lookupSpec(adapter.feedKey, id);
      const before = storedById.get(id);
      changed += specChanges(before, spec);

      db.prepare(
        `INSERT INTO models (provider_id, model_id, display_name, context_tokens, output_tokens, input_modalities,
                             tools, reasoning, structured, attachment, cost_in_per_m, cost_out_per_m, spec_source,
                             status, first_seen_at, last_seen_at, missing_since, miss_count)
         VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?, 'active', ?, ?, NULL, 0)
         ON CONFLICT(provider_id, model_id) DO UPDATE SET
           display_name = excluded.display_name, context_tokens = excluded.context_tokens,
           output_tokens = excluded.output_tokens, input_modalities = excluded.input_modalities,
           tools = excluded.tools, reasoning = excluded.reasoning, structured = excluded.structured,
           attachment = excluded.attachment, cost_in_per_m = excluded.cost_in_per_m,
           cost_out_per_m = excluded.cost_out_per_m, spec_source = excluded.spec_source,
           status = 'active', last_seen_at = excluded.last_seen_at, missing_since = NULL, miss_count = 0`,
      ).run(
        adapter.id, id, spec?.displayName ?? id, spec?.contextTokens ?? null, spec?.outputTokens ?? null,
        spec?.inputModalities ? JSON.stringify(spec.inputModalities) : null,
        spec?.tools === undefined ? null : Number(spec.tools),
        spec?.reasoning === undefined ? null : Number(spec.reasoning),
        spec?.structured === undefined ? null : Number(spec.structured),
        spec?.attachment === undefined ? null : Number(spec.attachment),
        spec?.costInPerM ?? null, spec?.costOutPerM ?? null, spec ? 'models.dev' : null, at, at,
      );

      if (!before) event.run(runId, adapter.id, id, 'added', null, null, id, null, at);
      else if (before.status === 'missing') event.run(runId, adapter.id, id, 'readded', null, 'missing', 'active', 'reappeared upstream', at);
    }

    // Layer 5 + 7: absence increments a counter; only the third consecutive one
    // retires the model, and even then the row survives with a status change.
    for (const m of absent) {
      const misses = m.miss_count + 1;
      if (misses >= opts.retireAfterMisses) {
        db.prepare(`UPDATE models SET status = 'retired', miss_count = ? WHERE provider_id = ? AND model_id = ?`).run(misses, adapter.id, m.model_id);
        event.run(runId, adapter.id, m.model_id, 'removed', null, m.status, 'retired', `absent for ${misses} consecutive syncs`, at);
        retired.push(m.model_id);
      } else {
        db.prepare(`UPDATE models SET status = 'missing', miss_count = ?, missing_since = COALESCE(missing_since, ?) WHERE provider_id = ? AND model_id = ?`)
          .run(misses, at, adapter.id, m.model_id);
        if (m.status !== 'missing') event.run(runId, adapter.id, m.model_id, 'changed', 'status', m.status, 'missing', `absent ${misses}/${opts.retireAfterMisses}`, at);
      }
    }

    db.prepare('UPDATE sync_runs SET finished_at = ?, outcome = ?, roster_count = ?, added = ?, removed = ?, changed = ?, http_status = ? WHERE id = ?')
      .run(at, 'ok', roster.length, added.length, retired.length, changed, httpStatus, runId);
    db.prepare('UPDATE providers SET last_sync_at = ?, last_success_at = ?, last_outcome = ? WHERE id = ?').run(at, at, 'ok', adapter.id);
  });

  return { provider: adapter.id, outcome: 'ok', rosterCount: roster.length, added, removed: retired, changed };
}
