import { hashApiKey } from "@/lib/crypto.server";
import { supabaseAdmin } from "@/integrations/supabase/client.server";

export interface ValidatedApiKey {
  id: string;
  allowedModels: Array<"lite" | "pro" | "max">;
  rpmLimit: number | null;
  tpdLimit: number | null;
  monthlyCupUsd: number | null;
}

export type ApiKeyError = "MISSING" | "INVALID" | "REVOKED";

export async function validateApiKey(
  raw: string | null | undefined,
): Promise<{ ok: true; key: ValidatedApiKey } | { ok: false; error: ApiKeyError }> {
  if (!raw || !raw.startsWith("vk_live_")) {
    return { ok: false, error: "MISSING" };
  }

  const hash = hashApiKey(raw);

  const { data } = await supabaseAdmin
    .from("venom_api_keys")
    .select("id, allowed_models, rpm_limit, tpd_limit, monthly_cap_usd, revoked_at")
    .eq("key_hash", hash)
    .single();

  if (!data) return { ok: false, error: "INVALID" };
  if (data.revoked_at) return { ok: false, error: "REVOKED" };

  // Fire-and-forget: stamp last usage
  void (async () => {
    try {
      await supabaseAdmin
        .from("venom_api_keys")
        .update({ last_used_at: new Date().toISOString() })
        .eq("id", data.id);
    } catch {}
  })();

  return {
    ok: true,
    key: {
      id: data.id as string,
      allowedModels: (data.allowed_models as Array<"lite" | "pro" | "max">) ?? [],
      rpmLimit: (data.rpm_limit as number | null) ?? null,
      tpdLimit: (data.tpd_limit as number | null) ?? null,
      monthlyCupUsd: (data.monthly_cap_usd as number | null) ?? null,
    },
  };
}
