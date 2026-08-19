import assert from 'node:assert/strict';
import { describe, test } from 'node:test';
import { openDb } from '../../db/index.ts';
import { createEvaluationRepository } from './repository.ts';
import { persistSpeedEvaluation, type SpeedProbe } from './speed-runner.ts';

describe('persisted offer speed runner', () => {
  test('excludes warmups, retains 20 requests and saves offer-scoped speed', async () => {
    const db = openDb(':memory:');
    db.prepare(`INSERT INTO providers (id,name,roster_url) VALUES ('p','P','https://p.test')`).run();
    db.prepare(`INSERT INTO models (provider_id,model_id,status,first_seen_at,last_seen_at)
      VALUES ('p','m','active','2026-08-19','2026-08-19')`).run();
    let calls = 0;
    const probe: SpeedProbe = async () => {
      calls++;
      return { success: true, ttftSeconds: 1, outputTokensPerSecond: 50, endToEndSeconds: 8, errorCode: null };
    };

    const result = await persistSpeedEvaluation({
      db, providerId: 'p', modelId: 'm', probe, now: () => '2026-08-19T00:00:00.000Z',
    });

    assert.equal(calls, 23);
    assert.equal(result.status, 'complete');
    assert.equal((db.prepare('SELECT COUNT(*) n FROM evaluation_samples').get() as unknown as { n: number }).n, 20);
    const speed = createEvaluationRepository(db).offerDimensions('p', 'm').find((row) => row.dimension === 'speed');
    assert.equal(speed?.status, 'scored');
    assert.ok(speed?.score !== null && speed?.score !== undefined && speed.score > 0 && speed.score < 100);
  });
});
