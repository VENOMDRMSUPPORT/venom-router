import type { Db } from '../db/index.ts';
import { loadChanges, type Change, type ChangeClass } from './changes.ts';
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

/**
 * How long a roster change stays on the alert list.
 *
 * A roster change is an event, but this ledger is level-triggered: a candidate
 * that stops being produced is resolved. Bounding the event by a window is what
 * reconciles the two — the change is a standing "you have not looked at this
 * yet" condition while it is recent, and ages out on its own afterwards. The
 * alternative was a second lifecycle mechanism beside `reconcileAlerts`, and a
 * second copy of a core mechanism is a defect here rather than a variation.
 *
 * Seven days, matching the dashboard's default change window, so the bell and
 * the change feed cannot disagree about what counts as recent.
 */
export const ROSTER_ALERT_WINDOW_MS = 7 * 24 * 60 * 60 * 1000;

/**
 * Which recorded changes are worth interrupting the owner for, and how loudly.
 *
 * Availability only. A price or context move is real news and belongs on the
 * change feed, but it is not something the bell should compete for attention
 * with — an alert list that reports everything reports nothing. A class absent
 * from this table is deliberately not an alert.
 *
 * `warning` means something a consumer depended on is gone or unusable;
 * `info` means new capacity arrived and nothing broke.
 */
const ROSTER_ALERT_SEVERITY: Partial<Record<ChangeClass, AlertSeverity>> = {
  added: 'info',
  readded: 'info',
  retired: 'warning',
  became_missing: 'warning',
  excluded: 'warning',
  quality_lost: 'warning',
};

/** The models a grouped alert names before it stops listing them. */
const ROSTER_ALERT_NAME_LIMIT = 3;

function nameList(modelIds: string[]): string {
  if (modelIds.length <= ROSTER_ALERT_NAME_LIMIT) return modelIds.join(', ');
  const shown = modelIds.slice(0, ROSTER_ALERT_NAME_LIMIT).join(', ');
  return `${shown} and ${modelIds.length - ROSTER_ALERT_NAME_LIMIT} more`;
}

const plural = (n: number) => (n === 1 ? 'model' : 'models');

function rosterAlertText(cls: ChangeClass, providerId: string, modelIds: string[], note: string | null): { title: string; detail: string } {
  const named = nameList(modelIds);
  const count = modelIds.length;
  switch (cls) {
    case 'added':
      return {
        title: count === 1 ? `${providerId} added ${named}` : `${providerId} added ${count} ${plural(count)}`,
        detail: `New in the ${providerId} roster: ${named}. Quality is measured per identity, so an offer of an already-measured model carries its score immediately.`,
      };
    case 'readded':
      return {
        title: count === 1 ? `${providerId} restored ${named}` : `${providerId} restored ${count} ${plural(count)}`,
        detail: `Back in the ${providerId} roster after having been withdrawn: ${named}.`,
      };
    case 'retired':
      return {
        title: count === 1 ? `${providerId} removed ${named}` : `${providerId} removed ${count} ${plural(count)}`,
        detail: `No longer offered by ${providerId}: ${named}. Anything routing to ${count === 1 ? 'it' : 'them'} needs a new destination.`,
      };
    case 'became_missing':
      return {
        title: count === 1 ? `${named} stopped appearing on ${providerId}` : `${count} ${plural(count)} stopped appearing on ${providerId}`,
        detail: `Absent from the ${providerId} roster but not yet retired: ${named}. Retained as missing until the provider confirms either way.`,
      };
    case 'excluded':
      return {
        title: count === 1
          ? `${providerId} withheld ${named}`
          : `${providerId} withheld ${count} ${plural(count)}`,
        detail: note
          ? `Publish policy withheld ${named} from ${providerId}: ${note}.`
          : `Publish policy withheld ${named} from ${providerId}.`,
      };
    case 'quality_lost':
      return {
        title: count === 1 ? `${named} lost its quality score` : `${count} ${plural(count)} lost their quality score`,
        detail: `The published quality value was withdrawn for ${named} on ${providerId}. They are unrated until measured again.`,
      };
    default:
      return {
        title: `${providerId} reported a change to ${count} ${plural(count)}`,
        detail: note ?? `A recorded catalog change affecting ${named}.`,
      };
  }
}

/**
 * Roster-change candidates from the recorded events, grouped.
 *
 * Grouped by class, provider, observation time and reason, because that is the
 * shape the decision actually had. One publish-policy sweep withheld eleven
 * ollama-cloud models at a single timestamp for one reason — as eleven alerts
 * that is a bell nobody can read, and it misreports one decision as eleven.
 *
 * The classification is NOT repeated here: it comes from `loadChanges`, the one
 * function that turns `model_events` rows into reader-facing classes. Two copies
 * of that rule would let the bell and the change feed describe one event
 * differently.
 */
export function candidatesFromChanges(changes: Change[]): AlertCandidate[] {
  const groups = new Map<string, { cls: ChangeClass; providerId: string; note: string | null; observedAt: string; modelIds: string[] }>();
  for (const change of changes) {
    if (!ROSTER_ALERT_SEVERITY[change.class]) continue;
    // `note` is part of the key, not just the text: two models withheld at the
    // same instant for different reasons are two decisions, not one.
    const key = JSON.stringify([change.class, change.providerId, change.observedAt, change.note]);
    const group = groups.get(key);
    if (group) {
      if (!group.modelIds.includes(change.modelId)) group.modelIds.push(change.modelId);
      continue;
    }
    groups.set(key, {
      cls: change.class,
      providerId: change.providerId,
      note: change.note,
      observedAt: change.observedAt,
      modelIds: [change.modelId],
    });
  }

  const candidates: AlertCandidate[] = [];
  for (const group of groups.values()) {
    const modelIds = [...group.modelIds].sort();
    const { title, detail } = rosterAlertText(group.cls, group.providerId, modelIds, group.note);
    candidates.push({
      // The observation time is in the id, so a model added, removed and added
      // again produces three alerts rather than one row overwritten twice.
      id: `model-change:${group.cls}:${group.providerId}:${group.observedAt}:${group.note ?? ''}`,
      kind: `model_${group.cls}`,
      severity: ROSTER_ALERT_SEVERITY[group.cls]!,
      title,
      detail,
      providerId: group.providerId,
      // Named only when the alert is about exactly one model. A grouped alert
      // that claimed one of its members would send the reader to the wrong row.
      modelId: modelIds.length === 1 ? modelIds[0] : null,
    });
  }
  return candidates;
}

/**
 * Recent roster changes, read through the same classifier the change feed uses.
 *
 * The model link is verified against `models` before it is kept. `operational_alerts`
 * carries a composite foreign key to that table and SQLite enforces it, so naming
 * a pair with no row raises `FOREIGN KEY constraint failed` inside the insert —
 * which does not fail one alert, it aborts the whole reconcile and takes
 * `GET /v1/alerts` down with it. The bell would go dark because of one dangling
 * event. Dropping to a provider-level link is a slightly worse deep link; losing
 * the ledger is losing the feature.
 */
export function rosterCandidates(db: Db, now: string, windowMs = ROSTER_ALERT_WINDOW_MS): AlertCandidate[] {
  const since = new Date(new Date(now).getTime() - windowMs).toISOString();
  const candidates = candidatesFromChanges(loadChanges(db, { since }).changes);
  const linkable = candidates.filter((candidate) => candidate.modelId !== null);
  if (linkable.length === 0) return candidates;

  const known = new Set(
    (db.prepare(`SELECT provider_id, model_id FROM models`).all() as unknown as { provider_id: string; model_id: string }[])
      .map((row) => `${row.provider_id}/${row.model_id}`),
  );
  return candidates.map((candidate) =>
    candidate.modelId !== null && !known.has(`${candidate.providerId}/${candidate.modelId}`)
      ? { ...candidate, modelId: null }
      : candidate,
  );
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

/**
 * Bring the ledger in line with what is true now.
 *
 * Roster candidates are read here rather than passed in, so a caller cannot
 * reconcile health and silently forget the roster — the parameter exists only so
 * a test can bound the window, and it defaults to the real thing.
 */
export function reconcileAlerts(
  db: Db,
  health: AlertHealthPayload,
  now = new Date().toISOString(),
  roster: AlertCandidate[] = rosterCandidates(db, now),
): AlertRecord[] {
  const candidates = [...candidatesFromHealth(health), ...roster];
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
    const reopened = currentStatus === 'resolved' && nextStatus === 'open';
    // The count moves only on a reopen, because that is the only reconcile in
    // which the condition actually arose again. Incrementing on every pass made
    // it a count of browser polls: every alert in the store carries exactly 2,
    // from a dashboard whose bell and alert panel both fetch on page load 69ms
    // apart. `last_seen_at` is the field that means "still true as of", and it
    // still advances every pass.
    db.prepare(`UPDATE operational_alerts
      SET severity = ?, title = ?, detail = ?, last_seen_at = ?, status = ?, resolved_at = CASE WHEN ? = 'open' THEN NULL ELSE resolved_at END,
          occurrence_count = occurrence_count + ?
      WHERE id = ?`).run(
      candidate.severity, candidate.title, candidate.detail, now, nextStatus, nextStatus, reopened ? 1 : 0, candidate.id,
    );
    if (reopened) {
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
