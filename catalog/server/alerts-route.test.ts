import test from 'node:test';
import assert from 'node:assert/strict';
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

test('GET /v1/alerts reconciles and returns an open server-owned alert', async () => {
  const { db, value } = deps();
  try {
    const response = await route(value, new URL('http://catalog.test/v1/alerts'), 'GET');
    assert.equal(response.status, 200);
    const body = response.body as { alerts: { id: string; status: string }[]; summary: { active: number } };
    assert.equal(body.alerts[0].id, 'service-degraded');
    assert.equal(body.alerts[0].status, 'open');
    assert.equal(body.summary.active, 1);
  } finally {
    db.close();
  }
});

test('PATCH /v1/alerts/:id changes lifecycle status and rejects invalid requests', async () => {
  const { db, value } = deps();
  try {
    await route(value, new URL('http://catalog.test/v1/alerts'), 'GET');
    const acknowledged = await route(value, new URL('http://catalog.test/v1/alerts/service-degraded'), 'PATCH', { status: 'acknowledged' });
    assert.equal(acknowledged.status, 200);
    assert.equal((acknowledged.body as { status: string }).status, 'acknowledged');

    const invalid = await route(value, new URL('http://catalog.test/v1/alerts/service-degraded'), 'PATCH', { status: 'ignored' });
    assert.equal(invalid.status, 400);

    const unknown = await route(value, new URL('http://catalog.test/v1/alerts/missing'), 'PATCH', { status: 'resolved' });
    assert.equal(unknown.status, 404);
  } finally {
    db.close();
  }
});
