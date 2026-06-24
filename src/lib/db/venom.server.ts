import type { SupabaseClient } from "@supabase/supabase-js";
import type { RoutingCondition } from "@/lib/routing/types";
import type { TierStrategyConfig } from "@/lib/routing/strategy.types";

// ── Types ──────────────────────────────────────────────────────────────────────

export type VenomModel = {
  slug: "lite" | "pro" | "max";
  display_name: string;
  description: string | null;
  weight_cost: number;
  weight_speed: number;
  weight_quality: number;
  max_fallback_attempts: number;
  timeout_ms: number;
  strategy_config: TierStrategyConfig | Record<string, unknown>;
};

export type RoutingRule = {
  id: string;
  venom_slug: "lite" | "pro" | "max";
  model_id: string;
  account_id: string;
  priority: number;
  active: boolean;
  role: string;
  condition: RoutingCondition | null;
  model_external_id: string;
  model_display_name: string;
  provider_slug: string;
  provider_name: string;
  account_email: string | null;
  account_label: string | null;
};

// ── Exported functions ─────────────────────────────────────────────────────────

export async function getVenomModel(
  supabase: SupabaseClient,
  slug: "lite" | "pro" | "max",
): Promise<VenomModel> {
  const { data, error } = await supabase
    .from("venom_models")
    .select(
      "slug,display_name,description,weight_cost,weight_speed,weight_quality,max_fallback_attempts,timeout_ms,strategy_config",
    )
    .eq("slug", slug)
    .single();
  if (error || !data) throw new Error(`getVenomModel: ${error?.message ?? "not found"}`);
  return data as unknown as VenomModel;
}

export async function listVenomModels(supabase: SupabaseClient): Promise<VenomModel[]> {
  const { data, error } = await supabase
    .from("venom_models")
    .select(
      "slug,display_name,description,weight_cost,weight_speed,weight_quality,max_fallback_attempts,timeout_ms,strategy_config",
    )
    .order("slug");
  if (error) throw new Error(`listVenomModels: ${error.message}`);
  return (data ?? []) as unknown as VenomModel[];
}

export async function listRoutingRules(
  supabase: SupabaseClient,
  opts?: { venomSlug?: "lite" | "pro" | "max"; activeOnly?: boolean },
): Promise<RoutingRule[]> {
  let q = supabase
    .from("routing_rules")
    .select(
      "id,venom_slug,model_id,account_id,priority,active,role,condition,models!inner(external_id,display_name,providers!inner(slug,name)),accounts!inner(email,label)",
    )
    .order("priority", { ascending: false });
  if (opts?.venomSlug) q = (q as any).eq("venom_slug", opts.venomSlug);
  if (opts?.activeOnly) q = (q as any).eq("active", true);

  const { data, error } = await q;
  if (error) throw new Error(`listRoutingRules: ${error.message}`);

  return ((data ?? []) as any[]).map((row) => ({
    id: row.id,
    venom_slug: row.venom_slug as "lite" | "pro" | "max",
    model_id: row.model_id,
    account_id: row.account_id,
    priority: row.priority,
    active: row.active,
    role: row.role ?? "",
    condition: (row.condition as RoutingCondition | null) ?? null,
    model_external_id: row.models?.external_id ?? "",
    model_display_name: row.models?.display_name ?? "",
    provider_slug: row.models?.providers?.slug ?? "",
    provider_name: row.models?.providers?.name ?? "",
    account_email: row.accounts?.email ?? null,
    account_label: row.accounts?.label ?? null,
  }));
}
