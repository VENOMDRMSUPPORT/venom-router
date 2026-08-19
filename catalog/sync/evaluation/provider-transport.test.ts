import assert from 'node:assert/strict';
import { describe, test } from 'node:test';
import { createEvaluationTransport, evaluationHeaders, resolveEvaluationCredential } from './provider-transport.ts';

describe('provider evaluation transport', () => {
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
