import { extractOpenAiMessageText, MODEL_TEST_MAX_TOKENS } from "./_shared/openai-chat.server";
import {
  MODEL_TEST_PROMPT,
  validateModelTestResponse,
} from "./_shared/model-test-validation.server";
import {
  buildOpenCodeZenFreeCatalog,
  type OpenCodeZenCatalogEntry,
} from "@/lib/providers/opencode-zen-snapshot";
import type {
  StoredCredentials,
  AccountIdentity,
  DiscoveredModel,
  ModelTestResult,
  ChatRequest,
  ChatResult,
} from "./types";

const BASE = "https://opencode.ai/zen";
const MODELS_DEV_URL = "https://models.dev/api.json";
const CATALOG_CACHE_TTL_MS = 10 * 60 * 1000;

export interface AccountHealthResult {
  ok: boolean;
  latency_ms: number;
  error?: string;
}

export interface SyncOpenCodeZenResult {
  creds: StoredCredentials;
  identity: AccountIdentity;
  models: DiscoveredModel[];
  health: AccountHealthResult;
  provider_calls: string[];
  stats: { rawFetched: number; freeVisible: number };
}

let catalogCache: {
  catalog: Record<string, OpenCodeZenCatalogEntry>;
  expiresAt: number;
} | null = null;

async function fetchModelsDevCatalog(): Promise<Record<string, OpenCodeZenCatalogEntry>> {
  const cached = catalogCache;
  if (cached && Date.now() < cached.expiresAt) return cached.catalog;

  const r = await fetch(MODELS_DEV_URL, { signal: AbortSignal.timeout(15_000) });
  if (!r.ok) throw new Error(`models.dev catalog fetch failed: ${r.status}`);
  const j = (await r.json()) as { opencode?: { models?: Record<string, OpenCodeZenCatalogEntry> } };
  const catalog = j?.opencode?.models ?? {};
  catalogCache = { catalog, expiresAt: Date.now() + CATALOG_CACHE_TTL_MS };
  return catalog;
}

async function fetchZenModelsRaw(
  apiKey: string,
): Promise<{ ok: boolean; error?: string; latency_ms: number; liveIds: string[] }> {
  const t0 = Date.now();
  try {
    const r = await fetch(`${BASE}/v1/models`, {
      headers: { Authorization: `Bearer ${apiKey}` },
      signal: AbortSignal.timeout(8_000),
    });
    const latency_ms = Date.now() - t0;
    if (!r.ok) {
      const text = await r.text().catch(() => "");
      return {
        ok: false,
        error: `${r.status} ${text.slice(0, 120) || r.statusText}`,
        latency_ms,
        liveIds: [],
      };
    }
    const j = (await r.json()) as { data?: Array<{ id?: string }> };
    const liveIds = (j?.data ?? [])
      .map((m) => m.id)
      .filter((id): id is string => typeof id === "string" && id.length > 0);
    return { ok: true, latency_ms, liveIds };
  } catch (e: unknown) {
    return {
      ok: false,
      error: String((e as { message?: string } | null)?.message ?? e),
      latency_ms: Date.now() - t0,
      liveIds: [],
    };
  }
}

export async function checkAccountHealth(creds: StoredCredentials): Promise<AccountHealthResult> {
  const apiKey = creds.api_key ?? "";
  if (!apiKey) return { ok: false, latency_ms: 0, error: "No API key" };

  const t0 = Date.now();
  try {
    const r = await fetch(`${BASE}/v1/models`, {
      headers: { Authorization: `Bearer ${apiKey}` },
      signal: AbortSignal.timeout(8_000),
    });
    const latency_ms = Date.now() - t0;
    if (!r.ok) {
      const text = await r.text().catch(() => "");
      return {
        ok: false,
        latency_ms,
        error: `${r.status} ${text.slice(0, 120) || r.statusText}`,
      };
    }
    await r.body?.cancel();
    return { ok: true, latency_ms };
  } catch (e: unknown) {
    return {
      ok: false,
      latency_ms: Date.now() - t0,
      error: String((e as { message?: string } | null)?.message ?? e),
    };
  }
}

export async function syncOpenCodeZenAccount(
  creds: StoredCredentials,
): Promise<SyncOpenCodeZenResult> {
  const provider_calls = ["fetchZenModelsRaw", "fetchModelsDevCatalog"];
  const raw = await fetchZenModelsRaw(creds.api_key ?? "");
  const health: AccountHealthResult = {
    ok: raw.ok,
    latency_ms: raw.latency_ms,
    error: raw.error,
  };
  if (!raw.ok) {
    throw Object.assign(new Error(raw.error ?? "OpenCode Zen health check failed"), { health });
  }

  const catalog = await fetchModelsDevCatalog();
  const free = buildOpenCodeZenFreeCatalog(raw.liveIds, catalog);
  const models: DiscoveredModel[] = free.map((m) => ({
    external_id: m.id,
    display_name: m.displayName,
    capabilities: [
      "chat",
      "tools",
      ...(m.reasoning ? ["reasoning"] : []),
      ...(m.inputModalities?.includes("image") ? ["vision"] : []),
    ],
    context_window: m.contextWindow,
  }));

  const identity: AccountIdentity = {
    email: null,
    plan: "Free",
    quota_used: null,
    quota_total: null,
    quota_unit: null,
  };

  return {
    creds,
    identity,
    models,
    health,
    provider_calls,
    stats: { rawFetched: raw.liveIds.length, freeVisible: free.length },
  };
}

export async function validateApiKey(
  api_key: string,
): Promise<{ ok: true } | { ok: false; error: string }> {
  const r = await checkAccountHealth({ kind: "api_key", api_key });
  return r.ok ? { ok: true } : { ok: false, error: r.error ?? "Invalid API key" };
}

export function buildCredentials(api_key: string, label?: string): StoredCredentials {
  return {
    kind: "api_key",
    api_key,
    extra: label ? { label } : undefined,
  };
}

export async function fetchIdentity(creds: StoredCredentials): Promise<{
  creds: StoredCredentials;
  identity: AccountIdentity;
  health: { ok: boolean; error?: string };
}> {
  const r = await syncOpenCodeZenAccount(creds);
  return {
    creds: r.creds,
    identity: r.identity,
    health: { ok: r.health.ok, error: r.health.error },
  };
}

export async function listModels(creds: StoredCredentials): Promise<DiscoveredModel[]> {
  const r = await syncOpenCodeZenAccount(creds);
  return r.models;
}

export async function testModel(
  creds: StoredCredentials,
  external_id: string,
): Promise<ModelTestResult> {
  const t0 = Date.now();
  try {
    const r = await fetch(`${BASE}/v1/chat/completions`, {
      method: "POST",
      headers: {
        Authorization: `Bearer ${creds.api_key}`,
        "Content-Type": "application/json",
      },
      body: JSON.stringify({
        model: external_id,
        max_tokens: MODEL_TEST_MAX_TOKENS,
        messages: [{ role: "user", content: MODEL_TEST_PROMPT }],
      }),
      signal: AbortSignal.timeout(30_000),
    });
    const text = await r.text();
    if (!r.ok) {
      return { external_id, ok: false, latency_ms: Date.now() - t0, error: text.slice(0, 200) };
    }
    let content = "";
    try {
      const j = JSON.parse(text) as {
        choices?: Array<{ message?: Parameters<typeof extractOpenAiMessageText>[0] }>;
      };
      content = extractOpenAiMessageText(j.choices?.[0]?.message, {
        includeReasoningFallback: true,
      });
    } catch {
      content = text;
    }
    const validation = validateModelTestResponse(content);
    if (!validation.ok) {
      return {
        external_id,
        ok: false,
        latency_ms: Date.now() - t0,
        error: validation.error,
      };
    }
    return { external_id, ok: true, latency_ms: Date.now() - t0 };
  } catch (e: unknown) {
    return {
      external_id,
      ok: false,
      latency_ms: Date.now() - t0,
      error: String((e as { message?: string } | null)?.message ?? e),
    };
  }
}

export async function chat(
  creds: StoredCredentials,
  externalId: string,
  req: ChatRequest,
): Promise<ChatResult> {
  try {
    const r = await fetch(`${BASE}/v1/chat/completions`, {
      method: "POST",
      headers: {
        Authorization: `Bearer ${creds.api_key}`,
        "Content-Type": "application/json",
      },
      body: JSON.stringify({
        model: externalId,
        messages: req.messages,
        max_tokens: req.maxTokens ?? 1024,
        temperature: req.temperature ?? 0.7,
        stream: false,
      }),
      signal: AbortSignal.timeout(120_000),
    });
    if (!r.ok) {
      const text = await r.text();
      return { ok: false, inputTokens: 0, outputTokens: 0, error: text.slice(0, 300) };
    }
    const j = (await r.json()) as {
      choices?: Array<{ message?: Parameters<typeof extractOpenAiMessageText>[0] }>;
      usage?: { prompt_tokens?: number; completion_tokens?: number };
    };
    const content = extractOpenAiMessageText(j?.choices?.[0]?.message);
    const inputTokens = j?.usage?.prompt_tokens ?? 0;
    const outputTokens = j?.usage?.completion_tokens ?? 0;
    return { ok: true, content, inputTokens, outputTokens };
  } catch (e: unknown) {
    return {
      ok: false,
      inputTokens: 0,
      outputTokens: 0,
      error: String((e as { message?: string } | null)?.message ?? e).slice(0, 300),
    };
  }
}

/** @internal test helper — reset in-memory models.dev cache */
export function _resetCatalogCacheForTests(): void {
  catalogCache = null;
}
