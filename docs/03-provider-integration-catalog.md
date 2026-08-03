# 03 — Provider Integration Catalog

**This is the one asset carried over from the old build.** These wire contracts were read from
proven, working provider code; treat them as the reference for *how each provider is correctly
connected*. Everything else about the old repo is discarded.

Confidence tags below mean: **proven** (endpoint + behavior verified in old source),
**partial** (endpoints proven; identity/quota weak), **unknown** (not proven — treat as a
blocker, re-verify live before implementing). Client IDs shown are public/embedded constants;
**only `VENOM_ANTIGRAVITY_CLIENT_SECRET` is a real secret** and must come from env.

---

## 1. Adapter interfaces (the pattern)

Every provider is one typed definition plus a set of small, pure, network-only adapters. No
adapter touches the database; all persistence is done by the account/model services around them.
Dispatch is by typed capability, **never** by `switch` on the provider slug.

**OAuth and API key are credential mechanisms only.** The actual inference execution — sending
a chat request, streaming, handling tools — is modelled by a separate `InferenceTransport`
interface (defined in [01-architecture.md §4](01-architecture.md#4-the-execution-boundary)),
not by these credential adapters.

```go
// Identity of a connected account as the provider reports it.
type IdentityResult struct {
    ExternalID string            // immutable provider ID when available
    Email      string
    Plan       string
    Funding    string            // free | paid | unknown  (provider_evidence only)
    Evidence   map[string]any    // sanitized before storage
}

// HealthObservation is returned by the standalone HealthAdapter.
type HealthObservation struct {
    Status            string        // healthy | degraded | unreachable  (maps to account health_state: unreachable → unavailable)
    Scope             string        // account | offering  (offering-level health is provider-specific)
    CredentialValid   bool
    TransportReachable bool
    CheckedAt         int64
    ExpiresAt         *int64
    Failure           *TypedFailure // nil when healthy; populated from InferenceTransport taxonomy
    Evidence          map[string]any
}

type DiscoveredModel struct {
    ProviderModelID string
    DisplayName     string
    ContextLength   *int          // nil = unknown (never 0-as-unknown)
    MaxInputTokens  *int
    MaxOutputTokens *int
    Capabilities    []string      // only from explicit provider fields
    Pricing         map[string]any
    Evidence        map[string]any
}

// A provider may report SEVERAL concurrent quota windows at once (e.g. Claude Code's 5-hour AND
// 7-day usage; an RPM AND a TPM limit; a credit balance). Each window maps to one quota_windows
// row ([02 §3](02-domain-model.md)). Venom adds its own local-safety windows on top; an empty
// Windows slice means "no provider quota evidence" — never "unlimited".
type QuotaWindow struct {
    Unit            string   // requests | input_tokens | output_tokens | tokens | credits | balance | percent
    WindowType      string   // rolling_5h | rolling_7d | rpm | tpm | balance | provider window key
    WindowKey       string   // provider-native window identifier when supplied ("" otherwise).
                             // Venom ALWAYS normalizes before persistence to the canonical
                             // NOT-NULL key of [02 §3] ("provider:<id>", "rolling:<seconds>s",
                             // "local:<unit>") — quota_windows.window_key is never NULL/"".
    DurationSeconds *int      // nil for non-time-boxed windows (balance)
    Used, Remaining, Total *float64
    ResetAt         *int64
    Confidence      float64
    Evidence        map[string]any
}
type QuotaResult struct {
    Windows []QuotaWindow    // zero or more provider-evidence windows (source = provider_evidence)
}

// HealthAdapter is a standalone interface for checking account/offering health.
// It works with any account type (API key, OAuth, Custom) because health is
// independent of the credential mechanism.
type HealthAdapter interface {
    CheckAccountHealth(ctx, creds StoredCredentials) (HealthObservation, error)
    CheckOfferingHealth(ctx, creds StoredCredentials, providerModelID string) (HealthObservation, error)
}

// API-key providers: credential only, no health responsibility.
type APIKeyAdapter interface {
    ConnectAPIKey(ctx, key string) (IdentityResult, StoredCredentials, error) // validates authentically
}

// OAuth providers: credential only, no health responsibility.
type OAuthAdapter interface {
    BeginOAuth(ctx, redirectURI, state, pkceChallenge string) (authorizeURL string, err error)
    CompleteOAuth(ctx, code, pkceVerifier, redirectURI string) (IdentityResult, StoredCredentials, error)
    RefreshCredentials(ctx, creds) (StoredCredentials, error)
}

// Optional, dispatched by capability
type ModelDiscoveryAdapter interface { DiscoverModels(ctx, creds) ([]DiscoveredModel, error) }
type QuotaAdapter          interface { FetchQuota(ctx, creds) (QuotaResult, error) }
type IdentityAdapter       interface { FetchIdentity(ctx, creds) (IdentityResult, error) }
```

Every built-in provider registers exactly **one** primary auth adapter — an `APIKeyAdapter`
**or** an `OAuthAdapter`, never both, and optionally a `HealthAdapter`. A **Custom OpenAI-Compatible**
provider is a generic
`APIKeyAdapter` + `ModelDiscoveryAdapter` parameterized by the owner-supplied base URL, headers,
and optional model list.

**Authentic validation rule:** connecting an API key must prove the key works, not just that the
host is up. Some `/v1/models` endpoints return 200 for any/no token — so validate with a
zero-cost `POST /v1/chat/completions` probe (e.g. `max_tokens: 1`) and treat only a genuine
auth error as invalid; 429/5xx = provider-unavailable (retryable), **not** invalid key.

---

## 2. Enrollment flows

### 2a. OAuth (built-in)
1. **Begin** — generate PKCE (`state`, `verifier`, S256 `challenge`); persist a pending
   `oauth_transaction` storing `sha256(state)`, the `provider` slug, an originating
   `transaction_id` (used by the caller for polling), and the **encrypted** verifier, with a
   10-minute expiry. No session cookie reference is stored — the callback uses state-based
   lookup, not session binding. Adapter builds the authorize URL.
2. **Browser** — navigate the owner to the authorize URL (`_self`).
3. **Callback** — the redirect target depends on the provider's registered client:
   - **Hosted-code providers** (claude-code): the client's ONLY registered redirect_uri is
     Anthropic's hosted page (`https://platform.claude.com/oauth/code/callback`); the browser
     NEVER returns to Venom. The owner copies the code the page displays (format
     `<auth_code>#<fragment>`, where the fragment echoes the `state`) and pastes it into the
     dashboard, which submits it to `POST /api/control/v1/oauth/complete` (owner-session +
     CSRF gated; transaction resolved by id).
   - **Redirect-back providers** (clinepass, antigravity): `GET /callback?code&state` on the main
     bind — the registered `{origin}/callback` shape. The provider is resolved from the
     transaction's stored slug (or from the legacy provider-specific path
     `GET /api/control/v1/oauth/{provider}/callback`), never from an unvalidated client value.
   - **Fixed-redirect providers** (codex, xAI): the fixed-port listener (see [01 §6](01-architecture.md#6-http-surfaces)).
   The callback **never depends on a session cookie** — it uses the state hash (or the
   unguessable transaction id for state-omitting providers) to locate the transaction, executes
   the exchange, and stores the result for the originating caller to retrieve via polling
   `GET /api/control/v1/oauth/{transaction_id}/status`.
4. **Complete (one transaction)** — look up the pending tx by `sha256(state)`; constant-time
   verify provider; check not expired; decrypt the verifier; mark consumed and
   null the verifier fields; commit. Only *after* commit exchange the code for tokens, then
   `FetchIdentity`. Any mismatch → invalid, row unchanged (replay-safe). **The auth code is
   never stored; the `state` nonce is always verified** (the old build's #1 OAuth bug was
   skipping this).
5. **Persist** — one transaction inserts the account + encrypted credential + first funding
   evidence, or handles a same-identity reconnection as **reauthentication** (see §2e below).
   An active duplicate with a **different** external_id → friendly `account_already_connected`,
   not a raw 409.
6. **Sync** — best-effort health + discovery + quota after connect.
7. **Polling endpoint** — `GET /api/control/v1/oauth/{transaction_id}/status` returns
   `{ status: "pending" | "completed" | "failed" | "expired", account_id?, error? }`.
   The originating control UI polls this until completion or timeout.

> **Multi-account note — `prompt=login`.** When connecting a *second* account of the same
> Auth0-backed provider (notably `codex`), add `prompt=login` to the authorize URL to force fresh
> authentication. Without it the provider's Auth0 session silently reuses the first login, and the
> new consent can invalidate the first account's refresh-token family — which would break Venom's
> multi-account-per-provider model. Apply to `codex` and any Auth0-style provider. (Adopted from the
> OmniRoute analysis, item E.)

### 2b. API key (built-in)
1. Owner pastes the key and optionally classifies the account **free / paid / unknown**
   (default: inherit the provider policy).
2. `NormalizeAPIKey` (trim/collapse), then `ConnectAPIKey` validates authentically (see rule
   above). external_id = provider immutable ID if the provider has an identity endpoint, else
   the key fingerprint.
3. Persist account + credential + funding (owner override recorded if the owner picked one).
   No account/credential is created before successful validation.

### 2c. Custom OpenAI-Compatible
1. Owner supplies: base URL, API key, optional extra **header declarations** (name only, without
   values — see header security rule below), optional explicit model list, and the account funding
   (free/paid).
2. **Header security rule:** Custom header values are **secrets by default**. In the enrollment form:
   - `name` is stored in `settings_json` (non-secret).
   - `value` is stored inside the **encrypted credential envelope** alongside the API key, keyed by
     a reference like `header:{name}`.
   - Runtime reconstructs headers in memory only: read `settings_json` for names, decrypt
     credential envelope for values, assemble, and use. Values are **never** stored in
     `settings_json` or any plaintext field.
   - The owner may explicitly mark a header value as `public` (e.g. `Content-Type:
     application/json`) if it genuinely contains no secret — but `public` is opt-in, never the
     default. The UI masks all values by default; only explicitly `public` values are shown.
   - Header names that commonly carry secrets: `Authorization`, `Proxy-Authorization`, `Cookie`,
     `x-api-key`, `X-API-Key`, `api-key`, and any owner-defined name.
3. Validate with the zero-cost chat probe against the base URL; discover models via
   `GET {base}/v1/models` unless an explicit list was given.
4. Persist like an API-key account; the provider record is owner-defined (`auth_mode =
   custom_openai`). Header values live in the credential envelope, never in `settings_json`.

### 2d. Identity, health, quota refresh
On-demand (connect-time, per-account "refresh", or provider "sync all"). For OAuth, refresh the
token first (rotating refresh tokens where the provider issues single-use ones), then fetch
identity + health + quota. Health maps to `healthy` / `degraded` with only safe error strings
persisted. A background scheduler is **out of scope** for v1 — refresh is explicit.

### 2e. OAuth reauthentication (same-identity reconnect)

When a user reconnects an OAuth account whose `external_id` matches an existing active account,
Venom must treat this as **reauthentication**, not a duplicate error — the new credential may be
valid while the old one is expired or invalidated.

**Flow:**

1. **Complete** the OAuth exchange (steps 1–4 of §2a) to obtain a new credential and identity.
2. **Match** — after `FetchIdentity`, search for an existing account by `(provider, external_id)`.
3. **If found and active → reauthentication path:**
   a. **Stage** the new credential — encrypt it and insert a **new credential row with
      `state = 'staged'`** (the active row is untouched; `idx_cred_active_per_kind` permits a staged
      row alongside the active one of the same kind). **At most one staged credential per
      `(account_id, kind)`** is allowed ([02 §3](02-domain-model.md#credential-encrypted-secret-for-an-account),
      enforced by `idx_cred_staged_per_kind`): if a staged row for this account+kind already exists,
      the reauth is rejected with `reauthentication_in_progress` rather than creating a second one.
      Set the account's `reauth_in_progress = 1`. **Do not** deactivate the old credential yet.
   b. **Validate the staged credential** — make a lightweight probe (e.g. a zero-cost identity or
      health check) to confirm the new credential works as expected. If the probe fails, the
      staged credential is discarded and the old one is preserved.
   c. **Atomic swap** — inside a single transaction:
      - Mark the old credential as `retired` (stamp `retired_at`).
      - Activate the new credential (`state = 'staged' → 'active'`).
      - Set the account's `health_state` to `healthy` and clear `reauth_in_progress` (unless the
        validation probe indicated otherwise); `connection_state` stays `connected`.
      - Record a `reauthentication` audit event.
   d. **Best-effort revoke** — after the transaction commits, attempt to revoke the old credential
      at the provider (best-effort — never block on failure). If revoking also invalidates the
      provider's refresh token family, the old credential is already retired in Venom's DB, so no
      session is orphaned.
   e. **Rollback on failure** — if step 3b (validation) or step 3c (transaction) fails, the old
      credential remains active and the staged credential is discarded. The caller receives the
      error and the existing account is untouched.
4. **If found but disconnected** → reactivate (same as the current §2a Step 5 behavior):
   replace credentials and update state to `connected`.
5. **If not found** → new account (standard insert path).
6. **Concurrency guard** — a concurrent reauthentication for the same `(provider, external_id)`
   is prevented by an **idempotency key** (the OAuth `state` is single-use) or an advisory
   lock. A second reconnect attempt for the same account while one is in-flight receives a
   friendly `reauthentication_in_progress` response.
7. **Identity mismatch guard** — if the OAuth exchange returns a **different** `external_id`
   from the matching account's current one, the new credential is **not** substituted.
   The caller receives `account_identity_mismatch` — the old account and its credential remain
   intact. Manual intervention (disconnect old, connect new as separate account) is required.

---

## 3. The 11 integrations

> In each entry, **Funding** is the provider's *default* funding policy for a *new* account —
> funding is always per-account, never per-provider. **Stable external ID** is the immutable
> identity used to dedup accounts.
>
> **Funding default → evidence `source` mapping** (canonical vocabulary, see
> [02 §2](02-domain-model.md#2-free--paid--the-semantics-precisely)): "paid (locked)" ⇒
> `provider_policy` with `funding_locked = 1`; "provider evidence" ⇒ `provider_evidence`;
> "free (owner policy)" ⇒ `owner_policy`; "unknown" ⇒ **`funding_policy.mode = evidence_required`**:
> the first evidence row is stamped `funding = unknown`, `source = provider_policy` (not locked —
> it records that the catalog cannot classify the account, never fabricated provider evidence).
> Such accounts are excluded from production routing (and never Lite-eligible) until authenticated
> provider evidence or an allowed owner override establishes `free`/`paid` ([02 §2](02-domain-model.md#2-free--paid--the-semantics-precisely)).

### Built-in — API Key

#### `opencode-zen` — OpenCode Zen — **proven**
- Base `https://opencode.ai/zen` (endpoints under `/v1`).
- Identity: none → **key fingerprint**; synthetic `plan: "Free"`.
- Health: `GET /v1/models` (Bearer) or a free-model `POST /v1/chat/completions` probe.
- Discovery: `GET /v1/models` **intersected with `https://models.dev/api.json`** to keep only
  zero-cost models (`cost.input == 0 && cost.output == 0 && status != "deprecated"`, cached
  ~10 min). This is the reference pattern for "a free account must never surface paid models."
- Quota: none proven. Funding: free (owner policy). Multi-account: by fingerprint.

#### `agnes-ai` — Agnes AI — **proven (identity partial)**
- Base `https://apihub.agnes-ai.com/v1`. Identity: none → fingerprint, synthetic `Free`.
- Health/Discovery: `GET /v1/models` (Bearer); drop video models.
- Quota: none proven. Funding: **unknown** (`evidence_required` — do not assume). Multi-account: by fingerprint.

#### `gemini-cli` — Gemini CLI — **proven (Google schema, not OpenAI)**
- Base `https://generativelanguage.googleapis.com`. Auth header **`x-goog-api-key`** (not Bearer).
- Health: `GET /v1beta/models?pageSize=1`. Discovery: `GET /v1beta/models?pageSize=200`
  (paginate `nextPageToken`; filter TTS/image/live/audio).
- Identity: none → fingerprint; provider returns a synthetic plan label `Free`. Quota: none proven.
  Funding: **unknown** (`evidence_required` — a "Free" label is a display string, not funding evidence).
- Note: Google model schema — needs a Google→OpenAI capability normalizer.

#### `ollama-cloud` — Ollama Cloud — **proven (immutable ID)**
- Base `https://ollama.com/v1` (OpenAI-compat); identity via native `/api`.
- Identity: `POST https://ollama.com/api/me` (Bearer) → `ID`, `Email`, `Name`, `Plan`,
  `WorkOSUserID`. **Stable external ID = `account.ID`.**
- Health/Discovery: `GET /v1/models` (Bearer). Quota: dashboard-only (no live API).
- Funding: free tier (from `/api/me`). Multi-account: yes.

#### `nvidia-nim` — NVIDIA NIM — **proven (identity partial)**
- Base `https://integrate.api.nvidia.com/v1`. Identity: none → fingerprint, synthetic `Free`.
- Health/Discovery: `GET /v1/models` (Bearer), normalized (no static list).
- Quota: no documented API. Funding: unknown (`evidence_required`).

### Built-in — OAuth

#### `antigravity` — Antigravity — **proven (needs client secret env)**
- OAuth2 + PKCE (S256), **confidential client**. Base `https://cloudcode-pa.googleapis.com`.
- Authorize `https://accounts.google.com/o/oauth2/v2/auth`; Token `https://oauth2.googleapis.com/token`.
- Identity: `GET https://www.googleapis.com/oauth2/v2/userinfo` + `POST /v1internal:loadCodeAssist`
  (→ `cloudaicompanionProject` = **project_id**, needed for discovery; `currentTier` → plan).
- Discovery: `POST /v1internal:fetchAvailableModels` with `{ "project": <project_id> }`.
- Refresh: `POST https://oauth2.googleapis.com/token` (needs client secret).
- Scopes: cloud-platform, userinfo.email, userinfo.profile, cclog, experimentsandconfigs.
- Client ID `1071006060591-tmhssin2h21lcre235vtolojh4g403ep.apps.googleusercontent.com` (public).
  **Client secret REQUIRED from env** `VENOM_ANTIGRAVITY_CLIENT_SECRET` (+ `..._CLIENT_ID`); when
  unset → **Setup required**, `configured=false`, list missing var names.
- Funding: provider evidence — map exact plan strings only (`"Free"→free 0.95`, `"Pro"→paid
  0.95`, else `unknown`); `tier_id` is evidence, never a decision. Quota: `fetchAvailableModels`
  remaining fractions + prompt credits (partial). Stable ID: email + project_id (no immutable
  `sub` surfaced — a known weakness).

#### `claude-code` — Claude Code — **proven**
- OAuth (PKCE, JSON token exchange). Base `https://api.anthropic.com`.
- Authorize `https://claude.ai/oauth/authorize`; Token `https://platform.claude.com/v1/oauth/token`.
- Identity: `GET /api/oauth/profile` → `account.uuid` (**stable external ID**), `account.email`,
  `organization.uuid`.
- Discovery: `GET /v1/models` with extended `anthropic-beta` + `X-App: cli` headers.
- Refresh: `POST https://platform.claude.com/v1/oauth/token`.
- Scopes `org:create_api_key user:profile user:inference`. Client ID
  `9d1c250a-e61b-44d9-88ed-5944d1962f5e` (public).
- Quota: usage 5h / 7d windows. Funding: provider evidence (Pro/Max/Team/Enterprise/Free).
- **Must send Claude-Code identity/beta headers or the API returns 429.**

#### `codex` — Codex (OpenAI) — **proven**
- OAuth (PKCE). Base `https://chatgpt.com/backend-api/codex/responses` (streaming responses).
- Authorize `https://auth.openai.com/oauth/authorize`; Token `https://auth.openai.com/oauth/token`.
- **Fixed redirect URI `http://localhost:1455/auth/callback`** (OpenAI-registered — must match).
- Identity: `GET https://auth.openai.com/oauth/userinfo` + JWT claims. **Stable external ID =
  `chatgpt_account_id`** (fallback `organizations[0].id`, last resort `sub`).
- Discovery/Quota: `GET https://chatgpt.com/backend-api/wham/usage` (model ids + usage from the
  payload) + rate-limit headers. Refresh: `POST https://auth.openai.com/oauth/token`.
- **Required headers: `ChatGPT-Account-Id`, `originator=codex_cli_rs`, `User-Agent:
  codex_cli_rs/...`.** Scopes `openid profile email offline_access`. Client ID
  `app_EMoamEEZ73f0CkXaXp7hrann` (public). Funding: provider evidence (`plan_type`).

#### `github-copilot` — GitHub Copilot — **proven**
- OAuth (web flow + PKCE) **+ two-token exchange** (GitHub token → Copilot token).
- Base `https://api.githubcopilot.com`. Authorize `https://github.com/login/oauth/authorize`;
  Token `https://github.com/login/oauth/access_token`.
- Identity: `GET https://api.github.com/user` → `id` (**stable external ID**), `login`,
  `plan.name`.
- Discovery: `GET https://models.github.ai/catalog/models` (`X-GitHub-Api-Version: 2026-03-10`).
- Refresh: GitHub token via `POST .../access_token`; **Copilot token via `GET
  https://api.github.com/copilot_internal/v2/token`** (two-token lifecycle).
- Quota: `GET https://api.github.com/copilot_internal/user` (undocumented, best-effort).
- Scopes `read:user`. Client ID `Iv1.b507a08c87ecfe98` (public). Funding: provider evidence.

#### `clinepass` — ClinePass — **proven** (was metadata-only; implement as OAuth)
- OAuth extension flow; **PKCE verifier generated but not sent on the wire**. Base
  `https://api.cline.bot`.
- Authorize `https://api.cline.bot/api/v1/auth/authorize` (`client_type=extension`,
  `callback_url`, `redirect_uri`, `state`); Token `.../api/v1/auth/token`
  (`grant_type=authorization_code`, `client_type=extension`, `provider=clinepass`).
- Identity: token `userInfo` (`subject`, `email`, `clineUserId`) + `GET /api/v1/users/me`.
  **Stable external ID = `clineUserId` / `userInfo.subject`.**
- Discovery: `GET /api/v1/ai/cline/recommended-models` (groups: clinePass / recommended / free).
- Refresh: `POST /api/v1/auth/refresh` (`{refreshToken, grantType:"refresh_token"}`).
- **Auth header quirk: token prefixed `workos:`; extra headers `HTTP-Referer:
  https://cline.bot`, `X-Title: Cline`, `X-CLIENT-TYPE: venom-router`.**
- Quota: `/api/v1/users/{id}/balance`, `/usages`, `/api/v1/users/me/plan/usage-limits`.
- Funding: **paid**, and **locked** (USD balance/credits) — override rejected.

#### `xai` — xAI (Grok) — **proven** (Grok Build OAuth)
- OAuth2 + PKCE (S256). Model base `https://api.x.ai/v1`.
- Authorize `https://auth.x.ai/oauth2/authorize` (OIDC-discovered, static fallback); Token
  `https://auth.x.ai/oauth2/token`; **fixed redirect `http://127.0.0.1:56121/callback`**.
- Scopes `openid profile email offline_access grok-cli:access api:access`. Client ID
  `b1a00492-073a-47ea-816f-4c329264a828` (public).
- Identity: id_token JWT (`sub` = **stable external ID**, email) + billing
  `https://cli-chat-proxy.grok.com/v1/billing?format=credits`.
- Discovery: `GET /v1/language-models` (aliases + pricing), fallback `GET /v1/models` (Bearer),
  tagged `live_oauth_catalog`.
- Refresh: `POST https://auth.x.ai/oauth2/token` (**single-use refresh tokens** — persist the
  rotated token atomically).
- Quota: billing credits. Funding: provider evidence + paid credits.

### Custom — OpenAI-Compatible
Not a fixed provider — the generic path (see §2c). Base URL + key (+ headers, + optional model
list) supplied by the owner; validate via the chat probe; discover via `GET {base}/v1/models`;
funding set per account. This is how anything OpenAI-shaped gets added with no code change.

---

## 4. Summary table

| slug | family | auth mode | stable external ID | discovery | quota | funding default |
|---|---|---|---|---|---|---|
| opencode-zen | Built-in | API key | fingerprint | `/v1/models` ∩ models.dev (free only) | — | free (policy) |
| agnes-ai | Built-in | API key | fingerprint | `/v1/models` | — | unknown (`evidence_required`) |
| gemini-cli | Built-in | API key (`x-goog-api-key`) | fingerprint | `/v1beta/models` | — | unknown (`evidence_required`) |
| ollama-cloud | Built-in | API key | `account.ID` | `/v1/models` | dashboard-only | free (evidence) |
| nvidia-nim | Built-in | API key | fingerprint | `/v1/models` | — | unknown (`evidence_required`) |
| antigravity | Built-in | OAuth2 (confidential) | email+project_id | `:fetchAvailableModels` | remaining fractions | provider evidence |
| claude-code | Built-in | OAuth2 (PKCE) | `account.uuid` | `/v1/models` (+beta hdrs) | 5h/7d usage | provider evidence |
| codex | Built-in | OAuth2 (fixed redirect) | `chatgpt_account_id` | `/wham/usage` | wham/usage + headers | provider evidence |
| github-copilot | Built-in | OAuth2 (two-token) | `user.id` | `models.github.ai/catalog` | copilot_internal (best-effort) | provider evidence |
| clinepass | Built-in | OAuth (workos: prefix) | `clineUserId` | `/recommended-models` | balance/usages/limits | **paid (locked)** |
| xai | Built-in | OAuth2 (PKCE, Grok Build) | JWT `sub` | `/v1/language-models` | billing/credits | provider evidence |
| *(custom)* | Custom | OpenAI-Compatible | fingerprint | `{base}/v1/models` | — | per account |

"All Integrations" = these 11 built-in providers plus the custom OpenAI-compatible path.
**Active Providers** = those with at least one connected account.

---

## 5. Re-verification checklist (before implementing any adapter)

The contracts above are proven-from-source, but providers drift. For each adapter, confirm live:
the authorize/token URLs and scopes still match; the identity endpoint still returns the immutable
ID field named here; the discovery endpoint shape; any required headers (Claude-Code beta,
Codex account-id, ClinePass `workos:`); the funding signal; and whether a real quota endpoint now
exists (several are `—`/best-effort today). Record each confirmation as evidence with a date.
Never re-introduce a hardcoded model list during this work.

### 5.1 Verification tiers (what is a CI gate vs. what is manual evidence)

The confidence tags in this doc (**proven / partial / unknown**) are *planning* confidence, not
test status. Each adapter carries four distinct verification activities, and they map to the
roadmap and CI differently (see [08 §5](08-engineering-standards.md#5-testing-strategy) and
[10-requirements-coverage](10-requirements-coverage.md)):

| Activity | What it proves | When | CI-blocking? |
|---|---|---|---|
| **Planning contract** | The documented wire shape in this doc. | Now (this doc) | n/a |
| **Fixture-based contract test** | The adapter parses recorded identity/discovery/quota/refresh fixtures correctly and enforces the authentic-validation rule. | Per adapter, Phase 2b/7 | **Yes** — deterministic, offline, no network. |
| **Live re-verification** | The provider's *current* endpoints/scopes/headers still match this doc. | Before implementing each adapter (Phase 7) | **No** — a manual step recorded as dated evidence; never a universal CI gate. |
| **Real-account validation** | A real connected account discovers models and certifies ≥ chat. | Phase 7 gate | **No** as a blocking unit gate — opt-in / operator-run, gated behind credentials; results recorded as evidence. |

**Rule:** a test that requires a live provider or a real credential must **never** become a flaky
universal CI gate. CI blocks only on the offline fixture/contract tests; live checks are recorded
evidence attached to the corresponding roadmap phase.
