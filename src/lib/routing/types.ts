export type Modality = "text" | "vision" | "audio" | "documents";

export interface RoutingCondition {
  requires?: string[];
  min_context_tokens?: number;
  quota_risk?: "low" | "medium" | "high";
}

export interface VenomWeights {
  costWeight: number;
  speedWeight: number;
  qualityWeight: number;
  maxFallbackAttempts: number;
}

export interface RoutingCandidate {
  ruleId: string;
  priority: number;
  role: string;
  condition: RoutingCondition | null;
  model: {
    id: string;
    externalId: string;
    lifecycle: string;
    enabled: boolean;
    inputCostPerMtok: number | null;
    outputCostPerMtok: number | null;
    capabilities: string[];
    latencyMs: number | null;
    provider: { adapter: string; baseUrl: string | null };
  };
  account: {
    id: string;
    status: string;
    credentialsEnc: unknown;
    credentialsIv: unknown;
    credentialsTag: unknown;
    quota: { used: number; total: number | null; confidence: string } | null;
  };
}

export interface ScoredCandidate {
  candidate: RoutingCandidate;
  score: number;
}

export interface RoutingRequest {
  venomSlug: "lite" | "pro" | "max";
  messages: import("@/lib/providers/adapters/types").ChatMessage[];
  maxTokens?: number;
  temperature?: number;
  apiKeyId?: string;
}

export interface RoutingResult {
  success: boolean;
  content?: string;
  inputTokens: number;
  outputTokens: number;
  latencyMs: number;
  fallbackUsed: boolean;
  fallbackCount: number;
  errorCode?: string;
  selectedRuleId?: string;
  modality: Modality;
  providerAdapter?: string;
}
