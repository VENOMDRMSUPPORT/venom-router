/**
 * `/v1/changes` — what actually changed, computed from recorded events.
 *
 * The source is `model_events`, written inside the same transaction that applied
 * the change, so a change cannot exist without its event and an event cannot
 * exist without the change. Nothing here is inferred from comparing two API
 * responses after the fact.
 *
 * Noise control is at the write end: the engine only records fields on its
 * tracked list, and only when the value actually moved. A sync that re-fetches
 * identical data produces zero events, not one per model.
 */

import type { Db } from '../db/index.ts';

/** Change classes a reader cares about. Anything else is not surfaced. */
export type ChangeClass =
  | 'added'
  | 'retired'
  | 'readded'
  | 'became_missing'
  | 'price_changed'
  | 'context_changed'
  | 'capability_changed'
  | 'quality_became_available'
  | 'quality_evidence_upgraded'
  | 'quality_changed'
  | 'quality_lost';

export interface Change {
  class: ChangeClass;
  providerId: string;
  modelId: string;
  field: string | null;
  from: string | null;
  to: string | null;
  /** Human-readable reason, straight from the event that recorded it. */
  note: string | null;
  observedAt: string;
}

const LEVEL_RANK: Record<string, number> = { unrated: 0, bounded: 1, calibrated: 2, measured: 3 };

interface EventRow {
  provider_id: string; model_id: string; kind: string; field: string | null;
  old_value: string | null; new_value: string | null; reason: string | null; at: string;
}

/**
 * How much a score must move to be worth reporting.
 *
 * A calibrated value carries several points of uncertainty, so a 0.4-point
 * wobble after a refit is not news — reporting it would bury the real changes.
 */
export const MATERIAL_SCORE_DELTA = 1;

function classify(e: EventRow): ChangeClass | null {
  switch (e.kind) {
    case 'added':
      return 'added';
    case 'readded':
      return 'readded';
    case 'removed':
      return 'retired';
    case 'changed':
      if (e.field === 'status') return e.new_value === 'missing' ? 'became_missing' : null;
      // `reason` carries the tracked field's class, set by the engine.
      if (e.reason === 'price') return 'price_changed';
      if (e.reason === 'context') return 'context_changed';
      if (e.reason === 'capability') return 'capability_changed';
      return null;
    case 'score_changed': {
      if (e.field !== 'VQ') return null;
      const [from, to] = (e.reason ?? '').split(' -> ');
      if (from === 'unrated' && to && to !== 'unrated') return 'quality_became_available';
      if (to === 'unrated' && from && from !== 'unrated') return 'quality_lost';
      if (from && to && LEVEL_RANK[to] > LEVEL_RANK[from]) return 'quality_evidence_upgraded';
      const a = Number(e.old_value);
      const bb = Number(e.new_value);
      if (Number.isFinite(a) && Number.isFinite(bb) && Math.abs(bb - a) >= MATERIAL_SCORE_DELTA) return 'quality_changed';
      return null; // a sub-threshold wobble is not a change worth showing
    }
    default:
      return null;
  }
}

export interface ChangesQuery {
  since?: string;
  limit?: number;
}

export interface ChangesResult {
  since: string | null;
  /** Newest event timestamp in the store, so a client can poll from here next. */
  cursor: string | null;
  total: number;
  byClass: Record<string, number>;
  changes: Change[];
}

export function loadChanges(db: Db, { since, limit = 500 }: ChangesQuery = {}): ChangesResult {
  const rows = (
    since
      ? db.prepare('SELECT * FROM model_events WHERE at > ? ORDER BY at DESC, id DESC LIMIT ?').all(since, limit * 4)
      : db.prepare('SELECT * FROM model_events ORDER BY at DESC, id DESC LIMIT ?').all(limit * 4)
  ) as unknown as EventRow[];

  const changes: Change[] = [];
  for (const e of rows) {
    const cls = classify(e);
    if (!cls) continue;
    changes.push({
      class: cls,
      providerId: e.provider_id,
      modelId: e.model_id,
      field: e.field,
      from: e.old_value,
      to: e.new_value,
      note: e.reason,
      observedAt: e.at,
    });
    if (changes.length >= limit) break;
  }

  const newest = db.prepare('SELECT MAX(at) at FROM model_events').get() as unknown as { at: string | null };
  const byClass: Record<string, number> = {};
  for (const c of changes) byClass[c.class] = (byClass[c.class] ?? 0) + 1;

  return { since: since ?? null, cursor: newest.at, total: changes.length, byClass, changes };
}
