import { createHmac, randomUUID } from 'node:crypto';
import type { Db } from '../db/index.ts';
import type { AlertRecord, AlertStatus } from './alerts.ts';

export type NotificationEvent = 'opened' | 'reopened' | 'acknowledged' | 'resolved';
export type NotificationStatus = 'pending' | 'delivered' | 'retrying' | 'failed';

export interface NotificationConfig {
  webhookUrl: string | null;
  webhookSecret: string | null;
  enabled: boolean;
  timeoutMs: number;
  maxAttempts: number;
  baseDelayMs: number;
}

export interface NotificationRecord {
  id: number;
  alertId: string;
  eventType: NotificationEvent;
  status: NotificationStatus;
  attempts: number;
  nextAttemptAt: string;
  lastAttemptAt: string | null;
  deliveredAt: string | null;
  responseStatus: number | null;
  lastError: string | null;
  createdAt: string;
}

export function notificationConfig(env: NodeJS.ProcessEnv = process.env): NotificationConfig {
  const rawTimeout = Number(env.CATALOG_WEBHOOK_TIMEOUT_MS ?? 5000);
  const rawMaxAttempts = Number(env.CATALOG_WEBHOOK_MAX_ATTEMPTS ?? 5);
  const rawDelay = Number(env.CATALOG_WEBHOOK_BASE_DELAY_MS ?? 1000);
  return {
    webhookUrl: env.CATALOG_ALERT_WEBHOOK_URL?.trim() || null,
    webhookSecret: env.CATALOG_ALERT_WEBHOOK_SECRET?.trim() || null,
    enabled: env.CATALOG_ALERT_NOTIFICATIONS === 'true' && Boolean(env.CATALOG_ALERT_WEBHOOK_URL?.trim()),
    timeoutMs: Number.isFinite(rawTimeout) ? Math.min(Math.max(rawTimeout, 500), 30_000) : 5000,
    maxAttempts: Number.isFinite(rawMaxAttempts) ? Math.min(Math.max(Math.floor(rawMaxAttempts), 1), 10) : 5,
    baseDelayMs: Number.isFinite(rawDelay) ? Math.min(Math.max(rawDelay, 100), 300_000) : 1000,
  };
}

function payloadFor(alert: AlertRecord, eventType: NotificationEvent, now: string) {
  return {
    id: randomUUID(),
    type: `catalog.alert.${eventType}`,
    occurredAt: now,
    source: 'venom-catalog',
    alert: {
      id: alert.id,
      kind: alert.kind,
      severity: alert.severity,
      status: alert.status,
      title: alert.title,
      detail: alert.detail,
      providerId: alert.providerId,
      modelId: alert.modelId,
      occurrenceCount: alert.occurrenceCount,
      firstSeenAt: alert.firstSeenAt,
      lastSeenAt: alert.lastSeenAt,
    },
  };
}

export function enqueueNotification(db: Db, alert: AlertRecord, eventType: NotificationEvent, now = new Date().toISOString()): void {
  const payload = payloadFor(alert, eventType, now);
  db.prepare(`INSERT INTO alert_notifications
    (alert_id, event_type, payload_json, status, attempts, next_attempt_at, created_at)
    VALUES (?, ?, ?, 'pending', 0, ?, ?)`)
    .run(alert.id, eventType, JSON.stringify(payload), now, now);
}

export function listNotifications(db: Db, alertId?: string): NotificationRecord[] {
  const rows = alertId
    ? db.prepare('SELECT * FROM alert_notifications WHERE alert_id = ? ORDER BY created_at DESC').all(alertId)
    : db.prepare('SELECT * FROM alert_notifications ORDER BY created_at DESC').all();
  return (rows as unknown as Record<string, unknown>[]).map((row) => ({
    id: Number(row.id),
    alertId: String(row.alert_id),
    eventType: row.event_type as NotificationEvent,
    status: row.status as NotificationStatus,
    attempts: Number(row.attempts),
    nextAttemptAt: String(row.next_attempt_at),
    lastAttemptAt: row.last_attempt_at === null ? null : String(row.last_attempt_at),
    deliveredAt: row.delivered_at === null ? null : String(row.delivered_at),
    responseStatus: row.response_status === null ? null : Number(row.response_status),
    lastError: row.last_error === null ? null : String(row.last_error),
    createdAt: String(row.created_at),
  }));
}

export interface NotificationDeliverySummary {
  pending: number;
  failed: number;
}

/**
 * Delivery counts, counted in SQL.
 *
 * `/v1/alerts` used to answer this by loading the whole queue with
 * `listNotifications(db)` and filtering in JavaScript — a full table scan on a
 * route two pollers hit every 30 seconds, over an append-only table that only
 * grows. Two aggregates replace it. `listNotifications` remains for the
 * single-alert read, where the row count is bounded by one alert's history.
 */
export function notificationDeliverySummary(db: Db): NotificationDeliverySummary {
  const row = db.prepare(`SELECT
      COUNT(CASE WHEN status IN ('pending','retrying') THEN 1 END) pending,
      COUNT(CASE WHEN status = 'failed' THEN 1 END) failed
    FROM alert_notifications`).get() as unknown as { pending: number; failed: number };
  return { pending: Number(row.pending), failed: Number(row.failed) };
}

export function notificationHeaders(payload: string, secret: string | null): Record<string, string> {
  const headers: Record<string, string> = {
    'content-type': 'application/json',
    'user-agent': 'Venom-Catalog-Webhook/1.0',
    'x-catalog-event': 'alert',
  };
  if (secret) headers['x-catalog-signature'] = `sha256=${createHmac('sha256', secret).update(payload).digest('hex')}`;
  return headers;
}

export async function deliverDueNotifications(
  db: Db,
  config: NotificationConfig = notificationConfig(),
  now = new Date(),
  fetcher: typeof fetch = fetch,
): Promise<number> {
  if (!config.enabled || !config.webhookUrl) return 0;
  const nowIso = now.toISOString();
  const rows = db.prepare(`SELECT * FROM alert_notifications
    WHERE status IN ('pending', 'retrying') AND next_attempt_at <= ?
    ORDER BY created_at ASC LIMIT 20`).all(nowIso) as unknown as Record<string, unknown>[];
  let delivered = 0;

  for (const row of rows) {
    const attempts = Number(row.attempts) + 1;
    const payload = String(row.payload_json);
    let responseStatus: number | null = null;
    let error: string | null = null;
    try {
      const response = await fetcher(config.webhookUrl, {
        method: 'POST',
        headers: notificationHeaders(payload, config.webhookSecret),
        body: payload,
        signal: AbortSignal.timeout(config.timeoutMs),
      });
      responseStatus = response.status;
      if (!response.ok) error = `webhook returned HTTP ${response.status}`;
    } catch (cause) {
      error = cause instanceof Error ? cause.message : String(cause);
    }

    if (!error) {
      db.prepare(`UPDATE alert_notifications
        SET status = 'delivered', attempts = ?, last_attempt_at = ?, delivered_at = ?, response_status = ?, last_error = NULL
        WHERE id = ?`).run(attempts, nowIso, nowIso, responseStatus, Number(row.id));
      delivered += 1;
      continue;
    }

    const terminal = attempts >= config.maxAttempts;
    const next = new Date(now.getTime() + config.baseDelayMs * (2 ** Math.min(attempts - 1, 8))).toISOString();
    db.prepare(`UPDATE alert_notifications
      SET status = ?, attempts = ?, last_attempt_at = ?, next_attempt_at = ?, response_status = ?, last_error = ?
      WHERE id = ?`).run(terminal ? 'failed' : 'retrying', attempts, nowIso, next, responseStatus, error.slice(0, 500), Number(row.id));
  }

  return delivered;
}

export function notificationForTransition(previous: AlertStatus, next: AlertStatus): NotificationEvent | null {
  if (next === 'resolved' && previous !== 'resolved') return 'resolved';
  if (next === 'acknowledged' && previous !== 'acknowledged') return 'acknowledged';
  if (next === 'open' && previous === 'resolved') return 'reopened';
  return null;
}
