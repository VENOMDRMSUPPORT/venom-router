/**
 * The one sync pipeline. Every provider runs through this; a provider adapter
 * declares what to fetch and how to read it, and contributes no control flow.
 *
 * A second copy of this logic per provider would be a defect under CLAUDE.md §7,
 * not a variation — and it would mean a new provider silently missing the
 * failure layers below.
 *
 * Layers implemented here: 1 isolation, 3 contract validation, 4 delta gate,
 * 5 first-miss retirement, 6 atomicity, 7 no physical delete.
 * Layer 2 lives in http.ts; layer 8 is a property of the read path.
 */

import type { Db } from '../db/index.ts';
import { transaction } from '../db/index.ts';
import type { FetchJson } from './http.ts';
import { FetchFailure } from './http.ts';
import { completeResolutionJob } from './resolution-jobs.ts';

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
  /**
   * What of this provider's roster reaches the PUBLISHED catalog.
   *
   *   'all'        (default) every rostered model is published
   *   'free_only'  only models proven free (a models.dev zero price) are
   *                published; the rest are `excluded` with a reason. For a
   *                provider the owner declares fully free but whose feed still
   *                lists paid models (OpenCode Zen), so a paid model is never
   *                presented as if this provider served it free.
   *
   * Applied as a pipeline step AFTER the roster is stored, never inside the
   * engine — it must not perturb the delta gate, and it is a publish decision,
   * not a fetch or a lifecycle transition driven by absence.
   */
  publishPolicy?: 'all' | 'free_only';
  /**
   * Exact offer ids withheld despite appearing in the public roster.
   *
   * This is for a provider-owned access verdict, not a benchmark verdict. The
   * row remains in history, but it cannot be advertised in the public catalog
   * when the configured product tier cannot call it.
   */
  /**
   * Ids the provider rosters but this deployment cannot publish, each with the
   * reason it cannot. Not every refusal is the same refusal, and flattening
   * them loses the only thing an owner can act on.
   */
  publishExclusions?: Record<string, 'plan_required' | 'provider_unsupported' | 'consent_required'>;
  /**
   * The provider's own published free-model ids, reviewed by hand.
   *
   * When set on a `free_only` provider, THIS list is the proof of freeness —
   * not a third-party price feed. The roster endpoint carries no prices, so an
   * index transcribing a zero for an id the provider has not listed as free is
   * a claim about an offer the provider has not made, and publishing it would
   * put the catalog ahead of the provider's own storefront. Review date and
   * source are carried with the ids so the list is auditable and re-checkable.
   */
  officialFreeList?: {
    ids: readonly string[];
    reviewedAt: string;
    sourceUrl: string;
  };
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
  /**
   * The vendor's own lifecycle marker, straight from the feed. `deprecated`
   * means the provider is retiring the model — its own app stops offering it —
   * so a catalog for CHOOSING a model must stop recommending it.
   */
  status?: string;
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
  retireAfterMisses: 1,
};

export interface SyncDeps {
  db: Db;
  fetchJson: FetchJson;
  now: () => string;
  lookupSpec: SpecLookup;
  options?: Partial<EngineOptions>;
  /**
   * Reviewed display name for an offering, or undefined to fall through.
   *
   * Wins over the spec feed's transcription: the provider's own documentation is
   * first party about its own product names, the same precedence the fact
   * resolvers apply. See sync/display-names.ts for the overlay contract.
   */
  displayNameFor?: (providerId: string, modelId: string) => string | undefined;
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
  /**
   * What the feed published for the TRACKED fields the last time this row was
   * synced, as written by this engine and by nothing else.
   *
   * The diff cannot use the `models` columns for this. Those hold the RESOLVED
   * answer, and the enrichment pass is authoritative over them: it moves a
   * subscription provider's per-token numbers into the reference columns and
   * NULLs the effective ones, and it lets a reviewed fact override the feed's
   * output limit. Comparing the feed against a column someone else owns
   * re-reported the same difference on every single run — `cost_out_per_m
   * null -> 1.6` thirteen times for one row, 298 of 459 events in the live
   * ledger — while a genuine price change would have been invisible in the
   * noise. So the feed is compared against the feed.
   *
   * NULL means no baseline was ever recorded (a row written before this column
   * existed). That is not evidence of a change, so the run seeds it and reports
   * nothing: an unknown previous value cannot support a claim about movement.
   */
  feed_tracked_json: string | null;
}

/**
 * Fields whose change is worth telling a reader about, and the class each change
 * belongs to.
 *
 * Only these are compared. A field absent from this list cannot generate an
 * event, which is what keeps `/v1/changes` from filling up with noise — a
 * re-fetch that returns identical data must produce zero events, not 116 rows
 * saying "observed again".
 */
const TRACKED: {
  field: string;
  cls: 'context' | 'price' | 'capability';
  incoming: (s: ModelSpec) => string | null;
}[] = [
  { field: 'context_tokens', cls: 'context', incoming: (s) => str(s.contextTokens) },
  { field: 'output_tokens', cls: 'context', incoming: (s) => str(s.outputTokens) },
  { field: 'cost_in_per_m', cls: 'price', incoming: (s) => str(s.costInPerM) },
  { field: 'cost_out_per_m', cls: 'price', incoming: (s) => str(s.costOutPerM) },
  { field: 'tools', cls: 'capability', incoming: (s) => bool(s.tools) },
  { field: 'reasoning', cls: 'capability', incoming: (s) => bool(s.reasoning) },
  { field: 'structured', cls: 'capability', incoming: (s) => bool(s.structured) },
  { field: 'attachment', cls: 'capability', incoming: (s) => bool(s.attachment) },
  { field: 'input_modalities', cls: 'capability', incoming: (s) => (s.inputModalities ? JSON.stringify(s.inputModalities) : null) },
];

const str = (v: number | null | undefined) => (v === null || v === undefined ? null : String(v));
const bool = (v: boolean | undefined) => (v === undefined ? null : String(v));

/**
 * The feed's own answer for every tracked field, in the exact string form the
 * diff compares. Written to `models.feed_tracked_json` on every sync, so the
 * next run has a baseline that belongs to the feed.
 */
export function feedSnapshot(spec: ModelSpec | null): Record<string, string | null> {
  const out: Record<string, string | null> = {};
  for (const t of TRACKED) out[t.field] = spec ? t.incoming(spec) : null;
  return out;
}

function readSnapshot(json: string | null): Record<string, string | null> | null {
  if (json === null) return null;
  try {
    const parsed: unknown = JSON.parse(json);
    if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) return null;
    const snapshot: Record<string, string | null> = {};
    for (const [field, value] of Object.entries(parsed as Record<string, unknown>)) {
      // Every value is a string or null, because that is the only shape this
      // engine writes. One value of any other type discards the WHOLE snapshot:
      // a number 1.6 would never equal the feed's `'1.6'` and would report a
      // change nobody made, and a wrong previous value is worse than no
      // baseline — for which the rule is already "seed it, claim nothing".
      if (value !== null && typeof value !== 'string') return null;
      snapshot[field] = value;
    }
    return snapshot;
  } catch {
    // A snapshot we cannot read is not a previous value we can cite. Same rule
    // as a missing one: seed it, claim nothing.
    return null;
  }
}

export interface FieldChange {
  field: string;
  cls: string;
  from: string | null;
  to: string | null;
}

/**
 * Diff a stored row against incoming specs.
 *
 * A field the feed stops publishing is NOT reported as a change to null: losing
 * sight of a value is not the same event as the value changing, and conflating
 * them would show "context dropped to unknown" every time the feed hiccups.
 */
function specChanges(before: StoredModel | undefined, spec: ModelSpec | null): FieldChange[] {
  if (!before || !spec) return [];
  const baseline = readSnapshot(before.feed_tracked_json);
  if (!baseline) return [];
  const out: FieldChange[] = [];
  for (const t of TRACKED) {
    const from = baseline[t.field] ?? null;
    const to = t.incoming(spec);
    if (to === null) continue;
    if (from !== to) out.push({ field: t.field, cls: t.cls, from, to });
  }
  return out;
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
    .prepare(`SELECT model_id, status, miss_count, feed_tracked_json
              FROM models WHERE provider_id = ? AND status != 'retired'`)
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
    // Prepared once per provider, like `event` beside it. Inside the loop this
    // compiled the same statement again for every id in the roster.
    const upsertModel = db.prepare(
      `INSERT INTO models (provider_id, model_id, display_name, context_tokens, output_tokens, input_modalities,
                           tools, reasoning, structured, attachment, cost_in_per_m, cost_out_per_m, spec_source,
                           feed_tracked_json, lifecycle_status, status, first_seen_at, last_seen_at, missing_since, miss_count)
       VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?, 'active', ?, ?, NULL, 0)
       ON CONFLICT(provider_id, model_id) DO UPDATE SET
         display_name = excluded.display_name, context_tokens = excluded.context_tokens,
         output_tokens = excluded.output_tokens, input_modalities = excluded.input_modalities,
         tools = excluded.tools, reasoning = excluded.reasoning, structured = excluded.structured,
         attachment = excluded.attachment, cost_in_per_m = excluded.cost_in_per_m,
         cost_out_per_m = excluded.cost_out_per_m, spec_source = excluded.spec_source,
         feed_tracked_json = excluded.feed_tracked_json,
         lifecycle_status = excluded.lifecycle_status, status = 'active', last_seen_at = excluded.last_seen_at, missing_since = NULL, miss_count = 0`,
    );

    for (const id of roster) {
      const spec = deps.lookupSpec(adapter.feedKey, id);
      const before = storedById.get(id);
      const diffs = specChanges(before, spec);
      changed += diffs.length;

      upsertModel.run(
        adapter.id, id, deps.displayNameFor?.(adapter.id, id) ?? spec?.displayName ?? id,
        spec?.contextTokens ?? null, spec?.outputTokens ?? null,
        spec?.inputModalities ? JSON.stringify(spec.inputModalities) : null,
        spec?.tools === undefined ? null : Number(spec.tools),
        spec?.reasoning === undefined ? null : Number(spec.reasoning),
        spec?.structured === undefined ? null : Number(spec.structured),
        spec?.attachment === undefined ? null : Number(spec.attachment),
        spec?.costInPerM ?? null, spec?.costOutPerM ?? null, spec ? 'models.dev' : null,
        // The baseline for the NEXT run's diff. Written unconditionally, so a
        // feed that goes quiet leaves a recorded "published nothing" rather
        // than a stale copy of what it used to say.
        JSON.stringify(feedSnapshot(spec)),
        spec?.status ?? null, at, at,
      );

      if (!before) event.run(runId, adapter.id, id, 'added', null, null, id, null, at);
      else if (before.status === 'missing') event.run(runId, adapter.id, id, 'readded', null, 'missing', 'active', 'reappeared upstream', at);
      for (const d of diffs) event.run(runId, adapter.id, id, 'changed', d.field, d.from, d.to, d.cls, at);
    }

    // Layer 5 + 7: a successful roster is the provider's existence declaration.
    // Its first omission retires the model, and the row survives with a status
    // change. Failed or quarantined runs never reach this loop.
    for (const m of absent) {
      const misses = m.miss_count + 1;
      if (misses >= opts.retireAfterMisses) {
        db.prepare(`UPDATE models SET status = 'retired', miss_count = ? WHERE provider_id = ? AND model_id = ?`).run(misses, adapter.id, m.model_id);
        // A retired offering cannot have a live resolution attempt. Cleared in
        // this same provider transaction so the scheduler cannot observe a due
        // job after the lifecycle transition commits — through the queue's own
        // module, because what "finished" means belongs to it, not to here.
        completeResolutionJob(db, adapter.id, m.model_id, at);
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
