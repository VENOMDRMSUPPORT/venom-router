/* Antigravity fetchAvailableModels diagnostics — client-safe search and inspection. */

export const IDE_DISPLAY_NAME_SEARCH_TERMS = [
  "Gemini 3.5 Flash",
  "Gemini 3.1 Pro",
  "Claude Sonnet",
  "Claude Opus",
  "GPT-OSS",
  "gpt-oss",
] as const;

export const IDE_MODEL_ID_SEARCH_TERMS = [
  "gemini-3.5-flash",
  "gemini-3.1-pro",
  "claude-sonnet-4.6",
  "claude-opus-4.6",
  "gpt-oss-120b",
  "tab_jump_flash_lite_preview",
  "chat_20706",
  "chat_23310",
] as const;

export const STRUCTURAL_SEARCH_TERMS = [
  "displayName",
  "quotaInfo",
  "remainingFraction",
  "agentModelSorts",
  "modelIds",
  "availableModels",
  "modelCatalog",
  "allowedModels",
] as const;

export type JsonPathMatch = {
  path: string;
  value: string;
  matchedTerm: string;
};

export function jsonPath(parent: string, key: string | number): string {
  if (parent === "$") {
    return typeof key === "number" ? `$[${key}]` : `$["${key}"]`;
  }
  return typeof key === "number" ? `${parent}[${key}]` : `${parent}["${key}"]`;
}

/** Recursively find string values containing any of the search terms. */
export function findStringsInRawResponse(
  raw: unknown,
  terms: readonly string[],
  opts: { maxMatches?: number; parentPath?: string } = {},
): JsonPathMatch[] {
  const maxMatches = opts.maxMatches ?? 200;
  const matches: JsonPathMatch[] = [];
  const lowerTerms = terms.map((t) => t.toLowerCase());

  function walk(node: unknown, path: string) {
    if (matches.length >= maxMatches) return;

    if (typeof node === "string") {
      const lower = node.toLowerCase();
      for (let i = 0; i < terms.length; i++) {
        if (lower.includes(lowerTerms[i]!)) {
          matches.push({ path, value: node, matchedTerm: terms[i]! });
          break;
        }
      }
      return;
    }

    if (Array.isArray(node)) {
      for (let i = 0; i < node.length; i++) walk(node[i], jsonPath(path, i));
      return;
    }

    if (node && typeof node === "object") {
      for (const [k, v] of Object.entries(node as Record<string, unknown>)) {
        walk(v, jsonPath(path, k));
      }
    }
  }

  walk(raw, opts.parentPath ?? "$");
  return matches;
}

export function topLevelKeys(raw: unknown): string[] {
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) return [];
  return Object.keys(raw as Record<string, unknown>);
}

export type ModelMapCandidate = {
  path: string;
  keyCount: number;
  sampleKeys: string[];
  withDisplayName: number;
  withoutDisplayName: number;
};

const MODEL_MAP_KEY_HINTS = /model|catalog|allowed|available|quota/i;

/** Find object maps that look like model registries anywhere in the JSON tree. */
export function findModelMapCandidates(raw: unknown): ModelMapCandidate[] {
  const results: ModelMapCandidate[] = [];

  function inspectMap(path: string, obj: Record<string, unknown>) {
    const keys = Object.keys(obj);
    if (keys.length === 0) return;

    let withDisplayName = 0;
    let withoutDisplayName = 0;
    for (const k of keys) {
      const entry = obj[k];
      if (!entry || typeof entry !== "object" || Array.isArray(entry)) continue;
      const dn = (entry as Record<string, unknown>).displayName;
      if (typeof dn === "string" && dn.trim()) withDisplayName++;
      else withoutDisplayName++;
    }

    const looksLikeModelMap =
      path.endsWith('["models"]') ||
      path === "$.models" ||
      (withDisplayName + withoutDisplayName >= 3 &&
        withDisplayName + withoutDisplayName === keys.length);

    if (looksLikeModelMap || MODEL_MAP_KEY_HINTS.test(path)) {
      results.push({
        path,
        keyCount: keys.length,
        sampleKeys: keys.slice(0, 8),
        withDisplayName,
        withoutDisplayName,
      });
    }
  }

  function walk(node: unknown, path: string) {
    if (!node || typeof node !== "object") return;
    if (Array.isArray(node)) {
      for (let i = 0; i < node.length; i++) walk(node[i], jsonPath(path, i));
      return;
    }
    const obj = node as Record<string, unknown>;
    inspectMap(path, obj);
    for (const [k, v] of Object.entries(obj)) {
      if (v && typeof v === "object") walk(v, jsonPath(path, k));
    }
  }

  walk(raw, "$");
  return results;
}

export type ModelEntryInspection = {
  rawKey: string;
  objectKeys: string[];
  displayName?: unknown;
  name?: unknown;
  title?: unknown;
  model?: unknown;
  modelId?: unknown;
  id?: unknown;
  metadata?: unknown;
  quotaInfo?: unknown;
  isInternal?: unknown;
  recommended?: unknown;
  nestedHints: Record<string, unknown>;
};

const ENTRY_FIELDS = [
  "displayName",
  "name",
  "title",
  "model",
  "modelId",
  "id",
  "metadata",
  "quotaInfo",
  "isInternal",
  "recommended",
] as const;

export function inspectModelEntry(rawKey: string, entry: unknown): ModelEntryInspection {
  const obj =
    entry && typeof entry === "object" && !Array.isArray(entry)
      ? (entry as Record<string, unknown>)
      : {};
  const nestedHints: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(obj)) {
    if (ENTRY_FIELDS.includes(k as (typeof ENTRY_FIELDS)[number])) continue;
    if (/display|model|quota|group|visibility|internal|recommend|tier|provider/i.test(k)) {
      nestedHints[k] = v;
    }
  }
  return {
    rawKey,
    objectKeys: Object.keys(obj),
    displayName: obj.displayName,
    name: obj.name,
    title: obj.title,
    model: obj.model,
    modelId: obj.modelId,
    id: obj.id,
    metadata: obj.metadata,
    quotaInfo: obj.quotaInfo,
    isInternal: obj.isInternal,
    recommended: obj.recommended,
    nestedHints,
  };
}

export function inspectModelsObject(models: Record<string, unknown> | undefined) {
  if (!models) return [];
  return Object.entries(models).map(([key, entry]) => inspectModelEntry(key, entry));
}

export function extractAgentModelSortIds(raw: unknown): {
  sortNames: string[];
  recommendedModelIds: string[];
  allReferencedIds: string[];
} {
  const sortNames: string[] = [];
  const recommendedModelIds: string[] = [];
  const allReferencedIds: string[] = [];

  if (!raw || typeof raw !== "object") {
    return { sortNames, recommendedModelIds, allReferencedIds };
  }
  const sorts = (raw as Record<string, unknown>).agentModelSorts;
  if (!Array.isArray(sorts)) return { sortNames, recommendedModelIds, allReferencedIds };

  for (const sort of sorts) {
    if (!sort || typeof sort !== "object") continue;
    const s = sort as Record<string, unknown>;
    const name = typeof s.displayName === "string" ? s.displayName : undefined;
    if (name) sortNames.push(name);
    const groups = Array.isArray(s.groups) ? s.groups : [];
    for (const group of groups) {
      if (!group || typeof group !== "object") continue;
      const ids = (group as Record<string, unknown>).modelIds;
      if (!Array.isArray(ids)) continue;
      for (const id of ids) {
        if (typeof id !== "string") continue;
        allReferencedIds.push(id);
        if (name?.trim().toLowerCase() === "recommended") {
          recommendedModelIds.push(id);
        }
      }
    }
  }

  return { sortNames, recommendedModelIds, allReferencedIds };
}

export function buildSearchReport(raw: unknown) {
  const ideNameMatches = findStringsInRawResponse(raw, IDE_DISPLAY_NAME_SEARCH_TERMS);
  const ideIdMatches = findStringsInRawResponse(raw, IDE_MODEL_ID_SEARCH_TERMS);
  const structuralMatches = findStringsInRawResponse(raw, STRUCTURAL_SEARCH_TERMS, {
    maxMatches: 50,
  });
  const agentSorts = extractAgentModelSortIds(raw);

  return {
    ideDisplayNameMatches: ideNameMatches,
    ideModelIdMatches: ideIdMatches,
    structuralMatches,
    agentModelSorts: agentSorts,
    ideNamesFoundInFetchResponse: ideNameMatches.length > 0,
    ideNamesFoundMessage:
      ideNameMatches.length > 0
        ? undefined
        : "Expected IDE model names do not exist anywhere in fetchAvailableModels raw response.",
  };
}
