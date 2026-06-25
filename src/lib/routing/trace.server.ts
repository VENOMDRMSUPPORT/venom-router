import { supabaseAdmin } from "@/integrations/supabase/client.server";
import type { Json } from "@/integrations/supabase/types";
import type { RoutingTraceCandidate } from "@/lib/routing/types";
import { createLogger } from "@/lib/logger";

const log = createLogger("trace");

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
  requestId?: string;
  candidates?: RoutingTraceCandidate[];
  fallbackChain?: string[];
}

/**
 * Persist a usage record and its routing trace.
 *
 * The usage record is written first and is allowed to fail loudly (a missing
 * usage row makes billing/analytics impossible). The trace is best-effort: it
 * carries no secrets (rule IDs and reasons only) and a trace write failure must
 * never mask a successful routing decision, so its error is logged and swallowed.
 */
export async function persistUsageAndTrace(opts: PersistOpts): Promise<void> {
  const requestId = opts.requestId ?? crypto.randomUUID();
  const venomSlug = opts.venomSlug as "lite" | "pro" | "max";

  const { data: usageRecord, error: usageErr } = await supabaseAdmin
    .from("usage_records")
    .insert({
      request_id: requestId,
      venom_slug: venomSlug,
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

  if (usageErr) {
    throw new Error(`persistUsageAndTrace: usage_records insert failed: ${usageErr.message}`);
  }

  // Trace payload. JSONB columns accept arbitrary JSON. The candidates/fallbackChain
  // are structurally JSON but their interfaces lack an index signature, so we widen
  // through `unknown` at this boundary — the row shape is validated by the generated
  // insert type on the next line.
  const trace = {
    request_id: requestId,
    venom_slug: venomSlug,
    success: opts.success,
    reason: opts.decisionReason,
    candidates: (opts.candidates ?? []) as unknown as Json,
    fallback_chain: (opts.fallbackChain ?? []) as unknown as Json,
    usage_record_id: usageRecord?.id ?? null,
    candidates_evaluated: opts.candidatesEvaluated,
    candidates_filtered: opts.candidatesFiltered,
    selected_rule_id: opts.selectedRuleId,
    decision_reason: opts.decisionReason,
    fallback_attempts: opts.fallbackCount,
    modality: opts.modality,
  };

  // `as never` widens past excess-property checking on the generated Insert type —
  // the field set above is exactly the routing_traces columns.
  const { error: traceErr } = await supabaseAdmin.from("routing_traces").insert(trace as never);
  if (traceErr) {
    // Trace is best-effort — log but do not fail the request.
    log.error("routing_traces insert failed", { requestId, error: traceErr.message });
  }
}
