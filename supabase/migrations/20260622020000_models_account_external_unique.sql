-- Models are per-account; the same external_id exists on every Antigravity account.
ALTER TABLE public.models
  DROP CONSTRAINT IF EXISTS models_provider_id_external_id_key;

ALTER TABLE public.models
  ADD CONSTRAINT models_account_id_external_id_key UNIQUE (account_id, external_id);
