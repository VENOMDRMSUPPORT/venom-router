import { describe, test } from 'node:test';
import assert from 'node:assert/strict';
import { openDb, type Db } from '../db/index.ts';
import {
  beginResolutionWindow,
  bootstrapResolutionJobs,
  finishResolutionAttempt,
  loadResolution,
  listDueResolutionJobs,
} from './resolution-jobs.ts';

const T0 = '2026-08-19T10:00:00.000Z';

function seedModel(db: Db, modelId = 'new-model') {
  db.prepare(`INSERT OR IGNORE INTO providers (id, name, roster_url) VALUES ('p', 'Provider', 'https://p.test/models')`).run();
  db.prepare(`INSERT INTO models (
    provider_id, model_id, display_name, status, first_seen_at, last_seen_at, miss_count
  ) VALUES ('p', ?, ?, 'active', ?, ?, 0)`).run(modelId, modelId, T0, T0);
}

describe('resolution job lifecycle', () => {
  test('startup bootstraps missing jobs without reactivating a dormant job', () => {
    const db = openDb(':memory:');
    seedModel(db, 'existing');
    beginResolutionWindow(db, T0);
    finishResolutionAttempt(db, 'p', 'existing', '2026-08-19T10:05:00.000Z');
    seedModel(db, 'added-before-restart');

    const inserted = bootstrapResolutionJobs(db, '2026-08-19T11:00:00.000Z');

    assert.equal(inserted, 1);
    assert.equal(loadResolution(db, 'p', 'existing')?.state, 'source_incomplete');
    assert.equal(loadResolution(db, 'p', 'existing')?.nextAttemptAt, null);
    assert.equal(loadResolution(db, 'p', 'added-before-restart')?.state, 'processing');
    assert.equal(loadResolution(db, 'p', 'added-before-restart')?.lastAttemptAt, null);
    assert.equal(loadResolution(db, 'p', 'added-before-restart')?.nextAttemptAt, '2026-08-19T11:00:00.000Z');
    assert.equal(listDueResolutionJobs(db, '2026-08-19T11:00:00.000Z').length, 1);
  });

  test('a published incomplete model starts processing and is due after one minute', () => {
    const db = openDb(':memory:');
    seedModel(db);

    beginResolutionWindow(db, T0);

    assert.deepEqual(loadResolution(db, 'p', 'new-model'), {
      state: 'processing',
      reasons: ['missing_context', 'missing_max_output', 'missing_modalities', 'missing_tools', 'missing_reasoning', 'missing_structured', 'missing_attachment', 'missing_cost', 'missing_vq', 'missing_vo'],
      firstDetectedAt: T0,
      lastAttemptAt: T0,
      nextAttemptAt: '2026-08-19T10:01:00.000Z',
    });
    assert.equal(listDueResolutionJobs(db, '2026-08-19T10:00:59.999Z').length, 0);
    assert.equal(listDueResolutionJobs(db, '2026-08-19T10:01:00.000Z').length, 1);
  });

  test('an unresolved job becomes dormant after the five-minute attempt', () => {
    const db = openDb(':memory:');
    seedModel(db);
    beginResolutionWindow(db, T0);

    finishResolutionAttempt(db, 'p', 'new-model', '2026-08-19T10:01:00.000Z');
    assert.equal(loadResolution(db, 'p', 'new-model')?.nextAttemptAt, '2026-08-19T10:05:00.000Z');

    finishResolutionAttempt(db, 'p', 'new-model', '2026-08-19T10:05:00.000Z');
    const resolution = loadResolution(db, 'p', 'new-model');
    assert.equal(resolution?.state, 'source_incomplete');
    assert.equal(resolution?.nextAttemptAt, null);
    assert.equal(listDueResolutionJobs(db, '2026-08-19T11:00:00.000Z').length, 0);
  });

  test('a fully specified model without a benchmark becomes awaiting_external_benchmark', () => {
    const db = openDb(':memory:');
    seedModel(db, 'unbenchmarked');
    db.exec(`UPDATE models SET
      context_tokens=128000, output_tokens=32000, input_modalities='["text"]',
      tools=1, reasoning=1, structured=1, attachment=0, cost_kind='free'
      WHERE provider_id='p' AND model_id='unbenchmarked'`);
    db.prepare(`INSERT INTO model_scores (
      provider_id, model_id, kind, value, evidence_level, precision_dp, methodology_ver, computed_at
    ) VALUES ('p','unbenchmarked','VQ',NULL,'unrated',0,'venom-score-v2',?)`).run(T0);
    db.prepare(`INSERT INTO model_scores (
      provider_id, model_id, kind, value, evidence_level, precision_dp, methodology_ver, computed_at
    ) VALUES ('p','unbenchmarked','VO',70,'derived',0,'venom-score-v2',?)`).run(T0);

    beginResolutionWindow(db, T0);
    finishResolutionAttempt(db, 'p', 'unbenchmarked', '2026-08-19T10:05:00.000Z');

    assert.equal(loadResolution(db, 'p', 'unbenchmarked')?.state, 'awaiting_external_benchmark');
  });

  test('a resolved model leaves the active queue and reports complete', () => {
    const db = openDb(':memory:');
    seedModel(db, 'resolved');
    beginResolutionWindow(db, T0);
    db.exec(`UPDATE models SET
      context_tokens=128000, output_tokens=32000, input_modalities='["text"]',
      tools=1, reasoning=1, structured=1, attachment=0, cost_kind='free'
      WHERE provider_id='p' AND model_id='resolved'`);
    for (const [kind, value, level] of [['VQ', 60, 'measured'], ['VO', 70, 'derived']] as const) {
      db.prepare(`INSERT INTO model_scores (
        provider_id, model_id, kind, value, evidence_level, precision_dp, methodology_ver, computed_at
      ) VALUES ('p','resolved',?,?,?,0,'venom-score-v2',?)`).run(kind, value, level, T0);
    }

    finishResolutionAttempt(db, 'p', 'resolved', '2026-08-19T10:01:00.000Z');

    assert.equal(loadResolution(db, 'p', 'resolved')?.state, 'complete');
    assert.equal(listDueResolutionJobs(db, '2026-08-19T11:00:00.000Z').length, 0);
  });

  test('retirement clears a held processing job instead of leaving it due forever', () => {
    const db = openDb(':memory:');
    seedModel(db, 'retired-while-processing');
    beginResolutionWindow(db, T0);
    db.prepare(`UPDATE models SET status='retired' WHERE provider_id='p' AND model_id='retired-while-processing'`).run();

    const result = finishResolutionAttempt(db, 'p', 'retired-while-processing', '2026-08-19T10:01:00.000Z');
    const job = db.prepare(`SELECT status, reasons_json, next_attempt_at FROM resolution_jobs WHERE provider_id='p' AND model_id='retired-while-processing'`).get() as unknown as {
      status: string; reasons_json: string; next_attempt_at: string | null;
    };

    assert.equal(result.state, 'complete');
    assert.equal(job.status, 'complete');
    assert.equal(job.reasons_json, '[]');
    assert.equal(job.next_attempt_at, null);
    assert.equal(listDueResolutionJobs(db, '2026-08-19T10:02:00.000Z').length, 0);
  });

  test('startup repairs a job stranded on a model that is no longer live', () => {
    // The crash-and-restart half of the same defect: a process that died between
    // the retirement and the job update leaves a due `processing` row behind,
    // and neither the window pass nor the attempt pass can see it — both iterate
    // live offerings only. Startup is the one pass that can, so it must.
    const db = openDb(':memory:');
    seedModel(db, 'stranded');
    beginResolutionWindow(db, T0);
    assert.equal(listDueResolutionJobs(db, '2026-08-19T10:02:00.000Z').length, 1, 'due before the retirement');
    db.prepare(`UPDATE models SET status='retired' WHERE provider_id='p' AND model_id='stranded'`).run();

    bootstrapResolutionJobs(db, '2026-08-19T11:00:00.000Z');

    const job = db.prepare(`SELECT status, reasons_json, next_attempt_at FROM resolution_jobs WHERE provider_id='p' AND model_id='stranded'`).get() as unknown as {
      status: string; reasons_json: string; next_attempt_at: string | null;
    };
    assert.equal(job.status, 'complete');
    assert.equal(job.reasons_json, '[]');
    assert.equal(job.next_attempt_at, null);
    assert.equal(listDueResolutionJobs(db, '2026-08-19T11:00:00.000Z').length, 0, 'and never due again');
  });

  test('a live job is left alone by that repair', () => {
    // The repair is scoped by liveness, not by age: a perfectly ordinary due job
    // must survive a restart, or the sweep would silently cancel real work.
    const db = openDb(':memory:');
    seedModel(db, 'still-live');
    beginResolutionWindow(db, T0);

    bootstrapResolutionJobs(db, '2026-08-19T11:00:00.000Z');

    assert.equal(loadResolution(db, 'p', 'still-live')?.state, 'processing');
    assert.equal(listDueResolutionJobs(db, '2026-08-19T11:00:00.000Z').length, 1);
  });

  test('only an official-source conflict opens a resolution reason', () => {
    const db = openDb(':memory:');
    seedModel(db, 'conflict-model');
    db.exec(`UPDATE models SET
      context_tokens=128000, output_tokens=32000, input_modalities='["text"]',
      tools=1, reasoning=1, structured=1, attachment=0, cost_kind='free'
      WHERE provider_id='p' AND model_id='conflict-model'`);
    for (const [kind, value, level] of [['VQ', 60, 'measured'], ['VO', 70, 'derived']] as const) {
      db.prepare(`INSERT INTO model_scores (
        provider_id, model_id, kind, value, evidence_level, precision_dp, methodology_ver, computed_at
      ) VALUES ('p','conflict-model',?,?,?,0,'venom-score-v2',?)`).run(kind, value, level, T0);
    }
    db.prepare(`INSERT INTO model_conflicts
      (provider_id, model_id, field, sides_json, conflict_type, detected_at)
      VALUES ('p','conflict-model','tools','[]','source_disagreement',?)`).run(T0);
    beginResolutionWindow(db, T0);
    assert.equal(loadResolution(db, 'p', 'conflict-model')?.state, 'complete');

    db.prepare(`UPDATE model_conflicts SET conflict_type='official_source_disagreement'
      WHERE provider_id='p' AND model_id='conflict-model' AND field='tools'`).run();
    bootstrapResolutionJobs(db, '2026-08-19T11:00:00.000Z');
    assert.deepEqual(loadResolution(db, 'p', 'conflict-model')?.reasons, ['conflicting_official_sources']);
  });

  test('startup finishes a parked job whose reasons have since been answered', () => {
    // The live case: `opencode-go/longcat-2.0` sat dormant on `missing_structured`
    // long after a probe measured `structured`. It read `source_incomplete` in the
    // panel while the same row reported no missing facts and `catalogReady` — one
    // model, two answers. Only a full sync cleared it, so the lie could stand for
    // a scheduler interval, or forever if syncs kept failing.
    const db = openDb(':memory:');
    seedModel(db, 'answered-while-dormant');
    beginResolutionWindow(db, T0);
    finishResolutionAttempt(db, 'p', 'answered-while-dormant', '2026-08-19T10:05:00.000Z');
    assert.equal(loadResolution(db, 'p', 'answered-while-dormant')?.state, 'source_incomplete');

    db.exec(`UPDATE models SET
      context_tokens=128000, output_tokens=32000, input_modalities='["text"]',
      tools=1, reasoning=1, structured=1, attachment=0, cost_kind='free'
      WHERE provider_id='p' AND model_id='answered-while-dormant'`);
    for (const [kind, value, level] of [['VQ', 60, 'measured'], ['VO', 70, 'derived']] as const) {
      db.prepare(`INSERT INTO model_scores (
        provider_id, model_id, kind, value, evidence_level, precision_dp, methodology_ver, computed_at
      ) VALUES ('p','answered-while-dormant',?,?,?,0,'venom-score-v2',?)`).run(kind, value, level, T0);
    }

    bootstrapResolutionJobs(db, '2026-08-19T11:00:00.000Z');

    const resolution = loadResolution(db, 'p', 'answered-while-dormant');
    assert.equal(resolution?.state, 'complete');
    assert.deepEqual(resolution?.reasons, []);
    assert.equal(listDueResolutionJobs(db, '2026-08-19T11:00:00.000Z').length, 0);
  });

  test('a parked job with real reasons left is still not reactivated', () => {
    // The other half of the rule: only an answered window is finished at startup.
    // A dormant job that still has something outstanding stays dormant, or the
    // repair would cancel work nobody did.
    const db = openDb(':memory:');
    seedModel(db, 'still-outstanding');
    beginResolutionWindow(db, T0);
    finishResolutionAttempt(db, 'p', 'still-outstanding', '2026-08-19T10:05:00.000Z');

    bootstrapResolutionJobs(db, '2026-08-19T11:00:00.000Z');

    const resolution = loadResolution(db, 'p', 'still-outstanding');
    assert.equal(resolution?.state, 'source_incomplete');
    assert.ok((resolution?.reasons.length ?? 0) > 0, 'the outstanding reasons survive the restart');
    assert.equal(listDueResolutionJobs(db, '2026-08-19T11:00:00.000Z').length, 0, 'and it is not put back in the queue');
  });

  test('startup refreshes active job reasons but preserves dormant lifecycle state', () => {
    const db = openDb(':memory:');
    seedModel(db, 'active-job');
    db.exec(`UPDATE models SET
      context_tokens=128000, output_tokens=32000, input_modalities='["text"]',
      tools=1, reasoning=1, structured=1, attachment=0, cost_kind='free'
      WHERE provider_id='p' AND model_id='active-job'`);
    beginResolutionWindow(db, T0);
    assert.deepEqual(loadResolution(db, 'p', 'active-job')?.reasons, ['missing_vq', 'missing_vo']);

    for (const [kind, value, level] of [['VQ', 60, 'measured'], ['VO', 70, 'derived']] as const) {
      db.prepare(`INSERT INTO model_scores (
        provider_id, model_id, kind, value, evidence_level, precision_dp, methodology_ver, computed_at
      ) VALUES ('p','active-job',?,?,?,0,'venom-score-v2',?)`).run(kind, value, level, T0);
    }
    bootstrapResolutionJobs(db, '2026-08-19T10:00:30.000Z');
    assert.equal(loadResolution(db, 'p', 'active-job')?.state, 'complete');
    assert.equal(listDueResolutionJobs(db, '2026-08-19T11:00:00.000Z').length, 0);
  });
});
