import { supabaseAdmin } from "@/integrations/supabase/client.server";
import type {
  RoutingRequest,
  RoutingResult,
  RoutingCandidate,
  RoutingTrace,
  RoutingTraceCandidate,
  VenomWeights,
  TaskClass,
} from "@/lib/routing/types";
import { detectModality, filterCandidatesWithDiagnostics } from "@/lib/routing/filter.server";
import { scoreCandidate } from "@/lib/routing/scorer.server";
import { executeWithFallback } from "@/lib/routing/executor.server";
import { persistUsageAndTrace } from "@/lib/routing/trace.server";
import { classifyTask } from "@/lib/routing/classifier.server";
import { enrichCandidate, getEscalationStages } from "@/lib/routing/policy.server";
import { applyAccountRotation } from "@/lib/routing/rotation.server";
import { mergeStrategyConfig } from "@/lib/routing/strategy.types";
import type { VenomTier } from "@/lib/routing/strategy.types";

function toTraceCandidate(
  c: RoutingCandidate,
  overrides: Partial<RoutingTraceCandidate> & Pick<RoutingTraceCandidate, "status">,
): RoutingTraceCandidate {
  return {
    rule_id: c.ruleId,
    external_id: c.model.externalId,
    adapter: c.model.provider.adapter,
    priority: c.priority,
    role: c.role,
    ...overrides,
  };
}

function buildTraceCandidates(
  rejected: Array<{ candidate: RoutingCandidate; reason: string }>,
  scored: Array<{ candidate: RoutingCandidate; score: number }>,
  attemptLog: Array<{ ruleId: string; error: string }>,
  selectedRuleId: string | undefined,
): RoutingTraceCandidate[] {
  const byRuleId = new Map<string, RoutingTraceCandidate>();

  for (const { candidate, reason } of rejected) {
    byRuleId.set(
      candidate.ruleId,
      toTraceCandidate(candidate, { status: "filtered", filter_reason: reason }),
    );
  }

  for (const { candidate, score } of scored) {
    byRuleId.set(candidate.ruleId, toTraceCandidate(candidate, { status: "eligible", score }));
  }

  for (const attempt of attemptLog) {
    const existing = byRuleId.get(attempt.ruleId);
    if (existing) {
      byRuleId.set(attempt.ruleId, {
        ...existing,
        status: "attempted",
        error: attempt.error,
      });
    }
  }

  if (selectedRuleId) {
    const existing = byRuleId.get(selectedRuleId);
    if (existing) {
      byRuleId.set(selectedRuleId, { ...existing, status: "selected" });
    }
  }

  return [...byRuleId.values()];
}

export async function routeRequest(req: RoutingRequest): Promise<RoutingResult> {
  const startedAt = Date.now();
  const requestId = crypto.randomUUID();

  const modality = detectModality(req.messages);
  const taskClass: TaskClass = classifyTask(req.messages);

  const { data: venomModel } = await supabaseAdmin
    .from("venom_models")
    .select(
      "slug, weight_cost, weight_speed, weight_quality, max_fallback_attempts, strategy_config",
    )
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
    costWeight: Number(venomModel.weight_cost),
    speedWeight: Number(venomModel.weight_speed),
    qualityWeight: Number(venomModel.weight_quality),
    maxFallbackAttempts: venomModel.max_fallback_attempts ?? 3,
  };

  const tier = req.venomSlug as VenomTier;
  const strategy = mergeStrategyConfig(
    tier,
    venomModel.strategy_config as Partial<import("@/lib/routing/strategy.types").TierStrategyConfig> | null,
  );

  const { data: rawRules } = await supabaseAdmin
    .from("routing_rules")
    .select(
      `
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
    `,
    )
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

  const rawCandidates: RoutingCandidate[] = rawRules
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

  // Enrich all candidates with costType and qualityScore
  const allCandidates = rawCandidates.map(enrichCandidate);

  // Global filter: lifecycle, enabled, account health, quota, capability, conditions
  const { eligible: globalEligible, rejected } = filterCandidatesWithDiagnostics(
    allCandidates,
    modality,
    strategy,
  );
  const filteredCount = rejected.length;

  const chatReq = {
    messages: req.messages,
    maxTokens: req.maxTokens,
    temperature: req.temperature,
  };

  const stages = getEscalationStages(tier);
  const allAttemptLog: Array<{ ruleId: string; error: string }> = [];
  const allScored: Array<{ candidate: RoutingCandidate; score: number }> = [];
  let finalResult: Awaited<ReturnType<typeof executeWithFallback>> | null = null;
  let escalationStage = 0;

  for (let si = 0; si < stages.length; si++) {
    const stage = stages[si];

    // Filter eligible candidates to those allowed in this stage
    let stageEligible = globalEligible.filter((c) =>
      stage.allowedCostTypes.includes(c.costType ?? "free"),
    );

    if (stage.requireHighQuality) {
      stageEligible = stageEligible.filter((c) => (c.qualityScore ?? 0) >= 0.6);
    }

    // Remove candidates already attempted in previous stages
    const attemptedRuleIds = new Set(allAttemptLog.map((a) => a.ruleId));
    stageEligible = stageEligible.filter((c) => !attemptedRuleIds.has(c.ruleId));

    if (stageEligible.length === 0) continue;

    const stageScored = stageEligible
      .map((c) => ({ candidate: c, score: scoreCandidate(c, tier, taskClass, stageEligible) }))
      .sort((a, b) => b.score - a.score);

    allScored.push(...stageScored);

    const rotated = applyAccountRotation(stageScored, strategy.account_rotation);

    escalationStage = si + 1;
    const result = await executeWithFallback(rotated, chatReq, weights.maxFallbackAttempts);
    allAttemptLog.push(...result.attemptLog);

    if (result.ok) {
      finalResult = result;
      break;
    }
  }

  // Compute cost
  const selectedRule = finalResult?.selectedRuleId
    ? allCandidates.find((c) => c.ruleId === finalResult!.selectedRuleId)
    : null;
  const inputCostPerMtok = selectedRule?.model.inputCostPerMtok ?? 0;
  const outputCostPerMtok = selectedRule?.model.outputCostPerMtok ?? 0;
  const inputTokens = finalResult?.inputTokens ?? 0;
  const outputTokens = finalResult?.outputTokens ?? 0;
  const costUsd =
    (inputTokens * inputCostPerMtok) / 1_000_000 +
    (outputTokens * outputCostPerMtok) / 1_000_000;

  const ok = finalResult?.ok ?? false;
  const decisionReason = ok
    ? `Stage ${escalationStage}: selected rule ${finalResult!.selectedRuleId} (taskClass=${taskClass})${allAttemptLog.length > 0 ? ` after ${allAttemptLog.map((a) => a.ruleId).join(", ")} failed` : ""}`
    : `All ${stages.length} escalation stages exhausted (taskClass=${taskClass})`;

  const traceCandidates = req.includeTrace
    ? buildTraceCandidates(rejected, allScored, allAttemptLog, finalResult?.selectedRuleId)
    : undefined;
  const fallbackChain = allAttemptLog.map((a) => a.ruleId);

  const trace: RoutingTrace | undefined = req.includeTrace
    ? {
        modality,
        task_class: taskClass,
        candidates_evaluated: allCandidates.length,
        candidates_filtered: filteredCount,
        decision_reason: decisionReason,
        selected_rule_id: finalResult?.selectedRuleId ?? null,
        fallback_attempts: finalResult?.fallbackCount ?? 0,
        escalation_stage: escalationStage,
        candidates: traceCandidates ?? [],
        cost_usd: costUsd,
      }
    : undefined;

  persistUsageAndTrace({
    venomSlug: req.venomSlug,
    ruleId: finalResult?.selectedRuleId ?? null,
    accountId: selectedRule?.account.id ?? null,
    modelId: selectedRule?.model.id ?? null,
    apiKeyId: req.apiKeyId,
    inputTokens,
    outputTokens,
    costUsd,
    latencyMs: finalResult?.latencyMs ?? Date.now() - startedAt,
    success: ok,
    fallbackUsed: finalResult?.fallbackUsed ?? false,
    fallbackCount: finalResult?.fallbackCount ?? 0,
    candidatesEvaluated: allCandidates.length,
    candidatesFiltered: filteredCount,
    selectedRuleId: finalResult?.selectedRuleId ?? null,
    decisionReason,
    modality,
    requestId,
    candidates: traceCandidates,
    fallbackChain,
  }).catch(() => {});

  return {
    success: ok,
    content: finalResult?.content,
    inputTokens,
    outputTokens,
    latencyMs: finalResult?.latencyMs ?? Date.now() - startedAt,
    fallbackUsed: finalResult?.fallbackUsed ?? false,
    fallbackCount: finalResult?.fallbackCount ?? 0,
    errorCode: ok ? undefined : (finalResult?.errorCode ?? "ALL_STAGES_EXHAUSTED"),
    selectedRuleId: finalResult?.selectedRuleId,
    modality,
    providerAdapter: finalResult?.providerAdapter,
    trace,
    costUsd,
  };
}
