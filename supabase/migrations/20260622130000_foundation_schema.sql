-- ============================================================
-- Phase 2: Foundation Schema
-- Adds missing columns to existing tables and creates new
-- tables required by the routing engine, workers, and pages.
-- All statements are idempotent (IF NOT EXISTS).
-- ============================================================

-- ----------------------------------------------------------
-- 1. Extend existing tables
-- ----------------------------------------------------------

-- models: cost data + blocked reason
ALTER TABLE public.models
  ADD COLUMN IF NOT EXISTS input_cost_per_mtok  NUMERIC,
  ADD COLUMN IF NOT EXISTS output_cost_per_mtok NUMERIC,
  ADD COLUMN IF NOT EXISTS max_output_tokens    INTEGER,
  ADD COLUMN IF NOT EXISTS blocked_reason       TEXT;

-- routing_rules: condition jsonb + role + fallback config
ALTER TABLE public.routing_rules
  ADD COLUMN IF NOT EXISTS condition             JSONB,
  ADD COLUMN IF NOT EXISTS role                  TEXT    NOT NULL DEFAULT 'primary',
  ADD COLUMN IF NOT EXISTS max_fallback_attempts INTEGER NOT NULL DEFAULT 3;

-- venom_models: routing weights + description
ALTER TABLE public.venom_models
  ADD COLUMN IF NOT EXISTS cost_weight    NUMERIC NOT NULL DEFAULT 0.3,
  ADD COLUMN IF NOT EXISTS speed_weight   NUMERIC NOT NULL DEFAULT 0.3,
  ADD COLUMN IF NOT EXISTS quality_weight NUMERIC NOT NULL DEFAULT 0.4,
  ADD COLUMN IF NOT EXISTS description    TEXT;

-- ----------------------------------------------------------
-- 2. Seed venom_models default weights
-- ----------------------------------------------------------
INSERT INTO public.venom_models (slug, display_name, cost_weight, speed_weight, quality_weight, description)
VALUES
  ('lite', 'Venom Lite', 0.7, 0.2, 0.1, 'Optimized for cost — fastest, cheapest routing'),
  ('pro',  'Venom Pro',  0.3, 0.3, 0.4, 'Balanced — quality-leaning for production use'),
  ('max',  'Venom Max',  0.1, 0.1, 0.8, 'Optimized for quality — best model available')
ON CONFLICT (slug) DO UPDATE SET
  cost_weight    = EXCLUDED.cost_weight,
  speed_weight   = EXCLUDED.speed_weight,
  quality_weight = EXCLUDED.quality_weight,
  description    = EXCLUDED.description;

-- ----------------------------------------------------------
-- 3. New table: account_health_checks
-- ----------------------------------------------------------
CREATE TABLE IF NOT EXISTS public.account_health_checks (
  id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id    UUID        NOT NULL REFERENCES public.accounts(id) ON DELETE CASCADE,
  checked_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  status        TEXT        NOT NULL, -- healthy | degraded | unreachable
  latency_ms    INTEGER,
  error_code    TEXT,
  error_message TEXT
);

CREATE INDEX IF NOT EXISTS account_health_checks_account_id_idx
  ON public.account_health_checks (account_id, checked_at DESC);

-- ----------------------------------------------------------
-- 4. New table: quota_snapshots
-- ----------------------------------------------------------
CREATE TABLE IF NOT EXISTS public.quota_snapshots (
  id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id   UUID        NOT NULL REFERENCES public.accounts(id) ON DELETE CASCADE,
  snapped_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  quota_type   TEXT        NOT NULL, -- tokens | requests | spend
  period       TEXT        NOT NULL, -- daily | monthly | rolling
  used         NUMERIC,
  total        NUMERIC,
  remaining    NUMERIC,
  resets_at    TIMESTAMPTZ,
  quota_source TEXT        NOT NULL DEFAULT 'locally_estimated',
                                    -- provider_reported | locally_estimated | manual
  confidence   TEXT        NOT NULL DEFAULT 'unknown'
                                    -- high | medium | low | unknown
);

CREATE INDEX IF NOT EXISTS quota_snapshots_account_id_idx
  ON public.quota_snapshots (account_id, snapped_at DESC);

-- ----------------------------------------------------------
-- 5. New table: routing_traces
-- (stores decision metadata only — NO provider secrets)
-- ----------------------------------------------------------
CREATE TABLE IF NOT EXISTS public.routing_traces (
  id                   UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  usage_record_id      UUID,
  candidates_evaluated INTEGER     NOT NULL DEFAULT 0,
  candidates_filtered  INTEGER     NOT NULL DEFAULT 0,
  selected_rule_id     UUID,
  decision_reason      TEXT,
  fallback_attempts    INTEGER     NOT NULL DEFAULT 0,
  modality             TEXT        NOT NULL DEFAULT 'text',
                                   -- text | vision | audio | documents
  created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS routing_traces_usage_record_idx
  ON public.routing_traces (usage_record_id);

CREATE INDEX IF NOT EXISTS routing_traces_created_at_idx
  ON public.routing_traces (created_at DESC);

-- ----------------------------------------------------------
-- 6. New table: system_settings (single-row config)
-- ----------------------------------------------------------
CREATE TABLE IF NOT EXISTS public.system_settings (
  id                            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  system_name                   TEXT        NOT NULL DEFAULT 'Venom Router',
  default_request_timeout_ms    INTEGER     NOT NULL DEFAULT 30000,
  default_max_fallback_attempts INTEGER     NOT NULL DEFAULT 3,
  health_check_interval_minutes INTEGER     NOT NULL DEFAULT 5,
  quota_warning_threshold_pct   INTEGER     NOT NULL DEFAULT 15,
  quota_critical_threshold_pct  INTEGER     NOT NULL DEFAULT 5,
  routing_trace_retention_days  INTEGER     NOT NULL DEFAULT 30,
  usage_record_retention_days   INTEGER     NOT NULL DEFAULT 90,
  updated_at                    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Seed one row if table is empty
INSERT INTO public.system_settings (id)
SELECT gen_random_uuid()
WHERE NOT EXISTS (SELECT 1 FROM public.system_settings);

-- ----------------------------------------------------------
-- 7. New table: audit_log_entries
-- ----------------------------------------------------------
CREATE TABLE IF NOT EXISTS public.audit_log_entries (
  id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  actor       TEXT,
  action      TEXT        NOT NULL,
  target      TEXT,
  metadata    JSONB,
  success     BOOLEAN     NOT NULL DEFAULT TRUE
);

CREATE INDEX IF NOT EXISTS audit_log_entries_occurred_at_idx
  ON public.audit_log_entries (occurred_at DESC);
