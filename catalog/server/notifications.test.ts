import test from 'node:test';
import assert from 'node:assert/strict';
import { openDb } from '../db/index.ts';
import { reconcileAlerts, type AlertHealthPayload } from './alerts.ts';
import { deliverDueNotifications, listNotifications, notificationConfig, notificationForTransition, notificationHeaders } from './notifications.ts';

const degraded: AlertHealthPayload = {
  service: { status: 'degraded', databaseReadable: false, syncInFlight: false },
  catalog: { staleProviders: [], providers: [] },
  lastSync: null,
};

test('notification configuration is disabled by default and requires a URL plus explicit flag', () => {
  assert.equal(notificationConfig({}).enabled, false);
  assert.equal(notificationConfig({ CATALOG_ALERT_NOTIFICATIONS: 'true' }).enabled, false);
  assert.equal(notificationConfig({ CATALOG_ALERT_NOTIFICATIONS: 'true', CATALOG_ALERT_WEBHOOK_URL: 'https://hooks.test/catalog' }).enabled, true);
});

test('notification signature is reproducible from the exact payload', () => {
  const headers = notificationHeaders('{"event":"alert"}', 'secret');
  assert.match(headers['x-catalog-signature'], /^sha256=[a-f0-9]{64}$/);
  assert.equal(notificationHeaders('{}', null)['x-catalog-signature'], undefined);
});

test('delivery marks queued notifications delivered and records attempts', async () => {
  const db = openDb(':memory:');
  try {
    const alerts = reconcileAlerts(db, degraded, '2026-08-23T10:00:00.000Z');
    assert.equal(listNotifications(db, alerts[0].id)[0].eventType, 'opened');
    let receivedBody = '';
    const delivered = await deliverDueNotifications(db, {
      webhookUrl: 'https://hooks.test/catalog', webhookSecret: 'secret', enabled: true,
      timeoutMs: 1000, maxAttempts: 3, baseDelayMs: 100,
    }, new Date('2026-08-23T10:00:01.000Z'), async (_url, init) => {
      receivedBody = String(init?.body);
      return new Response(null, { status: 202 });
    });
    assert.equal(delivered, 1);
    const notification = listNotifications(db, alerts[0].id)[0];
    assert.equal(notification.status, 'delivered');
    assert.equal(notification.attempts, 1);
    assert.equal(notification.responseStatus, 202);
    assert.ok(receivedBody.includes('catalog.alert.opened'));
  } finally {
    db.close();
  }
});

test('delivery retries failures and becomes terminal after max attempts', async () => {
  const db = openDb(':memory:');
  try {
    const alerts = reconcileAlerts(db, degraded, '2026-08-23T10:00:00.000Z');
    const config = { webhookUrl: 'https://hooks.test/catalog', webhookSecret: null, enabled: true, timeoutMs: 1000, maxAttempts: 2, baseDelayMs: 100 };
    const failing = async () => new Response(null, { status: 503 });
    assert.equal(await deliverDueNotifications(db, config, new Date('2026-08-23T10:00:01.000Z'), failing), 0);
    assert.equal(listNotifications(db, alerts[0].id)[0].status, 'retrying');
    assert.equal(await deliverDueNotifications(db, config, new Date('2026-08-23T10:00:02.000Z'), failing), 0);
    const notification = listNotifications(db, alerts[0].id)[0];
    assert.equal(notification.status, 'failed');
    assert.equal(notification.attempts, 2);
    assert.equal(notification.responseStatus, 503);
    assert.match(notification.lastError ?? '', /HTTP 503/);
  } finally {
    db.close();
  }
});

test('only meaningful lifecycle transitions emit notification events', () => {
  assert.equal(notificationForTransition('open', 'acknowledged'), 'acknowledged');
  assert.equal(notificationForTransition('acknowledged', 'resolved'), 'resolved');
  assert.equal(notificationForTransition('resolved', 'open'), 'reopened');
  assert.equal(notificationForTransition('open', 'open'), null);
});
