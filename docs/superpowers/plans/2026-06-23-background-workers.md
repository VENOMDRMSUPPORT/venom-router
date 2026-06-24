# Phase 5 — Background Workers Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add two background workers (health check + quota snapshot) that run every 5 minutes via Cloudflare Workers scheduled handler, keeping account status and quota data fresh without user action.

**Architecture:** The workers live in `src/lib/workers/`. A `scheduled` handler is added to `src/server.ts` (the Cloudflare Workers entry). The health check worker calls existing adapter methods (`fetchIdentity` for claude-code, `syncAntigravityAccount` for antigravity), always persists refreshed credentials back to the DB, then inserts rows into `account_health_checks`. The quota snapshot worker takes those results and inserts rows into `quota_snapshots`. A dev-only trigger endpoint (`POST /api/internal/run-workers`) allows manual testing without a Cloudflare deployment.

**Tech Stack:** Bun, TypeScript, TanStack Start / Nitro (Cloudflare Workers target), Supabase (service role client), existing adapters in `src/lib/providers/adapters/`

## Global Constraints

- Server-only files MUST end in `.server.ts` — never import from client code
- Use `supabaseAdmin` from `@/integrations/supabase/client.server` for all worker DB access (bypasses RLS)
- `ClaudeAuthError` (name === "ClaudeAuthError" OR message contains "re-login required") → set account `status = "expired"` and stop
- Any other error → set account `status = "degraded"`
- Always persist refreshed credentials: if the adapter returns updated `creds`, pack and save to `accounts` table
- `packCredentials` / `unpackCredentials` are in `@/lib/credentials.server`
- Path alias `@/` maps to `src/`
- Never add Nitro / TanStack / Vite plugins to `vite.config.ts` — they are already included by `@lovable.dev/vite-tanstack-config`

---

## File Map

| Path                                       | Action | Responsibility                                            |
| ------------------------------------------ | ------ | --------------------------------------------------------- |
| `src/lib/workers/health-check.server.ts`   | Create | Health check + token refresh for all non-expired accounts |
| `src/lib/workers/quota-snapshot.server.ts` | Create | Insert `quota_snapshots` rows from health check results   |
| `src/lib/workers/index.server.ts`          | Create | Dispatcher — runs both workers in sequence                |
| `src/server.ts`                            | Modify | Add `scheduled` handler + dev trigger endpoint            |

---

### Task 1: Health Check Worker

**Files:**

- Create: `src/lib/workers/health-check.server.ts`

**Interfaces:**

- Consumes: `supabaseAdmin` (passed in), adapters `fetchIdentity` (claude-code), `syncAntigravityAccount` (antigravity), `unpackCredentials` / `packCredentials`
- Produces: `AccountHealthCheckResult[]` — consumed by Task 2

- [ ] **Step 1: Create the file**

```typescript
// src/lib/workers/health-check.server.ts
import type { SupabaseClient } from "@supabase/supabase-js";
import { unpackCredentials, packCredentials } from "@/lib/credentials.server";
import type { StoredCredentials } from "@/lib/providers/adapters/types";

export interface AccountHealthCheckResult {
  account_id: string;
  provider_slug: string;
  ok: boolean;
  latency_ms: number;
  error?: string;
  new_status: "healthy" | "degraded" | "expired";
  quota_used: number | null;
  quota_total: number | null;
  quota_unit: string | null;
  quota_extra: Record<string, unknown> | null;
}

function isClaudeAuthError(slug: string, e: unknown): boolean {
  if (slug !== "claude-code") return false;
  const err = e as { name?: string; message?: string } | null;
  return (
    err?.name === "ClaudeAuthError" || String(err?.message ?? "").includes("re-login required")
  );
}

export async function runHealthChecks(
  supabase: SupabaseClient,
): Promise<AccountHealthCheckResult[]> {
  const { data: accounts, error } = await supabase
    .from("accounts")
    .select("id,credentials_enc,credentials_iv,credentials_tag,quota_extra,providers(slug)")
    .neq("status", "expired");

  if (error) throw new Error(`Health check: failed to fetch accounts: ${error.message}`);

  const results: AccountHealthCheckResult[] = [];
  const checkedAt = new Date().toISOString();

  for (const acct of accounts ?? []) {
    const slug = (acct.providers as { slug?: string } | null)?.slug ?? "";
    let result: AccountHealthCheckResult = {
      account_id: acct.id as string,
      provider_slug: slug,
      ok: false,
      latency_ms: 0,
      new_status: "degraded",
      quota_used: null,
      quota_total: null,
      quota_unit: null,
      quota_extra: null,
    };

    let refreshedCreds: StoredCredentials | null = null;

    try {
      const credsIn = unpackCredentials({
        credentials_enc: acct.credentials_enc,
        credentials_iv: acct.credentials_iv,
        credentials_tag: acct.credentials_tag,
      });
      const t0 = Date.now();

      if (slug === "claude-code") {
        const adapter = await import("@/lib/providers/adapters/claude-code.server");
        const r = await adapter.fetchIdentity(credsIn);
        refreshedCreds = r.creds;
        result = {
          ...result,
          ok: r.health.ok,
          latency_ms: Date.now() - t0,
          error: r.health.error,
          new_status: r.health.ok ? "healthy" : "degraded",
          quota_used: r.identity.quota_used,
          quota_total: r.identity.quota_total,
          quota_unit: r.identity.quota_unit,
          quota_extra: (r.identity.quota_extra as Record<string, unknown>) ?? null,
        };
      } else if (slug === "antigravity") {
        const adapter = await import("@/lib/providers/adapters/antigravity.server");
        const r = await adapter.syncAntigravityAccount(credsIn);
        refreshedCreds = r.creds;
        result = {
          ...result,
          ok: r.health.ok,
          latency_ms: r.health.latency_ms,
          error: r.health.error,
          new_status: r.health.ok ? "healthy" : "degraded",
          quota_used: r.identity.quota_used,
          quota_total: r.identity.quota_total,
          quota_unit: r.identity.quota_unit,
          quota_extra: (r.identity.quota_extra as Record<string, unknown>) ?? null,
        };
      }
      // opencode-zen: no health check method — skip
    } catch (e: unknown) {
      result.ok = false;
      result.error = String((e as { message?: string } | null)?.message ?? e).slice(0, 500);
      result.new_status = isClaudeAuthError(slug, e) ? "expired" : "degraded";
    }

    // Build account update patch
    const patch: Record<string, unknown> = {
      status: result.new_status,
      last_health_check_at: checkedAt,
    };
    if (result.quota_used !== null) patch.quota_used = result.quota_used;
    if (result.quota_total !== null) patch.quota_total = result.quota_total;
    if (result.quota_unit !== null) patch.quota_unit = result.quota_unit;
    if (result.quota_extra !== null) patch.quota_extra = result.quota_extra;

    if (refreshedCreds) {
      const packed = packCredentials(refreshedCreds);
      patch.credentials_enc = packed.credentials_enc;
      patch.credentials_iv = packed.credentials_iv;
      patch.credentials_tag = packed.credentials_tag;
    }

    await supabase.from("accounts").update(patch).eq("id", acct.id);

    await supabase.from("account_health_checks").insert({
      account_id: acct.id,
      checked_at: checkedAt,
      status: result.new_status,
      latency_ms: result.latency_ms,
      error_code: result.ok ? null : "check_failed",
      error_message: result.error ?? null,
    });

    results.push(result);
    console.log(
      `[health-check] ${slug}/${acct.id} → ${result.new_status} (${result.latency_ms}ms)${result.error ? ` err=${result.error.slice(0, 80)}` : ""}`,
    );
  }

  return results;
}
```

- [ ] **Step 2: Verify TypeScript compiles (no build step needed — just confirm no import errors)**

Run: `bun run tsc --noEmit 2>&1 | head -40`
Expected: zero errors related to `health-check.server.ts`

- [ ] **Step 3: Commit**

```bash
git add src/lib/workers/health-check.server.ts
git commit -m "feat(workers): add health check worker for all non-expired accounts"
```

---

### Task 2: Quota Snapshot Worker

**Files:**

- Create: `src/lib/workers/quota-snapshot.server.ts`

**Interfaces:**

- Consumes: `AccountHealthCheckResult[]` from Task 1
- Produces: rows inserted into `quota_snapshots` table

- [ ] **Step 1: Create the file**

```typescript
// src/lib/workers/quota-snapshot.server.ts
import type { SupabaseClient } from "@supabase/supabase-js";
import type { AccountHealthCheckResult } from "./health-check.server";

export async function runQuotaSnapshots(
  supabase: SupabaseClient,
  results: AccountHealthCheckResult[],
): Promise<void> {
  const withQuota = results.filter((r) => r.quota_used !== null && r.quota_total !== null);

  if (!withQuota.length) return;

  const snappedAt = new Date().toISOString();

  const { error } = await supabase.from("quota_snapshots").insert(
    withQuota.map((r) => ({
      account_id: r.account_id,
      snapped_at: snappedAt,
      quota_type: "tokens",
      period: "rolling",
      used: r.quota_used,
      total: r.quota_total,
      remaining:
        r.quota_total !== null && r.quota_used !== null ? r.quota_total - r.quota_used : null,
      quota_source: "provider_reported",
      confidence: "high",
    })),
  );

  if (error) {
    console.error("[quota-snapshot] insert failed:", error.message);
  } else {
    console.log(`[quota-snapshot] inserted ${withQuota.length} snapshot(s)`);
  }
}
```

- [ ] **Step 2: Verify TypeScript compiles**

Run: `bun run tsc --noEmit 2>&1 | head -40`
Expected: zero errors related to `quota-snapshot.server.ts`

- [ ] **Step 3: Commit**

```bash
git add src/lib/workers/quota-snapshot.server.ts
git commit -m "feat(workers): add quota snapshot worker"
```

---

### Task 3: Worker Dispatcher

**Files:**

- Create: `src/lib/workers/index.server.ts`

**Interfaces:**

- Consumes: `runHealthChecks` from Task 1, `runQuotaSnapshots` from Task 2, `supabaseAdmin`
- Produces: `runScheduled()` exported function — consumed by Task 4

- [ ] **Step 1: Create the file**

```typescript
// src/lib/workers/index.server.ts
import { supabaseAdmin } from "@/integrations/supabase/client.server";
import { runHealthChecks } from "./health-check.server";
import { runQuotaSnapshots } from "./quota-snapshot.server";

export async function runScheduled(_cron: string): Promise<void> {
  const t0 = Date.now();
  console.log("[workers] scheduled run starting");

  try {
    const results = await runHealthChecks(supabaseAdmin);
    const healthy = results.filter((r) => r.ok).length;
    console.log(`[workers] health checks: ${results.length} accounts, ${healthy} healthy`);

    await runQuotaSnapshots(supabaseAdmin, results);
    console.log("[workers] quota snapshots done");
  } catch (e) {
    console.error("[workers] scheduled run failed:", e);
  }

  console.log(`[workers] complete in ${Date.now() - t0}ms`);
}
```

- [ ] **Step 2: Verify TypeScript compiles**

Run: `bun run tsc --noEmit 2>&1 | head -40`
Expected: zero errors

- [ ] **Step 3: Commit**

```bash
git add src/lib/workers/index.server.ts
git commit -m "feat(workers): add worker dispatcher"
```

---

### Task 4: Wire Up Scheduled Handler + Dev Trigger

**Files:**

- Modify: `src/server.ts`

**Interfaces:**

- Consumes: `runScheduled` from `src/lib/workers/index.server.ts`
- Produces: Cloudflare Workers `scheduled` handler + `POST /api/internal/run-workers` for local testing

The current `src/server.ts` exports:

```ts
export default {
  async fetch(request: Request, env: unknown, ctx: unknown) { ... }
}
```

We add:

1. A `scheduled` handler to the export (Cloudflare Workers cron)
2. A branch in `fetch` for `POST /api/internal/run-workers` protected by `X-Worker-Secret` header (dev testing only)

- [ ] **Step 1: Read current src/server.ts**

Open `src/server.ts` — the current content is 78 lines ending at `};`

- [ ] **Step 2: Add the dev trigger and scheduled handler**

Replace the entire `export default { ... }` block (lines 41–78) with:

```typescript
const DEV_WORKER_SECRET = process.env.DEV_WORKER_SECRET ?? "";

export default {
  async fetch(request: Request, env: unknown, ctx: unknown) {
    try {
      const url = new URL(request.url);

      // ── Venom proxy API ──────────────────────────────────────────────
      if (url.pathname === "/api/v1/chat/completions") {
        if (request.method === "OPTIONS") {
          return new Response(null, { status: 204, headers: { Allow: "POST, OPTIONS" } });
        }
        if (request.method !== "POST") {
          return new Response(
            JSON.stringify({
              error: {
                message: "Method not allowed",
                type: "invalid_request_error",
                code: "method_not_allowed",
              },
            }),
            {
              status: 405,
              headers: { "Content-Type": "application/json", Allow: "POST, OPTIONS" },
            },
          );
        }
        try {
          return await handleChatCompletions(request);
        } catch (apiErr) {
          console.error("[venom/api] unhandled error in chat/completions:", apiErr);
          return new Response(
            JSON.stringify({
              error: {
                message: "Internal server error.",
                type: "server_error",
                code: "internal_error",
              },
            }),
            { status: 500, headers: { "Content-Type": "application/json" } },
          );
        }
      }

      // ── Dev worker trigger (local testing only) ───────────────────────
      if (url.pathname === "/api/internal/run-workers" && request.method === "POST") {
        const secret = request.headers.get("x-worker-secret") ?? "";
        if (!DEV_WORKER_SECRET || secret !== DEV_WORKER_SECRET) {
          return new Response(JSON.stringify({ error: "Unauthorized" }), {
            status: 401,
            headers: { "Content-Type": "application/json" },
          });
        }
        try {
          const { runScheduled } = await import("./lib/workers/index.server");
          await runScheduled("manual");
          return new Response(JSON.stringify({ ok: true }), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          });
        } catch (e: unknown) {
          const msg = String((e as { message?: string } | null)?.message ?? e);
          return new Response(JSON.stringify({ ok: false, error: msg }), {
            status: 500,
            headers: { "Content-Type": "application/json" },
          });
        }
      }

      // ── TanStack SSR ─────────────────────────────────────────────────
      const handler = await getServerEntry();
      const response = await handler.fetch(request, env, ctx);
      return await normalizeCatastrophicSsrResponse(response);
    } catch (error) {
      console.error(error);
      return new Response(renderErrorPage(), {
        status: 500,
        headers: { "content-type": "text/html; charset=utf-8" },
      });
    }
  },

  // ── Cloudflare Workers cron handler ────────────────────────────────
  async scheduled(
    event: { cron: string; scheduledTime: number },
    _env: unknown,
    ctx: { waitUntil: (p: Promise<unknown>) => void },
  ) {
    ctx.waitUntil(
      import("./lib/workers/index.server").then(({ runScheduled }) => runScheduled(event.cron)),
    );
  },
};
```

- [ ] **Step 3: Verify TypeScript compiles**

Run: `bun run tsc --noEmit 2>&1 | head -40`
Expected: zero errors

- [ ] **Step 4: Start the dev server and verify it boots**

Run (background): `bun dev`
Then: `curl -s http://localhost:8081/api/v1/chat/completions -X OPTIONS`
Expected: `HTTP/1.1 204 No Content`

- [ ] **Step 5: Test the dev trigger endpoint**

```bash
# Set a secret in your shell first:
export DEV_WORKER_SECRET=test-secret-123

# Start the server with the env var:
DEV_WORKER_SECRET=test-secret-123 bun dev &

# Trigger the workers:
curl -s -X POST http://localhost:8081/api/internal/run-workers \
  -H "x-worker-secret: test-secret-123"
```

Expected response: `{"ok":true}`
Expected server logs: `[workers] scheduled run starting`, `[health-check] ...`, `[workers] complete in ...ms`

- [ ] **Step 6: Verify rejection without secret**

```bash
curl -s -X POST http://localhost:8081/api/internal/run-workers
```

Expected: `{"error":"Unauthorized"}` with HTTP 401

- [ ] **Step 7: Commit**

```bash
git add src/server.ts
git commit -m "feat(workers): wire scheduled handler and dev trigger to server entry"
```

---

### Task 5: Cloudflare Cron Configuration

**Files:**

- Create: `wrangler.toml` (project root, if it does not exist yet)

The `scheduled` handler in `src/server.ts` is inert until Cloudflare knows to call it on a schedule. This is configured in `wrangler.toml`.

- [ ] **Step 1: Check if wrangler.toml already exists**

Run: `ls F:\projects\venom-router-react\wrangler.toml 2>/dev/null && echo exists || echo missing`

- [ ] **Step 2a: If it exists — add the triggers block**

Append to the existing `wrangler.toml`:

```toml
[triggers]
crons = ["*/5 * * * *"]
```

- [ ] **Step 2b: If it does not exist — create it**

```toml
# wrangler.toml — Cloudflare Workers deployment config
# The main build is handled by @lovable.dev/vite-tanstack-config.
# This file only declares the cron schedule for background workers.

name = "venom-router"
compatibility_date = "2024-09-23"

[triggers]
crons = ["*/5 * * * *"]
```

- [ ] **Step 3: Commit**

```bash
git add wrangler.toml
git commit -m "feat(workers): add Cloudflare cron trigger (every 5 min)"
```

---

## Verification Checklist

After all tasks complete, confirm:

- [ ] `bun run tsc --noEmit` passes with zero errors
- [ ] `bun dev` starts without errors on port 8081
- [ ] `POST /api/internal/run-workers` with correct secret returns `{"ok":true}`
- [ ] Server logs show `[health-check]` lines for each account
- [ ] Supabase `account_health_checks` table has new rows after trigger
- [ ] Supabase `quota_snapshots` table has new rows after trigger (for accounts with quota data)
- [ ] Supabase `accounts` table: `last_health_check_at` updated, `status` updated, credentials re-encrypted if refreshed
- [ ] `POST /api/internal/run-workers` without header returns 401
- [ ] Existing `/api/v1/chat/completions` still works (no regression)
