import type { Db } from '../db/index.ts';
import { classifyChangeEvent, type EventRow } from './changes.ts';

export type NotificationCategory = 'success' | 'error' | 'warning';
export type CatalogNotificationKind = 'model_added' | 'model_retired' | 'fetch_problem';

export interface CatalogNotification {
  id: string;
  category: NotificationCategory;
  kind: CatalogNotificationKind;
  title: string;
  detail: string;
  providerId: string | null;
  modelId: string | null;
  observedAt: string;
  readAt: string | null;
  createdAt: string;
}

interface StoredNotificationRow {
  id: string;
  category: NotificationCategory;
  kind: CatalogNotificationKind;
  title: string;
  detail: string;
  provider_id: string | null;
  model_id: string | null;
  observed_at: string;
  read_at: string | null;
  created_at: string;
}

interface ModelEventRow extends EventRow {
  id: number;
}

interface SyncFailureRow {
  id: number;
  provider_id: string;
  outcome: string;
  finished_at: string | null;
  started_at: string;
}

const NOTIFICATION_BATCH_SIZE = 250;

function fromRow(row: StoredNotificationRow): CatalogNotification {
  return {
    id: row.id,
    category: row.category,
    kind: row.kind,
    title: row.title,
    detail: row.detail,
    providerId: row.provider_id,
    modelId: row.model_id,
    observedAt: row.observed_at,
    readAt: row.read_at,
    createdAt: row.created_at,
  };
}

function eventNotification(event: ModelEventRow): Omit<CatalogNotification, 'readAt' | 'createdAt'> | null {
  const classification = classifyChangeEvent(event);
  if (classification === 'added' || classification === 'readded') {
    return {
      id: `model-event:${event.id}`,
      category: 'success',
      kind: 'model_added',
      title: `${event.provider_id} added model ${event.model_id}`,
      detail: classification === 'readded'
        ? 'The provider restored this model to its published roster.'
        : 'The provider added this model to its published roster.',
      providerId: event.provider_id,
      modelId: event.model_id,
      observedAt: event.at,
    };
  }
  if (classification === 'retired') {
    return {
      id: `model-event:${event.id}`,
      category: 'error',
      kind: 'model_retired',
      title: `${event.provider_id} removed model ${event.model_id}`,
      detail: 'The provider no longer offers this model. Its catalog history and evidence remain preserved.',
      providerId: event.provider_id,
      modelId: event.model_id,
      observedAt: event.at,
    };
  }
  return null;
}

function insertNotification(db: Db, notification: Omit<CatalogNotification, 'readAt' | 'createdAt'>, now: string): void {
  db.prepare(`INSERT OR IGNORE INTO catalog_notifications
    (id, source_kind, source_id, category, kind, title, detail, provider_id, model_id, observed_at, created_at)
    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
    .run(
      notification.id,
      notification.id.startsWith('model-event:') ? 'model_event' : 'sync_run',
      Number(notification.id.slice(notification.id.indexOf(':') + 1)),
      notification.category,
      notification.kind,
      notification.title,
      notification.detail,
      notification.providerId,
      notification.modelId,
      notification.observedAt,
      now,
    );
}

/**
 * Project immutable recorded events into user notifications.
 *
 * Every source row has a unique key, and insertion uses SQL-level idempotency.
 * The loop is deterministic and unbounded by the public change-feed limit, so a
 * catalog with more than 500 relevant events cannot silently lose notifications.
 */
export function reconcileCatalogNotifications(db: Db, now = new Date().toISOString()): void {
  let eventAfter = 0;
  while (true) {
    const events = db.prepare(`SELECT id, provider_id, model_id, kind, field, old_value, new_value, reason, at
      FROM model_events WHERE id > ? ORDER BY id ASC LIMIT ?`).all(eventAfter, NOTIFICATION_BATCH_SIZE) as unknown as ModelEventRow[];
    if (events.length === 0) break;
    for (const event of events) {
      const notification = eventNotification(event);
      if (notification) insertNotification(db, notification, now);
    }
    eventAfter = events[events.length - 1].id;
  }

  let runAfter = 0;
  while (true) {
    const failedRuns = db.prepare(`SELECT id, provider_id, outcome, finished_at, started_at
      FROM sync_runs WHERE id > ? AND outcome IN ('failed', 'quarantined') ORDER BY id ASC LIMIT ?`)
      .all(runAfter, NOTIFICATION_BATCH_SIZE) as unknown as SyncFailureRow[];
    if (failedRuns.length === 0) break;
    for (const run of failedRuns) {
      insertNotification(db, {
        id: `sync-run:${run.id}`,
        category: 'warning',
        kind: 'fetch_problem',
        title: `${run.provider_id} data refresh needs attention`,
        detail: 'The catalog could not refresh this provider’s model data. Review the service log for the recorded failure details.',
        providerId: run.provider_id,
        modelId: null,
        observedAt: run.finished_at ?? run.started_at,
      }, now);
    }
    runAfter = failedRuns[failedRuns.length - 1].id;
  }
}

export function listCatalogNotifications(db: Db, options: { providerId?: string; limit?: number } = {}): CatalogNotification[] {
  const limit = Math.max(1, Math.min(Math.trunc(options.limit ?? 100), 500));
  const rows = options.providerId
    ? db.prepare(`SELECT * FROM catalog_notifications WHERE provider_id = ? ORDER BY observed_at DESC, id DESC LIMIT ?`).all(options.providerId, limit)
    : db.prepare(`SELECT * FROM catalog_notifications ORDER BY observed_at DESC, id DESC LIMIT ?`).all(limit);
  return (rows as unknown as StoredNotificationRow[]).map(fromRow);
}

export function markCatalogNotificationsRead(db: Db, ids: string[] | null, now = new Date().toISOString()): number {
  if (ids === null) {
    const result = db.prepare('UPDATE catalog_notifications SET read_at = COALESCE(read_at, ?) WHERE read_at IS NULL').run(now);
    return Number(result.changes);
  }
  const uniqueIds = [...new Set(ids)].slice(0, 100);
  if (uniqueIds.length === 0) return 0;
  const placeholders = uniqueIds.map(() => '?').join(',');
  const result = db.prepare(`UPDATE catalog_notifications SET read_at = COALESCE(read_at, ?) WHERE id IN (${placeholders})`).run(now, ...uniqueIds);
  return Number(result.changes);
}

export function catalogNotificationSummary(rows: CatalogNotification[]) {
  const unread = rows.filter((row) => row.readAt === null).length;
  return { total: rows.length, unread, read: rows.length - unread };
}
