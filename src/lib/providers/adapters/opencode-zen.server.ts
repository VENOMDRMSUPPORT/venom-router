/* OpenCode Zen — OpenAI-compatible API key adapter. Server-only. */
import type { StoredCredentials, AccountIdentity, DiscoveredModel, ModelTestResult, ChatRequest, ChatResult } from "./types";

const BASE = "https://opencode.ai/zen";

export async function validateApiKey(
  api_key: string,
): Promise<{ ok: true } | { ok: false; error: string }> {
  try {
    const r = await fetch(`${BASE}/v1/models`, {
      headers: { Authorization: `Bearer ${api_key}` },
    });
    if (!r.ok) return { ok: false, error: `${r.status} ${r.statusText}` };
    return { ok: true };
  } catch (e: any) {
    return { ok: false, error: String(e?.message ?? e) };
  }
}

export function buildCredentials(api_key: string, label?: string): StoredCredentials {
  return {
    kind: "api_key",
    api_key,
    extra: label ? { label } : undefined,
  };
}

export async function fetchIdentity(
  creds: StoredCredentials,
): Promise<{ creds: StoredCredentials; identity: AccountIdentity }> {
  // OpenCode Zen has no documented account/quota endpoint — just confirm the key works.
  const v = await validateApiKey(creds.api_key ?? "");
  const identity: AccountIdentity = {
    email: null,
    plan: v.ok ? "Free" : "Invalid key",
    quota_used: null,
    quota_total: null,
    quota_unit: null,
  };
  return { creds, identity };
}

export async function listModels(creds: StoredCredentials): Promise<DiscoveredModel[]> {
  try {
    const r = await fetch(`${BASE}/v1/models`, {
      headers: { Authorization: `Bearer ${creds.api_key}` },
    });
    if (!r.ok) return [];
    const j: any = await r.json();
    const data = j?.data ?? [];
    return data.map((m: any) => ({
      external_id: m.id,
      display_name: m.id,
      capabilities: ["chat"],
    }));
  } catch {
    return [];
  }
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
        max_tokens: 8,
        messages: [{ role: "user", content: "ping" }],
      }),
    });
    if (!r.ok) {
      const text = await r.text();
      return { external_id, ok: false, latency_ms: Date.now() - t0, error: text.slice(0, 200) };
    }
    return { external_id, ok: true, latency_ms: Date.now() - t0 };
  } catch (e: any) {
    return { external_id, ok: false, latency_ms: Date.now() - t0, error: String(e?.message ?? e) };
  }
}

export async function chat(
  creds: StoredCredentials,
  externalId: string,
  req: ChatRequest,
): Promise<ChatResult> {
  const t0 = Date.now();
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
    });
    if (!r.ok) {
      const text = await r.text();
      return { ok: false, inputTokens: 0, outputTokens: 0, error: text.slice(0, 300) };
    }
    const j: any = await r.json();
    const content = j?.choices?.[0]?.message?.content ?? "";
    const inputTokens = j?.usage?.prompt_tokens ?? 0;
    const outputTokens = j?.usage?.completion_tokens ?? 0;
    return { ok: true, content, inputTokens, outputTokens };
  } catch (e: any) {
    return {
      ok: false,
      inputTokens: 0,
      outputTokens: 0,
      error: String(e?.message ?? e).slice(0, 300),
    };
  }
}
