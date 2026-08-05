# 02 — Domain Model

The vocabulary and data model. This is the part the owner cares about most, and the part the
old build got conceptually muddled. Get these boundaries right and everything else follows.

---

## 1. The integration taxonomy

A **Provider** is *how Venom knows how to talk to a vendor*. Providers are organized by how you
connect, never by whether they cost money:

```
Provider Integrations
├── Built-in / Ready Integrations   (Venom ships the wire logic)
│   ├── OAuth          — browser login; account info fetched automatically
│   └── API Key        — paste a key; the owner marks the account free or paid
└── Custom Integrations
    └── OpenAI-Compatible — owner supplies base URL + key (+ optional headers/models)
```

- **Built-in / OAuth** → the provider exposes an OAuth adapter. Enrollment opens a browser, and
  Venom fetches identity (email/plan), models, and quota automatically. Examples: Antigravity,
  Claude Code, Codex, ClinePass, GitHub Copilot, xAI (Grok).
- **Built-in / API Key** → the provider exposes an API-key adapter. Enrollment takes a key and
  asks the owner to classify the account **free / paid / (inherit provider default)**. Examples:
  OpenCode Zen, Agnes AI, Gemini CLI, Ollama Cloud, NVIDIA NIM.
- **Custom / OpenAI-Compatible** → not tied to a shipped adapter. The owner registers any
  OpenAI-compatible endpoint by URL + key (+ optional custom headers and an optional explicit
  model list), and marks each account's funding. This is how new/niche providers get added
  without a code change.

Every built-in provider has exactly **one** auth mode. There is deliberately no dual-mode
integration: a provider connects via OAuth or via API key, never both. This keeps enrollment,
identity, and credential handling single-path per provider.

> **`AuthMode` is the connection axis; funding is a separate account axis.** Never fold them
> together. A provider is never "free" or "paid."

---

## 2. Free / Paid — the semantics, precisely

**Funding lives on the account, and all Offerings under that account inherit its classification. It is never a provider type, and there is no per-Offering funding override in v1.**

- A **Provider** carries only a *default funding policy* used to stamp a new account's first
  funding record:
  - `fixed` — always the same value (e.g. ClinePass → `paid` and **locked**; an owner override is
    rejected). Stamps the first funding-evidence row with `source = provider_policy`.
  - `owner_policy` — the owner declares it (e.g. OpenCode Zen free accounts).
  - `provider_evidence` — derived from what the provider tells us (e.g. Antigravity plan → `free`
    for "Free", `paid` for "Pro", `unknown` otherwise).
  - `evidence_required` — the built-in provider definition **cannot guarantee** a free or paid
    classification (e.g. Agnes AI, Gemini CLI, NVIDIA NIM). Stamps the first funding-evidence row
    with `funding = unknown`, `source = provider_policy` (**not locked, not non-expiring**) —
    recording *that the catalog cannot classify the account*, never fabricated runtime provider
    evidence. Such accounts are excluded from all production routing (and are never Lite-eligible)
    until authenticated provider evidence or an allowed owner override establishes `free`/`paid`.
- An **Account** has a current funding value in `{free, paid, unknown}` backed by an
  **append-only evidence trail** (one current row, prior rows superseded). 
  - **OAuth accounts:** funding is **auto-detected** from verified provider data (`provider_evidence`).
    The owner may override it after enrollment.
  - **API-key and Custom accounts:** the owner **chooses** `free` or `paid` at enrollment time,
    recorded as `owner_override`. `unknown` is also an option when the owner isn't sure.

#### Funding-evidence source vocabulary (canonical — used everywhere)

Every funding-evidence row carries exactly one `source`. This is the **single authoritative
enum**; no other value (e.g. a bare `provider_default`) may appear in code, schema, or UI:

| `source` | Meaning | Overridable? | Required evidence | Freshness |
|---|---|---|---|---|
| `provider_policy` | Provider-declared catalog policy: a **fixed** value (`funding_policy.mode = fixed`), e.g. ClinePass → `paid`, **or** the initial `unknown` stamp of an **`evidence_required`** provider (mode = `evidence_required`), e.g. Agnes AI. | **No when `funding_locked = 1`** (owner override rejected at the API); yes otherwise (the `evidence_required` stamp is always overridable). | The provider definition's `funding_fixed` value, or the `evidence_required` declaration (`funding = unknown`). | Non-expiring when `funding_non_expiring = 1`; an `evidence_required` stamp is superseded by the first `provider_evidence` or owner action. |
| `provider_evidence` | Live, authenticated, account-specific provider data (`mode = provider_evidence`), e.g. Antigravity plan string. | Yes (by `owner_override`). | The exact provider field mapped to `free`/`paid`/`unknown`, stored sanitized in `evidence_json`. | Re-derived on each identity refresh; may supersede a prior `provider_evidence`/`owner_policy` row. |
| `owner_policy` | The provider's owner-declared **default** (`mode = owner_policy`) applied at enrollment, e.g. OpenCode Zen → `free`. | Yes. | The provider's `owner_policy` default. | Stamped once at first connect. |
| `owner_override` | The owner's explicit per-account choice. | Only by another owner action. | The owner's recorded decision + reason. | Never auto-superseded by any automated source. |

**Which mode stamps the first row:** `fixed → provider_policy`; `owner_policy → owner_policy`;
`provider_evidence → provider_evidence`; `evidence_required → provider_policy` with
`funding = unknown` (confidence and observation timestamp recorded; auditable like every row).

**Funding authority (which append-only row becomes current):** the newest non-superseded row wins,
governed by these rules:
1. A `provider_policy` row with `funding_locked = 1` is **immutable** — any override attempt is
   rejected with `funding_locked`; no later row supersedes it.
2. An `owner_override` row is **never** auto-superseded by `provider_evidence` or `owner_policy`;
   only a subsequent owner action supersedes it.
3. `provider_evidence` may supersede a prior `provider_evidence` or `owner_policy` row on refresh
   (fresher, higher-or-equal confidence), unless rule 2 applies.
4. Absent any of the above, `owner_policy` (or a non-locked `provider_policy`) stamped at connect
   is the current row.

Every supersession is an append (the prior row gets `superseded_at`); the partial-unique index
guarantees exactly one current row. All transitions emit an `audit_event`.
- All **Offerings** associated with an account inherit its funding value. The same model name
  appearing on a FREE account and a PAID account produces two Offerings with different funding,
  each correctly classified by its parent account.
- The **same provider** can therefore hold account A (`free`) and account B (`paid`)
  simultaneously; each account's Offerings carry the correct label independently.
- `unknown` funding is **excluded from all production routing** until classified. Never guess:
  a plan label, a `:free` model suffix, or a missing price does **not** prove `free`; and
  "OAuth ⇒ paid" / "API-key ⇒ free" prove nothing at all. Only verified zero marginal cost proves
  `free`.

**Consequence for the UI (Provider Fleet):** the provider row shows the *integration* (logo,
name, auth badge, aggregate health); the **billing badge lives on each account row**, showing
the account's real plan string when known, else Free / Paid / Unknown.

---

## 3. Entities

### Provider (definition, mostly static)
- `id` (slug, e.g. `opencode-zen`), `display_name`, `description`
- `auth_mode` ∈ `{oauth2, api_key, custom_openai}`
- `capabilities` (what the *adapter* can do: `connect_api_key`, `begin_oauth`,
  `complete_oauth`, `refresh_credentials`, `fetch_identity`, `check_health`,
  `discover_models`, `fetch_quota`, `multi_account`)
- `funding_policy` (`mode` ∈ `{fixed, owner_policy, provider_evidence, evidence_required}`,
  `fixed_value`, `locked`, `non_expiring`)
- `base_url`, and per-provider settings (endpoints, headers) — for custom providers these are
  owner-supplied.

### Account (a connected instance)
- `id` (uuid), `provider_id`
- `external_id` — the provider's immutable ID when it exposes one (e.g. `account.uuid`,
  `user.id`, JWT `sub`); otherwise a **key fingerprint** `key_<sha256(provider\0key)>`
- `display_name`, `auth_type` ∈ `{api_key, oauth2}`
- **Lifecycle is modeled as independent axes**, not one overloaded enum (see *Account lifecycle*
  immediately below):
  - `connection_state` ∈ `{connecting, connected, stopped, disconnected}` — the owner/enrollment
    lifecycle axis (persisted).
  - `health_state` ∈ `{unknown, healthy, degraded, unavailable, expired}` — the observed
    operational-health axis (persisted; updated by health checks and execution outcomes).
  - `reauth_in_progress` (bool) — a staged-credential reauthentication is underway.
  - **cooldown** — *not* an account column; derived at read time from the `cooldowns` table at the
    correct scope (account/offering/provider).
  - `display_status` — a **derived** projection for UI/diagnostics only (never persisted as truth).
- `identity_email`, `identity_plan`
- `last_health_check_at`, `last_health_error` (sanitized — safe strings only)
- Unique on `(provider_id, external_id)` — identity dedup. Multiple accounts per provider allowed.

#### Account lifecycle (multi-axis state model)

The old build muddled connection lifecycle with health in one enum. Venom models the account as a
**projection of independent axes**; routing reads the axes, and the UI reads a derived
`display_status`. No single persisted column is overloaded.

**Axis 1 — `connection_state` (owner/enrollment lifecycle):**

| State | Meaning | Legal transitions → | Trigger | Owner |
|---|---|---|---|---|
| `connecting` | Enrollment/OAuth in flight; no usable credential yet. | `connected` (success), `disconnected` (abort/expire) | Enrollment start / callback | `accounts/application` |
| `connected` | Enrolled with a usable active credential. | `stopped` (owner disables), `disconnected` (owner removes / identity mismatch) | Successful enroll or reauth | `accounts/application` |
| `stopped` | Owner-disabled; retained but excluded from routing. | `connected` (owner re-enables), `disconnected` (owner removes) | Owner action | Owner via control API |
| `disconnected` | Credential removed/revoked; terminal until re-enrolled. | `connecting` (re-enroll) | Owner disconnect / hard revoke | Owner / system |

> **V1 supports soft disconnect only.** `DELETE /accounts/{id}` ([09 §2](09-control-api.md#2-endpoint-catalog))
> stops routing through the account, revokes/retires usable credentials where possible, **retains
> sanitized operational history and audit records**, and leaves the account restorable only through
> a new authentication/enrollment flow. Hard delete, purge, and irreversible historical deletion
> are outside V1 scope.

**Axis 2 — `health_state` (observed operational health), meaningful only while `connected`:**

| State | Meaning | Set by | Routing impact |
|---|---|---|---|
| `unknown` | Not yet checked (fresh connect). | default | Eligible if credential valid; first health check scheduled |
| `healthy` | Last check/execution succeeded. | health check / successful execution | Eligible |
| `degraded` | Intermittent failures below the unavailable threshold. | repeated soft failures | Eligible with score penalty |
| `unavailable` | Provider/transport unreachable for this account. | `server_error`/`network_error` at account scope | Ineligible until next successful check |
| `expired` | Credential expired and refresh not yet successful. | `expires_at` in past / `auth_error` after refresh attempt | Ineligible; refresh/reauth required |

**Derived signals (not axes):** `cooldown` (from `cooldowns`), `reauth_in_progress` (bool).

**`display_status` derivation (first match wins):** `disconnected` → `stopped` →
(`connection_state = connecting`) `connecting` → (`reauth_in_progress`) `reauthenticating` →
(cooldown active) `cooling_down` → `health_state` (`expired`/`unavailable`/`degraded`/`healthy`) →
`unknown`.

**Routing eligibility (all must hold):** `connection_state = connected`, `health_state ∈
{healthy, degraded, unknown}`, an `active` credential exists and is not expired, and no active
cooldown at the relevant scope. Anything else ⇒ ineligible with a typed reason
(`account_stopped`, `account_disconnected`, `credential_expired`, `account_unavailable`,
`cooling_down`, `reauth_in_progress`).

**Invalid transitions** (rejected, never silently ignored): `disconnected → connected` without
re-enrollment; any `health_state` change while `connection_state ≠ connected`; setting
`health_state = healthy` while the active credential is expired. A rejected transition emits an
`audit_event` and leaves state unchanged.

**Persistence & recovery:** both axes persist across restart; `reauth_in_progress` is cleared by
startup reconciliation (a crashed reauth leaves the active credential intact — see Credential
above). Every axis change emits an `audit_event`.

### Credential (encrypted secret for an account)
- `id`, `account_id`, `provider_id`, `kind` ∈ `{api_key, oauth2, github_oauth, copilot_service}` plus
  provider-specific kinds as needed. An account may have **multiple active credentials of different kinds**.
- `fingerprint_sha256` (dedup without storing the secret), `key_id` (which keyring version),
  `nonce`, `ciphertext` (the encrypted envelope — token(s) and any custom header values live here,
  never in `settings_json`), `expires_at` (nullable = no known expiry; set from an OAuth
  access-token TTL when the provider returns one).
- `state` ∈ `{active, staged, retired}` — staged credentials are validated before activation;
  retired credentials are superseded by a newer credential of the same kind and kept for audit
  (`retired_at`).
- **Canonical cardinality invariant — one active credential per `(account_id, credential_kind)`.**
  An account may simultaneously hold **multiple active credentials of *different* kinds** (e.g. a
  GitHub OAuth credential *and* a Copilot service credential on one `github-copilot` account),
  but never two active credentials of the *same* kind. OAuth `access_token` / `refresh_token` that
  belong to the same OAuth flow are stored inside one encrypted envelope (one credential row, one
  kind) and are not two credentials. The schema column is named `kind`; "credential kind" and
  `kind` are the same axis.
- During **reauthentication** (OAuth same-identity reconnect), the new credential is first
  **staged** (`state = 'staged'`) while the old one remains `active`. After validation, an atomic
  transaction updates the old to `retired` and the new to `active`. On failure, the staged
  credential is discarded without affecting the active one.
- **Staged cardinality — deterministic rule:** **at most one `staged` credential per
  `(account_id, credential_kind)`.** A second reauthentication for the same account+kind while one
  is already staged is rejected with `reauthentication_in_progress` (it does **not** create a
  second staged row). This keeps the staging swap unambiguous — there is always exactly one
  candidate to promote — and is enforced structurally by the partial-unique index
  `idx_cred_staged_per_kind` (see §5 DDL), mirroring the active-per-kind rule.
- **Uniqueness (partial indexes, see §5 DDL):** `idx_cred_active_per_kind` on
  `(account_id, kind) WHERE state='active'` enforces the active-per-kind invariant and
  `idx_cred_staged_per_kind` on `(account_id, kind) WHERE state='staged'` enforces the
  one-staged-per-kind rule (an active and a staged row of the same kind coexist during
  reauthentication; retired rows never conflict); `idx_cred_fingerprint` on
  `(provider_id, fingerprint_sha256) WHERE state != 'retired'` dedups live secrets without blocking
  re-use of a previously-retired one.
- **Rotation / reauthentication transaction (atomic swap):** stage → validate → in one
  transaction set old `active → retired` (stamp `retired_at`) and staged `staged → active`. Keyed
  by the single-use OAuth `state` (or an advisory lock), the swap is **idempotent** — replaying it
  after the old row is already retired is a no-op. See [03 §2e](03-provider-integration-catalog.md#2e-oauth-reauthentication-same-identity-reconnect).
- **Revocation & expiry:** after a successful swap, the retired credential is best-effort revoked
  at the provider (never blocking). `expires_at` in the past marks a credential due for refresh;
  an expired OAuth credential drives the account's **health axis** to `expired` (see Account below),
  not a hard delete.
- **Interruption recovery:** a crash mid-reauthentication leaves **exactly one** `staged` row
  (the one-staged-per-kind rule above) and the original `active` row intact (the swap is a single
  transaction). Startup reconciliation discards any `staged` row older than the OAuth transaction
  TTL; the active credential is never affected by an incomplete staging.

### Funding evidence (append-only)
- `id`, `account_id`, `funding` ∈ `{free, paid, unknown}`,
  `source` ∈ `{provider_policy, provider_evidence, owner_policy, owner_override}` (canonical
  vocabulary — see §2), `confidence`, `evidence_json`
  (sanitized), `reason`, `observed_at`, `superseded_at` (NULL = current). Partial-unique index
  guarantees one current row per account.

### Canonical model
- `id`, `canonical_key_sha256` (unique, = SHA-256 of `(provider_id, provider_model_id)`),
  `display_name`. **Provider-scoped identity:** two providers returning the same display name
  remain two canonical models. Cross-provider equivalence is **not** supported in v1.
- `native_context_tokens INTEGER` (nullable, = unknown), `native_modalities_json TEXT`
  (nullable, = unknown), `quality_rating REAL` (nullable, = not rated, 0–100 scale) — native
  facts about the model version itself, discovered once per canonical identity.
  `quality_rating` is **never an absolute truth** — it carries a documented source (benchmark /
  probe / owner override), observed date, and confidence, stored as part of the evidence trail.
  A missing rating (`NULL`) does not disqualify a model; it simply means no quality signal is
  available for ranking.
- `provider_model_alias(provider_id, provider_model_id) → model_id` is the sole exact identity map.

### Offering — `(provider, account, model)` · routable unit = **offering-operation** `(…, operation)`
- **Offering** identity = `(account_id, provider_model_id)`. Per account+model:
  `availability` ∈ `{available, withdrawn, unknown}`,
  `context_length`, `max_input_tokens`, `max_output_tokens`, `capabilities` (a normalized set),
  `pricing`, `lifecycle`, `first_seen_at`, `last_seen_at`. The same model on two accounts is two
  distinct offerings. **All Offerings under an account inherit the account's funding classification.**
  No per-Offering funding override exists in v1.
- **Offering-operation** (per operation: chat / streaming / tools / structured_output / vision /
  context_window; `image_generation` is a recognized operation **reserved for future scope** —
  certifiable but not routed by any V1 tier, see [05 §9](05-tier-engine.md#9-future-scope-non-v1)):
  an independent **certification** record and evidence.
  **Amendment (2026-08-05, bounded additive vocabulary unfreeze):** `reasoning` joins the operation
  vocabulary (eighth value) on the same terms as `image_generation` — recognized and certifiable
  (a provider may declare it, e.g. claude-code's official `capabilities.reasoning`), but **reserved
  for future scope**, not routed by any V1 tier; see [05 §9](05-tier-engine.md#9-future-scope-non-v1).
  This 4-tuple is the only routable and certifiable unit; chat and vision on one offering are two
  offering-operations.

### Quota — the multi-window model (budgets/sources · windows · reservations · allocations)

An account is **never** described by a single row per unit. Real providers expose **several
concurrent budgets at once** — e.g. Claude Code's 5-hour *and* 7-day usage windows, a
request-per-minute *and* a token-per-minute limit, a provider credit balance — and Venom adds its
own **local routing-safety budget**. All of these are modeled as first-class **windows**, and one
execution reservation may debit **many windows atomically**. This replaces the old single-row
`quota_budgets` / `UNIQUE(account_id, unit)` assumption entirely.

Four concepts (normalized, planning-level):

1. **Quota source (budget origin).** *Where a budget's authority comes from,* one of
   `provider_evidence` (authenticated provider quota data), `local_safety` (Venom's own
   routing-safety policy — see below), or `owner_override`. Provider evidence, local safety policy,
   and owner overrides are **never conflated**: each window carries its `source` explicitly, and a
   `local_safety` limit is never presented as provider evidence (and vice-versa).
2. **Quota window.** One concurrently-tracked budget dimension for an account. **Window identity =
   `(account_id, source, unit, window_type, window_key)`** — so multiple windows for the same
   account *and the same unit* legitimately coexist (e.g. two `requests` windows: one `rpm`, one
   `rolling_7d`). **`window_key` is `NOT NULL`:** a stable provider-supplied identifier when
   available, otherwise a deterministic synthetic key (canonical normalization rule in the §3 DDL —
   never rely on SQLite NULL-uniqueness for window identity). Each window carries its **own**
   `used` / `remaining` / `reserved` / `reset_at` / freshness state.
3. **Reservation (per execution attempt).** A reservation is associated with an **execution
   attempt** (`request_id`, `attempt_id`), **not** with a single budget. No attempt executes before
   its reservation succeeds.
4. **Reservation allocations.** A reservation holds one **allocation** per applicable window — the
   amount debited from that specific window. A single reservation therefore allocates across
   *several* windows in one atomic operation.

#### Local routing-safety budget (mandatory for every account)

**Unknown provider quota does not mean unlimited, and the router never skips a reservation.** Every
connected account — including accounts whose provider exposes **no** quota or usage endpoint — owns
a `local_safety` source with, at minimum:
- a **concurrency** window (`unit = concurrency`) capping concurrent in-flight attempts, and
- an **estimated-local-consumption** window (e.g. `unit = requests` or `unit = input_tokens` over a
  rolling duration) capping estimated local consumption,

both bounded by **owner policy** defaults. An account with unknown provider quota may still be
*eligible* (with the documented Pro/Max scoring penalty — see [04 §5](04-model-intelligence.md#5-certification)),
but **execution still requires a successful reservation against its local-safety windows.** The
local-safety budget is authoritative local policy, never fabricated provider evidence.

#### Estimated consumption — canonical internal dimensions

Pre-execution estimated consumption is expressed in **canonical internal dimensions**, never
fabricated provider percentages. At minimum an attempt estimates, and reserves against every
*applicable measurable* window in:
- `requests` — always `1` per attempt;
- `input_tokens` — derived from the normalized request (message + tool-schema token count);
- `output_tokens` — the request's `max_tokens` when supplied, else a **conservative owner-policy
  default**;
- `concurrency` — always `1` per attempt (against the local-safety concurrency window);
- `credits` / `balance` — **only** where a **verified conversion rule** exists for that provider;
  tokens are **never** converted into provider credits without one.

Provenance: each estimate carries whether it came from the request (`from_request`), a provider
conversion rule (`provider_conversion`), or a conservative default (`policy_default`). If a provider
**balance/credit** window cannot be estimated safely (no verified conversion), the attempt relies on
the **local-safety** budget for admission and the credit/balance window is **reconciled against
provider evidence after execution** (see [05 §4](05-tier-engine.md#4-quota--consumption-accounting)),
rather than blocking on a guessed number.

#### Atomic reservation contract (across all applicable windows)

Reservations **must** be atomic to prevent concurrent overcommit (the old build's #6 bug) **and**
must never partially reserve. The mechanism is a **conditional UPDATE with a version field per
window**, executed for **every applicable window inside one `BEGIN IMMEDIATE` transaction** — never
a read-then-write split:

```sql
-- For EACH applicable window of this attempt, within ONE transaction:
UPDATE quota_windows
SET reserved = reserved + :estimated_cost,
    version  = version + 1
WHERE id = :window_id
  AND version = :expected_version
  -- capacity = remaining (provider evidence) or limit_value (local_safety/owner) when remaining is unknown
  AND COALESCE(remaining, limit_value) - reserved >= :estimated_cost;
```

- **All** window UPDATEs affect exactly 1 row → insert the reservation + its allocation rows →
  **COMMIT** → proceed to execute.
- **Any** window UPDATE affects 0 rows (headroom insufficient or version changed) → **ROLLBACK the
  whole transaction** (no window is left debited) → the attempt is **rejected before provider
  execution**; re-evaluate the candidate pool from a fresh snapshot. Do **not** blindly retry the
  same route.

So "failure to reserve against **any** required applicable window rejects that attempt" is enforced
structurally by the all-or-nothing transaction.

**Canonical reservation state machine (stored states — exactly five):**

```
reserved | reconciliation_pending | settled | released | unknown_consumption
```

`expires_at` is a **processing deadline, not a terminal state** — there is **no** stored `expired`
reservation state. A reservation with possible provider consumption **never silently releases
quota headroom**.

| Transition | Trigger | Owner |
|---|---|---|
| `reserved → settled` | Confirmed success; actual per-window costs known. | `routing`/`quota` reconcile txn |
| `reserved → released` | **Only** when execution never left Venom (abandoned pre-dispatch, incl. pre-dispatch deadline expiry) **or** the provider proves no consumption occurred. | reconcile txn / janitor |
| `reserved → reconciliation_pending` | Request was dispatched and the outcome is ambiguous (timeout after send, stream abort without a usage trailer, ambiguous 5xx, crash after dispatch). | reconcile txn / startup recovery |
| `reconciliation_pending → settled` | Reconciliation confirms actual usage, **or** settles the conservative estimate with `confidence = low`. | reconciliation worker |
| `reconciliation_pending → released` | **Only** with provider evidence that the request never consumed. | reconciliation worker |
| `reconciliation_pending → unknown_consumption` | Terminal retry boundary reached without resolution; emits a `usage_gap` audit event; the account is re-baselined at the next authoritative quota sync. | reconciliation worker / janitor |

All other transitions are **invalid** (rejected, audited, state unchanged): `settled`, `released`,
and `unknown_consumption` are terminal; `reconciliation_pending` never returns to `reserved`; and
**no path auto-releases a `reconciliation_pending` reservation merely because `expires_at`
passed.** Every transition emits an `audit_event`, updates **all** of the reservation's window
allocations consistently, and is idempotent (`reserve`/`settle`/`release`/transition calls repeated
with the same `reservation_id` have the effect of one call). While a reservation is
`reconciliation_pending`, **every affected allocation remains reserved** (headroom stays debited).

**Janitor / startup reconciliation — distinct branches, never keyed on `state = 'reserved'`
alone.** The attempt's `dispatched_at` marker (stamped before the provider call) distinguishes them:
1. **`reserved` past `expires_at`, never dispatched** (`dispatched_at IS NULL`) → transition to
   `released`, free all allocations, record an audit event.
2. **`reserved` past `expires_at`, dispatched** (crash between dispatch and reconcile) → transition
   to `reconciliation_pending` (crash recovery) — never to `released`.
3. **`reconciliation_pending` whose retry deadline / lease expired** → reclaim and re-enqueue the
   reconciliation work; allocations are **never freed silently**; once the terminal retry policy
   ([05 §4](05-tier-engine.md#4-quota--consumption-accounting)) is exhausted → `unknown_consumption`
   (+ `usage_gap` audit event).

**Contention and concurrency:** SQLite `busy_timeout=5000` and `BEGIN IMMEDIATE` on the reservation
transaction ensure linearizable writes across all windows. Under high contention the second request
waits, re-reads, and may find insufficient headroom on one of the windows.

**Schema** — the normalized multi-window tables (replacing `quota_budgets`):
```sql
-- One row per concurrently-tracked budget dimension. Multiple windows per (account, unit) are
-- normal — window identity includes the source, window type, and provider window key.
CREATE TABLE quota_windows (
    id            TEXT PRIMARY KEY,
    account_id    TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    source        TEXT NOT NULL,             -- provider_evidence | local_safety | owner_override
    unit          TEXT NOT NULL,             -- requests | input_tokens | output_tokens | tokens | credits | balance | percent | concurrency
    window_type   TEXT NOT NULL,             -- rolling_5h | rolling_7d | rpm | tpm | balance | concurrency | provider window key
    window_key    TEXT NOT NULL,             -- canonical identity token — NEVER NULL (normalization rule below; no reliance on SQLite NULL-uniqueness)
    duration_seconds INTEGER,                -- window length; NULL for non-time-boxed (balance, concurrency)
    used          REAL,                      -- nullable = unknown (never 0-as-unknown)
    remaining     REAL,                      -- nullable = unknown (provider evidence)
    total         REAL,
    reserved      REAL NOT NULL DEFAULT 0,   -- locked by active reservation allocations
    limit_value   REAL,                      -- authoritative ceiling for local_safety / owner_override windows
    reset_at      INTEGER,                   -- unix seconds; NULL = no reset (e.g. balance)
    version       INTEGER NOT NULL DEFAULT 1, -- optimistic concurrency counter (per window)
    confidence    REAL NOT NULL,
    freshness_state TEXT NOT NULL,           -- fresh | stale | unknown
    observed_at   INTEGER NOT NULL,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL,
    UNIQUE(account_id, source, unit, window_type, window_key)   -- window identity (all columns NOT NULL)
);
-- window_key canonical normalization (deterministic — same inputs always produce the same key):
--   form: "<namespace>:<token>", namespace ∈ {provider, rolling, local}; token lowercase snake_case.
--   * provider-supplied identifier available → "provider:" + normalize(id)  (trim, lowercase,
--     non-alphanumerics → "_"), e.g. provider:five_hour, provider:seven_day
--   * time-boxed window with no provider identifier → "rolling:<duration_seconds>s",
--     e.g. rolling:60s, rolling:3600s
--   * non-time-boxed local-safety window → "local:<unit>", e.g. local:concurrency,
--     local:estimated_tokens
-- Adapters may return an empty provider key; Venom ALWAYS normalizes to one of these synthetic
-- keys before persistence — window_key is never NULL and never "".

-- A reservation belongs to an execution ATTEMPT, not to one window.
-- Stored states are EXACTLY the five below; `expires_at` is a processing deadline, not a state
-- (there is no stored `expired` reservation state).
CREATE TABLE quota_reservations (
    id              TEXT PRIMARY KEY,         -- reservation_id = f(request_id, attempt_id)
    account_id      TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    request_id      TEXT NOT NULL,
    attempt_id      TEXT NOT NULL,
    state           TEXT NOT NULL
                    CHECK (state IN ('reserved','reconciliation_pending','settled',
                                     'released','unknown_consumption')),
    dispatched_at   INTEGER,                  -- stamped before the provider call; NULL = never dispatched (janitor branch discriminator)
    expires_at      INTEGER NOT NULL,         -- processing deadline (default now + 30 s) — never a terminal state
    created_at      INTEGER NOT NULL,
    settled_at      INTEGER,
    UNIQUE(request_id, attempt_id)            -- idempotency key
);

-- One reservation → many window allocations. This is how a single attempt reserves atomically
-- across several applicable windows.
CREATE TABLE quota_reservation_allocations (
    reservation_id  TEXT NOT NULL REFERENCES quota_reservations(id) ON DELETE CASCADE,
    window_id       TEXT NOT NULL REFERENCES quota_windows(id),
    unit            TEXT NOT NULL,            -- the dimension reserved on this window
    estimated_cost  REAL NOT NULL,            -- reserved on this window
    estimate_source TEXT NOT NULL,            -- from_request | provider_conversion | policy_default
    actual_cost     REAL,                     -- set at settle
    state           TEXT NOT NULL,            -- reserved | settled | released | unknown_consumption (mirrors the reservation outcome per window; all allocations of one reservation move together)
    PRIMARY KEY(reservation_id, window_id)
);
```

### Tier policy
- `tier` ∈ `{venom/lite, venom/pro, venom/max}`, hard gates, ranking weights, per-tier free/paid
  distribution policy (Lite free-only; Pro ~25/75 mix target; Max no funding-mix target — quota-fair),
  fallback depth. Owner-tunable within validated bounds. (Defined in [05-tier-engine](05-tier-engine.md).)

### Owner authentication (single-owner identity, session, re-verification)

Venom is a **single-owner local application**. There is exactly **one owner identity** — no users,
teams, roles, invitations, or RBAC (this does not add cloud identity or OS identity; it is a local
password). The control plane's loopback + Host-header gates ([01 §6a](01-architecture.md#6a-control-plane-owner-ui--control-api))
remain mandatory but are **network** defenses that do **not** replace owner authentication. The full
request/response/error/expiry contract lives in [09 §5](09-control-api.md#5-owner-authentication-first-run-login-session-re-verification);
the persisted entities are:

- **Owner secret (`owner_auth`, exactly one row):** `password_hash` (**Argon2id** only), per-install
  random `salt`, documented KDF parameters (`time`, `mem_kib`, `threads`, `key_len`), `created_at`,
  `updated_at`. **Only the hash is stored — never the password.** Created once at first run.
- **Owner session (`owner_sessions`):** `id` (opaque, high-entropy server-side handle; only a
  hash/verifier of the cookie value is stored), `created_at`, `last_seen_at`, `absolute_expires_at`,
  `idle_expires_at`, `revoked_at` (nullable), and a `reverify_fresh_until` timestamp used for the
  5-minute re-verification freshness window. The browser holds only an **HttpOnly, SameSite=Strict**
  cookie (Secure when transport permits); the session is opaque and server-side.
- **CSRF:** every mutating control-plane request carries a **session-bound** CSRF token validated
  against the session (see [09 §5.4](09-control-api.md#5-owner-authentication-first-run-login-session-re-verification)).
- **Auth attempt audit (`auth_events`):** append-only records of login / logout / re-verification /
  lockout outcomes (result, timestamp, reason code) — **no secrets, no password material** — feeding
  rate limiting and the audit trail.

Credential reveal and similarly sensitive control operations require a **password re-verification**
whose success stamps `reverify_fresh_until = now + 5 min` on the session; re-verification never
creates a new account or a separate long-lived session.

### Audit / routing records
- `route_decision` (candidate selection + exclusion reasons, no secrets),
  `route_attempt` (per-attempt provider/account/model, latency, normalized status),
  `usage_record`, `audit_event`. Only IDs, codes, scores, statuses — never prompts, responses,
  raw errors, or identifiers that are secrets.

---

## 4. How the entities relate

```
Provider (1) ──< Account (N) ──< Credential (N active, 1 per kind)
   │                  │
   │                  ├──< FundingEvidence (append-only, 1 current)
   │                  ├──< QuotaWindow (N: provider + local_safety) ──< ReservationAllocation
   │                  ├──< Reservation (per attempt) ──< ReservationAllocation
   │                  └──< Offering (N)  ─────────────┐
   │                                                   │
CanonicalModel (1) ──< ProviderModelAlias (N) ────────┘
                                                       │
                                              per Operation:
                                              Certification + Evidence

Tier(policy) ──selects──▶ Offering(operation)   [routing time]
VenomAPIKey  ──authorizes──▶ inference request
```

Key invariants restated as relationships:
- Funding hangs off **Account**, resolved onto **Offering** — never off Provider.
- Discovery/quota/health/certification are all keyed by **Account** (or Offering), never by
  Provider alone.
- The only thing routing may execute is a **certified Offering-operation** whose account is
  **routing-eligible** (per the Account lifecycle axes above), funded appropriately for the tier,
  and has quota.

---

## 5. SQLite schema sketch

Illustrative DDL for the core tables (elide indexes/checks for brevity; funding, quota,
certification, and audit tables follow the entity fields above).

```sql
CREATE TABLE providers (
  id            TEXT PRIMARY KEY,           -- slug
  display_name  TEXT NOT NULL,
  description   TEXT,
  auth_mode     TEXT NOT NULL,             -- oauth2 | api_key | custom_openai
  base_url      TEXT,
  settings_json TEXT,                      -- endpoints/header names only (values stored in encrypted credential envelope; see §2c header security rule)
  funding_mode  TEXT NOT NULL,             -- fixed | owner_policy | provider_evidence | evidence_required
  funding_fixed TEXT,                      -- free | paid | unknown (when mode=fixed)
  funding_locked        INTEGER NOT NULL DEFAULT 0,
  funding_non_expiring  INTEGER NOT NULL DEFAULT 0,
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL
);
-- Provider capabilities are DERIVED from which adapters are registered, not stored as truth.

CREATE TABLE accounts (
  id            TEXT PRIMARY KEY,
  provider_id   TEXT NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
  external_id   TEXT NOT NULL,
  display_name  TEXT,
  auth_type     TEXT NOT NULL,             -- api_key | oauth2
  connection_state TEXT NOT NULL DEFAULT 'connecting'
                CHECK (connection_state IN ('connecting','connected','stopped','disconnected')),
  health_state  TEXT NOT NULL DEFAULT 'unknown'
                CHECK (health_state IN ('unknown','healthy','degraded','unavailable','expired')),
  reauth_in_progress INTEGER NOT NULL DEFAULT 0,
  identity_email TEXT,
  identity_plan  TEXT,
  last_health_check_at INTEGER,
  last_health_error    TEXT,
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL,
  UNIQUE(provider_id, external_id),
  UNIQUE(id, provider_id)                  -- composite target for child FKs
);
-- display_status is DERIVED at read time (see §3 Account lifecycle) — never a stored column.

CREATE TABLE account_credentials (
  id            TEXT PRIMARY KEY,
  account_id    TEXT NOT NULL,
  provider_id   TEXT NOT NULL,
  kind          TEXT NOT NULL,             -- api_key | oauth2 | github_oauth | copilot_service
  state         TEXT NOT NULL DEFAULT 'active'
                CHECK (state IN ('active','staged','retired')),
  fingerprint_sha256 TEXT NOT NULL,
  key_id        TEXT NOT NULL,             -- keyring version
  nonce         BLOB NOT NULL,
  ciphertext    BLOB NOT NULL,             -- encrypted envelope: token(s) + any custom header values
  expires_at    INTEGER,                   -- nullable = no known expiry (e.g. OAuth access-token expiry)
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL,
  retired_at    INTEGER,                   -- set when state → retired (audit)
  FOREIGN KEY(account_id, provider_id) REFERENCES accounts(id, provider_id) ON DELETE CASCADE
);
-- Canonical invariant: one ACTIVE credential per (account, kind). Different kinds coexist
-- (e.g. github_oauth + copilot_service on one account); a staged replacement coexists with the
-- active one during reauthentication; retired rows never conflict. (SQLite partial index — the
-- reason this cannot be an inline UNIQUE constraint.)
CREATE UNIQUE INDEX idx_cred_active_per_kind
  ON account_credentials(account_id, kind) WHERE state = 'active';
-- Deterministic rule: at most ONE staged credential per (account, kind) during reauthentication.
CREATE UNIQUE INDEX idx_cred_staged_per_kind
  ON account_credentials(account_id, kind) WHERE state = 'staged';
-- Secret-fingerprint dedup, scoped to live credentials so a retired secret does not block re-use.
CREATE UNIQUE INDEX idx_cred_fingerprint
  ON account_credentials(provider_id, fingerprint_sha256) WHERE state != 'retired';

CREATE TABLE account_funding_evidence (
  id            TEXT PRIMARY KEY,
  account_id    TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  funding       TEXT NOT NULL,             -- free | paid | unknown
  source        TEXT NOT NULL,             -- provider_policy | provider_evidence | owner_policy | owner_override
  locked        INTEGER NOT NULL DEFAULT 0, -- 1 when this row derives from a locked provider_policy (override rejected)
  confidence    REAL NOT NULL,
  evidence_json TEXT,                      -- sanitized
  reason        TEXT,
  observed_at   INTEGER NOT NULL,
  superseded_at INTEGER
);
CREATE UNIQUE INDEX idx_funding_current
  ON account_funding_evidence(account_id) WHERE superseded_at IS NULL;

CREATE TABLE models (            -- canonical, provider-scoped
  id            TEXT PRIMARY KEY,
  canonical_key_sha256 TEXT NOT NULL UNIQUE,
  display_name  TEXT,
  native_context_tokens INTEGER,    -- nullable = unknown
  native_modalities_json TEXT,       -- nullable = unknown, JSON array of modality tokens
  quality_rating REAL,              -- nullable = not rated, 0-100 scale; always carries source+date+confidence in evidence trail
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL
);

CREATE TABLE provider_model_aliases (
  provider_id       TEXT NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
  provider_model_id TEXT NOT NULL,
  model_id          TEXT NOT NULL REFERENCES models(id) ON DELETE CASCADE,
  PRIMARY KEY(provider_id, provider_model_id)
);

-- Offering identity = (account_id, provider_model_id)
-- Every Offering inherits its parent account's funding; no per-Offering override in v1.
CREATE TABLE account_model_offerings (
  account_id        TEXT NOT NULL,
  provider_id       TEXT NOT NULL,
  provider_model_id TEXT NOT NULL,
  model_id          TEXT NOT NULL REFERENCES models(id),
  availability      TEXT NOT NULL,         -- available | withdrawn | unknown
  context_length    INTEGER,               -- nullable = unknown, never 0-as-unknown
  max_input_tokens  INTEGER,
  max_output_tokens INTEGER,
  capabilities_json TEXT,                  -- JSON array of normalized capability tokens
  pricing_json      TEXT,
  lifecycle_json    TEXT,
  first_seen_at     INTEGER NOT NULL,
  last_seen_at      INTEGER NOT NULL,
  UNIQUE(account_id, provider_model_id),  -- offering identity
  FOREIGN KEY(account_id, provider_id) REFERENCES accounts(id, provider_id) ON DELETE CASCADE
);

-- offering_operations(provider_id, account_id, provider_model_id, operation, ...)
-- certifications(offering_operation_id, status, version, certified_at, evidence_ref, ...)
--   status ∈ {discovered, observed, probing, certified, suspended, expired} — exactly six; there
--   is NO `rejected` status. Capability truth {unknown, supported, unsupported} is a separate
--   column/dimension (canonical machine + transition table: 04 §5).
-- quota_windows / quota_reservations / quota_reservation_allocations / cooldowns
-- owner_auth (single-owner password hash + params) / owner_sessions (see 09 §5)
-- venom_api_keys / usage_records / route_decisions / route_attempts / audit_events
```

**Modeling rules baked into the schema:**
- Nullable numerics mean *unknown* — never store `0` to mean "we don't know."
- `capabilities_json` is a normalized, sorted, de-duplicated token array — never inferred from
  the model name.
- Everything downstream keys on `account_id` (offerings, funding, quota, health), enforcing
  account-scoping structurally.
- Append-only evidence tables; a single "current" row enforced by partial-unique indexes.
