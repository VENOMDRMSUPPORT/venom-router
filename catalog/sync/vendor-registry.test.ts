/**
 * The shipped vendor registry.
 *
 * It is data, not code, which is exactly why it needs a test: a storefront key
 * that no longer exists in the feed, or a vendor with no namespaces, is a
 * silently dead branch — first-party evidence simply stops being found and
 * models drift back into "needs verification" with nothing failing anywhere.
 */

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { loadVendors } from './vendor-registry.ts';

describe('overlays/vendors.json', () => {
  const vendors = loadVendors();

  test('it ships with vendors configured', () => {
    // A registry that parsed to `{}` would disable the whole fallback quietly.
    assert.ok(Object.keys(vendors).length > 0);
  });

  test('every vendor names at least one storefront and one namespace', () => {
    for (const [id, v] of Object.entries(vendors)) {
      assert.ok(v.storefronts.length > 0, `${id} has no storefront`);
      assert.ok(v.namespaces.length > 0, `${id} has no namespace, so nothing can ever be recognised as its model`);
      assert.ok(v.label.length > 0, `${id} has no label`);
    }
  });

  test('no storefront key is claimed by two vendors', () => {
    // Two owners for one key would make membership depend on iteration order.
    const seen = new Map<string, string>();
    for (const [id, v] of Object.entries(vendors))
      for (const s of v.storefronts) {
        assert.equal(seen.get(s), undefined, `${s} is claimed by both ${seen.get(s)} and ${id}`);
        seen.set(s, id);
      }
  });

  test('every vendor declares the prefix the reference index writes it in', () => {
    // Without it `vendorIdentity` returns nothing, silently: the row loses the
    // identity it could have had and nothing fails. The values differ from the
    // registry keys on purpose — key `alibaba`, index prefix `qwen`.
    for (const [id, v] of Object.entries(vendors)) {
      assert.ok(v.canonicalPrefix && v.canonicalPrefix.length > 0, `${id} declares no canonicalPrefix`);
    }
  });

  test('no namespace is claimed by two vendors', () => {
    const seen = new Map<string, string>();
    for (const [id, v] of Object.entries(vendors))
      for (const n of v.namespaces) {
        assert.equal(seen.get(n), undefined, `${n} is claimed by both ${seen.get(n)} and ${id}`);
        seen.set(n, id);
      }
  });
});
