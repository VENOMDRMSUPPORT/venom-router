# 01 — Architecture

Clean, greenfield architecture. Directionally validated by the old build's final target, but
designed fresh here. Read [README](../README.md) first for the principles this serves.

---

## 1. Shape at a glance

```
                         ┌───────────────────────────────────────────────┐
   OpenAI-compatible     │                 venom (single Go binary)       │
   clients / IDEs / CLI  │                                                │
        │                │  ┌──────────────┐     ┌──────────────────────┐ │
        │  /v1/chat/...   │  │  Public API  │────▶│   Routing / Tier     │ │
        ├────────────────┼─▶│ (vk_live_*)  │     │   Engine (decides)   │ │
        │                │  └──────────────┘     └───────────┬──────────┘ │
        │                │                                   │            │
   Owner browser         │  ┌──────────────┐     ┌───────────▼──────────┐ │
        │  /dashboard     │  │ Control API  │     │  Embedded Bifrost    │ │
        ├────────────────┼─▶│ (loopback)   │     │  Core (executes)     │─┼──▶ provider
        │                │  └──────┬───────┘     └──────────────────────┘ │    accounts
        │                │         │                                      │    (HTTPS)
        │                │  ┌──────▼───────┐  ┌─────────┐  ┌────────────┐ │
        │                │  │ Providers /  │  │ Model    │  │ Secrets /  │ │
        │                │  │ Accounts     │  │ Intel.   │  │ Keyring    │ │
        │                │  └──────┬───────┘  └────┬────┘  └─────┬──────┘ │
        │                │         └──────────┬────┴─────────────┘        │
        │                │              ┌─────▼──────┐                    │
        │                │              │  SQLite    │  (single writer)   │
        │                │              └────────────┘                    │
        │                │  ┌──────────────────────────┐                  │
        │                │  │ Embedded React dashboard  │  system tray     │
        │                │  │ (//go:embed dist)         │  (desktop)       │
        │                │  └──────────────────────────┘                  │
        │                └───────────────────────────────────────────────┘
```

**One process. One node. One SQLite writer. One owner.** No separate gateway process, no
Node/Bun runtime in production, no external database.

---

## 2. Process model

- **Single executable**, `cmd/venom`, built `CGO_ENABLED=0` (pure-Go SQLite keeps CGO off).
  Primary target Windows/amd64; must also build and run on Linux/WSL.
- **Two run modes**:
  - bare `venom` (no args) → **tray mode**: starts the server, shows a system-tray icon with
    *Open Dashboard / Status / Restart / Quit*, hides the console window. This is how the owner
    runs it day to day on the desktop.
  - `venom serve` → headless server (for a home server / VPS), graceful shutdown on
    SIGINT/SIGTERM with a bounded timeout.
- **Single-instance lock** on `<dataDir>/venom.lock` acquired *before* any keyring/DB creation,
  so a second launch can never race first-run state. A second instance surfaces "already
  running" and focuses the first.
- **Fail-closed startup order:** validate embedded assets → acquire lock → load/create keyring
  (in memory) → open SQLite + run migrations → reconcile keyring with DB and **validate every
  stored ciphertext before opening any listener** → build repositories → build provider
  registry + register adapters → build services → mount HTTP mux → listen. Any integrity
  failure aborts startup; provider outages or empty tier pools do **not** (they are runtime
  states).

---

## 3. Components (package boundaries)

Dependency direction is acyclic and enforced by a test. Suggested Go layout under `internal/`:

| Package | Owns |
|---|---|
| `app` | Composition root: startup order, dependency wiring, HTTP mux, graceful shutdown. |
| `cli` | Command dispatch (`serve`/`version`/`help`/bare→tray), config + data-dir loading. |
| `config` | Typed config + precedence (defaults → env → flags); default bind `127.0.0.1:8081`. |
| `platform` | OS-specific paths (`%LOCALAPPDATA%\\VenomRouter`, `$XDG_DATA_HOME/venom-router`). |
| `tray` | OS-independent lifecycle manager + Windows systray UI. |
| `secrets` | AES-256-GCM keyring (stored outside SQLite), encrypt/decrypt with bound AAD, rotation barrier, startup reconciliation. |
| `sanitize` | Secret redaction for logs/errors/traces (full `[REDACTED]`, never partial). |
| `providers` | Provider registry + typed adapters (`APIKeyAdapter`, `OAuthAdapter`, `ModelDiscoveryAdapter`, `QuotaAdapter`); provider definitions and policies. Imports neither `accounts` nor `storage`. |
| `accounts/domain` | **Pure domain.** Account entity, states, funding classification and transition rules only. No network, no database, no secrets, no OAuth flows. |
| `accounts/application` | **Use-case orchestration.** Enrollment, reauthentication, disconnect, reveal (after owner verification). Coordinates between `accounts/domain`, `providers`, `secrets`, and `storage` via injected interfaces. Never executes OAuth HTTP or keyring operations directly. |
| `models` | Catalog domain: canonical identity, offerings, discovery runs, capability/context evidence, certification state. Pure; imports neither `storage` nor `database/sql`. |
| `intelligence` | Discovery orchestration, probing, external metadata sync, certification pipeline (uses `providers` + `models`). |
| `routing` | The tier engine: policies, offering selection, scoring, fallback/cooldown, quota reservation. |
| `execution` | Single `InferenceTransport` dispatcher; builds a resolved route and invokes the correct transport (Bifrost, native API, native OAuth, OpenAI-compatible, or custom). |
| `quota` | Per-account usage/quota snapshots, reservations, reset windows, enforcement. |
| `httpapi` | The two HTTP surfaces: public `/v1/*` (API-key auth) and control `/api/control/v1/*` (loopback + **owner authentication**: opaque server-side session + session-bound CSRF + re-verification). Owns the single-owner auth model of [09 §5](09-control-api.md#5-owner-authentication-first-run-login-session-re-verification) (first-run setup, login/logout, session lifecycle, CSRF). Stable error envelope. |
| `httpui` | Embeds and serves the compiled React dashboard with SPA fallback. |
| `storage` | All SQLite repositories + migrations; implements the interfaces defined by the pure domain packages. |
| `observability` | Structured, secret-free logging, metrics, and route-decision/attempt audit records. |

Rules: pure domain packages (`accounts/domain`, `providers`, `models`) never import `storage`,
`database/sql`, or any infrastructure package. `providers` never imports `accounts/domain` or
`accounts/application`. `accounts/application` orchestrates via injected interfaces — it never
executes OAuth HTTP or keyring operations directly. `storage` implements the interfaces owned
by the domain packages. This keeps the domain testable and the SQL isolated.

---

## 4. The execution boundary (Venom decides, Bifrost / custom transport executes)

All execution flows through a **single, typed `InferenceTransport` interface** — one entry point
for every provider regardless of auth mode. OAuth and API key are **credential mechanisms only**;
the actual inference transport is a separate concern selected per provider.

### 4.1 InferenceTransport interface

```go
// ResolvedRoute is a fully-decided single-choice route. The transport
// receives this and can neither re-select nor widen it.
type ResolvedRoute struct {
    Provider    ProviderID
    AccountID   string
    Credential  StoredCredentials
    ModelID     string
    BaseURL     string          // resolved by Venom before dispatch
}

// InferenceTransport executes an already-decided inference call.
// One implementation per transport type, never one per provider slug.
type InferenceTransport interface {
    // Execute sends a single non-streamed request.
    Execute(ctx, ResolvedRoute, NormalizedRequest) (*NormalizedResponse, error)

    // Stream sends a streaming request and returns a channel of chunks.
    Stream(ctx, ResolvedRoute, NormalizedRequest) (<-chan Chunk, error)

    // Cancel aborts an in-flight stream or request.
    Cancel(ctx, ResolvedRoute, requestID string) error

    // NormalizeError converts a provider-native error into the Venom
    // stable error envelope. Never leaks credentials or raw errors.
    NormalizeError(err error, route ResolvedRoute) VenomError

    // SupportedCapabilities returns the operation set this transport
    // can handle for the given route (e.g. chat, streaming, tools,
    // vision). Used during capability certification, never during
    // routing (certification is pre-computed).
    SupportedCapabilities(route ResolvedRoute) []Operation
}
```

### 4.2 Failure taxonomy

Every provider error is normalized into a `TypedFailure` by `InferenceTransport.NormalizeError`.
The error carries structured fields so fallback and cooldown logic can act correctly without
guessing from HTTP status alone.

```go
type FailureClass string

const (
    FailureClassInvalidRequest FailureClass = "invalid_request"    // bad prompt/schema/param
    FailureClassAuth           FailureClass = "auth_error"         // expired/invalid credential
    FailureClassNotFound       FailureClass = "not_found"          // model missing/disabled
    FailureClassQuota          FailureClass = "quota_error"        // account quota exhausted
    FailureClassRateLimit      FailureClass = "rate_limit"         // model-specific rate limit
    FailureClassServer         FailureClass = "server_error"       // provider outage / 5xx
    FailureClassNetwork        FailureClass = "network_error"      // timeout / DNS / reset
)

type FailureScope string

const (
    FailureScopeRequest           FailureScope = "request"
    FailureScopeAccount           FailureScope = "account"
    FailureScopeOffering          FailureScope = "offering"
    FailureScopeProvider          FailureScope = "provider"
    FailureScopeTransientTransport FailureScope = "transient_transport"
)

// TypedFailure is the normalised error envelope returned by
// InferenceTransport.NormalizeError. Fields are populated by priority:
// provider semantic code → HTTP headers → adapter rules → HTTP fallback.
type TypedFailure struct {
    FailureClass  FailureClass  // high-level error category (independent of scope)
    Scope         FailureScope  // which boundary to cooldown / bypass
    Retryable     bool          // whether retry (possibly after cooldown) may succeed
    CooldownUntil *time.Time    // when the scope may be retried (nil = no cooldown needed)
    RetryAfter    *int          // seconds parsed from Retry-After header (nil = no signal)
    QuotaResetAt  *time.Time    // when the account/offering quota resets (nil = unknown)
    ProviderCode  string        // provider-native error code, if available
    HTTPStatus    int           // HTTP status code (0 if not HTTP)
    SafeMessage   string        // user-safe error description, never raw provider text
    Evidence      map[string]any // sanitized diagnostic data for observability
}
```

**Scope classification priority** (first match wins):

1. **Provider semantic code / response body** — e.g. `context_length_exceeded`, `invalid_api_key`,
   `model_not_found`, explicit `quota_exhausted: account` vs `quota_exhausted: model`.
2. **Standard HTTP headers** — e.g. `Retry-After`, `X-RateLimit-Scope`, `X-Quota-Reset`.
3. **Adapter-specific mapping** — per-provider rules documented in the adapter contract.
4. **HTTP status fallback** when no richer signal is available (see table below).

| Condition / provider signal | FailureClass | Scope | Retryable | Action |
|---|---|---|---|---|---|
| Invalid prompt, bad schema, unsupported param | `invalid_request` | `request` | false | Reject, no cooldown |
| Invalid/expired credential (401/403) | `auth_error` | `account` | true (after refresh) | Refresh credential once; if still failing → suspend |
| Model missing, disabled, or unsupported (404) | `not_found` | `offering` | false | Suspend offering, schedule re-discovery |
| Account quota exhausted (429 with explicit scope=account) | `quota_error` | `account` | false | Cooldown until reset (`QuotaResetAt`) |
| Model-specific rate limit (429 with scope=model/offering) | `rate_limit` | `offering` | false | Cooldown the offering |
| Provider overload / maintenance (503) | `server_error` | `provider` | true (capped) | Short provider-level cooldown; fallback to other providers |
| Timeout, DNS failure, connection reset | `network_error` | `transient_transport` | true | Bounded retry then fallback |
| 429 without any scope signal | `rate_limit` | `transient_transport` (conservative) | true | Cap retries, treat as transient — never widen scope by default |
| Unrecognized 5xx | `server_error` | `provider` | true (capped) | Short cooldown, limited retries |

### 4.3 Transport types

| Transport type | When used | Execution path |
| `bifrost` | Standard API-key providers with OpenAI-compatible or OpenAI-translatable schemas | Embedded Bifrost core (pool size 1, one key, retries disabled) |
| `native_api` | API-key providers with non-OpenAI schemas (e.g. Gemini CLI) | Venom-owned adapter with schema normalization |
| `native_oauth` | OAuth providers whose account semantics Bifrost cannot model (e.g. Claude Code, Codex, GitHub Copilot) | Venom-owned adapter; credential refresh is handled by the credential provider, not the transport |
| `openai_compatible` | Custom OpenAI-Compatible providers | Generic OpenAI-format HTTPS call |
| `custom` | Any provider needing a one-off adapter | Venom-owned adapter registered at compile-time |

### 4.4 Division of responsibility

| Venom owns (policy / product) | Transport owns (commodity execution) |
|---|---|
| Public tier aliases `venom/lite\|pro\|max` | Protocol normalization & transformation |
| Request capability/modality/context extraction | Provider HTTP transports |
| The certified `(provider, account, model, operation)` catalog | Streaming implementation |
| Exact route selection + scoring + fallback | Request serialization / response normalization |
| Credential resolution, quota reservation/reconciliation | Low-level retries that never violate the chosen route |
| **Transport selection** per provider (declared in the provider catalog) | — |

### 4.5 Enforcement

- Venom resolves exactly one provider + account + model + credential, resolves the correct
  transport type for that provider, and hands the transport a single-choice `ResolvedRoute`.
- The transport **cannot** re-select a provider/account — e.g. it can never promote `venom/lite`
  onto a paid account.
- Bifrost, when used, is configured with pool size 1, one key whitelisted to the one model,
  and retries disabled. Bifrost holds no authoritative catalog.
- OAuth tokens are refreshed by the **credential provider** (`accounts` package), not by the
  transport. The transport receives a fresh credential on every call.
- **One execution path, one interface.** The `execution` package contains the single
  `InferenceTransport` dispatcher. Behind it, transports are selected by typed capability,
  **never** by a switch on provider slug. Adding a new provider means implementing or selecting
  an existing transport — never duplicating the execution path.

---

## 5. Persistence — SQLite

- Pure-Go driver (`modernc.org/sqlite`), one file `<dataDir>/venom.db`.
- Pragmas: `journal_mode=WAL`, `busy_timeout=5000`, `foreign_keys=ON`, `synchronous=NORMAL`.
- Migrations are embedded, checksum-guarded, and integrity-checked on open; rollback-tested.
  (Watch the Windows `file:///C:/...` URL form and force LF line endings so checksums are stable.)
- All access via typed repositories. Handlers, routing, and Bifrost never issue ad-hoc SQL.
- Writes that must be consistent (quota reservation, route decision, certification transitions)
  happen in short transactions — **and no provider HTTP call runs while a write transaction is
  open.** Reserve → release the txn → execute → reconcile in a second txn.

---

## 6. HTTP surfaces — two separate planes

Venom exposes **two independent HTTP surfaces** with different security postures.
They **may** bind to different addresses and ports.

### 6a. Control Plane (Owner UI + Control API)

| Endpoint | Purpose |
|---|---|
| `/` | Embedded React dashboard (SPA) |
| `/api/control/v1/auth/*` | Owner authentication: first-run `setup`, `login`, `logout`, `session`, `reverify` (see [09 §5](09-control-api.md#5-owner-authentication-first-run-login-session-re-verification)) |
| `/api/control/v1/*` | Providers, accounts, connect/OAuth, reveal/refresh/funding/disconnect, models, status — **all require an authenticated owner session** |
| `/api/control/v1/jobs/{job_id}` | **Canonical shared async-job status** (discovery, probe, benchmark, backup, restore — see [09 §1/§2](09-control-api.md)) |
| `/api/control/v1/oauth/{provider}/callback` | OAuth callbacks (transaction-based; not session-bound) |
| `/api/control/v1/oauth/{transaction_id}/status` | OAuth transaction polling (from fixed-port callbacks; transaction-based, not session-bound) |
| `/health` | **Process liveness — unauthenticated, outside the `/api/control/v1` authenticated API** (see §6d) |

The full per-endpoint request/response contracts (auth, schemas, errors, idempotency, concurrency,
async-job/polling, secret redaction, audit) are specified in
[09-control-api.md](09-control-api.md).

**Security constraints (v1, non-negotiable):**

- **Loopback-only.** Binds exclusively to `127.0.0.1` and `::1`. A configuration that attempts to
  bind the control plane to any other address is rejected at startup with a clear error. There is
  **no** "opt in deliberately" escape hatch in v1.
- **Remote socket address verification.** Every request is checked for `RemoteAddr` being a
  loopback address (`127.0.0.0/8`, `::1`). The check looks at the **actual TCP socket address**,
  not at any HTTP header.
- **No trust in `X-Forwarded-For` or any proxy header.** These headers are never consulted for
  bypassing the loopback gate.
- **Host header allowlist.** Only `Host` values matching `localhost`, `127.0.0.1`, `::1`, or the
  configured port (e.g. `127.0.0.1:8081`) are accepted. Any other `Host` header is rejected
  with 403 before any session or CSRF check — this prevents DNS rebinding attacks.
- **Owner authentication is mandatory and primary — not defence-in-depth.** Every
  `/api/control/v1/*` endpoint (except the unauthenticated `auth` setup/login handshake and the
  transaction-based OAuth callback/status) requires an authenticated owner session (opaque,
  server-side; HttpOnly SameSite=Strict cookie) plus a session-bound CSRF token on every mutating
  request. **The loopback gate and Host allowlist are network defenses that do *not* replace
  authentication**, and authentication does not replace them; both hold simultaneously. The full
  model — first-run setup, login/logout, session creation/renewal/idle+absolute expiry/revocation,
  CSRF issuance/validation, 5-minute re-verification freshness, lost-password recovery, and
  backup/restore interaction — is specified in [09 §5](09-control-api.md#5-owner-authentication-first-run-login-session-re-verification).
- OAuth callback listeners for fixed-port providers (Codex, xAI) are short-lived loopback-only
  listeners on the mandated ports, **never** on the control-plane bind port. Fixed-port OAuth
  callbacks are **transaction-based and do not depend on the owner session cookie** (they use the
  state-hash lookup of [03 §2a](03-provider-integration-catalog.md#2a-oauth-built-in)).

### 6b. Data Plane (Public Inference API)

| Endpoint | Purpose |
|---|---|
| `POST /v1/chat/completions` | Chat + streaming + tools + vision; **OpenAI-compatible**, with one optional namespaced `venom` request extension ([05 §1b](05-tier-engine.md#1b-public-request-contract--the-venom-extension)) |
| `GET /v1/models` | Tier model listing |

The request body stays OpenAI-compatible; Venom-specific semantics ride on **one** optional
`venom` object (`thinking_budget`, `required_capabilities`) — never on competing headers or
alternative shapes. Full wire contract, validation, and the `venom_invalid_extension` error are in
[05 §1b](05-tier-engine.md#1b-public-request-contract--the-venom-extension).

**Security constraints:**

- **Separate bind** — configured independently from the control plane. Default is `127.0.0.1:8081`
  (same port as control plane for the common local-only case), but the owner may choose any
  `host:port` without exposing the control plane.
- **Venom API key authentication.** Every request requires a valid `vk_live_*` key in the
  `Authorization` header. Per-key RPM limiting applies.
- **Three model names** it accepts are `venom/lite`, `venom/pro`, `venom/max`.
- **Never** serves control endpoints — there is no path overlap.
- Every response carries a small, sanitized **`X-Venom-*` telemetry header set** (§6c)
  so a plain OpenAI SDK can read the chosen route without parsing the body.

### 6c. Response telemetry headers (`X-Venom-*`)

A single, sanitized header builder stamps every inference response so clients see the routing
outcome without parsing the body. The set: `X-Venom-Request-Id`, `-Tier`, `-Provider`, `-Model`,
`-Funding`, `-Latency-Ms`, `-Tokens-In`, `-Tokens-Out`, `-Fallback-Attempts`, `-Version`. Values
pass through the same `sanitize` boundary as logs; a header **never** carries an account identifier,
a credential, or a raw provider error. For streaming, headers are sent at stream start with zeroed
metrics and the final values are emitted in a trailing SSE metadata comment — a pattern that works
with plain OpenAI SDKs. (Adopted from the OmniRoute analysis, item C; observability contract in
[05-tier-engine](05-tier-engine.md) §7.)

### 6d. Health endpoints (the final, single choice)

To avoid duplicate/ambiguous health surfaces, the choice is fixed:

- **`/health` — process liveness, unauthenticated, *outside* the authenticated control API.** It is
  served on the control-plane bind (still behind the loopback + Host-allowlist network gate) but is
  **not** under the `/api/control/v1` namespace and requires **no** owner session or CSRF. It returns
  only a minimal liveness signal (process up, listener accepting) and **no** owner data. This is the
  endpoint the Phase 0 gate and any external liveness probe use.
- **`/api/control/v1/health` — reserved for an authenticated readiness/diagnostic endpoint, only if
  one is needed.** If added, it is a **distinct** surface (owner-session-gated, reporting readiness
  detail such as DB/keyring/migration status) and its name/semantics must **not** duplicate
  liveness. V1 ships the liveness `/health` only; the authenticated readiness endpoint is optional
  and additive, never a second liveness check.

---

## 7. Tech stack

| Layer | Choice | Why |
|---|---|---|
| Core / gateway | **Go** (1.26+) | Matches embedded Bifrost, high throughput, single static binary, CGO-free. |
| Execution core | **Embedded Bifrost core** | Proven multi-provider transport/streaming; don't rebuild commodity plumbing. |
| Storage | **SQLite** (`modernc.org/sqlite`) | Zero-ops, single-file, single-writer fits a personal gateway; no CGO. |
| Migrations | goose (embedded) | Deterministic, checksummed. |
| Dashboard | **React + Vite + TypeScript**, embedded via `go:embed` | The Provider Fleet UI; ships inside the one binary. |
| Desktop | system tray (pure-Go) | Run-and-forget on the owner's PC. |
| Crypto | Go stdlib AES-256-GCM + `x/crypto` (Argon2id KDF and XChaCha20-Poly1305 for portable backup) + `x/oauth2` + `golang-jwt` | Standard, audited primitives. (Backup container spec: [08 §9](08-engineering-standards.md#9-release--operational-readiness).) |

Deployment is container-friendly for a home server or VPS, and tray-native on the desktop; the
same binary serves both. Choose the host at deploy time — the design is host-agnostic.

---

## 8. Security model

- Single-owner local trust. Exactly one owner identity (a local password — no users, teams, roles,
  RBAC, no cloud/OS identity); no shared tenancy.
- **Two planes, two security postures:**
  - **Control Plane** — loopback-only (`127.0.0.1`, `::1`), socket-address verified, Host
    header allowlist, no `X-Forwarded-For` trust, **plus mandatory owner authentication**
    (opaque server-side session, HttpOnly SameSite=Strict cookie, session-bound CSRF, 5-minute
    re-verification for sensitive ops). The network gate and authentication are **independent
    requirements** — neither replaces the other. **Never** exposed to the network in v1 (see §6a;
    full auth contract in [09 §5](09-control-api.md#5-owner-authentication-first-run-login-session-re-verification)).
  - **Data Plane** — independent bind (any address the owner chooses), authenticated by
    `vk_live_*` Venom API key, per-key RPM limiting. Only `/v1/chat/completions` and
    `/v1/models` are served. Never serves control endpoints.
- Provider credentials: AES-256-GCM, fresh nonce per encryption, AAD bound to
  `(purpose, provider, account, record, kind)` and derived (never stored). The master key lives
  **outside** SQLite (`<dataDir>/secrets/keyring.json`, owner-only file permissions; env override
  `VENOM_ENCRYPTION_KEY`). Startup fails closed if the DB references a key the keyring lacks.
- Credentials are decryptable only for an explicit dashboard "reveal" (with `Cache-Control:
  no-store`); Venom API keys are **hash-only** and shown once at creation.
- Confidential OAuth client secrets (only Antigravity today) come from env; when unset the
  provider shows **"Setup required"** listing the missing variable names (never values), not a
  crash.
- **Backup:** portable backup is a single encrypted container (passphrase + KDF + AEAD), never
  a raw DB + keyring pair (see [08-engineering-standards](08-engineering-standards.md) §9).
- A canary test asserts no injected secret ever appears in any log, error, trace, or audit row.
