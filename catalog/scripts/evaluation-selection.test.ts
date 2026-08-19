import assert from 'node:assert/strict';
import { test } from 'node:test';
import { shouldSkipDimension } from './evaluation-selection.ts';

test('only skips a scored dimension when its test-set hash is current', async () => {
  const existing = [{ dimension: 'coding', status: 'scored', testSetHash: 'old' }];
  assert.equal(shouldSkipDimension(existing, 'coding', 'new', false), false);
  assert.equal(shouldSkipDimension(existing, 'coding', 'old', false), true);
  assert.equal(shouldSkipDimension(existing, 'coding', 'old', true), false);
});
