# 09 — Control API Contracts (planning-level)

The owner/control-plane API contracts. **Planning-level and language-neutral** — this specifies
routes, auth, schemas, errors, idempotency, concurrency, async/polling, redaction, and audit so the
dashboard and services can be built against a stable contract. It does **not** contain handler code.

Read [01-architecture §6a](01-architecture.md#6a-control-plane-owner-ui--control-api) for the
network/security posture and [02-domain-model](02-domain-model.md) for the entities referenced here.

All routes are under the base `**/api/control/v1**` and are served **only** on the loopback control
plane. The public inference API (`/v1/*`) is out of scope for this doc (it is OpenAI-compatible; see
[01 §6b](01-architecture.md#6b-data-plane-public-inference-api) and [05 §5](05-tier-engine.md#5-errors-stable-envelope)).

---

## 1. Conventions (apply to every endpoint)

- **Transport:** JSON over HTTP; loopback-only; requests carry the session cookie **and** a
  session-bound CSRF token for all non-`GET` methods. The socket-level loopback + Host-allowlist
  gate (01 §6a) runs **before** session/CSRF; passing it does **not** authenticate the request.
- **Auth/authorization:** single owner; an authenticated owner session is required for every
  endpoint except the unauthenticated first-run/login handshake (`/auth/*`), the transaction-based
  OAuth callback/status, and the liveness `/health`. There are no roles. The full model — first-run
  setup, login/logout, session lifecycle, CSRF, re-verification, recovery — is **§5**. Sensitive
  operations (credential reveal, etc.) require a **fresh password re-verification** (§5.5) whose
  5-minute freshness window is checked server-side.
- **Success envelope:** `{ "data": <resource|list>, "meta"?: { … } }`. Lists carry
  `meta.page`/`meta.next_cursor` where paginated.
- **Error envelope (shared with the whole system):**
  `{ "error": { "code": string, "message": string, "request_id": string, "retryable": bool, "details"?: object } }`.
  `message` is user-safe; **never** a raw provider error, credential, or identifier that is a secret.
  Standard codes include `validation_error` (400), `unauthorized` (401), `csrf_failed` (403),
  `not_loopback` (403), `not_found` (404), `conflict` (409), `precondition_failed` (412),
  `reauthentication_in_progress` (409), `account_identity_mismatch` (409), `funding_locked` (409),
  `rate_limited` (429), `internal` (500).
- **Idempotency:** all mutating POSTs accept an `Idempotency-Key` header; replays return the original
  result. Sync/refresh actions are naturally idempotent (they converge on provider truth).
- **Optimistic concurrency:** resources that can be edited concurrently (funding, settings, keys)
  carry a `version` (or `updated_at` ETag); writes send `If-Match`/expected `version` and get `412`
  on mismatch (mirrors the quota conditional-update discipline in [02 §3](02-domain-model.md)).
- **Pagination/filtering:** list endpoints accept `?limit=&cursor=&q=&filter[...]`; cursor-based.
- **Secret redaction:** requests may *submit* secrets (API keys, header values) but responses
  **never** echo them; stored secrets are hash/fingerprint only; the `sanitize` boundary applies to
  every field, log, and audit row. OAuth `code`/`state`/PKCE verifiers and `Authorization` headers
  are never returned or logged.
- **Async jobs — one canonical shared surface.** Long-running mutations (discovery, probing,
  benchmark, backup, restore) return `202 Accepted` with `{ job_id, status_url }` where
  `status_url = /api/control/v1/jobs/{job_id}`, and are polled at that **single** shared endpoint
  `GET /jobs/{job_id}`. There are **no** competing per-resource status endpoints. The **sole
  exception** is the OAuth transaction, whose distinct transaction lifecycle keeps its dedicated
  `GET /oauth/{transaction_id}/status` (a transaction, not a job). Job semantics are specified in
  **§3.12**.
- **Audit:** every mutating call and every credential reveal emits an `audit_event` (ids/codes/
  timestamps only — never secrets or prompt content).

---

## 2. Endpoint catalog

| Area | Method & route | Purpose |
|---|---|---|
| Auth | `GET /auth/status` | Unauthenticated: is setup complete? is there a live session? (see §5) |
| Auth | `POST /auth/setup` | First-run: create the single owner password (rejected once set). |
| Auth | `POST /auth/login` | Authenticate the password → create an owner session (sets cookie + CSRF). |
| Auth | `POST /auth/logout` | Revoke the current session. |
| Auth | `POST /auth/reverify` | Re-verify the password → stamp the 5-minute freshness window. |
| Auth | `GET /auth/session` | Current session metadata (expiries, freshness) — authenticated. |
| Jobs | `GET /jobs/{job_id}` | **Canonical shared async-job status** (discovery/probe/benchmark/backup/restore). |
| Providers | `GET /providers` | List the integration catalog (all 11 built-ins + custom path), with derived capabilities and `configured` flag. |
| Providers | `GET /providers/{id}` | One provider definition + aggregate health + account count. |
| Enrollment (API key) | `POST /providers/{id}/accounts` | Create an API-key account (authentic validation). |
| Enrollment (custom) | `POST /providers/custom/accounts` | Create a Custom OpenAI-Compatible account. |
| Enrollment (OAuth) | `POST /providers/{id}/oauth/begin` | Begin an OAuth transaction (PKCE); returns authorize URL + `transaction_id`. |
| Enrollment (OAuth) | `GET /oauth/{provider}/callback` | OAuth redirect target (or fixed-port listener). |
| Enrollment (OAuth) | `GET /oauth/{transaction_id}/status` | Poll an OAuth transaction. |
| Reauthentication | `POST /accounts/{id}/reauth/begin` | Begin same-identity OAuth reauth (staged credential). |
| Accounts | `GET /accounts` / `GET /accounts/{id}` | List / read accounts (multi-axis status projection). |
| Accounts | `POST /accounts/{id}/reveal` | Reveal the credential once (fresh re-verification; `no-store`). |
| Accounts | `PUT /accounts/{id}/funding` | Owner funding override (rejected if `funding_locked`). |
| Accounts | `POST /accounts/{id}/stop` / `/resume` | Owner disable/enable (connection axis). |
| Accounts | `DELETE /accounts/{id}` | **Soft disconnect (the only V1 delete):** stop routing through the account; revoke/retire usable credentials where possible; **retain** sanitized operational history + audit records; mark `disconnected` (restorable only via a new enrollment flow). Hard delete/purge is outside V1 scope. |
| Discovery | `POST /accounts/{id}/discover` | Run account-scoped model discovery (async). |
| Health | `POST /accounts/{id}/health` / `POST /providers/{id}/sync` | Refresh health (+ discovery/quota) for an account / all provider accounts. |
| Quota | `POST /accounts/{id}/quota` | Refresh quota snapshot. |
| Models | `GET /models` / `GET /offerings` | Effective-offering read model (shared projection). |
| Probes | `POST /offerings/{id}/probe` | Run capability/context probes (async, quota-protected). |
| Certification | `GET /offerings/{id}/certification` | Certification state + capability truth + review reason. |
| Benchmark | `POST /models/{id}/benchmark` | Run/record a canonical quality rating (async, owner-enabled). |
| Enrichment | `PUT /settings/enrichment` | Toggle optional metadata enrichment (off by default). |
| Diagnostics | `GET /diagnostics/routes` / `GET /diagnostics/routes/{request_id}` | Route-decision & attempt records ("why this route?"). |
| Diagnostics | `GET /diagnostics/reconciliation` | `reconciliation_pending` / `unknown_consumption` reservations + manual re-sync ([02 §3](02-domain-model.md#quota--the-multi-window-model-budgetssources--windows--reservations--allocations) state machine). |
| Keys | `POST /keys` / `GET /keys` / `DELETE /keys/{id}` | Venom API keys (raw shown once; hash-only stored). |
| Settings | `GET /settings` / `PUT /settings` | Owner settings (theme/density, staleness windows, probe caps, binds). |
| Backup | `POST /backup` → `202 {job_id, status_url}` | Create a portable encrypted backup (async; poll `GET /jobs/{job_id}`). |
| Restore | `POST /restore` → `202 {job_id, status_url}` | Restore from an encrypted container (async; poll `GET /jobs/{job_id}`). |
| Health | `GET /health` | **Process liveness — unauthenticated, outside `/api/control/v1`** (see [01 §6d](01-architecture.md#6d-health-endpoints-the-final-single-choice)); listed here for completeness only. |

---

## 3. Detailed contracts (the ones with real nuance)

### 3.1 `POST /providers/{id}/accounts` — API-key enrollment
- **Request:** `{ "api_key": string, "display_name"?: string, "funding"?: "free"|"paid"|"unknown" }`
  (`funding` defaults to the provider policy; recorded as `owner_override` when supplied).
- **Behavior:** `NormalizeAPIKey` → `ConnectAPIKey` **authentic** validation (zero-cost chat probe;
  429/5xx = provider-unavailable, **not** invalid). No account/credential is created before success.
- **Success (201):** `{ data: Account }` (multi-axis status, identity, funding evidence row).
- **Errors:** `validation_error`, `invalid_api_key` (401 from provider), `conflict`
  (`account_already_connected` when a different `external_id` already exists), `502` provider
  unavailable (retryable).
- **Idempotency:** `Idempotency-Key`; the `(provider_id, fingerprint)` dedup makes re-submits safe.

### 3.2 `POST /providers/custom/accounts` — Custom OpenAI-Compatible
- **Request:** `{ base_url, api_key, headers?: [{name, public?: bool}], model_list?: string[], funding }`.
  **Header values are submitted separately and stored encrypted** (never in `settings_json`); only
  names (and any explicitly `public` values) are non-secret (see [03 §2c](03-provider-integration-catalog.md#2c-custom-openai-compatible)).
- **Behavior:** validate via the zero-cost chat probe against `base_url`; discover via
  `{base}/v1/models` unless `model_list` given.
- **Success (201):** `{ data: Account }` with `auth_mode = custom_openai`.

### 3.3 OAuth enrollment — begin / callback / status
- **`POST /providers/{id}/oauth/begin`** → generate PKCE (`state`, `verifier`, S256 `challenge`);
  persist a pending `oauth_transaction` storing `sha256(state)`, provider slug, `transaction_id`,
  **encrypted** verifier, 10-min expiry. **Response (202):**
  `{ data: { transaction_id, authorize_url, expires_at } }`. No session reference is stored on the
  transaction (callback uses state-based lookup). The `redirect_uri` handed to the adapter is the
  registered, provider-agnostic **`http://<control-bind>/callback`** — the shape every
  non-fixed-redirect provider's public client allows (claude-code, clinepass, antigravity; verified
  against the legacy implementation, 2026-08-03). For fixed-redirect providers (Codex, xAI) the
  authorize URL targets the fixed-port loopback listener; multi-account Auth0 providers add
  `prompt=login`.
- **`GET /callback?code&state`** (redirect target; the legacy provider-specific
  `GET /oauth/{provider}/callback` also works and supplies the provider directly) → look up tx by
  `sha256(state)`; resolve the provider from the transaction row (the path carries none);
  constant-time verify provider; check not expired; decrypt verifier; **mark consumed and null the
  verifier**; commit; *then* exchange code for tokens and `FetchIdentity`. The auth `code` is
  **never stored**; the `state` nonce is **always** verified. Persists account + encrypted
  credential + first funding evidence, or routes to reauthentication (§3.4). Response is a thin
  page/redirect; the originating UI learns the result via the status endpoint.
- **`GET /oauth/{transaction_id}/status`** →
  `{ data: { status: "pending"|"completed"|"failed"|"expired", account_id?, error? } }`.

### 3.4 `POST /accounts/{id}/reauth/begin` — same-identity reconnect
- Runs the OAuth begin/callback, then the **staging** flow of [03 §2e](03-provider-integration-catalog.md#2e-oauth-reauthentication-same-identity-reconnect):
  stage new credential (`state='staged'`, set `reauth_in_progress=1`) → validate → **atomic swap**
  (old `active→retired`, staged `staged→active`, `health_state→healthy`, clear `reauth_in_progress`)
  → best-effort revoke old.
- **Guards:** concurrent reauth for the same `(provider, external_id)` → `reauthentication_in_progress`
  (409); an OAuth exchange returning a **different** `external_id` → `account_identity_mismatch`
  (409), old credential untouched.

### 3.5 `POST /accounts/{id}/reveal` — credential reveal
- Requires a **fresh owner re-verification** (§5.5): the session's `reverify_fresh_until` must be in
  the future (5-minute window); otherwise `reverification_required` (401) and the UI prompts for the
  password. Response carries `Cache-Control: no-store`; returns the decrypted secret **once**; emits
  an audit event. The UI clears it on hide/blur. Rate-limited.

### 3.6 `PUT /accounts/{id}/funding` — funding override
- **Request:** `{ funding: "free"|"paid"|"unknown", reason?: string, expected_version: int }`.
- **Behavior:** appends an `owner_override` evidence row (supersedes the prior current row) unless
  the current row is a **locked** `provider_policy` → `funding_locked` (409). Optimistic concurrency
  via `expected_version`.

### 3.7 `POST /accounts/{id}/discover` — discovery (async)
- **202** `{ data: { job_id, status_url } }`; poll the **canonical shared** `GET /jobs/{job_id}`
  (§3.12) — not a per-resource endpoint. Applies the atomic-snapshot, generation-guarded discovery
  of [04 §1](04-model-intelligence.md);
  an explicit empty list withdraws offerings; a malformed/truncated response keeps last-known-good.
  Free accounts get the routing-critical free-safety filter ([04 §2b](04-model-intelligence.md#2b-free-safety-resolution-vs-metadata-enrichment-two-separate-pipelines)).

### 3.8 `POST /offerings/{id}/probe` — capability/context probes (async, quota-protected)
- **Request:** `{ operations?: ["context_window","tools","structured_output","vision"], force?: bool }`.
- **Behavior:** enforces per-provider concurrency (max 1 in-flight probe), per-probe/per-account cost
  caps, 7-day context-probe cooldown; probe failures for infra reasons **never** flip a capability to
  `unsupported` ([04 §2/§5](04-model-intelligence.md)). **202** + job; result reports capability truth
  and probe-execution state separately.

### 3.9 `GET /diagnostics/routes/{request_id}` — route explanation
- Returns the persisted `route_decision` (candidate set + exclusion reason codes + chosen route +
  scores) and `route_attempt` records (provider/account/model ids, latency, normalized status,
  thinking clamp). **No** prompts, responses, token content, or raw provider errors.

### 3.10 Backup / restore (async)
- **`POST /backup`** `{ passphrase }` → **202** `{ job_id, status_url }`; produces the single AEAD
  container (Argon2id KDF, wrapped data key) per [08 §9](08-engineering-standards.md#9-release--operational-readiness).
  The backup passphrase is never logged/stored; response never contains key material. **The backup
  passphrase is independent of the owner login password** (§5.7): a restored backup carries its own
  owner-auth row, so restoring onto a fresh install re-establishes the owner password from the
  container — see §5.7 for the interaction.
- **`POST /restore`** (multipart container upload + `{ passphrase }`) → **202** `{ job_id, status_url }`;
  decrypts to a temp dir, authenticates the AEAD tag, validates manifest + `PRAGMA integrity_check`,
  rewraps the data key to the current device key, atomic swap with rollback. On any failure the live
  state is untouched.
- Poll the shared `GET /jobs/{job_id}` (§3.12); its `error` reports `wrong_passphrase`,
  `corrupted_container`, `schema_incompatible` as typed, user-safe codes.

### 3.11 `POST /keys` — Venom API key
- **Request:** `{ label, rpm_limit? }`. **Success (201):** `{ data: { id, label, rpm_limit, raw_key } }`
  where `raw_key` (`vk_live_*`) is returned **once**; only a hash/verifier is stored. Subsequent
  reads never return the raw key.

### 3.12 `GET /jobs/{job_id}` — canonical shared async-job status
- **The single polling surface** for every async mutation (discovery, probe, benchmark, backup,
  restore). Resource-specific mutation endpoints return `202 { job_id, status_url }`; the client
  polls **only** here. (The OAuth transaction is the one exception and keeps its own
  `GET /oauth/{transaction_id}/status`.)
- **Job states:** `status ∈ {pending, running, completed, failed, expired}`.
- **Response:** `{ data: { job_id, kind, status, started_at, finished_at?, result_ref?, error? } }`.
  - `kind` — the job type (`discovery` | `probe` | `benchmark` | `backup` | `restore`).
  - `result_ref` — a **reference**, never inline secrets/content: e.g. the affected `account_id` +
    the read-model route to fetch results (`/models`, `/offerings/{id}/certification`), or a backup
    artifact locator. Never a credential, prompt, or provider raw error.
  - `error` — a typed, user-safe `{ code, message }` (e.g. `wrong_passphrase`, `probe_capped`).
- **Idempotent** per `job_id`; re-polling never mutates. **Authorization:** owner-session-gated like
  the rest of `/api/control/v1`; a job is visible only to the single owner.
- **Retention:** terminal jobs (`completed`/`failed`/`expired`) are retained for a bounded window
  (default 24 h) then reaped; `result_ref` targets outlive the job row. `expired` marks a job that
  never reached a terminal state within its TTL.

---

## 4. Coverage note

Every control-plane endpoint enumerated in [01 §6a](01-architecture.md#6a-control-plane-owner-ui--control-api)
has a contract here or in §2. Endpoints not yet detailed in §3 follow the §1 conventions (auth,
envelope, idempotency, concurrency, redaction, audit) and the entity schemas in
[02-domain-model](02-domain-model.md). This doc is planning-level; request/response field types are
language-neutral and become typed DTOs during implementation (Phase 2b onward).

---

## 5. Owner authentication (first-run, login, session, re-verification)

Venom is a **single-owner local application**. This section is the canonical, authoritative auth
model referenced by [README principle #5](../README.md#2-non-negotiable-principles),
[01 §6a/§8](01-architecture.md#6a-control-plane-owner-ui--control-api), and the
[02 §3 Owner-authentication entities](02-domain-model.md#owner-authentication-single-owner-identity-session-re-verification).

**Invariants (do not weaken):**
- **Exactly one owner identity.** No users, teams, roles, invitations, or RBAC. This is a **local
  password**, not cloud identity and not OS-specific identity.
- **The network gate does not replace authentication.** Loopback + Host-allowlist ([01 §6a](01-architecture.md#6a-control-plane-owner-ui--control-api))
  are mandatory *and* every authenticated endpoint additionally requires a valid owner session; each
  is independent.
- **Secrets are never stored in the clear.** Only an **Argon2id** password hash is stored — never
  the password; failed attempts are audit-recorded **without** storing the attempted secret.

### 5.1 First-run setup — `POST /auth/setup`
- **Precondition:** no `owner_auth` row exists (`GET /auth/status` → `{ setup_complete: false }`).
- **Request:** `{ password: string }` (min length/entropy policy enforced; the password is never
  logged).
- **Behavior:** generate a per-install random **salt**; derive the hash with **Argon2id** using
  documented parameters (default `time = 3, mem_kib = 65536 (64 MiB), threads = 4, key_len = 32`,
  stored alongside the hash so a later parameter bump can re-hash on next login); write the single
  `owner_auth` row; **create the first session** (as in §5.2) so setup flows straight into a
  logged-in dashboard.
- **Idempotency / errors:** if an `owner_auth` row already exists → `setup_already_complete` (409).
  Rate-limited.

### 5.2 Login — `POST /auth/login`
- **Request:** `{ password: string }`.
- **Behavior:** constant-time verify against the Argon2id hash. On success, mint an **opaque,
  high-entropy** session handle; store only its **hash/verifier** in `owner_sessions` with
  `created_at`, `last_seen_at = now`, `idle_expires_at = now + idle_ttl`,
  `absolute_expires_at = now + absolute_ttl`; set the cookie and issue a CSRF token (§5.4).
- **Cookie:** `HttpOnly`, `SameSite=Strict`, `Path=/api/control/v1`, `Secure` **when transport
  security permits** (always over TLS; on plain-loopback HTTP where `Secure` would break the cookie
  it is omitted, and this is the only case it may be omitted). The cookie carries only the opaque
  handle — **never** identity or the password.
- **Success (200):** `{ data: { session: { absolute_expires_at, idle_expires_at } }, csrf_token }`.
- **Errors:** `invalid_credentials` (401) — generic, never revealing whether setup is done;
  `locked_out` (429) when rate-limited (§5.6).

### 5.3 Session lifecycle — creation, renewal, idle & absolute expiry, revocation
- **Explicit expiry values (defaults, owner-tunable within bounds):** **idle timeout = 30 minutes**
  (sliding), **absolute lifetime = 12 hours** (hard cap, never extended by activity).
- **Renewal:** each authenticated request within the idle window advances `last_seen_at` and
  `idle_expires_at = now + idle_ttl`, but **never** past `absolute_expires_at`.
- **Idle expiry:** a request after `idle_expires_at` → `session_expired` (401); the session row is
  revoked.
- **Absolute expiry:** a request after `absolute_expires_at` → `session_expired` (401) regardless of
  activity; the owner must log in again.
- **Revocation:** `POST /auth/logout` sets `revoked_at` and clears the cookie; a revoked/expired
  session is never resurrected. Changing the owner password revokes **all** existing sessions.
- **Persistence:** sessions are server-side rows; a restart re-validates them against
  `absolute_expires_at`/`idle_expires_at`/`revoked_at` (no in-memory-only trust).

### 5.4 CSRF issuance & validation
- On login/setup a **session-bound** CSRF token is issued (returned in the JSON body and/or a
  readable `XSRF-TOKEN` cookie for the SPA to echo). Every **mutating** request (`POST/PUT/DELETE`)
  must present it in the `X-CSRF-Token` header.
- Validation is **constant-time** and bound to the current session (a token from another session is
  invalid). Failure → `csrf_failed` (403) **before** any side effect. `GET`s never require CSRF.
- `SameSite=Strict` plus the loopback gate are additional layers; the token is the primary CSRF
  defense for state-changing calls.

### 5.5 Re-verification (freshness) — `POST /auth/reverify`
- **Purpose:** gate sensitive operations (credential reveal, and similarly sensitive controls) on a
  **recent** password proof, independent of session age.
- **Request:** `{ password }`. **Behavior:** constant-time verify; on success stamp the session's
  `reverify_fresh_until = now + 5 minutes` (**exactly 5 minutes**). Re-verification **does not**
  create a new account, a new session, or a separate long-lived session — it only refreshes the
  freshness timestamp on the existing session.
- **Consumption:** a sensitive endpoint (e.g. §3.5 reveal) requires `reverify_fresh_until > now`;
  otherwise `reverification_required` (401) and the UI prompts for the password.
- **Errors:** `invalid_credentials` (401); `locked_out` (429) under rate limiting (§5.6).

### 5.6 Rate limiting & audit of failures
- Failed **login** and **re-verify** attempts are **rate-limited** (default: exponential backoff /
  lockout after **5 consecutive failures within 15 minutes**, per single owner) and each attempt
  (success or failure) emits an `auth_event` audit row: `{ action, result, reason_code, at }` —
  **never** the attempted password or any secret. Lockout returns `locked_out` (429) with a
  `retry_after`.

### 5.7 Recovery, and interaction with backup/restore
- **Lost password — no secret backdoor.** There is **no** password-reset-by-email or recoverable
  hint (single-owner local app; the hash is one-way). Recovery paths, explicitly:
  1. **Restore from a portable backup** (§3.10) — the encrypted container carries its own
     `owner_auth` row, so a restore re-establishes the owner password that was in effect when the
     backup was taken. This is the supported recovery route.
  2. **Local owner reset** — because the owner physically controls the machine, an owner who can
     prove local filesystem control may run a documented local reset (a `venom` CLI subcommand run
     on the host) that **clears the `owner_auth` row and revokes all sessions**, returning the app
     to the first-run `setup` state. It **cannot** decrypt existing provider credentials it lacks
     the keyring for — it only resets the login gate; encrypted credentials remain protected by the
     device keyring ([01 §8](01-architecture.md#8-security-model)).
- **The login password is independent of the backup passphrase.** The backup passphrase (§3.10,
  [08 §9](08-engineering-standards.md#9-release--operational-readiness)) protects the portable
  container; the login password gates the control plane. Neither is derivable from the other; losing
  the **backup passphrase** makes that backup unrecoverable, and losing the **login password**
  requires one of the two recovery paths above.

### 5.8 Typed errors (auth)
`setup_already_complete` (409), `invalid_credentials` (401), `session_expired` (401),
`reverification_required` (401), `csrf_failed` (403), `locked_out` (429), plus the shared envelope
codes in §1. `message` is always generic and never reveals whether setup is complete to an
unauthenticated caller beyond `GET /auth/status`.

### 5.9 Testable acceptance criteria
- First run with no `owner_auth` requires `setup`; a second `setup` is rejected.
- Login with the wrong password fails generically and is rate-limited after the configured threshold;
  no secret is written to any log/audit row (canary).
- A session past **idle** (30 min) or **absolute** (12 h) expiry is rejected with `session_expired`;
  activity renews idle but never beyond the absolute cap.
- A mutating request without a valid session-bound CSRF token is rejected with `csrf_failed` before
  any side effect; a `GET` needs none.
- Credential reveal without a fresh (≤ 5 min) re-verification returns `reverification_required`;
  after `reverify` it succeeds exactly once with `Cache-Control: no-store`; freshness expires at 5
  minutes to the second.
- Changing the password revokes all existing sessions.
- Restore re-establishes the owner password from the container; the local reset returns to first-run
  setup without exposing provider secrets.
- **Negative tests:** expired/revoked cookie, forged/cross-session CSRF token, replayed login after
  lockout, re-verify reuse past 5 minutes, and `setup` after completion — all rejected with the typed
  codes above and audited.
