import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, test } from 'node:test';

const HERE = dirname(fileURLToPath(import.meta.url));
const BATCH_ENTRYPOINTS = [
  'run-speed-evaluations.ts',
  'recalculate-overall.ts',
  'import-inspect-log.ts',
  'run-overall-evaluations.ts',
  'regrade-retained.ts',
  join('..', 'sync', 'run.ts'),
];

describe('catalog batch database access', () => {
  for (const entrypoint of BATCH_ENTRYPOINTS) {
    test(`${entrypoint} opens the database through the shared gateway`, () => {
      const source = readFileSync(join(HERE, entrypoint), 'utf8');
      assert.match(source, /openBatchDb\(/);
      assert.doesNotMatch(source, /openDb\(/);
    });
  }
});
