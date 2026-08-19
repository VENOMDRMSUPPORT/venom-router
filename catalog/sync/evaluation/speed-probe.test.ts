import assert from 'node:assert/strict';
import { describe, test } from 'node:test';
import { createStreamingSpeedProbe } from './speed-probe.ts';

describe('streaming speed probe', () => {
  test('measures actual first content, completion throughput and end-to-end time', async () => {
    const requests: Request[] = [];
    const encoder = new TextEncoder();
    const stream = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(encoder.encode('data: {"choices":[{"delta":{"content":"hello"}}]}\n\n'));
        controller.enqueue(encoder.encode('data: {"choices":[{"delta":{"content":" world"}}],"usage":{"completion_tokens":100}}\n\n'));
        controller.enqueue(encoder.encode('data: [DONE]\n\n'));
        controller.close();
      },
    });
    const times = [0, 1_000, 3_000];
    const probe = createStreamingSpeedProbe({
      providerId: 'clinepass', modelId: 'cline-pass/glm-5.2', credential: 'SECRET',
      fetchImpl: async (input, init) => {
        requests.push(new Request(input, init));
        return new Response(stream, { status: 200, headers: { 'content-type': 'text/event-stream' } });
      },
      nowMs: () => times.shift() ?? 3_000,
    });

    const result = await probe();
    assert.deepEqual(result, {
      success: true, ttftSeconds: 1, outputTokensPerSecond: 50, endToEndSeconds: 3, errorCode: null,
    });
    const body = await requests[0].json() as Record<string, unknown>;
    assert.equal(body.stream, true);
    assert.equal(body.max_tokens, 512);
  });
});
