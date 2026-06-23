-- Catalog-level models + per-account account_models junction table.

-- 1. Create account_models
CREATE TABLE IF NOT EXISTS public.account_models (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id      UUID NOT NULL REFERENCES public.accounts(id) ON DELETE CASCADE,
  model_id        UUID NOT NULL REFERENCES public.models(id) ON DELETE CASCADE,
  enabled         BOOLEAN NOT NULL DEFAULT true,
  test_status     TEXT NOT NULL DEFAULT 'untested' CHECK (test_status IN ('untested', 'working', 'failed')),
  lifecycle       public.model_lifecycle NOT NULL DEFAULT 'discovered',
  latency_ms      INT,
  last_test_error TEXT,
  last_tested_at  TIMESTAMPTZ,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (account_id, model_id)
);

CREATE INDEX IF NOT EXISTS account_models_account_id_idx ON public.account_models(account_id);
CREATE INDEX IF NOT EXISTS account_models_model_id_idx ON public.account_models(model_id);

GRANT SELECT, INSERT, UPDATE, DELETE ON public.account_models TO authenticated;
GRANT ALL ON public.account_models TO service_role;
ALTER TABLE public.account_models ENABLE ROW LEVEL SECURITY;
CREATE POLICY "owner all account_models" ON public.account_models
  FOR ALL TO authenticated USING (public.is_owner()) WITH CHECK (public.is_owner());
CREATE TRIGGER account_models_updated
  BEFORE UPDATE ON public.account_models
  FOR EACH ROW EXECUTE FUNCTION public.set_updated_at();

-- 2. Helper: canonical provider external id
CREATE OR REPLACE FUNCTION public.canonical_provider_external_id(ext_id TEXT, caps JSONB)
RETURNS TEXT
LANGUAGE sql
IMMUTABLE
AS $$
  SELECT COALESCE(
    NULLIF(TRIM(caps->>'provider_external_id'), ''),
    CASE
      WHEN ext_id LIKE 'acct:%' THEN regexp_replace(ext_id, '^acct:[^:]+:', '')
      ELSE ext_id
    END
  );
$$;

-- 3. Map each legacy model row to a canonical catalog row per (provider_id, external_id)
CREATE TEMP TABLE _model_canonical_map ON COMMIT DROP AS
WITH ranked AS (
  SELECT
    m.id AS old_id,
    m.provider_id,
    m.account_id,
    m.enabled,
    m.test_status,
    m.lifecycle,
    m.latency_ms,
    m.last_test_error,
    m.last_tested_at,
    public.canonical_provider_external_id(m.external_id, m.capabilities) AS canon_ext,
    ROW_NUMBER() OVER (
      PARTITION BY m.provider_id, public.canonical_provider_external_id(m.external_id, m.capabilities)
      ORDER BY
        CASE m.test_status WHEN 'working' THEN 0 WHEN 'untested' THEN 1 ELSE 2 END,
        m.updated_at DESC,
        m.id
    ) AS rn
  FROM public.models m
  WHERE m.account_id IS NOT NULL
),
keepers AS (
  SELECT provider_id, canon_ext, old_id AS canonical_id
  FROM ranked
  WHERE rn = 1
)
SELECT
  r.old_id,
  k.canonical_id,
  r.account_id,
  r.enabled,
  r.test_status,
  r.lifecycle,
  r.latency_ms,
  r.last_test_error,
  r.last_tested_at,
  r.canon_ext,
  r.provider_id
FROM ranked r
JOIN keepers k ON k.provider_id = r.provider_id AND k.canon_ext = r.canon_ext;

-- 4. Normalize canonical catalog rows
UPDATE public.models m
SET
  external_id = map.canon_ext,
  capabilities = (m.capabilities - 'stale' - 'stale_reason')
    || jsonb_build_object('provider_external_id', map.canon_ext)
FROM (
  SELECT DISTINCT canonical_id, canon_ext FROM _model_canonical_map
) map
WHERE m.id = map.canonical_id;

-- 5. Insert account_models links
INSERT INTO public.account_models (
  account_id, model_id, enabled, test_status, lifecycle,
  latency_ms, last_test_error, last_tested_at
)
SELECT
  map.account_id,
  map.canonical_id,
  map.enabled,
  map.test_status,
  map.lifecycle,
  map.latency_ms,
  map.last_test_error,
  map.last_tested_at
FROM _model_canonical_map map
ON CONFLICT (account_id, model_id) DO UPDATE SET
  enabled = EXCLUDED.enabled,
  test_status = EXCLUDED.test_status,
  lifecycle = EXCLUDED.lifecycle,
  latency_ms = EXCLUDED.latency_ms,
  last_test_error = EXCLUDED.last_test_error,
  last_tested_at = EXCLUDED.last_tested_at;

-- 6. Repoint routing_rules to canonical model ids
UPDATE public.routing_rules rr
SET model_id = map.canonical_id
FROM _model_canonical_map map
WHERE rr.model_id = map.old_id
  AND rr.model_id <> map.canonical_id;

-- 7. Repoint model_tests if any
UPDATE public.model_tests mt
SET model_id = map.canonical_id
FROM _model_canonical_map map
WHERE mt.model_id = map.old_id
  AND mt.model_id <> map.canonical_id;

-- 8. Delete duplicate catalog rows
DELETE FROM public.models m
USING _model_canonical_map map
WHERE m.id = map.old_id
  AND m.id <> map.canonical_id;

-- 9. Drop per-account columns from models (catalog only)
ALTER TABLE public.models DROP CONSTRAINT IF EXISTS models_account_id_external_id_key;
ALTER TABLE public.models DROP CONSTRAINT IF EXISTS models_account_id_fkey;

ALTER TABLE public.models
  DROP COLUMN IF EXISTS account_id,
  DROP COLUMN IF EXISTS enabled,
  DROP COLUMN IF EXISTS test_status,
  DROP COLUMN IF EXISTS latency_ms,
  DROP COLUMN IF EXISTS last_test_error;

-- Catalog lifecycle defaults to discovered; per-account lifecycle lives on account_models
UPDATE public.models SET lifecycle = 'discovered' WHERE lifecycle IS NULL;

-- 10. Restore provider-level uniqueness
ALTER TABLE public.models
  DROP CONSTRAINT IF EXISTS models_provider_id_external_id_key;
ALTER TABLE public.models
  ADD CONSTRAINT models_provider_id_external_id_key UNIQUE (provider_id, external_id);

DROP FUNCTION IF EXISTS public.canonical_provider_external_id(TEXT, JSONB);
