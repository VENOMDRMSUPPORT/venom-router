import assert from 'node:assert/strict';
import { describe, test } from 'node:test';
import { QUALITY_DIMENSIONS } from './score.ts';
import { benchmarkCrosswalkDigest, loadBenchmarkCrosswalk } from './benchmark-crosswalk.ts';

describe('catalog benchmark crosswalk v1', () => {
  test('maps every quality dimension to one fixed 20x3 task', () => {
    const crosswalk = loadBenchmarkCrosswalk();
    assert.equal(crosswalk.version, 'catalog-benchmark-crosswalk-v1');
    assert.deepEqual(Object.keys(crosswalk.dimensions).sort(), [...QUALITY_DIMENSIONS].sort());
    for (const dimension of QUALITY_DIMENSIONS) {
      const entry = crosswalk.dimensions[dimension];
      assert.equal(entry.sampleCount, 20);
      assert.equal(entry.repetitions, 3);
      assert.match(entry.methodologyUrl, /^https:\/\//);
      assert.ok(entry.task.length > 0);
    }
  });

  test('uses established inspect-evals tasks except for the deterministic structured-output fixture', () => {
    const dimensions = loadBenchmarkCrosswalk().dimensions;
    assert.equal(dimensions.coding.task, 'inspect_evals/humaneval');
    assert.equal(dimensions.reasoning.task, 'inspect_evals/gpqa_diamond');
    assert.equal(dimensions.longContext.task, 'inspect_evals/infinite_bench_kv_retrieval');
    assert.equal(dimensions.toolCalling.task, 'inspect_evals/bfcl');
    assert.equal(dimensions.vision.task, 'inspect_evals/mmmu_multiple_choice');
    assert.equal(dimensions.structuredOutput.task, 'catalog/structured-output-v1');
  });

  test('has a stable SHA-256 digest', () => {
    assert.match(benchmarkCrosswalkDigest(loadBenchmarkCrosswalk()), /^[a-f0-9]{64}$/);
  });
});
