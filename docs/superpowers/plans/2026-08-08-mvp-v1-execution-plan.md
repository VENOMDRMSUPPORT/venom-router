# MVP v1.0 Execution Plan (Program Plan)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development
> (recommended) or superpowers:executing-plans to implement this plan task-by-task.
> Steps use checkbox (`- [ ]`) syntax for tracking.
>
> **Plan level:** this is the PROGRAM plan operationalizing `docs/report-1.md`
> Part XVII (M1–M5). Each coding milestone below requires its own detailed
> per-task plan (superpowers:writing-plans) authored at dispatch time by the
> governor and executed by a SEPARATE implementer — the governor never
> implements its own work. This document fixes scope, order, gates, and the
> verified current state; the sub-plans fix the code.

**Goal:** Ship VENOM ROUTER v1.0 — the Go desktop build — as a signed,
single-binary MVP with dated live-run evidence, five complete providers, a
custom OpenAI-compatible provider path, encrypted backup/restore, and a green
sustained-load gate.

**Architecture:** Per `docs/report-1.md` Part XV, the Go desktop build
(`Desktop\venom-router`) carries the MVP. Parts I–XIV are design reference
("verify and align", largely already implemented); Parts XV–XVIII are the
authoritative readiness decision. This plan executes Part XVII with every gap
re-verified against the repo on 2026-08-08.

**Tech Stack:** Go 1.26, SQLite (modernc), React dashboard (`dashboard/`,
vite :8088 dev / go:embed :8081 prod), `@venom/design-system`, Taskfile gate,
self-hosted CI (win + linux).

## Global Constraints

- English only in every repo file (code, docs, commits). Chat stays Arabic.
- Governor dispatches; a separate implementer writes all code (`venom-no-self-implement`).
- Direct commit+push to `main` is authorized; independent review always precedes push.
- `task gate` on Windows is the only claim-worthy check; also run
  `npm run test:e2e` (Playwright, ~30s, 20 tests) before any push that changes
  user-visible strings; verify CI with `gh run view --json status,conclusion`
  per-step, never `gh run watch | tail`.
- Never edit an applied migration file (checksum guard bricks every existing DB).
- Never touch `G:\Venom-Router`. `F:\projects\venom-router` is READ-ONLY reference.
- Any live-instance restart: graceful Stop/Quit only, pre-restart DB backup
  mandatory, and note the owner's uncommitted `internal/tray/` work — never run
  `task bundle` for the owner without asking.
- Visual baselines are reviewed fixtures: regenerate ONLY via the
  `visual-baselines-linux` dispatch job after owner approval, never self-commit.
- Simplification is the owner's stated top priority: every batch prefers
  deletion and reuse over new surface.
- Tracker artifact republish must run `strip.mjs` first.

## Current State (verified 2026-08-08 against the working tree)

| Report gap | Claim | Verified today | Status |
|---|---|---|---|
| B1 | Handoff open: ClinePass + Live Models incomplete, AccountRow visuals rejected | Most 2026-08-04 handoff debt already landed on main (worktree committed `a0e7911`, token refresh + health/quota ticks, qualification tick `d7cf3bb`, baselines accepted `767bb23`). **Still open:** `dashboard/src/fleet/AccountRow.tsx:570-573` derives `subscriptionRequired` by substring-matching `last_health_error`; no typed backend contract; "live" definition duplicated across fleet components | **OPEN (narrowed)** |
| B2 | Zero dated live-run evidence | `docs/evidence/` = 3 runbooks + 1 wire reference, 0 dated results | **OPEN** |
| B3 | Two CI gates empty | `Taskfile.yml:196` (`schema-lint`) and `:205` (`no-hardcoding-lint`) both say `RESERVED placeholder`, not part of `gate` | **OPEN** |
| B4 | Bifrost transport is a shim | `internal/execution/bifrost.go:230,238` return "not implemented" for Stream/Cancel | **OPEN** |
| B5 | 4 providers seams-only | `agnes_ai`, `antigravity`, `nvidia_nim`, `ollama_cloud` have `*_seams.go` but no `*_usability.go` (the other 5 have full usability) | **OPEN** |
| B6 | No custom OpenAI-compatible path | No custom registration in `internal/providers/` (grep confirms) | **OPEN** |
| B7 | No backup/restore | No backup package under `internal/` | **OPEN** |
| B8 | Sustained-load gate never run | No load harness or report in repo | **OPEN** |
| B9 | Race detector non-blocking | `race.yml` = `workflow_dispatch` + nightly cron only | **OPEN** |

Additional verified fact that de-risks M3: **zero** `BuiltinCatalog` entries
declare `TransportKindBifrost` (grep on `internal/providers/catalog.go`), so no
shipped provider can currently resolve to the shim. `TransportKindBifrost` is
referenced in 12 files (`internal/execution/`, `internal/providers/`,
`internal/httpapi/`, `internal/staticgate/`, `internal/quota/`) — the removal
is bounded and enumerable.

---

## Verify & Align — Parts I–XIV (design reference)

The report's design half (Parts I–XIV) is explicitly **"verify and align to
this model, not build from zero"** (report line 40). Below is that
verification, run against the code on 2026-08-08 — not inherited from the
report's prose. Verdicts: ✅ built (often already gated) · ⚠️ divergent by
design · ❌ not built.

| Principle | Report § | Verdict | Evidence in the tree |
|---|---|---|---|
| Provider Adapter Contract | §6.3 | ✅ built | `internal/providers/adapters.go` — `DiscoverModels` / `FetchQuota` / `FetchIdentity` + core adapter, optional capabilities dispatched by the Registry |
| No provider-specific logic in core | §6.4 | ✅ built + gated | `internal/execution/noslugswitch.go` + `internal/providers/noslugswitch.go` — the **no-slug-switch** check is part of `task gate`; wire behaviour chosen by a typed catalog value, never a slug |
| No static model list | §13.1 | ✅ built + gated | `internal/staticgate/nostaticmodellist.go` (AST check, threshold 3) |
| Confidence-based metadata | §3.3 | ✅ built | honest-model-verification: declared≈ / proven✓ capability chips with provenance; `native_context_tokens` ✓ = native-provenance |
| UNKNOWN → Unverified | §2.3 | ✅ built | verify-before-count; UNTESTED / Detected states, no literal `UNKNOWN` display name |
| Circuit breakers | §8.4 | ✅ built | `internal/routing/breaker.go` — state machine, backoff, half-open probe |
| Quota-aware routing | §8.6 | ✅ built | multi-window atomic reservation + `internal/routing/deficit.go` / `drr.go` |
| Classified fallback graph | §8.10 | ✅ built | `internal/routing/fallback.go` |
| Secret hygiene in traces | §12.4 | ✅ built + gated | `internal/sanitize/sanitize.go` — full redaction, structured + free-text; secret canary in `task gate` |
| Cache-first, non-blocking startup | §5.1 | ✅ built | `internal/app/boot.go:133` — "Start itself never blocks on the boot-time pass"; background scheduler ticks |
| Data-model tables | §10 | ✅ built | storage layer, 17 migrations |
| **Canonical identity (cross-provider unify)** | §3.4 | ⚠️ **divergent by design** | `internal/models/canonical.go` — canonical key is **provider-scoped**; "No normalization … that would create cross-provider equivalence, which v1 does not support." Same model via two providers = two rows. The report's §3.4 envisions unifying them. |
| **Alias resolution** | §3.5 | ⚠️ **divergent by design** | `internal/models/alias.go` — **exact-match only**; a case/whitespace/prefix near-miss is a miss, never a family/alias fallback. The report's §3.5/§3.7 envision alias→canonical→family inheritance. |
| **Family-level metadata / brand-new-model inheritance** | §3.6, §3.7 | ❌ **not built** | no family resolver anywhere; deliberately out of v1. |
| **`canonicalization_rate` metric (>99% target)** | §12.5 | ❌ **not built** | grep for `canonicaliz` in `internal/` = zero. |
| **`unresolved_models_total` / coverage score** | §12.5, §12.6 | ❌ **not built** | grep = zero. Reported as a KPI with nothing measuring it. |

**What this verification changes in the plan:**

1. **The two missing observability metrics** (§12.5/§12.6) are folded into
   **M2** as Task 2.4 (below) — they are the measurement half of the same
   "enforce principles as gates" milestone.
2. **The §3.4–3.7 divergence is real and must be recorded, not left silent.**
   The build deliberately chose provider-scoped identity with no
   cross-provider merge and no family inheritance (this matches the
   honest-model-verification spec: "NO cross-provider model merging"). A
   reader of the report will expect VMIR-style family inheritance and not
   find it. This is filed as **M2 Task 2.5** (documentation only): record the
   divergence + rationale in `docs/04-model-intelligence.md` so the report
   and the build stop disagreeing silently. It is **not** a v1 build item —
   family inheritance stays a documented post-v1 option.
3. Everything marked ✅ needs **no work** — it is cited here so the plan can
   claim alignment with evidence rather than assertion.

---

## M0 — Decisions & Migration Assets (owner + one read-only batch)

**Goal:** every irreversible or scope-shaping decision is made once, up front,
and the F build's knowledge is captured before it freezes.

### Task 0.1: Owner ratifies the three scope decisions

- [ ] **D1 — Bifrost:** adopt report recommendation **(a) drop from V1**.
      Evidence for the recommendation: Stream/Cancel unimplemented
      (`bifrost.go:230,238`) AND zero catalog consumers — any future route
      resolving to it would fail on streaming silently.
- [ ] **D2 — Provider scope:** ship the **five complete providers**
      (`opencode-zen`, `claude-code`, `clinepass`, `gemini-cli`,
      `openai-generic`); mark `antigravity`, `agnes_ai`, `nvidia_nim`,
      `ollama_cloud` "coming soon" in the UI; defer `codex`/`copilot`/`xai`
      (P7 PROV-002/003/005) post-MVP. Consequence to record: the roadmap
      tracker's P7 breadth moves post-MVP; tracker gets a "V1 scope" note on
      its next publish (with `strip.mjs`).
- [ ] **D3 — Code signing:** owner picks the signing route (OV/EV cert or
      none-for-v1.0-with-documented-SmartScreen-consequence). Lead time on a
      cert is days-to-weeks — decide now so M5.5 is not the long pole.

### Task 0.2: Owner-side actions (blocking M4)

- [ ] **Freeze the F build** (`F:\projects\venom-router`): stop its scheduled
      jobs/serving. Report §18.3: both builds reserving quota on the same
      accounts corrupts accounting in both — M4 must not start before this.
- [ ] **Rotate the F build's secrets** (report §18.2): the cleartext owner
      password in `.env`, the Supabase `service_role` key, and
      `ANTIGRAVITY_CLIENT_SECRET` (it may live in Git history).

### Task 0.3: Extract migration assets from the F build (read-only batch)

**Files:**
- Create: `docs/evidence/codex-legacy-wire-reference.md`
- Create: `docs/evidence/github-copilot-legacy-wire-reference.md`
- Create: `docs/evidence/xai-legacy-wire-reference.md`
- Modify: `docs/03-provider-integration-catalog.md` (merge `PROVIDER-INVENTORY.md` / `PROVIDERS.md` facts that survive review)

- [ ] Dispatch a read-only extraction task: port the run-proven wire facts from
      `F:\projects\venom-router` (`codex.server.ts` + `codex-quota.server.ts` +
      `codex-model-discovery.server.ts`, `github-copilot.server.ts`,
      `xai-*.server.ts`) into wire-reference docs in the same format as
      `docs/evidence/clinepass-legacy-wire-reference.md`. Record facts only
      (endpoints, headers, token flows, response shapes) — explicitly refuse
      the legacy build's capability-inference heuristics.
- [ ] Skim the F build's `AUDIT_REPORT.md` ten-bug list; file one
      `docs/evidence/f-audit-checklist.md` mapping each bug to the desktop
      build's answer (especially: provider/account names in routing traces;
      swallowed consumption-logging failures — both already mandated by
      report §12.4, verify and cite the enforcing test or open an M2 item).
- [ ] Review + commit. Gate: docs-only diff, `task gate` green.

**Gate M0:** D1–D3 recorded in this file's decision log (below), F build
frozen + secrets rotated (owner confirms), wire references committed.

---

## M1 — Close Open Debt (critical path)

**Goal:** no announced-but-incomplete work remains; the 2026-08-04 handoff is
closed with a counter-report.

### Task 1.1: Typed operational-status contract (backend)

**Files (verify exact paths at sub-plan time):**
- Modify: `internal/httpapi` fleet read-model (accounts projection)
- Modify: `dashboard/src/api/controlClient.ts` (account type)
- Test: httpapi projection tests + dashboard type tests

**Interfaces:**
- Produces: three typed fields on the account read-model —
  `operational_status` (enum: `operational` | `subscription_required` |
  `expired` | `degraded`), `attention_code` (stable machine string or empty),
  `recovery_action` (enum: `reauth` | `subscribe` | `none`) — derived
  server-side from the same health facts that today feed `last_health_error`.
- Consumes: existing health/identity persistence (no schema change expected;
  if one proves necessary it is a NEW migration, never an edit).

- [ ] Governor writes the detailed TDD sub-plan (superpowers:writing-plans),
      after READING the current fleet read-model and pasting real signatures
      into the plan (controller-plan-defects rule).
- [ ] Dispatch to implementer; review; `task gate` green; push.

### Task 1.2: AccountRow rebuild on the typed contract (frontend)

**Files:**
- Modify: `dashboard/src/fleet/AccountRow.tsx` (delete the substring
  derivation at lines 570-573; one row template, no layout-changing branches)
- Modify: `dashboard/src/fleet/fleet.css` (one coherent treatment per
  non-operational class — kill the `--subscription-required` vs `--expired`
  divergence)
- Create: one shared "is live" selector consumed by `AccountRow`,
  `FleetOverview`, `ProviderRow` (today the definition is triplicated)
- Test: dashboard vitest suites + `npm run test:e2e`

- [ ] Governor writes the TDD sub-plan; dispatch; review; gates green; push.
- [ ] Visual QA on **:8088** (dev, live source — not :8081 which is stale
      until rebuild), both themes, wide + narrow window. Owner approves the
      visuals (they rejected the previous ones — that approval is the point).
- [ ] After owner approval ONLY: run the `visual-baselines-linux` dispatch
      job once, review artifact, commit baselines.

### Task 1.3: Live Models refresh — prove it, don't claim it

- [ ] With the dev backend running, capture two timestamped SQLite snapshots
      (probe/qualification tables) ≥2 tick intervals apart with NO manual
      button presses; show stored rows advancing (new probe runs / updated
      `last_seen`), not just a "X minutes ago" label. Record method + output
      in the counter-report.

### Task 1.4: Handoff counter-report

**Files:**
- Create: `docs/handoffs/2026-08-08-handoff-closure-counter-report.md`

- [ ] Enumerate every item of
      `docs/handoffs/2026-08-04-codex-incomplete-clinepass-live-models-handoff.md`
      with how each was proven (commit + test/evidence), including the items
      that closed before this plan (cite `a0e7911`, `d7cf3bb`, `767bb23`).
- [ ] Commit. **Gate M1:** counter-report complete, `task gate` green, tree
      clean and pushed, owner has approved the AccountRow visuals.

---

## M2 — Activate the Empty Gates (parallel with M1)

**Goal:** principles are machine-enforced, not trusted.

### Task 2.1: `no-hardcoding-lint` (report principle #1)

**Files:**
- Create: checker script (follow the repo's existing gate-script convention
  under `.github/scripts/` — e.g. `verify-bundle.mjs` precedent)
- Modify: `Taskfile.yml:205` (real command, drop `RESERVED`, add to `gate`)

- [ ] Sub-plan defines the ban precisely: no hardcoded model name, context
      window, capability flag, or price in `internal/` production code
      (tests + fixtures + the provider catalog's own declared definitions are
      the allowed homes). First run yields zero violations or a remediated
      list committed with the checker.
- [ ] Prove the checker can fail: plant a violation in a scratch copy, show
      RED, remove it (mutation-proof the gate itself — the useless-canary
      lesson).

### Task 2.2: `schema-lint`

**Files:**
- Create: checker script
- Modify: `Taskfile.yml:196` (real command, drop `RESERVED`, add to `gate`)

- [ ] Enforce: NULLable numeric columns mean "unknown"; append-only evidence
      tables keep exactly one current row; every account-scoped table links
      `account_id`. Runs against the migration set + a freshly-migrated DB.
- [ ] Same mutation-proof requirement as 2.1.

### Task 2.3: Race detector blocking on `main`

**Files:**
- Modify: `.github/workflows/race.yml` (add `push: branches: [main]` trigger)
  or fold a race job into `ci.yml` — sub-plan picks one, recording runtime
  cost on the self-hosted runners. Note: `-race` also runs on the dev host
  now (gcc installed) — add the local command to the sub-plan.

### Task 2.4: Canonicalization / coverage observability metrics (report §12.5/§12.6)

**Files (verify at sub-plan time):**
- Modify: `internal/observability/` (metric emission)
- Modify: the models read-model / qualification path that resolves discovered
  IDs to canonical rows

- [ ] Emit `unresolved_models_total` (a discovered `provider_model_id` with no
      resolved canonical row) and a `canonicalization_rate` derived from it,
      each unresolved entry recording provider + raw ID + failure stage
      (§12.5). Surface on the existing observability read path — no new UI is
      required for v1, a logged/queryable metric suffices.
- [ ] Per-model metadata coverage score (§12.6) is **optional for v1** — record
      the decision (implement or defer) in the sub-plan; if deferred, it is
      listed in the "out of scope" footer, not left as a silent KPI.

### Task 2.5: Record the canonical-identity divergence (documentation)

**Files:**
- Modify: `docs/04-model-intelligence.md`

- [ ] Document that v1 uses **provider-scoped canonical identity with no
      cross-provider merge and no family inheritance** (per
      `internal/models/canonical.go` / `alias.go`), and why — so the report's
      §3.4–3.7 VMIR/family vision is explicitly marked a documented post-v1
      option, not an unmet promise. Docs-only; no code.

- [ ] **Gate M2:** `task gate` includes the new blocking checks; both
      checkers proven mutable-to-red; race blocking on main;
      `unresolved_models_total` emitted and the §3.4–3.7 divergence recorded;
      CI green verified per-step.

---

## M3 — Drop Bifrost (parallel with M1, after D1)

**Goal:** no half-built transport in the production path.

**Files (the 12 verified reference sites):**
- Delete: `internal/execution/bifrost.go`, `internal/execution/bifrost_test.go`
- Modify: `internal/providers/transportkind.go` (+`_test`) — remove
  `TransportKindBifrost` from the closed vocabulary
- Modify: `internal/execution/types.go`, `internal/execution/dispatcher.go`,
  `internal/execution/canary_test.go`,
  `internal/execution/p4gate_noreselect_test.go`,
  `internal/httpapi/transportresolver_test.go`,
  `internal/providers/registry_test.go`, `internal/staticgate/norejectedstate.go`,
  `internal/quota/cooldown.go`
- Modify: `docs/01-architecture.md` (record decision (a) + rationale + the
  "documented future option" note), Taskfile/CI references to the submodule,
  `.gitmodules` (drop the pinned Bifrost submodule from the build path)

- [ ] Governor writes the TDD sub-plan after reading each of the 12 sites
      (some are tests that pin the vocabulary as closed — they must shrink,
      not be deleted).
- [ ] **Gate M3:** `grep -r "not implemented" internal/ --include="*.go"`
      outside tests = zero; `task gate` green; CI green on both runners
      (the linux runner's submodule checkout step must be updated in the
      same commit).

---

## M4 — Dated Live Evidence (after M1; F build frozen)

**Goal:** the three runbooks become dated proof. This milestone is
owner-in-the-loop by nature; the governor prepares, the owner executes the
human legs, results are recorded the same day.

**Pre-flight (hard, from the DB-corruption incident):** graceful Stop of any
running instance; backup `venom.db` before restart; the owner's live instance
runs an OLD binary — a rebuild is required and will bake in whatever is in
the tree, so build from a clean, pushed `main` only.

- [ ] **4.1 Execute `P2b-TEST-003`** — connect one real API-key account and
      one real OAuth account; prove identity, funding, health in the fleet
      view. Append a dated **Results** section (date, account used — with
      identifiers sanitized per §12.4 —, screenshots, what failed).
- [ ] **4.2 Execute `P5-TEST-001`** — real SDK (OpenAI SDK + Claude Code) at
      `http://127.0.0.1:8081/v1` through `venom/lite | pro | max`: chat,
      streaming, tools, vision; prove the `venom` extension clamps thinking
      budget above tier ceiling and survives streaming. Dated Results section.
- [ ] **4.3 Execute `P6-TEST-002`** — the human leg: double-click
      `venom.exe`, silent boot, tray, full cycle, no terminal. Dated Results
      section. (This also closes the long-held P6 gate.)
- [ ] **Gate M4:** three dated evidence files in `docs/evidence/`, each
      including what failed — a results section with zero failures listed is
      suspicious, not reassuring (apparent-maturity warning, §18.3).

---

## M5 — Close V1 Scope & Harden (release gate)

**Goal:** what ships is complete; what does not ship is declared.

### Task 5.1: Provider narrowing in the UI

- [ ] Per D2: the four seams-only providers render as "coming soon"
      (non-connectable) in the provider list; the five complete ones are
      unchanged. Copy lives in the dashboard; no backend deletion — the
      seams stay for post-MVP completion.

### Task 5.2: Custom OpenAI-compatible provider path (report §6.8; tracker PROV-010)

- [ ] Owner-supplied base URL + API key (+ optional extra headers, encrypted
      at rest like every credential; optional model allowlist) flows through
      the SAME adapter contract, discovery, usability, health, and routing as
      a built-in provider — definition as data, zero code per provider.
- [ ] This is its own security-reviewed batch (header values are secrets).
      Governor writes the sub-plan after reading the existing
      `openai_generic` adapter + catalog registration end-to-end, and the
      legacy archive's working adapters for wire facts (refusing its
      capability-inference heuristics).

### Task 5.3: Backup & restore (report §11.4)

- [ ] Single sealed AEAD container: Argon2id-derived key from a passphrase,
      consistent SQLite snapshot (WAL-aware — use SQLite's backup/serialize
      API, never file-copy a live DB), wrapped data key, version + integrity
      metadata. Round-trip test: backup → restore to a scratch
      `VENOM_DATA_DIR` → full equality of logical state. NEVER raw
      `venom.db` + keyring as a pair.

### Task 5.4: Sustained-load gate (report §12.7)

- [ ] Harness against mock backends: ≥30 continuous minutes, ≥20 RPS and ≥20
      concurrent; assert internal error rate ≤0.5%, report p95 routing
      latency, and zero invariant violations — no quota overrun on any
      window, `venom/lite` never selects a paid offering, no secret in any
      log (reuse the existing secret-canary approach). Result recorded as a
      dated file in `docs/evidence/`. CI placement decision (nightly vs
      pre-release manual dispatch) recorded in the sub-plan.

### Task 5.5: Signed build + first-run experience

- [ ] Per D3: sign `dist/venom.exe` (the PE-subsystem verifier from
      `4d380ff` stays; signing is a step after it) or record the explicit
      no-signing decision + SmartScreen consequence in `docs/01-architecture.md`.
- [ ] Quick-start path: create owner key → connect provider → point client →
      watch requests; verified on a clean machine/VM.
- [ ] **Gate M5 (release):** a clean machine installs, runs, connects a real
      provider, and serves a request from a real SDK — no terminal. Backup
      round-trips with integrity. Load gate green. Tag `v1.0.0`.

---

## Sequencing & Parallelism

```text
M0 (decisions + F-freeze + asset extraction)
 ├────────────► M1 (handoff debt)  ──────► M4 (live evidence) ─┐
 ├────────────► M2 (empty gates)  ─── independent ─────────────┤
 └── D1 ─────► M3 (drop Bifrost) ─── independent ──────────────┼─► M5 ─► v1.0.0
                                                               │
              (M5.2/5.3/5.4 sub-plans may be AUTHORED early;   │
               execution starts after M1+M2+M3 are green) ─────┘
```

- **Critical path: M0 → M1 → M4 → M5.** M2 and M3 run in parallel with M1
  (dependency-safe, boundary-disjoint — per the standing auto-batch policy).
- M4 additionally requires M2 in practice: live evidence should be gathered
  on a build whose gates are real.
- Part XIII's architecture phasing (P0/P1: registry, canonical IDs, aliases,
  cache-first) is **verify-and-align, not build**: the current build already
  implements the lower layer. Any misalignment discovered during M1–M4 is
  filed as a scoped batch, not a rewrite.

## Release KPIs (from report §13.13 — checked at Gate M5)

- Startup / Models-UI / routing-metadata blocking network calls: **0**
- Known-model resolution (canonicalization rate): **>99%**
- UNKNOWN display names: **0**
- Provider connect + model discovery automation: **100%** (Connect → usable,
  no wizard)
- Metadata cache hit: **>95%**; routing decision latency local-only

## Decision Log

| # | Decision | Options | Recommendation | Owner ruling | Date |
|---|---|---|---|---|---|
| D1 | Bifrost | (a) drop from V1 / (b) complete Stream+Cancel | **(a)** — zero catalog consumers, shim served its Phase-0 purpose | _pending_ | |
| D2 | Provider scope | 5 complete now + 4 "coming soon" + defer codex/copilot/xai / ship more | **narrow to 5** | _pending_ | |
| D3 | Code signing | cert (OV/EV) / unsigned v1.0 with documented consequence | owner's call (cost/lead-time) | _pending_ | |

## Self-Review Notes (governor, 2026-08-08)

- Every B1–B9 claim in this plan was re-verified against the working tree
  today (file paths and line numbers cited are from that verification, not
  from the report's prose).
- Line numbers (`AccountRow.tsx:570`, `Taskfile.yml:196/:205`,
  `bifrost.go:230/238`) are anchors valid as of `main@dd5eca5` + untracked
  `report-1.md`; sub-plan authors must re-verify before dispatch.
- Deliberately NOT in scope for v1.0: completing the four seams-only
  providers, codex/copilot/xai adapters, task classifier / semantic routing
  (report P2, §13.4), multi-model orchestration, Postgres/Redis.
