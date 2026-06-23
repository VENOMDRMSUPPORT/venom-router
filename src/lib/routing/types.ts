export type Modality = "text" | "vision" | "audio" | "documents";

/** Derived from model cost per million tokens. */
export type CostType = "free" | "cheap" | "balanced" | "premium";

/**
 * Task classification derived from request messages.
 * Used to weight scoring and select escalation stage entry points.
 */
export type TaskClass =
  | "simple_chat"
  | "coding"
  | "vision"
  | "tool_calling"
  | "long_context"
  | "reasoning_heavy"
  | "agentic_task"
  | "critical_task";

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
  /** Derived at engine time — not stored in DB. */
  costType?: CostType;
  /** Derived quality score (0–1) from priority. Higher priority = higher quality. */
  qualityScore?: number;
}

export interface ScoredCandidate {
  candidate: RoutingCandidate;
  score: number;
}

export interface RoutingTraceCandidate {
  rule_id: string;
  external_id: string;
  adapter: string;
  priority: number;
  role: string;
  score?: number;
  status: "eligible" | "filtered" | "attempted" | "selected";
  filter_reason?: string;
  error?: string;
}

export interface RoutingTrace {
  modality: Modality;
  task_class: TaskClass;
  candidates_evaluated: number;
  candidates_filtered: number;
  decision_reason: string;
  selected_rule_id: string | null;
  fallback_attempts: number;
  escalation_stage: number;
  candidates: RoutingTraceCandidate[];
  cost_usd: number;
}

export interface RoutingRequest {
  venomSlug: "lite" | "pro" | "max";
  messages: import("@/lib/providers/adapters/types").ChatMessage[];
  maxTokens?: number;
  temperature?: number;
  apiKeyId?: string;
  includeTrace?: boolean;
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
  trace?: RoutingTrace;
  costUsd?: number;
}
