
-- Roles
CREATE TYPE public.app_role AS ENUM ('owner');

CREATE TABLE public.user_roles (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES auth.users(id) ON DELETE CASCADE,
  role public.app_role NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (user_id, role)
);
GRANT SELECT ON public.user_roles TO authenticated;
GRANT ALL ON public.user_roles TO service_role;
ALTER TABLE public.user_roles ENABLE ROW LEVEL SECURITY;
CREATE POLICY "owner can read roles" ON public.user_roles FOR SELECT TO authenticated USING (auth.uid() = user_id);

CREATE OR REPLACE FUNCTION public.has_role(_user_id UUID, _role public.app_role)
RETURNS BOOLEAN LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public AS $$
  SELECT EXISTS (SELECT 1 FROM public.user_roles WHERE user_id = _user_id AND role = _role)
$$;

CREATE OR REPLACE FUNCTION public.is_owner() RETURNS BOOLEAN
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public AS $$
  SELECT public.has_role(auth.uid(), 'owner')
$$;

-- Auto-claim owner role for the first user that signs up
CREATE OR REPLACE FUNCTION public.handle_first_user()
RETURNS TRIGGER LANGUAGE plpgsql SECURITY DEFINER SET search_path = public AS $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM public.user_roles WHERE role = 'owner') THEN
    INSERT INTO public.user_roles (user_id, role) VALUES (NEW.id, 'owner');
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER on_auth_user_created_assign_owner
AFTER INSERT ON auth.users FOR EACH ROW EXECUTE FUNCTION public.handle_first_user();

-- updated_at helper
CREATE OR REPLACE FUNCTION public.set_updated_at()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN NEW.updated_at = now(); RETURN NEW; END;
$$;

-- Enums
CREATE TYPE public.provider_kind AS ENUM ('oauth','free','paid','custom');
CREATE TYPE public.account_status AS ENUM ('healthy','degraded','expired','revoked','unknown');
CREATE TYPE public.quota_strategy AS ENUM ('provider_api','local_estimation','manual');
CREATE TYPE public.quota_confidence AS ENUM ('high','medium','low');
CREATE TYPE public.model_lifecycle AS ENUM ('discovered','tested','approved','blocked');
CREATE TYPE public.venom_slug AS ENUM ('lite','pro','max');
CREATE TYPE public.rule_role AS ENUM ('primary','fallback');

-- Providers
CREATE TABLE public.providers (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  kind public.provider_kind NOT NULL,
  name TEXT NOT NULL,
  adapter TEXT NOT NULL,
  base_url TEXT,
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
GRANT SELECT, INSERT, UPDATE, DELETE ON public.providers TO authenticated;
GRANT ALL ON public.providers TO service_role;
ALTER TABLE public.providers ENABLE ROW LEVEL SECURITY;
CREATE POLICY "owner all providers" ON public.providers FOR ALL TO authenticated USING (public.is_owner()) WITH CHECK (public.is_owner());
CREATE TRIGGER providers_updated BEFORE UPDATE ON public.providers FOR EACH ROW EXECUTE FUNCTION public.set_updated_at();

-- Accounts
CREATE TABLE public.accounts (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  provider_id UUID NOT NULL REFERENCES public.providers(id) ON DELETE CASCADE,
  label TEXT NOT NULL,
  auth_type TEXT NOT NULL DEFAULT 'api_key',
  credentials_enc BYTEA,
  credentials_iv BYTEA,
  credentials_tag BYTEA,
  status public.account_status NOT NULL DEFAULT 'unknown',
  quota_strategy public.quota_strategy NOT NULL DEFAULT 'local_estimation',
  last_health_check_at TIMESTAMPTZ,
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
GRANT SELECT, INSERT, UPDATE, DELETE ON public.accounts TO authenticated;
GRANT ALL ON public.accounts TO service_role;
ALTER TABLE public.accounts ENABLE ROW LEVEL SECURITY;
CREATE POLICY "owner all accounts" ON public.accounts FOR ALL TO authenticated USING (public.is_owner()) WITH CHECK (public.is_owner());
CREATE TRIGGER accounts_updated BEFORE UPDATE ON public.accounts FOR EACH ROW EXECUTE FUNCTION public.set_updated_at();

-- Models
CREATE TABLE public.models (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  provider_id UUID NOT NULL REFERENCES public.providers(id) ON DELETE CASCADE,
  external_id TEXT NOT NULL,
  display_name TEXT NOT NULL,
  capabilities JSONB NOT NULL DEFAULT '{}'::jsonb,
  context_window INT,
  input_cost_per_mtok NUMERIC(12,4),
  output_cost_per_mtok NUMERIC(12,4),
  quality_rating INT NOT NULL DEFAULT 50,
  lifecycle public.model_lifecycle NOT NULL DEFAULT 'discovered',
  last_tested_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (provider_id, external_id)
);
GRANT SELECT, INSERT, UPDATE, DELETE ON public.models TO authenticated;
GRANT ALL ON public.models TO service_role;
ALTER TABLE public.models ENABLE ROW LEVEL SECURITY;
CREATE POLICY "owner all models" ON public.models FOR ALL TO authenticated USING (public.is_owner()) WITH CHECK (public.is_owner());
CREATE TRIGGER models_updated BEFORE UPDATE ON public.models FOR EACH ROW EXECUTE FUNCTION public.set_updated_at();

-- Model tests
CREATE TABLE public.model_tests (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  model_id UUID NOT NULL REFERENCES public.models(id) ON DELETE CASCADE,
  account_id UUID REFERENCES public.accounts(id) ON DELETE SET NULL,
  status TEXT NOT NULL,
  latency_ms INT,
  error TEXT,
  tested_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
GRANT SELECT, INSERT, UPDATE, DELETE ON public.model_tests TO authenticated;
GRANT ALL ON public.model_tests TO service_role;
ALTER TABLE public.model_tests ENABLE ROW LEVEL SECURITY;
CREATE POLICY "owner all model_tests" ON public.model_tests FOR ALL TO authenticated USING (public.is_owner()) WITH CHECK (public.is_owner());

-- Venom models
CREATE TABLE public.venom_models (
  slug public.venom_slug PRIMARY KEY,
  display_name TEXT NOT NULL,
  description TEXT,
  weight_cost NUMERIC(4,3) NOT NULL DEFAULT 0.33,
  weight_speed NUMERIC(4,3) NOT NULL DEFAULT 0.33,
  weight_quality NUMERIC(4,3) NOT NULL DEFAULT 0.34,
  max_fallback_attempts INT NOT NULL DEFAULT 3,
  timeout_ms INT NOT NULL DEFAULT 30000,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
GRANT SELECT, INSERT, UPDATE, DELETE ON public.venom_models TO authenticated;
GRANT ALL ON public.venom_models TO service_role;
ALTER TABLE public.venom_models ENABLE ROW LEVEL SECURITY;
CREATE POLICY "owner all venom_models" ON public.venom_models FOR ALL TO authenticated USING (public.is_owner()) WITH CHECK (public.is_owner());
CREATE TRIGGER venom_models_updated BEFORE UPDATE ON public.venom_models FOR EACH ROW EXECUTE FUNCTION public.set_updated_at();

INSERT INTO public.venom_models (slug, display_name, description, weight_cost, weight_speed, weight_quality) VALUES
  ('lite','Venom Lite','Fast & cheap for daily tasks',0.55,0.35,0.10),
  ('pro','Venom Pro','Balanced for coding & analysis',0.25,0.25,0.50),
  ('max','Venom Max','Maximum quality for deep work',0.10,0.15,0.75);

-- Routing rules
CREATE TABLE public.routing_rules (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  venom_slug public.venom_slug NOT NULL REFERENCES public.venom_models(slug) ON DELETE CASCADE,
  model_id UUID NOT NULL REFERENCES public.models(id) ON DELETE CASCADE,
  account_id UUID NOT NULL REFERENCES public.accounts(id) ON DELETE CASCADE,
  role public.rule_role NOT NULL DEFAULT 'primary',
  priority INT NOT NULL DEFAULT 100,
  conditions JSONB NOT NULL DEFAULT '{}'::jsonb,
  active BOOLEAN NOT NULL DEFAULT true,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
GRANT SELECT, INSERT, UPDATE, DELETE ON public.routing_rules TO authenticated;
GRANT ALL ON public.routing_rules TO service_role;
ALTER TABLE public.routing_rules ENABLE ROW LEVEL SECURITY;
CREATE POLICY "owner all routing_rules" ON public.routing_rules FOR ALL TO authenticated USING (public.is_owner()) WITH CHECK (public.is_owner());
CREATE TRIGGER routing_rules_updated BEFORE UPDATE ON public.routing_rules FOR EACH ROW EXECUTE FUNCTION public.set_updated_at();

-- Routing traces (sanitized)
CREATE TABLE public.routing_traces (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  request_id UUID NOT NULL,
  venom_slug public.venom_slug NOT NULL,
  candidates JSONB NOT NULL DEFAULT '[]'::jsonb,
  selected_rule_id UUID,
  fallback_chain JSONB NOT NULL DEFAULT '[]'::jsonb,
  reason TEXT,
  success BOOLEAN NOT NULL DEFAULT false,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
GRANT SELECT, INSERT, UPDATE, DELETE ON public.routing_traces TO authenticated;
GRANT ALL ON public.routing_traces TO service_role;
ALTER TABLE public.routing_traces ENABLE ROW LEVEL SECURITY;
CREATE POLICY "owner all routing_traces" ON public.routing_traces FOR ALL TO authenticated USING (public.is_owner()) WITH CHECK (public.is_owner());
CREATE INDEX routing_traces_created_idx ON public.routing_traces (created_at DESC);

-- Usage records
CREATE TABLE public.usage_records (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  request_id UUID NOT NULL,
  venom_slug public.venom_slug NOT NULL,
  rule_id UUID REFERENCES public.routing_rules(id) ON DELETE SET NULL,
  account_id UUID REFERENCES public.accounts(id) ON DELETE SET NULL,
  model_id UUID REFERENCES public.models(id) ON DELETE SET NULL,
  api_key_id UUID,
  input_tokens INT NOT NULL DEFAULT 0,
  output_tokens INT NOT NULL DEFAULT 0,
  cost_usd NUMERIC(12,6) NOT NULL DEFAULT 0,
  latency_ms INT,
  success BOOLEAN NOT NULL DEFAULT true,
  fallback_used BOOLEAN NOT NULL DEFAULT false,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
GRANT SELECT, INSERT, UPDATE, DELETE ON public.usage_records TO authenticated;
GRANT ALL ON public.usage_records TO service_role;
ALTER TABLE public.usage_records ENABLE ROW LEVEL SECURITY;
CREATE POLICY "owner all usage_records" ON public.usage_records FOR ALL TO authenticated USING (public.is_owner()) WITH CHECK (public.is_owner());
CREATE INDEX usage_records_created_idx ON public.usage_records (created_at DESC);
CREATE INDEX usage_records_venom_idx ON public.usage_records (venom_slug, created_at DESC);

-- Quotas
CREATE TABLE public.quotas (
  account_id UUID PRIMARY KEY REFERENCES public.accounts(id) ON DELETE CASCADE,
  used NUMERIC(18,4) NOT NULL DEFAULT 0,
  total NUMERIC(18,4),
  unit TEXT NOT NULL DEFAULT 'usd',
  confidence public.quota_confidence NOT NULL DEFAULT 'medium',
  source TEXT NOT NULL DEFAULT 'local_estimation',
  resets_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
GRANT SELECT, INSERT, UPDATE, DELETE ON public.quotas TO authenticated;
GRANT ALL ON public.quotas TO service_role;
ALTER TABLE public.quotas ENABLE ROW LEVEL SECURITY;
CREATE POLICY "owner all quotas" ON public.quotas FOR ALL TO authenticated USING (public.is_owner()) WITH CHECK (public.is_owner());
CREATE TRIGGER quotas_updated BEFORE UPDATE ON public.quotas FOR EACH ROW EXECUTE FUNCTION public.set_updated_at();

-- Venom API keys
CREATE TABLE public.venom_api_keys (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name TEXT NOT NULL,
  key_hash TEXT NOT NULL UNIQUE,
  key_prefix TEXT NOT NULL,
  allowed_models public.venom_slug[] NOT NULL DEFAULT ARRAY['lite','pro','max']::public.venom_slug[],
  rpm_limit INT,
  tpd_limit INT,
  monthly_cap_usd NUMERIC(12,2),
  revoked_at TIMESTAMPTZ,
  last_used_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
GRANT SELECT, INSERT, UPDATE, DELETE ON public.venom_api_keys TO authenticated;
GRANT ALL ON public.venom_api_keys TO service_role;
ALTER TABLE public.venom_api_keys ENABLE ROW LEVEL SECURITY;
CREATE POLICY "owner all api_keys" ON public.venom_api_keys FOR ALL TO authenticated USING (public.is_owner()) WITH CHECK (public.is_owner());

-- Audit log
CREATE TABLE public.audit_log (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  actor_user_id UUID,
  action TEXT NOT NULL,
  target_type TEXT,
  target_id TEXT,
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
GRANT SELECT, INSERT, UPDATE, DELETE ON public.audit_log TO authenticated;
GRANT ALL ON public.audit_log TO service_role;
ALTER TABLE public.audit_log ENABLE ROW LEVEL SECURITY;
CREATE POLICY "owner read audit" ON public.audit_log FOR SELECT TO authenticated USING (public.is_owner());
CREATE POLICY "owner insert audit" ON public.audit_log FOR INSERT TO authenticated WITH CHECK (public.is_owner());
CREATE INDEX audit_log_created_idx ON public.audit_log (created_at DESC);

-- Settings
CREATE TABLE public.app_settings (
  key TEXT PRIMARY KEY,
  value JSONB NOT NULL DEFAULT '{}'::jsonb,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
GRANT SELECT, INSERT, UPDATE, DELETE ON public.app_settings TO authenticated;
GRANT ALL ON public.app_settings TO service_role;
ALTER TABLE public.app_settings ENABLE ROW LEVEL SECURITY;
CREATE POLICY "owner all settings" ON public.app_settings FOR ALL TO authenticated USING (public.is_owner()) WITH CHECK (public.is_owner());
CREATE TRIGGER app_settings_updated BEFORE UPDATE ON public.app_settings FOR EACH ROW EXECUTE FUNCTION public.set_updated_at();

INSERT INTO public.app_settings (key, value) VALUES
  ('general', '{"system_name":"Venom Router","environment":"production","timezone":"UTC"}'::jsonb),
  ('security', '{"session_timeout_minutes":1440,"require_reauth_destructive":true,"audit_log_enabled":true,"audit_retention_days":90}'::jsonb),
  ('routing', '{"request_timeout_ms":30000,"max_fallback_attempts":3,"retry_backoff_ms":500,"trace_retention_days":30,"usage_retention_days":365}'::jsonb),
  ('health', '{"check_interval_minutes":15,"quota_warning_pct":80,"quota_critical_pct":95,"notify_webhook":null,"notify_email":null}'::jsonb);
