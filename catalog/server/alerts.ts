import type { Db } from '../db/index.ts';
import { enqueueNotification, notificationForTransition } from './notifications.ts';

export type AlertSeverity = 'critical' | 'warning' | 'info';
export type AlertStatus = 'open' | 'acknowledged' | 'resolved';

export interface AlertCandidate {
  id: string;
  kind: string;
  severity: AlertSeverity;
  title: string;
  detail: string;
  providerId: string | null;
  modelId: string | null;
}

export interface AlertRecord extends AlertCandidate {
  status: AlertStatus;
  firstSeenAt: string;
  lastSeenAt: string;
  acknowledgedAt: string | null;
  resolvedAt: string | null;
  occurrenceCount: number;
}

export interface AlertHealthPayload {
  service: {
    status: 'up' | 'degraded';
    databaseReadable: boolean;
    syncInFlight: boolean;
  };
  catalog: {
    staleProviders: { id: string; freshness: string; lastSuccessfulSyncAt: string | null; lastOutcome: string | null }[];
    providers: { id: string; lastOutcome: string | null }[];
  };
  lastSync: { providers: { provider: string; outcome: string; error: string | null }[] } | null;
}

function providerLabel(id: string, providers: AlertHealthPayload['catalog']['providers']): string {
  return providers.find((provider) => provider.id === id)?.id ?? id;
}

export function candidatesFromHealth(health: AlertHealthPayload): AlertCandidate[] {
  const candidates: AlertCandidate[] = [];

  if (health.service.status === 'degraded') {
    candidates.push({
      id: 'service-degraded',
      kind: 'service_degraded',
      severity: 'critical',
      title: 'Catalog service is degraded',
      detail: health.service.databaseReadable
        ? 'The service is responding, but the catalog is not currently safe to report as healthy.'
        : 'The service is responding but cannot read its catalog database.',
      providerId: null,
      modelId: null,
    });
  }

  for (const provider of health.catalog.staleProviders) {
    candidates.push({
      id: `stale-provider:${provider.id}`,
      kind: 'stale_provider',
      severity: 'warning',
      title: `${providerLabel(provider.id, health.catalog.providers)} needs a fresh sync`,
      detail: provider.lastOutcome && provider.lastOutcome !== 'ok'
        ? `The latest provider sync ended with ${provider.lastOutcome} and the data remains outside the freshness policy.`
        : 'The provider data is older than the Catalog freshness policy.',
      providerId: provider.id,
      modelId: null,
    });
  }

  for (const provider of health.lastSync?.providers ?? []) {
    if (provider.outcome === 'ok') continue;
    candidates.push({
      id: `sync-failure:${provider.provider}`,
      kind: 'sync_failure',
      severity: 'warning',
      title: `${provider.provider} sync reported ${provider.outcome}`,
      detail: provider.error ?? 'The latest provider refresh did not complete successfully.',
      providerId: provider.provider,
      modelId: null,
    });
  }

  if (health.service.syncInFlight) {
    candidates.push({
      id: 'sync-in-flight',
      kind: 'sync_in_flight',
      severity: 'info',
      title: 'Catalog sync is in progress',
      detail: 'Provider data is being refreshed. Freshness will be re-evaluated when the run completes.',
      providerId: null,
      modelId: null,
    });
  }

  return candidates;
}

function fromRow(row: Record<string, unknown>): AlertRecord {
  return {
    id: String(row.id),
    kind: String(row.kind),
    severity: row.severity as AlertSeverity,
    title: String(row.title),
    detail: String(row.detail),
    providerId: row.provider_id === null ? null : String(row.provider_id),
    modelId: row.model_id === null ? null : String(row.model_id),
    status: row.status as AlertStatus,
    firstSeenAt: String(row.first_seen_at),
    lastSeenAt: String(row.last_seen_at),
    acknowledgedAt: row.acknowledged_at === null ? null : String(row.acknowledged_at),
    resolvedAt: row.resolved_at === null ? null : String(row.resolved_at),
    occurrenceCount: Number(row.occurrence_count),
  };
}

export function reconcileAlerts(db: Db, health: AlertHealthPayload, now = new Date().toISOString()): AlertRecord[] {
  const candidates = candidatesFromHealth(health);
  const candidateIds = new Set(candidates.map((candidate) => candidate.id));

  for (const candidate of candidates) {
    const existing = db.prepare('SELECT * FROM operational_alerts WHERE id = ?').get(candidate.id) as Record<string, unknown> | undefined;
    if (!existing) {
      db.prepare(`INSERT INTO operational_alerts
        (id, kind, severity, title, detail, provider_id, model_id, status, first_seen_at, last_seen_at, occurrence_count)
        VALUES (?, ?, ?, ?, ?, ?, ?, 'open', ?, ?, 1)`).run(
        candidate.id, candidate.kind, candidate.severity, candidate.title, candidate.detail,
        candidate.providerId, candidate.modelId, now, now,
      );
      enqueueNotification(db, fromRow(db.prepare('SELECT * FROM operational_alerts WHERE id = ?').get(candidate.id) as Record<string, unknown>), 'opened', now);
      continue;
    }

    const currentStatus = existing.status as AlertStatus;
    const nextStatus: AlertStatus = currentStatus === 'resolved' ? 'open' : currentStatus;
    db.prepare(`UPDATE operational_alerts
      SET severity = ?, title = ?, detail = ?, last_seen_at = ?, status = ?, resolved_at = CASE WHEN ? = 'open' THEN NULL ELSE resolved_at END,
          occurrence_count = occurrence_count + 1
      WHERE id = ?`).run(
      candidate.severity, candidate.title, candidate.detail, now, nextStatus, nextStatus, candidate.id,
    );
    if (currentStatus === 'resolved' && nextStatus === 'open') {
      enqueueNotification(db, fromRow(db.prepare('SELECT * FROM operational_alerts WHERE id = ?').get(candidate.id) as Record<string, unknown>), 'reopened', now);
    }
  }

  const activeRows = db.prepare(`SELECT id, status FROM operational_alerts WHERE status IN ('open', 'acknowledged')`).all() as unknown as { id: string; status: AlertStatus }[];
  for (const row of activeRows) {
    if (candidateIds.has(row.id)) continue;
    db.prepare(`UPDATE operational_alerts SET status = 'resolved', resolved_at = ? WHERE id = ?`).run(now, row.id);
    enqueueNotification(db, fromRow(db.prepare('SELECT * FROM operational_alerts WHERE id = ?').get(row.id) as Record<string, unknown>), 'resolved', now);
  }

  return listAlerts(db);
}

export function listAlerts(db: Db, status?: AlertStatus): AlertRecord[] {
  const rows = status
    ? db.prepare('SELECT * FROM operational_alerts WHERE status = ? ORDER BY CASE severity WHEN \'critical\' THEN 0 WHEN \'warning\' THEN 1 ELSE 2 END, last_seen_at DESC').all(status)
    : db.prepare('SELECT * FROM operational_alerts ORDER BY CASE status WHEN \'open\' THEN 0 WHEN \'acknowledged\' THEN 1 ELSE 2 END, CASE severity WHEN \'critical\' THEN 0 WHEN \'warning\' THEN 1 ELSE 2 END, last_seen_at DESC').all();
  return (rows as unknown as Record<string, unknown>[]).map(fromRow);
}

export function transitionAlert(db: Db, id: string, status: AlertStatus, now = new Date().toISOString()): AlertRecord | null {
  if (!['open', 'acknowledged', 'resolved'].includes(status)) return null;
  const existing = db.prepare('SELECT * FROM operational_alerts WHERE id = ?').get(id) as Record<string, unknown> | undefined;
  if (!existing) return null;
  const previous = existing.status as AlertStatus;

  db.prepare(`UPDATE operational_alerts
    SET status = ?, acknowledged_at = CASE WHEN ? = 'acknowledged' THEN COALESCE(acknowledged_at, ?) ELSE acknowledged_at END,
        resolved_at = CASE WHEN ? = 'resolved' THEN COALESCE(resolved_at, ?) ELSE NULL END
    WHERE id = ?`).run(status, status, now, status, now, id);
  const updated = fromRow(db.prepare('SELECT * FROM operational_alerts WHERE id = ?').get(id) as Record<string, unknown>);
  const eventType = notificationForTransition(previous, status);
  if (eventType) enqueueNotification(db, updated, eventType, now);
  return updated;
}

export function alertSummary(alerts: AlertRecord[]) {
  return alerts.reduce((summary, alert) => {
    summary.total += 1;
    summary[alert.status] += 1;
    if (alert.status !== 'resolved') summary.active += 1;
    if (alert.status !== 'resolved') summary[alert.severity] += 1;
    return summary;
  }, { total: 0, active: 0, open: 0, acknowledged: 0, resolved: 0, critical: 0, warning: 0, info: 0 });
}
