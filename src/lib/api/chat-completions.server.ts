import { randomBytes } from "crypto";
import { validateApiKey } from "@/lib/api-key-auth.server";
import { routeRequest } from "@/lib/routing/engine.server";
import { supabaseAdmin } from "@/integrations/supabase/client.server";
import type { ChatMessage } from "@/lib/providers/adapters/types";

const MODEL_MAP: Record<string, "lite" | "pro" | "max"> = {
  "venom/lite": "lite",
  "venom/pro": "pro",
  "venom/max": "max",
  lite: "lite",
  pro: "pro",
  max: "max",
};

function json(body: unknown, status: number): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function errorBody(message: string, type: string, code: string) {
  return { error: { message, type, code } };
}

export async function handleChatCompletions(request: Request): Promise<Response> {
  // 1. Auth
  const authHeader = request.headers.get("authorization") ?? "";
  const rawKey = authHeader.startsWith("Bearer ") ? authHeader.slice(7).trim() : null;

  const authResult = await validateApiKey(rawKey);
  if (!authResult.ok) {
    const message =
      authResult.error === "MISSING"
        ? "Missing or invalid Authorization header. Use: Bearer vk_live_..."
        : authResult.error === "REVOKED"
          ? "API key has been revoked."
          : "Invalid API key.";
    return json(errorBody(message, "invalid_request_error", "invalid_api_key"), 401);
  }

  const { key } = authResult;

  // 2. Parse body
  let body: {
    model?: string;
    messages?: unknown[];
    max_tokens?: number;
    temperature?: number;
  };
  try {
    body = (await request.json()) as typeof body;
  } catch {
    return json(errorBody("Invalid JSON body.", "invalid_request_error", "invalid_body"), 400);
  }

  // 3. Validate messages
  if (!Array.isArray(body.messages) || body.messages.length === 0) {
    return json(
      errorBody(
        "messages must be a non-empty array.",
        "invalid_request_error",
        "invalid_messages",
      ),
      400,
    );
  }

  // 4. Map model → venom slug
  const venomSlug = body.model ? MODEL_MAP[body.model] : undefined;
  if (!venomSlug) {
    return json(
      errorBody(
        `Unknown model: "${body.model}". Valid models: venom/lite, venom/pro, venom/max`,
        "invalid_request_error",
        "model_not_found",
      ),
      400,
    );
  }

  // 5. Check model is allowed for this key
  if (!key.allowedModels.includes(venomSlug)) {
    return json(
      errorBody(
        `This API key is not authorized to use model: ${body.model}`,
        "invalid_request_error",
        "model_access_denied",
      ),
      403,
    );
  }

  // 6. RPM check — count requests from this key in the last 60 s
  if (key.rpmLimit !== null) {
    const oneMinuteAgo = new Date(Date.now() - 60_000).toISOString();
    const { count } = await supabaseAdmin
      .from("usage_records")
      .select("id", { count: "exact", head: true })
      .eq("api_key_id", key.id)
      .gte("created_at", oneMinuteAgo);

    if ((count ?? 0) >= key.rpmLimit) {
      return json(
        errorBody(
          `Rate limit exceeded: ${key.rpmLimit} requests per minute.`,
          "requests",
          "rate_limit_exceeded",
        ),
        429,
      );
    }
  }

  // 7. Route the request
  const result = await routeRequest({
    venomSlug,
    messages: body.messages as ChatMessage[],
    maxTokens: body.max_tokens,
    temperature: body.temperature,
    apiKeyId: key.id,
  });

  // 8. Routing failure → 503
  if (!result.success) {
    return json(
      errorBody(
        "No providers available to handle this request.",
        "server_error",
        result.errorCode ?? "provider_error",
      ),
      503,
    );
  }

  // 9. OpenAI-compatible success response
  return json(
    {
      id: `venom-${randomBytes(12).toString("hex")}`,
      object: "chat.completion",
      created: Math.floor(Date.now() / 1000),
      model: body.model,
      choices: [
        {
          index: 0,
          message: { role: "assistant", content: result.content },
          finish_reason: "stop",
        },
      ],
      usage: {
        prompt_tokens: result.inputTokens,
        completion_tokens: result.outputTokens,
        total_tokens: result.inputTokens + result.outputTokens,
      },
      // Non-standard venom extensions
      "x-venom": {
        provider_adapter: result.providerAdapter,
        fallback_used: result.fallbackUsed,
        fallback_count: result.fallbackCount,
        latency_ms: result.latencyMs,
        selected_rule_id: result.selectedRuleId,
        modality: result.modality,
      },
    },
    200,
  );
}
