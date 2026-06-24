# Venom Router — Production Design Spec

**Date:** 2026-06-22  
**Status:** Approved  
**Project:** `F:\projects\venom-router-react` (TanStack Start — final production target)  
**Reference:** `F:\projects\venom-router` (original Next.js — source of truth for business logic)

---

## 1. Project Vision

Venom Router is a **private single-owner AI gateway** that:

- Centralizes multiple AI provider accounts (Antigravity, Claude Code, OpenCode Zen, and future providers)
- Exposes exactly **3 unified model tiers**: `venom/lite`, `venom/pro`, `venom/max`
- All three tiers are **fully multimodal** (text, images, audio, documents) — differentiated by quality and speed only
- Routes every request through a **scoring engine** (cost × speed × quality weights)
- Provides **two external API endpoints**:
  - `POST /v1/chat/completions` — OpenAI compatible
  - `POST /v1/messages` — Anthropic compatible
- Exposes developer-facing **Docs** and **API Console** pages (owner-only)

---

## 2. Stack (Final — No Changes)

| Layer              | Technology                                   |
| ------------------ | -------------------------------------------- |
| Framework          | TanStack Start (SSR React) + Vite            |
| Auth               | Supabase Auth (owner-only)                   |
| Database           | Supabase (PostgreSQL)                        |
| Server Layer       | `createServerFn` from TanStack Start         |
| UI                 | shadcn/ui (Radix + Tailwind)                 |
| Charts             | Recharts                                     |
| Package Manager    | Bun                                          |
| Background Workers | Supabase pg_cron or GitHub Actions scheduled |

---

## 3. Architecture Decisions

| Decision           | Choice                          | Reason                                          |
| ------------------ | ------------------------------- | ----------------------------------------------- |
| Auth               | Supabase Auth                   | Already implemented, no migration needed        |
| Database           | Supabase (PostgreSQL)           | All adapters and server functions already wired |
| `/v1` API          | TanStack Start server route     | Shares runtime with routing engine              |
| Background workers | Supabase Cron or GitHub Actions | Simple, no extra infrastructure                 |
| API compatibility  | Two separate endpoints          | Clearest UX, matches industry (Z.AI pattern)    |

---

## 4. Venom Model Tiers

### Definitions

| Tier         | Optimized for | Default Weights                  |
| ------------ | ------------- | -------------------------------- |
| `venom/lite` | Cost          | cost=0.7, speed=0.2, quality=0.1 |
| `venom/pro`  | Balance       | cost=0.3, speed=0.3, quality=0.4 |
| `venom/max`  | Quality       | cost=0.1, speed=0.1, quality=0.8 |

### Multimodal Support

All three tiers accept any modality. The routing engine selects providers that support the requested modality:

| Modality  | Trigger                                      | Required capability |
| --------- | -------------------------------------------- | ------------------- |
| Text only | messages with text only                      | any                 |
| Vision    | messages contain `image_url` or base64 image | `vision`            |
| Audio     | messages contain audio content               | `audio`             |
| Documents | messages contain file/PDF content            | `documents`         |

The **scoring algorithm runs after modality filtering** — no other changes to the engine.

---

## 5. Routing Engine

### Scoring Algorithm

```
score = roleBonus×10 + cost_weight×costScore + speed_weight×speedScore + quality_weight×priorityScore

roleBonus     = 1 if role="primary" else 0
costScore     = 1 / (avgCost×1000 + 1)
              avgCost = (input_cost_per_mtok + output_cost_per_mtok×3) / 4
speedScore    = 1000 / avg_latency_ms  (default 0.5 if no data)
priorityScore = 1 / (priority + 1)
```

### Candidate Filter Rules (all must pass)

1. `lifecycle = 'approved'`
2. `account.status = 'healthy'`
3. `model.enabled = true`
4. Modality: model capabilities include required modality
5. Quota: if confidence=`high` → remaining > 5%
6. Condition (jsonb):
   - `null` → always eligible
   - `{ "requires": ["vision"] }` → only if request needs that capability
   - `{ "min_context_tokens": N }` → only for long-context requests
   - `{ "quota_risk": "low" }` → only when quota is healthy

### Execution Flow

```
routeRequest(venomSlug, request, apiKeyId)
  1. Load venom_model + routing_rules from DB
  2. Detect request modality from content
  3. Score all candidates (scoreCandidate)
  4. Filter candidates (filterCandidates)
  5. Sort by score descending
  6. Execute: decrypt creds → call provider adapter
  7. On failure: next candidate (up to max_fallback_attempts)
  8. Write usage_record (tokens, cost, latency, fallback info)
  9. Write routing_trace (rule IDs + reasons ONLY — no secrets)
```

### File Structure

```
src/lib/routing/
  engine.server.ts      — routeRequest() — main entry point
  scorer.server.ts      — scoreCandidate()
  filter.server.ts      — filterCandidates() + detectModality()
  executor.server.ts    — callProvider() + fallback loop
  trace.server.ts       — buildRoutingTrace() + persist
  parsers/
    openai.server.ts    — parseOpenAIRequest() + formatOpenAIResponse()
    anthropic.server.ts — parseAnthropicRequest() + formatAnthropicResponse()
```

---

## 6. External API Endpoints

### Shared Behavior (both endpoints)

**Authentication:** `Authorization: Bearer vk_live_*`

**Key validation (in order):**

1. Format: must start with `vk_live_`
2. bcrypt compare with stored hash
3. `revoked_at` is null
4. RPM check (requests per minute)
5. TPD check (tokens per day)
6. Monthly spend cap check
7. Fire-and-forget: update `last_used_at`

**Response headers (always):**

```
x-venom-model: venom/lite
x-venom-provider: antigravity
x-venom-latency-ms: 342
x-venom-fallback: false
x-venom-fallback-count: 0
```

**Error format (OpenAI-style for both):**

```json
{
  "error": {
    "message": "Rate limit exceeded",
    "type": "rate_limit_error",
    "code": "rate_limit_exceeded"
  }
}
```

---

### 6.1 — POST /v1/chat/completions (OpenAI Compatible)

**Request:**

```json
{
  "model": "venom/lite" | "venom/pro" | "venom/max",
  "messages": [
    { "role": "user", "content": "Hello" },
    { "role": "user", "content": [
      { "type": "text", "text": "What's in this image?" },
      { "type": "image_url", "image_url": { "url": "data:image/..." } }
    ]}
  ],
  "max_tokens": 1024,
  "temperature": 0.7,
  "stream": false
}
```

**Response:**

```json
{
  "id": "venom-ch-abc123",
  "object": "chat.completion",
  "created": 1749600000,
  "model": "venom/lite",
  "choices": [
    {
      "index": 0,
      "message": { "role": "assistant", "content": "..." },
      "finish_reason": "stop"
    }
  ],
  "usage": {
    "prompt_tokens": 10,
    "completion_tokens": 50,
    "total_tokens": 60
  }
}
```

---

### 6.2 — POST /v1/messages (Anthropic Compatible)

**Request:**

```json
{
  "model": "venom/lite" | "venom/pro" | "venom/max",
  "max_tokens": 1024,
  "messages": [
    {
      "role": "user",
      "content": [
        { "type": "text", "text": "What's in this image?" },
        { "type": "image", "source": {
          "type": "base64",
          "media_type": "image/jpeg",
          "data": "..."
        }}
      ]
    }
  ],
  "system": "You are a helpful assistant."
}
```

**Response:**

```json
{
  "id": "venom-msg-abc123",
  "type": "message",
  "role": "assistant",
  "model": "venom/max",
  "content": [{ "type": "text", "text": "..." }],
  "stop_reason": "end_turn",
  "usage": {
    "input_tokens": 10,
    "output_tokens": 50
  }
}
```

---

## 7. Database Schema Additions

### 7.1 — Columns added to existing tables

```sql
-- models
ALTER TABLE models ADD COLUMN IF NOT EXISTS input_cost_per_mtok  NUMERIC;
ALTER TABLE models ADD COLUMN IF NOT EXISTS output_cost_per_mtok NUMERIC;
ALTER TABLE models ADD COLUMN IF NOT EXISTS max_output_tokens    INTEGER;
ALTER TABLE models ADD COLUMN IF NOT EXISTS blocked_reason       TEXT;

-- routing_rules
ALTER TABLE routing_rules ADD COLUMN IF NOT EXISTS condition             JSONB;
ALTER TABLE routing_rules ADD COLUMN IF NOT EXISTS role                  TEXT DEFAULT 'primary';
ALTER TABLE routing_rules ADD COLUMN IF NOT EXISTS max_fallback_attempts INTEGER DEFAULT 3;

-- venom_models
ALTER TABLE venom_models ADD COLUMN IF NOT EXISTS cost_weight    NUMERIC DEFAULT 0.3;
ALTER TABLE venom_models ADD COLUMN IF NOT EXISTS speed_weight   NUMERIC DEFAULT 0.3;
ALTER TABLE venom_models ADD COLUMN IF NOT EXISTS quality_weight NUMERIC DEFAULT 0.4;
ALTER TABLE venom_models ADD COLUMN IF NOT EXISTS description    TEXT;
```

### 7.2 — New tables

```sql
CREATE TABLE IF NOT EXISTS account_health_checks (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id  UUID REFERENCES accounts(id) ON DELETE CASCADE,
  checked_at  TIMESTAMPTZ DEFAULT NOW(),
  status      TEXT,        -- healthy | degraded | unreachable
  latency_ms  INTEGER,
  error_code  TEXT,
  error_message TEXT
);

CREATE TABLE IF NOT EXISTS quota_snapshots (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id   UUID REFERENCES accounts(id) ON DELETE CASCADE,
  snapped_at   TIMESTAMPTZ DEFAULT NOW(),
  quota_type   TEXT,        -- tokens | requests | spend
  period       TEXT,        -- daily | monthly | rolling
  used         NUMERIC,
  total        NUMERIC,
  remaining    NUMERIC,
  resets_at    TIMESTAMPTZ,
  quota_source TEXT,        -- provider_reported | locally_estimated | manual
  confidence   TEXT         -- high | medium | low | unknown
);

CREATE TABLE IF NOT EXISTS routing_traces (
  id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  usage_record_id       UUID,
  candidates_evaluated  INTEGER,
  candidates_filtered   INTEGER,
  selected_rule_id      UUID,
  decision_reason       TEXT,
  fallback_attempts     INTEGER DEFAULT 0,
  modality              TEXT,   -- text | vision | audio | documents
  created_at            TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS system_settings (
  id                             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  system_name                    TEXT    DEFAULT 'Venom Router',
  default_request_timeout_ms     INTEGER DEFAULT 30000,
  default_max_fallback_attempts  INTEGER DEFAULT 3,
  health_check_interval_minutes  INTEGER DEFAULT 5,
  quota_warning_threshold_pct    INTEGER DEFAULT 15,
  quota_critical_threshold_pct   INTEGER DEFAULT 5,
  routing_trace_retention_days   INTEGER DEFAULT 30,
  usage_record_retention_days    INTEGER DEFAULT 90,
  updated_at                     TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS audit_log_entries (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  occurred_at TIMESTAMPTZ DEFAULT NOW(),
  actor       TEXT,
  action      TEXT,
  target      TEXT,
  metadata    JSONB,
  success     BOOLEAN DEFAULT TRUE
);
```

### 7.3 — Seed venom_models

```sql
INSERT INTO venom_models (slug, display_name, cost_weight, speed_weight, quality_weight, description)
VALUES
  ('lite', 'Venom Lite', 0.7, 0.2, 0.1, 'Optimized for cost — fastest, cheapest routing'),
  ('pro',  'Venom Pro',  0.3, 0.3, 0.4, 'Balanced — quality-leaning for production use'),
  ('max',  'Venom Max',  0.1, 0.1, 0.8, 'Optimized for quality — best model available')
ON CONFLICT (slug) DO UPDATE SET
  cost_weight    = EXCLUDED.cost_weight,
  speed_weight   = EXCLUDED.speed_weight,
  quality_weight = EXCLUDED.quality_weight,
  description    = EXCLUDED.description;
```

---

## 8. Dashboard Pages

### 8.1 — Completed Pages (no changes needed)

| Page            | Route              |
| --------------- | ------------------ |
| Overview        | `/overview`        |
| OAuth Providers | `/providers/oauth` |
| Free Providers  | `/providers/free`  |
| Models          | `/models`          |
| API Keys        | `/api-keys`        |

### 8.2 — Pages to Build

**Venom Models** `/venom-models`

- Cards for lite/pro/max with current weights
- Weight sliders (cost + speed + quality, locked to sum=1.0)
- Per-tier routing rule count + quick add button
- Supported modalities display

**Routing Rules** `/routing`

- Table: priority, role, venom tier, provider model, account, condition
- Add/edit/delete rules
- Fallback chain builder (primary + up to 3 fallbacks)
- Condition builder UI:
  - Always eligible (null)
  - Requires capability (vision, audio, etc.)
  - Min context tokens
  - Quota risk level

**Playground** `/playground`  
_(enhance existing stub into full API Console — route stays as-is)_

- Left panel: endpoint selector (OpenAI/Anthropic), model selector, message builder, params
- Right top: full request (headers + body)
- Right bottom: full response + latency
- Copy as: cURL | Python | JavaScript | OpenAI SDK | Anthropic SDK
- Routing Trace panel (collapsible): candidates, selected rule, fallback info
- Request history (last 20 calls)

**Usage & Analytics** `/usage`

- Charts (7d/30d/90d — actually wired): requests, tokens, estimated cost
- Breakdown by: venom tier / provider / modality
- Latency distribution

**Quota & Limits** `/quota`

- Per-account quota with confidence indicator
- `provider_reported` → show boldly
- `locally_estimated` → show with `≈` prefix
- `manual` → show with edit icon
- Reset time forecasts
- Warning at 15% / critical at 5%

**Diagnostics** `/diagnostics`

- Blocked models (with reason)
- Degraded accounts (with error from last health check)
- Recent routing failures
- Quota alerts (accounts < threshold)

**Settings** `/settings` _(extend existing skeleton)_

- General: system name
- Routing: request timeout, max fallback attempts, trace retention
- Health: check interval, quota warning/critical %
- Audit log viewer (last 50 entries)
- Danger Zone: encryption key note

---

## 9. Docs Page (`/docs`)

Inspired by Z.AI docs. Owner-only. Static content rendered from MDX or inline components.

### Navigation Structure

```
/docs
  ├── Quick Start
  │   Step 1: Get your API key (link to /api-keys)
  │   Step 2: Choose your model (lite / pro / max)
  │   Step 3: Make your first call
  │   Code tabs: cURL | Python | JavaScript | OpenAI SDK | Anthropic SDK
  │
  ├── Models
  │   ├── venom/lite   — use cases, speed, capabilities, default routing
  │   ├── venom/pro    — use cases, balance, capabilities, default routing
  │   └── venom/max    — use cases, quality, capabilities, default routing
  │
  ├── API Reference
  │   ├── Authentication        — vk_live_* format, headers
  │   ├── POST /v1/chat/completions  — full request/response schema
  │   ├── POST /v1/messages          — full request/response schema
  │   ├── Multimodal            — images, audio, documents
  │   └── Error codes           — full list with descriptions
  │
  ├── Guides
  │   ├── Choosing a model tier
  │   ├── Working with images
  │   ├── Rate limits & quotas
  │   └── Migrating from OpenAI / Anthropic
  │
  └── Changelog
```

### Layout

- **Left sidebar**: navigation tree (sticky, collapsible sections)
- **Center**: content with code blocks
- **Right**: code panel (sticky, shows code examples for current section)
- **Dark mode** default, matches dashboard theme

---

## 10. Background Workers

**Two recurring workers, every 5 minutes:**

### Health Check Worker

```
For each active account:
  1. Decrypt credentials
  2. Call adapter.healthCheck() — lightweight ping
  3. Record in account_health_checks (status, latency, error)
  4. Update accounts.status (healthy | degraded | unreachable)
```

### Quota Snapshot Worker

```
For each active account:
  1. Call provider quota API (or estimate from usage_records)
  2. Determine quota_source + confidence level
  3. Insert into quota_snapshots
  4. Update accounts.quota_* fields
```

**Deployment:** Supabase pg_cron (preferred) or GitHub Actions scheduled workflow.

---

## 11. Security Rules (Non-Negotiable)

1. `VENOM_ENCRYPTION_KEY` must be set — **no fallback, throw on missing**
2. Provider credentials encrypted at rest with AES-256-GCM
3. Raw credentials never appear in: logs, API responses, routing traces, UI (after initial save)
4. Venom API keys (`vk_live_*`): raw shown once on creation, bcrypt hash stored only
5. Routing traces: store only rule IDs + decision reasons — **never** provider names, URLs, or tokens
6. Dashboard + Docs + Console: behind Supabase Auth (owner-only)
7. OAuth state parameter: **required** (not optional) — CSRF check always runs
8. `/v1/*` endpoints: only accessible with valid `vk_live_*` key

---

## 12. Implementation Phases

| Phase | Scope                                                                        | Est. Time    |
| ----- | ---------------------------------------------------------------------------- | ------------ |
| 0     | Security fixes (encryption key + OAuth CSRF)                                 | 1 day        |
| 1     | Bug fixes (quota color, buttons, dedup, concurrency)                         | 1 day        |
| 2     | Database schema additions                                                    | 2 days       |
| 3     | Routing engine + multimodal filtering                                        | 7 days       |
| 4     | `/v1/chat/completions` + `/v1/messages`                                      | 5 days       |
| 5     | Dashboard pages (venom-models, routing, usage, quota, diagnostics, settings) | 8 days       |
| 6     | Background workers (health check + quota snapshot)                           | 3 days       |
| 7     | API Console (`/console`) + Docs (`/docs`)                                    | 7 days       |
|       | **Total**                                                                    | **~34 days** |

---

## 13. Production Readiness Checklist

After all phases complete, the project is production-ready when:

- [ ] `VENOM_ENCRYPTION_KEY` set to 64-char hex (never committed to git)
- [ ] Supabase RLS policies enabled on all tables
- [ ] At least one provider account connected and synced
- [ ] Venom models seeded (lite/pro/max) with routing rules
- [ ] Background workers running (health + quota every 5 min)
- [ ] `/v1/chat/completions` returns valid response for all 3 tiers
- [ ] `/v1/messages` returns valid response for all 3 tiers
- [ ] At least one `vk_live_*` API key issued and tested
- [ ] Playground/Console shows correct routing trace
- [ ] All stub pages implemented (routing, usage, quota, diagnostics)

---

## 14. Open Questions (Resolved)

| Question          | Answer                                                                   |
| ----------------- | ------------------------------------------------------------------------ |
| Auth system       | Supabase Auth                                                            |
| Database          | Supabase                                                                 |
| API location      | TanStack Start server route                                              |
| Workers location  | Supabase pg_cron or GitHub Actions                                       |
| API compatibility | Two separate endpoints (OpenAI + Anthropic)                              |
| Multimodal scope  | All modalities (text, images, audio, documents) via capability filtering |
| Docs audience     | Owner only (not public)                                                  |
| Console type      | Advanced playground with full request/response + copy-as-code            |
| Streaming         | Not in v1 — future roadmap                                               |
