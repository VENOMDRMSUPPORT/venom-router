import type { SupabaseClient } from "@supabase/supabase-js";

// ── Types ──────────────────────────────────────────────────────────────────────

export type ApiKey = {
  id: string;
  name: string;
  key_prefix: string;
  allowed_models: string[];
  rpm_limit: number | null;
  tpd_limit: number | null;
  monthly_cap_usd: number | null;
  revoked_at: string | null;
  last_used_at: string | null;
  created_at: string;
};

// key_hash intentionally excluded — never returned to callers
const KEY_SELECT =
  "id,name,key_prefix,allowed_models,rpm_limit,tpd_limit,monthly_cap_usd,revoked_at,last_used_at,created_at";

// ── Exported functions ─────────────────────────────────────────────────────────

export async function listApiKeys(
  supabase: SupabaseClient,
  opts?: { activeOnly?: boolean },
): Promise<ApiKey[]> {
  let q = supabase
    .from("venom_api_keys")
    .select(KEY_SELECT)
    .order("created_at", { ascending: false });
  if (opts?.activeOnly) q = (q as any).is("revoked_at", null);

  const { data, error } = await q;
  if (error) throw new Error(`listApiKeys: ${error.message}`);
  return (data ?? []) as unknown as ApiKey[];
}

export async function getApiKey(supabase: SupabaseClient, id: string): Promise<ApiKey> {
  const { data, error } = await supabase
    .from("venom_api_keys")
    .select(KEY_SELECT)
    .eq("id", id)
    .single();
  if (error || !data) throw new Error(`getApiKey: ${error?.message ?? "not found"}`);
  return data as unknown as ApiKey;
}
