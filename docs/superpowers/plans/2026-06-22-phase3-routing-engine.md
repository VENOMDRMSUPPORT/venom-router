# Phase 3 — Routing Engine

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the core routing engine that scores, filters, and executes AI provider calls with automatic fallback — transforming a `venomSlug + messages` request into a provider response and persisting usage + trace records.

**Architecture:** Eight independent files under `src/lib/routing/`. The engine is a plain async function (no TanStack server fn wrapper) — it will be called from the `/v1` API endpoints in Phase 4. Adapters get a new `chat()` export following the same pattern as the existing `testModel()`.

**Tech Stack:** TanStack Start, Supabase JS client (server-side), TypeScript, Bun. No test framework is configured — verification uses `bun run build` for type checks and `bun run dev` + manual testing for functional checks.

## Global Constraints

- Package manager: `bun` only — never `npm` or `yarn`
- Path alias: `@/` maps to `src/` — always use this, never relative `../../`
- Server-only files: suffix `.server.ts` — never import from client code
- Never commit `.env` files or secrets
- Never modify `routeTree.gen.ts` — auto-generated
- Never add duplicate Vite plugins
- Routing traces must NEVER contain provider names, URLs, credentials, or tokens — only rule IDs and decision reasons
- Supabase server client: import from `@/integrations/supabase/client.server`
- Credential decryption: use `unpackCredentials()` from `@/lib/credentials.server`

---

### Task 1: Routing Types + Extend Adapter Interface

**Files:**

- Modify: `src/lib/providers/adapters/types.ts`
- Create: `src/lib/routing/types.ts`

**Interfaces:**

- Produces: `ChatMessage`, `ChatRequest`, `ChatResult` in `@/lib/providers/adapters/types` (consumed by Tasks 3–5)
- Produces: `RoutingRequest`, `RoutingResult`, `RoutingCandidate`, `ScoredCandidate`, `VenomWeights`, `RoutingCondition`, `Modality` in `@/lib/routing/types` (consumed by Tasks 2, 6, 7, 8)

- [ ] **Step 1: Add chat types to adapter types**

Append to `src/lib/providers/adapters/types.ts` (after the existing exports):

```ts
export interface ChatMessage {
  role: "system" | "user" | "assistant";
  content: string;
}

export interface ChatRequest {
  messages: ChatMessage[];
  maxTokens?: number;
  temperature?: number;
}

export interface ChatResult {
  ok: boolean;
  content?: string;
  inputTokens: number;
  outputTokens: number;
  error?: string;
}
```

- [ ] **Step 2: Create routing types**

Create `src/lib/routing/types.ts`:

```ts
export type Modality = "text" | "vision" | "audio" | "documents";

export interface RoutingCondition {
  requires?: string[];
  min_context_tokens?: number;
  quota_risk?: "low" | "medium" | "high";
}

export interface VenomWeights {
  costWeight: number;
  speedWeight: number;
  qualityWeight: number;
  maxFallbackAttempts: number;
}

export interface RoutingCandidate {
  ruleId: string;
  priority: number;
  role: string;
  condition: RoutingCondition | null;
  model: {
    id: string;
    externalId: string;
    lifecycle: string;
    enabled: boolean;
    inputCostPerMtok: number | null;
    outputCostPerMtok: number | null;
    capabilities: string[];
    latencyMs: number | null;
    provider: { adapter: string; baseUrl: string | null };
  };
  account: {
    id: string;
    status: string;
    credentialsEnc: unknown;
    credentialsIv: unknown;
    credentialsTag: unknown;
    quota: { used: number; total: number | null; confidence: string } | null;
  };
}

export interface ScoredCandidate {
  candidate: RoutingCandidate;
  score: number;
}

export interface RoutingRequest {
  venomSlug: "lite" | "pro" | "max";
  messages: import("@/lib/providers/adapters/types").ChatMessage[];
  maxTokens?: number;
  temperature?: number;
  apiKeyId?: string;
}

export interface RoutingResult {
  success: boolean;
  content?: string;
  inputTokens: number;
  outputTokens: number;
  latencyMs: number;
  fallbackUsed: boolean;
  fallbackCount: number;
  errorCode?: string;
  selectedRuleId?: string;
  modality: Modality;
  providerAdapter?: string;
}
```

- [ ] **Step 3: Build check**

```bash
bun run build 2>&1
```

Expected: no TypeScript errors in the two modified/created files.

- [ ] **Step 4: Commit**

```bash
git add src/lib/providers/adapters/types.ts src/lib/routing/types.ts
git commit -m "feat(routing): add chat types and routing type definitions"
```

---

### Task 2: Scorer + Filter + Modality Detection

**Files:**

- Create: `src/lib/routing/scorer.server.ts`
- Create: `src/lib/routing/filter.server.ts`

**Interfaces:**

- Consumes: `RoutingCandidate`, `ScoredCandidate`, `VenomWeights`, `RoutingCondition`, `Modality` from `@/lib/routing/types`
- Produces:
  - `scoreCandidate(candidate: RoutingCandidate, weights: VenomWeights): number`
  - `detectModality(messages: ChatMessage[]): Modality`
  - `filterCandidates(candidates: RoutingCandidate[], modality: Modality): RoutingCandidate[]`

- [ ] **Step 1: Create scorer**

Create `src/lib/routing/scorer.server.ts`:

```ts
import type { RoutingCandidate, VenomWeights } from "@/lib/routing/types";

/**
 * Scores a routing candidate.
 *
 * score = roleBonus×10 + costWeight×costScore + speedWeight×speedScore + qualityWeight×priorityScore
 *
 * roleBonus     = 1 if role="primary" else 0
 * costScore     = 1 / (avgCost×1000 + 1)  where avgCost = (input + output×3) / 4
 * speedScore    = 1000 / latencyMs  (default 0.5 if no data)
 * priorityScore = 1 / (priority + 1)
 */
export function scoreCandidate(candidate: RoutingCandidate, weights: VenomWeights): number {
  const roleBonus = candidate.role === "primary" ? 1 : 0;
  const priorityScore = 1 / (candidate.priority + 1);

  const inputCost = candidate.model.inputCostPerMtok ?? 0.001;
  const outputCost = candidate.model.outputCostPerMtok ?? inputCost * 3;
  const avgCost = (inputCost + outputCost * 3) / 4;
  const costScore = avgCost > 0 ? 1 / (avgCost * 1000 + 1) : 0.5;

  const latency = candidate.model.latencyMs;
  const speedScore = typeof latency === "number" && latency > 0 ? 1000 / latency : 0.5;

  return (
    roleBonus * 10 +
    weights.costWeight * costScore +
    weights.speedWeight * speedScore +
    weights.qualityWeight * priorityScore
  );
}
```

- [ ] **Step 2: Create filter + modality detection**

Create `src/lib/routing/filter.server.ts`:

```ts
import type { ChatMessage } from "@/lib/providers/adapters/types";
import type { Modality, RoutingCandidate, RoutingCondition } from "@/lib/routing/types";

/**
 * Detects the modality of a request from its messages.
 * Checks content arrays for image_url (vision), audio (audio), or file/document (documents).
 */
export function detectModality(messages: ChatMessage[]): Modality {
  for (const msg of messages) {
    if (typeof msg.content !== "string") {
      // content is an array of content parts (multimodal)
      const parts = msg.content as unknown as Array<{ type: string }>;
      if (!Array.isArray(parts)) continue;
      for (const part of parts) {
        if (part.type === "image_url" || part.type === "image") return "vision";
        if (part.type === "audio") return "audio";
        if (part.type === "file" || part.type === "document") return "documents";
      }
    }
  }
  return "text";
}

function isQuotaExhausted(
  quota: { used: number; total: number | null; confidence: string } | null,
): boolean {
  if (!quota) return false;
  if (quota.confidence !== "high") return false;
  if (quota.total === null || quota.total <= 0) return false;
  const remaining = quota.total - quota.used;
  return remaining / quota.total < 0.05;
}

function matchesCondition(
  condition: RoutingCondition | null,
  capabilities: string[],
  modality: Modality,
): boolean {
  if (!condition) return true;

  if (condition.requires?.length) {
    for (const cap of condition.requires) {
      if (!capabilities.includes(cap)) return false;
    }
  }

  return true;
}

/**
 * Filters candidates by: lifecycle, enabled, account health, quota, modality, condition.
 * Returns only eligible candidates.
 */
export function filterCandidates(
  candidates: RoutingCandidate[],
  modality: Modality,
): RoutingCandidate[] {
  return candidates.filter((c) => {
    if (c.model.lifecycle !== "approved") return false;
    if (!c.model.enabled) return false;
    if (c.account.status !== "healthy") return false;
    if (isQuotaExhausted(c.account.quota)) return false;

    // Modality capability check
    if (modality !== "text") {
      const caps = c.model.capabilities;
      if (!caps.includes(modality)) return false;
    }

    if (!matchesCondition(c.condition, c.model.capabilities, modality)) return false;

    return true;
  });
}
```

- [ ] **Step 3: Build check**

```bash
bun run build 2>&1
```

Expected: no TypeScript errors in the two new files.

- [ ] **Step 4: Commit**

```bash
git add src/lib/routing/scorer.server.ts src/lib/routing/filter.server.ts
git commit -m "feat(routing): add scorer, modality detection, and candidate filter"
```

---

### Task 3: OpenCode Zen `chat()` Method

**Files:**

- Modify: `src/lib/providers/adapters/opencode-zen.server.ts`

**Interfaces:**

- Consumes: `StoredCredentials`, `ChatRequest`, `ChatResult` from `@/lib/providers/adapters/types`
- Produces: `export async function chat(creds: StoredCredentials, externalId: string, req: ChatRequest): Promise<ChatResult>`

- [ ] **Step 1: Read the file first**

Read `src/lib/providers/adapters/opencode-zen.server.ts` to see the current structure. Note that `BASE = "https://opencode.ai/zen"`.

- [ ] **Step 2: Add import and chat() function**

In `src/lib/providers/adapters/opencode-zen.server.ts`, add the import at the top:

```ts
import type {
  StoredCredentials,
  AccountIdentity,
  DiscoveredModel,
  ModelTestResult,
  ChatRequest,
  ChatResult,
} from "./types";
```

(Replace the existing import line which imports the same types without `ChatRequest` and `ChatResult`.)

Then append at the end of the file:

```ts
export async function chat(
  creds: StoredCredentials,
  externalId: string,
  req: ChatRequest,
): Promise<ChatResult> {
  const t0 = Date.now();
  try {
    const r = await fetch(`${BASE}/v1/chat/completions`, {
      method: "POST",
      headers: {
        Authorization: `Bearer ${creds.api_key}`,
        "Content-Type": "application/json",
      },
      body: JSON.stringify({
        model: externalId,
        messages: req.messages,
        max_tokens: req.maxTokens ?? 1024,
        temperature: req.temperature ?? 0.7,
        stream: false,
      }),
    });
    if (!r.ok) {
      const text = await r.text();
      return { ok: false, inputTokens: 0, outputTokens: 0, error: text.slice(0, 300) };
    }
    const j: any = await r.json();
    const content = j?.choices?.[0]?.message?.content ?? "";
    const inputTokens = j?.usage?.prompt_tokens ?? 0;
    const outputTokens = j?.usage?.completion_tokens ?? 0;
    return { ok: true, content, inputTokens, outputTokens };
  } catch (e: any) {
    return {
      ok: false,
      inputTokens: 0,
      outputTokens: 0,
      error: String(e?.message ?? e).slice(0, 300),
    };
  }
}
```

- [ ] **Step 3: Build check**

```bash
bun run build 2>&1
```

Expected: no TypeScript errors in `opencode-zen.server.ts`.

- [ ] **Step 4: Commit**

```bash
git add src/lib/providers/adapters/opencode-zen.server.ts
git commit -m "feat(routing): add chat() to OpenCode Zen adapter"
```

---

### Task 4: Claude Code `chat()` Method

**Files:**

- Modify: `src/lib/providers/adapters/claude-code.server.ts`

**Interfaces:**

- Consumes: `StoredCredentials`, `ChatRequest`, `ChatResult` from `@/lib/providers/adapters/types`
- Produces: `export async function chat(creds: StoredCredentials, externalId: string, req: ChatRequest): Promise<ChatResult>`

The Claude Code adapter uses the Anthropic Messages API at `https://api.anthropic.com/v1/messages` with OAuth Bearer token. Messages with `role: "system"` must be extracted into the top-level `system` field (Anthropic format). Assistant role stays as-is.

- [ ] **Step 1: Read the file**

Read `src/lib/providers/adapters/claude-code.server.ts`. Note the constants `API_VERSION`, `CLAUDE_CODE_BETA`, `CLAUDE_CODE_IDENTITY`, `USER_AGENT`, and the `refreshIfNeeded(creds)` function.

- [ ] **Step 2: Add chat() import and update types import**

Update the import from `"./types"` to include `ChatRequest` and `ChatResult`.

- [ ] **Step 3: Append chat() at end of file**

```ts
export async function chat(
  credsIn: StoredCredentials,
  externalId: string,
  req: ChatRequest,
): Promise<ChatResult> {
  const creds = await refreshIfNeeded(credsIn);
  const t0 = Date.now();
  try {
    // Extract system messages and convert to Anthropic format
    const systemParts = req.messages
      .filter((m) => m.role === "system")
      .map((m) => ({ type: "text", text: m.content }));

    const conversationMessages = req.messages
      .filter((m) => m.role !== "system")
      .map((m) => ({ role: m.role as "user" | "assistant", content: m.content }));

    if (conversationMessages.length === 0) {
      return { ok: false, inputTokens: 0, outputTokens: 0, error: "No user/assistant messages" };
    }

    const body: Record<string, unknown> = {
      model: externalId,
      max_tokens: req.maxTokens ?? 1024,
      messages: conversationMessages,
    };
    if (systemParts.length > 0) body.system = systemParts;

    const r = await fetch("https://api.anthropic.com/v1/messages", {
      method: "POST",
      headers: {
        Authorization: `Bearer ${creds.access_token}`,
        "anthropic-version": API_VERSION,
        "anthropic-beta": CLAUDE_CODE_BETA,
        "User-Agent": USER_AGENT,
        "X-App": "cli",
        "Content-Type": "application/json",
      },
      body: JSON.stringify(body),
    });

    if (!r.ok) {
      const text = await r.text();
      return { ok: false, inputTokens: 0, outputTokens: 0, error: text.slice(0, 300) };
    }

    const j: any = await r.json();
    const content =
      j?.content
        ?.filter((b: any) => b.type === "text")
        .map((b: any) => b.text)
        .join("") ?? "";
    const inputTokens = j?.usage?.input_tokens ?? 0;
    const outputTokens = j?.usage?.output_tokens ?? 0;
    return { ok: true, content, inputTokens, outputTokens };
  } catch (e: any) {
    return {
      ok: false,
      inputTokens: 0,
      outputTokens: 0,
      error: String(e?.message ?? e).slice(0, 300),
    };
  }
}
```

- [ ] **Step 4: Build check**

```bash
bun run build 2>&1
```

Expected: no TypeScript errors in `claude-code.server.ts`.

- [ ] **Step 5: Commit**

```bash
git add src/lib/providers/adapters/claude-code.server.ts
git commit -m "feat(routing): add chat() to Claude Code adapter"
```

---

### Task 5: Antigravity `chat()` Method

**Files:**

- Modify: `src/lib/providers/adapters/antigravity.server.ts`

**Interfaces:**

- Consumes: `StoredCredentials`, `ChatRequest`, `ChatResult` from `@/lib/providers/adapters/types`
- Produces: `export async function chat(creds: StoredCredentials, externalId: string, req: ChatRequest): Promise<ChatResult>`

Antigravity uses the Google Cloud Code Assist API at `https://cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse`. The endpoint returns Server-Sent Events (SSE) even for short responses. The `chat()` implementation reads the full SSE stream, collects all text parts, and extracts token counts from the final event's `usageMetadata`. The request uses the same body format as `testModel()`.

Message format conversion: `role: "system"` → extracted into `systemInstruction`; `role: "user"` → `role: "user"`; `role: "assistant"` → `role: "model"` (Gemini uses "model" not "assistant").

- [ ] **Step 1: Read the file**

Read `src/lib/providers/adapters/antigravity.server.ts`. Note:

- `GENERATE` constant = `${BASE}/v1internal:streamGenerateContent?alt=sse`
- `bearerHeaders(token)` helper
- `COMMON_HEADERS` object
- `refreshIfNeeded(creds)` function
- `USER_AGENT` constant
- Request body pattern from `testModel()`: `{ project, model, request: { contents, generationConfig }, requestType, userAgent, requestId }`

- [ ] **Step 2: Update types import**

Find the existing import from `"./types"` and add `ChatRequest` and `ChatResult` to it.

- [ ] **Step 3: Append chat() at end of file**

```ts
export async function chat(
  credsIn: StoredCredentials,
  externalId: string,
  req: ChatRequest,
): Promise<ChatResult> {
  const creds = await refreshIfNeeded(credsIn);
  try {
    // Extract system instruction
    const systemTexts = req.messages
      .filter((m) => m.role === "system")
      .map((m) => m.content)
      .join("\n");

    // Convert messages to Gemini contents format (role: "user" | "model")
    const contents = req.messages
      .filter((m) => m.role !== "system")
      .map((m) => ({
        role: m.role === "assistant" ? "model" : "user",
        parts: [{ text: m.content }],
      }));

    if (contents.length === 0) {
      return { ok: false, inputTokens: 0, outputTokens: 0, error: "No user/assistant messages" };
    }

    const body: Record<string, unknown> = {
      project: creds.project_id,
      model: externalId,
      request: {
        contents,
        generationConfig: { maxOutputTokens: req.maxTokens ?? 1024 },
        ...(systemTexts ? { systemInstruction: { parts: [{ text: systemTexts }] } } : {}),
      },
      requestType: "agent",
      userAgent: USER_AGENT,
      requestId: `chat-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
    };

    const r = await fetch(GENERATE, {
      method: "POST",
      headers: {
        ...bearerHeaders(creds.access_token!),
        "Content-Type": "application/json",
        ...COMMON_HEADERS,
      },
      body: JSON.stringify(body),
    });

    if (!r.ok) {
      const text = await r.text();
      return { ok: false, inputTokens: 0, outputTokens: 0, error: text.slice(0, 300) };
    }

    // Parse SSE stream: collect all text parts, grab final usageMetadata
    const raw = await r.text();
    const lines = raw.split("\n");
    let fullText = "";
    let inputTokens = 0;
    let outputTokens = 0;

    for (const line of lines) {
      if (!line.startsWith("data: ")) continue;
      const jsonStr = line.slice(6).trim();
      if (!jsonStr || jsonStr === "[DONE]") continue;
      try {
        const event: any = JSON.parse(jsonStr);
        const parts = event?.candidates?.[0]?.content?.parts ?? [];
        for (const p of parts) {
          if (typeof p.text === "string") fullText += p.text;
        }
        if (event?.usageMetadata) {
          inputTokens = event.usageMetadata.promptTokenCount ?? 0;
          outputTokens = event.usageMetadata.candidatesTokenCount ?? 0;
        }
      } catch {
        // skip malformed SSE line
      }
    }

    return { ok: true, content: fullText, inputTokens, outputTokens };
  } catch (e: any) {
    return {
      ok: false,
      inputTokens: 0,
      outputTokens: 0,
      error: String(e?.message ?? e).slice(0, 300),
    };
  }
}
```

- [ ] **Step 4: Build check**

```bash
bun run build 2>&1
```

Expected: no TypeScript errors in `antigravity.server.ts`.

- [ ] **Step 5: Commit**

```bash
git add src/lib/providers/adapters/antigravity.server.ts
git commit -m "feat(routing): add chat() to Antigravity adapter (SSE stream collection)"
```

---

### Task 6: Executor (callProvider + Fallback Loop)

**Files:**

- Create: `src/lib/routing/executor.server.ts`

**Interfaces:**

- Consumes:
  - `ScoredCandidate` from `@/lib/routing/types`
  - `ChatRequest`, `ChatResult`, `StoredCredentials` from `@/lib/providers/adapters/types`
  - `unpackCredentials()` from `@/lib/credentials.server`
  - `chat()` from each adapter: `@/lib/providers/adapters/opencode-zen.server`, `@/lib/providers/adapters/claude-code.server`, `@/lib/providers/adapters/antigravity.server`
- Produces:

  ```ts
  interface ExecutionResult {
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

  export async function executeWithFallback(
    scored: ScoredCandidate[],
    req: ChatRequest,
    maxAttempts: number,
  ): Promise<ExecutionResult>;
  ```

- [ ] **Step 1: Create executor**

Create `src/lib/routing/executor.server.ts`:

```ts
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

    // Decrypt credentials
    let creds;
    try {
      creds = unpackCredentials(candidate.account as any);
    } catch (e) {
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
      const callStart = Date.now();
      const result = await adapterFn(creds, candidate.model.externalId, req);
      const latencyMs = Date.now() - callStart;

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

      attemptLog.push({ ruleId: candidate.ruleId, error: result.error ?? "EMPTY_RESPONSE" });
    } catch (e: any) {
      attemptLog.push({
        ruleId: candidate.ruleId,
        error: String(e?.message ?? e).slice(0, 200),
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
```

- [ ] **Step 2: Build check**

```bash
bun run build 2>&1
```

Expected: no TypeScript errors in `executor.server.ts`.

- [ ] **Step 3: Commit**

```bash
git add src/lib/routing/executor.server.ts
git commit -m "feat(routing): add executor with fallback loop and adapter dispatch"
```

---

### Task 7: Trace Persistence

**Files:**

- Create: `src/lib/routing/trace.server.ts`

**Interfaces:**

- Consumes: Supabase server client from `@/integrations/supabase/client.server`
- Produces:
  ```ts
  export async function persistUsageAndTrace(opts: {
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
  }): Promise<void>;
  ```

**Security:** Never store provider names, URLs, or credentials in the trace. Only rule IDs and decision reasons.

- [ ] **Step 1: Create trace module**

Create `src/lib/routing/trace.server.ts`:

```ts
import { createClient } from "@/integrations/supabase/client.server";

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
  const supabase = createClient();

  // 1. Insert usage record
  const { data: usageRecord } = await supabase
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
  await supabase.from("routing_traces").insert({
    usage_record_id: usageRecord?.id ?? null,
    candidates_evaluated: opts.candidatesEvaluated,
    candidates_filtered: opts.candidatesFiltered,
    selected_rule_id: opts.selectedRuleId,
    decision_reason: opts.decisionReason,
    fallback_attempts: opts.fallbackCount,
    modality: opts.modality,
  });
}
```

- [ ] **Step 2: Build check**

```bash
bun run build 2>&1
```

Expected: no TypeScript errors in `trace.server.ts`.

- [ ] **Step 3: Commit**

```bash
git add src/lib/routing/trace.server.ts
git commit -m "feat(routing): add usage + trace persistence (rule IDs only)"
```

---

### Task 8: Main Engine `routeRequest()`

**Files:**

- Create: `src/lib/routing/engine.server.ts`

**Interfaces:**

- Consumes:
  - `RoutingRequest`, `RoutingResult`, `RoutingCandidate`, `VenomWeights` from `@/lib/routing/types`
  - `detectModality()`, `filterCandidates()` from `@/lib/routing/filter.server`
  - `scoreCandidate()` from `@/lib/routing/scorer.server`
  - `executeWithFallback()` from `@/lib/routing/executor.server`
  - `persistUsageAndTrace()` from `@/lib/routing/trace.server`
  - Supabase server client from `@/integrations/supabase/client.server`
- Produces:
  ```ts
  export async function routeRequest(req: RoutingRequest): Promise<RoutingResult>;
  ```

**DB query structure:**

- Load `venom_models` → get `cost_weight`, `speed_weight`, `quality_weight`, `max_fallback_attempts`
- Load `routing_rules` joined with `models` (+ `providers`) and `accounts` (+ `quotas`)
- All joins use Supabase PostgREST syntax

- [ ] **Step 1: Create engine**

Create `src/lib/routing/engine.server.ts`:

```ts
import { createClient } from "@/integrations/supabase/client.server";
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
  const supabase = createClient();

  // 1. Detect modality from message content
  const modality = detectModality(req.messages);

  // 2. Load venom model weights
  const { data: venomModel } = await supabase
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
  const { data: rawRules } = await supabase
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
        enabled,
        input_cost_per_mtok,
        output_cost_per_mtok,
        capabilities,
        latency_ms,
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

  // 4. Shape raw DB rows into RoutingCandidate
  const allCandidates: RoutingCandidate[] = rawRules
    .filter((r: any) => r.models && r.accounts)
    .map((r: any) => {
      const model = Array.isArray(r.models) ? r.models[0] : r.models;
      const account = Array.isArray(r.accounts) ? r.accounts[0] : r.accounts;
      const provider = Array.isArray(model?.providers) ? model.providers[0] : model?.providers;
      const quotaRow = account?.quotas?.[0] ?? null;

      return {
        ruleId: r.id,
        priority: r.priority,
        role: r.role,
        condition: r.condition ?? null,
        model: {
          id: model.id,
          externalId: model.external_id,
          lifecycle: model.lifecycle,
          enabled: model.enabled,
          inputCostPerMtok:
            model.input_cost_per_mtok !== null ? Number(model.input_cost_per_mtok) : null,
          outputCostPerMtok:
            model.output_cost_per_mtok !== null ? Number(model.output_cost_per_mtok) : null,
          capabilities: Array.isArray(model.capabilities) ? model.capabilities : [],
          latencyMs: model.latency_ms ?? null,
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

  // 5. Filter candidates
  const eligible = filterCandidates(allCandidates, modality);
  const filteredCount = allCandidates.length - eligible.length;

  if (eligible.length === 0) {
    await persistUsageAndTrace({
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

  // 6. Score + sort candidates
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

  // 8. Estimate cost (per-million-token pricing × actual tokens)
  const selectedRule = result.selectedRuleId
    ? allCandidates.find((c) => c.ruleId === result.selectedRuleId)
    : null;
  const inputCostPerMtok = selectedRule?.model.inputCostPerMtok ?? 0;
  const outputCostPerMtok = selectedRule?.model.outputCostPerMtok ?? 0;
  const costUsd =
    (result.inputTokens * inputCostPerMtok) / 1_000_000 +
    (result.outputTokens * outputCostPerMtok) / 1_000_000;

  // 9. Build decision reason (rule IDs only — no secrets)
  const decisionReason = result.ok
    ? `${result.fallbackCount > 0 ? `Fallback ${result.fallbackCount}: ` : "Primary: "}selected rule ${result.selectedRuleId} (score=${scored[0]?.score.toFixed(3) ?? "?"})${result.attemptLog.length > 0 ? ` after ${result.attemptLog.map((a) => a.ruleId).join(", ")} failed` : ""}`
    : `All ${Math.min(weights.maxFallbackAttempts, scored.length)} candidates failed`;

  // 10. Persist usage + trace (fire-and-forget — never block the response)
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
```

- [ ] **Step 2: Build check**

```bash
bun run build 2>&1
```

Expected: no TypeScript errors in any routing file.

- [ ] **Step 3: Verify the engine is importable**

```bash
bun --eval "import('@/lib/routing/engine.server.ts').then(() => console.log('PASS: engine imports cleanly')).catch(e => console.error('FAIL:', e.message))" 2>&1
```

Expected: `PASS: engine imports cleanly`

- [ ] **Step 4: Commit**

```bash
git add src/lib/routing/
git commit -m "feat(routing): implement routeRequest() — score, filter, execute, persist"
```

---

## Self-Review

**Spec coverage check:**

- ✅ Scoring algorithm (roleBonus×10 + costWeight×costScore + speedWeight×speedScore + qualityWeight×priorityScore): Task 2
- ✅ Candidate filter rules (lifecycle=approved, enabled, healthy, quota, modality, condition): Task 2
- ✅ Modality detection from message content: Task 2
- ✅ Execution flow (load → detect → score → filter → sort → execute → fallback → persist): Task 8
- ✅ Antigravity chat() (SSE stream collection): Task 5
- ✅ Claude Code chat() (Anthropic format, system extraction): Task 4
- ✅ OpenCode Zen chat() (OpenAI-compatible): Task 3
- ✅ Trace: only rule IDs and decision reasons — no secrets: Tasks 7, 8
- ✅ usage_records + routing_traces insertion: Task 7
- ✅ Fire-and-forget trace (never blocks response): Task 8
- ✅ maxFallbackAttempts from venom_models: Tasks 6, 8

**Placeholder scan:** No TBD, TODO, or vague steps. All code blocks are complete.

**Type consistency:**

- `RoutingCandidate.ruleId` used consistently across Tasks 2, 6, 7, 8
- `ChatResult.ok` / `ChatResult.content` / `ChatResult.inputTokens` / `ChatResult.outputTokens` defined in Task 1, used in Tasks 3–6
- `executeWithFallback(scored, req, maxAttempts)` defined in Task 6, called in Task 8
- `persistUsageAndTrace(opts)` defined in Task 7, called in Task 8
- `detectModality(messages)` → `Modality` defined in Task 2, used in Task 8

**No issues found.**

---

**Plan complete and saved to `docs/superpowers/plans/2026-06-22-phase3-routing-engine.md`. Two execution options:**

**1. Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration

**2. Inline Execution** — Execute tasks in this session using executing-plans

**Which approach?**
