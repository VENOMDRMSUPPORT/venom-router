import test from 'node:test';
import assert from 'node:assert/strict';
import { openDb } from '../db/index.ts';
import {
  ROSTER_ALERT_WINDOW_MS,
  alertSummary,
  candidatesFromChanges,
  candidatesFromHealth,
  reconcileAlerts,
  rosterCandidates,
  transitionAlert,
  type AlertHealthPayload,
} from './alerts.ts';
import type { Change, ChangeClass } from './changes.ts';

type AlertHealthOverrides = {
  service?: Partial<AlertHealthPayload['service']>;
  catalog?: Partial<AlertHealthPayload['catalog']>;
  lastSync?: AlertHealthPayload['lastSync'];
};

function health(overrides: AlertHealthOverrides = {}): AlertHealthPayload {
  return {
    service: { status: 'up', databaseReadable: true, syncInFlight: false, ...(overrides.service ?? {}) },
    catalog: { staleProviders: [], providers: [], ...(overrides.catalog ?? {}) },
    lastSync: overrides.lastSync ?? null,
  };
}

test('alert candidates preserve the server-owned severity and affected provider', () => {
  const candidates = candidatesFromHealth(health({
    catalog: { staleProviders: [{ id: 'acme', freshness: 'stale', lastSuccessfulSyncAt: null, lastOutcome: 'failed' }], providers: [{ id: 'acme', lastOutcome: 'failed' }] },
    lastSync: { providers: [{ provider: 'acme', outcome: 'failed', error: 'timeout' }] },
  }));

  assert.deepEqual(candidates.map((candidate) => candidate.id), ['stale-provider:acme', 'sync-failure:acme']);
  assert.equal(candidates[0].severity, 'warning');
  assert.equal(candidates[0].providerId, 'acme');
  assert.equal(candidates[1].detail, 'timeout');
});

test('alert lifecycle acknowledges, resolves, and reopens only when the issue returns', () => {
  const db = openDb(':memory:');
  try {
    const degraded = health({ service: { status: 'degraded', databaseReadable: false } });
    const first = reconcileAlerts(db, degraded, '2026-08-23T10:00:00.000Z');
    assert.equal(first[0].status, 'open');
    assert.equal(first[0].occurrenceCount, 1);

    const acknowledged = transitionAlert(db, 'service-degraded', 'acknowledged', '2026-08-23T10:01:00.000Z');
    assert.equal(acknowledged?.status, 'acknowledged');
    assert.equal(acknowledged?.acknowledgedAt, '2026-08-23T10:01:00.000Z');

    const resolved = reconcileAlerts(db, health(), '2026-08-23T10:02:00.000Z');
    assert.equal(resolved[0].status, 'resolved');
    assert.equal(resolved[0].resolvedAt, '2026-08-23T10:02:00.000Z');

    const returned = reconcileAlerts(db, degraded, '2026-08-23T10:03:00.000Z');
    assert.equal(returned[0].status, 'open');
    assert.equal(returned[0].resolvedAt, null);
    assert.equal(returned[0].occurrenceCount, 2);
  } finally {
    db.close();
  }
});

test('summary separates active severity counts from resolved history', () => {
  const db = openDb(':memory:');
  try {
    const first = reconcileAlerts(db, health({ service: { status: 'degraded' } }), '2026-08-23T10:00:00.000Z');
    assert.equal(alertSummary(first).critical, 1);
    const clear = reconcileAlerts(db, health(), '2026-08-23T10:01:00.000Z');
    const summary = alertSummary(clear);
    assert.equal(summary.total, 1);
    assert.equal(summary.active, 0);
    assert.equal(summary.resolved, 1);
    assert.equal(summary.critical, 0);
  } finally {
    db.close();
  }
});

function change(overrides: Partial<Change> & { class: ChangeClass }): Change {
  return {
    providerId: 'acme',
    modelId: 'model-a',
    field: null,
    from: null,
    to: null,
    note: null,
    observedAt: '2026-08-23T09:00:00.000Z',
    ...overrides,
  };
}

function recordEvent(db: ReturnType<typeof openDb>, kind: string, modelId: string, at: string, reason: string | null = null) {
  db.prepare(`INSERT INTO model_events (provider_id, model_id, kind, field, old_value, new_value, reason, at)
    VALUES ('acme', ?, ?, NULL, NULL, NULL, ?, ?)`).run(modelId, kind, reason, at);
}

/**
 * The row the alert's model link points at.
 *
 * Catalog never physically deletes a model, so in production a retired or
 * excluded model still has one — which is why the link is worth keeping at all.
 */
function seedModel(db: ReturnType<typeof openDb>, modelId: string, status = 'active') {
  db.prepare(`INSERT OR IGNORE INTO providers (id,name,roster_url) VALUES ('acme','Acme','https://acme.test')`).run();
  db.prepare(`INSERT INTO models (provider_id,model_id,status,first_seen_at,last_seen_at)
    VALUES ('acme',?,?,'2026-08-01','2026-08-23')`).run(modelId, status);
}

test('a roster change becomes an alert, and its severity says whether anything broke', () => {
  const candidates = candidatesFromChanges([
    change({ class: 'added', modelId: 'new-one' }),
    change({ class: 'retired', modelId: 'gone-one', observedAt: '2026-08-23T09:00:01.000Z' }),
  ]);

  assert.equal(candidates.length, 2);
  const added = candidates.find((candidate) => candidate.kind === 'model_added')!;
  const retired = candidates.find((candidate) => candidate.kind === 'model_retired')!;
  // New capacity is news; lost capacity is a problem. The bell must be able to
  // tell them apart without the reader opening anything.
  assert.equal(added.severity, 'info');
  assert.equal(retired.severity, 'warning');
  assert.equal(added.providerId, 'acme');
  assert.equal(added.modelId, 'new-one');
});

test('one batched decision is one alert, not one per model', () => {
  // The regression this exists for: a single publish-policy sweep withheld
  // eleven ollama-cloud models at one timestamp for one reason. Eleven rows
  // would both flood the bell and misreport one decision as eleven.
  const withheld = ['a', 'b', 'c', 'd'].map((modelId) => change({
    class: 'excluded', modelId, note: 'plan_required',
  }));
  const candidates = candidatesFromChanges(withheld);

  assert.equal(candidates.length, 1);
  assert.equal(candidates[0].severity, 'warning');
  assert.match(candidates[0].title, /4 models/);
  assert.match(candidates[0].detail, /plan_required/);
  // Naming one member of a group would send the reader to the wrong row.
  assert.equal(candidates[0].modelId, null);
  // The names are still recoverable, up to the point where a list stops helping.
  assert.match(candidates[0].detail, /a, b, c and 1 more/);
});

test('two reasons at the same instant are two decisions', () => {
  const candidates = candidatesFromChanges([
    change({ class: 'excluded', modelId: 'a', note: 'plan_required' }),
    change({ class: 'excluded', modelId: 'b', note: 'not_proven_free' }),
  ]);

  assert.equal(candidates.length, 2);
  assert.deepEqual(candidates.map((candidate) => candidate.modelId).sort(), ['a', 'b']);
});

test('a metadata change is deliberately not an alert', () => {
  // Real news, and it belongs on the change feed. An alert list that reports
  // every price move reports nothing.
  assert.deepEqual(candidatesFromChanges([
    change({ class: 'price_changed' }),
    change({ class: 'context_changed' }),
    change({ class: 'capability_changed' }),
    change({ class: 'quality_changed' }),
  ]), []);
});

test('roster candidates come from the events, and age out of the window', () => {
  const db = openDb(':memory:');
  try {
    const now = '2026-08-23T12:00:00.000Z';
    const inside = new Date(Date.parse(now) - 60_000).toISOString();
    const outside = new Date(Date.parse(now) - ROSTER_ALERT_WINDOW_MS - 60_000).toISOString();
    seedModel(db, 'recent-model');
    seedModel(db, 'ancient-model');
    recordEvent(db, 'added', 'recent-model', inside);
    recordEvent(db, 'added', 'ancient-model', outside);

    const candidates = rosterCandidates(db, now);
    assert.equal(candidates.length, 1);
    assert.equal(candidates[0].modelId, 'recent-model');
  } finally {
    db.close();
  }
});

test('a roster alert reaches the ledger, and ages out into resolved on its own', () => {
  const db = openDb(':memory:');
  try {
    const observedAt = '2026-08-23T09:00:00.000Z';
    seedModel(db, 'gone-one', 'retired');
    recordEvent(db, 'removed', 'gone-one', observedAt, 'absent for 3 consecutive syncs');

    const open = reconcileAlerts(db, health(), '2026-08-23T09:01:00.000Z');
    assert.equal(open.length, 1);
    assert.equal(open[0].kind, 'model_retired');
    assert.equal(open[0].status, 'open');

    // A week later the same event is no longer a candidate, so the ledger
    // resolves it. That is the mechanism that lets an event live in a
    // level-triggered store without a second lifecycle beside this one.
    const aged = reconcileAlerts(db, health(), new Date(Date.parse(observedAt) + ROSTER_ALERT_WINDOW_MS + 60_000).toISOString());
    assert.equal(aged.length, 1);
    assert.equal(aged[0].status, 'resolved');
  } finally {
    db.close();
  }
});

test('acknowledging a roster alert keeps it off the open list while it is still recent', () => {
  const db = openDb(':memory:');
  try {
    seedModel(db, 'new-one');
    recordEvent(db, 'added', 'new-one', '2026-08-23T09:00:00.000Z');
    const [alert] = reconcileAlerts(db, health(), '2026-08-23T09:01:00.000Z');
    transitionAlert(db, alert.id, 'acknowledged', '2026-08-23T09:02:00.000Z');

    // The event is still inside the window, so it is still a candidate. A
    // reconcile must not undo the acknowledgement by reopening it.
    const after = reconcileAlerts(db, health(), '2026-08-23T09:03:00.000Z');
    assert.equal(after.length, 1);
    assert.equal(after[0].status, 'acknowledged');
  } finally {
    db.close();
  }
});

test('repeated reconciles of one standing condition do not inflate its count', () => {
  // The regression: `occurrence_count` used to move on every reconcile, and
  // reconcile only ran inside `GET /v1/alerts`. Every alert in the live store
  // carried exactly 2, from a dashboard whose bell and alert panel both fetch on
  // page load 69ms apart — a count of browser polls, not of occurrences.
  const db = openDb(':memory:');
  try {
    const degraded = health({ service: { status: 'degraded' } });
    reconcileAlerts(db, degraded, '2026-08-23T10:00:00.000Z');
    reconcileAlerts(db, degraded, '2026-08-23T10:00:00.069Z');
    const [alert] = reconcileAlerts(db, degraded, '2026-08-23T10:00:30.000Z');

    assert.equal(alert.occurrenceCount, 1, 'polling is not occurring');
    // What "still true as of" means is `last_seen_at`, and it does advance.
    assert.equal(alert.lastSeenAt, '2026-08-23T10:00:30.000Z');
  } finally {
    db.close();
  }
});

test('an event whose model row is gone still raises its alert', () => {
  /**
   * The regression, and it was found by writing this file rather than by
   * reasoning: `operational_alerts` holds a composite foreign key into `models`
   * and SQLite enforces it, so naming a pair with no row raised
   * `FOREIGN KEY constraint failed` INSIDE the reconcile. That does not lose one
   * alert — it aborts the transaction, so `GET /v1/alerts` answers 500 and the
   * bell goes dark entirely because of one dangling event.
   */
  const db = openDb(':memory:');
  try {
    recordEvent(db, 'removed', 'never-stored', '2026-08-23T09:00:00.000Z');

    const alerts = reconcileAlerts(db, health(), '2026-08-23T09:01:00.000Z');
    assert.equal(alerts.length, 1);
    assert.equal(alerts[0].kind, 'model_retired');
    // The provider link survives; only the row-level link is dropped, and the
    // model is still named in the text a reader actually sees.
    assert.equal(alerts[0].providerId, 'acme');
    assert.equal(alerts[0].modelId, null);
    assert.match(alerts[0].title, /never-stored/);
  } finally {
    db.close();
  }
});

test('a model that does exist keeps its row-level link', () => {
  const db = openDb(':memory:');
  try {
    seedModel(db, 'still-here');
    recordEvent(db, 'added', 'still-here', '2026-08-23T09:00:00.000Z');

    const [alert] = reconcileAlerts(db, health(), '2026-08-23T09:01:00.000Z');
    assert.equal(alert.modelId, 'still-here');
  } finally {
    db.close();
  }
});
