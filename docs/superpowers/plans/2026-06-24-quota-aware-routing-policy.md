# Quota-Aware Routing Policy Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the current simple cost/speed/quality scorer with a multi-factor, tier-aware routing policy that enforces premium preservation, quota-aware filtering, task classification, account rotation, and escalation stages per venom tier.

**Architecture:** Four new/rewritten modules (`classifier.server.ts`, `policy.server.ts`, `rotation.server.ts`, updated `filter.server.ts`, updated `scorer.server.ts`) plus engine wiring that drives an escalation-stage loop — cheaper cost tiers are tried first, escalating to premium only when justified by tier policy and task complexity.

**Tech Stack:** TypeScript, Bun test (`bun:test` + `describe/it/expect/mock`), Supabase

## Global Constraints

- All server-only files must use `.server.ts` suffix
- Test files use `bun:test` — import with `from "bun:test"`, mock with `mock.module()`
- Import alias `@/` maps to `src/` — always use it, never relative `../../`
- Never edit `routeTree.gen.ts`
- No new npm/bun packages — use only what's already installed
- Do not edit `src/components/ui/` files directly
- `strategy_config` column in `venom_models` stores a JSON object matching `TierStrategyConfig`; the engine must fetch and apply it

---

## File Map

| Action | Path | Responsibility |
|--------|------|----------------|
| MODIFY | `src/lib/routing/types.ts` | Add `CostType`, `TaskClass`, `EscalationStage`, update `RoutingCandidate` |
| MODIFY | `src/lib/routing/strategy.types.ts` | Add `TierScoringWeights` type + `TIER_SCORING_WEIGHTS` presets, remove stale comment |
| CREATE | `src/lib/routing/classifier.server.ts` | Classify request into `TaskClass` |
| CREATE | `src/lib/routing/classifier.server.test.ts` | Tests for classifier |
| CREATE | `src/lib/routing/policy.server.ts` | `CostType` derivation, quality score, premium detection, escalation stages |
| CREATE | `src/lib/routing/policy.server.test.ts` | Tests for policy helpers |
| MODIFY | `src/lib/routing/filter.server.ts` | Strategy-aware quota threshold + premium reserve filter |
| CREATE | `src/lib/routing/filter.server.test.ts` | Tests for updated filter |
| MODIFY | `src/lib/routing/scorer.server.ts` | Multi-factor formula with tier-specific weights |
| CREATE | `src/lib/routing/scorer.server.test.ts` | Tests for new scorer |
| CREATE | `src/lib/routing/rotation.server.ts` | Account rotation strategies (quota_weighted, health_weighted, round_robin) |
| CREATE | `src/lib/routing/rotation.server.test.ts` | Tests for rotation |
| MODIFY | `src/lib/routing/engine.server.ts` | Wire classifier → policy → filter → rotation → scorer → escalation loop |

---

## Task 1: Extend Types

**Files:**
- Modify: `src/lib/routing/types.ts`
- Modify: `src/lib/routing/strategy.types.ts`

**Interfaces:**
- Produces: `CostType`, `TaskClass`, `EscalationStage`, updated `RoutingCandidate`, `TierScoringWeights`, `TIER_SCORING_WEIGHTS`

- [ ] **Step 1: Add `CostType` and `TaskClass` to `types.ts`**

Replace the entire file with:

```typescript
export type Modality = "text" | "vision" | "audio" | "documents";

/** Derived from model cost per million tokens. */
export type CostType = "free" | "cheap" | "balanced" | "premium";

/**
 * Task classification derived from request messages.
 * Used to weight scoring and select escalation stage entry points.
 */
export type TaskClass =
  | "simple_chat"
  | "coding"
  | "vision"
  | "tool_calling"
  | "long_context"
  | "reasoning_heavy"
  | "agentic_task"
  | "critical_task";

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
  /** Derived at engine time — not stored in DB. */
  costType?: CostType;
  /** Derived quality score (0–1) from priority. Higher priority = higher quality. */
  qualityScore?: number;
}

export interface ScoredCandidate {
  candidate: RoutingCandidate;
  score: number;
}

export interface RoutingTraceCandidate {
  rule_id: string;
  external_id: string;
  adapter: string;
  priority: number;
  role: string;
  score?: number;
  status: "eligible" | "filtered" | "attempted" | "selected";
  filter_reason?: string;
  error?: string;
}

export interface RoutingTrace {
  modality: Modality;
  task_class: TaskClass;
  candidates_evaluated: number;
  candidates_filtered: number;
  decision_reason: string;
  selected_rule_id: string | null;
  fallback_attempts: number;
  escalation_stage: number;
  candidates: RoutingTraceCandidate[];
  cost_usd: number;
}

export interface RoutingRequest {
  venomSlug: "lite" | "pro" | "max";
  messages: import("@/lib/providers/adapters/types").ChatMessage[];
  maxTokens?: number;
  temperature?: number;
  apiKeyId?: string;
  includeTrace?: boolean;
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
  trace?: RoutingTrace;
  costUsd?: number;
}
```

- [ ] **Step 2: Add `TierScoringWeights` to `strategy.types.ts`**

Replace the entire file with:

```typescript
import type { RoutingCondition } from "@/lib/routing/types";

export type AutoEscalation = "off" | "on_failure" | "on_quota" | "on_complexity";

export type AccountRotation = "off" | "round_robin" | "quota_weighted" | "health_weighted";

export type HealthRequirement = "healthy_only" | "allow_degraded";

export type FallbackBehavior = "sequential" | "skip_exhausted" | "premium_last";

export type TierStrategyConfig = {
  quota_threshold_pct: number;
  premium_reserve_pct: number;
  auto_escalation: AutoEscalation;
  account_rotation: AccountRotation;
  health_requirement: HealthRequirement;
  fallback_behavior: FallbackBehavior;
};

export type VenomTier = "lite" | "pro" | "max";

export const TIER_STRATEGY_PRESETS: Record<VenomTier, TierStrategyConfig> = {
  lite: {
    quota_threshold_pct: 15,
    premium_reserve_pct: 5,
    auto_escalation: "on_failure",
    account_rotation: "quota_weighted",
    health_requirement: "healthy_only",
    fallback_behavior: "premium_last",
  },
  pro: {
    quota_threshold_pct: 10,
    premium_reserve_pct: 15,
    auto_escalation: "on_complexity",
    account_rotation: "health_weighted",
    health_requirement: "healthy_only",
    fallback_behavior: "sequential",
  },
  max: {
    quota_threshold_pct: 5,
    premium_reserve_pct: 25,
    auto_escalation: "on_failure",
    account_rotation: "quota_weighted",
    health_requirement: "healthy_only",
    fallback_behavior: "premium_last",
  },
};

export function mergeStrategyConfig(
  tier: VenomTier,
  partial: Partial<TierStrategyConfig> | null | undefined,
): TierStrategyConfig {
  return { ...TIER_STRATEGY_PRESETS[tier], ...(partial ?? {}) };
}

/** Per-factor scoring weights for each tier. All values 0–1. */
export type TierScoringWeights = {
  quality: number;
  quota: number;
  accountBalance: number;
  taskFit: number;
  health: number;
  costPenalty: number;
  premiumPressure: number;
  overuse: number;
};

export const TIER_SCORING_WEIGHTS: Record<VenomTier, TierScoringWeights> = {
  lite: {
    quality: 0.3,
    quota: 0.6,
    accountBalance: 0.5,
    taskFit: 0.3,
    health: 0.5,
    costPenalty: 0.8,
    premiumPressure: 0.9,
    overuse: 0.6,
  },
  pro: {
    quality: 0.6,
    quota: 0.5,
    accountBalance: 0.6,
    taskFit: 0.5,
    health: 0.6,
    costPenalty: 0.4,
    premiumPressure: 0.6,
    overuse: 0.4,
  },
  max: {
    quality: 0.9,
    quota: 0.4,
    accountBalance: 0.4,
    taskFit: 0.8,
    health: 0.5,
    costPenalty: 0.1,
    premiumPressure: 0.3,
    overuse: 0.3,
  },
};

export type { RoutingCondition };
```

- [ ] **Step 3: Commit**

```bash
git add src/lib/routing/types.ts src/lib/routing/strategy.types.ts
git commit -m "feat(routing): extend types with CostType, TaskClass, TierScoringWeights"
```

---

## Task 2: Policy Module

Responsible for pure, side-effect-free helpers: deriving `CostType` and `qualityScore` from a candidate, detecting premium status, and defining escalation stage configurations per tier.

**Files:**
- Create: `src/lib/routing/policy.server.ts`
- Create: `src/lib/routing/policy.server.test.ts`

**Interfaces:**
- Consumes: `RoutingCandidate` (Task 1), `VenomTier`, `CostType`
- Produces:
  - `getCostType(c: RoutingCandidate): CostType`
  - `getQualityScore(c: RoutingCandidate): number` (returns 0–1)
  - `isPremium(c: RoutingCandidate): boolean`
  - `enrichCandidate(c: RoutingCandidate): RoutingCandidate` (adds costType + qualityScore)
  - `EscalationStage` type: `{ allowedCostTypes: CostType[]; requireHighQuality?: boolean }`
  - `getEscalationStages(tier: VenomTier): EscalationStage[]`

- [ ] **Step 1: Write failing tests for policy helpers**

Create `src/lib/routing/policy.server.test.ts`:

```typescript
import { describe, it, expect } from "bun:test";
import {
  getCostType,
  getQualityScore,
  isPremium,
  enrichCandidate,
  getEscalationStages,
} from "./policy.server";
import type { RoutingCandidate } from "@/lib/routing/types";

function makeCandidate(
  overrides: Partial<RoutingCandidate["model"]> = {},
  priority = 5,
): RoutingCandidate {
  return {
    ruleId: "r1",
    priority,
    role: "primary",
    condition: null,
    model: {
      id: "m1",
      externalId: "model-x",
      lifecycle: "approved",
      enabled: true,
      inputCostPerMtok: null,
      outputCostPerMtok: null,
      capabilities: [],
      latencyMs: null,
      provider: { adapter: "test", baseUrl: null },
      ...overrides,
    },
    account: {
      id: "a1",
      status: "healthy",
      credentialsEnc: null,
      credentialsIv: null,
      credentialsTag: null,
      quota: null,
    },
  };
}

describe("getCostType", () => {
  it("returns free when both costs are null", () => {
    expect(getCostType(makeCandidate())).toBe("free");
  });

  it("returns free when both costs are 0", () => {
    expect(getCostType(makeCandidate({ inputCostPerMtok: 0, outputCostPerMtok: 0 }))).toBe("free");
  });

  it("returns cheap when cost is 0.3/mtok", () => {
    expect(getCostType(makeCandidate({ inputCostPerMtok: 0.3, outputCostPerMtok: 0.6 }))).toBe(
      "cheap",
    );
  });

  it("returns balanced when cost is 2/mtok", () => {
    expect(getCostType(makeCandidate({ inputCostPerMtok: 2, outputCostPerMtok: 6 }))).toBe(
      "balanced",
    );
  });

  it("returns premium when cost is 15/mtok", () => {
    expect(getCostType(makeCandidate({ inputCostPerMtok: 15, outputCostPerMtok: 60 }))).toBe(
      "premium",
    );
  });
});

describe("getQualityScore", () => {
  it("returns a value between 0 and 1", () => {
    const score = getQualityScore(makeCandidate({}, 5));
    expect(score).toBeGreaterThan(0);
    expect(score).toBeLessThanOrEqual(1);
  });

  it("lower priority number = higher quality score", () => {
    const high = getQualityScore(makeCandidate({}, 1));
    const low = getQualityScore(makeCandidate({}, 10));
    expect(high).toBeGreaterThan(low);
  });
});

describe("isPremium", () => {
  it("returns true for premium cost type", () => {
    expect(isPremium(makeCandidate({ inputCostPerMtok: 15, outputCostPerMtok: 60 }))).toBe(true);
  });

  it("returns false for free model", () => {
    expect(isPremium(makeCandidate())).toBe(false);
  });
});

describe("enrichCandidate", () => {
  it("adds costType and qualityScore to candidate", () => {
    const c = makeCandidate({ inputCostPerMtok: 0.3, outputCostPerMtok: 0.6 }, 3);
    const enriched = enrichCandidate(c);
    expect(enriched.costType).toBe("cheap");
    expect(typeof enriched.qualityScore).toBe("number");
  });
});

describe("getEscalationStages", () => {
  it("lite has 3 stages and no premium in any stage", () => {
    const stages = getEscalationStages("lite");
    expect(stages).toHaveLength(3);
    for (const stage of stages) {
      expect(stage.allowedCostTypes).not.toContain("premium");
    }
  });

  it("pro has 3 stages; last stage includes premium", () => {
    const stages = getEscalationStages("pro");
    expect(stages).toHaveLength(3);
    expect(stages[2].allowedCostTypes).toContain("premium");
  });

  it("max has 4 stages; last stage is premium with requireHighQuality", () => {
    const stages = getEscalationStages("max");
    expect(stages).toHaveLength(4);
    expect(stages[3].allowedCostTypes).toContain("premium");
    expect(stages[3].requireHighQuality).toBe(true);
  });

  it("each stage expands or equals previous allowed cost types", () => {
    for (const tier of ["lite", "pro", "max"] as const) {
      const stages = getEscalationStages(tier);
      for (let i = 1; i < stages.length; i++) {
        // Each stage's allowed set must include everything from the prior
        const prev = new Set(stages[i - 1].allowedCostTypes);
        for (const ct of prev) {
          expect(stages[i].allowedCostTypes).toContain(ct);
        }
      }
    }
  });
});
```

- [ ] **Step 2: Run tests to confirm they fail**

```bash
bun test src/lib/routing/policy.server.test.ts 2>&1 | head -30
```

Expected: `Cannot find module './policy.server'`

- [ ] **Step 3: Implement `policy.server.ts`**

Create `src/lib/routing/policy.server.ts`:

```typescript
import type { CostType, RoutingCandidate } from "@/lib/routing/types";
import type { VenomTier } from "@/lib/routing/strategy.types";

/**
 * Thresholds (input cost per million tokens) for cost classification.
 * free:     both costs null or 0
 * cheap:    avg ≤ 0.5
 * balanced: avg ≤ 5
 * premium:  avg > 5
 */
const COST_CHEAP_THRESHOLD = 0.5;
const COST_BALANCED_THRESHOLD = 5;

function avgCostPerMtok(c: RoutingCandidate): number {
  const input = c.model.inputCostPerMtok;
  const output = c.model.outputCostPerMtok;
  if ((input === null || input === 0) && (output === null || output === 0)) return 0;
  const i = input ?? 0;
  const o = output ?? i * 3;
  return (i + o * 3) / 4;
}

export function getCostType(c: RoutingCandidate): CostType {
  const avg = avgCostPerMtok(c);
  if (avg === 0) return "free";
  if (avg <= COST_CHEAP_THRESHOLD) return "cheap";
  if (avg <= COST_BALANCED_THRESHOLD) return "balanced";
  return "premium";
}

export function getQualityScore(c: RoutingCandidate): number {
  // priority 1 → score near 1.0; priority 10 → score near 0.1
  // 1 / (priority + 1) * 2 capped at 1
  return Math.min(1, 2 / (c.priority + 1));
}

export function isPremium(c: RoutingCandidate): boolean {
  return getCostType(c) === "premium";
}

export function enrichCandidate(c: RoutingCandidate): RoutingCandidate {
  return {
    ...c,
    costType: getCostType(c),
    qualityScore: getQualityScore(c),
  };
}

export interface EscalationStage {
  /** Cost types allowed in this stage. */
  allowedCostTypes: CostType[];
  /** If true, only candidates with qualityScore >= 0.6 pass. Used in Max stage 1. */
  requireHighQuality?: boolean;
}

const ESCALATION_STAGES: Record<VenomTier, EscalationStage[]> = {
  lite: [
    { allowedCostTypes: ["free"] },
    { allowedCostTypes: ["free", "cheap"] },
    { allowedCostTypes: ["free", "cheap", "balanced"] },
  ],
  pro: [
    { allowedCostTypes: ["free", "cheap"] },
    { allowedCostTypes: ["free", "cheap", "balanced"] },
    { allowedCostTypes: ["free", "cheap", "balanced", "premium"] },
  ],
  max: [
    { allowedCostTypes: ["free"], requireHighQuality: true },
    { allowedCostTypes: ["free", "cheap", "balanced"] },
    { allowedCostTypes: ["free", "cheap", "balanced", "premium"] },
    { allowedCostTypes: ["premium"], requireHighQuality: true },
  ],
};

export function getEscalationStages(tier: VenomTier): EscalationStage[] {
  return ESCALATION_STAGES[tier];
}
```

- [ ] **Step 4: Run tests to confirm they pass**

```bash
bun test src/lib/routing/policy.server.test.ts
```

Expected: all 12 tests pass.

- [ ] **Step 5: Commit**

```bash
git add src/lib/routing/policy.server.ts src/lib/routing/policy.server.test.ts
git commit -m "feat(routing): add policy module with CostType derivation and escalation stages"
```

---

## Task 3: Task Classifier

Classifies an incoming request's messages into a `TaskClass` based on content patterns.

**Files:**
- Create: `src/lib/routing/classifier.server.ts`
- Create: `src/lib/routing/classifier.server.test.ts`

**Interfaces:**
- Consumes: `ChatMessage[]` from `@/lib/providers/adapters/types`
- Produces: `classifyTask(messages: ChatMessage[]): TaskClass`

- [ ] **Step 1: Write failing tests**

Create `src/lib/routing/classifier.server.test.ts`:

```typescript
import { describe, it, expect } from "bun:test";
import { classifyTask } from "./classifier.server";
import type { ChatMessage } from "@/lib/providers/adapters/types";

function textMsg(text: string): ChatMessage {
  return { role: "user", content: text };
}

function visionMsg(): ChatMessage {
  return {
    role: "user",
    content: [
      { type: "image_url", image_url: { url: "data:image/png;base64,abc" } },
      { type: "text", text: "What is in this image?" },
    ] as unknown as string,
  };
}

describe("classifyTask", () => {
  it("classifies image message as vision", () => {
    expect(classifyTask([visionMsg()])).toBe("vision");
  });

  it("classifies coding keywords as coding", () => {
    expect(classifyTask([textMsg("Fix this TypeScript function: function foo() { return 1; }")])).toBe("coding");
  });

  it("classifies code block as coding", () => {
    expect(classifyTask([textMsg("Review this code:\n```python\ndef main(): pass\n```")])).toBe("coding");
  });

  it("classifies tool_calls mention as tool_calling", () => {
    expect(classifyTask([textMsg("Use the search tool to find relevant docs")])).toBe("tool_calling");
  });

  it("classifies long messages as long_context", () => {
    const longText = "word ".repeat(3000); // ~15000 chars
    expect(classifyTask([textMsg(longText)])).toBe("long_context");
  });

  it("classifies agent/step keywords as agentic_task", () => {
    expect(classifyTask([textMsg("Complete this multi-step task: first search, then summarize, then write")])).toBe("agentic_task");
  });

  it("classifies short generic messages as simple_chat", () => {
    expect(classifyTask([textMsg("Hello, how are you?")])).toBe("simple_chat");
  });

  it("classifies 'why' / 'explain' / 'reason' as reasoning_heavy", () => {
    expect(classifyTask([textMsg("Explain in depth why functional programming is better")])).toBe("reasoning_heavy");
  });

  it("prioritizes vision over coding", () => {
    const mixed: ChatMessage = {
      role: "user",
      content: [
        { type: "image_url", image_url: { url: "data:image/png;base64,abc" } },
        { type: "text", text: "Fix the bug in this code ```js const x = 1```" },
      ] as unknown as string,
    };
    expect(classifyTask([mixed])).toBe("vision");
  });
});
```

- [ ] **Step 2: Run to confirm failure**

```bash
bun test src/lib/routing/classifier.server.test.ts 2>&1 | head -10
```

Expected: `Cannot find module './classifier.server'`

- [ ] **Step 3: Implement classifier**

Create `src/lib/routing/classifier.server.ts`:

```typescript
import type { ChatMessage } from "@/lib/providers/adapters/types";
import type { TaskClass } from "@/lib/routing/types";

const LONG_CONTEXT_CHAR_THRESHOLD = 10_000;

function extractTextContent(messages: ChatMessage[]): string {
  return messages
    .map((m) => {
      if (typeof m.content === "string") return m.content;
      if (Array.isArray(m.content)) {
        return (m.content as Array<{ type: string; text?: string }>)
          .filter((p) => p.type === "text")
          .map((p) => p.text ?? "")
          .join(" ");
      }
      return "";
    })
    .join(" ");
}

function hasImageContent(messages: ChatMessage[]): boolean {
  for (const m of messages) {
    if (Array.isArray(m.content)) {
      const parts = m.content as Array<{ type: string }>;
      if (parts.some((p) => p.type === "image_url" || p.type === "image")) return true;
    }
  }
  return false;
}

export function classifyTask(messages: ChatMessage[]): TaskClass {
  if (hasImageContent(messages)) return "vision";

  const text = extractTextContent(messages);

  if (text.length > LONG_CONTEXT_CHAR_THRESHOLD) return "long_context";

  // Tool calling: explicit mention of tools or function calls
  if (/\b(use the .+ tool|call the|function call|tool_call|tool use)\b/i.test(text)) {
    return "tool_calling";
  }

  // Agentic: multi-step workflows
  if (/\b(step[- ]by[- ]step|multi[- ]step|first .+ then .+|agent|complete this task)\b/i.test(text)) {
    return "agentic_task";
  }

  // Coding: code blocks or programming keywords
  if (/```|\bfunction\b|\bclass\b|\bconst\b|\bdef\b|\bimport\b|\bfix (this|the) (code|bug|error)\b/i.test(text)) {
    return "coding";
  }

  // Reasoning: deep explanation requests
  if (/\b(explain in depth|why is|reason(ing)?|analyze|compare|evaluate|pros and cons)\b/i.test(text)) {
    return "reasoning_heavy";
  }

  return "simple_chat";
}
```

- [ ] **Step 4: Run tests to confirm they pass**

```bash
bun test src/lib/routing/classifier.server.test.ts
```

Expected: all 9 tests pass.

- [ ] **Step 5: Commit**

```bash
git add src/lib/routing/classifier.server.ts src/lib/routing/classifier.server.test.ts
git commit -m "feat(routing): add task classifier"
```

---

## Task 4: Strategy-Aware Filter

Updates `filter.server.ts` to use the tier's `quota_threshold_pct` (replaces the hardcoded 5%) and `premium_reserve_pct` (rejects premium models whose quota is within the reserve window).

**Files:**
- Modify: `src/lib/routing/filter.server.ts`
- Create: `src/lib/routing/filter.server.test.ts`

**Interfaces:**
- Consumes: `RoutingCandidate`, `Modality`, `TierStrategyConfig` (from Task 1/strategy.types.ts)
- Produces:
  - Updated `getFilterReason(candidate, modality, strategy): string | null`
  - Updated `filterCandidatesWithDiagnostics(candidates, modality, strategy): FilterDiagnostics`
  - Updated `filterCandidates(candidates, modality, strategy): RoutingCandidate[]`
  - `isQuotaExhausted(quota, thresholdPct): boolean` — exported for testing

- [ ] **Step 1: Write failing tests**

Create `src/lib/routing/filter.server.test.ts`:

```typescript
import { describe, it, expect } from "bun:test";
import {
  isQuotaExhausted,
  getFilterReason,
  filterCandidatesWithDiagnostics,
} from "./filter.server";
import type { RoutingCandidate } from "@/lib/routing/types";
import { TIER_STRATEGY_PRESETS } from "@/lib/routing/strategy.types";

function makeCandidate(overrides: {
  lifecycle?: string;
  enabled?: boolean;
  accountStatus?: string;
  quota?: { used: number; total: number | null; confidence: string } | null;
  capabilities?: string[];
  costType?: import("@/lib/routing/types").CostType;
  priority?: number;
}): RoutingCandidate {
  return {
    ruleId: "r1",
    priority: overrides.priority ?? 1,
    role: "primary",
    condition: null,
    model: {
      id: "m1",
      externalId: "model-x",
      lifecycle: overrides.lifecycle ?? "approved",
      enabled: overrides.enabled ?? true,
      inputCostPerMtok: null,
      outputCostPerMtok: null,
      capabilities: overrides.capabilities ?? [],
      latencyMs: null,
      provider: { adapter: "test", baseUrl: null },
    },
    account: {
      id: "a1",
      status: overrides.accountStatus ?? "healthy",
      credentialsEnc: null,
      credentialsIv: null,
      credentialsTag: null,
      quota: overrides.quota !== undefined ? overrides.quota : null,
    },
    costType: overrides.costType,
  };
}

describe("isQuotaExhausted", () => {
  it("returns false when quota is null", () => {
    expect(isQuotaExhausted(null, 15)).toBe(false);
  });

  it("returns false when confidence is not high", () => {
    expect(isQuotaExhausted({ used: 95, total: 100, confidence: "low" }, 15)).toBe(false);
  });

  it("returns true when remaining < threshold", () => {
    // remaining = 5/100 = 5%, threshold = 15% → exhausted
    expect(isQuotaExhausted({ used: 95, total: 100, confidence: "high" }, 15)).toBe(true);
  });

  it("returns false when remaining >= threshold", () => {
    // remaining = 20/100 = 20%, threshold = 15% → not exhausted
    expect(isQuotaExhausted({ used: 80, total: 100, confidence: "high" }, 15)).toBe(false);
  });

  it("returns false when total is null", () => {
    expect(isQuotaExhausted({ used: 95, total: null, confidence: "high" }, 15)).toBe(false);
  });
});

describe("getFilterReason with strategy", () => {
  const liteStrategy = TIER_STRATEGY_PRESETS.lite;
  const maxStrategy = TIER_STRATEGY_PRESETS.max;

  it("returns lifecycle_not_approved for unapproved model", () => {
    expect(getFilterReason(makeCandidate({ lifecycle: "discovered" }), "text", liteStrategy)).toBe(
      "lifecycle_not_approved",
    );
  });

  it("returns model_disabled when disabled", () => {
    expect(getFilterReason(makeCandidate({ enabled: false }), "text", liteStrategy)).toBe(
      "model_disabled",
    );
  });

  it("returns account_unhealthy for unhealthy account", () => {
    expect(
      getFilterReason(makeCandidate({ accountStatus: "suspended" }), "text", liteStrategy),
    ).toBe("account_unhealthy");
  });

  it("returns quota_exhausted using lite threshold (15%)", () => {
    // 92% used = 8% remaining, below 15% threshold
    const quota = { used: 92, total: 100, confidence: "high" };
    expect(getFilterReason(makeCandidate({ quota }), "text", liteStrategy)).toBe("quota_exhausted");
  });

  it("does NOT exhaust at 8% remaining for max threshold (5%)", () => {
    // 92% used = 8% remaining, above 5% threshold
    const quota = { used: 92, total: 100, confidence: "high" };
    expect(getFilterReason(makeCandidate({ quota }), "text", maxStrategy)).toBeNull();
  });

  it("returns premium_reserved when premium model within reserve", () => {
    // lite reserve = 5%, so 3% remaining triggers reserve
    const quota = { used: 97, total: 100, confidence: "high" };
    const c = makeCandidate({ quota, costType: "premium" });
    expect(getFilterReason(c, "text", liteStrategy)).toBe("premium_reserved");
  });

  it("does not reserve premium when outside reserve window", () => {
    // lite reserve = 5%, 10% remaining is outside reserve
    const quota = { used: 90, total: 100, confidence: "high" };
    const c = makeCandidate({ quota, costType: "premium" });
    // Should be null (not filtered) since 10% > 5% reserve AND 10% > 15% threshold? 
    // Actually 10% < 15% lite threshold, so it's quota_exhausted. Use a different quota.
    const quota2 = { used: 70, total: 100, confidence: "high" };
    const c2 = makeCandidate({ quota: quota2, costType: "premium" });
    expect(getFilterReason(c2, "text", liteStrategy)).toBeNull();
  });

  it("returns missing_capability for vision request without vision capability", () => {
    expect(getFilterReason(makeCandidate({ capabilities: [] }), "vision", liteStrategy)).toBe(
      "missing_capability:vision",
    );
  });

  it("passes candidate with vision capability for vision request", () => {
    expect(
      getFilterReason(makeCandidate({ capabilities: ["vision"] }), "vision", liteStrategy),
    ).toBeNull();
  });
});

describe("filterCandidatesWithDiagnostics", () => {
  it("separates eligible from rejected", () => {
    const strategy = TIER_STRATEGY_PRESETS.pro;
    const eligible = makeCandidate({});
    const rejected = makeCandidate({ lifecycle: "discovered" });
    const { eligible: e, rejected: r } = filterCandidatesWithDiagnostics(
      [eligible, rejected],
      "text",
      strategy,
    );
    expect(e).toHaveLength(1);
    expect(r).toHaveLength(1);
    expect(r[0].reason).toBe("lifecycle_not_approved");
  });
});
```

- [ ] **Step 2: Run to confirm failure**

```bash
bun test src/lib/routing/filter.server.test.ts 2>&1 | head -20
```

Expected: errors about missing export `isQuotaExhausted` and wrong function signatures.

- [ ] **Step 3: Rewrite `filter.server.ts`**

Replace the entire file:

```typescript
import type { ChatMessage } from "@/lib/providers/adapters/types";
import type { CostType, Modality, RoutingCandidate, RoutingCondition } from "@/lib/routing/types";
import type { TierStrategyConfig } from "@/lib/routing/strategy.types";
import { getCostType } from "@/lib/routing/policy.server";

/**
 * Detects the modality of a request from its messages.
 */
export function detectModality(messages: ChatMessage[]): Modality {
  for (const msg of messages) {
    if (typeof msg.content !== "string") {
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

export function isQuotaExhausted(
  quota: { used: number; total: number | null; confidence: string } | null,
  thresholdPct: number,
): boolean {
  if (!quota) return false;
  if (quota.confidence !== "high") return false;
  if (quota.total === null || quota.total <= 0) return false;
  const remainingPct = ((quota.total - quota.used) / quota.total) * 100;
  return remainingPct < thresholdPct;
}

function isPremiumReserved(
  candidate: RoutingCandidate,
  quota: { used: number; total: number | null; confidence: string } | null,
  reservePct: number,
): boolean {
  const costType = candidate.costType ?? getCostType(candidate);
  if (costType !== "premium") return false;
  if (!quota || quota.confidence !== "high") return false;
  if (quota.total === null || quota.total <= 0) return false;
  const remainingPct = ((quota.total - quota.used) / quota.total) * 100;
  return remainingPct < reservePct;
}

function matchesCondition(condition: RoutingCondition | null, capabilities: string[]): boolean {
  if (!condition) return true;
  if (condition.requires?.length) {
    for (const cap of condition.requires) {
      if (!capabilities.includes(cap)) return false;
    }
  }
  return true;
}

export function getFilterReason(
  candidate: RoutingCandidate,
  modality: Modality,
  strategy: TierStrategyConfig,
): string | null {
  if (candidate.model.lifecycle !== "approved") return "lifecycle_not_approved";
  if (!candidate.model.enabled) return "model_disabled";
  if (candidate.account.status !== "healthy") return "account_unhealthy";

  const quota = candidate.account.quota;

  if (isQuotaExhausted(quota, strategy.quota_threshold_pct)) return "quota_exhausted";

  if (isPremiumReserved(candidate, quota, strategy.premium_reserve_pct)) return "premium_reserved";

  if (modality !== "text") {
    const caps = candidate.model.capabilities;
    if (!caps.includes(modality)) return `missing_capability:${modality}`;
  }

  if (!matchesCondition(candidate.condition, candidate.model.capabilities)) {
    const required = candidate.condition?.requires?.join(",") ?? "unknown";
    return `condition_requires:${required}`;
  }

  return null;
}

export interface FilterDiagnostics {
  eligible: RoutingCandidate[];
  rejected: Array<{ candidate: RoutingCandidate; reason: string }>;
}

export function filterCandidatesWithDiagnostics(
  candidates: RoutingCandidate[],
  modality: Modality,
  strategy: TierStrategyConfig,
): FilterDiagnostics {
  const eligible: RoutingCandidate[] = [];
  const rejected: Array<{ candidate: RoutingCandidate; reason: string }> = [];

  for (const c of candidates) {
    const reason = getFilterReason(c, modality, strategy);
    if (reason) {
      rejected.push({ candidate: c, reason });
    } else {
      eligible.push(c);
    }
  }

  return { eligible, rejected };
}

export function filterCandidates(
  candidates: RoutingCandidate[],
  modality: Modality,
  strategy: TierStrategyConfig,
): RoutingCandidate[] {
  return filterCandidatesWithDiagnostics(candidates, modality, strategy).eligible;
}
```

- [ ] **Step 4: Run tests**

```bash
bun test src/lib/routing/filter.server.test.ts
```

Expected: all tests pass.

- [ ] **Step 5: Commit**

```bash
git add src/lib/routing/filter.server.ts src/lib/routing/filter.server.test.ts
git commit -m "feat(routing): strategy-aware quota threshold and premium reserve filter"
```

---

## Task 5: Multi-Factor Scorer

Rewrites `scorer.server.ts` to use the new multi-factor formula with tier-specific weights from `TIER_SCORING_WEIGHTS`.

**Files:**
- Modify: `src/lib/routing/scorer.server.ts`
- Create: `src/lib/routing/scorer.server.test.ts`

**Interfaces:**
- Consumes: `RoutingCandidate` (enriched with costType/qualityScore), `VenomTier`, `TaskClass`, `TierScoringWeights`
- Produces:
  - `scoreCandidate(c: RoutingCandidate, tier: VenomTier, taskClass: TaskClass, allCandidates: RoutingCandidate[]): number`

Note: `allCandidates` is needed to compute `accountBalanceScore` (how loaded this account is relative to others).

- [ ] **Step 1: Write failing tests**

Create `src/lib/routing/scorer.server.test.ts`:

```typescript
import { describe, it, expect } from "bun:test";
import { scoreCandidate } from "./scorer.server";
import type { RoutingCandidate } from "@/lib/routing/types";

function makeCandidate(overrides: {
  priority?: number;
  inputCostPerMtok?: number | null;
  outputCostPerMtok?: number | null;
  accountStatus?: string;
  quota?: { used: number; total: number | null; confidence: string } | null;
  costType?: import("@/lib/routing/types").CostType;
  qualityScore?: number;
  latencyMs?: number | null;
}): RoutingCandidate {
  return {
    ruleId: "r1",
    priority: overrides.priority ?? 5,
    role: "primary",
    condition: null,
    model: {
      id: "m1",
      externalId: "model-x",
      lifecycle: "approved",
      enabled: true,
      inputCostPerMtok: overrides.inputCostPerMtok ?? null,
      outputCostPerMtok: overrides.outputCostPerMtok ?? null,
      capabilities: [],
      latencyMs: overrides.latencyMs ?? null,
      provider: { adapter: "test", baseUrl: null },
    },
    account: {
      id: "a1",
      status: overrides.accountStatus ?? "healthy",
      credentialsEnc: null,
      credentialsIv: null,
      credentialsTag: null,
      quota: overrides.quota !== undefined ? overrides.quota : null,
    },
    costType: overrides.costType,
    qualityScore: overrides.qualityScore,
  };
}

describe("scoreCandidate", () => {
  it("returns a positive number", () => {
    const c = makeCandidate({});
    const score = scoreCandidate(c, "pro", "simple_chat", [c]);
    expect(score).toBeGreaterThan(0);
  });

  it("lite scores free model higher than premium model", () => {
    const free = makeCandidate({ costType: "free", inputCostPerMtok: 0 });
    const premium = makeCandidate({ costType: "premium", inputCostPerMtok: 15, outputCostPerMtok: 60 });
    const all = [free, premium];
    expect(scoreCandidate(free, "lite", "simple_chat", all)).toBeGreaterThan(
      scoreCandidate(premium, "lite", "simple_chat", all),
    );
  });

  it("max scores high-quality model higher than low-quality for complex task", () => {
    const highQ = makeCandidate({ priority: 1, qualityScore: 0.9, costType: "premium", inputCostPerMtok: 15 });
    const lowQ = makeCandidate({ priority: 10, qualityScore: 0.1, costType: "free" });
    const all = [highQ, lowQ];
    expect(scoreCandidate(highQ, "max", "reasoning_heavy", all)).toBeGreaterThan(
      scoreCandidate(lowQ, "max", "reasoning_heavy", all),
    );
  });

  it("premium model is penalized more for lite than max", () => {
    const premium = makeCandidate({ costType: "premium", inputCostPerMtok: 15, outputCostPerMtok: 60, qualityScore: 0.9 });
    const litePenalty = scoreCandidate(premium, "lite", "simple_chat", [premium]);
    const maxPenalty = scoreCandidate(premium, "max", "simple_chat", [premium]);
    expect(maxPenalty).toBeGreaterThan(litePenalty);
  });

  it("candidate with more remaining quota scores higher", () => {
    const highQuota = makeCandidate({ quota: { used: 10, total: 100, confidence: "high" } });
    const lowQuota = makeCandidate({ quota: { used: 90, total: 100, confidence: "high" } });
    const all = [highQuota, lowQuota];
    expect(scoreCandidate(highQuota, "pro", "coding", all)).toBeGreaterThan(
      scoreCandidate(lowQuota, "pro", "coding", all),
    );
  });
});
```

- [ ] **Step 2: Run to confirm failure**

```bash
bun test src/lib/routing/scorer.server.test.ts 2>&1 | head -20
```

Expected: errors about wrong function signature (current scorer takes `VenomWeights`, not `VenomTier`).

- [ ] **Step 3: Rewrite `scorer.server.ts`**

Replace the entire file:

```typescript
import type { RoutingCandidate } from "@/lib/routing/types";
import type { TaskClass } from "@/lib/routing/types";
import type { VenomTier } from "@/lib/routing/strategy.types";
import { TIER_SCORING_WEIGHTS } from "@/lib/routing/strategy.types";
import { getCostType, getQualityScore } from "@/lib/routing/policy.server";

/** Compute what fraction of this account's usage relative to all candidates (0–1). */
function accountOveruseFraction(candidate: RoutingCandidate, all: RoutingCandidate[]): number {
  const totalUsed = all.reduce((sum, c) => sum + (c.account.quota?.used ?? 0), 0);
  if (totalUsed === 0) return 0;
  const thisUsed = candidate.account.quota?.used ?? 0;
  return thisUsed / totalUsed;
}

/** Remaining quota fraction (0–1). Returns 0.5 when quota unknown. */
function quotaRemainingFraction(
  quota: { used: number; total: number | null; confidence: string } | null,
): number {
  if (!quota || quota.total === null || quota.total <= 0) return 0.5;
  return Math.max(0, (quota.total - quota.used) / quota.total);
}

/** Task fit bonus: coding tasks benefit from coding-capable models. */
function taskFitScore(candidate: RoutingCandidate, taskClass: TaskClass): number {
  const caps = candidate.model.capabilities;
  switch (taskClass) {
    case "coding":
      return caps.includes("coding") ? 1 : 0.4;
    case "vision":
      return caps.includes("vision") ? 1 : 0;
    case "tool_calling":
      return caps.includes("tools") ? 1 : 0.3;
    case "long_context":
      return caps.includes("long_context") ? 1 : 0.5;
    case "reasoning_heavy":
      return caps.includes("reasoning") ? 1 : 0.6;
    default:
      return 0.7;
  }
}

/**
 * Multi-factor scorer.
 *
 * score = quality×w.quality + quotaRem×w.quota + (1-overuse)×w.accountBalance
 *       + taskFit×w.taskFit + healthBonus×w.health
 *       - costNorm×w.costPenalty - premiumPenalty×w.premiumPressure
 *       - overuse×w.overuse
 */
export function scoreCandidate(
  candidate: RoutingCandidate,
  tier: VenomTier,
  taskClass: TaskClass,
  allCandidates: RoutingCandidate[],
): number {
  const w = TIER_SCORING_WEIGHTS[tier];

  const qualityScore = candidate.qualityScore ?? getQualityScore(candidate);

  const quota = candidate.account.quota;
  const quotaScore = quotaRemainingFraction(quota);

  const overuse = accountOveruseFraction(candidate, allCandidates);
  const accountBalanceScore = 1 - overuse;

  const tFit = taskFitScore(candidate, taskClass);

  const healthScore = candidate.account.status === "healthy" ? 1 : 0.3;

  const costType = candidate.costType ?? getCostType(candidate);
  const costRank: Record<string, number> = { free: 0, cheap: 0.2, balanced: 0.5, premium: 1 };
  const costNorm = costRank[costType] ?? 0.5;

  const premiumPenalty = costType === "premium" ? 1 : 0;

  return (
    qualityScore * w.quality +
    quotaScore * w.quota +
    accountBalanceScore * w.accountBalance +
    tFit * w.taskFit +
    healthScore * w.health -
    costNorm * w.costPenalty -
    premiumPenalty * w.premiumPressure -
    overuse * w.overuse
  );
}
```

- [ ] **Step 4: Run tests**

```bash
bun test src/lib/routing/scorer.server.test.ts
```

Expected: all 5 tests pass.

- [ ] **Step 5: Commit**

```bash
git add src/lib/routing/scorer.server.ts src/lib/routing/scorer.server.test.ts
git commit -m "feat(routing): multi-factor tier-aware scorer with premium pressure penalty"
```

---

## Task 6: Account Rotation

Groups candidates by account ID and reorders them so all accounts are represented before any single account is exhausted.

**Files:**
- Create: `src/lib/routing/rotation.server.ts`
- Create: `src/lib/routing/rotation.server.test.ts`

**Interfaces:**
- Consumes: `ScoredCandidate[]`, `AccountRotation` strategy
- Produces: `applyAccountRotation(scored: ScoredCandidate[], strategy: AccountRotation): ScoredCandidate[]`

- [ ] **Step 1: Write failing tests**

Create `src/lib/routing/rotation.server.test.ts`:

```typescript
import { describe, it, expect } from "bun:test";
import { applyAccountRotation } from "./rotation.server";
import type { ScoredCandidate } from "@/lib/routing/types";

function makeScoredCandidate(
  accountId: string,
  score: number,
  used = 0,
  total: number | null = 100,
): ScoredCandidate {
  return {
    score,
    candidate: {
      ruleId: `r-${accountId}-${score}`,
      priority: 1,
      role: "primary",
      condition: null,
      model: {
        id: "m1",
        externalId: "model-x",
        lifecycle: "approved",
        enabled: true,
        inputCostPerMtok: null,
        outputCostPerMtok: null,
        capabilities: [],
        latencyMs: null,
        provider: { adapter: "test", baseUrl: null },
      },
      account: {
        id: accountId,
        status: "healthy",
        credentialsEnc: null,
        credentialsIv: null,
        credentialsTag: null,
        quota: total !== null ? { used, total, confidence: "high" } : null,
      },
    },
  };
}

describe("applyAccountRotation", () => {
  it("off: returns candidates in original order", () => {
    const a = makeScoredCandidate("a1", 0.9);
    const b = makeScoredCandidate("a2", 0.8);
    const c = makeScoredCandidate("a1", 0.7);
    const result = applyAccountRotation([a, b, c], "off");
    expect(result.map((r) => r.candidate.ruleId)).toEqual([a, b, c].map((x) => x.candidate.ruleId));
  });

  it("quota_weighted: interleaves accounts by remaining quota", () => {
    // a1 low quota (80 used/100), a2 high quota (20 used/100)
    const a1_1 = makeScoredCandidate("a1", 0.9, 80);
    const a1_2 = makeScoredCandidate("a1", 0.8, 80);
    const a2_1 = makeScoredCandidate("a2", 0.7, 20);
    // After rotation: a2 (more quota) should appear first for its slot
    const result = applyAccountRotation([a1_1, a1_2, a2_1], "quota_weighted");
    expect(result).toHaveLength(3);
    // The first candidate should be from a2 (higher remaining quota)
    expect(result[0].candidate.account.id).toBe("a2");
  });

  it("round_robin: interleaves candidates from different accounts", () => {
    const a1 = makeScoredCandidate("a1", 0.9);
    const a2 = makeScoredCandidate("a2", 0.8);
    const a3 = makeScoredCandidate("a1", 0.7);
    const result = applyAccountRotation([a1, a2, a3], "round_robin");
    expect(result).toHaveLength(3);
    // First two should be from different accounts
    expect(result[0].candidate.account.id).not.toBe(result[1].candidate.account.id);
  });

  it("health_weighted: healthy accounts preferred", () => {
    const healthy = makeScoredCandidate("a1", 0.5);
    const degraded = { ...makeScoredCandidate("a2", 0.9), score: 0.9 };
    degraded.candidate = { ...degraded.candidate, account: { ...degraded.candidate.account, status: "degraded" } };
    const result = applyAccountRotation([degraded, healthy], "health_weighted");
    expect(result[0].candidate.account.id).toBe("a1");
  });
});
```

- [ ] **Step 2: Run to confirm failure**

```bash
bun test src/lib/routing/rotation.server.test.ts 2>&1 | head -10
```

Expected: `Cannot find module './rotation.server'`

- [ ] **Step 3: Implement `rotation.server.ts`**

Create `src/lib/routing/rotation.server.ts`:

```typescript
import type { ScoredCandidate } from "@/lib/routing/types";
import type { AccountRotation } from "@/lib/routing/strategy.types";

function remainingQuotaFraction(
  quota: { used: number; total: number | null; confidence: string } | null,
): number {
  if (!quota || quota.total === null || quota.total <= 0) return 0.5;
  return Math.max(0, (quota.total - quota.used) / quota.total);
}

/**
 * Interleaves candidates so no single account dominates the top of the list.
 * Within each "round", the best candidate from each account is picked.
 */
function interleaveByAccount(
  scored: ScoredCandidate[],
  accountOrder: (accountIds: string[]) => string[],
): ScoredCandidate[] {
  // Group by account
  const byAccount = new Map<string, ScoredCandidate[]>();
  for (const sc of scored) {
    const id = sc.candidate.account.id;
    if (!byAccount.has(id)) byAccount.set(id, []);
    byAccount.get(id)!.push(sc);
  }

  const sortedAccountIds = accountOrder([...byAccount.keys()]);
  const result: ScoredCandidate[] = [];

  let remaining = scored.length;
  while (remaining > 0) {
    for (const accountId of sortedAccountIds) {
      const bucket = byAccount.get(accountId);
      if (bucket && bucket.length > 0) {
        result.push(bucket.shift()!);
        remaining--;
      }
    }
  }

  return result;
}

export function applyAccountRotation(
  scored: ScoredCandidate[],
  strategy: AccountRotation,
): ScoredCandidate[] {
  if (scored.length === 0 || strategy === "off") return scored;

  switch (strategy) {
    case "quota_weighted": {
      // Sort accounts by descending remaining quota, then interleave
      const quotaByAccount = new Map<string, number>();
      for (const sc of scored) {
        const id = sc.candidate.account.id;
        if (!quotaByAccount.has(id)) {
          quotaByAccount.set(id, remainingQuotaFraction(sc.candidate.account.quota));
        }
      }
      return interleaveByAccount(scored, (ids) =>
        [...ids].sort((a, b) => (quotaByAccount.get(b) ?? 0) - (quotaByAccount.get(a) ?? 0)),
      );
    }

    case "health_weighted": {
      // Healthy accounts first, then by score
      const healthScore = (sc: ScoredCandidate) =>
        sc.candidate.account.status === "healthy" ? 1 : 0;
      const accountBestScore = new Map<string, number>();
      for (const sc of scored) {
        const id = sc.candidate.account.id;
        const existing = accountBestScore.get(id) ?? -Infinity;
        accountBestScore.set(id, Math.max(existing, healthScore(sc) * 10 + sc.score));
      }
      return interleaveByAccount(scored, (ids) =>
        [...ids].sort(
          (a, b) => (accountBestScore.get(b) ?? 0) - (accountBestScore.get(a) ?? 0),
        ),
      );
    }

    case "round_robin": {
      // Sort account IDs deterministically (by ID string), then interleave
      return interleaveByAccount(scored, (ids) => [...ids].sort());
    }

    default:
      return scored;
  }
}
```

- [ ] **Step 4: Run tests**

```bash
bun test src/lib/routing/rotation.server.test.ts
```

Expected: all 4 tests pass.

- [ ] **Step 5: Commit**

```bash
git add src/lib/routing/rotation.server.ts src/lib/routing/rotation.server.test.ts
git commit -m "feat(routing): account rotation strategies (quota_weighted, health_weighted, round_robin)"
```

---

## Task 7: Wire Engine

Updates `engine.server.ts` to:
1. Fetch `strategy_config` from `venom_models`
2. Merge with tier presets via `mergeStrategyConfig`
3. Enrich all candidates with `costType` + `qualityScore`
4. Classify task with `classifyTask`
5. Apply the **escalation stage loop** — per stage: filter by allowed cost types + strategy, score, rotate, attempt; escalate only on failure
6. Use the new `scoreCandidate(c, tier, taskClass, stageEligible)` signature
7. Pass `taskClass` + `escalation_stage` into the trace

**Files:**
- Modify: `src/lib/routing/engine.server.ts`

**Interfaces:**
- Consumes: all modules from Tasks 1–6
- Produces: `routeRequest(req: RoutingRequest): Promise<RoutingResult>` (same external signature)

- [ ] **Step 1: Read the current engine file before editing**

Already read above. Confirm key change points:
- Line 75–79: add `strategy_config` to venom_models select
- Line 94–99: `weights` from DB stays (needed for backward compat in trace + `maxFallbackAttempts`)
- Line 222: `filterCandidatesWithDiagnostics` call needs `strategy` arg
- Line 280–282: scoring loop needs new signature + rotation
- Entirely new: escalation stage loop wrapping filter → score → rotate → execute

- [ ] **Step 2: Replace `engine.server.ts`**

Replace the entire file with:

```typescript
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
```

- [ ] **Step 3: Run all routing tests**

```bash
bun test src/lib/routing/
```

Expected: all tests pass (trace test, classifier, policy, filter, scorer, rotation).

- [ ] **Step 4: Check TypeScript compilation**

```bash
bun run build 2>&1 | head -40
```

Fix any type errors before committing. Common issue: `ChatMessage` import in `filter.server.ts` — verify `@/lib/providers/adapters/types` exports `ChatMessage`. If not, remove the import (it's only used by `detectModality` which already had it).

- [ ] **Step 5: Commit**

```bash
git add src/lib/routing/engine.server.ts
git commit -m "feat(routing): wire escalation stages, task classification, account rotation, and strategy config into engine"
```

---

## Self-Review

### Spec Coverage Checklist

| Spec requirement | Covered by |
|---|---|
| 3 routing policies (lite/pro/max) | `strategy.types.ts` presets + escalation stages |
| Task classifier | Task 3: `classifier.server.ts` |
| Capability filter | Task 4: filter checks modality caps |
| Quota & health filter | Task 4: strategy-aware `isQuotaExhausted` |
| Near-reserve / premium reserve | Task 4: `isPremiumReserved` |
| Account rotation | Task 6: `rotation.server.ts` |
| Multi-factor scoring formula | Task 5: `scorer.server.ts` |
| Tier-specific score weights | Task 1: `TIER_SCORING_WEIGHTS` in `strategy.types.ts` |
| Escalation stages per tier | Task 2: `getEscalationStages` in `policy.server.ts` |
| Premium preservation | Task 4 (reserve filter) + Task 5 (premiumPressure penalty) + Task 2 (no premium in lite stages) |
| Cost type derivation | Task 2: `getCostType` |
| Quality score | Task 2: `getQualityScore` |
| Account balance score | Task 5: `accountOveruseFraction` |
| Task fit score | Task 5: `taskFitScore` |
| Engine wiring | Task 7: full escalation loop |
| Trace includes taskClass + stage | Task 1 types + Task 7 engine |

### Placeholder Scan

None — all code blocks are complete and specific.

### Type Consistency

- `scoreCandidate(c, tier, taskClass, allCandidates)` — defined in Task 5, called in Task 7 ✓
- `filterCandidatesWithDiagnostics(candidates, modality, strategy)` — defined in Task 4, called in Task 7 ✓
- `enrichCandidate(c): RoutingCandidate` — defined in Task 2, called in Task 7 ✓
- `classifyTask(messages): TaskClass` — defined in Task 3, called in Task 7 ✓
- `applyAccountRotation(scored, strategy): ScoredCandidate[]` — defined in Task 6, called in Task 7 ✓
- `RoutingTrace.task_class` — added in Task 1 types, set in Task 7 ✓
- `RoutingTrace.escalation_stage` — added in Task 1 types, set in Task 7 ✓
