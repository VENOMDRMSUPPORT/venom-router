# Port Antigravity + Claude Code from the reference repo

The reference repo (`venom-router-main`) is Next.js + Prisma; this project is TanStack Start + Supabase. So this is a logic port, not a copy-paste — every Prisma call becomes a Supabase call, every Next API route becomes a TSS server function or `/api/public/*` server route, and credentials go through the existing `encryptSecret`/`packCredentials` helpers.

I'll do it in three waves so the app keeps working between turns.

## Wave 1 — Shared infra + Antigravity adapter (this turn)

Files to add (logic adapted from `lib/adapters/_shared/*` and `lib/adapters/antigravity/*`):

- `src/lib/providers/adapters/_shared/types.ts` — `ModelInfo`, `QuotaPeriod`, `ModelQuotaInfo`, `AntigravityQuotaGroup`, `HealthCheckResult`, `TestCallResult`.
- `src/lib/providers/adapters/_shared/http.server.ts` — `resolveBaseUrl`, `asRecord`, `sleep`, `createAuthHeaders`.
- `src/lib/providers/adapters/antigravity.server.ts` — full rewrite:
  - `startFlow()` — Google OAuth PKCE for installed-app client (ANTIGRAVITY_CLIENT_ID / SECRET).
  - `completeFlow()` — exchange code → access/refresh tokens.
  - `refreshAccessToken()` + `ensureValidToken()` — auto-refresh 5 min before expiry, persist back to `accounts` table via `packCredentials`.
  - `fetchProfile()` — `POST /v1internal:loadCodeAssist` to get `projectId`, plan tier, `currentTier.userDefinedCloudaicompanionProject`.
  - `fetchQuota()` — `POST /v1internal:countGenerateTokensQuota` parsed into weekly + 5-hour quota groups per model family (Gemini 3 Pro, Gemini 2.5 Pro, etc.).
  - `listModels()` — curated catalog merged with live quota.
  - `testModel()` — non-streaming `generateContent` ping.
  - `fetchIdentity()` — packages email/plan/quota for the `accounts` row.
- `supabase/migrations/<ts>_antigravity_secrets.sql` — adds two app-level secret names to `secrets`: `ANTIGRAVITY_CLIENT_ID`, `ANTIGRAVITY_CLIENT_SECRET` (requested via `add_secret` tool, not committed values).
- Wire the new `fetchIdentity` / `listModels` into the existing `integrations.functions.ts` switch so the OAuth dialog already triggers it.

## Wave 2 — Claude Code (Anthropic) parity (next turn)

- `src/lib/providers/adapters/_shared/anthropic-errors.ts` — port `errors.ts` classifier (rate-limit / quota / auth / model-disabled mapping).
- Rewrite `claude-code.server.ts`:
  - Keep current PKCE flow; add full **profile** fetch via JWT claim parsing + `/api/oauth/profile` fallback.
  - Add `/api/oauth/usage` parsing into both 5-hour and weekly windows (matches `lib/adapters/anthropic/profile.ts`).
  - Add `capability-probe` and `rate-aware-test` so `testModel` doesn't get throttled mid-discovery.
  - Use the curated catalog from `lib/adapters/anthropic/catalog.ts` (Opus 4.5 / Sonnet 4.5 / Haiku 4.5 / 3.5 family).

## Wave 3 — UI parity for quota + usage views (turn 3)

- Port `components/providers/antigravity-quota-details.tsx` into `src/components/providers/antigravity-quota-details.tsx` — bar chart per model family with reset countdown.
- Add a "Refresh" button on connected accounts that triggers `refreshAccount` server fn (which calls `fetchIdentity` + writes new `quotas` rows).
- Quota page (`/quota`) and Usage page (`/usage`): replace empty state with real tables sourced from `quotas` and `usage_records`, grouped per account, matching the reference layout.

## Secrets needed before Wave 1 ships

Antigravity uses Google's installed-app OAuth, which requires a client ID + secret. Reference repo reads `ANTIGRAVITY_CLIENT_ID` / `ANTIGRAVITY_CLIENT_SECRET`. After you approve this plan I'll request both via `add_secret` (one prompt, both keys) before writing the adapter — without them the flow can't exchange the auth code.

## Risk + rollback

- Each wave is additive. I won't delete the existing `antigravity.server.ts` / `claude-code.server.ts` until the replacement compiles and passes a smoke call.
- DB schema is unchanged — the `accounts` / `quotas` / `models` tables already match what we need; only the adapter logic changes.

## Out of scope

- OpenCode Zen (kept as-is — reference repo has no special handling beyond api-key).
- Routing / playground / venom-models pages (not part of the request).

Approve to start Wave 1.
