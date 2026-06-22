/* Structured syncAccount response — readable in DevTools, self-contained for UI cache updates. */

export type SyncAccountStatus = "healthy" | "degraded" | "expired";

export interface SyncQuotaGroupResponse {
  name: string;
  short_label: string;
  model_count: number;
  five_hour?: {
    remaining_pct: number;
    reset_at: string;
    exhausted: boolean;
  };
}

export interface SyncAccountResponse {
  ok: true;
  account_id: string;
  provider_slug: string;
  synced_at: string;

  account: {
    email: string | null;
    label: string;
    plan: string | null;
    status: SyncAccountStatus;
    last_synced_at: string;
    last_health_check_at: string;
    quota_used: number | null;
    quota_total: number | null;
    quota_unit: string | null;
    quota_extra: Record<string, unknown> | null;
  };

  health: {
    ok: boolean;
    latency_ms: number;
    checked_at: string;
    error?: string;
  };

  models: {
    fetched: number;
    added: number;
    updated: number;
    removed: number;
    enabled: number;
    total: number;
  };

  quota: {
    synced: boolean;
    used: number | null;
    total: number | null;
    unit: string | null;
    groups: SyncQuotaGroupResponse[];
  };

  meta: {
    provider_calls: string[];
    db_writes: string[];
    duration_ms: number;
  };
}

export interface SyncAccountErrorResponse {
  ok: false;
  account_id: string;
  synced_at: string;
  error: {
    code: string;
    message: string;
    health?: { ok: false; latency_ms: number; error: string };
  };
}

export type SyncAccountResult = SyncAccountResponse | SyncAccountErrorResponse;
