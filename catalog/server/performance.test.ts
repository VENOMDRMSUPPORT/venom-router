import test from 'node:test';
import assert from 'node:assert/strict';
import { openDb } from '../db/index.ts';
import { loadPerformanceByModel } from './performance.ts';

function seedModel(db: ReturnType<typeof openDb>, providerId: string, modelId: string) {
  db.prepare(`INSERT INTO providers (id, name, roster_url) VALUES (?, ?, ?)`).run(providerId, providerId, `https://${providerId}.test/models`);
  db.prepare(`INSERT INTO models (provider_id, model_id, display_name, status, first_seen_at, last_seen_at) VALUES (?, ?, ?, 'active', ?, ?)`)
    .run(providerId, modelId, modelId, '2026-08-23T09:00:00.000Z', '2026-08-23T09:00:00.000Z');
}

function addRun(db: ReturnType<typeof openDb>, providerId: string, modelId: string, runId: string, finishedAt: string, status = 'complete') {
  const run = db.prepare(`INSERT INTO evaluation_runs (
    provider_id, model_id, identity_id, dimension, run_kind, status,
    evaluator_version, rubric_version, test_set_version, test_set_hash,
    methodology_ver, region, independent_run_key, error_code, started_at, finished_at
  ) VALUES (?, ?, NULL, 'speed', 'speed', ?, 'eval', 'rubric', 'speed', NULL, 'overall-score-v1', 'local', ?, NULL, ?, ?)`)
    .run(providerId, modelId, status, runId, finishedAt, finishedAt) as unknown as { lastInsertRowid: number | bigint };
  return Number(run.lastInsertRowid);
}

function addSample(db: ReturnType<typeof openDb>, runId: number, sample: { ttftSeconds: number | null; outputTokensPerSecond: number | null; endToEndSeconds: number | null; success: boolean }, index: number) {
  db.prepare(`INSERT INTO evaluation_samples (
    run_id, scenario_id, repetition, outcome, weighted_successes, weighted_criteria,
    metrics_json, artifact_ref, response_json, error_code, recorded_at
  ) VALUES (?, ?, 1, ?, NULL, NULL, ?, NULL, NULL, NULL, ?)`)
    .run(runId, `speed-${index}`, sample.success ? 'passed' : 'provider_failure', JSON.stringify(sample), '2026-08-23T10:00:00.000Z');
}

test('performance projection aggregates latest complete speed measurements per model', () => {
  const db = openDb(':memory:');
  try {
    seedModel(db, 'acme', 'fast');
    const first = addRun(db, 'acme', 'fast', 'run-1', '2026-08-22T10:00:00.000Z');
    addSample(db, first, { ttftSeconds: 0.2, outputTokensPerSecond: 40, endToEndSeconds: 2, success: true }, 1);
    addSample(db, first, { ttftSeconds: 0.4, outputTokensPerSecond: 60, endToEndSeconds: 3, success: true }, 2);
    const second = addRun(db, 'acme', 'fast', 'run-2', '2026-08-23T10:00:00.000Z');
    addSample(db, second, { ttftSeconds: 0.1, outputTokensPerSecond: 80, endToEndSeconds: 1, success: true }, 1);

    const performance = loadPerformanceByModel(db).get('acme\u0000fast');
    assert.equal(performance?.status, 'measured');
    assert.equal(performance?.runId, second);
    assert.equal(performance?.sampleCount, 1);
    assert.equal(performance?.ttftMedianSeconds, 0.1);
    assert.equal(performance?.outputTokensPerSecondMedian, 80);
    assert.equal(performance?.endToEndP95Seconds, 1);
  } finally {
    db.close();
  }
});

test('performance projection reports not measured when a speed run has no complete samples', () => {
  const db = openDb(':memory:');
  try {
    seedModel(db, 'acme', 'partial');
    const run = addRun(db, 'acme', 'partial', 'run-partial', '2026-08-23T10:00:00.000Z', 'insufficient_evidence');
    addSample(db, run, { ttftSeconds: null, outputTokensPerSecond: null, endToEndSeconds: null, success: false }, 1);
    const performance = loadPerformanceByModel(db).get('acme\u0000partial');
    assert.equal(performance?.status, 'not_measured');
    assert.equal(performance?.ttftMedianSeconds, null);
    assert.equal(performance?.speedScore, null);
  } finally {
    db.close();
  }
});
