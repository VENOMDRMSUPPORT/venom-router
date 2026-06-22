/* Claude Code OAuth adapter (PKCE, JSON token exchange). Server-only. */
import type { StoredCredentials, AccountIdentity, DiscoveredModel, ModelTestResult } from "./types";
import { CLAUDE_CURATED_MODELS, CLAUDE_OAUTH } from "./_shared/oauth-clients.server";
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
  const [authCode, returnedState] = input.code.includes("#")
    ? input.code.split("#")
    : [input.code, input.state];
  const body = {
    grant_type: "authorization_code",
    client_id: CLAUDE_OAUTH.clientId,
    code: authCode,
    redirect_uri: input.redirect_uri,
    code_verifier: input.code_verifier,
    state: returnedState,
  };
  const res = await fetch(CLAUDE_OAUTH.tokenUrl, {
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(`Claude token exchange failed (${res.status}): ${text.slice(0, 300)}`);
  }
  const j: any = await res.json();
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
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      grant_type: "refresh_token",
      refresh_token: creds.refresh_token,
      client_id: CLAUDE_OAUTH.clientId,
    }),
  });
  if (!res.ok) throw new Error("Claude token refresh failed");
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
): Promise<{ creds: StoredCredentials; identity: AccountIdentity }> {
  const creds = await refreshIfNeeded(credsIn);
  const token = creds.access_token!;

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

  return {
    creds,
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
        fetchedAt: new Date().toISOString(),
      },
    },
  };
}

export async function listModels(_creds: StoredCredentials): Promise<DiscoveredModel[]> {
  return CLAUDE_CURATED_MODELS.map((m) => ({ ...m, capabilities: [...m.capabilities] }));
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
