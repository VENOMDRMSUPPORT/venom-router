import type { SupabaseClient } from "@supabase/supabase-js"

// ── Types ──────────────────────────────────────────────────────────────────────

export type VenomModel = {
  slug: "lite" | "pro" | "max"
  weight_cost: number
  weight_speed: number
  weight_quality: number
  max_fallback_attempts: number
  timeout_ms: number
}

export type RoutingRule = {
  id: string
  venom_slug: "lite" | "pro" | "max"
  model_id: string
  account_id: string
  priority: number
  active: boolean
  role: string
  model_external_id: string
  provider_slug: string
}

// ── Exported functions ─────────────────────────────────────────────────────────

export async function getVenomModel(
  supabase: SupabaseClient,
  slug: "lite" | "pro" | "max",
): Promise<VenomModel> {
  const { data, error } = await supabase
    .from("venom_models")
    .select("slug,weight_cost,weight_speed,weight_quality,max_fallback_attempts,timeout_ms")
    .eq("slug", slug)
    .single()
  if (error || !data) throw new Error(`getVenomModel: ${error?.message ?? "not found"}`)
  return data as unknown as VenomModel
}

export async function listVenomModels(supabase: SupabaseClient): Promise<VenomModel[]> {
  const { data, error } = await supabase
    .from("venom_models")
    .select("slug,weight_cost,weight_speed,weight_quality,max_fallback_attempts,timeout_ms")
    .order("slug")
  if (error) throw new Error(`listVenomModels: ${error.message}`)
  return (data ?? []) as unknown as VenomModel[]
}

export async function listRoutingRules(
  supabase: SupabaseClient,
  opts?: { venomSlug?: "lite" | "pro" | "max"; activeOnly?: boolean },
): Promise<RoutingRule[]> {
  let q = supabase
    .from("routing_rules")
    .select(
      "id,venom_slug,model_id,account_id,priority,active,role,models!inner(external_id,providers!inner(slug))",
    )
    .order("priority", { ascending: false })
  if (opts?.venomSlug) q = (q as any).eq("venom_slug", opts.venomSlug)
  if (opts?.activeOnly) q = (q as any).eq("active", true)

  const { data, error } = await q
  if (error) throw new Error(`listRoutingRules: ${error.message}`)

  return ((data ?? []) as any[]).map((row) => ({
    id: row.id,
    venom_slug: row.venom_slug as "lite" | "pro" | "max",
    model_id: row.model_id,
    account_id: row.account_id,
    priority: row.priority,
    active: row.active,
    role: row.role ?? "",
    model_external_id: row.models?.external_id ?? "",
    provider_slug: row.models?.providers?.slug ?? "",
  }))
}
