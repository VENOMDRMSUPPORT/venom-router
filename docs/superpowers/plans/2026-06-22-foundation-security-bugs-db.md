# Foundation — Security, Bug Fixes & DB Schema

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Harden security, eliminate known bugs, extract shared utilities, fix performance issues, and add the missing database tables and columns required by the routing engine.

**Architecture:** Seven independent tasks applied to existing files. No new abstractions — surgical edits only. DB changes are additive SQL migrations that don't break existing queries.

**Tech Stack:** TanStack Start, Supabase, TypeScript, Bun, shadcn/ui. No test framework is configured — verification uses `bun run` inline checks, `bun build` type checks, and visual verification via `bun dev`.

## Global Constraints

- Package manager: `bun` only — never `npm` or `yarn`
- Path alias: `@/` maps to `src/` — always use alias, never relative `../../`
- Server-only files: suffix `.server.ts` — never import from client code
- Never commit `.env` files or secrets
- Never modify `routeTree.gen.ts` — auto-generated
- Never add duplicate Vite plugins — already handled by `@lovable.dev/vite-tanstack-config`

---

### Task 1: Harden Encryption Key

**Files:**
- Modify: `src/lib/crypto.server.ts:4-11`

**Interfaces:**
- Produces: `getKey()` throws `Error` when `VENOM_ENCRYPTION_KEY` is unset instead of silently using a weak fallback

- [ ] **Step 1: Read the current file**

Open `src/lib/crypto.server.ts` and confirm lines 4–11 contain the fallback logic with `"venom-dev"`.

- [ ] **Step 2: Replace the fallback with a throw**

Replace lines 4–17 in `src/lib/crypto.server.ts`:

```ts
function getKey(): Buffer {
  const raw = process.env.VENOM_ENCRYPTION_KEY;
  if (!raw) {
    throw new Error(
      "VENOM_ENCRYPTION_KEY is required. Generate one with: openssl rand -hex 32",
    );
  }
  // Accept hex (64 chars), base64 (>=43 chars), or raw 32-byte string.
  if (/^[0-9a-f]{64}$/i.test(raw)) return Buffer.from(raw, "hex");
  const b = Buffer.from(raw, "base64");
  if (b.length === 32) return b;
  return createHash("sha256").update(raw).digest();
}
```

- [ ] **Step 3: Verify the build passes**

```bash
bun build
```

Expected: no TypeScript errors in `crypto.server.ts`.

- [ ] **Step 4: Verify the throw fires when key is absent**

```bash
bun --eval "process.env.VENOM_ENCRYPTION_KEY = ''; const { encryptSecret } = await import('./src/lib/crypto.server.ts'); try { encryptSecret('test'); console.log('FAIL - should have thrown'); } catch(e) { console.log('PASS:', e.message); }"
```

Expected output: `PASS: VENOM_ENCRYPTION_KEY is required. Generate one with: openssl rand -hex 32`

- [ ] **Step 5: Commit**

```bash
git add src/lib/crypto.server.ts
git commit -m "security: throw on missing VENOM_ENCRYPTION_KEY instead of fallback"
```

---

### Task 2: Fix OAuth CSRF State Check

**Files:**
- Modify: `src/lib/providers/integrations.functions.ts:130-154`

**Interfaces:**
- Consumes: existing `completeOAuthFlow` server function shape
- Produces: `state` field is required (not optional) in the Zod schema; the CSRF check runs unconditionally

- [ ] **Step 1: Make `state` required in the Zod schema**

In `src/lib/providers/integrations.functions.ts`, find the `inputValidator` for `completeOAuthFlow` (around line 128). Change:

```ts
// BEFORE
z.object({
  flow_id: z.string().uuid(),
  code: z.string().min(1),
  state: z.string().optional(),
})
```

To:

```ts
// AFTER
z.object({
  flow_id: z.string().uuid(),
  code: z.string().min(1),
  state: z.string().min(1),
})
```

- [ ] **Step 2: Remove the conditional guard on the state check**

Find line 152 in `src/lib/providers/integrations.functions.ts`. Change:

```ts
// BEFORE
if (data.state && flow.state !== data.state) {
  throw new Error("OAuth state mismatch");
}
```

To:

```ts
// AFTER
if (flow.state !== data.state) {
  throw new Error("OAuth state mismatch");
}
```

- [ ] **Step 3: Verify callers send state**

Search for all callers of `completeOAuthFlow` in the client code:

```bash
grep -r "completeOAuthFlow" src/ --include="*.tsx" --include="*.ts" -n
```

For each caller, confirm it passes a `state` value. The OAuth callback pages must already have `state` from the URL query params — if any caller omits `state`, add it from `window.location` or the URL search params.

- [ ] **Step 4: Build check**

```bash
bun build
```

Expected: no TypeScript errors.

- [ ] **Step 5: Commit**

```bash
git add src/lib/providers/integrations.functions.ts
git commit -m "security: make OAuth state required and enforce CSRF check unconditionally"
```

---

### Task 3: Fix QuotaRing Color When Exhausted

**Files:**
- Modify: `src/components/providers/antigravity-quota-details.tsx:28-29`

**Interfaces:**
- Produces: `QuotaRing` shows red (`#ef4444`) when `remainingFraction <= 0`, orange when `< 0.2`, green otherwise

- [ ] **Step 1: Fix the color logic**

In `src/components/providers/antigravity-quota-details.tsx`, find line 28–29:

```ts
// BEFORE
const color =
  remainingFraction <= 0 ? "#22c55e" : remainingFraction < 0.2 ? "#f97316" : "#22c55e";
```

Replace with:

```ts
// AFTER
const color =
  remainingFraction <= 0 ? "#ef4444" : remainingFraction < 0.2 ? "#f97316" : "#22c55e";
```

- [ ] **Step 2: Verify visually**

```bash
bun dev
```

Navigate to a provider that has quota data. Confirm:
- Full quota → green ring
- Below 20% → orange ring
- At 0% / exhausted → red ring (was incorrectly green before)

- [ ] **Step 3: Commit**

```bash
git add src/components/providers/antigravity-quota-details.tsx
git commit -m "fix: quota ring shows red when exhausted instead of green"
```

---

### Task 4: Extract `formatRelativeTime` + Remove Dead Time Buttons

**Files:**
- Modify: `src/lib/utils.ts`
- Modify: `src/routes/_authenticated/overview.tsx`
- Modify: `src/routes/_authenticated/models.tsx`

**Interfaces:**
- Produces: `formatRelativeTime(dateStr: string | null): string` exported from `@/lib/utils`
- Consumes: both `overview.tsx` and `models.tsx` import from `@/lib/utils` — local copies deleted

- [ ] **Step 1: Add `formatRelativeTime` to shared utils**

In `src/lib/utils.ts`, append after the existing `cn` function:

```ts
export function formatRelativeTime(dateStr: string | null): string {
  if (!dateStr) return "never";
  const diffMs = Date.now() - new Date(dateStr).getTime();
  const diffMins = Math.floor(diffMs / 60000);
  if (diffMins < 1) return "just now";
  if (diffMins < 60) return `${diffMins}m ago`;
  const diffHours = Math.floor(diffMins / 60);
  if (diffHours < 24) return `${diffHours}h ago`;
  return new Date(dateStr).toLocaleDateString(undefined, { month: "short", day: "numeric" });
}
```

- [ ] **Step 2: Verify the function works inline**

```bash
bun --eval "import { formatRelativeTime } from './src/lib/utils.ts'; console.log(formatRelativeTime(null)); console.log(formatRelativeTime(new Date(Date.now() - 30000).toISOString())); console.log(formatRelativeTime(new Date(Date.now() - 3600000 * 2).toISOString()));"
```

Expected output:
```
never
just now
2h ago
```

- [ ] **Step 3: Update `models.tsx` to use shared util**

In `src/routes/_authenticated/models.tsx`:

1. Add import at the top (inside existing imports from `@/lib/utils`):
```ts
import { cn, formatRelativeTime } from "@/lib/utils";
```

2. Delete the local `formatRelativeTime` function (lines 67–76):
```ts
// DELETE this entire function:
function formatRelativeTime(dateStr: string | null): string {
  if (!dateStr) return "never";
  const diffMs = Date.now() - new Date(dateStr).getTime();
  const diffMins = Math.floor(diffMs / 60000);
  if (diffMins < 1) return "just now";
  if (diffMins < 60) return `${diffMins}m ago`;
  const diffHours = Math.floor(diffMins / 60);
  if (diffHours < 24) return `${diffHours}h ago`;
  return new Date(dateStr).toLocaleDateString(undefined, { month: "short", day: "numeric" });
}
```

- [ ] **Step 4: Update `overview.tsx` to use shared util and remove dead buttons**

In `src/routes/_authenticated/overview.tsx`:

1. Update the import from `@/lib/utils`:
```ts
import { cn, formatRelativeTime } from "@/lib/utils";
```

2. Delete the local `formatRelativeTime` function (lines 61–70).

3. Find the time period buttons (lines 200–210) and replace the entire button group + heading with a static label — the 30d/90d buttons have no data source and no onClick handler:

```tsx
// BEFORE (lines 191–211):
<div className="flex items-start justify-between gap-4 mb-4">
  <div>
    <h3 className="font-display text-sm font-semibold tracking-tight">
      Request volume
    </h3>
    <p className="text-xs text-muted-foreground mt-0.5">
      Last 7 days · all venom models
    </p>
  </div>
  <div className="flex items-center gap-1.5 text-[11px]">
    <button className="rounded-md bg-accent px-2 py-1 font-medium text-accent-foreground">
      7d
    </button>
    <button className="rounded-md px-2 py-1 text-muted-foreground hover:bg-muted">
      30d
    </button>
    <button className="rounded-md px-2 py-1 text-muted-foreground hover:bg-muted">
      90d
    </button>
  </div>
</div>

// AFTER:
<div className="mb-4">
  <h3 className="font-display text-sm font-semibold tracking-tight">
    Request volume
  </h3>
  <p className="text-xs text-muted-foreground mt-0.5">
    Last 7 days · all venom models
  </p>
</div>
```

- [ ] **Step 5: Build check**

```bash
bun build
```

Expected: no TypeScript errors. No duplicate `formatRelativeTime` declarations.

- [ ] **Step 6: Visual check**

```bash
bun dev
```

Go to `/models` — relative timestamps render correctly.
Go to `/overview` — chart header is clean with no non-functional buttons.

- [ ] **Step 7: Commit**

```bash
git add src/lib/utils.ts src/routes/_authenticated/overview.tsx src/routes/_authenticated/models.tsx
git commit -m "refactor: extract formatRelativeTime to shared utils, remove dead period buttons"
```

---

### Task 5: Make Model Testing Concurrent + Parallel Enable Updates

**Files:**
- Modify: `src/lib/providers/integrations.functions.ts:1266-1301`

**Interfaces:**
- Consumes: `adapter.testModel(creds, ext)` returns `{ ok: boolean; latency_ms: number; error?: string }`
- Produces: `testAccountModels` tests all external IDs concurrently; `setModelsEnabled` writes all rows in parallel

- [ ] **Step 1: Replace sequential test loop with concurrent Promise.all**

In `src/lib/providers/integrations.functions.ts`, find the `testAccountModels` handler (around line 1266):

```ts
// BEFORE
const results = [];
for (const ext of data.external_ids) {
  const r = await adapter.testModel(creds, ext);
  results.push(r);
  await supabase
    .from("models")
    .update({
      test_status: r.ok ? "working" : "failed",
      latency_ms: r.latency_ms,
      last_test_error: r.ok ? null : (r.error ?? null),
      last_tested_at: new Date().toISOString(),
      lifecycle: r.ok ? "approved" : "blocked",
    })
    .eq("account_id", data.account_id)
    .in("external_id", modelRowLookupKeys(data.account_id, ext));
}
return results;
```

Replace with:

```ts
// AFTER
const results = await Promise.all(
  data.external_ids.map(async (ext) => {
    const r = await adapter.testModel(creds, ext);
    await supabase
      .from("models")
      .update({
        test_status: r.ok ? "working" : "failed",
        latency_ms: r.latency_ms,
        last_test_error: r.ok ? null : (r.error ?? null),
        last_tested_at: new Date().toISOString(),
        lifecycle: r.ok ? "approved" : "blocked",
      })
      .eq("account_id", data.account_id)
      .in("external_id", modelRowLookupKeys(data.account_id, ext));
    return r;
  }),
);
return results;
```

- [ ] **Step 2: Replace sequential enable loop with parallel writes**

In `src/lib/providers/integrations.functions.ts`, find the `setModelsEnabled` handler (around line 1297):

```ts
// BEFORE
const { supabase } = context as any;
for (const [id, enabled] of Object.entries(data.enabled)) {
  await supabase.from("models").update({ enabled }).eq("id", id);
}
return { ok: true };
```

Replace with:

```ts
// AFTER
const { supabase } = context as any;
await Promise.all(
  Object.entries(data.enabled).map(([id, enabled]) =>
    supabase.from("models").update({ enabled }).eq("id", id),
  ),
);
return { ok: true };
```

- [ ] **Step 3: Build check**

```bash
bun build
```

Expected: no TypeScript errors.

- [ ] **Step 4: Functional check**

```bash
bun dev
```

Go to `/models`, select one or more models and click the test (▶) button. Confirm:
- When testing multiple models, they all complete faster than before (concurrent vs sequential)
- Toggle enable/disable on multiple models — UI updates correctly

- [ ] **Step 5: Commit**

```bash
git add src/lib/providers/integrations.functions.ts
git commit -m "perf: run model tests concurrently, parallelize setModelsEnabled writes"
```

---

### Task 6: Sidebar User from Route Context

**Files:**
- Modify: `src/components/layout/sidebar.tsx`

**Interfaces:**
- Consumes: `/_authenticated` route context `{ user: User }` via `useRouteContext({ from: '/_authenticated' })`
- Produces: `SidebarFooter` removes `useEffect` + `useState` for email; reads `user.email` directly

- [ ] **Step 1: Add `useRouteContext` import to sidebar.tsx**

In `src/components/layout/sidebar.tsx`, find the existing import from `@tanstack/react-router`:

```ts
// Find the line that imports from @tanstack/react-router (likely has useNavigate, Link, etc.)
// Add useRouteContext to that import:
import { useNavigate, Link, useRouteContext } from "@tanstack/react-router";
```

If `useRouteContext` is not yet in the import, add it.

- [ ] **Step 2: Replace the `useEffect` + `useState` in `SidebarFooter`**

Find `SidebarFooter` (around line 223). Replace:

```ts
// BEFORE
function SidebarFooter() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [busy, setBusy] = useState(false);
  const [email, setEmail] = useState<string | null>(null);

  useEffect(() => {
    supabase.auth.getUser().then(({ data }) => setEmail(data.user?.email ?? null));
  }, []);
```

With:

```ts
// AFTER
function SidebarFooter() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [busy, setBusy] = useState(false);
  const { user } = useRouteContext({ from: "/_authenticated" });
  const email = user?.email ?? null;
```

- [ ] **Step 3: Remove unused `useState` import if `email` state was the only useState**

Check if `useState` is still used elsewhere in `SidebarFooter`. If `busy` still needs `useState`, keep the import. Only remove `useState` from imports if it's no longer used anywhere in the file.

- [ ] **Step 4: Build check**

```bash
bun build
```

Expected: no TypeScript errors. TypeScript will catch any type mismatch on `user`.

- [ ] **Step 5: Visual check**

```bash
bun dev
```

Log in and confirm the sidebar footer still shows the correct email. Open DevTools Network tab — confirm there is no extra `auth/v1/user` request firing on load from the sidebar.

- [ ] **Step 6: Commit**

```bash
git add src/components/layout/sidebar.tsx
git commit -m "refactor: sidebar reads user email from route context, removes extra getUser() call"
```

---

### Task 7: Database Schema Migration

**Files:**
- Create: `supabase/migrations/20260622130000_foundation_schema.sql`

**Interfaces:**
- Produces: all columns and tables required by the routing engine, workers, and dashboard pages
- Note: All changes are `IF NOT EXISTS` / `IF NOT EXISTS` safe — re-running is idempotent

- [ ] **Step 1: Create the migration file**

Create `supabase/migrations/20260622130000_foundation_schema.sql` with the following content:

```sql
-- ============================================================
-- Phase 2: Foundation Schema
-- Adds missing columns to existing tables and creates new
-- tables required by the routing engine, workers, and pages.
-- All statements are idempotent (IF NOT EXISTS).
-- ============================================================

-- ----------------------------------------------------------
-- 1. Extend existing tables
-- ----------------------------------------------------------

-- models: cost data + blocked reason
ALTER TABLE public.models
  ADD COLUMN IF NOT EXISTS input_cost_per_mtok  NUMERIC,
  ADD COLUMN IF NOT EXISTS output_cost_per_mtok NUMERIC,
  ADD COLUMN IF NOT EXISTS max_output_tokens    INTEGER,
  ADD COLUMN IF NOT EXISTS blocked_reason       TEXT;

-- routing_rules: condition jsonb + role + fallback config
ALTER TABLE public.routing_rules
  ADD COLUMN IF NOT EXISTS condition             JSONB,
  ADD COLUMN IF NOT EXISTS role                  TEXT    NOT NULL DEFAULT 'primary',
  ADD COLUMN IF NOT EXISTS max_fallback_attempts INTEGER NOT NULL DEFAULT 3;

-- venom_models: routing weights + description
ALTER TABLE public.venom_models
  ADD COLUMN IF NOT EXISTS cost_weight    NUMERIC NOT NULL DEFAULT 0.3,
  ADD COLUMN IF NOT EXISTS speed_weight   NUMERIC NOT NULL DEFAULT 0.3,
  ADD COLUMN IF NOT EXISTS quality_weight NUMERIC NOT NULL DEFAULT 0.4,
  ADD COLUMN IF NOT EXISTS description    TEXT;

-- ----------------------------------------------------------
-- 2. Seed venom_models default weights
-- ----------------------------------------------------------
INSERT INTO public.venom_models (slug, display_name, cost_weight, speed_weight, quality_weight, description)
VALUES
  ('lite', 'Venom Lite', 0.7, 0.2, 0.1, 'Optimized for cost — fastest, cheapest routing'),
  ('pro',  'Venom Pro',  0.3, 0.3, 0.4, 'Balanced — quality-leaning for production use'),
  ('max',  'Venom Max',  0.1, 0.1, 0.8, 'Optimized for quality — best model available')
ON CONFLICT (slug) DO UPDATE SET
  cost_weight    = EXCLUDED.cost_weight,
  speed_weight   = EXCLUDED.speed_weight,
  quality_weight = EXCLUDED.quality_weight,
  description    = EXCLUDED.description;

-- ----------------------------------------------------------
-- 3. New table: account_health_checks
-- ----------------------------------------------------------
CREATE TABLE IF NOT EXISTS public.account_health_checks (
  id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id    UUID        NOT NULL REFERENCES public.accounts(id) ON DELETE CASCADE,
  checked_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  status        TEXT        NOT NULL, -- healthy | degraded | unreachable
  latency_ms    INTEGER,
  error_code    TEXT,
  error_message TEXT
);

CREATE INDEX IF NOT EXISTS account_health_checks_account_id_idx
  ON public.account_health_checks (account_id, checked_at DESC);

-- ----------------------------------------------------------
-- 4. New table: quota_snapshots
-- ----------------------------------------------------------
CREATE TABLE IF NOT EXISTS public.quota_snapshots (
  id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id   UUID        NOT NULL REFERENCES public.accounts(id) ON DELETE CASCADE,
  snapped_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  quota_type   TEXT        NOT NULL, -- tokens | requests | spend
  period       TEXT        NOT NULL, -- daily | monthly | rolling
  used         NUMERIC,
  total        NUMERIC,
  remaining    NUMERIC,
  resets_at    TIMESTAMPTZ,
  quota_source TEXT        NOT NULL DEFAULT 'locally_estimated',
                                    -- provider_reported | locally_estimated | manual
  confidence   TEXT        NOT NULL DEFAULT 'unknown'
                                    -- high | medium | low | unknown
);

CREATE INDEX IF NOT EXISTS quota_snapshots_account_id_idx
  ON public.quota_snapshots (account_id, snapped_at DESC);

-- ----------------------------------------------------------
-- 5. New table: routing_traces
-- (stores decision metadata only — NO provider secrets)
-- ----------------------------------------------------------
CREATE TABLE IF NOT EXISTS public.routing_traces (
  id                   UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  usage_record_id      UUID,
  candidates_evaluated INTEGER     NOT NULL DEFAULT 0,
  candidates_filtered  INTEGER     NOT NULL DEFAULT 0,
  selected_rule_id     UUID,
  decision_reason      TEXT,
  fallback_attempts    INTEGER     NOT NULL DEFAULT 0,
  modality             TEXT        NOT NULL DEFAULT 'text',
                                   -- text | vision | audio | documents
  created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS routing_traces_usage_record_idx
  ON public.routing_traces (usage_record_id);

CREATE INDEX IF NOT EXISTS routing_traces_created_at_idx
  ON public.routing_traces (created_at DESC);

-- ----------------------------------------------------------
-- 6. New table: system_settings (single-row config)
-- ----------------------------------------------------------
CREATE TABLE IF NOT EXISTS public.system_settings (
  id                            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  system_name                   TEXT        NOT NULL DEFAULT 'Venom Router',
  default_request_timeout_ms    INTEGER     NOT NULL DEFAULT 30000,
  default_max_fallback_attempts INTEGER     NOT NULL DEFAULT 3,
  health_check_interval_minutes INTEGER     NOT NULL DEFAULT 5,
  quota_warning_threshold_pct   INTEGER     NOT NULL DEFAULT 15,
  quota_critical_threshold_pct  INTEGER     NOT NULL DEFAULT 5,
  routing_trace_retention_days  INTEGER     NOT NULL DEFAULT 30,
  usage_record_retention_days   INTEGER     NOT NULL DEFAULT 90,
  updated_at                    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Seed one row if table is empty
INSERT INTO public.system_settings (id)
SELECT gen_random_uuid()
WHERE NOT EXISTS (SELECT 1 FROM public.system_settings);

-- ----------------------------------------------------------
-- 7. New table: audit_log_entries
-- ----------------------------------------------------------
CREATE TABLE IF NOT EXISTS public.audit_log_entries (
  id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  actor       TEXT,
  action      TEXT        NOT NULL,
  target      TEXT,
  metadata    JSONB,
  success     BOOLEAN     NOT NULL DEFAULT TRUE
);

CREATE INDEX IF NOT EXISTS audit_log_entries_occurred_at_idx
  ON public.audit_log_entries (occurred_at DESC);
```

- [ ] **Step 2: Apply the migration**

Option A — Supabase CLI (if logged in):
```bash
bun supabase db push
```

Option B — Supabase Dashboard (always works):
1. Open your Supabase project → SQL Editor
2. Paste the full content of the migration file
3. Click "Run"
4. Confirm no errors

- [ ] **Step 3: Verify tables exist**

In Supabase Dashboard → Table Editor, confirm these tables are visible:
- `account_health_checks`
- `quota_snapshots`
- `routing_traces`
- `system_settings`
- `audit_log_entries`

And confirm these columns exist on existing tables:
- `models.input_cost_per_mtok`, `models.output_cost_per_mtok`, `models.blocked_reason`
- `routing_rules.condition`, `routing_rules.role`, `routing_rules.max_fallback_attempts`
- `venom_models.cost_weight`, `venom_models.speed_weight`, `venom_models.quality_weight`

- [ ] **Step 4: Verify venom_models seeded**

In SQL Editor:
```sql
SELECT slug, display_name, cost_weight, speed_weight, quality_weight FROM venom_models;
```

Expected 3 rows:
```
lite | Venom Lite | 0.7 | 0.2 | 0.1
pro  | Venom Pro  | 0.3 | 0.3 | 0.4
max  | Venom Max  | 0.1 | 0.1 | 0.8
```

- [ ] **Step 5: Confirm app still loads**

```bash
bun dev
```

Navigate through all existing pages — confirm nothing broke from the schema additions.

- [ ] **Step 6: Commit**

```bash
git add supabase/migrations/20260622130000_foundation_schema.sql
git commit -m "feat: add foundation schema — health checks, quota snapshots, routing traces, system settings, audit log"
```

---

## Self-Review

**Spec coverage check:**
- ✅ Phase 0.1 — encryption key throw: Task 1
- ✅ Phase 0.2 — OAuth CSRF fix: Task 2
- ✅ Phase 1.1 — quota ring color: Task 3
- ✅ Phase 1.2 — dead buttons removed: Task 4
- ✅ Phase 1.3 — formatRelativeTime extracted: Task 4
- ✅ Phase 1.4 — concurrent model testing: Task 5
- ✅ Phase 1.5 — Promise.all for setModelsEnabled: Task 5
- ✅ Phase 1.6 — sidebar route context: Task 6
- ✅ Phase 2.1 — missing columns: Task 7
- ✅ Phase 2.2 — new tables: Task 7
- ✅ Phase 2.3 — venom_models seed: Task 7

**Placeholder scan:** No TBD, TODO, or vague steps. All code blocks are complete.

**Type consistency:**
- `formatRelativeTime(dateStr: string | null): string` — same signature in utils.ts and both usages
- `useRouteContext({ from: '/_authenticated' })` returns `{ user: User }` — matches `route.tsx` return type
- SQL column names match spec exactly

**No issues found.**

---

## Execution Options

**Plan complete and saved to `docs/superpowers/plans/2026-06-22-foundation-security-bugs-db.md`. Two execution options:**

**1. Subagent-Driven (recommended)** — fresh subagent per task, review between tasks, fast iteration

**2. Inline Execution** — execute tasks in this session using executing-plans

**Which approach?**
