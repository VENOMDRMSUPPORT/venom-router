/* Antigravity (Google Cloud Code Assist) OAuth adapter. Server-only. Picoclaw + 9router parity. */
import { createHash, randomBytes } from "crypto";
import type { StoredCredentials, AccountIdentity, DiscoveredModel, ModelTestResult } from "./types";
import {
  antigravityClientForRedirect,
  ANTIGRAVITY_OAUTH_CLIENT,
} from "./_shared/oauth-clients.server";
import {
  ANTIGRAVITY_BASE,
  ANTIGRAVITY_USER_AGENT,
  GOOGLE_USERINFO_URL,
  OAUTH_CLIENT_METADATA,
  loadCodeAssistBody,
} from "./_shared/antigravity-constants.server";
import {
  buildAntigravityPlanInfo,
  resolveAntigravityPlan,
  resolveOnboardTierId,
  type LoadCodeAssistResponse,
} from "./_shared/antigravity-plan.server";
import {
  fetchAntigravitySnapshot,
  fetchAvailableModels,
  fetchAvailableModelsRaw,
  ANTIGRAVITY_FETCH_ENDPOINT_VARIANTS,
} from "./_shared/antigravity-models.server";
import type {
  AntigravityFetchDiagnosis,
  AntigravityFetchVariantDiagnosis,
} from "../antigravity-fetch-diagnostics.types";
import { fetchAntigravityUsage } from "./_shared/antigravity-usage.server";
import {
  buildSearchReport,
  extractAgentModelSortIds,
  findModelMapCandidates,
  inspectModelEntry,
  inspectModelsObject,
  topLevelKeys,
} from "../antigravity-fetch-diagnostics";

const BASE = ANTIGRAVITY_BASE;
const AUTHORIZE_URL = "https://accounts.google.com/o/oauth2/v2/auth";
const TOKEN_URL = "https://oauth2.googleapis.com/token";
const LOAD_CODE_ASSIST = `${BASE}/v1internal:loadCodeAssist`;
const ONBOARD_USER = `${BASE}/v1internal:onboardUser`;
const GENERATE = `${BASE}/v1internal:streamGenerateContent?alt=sse`;

const USER_AGENT = ANTIGRAVITY_USER_AGENT;
const REFRESH_LEAD_MS = 300_000;

const SCOPES = [
  "https://www.googleapis.com/auth/cloud-platform",
  "https://www.googleapis.com/auth/userinfo.email",
  "https://www.googleapis.com/auth/userinfo.profile",
  "https://www.googleapis.com/auth/cclog",
  "https://www.googleapis.com/auth/experimentsandconfigs",
];

const COMMON_HEADERS = {
  "User-Agent": USER_AGENT,
  "X-Goog-Api-Client": "google-cloud-sdk vscode_cloudshelleditor/0.1",
  "X-Client-Name": "antigravity",
  "X-Client-Version": "1.107.0",
  "x-request-source": "local",
  "Client-Metadata": JSON.stringify(OAUTH_CLIENT_METADATA),
};

export type AntigravityClientCreds = ReturnType<typeof antigravityClientForRedirect>;

function b64urlChallenge(verifier: string) {
  return createHash("sha256").update(verifier).digest("base64url");
}

export function startFlow(input: { redirect_uri: string }) {
  const client = antigravityClientForRedirect(input.redirect_uri);
  const code_verifier = randomBytes(32).toString("hex");
  const code_challenge = b64urlChallenge(code_verifier);
  const state = randomBytes(16).toString("hex");
  const url = new URL(AUTHORIZE_URL);
  url.searchParams.set("client_id", client.client_id);
  url.searchParams.set("response_type", "code");
  url.searchParams.set("redirect_uri", client.redirect_uri);
  url.searchParams.set("scope", SCOPES.join(" "));
  url.searchParams.set("access_type", "offline");
  url.searchParams.set("prompt", "consent");
  url.searchParams.set("state", state);
  url.searchParams.set("code_challenge", code_challenge);
  url.searchParams.set("code_challenge_method", "S256");
  return {
    authorize_url: url.toString(),
    redirect_uri: client.redirect_uri,
    code_verifier,
    state,
    client,
  };
}

export async function completeFlow(input: {
  code: string;
  state: string;
  code_verifier: string;
  redirect_uri: string;
}): Promise<StoredCredentials> {
  let code = input.code.trim();
  if (code.startsWith("http")) {
    try {
      const u = new URL(code);
      code = u.searchParams.get("code") ?? code;
    } catch {
      /* ignore */
    }
  }

  const client = antigravityClientForRedirect(input.redirect_uri);
  const form = new URLSearchParams({
    grant_type: "authorization_code",
    client_id: client.client_id,
    client_secret: client.client_secret,
    code,
    redirect_uri: client.redirect_uri,
    code_verifier: input.code_verifier,
  });
  const r = await fetch(TOKEN_URL, {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body: form.toString(),
  });
  if (!r.ok) {
    const text = await r.text();
    throw new Error(`Antigravity token exchange failed (${r.status}): ${text.slice(0, 300)}`);
  }
  const j: any = await r.json();

  const creds: StoredCredentials = {
    kind: "oauth2",
    access_token: j.access_token,
    refresh_token: j.refresh_token,
    expires_at: Date.now() + Number(j.expires_in ?? 3600) * 1000 - 5 * 60_000,
    scope: j.scope,
    extra: { client },
  };

  const profile = await fetchProfile(creds.access_token!, { allowOnboard: true });
  if (profile.projectId) creds.project_id = profile.projectId;
  creds.extra = {
    ...(creds.extra ?? {}),
    email: profile.email,
    tierId: profile.tierId,
    tierName: profile.tierName,
    plan: profile.plan,
    availablePromptCredits: profile.availablePromptCredits,
    planInfo: profile.planInfo,
    onboarded: profile.onboarded,
  };

  return creds;
}

async function refreshIfNeeded(creds: StoredCredentials): Promise<StoredCredentials> {
  if (!creds.refresh_token) return creds;
  if (creds.expires_at && creds.expires_at - Date.now() > REFRESH_LEAD_MS) return creds;
  const client = (creds.extra?.client as AntigravityClientCreds | undefined) ?? {
    ...ANTIGRAVITY_OAUTH_CLIENT,
    redirect_uri: "",
  };
  const form = new URLSearchParams({
    grant_type: "refresh_token",
    refresh_token: creds.refresh_token,
    client_id: client.client_id,
    client_secret: client.client_secret,
  });
  const r = await fetch(TOKEN_URL, {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body: form.toString(),
  });
  if (!r.ok) throw new Error("Antigravity token refresh failed");
  const j: any = await r.json();
  return {
    ...creds,
    access_token: j.access_token ?? creds.access_token,
    refresh_token: j.refresh_token ?? creds.refresh_token,
    expires_at: Date.now() + Number(j.expires_in ?? 3600) * 1000 - 5 * 60_000,
  };
}

interface AntigravityProfile {
  email?: string;
  displayName?: string;
  projectId?: string;
  tierId?: string;
  tierName?: string;
  allowedTierIds?: string[];
  onboarded: boolean;
  plan?: string;
  availablePromptCredits?: number;
  planInfo?: Record<string, unknown>;
}

export interface AccountHealthResult {
  ok: boolean;
  latency_ms: number;
  error?: string;
}

function bearerHeaders(token: string) {
  return { Authorization: `Bearer ${token}`, "User-Agent": USER_AGENT, Accept: "application/json" };
}

async function fetchGoogleUserinfo(token: string): Promise<{ email?: string; name?: string }> {
  try {
    const res = await fetch(GOOGLE_USERINFO_URL, {
      headers: { Authorization: `Bearer ${token}`, Accept: "application/json" },
      signal: AbortSignal.timeout(10_000),
    });
    if (!res.ok) return {};
    const d: any = await res.json();
    return { email: d.email, name: d.name };
  } catch {
    return {};
  }
}

async function loadCodeAssist(token: string): Promise<LoadCodeAssistResponse> {
  const res = await fetch(LOAD_CODE_ASSIST, {
    method: "POST",
    headers: { ...bearerHeaders(token), "Content-Type": "application/json", ...COMMON_HEADERS },
    body: JSON.stringify(loadCodeAssistBody()),
    signal: AbortSignal.timeout(15_000),
  });
  if (!res.ok) {
    const text = await res.text().catch(() => "");
    throw new Error(`loadCodeAssist ${res.status}: ${text.slice(0, 200)}`);
  }
  return res.json();
}

async function onboardUser(
  token: string,
  tierId: string,
  maxRetries = 10,
  intervalMs = 5000,
): Promise<string | undefined> {
  for (let i = 0; i < maxRetries; i++) {
    try {
      const res = await fetch(ONBOARD_USER, {
        method: "POST",
        headers: { ...bearerHeaders(token), "Content-Type": "application/json", ...COMMON_HEADERS },
        body: JSON.stringify({ tierId, metadata: OAUTH_CLIENT_METADATA }),
        signal: AbortSignal.timeout(15_000),
      });
      if (res.ok) {
        const d: any = await res.json();
        if (d.done) return d.response?.cloudaicompanionProject;
      }
    } catch {
      /* retry */
    }
    if (i < maxRetries - 1) await new Promise((r) => setTimeout(r, intervalMs));
  }
  return undefined;
}

export async function fetchProfile(
  token: string,
  opts: { allowOnboard?: boolean; loadResult?: LoadCodeAssistResponse; strict?: boolean } = {},
): Promise<AntigravityProfile> {
  const userinfo = await fetchGoogleUserinfo(token);
  let load: LoadCodeAssistResponse;
  if (opts.loadResult) {
    load = opts.loadResult;
  } else {
    try {
      load = await loadCodeAssist(token);
    } catch (e: any) {
      if (opts.strict) throw e;
      return { email: userinfo.email, displayName: userinfo.name, onboarded: false };
    }
  }

  let projectId: string | undefined = load.cloudaicompanionProject ?? undefined;
  const allowedTiers = load.allowedTiers ?? [];
  const onboardTierId = resolveOnboardTierId(load);
  let onboarded = Boolean(projectId);

  if (!projectId && opts.allowOnboard && onboardTierId) {
    const got = await onboardUser(token, onboardTierId);
    if (got) {
      projectId = got;
      onboarded = true;
    }
  }

  const tierName = load.currentTier?.name;
  const tierId = load.currentTier?.id ?? onboardTierId;
  const plan = resolveAntigravityPlan(load);

  return {
    email: userinfo.email,
    displayName: userinfo.name,
    projectId,
    tierId,
    tierName,
    allowedTierIds: allowedTiers.map((t) => t.id).filter(Boolean) as string[],
    onboarded,
    plan,
    availablePromptCredits: load.availablePromptCredits,
    planInfo: buildAntigravityPlanInfo(load),
  };
}

function buildIdentityFromSync(
  creds: StoredCredentials,
  profile: AntigravityProfile,
  usage: Awaited<ReturnType<typeof fetchAntigravityUsage>>,
  snapshot: Awaited<ReturnType<typeof fetchAntigravitySnapshot>>,
  health: AccountHealthResult,
): AccountIdentity {
  const allFractions = Object.values(snapshot.quotaMap).map((q) => q.remainingFraction);
  let quota_used: number | null = null;
  let quota_total: number | null = null;
  let quota_unit: string | null = null;
  if (allFractions.length) {
    const avgRemaining = allFractions.reduce((s, n) => s + n, 0) / allFractions.length;
    quota_used = Math.round((1 - avgRemaining) * 100);
    quota_total = 100;
    quota_unit = "%";
  }

  return {
    email: profile.email ?? null,
    plan: profile.plan ?? usage.plan ?? null,
    quota_used,
    quota_total,
    quota_unit,
    quota_extra: {
      projectId: creds.project_id,
      tierId: profile.tierId,
      tierName: profile.tierName ?? usage.tierName,
      displayName: profile.displayName,
      availablePromptCredits: profile.availablePromptCredits ?? usage.availablePromptCredits,
      planInfo: profile.planInfo ?? usage.planInfo,
      groups: snapshot.groups,
      models: snapshot.quotaMap,
      usageQuotas: usage.quotas,
      health: {
        ok: health.ok,
        latency_ms: health.latency_ms,
        checked_at: new Date().toISOString(),
        error: health.error,
      },
      fetchedAt: new Date().toISOString(),
    },
  };
}

export async function checkAccountHealth(credsIn: StoredCredentials): Promise<AccountHealthResult> {
  const creds = await refreshIfNeeded(credsIn);
  const t0 = Date.now();
  if (!creds.access_token) {
    return { ok: false, latency_ms: 0, error: "No access token" };
  }
  try {
    await loadCodeAssist(creds.access_token);
    return { ok: true, latency_ms: Date.now() - t0 };
  } catch (e: any) {
    return { ok: false, latency_ms: Date.now() - t0, error: String(e?.message ?? e) };
  }
}

export interface SyncAntigravityResult {
  creds: StoredCredentials;
  identity: AccountIdentity;
  models: DiscoveredModel[];
  rawResponse: unknown;
  health: AccountHealthResult;
  provider_calls: string[];
}

export async function syncAntigravityAccount(
  credsIn: StoredCredentials,
): Promise<SyncAntigravityResult> {
  const providerCalls: string[] = [];
  const tokenBefore = credsIn.access_token;
  const expiresBefore = credsIn.expires_at;
  const creds = await refreshIfNeeded(credsIn);
  if (creds.access_token !== tokenBefore || creds.expires_at !== expiresBefore) {
    providerCalls.push("token_refresh");
  }

  const healthStart = Date.now();
  let load: LoadCodeAssistResponse;
  try {
    load = await loadCodeAssist(creds.access_token!);
    providerCalls.push("loadCodeAssist");
  } catch (e: any) {
    const health: AccountHealthResult = {
      ok: false,
      latency_ms: Date.now() - healthStart,
      error: String(e?.message ?? e),
    };
    throw Object.assign(new Error(`Account health check failed: ${health.error}`), { health });
  }
  const health: AccountHealthResult = { ok: true, latency_ms: Date.now() - healthStart };

  const hadProject = Boolean(creds.project_id);
  const profile = await fetchProfile(creds.access_token!, {
    allowOnboard: true,
    loadResult: load,
    strict: true,
  });
  if (!hadProject && profile.projectId) providerCalls.push("onboardUser");
  if (profile.projectId) creds.project_id = profile.projectId;
  creds.extra = {
    ...(creds.extra ?? {}),
    email: profile.email,
    tierId: profile.tierId,
    tierName: profile.tierName,
    plan: profile.plan,
    availablePromptCredits: profile.availablePromptCredits,
    planInfo: profile.planInfo,
    onboarded: profile.onboarded,
  };

  if (!creds.project_id) {
    throw new Error("Antigravity project not initialized. Re-sync to onboard.");
  }

  const snapshot = await fetchAntigravitySnapshot(creds.access_token!, creds.project_id);
  providerCalls.push("fetchAvailableModels");
  const usage = await fetchAntigravityUsage(
    creds.access_token!,
    creds.project_id,
    snapshot.liveModels,
    load,
  );

  return {
    creds,
    identity: buildIdentityFromSync(creds, profile, usage, snapshot, health),
    models: snapshot.discovered,
    rawResponse: snapshot.rawResponse,
    health,
    provider_calls: providerCalls,
  };
}

export async function fetchIdentity(
  credsIn: StoredCredentials,
): Promise<{ creds: StoredCredentials; identity: AccountIdentity }> {
  const { creds, identity } = await syncAntigravityAccount(credsIn);
  return { creds, identity };
}

export async function listModels(credsIn: StoredCredentials): Promise<DiscoveredModel[]> {
  const creds = await refreshIfNeeded(credsIn);
  if (!creds.project_id) {
    throw new Error("Antigravity project not initialized. Re-sync to onboard.");
  }
  const snapshot = await fetchAntigravitySnapshot(creds.access_token!, creds.project_id);
  return snapshot.discovered;
}

function needsProjectResolution(projectId?: string | null): boolean {
  if (!projectId || !String(projectId).trim()) return true;
  const trimmed = String(projectId).trim();
  if (trimmed.length < 3) return true;
  if (/^(unknown|undefined|null|none)$/i.test(trimmed)) return true;
  return false;
}

export interface AntigravityLiveRawResult {
  creds: StoredCredentials;
  projectId: string;
  planTier?: string;
  rawResponse: unknown;
  loadCodeAssistUsed: boolean;
}

/** Resolve project via loadCodeAssist when missing, then fetch live models only. */
export async function fetchAntigravityLiveRaw(
  credsIn: StoredCredentials,
): Promise<AntigravityLiveRawResult> {
  let creds = await refreshIfNeeded(credsIn);
  let loadCodeAssistUsed = false;
  let planTier = (creds.extra?.plan as string | undefined) ?? undefined;
  let projectId = creds.project_id;

  if (needsProjectResolution(projectId)) {
    loadCodeAssistUsed = true;
    const profile = await fetchProfile(creds.access_token!, { allowOnboard: true });
    if (profile.projectId) {
      projectId = profile.projectId;
      creds = {
        ...creds,
        project_id: profile.projectId,
        extra: {
          ...(creds.extra ?? {}),
          email: profile.email,
          tierId: profile.tierId,
          tierName: profile.tierName,
          plan: profile.plan,
          planInfo: profile.planInfo,
          onboarded: profile.onboarded,
        },
      };
    }
    planTier = profile.plan ?? planTier;
  }

  if (!projectId) {
    throw new Error(
      "Antigravity project ID unavailable. loadCodeAssist did not return cloudaicompanionProject.",
    );
  }

  const fetchResult = await fetchAvailableModelsRaw(creds.access_token!, projectId);
  if (fetchResult.error) {
    throw new Error(fetchResult.error);
  }
  return {
    creds,
    projectId,
    planTier,
    rawResponse: fetchResult.rawResponse,
    loadCodeAssistUsed,
  };
}

export type { AntigravityFetchDiagnosis, AntigravityFetchVariantDiagnosis };

function suspiciousIds(keys: string[]): string[] {
  return keys.filter((k) => /^chat_\d+/.test(k) || k.includes("tab_jump") || k.includes("preview"));
}

function buildDiagnosisConclusions(input: {
  primary: AntigravityFetchVariantDiagnosis;
  loadSearch: ReturnType<typeof buildSearchReport>;
  allVariants: AntigravityFetchVariantDiagnosis[];
}): string[] {
  const lines: string[] = [];
  const { primary, loadSearch } = input;

  if (primary.searchReport.ideNamesFoundInFetchResponse) {
    lines.push(
      "Outcome A candidate: Expected IDE display names exist in fetchAvailableModels raw JSON.",
    );
    if (primary.agentModelSorts.recommendedModelIds.length > 0) {
      lines.push(
        `agentModelSorts.Recommended lists ${primary.agentModelSorts.recommendedModelIds.length} model IDs — IDE likely shows this subset, not all ${primary.fetch.modelKeys.length} keys in response.models.`,
      );
    }
  } else if (loadSearch.ideDisplayNameMatches.length > 0) {
    lines.push(
      "IDE display names found in loadCodeAssist but NOT in fetchAvailableModels — catalog may be split across endpoints.",
    );
  } else {
    lines.push(
      "Outcome C candidate: Expected IDE model names not found in fetchAvailableModels or loadCodeAssist raw responses.",
    );
  }

  const chatIds = suspiciousIds(primary.fetch.modelKeys);
  if (chatIds.length) {
    lines.push(
      `Internal-style model keys (${chatIds.slice(0, 5).join(", ")}) are returned directly by the backend in response.models — not created by Venom Router.`,
    );
  }

  const noName = primary.firstFiveModelEntries.filter(
    (e) =>
      typeof (e as { displayName?: unknown }).displayName !== "string" ||
      !String((e as { displayName?: unknown }).displayName).trim(),
  ).length;
  if (noName > 0) {
    lines.push(
      "Many response.models entries lack displayName — fallback-to-id is parser behavior, not fabricated IDs.",
    );
  }

  const variantDiff = input.allVariants.filter(
    (v) => v.fetch.modelKeys.length !== primary.fetch.modelKeys.length,
  );
  if (variantDiff.length) {
    lines.push(
      "Outcome B candidate: Endpoint/body variants return different model counts — compare fetchVariants.",
    );
  }

  return lines;
}

/** Deep diagnostics: loadCodeAssist + multiple fetchAvailableModels variants. Debug only. */
export async function diagnoseAntigravityFetch(
  credsIn: StoredCredentials,
): Promise<AntigravityFetchDiagnosis> {
  let creds = await refreshIfNeeded(credsIn);
  let loadCodeAssistUsed = false;
  let planTier = (creds.extra?.plan as string | undefined) ?? undefined;
  let projectId = creds.project_id;

  const loadRaw = await loadCodeAssist(creds.access_token!);
  const profile = await fetchProfile(creds.access_token!, {
    loadResult: loadRaw,
    allowOnboard: false,
  });

  if (needsProjectResolution(projectId)) {
    loadCodeAssistUsed = true;
    if (profile.projectId) {
      projectId = profile.projectId;
      creds = { ...creds, project_id: profile.projectId };
    }
    planTier = profile.plan ?? planTier;
  } else if (profile.projectId && profile.projectId !== projectId) {
    projectId = profile.projectId;
  }

  if (!projectId) {
    throw new Error("Cannot diagnose: project ID unavailable after loadCodeAssist.");
  }

  const loadSearch = buildSearchReport(loadRaw);

  const variantSpecs: Array<{
    label: string;
    endpointBase: string;
    bodyVariant: "project_only" | "project_with_metadata";
  }> = [
    {
      label: "production + project_only (current default)",
      endpointBase: ANTIGRAVITY_FETCH_ENDPOINT_VARIANTS.production,
      bodyVariant: "project_only",
    },
    {
      label: "production + project_with_metadata",
      endpointBase: ANTIGRAVITY_FETCH_ENDPOINT_VARIANTS.production,
      bodyVariant: "project_with_metadata",
    },
    {
      label: "daily + project_only (debug compare)",
      endpointBase: ANTIGRAVITY_FETCH_ENDPOINT_VARIANTS.daily,
      bodyVariant: "project_only",
    },
  ];

  const fetchVariants: AntigravityFetchVariantDiagnosis[] = [];
  for (const spec of variantSpecs) {
    const fetch = await fetchAvailableModelsRaw(creds.access_token!, projectId, {
      endpointBase: spec.endpointBase,
      bodyVariant: spec.bodyVariant,
    });
    const modelsObj =
      fetch.rawResponse && typeof fetch.rawResponse === "object"
        ? ((fetch.rawResponse as Record<string, unknown>).models as Record<string, unknown>)
        : undefined;
    const inspections = inspectModelsObject(modelsObj);
    fetchVariants.push({
      label: spec.label,
      fetch,
      searchReport: buildSearchReport(fetch.rawResponse),
      modelMapCandidates: findModelMapCandidates(fetch.rawResponse),
      firstFiveModelEntries: inspections.slice(0, 5),
      suspiciousModelEntries: inspections
        .filter((e) => suspiciousIds([e.rawKey]).length > 0)
        .slice(0, 10),
      agentModelSorts: extractAgentModelSortIds(fetch.rawResponse),
    });
  }

  const primary = fetchVariants[0]!;
  const modelsObj = primary.fetch.models;
  let withDn = 0;
  let withoutDn = 0;
  for (const entry of Object.values(modelsObj)) {
    if (typeof entry.displayName === "string" && entry.displayName.trim()) withDn++;
    else withoutDn++;
  }

  return {
    diagnosedAt: new Date().toISOString(),
    projectId,
    planTier,
    loadCodeAssistUsed,
    loadCodeAssist: {
      url: LOAD_CODE_ASSIST,
      body: loadCodeAssistBody(),
      rawResponse: loadRaw,
      topLevelKeys: topLevelKeys(loadRaw),
      searchReport: loadSearch,
      relevantFields: {
        cloudaicompanionProject: loadRaw.cloudaicompanionProject,
        paidTier: loadRaw.paidTier,
        currentTier: loadRaw.currentTier,
        allowedTiers: loadRaw.allowedTiers,
        availablePromptCredits: loadRaw.availablePromptCredits,
      },
    },
    fetchVariants,
    conclusions: buildDiagnosisConclusions({
      primary,
      loadSearch,
      allVariants: fetchVariants,
    }),
    parserAudit: {
      currentParserPath: "$.models (Object.entries on response.models)",
      agentModelSortsPresent: primary.agentModelSorts.allReferencedIds.length > 0,
      recommendedIdsCount: primary.agentModelSorts.recommendedModelIds.length,
      modelsWithDisplayName: withDn,
      modelsWithoutDisplayName: withoutDn,
      chatLikeIdsInModels: suspiciousIds(primary.fetch.modelKeys),
    },
  };
}

export async function testModel(
  credsIn: StoredCredentials,
  external_id: string,
): Promise<ModelTestResult> {
  const creds = await refreshIfNeeded(credsIn);
  const t0 = Date.now();
  try {
    const r = await fetch(GENERATE, {
      method: "POST",
      headers: {
        ...bearerHeaders(creds.access_token!),
        "Content-Type": "application/json",
        ...COMMON_HEADERS,
      },
      body: JSON.stringify({
        project: creds.project_id,
        model: external_id,
        request: {
          contents: [{ role: "user", parts: [{ text: "ping" }] }],
          generationConfig: { maxOutputTokens: 8 },
        },
        requestType: "agent",
        userAgent: USER_AGENT,
        requestId: `test-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
      }),
    });
    if (!r.ok) {
      const text = await r.text();
      return { external_id, ok: false, latency_ms: Date.now() - t0, error: text.slice(0, 200) };
    }
    await r.text();
    return { external_id, ok: true, latency_ms: Date.now() - t0 };
  } catch (e: any) {
    return { external_id, ok: false, latency_ms: Date.now() - t0, error: String(e?.message ?? e) };
  }
}
