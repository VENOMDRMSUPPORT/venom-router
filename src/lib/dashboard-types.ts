/**
 * Shared response types for the `/api/dashboard/*` REST endpoints.
 *
 * These describe the JSON shapes the dashboard router returns so that route
 * components get static typing instead of falling back to `any`. They are kept
 * in one place because several pages consume the same payloads (e.g. metrics).
 */

export interface DashboardChecklist {
  owner_created: boolean;
  provider_connected: boolean;
  routing_configured: boolean;
  api_key_issued: boolean;
  first_request_sent: boolean;
}

export interface TrafficPoint {
  day: string;
  requests: number;
}

export interface DistributionPoint {
  slug: string;
  requests: number;
}

export interface RecentActivityItem {
  id: string;
  kind: "request" | "sync";
  title: string;
  detail: string | null;
  status: "success" | "failure";
  created_at: string;
}

export interface ProviderHealthItem {
  account_id: string;
  provider_name: string;
  provider_slug: string;
  email: string | null;
  label: string | null;
  status: string;
  last_synced_at: string | null;
  models_enabled: number;
  quota_used: number | null;
  quota_unit: string | null;
}

export interface DashboardMetrics {
  kpis: {
    provider_models: number;
    venom_models: number;
    routing_rules: number;
    api_keys: number;
  };
  working_models: number;
  traffic_7d: TrafficPoint[];
  distribution: DistributionPoint[];
  checklist: DashboardChecklist;
  recent_activity: RecentActivityItem[];
  provider_health: ProviderHealthItem[];
  accounts_healthy: number;
  accounts_total: number;
}

// ── /api/dashboard/usage?period=7d|30d ──────────────────────────────────────

export interface UsageSummary {
  total_requests: number;
  total_tokens: number;
  total_cost_usd: number;
  success_rate: number;
  fallback_rate: number;
}

export interface UsagePeriodPoint {
  day: string;
  requests: number;
  tokens: number;
  cost_usd: number;
}

export interface UsageByModel {
  slug: string;
  requests: number;
  tokens: number;
  cost_usd: number;
}

export interface UsageRecord {
  id: string;
  venom_slug: string;
  cost_usd: number | null;
  input_tokens: number | null;
  output_tokens: number | null;
  success: boolean;
  fallback_used: boolean;
  created_at: string;
}

export interface UsageAnalytics {
  summary: UsageSummary;
  traffic: UsagePeriodPoint[];
  by_model: UsageByModel[];
  recent: UsageRecord[];
}

// ── /api/dashboard/diagnostics ──────────────────────────────────────────────

export interface DegradedAccount {
  id: string;
  label: string | null;
  email: string | null;
  status: string;
  provider_slug: string;
  provider_name: string;
  last_health_check_at: string | null;
  quota_extra: Record<string, unknown> | null;
}

export interface FailedTrace {
  id: string;
  request_id: string | null;
  venom_slug: string;
  reason: string;
  decision_reason: string | null;
  candidates_evaluated: number;
  candidates_filtered: number;
  fallback_attempts: number;
  created_at: string;
}

export interface DiagnosticsResponse {
  degraded_accounts: DegradedAccount[];
  failed_traces: FailedTrace[];
  health_check_runs: {
    total: number;
    healthy: number;
    degraded: number;
    unreachable: number;
  };
}
