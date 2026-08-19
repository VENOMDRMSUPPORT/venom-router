import assert from 'node:assert/strict';
import { describe, test } from 'node:test';
import { assessExternalBenchmark, type ExternalBenchmarkEvidence } from './external-benchmark.ts';

const NOW = new Date('2026-08-19T00:00:00.000Z');
const valid = (overrides: Partial<ExternalBenchmarkEvidence> = {}): ExternalBenchmarkEvidence => ({
  expectedIdentity: 'z-ai/glm-5.3', sourceIdentity: 'z-ai/glm-5.3',
  dimension: 'coding', crosswalkVersion: 'coding-aa-v1', point: 80, rangeMin: 0, rangeMax: 100,
  methodologyUrl: 'https://example.test/method', sampleCount: 60,
  confidenceInterval95: null, publishedAt: '2026-08-01T00:00:00.000Z',
  sourceUrl: 'https://example.test/result', ...overrides,
});

describe('external benchmark eligibility', () => {
  test('accepts exact fresh finite evidence and normalizes it', () => {
    const decision = assessExternalBenchmark(valid(), NOW);
    assert.equal(decision.status, 'accepted');
    assert.ok(decision.result!.score < 100);
    assert.equal(decision.effectiveCriteria, 60);
  });

  test('keeps an inexact identity as provenance only', () => {
    assert.deepEqual(assessExternalBenchmark(valid({ sourceIdentity: 'z-ai/glm-5.2' }), NOW), {
      status: 'provenance_only', reason: 'identity_mismatch', effectiveCriteria: null, result: null,
    });
  });

  test('rejects absent crosswalk, methodology, or stale evidence', () => {
    assert.equal(assessExternalBenchmark(valid({ crosswalkVersion: null }), NOW).reason, 'missing_crosswalk');
    assert.equal(assessExternalBenchmark(valid({ methodologyUrl: null }), NOW).reason, 'missing_methodology');
    assert.equal(assessExternalBenchmark(valid({ publishedAt: '2026-01-01T00:00:00.000Z' }), NOW).reason, 'stale_evidence');
  });

  test('infers finite evidence from a non-zero normalized 95% interval', () => {
    const decision = assessExternalBenchmark(valid({ sampleCount: null, confidenceInterval95: { lower: 75, upper: 85 } }), NOW);
    assert.equal(decision.status, 'accepted');
    assert.ok(decision.effectiveCriteria! > 1);
  });

  test('zero-width interval without sample count is provenance only', () => {
    const decision = assessExternalBenchmark(valid({ sampleCount: null, confidenceInterval95: { lower: 80, upper: 80 } }), NOW);
    assert.equal(decision.status, 'provenance_only');
    assert.equal(decision.reason, 'non_finite_evidence');
  });
});
