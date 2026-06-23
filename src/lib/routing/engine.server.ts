import { supabaseAdmin } from "@/integrations/supabase/client.server";
import type {
  RoutingRequest,
  RoutingResult,
  RoutingCandidate,
  VenomWeights,
} from "@/lib/routing/types";
import { detectModality, filterCandidates } from "@/lib/routing/filter.server";
import { scoreCandidate } from "@/lib/routing/scorer.server";
import { executeWithFallback } from "@/lib/routing/executor.server";
import { persistUsageAndTrace } from "@/lib/routing/trace.server";

export async function routeRequest(req: RoutingRequest): Promise<RoutingResult> {
  const startedAt = Date.now();

  // 1. Detect modality from message content
  const modality = detectModality(req.messages);

  // 2. Load venom model weights
  const { data: venomModel } = await supabaseAdmin
    .from("venom_models")
    .select("slug, cost_weight, speed_weight, quality_weight, max_fallback_attempts")
    .eq("slug", req.venomSlug)
    .single();

  if (!venomModel) {
    return {
      success: false,
      inputTokens: 0,
      outputTokens: 0,
      latencyMs: Date.now() - startedAt,
      fallbackUsed: false,
      fallbackCount: 0,
      errorCode: "VENOM_MODEL_NOT_FOUND",
      modality,
    };
  }

  const weights: VenomWeights = {
    costWeight: Number(venomModel.cost_weight),
    speedWeight: Number(venomModel.speed_weight),
    qualityWeight: Number(venomModel.quality_weight),
    maxFallbackAttempts: venomModel.max_fallback_attempts ?? 3,
  };

  // 3. Load routing rules with model + account data
  const { data: rawRules } = await supabaseAdmin
    .from("routing_rules")
    .select(`
      id,
      priority,
      role,
      condition,
      models!model_id (
        id,
        external_id,
        lifecycle,
        input_cost_per_mtok,
        output_cost_per_mtok,
        capabilities,
        providers!provider_id (
          adapter,
          base_url
        )
      ),
      accounts!account_id (
        id,
        status,
        credentials_enc,
        credentials_iv,
        credentials_tag,
        quotas (
          used,
          total,
          unit,
          confidence
        )
      )
    `)
    .eq("venom_slug", req.venomSlug)
    .eq("active", true);

  if (!rawRules?.length) {
    return {
      success: false,
      inputTokens: 0,
      outputTokens: 0,
      latencyMs: Date.now() - startedAt,
      fallbackUsed: false,
      fallbackCount: 0,
      errorCode: "NO_ROUTING_RULES",
      modality,
    };
  }

  const modelIds = [...new Set(rawRules.map((r: { model_id: string }) => r.model_id))];
  const accountIds = [...new Set(rawRules.map((r: { account_id: string }) => r.account_id))];
  const { data: accountModelRows } = await supabaseAdmin
    .from("account_models")
    .select("account_id, model_id, enabled, lifecycle, latency_ms, test_status")
    .in("model_id", modelIds)
    .in("account_id", accountIds);
  const accountModelByKey = new Map(
    (accountModelRows ?? []).map(
      (r: {
        account_id: string;
        model_id: string;
        enabled: boolean;
        lifecycle: string;
        latency_ms: number | null;
      }) => [`${r.account_id}:${r.model_id}`, r],
    ),
  );

  // 4. Shape raw DB rows into RoutingCandidate[]
  const allCandidates: RoutingCandidate[] = rawRules
    .filter((r: any) => r.models && r.accounts)
    .map((r: any) => {
      const model = Array.isArray(r.models) ? r.models[0] : r.models;
      const account = Array.isArray(r.accounts) ? r.accounts[0] : r.accounts;
      const provider = Array.isArray(model?.providers) ? model.providers[0] : model?.providers;
      const quotaRow = account?.quotas?.[0] ?? null;
      const accountModel = accountModelByKey.get(`${account.id}:${model.id}`);

      return {
        ruleId: r.id,
        priority: r.priority,
        role: r.role,
        condition: r.condition ?? null,
        model: {
          id: model.id,
          externalId: model.external_id,
          lifecycle: accountModel?.lifecycle ?? model.lifecycle,
          enabled: accountModel?.enabled ?? false,
          inputCostPerMtok:
            model.input_cost_per_mtok !== null ? Number(model.input_cost_per_mtok) : null,
          outputCostPerMtok:
            model.output_cost_per_mtok !== null ? Number(model.output_cost_per_mtok) : null,
          capabilities: Array.isArray(model.capabilities?.list)
            ? model.capabilities.list
            : Array.isArray(model.capabilities)
              ? model.capabilities
              : [],
          latencyMs: accountModel?.latency_ms ?? null,
          provider: {
            adapter: provider?.adapter ?? "",
            baseUrl: provider?.base_url ?? null,
          },
        },
        account: {
          id: account.id,
          status: account.status,
          credentialsEnc: account.credentials_enc,
          credentialsIv: account.credentials_iv,
          credentialsTag: account.credentials_tag,
          quota: quotaRow
            ? {
                used: Number(quotaRow.used ?? 0),
                total: quotaRow.total !== null ? Number(quotaRow.total) : null,
                confidence: quotaRow.confidence ?? "unknown",
              }
            : null,
        },
      } satisfies RoutingCandidate;
    });

  // 5. Filter candidates by modality + eligibility rules
  const eligible = filterCandidates(allCandidates, modality);
  const filteredCount = allCandidates.length - eligible.length;

  if (eligible.length === 0) {
    persistUsageAndTrace({
      venomSlug: req.venomSlug,
      ruleId: null,
      accountId: null,
      modelId: null,
      apiKeyId: req.apiKeyId,
      inputTokens: 0,
      outputTokens: 0,
      costUsd: 0,
      latencyMs: Date.now() - startedAt,
      success: false,
      fallbackUsed: false,
      fallbackCount: 0,
      candidatesEvaluated: allCandidates.length,
      candidatesFiltered: filteredCount,
      selectedRuleId: null,
      decisionReason: `No eligible candidates after filtering (${filteredCount} filtered from ${allCandidates.length})`,
      modality,
    }).catch(() => {});

    return {
      success: false,
      inputTokens: 0,
      outputTokens: 0,
      latencyMs: Date.now() - startedAt,
      fallbackUsed: false,
      fallbackCount: 0,
      errorCode: "NO_ELIGIBLE_CANDIDATES",
      modality,
    };
  }

  // 6. Score + sort candidates descending
  const scored = eligible
    .map((c) => ({ candidate: c, score: scoreCandidate(c, weights) }))
    .sort((a, b) => b.score - a.score);

  // 7. Execute with fallback
  const chatReq = {
    messages: req.messages,
    maxTokens: req.maxTokens,
    temperature: req.temperature,
  };

  const result = await executeWithFallback(scored, chatReq, weights.maxFallbackAttempts);

  // 8. Calculate cost (inputCostPerMtok * inputTokens / 1_000_000 + outputCostPerMtok * outputTokens / 1_000_000)
  const selectedRule = result.selectedRuleId
    ? allCandidates.find((c) => c.ruleId === result.selectedRuleId)
    : null;
  const inputCostPerMtok = selectedRule?.model.inputCostPerMtok ?? 0;
  const outputCostPerMtok = selectedRule?.model.outputCostPerMtok ?? 0;
  const costUsd =
    (result.inputTokens * inputCostPerMtok) / 1_000_000 +
    (result.outputTokens * outputCostPerMtok) / 1_000_000;

  // 9. Build decision reason — rule IDs only, no secrets
  const decisionReason = result.ok
    ? `${result.fallbackCount > 0 ? `Fallback ${result.fallbackCount}: ` : "Primary: "}selected rule ${result.selectedRuleId} (score=${scored[0]?.score.toFixed(3) ?? "?"})${result.attemptLog.length > 0 ? ` after ${result.attemptLog.map((a) => a.ruleId).join(", ")} failed` : ""}`
    : `All ${Math.min(weights.maxFallbackAttempts, scored.length)} candidates failed`;

  // 10. Persist usage + trace — fire-and-forget, never blocks the response
  persistUsageAndTrace({
    venomSlug: req.venomSlug,
    ruleId: result.selectedRuleId ?? null,
    accountId: selectedRule?.account.id ?? null,
    modelId: selectedRule?.model.id ?? null,
    apiKeyId: req.apiKeyId,
    inputTokens: result.inputTokens,
    outputTokens: result.outputTokens,
    costUsd,
    latencyMs: result.latencyMs,
    success: result.ok,
    fallbackUsed: result.fallbackUsed,
    fallbackCount: result.fallbackCount,
    candidatesEvaluated: allCandidates.length,
    candidatesFiltered: filteredCount,
    selectedRuleId: result.selectedRuleId ?? null,
    decisionReason,
    modality,
  }).catch(() => {});

  // 11. Return RoutingResult
  return {
    success: result.ok,
    content: result.content,
    inputTokens: result.inputTokens,
    outputTokens: result.outputTokens,
    latencyMs: result.latencyMs,
    fallbackUsed: result.fallbackUsed,
    fallbackCount: result.fallbackCount,
    errorCode: result.errorCode,
    selectedRuleId: result.selectedRuleId,
    modality,
    providerAdapter: result.providerAdapter,
  };
}
