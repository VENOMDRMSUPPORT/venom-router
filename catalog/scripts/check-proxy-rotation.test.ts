import assert from 'node:assert/strict';
import { test } from 'node:test';
import { runProxyCheck } from './check-proxy-rotation.ts';

test('reports distinct masked exits without printing a complete IP address', async () => {
  const exits = ['198.51.100.10', '203.0.113.20', '192.0.2.30'];
  const lines: string[] = [];
  const result = await runProxyCheck({
    providerId: 'opencode-zen',
    samples: 3,
    fetchImpl: async () => new Response(exits.shift(), { status: 200 }),
    write: (line) => lines.push(line),
  });

  assert.deepEqual(result, { samples: 3, successful: 3, uniqueExits: 3, allDifferent: true });
  assert.match(lines.join('\n'), /198\.51\.x\.x/);
  assert.match(lines.join('\n'), /203\.0\.x\.x/);
  assert.ok(!lines.join('\n').includes('198.51.100.10'));
  assert.ok(!lines.join('\n').includes('203.0.113.20'));
});

test('does not claim rotation when the same exit is reused', async () => {
  const result = await runProxyCheck({
    providerId: 'opencode-zen',
    samples: 2,
    fetchImpl: async () => new Response('198.51.100.10', { status: 200 }),
    write: () => {},
  });

  assert.deepEqual(result, { samples: 2, successful: 2, uniqueExits: 1, allDifferent: false });
});
