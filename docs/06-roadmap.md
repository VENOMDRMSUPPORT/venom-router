# 06 — Greenfield Build Roadmap

A phased plan from empty repo to a working private gateway. Each phase ends at a **gate**: a
demonstrable, tested capability. Build in order; don't start a phase until the prior gate passes.
Every behavior change is test-first, and every phase runs the full verification on **both**
Windows and Linux.

Guiding constraints for all phases: no hardcoded models/capabilities; funding on the account;
account-scoped everything; secrets never logged; Venom decides / the transport executes; SQLite is
the source of truth. The requirement → phase → deliverable → gate mapping is tracked in
[10-requirements-coverage](10-requirements-coverage.md).

---

## Phase 0 — Foundation (single binary that boots)
- `cmd/venom`, config (defaults → env → flags, bind `127.0.0.1:8081`), OS paths, single-instance
  lock, structured secret-free logging, graceful shutdown.
- Embedded Bifrost core wired as a submodule + `replace`; a smoke test that executes one chat call
  through Bifrost against a local fake server (proves the execution seam).
- SQLite open with WAL/pragmas, embedded checksummed migrations, integrity check on open.
- **Gate:** `venom serve` boots, `/health` responds, one fake chat round-trips through Bifrost,
  migrations verified, clean shutdown.

## Phase 1 — Secrets & keyring
- AES-256-GCM keyring stored outside SQLite (owner-only perms; env override), bound AAD, rotation
  barrier + crash-safe rotation, startup reconciliation that fails closed on a missing key.
- `sanitize` package + a canary test proving no injected secret appears in any output.
- **Gate:** credentials encrypt/decrypt with bound AAD; rotation re-wraps atomically; canary passes.

## Phase 2a — Design foundation (before first production UI)
- **The Venom Design System is produced in a separate, dedicated Design System creation task**
  from the authoritative brief in [07-design-system](07-design-system.md) — **it does not exist in
  the repository yet** (an unrelated legacy bundle was removed during planning remediation). This
  roadmap phase **consumes** that finished Design System; it does not author tokens or components
  inline here.
- Deliverables owned by the separate Design System task (its own acceptance gate, per 07 §8/§10):
  a single token source of truth (`tokens/*.json`, W3C format) compiling to CSS custom properties +
  a TypeScript object + a Tailwind theme; the three themes (Venom Dark default, Venom Light, High
  Contrast); the core primitive + composite + Venom-domain component inventory in Storybook with all
  states; and the drift-prevention CI gates (no-raw-values, theme completeness, contrast, inventory,
  visual regression, axe).
- **Gate:** the dedicated Design System task has **passed its own acceptance gate** and published a
  consumable, versioned package — tokens compile to CSS custom properties + Tailwind theme; Storybook
  renders the inventory in Venom Dark, Venom Light, and High Contrast; contrast (AA / AAA-for-HC) and
  theme-completeness CI pass. **No production Venom UI is built until this gate is green.**

## Phase 2b — Providers, accounts, enrollment (the Provider Fleet)
- Typed provider registry + adapter interfaces; **no slug switches**.
- Implement the **API-key** path end to end with `opencode-zen` (authentic validation, fingerprint
  identity, free-set discovery) — the simplest proven provider.
- Implement the **OAuth** path end to end with one provider (Antigravity, env-gated; show "Setup
  required" when unset) including PKCE, state-nonce verification, one-transaction consume,
  identity + funding evidence.
- Account lifecycle: connect, reveal (no-store), refresh, funding override (respect `locked`),
  stop/resume, and **soft disconnect** (`DELETE /accounts/{id}`: stop routing, revoke/retire usable
  credentials where possible, retain sanitized operational history + audit records, restorable only
  via re-enrollment — hard delete/purge is **outside V1 scope**, [02 §3](02-domain-model.md#account-lifecycle-multi-axis-state-model)).
  Funding as append-only evidence with one current row.
- **Owner authentication** ([09 §5](09-control-api.md#5-owner-authentication-first-run-login-session-re-verification)):
  first-run password setup (Argon2id hash + per-install salt + documented params), login/logout,
  opaque server-side session (HttpOnly SameSite=Strict cookie), session-bound CSRF on mutations,
  5-minute re-verification gating credential reveal, failed-attempt rate limiting + audit, and the
  documented lost-password recovery paths. This is **on top of** the loopback + Host-allowlist gate,
  not a replacement for it.
- Control API (loopback + Host-allowlist + **owner authentication** + session-bound CSRF),
  implemented to the contracts in [09-control-api](09-control-api.md), + the **Provider Fleet
  dashboard**: provider rows → account rows → per-account status/funding/models, stat cards
  (Providers / Accounts / Healthy / Models), connect dialogs, reveal/copy that clears on hide/blur.
  (Dashboard UI depends on the Phase 2a Design System gate being green.)
- **Gate:** first run forces owner-password setup, then login yields a working session; a session
  past idle (30 min) or absolute (12 h) expiry is rejected; a mutation without a valid session-bound
  CSRF token is rejected before any side effect; credential reveal requires a fresh (≤ 5 min)
  re-verification; wrong-password attempts are rate-limited and audited with no secret stored. Then:
  connect a real free API-key account and a real OAuth account; both show correct identity, funding,
  and health in the fleet UI; secrets never logged; duplicates handled friendly.

## Phase 3 — Model intelligence (safe discovery first, probes after quota)

### Phase 3a — Catalog discovery (non-inference)
- Account-scoped discovery with generations + atomic snapshot apply; free-account free-only
  filtering; append-only sanitized evidence.
- Canonical identity (provider-scoped SHA-256) + aliases + offerings.
- Per-operation certification lifecycle (`discovered → observed → probing → certified`; canonical
  six-state machine, **no `rejected` state** — [04 §5](04-model-intelligence.md#5-certification)).
- The shared **effective-offering** read model powering `/models`.
- **Gate:** for a connected account, models are discovered and stored with provenance;
  `/models` reflects the raw catalog and certification state. **No inference probes run yet.**

### Phase 3b — Quota & consumption accounting
- **Multi-window quota model** ([02 §3](02-domain-model.md#quota--the-multi-window-model-budgetssources--windows--reservations--allocations)):
  `quota_windows` / `quota_reservations` / `quota_reservation_allocations` — multiple concurrent
  windows per account (provider-native units + reset + confidence), plus the **mandatory
  local-safety budget** (concurrency + estimated-consumption windows) on **every** account,
  including accounts with no provider quota endpoint.
- Estimated consumption in canonical dimensions (requests / input_tokens / output_tokens /
  concurrency / credits-where-a-verified-conversion-exists) with provenance.
- Atomic reserve **across all applicable windows** (all-or-nothing) → execute → reconcile;
  the **canonical five-state reservation machine** (`reserved | reconciliation_pending | settled |
  released | unknown_consumption`, no stored `expired` state; [02 §3](02-domain-model.md#quota--the-multi-window-model-budgetssources--windows--reservations--allocations))
  with idempotent transitions across every allocation; the **discriminated janitor branches**
  (`dispatched_at` marker); **NOT-NULL `window_key`** with the deterministic normalization rule;
  cooldowns persisted; per-account per-window usage/quota surfaced in the dashboard.
- **Gate:** concurrent requests can't overcommit **any** window; an attempt that can't reserve on
  one applicable window is rejected before execution with no window left debited; an account with
  **unknown** provider quota still reserves against (and is bounded by) its local-safety windows —
  never treated as unlimited; quota/usage/reset render per account per window; exhaustion on a
  window suspends only that account/operation; window-identity normalization is deterministic
  (same inputs ⇒ same `window_key`, never NULL). Plus the **six deterministic no-leak /
  no-double-charge reservation tests**: (1) pre-dispatch deadline expiry → released + audit;
  (2) post-dispatch network ambiguity → `reconciliation_pending`, headroom stays debited, never
  auto-released on deadline; (3) reconciliation success → settle (actual or low-confidence
  estimate); (4) proven no-consumption → release; (5) terminal retry boundary →
  `unknown_consumption` + `usage_gap` + re-baseline at next sync; (6) worker crash → lease expiry
  → reclaim with idempotent, consistent allocation updates.

### Phase 3c — Probing (inference-based, quota-protected)
- Context-window probing + capability probing (probe failures never flip a capability false);
  the provenance/precedence engine; optional external metadata sync (off by default).
- **Probe safety:** concurrency limit per provider (max 1 in-flight probe), hard cost cap per
  probe and per account (owner-configurable), opt-in toggle for expensive probes.
- **Gate:** for a connected account, context/capabilities are probed and stored with provenance;
  offerings reach `certified`; `/models` reflects verified facts — with zero hardcoded model
  data anywhere. The **deterministic Cartesian certification test** ([04 §5](04-model-intelligence.md#5-certification))
  passes: all 6 certification states × 3 capability truths (18 combinations) assert that exactly
  (`certified`, `supported`) is routable, invalid transitions are rejected + audited, and no
  `rejected` state exists anywhere in code, schema, or API.

## Phase 4 — Tier engine & routing
- Tier policies (`lite`/`pro`/`max`) with the context ceilings and thinking budgets from
  [05-tier-engine](05-tier-engine.md): **Lite** free-only (paid = hard rejection); **Pro** ~25/75
  paid/free deficit mix; **Max** no funding-mix target — quality-first → quota-fair DRR + P2C.
- Selection: hard gates → route groups (accounts don't inflate score) → weighted scoring →
  competitive band (**quality-score band: Pro ≤ 0.08 / Max ≤ 0.03 below top; applied after hard
  gates; never auto-widened** — [05 §2 Step 6](05-tier-engine.md#2-per-request-selection-algorithm)) →
  **Pro deficit mix controller** (deficit per `(tier, workload_profile_bucket,
  funding_class)`) / **Max quota-fair DRR + P2C** → capacity-fair account selection.
- Scope-classified fallback + scoped circuit breakers; the stable error envelope.
- **Gate (deterministic thresholds):**
  - **Lite:** over the test sample, **zero** paid selections (categorical), and fail-closed under
    free exhaustion.
  - **Pro:** mix **convergence** over **N = 2,000** synthetic successful requests spread across the
    standard workload buckets (`standard`, `vision`, `tool_use`, `structured`, `large_context`) —
    realized paid share per bucket within **±5 percentage points** of the 25% target, with no weak
    route promoted (competitive-band respected: no winner more than **0.08** (Pro) / **0.03** (Max)
    quality-score below the top eligible candidate; a sub-two-candidate band proceeds without
    widening).
  - **Max:** **quota-fairness and quality-band behavior** — DRR frequency converges to the
    capacity-weight ratio across eligible accounts and an account saturated on any required window
    is skipped; **the gate does NOT assert any 50/50 (or fixed) funding ratio**.
  - Fallback respects funding/capability boundaries; stickiness never violates a reservation or
    eligibility; Bifrost never re-selects.

## Phase 5 — Public API surface
- `POST /v1/chat/completions` (+ `/v1/models`) OpenAI-compatible, `vk_live_*` auth, per-key RPM,
  streaming, the three tier model names; usage recorded on every terminal path (never swallowed).
- **The optional `venom` request extension** ([05 §1b](05-tier-engine.md#1b-public-request-contract--the-venom-extension)):
  one namespaced object with `thinking_budget` (`none|standard|extended|ultra`) and
  `required_capabilities` (canonical identifiers → hard gates); absence uses tier defaults; unknown
  fields / invalid values return `venom_invalid_extension`; preserved through non-streaming and
  streaming; never exposes provider names/account IDs/raw model IDs.
  **Image generation is not in V1** ([05 §9](05-tier-engine.md#9-future-scope-non-v1)); there is no
  V1 image endpoint and no gate depends on it.
- **Gate:** point a real OpenAI SDK / IDE at Venom and use `venom/lite|pro|max` for chat +
  streaming + tools + vision; the `venom` extension clamps thinking above the tier ceiling (reported
  in diagnostics), enforces `required_capabilities` as hard gates, survives streaming, and rejects
  invalid fields with the typed error; usage and route decisions are recorded.

## Phase 6 — Dashboard completion & operations
- Analytics (consumption per account/model/tier), diagnostics ("why this route?"), benchmarking/
  quality views, quota monitoring, review-queue banner, model catalog.
- **"Connect a client" page** — a first-run Quick Start (create key → connect providers → point your
  client at the live base URL → watch requests) plus a client-setup catalog that generates
  copy-paste config for Claude Code (`ANTHROPIC_BASE_URL` / `ANTHROPIC_AUTH_TOKEN` + model envs),
  Codex, Cursor, Cline, Continue, and the generic OpenAI SDK — all against `http://127.0.0.1:8081/v1`
  with the three tier model names. The Venom API key is **injected at display/launch time and never
  written into generated files by default**; per [08-engineering-standards](08-engineering-standards.md)
  there is exactly **one** generator per target config shape (no divergent duplicates). (Adopted from
  the OmniRoute analysis, item D.)
- System tray (Open Dashboard / Status / Restart / Quit); desktop run-and-forget.
- **Gate:** the owner can operate the whole system from the dashboard + tray without touching a
  terminal.

## Phase 7 — Provider breadth & custom integrations
- Add the remaining proven built-ins from [03-provider-integration-catalog](03-provider-integration-catalog.md):
  `claude-code`, `codex`, `github-copilot`, `clinepass` (OAuth), `xai` (Grok OAuth), `ollama-cloud`,
  `gemini-cli` (Google schema), `agnes-ai`, `nvidia-nim` — each with **offline fixture/contract
  tests (CI-blocking)** plus a **live re-verification recorded as dated evidence (non-CI)**, per the
  verification tiers in [03 §5.1](03-provider-integration-catalog.md#51-verification-tiers-what-is-a-ci-gate-vs-what-is-manual-evidence).
- The **Custom OpenAI-Compatible** enrollment path (base URL + key + headers + optional model
  list + per-account funding).
- **Gate:** every shipped integration connects a real account, discovers models, and certifies at
  least chat; the custom path onboards a new OpenAI-compatible provider with no code change.

## Phase 8 — Packaging & hardening
- Signed builds, first-run experience, **portable encrypted backup/restore** (single AEAD container
  with passphrase-derived key via Argon2id, consistent SQLite snapshot, wrapped data key, manifest/
  version/integrity metadata; never a raw `venom.db` + keyring pair), optional non-loopback
  inference bind with explicit config (control plane remains loopback-only).
- **Gate (deterministic thresholds — planning contracts, may be tightened later without changing
  architecture):** a clean machine installs, runs, and connects providers; **sustained-load
  readiness** = **≥ 30 minutes** continuous at a **≥ 20 RPS / ≥ 20 concurrent** request profile
  against fake provider backends, with **internal error rate ≤ 0.5%** (Venom-origin 5xx, excluding
  provider-origin failures), **p95 routing-overhead latency reported** (Venom decision + reservation
  time, provider time excluded), and **zero invariant violations** (no quota overcommit on any
  window, no Lite paid selection, no secret in any log/audit). Backup/restore round-trips with full
  data integrity and no plaintext exposure, including restore re-establishing the owner password
  from the container ([09 §5.7](09-control-api.md#5-owner-authentication-first-run-login-session-re-verification)).

---

## Sequencing rationale
Design foundation (Phase 2a) comes before the first production UI (Phase 2b) so the Provider Fleet
dashboard is built on established tokens and primitives, not reworked later.
Catalog discovery (Phase 3a) comes before quota (Phase 3b) because it reads model lists without
inference cost; quota before probes (Phase 3c) because probes consume quota/budget.
Tier engine (Phase 4) requires certified offerings from Phase 3; the public API (Phase 5)
is a thin shell over the engine. Breadth (Phase 7) is deliberately late: prove the whole
vertical slice on two providers first, then scale out the catalog.
