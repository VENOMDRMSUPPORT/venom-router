/* Shared Antigravity fetch diagnosis types — safe for client + server imports. */

export type AntigravitySearchReport = {
  ideDisplayNameMatches: Array<{ path: string; value: string; matchedTerm: string }>;
  ideModelIdMatches: Array<{ path: string; value: string; matchedTerm: string }>;
  structuralMatches: Array<{ path: string; value: string; matchedTerm: string }>;
  agentModelSorts: {
    sortNames: string[];
    recommendedModelIds: string[];
    allReferencedIds: string[];
  };
  ideNamesFoundInFetchResponse: boolean;
  ideNamesFoundMessage?: string;
};

export type FetchAvailableModelsRequestMeta = {
  endpointBase: string;
  path: "/v1internal:fetchAvailableModels";
  url: string;
  bodyVariant: "project_only" | "project_with_metadata";
  body: Record<string, unknown>;
  headers: Record<string, string>;
  projectId: string;
  accessTokenSource: "account_oauth";
};

export type FetchAvailableModelsRawResult = {
  request: FetchAvailableModelsRequestMeta;
  status: number;
  rawResponse: unknown;
  models: Record<string, unknown>;
  topLevelKeys: string[];
  modelKeys: string[];
  error?: string;
};

export type AntigravityFetchVariantDiagnosis = {
  label: string;
  fetch: FetchAvailableModelsRawResult;
  searchReport: AntigravitySearchReport;
  modelMapCandidates: Array<{
    path: string;
    keyCount: number;
    sampleKeys: string[];
    withDisplayName: number;
    withoutDisplayName: number;
  }>;
  firstFiveModelEntries: Array<Record<string, unknown>>;
  suspiciousModelEntries: Array<Record<string, unknown>>;
  agentModelSorts: AntigravitySearchReport["agentModelSorts"];
};

export type AntigravityFetchDiagnosis = {
  diagnosedAt: string;
  projectId: string;
  planTier?: string;
  loadCodeAssistUsed: boolean;
  loadCodeAssist: {
    url: string;
    body: unknown;
    rawResponse: unknown;
    topLevelKeys: string[];
    searchReport: AntigravitySearchReport;
    relevantFields: Record<string, unknown>;
  };
  fetchVariants: AntigravityFetchVariantDiagnosis[];
  conclusions: string[];
  parserAudit: {
    currentParserPath: string;
    agentModelSortsPresent: boolean;
    recommendedIdsCount: number;
    modelsWithDisplayName: number;
    modelsWithoutDisplayName: number;
    chatLikeIdsInModels: string[];
  };
};
