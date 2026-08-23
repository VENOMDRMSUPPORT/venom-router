import test from 'node:test';
import assert from 'node:assert/strict';
import { openDb } from '../db/index.ts';
import { alertSummary, candidatesFromHealth, reconcileAlerts, transitionAlert, type AlertHealthPayload } from './alerts.ts';

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
