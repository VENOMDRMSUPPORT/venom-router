# AGENTS.md

Source-of-truth guide for any AI agent (or human contributor) working in this
repository. Read this before editing anything. When this file and another doc
disagree, **this file wins**.

> A CLAUDE.md sits next to this file for Claude Code; it is a thin pointer back
> here so the guidance never diverges.

## FIRST THING — MANDATORY

Before reading ANY source file, run this:

```bash
graphify query "<your question about the codebase>"
```

The knowledge graph (`graphify-out/graph.json`) contains the full codebase
structure — 1,300+ nodes, 2,700+ edges. It gives faster, cheaper answers than
reading files one by one. Only read source files when the graph doesn't have
enough detail. If `graphify-out/graph.json` is missing, rebuild it first:

```bash
graphify .   # full build (~30s)
```

## Stack

| Layer     | Technology                                              |
| --------- | ------------------------------------------------------- |
| Framework | TanStack Start (SSR React 19) + Vite                    |
| Auth      | Supabase Auth (owner-only; first signup becomes owner)  |
| Database  | Supabase (PostgreSQL) — migrations under `supabase/migrations/` |
| API       | Plain REST under `/api/*`, dispatched from `src/server.ts` |
| UI        | shadcn/ui (Radix + Tailwind v4)                         |
| Charts    | Recharts                                                |
| Tests     | Vitest (`node` env, `src/**/*.test.ts`)                 |
| Package manager | Bun (`bun.lock` is canonical)                    |

## Commands

```bash
bun install        # install deps (24h supply-chain guard via bunfig.toml)
bun dev            # dev server — http://localhost:8084 (strictPort)
bun build          # production build
bun preview        # preview production build — http://localhost:8084
bun lint           # ESLint
bun format         # Prettier write
bun test           # vitest run (one-shot)
bun test:watch     # vitest watch
```

Before claiming work is done, run **all four**: `tsc --noEmit`, `bun lint`,
`bun test`, `bun build`. All must pass.

## Architecture

Venom Router is a **private single-owner AI gateway**. It connects provider
accounts (Claude, Antigravity, OpenCode Zen) and exposes three unified model
tiers — `venom/lite`, `venom/pro`, `venom/max` — through an OpenAI-compatible
`/v1/chat/completions` endpoint plus an owner dashboard.

**No Lovable.** This project has zero Lovable dependencies, no `.lovable/` directory,
and no external error-reporting SaaS. All logging is local via `src/lib/logger.ts`.

### Request flow

```
Browser
  └─ src/lib/api-client.ts (authFetch — injects Supabase session Bearer)
       └─ fetch("/api/dashboard/{resource}/{id?}/{sub?}")
            └─ src/server.ts (worker fetch handler)
                 └─ handleDashboardAPI(request)   [src/lib/api/dashboard-router.server.ts]
                      ├─ requireDashboardAuth      [src/lib/dashboard-auth.server.ts]
                      ├─ zod schema validation
                      └─ dispatch by path → handler
```

Public `/api/v1/chat/completions` is separate: API-key auth
(`src/lib/api-key-auth.server.ts`) → `routeRequest` (routing engine).

### There is exactly ONE transport

There is **no** `createServerFn` / `useServerFn` layer. All dashboard data goes
through the REST dispatcher above. Do not reintroduce server-function wrappers.

### Routing engine (`src/lib/routing/`)

```
routeRequest(venomSlug, messages, apiKeyId)
  ├─ classifyTask(messages)            → task class (simple_chat/coding/…/critical_task)
  ├─ load venom_model + routing_rules
  ├─ enrichCandidate (costType, qualityScore)
  ├─ filterCandidatesWithDiagnostics   → lifecycle/health/quota/capability/condition
  ├─ for each escalation stage (free→premium):
  │     ├─ scoreCandidate              → cost/speed/quality weights + premium penalty
  │     ├─ applyAccountRotation        → round_robin / quota_weighted / health_weighted
  │     └─ executeWithFallback         → decrypt creds → adapter.chat()
  ├─ persistUsageAndTrace              → usage_records + routing_traces (best-effort)
  └─ return result (+ trace if includeTrace)
```

Key files: `engine`, `scorer`, `filter`, `policy`, `rotation`, `executor`,
`classifier`, `trace`, `strategy.types`.

**Strategy config** lives in `venom_models.strategy_jsonb` (per-tier weights,
escalation stages, rotation strategy, premium reserve). Edit via
`/venom-models` dashboard — do not hand-edit JSONB.

### File-based routing (`src/routes/`)

- `__root.tsx` — app shell, providers, error boundary
- `_authenticated/route.tsx` — auth guard + dashboard layout
- `_authenticated/overview.tsx` — dashboard home (usage summary, quick actions)
- `_authenticated/models.tsx` — provider accounts + model catalog sync
- `_authenticated/venom-models.tsx` — venom tier config (rules, escalation, weights)
- `_authenticated/api-keys.tsx` — API key management (create, revoke, limits)
- `_authenticated/usage.tsx` — 7d traffic + model/account breakdown charts
- `_authenticated/diagnostics.tsx` — routing trace inspector, filter reasons
- `_authenticated/settings.tsx` — encryption key, worker secret, env validation
- `routeTree.gen.ts` — **auto-generated, never hand-edit**

### File naming

- `.server.ts` — server-only (may use Node APIs like `crypto`). The TanStack
  Start import-protection plugin errors if these reach the client bundle.
- `.test.ts` — Vitest suite, co-located with the module it tests.
- `@/` always maps to `src/`. Prefer it over deep relative imports.

### Provider adapters (`src/lib/providers/adapters/<slug>.server.ts`)

Each OAuth provider exports: `startFlow`, `completeFlow`, `fetchIdentity`,
`listModels`, `testModel`, `chat`. Shared helpers live in `_shared/`.

### Background workers (`src/lib/workers/`)

`runScheduled(cron)` runs health checks + quota snapshots. Triggered via the
Cloudflare cron in `wrangler.toml` (`*/5 * * * *`) → `/api/internal/run-workers`
(protected by `DEV_WORKER_SECRET`).

## Security rules — non-negotiable

1. **Credentials** are encrypted at rest with AES-256-GCM (`crypto.server.ts`).
   `VENOM_ENCRYPTION_KEY` **must** be set — the app refuses to start without it.
   There is no insecure fallback.
2. **Never log secrets.** Use `src/lib/logger.ts` (`createLogger`) instead of
   `console.*` — it redacts keys matching `secret|password|token|api_key|
   credentials|authorization`. Do not stringify request/response bodies that
   may contain tokens.
3. **Routing traces store only rule IDs + decision reasons** — never provider
   names, URLs, or tokens.
4. **API keys** (`venom_api_keys`): raw shown once on creation; only the SHA-256
   hash is stored. Revocation sets `revoked_at`.
5. **API key limits** — RPM enforced in-memory; TPD and monthly USD caps
   enforced via single-query atomic upsert in `checkKeyLimits` (no race window).
5. **RLS** is enabled on every owner-facing table, scoped to `is_owner()`
   (first signup is auto-granted the `owner` role).
6. **No off-process telemetry.** Errors are logged locally only — never forward
   to an external SaaS. (Sentry/self-hosted can be added later behind the logger.)
7. The service-role key bypasses RLS — keep it server-side only; `client.server.ts`
   is a lazy proxy that never ships to the browser bundle.

## Environment

Required vars are documented in `.env.example`. Copy it to `.env` (gitignored).
Minimum to run: `SUPABASE_URL`, `SUPABASE_SERVICE_ROLE_KEY`,
`SUPABASE_PUBLISHABLE_KEY` (+ `VITE_` copies), `VENOM_ENCRYPTION_KEY`.

## Language

- **Chat responses:** Always respond in **Arabic** in chat. If the user writes
  in English, respond in English to match their language.
- **Code and files:** Always written in **English** — no Arabic in source code,
  comments, commit messages, or documentation files.

## Conventions

- **Tests:** Vitest. Mock the Supabase client with a small in-memory fake (see
  `src/lib/db/*.test.ts` for the pattern). Use `vi.mock`/`vi.fn` (not `bun:test`).
- **Types:** `strict` is on. Fix `tsc` errors; don't silence them with `any`.
  (Some `any` remains in the Supabase query layer where generated types lag the
  migrations — those are `warn` in eslint, tracked for a separate cleanup.)
- **DB access:** go through the helpers in `src/lib/db/*.server.ts`. Never inline
  raw query strings in route components.
- **Dashboard responses** are typed in `src/lib/dashboard-types.ts` — extend it
  when adding endpoints rather than casting to `any`.
- **UI components** in `src/components/ui/` are shadcn/ui — regenerate via the
  `shadcn` CLI rather than hand-editing.

## Things that will bite you

- The dev server port is **8084**, not 8081 (older docs were wrong).
- `bun:test` imports break under Vitest — the project standardized on Vitest.
  Don't reintroduce Bun's test runner.
- Don't add `tanstackStart`/`viteReact`/`tailwindcss`/`tsConfigPaths`/`nitro`
  plugins through some meta-wrapper — they're wired explicitly in `vite.config.ts`.
  Duplicate plugins break the build.
- The generated Supabase types (`src/integrations/supabase/types.ts`) lag behind
  migrations (e.g. the `condition` column on `routing_rules`). When the type says
  a column is missing but a migration adds it, the migration is right.

## Knowledge Graph (graphify)

This project uses [graphify](https://graphify.net) to maintain a knowledge
graph of the codebase. The graph lives in `graphify-out/` (gitignored) and
provides a queryable map of entities, relationships, and community structure.

### Why this exists

Reading every file burns context tokens. The graph compresses the codebase into
a structured summary that agents can query with `graphify query` — fast, cheap,
and accurate.

### Agent instructions

Before answering codebase questions or making code changes, **query the graph
first**:

```bash
graphify query "what does this module do"       # BFS broad context
graphify query "how does X connect to Y" --dfs   # DFS specific path
graphify explain "ModuleOrConcept"               # single-node explanation
```

Only read source files directly when the graph doesn't have enough detail.

### Rebuilding the graph

**Automatic:** A git `post-commit` hook runs `graphify --update .` after every
commit. You don't need to do anything — the graph stays fresh as code is
committed.

**Manual (after big changes):** If you've made significant changes (new modules,
major refactors, deleted files), run the update immediately so the graph reflects
the current state:

```bash
graphify --update .    # incremental — only re-extracts changed files
graphify .             # full rebuild (slower, more thorough)
```

**Rule of thumb:** If you changed 5+ files or added/removed a module, run
`graphify --update .` before committing. The hook will catch smaller changes.

### Key commands

| Command | What it does |
|---------|-------------|
| `graphify query "question"` | BFS traversal — broad context |
| `graphify query "question" --dfs` | DFS — trace a specific path |
| `graphify explain "NodeName"` | Explain a single concept |
| `graphify path "A" "B"` | Shortest path between concepts |
| `graphify --update .` | Incremental update (changed files only) |
| `graphify .` | Full rebuild |
