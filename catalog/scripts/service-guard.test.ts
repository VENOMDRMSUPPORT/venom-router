import assert from 'node:assert/strict';
import { createServer } from 'node:net';
import { describe, test } from 'node:test';
import { BIND_HOST } from '../server/index.ts';
import { assertServiceNotListening, portIsListening } from './service-guard.ts';

describe('terminal batch service guard', () => {
  test('refuses to run while the service holds the port', async () => {
    await assert.rejects(
      () => assertServiceNotListening(async () => true),
      /service_is_listening/,
    );
  });

  test('allows the run when the port is free', async () => {
    await assert.doesNotReject(() => assertServiceNotListening(async () => false));
  });

  test('the probe reports a real listener on loopback', async () => {
    const server = createServer();
    const port: number = await new Promise((resolve) => {
      server.listen(0, BIND_HOST, () => resolve((server.address() as { port: number }).port));
    });
    try {
      assert.equal(await portIsListening(port), true);
    } finally {
      await new Promise((resolve) => server.close(resolve));
    }
    // Once closed, the same port must read as free — otherwise the guard would
    // block every batch forever after the service is stopped.
    assert.equal(await portIsListening(port), false);
  });
});
