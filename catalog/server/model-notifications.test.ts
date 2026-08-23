import assert from 'node:assert/strict';
import { test } from 'node:test';
import { openDb } from '../db/index.ts';
import { markCatalogNotificationsRead, reconcileCatalogNotifications } from './model-notifications.ts';

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
