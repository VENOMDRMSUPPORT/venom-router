import assert from 'node:assert/strict';
import { test } from 'node:test';
import { createApp } from './index.ts';
import { CATALOG_API_CONTRACT_HEADER, CATALOG_API_CONTRACT_VERSION } from '../config/api-contract.ts';

test('HTTP errors do not expose internal exception text', async (t) => {
  const app = createApp(0, ':memory:');
  await new Promise<void>((resolve, reject) => {
    app.server.once('error', reject);
    app.server.listen(0, '127.0.0.1', resolve);
  });
  t.after(() => {
    app.scheduler.stop();
    app.server.close();
  });

  const address = app.server.address();
  assert.ok(address && typeof address !== 'string');
  app.db.close();

  const response = await fetch(`http://127.0.0.1:${address.port}/v1/models`);
  assert.equal(response.status, 500);
  assert.equal(response.headers.get(CATALOG_API_CONTRACT_HEADER), CATALOG_API_CONTRACT_VERSION);
  assert.deepEqual(await response.json(), { error: 'internal server error' });
});
