
-- Extend providers
ALTER TABLE public.providers
  ADD COLUMN IF NOT EXISTS slug text UNIQUE,
  ADD COLUMN IF NOT EXISTS category text NOT NULL DEFAULT 'oauth' CHECK (category IN ('oauth','free')),
  ADD COLUMN IF NOT EXISTS auth_type text NOT NULL DEFAULT 'api_key' CHECK (auth_type IN ('oauth2_pkce','oauth2_secret','api_key')),
  ADD COLUMN IF NOT EXISTS description text,
  ADD COLUMN IF NOT EXISTS homepage text,
  ADD COLUMN IF NOT EXISTS is_builtin boolean NOT NULL DEFAULT false;

-- Extend accounts (account-level identity, plan, quota, oauth state)
ALTER TABLE public.accounts
  ADD COLUMN IF NOT EXISTS email text,
  ADD COLUMN IF NOT EXISTS plan text,
  ADD COLUMN IF NOT EXISTS quota_used numeric,
  ADD COLUMN IF NOT EXISTS quota_total numeric,
  ADD COLUMN IF NOT EXISTS quota_unit text,
  ADD COLUMN IF NOT EXISTS quota_extra jsonb,
  ADD COLUMN IF NOT EXISTS last_synced_at timestamptz;

-- Extend models with test/enable flags
ALTER TABLE public.models
  ADD COLUMN IF NOT EXISTS account_id uuid REFERENCES public.accounts(id) ON DELETE CASCADE,
  ADD COLUMN IF NOT EXISTS latency_ms integer,
  ADD COLUMN IF NOT EXISTS test_status text NOT NULL DEFAULT 'untested' CHECK (test_status IN ('untested','working','failed')),
  ADD COLUMN IF NOT EXISTS enabled boolean NOT NULL DEFAULT true,
  ADD COLUMN IF NOT EXISTS last_test_error text;

CREATE INDEX IF NOT EXISTS models_account_id_idx ON public.models(account_id);

-- Transient OAuth flow store (PKCE verifier + state, valid 10 min)
CREATE TABLE IF NOT EXISTS public.oauth_flows (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  provider_slug text NOT NULL,
  code_verifier text,
  state text NOT NULL,
  redirect_uri text,
  extra jsonb,
  created_at timestamptz NOT NULL DEFAULT now()
);
GRANT SELECT, INSERT, UPDATE, DELETE ON public.oauth_flows TO authenticated;
GRANT ALL ON public.oauth_flows TO service_role;
ALTER TABLE public.oauth_flows ENABLE ROW LEVEL SECURITY;
CREATE POLICY "owner all oauth_flows" ON public.oauth_flows FOR ALL
  USING (public.is_owner()) WITH CHECK (public.is_owner());

-- Seed 3 built-in providers (idempotent via slug)
INSERT INTO public.providers (slug, name, kind, adapter, base_url, category, auth_type, description, homepage, is_builtin)
VALUES
  ('claude-code', 'Claude Code', 'oauth', 'claude-code', 'https://api.anthropic.com',
    'oauth', 'oauth2_pkce',
    'Anthropic Claude via the Claude Code OAuth flow. Sign in with your claude.ai account to access Opus, Sonnet and Haiku.',
    'claude.ai', true),
  ('antigravity', 'Antigravity', 'oauth', 'antigravity', 'https://cloudcode-pa.googleapis.com',
    'oauth', 'oauth2_secret',
    'Google Antigravity IDE — sign in with Google to access Gemini, Claude and GPT-OSS models via the Cloud Code Assist API.',
    'antigravity.google', true),
  ('opencode-zen', 'OpenCode Zen', 'free', 'opencode-zen', 'https://opencode.ai/zen',
    'free', 'api_key',
    'OpenAI-compatible free gateway from OpenCode. Add your zen API key to access deepseek, nemotron and other free models.',
    'opencode.ai', true)
ON CONFLICT (slug) DO UPDATE SET
  name = EXCLUDED.name,
  kind = EXCLUDED.kind,
  adapter = EXCLUDED.adapter,
  base_url = EXCLUDED.base_url,
  category = EXCLUDED.category,
  auth_type = EXCLUDED.auth_type,
  description = EXCLUDED.description,
  homepage = EXCLUDED.homepage,
  is_builtin = true,
  updated_at = now();
