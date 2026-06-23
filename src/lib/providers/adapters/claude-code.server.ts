/* Claude Code OAuth adapter (PKCE, JSON token exchange). Server-only. */
import type { StoredCredentials, AccountIdentity, DiscoveredModel, ModelTestResult, ChatRequest, ChatResult } from "./types";
import { CLAUDE_OAUTH } from "./_shared/oauth-clients.server";
import {
  MODEL_TEST_PROMPT,
  validateModelTestResponse,
} from "./_shared/model-test-validation.server";
import {
  buildOAuthAuthorizeUrl,
  generateCodeChallenge,
  generateCodeVerifier,
  generateState,
} from "./_shared/oauth-pkce.server";
import { fetchClaudeUsage } from "./_shared/claude-usage.server";

const API_VERSION = "2023-06-01";
const BETA_HEADER = "oauth-2025-04-20";
const CLAUDE_CODE_BETA =
  "claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14,context-management-2025-06-27,prompt-caching-scope-2026-01-05";
const CLAUDE_CODE_IDENTITY = "You are Claude Code, Anthropic's official CLI for Claude.";
const USER_AGENT = "claude-cli/2.1.92 (external, sdk-cli)";

export class ClaudeAuthError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "ClaudeAuthError";
  }
}

function isUnrecoverableRefreshError(text: string): boolean {
  const lower = text.toLowerCase();
  return (
    lower.includes("invalid_grant") ||
    lower.includes("invalid_request") ||
    lower.includes("refresh_token_expired") ||
    lower.includes("refresh_token_reused") ||
    lower.includes("refresh_token_invalidated")
  );
}

function oauthHeaders(token: string) {
  return {
    Authorization: `Bearer ${token}`,
    "anthropic-beta": BETA_HEADER,
    "anthropic-version": API_VERSION,
    Accept: "application/json",
    "User-Agent": USER_AGENT,
  };
}

export function startFlow(input: { redirect_uri: string }) {
  const code_verifier = generateCodeVerifier();
  const code_challenge = generateCodeChallenge(code_verifier);
  const state = generateState();
  const authorize_url = buildOAuthAuthorizeUrl(CLAUDE_OAUTH.authorizeUrl, {
    response_type: "code",
    client_id: CLAUDE_OAUTH.clientId,
    redirect_uri: input.redirect_uri,
    state,
    code: "true",
    scope: CLAUDE_OAUTH.scopes.join(" "),
    code_challenge,
    code_challenge_method: "S256",
  });
  return { authorize_url, redirect_uri: input.redirect_uri, code_verifier, state };
}

export async function completeFlow(input: {
  code: string;
  code_verifier: string;
  state: string;
  redirect_uri: string;
}): Promise<StoredCredentials> {
  const [authCode, codeState] = input.code.includes("#")
    ? input.code.split("#")
    : [input.code, ""];
  const tokenBody = {
    code: authCode,
    state: codeState || input.state,
    grant_type: "authorization_code",
    client_id: CLAUDE_OAUTH.clientId,
    redirect_uri: input.redirect_uri,
    code_verifier: input.code_verifier,
  };
  console.log("[claude-oauth] token exchange →", JSON.stringify(tokenBody));
  const res = await fetch(CLAUDE_OAUTH.tokenUrl, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Accept: "application/json",
    },
    body: JSON.stringify(tokenBody),
  });
  if (!res.ok) {
    const text = await res.text();
    console.log("[claude-oauth] token exchange ←", res.status, text.slice(0, 300));
    throw new Error(`Claude token exchange failed (${res.status}): ${text.slice(0, 300)}`);
  }
  const j: any = await res.json();
  if (!j.access_token) {
    throw new Error("Claude token exchange succeeded but no access_token in response");
  }
  return {
    kind: "oauth2",
    access_token: j.access_token,
    refresh_token: j.refresh_token,
    expires_at: Date.now() + Number(j.expires_in ?? 3600) * 1000,
    scope: j.scope,
  };
}

async function refreshIfNeeded(creds: StoredCredentials): Promise<StoredCredentials> {
  if (!creds.refresh_token) return creds;
  const lead = CLAUDE_OAUTH.refreshLeadMs;
  if (creds.expires_at && creds.expires_at - Date.now() > lead) return creds;
  const res = await fetch(CLAUDE_OAUTH.tokenUrl, {
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json" },
    body: JSON.stringify({
      grant_type: "refresh_token",
      refresh_token: creds.refresh_token,
      client_id: CLAUDE_OAUTH.clientId,
    }),
  });
  if (!res.ok) {
    const text = await res.text();
    console.log("[claude-oauth] token refresh ←", res.status, text.slice(0, 300));
    if (isUnrecoverableRefreshError(text)) {
      throw new ClaudeAuthError(`Claude token expired — re-login required (${res.status})`);
    }
    throw new Error(`Claude token refresh failed (${res.status}): ${text.slice(0, 300)}`);
  }
  const j: any = await res.json();
  return {
    ...creds,
    access_token: j.access_token ?? creds.access_token,
    refresh_token: j.refresh_token ?? creds.refresh_token,
    expires_at: Date.now() + Number(j.expires_in ?? 3600) * 1000,
  };
}

interface ClaudeProfile {
  email?: string;
  displayName?: string;
  accountId?: string;
  organizationId?: string;
  organizationType?: string;
  rateLimitTier?: string;
  plan?: string;
}

function formatPlan(
  orgType?: string,
  tier?: string,
  account?: { has_claude_pro?: boolean; has_claude_max?: boolean; uuid?: string },
): string | undefined {
  if (orgType === "claude_pro" || account?.has_claude_pro) return "Pro Plan";
  if (orgType === "claude_team") return "Team Plan";
  if (orgType === "claude_enterprise") return "Enterprise";
  if (orgType === "claude_max" || account?.has_claude_max) {
    if (tier && /20x/i.test(tier)) return "Max 20x";
    if (tier && /5x/i.test(tier)) return "Max 5x";
    return "Max Plan";
  }
  if (account?.uuid) return "Free";
  return undefined;
}

async function fetchProfileEndpoint(token: string): Promise<ClaudeProfile | null> {
  try {
    const r = await fetch("https://api.anthropic.com/api/oauth/profile", {
      headers: oauthHeaders(token),
      signal: AbortSignal.timeout(10_000),
    });
    if (!r.ok) return null;
    const d: any = await r.json();
    const acc = d.account ?? {};
    const org = d.organization ?? {};
    return {
      email: acc.email,
      displayName: acc.display_name,
      accountId: acc.uuid,
      organizationId: org.uuid,
      organizationType: org.organization_type,
      rateLimitTier: org.rate_limit_tier,
      plan: formatPlan(org.organization_type, org.rate_limit_tier, acc),
    };
  } catch {
    return null;
  }
}

async function fetchUserinfoFallback(token: string): Promise<ClaudeProfile | null> {
  try {
    const r = await fetch("https://api.anthropic.com/v1/oauth/userinfo", {
      headers: oauthHeaders(token),
      signal: AbortSignal.timeout(10_000),
    });
    if (!r.ok) return null;
    const d: any = await r.json();
    const acc = d.oauthAccount;
    if (!acc) return null;
    return {
      email: acc.emailAddress,
      displayName: acc.displayName,
      accountId: acc.accountUuid,
      organizationId: acc.organizationUuid,
    };
  } catch {
    return null;
  }
}

function parseJwtClaims(token: string): { email?: string; plan?: string } {
  try {
    const [, payload] = token.split(".");
    if (!payload) return {};
    const claims = JSON.parse(Buffer.from(payload, "base64").toString("utf8"));
    return {
      email: claims.email ?? claims.sub_email,
      plan: claims.subscriptionType ?? claims.plan,
    };
  } catch {
    return {};
  }
}

async function resolveProfile(token: string): Promise<ClaudeProfile> {
  const primary = await fetchProfileEndpoint(token);
  if (primary?.email) return primary;
  const fallback = await fetchUserinfoFallback(token);
  const jwt = parseJwtClaims(token);
  return {
    ...(fallback ?? {}),
    ...(primary ?? {}),
    email: primary?.email ?? fallback?.email ?? jwt.email,
    plan: primary?.plan ?? jwt.plan ?? "Free",
  };
}

export async function fetchIdentity(
  credsIn: StoredCredentials,
): Promise<{
  creds: StoredCredentials;
  identity: AccountIdentity;
  health: { ok: boolean; error?: string };
}> {
  const creds = await refreshIfNeeded(credsIn);
  const token = creds.access_token;
  if (!token) {
    throw new ClaudeAuthError("No access token — re-login required");
  }

  const [profile, usage] = await Promise.all([resolveProfile(token), fetchClaudeUsage(token)]);

  const sessionQuota = usage.quotas["session (5h)"];
  let quota_used: number | null = null;
  let quota_total: number | null = null;
  let quota_unit: string | null = null;
  if (sessionQuota) {
    quota_used = Math.round(sessionQuota.used);
    quota_total = sessionQuota.total;
    quota_unit = "%";
  }

  const hasEmail = !!profile.email;
  const hasQuota = !!sessionQuota;
  let healthError: string | undefined;
  if (!hasEmail) {
    healthError = "Could not fetch profile email";
  } else if (!hasQuota) {
    healthError = usage.message ?? "Could not fetch usage quota";
  }

  return {
    creds,
    health: {
      ok: hasEmail && hasQuota,
      error: healthError,
    },
    identity: {
      email: profile.email ?? null,
      plan: profile.plan ?? usage.plan ?? "Free",
      quota_used,
      quota_total,
      quota_unit,
      quota_extra: {
        displayName: profile.displayName,
        accountId: profile.accountId,
        organizationId: profile.organizationId,
        organizationType: profile.organizationType,
        rateLimitTier: profile.rateLimitTier,
        fiveHour: usage.quotas["session (5h)"] ?? null,
        sevenDay: usage.quotas["weekly (7d)"] ?? null,
        quotas: usage.quotas,
        extraUsage: usage.extraUsage,
        usageMessage: usage.message,
        health_error: healthError,
        fetchedAt: new Date().toISOString(),
      },
    },
  };
}

export async function listModels(credsIn: StoredCredentials): Promise<DiscoveredModel[]> {
  const creds = await refreshIfNeeded(credsIn);
  const token = creds.access_token;
  if (!token) throw new ClaudeAuthError("No access token — re-login required");

  const r = await fetch("https://api.anthropic.com/v1/models", {
    headers: {
      ...oauthHeaders(token),
      "anthropic-beta": CLAUDE_CODE_BETA,
      "X-App": "cli",
    },
    signal: AbortSignal.timeout(15_000),
  });
  if (!r.ok) {
    const text = await r.text();
    throw new Error(`Claude models list failed (${r.status}): ${text.slice(0, 200)}`);
  }

  const j = (await r.json()) as {
    data?: Array<{ id?: string; display_name?: string; type?: string }>;
  };
  return (j.data ?? [])
    .filter((m) => typeof m.id === "string" && m.id.length > 0)
    .map((m) => ({
      external_id: m.id!,
      display_name: m.display_name ?? m.id!,
      capabilities: ["chat", "tools", "vision"],
    }));
}

export async function testModel(
  credsIn: StoredCredentials,
  external_id: string,
): Promise<ModelTestResult> {
  const creds = await refreshIfNeeded(credsIn);
  const t0 = Date.now();
  try {
    const r = await fetch("https://api.anthropic.com/v1/messages", {
      method: "POST",
      headers: {
        Authorization: `Bearer ${creds.access_token}`,
        "anthropic-version": API_VERSION,
        "anthropic-beta": CLAUDE_CODE_BETA,
        "User-Agent": USER_AGENT,
        "X-App": "cli",
        "Content-Type": "application/json",
      },
      body: JSON.stringify({
        model: external_id,
        max_tokens: 8,
        system: [{ type: "text", text: CLAUDE_CODE_IDENTITY }],
        messages: [{ role: "user", content: MODEL_TEST_PROMPT }],
      }),
    });
    if (!r.ok) {
      const text = await r.text();
      return { external_id, ok: false, latency_ms: Date.now() - t0, error: text.slice(0, 200) };
    }
    const j: { content?: Array<{ type?: string; text?: string }> } = await r.json();
    const text =
      j.content
        ?.filter((b) => b.type === "text")
        .map((b) => b.text ?? "")
        .join("") ?? "";
    const validation = validateModelTestResponse(text);
    if (!validation.ok) {
      return {
        external_id,
        ok: false,
        latency_ms: Date.now() - t0,
        error: validation.error,
      };
    }
    return { external_id, ok: true, latency_ms: Date.now() - t0 };
  } catch (e: any) {
    return { external_id, ok: false, latency_ms: Date.now() - t0, error: String(e?.message ?? e) };
  }
}

export async function chat(
  credsIn: StoredCredentials,
  externalId: string,
  req: ChatRequest,
): Promise<ChatResult> {
  const creds = await refreshIfNeeded(credsIn);
  const t0 = Date.now();
  try {
    // Extract system messages and convert to Anthropic format
    const systemParts = req.messages
      .filter((m) => m.role === "system")
      .map((m) => ({ type: "text", text: m.content }));

    const conversationMessages = req.messages
      .filter((m) => m.role !== "system")
      .map((m) => ({ role: m.role as "user" | "assistant", content: m.content }));

    if (conversationMessages.length === 0) {
      return { ok: false, inputTokens: 0, outputTokens: 0, error: "No user/assistant messages" };
    }

    const body: Record<string, unknown> = {
      model: externalId,
      max_tokens: req.maxTokens ?? 1024,
      messages: conversationMessages,
    };
    if (systemParts.length > 0) body.system = systemParts;

    const r = await fetch("https://api.anthropic.com/v1/messages", {
      method: "POST",
      headers: {
        Authorization: `Bearer ${creds.access_token}`,
        "anthropic-version": API_VERSION,
        "anthropic-beta": CLAUDE_CODE_BETA,
        "User-Agent": USER_AGENT,
        "X-App": "cli",
        "Content-Type": "application/json",
      },
      body: JSON.stringify(body),
    });

    if (!r.ok) {
      const text = await r.text();
      return { ok: false, inputTokens: 0, outputTokens: 0, error: text.slice(0, 300) };
    }

    const j: any = await r.json();
    const content =
      j?.content
        ?.filter((b: any) => b.type === "text")
        .map((b: any) => b.text)
        .join("") ?? "";
    const inputTokens = j?.usage?.input_tokens ?? 0;
    const outputTokens = j?.usage?.output_tokens ?? 0;
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
