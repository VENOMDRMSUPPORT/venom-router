import type { ScoredCandidate } from "@/lib/routing/types";
import type { ChatRequest } from "@/lib/providers/adapters/types";
import { unpackCredentials } from "@/lib/credentials.server";
import * as opencodeZen from "@/lib/providers/adapters/opencode-zen.server";
import * as claudeCode from "@/lib/providers/adapters/claude-code.server";
import * as antigravity from "@/lib/providers/adapters/antigravity.server";

export interface ExecutionResult {
  ok: boolean;
  content?: string;
  inputTokens: number;
  outputTokens: number;
  latencyMs: number;
  fallbackUsed: boolean;
  fallbackCount: number;
  selectedRuleId?: string;
  errorCode?: string;
  providerAdapter?: string;
  attemptLog: Array<{ ruleId: string; error: string }>;
}

function getAdapterChat(adapterSlug: string) {
  switch (adapterSlug) {
    case "antigravity":
      return antigravity.chat;
    case "claude-code":
      return claudeCode.chat;
    case "opencode-zen":
      return opencodeZen.chat;
    default:
      return null;
  }
}

export async function executeWithFallback(
  scored: ScoredCandidate[],
  req: ChatRequest,
  maxAttempts: number,
): Promise<ExecutionResult> {
  const startedAt = Date.now();
  const attempts = Math.min(maxAttempts, scored.length);
  const attemptLog: Array<{ ruleId: string; error: string }> = [];
  let fallbackCount = 0;

  for (let i = 0; i < attempts; i++) {
    const { candidate } = scored[i];
    const isFallback = i > 0;
    if (isFallback) fallbackCount++;

    // Reshape camelCase account fields to snake_case for unpackCredentials
    let creds;
    try {
      creds = unpackCredentials({
        credentials_enc: candidate.account.credentialsEnc as Parameters<
          typeof unpackCredentials
        >[0]["credentials_enc"],
        credentials_iv: candidate.account.credentialsIv as Parameters<
          typeof unpackCredentials
        >[0]["credentials_iv"],
        credentials_tag: candidate.account.credentialsTag as Parameters<
          typeof unpackCredentials
        >[0]["credentials_tag"],
      });
    } catch {
      attemptLog.push({ ruleId: candidate.ruleId, error: "DECRYPT_FAILED" });
      continue;
    }

    const adapterFn = getAdapterChat(candidate.model.provider.adapter);
    if (!adapterFn) {
      attemptLog.push({
        ruleId: candidate.ruleId,
        error: `UNKNOWN_ADAPTER:${candidate.model.provider.adapter}`,
      });
      continue;
    }

    try {
      const result = await adapterFn(creds, candidate.model.externalId, req);

      if (result.ok && result.content !== undefined) {
        return {
          ok: true,
          content: result.content,
          inputTokens: result.inputTokens,
          outputTokens: result.outputTokens,
          latencyMs: Date.now() - startedAt,
          fallbackUsed: isFallback,
          fallbackCount,
          selectedRuleId: candidate.ruleId,
          providerAdapter: candidate.model.provider.adapter,
          attemptLog,
        };
      }

      attemptLog.push({
        ruleId: candidate.ruleId,
        error: result.error ?? "EMPTY_RESPONSE",
      });
    } catch (e: unknown) {
      const message = e instanceof Error ? e.message : String(e);
      attemptLog.push({
        ruleId: candidate.ruleId,
        error: message.slice(0, 200),
      });
    }
  }

  return {
    ok: false,
    inputTokens: 0,
    outputTokens: 0,
    latencyMs: Date.now() - startedAt,
    fallbackUsed: fallbackCount > 0,
    fallbackCount,
    errorCode: scored.length === 0 ? "NO_ELIGIBLE_CANDIDATES" : "ALL_CANDIDATES_FAILED",
    attemptLog,
  };
}
