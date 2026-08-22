import assert from 'node:assert/strict';
import { describe, test } from 'node:test';
import { createEvaluationTransport, evaluationCredentialReport, evaluationHeaders, resolveEvaluationCredential, protocolFor } from './provider-transport.ts';

describe('provider evaluation transport', () => {
  test('normalizes the Cline vision fixture to a supported PNG without changing other providers', async () => {
    const svg = '<svg xmlns="http://www.w3.org/2000/svg" width="128" height="128"><rect width="128" height="128" fill="white"/><text x="64" y="94" font-size="96" text-anchor="middle" fill="black">7</text></svg>';
    const image = `data:image/svg+xml;base64,${Buffer.from(svg).toString('base64')}`;
    const requests: Request[] = [];
    const transport = createEvaluationTransport({
      providerId: 'clinepass', modelId: 'cline-pass/kimi-k2.6', credential: 'secret',
      fetchImpl: async (input, init) => {
        requests.push(new Request(input, init));
        return new Response(JSON.stringify({ data: { choices: [{ message: { content: '{}' } }] }, success: true }), { status: 200 });
      },
    });

    await transport({ messages: [{ role: 'user', content: [{ type: 'image_url', image_url: { url: image } }] }] }, 'secret');
    const body = await requests[0].json() as { messages: Array<{ content: Array<{ image_url: { url: string } }> }> };
    assert.match(body.messages[0].content[0].image_url.url, /^data:image\/png;base64,/);
  });

  test('posts a non-streaming OpenAI chat request and never exposes the credential', async () => {
    const requests: Request[] = [];
    const transport = createEvaluationTransport({
      providerId: 'ollama-cloud',
      modelId: 'kimi-k3',
      credential: 'SECRET-CANARY',
      fetchImpl: async (input, init) => {
        requests.push(new Request(input, init));
        return new Response(JSON.stringify({ choices: [{ message: { content: 'ok' } }] }), {
          status: 200,
          headers: { 'content-type': 'application/json' },
        });
      },
    });

    const outcome = await transport({ messages: [{ role: 'user', content: 'hello' }] }, 'SECRET-CANARY');
    assert.equal(outcome.kind, 'success');
    assert.equal(requests.length, 1);
    assert.equal(requests[0].url, 'https://ollama.com/v1/chat/completions');
    assert.equal(requests[0].headers.get('authorization'), 'Bearer SECRET-CANARY');
    const body = await requests[0].json() as Record<string, unknown>;
    assert.equal(body.model, 'kimi-k3');
    assert.equal(body.stream, false);
  });

  test('bounds the underlying request so a dead proxy cannot outlive the evaluation timeout', async () => {
    let signal: AbortSignal | null | undefined;
    const transport = createEvaluationTransport({
      providerId: 'opencode-zen', modelId: 'mimo-v2.5-free', credential: 'secret',
      fetchImpl: (async (_input: string | URL | Request, init?: RequestInit) => {
        signal = init?.signal;
        return new Response(JSON.stringify({ choices: [{ message: { content: 'ok' } }] }), { status: 200 });
      }) as typeof fetch,
    });

    await transport({ messages: [{ role: 'user', content: 'hello' }] }, 'secret');
    assert.ok(signal instanceof AbortSignal);
  });

  test('normalizes the ClinePass data envelope to the OpenAI response shape', async () => {
    const transport = createEvaluationTransport({
      providerId: 'clinepass',
      modelId: 'cline-pass/mimo-v2.5',
      credential: 'SECRET-CANARY',
      fetchImpl: async () => new Response(JSON.stringify({
        data: {
          choices: [{ message: { content: 'OK', reasoning: 'internal' } }],
          usage: { prompt_tokens: 11, completion_tokens: 3 },
        },
        success: true,
      }), { status: 200, headers: { 'content-type': 'application/json' } }),
    });

    const outcome = await transport({ messages: [{ role: 'user', content: 'hello' }] }, 'SECRET-CANARY');
    assert.equal(outcome.kind, 'success');
    if (outcome.kind !== 'success') throw new Error('expected success');
    assert.deepEqual(outcome.response.body, {
      choices: [{ message: { content: 'OK', reasoning: 'internal' } }],
      usage: { prompt_tokens: 11, completion_tokens: 3 },
    });
  });

  test('adds the Cline client identity headers only for ClinePass', () => {
    const headers = evaluationHeaders('clinepass', 'SECRET-CANARY');
    assert.equal(headers['x-client-type'], 'cline-vscode');
    assert.equal(headers['x-platform'], 'vscode');
    assert.equal(headers.authorization, 'Bearer SECRET-CANARY');
    assert.equal(evaluationHeaders('ollama-cloud', 'SECRET').authorization, 'Bearer SECRET');
    assert.equal(evaluationHeaders('ollama-cloud', 'SECRET')['x-client-type'], undefined);
  });

  test('does not create a transport for an unimplemented protocol', () => {
    assert.throws(() => createEvaluationTransport({
      providerId: 'opencode-go', modelId: 'minimax-m3', protocol: 'messages', credential: 'secret',
    }), /unsupported_evaluation_protocol/);
  });

  test('resolves only the named environment variable', () => {
    const previous = process.env.VENOM_CATALOG_OLLAMA_CLOUD_API_KEY;
    process.env.VENOM_CATALOG_OLLAMA_CLOUD_API_KEY = 'DO_NOT_PRINT';
    try {
      assert.equal(resolveEvaluationCredential('ollama-cloud'), 'DO_NOT_PRINT');
      assert.equal(resolveEvaluationCredential('missing-provider'), null);
    } finally {
      if (previous === undefined) delete process.env.VENOM_CATALOG_OLLAMA_CLOUD_API_KEY;
      else process.env.VENOM_CATALOG_OLLAMA_CLOUD_API_KEY = previous;
    }
  });
});

describe('the Responses protocol', () => {
  test('sends what the Responses API expects, not a chat payload', async () => {
    const seen: { url: string; body: Record<string, unknown> }[] = [];
    const transport = createEvaluationTransport({
      providerId: 'opencode-go',
      modelId: 'grok-4.5',
      protocol: 'responses',
      credential: 'secret',
      fetchImpl: (async (url: string | URL, init?: RequestInit) => {
        seen.push({ url: String(url), body: JSON.parse(String(init?.body)) });
        return new Response(JSON.stringify({
          output: [{ type: 'message', role: 'assistant', content: [{ type: 'output_text', text: 'catalog' }] }],
        }), { status: 200 });
      }) as typeof fetch,
    });

    await transport({ messages: [{ role: 'user', content: 'Say catalog.' }], max_tokens: 512, temperature: 0 }, 'secret');

    assert.match(seen[0].url, /\/responses$/);
    assert.equal(seen[0].body.model, 'grok-4.5');
    assert.equal(seen[0].body.max_output_tokens, 512, 'max_tokens has a different name here');
    assert.equal(seen[0].body.max_tokens, undefined, 'and the chat name must not be sent');
    assert.deepEqual(seen[0].body.input, [{ role: 'user', content: [{ type: 'input_text', text: 'Say catalog.' }] }]);
  });

  test('hands the graders the shape they already read', async () => {
    // The graders read choices[0].message.content. Normalising here is what keeps
    // every one of them from having to learn a second wire format — and a grader
    // that silently reads nothing is exactly how a whole paid run scored zero.
    const transport = createEvaluationTransport({
      providerId: 'opencode-go', modelId: 'grok-4.5', protocol: 'responses', credential: 'secret',
      fetchImpl: (async () => new Response(JSON.stringify({
        output: [
          { type: 'reasoning', content: [{ type: 'reasoning_text', text: 'thinking' }] },
          { type: 'message', role: 'assistant', content: [{ type: 'output_text', text: '{"recordId": 1}' }] },
        ],
      }), { status: 200 })) as typeof fetch,
    });

    const outcome = await transport({ messages: [{ role: 'user', content: 'x' }] }, 'secret');
    assert.equal(outcome.kind, 'success');
    const body = outcome.kind === 'success' ? outcome.response.body as any : null;
    assert.equal(body.choices[0].message.content, '{"recordId": 1}');
    assert.equal(body.choices[0].message.reasoning, 'thinking', 'thinking is kept, and kept separate');
  });

  test('carries an image the way this API names it', async () => {
    const seen: Record<string, unknown>[] = [];
    const transport = createEvaluationTransport({
      providerId: 'opencode-go', modelId: 'grok-4.5', protocol: 'responses', credential: 'secret',
      fetchImpl: (async (_url: string | URL, init?: RequestInit) => {
        seen.push(JSON.parse(String(init?.body)));
        return new Response(JSON.stringify({ output: [] }), { status: 200 });
      }) as typeof fetch,
    });

    await transport({
      messages: [{ role: 'user', content: [
        { type: 'text', text: 'what digit' },
        { type: 'image_url', image_url: { url: 'data:image/png;base64,AAA' } },
      ] }],
    }, 'secret');

    assert.deepEqual((seen[0].input as any[])[0].content, [
      { type: 'input_text', text: 'what digit' },
      { type: 'input_image', image_url: 'data:image/png;base64,AAA' },
    ]);
  });

  test('surfaces a tool call where the tool-calling grader looks for it', async () => {
    const transport = createEvaluationTransport({
      providerId: 'opencode-go', modelId: 'grok-4.5', protocol: 'responses', credential: 'secret',
      fetchImpl: (async () => new Response(JSON.stringify({
        output: [{ type: 'function_call', call_id: 'call-1', name: 'get_weather', arguments: '{"city":"Cairo"}' }],
      }), { status: 200 })) as typeof fetch,
    });

    const outcome = await transport({ messages: [{ role: 'user', content: 'x' }] }, 'secret');
    const body = outcome.kind === 'success' ? outcome.response.body as any : null;
    assert.deepEqual(body.choices[0].message.tool_calls, [
      { id: 'call-1', type: 'function', function: { name: 'get_weather', arguments: '{"city":"Cairo"}' } },
    ]);
  });
});

describe('choosing the protocol a model is actually served on', () => {
  test('routes the Responses-API families to /responses', () => {
    for (const modelId of ['grok-4.5', 'gpt-5.6-luna', 'muse-spark-1.2-contributor']) {
      assert.equal(protocolFor('opencode-go', modelId), 'responses', modelId);
    }
  });

  test('leaves the chat-completions models alone', () => {
    for (const modelId of ['glm-5.3', 'kimi-k3', 'minimax-m3', 'deepseek-v4-pro', 'qwen3.7-max']) {
      assert.equal(protocolFor('opencode-go', modelId), 'chat-completions', modelId);
    }
  });

  test('does not apply OpenCode routing to other providers', () => {
    // clinepass prefixes its ids; a `gpt-` model there is still its own gateway.
    assert.equal(protocolFor('clinepass', 'cline-pass/glm-5.3'), 'chat-completions');
    assert.equal(protocolFor('ollama-cloud', 'gpt-oss:120b'), 'chat-completions');
  });
});

describe('translating a tool definition for the Responses API', () => {
  test('flattens the chat tool shape, which nests what this API keeps at the top', async () => {
    const seen: Record<string, unknown>[] = [];
    const transport = createEvaluationTransport({
      providerId: 'opencode-go', modelId: 'grok-4.5', protocol: 'responses', credential: 'secret',
      fetchImpl: (async (_url: string | URL, init?: RequestInit) => {
        seen.push(JSON.parse(String(init?.body)));
        return new Response(JSON.stringify({ output: [] }), { status: 200 });
      }) as typeof fetch,
    });

    await transport({
      messages: [{ role: 'user', content: 'weather in Cairo' }],
      tools: [{
        type: 'function',
        function: {
          name: 'get_weather',
          description: 'Get current weather for a city.',
          parameters: { type: 'object', properties: { city: { type: 'string' } }, required: ['city'] },
        },
      }],
    }, 'secret');

    assert.deepEqual(seen[0].tools, [{
      type: 'function',
      name: 'get_weather',
      description: 'Get current weather for a city.',
      parameters: { type: 'object', properties: { city: { type: 'string' } }, required: ['city'] },
    }]);
  });

  test('gives Muse enough output budget to finish reasoning before its answer', async () => {
    const seen: Record<string, unknown>[] = [];
    const transport = createEvaluationTransport({
      providerId: 'opencode-zen', modelId: 'muse-spark-1.2-contributor-free', credential: 'secret',
      fetchImpl: (async (_url: string | URL, init?: RequestInit) => {
        seen.push(JSON.parse(String(init?.body)));
        return new Response(JSON.stringify({
          status: 'completed',
          output: [{ type: 'message', role: 'assistant', content: [{ type: 'output_text', text: 'ok' }] }],
        }), { status: 200 });
      }) as typeof fetch,
    });

    await transport({ messages: [{ role: 'user', content: 'x' }], max_tokens: 512 }, 'secret');
    assert.equal(seen[0].max_output_tokens, 2048);
  });

  test('does not grade an incomplete Responses envelope with no answer', async () => {
    const transport = createEvaluationTransport({
      providerId: 'opencode-zen', modelId: 'muse-spark-1.2-contributor-free', credential: 'secret',
      fetchImpl: (async () => new Response(JSON.stringify({
        status: 'incomplete',
        incomplete_details: { reason: 'max_output_tokens' },
        output: [],
      }), { status: 200 })) as typeof fetch,
    });

    const outcome = await transport({ messages: [{ role: 'user', content: 'x' }], max_tokens: 512 }, 'secret');
    assert.equal(outcome.kind, 'provider_failure');
    if (outcome.kind !== 'provider_failure') throw new Error('expected provider failure');
    assert.equal(outcome.status, 503);
  });
});

/**
 * `missing_credentials` is decided here, from `process.env` alone. A key can be
 * perfectly present in `catalog/.env` and still be invisible — because nothing
 * loaded the file, or because its NAME carries a UTF-8 BOM that `node --env-file`
 * does not strip. Both produced the identical unactionable sentence in the UI,
 * so the report exists to tell them apart by name.
 */
describe('reporting which evaluation credentials this process can see', () => {
  const NAME = 'VENOM_CATALOG_OPENCODE_ZEN_API_KEY';
  const find = (report: ReturnType<typeof evaluationCredentialReport>, providerId: string) =>
    report.find((row) => row.providerId === providerId)!;

  test('every evaluable provider is reported, by variable name', () => {
    const report = evaluationCredentialReport({});
    assert.deepEqual(
      report.map((row) => row.providerId).sort(),
      ['clinepass', 'ollama-cloud', 'opencode-go', 'opencode-zen'],
    );
    assert.equal(find(report, 'opencode-zen').envName, NAME);
    assert.ok(report.every((row) => row.state === 'missing'));
  });

  test('a set variable reads as present', () => {
    assert.equal(find(evaluationCredentialReport({ [NAME]: 'DO_NOT_PRINT' }), 'opencode-zen').state, 'present');
  });

  test('a blank value is missing, not present', () => {
    assert.equal(find(evaluationCredentialReport({ [NAME]: '   ' }), 'opencode-zen').state, 'missing');
  });

  /**
   * The whole reason this function exists. PowerShell's `>` / `Out-File` /
   * `Set-Content` write a UTF-8 BOM, it binds to the FIRST variable name in the
   * env file, and the process then holds a key nothing asks for — while every
   * other line in the same file works perfectly.
   */
  test('a name corrupted by a BOM is named as corrupted, not reported missing', () => {
    const row = find(evaluationCredentialReport({ [`﻿${NAME}`]: 'DO_NOT_PRINT' }), 'opencode-zen');
    assert.equal(row.state, 'malformed_name');
    assert.equal(row.foundAs, `﻿${NAME}`);
  });

  test('a corrupted name with no value is still just missing', () => {
    assert.equal(find(evaluationCredentialReport({ [`﻿${NAME}`]: '' }), 'opencode-zen').state, 'missing');
  });

  test('no credential value reaches the report, in any state', () => {
    const secret = 'VENOM_CATALOG_SECRET_CANARY_VALUE';
    const serialized = JSON.stringify(evaluationCredentialReport({
      [NAME]: secret,
      [`﻿VENOM_CATALOG_CLINEPASS_API_KEY`]: secret,
      VENOM_CATALOG_OLLAMA_CLOUD_API_KEY: secret,
    }));
    assert.ok(!serialized.includes(secret), 'a diagnostic is exactly where a secret leaks by accident');
  });
});
