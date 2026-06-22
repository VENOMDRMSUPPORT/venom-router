import { supabaseAdmin } from "@/integrations/supabase/client.server";

export interface PersistOpts {
  venomSlug: string;
  ruleId: string | null;
  accountId: string | null;
  modelId: string | null;
  apiKeyId: string | undefined;
  inputTokens: number;
  outputTokens: number;
  costUsd: number;
  latencyMs: number;
  success: boolean;
  fallbackUsed: boolean;
  fallbackCount: number;
  candidatesEvaluated: number;
  candidatesFiltered: number;
  selectedRuleId: string | null;
  decisionReason: string;
  modality: string;
}

export async function persistUsageAndTrace(opts: PersistOpts): Promise<void> {
  // 1. Insert usage record
  const { data: usageRecord } = await supabaseAdmin
    .from("usage_records")
    .insert({
      request_id: crypto.randomUUID(),
      venom_slug: opts.venomSlug,
      rule_id: opts.ruleId,
      account_id: opts.accountId,
      model_id: opts.modelId,
      api_key_id: opts.apiKeyId ?? null,
      input_tokens: opts.inputTokens,
      output_tokens: opts.outputTokens,
      cost_usd: opts.costUsd,
      latency_ms: opts.latencyMs,
      success: opts.success,
      fallback_used: opts.fallbackUsed,
    })
    .select("id")
    .single();

  // 2. Insert routing trace (rule IDs + reasons ONLY — no secrets)
  await supabaseAdmin.from("routing_traces").insert({
    usage_record_id: usageRecord?.id ?? null,
    candidates_evaluated: opts.candidatesEvaluated,
    candidates_filtered: opts.candidatesFiltered,
    selected_rule_id: opts.selectedRuleId,
    decision_reason: opts.decisionReason,
    fallback_attempts: opts.fallbackCount,
    modality: opts.modality,
  });
}
