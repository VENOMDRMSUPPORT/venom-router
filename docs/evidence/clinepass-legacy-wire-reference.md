# ClinePass (cline.bot) — Legacy Reference Behavior

Source: the owner's working legacy app (`F:\projects\venom-router`, read-only
reference, ~90% correct in production per the owner). Extracted 2026-08-04 from
`src/lib/providers/adapters/clinepass.server.ts`,
`clinepass-quota.server.ts`, `codex-clinepass-plan.ts`,
`clinepass-quota-display.ts`, `src/lib/workers/*.server.ts`, and
`src/routes/callback.tsx`. This document records the *proven* wire contracts
and orchestration cadence so the current Go app can be held to the same
behavior.

Base URL: `https://api.cline.bot`

## 1. Wire decoration (every authenticated call)

- `Authorization: Bearer workos:<access_token>` — the `workos:` prefix is
  applied idempotently at the wire; the stored token stays raw.
- Chat/token/refresh calls also send:
  `HTTP-Referer: https://cline.bot`, `X-Title: Cline`,
  `User-Agent: venom-router`, `X-CLIENT-TYPE: venom-router`.
- Every authenticated JSON endpoint answers with the envelope
  `{ "success": bool, "data": T | null, "error": string? }`.

## 2. Login (OAuth, extension flow — NO PKCE on the wire)

1. **Authorize URL**: `GET /api/v1/auth/authorize` with exactly
   `client_type=extension`, `callback_url=<redirect>`, `redirect_uri=<redirect>`,
   `state=<random>`. A PKCE verifier is generated to satisfy the framework
   shape but is never sent.
2. **Callback**: cline does NOT echo `state` back. The legacy callback page
   recovered the pending flow from localStorage (5-minute freshness window)
   and used a `__recovered__` sentinel. The current app solves the same
   problem with `OmitStateFromCallback() == true` + transaction id embedded
   in the callback URL path.
   The `code` may carry a URL fragment — only the pre-`#` part is exchanged.
   Some redirects place an already-minted base64 JSON token blob directly in
   `code` (`eyJ...`, `{"accessToken": ...}`, optionally with a trailing
   signature) — detected and used without a token exchange.
3. **Token exchange**: `POST /api/v1/auth/token`, JSON body
   `{grant_type:"authorization_code", code, client_type:"extension",
   redirect_uri, provider:"clinepass"}` (30s timeout).
   Response (enveloped): `data.accessToken`, `data.refreshToken`,
   `data.tokenType`, `data.expiresAt` (ISO string), `data.userInfo
   {subject, email, name, clineUserId, accounts[]}`.
   Missing `expiresAt` → assume now + 1h. `userInfo` is the primary identity
   source (email/name/clineUserId) and is persisted with the credential.

## 3. Staying connected (token refresh) — THE CADENCE THAT KEPT IT ALIVE

- **Refresh call**: `POST /api/v1/auth/refresh`, JSON
  `{refreshToken, grantType:"refresh_token"}` (camelCase). Same enveloped
  token response. A missing `refreshToken` in the response keeps the old one;
  missing `userInfo` keeps the stored one.
- **Refresh-before-use**: every adapter entry point (identity, quota, model
  test, chat) ran `refreshIfNeeded` first: refresh when
  `expires_at - now <= 5 minutes`.
- **Proactive hourly worker** (all OAuth providers): refresh when the token
  expires within 2 hours OR is older than 24 hours OR has no stored expiry.
  On success the rotated token is re-encrypted and persisted; degraded
  accounts that refresh successfully flip back to healthy.
- **Unrecoverable refresh errors** (`invalid_grant`, `invalid_request`,
  `invalid_token`, `refresh_token_expired`, `refresh_token_reused`,
  `refresh_token_invalidated`, `unauthorized` in the body) → typed auth error
  → account marked expired ("re-login required") + owner notification.
  Any other failure is transient: keep credentials, retry later.

## 4. Account info (email / plan / subscription)

- Email + name + clineUserId come from the **token response's userInfo**
  (not from `/users/me`, whose data may omit them).
- `GET /api/v1/users/me` → `data.id` (numeric user id — the key for the
  balance path), `data.organizations[{organizationId}]`. Also used as the
  health-check call (10s timeout).
- Plan label is the fixed string `ClinePass`; billing type **paid** (there is
  no free tier; classification is locked).

## 5. Quota / consumption

Fetched in parallel after `/users/me` (all enveloped, 10s timeouts, failures
return null → "no evidence", never fabricated):

- `GET /api/v1/users/{id}/balance` → `data.balance` in **micro-USD**
  (balance / 1,000,000 = USD; confirmed against cline's own dashboard).
- `GET /api/v1/users/me/plan/usage-limits` (note: `/users/me`, not
  `/users/{id}`) → `data.limits[{type: five_hour|weekly|monthly,
  percentUsed, resetsAt}]` — the same rolling windows cline's dashboard
  rate-limit widget shows. UI rows: `5H`, `7D`, `30D` with percent used and
  a humanized reset label.
- `GET /api/v1/users/{id}/usages` → `data.items[]` (usage history);
  `GET /api/v1/users/{id}/payments` → `data.paymentTransactions[]`.
- Organization variant: `GET /api/v1/organizations/{orgId}/balance`.
- Status derivation: balance null → `unknown`; ≤ 0 → `exhausted` (account
  degraded); < $5 → `low_balance`; else `available`.
- **Cadence**: quota refreshed every 15 minutes by a worker (with a 15-minute
  cooldown after a failure), plus on every health check via `syncAccount`.

## 6. Models (discovery)

- `GET /api/v1/ai/cline/recommended-models` — **public, no auth header**.
- Shape: three arrays `recommended[]`, `free[]`, `clinePass[]` of
  `{id, name}`. Merge by id with priority `clinePass > recommended > free`
  (the group is kept as `sourceGroup` metadata).
- Legacy inferred capabilities from id substrings — the current app
  deliberately refuses capability inference; only explicit facts (id, name,
  chat) are recorded.

## 7. Chat + model testing (proving a model ACTUALLY works)

- **Chat**: `POST /api/v1/chat/completions`
  `{model, messages, max_tokens (default 1024), temperature (default 0.7),
  stream:false}`.
  **The non-stream response is ENVELOPED**: `data.choices[0].message`,
  `data.usage.{prompt_tokens, completion_tokens}` — NOT a bare OpenAI body.
  Streaming uses standard OpenAI SSE chunks (generic parser worked).
- **Model test** (the "actually works" proof, not just HTTP 200):
  send `{model, max_tokens: 256, messages:[{role:"user", content:
  'Reply with exactly the word "ok" and nothing else.'}]}` and **validate the
  response text**: non-empty AND matches `/\bok\b/i` (reasoning fallback text
  is accepted). Empty body or missing "ok" ⇒ the model FAILED even on a 200.
- **Error classification** (from status + parsed `{error:{code,message}}`):
  - 401/403 + subscription wording → `quota_limited`;
    401/403 + token wording → `reauth_required`; else `auth_invalid`.
  - 429 or rate-limit wording → `rate_limit_error`.
  - 402 / insufficient / balance / credits wording → `quota_limited`.
  - 400/404 + "not available/found" → `model_not_available`; other invalid →
    `invalid_model`.
  - 5xx → `transient_provider_error`; anything else → `provider_error`.
  - Network errors (`fetch failed`, `ECONNREFUSED`, `ETIMEDOUT`) →
    transient, never a verdict.

## 8. Health-check orchestration (all providers, for context)

- Every 5 minutes: `syncAccount` per account (refresh-if-needed → `/users/me`
  → full quota) with concurrency 5; one 500ms retry on a transient failure;
  a state machine tolerates single failures (degrade only after consecutive
  failures), writes status transitions + quota snapshots + health history,
  sends degraded/expired notifications, and re-checks expired accounts every
  5 minutes so recovery is detected. Post-refresh (hourly) a second health +
  quota pass runs.
