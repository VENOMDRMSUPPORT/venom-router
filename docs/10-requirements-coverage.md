# 10 — Requirements → Roadmap Coverage Matrix

Every core requirement, mapped to its **authoritative definition**, the **responsible subsystem**,
its **roadmap phase**, the **deliverable**, and the **acceptance gate** that proves it. A blank cell
is a gap, not an accepted risk. This is the audit surface used to declare planning readiness.

Phases refer to [06-roadmap](06-roadmap.md); subsystem packages to [01 §3](01-architecture.md#3-components-package-boundaries);
enforcement gates to [08 §7](08-engineering-standards.md#7-principle--enforcement-map).

---

## 1. Non-negotiable principles (README §2)

| # | Requirement | Definition | Subsystem | Phase | Deliverable | Acceptance gate |
|---|---|---|---|---|---|---|
| 1 | Zero hardcoding of models/capabilities | [04](04-model-intelligence.md) | `intelligence`, `models` | 3a/3c | Discovery + probe + provenance; no static model list | Lint bans model-name literals; "no static list" test; probed facts in `/models` |
| 2 | Free/Paid is an account+offering fact, never a provider | [02 §2](02-domain-model.md) | `accounts/domain` | 2b | Funding-evidence trail; 4-source vocabulary | Schema keys funding on account; test forbids provider-level funding |
| 3 | Everything account-scoped | [02](02-domain-model.md), [04 §1](04-model-intelligence.md) | `accounts`, `intelligence` | 2b/3a | Offerings/quota/health/cert keyed on `account_id` | Schema/import tests assert account-scoping |
| 4 | Offering-operation is the routable/certifiable unit | [02 §3](02-domain-model.md), [04 §5](04-model-intelligence.md) | `models`, `routing` | 3c/4 | Per-operation certification | Routing consumes certified offering-operations only (type + gate test) |
| 5 | Single-owner local trust (authenticated) | [01 §6a/§8](01-architecture.md), [09 §5](09-control-api.md#5-owner-authentication-first-run-login-session-re-verification), [02 §3](02-domain-model.md#owner-authentication-single-owner-identity-session-re-verification) | `httpapi` | 2b | Loopback gate + Host allowlist **+ owner authentication** (Argon2id hash, opaque session, session-bound CSRF, 5-min re-verification, recovery) | 403 on non-loopback test; owner-auth suite ([09 §5.9](09-control-api.md#5-owner-authentication-first-run-login-session-re-verification)); no role system |
| 6 | Venom decides; transport executes | [01 §4](01-architecture.md) | `execution`, `routing` | 0/4 | Single `InferenceTransport`; pool-size-1 Bifrost | Transport can't re-select test; no slug switch (vet) |
| 7 | Fail closed | [04 §5](04-model-intelligence.md), [05 §3/§8](05-tier-engine.md) | `routing` | 4 | Admission gates; Lite never paid | `lite` fail-closed scenario; unknown⇒ineligible tests |
| 8 | Secrets are sacred | [01 §8](01-architecture.md), [08 §6](08-engineering-standards.md) | `secrets`, `sanitize` | 1 | AES-256-GCM keyring; hash-only Venom keys | Canary test (no secret in any output); env-only secrets |
| 9 | SQLite is the source of truth | [01 §5](01-architecture.md), [02 §5](02-domain-model.md) | `storage` | 0 | Typed repositories; migrations | No-ad-hoc-SQL lint; migration integrity/rollback tests |
| 10 | One design system, no drift | [07](07-design-system.md) | Design System (separate task) → dashboard | 2a | See §3 below | DS task acceptance gate green **before** any UI |

## 2. Domain & engine requirements

| Requirement | Definition | Subsystem | Phase | Deliverable | Acceptance gate |
|---|---|---|---|---|---|
| Credential cardinality: one active per `(account_id, kind)`; ≤ one staged per kind | [02 §3](02-domain-model.md#credential-encrypted-secret-for-an-account), [03 §2e](03-provider-integration-catalog.md) | `accounts` | 2b | `state` column + `idx_cred_active_per_kind` + `idx_cred_staged_per_kind`; staging swap | Reauth staging test; interruption-recovery test; multi-kind coexist (github_oauth + copilot_service); second-staged rejected `reauthentication_in_progress` |
| Owner authentication (single-owner login/session/re-verify/recovery) | [09 §5](09-control-api.md#5-owner-authentication-first-run-login-session-re-verification), [02 §3](02-domain-model.md#owner-authentication-single-owner-identity-session-re-verification) | `httpapi` | 2b | First-run setup; login/logout; session (idle 30 m / absolute 12 h) + CSRF; 5-min re-verify; rate-limit + audit; recovery via restore/local reset | Setup-once; expiry/CSRF/re-verify negative tests; audit stores no secret; password change revokes sessions |
| Funding-evidence source vocabulary (4 values) + `evidence_required` initialization | [02 §2](02-domain-model.md#2-free--paid--the-semantics-precisely), [03 §3](03-provider-integration-catalog.md#3-the-11-integrations) | `accounts/domain` | 2b | `{provider_policy, provider_evidence, owner_policy, owner_override}` + precedence; unknown-default providers use `funding_policy.mode = evidence_required` → first row `funding = unknown`, `source = provider_policy` (not locked, auditable) | Fixed-policy provider stamps a valid source; locked override rejected (`funding_locked`); an `evidence_required` provider stamps the `unknown`/`provider_policy` row and stays ineligible everywhere until classified (test) |
| Account lifecycle (multi-axis) | [02 §3](02-domain-model.md) | `accounts/domain` | 2b | connection × health axes + derived `display_status` | Transition tests (legal/invalid); eligibility projection test |
| Account-scoped discovery + free-safety | [04 §1/§2b](04-model-intelligence.md) | `intelligence` | 3a | Generation-guarded snapshots; routing-critical free-safety | Free account never surfaces paid; fail-closed on dataset miss |
| Free-safety vs. optional enrichment separation | [04 §2b](04-model-intelligence.md) | `intelligence` | 3a/3c | Two pipelines; enrichment off by default | Disabling enrichment does not weaken free-safety (test) |
| Capability/context probing (truth vs. execution) | [04 §2/§5](04-model-intelligence.md) | `intelligence` | 3c | Probe outcome taxonomy; probe caps | Infra failure never flips capability false (test) |
| Certification lifecycle (per-operation, revocable; **no `rejected` state**) | [04 §5](04-model-intelligence.md#5-certification) | `models`, `intelligence` | 3c | The canonical six-state machine (`discovered→observed→probing→certified`; `certified ⇄ suspended`; `probing → suspended → probing`; `certified → expired → probing`) + explicit legal/invalid transition table; capability truth as a separate dimension | **Deterministic Cartesian test:** 6 states × 3 truths (18 combos) ⇒ only (`certified`,`supported`) routable; invalid transitions rejected + audited; no `rejected` state in code/schema/API |
| Multi-window quota + mandatory local-safety budget | [02 §3](02-domain-model.md#quota--the-multi-window-model-budgetssources--windows--reservations--allocations), [05 §4](05-tier-engine.md#4-quota--consumption-accounting), [04 §5](04-model-intelligence.md#5-certification) | `quota` | 3b | `quota_windows` / `quota_reservations` / `quota_reservation_allocations`; provider windows + local-safety (concurrency + consumption) on every account; canonical estimate dimensions; **NOT-NULL `window_key`** with deterministic normalization (`provider:*` / `rolling:*` / `local:*`) | Multiple windows per (account,unit); unknown provider quota still reserves against local-safety (never unlimited); estimate provenance recorded; window-identity normalization deterministic, never NULL |
| Atomic reservation across all applicable windows (no overcommit, all-or-nothing) | [02 §3](02-domain-model.md#quota--the-multi-window-model-budgetssources--windows--reservations--allocations), [05 §2/§4](05-tier-engine.md) | `quota` | 3b | Per-window conditional UPDATE + version, one `BEGIN IMMEDIATE`; idempotent reserve/settle/release across every allocation | Concurrency test: two requests can't overcommit any window; any window short ⇒ whole reservation rolled back before execution |
| Reservation state machine + unknown-consumption reconciliation | [02 §3](02-domain-model.md#quota--the-multi-window-model-budgetssources--windows--reservations--allocations), [05 §4](05-tier-engine.md#4-quota--consumption-accounting) | `quota` | 3b | Canonical five states (`reserved`/`reconciliation_pending`/`settled`/`released`/`unknown_consumption`; **no stored `expired` state** — `expires_at` is a deadline); discriminated janitor branches via `dispatched_at`; `reconciliation_pending` never auto-released; idempotent transitions across all allocations | The **six deterministic no-leak/no-double-charge tests** (pre-dispatch expiry; post-dispatch ambiguity; reconciliation success; proven no-consumption release; terminal `unknown_consumption` + `usage_gap`; worker crash / lease recovery); no-provider-API fallback |
| Tier policies + selection (gates→groups→score→band→distribute) | [05 §1–§2, §8.5](05-tier-engine.md) | `routing` | 4 | Lite free-only; Pro ~25/75 deficit mix; **Max no funding-mix target — quality-first → quota-fair DRR + P2C**; anti-inflation groups; **competitive quality band: Pro ≤ 0.08 / Max ≤ 0.03 below top (0–1 quality scale), post-hard-gates, never auto-widened** | Lite **zero** paid + fail-closed; Pro mix within ±5 pp over **N=2,000** across workload buckets; band respected (no winner outside 0.08/0.03); **Max quota-fairness + quality-band, NOT a 50/50 ratio** |
| Workload-profile bucketing (deterministic key; per-bucket deficit) | [05 §2 Step 7](05-tier-engine.md#2-per-request-selection-algorithm) | `routing` | 4 | One bucket key = normalized→sorted→deduped property set; deficit per `(tier, workload_profile_bucket, funding_class)` — not global | Same property set ⇒ same bucket; separate deficit cells per bucket/funding_class |
| Public `venom` request extension (namespaced, optional) | [05 §1b](05-tier-engine.md#1b-public-request-contract--the-venom-extension), [01 §6b](01-architecture.md#6b-data-plane-public-inference-api) | `httpapi`, `routing` | 5 | `thinking_budget` (none/standard/extended/ultra, clamp to tier ceiling) + `required_capabilities` (hard gates); typed `venom_invalid_extension`; preserved streaming/non-streaming; no provider internals leaked | Extension parsed/validated; clamp reported in diagnostics; required-caps gate; streaming preserved; invalid ⇒ typed error |
| Thinking-budget normalization | [05 §1a](05-tier-engine.md) | `routing`, `execution` | 4 | none/standard/extended/ultra; clamp; graceful degrade | Tier defaults, clamp-to-max, degrade-vs-explicit-require tests |
| Scope-classified fallback + circuit breakers | [01 §4.2](01-architecture.md), [05 §3](05-tier-engine.md) | `routing`, `execution` | 4 | TypedFailure scopes; adaptive backoff | Fallback never crosses funding/capability; streaming first-byte rule |
| Failure taxonomy (normalized errors) | [01 §4.2](01-architecture.md) | `execution` | 4/5 | `TypedFailure`; scope priority | NormalizeError never leaks secrets/raw text (test) |
| Public inference API (OpenAI-compatible + `venom` extension) | [01 §6b](01-architecture.md), [05 §1b/§5](05-tier-engine.md) | `httpapi` | 5 | `/v1/chat/completions` + `/v1/models`; `vk_live_*`; RPM; optional `venom` extension | Real SDK: chat+stream+tools+vision; extension clamp/gates/validation; usage recorded |
| Quantitative acceptance gates (deterministic thresholds) | [06 Phase 4/8](06-roadmap.md), [05 §8.1](05-tier-engine.md#8-resolved-product-decisions), [08 §5/§9](08-engineering-standards.md) | `routing`, all | 4/8 | Pro N=2,000 / ±5 pp / per-bucket; sustained load ≥30 min, ≥20 RPS/≥20 concurrent, ≤0.5% internal error, zero invariant violations; auth expiry/negative tests | Gates assert the stated numbers; values may tighten without architecture change |
| Control API contracts | [09](09-control-api.md) | `httpapi` | 2b+ | Per-endpoint contracts | Contract/handler tests per endpoint; redaction/audit |
| Shared async-job status surface | [09 §1/§3.12](09-control-api.md#312-get-jobsjob_id--canonical-shared-async-job-status) | `httpapi` | 2b+ | One `GET /jobs/{job_id}` for all async mutations; mutations return `202 {job_id, status_url}`; OAuth transaction is the sole exception | No competing per-resource status endpoints (test); job states/result_ref/retention/authorization defined |
| Health endpoints (single final choice) | [01 §6d](01-architecture.md#6d-health-endpoints-the-final-single-choice), [09 §2](09-control-api.md) | `httpapi` | 0 | `/health` liveness **unauthenticated, outside** `/api/control/v1`; `/api/control/v1/health` reserved for a distinct authenticated readiness endpoint only if needed | `/health` needs no session; no duplicate liveness surface |
| Provider adapters (11 built-ins + custom) | [03](03-provider-integration-catalog.md) | `providers` | 2b/7 | Typed adapters; fixtures | Offline fixture tests (CI); live re-verification (evidence, non-CI) — [03 §5.1](03-provider-integration-catalog.md#51-verification-tiers-what-is-a-ci-gate-vs-what-is-manual-evidence) |
| Portable encrypted backup/restore | [08 §9](08-engineering-standards.md) | `storage`, `secrets` | 8 | Single AEAD container; rewrap; rollback | Round-trip + wrong-passphrase + interrupted-restore + cross-device tests |
| Observability (route/attempt/usage, secret-free) | [05 §7](05-tier-engine.md), [01 §6c](01-architecture.md) | `observability` | 4/5 | Decision/attempt records; `X-Venom-*` headers | Secret-free records; RouteExplain reads them |

## 3. Design System (separate dedicated task, consumed at Phase 2a)

| Requirement | Definition | Owner | Phase | Deliverable | Acceptance gate |
|---|---|---|---|---|---|
| Product identity & owner-console context | [07 §0](07-design-system.md) | DS task | (DS task) | Identity brief applied | Reviewed against brief |
| Token source of truth + 3 outputs | [07 §2/§10](07-design-system.md) | DS task | (DS task) | `tokens/*.json` → CSS/TS/Tailwind | Compiles to all three outputs |
| Themes: Dark / Light / High-Contrast | [07 §3](07-design-system.md) | DS task | (DS task) | Complete semantic mappings | Theme-completeness test |
| Component inventory (primitives/composite/domain) | [07 §5/§5a](07-design-system.md) | DS task | (DS task) | All states + Storybook + a11y | Inventory gate; visual regression |
| Domain state coverage | [07 §5a](07-design-system.md) | DS task | (DS task) | Account/funding/cert/quota/route states | Stories per state; axe checks |
| Accessibility (AA; AAA-HC) | [07 §7](07-design-system.md) | DS task | (DS task) | Contrast matrix; keyboard/focus | Contrast + axe CI |
| Integration handoff contract | [07 §10](07-design-system.md) | DS task → dashboard | 2a→2b | Versioned package + consumption contract | Dashboard consumes one artifact; Phase 2a gate |

---

## 4. Gaps

No core requirement currently lacks a definition, subsystem, phase, deliverable, and gate. The only
work deliberately **outside** the plan is [05 §9](05-tier-engine.md#9-future-scope-non-v1) future
scope (image generation, embeddings/audio, cross-provider equivalence, per-offering funding
override), which by design has **no** V1 phase or gate.
