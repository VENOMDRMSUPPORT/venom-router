import type { Db } from '../db/index.ts';

export const FIRST_RETRY_MS = 60_000;
export const RESOLUTION_WINDOW_MS = 5 * 60_000;

export type ModelResolutionState =
  | 'complete'
  | 'processing'
  | 'awaiting_external_benchmark'
  | 'source_incomplete'
  | 'unknown';

export interface ModelResolution {
  state: ModelResolutionState;
  reasons: string[];
  firstDetectedAt: string | null;
  lastAttemptAt: string | null;
  nextAttemptAt: string | null;
}

export interface ResolutionJob {
  providerId: string;
  modelId: string;
  status: 'processing' | 'dormant' | 'complete';
  reasons: string[];
  firstDetectedAt: string;
  windowStartedAt: string;
  lastAttemptAt: string | null;
  nextAttemptAt: string | null;
  attemptCount: number;
}

interface IssueRow {
  provider_id: string;
  model_id: string;
  context_tokens: number | null;
  output_tokens: number | null;
  input_modalities: string | null;
  tools: number | null;
  reasoning: number | null;
  structured: number | null;
  attachment: number | null;
  cost_kind: string | null;
  vq: number | null;
  vo: number | null;
  conflict_count: number;
}

interface JobRow {
  provider_id: string;
  model_id: string;
  status: 'processing' | 'dormant' | 'complete';
  reasons_json: string;
  first_detected_at: string;
  window_started_at: string;
  last_attempt_at: string | null;
  next_attempt_at: string | null;
  attempt_count: number;
}

const addMs = (iso: string, ms: number) => new Date(new Date(iso).getTime() + ms).toISOString();

function issueRow(db: Db, providerId: string, modelId: string): IssueRow | undefined {
  return db.prepare(`
    SELECT m.provider_id, m.model_id, m.context_tokens, m.output_tokens, m.input_modalities,
           m.tools, m.reasoning, m.structured, m.attachment, m.cost_kind,
           (SELECT value FROM model_scores s WHERE s.provider_id=m.provider_id AND s.model_id=m.model_id AND s.kind='VQ') vq,
           (SELECT value FROM model_scores s WHERE s.provider_id=m.provider_id AND s.model_id=m.model_id AND s.kind='VO') vo,
           (SELECT COUNT(*) FROM model_conflicts c WHERE c.provider_id=m.provider_id AND c.model_id=m.model_id AND c.status='open' AND c.conflict_type='official_source_disagreement') conflict_count
      FROM models m
     WHERE m.provider_id=? AND m.model_id=? AND m.status IN ('active','missing')
  `).get(providerId, modelId) as unknown as IssueRow | undefined;
}

function issueRows(db: Db): IssueRow[] {
  return db.prepare(`
    SELECT m.provider_id, m.model_id, m.context_tokens, m.output_tokens, m.input_modalities,
           m.tools, m.reasoning, m.structured, m.attachment, m.cost_kind,
           (SELECT value FROM model_scores s WHERE s.provider_id=m.provider_id AND s.model_id=m.model_id AND s.kind='VQ') vq,
           (SELECT value FROM model_scores s WHERE s.provider_id=m.provider_id AND s.model_id=m.model_id AND s.kind='VO') vo,
           (SELECT COUNT(*) FROM model_conflicts c WHERE c.provider_id=m.provider_id AND c.model_id=m.model_id AND c.status='open' AND c.conflict_type='official_source_disagreement') conflict_count
      FROM models m
     WHERE m.status IN ('active','missing')
     ORDER BY m.provider_id, m.model_id
  `).all() as unknown as IssueRow[];
}

function reasonsOf(row: IssueRow): string[] {
  const reasons: string[] = [];
  if (row.context_tokens === null) reasons.push('missing_context');
  if (row.output_tokens === null) reasons.push('missing_max_output');
  if (row.input_modalities === null) reasons.push('missing_modalities');
  if (row.tools === null) reasons.push('missing_tools');
  if (row.reasoning === null) reasons.push('missing_reasoning');
  if (row.structured === null) reasons.push('missing_structured');
  if (row.attachment === null) reasons.push('missing_attachment');
  if (row.cost_kind === null || row.cost_kind === 'unknown') reasons.push('missing_cost');
  if (row.vq === null) reasons.push('missing_vq');
  if (row.vo === null) reasons.push('missing_vo');
  if (row.conflict_count > 0) reasons.push('conflicting_official_sources');
  return reasons;
}

const hasOperationalReason = (reasons: string[]) =>
  reasons.some((reason) => reason.startsWith('missing_') && reason !== 'missing_vq' && reason !== 'missing_vo') ||
  reasons.includes('conflicting_official_sources');

function publicState(status: JobRow['status'], reasons: string[]): ModelResolutionState {
  if (status === 'processing') return 'processing';
  if (status === 'complete' || reasons.length === 0) return 'complete';
  return hasOperationalReason(reasons) ? 'source_incomplete' : 'awaiting_external_benchmark';
}

function toJob(row: JobRow): ResolutionJob {
  return {
    providerId: row.provider_id,
    modelId: row.model_id,
    status: row.status,
    reasons: JSON.parse(row.reasons_json) as string[],
    firstDetectedAt: row.first_detected_at,
    windowStartedAt: row.window_started_at,
    lastAttemptAt: row.last_attempt_at,
    nextAttemptAt: row.next_attempt_at,
    attemptCount: row.attempt_count,
  };
}

/**
 * Terminalise one offering's job: no reasons, nothing due, nothing to poll.
 *
 * The single definition of "this job is finished". Three paths reach it - a
 * window that closed with nothing outstanding, an attempt that finished on an
 * offering which has since retired, and the retirement itself over in the sync
 * engine - and a second copy of this statement is exactly how those three drift
 * into disagreeing about what a finished job looks like.
 */
export function completeResolutionJob(db: Db, providerId: string, modelId: string, now: string): void {
  db.prepare(`
    UPDATE resolution_jobs SET status='complete', reasons_json='[]', last_attempt_at=?,
      next_attempt_at=NULL, updated_at=? WHERE provider_id=? AND model_id=?
  `).run(now, now, providerId, modelId);
}

/**
 * Upgrade an existing catalog to the durable queue without disturbing jobs
 * that already completed a processing window. Newly discovered gaps are due
 * immediately so the service's startup pass becomes their first attempt.
 */
export function bootstrapResolutionJobs(db: Db, now: string): number {
  // Repair jobs left by an older process or by a retirement that raced a
  // scheduler tick. Non-live offerings are terminal and must never be due.
  db.prepare(`
    UPDATE resolution_jobs SET status='complete', reasons_json='[]', next_attempt_at=NULL, updated_at=?
    WHERE status='processing'
      AND NOT EXISTS (
        SELECT 1 FROM models m
         WHERE m.provider_id=resolution_jobs.provider_id
           AND m.model_id=resolution_jobs.model_id
           AND m.status IN ('active','missing')
      )
  `).run(now);

  const insert = db.prepare(`
    INSERT OR IGNORE INTO resolution_jobs (
      provider_id, model_id, status, reasons_json, first_detected_at, window_started_at,
      last_attempt_at, next_attempt_at, attempt_count, updated_at
    ) VALUES (?,?,'processing',?,?,?,NULL,?,0,?)
  `);
  let inserted = 0;
  for (const row of issueRows(db)) {
    const reasons = reasonsOf(row);
    const existing = db.prepare(`SELECT status FROM resolution_jobs WHERE provider_id=? AND model_id=?`)
      .get(row.provider_id, row.model_id) as unknown as { status: JobRow['status'] } | undefined;
    if (existing) {
      // A deploy can tighten the definition of a reason while an old process
      // is still running. Refresh only the active queue; dormant windows stay
      // dormant until a real source-change trigger reactivates them.
      if (existing.status === 'processing') {
        if (reasons.length === 0) {
          db.prepare(`UPDATE resolution_jobs SET status='complete', reasons_json='[]', next_attempt_at=NULL, updated_at=?
            WHERE provider_id=? AND model_id=?`).run(now, row.provider_id, row.model_id);
        } else {
          db.prepare(`UPDATE resolution_jobs SET reasons_json=?, updated_at=?
            WHERE provider_id=? AND model_id=?`).run(JSON.stringify(reasons), now, row.provider_id, row.model_id);
        }
      }
      continue;
    }
    if (reasons.length === 0) continue;
    inserted += Number(insert.run(
      row.provider_id, row.model_id, JSON.stringify(reasons), now, now, now, now,
    ).changes);
  }
  return inserted;
}

/** Start or reactivate one five-minute resolution window after a full sync. */
export function beginResolutionWindow(db: Db, now: string): number {
  const upsert = db.prepare(`
    INSERT INTO resolution_jobs (
      provider_id, model_id, status, reasons_json, first_detected_at, window_started_at,
      last_attempt_at, next_attempt_at, attempt_count, updated_at
    ) VALUES (?,?,'processing',?,?,?,?,?,1,?)
    ON CONFLICT(provider_id, model_id) DO UPDATE SET
      status='processing', reasons_json=excluded.reasons_json,
      window_started_at=excluded.window_started_at, last_attempt_at=excluded.last_attempt_at,
      next_attempt_at=excluded.next_attempt_at, attempt_count=1, updated_at=excluded.updated_at
  `);
  let active = 0;
  for (const row of issueRows(db)) {
    const reasons = reasonsOf(row);
    if (reasons.length === 0) {
      completeResolutionJob(db, row.provider_id, row.model_id, now);
      continue;
    }
    const existing = db.prepare(`SELECT first_detected_at FROM resolution_jobs WHERE provider_id=? AND model_id=?`)
      .get(row.provider_id, row.model_id) as unknown as { first_detected_at: string } | undefined;
    upsert.run(
      row.provider_id, row.model_id, JSON.stringify(reasons), existing?.first_detected_at ?? now,
      now, now, addMs(now, FIRST_RETRY_MS), now,
    );
    active++;
  }
  return active;
}

/** Record a targeted pass and either schedule the final attempt or go dormant. */
export function finishResolutionAttempt(db: Db, providerId: string, modelId: string, now: string): ModelResolution {
  const row = issueRow(db, providerId, modelId);
  const held = db.prepare(`SELECT * FROM resolution_jobs WHERE provider_id=? AND model_id=?`)
    .get(providerId, modelId) as unknown as JobRow | undefined;
  if (!held) return { state: 'complete', reasons: [], firstDetectedAt: null, lastAttemptAt: now, nextAttemptAt: null };

  // Retirement is a terminal lifecycle decision for the offering. It is not an
  // unresolved resolution attempt, so clear a held job even though issueRow
  // intentionally excludes retired models from the active queue.
  if (!row) {
    completeResolutionJob(db, providerId, modelId, now);
    return {
      state: 'complete', reasons: [], firstDetectedAt: held.first_detected_at,
      lastAttemptAt: now, nextAttemptAt: null,
    };
  }

  const reasons = reasonsOf(row);
  const resolved = reasons.length === 0;
  const expired = new Date(now).getTime() >= new Date(held.window_started_at).getTime() + RESOLUTION_WINDOW_MS;
  const status: JobRow['status'] = resolved ? 'complete' : expired ? 'dormant' : 'processing';
  const nextAttemptAt = status === 'processing' ? addMs(held.window_started_at, RESOLUTION_WINDOW_MS) : null;
  db.prepare(`
    UPDATE resolution_jobs SET status=?, reasons_json=?, last_attempt_at=?, next_attempt_at=?,
      attempt_count=attempt_count+1, updated_at=? WHERE provider_id=? AND model_id=?
  `).run(status, JSON.stringify(reasons), now, nextAttemptAt, now, providerId, modelId);

  if (status === 'complete') {
    db.prepare(`INSERT INTO model_events (provider_id, model_id, kind, reason, at) VALUES (?,?, 'resolution_resolved', ?, ?)`)
      .run(providerId, modelId, 'official sources and scoring are complete', now);
  }
  return {
    state: publicState(status, reasons), reasons,
    firstDetectedAt: held.first_detected_at, lastAttemptAt: now, nextAttemptAt,
  };
}

export function loadResolution(db: Db, providerId: string, modelId: string): ModelResolution | null {
  const row = db.prepare(`SELECT * FROM resolution_jobs WHERE provider_id=? AND model_id=?`)
    .get(providerId, modelId) as unknown as JobRow | undefined;
  if (!row) {
    const current = issueRow(db, providerId, modelId);
    if (!current) return null;
    const reasons = reasonsOf(current);
    return {
      state: reasons.length === 0 ? 'complete' : hasOperationalReason(reasons) ? 'source_incomplete' : 'awaiting_external_benchmark',
      reasons, firstDetectedAt: null, lastAttemptAt: null, nextAttemptAt: null,
    };
  }
  const reasons = JSON.parse(row.reasons_json) as string[];
  return {
    state: publicState(row.status, reasons), reasons,
    firstDetectedAt: row.first_detected_at,
    lastAttemptAt: row.last_attempt_at,
    nextAttemptAt: row.next_attempt_at,
  };
}

export function listDueResolutionJobs(db: Db, now: string): ResolutionJob[] {
  return (db.prepare(`
    SELECT * FROM resolution_jobs
     WHERE status='processing' AND next_attempt_at IS NOT NULL AND next_attempt_at <= ?
     ORDER BY next_attempt_at, provider_id, model_id
  `).all(now) as unknown as JobRow[]).map(toJob);
}
