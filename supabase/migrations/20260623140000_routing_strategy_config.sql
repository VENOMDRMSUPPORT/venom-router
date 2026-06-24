-- Tier-level routing strategy defaults (UI + future engine consumption)
ALTER TABLE public.venom_models
  ADD COLUMN IF NOT EXISTS strategy_config JSONB NOT NULL DEFAULT '{}';

-- Unify routing_rules condition column: backfill from legacy `conditions` if present
UPDATE public.routing_rules
SET condition = conditions
WHERE condition IS NULL AND conditions IS NOT NULL AND conditions != '{}'::jsonb;
