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
    // Four times the 512 answer tokens the prompt asks for. A reasoning model
    // spends the head of its budget on `reasoning_content`; capping at 512 left
    // it no room to think and then answer, so it streamed no answer at all and
    // the run failed after paying for every request. The headroom is for the
    // preamble only — the measurement still starts at the first ANSWER token.
    assert.equal(body.max_tokens, 2048);
  });

  const streamOf = (chunks: string[]) => (async () => new Response(
    new ReadableStream({
      start(controller) {
        for (const chunk of chunks) controller.enqueue(new TextEncoder().encode(chunk));
        controller.close();
      },
    }),
    { status: 200 },
  )) as typeof fetch;

  test('does not mistake a reasoning stream for an answer', async () => {
    let clock = 0;
    const probe = createStreamingSpeedProbe({
      providerId: 'clinepass', modelId: 'm', credential: 'secret',
      nowMs: () => (clock += 1000),
      fetchImpl: streamOf([
        'data: {"choices":[{"delta":{"reasoning_content":"thinking"}}]}\n\n',
        'data: {"choices":[{"delta":{"reasoning_content":" some more"}}]}\n\n',
        'data: [DONE]\n\n',
      ]),
    });

    const result = await probe();
    // Observed verbatim from opencode-go/glm-5.3: 39 chunks, every one
    // reasoning, no answer at all. Counting them would make a model that thinks
    // for a long time look fast, which is backwards, and would make every score
    // measured before this incomparable with everything after it.
    assert.equal(result.success, false);
    assert.equal(result.errorCode, 'empty_stream_content');
  });

  test('a reasoning preamble does not stop a real answer being measured', async () => {
    let clock = 0;
    const probe = createStreamingSpeedProbe({
      providerId: 'clinepass', modelId: 'm', credential: 'secret',
      nowMs: () => (clock += 1000),
      fetchImpl: streamOf([
        'data: {"choices":[{"delta":{"reasoning_content":"thinking"}}]}\n\n',
        'data: {"choices":[{"delta":{"content":"catalog catalog"}}]}\n\n',
        'data: {"usage":{"completion_tokens":2}}\n\n',
        'data: [DONE]\n\n',
      ]),
    });

    const result = await probe();
    assert.equal(result.success, true);
    assert.ok(result.ttftSeconds !== null && result.ttftSeconds > 0);
    assert.ok(result.outputTokensPerSecond !== null && result.outputTokensPerSecond > 0);
  });
});

describe('what the speed probe is entitled to ask for', () => {
  test('does not pin temperature, because latency does not depend on it', async () => {
    // kimi-k3 and kimi-k2.7-code on OpenCode Go reject anything but temperature
    // 1: "invalid temperature: only 1 is allowed for this model". The probe was
    // sending 0 — a determinism setting that belongs to QUALITY grading, where
    // the same prompt must produce a comparable answer. This measures how fast
    // tokens arrive, and sending a parameter it does not need cost two models
    // their speed score.
    let sent: Record<string, unknown> = {};
    const probe = createStreamingSpeedProbe({
      providerId: 'opencode-go', modelId: 'kimi-k3', credential: 'secret',
      nowMs: () => 1000,
      fetchImpl: (async (_url: string | URL, init?: RequestInit) => {
        sent = JSON.parse(String(init?.body));
        return new Response(new ReadableStream({ start(c) { c.close(); } }), { status: 200 });
      }) as typeof fetch,
    });

    await probe();
    assert.equal(sent.temperature, undefined);
    assert.equal(sent.stream, true, 'streaming is what makes time-to-first-token measurable');
  });
});
