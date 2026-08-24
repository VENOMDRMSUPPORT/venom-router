import assert from 'node:assert/strict';
import { test } from 'node:test';
import { openDb } from '../db/index.ts';
import {
  deliverDueNotifications,
  enqueueNotification,
  listNotifications,
  notificationConfig,
  notificationDeliverySummary,
  notificationForTransition,
  notificationHeaders,
  type AlertRecord,
} from './notifications.ts';

/**
 * These cover the webhook module on its own terms.
 *
 * The previous suite reached this code through `reconcileAlerts`, so deleting
 * the alert engine with X1 took the coverage of the delivery loop with it — and
 * that loop still runs on a five-second timer in `server/index.ts`. A shipped
 * path with no test is the state this file exists to end.
 *
 * The fixture seeds `operational_alerts` directly, because the engine that used
 * to do it is gone. That is not a convenience: it is the finding. See the last
 * test in this file.
 */
function seedAlert(db: ReturnType<typeof openDb>, record: AlertRecord): void {
  db.prepare(`INSERT INTO operational_alerts
    (id, kind, severity, title, detail, provider_id, model_id, status, first_seen_at, last_seen_at, occurrence_count)
    VALUES (?, ?, ?, ?, ?, NULL, NULL, ?, ?, ?, ?)`)
    .run(record.id, record.kind, record.severity, record.title, record.detail,
      record.status, record.firstSeenAt, record.lastSeenAt, record.occurrenceCount);
}

const alert = (over: Partial<AlertRecord> = {}): AlertRecord => ({
  id: 'alert-1',
  kind: 'service_degraded',
  severity: 'critical',
  title: 'Catalog service degraded',
  detail: 'The database was not readable on the last health check.',
  providerId: null,
  modelId: null,
  status: 'open',
  firstSeenAt: '2026-08-25T10:00:00.000Z',
  lastSeenAt: '2026-08-25T10:00:00.000Z',
  occurrenceCount: 1,
  ...over,
});

function withEnabledSync<T>(work: () => T): T {
  const previous = { enabled: process.env.CATALOG_ALERT_NOTIFICATIONS, url: process.env.CATALOG_ALERT_WEBHOOK_URL };
  process.env.CATALOG_ALERT_NOTIFICATIONS = 'true';
  process.env.CATALOG_ALERT_WEBHOOK_URL = 'https://hooks.test/catalog';
  try {
    return work();
  } finally {
    if (previous.enabled === undefined) delete process.env.CATALOG_ALERT_NOTIFICATIONS;
    else process.env.CATALOG_ALERT_NOTIFICATIONS = previous.enabled;
    if (previous.url === undefined) delete process.env.CATALOG_ALERT_WEBHOOK_URL;
    else process.env.CATALOG_ALERT_WEBHOOK_URL = previous.url;
  }
}

async function withWebhookEnabled<T>(work: () => Promise<T> | T): Promise<T> {
  const previous = {
    enabled: process.env.CATALOG_ALERT_NOTIFICATIONS,
    url: process.env.CATALOG_ALERT_WEBHOOK_URL,
  };
  process.env.CATALOG_ALERT_NOTIFICATIONS = 'true';
  process.env.CATALOG_ALERT_WEBHOOK_URL = 'https://hooks.test/catalog';
  try {
    return await work();
  } finally {
    if (previous.enabled === undefined) delete process.env.CATALOG_ALERT_NOTIFICATIONS;
    else process.env.CATALOG_ALERT_NOTIFICATIONS = previous.enabled;
    if (previous.url === undefined) delete process.env.CATALOG_ALERT_WEBHOOK_URL;
    else process.env.CATALOG_ALERT_WEBHOOK_URL = previous.url;
  }
}

test('webhook delivery is off unless a URL and the explicit flag are both present', () => {
  assert.equal(notificationConfig({}).enabled, false);
  assert.equal(notificationConfig({ CATALOG_ALERT_NOTIFICATIONS: 'true' }).enabled, false, 'a flag with no URL delivers nowhere');
  assert.equal(notificationConfig({ CATALOG_ALERT_WEBHOOK_URL: 'https://hooks.test/x' }).enabled, false, 'a URL is not consent');
  assert.equal(
    notificationConfig({ CATALOG_ALERT_NOTIFICATIONS: 'true', CATALOG_ALERT_WEBHOOK_URL: 'https://hooks.test/x' }).enabled,
    true,
  );
});

test('a disabled webhook queues nothing, so nothing can be delivered later', () => {
  const db = openDb(':memory:');
  try {
    enqueueNotification(db, alert(), 'opened');
    assert.deepEqual(listNotifications(db), []);
    assert.deepEqual(notificationDeliverySummary(db), { pending: 0, failed: 0 });
  } finally {
    db.close();
  }
});

test('the signature is reproducible from the exact payload bytes', () => {
  const headers = notificationHeaders('{"a":1}', 'shh');
  assert.deepEqual(notificationHeaders('{"a":1}', 'shh'), headers);
  assert.match(headers['x-catalog-signature'], /^sha256=[0-9a-f]{64}$/);
  assert.equal(notificationHeaders('{"a":1}', null)['x-catalog-signature'], undefined, 'no secret, no signature header');
  assert.notEqual(notificationHeaders('{"a":2}', 'shh')['x-catalog-signature'], headers['x-catalog-signature']);
});

test('a delivered notification records its attempt and stops being due', async () => {
  await withWebhookEnabled(async () => {
    const db = openDb(':memory:');
    try {
      seedAlert(db, alert());
      enqueueNotification(db, alert(), 'opened', '2026-08-25T10:00:00.000Z');
      const delivered = await deliverDueNotifications(
        db, notificationConfig(), new Date('2026-08-25T10:00:01.000Z'),
        async () => new Response(null, { status: 204 }),
      );

      assert.equal(delivered, 1);
      const [row] = listNotifications(db);
      assert.equal(row.status, 'delivered');
      assert.equal(row.attempts, 1);
      assert.equal(row.responseStatus, 204);
      assert.equal(row.lastError, null);
      assert.deepEqual(notificationDeliverySummary(db), { pending: 0, failed: 0 });
    } finally {
      db.close();
    }
  });
});

test('a failing webhook retries with backoff and becomes terminal at the attempt ceiling', async () => {
  await withWebhookEnabled(async () => {
    const db = openDb(':memory:');
    try {
      seedAlert(db, alert());
      enqueueNotification(db, alert(), 'opened', '2026-08-25T10:00:00.000Z');
      const config = { ...notificationConfig(), maxAttempts: 2, baseDelayMs: 1000 };
      const failing = async () => new Response('nope', { status: 500 });

      await deliverDueNotifications(db, config, new Date('2026-08-25T10:00:01.000Z'), failing);
      let [row] = listNotifications(db);
      assert.equal(row.status, 'retrying');
      assert.equal(row.attempts, 1);
      assert.equal(row.responseStatus, 500);
      assert.match(row.lastError ?? '', /HTTP 500/);
      assert.deepEqual(notificationDeliverySummary(db), { pending: 1, failed: 0 }, 'retrying still counts as outstanding');

      // Far enough ahead that the backoff window has elapsed.
      await deliverDueNotifications(db, config, new Date('2026-08-25T10:05:00.000Z'), failing);
      [row] = listNotifications(db);
      assert.equal(row.status, 'failed', 'the second attempt reaches maxAttempts');
      assert.equal(row.attempts, 2);
      assert.deepEqual(notificationDeliverySummary(db), { pending: 0, failed: 1 });
    } finally {
      db.close();
    }
  });
});

test('a notification that is not yet due is left alone', async () => {
  await withWebhookEnabled(async () => {
    const db = openDb(':memory:');
    try {
      seedAlert(db, alert());
      enqueueNotification(db, alert(), 'opened', '2026-08-25T10:00:00.000Z');
      const config = { ...notificationConfig(), maxAttempts: 3, baseDelayMs: 60_000 };
      await deliverDueNotifications(db, config, new Date('2026-08-25T10:00:01.000Z'), async () => new Response(null, { status: 500 }));

      let calls = 0;
      const delivered = await deliverDueNotifications(
        db, config, new Date('2026-08-25T10:00:02.000Z'),
        async () => { calls += 1; return new Response(null, { status: 204 }); },
      );
      assert.equal(delivered, 0);
      assert.equal(calls, 0, 'the backoff window is respected, not just recorded');
    } finally {
      db.close();
    }
  });
});

test('nothing in the service can put work in this queue any more', () => {
  // `alert_notifications.alert_id` is a NOT NULL reference to `operational_alerts`,
  // and after X1 removed the alert engine no code writes that table. So the
  // insert below cannot succeed from any production path, the queue cannot gain a
  // row, and the five-second delivery loop in `server/index.ts` polls a table
  // that can never be non-empty.
  //
  // This is pinned rather than papered over: whoever decides the subsystem's
  // fate — give it a producer, or delete it with its timer and its two tables —
  // should find the reason stated, not rediscover it.
  const db = openDb(':memory:');
  try {
    assert.throws(
      () => withEnabledSync(() => enqueueNotification(db, alert(), 'opened')),
      /constraint/i,
      'an orphaned alert id is refused by the schema',
    );
    assert.deepEqual(listNotifications(db), []);
  } finally {
    db.close();
  }
});

test('only a real status change is worth telling anyone about', () => {
  assert.equal(notificationForTransition('open', 'resolved'), 'resolved');
  assert.equal(notificationForTransition('open', 'acknowledged'), 'acknowledged');
  assert.equal(notificationForTransition('resolved', 'open'), 'reopened');
  assert.equal(notificationForTransition('open', 'open'), null);
  assert.equal(notificationForTransition('resolved', 'resolved'), null);
  assert.equal(notificationForTransition('acknowledged', 'open'), null, 'an alert still open is not news');
});
