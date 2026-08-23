import assert from 'node:assert/strict';
import { test } from 'node:test';
import { openDb } from '../db/index.ts';
import { route, type AppDeps } from './app.ts';

function deps() {
  const db = openDb(':memory:');
  const value = {
    db,
    runner: { isRunning: false, currentRunStartedAt: null, lastOutcome: null },
    evaluations: { state: { state: 'idle' } },
    now: () => new Date('2026-08-23T10:00:00.000Z'),
  } as unknown as AppDeps;
  return { db, value };
}

test('GET /v1/notifications projects typed model events once and preserves read history', async () => {
  const { db, value } = deps();
  try {
    db.prepare(`INSERT INTO model_events (provider_id, model_id, kind, field, old_value, new_value, reason, at)
      VALUES ('clinepass', 'clinepass-code', 'added', NULL, NULL, 'clinepass-code', NULL, '2026-08-23T09:00:00.000Z'),
             ('clinepass', 'clinepass-old', 'removed', NULL, 'active', 'retired', 'absent upstream', '2026-08-23T09:01:00.000Z'),
             ('clinepass', 'clinepass-code', 'changed', 'context', '128000', '256000', 'context', '2026-08-23T09:02:00.000Z')`).run();

    const first = await route(value, new URL('http://catalog.test/v1/notifications?provider=clinepass'), 'GET');
    assert.equal(first.status, 200);
    const body = first.body as { notifications: { id: string; kind: string; category: string; readAt: string | null }[]; summary: { total: number; unread: number } };
    assert.equal(body.notifications.length, 2);
    assert.deepEqual(body.notifications.map((notification) => [notification.kind, notification.category]), [
      ['model_retired', 'error'],
      ['model_added', 'success'],
    ]);
    assert.equal(body.summary.unread, 2);

    const second = await route(value, new URL('http://catalog.test/v1/notifications?provider=clinepass'), 'GET');
    assert.equal((second.body as { notifications: unknown[] }).notifications.length, 2);

    const read = await route(value, new URL('http://catalog.test/v1/notifications/read'), 'PATCH', { ids: [body.notifications[0].id] });
    assert.equal(read.status, 200);
    assert.equal((read.body as { updated: number }).updated, 1);

    const afterRead = await route(value, new URL('http://catalog.test/v1/notifications?provider=clinepass'), 'GET');
    const afterRows = (afterRead.body as { notifications: { id: string; readAt: string | null }[] }).notifications;
    assert.notEqual(afterRows.find((row) => row.id === body.notifications[0].id)?.readAt, null);
  } finally {
    db.close();
  }
});

test('notification read endpoint rejects malformed identifiers and fetch failures become a warning once', async () => {
  const { db, value } = deps();
  try {
    db.prepare(`INSERT INTO sync_runs (provider_id, started_at, finished_at, outcome, error)
      VALUES ('clinepass', '2026-08-23T09:00:00.000Z', '2026-08-23T09:03:00.000Z', 'failed', 'upstream returned 500')`).run();
    const response = await route(value, new URL('http://catalog.test/v1/notifications'), 'GET');
    const body = response.body as { notifications: { kind: string; category: string; detail: string }[] };
    assert.deepEqual(body.notifications.map((notification) => [notification.kind, notification.category]), [['fetch_problem', 'warning']]);
    assert.equal(body.notifications[0].detail.includes('500'), false);

    const invalid = await route(value, new URL('http://catalog.test/v1/notifications/read'), 'PATCH', { ids: [4] });
    assert.equal(invalid.status, 400);
  } finally {
    db.close();
  }
});
