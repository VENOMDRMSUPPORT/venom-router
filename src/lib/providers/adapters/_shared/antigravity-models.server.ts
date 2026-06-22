/* Antigravity live model discovery — single source of truth for fetchAvailableModels. */

import {
  ANTIGRAVITY_BASE,
  ANTIGRAVITY_USER_AGENT,
  OAUTH_CLIENT_METADATA,
} from "./antigravity-constants.server";
import type { DiscoveredModel } from "../types";
import type { ModelQuotaInfo } from "./quota-types";
import {
  buildVisibleCatalogModels,
  buildAntigravityQuotaGroups,
  extractRecommendedModelIds,
  inferCapabilities,
  parseModelQuota,
  type AntigravityLiveModelEntry,
} from "../../antigravity-live-snapshot";
import type { QuotaGroup } from "./quota-types";

const FETCH_MODELS = `${ANTIGRAVITY_BASE}/v1internal:fetchAvailableModels`;

export const ANTIGRAVITY_FETCH_ENDPOINT_VARIANTS = {
  production: ANTIGRAVITY_BASE,
  daily: "https://daily-cloudcode-pa.googleapis.com",
  dailySandbox: "https://daily-cloudcode-pa.sandbox.googleapis.com",
} as const;

export type FetchAvailableModelsBodyVariant =
  | { kind: "project_only"; body: { project: string } }
  | {
      kind: "project_with_metadata";
      body: { project: string; metadata: typeof OAUTH_CLIENT_METADATA };
    };

export type FetchAvailableModelsRequestMeta = {
  endpointBase: string;
  path: "/v1internal:fetchAvailableModels";
  url: string;
  bodyVariant: FetchAvailableModelsBodyVariant["kind"];
  body: Record<string, unknown>;
  headers: Record<string, string>;
  projectId: string;
  accessTokenSource: "account_oauth";
};

export type FetchAvailableModelsRawResult = {
  request: FetchAvailableModelsRequestMeta;
  status: number;
  rawResponse: unknown;
  models: Record<string, LiveModelEntry>;
  topLevelKeys: string[];
  modelKeys: string[];
  error?: string;
};

export type LiveModelEntry = AntigravityLiveModelEntry;

export interface AntigravityModelsSnapshot {
  liveModels: Record<string, LiveModelEntry>;
  discovered: DiscoveredModel[];
  quotaMap: Record<string, ModelQuotaInfo>;
  groups: QuotaGroup[];
  rawResponse: unknown;
}

function antigravityHeaders(token: string) {
  return {
    Authorization: `Bearer ${token}`,
    "Content-Type": "application/json",
    Accept: "application/json",
    "User-Agent": ANTIGRAVITY_USER_AGENT,
    "X-Goog-Api-Client": "google-cloud-sdk vscode_cloudshelleditor/0.1",
    "X-Client-Name": "antigravity",
    "X-Client-Version": "1.107.0",
    "x-request-source": "local",
    "Client-Metadata": JSON.stringify(OAUTH_CLIENT_METADATA),
  };
}

export function extractModelQuotaInfo(entry: LiveModelEntry): ModelQuotaInfo | undefined {
  const parsed = parseModelQuota(entry);
  if (!parsed?.resetTime) return undefined;
  const remainingFraction = parsed.remainingFraction ?? 1;
  const isExhausted = Boolean(parsed.isExhausted);
  return {
    remainingFraction,
    resetTime: parsed.resetTime,
    isExhausted,
    fiveHourQuota: { remainingFraction, resetTime: parsed.resetTime, isExhausted },
  };
}

/** Map IDE-visible Recommended models to DiscoveredModel — raw catalog entries excluded. */
export function parseIdeVisibleLiveModels(
  rawResponse: unknown,
  liveModels: Record<string, LiveModelEntry>,
): DiscoveredModel[] {
  const recommendedIds = extractRecommendedModelIds(rawResponse);
  const { models } = buildVisibleCatalogModels(recommendedIds, liveModels);
  return models.map((m) => ({
    external_id: m.id,
    display_name: m.displayName,
    capabilities: m.capabilities,
  }));
}

/** @deprecated Use parseIdeVisibleLiveModels — kept for diagnostics tooling. */
export function parseLiveModels(liveModels: Record<string, LiveModelEntry>): DiscoveredModel[] {
  return Object.entries(liveModels).map(([id, entry]) => ({
    external_id: id,
    display_name:
      typeof entry.displayName === "string" && entry.displayName.trim()
        ? entry.displayName.trim()
        : id,
    capabilities: inferCapabilities(entry),
  }));
}

/** Full raw fetch — preserves entire JSON including agentModelSorts. */
export async function fetchAvailableModelsRaw(
  token: string,
  projectId: string,
  opts: {
    endpointBase?: string;
    bodyVariant?: FetchAvailableModelsBodyVariant["kind"];
  } = {},
): Promise<FetchAvailableModelsRawResult> {
  const endpointBase = opts.endpointBase ?? ANTIGRAVITY_BASE;
  const url = `${endpointBase}/v1internal:fetchAvailableModels`;
  const bodyVariant = opts.bodyVariant ?? "project_only";
  const body: Record<string, unknown> =
    bodyVariant === "project_with_metadata"
      ? { project: projectId, metadata: { ...OAUTH_CLIENT_METADATA } }
      : { project: projectId };

  const headers = antigravityHeaders(token);
  const request: FetchAvailableModelsRequestMeta = {
    endpointBase,
    path: "/v1internal:fetchAvailableModels",
    url,
    bodyVariant,
    body,
    headers: {
      ...headers,
      Authorization: "Bearer [REDACTED]",
    },
    projectId,
    accessTokenSource: "account_oauth",
  };

  const r = await fetch(url, {
    method: "POST",
    headers,
    body: JSON.stringify(body),
    signal: AbortSignal.timeout(15_000),
  });

  const text = await r.text();
  if (!r.ok) {
    return {
      request,
      status: r.status,
      rawResponse: { _parseError: text.slice(0, 500) },
      models: {},
      topLevelKeys: [],
      modelKeys: [],
      error: `fetchAvailableModels ${r.status}: ${text.slice(0, 200)}`,
    };
  }

  let rawResponse: unknown;
  try {
    rawResponse = JSON.parse(text);
  } catch {
    return {
      request,
      status: r.status,
      rawResponse: { _rawText: text.slice(0, 500) },
      models: {},
      topLevelKeys: [],
      modelKeys: [],
      error: "Response was not valid JSON",
    };
  }

  const j = rawResponse as { models?: Record<string, LiveModelEntry> };
  const models = j?.models && typeof j.models === "object" ? j.models : {};
  const topLevelKeys =
    rawResponse && typeof rawResponse === "object" && !Array.isArray(rawResponse)
      ? Object.keys(rawResponse as Record<string, unknown>)
      : [];

  return {
    request,
    status: r.status,
    rawResponse,
    models,
    topLevelKeys,
    modelKeys: Object.keys(models),
  };
}

export async function fetchAvailableModels(
  token: string,
  projectId: string,
): Promise<Record<string, LiveModelEntry>> {
  const result = await fetchAvailableModelsRaw(token, projectId);
  if (result.error) throw new Error(result.error);
  if (!Object.keys(result.models).length) {
    throw new Error("fetchAvailableModels returned no models payload");
  }
  return result.models;
}

export async function fetchAntigravitySnapshot(
  token: string,
  projectId: string,
): Promise<AntigravityModelsSnapshot> {
  const raw = await fetchAvailableModelsRaw(token, projectId);
  if (raw.error) throw new Error(raw.error);
  const liveModels = raw.models;
  const discovered = parseIdeVisibleLiveModels(raw.rawResponse, liveModels);
  if (!discovered.length) {
    throw new Error("No IDE-visible models in agentModelSorts.Recommended");
  }

  const quotaMap: Record<string, ModelQuotaInfo> = {};
  for (const m of discovered) {
    const entry = liveModels[m.external_id];
    if (!entry) continue;
    const q = extractModelQuotaInfo(entry);
    if (q) quotaMap[m.external_id] = q;
  }

  const groups = buildAntigravityQuotaGroups(raw.rawResponse, liveModels);

  return { liveModels, discovered, quotaMap, groups, rawResponse: raw.rawResponse };
}
