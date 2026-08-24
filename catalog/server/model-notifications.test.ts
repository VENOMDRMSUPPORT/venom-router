import assert from 'node:assert/strict';
import { test } from 'node:test';
import { openDb } from '../db/index.ts';
import {
  clampNotificationLimit,
  listCatalogNotifications,
  markCatalogNotificationsRead,
  reconcileCatalogNotifications,
} from './model-notifications.ts';

test('notification reconciliation classifies every recorded source event beyond the public change-feed limit exactly once', () => {
  const db = openDb(':memory:');
  try {
    const insert = db.prepare(`INSERT INTO model_events (provider_id, model_id, kind, field, old_value, new_value, reason, at)
      VALUES (?, ?, 'added', NULL, NULL, ?, NULL, ?)`);
    for (let index = 0; index < 501; index += 1) {
      insert.run('acme', `model-${index}`, `model-${index}`, `2026-08-23T10:${String(index % 60).padStart(2, '0')}:${String(Math.floor(index / 60)).padStart(2, '0')}.000Z`);
    }

    reconcileCatalogNotifications(db, '2026-08-24T00:00:00.000Z');
    const firstCount = Number((db.prepare('SELECT COUNT(*) count FROM catalog_notifications').get() as { count: number }).count);
    assert.equal(firstCount, 501);

    reconcileCatalogNotifications(db, '2026-08-24T00:01:00.000Z');
    const secondCount = Number((db.prepare('SELECT COUNT(*) count FROM catalog_notifications').get() as { count: number }).count);
    assert.equal(secondCount, 501);

    const updated = markCatalogNotificationsRead(db, ['model-event:1'], { now: '2026-08-24T00:02:00.000Z' });
    assert.equal(updated, 1);
    const readAt = (db.prepare('SELECT read_at readAt FROM catalog_notifications WHERE id = ?').get('model-event:1') as { readAt: string | null }).readAt;
    assert.equal(readAt, '2026-08-24T00:02:00.000Z');
  } finally {
    db.close();
  }
});

test('a shared-source failure becomes one warning notification and reconciliation is idempotent', () => {
  const db = openDb(':memory:');
  try {
    db.prepare(`INSERT INTO sync_runs (provider_id, started_at, finished_at, outcome, roster_count, error)
      VALUES ('catalog-shared-sources', '2026-08-24T10:00:00.000Z', '2026-08-24T10:00:01.000Z', 'failed', 0, 'models.dev unavailable')`).run();

    reconcileCatalogNotifications(db, '2026-08-24T10:01:00.000Z');
    reconcileCatalogNotifications(db, '2026-08-24T10:02:00.000Z');

    const rows = (db.prepare(`SELECT id, category, kind, provider_id, model_id, title, detail FROM catalog_notifications`).all() as unknown as {
      id: string; category: string; kind: string; provider_id: string | null; model_id: string | null;
      title: string; detail: string;
    }[]);
    assert.deepEqual(rows.map((row) => ({
      id: row.id, category: row.category, kind: row.kind,
      provider_id: row.provider_id, model_id: row.model_id,
    })), [{
      // Null, not the storage sentinel. The panel turns `providerId` into a link
      // to /provider/<id>, and `catalog-shared-sources` is not a provider — that
      // link renders the SPA's 404. A failure that belongs to no provider says so.
      id: 'sync-run:1', category: 'warning', kind: 'fetch_problem',
      provider_id: null, model_id: null,
    }]);
    assert.ok(!rows[0].title.includes('catalog-shared-sources'), 'the sentinel is not shown to a reader');
    assert.ok(!rows[0].detail.includes('this provider'), 'and the copy does not call it a provider');
  } finally {
    db.close();
  }
});

test('notification listing uses one finite clamp for default, invalid, small, and large limits', () => {
  assert.equal(clampNotificationLimit(undefined), 100);
  assert.equal(clampNotificationLimit(Number.NaN), 100);
  assert.equal(clampNotificationLimit(Number.POSITIVE_INFINITY), 100);
  assert.equal(clampNotificationLimit(0), 1);
  assert.equal(clampNotificationLimit(501), 500);

  const db = openDb(':memory:');
  try {
    const insert = db.prepare(`INSERT INTO catalog_notifications
      (id, source_kind, source_id, category, kind, title, detail, observed_at, created_at)
      VALUES (?, 'sync_run', ?, 'warning', 'fetch_problem', 'title', 'detail', ?, ?)`);
    for (let index = 0; index < 3; index += 1) {
      const timestamp = `2026-08-24T10:0${index}:00.000Z`;
      insert.run(`sync-run:${index + 1}`, index + 1, timestamp, timestamp);
    }
    assert.equal(listCatalogNotifications(db, { limit: 0 }).length, 1);
    assert.equal(listCatalogNotifications(db, { limit: Number.NaN }).length, 3);
    assert.equal(listCatalogNotifications(db, { limit: 500 }).length, 3);
  } finally {
    db.close();
  }
});

test('mark-read updates every requested id instead of silently truncating the batch', () => {
  const db = openDb(':memory:');
  try {
    const insert = db.prepare(`INSERT INTO catalog_notifications
      (id, source_kind, source_id, category, kind, title, detail, observed_at, created_at)
      VALUES (?, 'sync_run', ?, 'warning', 'fetch_problem', 'title', 'detail', ?, ?)`);
    const ids = Array.from({ length: 101 }, (_, index) => `sync-run:${index + 1}`);
    for (let index = 0; index < ids.length; index += 1) {
      const timestamp = `2026-08-24T11:${String(index % 60).padStart(2, '0')}:00.000Z`;
      insert.run(ids[index], index + 1, timestamp, timestamp);
    }
    assert.equal(markCatalogNotificationsRead(db, ids, { now: '2026-08-24T12:00:00.000Z' }), 101);
  } finally {
    db.close();
  }
});
