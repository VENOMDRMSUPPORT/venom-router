# Venom Router — Roadmap & Migration Plan

> Last updated: 2026-06-22
> Source of truth: migrating from `F:\projects\venom-router` (Next.js) → `F:\projects\venom-router-react` (TanStack Start) as the final production project.

---

## Project Goal

Venom Router is a **private single-owner AI gateway** that:

- Centralizes all AI provider accounts (Claude, Antigravity, OpenCode Zen, etc.)
- Exposes exactly 3 unified model names: `venom/lite`, `venom/pro`, `venom/max`
- Routes requests through a scoring engine (cost/speed/quality weights)
- Provides an OpenAI-compatible `/v1/chat/completions` endpoint for external projects
- Logs all routing decisions, usage, and quota

---

## Stack (Final — No Changes)

| Layer           | Technology                           |
| --------------- | ------------------------------------ |
| Framework       | TanStack Start (SSR React) + Vite    |
| Auth            | Supabase Auth                        |
| Database        | Supabase (PostgreSQL)                |
| API             | `createServerFn` from TanStack Start |
| UI              | shadcn/ui (Radix + Tailwind)         |
| Charts          | Recharts                             |
| Package Manager | Bun                                  |

---

## Pages Status

| Page              | Route              | Status           |
| ----------------- | ------------------ | ---------------- |
| Overview          | `/overview`        | ✅ Done          |
| OAuth Providers   | `/providers/oauth` | ✅ Done          |
| Free Providers    | `/providers/free`  | ✅ Done          |
| Models            | `/models`          | ✅ Done          |
| API Keys          | `/api-keys`        | ✅ Done          |
| Settings          | `/settings`        | 🔶 Skeleton only |
| Venom Models      | `/venom-models`    | ❌ Stub          |
| Routing Rules     | `/routing`         | ❌ Stub          |
| Playground        | `/playground`      | ❌ Stub          |
| Usage & Analytics | `/usage`           | ❌ Stub          |
| Quota & Limits    | `/quota`           | ❌ Stub          |
| Diagnostics       | `/diagnostics`     | ❌ Stub          |

---

## Scoring Algorithm (from original engine)

```
score = roleBonus×10 + costWeight×costScore + speedWeight×speedScore + qualityWeight×priorityScore

roleBonus    = 1 if role="primary" else 0
costScore    = 1 / (avgCost×1000 + 1)
               avgCost = (inputCost + outputCost×3) / 4
speedScore   = 1000 / avg_latency_ms  (or 0.5 if no data)
priorityScore = 1 / (priority + 1)
```

**Venom Model Default Weights:**

| Model | cost_weight | speed_weight | quality_weight |
| ----- | ----------- | ------------ | -------------- |
| lite  | 0.7         | 0.2          | 0.1            |
| pro   | 0.3         | 0.3          | 0.4            |
| max   | 0.1         | 0.1          | 0.8            |

---

## Routing Candidate Filter Rules

A candidate passes if ALL of:

- `lifecycle = 'approved'`
- `account.status = 'healthy'`
- `model.enabled = true`
- Quota: if confidence=high → remaining > 5%
- Condition (jsonb): null=always | requires_capability | min_context_tokens | quota_risk

---

## Security Rules (Non-Negotiable)

1. Provider credentials encrypted at rest with AES-256-GCM
2. `VENOM_ENCRYPTION_KEY` must be set — never fall back to hardcoded seed
3. Raw keys/tokens never appear in logs, API responses, traces, or UI
4. Venom API keys: raw shown once on creation, bcrypt hash stored only
5. Routing traces: store only rule IDs + decision reasons, NEVER provider names/URLs/tokens
6. Dashboard behind Supabase Auth (owner-only)

---

## Phase 0 — Security Fixes (URGENT — Before Anything Else)

**Estimated: 1 hour**

### 0.1 — Harden encryption key fallback

File: `src/lib/crypto.server.ts:9`

```ts
// REMOVE the "venom-dev" fallback. Replace with:
if (!raw) {
  throw new Error("VENOM_ENCRYPTION_KEY is required. Generate one with: openssl rand -hex 32");
}
```

### 0.2 — Fix OAuth CSRF state check

File: `src/lib/providers/integrations.functions.ts:152`

```ts
// Make state required in Zod:
state: z.string().min(1),

// Make check unconditional:
if (flow.state !== data.state) {
  throw new Error("OAuth state mismatch");
}
```

---

## Phase 1 — Bug Fixes (Day 1)

**Estimated: 3-4 hours**

| #   | File                                                        | Fix                                                                       |
| --- | ----------------------------------------------------------- | ------------------------------------------------------------------------- |
| 1.1 | `src/components/providers/antigravity-quota-details.tsx:29` | QuotaRing: `remainingFraction <= 0` → red (#ef4444), not green            |
| 1.2 | `src/routes/_authenticated/overview.tsx:202`                | 30d/90d buttons: wire to state+query or remove until built                |
| 1.3 | `src/lib/utils.ts`                                          | Extract `formatRelativeTime` to shared utils, import in overview + models |
| 1.4 | `src/lib/providers/integrations.functions.ts:1267`          | `testAccountModels`: replace for-loop with concurrent worker-pool         |
| 1.5 | `src/lib/providers/integrations.functions.ts:1296`          | `setModelsEnabled`: replace for-loop with `Promise.all`                   |
| 1.6 | `src/components/layout/sidebar.tsx:230`                     | Read user email from route context instead of extra `getUser()` call      |

---

## Phase 2 — Database Schema Additions (Day 2-3)

**Estimated: 3-4 hours**

### 2.1 — Add missing columns to existing tables

```sql
-- models table
ALTER TABLE models ADD COLUMN IF NOT EXISTS input_cost_per_mtok NUMERIC;
ALTER TABLE models ADD COLUMN IF NOT EXISTS output_cost_per_mtok NUMERIC;
ALTER TABLE models ADD COLUMN IF NOT EXISTS max_output_tokens INTEGER;
ALTER TABLE models ADD COLUMN IF NOT EXISTS blocked_reason TEXT;

-- routing_rules table
ALTER TABLE routing_rules ADD COLUMN IF NOT EXISTS condition JSONB;
ALTER TABLE routing_rules ADD COLUMN IF NOT EXISTS role TEXT DEFAULT 'primary'; -- primary|fallback
ALTER TABLE routing_rules ADD COLUMN IF NOT EXISTS max_fallback_attempts INTEGER DEFAULT 3;

-- venom_models table
ALTER TABLE venom_models ADD COLUMN IF NOT EXISTS cost_weight NUMERIC DEFAULT 0.3;
ALTER TABLE venom_models ADD COLUMN IF NOT EXISTS speed_weight NUMERIC DEFAULT 0.3;
ALTER TABLE venom_models ADD COLUMN IF NOT EXISTS quality_weight NUMERIC DEFAULT 0.4;
ALTER TABLE venom_models ADD COLUMN IF NOT EXISTS description TEXT;
```

### 2.2 — New tables

```sql
-- Account health check history
CREATE TABLE IF NOT EXISTS account_health_checks (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id UUID REFERENCES accounts(id) ON DELETE CASCADE,
  checked_at TIMESTAMPTZ DEFAULT NOW(),
  status TEXT, -- healthy|degraded|unreachable
  latency_ms INTEGER,
  error_code TEXT,
  error_message TEXT
);

-- Periodic quota readings
CREATE TABLE IF NOT EXISTS quota_snapshots (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id UUID REFERENCES accounts(id) ON DELETE CASCADE,
  snapped_at TIMESTAMPTZ DEFAULT NOW(),
  quota_type TEXT, -- tokens|requests|spend
  period TEXT, -- daily|monthly|rolling
  used NUMERIC,
  total NUMERIC,
  remaining NUMERIC,
  resets_at TIMESTAMPTZ,
  quota_source TEXT, -- provider_reported|locally_estimated|manual
  confidence TEXT    -- high|medium|low|unknown
);

-- Routing decision traces (NO secrets — rule IDs and reasons only)
CREATE TABLE IF NOT EXISTS routing_traces (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  usage_record_id UUID,
  candidates_evaluated INTEGER,
  candidates_filtered_out INTEGER,
  selected_rule_id UUID,
  decision_reason TEXT,
  fallback_attempts INTEGER DEFAULT 0,
  created_at TIMESTAMPTZ DEFAULT NOW()
);

-- System configuration (single row)
CREATE TABLE IF NOT EXISTS system_settings (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  system_name TEXT DEFAULT 'Venom Router',
  default_request_timeout_ms INTEGER DEFAULT 30000,
  default_max_fallback_attempts INTEGER DEFAULT 3,
  health_check_interval_minutes INTEGER DEFAULT 5,
  quota_warning_threshold_pct INTEGER DEFAULT 15,
  quota_critical_threshold_pct INTEGER DEFAULT 5,
  routing_trace_retention_days INTEGER DEFAULT 30,
  usage_record_retention_days INTEGER DEFAULT 90,
  updated_at TIMESTAMPTZ DEFAULT NOW()
);

-- Sensitive operation audit log
CREATE TABLE IF NOT EXISTS audit_log_entries (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  occurred_at TIMESTAMPTZ DEFAULT NOW(),
  actor TEXT,
  action TEXT,
  target TEXT,
  metadata JSONB,
  success BOOLEAN DEFAULT TRUE
);
```

### 2.3 — Seed venom_models defaults

```sql
INSERT INTO venom_models (slug, display_name, cost_weight, speed_weight, quality_weight)
VALUES
  ('lite', 'Venom Lite', 0.7, 0.2, 0.1),
  ('pro',  'Venom Pro',  0.3, 0.3, 0.4),
  ('max',  'Venom Max',  0.1, 0.1, 0.8)
ON CONFLICT (slug) DO UPDATE SET
  cost_weight = EXCLUDED.cost_weight,
  speed_weight = EXCLUDED.speed_weight,
  quality_weight = EXCLUDED.quality_weight;
```

---

## Phase 3 — Routing Engine (Week 1, Days 4-7)

**Estimated: 5-7 days**  
**Reference:** `F:\projects\venom-router\lib\routing\engine.ts`

### Files to create:

```
src/lib/routing/
  engine.server.ts       — main routeRequest() function
  scorer.server.ts       — scoreCandidate() function
  filter.server.ts       — filterCandidates() function
  executor.server.ts     — callProvider() + fallback loop
  trace.server.ts        — buildRoutingTrace() + persist
```

### Core flow:

```
routeRequest(venomSlug, messages, apiKeyId)
  │
  ├─ 1. Load venom_model + routing_rules from DB
  ├─ 2. Score each candidate (scoreCandidate)
  ├─ 3. Filter candidates (lifecycle, health, quota, conditions)
  ├─ 4. Sort by score descending
  ├─ 5. Execute via provider adapter (decrypt creds → call API)
  ├─ 6. On failure → next candidate (up to maxFallbackAttempts)
  ├─ 7. Record usage_record (tokens, cost, latency, fallback info)
  └─ 8. Record routing_trace (rule IDs only — NO secrets)
```

---

## Phase 4 — /v1/chat/completions API (Week 2, Days 1-3)

**Estimated: 4-5 days**  
**Reference:** `F:\projects\venom-router\app\api\v1\chat\completions\route.ts`

### Endpoint spec:

```
POST /v1/chat/completions
Authorization: Bearer vk_live_*

Request body:
{
  "model": "venom/lite" | "venom/pro" | "venom/max",
  "messages": [...],
  "max_tokens": number,          // optional
  "temperature": number          // optional
}

Response: OpenAI-compatible JSON
{
  "id": "venom-*",
  "object": "chat.completion",
  "model": "venom/lite",
  "choices": [{ "message": { "role": "assistant", "content": "..." } }],
  "usage": { "prompt_tokens": N, "completion_tokens": N, "total_tokens": N }
}
```

### API key validation:

1. Format check: starts with `vk_live_`
2. bcrypt compare with stored hash
3. Check `revoked_at` is null
4. Check RPM (requests per minute)
5. Check TPD (tokens per day)
6. Check monthly spend cap
7. Fire-and-forget: update `last_used_at`

---

## Phase 5 — Missing Pages (Week 2-3)

**Estimated: 8-10 days**

### 5.1 — Venom Models `/venom-models`

- Display lite/pro/max cards
- Sliders for cost/speed/quality weights (must sum to 1.0)
- Associated routing rules count per tier
- Quick link to add routing rule

### 5.2 — Routing Rules `/routing`

- Full table: priority, role, venom tier, provider model, account, condition
- Add/edit/delete rules
- Drag-and-drop priority reordering
- Fallback chain builder (primary + up to 3 fallbacks)
- Condition builder:
  - `null` → always eligible
  - `{ "requires": ["vision"] }` → needs capability
  - `{ "min_context_tokens": 100000 }` → long-context only
  - `{ "quota_risk": "low" }` → healthy quota only

### 5.3 — Playground `/playground`

- Select venom model (lite/pro/max)
- Message input
- Send via routing engine
- Live routing trace panel:
  - Candidates evaluated (list with scores)
  - Candidates filtered (with filter reason)
  - Selected rule + decision reason
  - Fallback attempts (if any)
  - Final response

### 5.4 — Usage & Analytics `/usage`

- Query `usage_records` table
- Charts (Recharts):
  - Requests over time (7d / 30d / 90d — actually wired)
  - Token usage (input vs output)
  - Cost estimates
  - Latency distribution
- Breakdowns by: venom tier / provider / model
- Total spend estimate

### 5.5 — Quota & Limits `/quota`

- Read from `quota_snapshots` + live `accounts.quota_*`
- Per-account quota with confidence indicator:
  - `provider_reported` → show boldly
  - `locally_estimated` → show with `≈` prefix + note
  - `manual` → show with edit icon
- Reset time forecasts
- Warning indicators: orange at 15%, red at 5%

### 5.6 — Diagnostics `/diagnostics`

- Blocked models list (with blocked_reason)
- Degraded/unreachable accounts (with error from health checks)
- Recent routing failures (from usage_records where status=failed)
- Accounts below quota threshold
- Health check history per account

### 5.7 — Settings `/settings`

- **General:** system name, timezone
- **Routing:** request timeout, max fallback attempts, trace retention days
- **Health:** check interval minutes, quota warning %, quota critical %
- **Audit Log:** viewer (last N entries), retention days
- **Danger Zone:** encryption key rotation note

---

## Phase 6 — Background Workers (Week 3-4)

**Estimated: 3 days**  
**Reference:** `F:\projects\venom-router\lib\workers\`

### Option A: Supabase Edge Functions + pg_cron (Recommended)

```sql
-- Run every 5 minutes via pg_cron or Supabase cron
SELECT cron.schedule('health-check', '*/5 * * * *', 'SELECT run_health_check()');
SELECT cron.schedule('quota-snapshot', '*/5 * * * *', 'SELECT run_quota_snapshot()');
```

### Option B: External cron (GitHub Actions / Vercel Cron)

```yaml
# .github/workflows/workers.yml
on:
  schedule:
    - cron: "*/5 * * * *"
```

### 6.1 — Health Check Worker

```ts
// For each active account:
// 1. Decrypt credentials
// 2. Call adapter.healthCheck()
// 3. Insert into account_health_checks
// 4. Update accounts.status (healthy|degraded|unreachable)
```

### 6.2 — Quota Snapshot Worker

```ts
// For each active account:
// 1. Call provider quota API (or estimate from usage_records)
// 2. Determine quota_source + confidence
// 3. Insert into quota_snapshots
// 4. Update accounts.quota_* fields
```

---

## Timeline Summary

```
Week 1
  Day 1     Phase 0 (security) + Phase 1 (bug fixes)
  Day 2-3   Phase 2 (database schema)
  Day 4-7   Phase 3 (routing engine — core logic)

Week 2
  Day 1-2   Phase 3 continued (fallback + traces)
  Day 3-5   Phase 4 (/v1 API endpoint)

Week 3
  Day 1-5   Phase 5 (missing pages: venom-models, routing, playground)

Week 4
  Day 1-3   Phase 5 continued (usage, quota, diagnostics, settings)
  Day 4-5   Phase 6 (background workers)

Week 5
  Day 1-3   End-to-end testing
  Day 4-5   Production deployment + final polish
```

---

## Decisions Made

| Question                      | Decision                            | Reason                                      |
| ----------------------------- | ----------------------------------- | ------------------------------------------- |
| Auth: Supabase vs Better Auth | **Supabase Auth**                   | Code already exists, no need to rewrite     |
| DB: Supabase vs Prisma        | **Supabase**                        | Adapters and server functions already wired |
| /v1 API location              | **TanStack Start server route**     | Needs routing engine in same runtime        |
| Background workers            | **Supabase Cron or GitHub Actions** | Simplest deployment, no extra infra         |

---

## Open Questions (Need Decision)

- [ ] Should `/v1/chat/completions` support streaming in v1, or keep it sync-only like the original?
- [ ] Which cron mechanism for workers: Supabase pg_cron, GitHub Actions, or Vercel Cron?
- [ ] Routing trace retention: 30 days default — OK?
- [ ] Should the Playground show real token costs or just the routing trace?
