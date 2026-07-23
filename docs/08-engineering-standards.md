# 08 — Engineering Standards (drift-prevention & extensibility)

How Venom Router is built so it stays professional, coherent, and easy to extend — instead of
drifting into the 75+-issue state the old build reached. This doc is the **process contract**: the
layering rules, the definition of done, the automated gates, and the extension points. Where
[07-design-system](07-design-system.md) prevents *visual* drift, this doc prevents *architectural*
drift. Both are enforced by CI, not by memory.

The old repo's own audit is the cautionary evidence throughout: three duplicated execution engines
that diverged, slug-string branching, a committed OAuth secret, unverified OAuth `state`, non-atomic
quota checks, silently swallowed billing writes. Every rule below maps to a failure we refuse to
repeat.

---

## 1. Guardrail principles

1. **The invariants are executable, not aspirational.** Each README principle has at least one test
   or lint that fails the build when violated (see §7). A principle with no gate is a gap to close.
2. **One way to do each thing.** One execution path, one router, one persistence layer, one design
   system, one HTTP error envelope. Duplication of a core mechanism is the specific rot that killed
   the old build; a second copy is a defect.
3. **Types over strings.** Behavior is selected by typed capability/policy, never by `switch` on a
   provider slug or a model name. Illegal states are unrepresentable where the type system allows.
4. **Pure core, imperative shell.** Domain logic (`providers`, `accounts/domain`, `models`, `routing`) is
   pure and dependency-light; I/O (SQLite, HTTP, Bifrost, keyring) lives at the edges and is injected.
5. **Fail closed, everywhere.** Unknown ⇒ ineligible/rejected. This is a coding default, not just a
   routing rule: unhandled cases return typed errors, they don't guess.
6. **Secrets never leak — provably.** A canary test asserts no injected secret appears in any log,
   error, trace, or audit row. Sanitization is a package, applied at the boundary, tested.
7. **Change is test-first and gated.** Every behavior change lands with tests; every phase ends at a
   demonstrable gate ([06-roadmap](06-roadmap.md)); nothing merges red.
8. **Extensibility is designed, not hoped for.** Adding a provider, a theme, an operation, or a
   dashboard surface follows a documented recipe that touches known extension points and nothing else.

---

## 2. Repository & module layering

The package layout and its acyclic dependency rule are defined in
[01-architecture](01-architecture.md) §3. The standards that keep it intact:

- **Dependency direction is enforced by a test.** An import-graph check fails CI on any cycle or any
  forbidden edge (`providers`/`accounts/domain`/`models` importing `storage` or `database/sql`; `providers`
  importing `accounts`/`models`; `accounts/application` importing infrastructure directly; domain importing `httpapi`). The diagram is not a suggestion.
- **Pure domain packages** contain no I/O, no clock, no randomness, no global state — those are
  injected interfaces, so the domain is unit-testable without a database or network.
- **`storage` implements interfaces the domain owns.** Repositories are the only place SQL exists.
  Handlers, routing, and Bifrost never issue ad-hoc SQL (an anti-pattern from the old build).
- **The dashboard is its own workspace** with the design-system package (07) as a dependency; it
  talks to the app only through the typed control API. UI and core evolve behind that contract.
  **The Venom Design System is implemented and approved** as the versioned `Design_System/`
  package. Application consumption begins in Phase P2a. P0 and P1 must neither rebuild nor modify
  it.
- **`third_party/bifrost` is vendored, never edited in product branches.** Execution seams are added
  upstream and pulled via submodule SHA; a check flags local modifications to vendored code.

---

## 3. Coding standards

- **Language/tooling:** Go 1.26+, `CGO_ENABLED=0`. `gofmt`/`goimports` clean; `go vet` +
  `golangci-lint` (staticcheck, errcheck, ineffassign, gocyclo, forbidigo) green. Dashboard:
  TypeScript `strict`, ESLint, Prettier, no `any` without justification.
- **Bootstrap tool versions:** the verified Windows baseline is Go `1.26.5`, `goimports` from
  `golang.org/x/tools@v0.48.0`, `golangci-lint` `v2.12.2`, and Task `3.52.0`. P0-FND-001 records
  repository-level Go/tool pins and the Taskfile so a global workstation install is never the sole
  source of reproducibility. Long-running shells and desktop apps must be restarted after PATH
  changes, then all four commands must resolve by name before P0-FND-001 begins.
- **Error handling:** wrap with context (`%w`); never discard an error silently (the old build
  swallowed usage/billing writes — errcheck + a review rule forbid it). Public failures use the one
  stable envelope `{ error: { code, message, request_id, retryable } }` — no raw provider errors, no
  secrets.
- **No forbidden constructs:** `forbidigo` bans `panic` in request paths, raw `fmt.Print*` logging,
  and direct `os.Getenv` outside `config`. A custom vet check bans `switch` on provider slug strings
  in `routing`/`execution`.
- **Concurrency:** shared state is guarded or owned by one goroutine; the race detector runs in CI.
  Quota reservation and certification transitions use short transactions and never hold a write txn
  across a provider HTTP call (the old build's overcommit bug).
- **Time, IDs, randomness:** injected, never called ambiently in the domain, so logic is
  deterministic and testable.
- **Comments explain *why*.** Public APIs are documented; invariants are annotated with the principle
  they uphold.

---

## 4. Data & migrations

- **SQLite is the source of truth**; access only through typed repositories (see
  [02-domain-model](02-domain-model.md) for the schema).
- **Migrations** are embedded, checksum-guarded, integrity-checked on open, and **rollback-tested**
  in CI. Forward-only in production; every migration has a tested down path in dev. LF line endings
  forced so checksums are stable across OSes.
- **Modeling rules are enforced:** nullable numerics mean *unknown* (never `0`-as-unknown);
  append-only evidence tables keep exactly one current row via partial-unique indexes; everything
  downstream keys on `account_id`. A schema-lint/test asserts these where feasible.
- **Backup/restore contract (portable encrypted backup):** see §9 for the full specification.
  The old pattern of `venom.db` + keyring treated as plaintext backup is **never** used —
  portable backup is always a single encrypted container.

---

## 5. Testing strategy

- **Unit** — pure domain logic (routing gates, scoring, precedence, funding transitions,
  certification lifecycle) tested exhaustively with table-driven cases; no network/DB. Includes the
  **deterministic Cartesian certification test** ([04 §5](04-model-intelligence.md#5-certification)):
  all 6 certification states × 3 capability truths assert exactly (`certified`, `supported`)
  routable and every invalid transition (including any `rejected` state — which does not exist)
  rejected + audited.
- **Repository/integration** — repositories against a real temp SQLite; migration up/down;
  concurrency tests proving no quota overcommit **on any window** and that a reservation is
  **all-or-nothing across all applicable windows** (provider + local-safety); the **six
  deterministic no-leak/no-double-charge reservation tests** ([02 §3](02-domain-model.md#quota--the-multi-window-model-budgetssources--windows--reservations--allocations),
  [06 Phase 3b](06-roadmap.md#phase-3b--quota--consumption-accounting)): pre-dispatch expiry →
  released; post-dispatch ambiguity → `reconciliation_pending` never auto-released; reconciliation
  success settle; proven no-consumption release; terminal `unknown_consumption` + `usage_gap`;
  worker crash / lease recovery — all idempotent across every allocation; the deterministic
  NOT-NULL `window_key` normalization; credential active-per-kind and one-staged-per-kind
  partial-unique indexes; owner-auth session/CSRF/re-verification lifecycle.
- **Owner authentication** — setup-once; generic `invalid_credentials`; login/re-verify rate-limit +
  lockout with audit rows that store **no** secret (canary); idle (30 min) and absolute (12 h)
  session expiry; session-bound CSRF rejected before any side effect; reveal gated on ≤ 5-min
  re-verification; password change revokes all sessions; restore re-establishes the owner password
  ([09 §5.9](09-control-api.md#5-owner-authentication-first-run-login-session-re-verification)).
- **Adapter contract tests** — each provider adapter tested against recorded fixtures for identity,
  discovery, quota, refresh, and the authentic-validation rule (a 200-for-any-token endpoint must
  still be caught). These offline fixture tests are **CI-blocking**. **Live re-verification and
  real-account validation are separate, recorded, non-CI-blocking steps** — a test requiring a live
  provider or real credential must never become a flaky universal CI gate (verification tiers in
  [03 §5.1](03-provider-integration-catalog.md#51-verification-tiers-what-is-a-ci-gate-vs-what-is-manual-evidence)).
- **Execution seam** — a smoke test round-trips a chat through embedded Bifrost against a local fake
  server, proving Venom→Bifrost hands off exactly one route.
- **Security canary** — injected secrets must never appear in any output; runs every build.
- **End-to-end gates** — each roadmap phase's gate is an automated scenario with **deterministic
  thresholds** ([06-roadmap](06-roadmap.md)): Phase 4 — `lite` **zero** paid selections and
  fail-closed; `pro` mix converges within **±5 pp** of 25% paid over **N = 2,000** requests across
  the standard workload buckets without promoting weak routes (competitive quality band: **Pro
  ≤ 0.08 / Max ≤ 0.03** below the top score, never auto-widened — [05 §2 Step 6](05-tier-engine.md#2-per-request-selection-algorithm));
  `max` proves **quota-fairness + quality-band** behavior (**not** any 50/50 funding ratio). Phase 8 — sustained-load readiness
  (**≥ 30 min**, **≥ 20 RPS / ≥ 20 concurrent**, internal error rate **≤ 0.5%**, p95 routing
  overhead reported, **zero** invariant violations). These are conservative planning contracts that
  may be tightened later without changing architecture.
- **Dashboard** — component stories (all states), visual regression per theme+density, axe a11y
  checks, and a few Playwright flows for the critical paths (connect account, view fleet, read a
  route explanation).
- **Cross-platform** — the full suite runs on **Windows and Linux** every phase (the primary target
  is Windows/amd64; Linux/WSL must also pass).
- **Coverage** is a floor on the domain packages, not a vanity number; the gates above matter more.

---

## 6. Definition of Done

A change is done only when **all** hold:

1. Tests written first and passing; the relevant phase gate still green.
2. Lints/vet/type-check/race detector clean on Windows and Linux.
3. No new dependency direction violation; no duplicated core mechanism introduced.
4. Secrets: nothing sensitive logged; canary passes; any new secret comes from env with a "Setup
   required" surface when unset (never a crash, never a committed value).
5. Errors use the stable envelope; failures are typed and fail closed.
6. If it's a UI change: built from tokens + inventory components; design-system gates (07 §8) pass.
7. Docs/changelog updated when behavior or an extension point changes; the numbered spec docs stay
   the source of truth (update them in the same change, don't let code and spec diverge).
8. Observability: new route/attempt/decision paths emit secret-free records so the dashboard can
   explain them.

---

## 7. Principle → enforcement map

Every non-negotiable is mechanically checked. This table is the audit surface; a blank cell is a
gap, not an accepted risk.

| Principle (README) | Enforced by |
|---|---|
| Zero hardcoding of models/capabilities | Lint bans model-name literals in `routing`/`models`; discovery/probe tests; a check that no static model list exists |
| Funding is account-scoped, never provider | Schema (funding keyed on account); test forbidding a provider-level funding decision; review rule |
| Everything account-scoped | Import/schema tests: offerings/quota/health/cert key on `account_id` |
| Offering-operation is the routable unit | Type system: routing consumes certified offering-operations only; gate test |
| Single-owner local trust (authenticated) | Control API loopback-gated test (403 on non-loopback) **and** owner-auth tests (setup-once; login rate-limit + audit-without-secret; idle/absolute session expiry; session-bound CSRF rejected before side effects; 5-min re-verification gate on reveal; password change revokes sessions) — [09 §5.9](09-control-api.md#5-owner-authentication-first-run-login-session-re-verification); Argon2id-hash-only assertion; no role system in code |
| Venom decides, the transport executes | Execution builds a pool-size-1 single-key route; test that no transport (Bifrost or native) can re-select |
| Fail closed | Routing admission tests; `lite` never escalates to paid/unknown under exhaustion |
| Secrets sacred | Canary test; `sanitize` at boundaries; forbidigo on raw logging; secret from env only |
| SQLite source of truth | No-ad-hoc-SQL lint; repositories are the only `database/sql` importer |
| Portable backup is encrypted | Backup/restore integration test; audit that no raw `venom.db` + keyring export exists |
| One design system, no drift | Design-system CI gates (07 §8): no-raw-values, theme completeness, contrast, inventory, visual regression |
| Types over slug strings | Custom vet check banning `switch` on provider slug in routing/execution |
| No duplicated core mechanism | Architecture review + a single-execution-engine test asserting one route path |

---

## 8. Extension points (the documented recipes)

Extending Venom means following a recipe that touches only the intended seam. If an addition forces
edits scattered across unrelated packages, the seam is wrong — fix the seam, don't spread the change.

- **Add a built-in provider:** implement the typed adapter(s) (`APIKeyAdapter` **or**
  `OAuthAdapter`, + optional `ModelDiscoveryAdapter`/`QuotaAdapter`) per
  [03-provider-integration-catalog](03-provider-integration-catalog.md); register it in the provider
  registry; add fixtures + contract tests; record a live re-verification. **No** routing, schema, or
  UI change is required — the fleet UI and router are provider-agnostic.
- **Add a custom OpenAI-compatible provider:** no code change at all — it's the owner-driven
  enrollment path (base URL + key + headers + optional model list + funding).
- **Add an operation** (e.g. embeddings/audio): extend the operation enum + certification fixtures +
  the effective-offering projection; tiers pick it up through capability gates without per-model
  logic.
- **Add/adjust a tier or its policy:** change the typed policy (gates/weights/mix/fallback) in
  `routing`; it's data-shaped and bounded-validated, not new branching. Tier accents come from
  design tokens (07), so it renders consistently automatically.
- **Add a theme:** author one semantic mapping, register it, pass completeness + contrast (07 §9).
- **Add a dashboard surface:** compose inventory components in the standard page anatomy against the
  control API; new data → a new domain component added to the inventory first.
- **Advance Bifrost:** bump the submodule SHA; never edit vendored code in a product branch.

---

## 9. Release & operational readiness

- **Reproducible builds** — single static binary per platform, versioned, signed (Phase 8 —
  Packaging & hardening); the dashboard is embedded via `go:embed` so there is one artifact.
- **First-run** — single-instance lock, keyring creation, migrations, integrity check all fail
  closed before a listener opens; a broken first-run never leaves half state.
- **Config precedence** — defaults → env → flags, in `config` only; documented and tested.
- **Portable encrypted backup/restore specification:**

  **Export flow:**
  1. Create a **consistent SQLite snapshot** (`.backup` API or `BEGIN IMMEDIATE` + full read).
  2. Derive a backup key from the owner-supplied passphrase using **Argon2id** (recommended:
     `time=3, mem=64MiB, threads=4`).
  3. Encrypt the snapshot + credential material (wrapped data key, not the raw device master key)
     into a **single AEAD container** using AES-256-GCM or XChaCha20-Poly1305.
  4. Include a **manifest** inside the encrypted payload: schema version, integrity hash,
     creation timestamp, and the passphrase KDF parameters (salt, time, mem, threads).
  5. Write atomically to the final file path (write to `.tmp`, then rename).
  6. Securely erase temporary files (overwrite before deletion where the OS permits).

  **Restore flow:**
  1. Decrypt the container in a **temporary directory** — never directly over the live DB.
  2. Authenticate the AEAD tag; fail on any mismatch.
  3. Validate the manifest: schema version compatibility, integrity hash of the snapshot.
  4. Validate the database: open with `PRAGMA integrity_check`, run a schema version check.
  5. **Rewrap the data key** — decrypt the wrapped backup key with the passphrase, then
     re-encrypt it with the current device's master keyring key (never export the raw master key).
  6. **Atomic swap** — replace the live DB and keyring only after all validation passes.
     Keep the previous state on disk for **rollback** (the user can restore from the backup
     directory if the swap fails).
  7. On any failure during restore (decrypt, auth, validation, swap), the live state is
     untouched. Clean up the temporary directory.

  **Security rules:**
  - The OS keyring / device master key is **never** exported raw — only the data key is wrapped.
  - The passphrase is **never** logged or stored; losing it permanently prevents backup recovery.
  - The UI warns the owner: *"Without your passphrase, this backup cannot be restored."*
  - No credential material, passphrase, or plaintext DB exists in logs, errors, or temporary
    paths beyond the short-lived decrypt operation.
  - `settings_json` values are encrypted inside the container (header values are in the
    credential envelope which is part of the backup).
  - **Owner-authentication interaction:** the container includes the **`owner_auth`** row (the
    Argon2id password hash + params, [09 §5](09-control-api.md#5-owner-authentication-first-run-login-session-re-verification)),
    so a restore **re-establishes the owner login password** that was in effect when the backup was
    taken — this is the supported lost-password recovery route. The **backup passphrase is
    independent of the owner login password**; neither is derivable from the other. Sessions are
    **not** exported (they are revoked/rebuilt after restore).
  - Tested paths: backup → restore round-trip, wrong passphrase, corrupted container,
    interrupted restore, cross-device restore (different master key), and restore re-establishing
    the owner login password.
- **Load/soak (deterministic readiness thresholds)** — before release the routing hot path is
  load-tested against fake provider backends: **≥ 30 minutes** continuous at **≥ 20 RPS / ≥ 20
  concurrent**, **internal (Venom-origin) error rate ≤ 0.5%**, **p95 routing-overhead latency
  reported** (Venom decision + reservation only; provider time excluded), and **zero invariant
  violations** (no quota overcommit on any window, no Lite paid selection, no secret leaked). These
  are conservative planning contracts, **not** production performance targets, and may be tightened
  later without changing architecture.

---

## 10. Working agreement (the short version)

Build one thing, one way, behind a typed seam, with a test that proves the invariant and a gate that
blocks the regression. Keep the domain pure and the secrets sealed. Make every visual come from a
token and every screen from an inventory component. When you add something, add it at the documented
extension point and update the spec in the same breath. That is the whole method: it is why the
project stays professional, prevents drift, and remains ready to grow.
