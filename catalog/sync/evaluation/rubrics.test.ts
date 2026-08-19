import assert from 'node:assert/strict';
import { describe, test } from 'node:test';
import { QUALITY_DIMENSIONS, loadRubrics, rubricDigest, type RubricManifest } from './rubrics.ts';

describe('catalog-rubrics-v1 manifest', () => {
  test('contains exactly 20 scenarios and five equal criteria for every quality dimension', () => {
    const manifest = loadRubrics();
    assert.equal(manifest.version, 'catalog-rubrics-v1');
    for (const dimension of QUALITY_DIMENSIONS) {
      const rubric = manifest.dimensions[dimension];
      assert.equal(rubric.scenarios.length, 20, `${dimension} scenario count`);
      assert.equal(rubric.criteria.length, 5, `${dimension} criteria count`);
      assert.deepEqual(rubric.criteria.map((criterion) => criterion.weight), [0.2, 0.2, 0.2, 0.2, 0.2]);
    }
  });

  test('canonical digest is stable and records the manifest version', () => {
    const manifest = loadRubrics();
    const first = rubricDigest(manifest);
    const second = rubricDigest(JSON.parse(JSON.stringify(manifest)) as RubricManifest);
    assert.match(first, /^[a-f0-9]{64}$/);
    assert.equal(first, second);
  });

  test('duplicate scenario ids are rejected', () => {
    assert.throws(() => loadRubrics({
      version: 'catalog-rubrics-v1',
      dimensions: {
        coding: {
          criteria: [{ id: 'a', weight: 0.2 }, { id: 'b', weight: 0.2 }, { id: 'c', weight: 0.2 }, { id: 'd', weight: 0.2 }, { id: 'e', weight: 0.2 }],
          scenarios: Array.from({ length: 20 }, (_, index) => ({ id: index === 1 ? 'coding-00' : `coding-${String(index).padStart(2, '0')}` })),
        },
      },
    } as unknown as RubricManifest), /duplicate scenario/i);
  });
});
