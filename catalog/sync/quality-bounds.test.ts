/**
 * The shipped bounds overlay.
 *
 * The one place in the catalog where a human's judgement becomes a published
 * number, so the file itself is held to the shape that makes it auditable: a
 * side, a reason, a reference model and re-verifiable evidence. An entry
 * missing any of those is a number with nobody standing behind it.
 */

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { loadQualityBounds } from './quality-bounds.ts';

describe('overlays/quality-bounds.json', () => {
  const bounds = loadQualityBounds();

  test('every entry states a side, and only the two that exist', () => {
    for (const [id, b] of Object.entries(bounds)) {
      assert.ok(b.side === 'lower' || b.side === 'upper', `${id} has no valid side`);
    }
  });

  test('every entry carries a numeric value', () => {
    for (const [id, b] of Object.entries(bounds)) {
      assert.equal(typeof b.value, 'number', `${id} has no numeric value`);
      assert.ok(Number.isFinite(b.value), `${id} value is not finite`);
    }
  });

  test('every entry names the model it is a relation to, and says why', () => {
    // A bound with no stated relation is indistinguishable from a guess, and
    // this file is the only place a guess could enter the catalog as a number.
    for (const [id, b] of Object.entries(bounds)) {
      assert.ok(b.referenceModel && b.referenceModel.length > 0, `${id} names no reference model`);
      assert.ok(b.reason && b.reason.length > 40, `${id} has no substantive reason`);
      assert.ok(b.evidence.length > 0, `${id} carries no re-verifiable evidence`);
    }
  });

  test('the reason mentions the reference model, so a reader sees the relation', () => {
    for (const [id, b] of Object.entries(bounds)) {
      const bare = b.referenceModel.split('/').pop()!;
      assert.match(b.reason.toLowerCase(), new RegExp(bare.toLowerCase().replace('.', '\.')), `${id}'s reason never mentions ${b.referenceModel}`);
    }
  });
});
