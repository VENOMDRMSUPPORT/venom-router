import assert from 'node:assert/strict';
import { describe, test } from 'node:test';
import {
  createRotatingSocksFetch,
  fetchForEvaluationProvider,
  parseSocksProxyList,
  resolveEvaluationProxyListUrl,
  type SocksRequest,
} from './proxy-pool.ts';

const LIST_URL = 'https://proxy-list.test/SECRET-CANARY';

describe('the evaluation SOCKS proxy pool', () => {
  test('normalizes a whitelist response and rejects malformed entries', () => {
    assert.deepEqual(parseSocksProxyList([
      '198.51.100.10:9999',
      '',
      'not a proxy',
      '198.51.100.11:1080',
      '198.51.100.10:9999',
    ].join('\n')), [
      'socks5h://198.51.100.10:9999',
      'socks5h://198.51.100.11:1080',
    ]);
  });

  test('uses a different gateway for consecutive provider requests', async () => {
    const used: string[] = [];
    const requestThroughProxy: SocksRequest = async (proxyUrl) => {
      used.push(proxyUrl);
      return new Response('ok', { status: 200 });
    };
    const providerFetch = createRotatingSocksFetch({
      listUrl: LIST_URL,
      fetchList: async () => new Response('198.51.100.10:9999\n198.51.100.11:9999'),
      requestThroughProxy,
    });

    assert.equal((await providerFetch('https://provider.test/one')).status, 200);
    assert.equal((await providerFetch('https://provider.test/two')).status, 200);
    assert.equal((await providerFetch('https://provider.test/three')).status, 200);
    assert.deepEqual(used, [
      'socks5h://198.51.100.10:9999',
      'socks5h://198.51.100.11:9999',
      'socks5h://198.51.100.10:9999',
    ]);
  });

  test('rotates past a dead gateway and a rate-limited exit before returning success', async () => {
    const used: string[] = [];
    const requestThroughProxy: SocksRequest = async (proxyUrl) => {
      used.push(proxyUrl);
      if (proxyUrl.endsWith('10:9999')) throw new Error('connect failed at a secret gateway');
      if (proxyUrl.endsWith('11:9999')) {
        return new Response(null, { status: 429, headers: { 'retry-after': '41922' } });
      }
      return new Response('{"ok":true}', { status: 200 });
    };
    const providerFetch = createRotatingSocksFetch({
      listUrl: LIST_URL,
      fetchList: async () => new Response([
        '198.51.100.10:9999',
        '198.51.100.11:9999',
        '198.51.100.12:9999',
      ].join('\n')),
      requestThroughProxy,
      maxProxyAttempts: 3,
    });

    const response = await providerFetch('https://provider.test/chat/completions', { method: 'POST' });
    assert.equal(response.status, 200);
    assert.deepEqual(used, [
      'socks5h://198.51.100.10:9999',
      'socks5h://198.51.100.11:9999',
      'socks5h://198.51.100.12:9999',
    ]);
  });

  test('rotates past an exit rejected by the provider before returning success', async () => {
    const used: string[] = [];
    const requestThroughProxy: SocksRequest = async (proxyUrl) => {
      used.push(proxyUrl);
      if (proxyUrl.endsWith('10:9999')) return new Response(null, { status: 403 });
      return new Response('{"ok":true}', { status: 200 });
    };
    const providerFetch = createRotatingSocksFetch({
      listUrl: LIST_URL,
      fetchList: async () => new Response('198.51.100.10:9999\n198.51.100.11:9999'),
      requestThroughProxy,
      maxProxyAttempts: 2,
    });

    const response = await providerFetch('https://provider.test/chat/completions', { method: 'POST' });
    assert.equal(response.status, 200);
    assert.deepEqual(used, [
      'socks5h://198.51.100.10:9999',
      'socks5h://198.51.100.11:9999',
    ]);
  });

  test('searches beyond the first rejected slice of the default pool', async () => {
    const used: string[] = [];
    const entries = Array.from({ length: 9 }, (_, index) => `198.51.100.${index + 10}:9999`);
    const requestThroughProxy: SocksRequest = async (proxyUrl) => {
      used.push(proxyUrl);
      return proxyUrl.endsWith('18:9999')
        ? new Response('{"ok":true}', { status: 200 })
        : new Response(null, { status: 403 });
    };
    const providerFetch = createRotatingSocksFetch({
      listUrl: LIST_URL,
      fetchList: async () => new Response(entries.join('\n')),
      requestThroughProxy,
    });

    const response = await providerFetch('https://provider.test/chat/completions');
    assert.equal(response.status, 200);
    assert.equal(used.length, 9);
  });

  test('returns the final 429 so the existing retry-after policy ends the round', async () => {
    const providerFetch = createRotatingSocksFetch({
      listUrl: LIST_URL,
      fetchList: async () => new Response('198.51.100.10:9999\n198.51.100.11:9999'),
      requestThroughProxy: async () => new Response(null, {
        status: 429,
        headers: { 'retry-after': '41922' },
      }),
      maxProxyAttempts: 2,
    });

    const response = await providerFetch('https://provider.test/chat/completions');
    assert.equal(response.status, 429);
    assert.equal(response.headers.get('retry-after'), '41922');
  });

  test('keeps a remembered 429 readable after later gateways fail', async () => {
    let attempt = 0;
    const providerFetch = createRotatingSocksFetch({
      listUrl: LIST_URL,
      fetchList: async () => new Response([
        '198.51.100.10:9999',
        '198.51.100.11:9999',
        '198.51.100.12:9999',
      ].join('\n')),
      requestThroughProxy: async () => {
        attempt += 1;
        if (attempt === 1) return new Response('rate limited', {
          status: 429,
          headers: { 'retry-after': '41922' },
        });
        throw new Error('dead exit');
      },
      maxProxyAttempts: 3,
    });

    const response = await providerFetch('https://provider.test/chat/completions');
    assert.equal(response.status, 429);
    await assert.doesNotReject(() => response.text());
  });

  /**
   * An undrained body is a live socket. The response handed back on a rejected
   * attempt is never the one returned to the caller, and `requestThroughSocksProxy`
   * destroys its agent on the response stream's `close` — so a body nobody reads
   * or cancels keeps the SOCKS tunnel open for the rest of the process. The last
   * attempt used to be exempt, which is exactly the attempt a degraded pool
   * always reaches.
   */
  test('drains every rejected response, the last attempt included', async () => {
    const handed: Response[] = [];
    const providerFetch = createRotatingSocksFetch({
      listUrl: LIST_URL,
      fetchList: async () => new Response('198.51.100.10:9999\n198.51.100.11:9999'),
      requestThroughProxy: async () => {
        const response = new Response('forbidden', { status: 403 });
        handed.push(response);
        return response;
      },
      maxProxyAttempts: 2,
    });

    const response = await providerFetch('https://provider.test/chat/completions');
    assert.equal(response.status, 503);
    assert.equal(handed.length, 2);
    for (const [index, rejected] of handed.entries()) {
      assert.equal(rejected.bodyUsed, true, `attempt ${index + 1} left its body undrained`);
    }
  });

  /**
   * The pool cannot be refreshed mid-round. Rotating further is pointless, but
   * the 429 this round already collected carries the provider's own retry-after
   * and outranks a bare list failure — throwing it away turned a governed
   * back-off into an error code the runtime read as permanent.
   */
  test('reports a remembered 429 when the pool can no longer be refreshed', async () => {
    let listCalls = 0;
    const providerFetch = createRotatingSocksFetch({
      listUrl: LIST_URL,
      fetchList: async () => {
        listCalls += 1;
        if (listCalls === 1) return new Response('198.51.100.10:9999');
        throw new Error('list host unreachable');
      },
      requestThroughProxy: async () => new Response('rate limited', {
        status: 429,
        headers: { 'retry-after': '41922' },
      }),
      maxProxyAttempts: 3,
    });

    const response = await providerFetch('https://provider.test/chat/completions');
    assert.equal(response.status, 429);
    assert.equal(response.headers.get('retry-after'), '41922');
    assert.equal(listCalls, 2, 'the round stops at the first refresh it cannot complete');
  });

  test('a list failure with nothing collected is still reported as such', async () => {
    const providerFetch = createRotatingSocksFetch({
      listUrl: LIST_URL,
      fetchList: async () => { throw new Error('list host unreachable'); },
      requestThroughProxy: async () => new Response(null, { status: 200 }),
      maxProxyAttempts: 3,
    });

    await assert.rejects(
      () => providerFetch('https://provider.test/chat/completions'),
      (error: Error) => {
        assert.equal(error.message, 'proxy_list_unavailable');
        assert.ok(!error.message.includes('SECRET-CANARY'), 'the list URL is a credential');
        return true;
      },
    );
  });

  test('reports exhausted rejected exits as a transient proxy failure', async () => {
    const providerFetch = createRotatingSocksFetch({
      listUrl: LIST_URL,
      fetchList: async () => new Response('198.51.100.10:9999\n198.51.100.11:9999'),
      requestThroughProxy: async () => new Response(null, { status: 403 }),
      maxProxyAttempts: 2,
    });

    const response = await providerFetch('https://provider.test/chat/completions');
    assert.equal(response.status, 503);
  });

  test('does not rotate after the caller aborts the evaluation request', async () => {
    const controller = new AbortController();
    let attempts = 0;
    const providerFetch = createRotatingSocksFetch({
      listUrl: LIST_URL,
      fetchList: async () => new Response('198.51.100.10:9999\n198.51.100.11:9999'),
      requestThroughProxy: async () => {
        attempts += 1;
        controller.abort();
        throw new DOMException('evaluation stopped', 'AbortError');
      },
      maxProxyAttempts: 2,
    });

    await assert.rejects(
      providerFetch('https://provider.test/chat/completions', { signal: controller.signal }),
      (error: Error) => error.name === 'AbortError',
    );
    assert.equal(attempts, 1);
  });

  test('never includes the secret list URL in a load failure', async () => {
    const providerFetch = createRotatingSocksFetch({
      listUrl: LIST_URL,
      fetchList: async () => { throw new Error(`cannot fetch ${LIST_URL}`); },
      requestThroughProxy: async () => new Response('unused'),
    });

    await assert.rejects(
      providerFetch('https://provider.test/chat/completions'),
      (error: Error) => error.message === 'proxy_list_unavailable' && !error.message.includes('SECRET-CANARY'),
    );
  });
});

describe('provider proxy configuration', () => {
  test('is opt-in for OpenCode Zen and cannot affect another provider', () => {
    const env = { VENOM_CATALOG_OPENCODE_ZEN_PROXY_LIST_URL: LIST_URL };
    assert.equal(resolveEvaluationProxyListUrl('opencode-zen', env), LIST_URL);
    assert.equal(resolveEvaluationProxyListUrl('opencode-go', env), null);
    assert.equal(resolveEvaluationProxyListUrl('clinepass', env), null);
  });

  test('builds a rotating fetch only for a configured OpenCode Zen process', async () => {
    const directCalls: string[] = [];
    const proxyCalls: string[] = [];
    const directFetch = (async (input: string | URL | Request) => {
      directCalls.push(String(input));
      return new Response('direct');
    }) as typeof fetch;
    const options = {
      env: { VENOM_CATALOG_OPENCODE_ZEN_PROXY_LIST_URL: LIST_URL },
      directFetch,
      fetchList: async () => new Response('198.51.100.10:9999'),
      requestThroughProxy: (async (proxyUrl: string) => {
        proxyCalls.push(proxyUrl);
        return new Response('proxied');
      }) as SocksRequest,
    };

    const zenFetch = fetchForEvaluationProvider('opencode-zen', options);
    const goFetch = fetchForEvaluationProvider('opencode-go', options);
    assert.equal(await (await zenFetch('https://provider.test/zen')).text(), 'proxied');
    assert.equal(await (await goFetch('https://provider.test/go')).text(), 'direct');
    assert.deepEqual(proxyCalls, ['socks5h://198.51.100.10:9999']);
    assert.deepEqual(directCalls, ['https://provider.test/go']);
  });
});
