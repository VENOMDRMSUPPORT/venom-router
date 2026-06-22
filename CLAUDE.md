# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
bun dev          # start dev server (Vite + TanStack Start)
bun build        # production build (Nitro/Cloudflare target)
bun preview      # preview production build locally
bun lint         # ESLint
bun format       # Prettier write
bun run test:sync-sim   # run simulate-sync.ts script (tsx)
```

## Architecture

**Venom Router** is a private AI gateway — a single-owner dashboard that connects multiple AI provider accounts and routes traffic through three unified model tiers: `venom/lite`, `venom/pro`, `venom/max`.

### Framework

TanStack Start (SSR React) + Vite, configured via `@lovable.dev/vite-tanstack-config`. Do not add `tanstackStart`, `viteReact`, `tailwindcss`, `tsConfigPaths`, or `nitro` plugins manually — they're already included by that config and duplicating them breaks the build.

### Routing

File-based routing under `src/routes/`. Key conventions:

- `__root.tsx` — app shell, wraps everything
- `_authenticated/route.tsx` — layout guard: redirects to `/auth` if no session, renders sidebar + `<Outlet>`
- `_authenticated/*.tsx` — all authenticated pages
- `routeTree.gen.ts` — auto-generated, never edit by hand
- SSR is disabled globally (`defaultSsr: false`) because auth relies on `localStorage`

### Server Functions

All data mutations and fetches that need a database use `createServerFn` from `@tanstack/react-start`. Every server function requires the `requireSupabaseAuth` middleware, which validates the Bearer token and injects `{ supabase, userId, claims }` into context.

Server function files are co-located in `src/lib/`:

- `venom.functions.ts` — dashboard stats, venom model config, API key management, quota
- `lib/providers/integrations.functions.ts` — provider sync, OAuth flows, model test/enable

### File Naming Convention

Files suffixed `.server.ts` are server-only (can import Node.js APIs like `crypto`). They must never be imported from client code. The Vite config enforces this boundary.

### Supabase

Two clients exist:

- `src/integrations/supabase/client.ts` — browser client (uses `VITE_SUPABASE_*` env vars)
- `src/integrations/supabase/client.server.ts` — server client (uses `SUPABASE_*` env vars)

The auth middleware (`auth-middleware.ts`) creates a per-request server client authenticated with the user's Bearer token.

### Credential Encryption

Provider credentials (OAuth tokens, API keys) are encrypted with AES-256-GCM before being stored in the `accounts` table. The encryption key comes from `CREDENTIALS_SECRET` env var. `src/lib/crypto.server.ts` handles encrypt/decrypt; `src/lib/credentials.server.ts` handles pack/unpack with the PostgREST bytea wire format.

### Provider Adapters

Each provider lives in `src/lib/providers/adapters/<slug>.server.ts` and must export:

- `startFlow(opts)` — returns OAuth authorize URL + PKCE state (OAuth providers)
- `completeFlow(opts)` — exchanges code for tokens, returns `StoredCredentials`
- `fetchIdentity(creds)` — returns `AccountIdentity` + possibly refreshed `creds`
- `listModels(creds)` — returns `DiscoveredModel[]`
- `testModel(creds, externalId)` — returns `ModelTestResult`

Current providers: `claude-code`, `antigravity`, `opencode-zen`.

### Database Schema (key tables)

| Table            | Purpose                                                                     |
| ---------------- | --------------------------------------------------------------------------- |
| `providers`      | Static provider registry (slug, kind, adapter, base_url)                    |
| `accounts`       | Connected provider accounts with encrypted credentials                      |
| `models`         | Discovered models per account; lifecycle: `discovered → approved / blocked` |
| `venom_models`   | The three unified tiers (lite/pro/max) with routing weights                 |
| `routing_rules`  | Maps a venom slug to a provider model with priority + fallback config       |
| `venom_api_keys` | API keys issued to callers; stored as prefix + bcrypt hash                  |
| `usage_records`  | Per-request traces with cost, tokens, venom_slug, fallback_used             |
| `quotas`         | Provider quota snapshots per account                                        |
| `oauth_flows`    | Short-lived PKCE state rows (10 min TTL)                                    |

### UI Components

`src/components/ui/` — shadcn/ui components (Radix + Tailwind). Do not modify these directly; regenerate with `shadcn` CLI if needed.

`src/components/layout/` — app shell pieces (Sidebar, Header, PageShell).

`src/components/providers/` — provider management UI (cards, dialogs, quota bars).

### Path Alias

`@/` maps to `src/`. Always use this alias instead of relative `../../` imports.

### Lovable Integration

This project is connected to [Lovable](https://lovable.dev). Avoid rewriting published git history — no force-push, rebase, or amend on commits that are already pushed.
