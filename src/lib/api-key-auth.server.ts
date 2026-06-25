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

/** Result of a usage-limit check performed before a request is routed. */
export type KeyLimitResult =
  | { ok: true }
  | {
      ok: false;
      errorCode: "TPD_EXCEEDED" | "MONTHLY_CAP_EXCEEDED";
      limit: number;
      window: string;
    };

/**
 * Enforce per-key usage caps that the key payload carries but that the request
 * pipeline did not previously evaluate (tokens-per-day and monthly spend).
 *
 * Both windows are computed in UTC to match how `usage_records.created_at` is stored.
 * When both caps are configured we still only need rows since the tighter window
 * (start of UTC day ⊆ start of UTC month), and aggregate tokens + cost from one fetch.
 */
export async function checkKeyLimits(key: ValidatedApiKey): Promise<KeyLimitResult> {
  const sinceTokens = key.tpdLimit !== null ? startOfUtcDay() : null;
  const sinceSpend = key.monthlyCupUsd !== null ? startOfUtcMonth() : null;

  // Nothing to enforce — short-circuit without touching the DB.
  if (sinceTokens === null && sinceSpend === null) return { ok: true };

  // Pick the earliest window; one query covers both aggregations.
  const sinceIso = [sinceTokens, sinceSpend]
    .filter((d): d is Date => d !== null)
    .sort((a, b) => a.getTime() - b.getTime())[0]!
    .toISOString();

  const { data } = await supabaseAdmin
    .from("usage_records")
    .select("input_tokens,output_tokens,cost_usd,created_at")
    .eq("api_key_id", key.id)
    .gte("created_at", sinceIso);

  const rows = data ?? [];
  const tokensSince = (sinceTokens ?? new Date(0)).getTime();
  const spendSince = (sinceSpend ?? new Date(0)).getTime();

  let tokensToday = 0;
  let spendThisMonth = 0;
  for (const r of rows) {
    const t = new Date(r.created_at ?? 0).getTime();
    if (sinceTokens && t >= tokensSince) {
      tokensToday += (r.input_tokens ?? 0) + (r.output_tokens ?? 0);
    }
    if (sinceSpend && t >= spendSince) {
      spendThisMonth += Number(r.cost_usd ?? 0);
    }
  }

  if (key.tpdLimit !== null && tokensToday > key.tpdLimit) {
    return { ok: false, errorCode: "TPD_EXCEEDED", limit: key.tpdLimit, window: "tokens-per-day" };
  }
  if (key.monthlyCupUsd !== null && spendThisMonth > key.monthlyCupUsd) {
    return {
      ok: false,
      errorCode: "MONTHLY_CAP_EXCEEDED",
      limit: key.monthlyCupUsd,
      window: "monthly-spend-usd",
    };
  }

  return { ok: true };
}

function startOfUtcDay(): Date {
  const d = new Date();
  d.setUTCHours(0, 0, 0, 0);
  return d;
}

function startOfUtcMonth(): Date {
  const d = new Date();
  d.setUTCDate(1);
  d.setUTCHours(0, 0, 0, 0);
  return d;
}

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
    } catch {
      // Fire-and-forget: a failure to stamp last_used_at must not block the request.
    }
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
