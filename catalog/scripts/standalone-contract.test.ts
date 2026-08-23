import test from 'node:test';
import assert from 'node:assert/strict';
import {
  CATALOG_API_PORT,
  CATALOG_BIND_HOST,
  CATALOG_UI_PORT,
  VENOM_ROUTER_CONTROL_PORT,
} from '../config/ports.ts';

test('standalone Catalog endpoints do not collide with each other or Router', () => {
  assert.equal(CATALOG_BIND_HOST, '127.0.0.1');
  assert.notEqual(CATALOG_UI_PORT, CATALOG_API_PORT);
  assert.notEqual(CATALOG_UI_PORT, VENOM_ROUTER_CONTROL_PORT);
  assert.notEqual(CATALOG_API_PORT, VENOM_ROUTER_CONTROL_PORT);
});

test('standalone Catalog defaults remain the documented local endpoints', () => {
  assert.equal(CATALOG_UI_PORT, 5173);
  assert.equal(CATALOG_API_PORT, 8791);
  assert.equal(VENOM_ROUTER_CONTROL_PORT, 8081);
});
