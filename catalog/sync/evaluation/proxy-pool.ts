import { request as httpsRequest } from 'node:https';
import { Readable } from 'node:stream';
import { SocksProxyAgent } from 'socks-proxy-agent';

const PROXY_LIST_ENV: Record<string, string> = {
  'opencode-zen': 'VENOM_CATALOG_OPENCODE_ZEN_PROXY_LIST_URL',
};

// A public whitelist can contain a large share of stale or provider-rejected
// exits. Eight attempts made a single dead batch look like a model failure even
// when the next entries in the same 100-entry list were healthy. Keep the
// bound finite, but search a meaningful slice of the refreshed pool.
const DEFAULT_PROXY_ATTEMPTS = 32;
const PROXY_LIST_TIMEOUT_MS = 30_000;
/**
 * Measured against the paid whitelist this rotation serves (2026-08-23): of 14
 * fresh exits, 7 completed SOCKS+TLS, and every one of those 7 was under 5s —
 * but only 3 were under 2s. The former 2s bound therefore discarded about half
 * the healthy pool as "dead", each discard costing a full attempt, until a bad
 * draw churned past the caller's request timeout and the whole sample failed.
 * 5s covers every exit observed to work; a truly dead exit still costs at most
 * one bounded attempt instead of stalling to the outer timeout.
 */
const SOCKS_CONNECT_TIMEOUT_MS = 5_000;

export type SocksRequest = (
  proxyUrl: string,
  input: string | URL,
  init?: RequestInit,
) => Promise<Response>;

export interface RotatingSocksFetchOptions {
  listUrl: string;
  fetchList?: typeof fetch;
  requestThroughProxy?: SocksRequest;
  maxProxyAttempts?: number;
}

export interface EvaluationProviderFetchOptions {
  env?: Record<string, string | undefined>;
  directFetch?: typeof fetch;
  fetchList?: typeof fetch;
  requestThroughProxy?: SocksRequest;
  maxProxyAttempts?: number;
}

/** The variable a provider's rotation is configured through, if it has one. */
export function proxyListEnvName(providerId: string): string | null {
  return PROXY_LIST_ENV[providerId] ?? null;
}

/** The list URL is a credential. Only its environment-variable name is public. */
export function resolveEvaluationProxyListUrl(
  providerId: string,
  env: Record<string, string | undefined> = process.env,
): string | null {
  const name = PROXY_LIST_ENV[providerId];
  if (!name) return null;
  const value = env[name];
  return value?.trim() ? value.trim() : null;
}

function normalizeProxyEntry(value: string): string | null {
  const entry = value.trim();
  if (!entry || /\s/.test(entry)) return null;
  try {
    const parsed = new URL(entry.includes('://') ? entry : `socks5h://${entry}`);
    if (!parsed.hostname || !parsed.port) return null;
    const port = Number(parsed.port);
    if (!Number.isInteger(port) || port < 1 || port > 65_535) return null;
    const auth = parsed.username
      ? `${parsed.username}${parsed.password ? `:${parsed.password}` : ''}@`
      : '';
    return `socks5h://${auth}${parsed.hostname}:${port}`;
  } catch {
    return null;
  }
}

export function parseSocksProxyList(raw: string): string[] {
  const unique = new Set<string>();
  for (const line of raw.split(/\r?\n/)) {
    const proxy = normalizeProxyEntry(line);
    if (proxy) unique.add(proxy);
  }
  return [...unique];
}

function requestBody(init?: RequestInit): string | Uint8Array | undefined {
  const body = init?.body;
  if (body === null || body === undefined) return undefined;
  if (typeof body === 'string') return body;
  if (body instanceof URLSearchParams) return body.toString();
  if (body instanceof Uint8Array) return body;
  if (body instanceof ArrayBuffer) return new Uint8Array(body);
  throw new Error('unsupported_proxy_request_body');
}

/**
 * A fresh agent per request deliberately prevents connection reuse from pinning
 * several evaluation samples to the same residential exit.
 */
export const requestThroughSocksProxy: SocksRequest = (proxyUrl, input, init = {}) =>
  new Promise<Response>((resolve, reject) => {
    const agent = new SocksProxyAgent(proxyUrl);
    const connectController = new AbortController();
    const signal = init.signal
      ? AbortSignal.any([init.signal, connectController.signal])
      : connectController.signal;
    const connectTimer = setTimeout(
      () => connectController.abort(new Error('proxy_connect_timeout')),
      SOCKS_CONNECT_TIMEOUT_MS,
    );
    connectTimer.unref?.();
    const clearConnectTimer = () => clearTimeout(connectTimer);
    const headers = Object.fromEntries(new Headers(init.headers).entries());
    const req = httpsRequest(input, {
      method: init.method ?? 'GET',
      headers,
      agent,
      signal,
    }, (res) => {
      clearConnectTimer();
      const status = res.statusCode ?? 502;
      const responseHeaders = new Headers();
      for (const [name, value] of Object.entries(res.headers)) {
        if (Array.isArray(value)) for (const item of value) responseHeaders.append(name, item);
        else if (value !== undefined) responseHeaders.set(name, String(value));
      }
      const hasBody = init.method !== 'HEAD' && status !== 204 && status !== 205 && status !== 304;
      const body = hasBody ? Readable.toWeb(res) as ReadableStream<Uint8Array> : null;
      res.once('close', () => agent.destroy());
      resolve(new Response(body, { status, statusText: res.statusMessage, headers: responseHeaders }));
    });
    req.once('socket', (socket) => {
      // A SOCKS server accepting the tunnel is not enough: several pool members
      // accept it and then stall during TLS. Only a completed secure handshake
      // earns the model's full request timeout.
      socket.once('secureConnect', clearConnectTimer);
    });
    req.once('error', (error) => {
      clearConnectTimer();
      agent.destroy();
      reject(error);
    });
    try {
      req.end(requestBody(init));
    } catch (error) {
      clearConnectTimer();
      req.destroy();
      agent.destroy();
      reject(error);
    }
  });

export function createRotatingSocksFetch(options: RotatingSocksFetchOptions): typeof fetch {
  const fetchList = options.fetchList ?? fetch;
  const request = options.requestThroughProxy ?? requestThroughSocksProxy;
  const maxAttempts = Math.max(1, options.maxProxyAttempts ?? DEFAULT_PROXY_ATTEMPTS);
  let proxies: string[] = [];
  let cursor = 0;
  let loading: Promise<void> | null = null;

  const reload = async (): Promise<void> => {
    if (loading) return loading;
    loading = (async () => {
      try {
        const response = await fetchList(options.listUrl, {
          headers: { accept: 'text/plain' },
          signal: AbortSignal.timeout(PROXY_LIST_TIMEOUT_MS),
        });
        if (!response.ok) throw new Error('list rejected');
        const parsed = parseSocksProxyList(await response.text());
        if (parsed.length === 0) throw new Error('empty list');
        proxies = parsed;
        cursor = 0;
      } catch {
        throw new Error('proxy_list_unavailable');
      } finally {
        loading = null;
      }
    })();
    return loading;
  };

  const nextProxy = async (): Promise<string> => {
    if (proxies.length === 0 || cursor >= proxies.length) await reload();
    // Modulo rather than a bare index: two callers can resume from one reload,
    // and the second would read past the end of a single-entry pool and hand
    // back `undefined` for a proxy URL.
    return proxies[cursor++ % proxies.length];
  };

  const rotatingFetch = async (input: string | URL | Request, init?: RequestInit): Promise<Response> => {
    if (input instanceof Request) throw new Error('unsupported_proxy_request_input');
    let finalRateLimit: { status: number; statusText: string; headers: Headers } | null = null;
    let rejectedExit = false;
    /**
     * What this round established, for the moment it can no longer keep rotating.
     *
     * A remembered 429 carries the provider's own retry-after and a rejected
     * exit is transient provider state; either is a better answer than the
     * reason rotation stopped. Null means the round learned nothing.
     */
    const collected = (): Response | null => {
      if (finalRateLimit) return new Response(null, finalRateLimit);
      if (rejectedExit) return new Response(null, { status: 503 });
      return null;
    };
    for (let attempt = 1; attempt <= maxAttempts; attempt++) {
      let proxy: string;
      try {
        proxy = await nextProxy();
      } catch (error) {
        // The pool itself cannot be refreshed, so rotating further is pointless.
        // Reported as what the round already found, if it found anything —
        // discarding a 429 here threw away the provider's own retry-after.
        const learned = collected();
        if (learned) return learned;
        throw error;
      }
      try {
        const response = await request(proxy, input, init);
        if (response.status !== 403 && response.status !== 429) return response;
        if (response.status === 429) {
          finalRateLimit = {
            status: response.status,
            statusText: response.statusText,
            headers: new Headers(response.headers),
          };
        }
        else rejectedExit = true;
        // Drained on every attempt, including the last. None of the paths out of
        // this loop return THIS response, and an unread body keeps `res` open,
        // so the agent's `close` handler never runs and the tunnel socket
        // outlives the round.
        await response.body?.cancel();
      } catch (error) {
        if (init?.signal?.aborted) throw error;
        // A dead residential exit is expected pool churn. The next gateway is
        // tried without leaking its address or credentials into the error.
      }
    }
    const learned = collected();
    if (learned) return learned;
    throw new Error('proxy_pool_exhausted');
  };

  return rotatingFetch as typeof fetch;
}

const providerFetches = new Map<string, { listUrl: string; fetchImpl: typeof fetch }>();

/** Shared across quality dimensions and speed probes for process-wide rotation. */
export function fetchForEvaluationProvider(
  providerId: string,
  options: EvaluationProviderFetchOptions = {},
): typeof fetch {
  const directFetch = options.directFetch ?? fetch;
  const listUrl = resolveEvaluationProxyListUrl(providerId, options.env ?? process.env);
  if (!listUrl) return directFetch;
  const hasOverrides = options.env !== undefined
    || options.directFetch !== undefined
    || options.fetchList !== undefined
    || options.requestThroughProxy !== undefined
    || options.maxProxyAttempts !== undefined;
  if (hasOverrides) {
    return createRotatingSocksFetch({
      listUrl,
      fetchList: options.fetchList ?? directFetch,
      requestThroughProxy: options.requestThroughProxy,
      maxProxyAttempts: options.maxProxyAttempts,
    });
  }
  const cached = providerFetches.get(providerId);
  if (cached?.listUrl === listUrl) return cached.fetchImpl;
  const fetchImpl = createRotatingSocksFetch({ listUrl });
  providerFetches.set(providerId, { listUrl, fetchImpl });
  return fetchImpl;
}
