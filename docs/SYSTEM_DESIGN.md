# Venom Router — System Design & Provider/Model Lifecycle

> **Purpose:** One-stop reference for any agent or developer who needs to quickly understand how providers, accounts, models, sync, testing, and deletion work in this codebase. Includes diagrams, current implementation status, and known gaps.

---

## Table of Contents

1. [Architecture Overview](#1-architecture-overview)
2. [Providers & Accounts](#2-providers--accounts)
3. [Fetch Models Scenario](#3-fetch-models-scenario)
4. [Model Display Rules](#4-model-display-rules)
5. [Model Test Scenario](#5-model-test-scenario)
6. [Account Deletion Scenario](#6-account-deletion-scenario)
7. [Current Implementation vs. Desired Behaviour (Gap Analysis)](#7-current-implementation-vs-desired-behaviour-gap-analysis)
8. [Key Files Map](#8-key-files-map)
9. [Database Schema (key tables)](#9-database-schema-key-tables)

---

## 1. Architecture Overview

```
┌─────────────────────────────────────────────────────────────────┐
│                        Venom Router                             │
│                                                                 │
│  ┌──────────────┐   TanStack Start + Vite   ┌───────────────┐  │
│  │  React UI    │ ◄──────── SSR ──────────► │ Server Fns    │  │
│  │  (client)    │                           │ (createServerFn│  │
│  └──────────────┘                           └──────┬────────┘  │
│                                                    │           │
│                           ┌────────────────────────▼──────┐   │
│                           │         Supabase (Postgres)   │   │
│                           │  providers / accounts / models │   │
│                           │  venom_models / routing_rules  │   │
│                           └───────────────────────────────┘   │
│                                                                 │
│  ┌──────────────────┐          ┌───────────────────────────┐   │
│  │  Provider Adapters│          │  Routing Engine           │   │
│  │  claude-code      │          │  venom/lite|pro|max       │   │
│  │  antigravity      │          │  → routing_rules → model  │   │
│  │  opencode-zen     │          └───────────────────────────┘   │
│  └──────────────────┘                                           │
└─────────────────────────────────────────────────────────────────┘
```

**Auth flow:** Every server function requires `requireSupabaseAuth` middleware. The browser client passes a Bearer token; the middleware validates it and injects `{ supabase, userId, claims }`.

**Credential encryption:** Provider OAuth tokens and API keys are encrypted with AES-256-GCM (`CREDENTIALS_SECRET` env var) before storage in `accounts.credentials_enc/iv/tag`.

---

## 2. Providers & Accounts

Two active OAuth providers:

| Provider                               | Slug          | Auth                 | Base URL                              |
| -------------------------------------- | ------------- | -------------------- | ------------------------------------- |
| Claude Code                            | `claude-code` | OAuth2 PKCE          | `https://api.anthropic.com`           |
| Antigravity (Google Cloud Code Assist) | `antigravity` | OAuth2 PKCE + Google | `https://cloudcode-pa.googleapis.com` |

A single user can connect **multiple accounts** per provider. Each account row in `accounts` table holds encrypted credentials and links to one provider via `provider_id`.

```
providers (1) ──────── (N) accounts (1) ──────── (N) models
```

---

## 3. Fetch Models Scenario

### Desired Behaviour

```
┌─────────────────────────────────────────────────────────────────┐
│  FETCH MODELS — Desired Flow                                    │
│                                                                 │
│  1. Call provider API directly (NO hardcoded list, NO fallback) │
│     → get fresh model list                                      │
│                                                                 │
│  2. Compare with DB (models table, same account)               │
│                                                                 │
│     For each model from provider:                               │
│       ┌─ Already in DB? ──YES──► Skip (no write, no re-test)   │
│       └─ NOT in DB?    ──────►  Test model (real inference)     │
│                                  │                              │
│                              PASS──► Insert to DB, enabled=true │
│                              FAIL──► Skip / log error           │
│                                                                 │
│  3. For each model in DB that is NOT in provider list:          │
│       → Apply shared-model deletion logic (see §6)              │
│         (only delete if no other account still owns it)         │
│                                                                 │
│  4. Return diff report: added[], removed[], unchanged[]         │
└─────────────────────────────────────────────────────────────────┘
```

### Current Implementation — Claude Code

```
src/lib/providers/adapters/claude-code.server.ts
  └── listModels() ──► returns CLAUDE_CURATED_MODELS (hardcoded array in oauth-clients.server.ts)
```

> ⚠️ **GAP:** Claude Code models are **hardcoded** in `CLAUDE_CURATED_MODELS` (7 static entries). The adapter does NOT call the Anthropic API to discover models dynamically. This is intentional for now but deviates from the desired fully-dynamic design.

### Current Implementation — Antigravity

```
fetchModels server fn (integrations.functions.ts)
  └── runAntigravityLiveSnapshotFetch()
        ├── adapter.fetchAntigravityLiveRaw(creds)
        │     ├── refreshIfNeeded()          ← refresh OAuth token if needed
        │     ├── loadCodeAssist()           ← get project ID + plan info
        │     └── fetchAvailableModelsRaw()  ← call /v1internal:fetchAvailableModels
        │
        ├── persistAntigravityQuotaToAccount()  ← update quota snapshot
        │
        ├── upsertAntigravityIdeVisibleModelsSupabase()
        │     ├── dedupeAccountModelRowsSupabase()   ← clean up any duplicates
        │     ├── for each IDE-visible model:
        │     │     ├─ exists in DB? → update if changed / skip if same
        │     │     └─ not in DB?   → INSERT (lifecycle: discovered, enabled: true)
        │     └── markStaleModelsSupabase()  ← models no longer in provider list
        │           → sets: enabled=false, lifecycle=blocked, capabilities.stale=true
        │
        └── testAntigravityModelsConcurrent()  ← test all visible models (3 workers)
              └── adapter.testModel() per model
                    → updates: test_status, lifecycle, enabled, latency_ms
```

> ✅ Antigravity fetch is **fully dynamic** — calls the live API, compares with DB, marks removed models as stale.

> ⚠️ **GAP (stale models):** Removed Antigravity models are **marked stale** (capabilities.stale=true, enabled=false) but are NOT hard-deleted from DB. The desired scenario calls for proper deletion using shared-model logic (§6).

### Fetch Models Flow Diagram (Antigravity)

```
Provider API (/fetchAvailableModels)
       │
       ▼
  rawResponse
       │
       ├── extractRecommendedModelIds()  ← reads agentModelSorts.Recommended
       │
       ▼
  IDE-visible model list  (subset of raw catalog)
       │
       ├──────────────────────────────────────────────────────────┐
       │                                                          │
       ▼                                                          ▼
  For each model                                         Models in DB
  from provider:                                         NOT in provider list:
       │                                                          │
       ├─ In DB already? ──YES──► compare fields                  ▼
       │                          ├─ same? → unchanged        markStaleModels()
       │                          └─ diff? → UPDATE            (enabled=false,
       │                                                        stale=true)
       └─ Not in DB? ──────────► INSERT
                                 (lifecycle=discovered)
                                 then → testModel()
```

---

## 4. Model Display Rules

Models are displayed by reading directly from the DB. **Never** from hardcoded lists.

### `listAccountModels` (per-account view)

```
Server fn: listAccountModels
  SELECT from models WHERE account_id = ?
  Returns: id, external_id, display_name, capabilities, test_status, enabled, latency_ms
```

Displayed fields used in UI:

- Provider name (via `providers.name`)
- Account email / label (via `accounts.email`, `accounts.label`)
- Model display name (`models.display_name`)
- Capabilities (`models.capabilities.list`)
- Test status, latency

### `listCatalogModels` (cross-account catalog view)

```
Server fn: listCatalogModels
  SELECT from models JOIN accounts JOIN providers
  → aggregateCatalogModels()
     Groups by providerSlug:externalId
     For each group (same model across multiple accounts):
       - picks "best" row (working > failed > untested, approved > discovered)
       - merges account list
       - picks best latency_ms (min of working tests)
     Returns CatalogModel[]
```

This means if model `gemini-pro` is available via **Account 1** AND **Account 2**, the catalog shows it once with both accounts listed and the best latency.

---

## 5. Model Test Scenario

### Desired Behaviour

A real inference call — not just a status-200 check. Must get an actual response from the model (e.g., ask it to reply "ok").

### Current Implementation

Both adapters send a **real inference request** with a short prompt and `max_tokens: 8`:

#### Claude Code (`testModel`)

```
POST https://api.anthropic.com/v1/messages
Body: { model: external_id, max_tokens: 8, messages: [{ role: "user", content: "ping" }] }
Result: ok = r.ok (HTTP 2xx)
```

#### Antigravity (`testModel`)

```
POST /v1internal:streamGenerateContent?alt=sse
Body: { project, model, request: { contents: [{ role:"user", parts:[{text:"ping"}] }], maxOutputTokens: 8 } }
Result: ok = r.ok (HTTP 2xx), reads body to drain SSE stream
```

> ⚠️ **GAP:** Tests check `r.ok` (HTTP status 2xx) but do **NOT** validate the actual content of the response. The desired scenario requires verifying the model actually returned text (e.g., responded with "ok"). Current tests would pass even if the model returned an empty body.

### Test → DB Update

After testing, the DB is updated:

```
models table:
  test_status   → "working" | "failed"
  lifecycle     → "approved" | "blocked"
  enabled       → true (if working AND quota not exhausted) | false
  latency_ms    → measured round-trip ms
  last_tested_at → timestamp
  last_test_error → null | error string
```

---

## 6. Account Deletion Scenario

### Desired Behaviour

```
EXAMPLE: 2 accounts for provider "antigravity"

Account 1 models: [A, B]
Account 2 models: [A, B, C]

Delete Account 1:
  → A still exists (owned by Account 2) ✓ keep
  → B still exists (owned by Account 2) ✓ keep
  → C not affected                       ✓ keep
  Result: models [A, B, C] remain

Delete Account 2:
  → A still exists (owned by Account 1) ✓ keep
  → B still exists (owned by Account 1) ✓ keep
  → C ONLY in Account 2 being deleted  → DELETE model C
  Result: models [A, B] remain
```

The rule: **only delete a model row when no other account (for same provider) still has that model.**

### Current Implementation

```typescript
// integrations.functions.ts — disconnectAccount
const { error } = await supabase.from("accounts").delete().eq("id", data.account_id);
```

> ⚠️ **GAP — Critical:** The current implementation **simply deletes the account row**. Model cleanup depends entirely on Supabase FK cascade rules. There is **no shared-model logic** — it does not check whether other accounts still own the same models before deleting.

If the DB has `ON DELETE CASCADE` on `models.account_id → accounts.id`, all models for that account are deleted regardless of whether another account has the same model. If there is NO cascade, orphaned model rows remain.

### Desired Deletion Flow (not yet implemented)

```
disconnectAccount(account_id):
  1. Get provider_id for this account
  2. Get all models for this account: myModels[]
  3. Get all OTHER accounts for same provider: siblingAccountIds[]
  4. Get all models for sibling accounts: siblingModels[]
  5. Build siblingModelSet = Set of external_ids from step 4

  6. For each model in myModels:
     ├─ external_id IN siblingModelSet? → keep (don't delete)
     └─ external_id NOT in siblingModelSet? → DELETE model row

  7. Delete account row
```

---

## 7. Current Implementation vs. Desired Behaviour (Gap Analysis)

| Scenario                               | Desired                                       | Current Status                                                                                          |
| -------------------------------------- | --------------------------------------------- | ------------------------------------------------------------------------------------------------------- |
| **Claude Code model list**             | Dynamic from Anthropic API                    | ❌ Hardcoded (`CLAUDE_CURATED_MODELS`, 7 entries)                                                       |
| **Antigravity model list**             | Dynamic from provider API                     | ✅ Calls `/v1internal:fetchAvailableModels` live                                                        |
| **No hardcoded fallback**              | Zero hardcoded models                         | ❌ Claude Code uses hardcoded list                                                                      |
| **New model → test before insert**     | Test first, then insert                       | ❌ Antigravity inserts first (lifecycle=discovered), tests after. Claude Code never tests during fetch. |
| **Existing model → skip**              | Skip write + test                             | ✅ `unchangedCount` logic in upsertAntigravityIdeVisibleModelsSupabase                                  |
| **Removed model → smart delete**       | Delete only if unshared                       | ❌ Antigravity marks `stale=true` (soft delete). Account deletion has no shared-model logic.            |
| **Model test = real inference**        | Verify actual content                         | ⚠️ Real call is made but only HTTP 2xx is checked, content not verified                                 |
| **Display from DB only**               | DB as source of truth                         | ✅ Both listAccountModels and listCatalogModels read from DB                                            |
| **Display uses provider+account info** | Show provider slug, account email, model name | ✅ Implemented in aggregateCatalogModels                                                                |
| **Fetch diff report**                  | added / removed / unchanged                   | ✅ Returned in sync response for antigravity; partial for claude-code                                   |

---

## 8. Key Files Map

```
src/lib/providers/
├── integrations.functions.ts          ← All server functions (sync, fetch, test, delete)
│     fetchModels()                    ← Entry point for "Fetch Models" button
│     syncAccount()                    ← Full sync (identity + models + quota)
│     testAccountModels()              ← Manual test trigger
│     disconnectAccount()              ← Account deletion (currently naive)
│     listCatalogModels()              ← Cross-account model catalog
│     listAccountModels()              ← Per-account model list
│
├── antigravity-persistence.ts         ← DB upsert logic for Antigravity models
│     upsertAntigravityIdeVisibleModelsSupabase()  ← main upsert + stale marking
│     markStaleModelsSupabase()        ← marks removed models as stale
│     dedupeAccountModelRowsSupabase() ← deduplication pass
│
├── models-persistence.ts              ← Generic model store interface
├── model-keys.ts                      ← external_id ↔ DB key transformations
├── sync-response.types.ts             ← SyncAccountResponse type
│
└── adapters/
    ├── claude-code.server.ts          ← Claude OAuth adapter
    │     listModels()  ← HARDCODED (CLAUDE_CURATED_MODELS)
    │     testModel()   ← Real inference, checks HTTP 2xx
    │     fetchIdentity() ← Profile + quota fetch
    │
    ├── antigravity.server.ts          ← Google Cloud Code Assist adapter
    │     listModels()     ← Dynamic: calls fetchAntigravitySnapshot()
    │     testModel()      ← Real inference via streamGenerateContent
    │     syncAntigravityAccount() ← Full sync: profile + models + quota
    │     fetchAntigravityLiveRaw() ← Raw model fetch only (for live snapshot)
    │
    └── _shared/
        ├── oauth-clients.server.ts    ← CLAUDE_CURATED_MODELS defined here ⚠️
        ├── antigravity-models.server.ts ← fetchAvailableModels, fetchAntigravitySnapshot
        └── antigravity-constants.server.ts ← base URLs, headers
```

---

## 9. Database Schema (key tables)

```sql
providers
  id          uuid PK
  slug        text  -- "claude-code" | "antigravity" | "opencode-zen"
  name        text
  category    text  -- "oauth" | "free"
  auth_type   text

accounts
  id              uuid PK
  provider_id     uuid FK → providers.id
  label           text    -- email or user-set name
  email           text
  plan            text
  status          text    -- "healthy" | "degraded" | "expired"
  credentials_enc bytea   -- AES-256-GCM encrypted
  credentials_iv  bytea
  credentials_tag bytea
  quota_used      int
  quota_total     int
  quota_unit      text    -- "%"
  quota_extra     jsonb   -- nested quota groups, project ID, etc.
  last_synced_at  timestamptz
  last_health_check_at timestamptz

models
  id              uuid PK
  provider_id     uuid FK → providers.id
  account_id      uuid FK → accounts.id   ← ONE account per row
  external_id     text    -- "acct:<account_id>:<provider_external_id>"
  display_name    text
  capabilities    jsonb   -- { list: string[], provider_external_id, ...extra }
  lifecycle       text    -- "discovered" | "approved" | "blocked"
  enabled         bool
  test_status     text    -- "working" | "failed" | null
  latency_ms      int
  last_tested_at  timestamptz
  last_test_error text
  context_window  int
  quality_rating  int

venom_models
  id    uuid PK
  slug  text   -- "venom/lite" | "venom/pro" | "venom/max"

routing_rules
  id            uuid PK
  venom_model_id uuid FK → venom_models.id
  model_id       uuid FK → models.id
  priority       int
  enabled        bool
```

### external_id encoding

Model rows use a prefixed `external_id` to scope them per-account:

```
DB external_id = "acct:<account_uuid>:<provider_model_id>"
Provider external_id = "<provider_model_id>"   (e.g., "gemini-2.0-flash")
```

`providerExternalId(row.external_id, row.capabilities)` strips the prefix to recover the bare provider ID. `toDbExternalId(accountId, ext)` re-applies it.

### capabilities JSONB shape

```jsonb
{
  "list": ["chat", "tools", "vision"],
  "provider_external_id": "gemini-2.0-flash",
  "stale": true,                         ← set when model no longer in provider list
  "stale_reason": "removed_from_recommended",
  "ide_visible": true,                   ← antigravity: model appeared in Recommended list
  "antigravity_raw": { ... },            ← raw entry from fetchAvailableModels
  "quota": { "remainingFraction": 0.8, "resetTime": "...", "isExhausted": false }
}
```

---

## Quick Reference: What needs fixing to match desired design

### Fix 1 — Claude Code: make model list dynamic

**File:** `src/lib/providers/adapters/claude-code.server.ts` → `listModels()`

Remove: `return CLAUDE_CURATED_MODELS.map(...)`
Replace with: actual call to Anthropic models API (e.g., `GET /v1/models`) using the OAuth token.

### Fix 2 — Fetch models: test-before-insert for new models

**File:** `src/lib/providers/antigravity-persistence.ts` → `upsertAntigravityIdeVisibleModelsSupabase()`

For new models (not in DB), call `testModel()` before inserting. Only insert if test passes.

### Fix 3 — Removed models: smart delete instead of soft-mark-stale

**File:** `src/lib/providers/antigravity-persistence.ts` → `markStaleModelsSupabase()`

Instead of marking `stale=true`, implement shared-model logic:

- Check if another account (same provider) also has this model
- If yes: only remove the current account's row
- If no: hard-delete the model row

### Fix 4 — Account deletion: shared-model cleanup

**File:** `src/lib/providers/integrations.functions.ts` → `disconnectAccount()`

Before deleting the account, iterate its models and apply the shared-model deletion logic above.

### Fix 5 — Model test: validate actual response content

**File:** Both adapter `testModel()` functions

Parse the response body and check that actual text content was returned (not just `r.ok`). Suggested: require non-empty response text to consider the test a pass.
