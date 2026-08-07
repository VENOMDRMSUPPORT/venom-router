# 11 — Venom Router Enterprise Implementation Plan

> **Status:** Authoritative execution program. Converts the approved planning package
> (`docs/01`–`docs/10`, `README.md`) and the approved, gate-passed Venom Design System
> (`Design_System/`, `@venom/design-system@1.0.0`) into an implementation-grade plan an
> independent engineering organization can execute from an empty application workspace to a
> production-ready V1.
>
> **This document does not restate architecture or Design System content — it references it.**
> When this plan and a source document appear to differ, the source document wins and the difference
> is a defect in this plan. The numbered spec docs remain the source of truth for behavior; this doc
> is the source of truth for *execution order, task boundaries, dependencies, and evidence*.

**Companion artifacts (this plan is incomplete without them):**
- [`11-appendix-A-task-dependency-matrix.md`](11-appendix-A-task-dependency-matrix.md) — machine-readable task graph (every task's hard/soft deps, parallel group, blocks, gate).
- [`11-appendix-B-requirement-traceability.md`](11-appendix-B-requirement-traceability.md) — requirement → task → phase → test → evidence → release-gate traceability, cross-checked against [`10-requirements-coverage.md`](10-requirements-coverage.md).

---

## 0. How to read this plan

- **Phases** preserve the exact identity and ordering of [`06-roadmap.md`](06-roadmap.md)
  (`P0, P1, P2a, P2b, P3a, P3b, P3c, P4, P5, P6, P7, P8`). They are **refined into implementation
  units**, never renumbered or merged.
- **Workstreams** are derived from the package boundaries in [`01-architecture.md §3`](01-architecture.md#3-components-package-boundaries).
  Each has a stable code (§4).
- **Task IDs** are `P<phase>-<WS>-<NNN>` — deterministic, unique, sortable, phase-linked,
  workstream-linked, and stable for the life of the program. Example: `P2b-SEC-003`.
- Every task carries the full metadata block defined in §6. **Global Constraints (§5) are implicit
  in every task** and are not repeated per card.
- A **gate** passes only through demonstrable, tested behavior — never because files exist (§ per phase).
- Package/path references use the approved layout from [`01 §3`](01-architecture.md#3-components-package-boundaries)
  (`internal/<pkg>`, `third_party/bifrost`, the dashboard workspace, and `@venom/design-system`).
  Exact filenames are prescribed **only** where the approved architecture fixes them; otherwise the
  affected *boundary* is named and the implementer chooses the file within it.

---

## 1. Repository baseline (as audited)

| Fact | Finding |
|---|---|
| Application implementation code | **None.** No `go.mod`, `cmd/`, `internal/`, `third_party/`, migrations, or dashboard workspace exist. The application workspace is empty. |
| Approved planning package | `README.md` + `docs/01`–`docs/10`, internally consistent and cross-referenced. |
| Approved Design System | `Design_System/` = `@venom/design-system@1.0.0`, **12/12 `npm run validate` gates PASS** (see `Design_System/validation/report.md`), handoff-cleaned and manifest-classified. |
| Version control | Git repository initialized at workspace root; `main` tracks `origin/main` at `git@github.com:VENOMDRMSUPPORT/venom-router.git` over SSH. No dependency/build/test output is tracked. `.gitignore` excludes those outputs and `.gitattributes` explicitly enforces LF for repository metadata, Go, SQL, JS/TS, config, docs, and scripts. The Design System keeps its additional handoff exclusions in `Design_System/validation/handoff-manifest.json`. |
| Frontend contract | React + Vite + TypeScript (strict), embedded via `go:embed`; consumes `@venom/design-system`. ([`01 §7`](01-architecture.md#7-tech-stack), [`07 §2.3`](07-design-system.md)) |
| Backend contract | Go 1.26+, `CGO_ENABLED=0`; embedded Bifrost core (submodule + `replace`) as the primary OpenAI-compatible transport. Approved P0-EXEC-001 pin: `github.com/maximhq/bifrost/core@v1.7.3`, release commit `8f0ef396f589528210f6409383c90863bfa1e99f`. The dependency is not fetched before that task. ([`01 §7`](01-architecture.md#7-tech-stack)) |
| Database contract | SQLite via `modernc.org/sqlite` (pure-Go), single file, single writer, WAL; goose embedded checksummed migrations. ([`01 §5/§7`](01-architecture.md#5-persistence--sqlite)) |
| Crypto contract | AES-256-GCM keyring outside SQLite; Argon2id KDF; XChaCha20-Poly1305/AES-256-GCM for backup; `x/oauth2`, `golang-jwt`. ([`01 §7/§8`](01-architecture.md#8-security-model)) |
| Undecided technology | **None material.** All layers are fixed by the approved stack. The only host choice (desktop tray vs headless `serve`) is a run-mode, not an undecided dependency. |
| `@venom/design-system` package boundary | Canonical local package; two supported consumption modes (workspace `file:` dependency, or deterministic handoff copy). Public import surface: root + `/primitives`, `/domain`, `/tokens`, `/themes`, `/density`, `/icons`, `/tailwind`, `/styles.css`. ([`Design_System/SKILL.md`], [`.../validation/handoff-contract.md`]) |
| Public API surface | `POST /v1/chat/completions`, `GET /v1/models` only. ([`01 §6b`](01-architecture.md#6b-data-plane-public-inference-api)) |
| Control API surface | `/api/control/v1/*` per [`09`](09-control-api.md); canonical shared jobs endpoint `GET /jobs/{job_id}` (OAuth transaction-status the sole exception); `/health` liveness outside the authenticated API. |
| Domain state machines | Account multi-axis (connection × health) [`02 §3`]; credential `{active,staged,retired}` [`02 §3`]; funding evidence 4-source [`02 §2`]; certification six-state + capability-truth [`04 §5`]; reservation five-state [`02 §3`]; job five-state [`09 §3.12`]; owner-session lifecycle [`09 §5`]. All confirmed and mapped to tasks. |
| Roadmap phases + gates | 12 phases (`P0`–`P8` with `2a/2b`, `3a/3b/3c`), each with a deterministic gate [`06`]. Preserved verbatim. |

**Conflict scan (mission requirement — verify, don't invent resolutions).** No genuine
source-to-source conflict was found. The former Design System status drift has been remediated
across `README`, `07`, and `08`; the package is now consistently recorded as implemented and
gate-passed. One apparent sequencing tension remains documented rather than resolved by invention:

1. **`06 Phase 2b` "free-set discovery" for `opencode-zen` vs. `06 Phase 3a` "discovery
   orchestration."** Not a conflict: 2b proves the *adapter's* `DiscoverModels` + authentic
   validation at the fixture level and connect-time best-effort sync; 3a owns the *pipeline*
   (generations, atomic snapshot apply, effective-offering read model, certification lifecycle) and
   the routing-critical free-safety resolver. Offering **persistence** (schema group M4) lands in 3a.
   **[DEC-DISC-LAYERING]**

No blocker. Planning proceeds.

---

## 2. Executive implementation strategy

**Build order (critical path).** `P0 → P1 → P2b → P3a ∥ P3b → P3c → P4 → P5 → P6/P8`, with `P2a`
(Design System integration) running alongside `P1`/early `P2b`, and `P7` (provider breadth)
fanning out after the adapter contract and transport dispatcher freeze in `P2b`/`P4`.

**Why the order is safe.**
1. **Foundation before state.** A bootable binary, SQLite + migrations, and the typed
   `InferenceTransport` seam (P0) exist before anything stores state — so the execution boundary and
   persistence discipline are fixed before any subsystem depends on them.
2. **Secrets before credentials.** The keyring, bound-AAD encryption, `sanitize` boundary, and the
   secret canary (P1) exist *before* any provider credential is ever written (P2b) — security
   precedes the first sensitive value.
3. **Authentication before mutation.** Within P2b, owner auth (setup/login/session/CSRF/re-verify)
   and the loopback+Host network gate are built *before* any control-plane mutation endpoint — no
   sensitive endpoint ships without its auth/CSRF contract.
4. **Truth before routing.** Discovery + free-safety (P3a), the multi-window quota reservation
   invariants (P3b), and per-operation certification + capability truth (P3c) all exist before the
   tier engine (P4) is allowed to select a route. Routing consumes only `(certified ∧ supported)`
   offering-operations and can only execute behind a successful atomic reservation.
5. **Contracts before UI, backend before public surface.** The Design System package is integrated
   and gated (P2a) before any production UI. Each backend read/write contract stabilizes before the
   UI surface that consumes it. The public inference API (P5) is a thin shell over an engine whose
   transport and failure contracts already stabilized in P4.
6. **Backup last.** Portable encrypted backup/restore (P8) is built only after encryption (P1) and
   the full schema (M1–M7) are stable — a backup container is meaningless before the thing it
   protects is fixed.

**Where parallel work is safe / prohibited.** See §8 (Parallel Execution Matrix). Summary:
adapters after the contract freeze, most UI surfaces after their API, and the two pure-domain
tracks are broadly parallel; **schema migrations serialize**, **public/control API envelopes and the
transport dispatcher are single-owner contracts that must freeze before dependents**, and the
Design System package is **read-only** to the application.

**When the Design System is consumed.** At P2a: the app takes the versioned package as a dependency
(no copies, no edits to generated artifacts), and the P2a gate re-verifies the package's own gate is
green. No production Venom UI is built before P2a is green (roadmap invariant).

**How backend contracts stabilize before UI integration.** Every control endpoint is specified in
[`09`](09-control-api.md); this plan builds each endpoint (with its typed DTOs, redaction, and audit)
and its tests *before* the UI task that consumes it, and each UI task lists the exact backend
contracts it requires as preconditions.

**How every phase ends with executable evidence.** Each phase gate (§ per phase, and §17) names exact
commands, exact suites, expected results, and retained artifacts. A gate is CI-blocking or
manual-evidence — never "files exist."

---

## 3. Confidence & headline numbers

- **Planning status:** implementation-grade, ready for independent audit.
- **Phases:** 12 (`P0, P1, P2a, P2b, P3a, P3b, P3c, P4, P5, P6, P7, P8`).
- **Workstreams:** 22 (§4).
- **Implementation units (tasks):** 179 (`P0`:15, `P1`:6, `P2a`:6, `P2b`:34, `P3a`:14, `P3b`:14,
  `P3c`:12, `P4`:22, `P5`:12, `P6`:16, `P7`:14, `P8`:14). Counts verified against the authored cards in §§9–13.
- **Critical path length:** 8 phase hops (`P0→P1→P2b→P3a→P3c→P4→P5→P8`), with `P3b` joined into
  `P3c` and `P2a`/`P7`/`P6` overlapping off the critical line.

---

## 4. Workstream model

The implementation workstreams are derived from [`01 §3`](01-architecture.md#3-components-package-boundaries),
plus one program-enablement stream (`ENV`) for the reproducible developer/CI bootstrap. No workstream
contradicts the approved package boundaries (pure-domain packages never own I/O;
`@venom/design-system` is never duplicated).

> **Workstream count note:** `HLTH` is a **cross-cutting capability workstream** — it defines the
> health/cooldown/circuit-breaker behavior but is implemented entirely through tasks owned by
> `DOM`/`CAPI`/`QUOTA`/`ROUTE`/`UI` (see its row below). The 22 workstreams are therefore 21
> task-owning workstreams plus this one cross-cutting workstream; no artificial `HLTH-*` tasks exist.

| Code | Workstream | Primary package(s) | Owns |
|---|---|---|---|
| `ENV` | Development environment bootstrap | host + CI bootstrap | Exact tool versions, persistent PATH verification, Git/SSH readiness, and the handoff into repository-level pins. |
| `FND` | Repository & build foundation | `app`, `cli`, `config`, `platform`, `tray` | Module skeleton, config precedence, OS paths, run modes, composition root, single-instance lock, tray. |
| `SEC` | Security & owner authentication | `secrets`, `sanitize`, `httpapi`(auth) | Keyring, bound-AAD crypto, redaction, canary, owner auth/session/CSRF/re-verify/lockout/recovery. |
| `DB` | Database & migrations | `storage` (migrations) | Migration groups M1–M8, pragmas, checksum/integrity/rollback harness, schema-lint. |
| `DOM` | Domain repositories & invariants | `accounts/domain`, `models`, funding/credential domain | Pure entities, state machines, transition legality, evidence authority. |
| `PROV` | Provider framework, adapters & enrollment | `providers`, `accounts/application` | Registry, typed adapter contract, OAuth/API-key frameworks, reauth staging, per-provider adapters. |
| `DISC` | Model discovery & evidence | `intelligence`, `models` | Account-scoped discovery, generations, free-safety, enrichment, effective-offering read model, precedence. |
| `CERT` | Probes & certification | `intelligence` | Probe taxonomy, context/capability probes, six-state machine, capability truth, review drainer. |
| `HLTH` | Health, cooldown, circuit breakers (**cross-cutting**) | `accounts`, `routing`, `execution` | Health observation, scoped cooldowns, circuit breakers, adaptive backoff. **Owns no `HLTH`-prefixed task IDs** — implemented via `P2b-DOM-001` (health axis), `P2b-CAPI-004` (health endpoints), `P3b-QUOTA-008` (quota sync + cooldowns), `P4-ROUTE-014` (scoped breakers/backoff), `P6-UI-007` (Token Health surface). |
| `QUOTA` | Quota windows, budgets, reservation & reconciliation | `quota` | Windows, local-safety budgets, atomic reservation, five-state machine, janitor, reconciliation worker. |
| `ROUTE` | Tier routing engine | `routing` | Normalization, gates, groups, scoring, band, Pro deficit / Max DRR+P2C, stickiness, fallback loop. |
| `EXEC` | Inference transport & provider execution | `execution` | `InferenceTransport` dispatcher, transports, streaming/cancel, `NormalizeError` failure taxonomy. |
| `CAPI` | Control-plane API | `httpapi`(control) | Endpoint contracts, envelope, idempotency, optimistic concurrency, redaction, audit. |
| `PAPI` | Public OpenAI-compatible API | `httpapi`(public) | `/v1/*`, vk auth, RPM, `venom` extension, telemetry headers, error envelope. |
| `JOBS` | Background jobs & scheduling | `httpapi`(jobs), workers | Job persistence, leases, retry/backoff, crash recovery, canonical `GET /jobs/{job_id}`. |
| `OBS` | Diagnostics, traces, auditing | `observability` | Route-decision/attempt records, audit events, `X-Venom-*` builder, RouteExplain data. |
| `BKP` | Backup & restore | `storage`, `secrets` | AEAD container, KDF, wrap/rewrap, atomic swap + rollback. |
| `DS` | Design System package integration | dashboard workspace | Dependency wiring, styles/tokens/tailwind, theme/density, consumption-mode + no-edit enforcement. |
| `UI` | App shell, navigation, dashboard surfaces | dashboard workspace | Shell, auth screens, the 12 nav surfaces, connect-a-client, recovery surfaces. |
| `TEST` | Testing & verification | cross-cutting | Static gates, unit/integration/adapter/API/UI/E2E/load suites, evidence retention. |
| `REL` | Packaging & release | `app`, build | Reproducible signed builds, first-run, load/soak, readiness/shutdown, release verification. |
| `OPS` | Operational documentation | docs | Install/run/backup/recovery runbooks, config reference, release notes. |

---

## 5. Global Constraints (implicit in every task)

Copied verbatim from the approved sources; every task's requirements implicitly include these.

- **Zero hardcoding** of model names, context windows, capabilities, or prices anywhere in code,
  schema, or UI. A model name may pick a logo, never a behavior. ([`README §2.1`], [`04`])
- **Funding is an account+offering fact, never a provider fact.** No "free provider" anywhere. ([`README §2.2`], [`02 §2`])
- **Everything account-scoped.** Discovery/quota/health/certification key on `account_id`. ([`README §2.3`])
- **The routable/certifiable unit is the offering-operation** `(provider, account, model, operation)`. ([`README §2.4`])
- **Fail closed.** Unknown ⇒ ineligible/rejected; Lite never touches paid/unknown, even under exhaustion. ([`README §2.7`])
- **Secrets are sacred.** Credentials encrypted at rest (key outside DB); Venom API keys hash-only;
  credentials/tokens/OAuth `code`/`state`/PKCE verifiers/`Authorization` headers never logged. ([`README §2.8`], [`01 §8`])
- **SQLite is the source of truth.** All state via typed repositories; no ad-hoc SQL from handlers;
  no second authoritative catalog in Bifrost. ([`README §2.9`])
- **Venom decides; the transport executes.** One typed `InferenceTransport`; transport selection by
  typed capability, never a `switch` on provider slug; Bifrost pool-size-1/one-key/retries-off. ([`README §2.6`], [`01 §4`])
- **One design system, no drift.** Every UI value is a token; screens compose inventory components;
  the `@venom/design-system` package is read-only to the app and its generated artifacts are never
  hand-edited. ([`README §2.10`], [`07`], [`Design_System/README.md`])
- **Layering is acyclic and enforced.** Pure domain packages import no `storage`/`database/sql`/HTTP;
  `providers` imports neither `accounts` nor `models`; `accounts/application` orchestrates via
  injected interfaces. ([`01 §3`], [`08 §2`])
- **Time, IDs, randomness are injected** in the domain (deterministic, testable). ([`08 §3`])
- **Language floors:** Go 1.26+, `CGO_ENABLED=0`; dashboard TypeScript `strict`, no unjustified `any`. ([`08 §3`])
- **Cross-platform:** the full suite runs on **Windows/amd64 (primary) and Linux/WSL** every phase. ([`06`], [`08 §5`])
- **Test-first & gated:** every behavior change lands with tests; nothing merges red; each phase ends
  at its gate. ([`08 §7`])
- **Stable error envelope** everywhere: `{ error: { code, message, request_id, retryable, details? } }`;
  no raw provider errors, no secrets. ([`05 §5`], [`09 §1`])

---

## 6. Task card format

Every implementation unit below uses this block. Fields never left as `TBD`.

```
### <TaskID> — <Title>
Purpose:        why this exists, at this point.
References:     authoritative doc §s.
Preconditions:  what must be true/done first (task IDs + facts).
Scope:          exact implementation scope.
Non-goals:      explicit exclusions.
Boundaries:     packages/paths expected to change.
Data impact:    schema/migration effect (or "none").
API impact:     endpoints/contracts affected (or "none").
Security:       secret/authn/authz/redaction requirements (or "n/a").
Failure/rollback: required failure + recovery/rollback behavior.
Tests:          the tests that prove it (types + key assertions).
Evidence:       the artifact/output that proves completion.
Deps:           hard deps (task IDs).
Parallel-with:  tasks safe to run concurrently.
Blocks:         tasks this unblocks.
DoD:            done only when all of these hold.
```

Full per-task dep/parallel/blocks data is also in
[Appendix A](11-appendix-A-task-dependency-matrix.md) (machine-readable).

---

## 7. Dependency graph (readable)

Phase-level hard dependencies (`→` = hard, `⇢` = soft/enabling, `∥` = parallelizable):

```
                    ┌────────────────────────────────────────────────────────────┐
                    │                        P0 Foundation                        │
                    │  (module, config, SQLite+migrations, InferenceTransport seam,│
                    │   Bifrost smoke, /health, fail-closed startup)               │
                    └───────────────┬───────────────────────────┬────────────────┘
                                    │ (keyring needs DB + startup)│ (seam frozen)
                                    ▼                             │
                           ┌────────────────┐                     │
                           │ P1 Secrets &   │                     │
                           │ keyring + canary│                    │
                           └───────┬────────┘                     │
                  ┌────────────────┤                              │
        (DS is    │                │ (credentials need keyring)   │
     independent) ▼                ▼                              │
        ┌──────────────────┐  ┌──────────────────────────────────┴──────────────┐
        │ P2a Design System│  │ P2b Providers · Accounts · Enrollment · Owner    │
        │ integration      │  │ Auth · Control API · Provider Fleet UI           │
        │ (consume @venom) │  │  (auth BEFORE any mutation; adapter contract     │
        └────────┬─────────┘  │   freeze; OAuth framework; reauth staging)       │
                 │ (UI needs  └───────┬───────────────────────────────┬──────────┘
                 │  DS gate)          │ (accounts+creds)               │ (adapter contract
                 │            ┌───────┴────────┐            ┌──────────┴─ freeze → P7)
                 │            ▼                ▼            │
                 │   ┌─────────────────┐ ┌───────────────┐ │
                 │   │ P3a Catalog     │ │ P3b Quota +   │ │   P3a ∥ P3b
                 │   │ discovery +     │ │ reservation + │ │  (both need only P2b)
                 │   │ free-safety +   │ │ local-safety +│ │
                 │   │ effective read  │ │ reconciliation│ │
                 │   └───────┬─────────┘ └──────┬────────┘ │
                 │           └───────┬──────────┘          │
                 │                   ▼ (cert needs offerings + quota-protected probes)
                 │           ┌──────────────────┐          │
                 │           │ P3c Probing +    │          │
                 │           │ certification +  │          │
                 │           │ capability truth │          │
                 │           └───────┬──────────┘          │
                 │                   ▼ (routable = certified ∧ supported; reserve to execute)
                 │           ┌──────────────────┐          │
                 │           │ P4 Tier engine + │◀─────────┘ (transport dispatcher + NormalizeError)
                 │           │ routing + EXEC   │
                 │           │ transports/fail  │───────────────┐ (dispatcher freeze → P7 transports)
                 │           └───────┬──────────┘               │
                 │                   ▼ (thin shell over engine) │
                 │           ┌──────────────────┐               ▼
                 └──────────▶│ P5 Public API +  │        ┌───────────────┐
                  (surfaces  │ venom extension +│        │ P7 Provider   │
                   need DS)  │ telemetry hdrs   │        │ breadth +     │
                             └───────┬──────────┘        │ custom path   │
                                     ▼                    └──────┬────────┘
                             ┌──────────────────┐                │
                             │ P6 Dashboard     │◀───────────────┘ (surfaces read all read-models)
                             │ completion + tray│
                             └───────┬──────────┘
                                     ▼
                             ┌──────────────────┐
                             │ P8 Packaging +   │  (needs encryption P1 + schema M1–M7 stable)
                             │ backup/restore + │
                             │ load/soak        │
                             └──────────────────┘
```

**Invalid-sequencing guards this graph enforces** (mission requirement):
- routing (P4) cannot start before quota reservation invariants (P3b `QUOTA-004/005`) exist;
- provider UI (P2b `UI-003`) cannot start before the control-API enrollment contracts (P2b `CAPI-003/004`);
- certification-driven routing (P4) cannot start before capability truth (P3c `CERT-005`);
- credential reveal (P2b `CAPI-004`) cannot ship before owner re-verification (P2b `SEC-005`);
- production UI (any `UI-*`) cannot start before the Design System is integrated (P2a gate);
- the public API (P5) cannot start before transport + routing failure contracts (P4 `EXEC-002/003`) stabilize;
- backup (P8 `BKP-001`) cannot start before encryption (P1) and schema (M1–M7) are stable.

---

## 8. Parallel execution matrix

**Serialization hotspots (must not be worked concurrently by two changes):**
| Hotspot | Owner task(s) | Rule |
|---|---|---|
| Migration files (`storage` migrations dir) | one migration-group task per phase (M1…M8) | Schema design, migration preparation, and isolated testing of independent groups may proceed **concurrently**; migration **numbering, landing, application, and integration serialize** — no two migration owners concurrently modify or land the canonical migration sequence, and a later group is conceptually rebased onto the latest accepted schema ordering before landing. Each group is one reviewable task; forward-only in prod, down-path tested in dev. |
| Public/control error envelope | `P2b-CAPI-002`, `P5-PAPI-006` | The envelope is one contract; frozen in P2b, extended (not forked) in P5. |
| `InferenceTransport` dispatcher | `P0-EXEC-002`, `P4-EXEC-001` | Single dispatcher; transports are added behind it, never a second path. Freeze before P7. |
| Adapter contract (`providers` interfaces) | `P2b-PROV-001` | Freezes at end of P2b; all P7 adapters build against the frozen contract. |
| Design System package | `@venom/design-system` | **Read-only** to the app; never edited; generated artifacts never hand-touched. |
| Effective-offering read model | `P3a-DISC-005` | Single shared projection; UI + routing + diagnostics all read it; no re-derivation. |
| `X-Venom-*` header builder | `P5-OBS-001` | One sanitized builder; never duplicated. |
| Client-config generator | `P6-UI-011` | Exactly one generator per target config shape. |

**Broadly parallel (safe concurrently):**
- The two pure-domain tracks: `accounts/domain` + funding + credential domain (`DOM`) ∥ `models`/certification domain.
- `P3a` (discovery) ∥ `P3b` (quota) — both depend only on `P2b`; they join at `P3c`.
- `P2a` (DS integration) ∥ `P1` and early `P2b` non-UI work.
- All `P7` per-provider adapters, once `P2b-PROV-001` (contract) and `P4-EXEC-001` (dispatcher) freeze.
- Most `P6` UI surfaces, once their named backend contract is stable and P2a is green.

**Integration cadence.** Because the repo imposes no branch/worktree policy, this plan requires
**trunk-based, small, test-first changes** (one task = one reviewable change) with the full static
gate suite (§16.1) green on every change, on Windows and Linux. No long-lived divergent work; a task
that cannot land green is split, not accumulated. (This plan does **not** prescribe git worktrees or
branches — [`06`]/[`08`] impose none.)

---

## 9. Phase structure — Foundation & security (P0, P1, P2a)

Each phase header carries: **Objective · Scope · Out of scope · Entry · Dependencies ·
Parallelization · Expected artifacts · DB/API/Security/DS impact · Testing · Migration/recovery ·
Acceptance gate · Required evidence · Exit · Rollback/containment.** Task cards follow.

---

### Phase P0 — Foundation (a single binary that boots)

- **Objective:** a bootable `venom` binary with config, OS paths, single-instance lock, SQLite +
  checksummed migrations, the typed `InferenceTransport` seam proven through embedded Bifrost, and
  `/health`.
- **Scope:** `FND`, `DB`, `EXEC` seam, minimal `OBS`/`CAPI`(health).
- **Out of scope:** any credential, any provider adapter, any real UI, any routing.
- **Entry:** empty application workspace + approved docs + approved DS package (baseline §1).
- **Dependencies:** none (program start).
- **Parallelization:** after `P0-FND-001`, the `config`/`platform`/`logging` tasks and the `EXEC`
  seam run parallel to the `DB` track.
- **Expected artifacts:** `go.mod`, `internal/*` skeleton, `third_party/bifrost` submodule + `replace`,
  first migration, a green `/health`, a passing Bifrost smoke test.
- **DB impact:** SQLite open + pragmas + migration runner + baseline migration (empty/system table).
- **API impact:** `/health` liveness only (unauthenticated, outside `/api/control/v1`).
- **Security impact:** structured secret-free logging baseline; `forbidigo` bans on raw logging/`os.Getenv`.
- **DS impact:** none.
- **Testing:** unit (config precedence), integration (migration up/down + integrity), execution-seam smoke.
- **Migration/recovery:** migration integrity check on open; startup fails closed on integrity failure.
- **Acceptance gate:** `venom serve` boots; `GET /health` responds; one fake chat round-trips through
  Bifrost against a local fake server; migrations verified; clean graceful shutdown — **on Windows and Linux**.
- **Required evidence:** CI logs of boot + `/health` 200 + Bifrost smoke pass + migration integrity + shutdown, on both OSes.
- **Exit:** gate green.
- **Rollback/containment:** none needed — no persistent external state yet; a failed startup leaves no half-state (lock acquired before any DB/keyring work).

#### P0-ENV-001 — Development environment setup
Purpose: ensure Go toolchain, Git, and essential dev tools are installed and configured before any code task begins.
References: [`01 §7`](01-architecture.md#7-tech-stack), [`08 §3`](08-engineering-standards.md#3-coding-standards).
Preconditions: none (first task in program).
Scope:
- Install Go 1.26.5 via Winget (`GoLang.Go`); verify `go version` returns `go1.26.5`.
- Initialize Git at workspace root: `git init`, `.gitignore` (node_modules, dist, test outputs), `.gitattributes` (LF enforcement), remote `origin` via SSH.
- Install Go tools at exact bootstrap versions: `go install golang.org/x/tools/cmd/goimports@v0.48.0` and `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2`.
- Install Task runner via Winget (`Task.Task` v3.52.0) for cross-platform build orchestration.
- Persist `C:\Program Files\Go\bin`, `%USERPROFILE%\go\bin`, and the Winget Task package path in
  machine/user PATH so every new shell resolves all four tools by name.
Non-goals: any project code, go.mod, or application skeleton.
Boundaries: host machine, shell profiles, `~/.ssh/config`.
Data impact: none. API impact: none. Security: SSH key added to GitHub; `.gitignore` excludes secrets.
Tests: from a **fresh shell**, `Get-Command go,goimports,golangci-lint,task` resolves all four;
`go version` → 1.26.5; `go version -m` proves goimports `v0.48.0`; `golangci-lint version` →
`2.12.2`; `task --version` → `3.52.0`; `ssh -T git@github.com` authenticates; `git status` is clean;
ignored dependency/build/test paths remain untracked; `git check-attr eol` returns `lf` for Go/SQL.
Evidence: fresh-shell command resolution + tool version outputs, SSH auth, remote HEAD, Git ignore/
attribute checks.
Deps: none. Parallel-with: —. Blocks: P0-FND-001.
DoD: exact versions installed; permanent PATH contains every tool; a fresh shell resolves every
command by name; Git/SSH/ignore/LF checks pass. Global installation is bootstrap evidence only —
repository-level pins land in P0-FND-001.

#### P0-FND-001 — Go module, repo skeleton & tooling
Purpose: establish the build so every later task compiles and lints from day one.
References: [`01 §3/§7`](01-architecture.md#3-components-package-boundaries), [`08 §3`](08-engineering-standards.md#3-coding-standards).
Preconditions: P0-ENV-001 (Go + tools + Git installed).
Scope: `go.mod` (`go 1.26.0`, `toolchain go1.26.5`) with Go `tool` directives pinning goimports
`v0.48.0` and golangci-lint `v2.12.2`; `internal/{app,cli,config,platform,tray,secrets,sanitize,providers,accounts/domain,accounts/application,models,intelligence,routing,execution,quota,httpapi,httpui,storage,observability}` package skeletons; `golangci-lint` config (staticcheck, errcheck, ineffassign, gocyclo, forbidigo); Taskfile using Task `3.52.0`; LF enforcement (`.gitattributes`).
Non-goals: any behavior.
Boundaries: repo root, `internal/*`.
Data impact: none. API impact: none. Security: forbidigo rules declared. Failure/rollback: n/a.
Tests: `go build ./...` + `golangci-lint run` green on empty stubs (Win+Linux).
Evidence: CI build+lint logs both OSes.
Deps: none. Parallel-with: —. Blocks: all P0.
DoD: compiles + lints clean on both OSes; layering directories exist per [`01 §3`].

#### P0-FND-002 — Config package (precedence)
Purpose: one typed config with deterministic precedence.
References: [`01 §3`], [`08 §9`](08-engineering-standards.md#9-release--operational-readiness).
Preconditions: P0-FND-001.
Scope: defaults → env → flags; default bind `127.0.0.1:8081`; typed struct; `os.Getenv` only here (forbidigo elsewhere); documented keys.
Non-goals: secrets loading (P1).
Boundaries: `internal/config`.
Data impact: none. API impact: none. Security: env-only secret var *names* documented, values never logged. Failure/rollback: invalid config aborts startup with a clear typed error.
Tests: table-driven precedence (default/env/flag override, invalid values rejected).
Evidence: unit test report.
Deps: P0-FND-001. Parallel-with: P0-FND-003/006, P0-EXEC-001. Blocks: P0-FND-007.
DoD: precedence proven; bind default correct; no `os.Getenv` outside `config` (forbidigo).

#### P0-FND-003 — Platform paths
Purpose: OS-correct data dirs.
References: [`01 §3`](01-architecture.md#3-components-package-boundaries).
Preconditions: P0-FND-001.
Scope: `%LOCALAPPDATA%\venom-router` (Windows), `$XDG_DATA_HOME/venom-router` (Linux) resolution; data-dir creation with correct perms.
Non-goals: keyring file (P1).
Boundaries: `internal/platform`. Data/API impact: none. Security: dir perms owner-only. Failure/rollback: unwritable dir aborts startup.
Tests: unit per-OS path resolution (build-tagged).
Evidence: unit report both OSes.
Deps: P0-FND-001. Parallel-with: P0-FND-002/006. Blocks: P0-FND-005/007.
DoD: correct paths + perms on both OSes.

#### P0-FND-004 — CLI dispatch & graceful shutdown
Purpose: the two run modes + clean lifecycle.
References: [`01 §2`](01-architecture.md#2-process-model).
Preconditions: P0-FND-001/002.
Scope: `serve`/`version`/`help`/bare→tray dispatch; SIGINT/SIGTERM graceful shutdown with bounded timeout; tray entry is a stub (real tray P6).
Non-goals: tray UI (P6).
Boundaries: `internal/cli`, `internal/app`. Data/API impact: none. Security: n/a. Failure/rollback: shutdown drains within timeout then force-exits.
Tests: unit dispatch; integration graceful-shutdown returns within bound.
Evidence: shutdown timing log.
Deps: P0-FND-002. Parallel-with: DB track. Blocks: P0-FND-007.
DoD: modes dispatch; shutdown bounded on both OSes.

#### P0-FND-005 — Single-instance lock
Purpose: prevent first-run races.
References: [`01 §2`](01-architecture.md#2-process-model).
Preconditions: P0-FND-003.
Scope: lock on `<dataDir>/venom.lock` acquired **before** any keyring/DB creation; second instance surfaces "already running" and focuses the first (focus best-effort).
Non-goals: keyring (P1).
Boundaries: `internal/app`, `internal/platform`. Data impact: lockfile. API impact: none. Security: n/a. Failure/rollback: lock failure aborts startup cleanly.
Tests: integration — second launch rejected; stale lock recovered.
Evidence: two-launch test log.
Deps: P0-FND-003. Parallel-with: EXEC track. Blocks: P0-FND-007.
DoD: single-instance enforced; acquired before DB/keyring.

#### P0-FND-006 — Structured secret-free logging baseline
Purpose: the logging boundary everything uses.
References: [`01 §3`](01-architecture.md#3-components-package-boundaries), [`08 §3/§6`](08-engineering-standards.md#3-coding-standards).
Preconditions: P0-FND-001.
Scope: structured logger (`slog`-based) wrapper; forbidigo bans raw `fmt.Print*`/`panic` in request paths; log fields are typed; no secret sink (full redaction lands in P1 `sanitize`).
Non-goals: `sanitize` package (P1-SEC-005).
Boundaries: `internal/observability`. Data/API impact: none. Security: no raw logging; foundation for canary. Failure/rollback: n/a.
Tests: unit — forbidden constructs fail lint; logger emits structured records.
Evidence: lint + unit logs.
Deps: P0-FND-001. Parallel-with: all P0. Blocks: P1-SEC-006.
DoD: structured logging in place; forbidigo green.

#### P0-DB-001 — SQLite open + pragmas
Purpose: the single-writer store.
References: [`01 §5`](01-architecture.md#5-persistence--sqlite).
Preconditions: P0-FND-003.
Scope: `modernc.org/sqlite`; one file `<dataDir>/venom.db`; pragmas `journal_mode=WAL`, `busy_timeout=5000`, `foreign_keys=ON`, `synchronous=NORMAL`; single writer discipline; typed-repository base (no ad-hoc SQL elsewhere — lint).
Non-goals: any table (M1+ later).
Boundaries: `internal/storage`. Data impact: DB file + pragmas. API impact: none. Security: file perms owner-only. Failure/rollback: open failure aborts startup.
Tests: integration — pragmas set; WAL active; concurrent-open guarded.
Evidence: pragma assertion log.
Deps: P0-FND-003. Parallel-with: EXEC track. Blocks: P0-DB-002.
DoD: DB opens with correct pragmas both OSes.

#### P0-DB-002 — Migration runner (goose, checksummed, integrity-checked, rollback-tested)
Purpose: deterministic schema evolution.
References: [`01 §5`](01-architecture.md#5-persistence--sqlite), [`08 §4`](08-engineering-standards.md#4-data--migrations).
Preconditions: P0-DB-001.
Scope: goose embedded; checksum guard; integrity check on open (`PRAGMA integrity_check`); LF line endings forced (stable checksums cross-OS; watch Windows `file:///C:/...`); forward-only in prod with a **tested down path in dev**; a baseline/system migration (schema-version table).
Non-goals: domain tables (M1+).
Boundaries: `internal/storage` (migrations). Data impact: baseline migration. API impact: none. Security: n/a. Failure/rollback: checksum/integrity mismatch fails closed on open; every migration has a tested down path.
Tests: integration — up→down→up; checksum tamper rejected; integrity check on open; cross-OS checksum stability.
Evidence: migration up/down + integrity CI log both OSes.
Deps: P0-DB-001. Parallel-with: EXEC track. Blocks: P0-FND-007, every M-group.
DoD: migrations run, verify, roll back in dev, and fail closed on tamper — both OSes.

#### P0-EXEC-001 — Vendor embedded Bifrost core
Purpose: the commodity execution plumbing.
References: [`01 §4.3/§7`](01-architecture.md#4-the-execution-boundary), [`08 §2/§8`](08-engineering-standards.md#2-repository--module-layering).
Preconditions: P0-FND-001.
Scope: fetch the approved module pin `github.com/maximhq/bifrost/core@v1.7.3` using the upstream
installation contract; pin the corresponding release commit
`8f0ef396f589528210f6409383c90863bfa1e99f` in `third_party/bifrost` as a submodule; wire the Go
`replace`; add a check that flags local modifications to vendored code.
Non-goals: fetching/installing Bifrost before this task; editing Bifrost; adding transports (P4).
Boundaries: `third_party/bifrost`, `go.mod`. Data/API impact: none. Security: n/a. Failure/rollback: build pins SHA; local-mod check blocks drift.
Tests: build includes vendored core; local-mod check green.
Evidence: submodule SHA + local-mod check log.
Deps: P0-FND-001. Parallel-with: DB track. Blocks: P0-EXEC-002.
DoD: Bifrost vendored, pinned, unmodified.

#### P0-EXEC-002 — `InferenceTransport` interface + dispatcher skeleton
Purpose: freeze the typed execution seam early.
References: [`01 §4.1/§4.2/§4.5`](01-architecture.md#41-inferencetransport-interface).
Preconditions: P0-EXEC-001.
Scope: define `ResolvedRoute`, `NormalizedRequest`/`NormalizedResponse`, `Chunk`, `Operation`, `TypedFailure`, `FailureClass`, `FailureScope`; the `InferenceTransport` interface (`Execute`/`Stream`/`Cancel`/`NormalizeError`/`SupportedCapabilities`); a single dispatcher in `execution` selecting transport **by typed capability, never slug** (custom vet check).
Non-goals: transport implementations beyond the Bifrost smoke shim (P4).
Boundaries: `internal/execution`. Data/API impact: none. Security: `NormalizeError` must never leak secrets (asserted in P4). Failure/rollback: dispatcher rejects an unresolvable route with a typed error.
Tests: unit — dispatcher can't re-select/widen a `ResolvedRoute`; slug-switch vet check present.
Evidence: interface + vet-check tests.
Deps: P0-EXEC-001. Parallel-with: DB track. Blocks: P0-EXEC-003, P4-EXEC-001.
DoD: seam types + single dispatcher exist; no-slug-switch enforced.

#### P0-EXEC-003 — Bifrost execution smoke test
Purpose: prove Venom→Bifrost hands off exactly one route.
References: [`01 §4.5`](01-architecture.md#45-enforcement), [`06 P0`](06-roadmap.md), [`08 §5`](08-engineering-standards.md#5-testing-strategy).
Preconditions: P0-EXEC-002.
Scope: a `bifrost` transport shim configured pool-size-1, one key whitelisted to one model, retries disabled; a local fake OpenAI server; one chat round-trip.
Non-goals: streaming/tools (P4).
Boundaries: `internal/execution`, test fixtures. Data/API impact: none. Security: fake server only. Failure/rollback: n/a.
Tests: integration — one chat round-trips; Bifrost cannot re-select.
Evidence: smoke-test CI log both OSes.
Deps: P0-EXEC-002. Parallel-with: DB track. Blocks: P0 gate.
DoD: fake chat round-trips through Bifrost; single-route handoff proven.

#### P0-CAPI-001 — `/health` liveness endpoint
Purpose: the Phase-0 gate + external liveness probe surface.
References: [`01 §6a/§6d`](01-architecture.md#6d-health-endpoints-the-final-single-choice), [`09 §2`](09-control-api.md).
Preconditions: P0-FND-007 (mux).
Scope: `GET /health` — unauthenticated, **outside** `/api/control/v1`, behind the loopback + Host-allowlist network gate; minimal liveness only (process up, listener accepting); no owner data; no session/CSRF.
Non-goals: authenticated readiness `/api/control/v1/health` (optional, not V1).
Boundaries: `internal/httpapi`. Data impact: none. API impact: `/health`. Security: network-gated, unauthenticated, no owner data. Failure/rollback: n/a.
Tests: integration — `/health` 200 with no session; not under `/api/control/v1`; no duplicate liveness surface.
Evidence: `/health` test log.
Deps: P0-FND-007. Parallel-with: —. Blocks: P0 gate.
DoD: single liveness surface, unauthenticated, network-gated.

#### P0-FND-007 — Composition root & fail-closed startup order
Purpose: wire the binary with the mandated startup order.
References: [`01 §2`](01-architecture.md#2-process-model).
Preconditions: P0-FND-002/003/004/005/006, P0-DB-002.
Scope: startup order — validate embedded assets → acquire lock → load/create keyring (stub until P1) → open SQLite + migrate → reconcile keyring/DB + validate ciphertext (stub until P1) → build repositories → build provider registry → build services → mount HTTP mux → listen. Any integrity failure aborts before opening a listener; provider outages/empty pools do **not** abort.
Non-goals: real keyring/reconcile (P1), real registry/services (P2b+).
Boundaries: `internal/app`. Data impact: none new. API impact: mounts mux (incl. `/health`). Security: fail-closed ordering. Failure/rollback: integrity failure → no listener, clean abort, no half-state.
Tests: integration — startup order enforced; integrity failure aborts before listen; happy path listens.
Evidence: startup-order test + boot log.
Deps: P0-DB-002, P0-FND-002/003/004/005/006. Parallel-with: —. Blocks: P0-CAPI-001, P0 gate.
DoD: boots in the mandated order, fails closed, mounts `/health`.

#### P0-TEST-001 — Static gate suite (baseline) wired into CI
Purpose: the always-on invariants harness.
References: [`08 §5/§7`](08-engineering-standards.md#5-testing-strategy).
Preconditions: P0-FND-001/006, P0-EXEC-002.
Scope: wire CI to run on Win+Linux: `gofmt`/`goimports`, `go vet` + `golangci-lint`, the import-graph/layering test (acyclic; forbidden edges), the no-slug-switch vet check, race detector, and a placeholder for schema-lint/no-hardcoding/secret-canary (populated in P1/P3). One command, blocking.
Non-goals: domain suites (per phase).
Boundaries: CI config, `internal/*` test helpers. Data/API impact: none. Security: canary hook reserved. Failure/rollback: red gate blocks merge.
Tests: the gate suite itself (meta-test that a forbidden import fails).
Evidence: CI run both OSes.
Deps: P0-FND-001/006, P0-EXEC-002. Parallel-with: all P0. Blocks: every later gate.
DoD: static gates run green on both OSes and fail on an injected violation.

---

### Phase P1 — Secrets & keyring

- **Objective:** encryption at rest with a key outside the DB, bound-AAD, crash-safe rotation,
  startup reconciliation that fails closed, and a proven secret canary.
- **Scope:** `SEC`.
- **Out of scope:** provider credentials (P2b), owner password (P2b), backup container (P8).
- **Entry:** P0 gate green.
- **Dependencies:** P0-DB (ciphertext storage reference), P0-FND-006/007 (logging, startup reconcile hook).
- **Parallelization:** `sanitize` (SEC-005) + canary (SEC-006) run parallel to keyring tasks.
- **Expected artifacts:** `<dataDir>/secrets/keyring.json`, encrypt/decrypt with bound AAD, rotation barrier, canary test.
- **DB impact:** none (keyring is a file); ciphertext columns are referenced but defined with credentials in M2 (P2b).
- **API impact:** none. **Security impact:** the whole phase. **DS impact:** none.
- **Testing:** unit (AAD binding, nonce uniqueness), integration (rotation re-wrap atomic; startup reconcile fails closed), canary.
- **Migration/recovery:** startup reconciliation validates every stored ciphertext before opening any listener; missing key ⇒ fail closed.
- **Acceptance gate:** credentials encrypt/decrypt with bound AAD; rotation re-wraps atomically; the canary passes.
- **Required evidence:** encrypt/decrypt round-trip with AAD, rotation atomicity, fail-closed reconcile, canary — CI both OSes.
- **Exit:** gate green.
- **Rollback/containment:** rotation is a barrier with crash-safe re-wrap; an interrupted rotation leaves the prior key usable; a missing key aborts startup (no partial decrypt).

#### P1-SEC-001 — AES-256-GCM keyring (outside SQLite)
Purpose: master key material stored outside the DB.
References: [`01 §8`](01-architecture.md#8-security-model).
Preconditions: P0-FND-007.
Scope: keyring at `<dataDir>/secrets/keyring.json` (owner-only perms); env override `VENOM_ENCRYPTION_KEY`; load/create in memory during startup; `key_id` versioning.
Non-goals: rotation (SEC-003).
Boundaries: `internal/secrets`, `internal/platform`. Data impact: keyring file. API impact: none. Security: key never in DB, never logged; file perms enforced. Failure/rollback: missing/corrupt keyring at startup → fail closed.
Tests: unit — create/load; perms; env override; missing-key path fails closed.
Evidence: keyring lifecycle test.
Deps: P0-FND-007. Parallel-with: P1-SEC-005. Blocks: P1-SEC-002.
DoD: keyring created/loaded outside DB with owner-only perms.

#### P1-SEC-002 — Encrypt/decrypt with bound AAD
Purpose: authenticated encryption bound to record identity.
References: [`01 §8`](01-architecture.md#8-security-model), [`02 §3`](02-domain-model.md).
Preconditions: P1-SEC-001.
Scope: AES-256-GCM; fresh nonce per encryption; AAD **derived** (never stored) from `(purpose, provider, account, record, kind)`; envelope stores `key_id`, `nonce`, `ciphertext`.
Non-goals: credential rows (M2/P2b).
Boundaries: `internal/secrets`. Data impact: defines envelope shape used by M2 columns. API impact: none. Security: AAD binding prevents ciphertext relocation; nonce uniqueness enforced. Failure/rollback: decrypt with wrong AAD fails (tamper-evident).
Tests: unit — round-trip; AAD mismatch fails; nonce uniqueness; ciphertext relocation rejected.
Evidence: crypto unit report.
Deps: P1-SEC-001. Parallel-with: P1-SEC-005. Blocks: P1-SEC-003/004, P2b credential storage.
DoD: bound-AAD encryption proven tamper-evident.

#### P1-SEC-003 — Rotation barrier + crash-safe rotation
Purpose: rotate the master key without data loss.
References: [`01 §8`](01-architecture.md#8-security-model).
Preconditions: P1-SEC-002.
Scope: rotation barrier; re-wrap all ciphertext atomically to a new `key_id`; crash-safe (interrupted rotation leaves prior key usable and resumable).
Non-goals: scheduled rotation UI.
Boundaries: `internal/secrets`, `internal/storage`. Data impact: `key_id` updates on re-wrap. API impact: none. Security: no plaintext persisted during rotation. Failure/rollback: crash mid-rotation → resume/rollback to prior key; never a mixed unreadable state.
Tests: integration — rotation re-wraps atomically; crash mid-rotation recovers.
Evidence: rotation atomicity + crash-recovery test.
Deps: P1-SEC-002. Parallel-with: P1-SEC-005. Blocks: P1 gate.
DoD: atomic, crash-safe rotation proven.

#### P1-SEC-004 — Startup ciphertext reconciliation (fail closed)
Purpose: never open a listener with unreadable secrets.
References: [`01 §2/§8`](01-architecture.md#2-process-model).
Preconditions: P1-SEC-002, P0-FND-007.
Scope: reconcile keyring with DB and **validate every stored ciphertext before opening any listener**; a DB reference to a key the keyring lacks aborts startup with a clear error.
Non-goals: credential rows (M2/P2b) — validated once they exist.
Boundaries: `internal/app`, `internal/secrets`, `internal/storage`. Data impact: read-only validation. API impact: none. Security: fail-closed on missing key. Failure/rollback: any validation failure aborts before listen.
Tests: integration — missing key aborts startup; all-valid path listens.
Evidence: fail-closed reconcile test.
Deps: P1-SEC-002. Parallel-with: P1-SEC-005. Blocks: P1 gate.
DoD: startup fails closed on any unreadable ciphertext.

#### P1-SEC-005 — `sanitize` package (full redaction boundary)
Purpose: guarantee no secret crosses a log/error/trace/audit boundary.
References: [`01 §3/§8`](01-architecture.md#3-components-package-boundaries), [`08 §6`](08-engineering-standards.md#6-definition-of-done).
Preconditions: P0-FND-006.
Scope: full `[REDACTED]` replacement (never partial); applied at the boundary for logs, errors, traces, audit rows; redacts credentials/tokens/OAuth `code`/`state`/PKCE verifiers/`Authorization` headers.
Non-goals: per-domain field maps (extended as fields appear).
Boundaries: `internal/sanitize`. Data/API impact: none. Security: the core redaction contract. Failure/rollback: unknown/uncertain field defaults to redaction (fail closed).
Tests: unit — known secret shapes fully redacted; partial redaction never emitted.
Evidence: redaction unit report.
Deps: P0-FND-006. Parallel-with: keyring tasks. Blocks: P1-SEC-006.
DoD: full redaction at the boundary, fail-closed on uncertainty.

#### P1-SEC-006 — Secret canary test (runs every build)
Purpose: mechanically prove secrets never leak.
References: [`08 §5/§7`](08-engineering-standards.md#5-testing-strategy).
Preconditions: P1-SEC-002/005.
Scope: inject a known secret through encryption + logging + error + trace + audit paths; assert it appears in **no** output; wire into the P0 static gate suite to run every build.
Non-goals: —.
Boundaries: test harness across `secrets`/`sanitize`/`observability`. Data/API impact: none. Security: the canary invariant. Failure/rollback: any leak fails the build.
Tests: the canary itself + a meta-test that an intentional leak fails it.
Evidence: canary result in CI both OSes.
Deps: P1-SEC-002, P1-SEC-005. Parallel-with: —. Blocks: P1 gate; runs forever after.
DoD: canary green every build; catches an injected leak.

---

### Phase P2a — Design System integration (consume the finished package)

- **Objective:** the dashboard workspace consumes the approved, versioned `@venom/design-system`
  package correctly (styles, tokens, Tailwind preset, themes, density), embedded into the binary —
  with the package treated as read-only and its own gate re-verified green.
- **Scope:** `DS`, minimal `UI`(embed), `FND`(httpui).
- **Out of scope:** any production Venom UI surface (P2b+); any change to the Design System.
- **Entry:** P1 gate green **and** the Design System package has passed its own acceptance gate
  (evidence: `Design_System/validation/report.md` = 12/12 PASS) and exposes a consumable versioned package.
- **Dependencies:** P0 (app + build + `httpui` embed), the DS package (external, frozen).
- **Parallelization:** DS-001..005 have **no hard dependency on P1** and may run alongside P1; the
  phase gate is ordered after P1 per the roadmap.
- **Expected artifacts:** dashboard workspace; `file:` dependency on `@venom/design-system`; global
  styles + tokens + Tailwind preset wired; `go:embed` pipeline; consumption-mode doc.
- **DB impact:** none. **API impact:** none (theme/density persistence endpoint lands in P2b).
- **Security impact:** offline/no-CDN enforced; no secret in the bundle.
- **DS impact:** **consumption only** — the package is not modified; generated artifacts are never hand-edited.
- **Testing:** DS package `npm run validate` (12/12) re-run as a pinned prerequisite; a smoke page
  renders inventory across 3 themes × 2 densities; app-side no-raw-values lint (via the generated Tailwind preset).
- **Migration/recovery:** none.
- **Acceptance gate:** the DS package gate is green (12/12 `validate`); the app consumes the versioned
  package (not a copy of components); tokens compile to CSS + Tailwind; the app renders the DS
  inventory in Venom Dark, Venom Light, and High Contrast. **No production Venom UI is built until this gate is green.**
- **Required evidence:** DS `validate` report (12/12); a rendered smoke page screenshot per theme;
  proof the app imports from `@venom/design-system` (no vendored component copies); embed build log.
- **Exit:** gate green.
- **Rollback/containment:** DS integration is additive; a broken integration is reverted without touching the frozen package.

#### P2a-DS-001 — Dashboard workspace + package dependency
Purpose: create the consuming workspace and wire the package.
References: [`Design_System/SKILL.md`], [`.../validation/handoff-contract.md`], [`07 §2.3`](07-design-system.md), [`08 §2`](08-engineering-standards.md#2-repository--module-layering).
Preconditions: P0-FND-001; DS package present + gate-green.
Scope: dashboard workspace (React 18.3 + Vite + TS strict, ESLint/Prettier); depend on `@venom/design-system` via `file:` (supported consumption mode 1); build the DS package first (`npm run build` in `Design_System/`) so `dist/` exists before consumption.
Non-goals: copying components (forbidden); handoff-copy mode (mode 2 documented as alternative only).
Boundaries: dashboard workspace, `package.json`. Data/API impact: none. Security: no CDN; offline install from `package-lock.json`. Failure/rollback: build fails if `dist/` missing.
Tests: workspace builds; import from `@venom/design-system` resolves.
Evidence: install + build log; dependency graph shows the package (no component copies).
Deps: P0-FND-001. Parallel-with: P1. Blocks: P2a-DS-002/003, P2a-UI-001.
DoD: workspace consumes the versioned package; no duplicated components.

#### P2a-DS-002 — Global styles, tokens, theme & density wiring
Purpose: apply the one stylesheet and runtime theming.
References: [`Design_System/SKILL.md`], [`.../handoff-contract.md`], [`07 §2.3/§3`](07-design-system.md).
Preconditions: P2a-DS-001.
Scope: `import "@venom/design-system/styles.css"`; set `data-theme` (`venom-dark` default) + `data-density` (`comfortable` default) on root; use `applyTheme`/`applyDensity` from `/themes` + `/density`; consume `THEMES`/`DENSITIES`/`DEFAULT_THEME`; server-driven persistence deferred to P2b (`PUT /settings`) — default until then.
Non-goals: browser-storage persistence (forbidden by contract).
Boundaries: dashboard workspace. Data/API impact: none yet. Security: n/a. Failure/rollback: n/a.
Tests: theme/density switch flips attributes; all 3 themes load.
Evidence: theme-switch demo.
Deps: P2a-DS-001. Parallel-with: P2a-DS-003. Blocks: P2a-DS-004.
DoD: styles applied; theme/density runtime-switch via package helpers.

#### P2a-DS-003 — Tailwind preset integration + no-raw-values lint
Purpose: utilities resolve to tokens, never literals.
References: [`.../handoff-contract.md`], [`07 §2.3/§8`](07-design-system.md).
Preconditions: P2a-DS-001.
Scope: `import { venomTailwindPreset } from "@venom/design-system/tailwind"`; `presets: [venomTailwindPreset]`; **never** hand-author/duplicate a token→Tailwind mapping; add the app-repo no-raw-values lint (bans hex/rgb/hsl, raw px spacing, raw shadows, off-scale numbers) — CI-blocking.
Non-goals: editing the generated Tailwind artifact.
Boundaries: dashboard `tailwind.config`, lint config. Data/API impact: none. Security: n/a. Failure/rollback: lint blocks a raw value.
Tests: an injected raw hex fails the lint; utilities resolve to `var(--…)`.
Evidence: lint run; a utility→token resolution proof.
Deps: P2a-DS-001. Parallel-with: P2a-DS-002. Blocks: every UI task.
DoD: Tailwind preset wired; raw values blocked in the app.

#### P2a-UI-001 — `httpui` embed pipeline (`go:embed`)
Purpose: ship the dashboard inside the one binary.
References: [`01 §1/§3`](01-architecture.md#1-shape-at-a-glance).
Preconditions: P2a-DS-001, P0-FND-007.
Scope: build dashboard → `go:embed dist` in `internal/httpui` → serve on the control plane with SPA fallback; behind the loopback + Host gate.
Non-goals: real surfaces (P2b+).
Boundaries: `internal/httpui`, build pipeline. Data/API impact: serves `/` SPA. Security: served only behind the control-plane network gate. Failure/rollback: missing `dist` fails the build (fail closed).
Tests: integration — embedded SPA served; SPA fallback works; missing-asset fails build.
Evidence: embed build + serve test.
Deps: P2a-DS-001. Parallel-with: P2a-DS-002/003. Blocks: P2b UI.
DoD: embedded dashboard served behind the network gate.

#### P2a-DS-004 — Consumption verification (inventory in 3 themes × 2 densities)
Purpose: prove the package renders correctly end-to-end.
References: [`07 §3/§8/§10`](07-design-system.md), [`.../handoff-contract.md`], [`Design_System/validation/report.md`].
Preconditions: P2a-DS-002/003.
Scope: a smoke page composing representative primitives + one domain component from `/primitives` and `/domain` (e.g. `Button`, `Table`, `ThemeSwitcher`, `ProviderFleet`) rendering across all 3 themes × 2 densities; re-run the DS package `npm run validate` (12/12) as a pinned prerequisite; wire app CI to require DS validate-green + build before UI work.
Non-goals: production pages.
Boundaries: dashboard workspace, CI. Data/API impact: none. Security: n/a. Failure/rollback: a failed DS validate blocks UI work.
Tests: smoke render per theme×density; DS validate 12/12 asserted in CI.
Evidence: DS `report.md` (12/12); per-theme render screenshots.
Deps: P2a-DS-002/003. Parallel-with: —. Blocks: P2a gate.
DoD: inventory renders in all 3 themes; DS gate re-verified green in app CI.

#### P2a-DS-005 — Pin version + handoff-contract adherence check
Purpose: lock the integration contract and the no-edit rule.
References: [`.../handoff-contract.md`], [`Design_System/validation/handoff-manifest.json`], [`07 §9/§10`](07-design-system.md).
Preconditions: P2a-DS-004.
Scope: pin `@venom/design-system` version; document the chosen consumption mode; add a check that the app never imports from the package's generated internals or vendors component copies, and that no app code edits package files; record the offline/no-CDN guarantee.
Non-goals: token changes (would be a DS-package change, out of scope here).
Boundaries: dashboard workspace, CI. Data/API impact: none. Security: offline enforced. Failure/rollback: a component copy or package edit fails the check.
Tests: adherence check fails on an injected component copy.
Evidence: pinned version; adherence-check log.
Deps: P2a-DS-004. Parallel-with: —. Blocks: P2a gate.
DoD: version pinned; package read-only to the app, enforced.

---

## 10. Phase structure — Providers, accounts, enrollment & control plane (P2b)

### Phase P2b — Providers, accounts, enrollment (the Provider Fleet)

- **Objective:** owner authentication + the control plane + provider/account enrollment for one
  API-key provider (`opencode-zen`) and one OAuth provider (`antigravity`), with the Provider Fleet UI.
- **Scope:** `DB`(M1–M3), `SEC`, `CAPI`, `JOBS`, `DOM`, `PROV`, `OBS`, `UI`.
- **Out of scope:** discovery pipeline (P3a), quota (P3b), probing/cert (P3c), routing (P4),
  remaining providers + custom path (P7), full Settings surface (P6).
- **Entry:** P1 gate green; P2a gate green (for the UI tasks).
- **Dependencies:** P1 (keyring/crypto for credentials), P2a (DS package for UI), P0 (mux/startup/jobs mount).
- **Parallelization:** `DOM` pure packages, `PROV` framework, and `SEC` auth can develop in parallel;
  **all control-plane mutation endpoints hard-depend on the SEC auth + CSRF middleware.**
- **Expected artifacts:** M1–M3 migrations; auth endpoints; control middleware; enrollment + account
  endpoints; job surface; two working adapters; Provider Fleet + auth UI.
- **DB impact:** M1 (`owner_auth`, `owner_sessions`, `auth_events`); M2 (`providers`, `accounts`,
  `account_credentials` + 3 partial indexes, `account_funding_evidence` + current index,
  `oauth_transactions`); M3 (`audit_events`, `jobs`).
- **API impact:** `/auth/*`, `/jobs/{job_id}`, `/providers*`, `/accounts*`, OAuth begin/callback/status, `/settings` (theme/density subset).
- **Security impact:** the entire owner-auth model + loopback/Host gate + CSRF + reveal re-verify.
- **DS impact:** consumes `ProviderFleet`, `Security` (auth), primitives; no package change.
- **Testing:** owner-auth suite; credential cardinality + reauth interruption; enrollment E2E on real accounts; fixture contract tests for the two adapters.
- **Migration/recovery:** M1 first (auth before anything); `reauth_in_progress` cleared by startup reconciliation; stale `staged` credentials discarded on startup.
- **Acceptance gate:** **(auth)** first run forces setup; login yields a session; a session past idle
  (30 min) or absolute (12 h) expiry is rejected; a mutation without a valid session-bound CSRF token
  is rejected **before any side effect**; credential reveal requires a fresh (≤ 5 min) re-verification;
  wrong-password attempts are rate-limited and audited with **no secret stored**. **(enrollment)**
  connect a real free API-key account and a real OAuth account; both show correct identity, funding,
  and health in the fleet UI; secrets never logged; duplicates handled friendly.
- **Required evidence:** owner-auth negative-test suite; CSRF-before-side-effect proof; reveal-gated-on-reverify
  proof; canary on auth audit rows; two real-account connect recordings; fixture contract test reports.
- **Exit:** gate green (auth + enrollment), Win+Linux.
- **Rollback/containment:** enrollment creates no account/credential before authentic validation
  succeeds; reauth staging never touches the active credential until the atomic swap; soft-disconnect
  is reversible via re-enrollment; a failed migration group rolls back (down path) in dev.

**Migration ordering within P2b (auth before mutation):** `M1 → M2 → M3`.

#### P2b-DB-001 — Migration M1: owner auth
Purpose: persist the single-owner identity + sessions + auth audit *first*.
References: [`02 §3 owner-auth`](02-domain-model.md#owner-authentication-single-owner-identity-session-re-verification), [`09 §5`](09-control-api.md#5-owner-authentication-first-run-login-session-re-verification).
Preconditions: P0-DB-002.
Scope: `owner_auth` (exactly one row: `password_hash` Argon2id, `salt`, KDF params `time/mem_kib/threads/key_len`, timestamps); `owner_sessions` (opaque handle **hash/verifier only**, `created_at`, `last_seen_at`, `idle_expires_at`, `absolute_expires_at`, `revoked_at`, `reverify_fresh_until`); `auth_events` (append-only `{action,result,reason_code,at}`, no secret). CHECK/constraints; tested down path.
Non-goals: any other table.
Boundaries: `internal/storage` migrations. Data impact: M1 tables. API impact: none. Security: hash-only; no password/secret columns. Failure/rollback: down path in dev; forward-only prod.
Tests: migration up/down; single-`owner_auth`-row constraint; append-only `auth_events`.
Evidence: migration CI log.
Deps: P0-DB-002. Parallel-with: P2b-DOM-*. Blocks: P2b-SEC-001, P2b-DB-002.
DoD: M1 applies/rolls back; auth schema present.

#### P2b-DB-002 — Migration M2: providers, accounts, credentials, funding, oauth_transactions
Purpose: the enrollment core schema.
References: [`02 §3/§5`](02-domain-model.md#5-sqlite-schema-sketch), [`03 §2`](03-provider-integration-catalog.md#2-enrollment-flows).
Preconditions: P2b-DB-001, P1-SEC-002 (envelope shape).
Scope: `providers` (funding_mode/fixed/locked/non_expiring); `accounts` (multi-axis `connection_state`+`health_state` CHECKs, `reauth_in_progress`, `UNIQUE(provider_id,external_id)`, `UNIQUE(id,provider_id)`); `account_credentials` (`kind`, `state` CHECK `{active,staged,retired}`, `key_id/nonce/ciphertext`, `expires_at`) + `idx_cred_active_per_kind`, `idx_cred_staged_per_kind`, `idx_cred_fingerprint` (partial indexes); `account_funding_evidence` (append-only, `source`, `locked`, `confidence`, `evidence_json`, `superseded_at`) + `idx_funding_current`; `oauth_transactions` (`sha256(state)`, provider, `transaction_id`, encrypted verifier, expiry, consumed).
Non-goals: offerings (M4), quota (M5), keys (M7).
Boundaries: `internal/storage` migrations. Data impact: M2 tables + indexes. API impact: none. Security: ciphertext columns only for secrets; header values live in envelope (P7). Failure/rollback: down path in dev.
Tests: migration up/down; partial-index enforcement (active-per-kind, staged-per-kind, fingerprint dedup); funding one-current-row; nullable-means-unknown lint.
Evidence: migration + index-enforcement CI log.
Deps: P2b-DB-001, P1-SEC-002. Parallel-with: P2b-SEC-*. Blocks: P2b-PROV-*, P2b-CAPI-003/004.
DoD: M2 applies/rolls back; all invariants enforced structurally.

#### P2b-DB-003 — Migration M3: audit_events + jobs
Purpose: audit trail + async job persistence.
References: [`02 §3`](02-domain-model.md), [`09 §1/§3.12`](09-control-api.md#312-get-jobsjob_id--canonical-shared-async-job-status).
Preconditions: P2b-DB-002.
Scope: `audit_events` (append-only, ids/codes/timestamps only); `jobs` (`kind`, `status` CHECK `{pending,running,completed,failed,expired}`, `started_at`, `finished_at`, `result_ref`, `error`, TTL/retention fields).
Non-goals: route/usage records (M6/M7).
Boundaries: `internal/storage` migrations. Data impact: M3 tables. API impact: none. Security: audit rows secret-free (canary). Failure/rollback: down path in dev.
Tests: migration up/down; audit append-only; job state CHECK.
Evidence: migration CI log.
Deps: P2b-DB-002. Parallel-with: —. Blocks: P2b-JOBS-001, P2b-OBS-001.
DoD: M3 applies/rolls back.

#### P2b-SEC-001 — First-run owner setup
Purpose: create the single owner password once.
References: [`09 §5.1`](09-control-api.md#5-owner-authentication-first-run-login-session-re-verification).
Preconditions: P2b-DB-001.
Scope: `POST /auth/setup` — precondition no `owner_auth` row; min length/entropy; per-install random salt; Argon2id (`time=3, mem_kib=65536, threads=4, key_len=32`, stored); write the one row; create the first session (→ SEC-002); `setup_already_complete` (409) if set; rate-limited; `GET /auth/status` → `{setup_complete}`.
Non-goals: login (SEC-002).
Boundaries: `internal/httpapi`(auth), `internal/secrets`. Data impact: `owner_auth` row. API impact: `/auth/setup`, `/auth/status`. Security: password never logged; hash-only. Failure/rollback: second setup rejected.
Tests: setup-once; second setup rejected; password never in any log/audit (canary); params stored.
Evidence: setup test + canary.
Deps: P2b-DB-001. Parallel-with: P2b-PROV-*. Blocks: P2b-SEC-002.
DoD: setup-once with Argon2id + first session; canary clean.

#### P2b-SEC-002 — Login / logout + session creation
Purpose: authenticate and mint opaque sessions.
References: [`09 §5.2`](09-control-api.md#5-owner-authentication-first-run-login-session-re-verification).
Preconditions: P2b-SEC-001.
Scope: `POST /auth/login` (constant-time verify; mint high-entropy handle; store **verifier only**; set cookie `HttpOnly; SameSite=Strict; Path=/api/control/v1; Secure-when-TLS`; issue CSRF token via SEC-004); `POST /auth/logout` (revoke + clear cookie); `GET /auth/session`; generic `invalid_credentials` (401); `locked_out` (429) via SEC-006.
Non-goals: idle/absolute expiry mechanics (SEC-003).
Boundaries: `internal/httpapi`(auth). Data impact: `owner_sessions` rows. API impact: `/auth/login`, `/auth/logout`, `/auth/session`. Security: opaque server-side session; cookie carries only the handle. Failure/rollback: revoked session never resurrected.
Tests: login success/failure (generic); cookie flags; logout revokes.
Evidence: login/logout test.
Deps: P2b-SEC-001. Parallel-with: —. Blocks: P2b-SEC-003/004, P2b-CAPI-001.
DoD: opaque sessions minted/revoked with correct cookie semantics.

#### P2b-SEC-003 — Session lifecycle (idle/absolute/renewal/revocation/restart)
Purpose: enforce session expiry and persistence.
References: [`09 §5.3`](09-control-api.md#5-owner-authentication-first-run-login-session-re-verification).
Preconditions: P2b-SEC-002.
Scope: idle 30 min (sliding: advance `last_seen_at`/`idle_expires_at`, never past absolute), absolute 12 h (hard cap); expiry → `session_expired` (401) + revoke; password change revokes **all** sessions; restart re-validates rows (no in-memory-only trust); owner-tunable within bounds.
Non-goals: CSRF (SEC-004).
Boundaries: `internal/httpapi`(auth), `internal/storage`. Data impact: session updates. API impact: 401 on expiry. Security: server-side lifecycle. Failure/rollback: expired/revoked never resurrected.
Tests: idle expiry; absolute cap not extended by activity; restart re-validation; password change revokes all.
Evidence: expiry negative-test suite.
Deps: P2b-SEC-002. Parallel-with: P2b-SEC-004. Blocks: P2b gate.
DoD: idle+absolute expiry + revocation proven; survives restart.

#### P2b-SEC-004 — CSRF issuance & validation (session-bound)
Purpose: block cross-site state changes before any side effect.
References: [`09 §5.4`](09-control-api.md#5-owner-authentication-first-run-login-session-re-verification).
Preconditions: P2b-SEC-002.
Scope: session-bound CSRF token issued on login/setup (JSON body and/or readable `XSRF-TOKEN` cookie); every `POST/PUT/DELETE` presents `X-CSRF-Token`; constant-time, session-bound validation; failure → `csrf_failed` (403) **before any side effect**; `GET` needs none.
Non-goals: —.
Boundaries: `internal/httpapi`(auth+control middleware). Data impact: none. API impact: CSRF on all mutations. Security: primary CSRF defense. Failure/rollback: rejection before side effect.
Tests: missing/forged/cross-session token → 403 before mutation; GET exempt.
Evidence: CSRF negative tests.
Deps: P2b-SEC-002. Parallel-with: P2b-SEC-003. Blocks: P2b-CAPI-001, all mutation endpoints.
DoD: CSRF enforced before side effects, session-bound.

#### P2b-SEC-005 — Re-verification freshness
Purpose: gate sensitive ops on a recent password proof.
References: [`09 §5.5`](09-control-api.md#5-owner-authentication-first-run-login-session-re-verification).
Preconditions: P2b-SEC-002.
Scope: `POST /auth/reverify` (constant-time verify → stamp `reverify_fresh_until = now + 5 min`; never a new session/account); consumption: a sensitive endpoint requires `reverify_fresh_until > now` else `reverification_required` (401); freshness expires at exactly 5 min.
Non-goals: which endpoints are sensitive (reveal declared in CAPI-004).
Boundaries: `internal/httpapi`(auth). Data impact: session stamp. API impact: `/auth/reverify`. Security: freshness independent of session age. Failure/rollback: stale freshness rejects sensitive op.
Tests: reverify stamps 5 min; expiry to the second; reuse past 5 min rejected.
Evidence: reverify freshness tests.
Deps: P2b-SEC-002. Parallel-with: P2b-SEC-003/004. Blocks: P2b-CAPI-004 (reveal).
DoD: 5-min freshness gate proven.

#### P2b-SEC-006 — Auth rate limiting, lockout & audit
Purpose: throttle guessing; audit without secrets.
References: [`09 §5.6`](09-control-api.md#5-owner-authentication-first-run-login-session-re-verification).
Preconditions: P2b-SEC-002, P2b-DB-001.
Scope: rate-limit login + reverify (default: lockout after 5 consecutive failures within 15 min, exponential backoff); `locked_out` (429) with `retry_after`; every attempt (success/failure) emits an `auth_event` with **no** secret.
Non-goals: ingress RPM for `/v1/*` (P5).
Boundaries: `internal/httpapi`(auth), `internal/storage`. Data impact: `auth_events`. API impact: 429 on lockout. Security: canary on audit rows. Failure/rollback: lockout window bounded.
Tests: lockout after threshold; replay after lockout rejected; audit stores no secret (canary).
Evidence: lockout + canary tests.
Deps: P2b-SEC-002, P2b-DB-001. Parallel-with: —. Blocks: P2b gate.
DoD: lockout enforced; audit secret-free.

#### P2b-SEC-007 — Lost-password local reset (recovery)
Purpose: the local recovery path (no email/backdoor).
References: [`09 §5.7`](09-control-api.md#5-owner-authentication-first-run-login-session-re-verification).
Preconditions: P2b-SEC-001, P0-FND-004 (CLI).
Scope: a documented `venom` CLI subcommand (run on the host, proving local filesystem control) that clears the `owner_auth` row and revokes all sessions → returns to first-run `setup`; it **cannot** decrypt provider credentials (keyring untouched).
Non-goals: backup-based recovery (P8-BKP-002).
Boundaries: `internal/cli`, `internal/secrets`, `internal/storage`. Data impact: clears `owner_auth`, revokes sessions. API impact: none (CLI). Security: only resets the login gate; credentials stay keyring-protected. Failure/rollback: no credential exposure.
Tests: reset → first-run state; credentials remain encrypted/unreadable without keyring.
Evidence: reset test.
Deps: P2b-SEC-001, P0-FND-004. Parallel-with: —. Blocks: P2b gate (recovery portion).
DoD: local reset returns to setup without exposing secrets.

#### P2b-CAPI-001 — Control-plane network + auth middleware
Purpose: the mandatory network gate + owner-session gate wrapping every control endpoint.
References: [`01 §6a/§8`](01-architecture.md#6a-control-plane-owner-ui--control-api), [`09 §1`](09-control-api.md).
Preconditions: P2b-SEC-002/004.
Scope: loopback-only bind (`127.0.0.1`,`::1`; reject other binds at startup, no escape hatch); `RemoteAddr` socket loopback check (never headers); no `X-Forwarded-For` trust; Host-header allowlist → 403 **before** session/CSRF; owner-session gate on all `/api/control/v1/*` except the unauthenticated `/auth/*` handshake, transaction-based OAuth callback/status, and `/health`; the network gate and auth are **independent** (both hold).
Non-goals: per-endpoint contracts (CAPI-003/004).
Boundaries: `internal/httpapi`(control). Data impact: none. API impact: gates all control routes. Security: the core control-plane posture. Failure/rollback: any gate failure → typed error, no side effect.
Tests: non-loopback → `not_loopback` 403; bad Host → 403 before session; no `X-Forwarded-For` bypass; unauthenticated mutation → 401.
Evidence: gate negative tests.
Deps: P2b-SEC-002/004. Parallel-with: P2b-DOM-*. Blocks: all control mutation endpoints.
DoD: loopback+Host+session+CSRF enforced independently, before side effects.

#### P2b-CAPI-002 — Control API conventions (envelope, idempotency, concurrency, redaction)
Purpose: one consistent contract shape for every endpoint.
References: [`09 §1`](09-control-api.md).
Preconditions: P2b-CAPI-001.
Scope: success `{data, meta?}`; error `{error:{code,message,request_id,retryable,details?}}` with standard codes; `Idempotency-Key` on mutating POSTs (replay returns original); optimistic concurrency (`If-Match`/`expected_version` → 412); cursor pagination/filtering; secret redaction on every field/log/audit via `sanitize`.
Non-goals: specific resources.
Boundaries: `internal/httpapi`(control). Data impact: none. API impact: shared envelope. Security: redaction boundary. Failure/rollback: 412 on version mismatch; idempotent replays safe.
Tests: envelope shape; idempotent replay; 412 on stale version; responses never echo secrets.
Evidence: convention tests.
Deps: P2b-CAPI-001. Parallel-with: —. Blocks: CAPI-003/004/005, P5-PAPI-006 (extends, never forks).
DoD: conventions applied + redaction proven.

#### P2b-JOBS-001 — Job persistence + canonical `GET /jobs/{job_id}`
Purpose: the single async-status surface.
References: [`09 §1/§3.12`](09-control-api.md#312-get-jobsjob_id--canonical-shared-async-job-status).
Preconditions: P2b-DB-003, P2b-CAPI-002.
Scope: job rows (states `pending/running/completed/failed/expired`); mutations return `202 {job_id, status_url}`; **only** `GET /jobs/{job_id}` polls (OAuth transaction the sole exception); `result_ref` is a reference never inline secrets/content; typed user-safe `error`; owner-gated; retention (default 24 h) then reap; idempotent re-poll.
Non-goals: specific job kinds (discovery/probe/backup register later).
Boundaries: `internal/httpapi`(jobs), workers base. Data impact: `jobs`. API impact: `/jobs/{job_id}`. Security: owner-gated; no secret in `result_ref`/`error`. Failure/rollback: `expired` marks never-terminal-within-TTL.
Tests: no competing per-resource status endpoint (test); states/result_ref/retention/authorization.
Evidence: jobs contract tests.
Deps: P2b-DB-003, P2b-CAPI-002. Parallel-with: —. Blocks: P3a/P3b/P3c/P6/P8 async work.
DoD: one shared jobs surface, owner-gated, secret-free.

#### P2b-DOM-001 — Account multi-axis domain
Purpose: pure account entity + state machines.
References: [`02 §3 account lifecycle`](02-domain-model.md#account-lifecycle-multi-axis-state-model).
Preconditions: P2b-DB-002 (interface target), P0-FND-001.
Scope: `Account` entity; `connection_state {connecting,connected,stopped,disconnected}`; `health_state {unknown,healthy,degraded,unavailable,expired}`; `reauth_in_progress`; derived `display_status` projection (first-match precedence); legal transitions + invalid-transition rejection (audited, state unchanged); routing-eligibility projection with typed reasons. Pure (no I/O; injected clock/IDs).
Non-goals: persistence (storage implements the interface).
Boundaries: `internal/accounts/domain`. Data impact: none (interfaces). API impact: none. Security: n/a. Failure/rollback: invalid transition rejected + audited.
Tests: table-driven legal/invalid transitions; `display_status` precedence; eligibility projection.
Evidence: domain unit report.
Deps: P0-FND-001. Parallel-with: P2b-DOM-002/003, P2b-PROV-001. Blocks: P2b-CAPI-004, P3/P4 eligibility.
DoD: axes + derived status + transition legality proven, pure.

#### P2b-DOM-002 — Funding evidence domain
Purpose: pure funding classification + authority rules.
References: [`02 §2`](02-domain-model.md#2-free--paid--the-semantics-precisely), [`03 §3`](03-provider-integration-catalog.md#3-the-11-integrations).
Preconditions: P0-FND-001.
Scope: funding `{free,paid,unknown}`; source `{provider_policy, provider_evidence, owner_policy, owner_override}`; funding-authority rules (locked `provider_policy` immutable; `owner_override` never auto-superseded; `provider_evidence` may supersede prior evidence/policy; append-only supersession → one current row); `evidence_required` init stamps `unknown`/`provider_policy` (not locked). Pure.
Non-goals: provider policy data (PROV-002).
Boundaries: funding domain (in `accounts/domain`). Data impact: none. API impact: none. Security: `evidence_json` sanitized. Failure/rollback: override of locked rejected (`funding_locked`).
Tests: which-row-is-current authority rules; locked-override rejection; evidence_required stays ineligible until classified.
Evidence: funding unit report.
Deps: P0-FND-001. Parallel-with: P2b-DOM-001/003. Blocks: P2b-PROV-005/007, P2b-CAPI-004 (funding), P4 gates.
DoD: 4-source authority + append-only current-row proven, pure.

#### P2b-DOM-003 — Credential domain (cardinality + staging)
Purpose: pure credential invariants.
References: [`02 §3 credential`](02-domain-model.md#credential-encrypted-secret-for-an-account), [`03 §2e`](03-provider-integration-catalog.md#2e-oauth-reauthentication-same-identity-reconnect).
Preconditions: P0-FND-001.
Scope: kinds (`api_key/oauth2/github_oauth/copilot_service/…`); states `{active,staged,retired}`; **one active per (account,kind)**; **≤ one staged per (account,kind)**; staging-swap rule (stage→validate→atomic old active→retired + staged→active); idempotent swap keyed by single-use OAuth `state`. Pure rules (indexes enforce structurally in storage).
Non-goals: OAuth HTTP (PROV-006).
Boundaries: credential domain (in `accounts/domain`). Data impact: none. API impact: none. Security: secret material never in domain (only fingerprints/kinds). Failure/rollback: second staged rejected (`reauthentication_in_progress`).
Tests: cardinality invariants; multi-kind coexistence (github_oauth + copilot_service); staging swap idempotency.
Evidence: credential unit report.
Deps: P0-FND-001. Parallel-with: P2b-DOM-001/002. Blocks: P2b-PROV-008.
DoD: cardinality + staging rules proven, pure.

#### P2b-PROV-001 — Provider registry + typed adapter contract (freeze)
Purpose: the extension seam for all providers.
References: [`03 §1`](03-provider-integration-catalog.md#1-adapter-interfaces-the-pattern), [`01 §3`](01-architecture.md#3-components-package-boundaries), [`08 §8`](08-engineering-standards.md#8-extension-points-the-documented-recipes).
Preconditions: P0-FND-001.
Scope: registry + typed adapters `APIKeyAdapter`, `OAuthAdapter`, `ModelDiscoveryAdapter`, `QuotaAdapter`, `IdentityAdapter`, `HealthAdapter`; dispatch by typed capability (no slug switch — vet); `providers` imports neither `accounts` nor `storage`; **contract freezes at end of P2b** for P7 fan-out.
Non-goals: adapter implementations (PROV-005/007, P7).
Boundaries: `internal/providers`. Data impact: none. API impact: none. Security: adapters are network-only, touch no DB. Failure/rollback: unknown capability → typed error.
Tests: registry dispatch by capability; no-slug-switch vet; layering test (no forbidden imports).
Evidence: contract + vet tests.
Deps: P0-FND-001. Parallel-with: P2b-DOM-*. Blocks: PROV-002/005/006/007/008, all P7 adapters, P4-EXEC-001.
DoD: typed contract + registry, layering-clean, frozen for extension.

#### P2b-PROV-002 — Provider definitions (11 built-ins + custom path)
Purpose: the integration catalog + funding policies.
References: [`03 §3/§4`](03-provider-integration-catalog.md#3-the-11-integrations), [`02 §2`](02-domain-model.md#2-free--paid--the-semantics-precisely).
Preconditions: P2b-PROV-001, P2b-DB-002.
Scope: definitions for all 11 built-ins (slug, `auth_mode`, `funding_policy` mode/fixed/locked/non_expiring, base_url, capability set derived from registered adapters) + the custom OpenAI-compatible path descriptor; `GET /providers` lists all + `configured` flag; capabilities **derived from registered adapters**, not stored as truth. Only `opencode-zen` + `antigravity` get live adapters this phase; the rest are catalog entries used in P7.
Non-goals: the other adapters (P7).
Boundaries: `internal/providers`, `GET /providers` handler. Data impact: `providers` rows seeded. API impact: `GET /providers`, `GET /providers/{id}`. Security: `configured=false` lists missing env var **names** only. Failure/rollback: unset OAuth secret → "Setup required", not a crash.
Tests: catalog completeness (11 + custom); funding policy per provider (e.g. ClinePass paid-locked, evidence_required set); derived capabilities.
Evidence: `GET /providers` contract test.
Deps: P2b-PROV-001, P2b-DB-002. Parallel-with: —. Blocks: P2b-CAPI-003, P7.
DoD: catalog complete; funding policies correct; capabilities derived.

#### P2b-PROV-003 — Credential envelope storage integration
Purpose: persist encrypted credentials via the keyring.
References: [`01 §8`](01-architecture.md#8-security-model), [`02 §3`](02-domain-model.md), [`03 §2c`](03-provider-integration-catalog.md#2c-custom-openai-compatible).
Preconditions: P1-SEC-002, P2b-DB-002, P2b-DOM-003.
Scope: store/retrieve `account_credentials` via bound-AAD encryption; envelope holds token(s) + (for custom, P7) header values keyed `header:{name}`; credential lease in a callback scope (plaintext never in a long-lived field); fingerprint dedup.
Non-goals: custom header UI (P7).
Boundaries: `internal/storage`, `internal/secrets`, `internal/accounts/application`. Data impact: writes credentials. API impact: none. Security: AAD bound to record; never logs secret. Failure/rollback: decrypt failure surfaces as typed error, never plaintext.
Tests: encrypt→store→retrieve→decrypt with bound AAD; fingerprint dedup; lease scope leaves no plaintext.
Evidence: credential storage integration test.
Deps: P1-SEC-002, P2b-DB-002, P2b-DOM-003. Parallel-with: PROV-004. Blocks: PROV-005/007/008.
DoD: credentials stored encrypted with bound AAD; canary clean.

#### P2b-PROV-004 — Authentic validation rule (shared)
Purpose: prove a key actually works, not just that the host is up.
References: [`03 §1`](03-provider-integration-catalog.md#1-adapter-interfaces-the-pattern).
Preconditions: P2b-PROV-001.
Scope: shared helper — zero-cost `POST /v1/chat/completions` probe (`max_tokens: 1`); only a genuine auth error ⇒ invalid; 429/5xx ⇒ provider-unavailable (retryable), not invalid; `NormalizeAPIKey` (trim/collapse).
Non-goals: per-provider quirks (adapters).
Boundaries: `internal/providers`. Data impact: none. API impact: none. Security: probe never logs the key. Failure/rollback: ambiguous → treat as unavailable (retryable), never as valid.
Tests: fixture — 200-for-any-token endpoint still caught; 429/5xx classified retryable; genuine 401 = invalid.
Evidence: authentic-validation fixture test.
Deps: P2b-PROV-001. Parallel-with: PROV-003. Blocks: PROV-005, P7 adapters, P7-PROV-010 custom.
DoD: authentic validation proven against the 200-for-any-token trap.

#### P2b-PROV-005 — `opencode-zen` API-key adapter + connect-time sync
Purpose: the simplest proven provider, end-to-end enrollment.
References: [`03 §2b/§3 opencode-zen`](03-provider-integration-catalog.md#opencode-zen--opencode-zen--proven), [`06 P2b`](06-roadmap.md).
Preconditions: PROV-002/003/004, P2b-DOM-002.
Scope: `APIKeyAdapter.ConnectAPIKey` (authentic validation; fingerprint identity; synthetic plan); funding `owner_policy → free`; `DiscoverModels` method (fixture-tested) returning `/v1/models`; best-effort connect-time health/identity sync. **Offering *persistence* + the generation-guarded pipeline are P3a** (see [DEC-DISC-LAYERING]); this task proves the adapter capabilities at the fixture level.
Non-goals: discovery orchestration/free-safety persistence (P3a).
Boundaries: `internal/providers`, `internal/accounts/application`. Data impact: account + credential + funding rows. API impact: consumed by CAPI-003. Security: key never logged; canary. Failure/rollback: no account/credential created before validation succeeds.
Tests: fixture — identity/discovery parse; authentic validation; funding stamped `owner_policy/free`.
Evidence: adapter fixture contract test (CI-blocking).
Deps: PROV-002/003/004, P2b-DOM-002. Parallel-with: PROV-006/007. Blocks: P2b-CAPI-003, P2b-TEST-003, P3a.
DoD: opencode-zen connects a real free account with correct identity/funding; fixtures pass.

#### P2b-PROV-006 — OAuth enrollment framework (PKCE, transactions, fixed-port)
Purpose: the reusable OAuth path.
References: [`03 §2a`](03-provider-integration-catalog.md#2a-oauth-built-in), [`01 §6a`](01-architecture.md#6a-control-plane-owner-ui--control-api), [`09 §3.3`](09-control-api.md).
Preconditions: PROV-001, P2b-DB-002 (oauth_transactions), P2b-CAPI-001.
Scope: PKCE (`state`/`verifier`/S256 `challenge`); persist pending `oauth_transaction` (`sha256(state)`, provider, `transaction_id`, **encrypted** verifier, 10-min expiry, no session ref); begin → authorize URL; callback (`GET /oauth/{provider}/callback`) looks up by `sha256(state)`, constant-time verify provider, check expiry, decrypt verifier, **mark consumed + null verifier, commit**, *then* exchange code, `FetchIdentity`; **code never stored; state always verified**; status polling `GET /oauth/{transaction_id}/status`; **fixed-port loopback listener framework** (separate from control-plane port, transaction-based, not session-bound) for Codex/xAI (used in P7); `prompt=login` for Auth0 multi-account.
Non-goals: provider-specific adapters (PROV-007, P7).
Boundaries: `internal/httpapi`(oauth), `internal/accounts/application`, `internal/providers`. Data impact: `oauth_transactions`. API impact: OAuth begin/callback/status. Security: verifier encrypted; code never stored; state nonce verified; callback not session-bound. Failure/rollback: mismatch → row unchanged (replay-safe).
Tests: state-nonce verification; one-transaction consume; replay-safety; expired tx rejected; fixed-port listener isolation.
Evidence: OAuth framework tests.
Deps: PROV-001, P2b-DB-002, P2b-CAPI-001. Parallel-with: PROV-005. Blocks: PROV-007/008, P7 OAuth providers.
DoD: OAuth transaction framework proven replay-safe; fixed-port listener isolated.

#### P2b-PROV-007 — `antigravity` OAuth adapter (env-gated)
Purpose: the OAuth path end-to-end on a real provider.
References: [`03 §3 antigravity`](03-provider-integration-catalog.md#antigravity--antigravity--proven-needs-client-secret-env), [`01 §8`](01-architecture.md#8-security-model).
Preconditions: PROV-006, PROV-003, P2b-DOM-002.
Scope: OAuth2 + PKCE confidential client; client secret from `VENOM_ANTIGRAVITY_CLIENT_SECRET` (+ `..._CLIENT_ID`); unset → "Setup required", `configured=false`, list missing var **names** (never values, never crash); identity (`userinfo` + `loadCodeAssist` → project_id; `currentTier`→plan); funding `provider_evidence` (map exact plan strings only: `Free→free 0.95`, `Pro→paid 0.95`, else `unknown`); refresh with client secret; external ID = email+project_id (documented weakness).
Non-goals: discovery orchestration (P3a); quota (P3b).
Boundaries: `internal/providers`, `internal/accounts/application`. Data impact: account + credential + funding. API impact: consumed by CAPI-003. Security: client secret env-only, never logged; "Setup required" lists names. Failure/rollback: unset secret is a graceful state, not a crash.
Tests: fixture — identity/funding mapping; "Setup required" when unset; refresh path.
Evidence: adapter fixture contract test (CI-blocking) + real-account connect recording (non-CI).
Deps: PROV-006, PROV-003, P2b-DOM-002. Parallel-with: PROV-005. Blocks: P2b-CAPI-003, P2b-TEST-003.
DoD: antigravity connects a real OAuth account with correct identity/funding; "Setup required" graceful.

#### P2b-PROV-008 — OAuth reauthentication staging flow
Purpose: same-identity reconnect without breaking the active credential.
References: [`03 §2e`](03-provider-integration-catalog.md#2e-oauth-reauthentication-same-identity-reconnect), [`09 §3.4`](09-control-api.md).
Preconditions: PROV-006, P2b-DOM-003, P2b-PROV-003.
Scope: `POST /accounts/{id}/reauth/begin`; match by `(provider, external_id)`; if active → stage new credential (`state='staged'`, set `reauth_in_progress=1`), validate (zero-cost probe), **atomic swap** (old active→retired, staged→active, health→healthy, clear `reauth_in_progress`), best-effort revoke old; concurrency guard (`reauthentication_in_progress`); identity mismatch guard (`account_identity_mismatch`, old untouched); crash recovery discards stale staged (older than tx TTL), active never affected.
Non-goals: —.
Boundaries: `internal/accounts/application`, `internal/httpapi`, `internal/storage`. Data impact: staged/retired credential rows. API impact: `/accounts/{id}/reauth/begin`. Security: staged credential encrypted; canary. Failure/rollback: validation/swap failure discards staged, keeps active.
Tests: staging swap; interruption recovery; second-staged rejected; identity mismatch; multi-kind coexistence.
Evidence: reauth staging integration tests.
Deps: PROV-006, P2b-DOM-003, PROV-003. Parallel-with: —. Blocks: P2b gate.
DoD: reauth staging atomic + crash-safe; guards enforced.

#### P2b-CAPI-003 — Enrollment endpoints (API-key + OAuth) + providers
Purpose: expose enrollment behind the auth+CSRF gate.
References: [`09 §2/§3.1/§3.3`](09-control-api.md#2-endpoint-catalog).
Preconditions: P2b-CAPI-002, P2b-PROV-005/007, P2b-JOBS-001.
Scope: `GET /providers`, `GET /providers/{id}`; `POST /providers/{id}/accounts` (API-key: NormalizeAPIKey → authentic validation; funding default = policy, `owner_override` if supplied; no account before success; `account_already_connected` on different external_id; 502 provider-unavailable retryable; `Idempotency-Key`); OAuth begin/callback/status (via PROV-006). Custom path endpoint deferred to P7.
Non-goals: custom enrollment (P7-PROV-010).
Boundaries: `internal/httpapi`(control). Data impact: via adapters. API impact: enrollment endpoints. Security: behind CAPI-001 gate + CSRF; responses never echo the key. Failure/rollback: no side effect before validation.
Tests: API-key enrollment (valid/invalid/unavailable); OAuth begin→callback→status; duplicate friendly; redaction.
Evidence: enrollment contract tests + canary.
Deps: P2b-CAPI-002, P2b-PROV-005/007, P2b-JOBS-001. Parallel-with: P2b-CAPI-004. Blocks: P2b-UI-003, P2b-TEST-003.
DoD: both enrollment paths behind auth+CSRF; secrets redacted.

#### P2b-CAPI-004 — Account lifecycle endpoints
Purpose: reveal/funding/stop/resume/soft-disconnect/health/sync.
References: [`09 §2/§3.5/§3.6`](09-control-api.md#2-endpoint-catalog), [`02 §3`](02-domain-model.md).
Preconditions: P2b-CAPI-002, P2b-DOM-001/002, P2b-SEC-005, P2b-PROV-003.
Scope: `GET /accounts`, `GET /accounts/{id}` (multi-axis projection); `POST /accounts/{id}/reveal` (**requires fresh ≤5-min re-verify** else `reverification_required`; `Cache-Control: no-store`; decrypt once; audit; rate-limited); `PUT /accounts/{id}/funding` (append `owner_override`; `funding_locked` 409; `expected_version` 412); `POST /accounts/{id}/stop` & `/resume`; `DELETE /accounts/{id}` (**soft disconnect only**: stop routing, revoke/retire usable credentials, retain sanitized history + audit, mark `disconnected`, restorable only via re-enroll; hard delete/purge out of V1); `POST /accounts/{id}/health`, `POST /providers/{id}/sync`.
Non-goals: discovery/quota endpoints (P3a/P3b).
Boundaries: `internal/httpapi`(control), `internal/accounts/application`. Data impact: funding rows, credential retire, health updates. API impact: account endpoints. Security: reveal gated on re-verify + no-store + audit; secrets never in response. Failure/rollback: soft-disconnect reversible; locked funding rejected.
Tests: reveal requires reverify then once + no-store; funding override + locked + version; soft-disconnect retains history; stop/resume transitions.
Evidence: account-lifecycle contract tests + canary.
Deps: P2b-CAPI-002, P2b-DOM-001/002, P2b-SEC-005, P2b-PROV-003. Parallel-with: P2b-CAPI-003. Blocks: P2b-UI-003, P2b gate.
DoD: lifecycle endpoints correct; reveal gated; soft-disconnect only.

#### P2b-CAPI-005 — Settings endpoint (theme/density subset)
Purpose: server-side theme/density persistence for the UI.
References: [`07 §2.3`](07-design-system.md), [`09 §2`](09-control-api.md).
Preconditions: P2b-CAPI-002.
Scope: `GET /settings`, `PUT /settings` for `theme`/`density` (server-side, not browser storage); applied on boot before first paint. Full settings (staleness windows, probe caps, binds, enrichment) added in P3/P6.
Non-goals: other settings (later).
Boundaries: `internal/httpapi`(control), `internal/storage`. Data impact: settings row. API impact: `/settings` subset. Security: owner-gated. Failure/rollback: invalid value → validation_error.
Tests: get/put theme+density; persisted server-side; applied on boot.
Evidence: settings subset test.
Deps: P2b-CAPI-002. Parallel-with: —. Blocks: P2b-UI-001 (persistence).
DoD: theme/density persist server-side.

#### P2b-OBS-001 — Audit event emission
Purpose: audit every mutation + reveal, secret-free.
References: [`02 §3`](02-domain-model.md), [`09 §1`](09-control-api.md).
Preconditions: P2b-DB-003, P1-SEC-005.
Scope: emit an `audit_event` on every mutating control call and every reveal (ids/codes/timestamps only); append-only; passes through `sanitize`.
Non-goals: route/attempt records (P4).
Boundaries: `internal/observability`, `internal/httpapi`. Data impact: `audit_events`. API impact: none. Security: canary on audit rows. Failure/rollback: audit write failure logged (never blocks the primary op inconsistently — but never silently swallowed).
Tests: mutation emits audit; reveal emits audit; no secret in rows (canary).
Evidence: audit + canary tests.
Deps: P2b-DB-003, P1-SEC-005. Parallel-with: —. Blocks: P2b gate.
DoD: audit on every mutation/reveal; secret-free.

#### P2b-UI-001 — App shell + navigation + theme/density persistence
Purpose: the persistent operator console frame.
References: [`07 §6`](07-design-system.md), [`Design_System patterns/patterns.md`], [`Design_System ui_kits/venom-console`].
Preconditions: P2a gate, P2b-CAPI-005.
Scope: left nav grouped **Overview / Operate / Insights / Manage**; top bar (health summary, `ThemeSwitcher`, `DensityToggle`, owner menu); page-header pattern; theme/density read/written via `GET/PUT /settings` (server-side), applied before first paint via `applyTheme`/`applyDensity`. Composed from `@venom/design-system` primitives + patterns; fork reference: `ui_kits/venom-console` shell (a reference, not production code).
Non-goals: surface content (built per phase).
Boundaries: dashboard workspace. Data impact: none. API impact: consumes `/settings`. Security: n/a. Failure/rollback: n/a.
Tests: nav renders all groups; theme/density persist server-side; keyboard nav + focus (axe).
Evidence: shell render + a11y.
Deps: P2a gate, P2b-CAPI-005. Parallel-with: P2b-UI-002. Blocks: all UI surfaces.
DoD: shell + nav + persistent theming, accessible, from the package.

#### P2b-UI-002 — Owner authentication & session surfaces
Purpose: first-run, login, expiry, re-verify, lockout screens.
References: [`07 §5a owner-auth`](07-design-system.md), [`Design_System states/state-matrix.md`], [`09 §5`](09-control-api.md).
Preconditions: P2a gate, P2b-SEC-001/002/003/005/006.
Scope: first-run setup (create password), login, session-expiry handling (route to login, preserve no secret), re-verify modal (gates sensitive actions 5 min), locked-out (retry-after) — single-owner login (never a sign-up/role picker); password fields masked, never echoed. Uses DS auth state coverage; fork reference: console `Login`/`FirstRun`.
Non-goals: recovery UI beyond messaging (local reset is CLI).
Boundaries: dashboard workspace. Data impact: none. API impact: consumes `/auth/*`. Security: masked fields; no secret in DOM/logs. Failure/rollback: expired session routes to login.
Tests: setup→login flow; expiry→login; reverify modal gates a sensitive action; locked-out shows retry-after; axe.
Evidence: auth-flow Playwright + a11y.
Deps: P2a gate, P2b-SEC-001/002/003/005/006. Parallel-with: P2b-UI-001. Blocks: P2b gate.
DoD: all auth/session states rendered accessibly; single-owner login.

#### P2b-UI-003 — Provider Fleet dashboard
Purpose: the core Phase-2 surface.
References: [`07 §5 ProviderFleet`](07-design-system.md), [`Design_System domain/ProviderFleet`], [`02 §2`](02-domain-model.md#2-free--paid--the-semantics-precisely).
Preconditions: P2b-UI-001, P2b-CAPI-003/004, P2b-PROV-002.
Scope: provider rows → expandable account rows → per-account status/funding/models; stat cards (Providers/Accounts/Healthy/Models); connect dialogs (API-key + OAuth); reveal/copy that clears on hide/blur; funding badge on the **account** row (not provider); "Setup required" banner lists env var **names** only. Uses `ProviderFleet` domain component + `Security` (reveal) + state-matrix renderers.
Non-goals: models detail (P6), quota meters (P3b UI).
Boundaries: dashboard workspace. Data impact: none. API impact: consumes `/providers`, `/accounts*`, OAuth flow. Security: reveal clears on blur; funding on account row. Failure/rollback: n/a.
Tests: fleet renders provider→account rows; connect dialogs (both paths); reveal clears on blur; unknown/degraded states distinct; axe.
Evidence: fleet Playwright + a11y; per-theme render.
Deps: P2b-UI-001, P2b-CAPI-003/004, P2b-PROV-002. Parallel-with: P2b-UI-002. Blocks: P2b gate.
DoD: fleet operates enrollment + reveal accessibly; funding account-scoped.

#### P2b-TEST-001 — Owner-authentication acceptance suite
Purpose: the P2b auth gate, mechanized.
References: [`09 §5.9`](09-control-api.md#5-owner-authentication-first-run-login-session-re-verification), [`08 §5`](08-engineering-standards.md#5-testing-strategy).
Preconditions: P2b-SEC-001..007, P2b-CAPI-001.
Scope: setup-once; generic `invalid_credentials`; idle (30 m) + absolute (12 h) expiry; CSRF rejected before side effect; reveal needs ≤5-min reverify then once with `no-store`; password change revokes all sessions; lockout + audit-without-secret (canary); negative tests (expired/revoked cookie, forged/cross-session CSRF, replay after lockout, reverify reuse past 5 min, setup after completion).
Non-goals: enrollment (TEST-003).
Boundaries: test suite. Data/API impact: none. Security: canary included. Failure/rollback: n/a.
Tests: the full [`09 §5.9`] criteria.
Evidence: auth suite report both OSes.
Deps: P2b-SEC-*, P2b-CAPI-001. Parallel-with: P2b-TEST-002. Blocks: P2b gate.
DoD: all [`09 §5.9`] criteria pass.

#### P2b-TEST-002 — Credential cardinality & reauth interruption integration
Purpose: prove the credential invariants against real SQLite.
References: [`02 §3`](02-domain-model.md#credential-encrypted-secret-for-an-account), [`03 §2e`](03-provider-integration-catalog.md#2e-oauth-reauthentication-same-identity-reconnect), [`10 §2`](10-requirements-coverage.md).
Preconditions: P2b-DB-002, P2b-DOM-003, P2b-PROV-008.
Scope: active-per-kind + staged-per-kind partial indexes; staging swap; interruption recovery (crash leaves ≤1 staged + intact active; startup discards stale staged); multi-kind coexistence (github_oauth + copilot_service); second-staged rejected `reauthentication_in_progress`.
Non-goals: —.
Boundaries: integration tests. Data/API impact: none. Security: canary. Failure/rollback: n/a.
Tests: the above against temp SQLite.
Evidence: integration report both OSes.
Deps: P2b-DB-002, P2b-DOM-003, P2b-PROV-008. Parallel-with: P2b-TEST-001. Blocks: P2b gate.
DoD: cardinality + staging invariants proven on real DB.

#### P2b-TEST-003 — Enrollment E2E (real free API-key + real OAuth)
Purpose: the P2b enrollment gate on real accounts.
References: [`06 P2b`](06-roadmap.md), [`03 §5.1`](03-provider-integration-catalog.md#51-verification-tiers-what-is-a-ci-gate-vs-what-is-manual-evidence).
Preconditions: P2b-CAPI-003/004, P2b-PROV-005/007, P2b-UI-003.
Scope: connect a real free `opencode-zen` account + a real `antigravity` OAuth account; assert correct identity, funding, health in the fleet UI; secrets never logged (canary); duplicates handled friendly. **Live/real-account steps are recorded evidence, non-CI-blocking** (fixture contract tests are the CI gate).
Non-goals: other providers (P7).
Boundaries: E2E harness + manual evidence. Data/API impact: none. Security: canary. Failure/rollback: n/a.
Tests: fixture contract tests (CI) + real-account connect recording (evidence).
Evidence: fixture reports (CI) + dated real-account recordings.
Deps: P2b-CAPI-003/004, P2b-PROV-005/007, P2b-UI-003. Parallel-with: —. Blocks: P2b gate.
DoD: two real accounts connect with correct identity/funding/health; secrets clean.

---

## 11. Phase structure — Model intelligence & quota (P3a, P3b, P3c)

### Phase P3a — Catalog discovery (non-inference)

- **Objective:** account-scoped discovery with generations + atomic snapshot apply; routing-critical
  free-safety; canonical identity + offerings; the shared effective-offering read model; certification
  lifecycle up to `observed`.
- **Scope:** `DB`(M4), `DISC`, `CERT`(domain), `CAPI`, `JOBS`.
- **Out of scope:** inference probes (P3c); quota (P3b); routing (P4).
- **Entry:** P2b gate green. **Dependencies:** P2b (accounts/credentials/providers), P2b-JOBS-001.
- **Parallelization:** `P3a` runs parallel to `P3b` (both depend only on P2b); the `DISC` and
  `CERT`-domain tasks are internally parallel after M4.
- **Expected artifacts:** M4; discovery orchestration; free-safety resolver; effective read model;
  `/models`+`/offerings`; discovery job.
- **DB impact:** M4 (`models`, `provider_model_aliases`, `account_model_offerings`,
  `offering_operations`, `certifications`, `discovery_runs`). **API impact:** `/models`, `/offerings`,
  `/accounts/{id}/discover`, `/offerings/{id}/certification`, `/settings/enrichment`.
- **Security impact:** discovery evidence sanitized; no prompt/credential upstream. **DS impact:** none (Models UI is P6).
- **Testing:** free-account-never-surfaces-paid (fail-closed on dataset miss); enrichment-off doesn't weaken free-safety; generation-guarded atomic snapshot.
- **Migration/recovery:** a superseded (older-generation) run is marked, never applied; malformed response keeps last-known-good.
- **Acceptance gate:** for a connected account, models are discovered and stored with provenance;
  `/models` reflects the raw catalog + certification state; a free account never surfaces a paid model
  (fail-closed on dataset miss). **No inference probes run yet.**
- **Required evidence:** discovery run with provenance; `/models` snapshot; free-safety fail-closed test; generation-guard test.
- **Exit:** gate green. **Rollback/containment:** discovery failure keeps the previous snapshot intact; no partial catalog.

#### P3a-DB-001 — Migration M4: models, offerings, certifications, discovery_runs
Purpose: the catalog schema.
References: [`02 §5`](02-domain-model.md#5-sqlite-schema-sketch), [`04 §3/§5`](04-model-intelligence.md#3-canonical-facts-vs-effective-offering).
Preconditions: P2b-DB-002.
Scope: `models` (canonical, `canonical_key_sha256` unique, nullable native facts, `quality_rating` nullable); `provider_model_aliases`; `account_model_offerings` (offering identity `UNIQUE(account_id, provider_model_id)`, nullable numerics = unknown, capabilities_json normalized); `offering_operations` (per-operation); `certifications` (`status` CHECK `{discovered,observed,probing,certified,suspended,expired}` — **no `rejected`**, capability truth as a **separate** column, `version`, `certified_at`, `evidence_ref`); `discovery_runs` (generation).
Non-goals: quota (M5).
Boundaries: `internal/storage` migrations. Data impact: M4 tables. API impact: none. Security: n/a. Failure/rollback: down path in dev; nullable-means-unknown lint.
Tests: migration up/down; no `rejected` value accepted; capability-truth column separate; offering identity unique.
Evidence: migration CI log.
Deps: P2b-DB-002. Parallel-with: P3b-DB-001 (design/fixture prep and isolated tests only — migration numbering + landing serialize, §8). Blocks: P3a-DISC-*, P3a-CERT-001.
DoD: M4 applies/rolls back; six-state CHECK; truth separate.

#### P3a-DISC-001 — `models` canonical-identity domain
Purpose: pure canonical model + offering identity.
References: [`04 §3`](04-model-intelligence.md#3-canonical-facts-vs-effective-offering), [`02 §3`](02-domain-model.md).
Preconditions: P3a-DB-001.
Scope: `canonical_key = SHA-256(provider_id, provider_model_id)` (provider-scoped; two providers with the same name → two canonical models; no cross-provider equivalence); native facts (nullable); offering identity `(account_id, provider_model_id)`; alias map. Pure (no I/O).
Non-goals: discovery I/O (DISC-002).
Boundaries: `internal/models`. Data impact: none. API impact: none. Security: n/a. Failure/rollback: n/a.
Tests: canonical key determinism; provider-scoped uniqueness; alias mapping.
Evidence: models unit report.
Deps: P3a-DB-001. Parallel-with: P3a-CERT-001. Blocks: P3a-DISC-002/005.
DoD: canonical identity + offering identity proven, pure.

#### P3a-DISC-002 — Discovery orchestration (generations, atomic snapshot)
Purpose: safe per-account discovery.
References: [`04 §1`](04-model-intelligence.md#1-account-scoped-discovery), [`09 §3.7`](09-control-api.md).
Preconditions: P3a-DISC-001, P2b-PROV-005 (DiscoverModels), P2b-PROV-003 (lease).
Scope: per-account; provider from trusted account record; monotonic generation + run row; credential lease in callback scope; normalize/validate every model (bounds/UTF-8/control chars; cap count); sanitize evidence; apply snapshot atomically **only if newest generation** (older run marked superseded); explicit empty list = authoritative withdraw; malformed/truncated = failure → keep last-known-good.
Non-goals: free-safety (DISC-003); probing (P3c).
Boundaries: `internal/intelligence`, `internal/storage`. Data impact: offerings snapshot. API impact: consumed by `/discover`. Security: evidence sanitized; no prompt upstream. Failure/rollback: failed run keeps prior snapshot.
Tests: generation guard (older run superseded); explicit-empty withdraws; malformed keeps last-known-good; atomic apply.
Evidence: discovery orchestration integration test.
Deps: P3a-DISC-001, P2b-PROV-005/003. Parallel-with: —. Blocks: P3a-DISC-005, P3a-CAPI-002.
DoD: generation-guarded atomic discovery proven.

#### P3a-DISC-003 — Free-safety resolution (pipeline A, always-on)
Purpose: a free account (and Lite) can never touch paid capacity.
References: [`04 §1/§2b`](04-model-intelligence.md#2b-free-safety-resolution-vs-metadata-enrichment-two-separate-pipelines).
Preconditions: P3a-DISC-002.
Scope: provider authenticated per-model price first (`cost.input==0 && cost.output==0 && status!=deprecated` ⇒ free-safe); else the `models.dev` **cost dataset** consulted **at discovery** (cache ~10-min TTL + last-known-good + bounded staleness window, owner-configurable default 24 h, with `stale` flag); **fail closed** — unverifiable ⇒ `unknown` ⇒ withdrawn from the free account's offerings; provenance (`source ∈ {provider_price, models_dev, owner_override}`, dataset version, observed_at, confidence, `exact_identity_match = true` required); **independent of the enrichment toggle**; the model list is never hardcoded — only the cost fact is looked up.
Non-goals: enrichment (DISC-004).
Boundaries: `internal/intelligence`. Data impact: cost-fact provenance on offerings. API impact: none. Security: no prompt/credential upstream. Failure/rollback: dataset unavailable + no fresh cache ⇒ fail closed to unknown.
Tests: free account never surfaces paid; dataset miss → withdraw; price-less provider (opencode-zen) resolved via dataset; staleness window behavior; conflicting evidence → fail closed for Lite.
Evidence: free-safety fail-closed test.
Deps: P3a-DISC-002. Parallel-with: P3a-DISC-004. Blocks: P3a gate, P4 funding gate.
DoD: free-safety fail-closed + provenance proven; independent of enrichment.

#### P3a-DISC-004 — Metadata enrichment (pipeline B, optional, off by default)
Purpose: enrich non-routing-critical facts without ever weakening free-safety.
References: [`04 §2/§2b`](04-model-intelligence.md#2b-free-safety-resolution-vs-metadata-enrichment-two-separate-pipelines), [`09 §2`](09-control-api.md).
Preconditions: P3a-DISC-002.
Scope: background enrichment of context/family/release/quality hints from `models.dev` + analysis leaderboard; **off by default**, owner-enabled via `PUT /settings/enrichment`; never in the request hot path; never sends prompts/credentials upstream; weakest source; last-known-good on failure; **separate reads/provenance from pipeline A**; disabling B never disables A.
Non-goals: free-safety (DISC-003).
Boundaries: `internal/intelligence`. Data impact: enrichment facts with provenance. API impact: `/settings/enrichment`. Security: no prompt/credential upstream. Failure/rollback: enrichment failure keeps last-known-good, no routing impact.
Tests: enrichment off by default; disabling enrichment doesn't weaken free-safety (cross-check with DISC-003); enrichment can only *enable* a hard capability after exact identity match.
Evidence: enrichment-separation test.
Deps: P3a-DISC-002. Parallel-with: P3a-DISC-003. Blocks: P3a gate.
DoD: enrichment optional + separate; free-safety independence proven.

#### P3a-DISC-005 — Effective-offering read model (shared projection)
Purpose: one projection the dashboard + router + diagnostics all read.
References: [`04 §3/§6`](04-model-intelligence.md#3-canonical-facts-vs-effective-offering).
Preconditions: P3a-DISC-001/002.
Scope: effective context = `min(native, provider_cap)` when both known, else the known one with provenance, else NULL (unknown ⇒ ineligible); effective capability = native ∧ provider exposure ∧ transport support; quality NULL ⇒ neutral 0.5 ranking; **no consumer re-derives** context/capabilities/quality/eligibility.
Non-goals: routing gates (P4 consumes this).
Boundaries: `internal/models`, `internal/intelligence`. Data impact: read projection. API impact: backs `/models`,`/offerings`. Security: n/a. Failure/rollback: unknown fields stay unknown.
Tests: effective-context resolution matrix; single-projection (no re-derivation) assertion.
Evidence: read-model unit report.
Deps: P3a-DISC-001/002. Parallel-with: P3a-DISC-006. Blocks: P3a-CAPI-001, P4-ROUTE-004, P6 UI.
DoD: one shared effective projection; unknown fail-closed.

#### P3a-DISC-006 — Evidence precedence engine
Purpose: resolve conflicting facts deterministically.
References: [`04 §4`](04-model-intelligence.md#4-evidence-precedence).
Preconditions: P3a-DISC-001.
Scope: precedence `owner override > verified probe > provider metadata > provider discovery > external registry > heuristic > unknown`; owner overrides never auto-overwritten; proven narrower restriction beats broader claim; proven negative wins until revalidated; tie-breaks (verification status → confidence → freshness → scope specificity); heuristics may *schedule* a probe, never certify.
Non-goals: probe production (P3c).
Boundaries: `internal/intelligence`. Data impact: resolved facts with provenance. API impact: none. Security: n/a. Failure/rollback: unresolved → unknown.
Tests: precedence ordering; owner-override immunity; proven-negative wins; narrower-beats-broader.
Evidence: precedence unit report.
Deps: P3a-DISC-001. Parallel-with: P3a-DISC-005. Blocks: P3c-CERT-006.
DoD: precedence + tie-breaks proven.

#### P3a-CERT-001 — Certification lifecycle domain (six-state, capability truth)
Purpose: pure certification state machine (up to `observed` here).
References: [`04 §5`](04-model-intelligence.md#5-certification).
Preconditions: P3a-DB-001.
Scope: states `{discovered, observed, probing, certified, suspended, expired}` (exactly six, **no `rejected`**); capability truth `{unknown, supported, unsupported}` as a **separate** dimension; full legal/invalid transition table; `discovered → observed` on first concrete evidence. Pure. (`observed → probing → certified` wired in P3c.)
Non-goals: probe execution (P3c).
Boundaries: `internal/models`, `internal/intelligence`(domain). Data impact: none. API impact: none. Security: n/a. Failure/rollback: invalid transition rejected + audited, state unchanged.
Tests: legal/invalid transition table (partial — full Cartesian in P3c); no `rejected` anywhere.
Evidence: cert-domain unit report.
Deps: P3a-DB-001. Parallel-with: P3a-DISC-001. Blocks: P3a-CERT-002, P3c-CERT-004.
DoD: six-state machine + truth dimension, pure; no `rejected`.

#### P3a-CERT-002 — `catalog_only` terminal for media-only offerings
Purpose: cataloged-but-never-routed media/image/embedding models.
References: [`04 §5`](04-model-intelligence.md#5-certification), [`05 §9`](05-tier-engine.md#9-future-scope-non-v1).
Preconditions: P3a-CERT-001.
Scope: media-only models (image/embedding/translation) get a terminal `catalog_only` state: visible, never entering tiers, not counted as failure; `image_generation` recognized/certifiable but not V1-routed.
Non-goals: image endpoint (future scope).
Boundaries: `internal/intelligence`. Data impact: `catalog_only` marker. API impact: reflected in `/offerings`. Security: n/a. Failure/rollback: n/a.
Tests: media-only → `catalog_only`, excluded from tiers, not a failure.
Evidence: catalog_only unit test.
Deps: P3a-CERT-001. Parallel-with: —. Blocks: P3a gate.
DoD: media-only correctly cataloged, never routed.

#### P3a-CAPI-001 — `GET /models` + `GET /offerings`
Purpose: expose the effective-offering read model.
References: [`09 §2`](09-control-api.md), [`04 §3`](04-model-intelligence.md#3-canonical-facts-vs-effective-offering).
Preconditions: P3a-DISC-005, P2b-CAPI-002.
Scope: `GET /models`, `GET /offerings` return the shared effective projection incl. certification state + capability truth; owner-gated.
Non-goals: probe results (P3c).
Boundaries: `internal/httpapi`(control). Data impact: none. API impact: `/models`,`/offerings`. Security: owner-gated; unknown never shown as 0. Failure/rollback: n/a.
Tests: reads the shared projection; unknown context not shown as 0.
Evidence: read-model contract test.
Deps: P3a-DISC-005, P2b-CAPI-002. Parallel-with: P3a-CAPI-002. Blocks: P6 Models UI.
DoD: read model exposed; unknown truthful.

#### P3a-CAPI-002 — `POST /accounts/{id}/discover` (async) + certification read
Purpose: trigger discovery + read certification.
References: [`09 §3.7`](09-control-api.md), [`04 §1`](04-model-intelligence.md#1-account-scoped-discovery).
Preconditions: P3a-DISC-002, P2b-JOBS-001, P3a-CERT-001.
Scope: `POST /accounts/{id}/discover` → `202 {job_id, status_url}` (poll canonical `/jobs/{job_id}`); `GET /offerings/{id}/certification` (state + capability truth + review reason).
Non-goals: probe endpoint (P3c).
Boundaries: `internal/httpapi`(control). Data impact: none. API impact: `/discover`,`/certification`. Security: owner-gated. Failure/rollback: failed job reports typed error via jobs surface.
Tests: discover async → job; certification read.
Evidence: discovery/cert contract test.
Deps: P3a-DISC-002, P2b-JOBS-001, P3a-CERT-001. Parallel-with: P3a-CAPI-001. Blocks: P3a gate.
DoD: async discovery + certification read wired.

#### P3a-JOBS-001 — Discovery job kind
Purpose: register discovery on the job surface.
References: [`09 §3.12`](09-control-api.md#312-get-jobsjob_id--canonical-shared-async-job-status).
Preconditions: P2b-JOBS-001, P3a-DISC-002.
Scope: `discovery` job kind; `result_ref` = affected `account_id` + `/models` route (never inline content); retention per jobs contract.
Non-goals: probe/benchmark jobs (P3c/P6).
Boundaries: `internal/httpapi`(jobs), `internal/intelligence`. Data impact: `jobs`. API impact: none new. Security: no content in result_ref. Failure/rollback: crash → job `failed`/`expired`.
Tests: discovery job lifecycle; result_ref is a reference.
Evidence: job test.
Deps: P2b-JOBS-001, P3a-DISC-002. Parallel-with: —. Blocks: P3a gate.
DoD: discovery runs as a tracked job.

#### P3a-CAPI-003 — `PUT /settings/enrichment`
Purpose: owner toggle for optional enrichment.
References: [`09 §2`](09-control-api.md), [`04 §2b`](04-model-intelligence.md#2b-free-safety-resolution-vs-metadata-enrichment-two-separate-pipelines).
Preconditions: P2b-CAPI-005, P3a-DISC-004.
Scope: `PUT /settings/enrichment` (off by default); toggling never affects free-safety.
Boundaries: `internal/httpapi`(control). Data impact: settings. API impact: `/settings/enrichment`. Security: owner-gated. Failure/rollback: n/a.
Tests: toggle persists; free-safety unaffected.
Evidence: settings test.
Deps: P2b-CAPI-005, P3a-DISC-004. Parallel-with: —. Blocks: P3a gate.
DoD: enrichment toggle, free-safety independent.

#### P3a-TEST-001 — Discovery & free-safety acceptance suite
Purpose: the P3a gate, mechanized.
References: [`06 P3a`](06-roadmap.md), [`04 §1/§2b`](04-model-intelligence.md), [`10 §2`](10-requirements-coverage.md).
Preconditions: P3a-DISC-002/003/004, P3a-CAPI-001.
Scope: models discovered with provenance; `/models` reflects catalog + cert state; free account never surfaces paid (fail-closed on dataset miss); enrichment-off doesn't weaken free-safety; generation guard; **no inference probe runs**.
Boundaries: test suite. Data/API impact: none. Security: canary. Failure/rollback: n/a.
Tests: the above.
Evidence: P3a suite report both OSes.
Deps: P3a-DISC-002/003/004, P3a-CAPI-001. Parallel-with: —. Blocks: P3a gate.
DoD: all P3a gate criteria pass; no probes ran.

---

### Phase P3b — Quota & consumption accounting

- **Objective:** the multi-window quota model + mandatory local-safety budgets + atomic all-or-nothing
  reservation + the five-state reservation machine + discriminated janitor + reconciliation worker.
- **Scope:** `DB`(M5), `QUOTA`, `HLTH`(cooldown), `CAPI`, `JOBS`, `UI`.
- **Out of scope:** routing selection (P4); probing (P3c).
- **Entry:** P2b gate green. **Dependencies:** P2b (accounts), P2b-JOBS-001, P0-DB.
- **Parallelization:** runs parallel to P3a (both depend only on P2b); joins at P3c/P4.
- **Expected artifacts:** M5; reservation engine; janitor; reconciliation worker; QuotaMeter data.
- **DB impact:** M5 (`quota_windows`, `quota_reservations`, `quota_reservation_allocations`, `cooldowns`).
- **API impact:** `/accounts/{id}/quota`, `/diagnostics/reconciliation`. **Security impact:** no content read during reconciliation. **DS impact:** consumes `Quota` domain component (QuotaMeter).
- **Testing:** the six deterministic no-leak/no-double-charge tests + concurrency no-overcommit + window-key normalization.
- **Migration/recovery:** janitor discriminated by `dispatched_at`; `reconciliation_pending` never auto-released; crash recovery via leases.
- **Acceptance gate:** concurrent requests can't overcommit **any** window; an attempt that can't reserve
  on one applicable window is rejected before execution with no window left debited; an account with
  **unknown** provider quota still reserves against (and is bounded by) its local-safety windows (never
  unlimited); quota/usage/reset render per account per window; exhaustion on a window suspends only that
  account/operation; window-identity normalization deterministic (same inputs ⇒ same `window_key`, never
  NULL); **plus the six deterministic no-leak/no-double-charge reservation tests**.
- **Required evidence:** the six reservation tests + concurrency test + normalization test reports.
- **Exit:** gate green. **Rollback/containment:** every reservation is all-or-nothing; janitor never frees a possibly-consumed reservation silently.

#### P3b-DB-001 — Migration M5: quota windows, reservations, allocations, cooldowns
Purpose: the multi-window quota schema.
References: [`02 §3 quota`](02-domain-model.md#quota--the-multi-window-model-budgetssources--windows--reservations--allocations).
Preconditions: P2b-DB-002.
Scope: `quota_windows` (`source`, `unit`, `window_type`, `window_key` **NOT NULL**, `used/remaining/total/reserved/limit_value` nullable-means-unknown, `version`, `confidence`, `freshness_state`, `UNIQUE(account_id,source,unit,window_type,window_key)`); `quota_reservations` (`state` CHECK **exactly five**, `dispatched_at`, `expires_at`, `UNIQUE(request_id,attempt_id)`); `quota_reservation_allocations` (PK `(reservation_id,window_id)`, `estimate_source`, `actual_cost`, per-window `state`); `cooldowns` (scope account/offering/provider).
Non-goals: route records (M6).
Boundaries: `internal/storage` migrations. Data impact: M5 tables. API impact: none. Security: n/a. Failure/rollback: down path in dev.
Tests: migration up/down; five-state CHECK; no stored `expired`; window_key NOT NULL; multiple windows per (account,unit).
Evidence: migration CI log.
Deps: P2b-DB-002. Parallel-with: P3a-DB-001 (design/fixture prep and isolated tests only — migration numbering + landing serialize, §8). Blocks: P3b-QUOTA-*.
DoD: M5 applies/rolls back; five-state CHECK; window_key NOT NULL.

#### P3b-QUOTA-001 — Quota window model + `window_key` normalization + per-window states
Purpose: the domain rules for windows.
References: [`02 §3`](02-domain-model.md#quota--the-multi-window-model-budgetssources--windows--reservations--allocations), [`04 §5`](04-model-intelligence.md#5-certification), [`05 §4`](05-tier-engine.md#4-quota--consumption-accounting).
Preconditions: P3b-DB-001.
Scope: window identity `(account_id, source, unit, window_type, window_key)`; canonical `window_key` normalization (`provider:<norm(id)>` / `rolling:<seconds>s` / `local:<unit>`) — deterministic, never NULL/""; per-window states `{available, insufficient, exhausted, unknown, stale}`; attempt takes the **most restrictive** across applicable windows; `stale` (~15 min) treated as `unknown` + refresh trigger; source separation (`provider_evidence`/`local_safety`/`owner_override` never conflated).
Non-goals: reservation txn (QUOTA-004).
Boundaries: `internal/quota`. Data impact: window rows. API impact: none. Security: n/a. Failure/rollback: unknown ≠ unlimited.
Tests: normalization determinism (same inputs ⇒ same key); adapter empty key → synthetic key; most-restrictive selection; stale→unknown.
Evidence: window-model unit report.
Deps: P3b-DB-001. Parallel-with: —. Blocks: P3b-QUOTA-004/008.
DoD: deterministic keys + per-window states proven.

#### P3b-QUOTA-002 — Local-safety budget (mandatory per account)
Purpose: bound every account, even with no provider quota endpoint.
References: [`02 §3 local-safety`](02-domain-model.md#local-routing-safety-budget-mandatory-for-every-account), [`05 §4`](05-tier-engine.md#4-quota--consumption-accounting).
Preconditions: P3b-QUOTA-001.
Scope: every connected account owns a `local_safety` source with at least a **concurrency** window (default cap 1 in-flight until provider quota confirmed) and an **estimated-consumption** window; owner-policy defaults; `limit_value` authoritative ceiling; never presented as provider evidence.
Non-goals: provider quota sync (QUOTA-008).
Boundaries: `internal/quota`. Data impact: local_safety windows per account. API impact: none. Security: n/a. Failure/rollback: absent provider quota ⇒ local-safety still bounds.
Tests: local-safety windows created on connect; concurrency default 1; source never conflated.
Evidence: local-safety unit/integration test.
Deps: P3b-QUOTA-001. Parallel-with: P3b-QUOTA-003. Blocks: P3b-QUOTA-004, P4-ROUTE-011.
DoD: mandatory local-safety windows on every account.

#### P3b-QUOTA-003 — Estimated consumption dimensions + provenance
Purpose: canonical pre-execution estimates.
References: [`02 §3 estimated consumption`](02-domain-model.md#estimated-consumption--canonical-internal-dimensions), [`05 §4`](05-tier-engine.md#4-quota--consumption-accounting).
Preconditions: P3b-QUOTA-001.
Scope: dimensions — `requests`=1; `input_tokens` from normalized request (message + tool-schema tokens); `output_tokens` from `max_tokens` or conservative owner-policy default; `concurrency`=1; `credits`/`balance` **only** with a verified conversion; provenance `from_request`/`provider_conversion`/`policy_default`; a balance window with no safe estimate relies on local-safety and reconciles post-execution.
Non-goals: token content storage (never).
Boundaries: `internal/quota`, `internal/routing`(request normalize hook). Data impact: allocation estimates. API impact: none. Security: no prompt content stored. Failure/rollback: unsafe credit estimate → defer to reconciliation.
Tests: estimate per dimension; provenance tags; no credit conversion without a verified rule.
Evidence: estimate unit report.
Deps: P3b-QUOTA-001. Parallel-with: P3b-QUOTA-002. Blocks: P3b-QUOTA-004.
DoD: canonical estimates + provenance; no fabricated credit conversion.

#### P3b-QUOTA-004 — Atomic reservation (all-or-nothing across windows)
Purpose: prevent concurrent overcommit; never partially reserve.
References: [`02 §3 atomic reservation`](02-domain-model.md#atomic-reservation-contract-across-all-applicable-windows), [`05 §2 Step 8 / §4`](05-tier-engine.md#2-per-request-selection-algorithm).
Preconditions: P3b-QUOTA-001/002/003.
Scope: per-window conditional `UPDATE … SET reserved=reserved+cost, version=version+1 WHERE id=? AND version=? AND COALESCE(remaining,limit_value)-reserved>=cost` for **every applicable window** (provider + local-safety) inside **one `BEGIN IMMEDIATE`**; all rows affected → insert reservation + allocations → COMMIT; any window 0 rows → ROLLBACK whole txn (nothing debited) → reject before execution → re-evaluate pool from a fresh snapshot; idempotent `reserve` keyed by `(request_id,attempt_id)`; no provider HTTP while the write txn is open.
Non-goals: settle/release (QUOTA-005).
Boundaries: `internal/quota`, `internal/storage`. Data impact: reservation + allocation rows. API impact: none. Security: n/a. Failure/rollback: any-window-short rolls back the whole txn.
Tests: two concurrent requests can't overcommit any window; any-window-short → whole rollback, nothing debited; idempotent reserve; unknown-provider-quota still needs local-safety headroom.
Evidence: concurrency + all-or-nothing tests.
Deps: P3b-QUOTA-001/002/003. Parallel-with: —. Blocks: P3b-QUOTA-005, P4-ROUTE-013.
DoD: atomic all-or-nothing reservation proven under contention.

#### P3b-QUOTA-005 — Reservation five-state machine + idempotent settle/release
Purpose: the canonical reservation lifecycle.
References: [`02 §3 reservation state machine`](02-domain-model.md#quota--the-multi-window-model-budgetssources--windows--reservations--allocations), [`05 §4`](05-tier-engine.md#4-quota--consumption-accounting).
Preconditions: P3b-QUOTA-004.
Scope: states `reserved | reconciliation_pending | settled | released | unknown_consumption` (no stored `expired`); transitions per the table (settle on success; release only if never-left-Venom or proven no-consumption; `reserved→reconciliation_pending` on dispatched ambiguity; pending→settled/released/unknown_consumption); invalid transitions rejected + audited; idempotent `settle`/`release`/transition; **all allocations move together**; `reconciliation_pending` keeps headroom debited.
Non-goals: janitor (QUOTA-006).
Boundaries: `internal/quota`. Data impact: reservation/allocation states. API impact: none. Security: n/a. Failure/rollback: no path auto-releases a `reconciliation_pending` on deadline.
Tests: each legal transition; invalid rejected + audited; idempotency; allocations consistent.
Evidence: state-machine unit report.
Deps: P3b-QUOTA-004. Parallel-with: —. Blocks: P3b-QUOTA-006/007, P4-ROUTE-013.
DoD: five-state machine + idempotent transitions proven; no stored `expired`.

#### P3b-QUOTA-006 — Janitor / startup reconciliation (discriminated branches)
Purpose: recover reservations after crash/deadline without leaking or double-charging.
References: [`02 §3 janitor`](02-domain-model.md#quota--the-multi-window-model-budgetssources--windows--reservations--allocations), [`05 §2/§4`](05-tier-engine.md#4-quota--consumption-accounting).
Preconditions: P3b-QUOTA-005.
Scope: **never keyed on `state='reserved'` alone** — discriminate by `dispatched_at`: never-dispatched `reserved` past deadline → `released` (+audit); dispatched `reserved` past deadline (crash before reconcile) → `reconciliation_pending`; `reconciliation_pending` past retry deadline/lease → reclaim + re-enqueue (allocations never freed); terminal retry boundary → `unknown_consumption` (+`usage_gap`). Worker leases (auto-expiring).
Non-goals: reconciliation lookups (QUOTA-007).
Boundaries: `internal/quota`, workers. Data impact: reservation transitions. API impact: none. Security: n/a. Failure/rollback: allocations never freed silently.
Tests: three branches; crash between dispatch and reconcile → pending; lease reclaim.
Evidence: janitor branch tests.
Deps: P3b-QUOTA-005. Parallel-with: P3b-QUOTA-007. Blocks: P3b gate.
DoD: discriminated janitor branches proven.

#### P3b-QUOTA-007 — Unknown-consumption reconciliation worker
Purpose: resolve ambiguous consumption safely.
References: [`05 §4 reconciliation`](05-tier-engine.md#unknown-consumption-reconciliation), [`09 §3`](09-control-api.md).
Preconditions: P3b-QUOTA-005, P2b-JOBS-001.
Scope: bounded, idempotent, small-batch worker over `reconciliation_pending`; per item: (1) read provider usage endpoint if any; (2) match by `request_id`/`attempt_id`; (3) else estimate with `confidence=low`; retry backoff (30 s→5 m→30 m), capped (default 5) or reset window; auto-expiring lease; keyed by `(reservation_id/attempt_id)`; **no prompt/response content**; final `settle(actual)` / `settle(estimate,low)` / `release` (proven no-consumption) / `unknown_consumption` (+`usage_gap` + re-baseline at next sync); no-provider-API path → settle low-confidence at first retry; every outcome updates all allocations consistently.
Non-goals: quota sync (QUOTA-008).
Boundaries: `internal/quota`, workers. Data impact: reservation resolution. API impact: feeds `/diagnostics/reconciliation`. Security: no content read. Failure/rollback: worker crash → lease expiry → reclaim.
Tests: reconciliation success; proven no-consumption release; terminal unknown_consumption + usage_gap; no-provider-API fallback; worker crash recovery.
Evidence: reconciliation worker tests.
Deps: P3b-QUOTA-005, P2b-JOBS-001. Parallel-with: P3b-QUOTA-006. Blocks: P3b gate.
DoD: reconciliation resolves safely; no leak, no double-charge.

#### P3b-QUOTA-008 — Quota sync + cooldown handling
Purpose: ingest provider quota; handle 429s as cooldowns.
References: [`04 §5 quota states`](04-model-intelligence.md#5-certification), [`05 §4`](05-tier-engine.md#4-quota--consumption-accounting), [`03 §1`](03-provider-integration-catalog.md#1-adapter-interfaces-the-pattern).
Preconditions: P3b-QUOTA-001, P2b-PROV-001 (QuotaAdapter).
Scope: `QuotaAdapter.FetchQuota` → provider-evidence windows (may be several concurrent); 429 → **cooldown** at correct scope (account/offering/provider) + schedule refresh (never `exhausted` beyond cooldown); staleness ~15 min → `unknown` + background refresh; persist cooldowns (survive restart).
Non-goals: circuit breakers (P4-ROUTE-014).
Boundaries: `internal/quota`, `internal/accounts` (cooldown), `internal/providers`. Data impact: windows + cooldowns. API impact: consumed by `/quota`. Security: evidence sanitized. Failure/rollback: sync failure keeps last-known-good windows.
Tests: multiple windows from one fetch; 429→cooldown not exhausted; staleness→unknown; cooldown persists.
Evidence: quota-sync tests.
Deps: P3b-QUOTA-001, P2b-PROV-001. Parallel-with: —. Blocks: P3b gate, P4.
DoD: quota sync + scope-correct cooldowns proven.

#### P3b-CAPI-001 — `POST /accounts/{id}/quota` (refresh)
Purpose: owner-triggered quota refresh.
References: [`09 §2`](09-control-api.md).
Preconditions: P3b-QUOTA-008, P2b-CAPI-002.
Scope: `POST /accounts/{id}/quota` refresh (naturally idempotent, converges on provider truth).
Boundaries: `internal/httpapi`(control). Data impact: windows. API impact: `/quota`. Security: owner-gated. Failure/rollback: n/a.
Tests: refresh converges; owner-gated.
Evidence: quota endpoint test.
Deps: P3b-QUOTA-008, P2b-CAPI-002. Parallel-with: P3b-CAPI-002. Blocks: P3b gate.
DoD: quota refresh endpoint works.

#### P3b-CAPI-002 — `GET /diagnostics/reconciliation` (+ manual re-sync)
Purpose: expose pending/unknown-consumption reservations.
References: [`09 §3`](09-control-api.md), [`05 §4`](05-tier-engine.md#unknown-consumption-reconciliation).
Preconditions: P3b-QUOTA-007, P2b-CAPI-002.
Scope: `GET /diagnostics/reconciliation` lists `reconciliation_pending`/`unknown_consumption` reservations; manual re-sync / accept-estimate action.
Boundaries: `internal/httpapi`(control). Data impact: reservation transitions on re-sync. API impact: `/diagnostics/reconciliation`. Security: owner-gated; ids/costs/confidence only. Failure/rollback: n/a.
Tests: lists pending; manual re-sync transitions correctly.
Evidence: reconciliation diagnostics test.
Deps: P3b-QUOTA-007, P2b-CAPI-002. Parallel-with: P3b-CAPI-001. Blocks: P3b gate, P6 Diagnostics UI.
DoD: reconciliation visible + manually recoverable.

#### P3b-JOBS-001 — Reconciliation + quota-sync job kinds
Purpose: run reconciliation and quota sync as tracked jobs/workers.
References: [`09 §3.12`](09-control-api.md#312-get-jobsjob_id--canonical-shared-async-job-status).
Preconditions: P2b-JOBS-001, P3b-QUOTA-007/008.
Scope: register reconciliation + quota-sync workers with leases, retry/backoff, crash recovery; `result_ref` references windows, no content.
Boundaries: `internal/httpapi`(jobs), `internal/quota` workers. Data impact: `jobs`. API impact: none new. Security: no content. Failure/rollback: crash → lease reclaim.
Tests: worker lease + retry + crash recovery.
Evidence: worker job tests.
Deps: P2b-JOBS-001, P3b-QUOTA-007/008. Parallel-with: —. Blocks: P3b gate.
DoD: reconciliation/quota-sync run as recoverable jobs.

#### P3b-UI-001 — QuotaMeter surfacing
Purpose: honest per-account per-window usage display.
References: [`07 §5 QuotaMeter / §5a`](07-design-system.md), [`Design_System domain/Quota`].
Preconditions: P2a gate, P3b-CAPI-001.
Scope: per-account per-window usage %, remaining, reset in provider-native units; `unknown`/`stale`/low-confidence rendered **distinctly** (hatched/dashed, never a fabricated number). Uses `Quota` domain component + state-matrix quota rules.
Boundaries: dashboard workspace. Data impact: none. API impact: consumes `/accounts`,`/quota`. Security: n/a. Failure/rollback: n/a.
Tests: known values render; unknown/stale distinct (not a number); axe.
Evidence: QuotaMeter render + a11y.
Deps: P2a gate, P3b-CAPI-001. Parallel-with: —. Blocks: P6 Quota surface.
DoD: honest quota rendering; unknown never faked.

#### P3b-TEST-001 — Reservation acceptance suite (six no-leak tests)
Purpose: the P3b gate, mechanized.
References: [`06 P3b`](06-roadmap.md), [`02 §3`](02-domain-model.md#quota--the-multi-window-model-budgetssources--windows--reservations--allocations), [`08 §5`](08-engineering-standards.md#5-testing-strategy).
Preconditions: P3b-QUOTA-004..008.
Scope: the **six** deterministic tests — (1) pre-dispatch deadline expiry → released + audit; (2) post-dispatch ambiguity → `reconciliation_pending`, headroom stays debited, never auto-released on deadline; (3) reconciliation success → settle (actual or low-confidence); (4) proven no-consumption → release; (5) terminal retry boundary → `unknown_consumption` + `usage_gap` + re-baseline; (6) worker crash → lease expiry → reclaim with idempotent consistent allocation updates — **plus** concurrency no-overcommit on any window and deterministic `window_key` normalization.
Boundaries: integration tests (temp SQLite). Data/API impact: none. Security: canary. Failure/rollback: n/a.
Tests: the eight above.
Evidence: P3b suite report both OSes.
Deps: P3b-QUOTA-004..008. Parallel-with: —. Blocks: P3b gate.
DoD: all six + concurrency + normalization pass.

---

### Phase P3c — Probing (inference-based, quota-protected)

- **Objective:** context + capability probing that never flips a capability false on infra failure;
  the certification state machine reaching `certified`; the routing-admission conjunction; the
  deterministic Cartesian certification test.
- **Scope:** `CERT`, `QUOTA`(probe safety), `CAPI`, `JOBS`, `UI`.
- **Out of scope:** routing selection (P4).
- **Entry:** P3a gate green **and** P3b gate green.
- **Dependencies:** P3a (offerings + cert domain + precedence), P3b (reservation for probe protection), P0-EXEC (transport to send probes).
- **Parallelization:** context vs capability probes internally parallel; the Cartesian test is a single join.
- **Expected artifacts:** probe engine; certification transitions to `certified`; review drainer; `/offerings/{id}/probe`; Cartesian test.
- **DB impact:** uses M4 certification tables (+ probe records if needed as a small M4 addition). **API impact:** `/offerings/{id}/probe`.
- **Security impact:** probe evidence redacted; 401/403 never a capability judgment. **DS impact:** consumes `ModelIntelligence` (CertificationState).
- **Testing:** probe-failure-never-flips-capability; the Cartesian 18-combination test; no `rejected` state anywhere.
- **Migration/recovery:** probes reserve quota (P3b) and honor cooldowns; per-provider max 1 in-flight probe.
- **Acceptance gate:** context/capabilities probed + stored with provenance; offerings reach `certified`;
  `/models` reflects verified facts with **zero hardcoded model data**; the **deterministic Cartesian
  certification test** passes (6 states × 3 truths = 18; only `(certified, supported)` routable;
  invalid transitions rejected + audited; no `rejected` state in code/schema/API).
- **Required evidence:** probe runs with provenance; Cartesian test report; no-static-list lint.
- **Exit:** gate green. **Rollback/containment:** a probe failure for infra reasons leaves the fact unknown and reschedules; never a false negative.

#### P3c-CERT-001 — Probe outcome taxonomy
Purpose: separate capability truth from probe execution.
References: [`04 §2/§5`](04-model-intelligence.md#2-capability--context-detection-dynamic-three-sources).
Preconditions: P3a-CERT-001.
Scope: capability truth `{unknown, supported, unsupported}` vs probe execution `{pending, running, succeeded, inconclusive, retryable_failure, terminal_failure}`; rules — genuine response → supported; semantic rejection → unsupported; 429/timeout/5xx/network → unknown + retryable_failure (reschedule); 401/403 → terminal_failure but truth stays unknown; malformed → inconclusive. **Hard rule: quota/rate-limit failure never flips a capability to false.**
Boundaries: `internal/intelligence`. Data impact: probe records. API impact: none. Security: n/a. Failure/rollback: infra failure leaves truth unknown.
Tests: each outcome → correct truth/execution; infra failure never `unsupported`.
Evidence: taxonomy unit report.
Deps: P3a-CERT-001. Parallel-with: —. Blocks: P3c-CERT-004.
DoD: taxonomy + hard rule proven.

#### P3c-CERT-002 — Context-window probe
Purpose: read the real context limit empirically.
References: [`04 §2`](04-model-intelligence.md#2-capability--context-detection-dynamic-three-sources), [`06 P3c`](06-roadmap.md).
Preconditions: P3c-CERT-001, P0-EXEC-002, P3c-QUOTA-001.
Scope: one oversized request (~3,000,000 tokens, `max_tokens: 8`); read limit from rejection (structured field → OpenAI "maximum context length is N" gated on `context_length_exceeded` → provider regex → generic number-near-keyword → `no_signal`, never a guess); 7-day cooldown; per-provider circuit breaker; concurrency cap (max 1 in-flight probe/provider); account pre-check; evidence redacted to a short snippet (credential shapes stripped).
Boundaries: `internal/intelligence`, `internal/execution`. Data impact: context fact + provenance. API impact: none. Security: evidence redacted; canary. Failure/rollback: no_signal never a guessed number.
Tests: limit extraction ladder; cooldown; concurrency cap; redaction.
Evidence: context-probe tests.
Deps: P3c-CERT-001, P0-EXEC-002, P3c-QUOTA-001. Parallel-with: P3c-CERT-003. Blocks: P3c-CERT-004.
DoD: context probe reads real limit or `no_signal`; safe + redacted.

#### P3c-CERT-003 — Capability probes (tools / structured_output / vision)
Purpose: verify each operation on the specific offering-operation.
References: [`04 §2/§5`](04-model-intelligence.md#2-capability--context-detection-dynamic-three-sources).
Preconditions: P3c-CERT-001, P0-EXEC-002, P3c-QUOTA-001.
Scope: tiny fixed fixtures exercising tools/structured_output/vision; genuine capability response → supported; reliable semantic rejection → unsupported; per-operation.
Boundaries: `internal/intelligence`, `internal/execution`. Data impact: capability truth per operation. API impact: none. Security: fixtures only; redacted. Failure/rollback: infra failure → unknown (taxonomy).
Tests: supported/unsupported per operation; chat success does not certify tools.
Evidence: capability-probe tests.
Deps: P3c-CERT-001, P0-EXEC-002, P3c-QUOTA-001. Parallel-with: P3c-CERT-002. Blocks: P3c-CERT-004.
DoD: per-operation capability probing proven.

#### P3c-CERT-004 — Certification transitions wired (`observed→probing→certified`)
Purpose: drive the lifecycle to `certified` from probe verdicts.
References: [`04 §5`](04-model-intelligence.md#5-certification).
Preconditions: P3c-CERT-001/002/003, P3a-CERT-001.
Scope: `observed→probing` (probe started); `probing→probing` (retryable/inconclusive within budget: default 3, exp backoff); `probing→certified` (definitive verdict — supported **or** unsupported); `probing→suspended` (terminal_failure or budget exhausted, reason-coded); `certified⇄suspended`; `suspended→certified`/`suspended→probing`; `certified→expired→probing`; each emits an audit event.
Boundaries: `internal/intelligence`, `internal/models`. Data impact: certification transitions. API impact: none. Security: n/a. Failure/rollback: invalid transitions rejected + audited.
Tests: each legal transition; retry budget; suspension reasons; expiry→probing.
Evidence: transition tests.
Deps: P3c-CERT-001/002/003, P3a-CERT-001. Parallel-with: P3c-CERT-006. Blocks: P3c-CERT-005/007.
DoD: lifecycle reaches `certified` from verdicts; transitions legal.

#### P3c-CERT-005 — Routing admission conjunction + review drainer
Purpose: `routable = certified ∧ supported`; work the backlog.
References: [`04 §5/§6`](04-model-intelligence.md#5-certification).
Preconditions: P3c-CERT-004.
Scope: **routing admission requires both** certification state `certified` **and** capability truth `supported` — the only routable combination; `certified+unknown`/`certified+unsupported`/`suspended`/`expired`/`discovered`/`observed`/`probing` all not routable; bounded review drainer (idempotent, small batches, never re-touches certified rows); review count grouped by reason; routing-admission reasons (`identity_unresolved`, `context_unverified`, `capability_not_certified`, `funding_unknown`, `no_healthy_account`, `quota_exhausted`, `quota_insufficient`, `cooling_down`).
Boundaries: `internal/intelligence`, `internal/routing`(admission read). Data impact: review state. API impact: reflected in `/certification`. Security: n/a. Failure/rollback: unknown ⇒ not routable.
Tests: conjunction (only certified∧supported routable); drainer idempotency; typed reasons.
Evidence: admission + drainer tests.
Deps: P3c-CERT-004. Parallel-with: —. Blocks: P3c-CERT-007, P4-ROUTE-004/005.
DoD: conjunction admission + review drainer proven.

#### P3c-CERT-006 — Evidence precedence fully wired to probes
Purpose: probe evidence takes its rightful precedence.
References: [`04 §4`](04-model-intelligence.md#4-evidence-precedence).
Preconditions: P3c-CERT-002/003, P3a-DISC-006.
Scope: verified probe beats provider metadata/discovery; proven narrower restriction beats a broader external claim; proven negative wins until revalidated; owner override still supreme.
Boundaries: `internal/intelligence`. Data impact: resolved facts. API impact: none. Security: n/a. Failure/rollback: n/a.
Tests: probe-positive beats metadata; probed cap beats external "1M"; proven-negative persists.
Evidence: precedence-with-probes tests.
Deps: P3c-CERT-002/003, P3a-DISC-006. Parallel-with: P3c-CERT-004. Blocks: P3c gate.
DoD: probe evidence precedence proven.

#### P3c-QUOTA-001 — Probe safety (cost caps + reservation)
Purpose: probes never blow quota/budget.
References: [`04 §2`](04-model-intelligence.md#2-capability--context-detection-dynamic-three-sources), [`09 §3.8`](09-control-api.md).
Preconditions: P3b-QUOTA-004.
Scope: per-provider concurrency (max 1 in-flight probe), hard cost cap per probe and per account (owner-configurable), opt-in toggle for expensive probes; every probe obtains a reservation (P3b) before execution.
Boundaries: `internal/intelligence`, `internal/quota`. Data impact: reservations. API impact: none. Security: n/a. Failure/rollback: cap exceeded → `probe_capped`, no execution.
Tests: cost cap enforced; probe reserves quota; expensive-probe toggle.
Evidence: probe-safety tests.
Deps: P3b-QUOTA-004. Parallel-with: —. Blocks: P3c-CERT-002/003, P3c-CAPI-001.
DoD: probes bounded by caps + reservation.

#### P3c-CAPI-001 — `POST /offerings/{id}/probe` (async, quota-protected)
Purpose: owner-triggered probing.
References: [`09 §3.8`](09-control-api.md).
Preconditions: P3c-CERT-004, P3c-QUOTA-001, P2b-JOBS-001.
Scope: `POST /offerings/{id}/probe` `{operations?, force?}` → `202` + job; enforces per-provider concurrency + caps + 7-day context cooldown; result reports **capability truth and probe-execution state separately**; infra failure never flips a capability.
Boundaries: `internal/httpapi`(control). Data impact: none. API impact: `/probe`. Security: owner-gated; redacted. Failure/rollback: `probe_capped` typed error.
Tests: async probe → job; result reports truth + execution; caps enforced.
Evidence: probe endpoint tests.
Deps: P3c-CERT-004, P3c-QUOTA-001, P2b-JOBS-001. Parallel-with: —. Blocks: P3c gate.
DoD: async quota-protected probe endpoint.

#### P3c-JOBS-001 — Probe + recertification job kinds
Purpose: probing/recert as tracked jobs.
References: [`09 §3.12`](09-control-api.md#312-get-jobsjob_id--canonical-shared-async-job-status).
Preconditions: P2b-JOBS-001, P3c-CERT-004.
Scope: `probe` job kind + a recertification/expiry-refresh worker (drift/TTL → `expired→probing`); leases + retry; `result_ref` references `/offerings/{id}/certification`.
Boundaries: `internal/httpapi`(jobs), `internal/intelligence`. Data impact: `jobs`. API impact: none new. Security: no content. Failure/rollback: crash → reclaim.
Tests: probe job lifecycle; recert refresh.
Evidence: job tests.
Deps: P2b-JOBS-001, P3c-CERT-004. Parallel-with: —. Blocks: P3c gate.
DoD: probing/recert run as tracked jobs.

#### P3c-CERT-007 — Deterministic Cartesian certification test
Purpose: the P3c invariant, mechanized (CI-blocking).
References: [`04 §5`](04-model-intelligence.md#5-certification), [`06 P3c`](06-roadmap.md), [`08 §5`](08-engineering-standards.md#5-testing-strategy).
Preconditions: P3c-CERT-005.
Scope: table-driven test of all **6 certification states × 3 capability truths (18 combinations)** asserting **exactly** `(certified, supported)` is routable and all 17 others are not; plus one legality case per invalid transition (rejected, state unchanged, audit emitted); assert **no `rejected` state** exists in code/schema/API; assert **no static model list** exists (no-hardcoding lint).
Boundaries: test suite + lints. Data/API impact: none. Security: n/a. Failure/rollback: n/a.
Tests: the 18-combination matrix + invalid-transition legality + no-rejected + no-static-list.
Evidence: Cartesian test report both OSes.
Deps: P3c-CERT-005. Parallel-with: —. Blocks: P3c gate, P4.
DoD: 18-combination + legality + no-rejected + no-hardcoding all pass.

#### P3c-UI-001 — CertificationState chip
Purpose: render lifecycle + capability truth + review reason honestly.
References: [`07 §5/§5a`](07-design-system.md), [`Design_System domain/ModelIntelligence`], [`Design_System states/state-matrix.md`].
Preconditions: P2a gate, P3a-CAPI-002.
Scope: lifecycle chip (`discovered→…→certified`,`suspended`,`expired`) + capability-truth sub-state + review-reason tooltip; **`certified+unknown` must read as "not routable yet"** (show the conjunction). Uses `ModelIntelligence` domain component + state-matrix cert rules.
Boundaries: dashboard workspace. Data impact: none. API impact: consumes `/certification`. Security: n/a. Failure/rollback: n/a.
Tests: all states render; certified+unknown reads not-routable; axe.
Evidence: cert-chip render + a11y.
Deps: P2a gate, P3a-CAPI-002. Parallel-with: —. Blocks: P6 Models surface.
DoD: conjunction shown; states truthful.

#### P3c-TEST-001 — Probe-integrity acceptance suite
Purpose: prove the hard rules across the pipeline.
References: [`04 §2/§2b/§5`](04-model-intelligence.md), [`10 §2`](10-requirements-coverage.md).
Preconditions: P3c-CERT-001..006, P3a-DISC-003/004.
Scope: infra failure (429/timeout/5xx/401/403) never flips a capability to `unsupported`; free-safety remains independent of enrichment (re-assert with probing active); offerings reach `certified` only via a verdict; zero hardcoded model data anywhere.
Boundaries: test suite. Data/API impact: none. Security: canary. Failure/rollback: n/a.
Tests: the above.
Evidence: P3c suite report both OSes.
Deps: P3c-CERT-001..006, P3a-DISC-003/004. Parallel-with: P3c-CERT-007. Blocks: P3c gate.
DoD: probe-integrity + free-safety-independence + no-hardcoding proven.

---

## 12. Phase structure — Tier engine, routing & public API (P4, P5)

### Phase P4 — Tier engine & routing

- **Objective:** the `lite/pro/max` selection algorithm end-to-end (normalize → gates → groups →
  score → band → distribute → select → reserve → execute → reconcile/fallback), the transport
  implementations behind the frozen dispatcher, `NormalizeError`, and scoped circuit breakers.
- **Scope:** `ROUTE`, `EXEC`, `HLTH`, `OBS`, `DB`(M6).
- **Out of scope:** the public HTTP surface (P5); provider breadth (P7).
- **Entry:** P3c gate green **and** P3b gate green.
- **Dependencies:** P3c (`certified ∧ supported`), P3b (reservation), P0-EXEC (seam), P3a (effective read model), P2b-DOM (eligibility).
- **Parallelization:** `EXEC` transports ∥ `ROUTE` scoring/distribution; the two tier controllers
  (Pro deficit, Max DRR+P2C) are independent modules.
- **Expected artifacts:** M6; the routing engine; transports (bifrost + native for the 2 proven providers); failure taxonomy; circuit breakers; route records.
- **DB impact:** M6 (`route_decisions`, `route_attempts`; routing policy/state; circuit-breaker state; deficit cells — stickiness is in-memory only).
- **API impact:** none public (invoked internally; exposed in P5). **Security impact:** `NormalizeError` never leaks secrets; route records secret-free. **DS impact:** none (Routing UI is P6).
- **Testing:** Lite zero-paid + fail-closed; Pro ±5 pp over N=2,000 across buckets; Max quota-fairness + quality-band; fallback funding/capability boundaries; stickiness never violates reservation; Bifrost never re-selects.
- **Migration/recovery:** cooldowns/circuit-breaker state persist; lazy recovery on read.
- **Acceptance gate (deterministic):** **Lite** zero paid selections (categorical) + fail-closed under
  free exhaustion; **Pro** mix converges within **±5 pp** of 25% paid over **N = 2,000** synthetic
  successful requests across the standard workload buckets (`standard`, `vision`, `tool_use`,
  `structured`, `large_context`) with **no** winner more than **0.08** (Pro) / **0.03** (Max) quality
  below the top eligible candidate and no auto-widening; **Max** proves **quota-fairness + quality-band**
  (DRR frequency converges to the capacity-weight ratio; saturated-on-any-window skipped) — **not** any
  50/50 funding ratio; fallback respects funding/capability boundaries; stickiness never violates a
  reservation/eligibility; Bifrost never re-selects.
- **Required evidence:** the Lite/Pro/Max scenario reports (with the numeric thresholds); fallback + stickiness + no-reselect tests.
- **Exit:** gate green. **Rollback/containment:** every attempt reserves before executing; a failed attempt releases/reconciles; fallback is bounded and never crosses funding/capability.

#### P4-DB-001 — Migration M6: route records + routing state
Purpose: persist decisions/attempts + breaker/deficit state.
References: [`02 §3`](02-domain-model.md), [`05 §7`](05-tier-engine.md#7-observability).
Preconditions: P3a-DB-001.
Scope: `route_decisions` (candidate set + exclusion reason codes + chosen route + scores, secret-free); `route_attempts` (provider/account/model ids, latency, normalized status, thinking clamp); circuit-breaker state; per-`(tier, workload_profile_bucket, funding_class)` deficit cells. (Session stickiness is in-memory LRU, **not** persisted.)
Boundaries: `internal/storage` migrations. Data impact: M6 tables. API impact: none. Security: no prompts/responses/raw errors/identifiers-that-are-secrets. Failure/rollback: down path in dev.
Tests: migration up/down; route records secret-free (canary).
Evidence: migration CI log.
Deps: P3a-DB-001. Parallel-with: —. Blocks: P4-OBS-001, P4-ROUTE-010/011/014.
DoD: M6 applies/rolls back; records secret-free.

#### P4-ROUTE-001 — Request normalization & hard-requirement derivation (Step 1)
Purpose: turn a request into typed requirements.
References: [`05 §2 Step 1`](05-tier-engine.md#2-per-request-selection-algorithm).
Preconditions: P3a-DISC-005.
Scope: detect modality (text/vision/documents), required capabilities (`structured_output`?, tools?, thinking?), context need `S`; **union** inferred requirements with explicit `venom.required_capabilities`; set requested thinking level from `venom.thinking_budget`. `image_generation` is not a V1 request operation.
Boundaries: `internal/routing`. Data impact: none. API impact: none (P5 feeds it). Security: n/a. Failure/rollback: n/a.
Tests: modality/capability/context extraction; union with extension.
Evidence: normalization unit report.
Deps: P3a-DISC-005. Parallel-with: P4-ROUTE-002. Blocks: P4-ROUTE-004/009.
DoD: requirements derived + unioned deterministically.

#### P4-ROUTE-002 — Tier policy definitions (typed, bounded)
Purpose: the `lite/pro/max` policies as data-shaped typed policy.
References: [`05 §1/§8.4`](05-tier-engine.md#1-the-three-tiers-authoritative-policy).
Preconditions: P0-FND-001.
Scope: per tier — funding rule (Lite free-only; Pro/Max free|paid, never unknown), context ceiling (256K/512K/1M), thinking ceiling (none/extended/ultra), attempt budget (3/4/5), fixed V1 scoring weights, competitive band (Pro 0.08 / Max 0.03), fallback rules; bounded-validated; **no slug switch**.
Boundaries: `internal/routing`. Data impact: none. API impact: none. Security: n/a. Failure/rollback: out-of-bound tuning rejected.
Tests: policy values match [`05 §1`]; bounds validated.
Evidence: policy unit report.
Deps: P0-FND-001. Parallel-with: P4-ROUTE-001. Blocks: P4-ROUTE-005/007/008/013.
DoD: typed tier policies with correct values + bounds.

#### P4-ROUTE-003 — Thinking-budget normalization
Purpose: provider-neutral effort levels with clamps + graceful degradation.
References: [`05 §1a`](05-tier-engine.md#1a-thinking--reasoning-budget-normalization).
Preconditions: P4-ROUTE-002.
Scope: levels `none/standard/extended/ultra`; tier defaults+ceilings; downward override; **tier-ceiling clamp** (reported in diagnostics + `X-Venom-*`), applied before the per-offering **certified-max clamp**; map to provider effort flag / token budget / reasoning variant (per adapter, discovered — never model-name inferred); graceful degradation vs. explicit-required-capability rejection; preserved across fallback (re-clamped per candidate).
Boundaries: `internal/routing`, `internal/execution`. Data impact: none. API impact: none. Security: n/a. Failure/rollback: unsupported thinking degrades, never eliminates (unless required-capability).
Tests: tier defaults; downward override; clamp-to-tier + clamp-to-certified-max; degrade vs explicit-require; no raw model ID drives mapping.
Evidence: thinking-normalization unit report.
Deps: P4-ROUTE-002. Parallel-with: P4-ROUTE-004. Blocks: P4-ROUTE-013.
DoD: levels/clamps/degradation proven; mapping model-agnostic.

#### P4-ROUTE-004 — Candidate pool build (Step 2)
Purpose: only certified∧supported, healthy, valid-cred, non-cooling offerings.
References: [`05 §2 Step 2`](05-tier-engine.md#2-per-request-selection-algorithm), [`04 §5`](04-model-intelligence.md#5-certification).
Preconditions: P4-ROUTE-001, P3c-CERT-005, P3a-DISC-005, P2b-DOM-001.
Scope: build over the immutable routing snapshot; include only offering-operations with certification `certified` **and** capability truth `supported`, healthy account, valid credential, not cooling; tag with parent-account funding (inherited).
Boundaries: `internal/routing`. Data impact: none. API impact: none. Security: n/a. Failure/rollback: unknown ⇒ excluded.
Tests: only certified∧supported enter; funding inherited; snapshot immutable.
Evidence: candidate-pool unit report.
Deps: P4-ROUTE-001, P3c-CERT-005, P3a-DISC-005, P2b-DOM-001. Parallel-with: P4-ROUTE-003. Blocks: P4-ROUTE-005.
DoD: candidate pool correct + immutable-snapshot based.

#### P4-ROUTE-005 — Hard eligibility gates (Step 3, fail closed)
Purpose: funding/context/capability/health/quota/cooldown gates.
References: [`05 §2 Step 3`](05-tier-engine.md#2-per-request-selection-algorithm).
Preconditions: P4-ROUTE-004, P4-ROUTE-002, P3b-QUOTA-001, P2b-DOM-002.
Scope: funding gate (Lite free-only — a paid offering flatly ineligible; Pro/Max free|paid, never unknown incl. `evidence_required` unknown); context gate (verified ≥ S; reject if S > ceiling → `venom_context_exceeds_tier`); capability gate (every required capability certified); health/quota/cooldown gates; anything unknown ⇒ excluded with a typed reason.
Boundaries: `internal/routing`. Data impact: none. API impact: none. Security: n/a. Failure/rollback: fail closed with typed reason.
Tests: Lite excludes paid categorically; unknown funding excluded everywhere; S>ceiling rejected; capability gate.
Evidence: hard-gate unit report.
Deps: P4-ROUTE-004, P4-ROUTE-002, P3b-QUOTA-001, P2b-DOM-002. Parallel-with: —. Blocks: P4-ROUTE-007.
DoD: fail-closed hard gates with typed reasons.

#### P4-ROUTE-006 — Route groups (Step 4, anti-inflation)
Purpose: N accounts of one offering score once.
References: [`05 §2 Step 4`](05-tier-engine.md#2-per-request-selection-algorithm).
Preconditions: P4-ROUTE-005.
Scope: group eligible offerings by `provider : model : funding`; one group scored once (best known quota headroom); account selection happens inside the winning group; many accounts add capacity, not ranking.
Boundaries: `internal/routing`. Data impact: none. API impact: none. Security: n/a. Failure/rollback: n/a.
Tests: N-accounts → one group, one score; no ranking advantage from account count.
Evidence: grouping unit report.
Deps: P4-ROUTE-005. Parallel-with: —. Blocks: P4-ROUTE-007.
DoD: anti-inflation grouping proven.

#### P4-ROUTE-007 — Scoring (Step 5)
Purpose: weighted utility per route group.
References: [`05 §2 Step 5`](05-tier-engine.md#2-per-request-selection-algorithm), [`04 §3`](04-model-intelligence.md#3-canonical-facts-vs-effective-offering).
Preconditions: P4-ROUTE-006, P4-ROUTE-002.
Scope: factors normalized 0–1 (missing = neutral 0.5, reduced confidence); per-tier weights (Pro/Max as in [`05 §2`]); **Lite is pure hard-eligibility** — no quality/speed scoring, latency the only tie-break; no fixed ratio at scoring time; free never gets an unconditional win.
Boundaries: `internal/routing`. Data impact: none. API impact: none. Security: n/a. Failure/rollback: missing factor = neutral.
Tests: weights applied; Lite unscored (latency tie-break); missing factor neutral.
Evidence: scoring unit report.
Deps: P4-ROUTE-006, P4-ROUTE-002. Parallel-with: —. Blocks: P4-ROUTE-008/010/011.
DoD: scoring correct per tier; Lite hard-eligibility only.

#### P4-ROUTE-008 — Competitive band (Step 6, fixed, never auto-widen)
Purpose: keep distribution from promoting a weak route.
References: [`05 §2 Step 6/§8.5`](05-tier-engine.md#2-per-request-selection-algorithm).
Preconditions: P4-ROUTE-007.
Scope: on the normalized quality score (0.0–1.0; missing = 0.5), after all hard gates: `top − candidate ≤ 0.08` (Pro) / `≤ 0.03` (Max); routes outside dropped; fewer than two in-band ⇒ continue with available candidates — **never auto-widen**; Lite has no band.
Boundaries: `internal/routing`. Data impact: none. API impact: none. Security: n/a. Failure/rollback: no widening.
Tests: band exactly 0.08/0.03; no auto-widen; sub-two proceeds.
Evidence: band unit report.
Deps: P4-ROUTE-007. Parallel-with: P4-ROUTE-009. Blocks: P4-ROUTE-010/011.
DoD: fixed band, never widened.

#### P4-ROUTE-009 — Workload-profile bucket (deterministic key)
Purpose: the deterministic key for per-bucket deficit accounting.
References: [`05 §2 Step 7`](05-tier-engine.md#2-per-request-selection-algorithm).
Preconditions: P4-ROUTE-001.
Scope: properties `vision`/`tool_use`/`structured`/`large_context`(default 32K, tunable)/`standard`; bucket key = matched-property set **normalized (lowercase) → sorted → deduped → canonically joined**; same set → same bucket; a scoring/monitoring signal, not a hard gate.
Boundaries: `internal/routing`. Data impact: none. API impact: none. Security: n/a. Failure/rollback: n/a.
Tests: `vision+structured` == `structured+vision`; determinism.
Evidence: bucket unit report.
Deps: P4-ROUTE-001. Parallel-with: P4-ROUTE-008. Blocks: P4-ROUTE-010.
DoD: deterministic bucket key proven.

#### P4-ROUTE-010 — Pro funding-mix deficit controller (workload-isolated)
Purpose: converge to ~25% paid without promoting weak routes.
References: [`05 §2 Step 7/§8.1`](05-tier-engine.md#2-per-request-selection-algorithm).
Preconditions: P4-ROUTE-008/009, P4-DB-001.
Scope: deficit per `(pro, workload_profile_bucket, funding_class)` (not global); read recent *successful* distribution; compute deficit vs `paid_share_target` (~25%); prefer the funding pool with the larger positive deficit — **only among already-competitive routes**; deterministic (not random).
Boundaries: `internal/routing`. Data impact: deficit cells (M6). API impact: none. Security: n/a. Failure/rollback: never promotes outside band.
Tests: per-bucket deficit; converges to 25% among competitive routes; never promotes weak route.
Evidence: Pro-controller unit report.
Deps: P4-ROUTE-008/009, P4-DB-001. Parallel-with: P4-ROUTE-011. Blocks: P4-ROUTE-013, P4-TEST-001.
DoD: workload-isolated deficit controller proven.

#### P4-ROUTE-011 — Max quality-first → quota-fair DRR + P2C (no funding-mix target)
Purpose: Max distribution.
References: [`05 §2 Step 7/§8.3`](05-tier-engine.md#2-per-request-selection-algorithm).
Preconditions: P4-ROUTE-008, P3b-QUOTA-002.
Scope: (1) quality band first; (2) quota-fair DRR across **all** eligible accounts (free or paid), weighted by available capacity across all applicable quota windows **and** local-safety; accounts **saturated on any required window** ineligible (fail-open only if all saturated); credit `quantum = weight/Σweight`, pick max-deficit, debit, carry fractional credit → converges to capacity ratio; (3) P2C final pick (fewer in-flight/latency/health/headroom) with an idempotent auto-expiring in-flight lease; **no funding-mix target** — funding observable, not an objective.
Boundaries: `internal/routing`. Data impact: DRR state. API impact: none. Security: n/a. Failure/rollback: fail-open only if all saturated.
Tests: DRR frequency converges to capacity ratio; saturated-on-any-window skipped; P2C pick; no funding target.
Evidence: Max-controller unit report.
Deps: P4-ROUTE-008, P3b-QUOTA-002. Parallel-with: P4-ROUTE-010. Blocks: P4-ROUTE-013, P4-TEST-001.
DoD: quality-first → quota-fair DRR+P2C proven; no funding target.

#### P4-ROUTE-012 — Account selection + session stickiness (preference only)
Purpose: pick the account inside the winning group; preserve prompt cache.
References: [`05 §2 Step 7`](05-tier-engine.md#2-per-request-selection-algorithm).
Preconditions: P4-ROUTE-010/011.
Scope: capacity-fairness selection within the group (headroom across windows + local-safety, rate-limit headroom, inverse recent load, reliability, latency; respect in-flight leases + concurrency caps); session stickiness key `sha256(first user msg)[:16]`, **preference only after hard gates + reservation** (a sticky account failing any gate or reservation is never used); pin recorded only on success; dropped on ≤15% headroom on any window / unhealthy / cooling / left pool / 15-min TTL; in-memory LRU (~500), fail-open.
Boundaries: `internal/routing`. Data impact: none persisted (in-memory LRU). API impact: none. Security: stickiness key is a hash, not content stored. Failure/rollback: stickiness fail-open.
Tests: capacity-fair selection; stickiness never violates reservation/eligibility; drop conditions.
Evidence: selection/stickiness unit report.
Deps: P4-ROUTE-010/011. Parallel-with: —. Blocks: P4-ROUTE-013, P4-TEST-002.
DoD: capacity-fair selection; stickiness safe.

#### P4-ROUTE-013 — Fallback loop (Step 8: reserve→execute→reconcile)
Purpose: the per-attempt independent cycle.
References: [`05 §2 Step 8/§3`](05-tier-engine.md#2-per-request-selection-algorithm), [`02 §3`](02-domain-model.md#quota--the-multi-window-model-budgetssources--windows--reservations--allocations).
Preconditions: P4-ROUTE-005/012, P3b-QUOTA-004/005, P4-EXEC-001, P4-EXEC-002.
Scope: for up to N attempts (Lite 3/Pro 4/Max 5): select candidate → create attempt record → **reserve atomically across all applicable windows** (P3b-QUOTA-004; 0-rows → rollback + re-evaluate pool) → execute (no write txn open) → reconcile (success→settle; pre-consumption failure→release + classify/cooldown + loop; partial→settle known + loop; network cut/unknown→`reserved→reconciliation_pending`, headroom stays); each attempt has its own `reservation_id = f(request_id, attempt_id)`; no reservation inherited; exhaustion → `venom_no_eligible_offering` (503) with earliest `retry_after`.
Boundaries: `internal/routing`, `internal/quota`, `internal/execution`. Data impact: attempts + reservations. API impact: none (P5 wraps). Security: n/a. Failure/rollback: per-attempt release/reconcile; bounded fallback.
Tests: reserve-before-execute; rollback re-evaluates; per-attempt independent reservation; unknown→pending; exhaustion error.
Evidence: fallback-loop integration test.
Deps: P4-ROUTE-005/012, P3b-QUOTA-004/005, P4-EXEC-001/002. Parallel-with: —. Blocks: P4 gate, P5-PAPI-002.
DoD: reserve→execute→reconcile/fallback proven.

#### P4-ROUTE-014 — Scope-classified fallback + scoped circuit breakers
Purpose: correct scope handling + adaptive backoff.
References: [`05 §3`](05-tier-engine.md#3-fallback--cooldown), [`01 §4.2`](01-architecture.md#42-failure-taxonomy).
Preconditions: P4-ROUTE-013, P4-EXEC-002.
Scope: use `TypedFailure.Scope` — `request`→stop; `account`→next account, cooldown account only; `offering`→another offering same account, cooldown offering; `provider`→skip provider, short cooldown only on cross-account evidence; `transient_transport`→bounded retry (≤3, exp backoff) before fallback; scoped circuit breakers (account/offering/provider) with half-open probes, **adaptive backoff** (double, cap ~16×), **lazy recovery** (refresh on read); cooldown as eligibility input (skip, don't sleep); persist cooldowns; fallback never crosses funding/capability; streaming fallback only before first byte.
Boundaries: `internal/routing`, `internal/execution`, `internal/accounts`. Data impact: breaker/cooldown state (M6). API impact: none. Security: n/a. Failure/rollback: bounded retries; scoped cooldown.
Tests: scope→action mapping; adaptive backoff; lazy recovery; never widen scope on unscoped 429; fallback boundary.
Evidence: fallback/breaker tests.
Deps: P4-ROUTE-013, P4-EXEC-002. Parallel-with: —. Blocks: P4 gate.
DoD: scope-correct fallback + adaptive breakers proven.

#### P4-ROUTE-015 — Routing error envelope
Purpose: stable typed routing errors.
References: [`05 §5`](05-tier-engine.md#5-errors-stable-envelope).
Preconditions: P4-ROUTE-013, P2b-CAPI-002 (envelope).
Scope: `venom_free_capacity_exhausted` (429), `venom_no_eligible_offering` (503), `venom_context_exceeds_tier` (400), `venom_capability_unsupported` (400/501), `venom_invalid_extension` (400); Lite fails closed with one of the first two, never escalating to paid/unknown.
Boundaries: `internal/routing`, `internal/httpapi`. Data impact: none. API impact: error codes (exposed in P5). Security: no secrets/raw errors. Failure/rollback: Lite fail-closed.
Tests: each code; Lite fail-closed under exhaustion.
Evidence: error-envelope tests.
Deps: P4-ROUTE-013, P2b-CAPI-002. Parallel-with: —. Blocks: P5-PAPI-006.
DoD: stable routing errors; Lite fail-closed.

#### P4-EXEC-001 — Transport implementations behind the frozen dispatcher
Purpose: execute a resolved route via the correct transport.
References: [`01 §4.3/§4.5`](01-architecture.md#43-transport-types).
Preconditions: P0-EXEC-002, P2b-PROV-001.
Scope: implement transports selected **by typed capability** — `bifrost` (pool 1, one key, retries off), `native_api` (Google/Gemini schema normalizer), `native_oauth` (OAuth account semantics), `openai_compatible`, `custom`; enough transports to run the two proven providers (opencode-zen via bifrost/openai_compatible; antigravity via native); OAuth token refresh handled by the **credential provider**, not the transport; the transport can neither re-select nor widen a `ResolvedRoute`. **Additional native_oauth transports are added per-provider in P7 behind this frozen dispatcher.**
Boundaries: `internal/execution`. Data impact: none. API impact: none. Security: no credential/raw-error leak. Failure/rollback: unresolvable → typed error.
Tests: transport selection by capability (no slug switch); Bifrost can't re-select; native path for antigravity.
Evidence: transport unit/integration tests.
Deps: P0-EXEC-002, P2b-PROV-001. Parallel-with: P4-ROUTE-*. Blocks: P4-ROUTE-013, P7 transports.
DoD: transports behind the frozen dispatcher; no re-selection.

#### P4-EXEC-002 — Failure classification (`NormalizeError` → `TypedFailure`)
Purpose: the failure taxonomy driving fallback/cooldown.
References: [`01 §4.2`](01-architecture.md#42-failure-taxonomy).
Preconditions: P4-EXEC-001.
Scope: map every provider error to `TypedFailure` by scope priority (provider semantic code/body → standard HTTP headers → adapter rules → HTTP status fallback); populate `FailureClass`/`Scope`/`Retryable`/`CooldownUntil`/`RetryAfter`/`QuotaResetAt`/`ProviderCode`/`HTTPStatus`/`SafeMessage`/`Evidence`; **never leak credentials or raw provider text**; unscoped 429 → conservative `transient_transport` (never widen scope).
Boundaries: `internal/execution`. Data impact: none. API impact: none. Security: secret-safe (canary). Failure/rollback: n/a.
Tests: scope-priority table; secret-safe `SafeMessage`; unscoped-429 conservative.
Evidence: NormalizeError tests + canary.
Deps: P4-EXEC-001. Parallel-with: —. Blocks: P4-ROUTE-013/014, P5.
DoD: full taxonomy; secret-safe.

#### P4-EXEC-003 — Streaming, cancellation, timeouts, first-byte boundary
Purpose: streaming semantics + cancellation.
References: [`01 §4.1`](01-architecture.md#41-inferencetransport-interface), [`05 §3`](05-tier-engine.md#3-fallback--cooldown).
Preconditions: P4-EXEC-001.
Scope: `Stream` channel of chunks; `Cancel` aborts in-flight; timeouts; first-byte boundary (fallback only before first byte reaches client; never a second response after streaming begins); partial-consumption handling feeds reconciliation; no write txn during the HTTP call.
Boundaries: `internal/execution`. Data impact: none. API impact: none. Security: n/a. Failure/rollback: partial stream → settle known/reconcile.
Tests: stream round-trip; cancel; timeout; first-byte fallback boundary.
Evidence: streaming/cancel tests.
Deps: P4-EXEC-001. Parallel-with: P4-EXEC-002. Blocks: P4-ROUTE-013, P5-PAPI-002.
DoD: streaming/cancel/timeout + first-byte boundary proven.

#### P4-OBS-001 — Route-decision & attempt records
Purpose: the "why this route?" data, secret-free.
References: [`05 §7`](05-tier-engine.md#7-observability), [`01 §6c`](01-architecture.md#6c-response-telemetry-headers-x-venom-).
Preconditions: P4-DB-001, P4-ROUTE-013.
Scope: persist `route_decision` (candidate set + exclusion reason codes + chosen route + scores) and `route_attempt` (provider/account/model ids, latency, normalized status, thinking clamp); never prompts/responses/token content/raw errors/`Authorization`.
Boundaries: `internal/observability`. Data impact: M6 records. API impact: backs `/diagnostics/routes` (P6). Security: secret-free (canary). Failure/rollback: n/a.
Tests: records written; secret-free (canary).
Evidence: route-record tests + canary.
Deps: P4-DB-001, P4-ROUTE-013. Parallel-with: —. Blocks: P6 Diagnostics, P5-OBS-001.
DoD: decision/attempt records, secret-free.

#### P4-TEST-001 — Lite/Pro/Max distribution acceptance gate
Purpose: the P4 quantitative gate, mechanized.
References: [`06 P4`](06-roadmap.md), [`05 §8`](05-tier-engine.md#8-resolved-product-decisions), [`08 §5`](08-engineering-standards.md#5-testing-strategy).
Preconditions: P4-ROUTE-010/011, P4-ROUTE-005/008.
Scope: **Lite** zero paid selections + fail-closed under free exhaustion; **Pro** mix within **±5 pp** of 25% over **N = 2,000** across the five workload buckets, band respected (no winner >0.08 below top, no widening); **Max** DRR frequency converges to the capacity-weight ratio + saturated-on-any-window skipped + quality-band respected (≤0.03) — **not** any 50/50 ratio.
Boundaries: test harness (synthetic fleet). Data/API impact: none. Security: n/a. Failure/rollback: n/a.
Tests: the three tier scenarios with the numeric thresholds.
Evidence: Lite/Pro/Max scenario reports both OSes.
Deps: P4-ROUTE-010/011/005/008. Parallel-with: P4-TEST-002. Blocks: P4 gate.
DoD: all three tier gates pass at the stated thresholds.

#### P4-TEST-002 — Fallback / stickiness / no-reselect suite
Purpose: prove the safety invariants of selection + fallback.
References: [`05 §2/§3`](05-tier-engine.md#2-per-request-selection-algorithm), [`01 §4.5`](01-architecture.md#45-enforcement).
Preconditions: P4-ROUTE-012/013/014, P4-EXEC-001.
Scope: fallback never crosses funding/capability boundaries; stickiness never violates a reservation/eligibility; Bifrost (and every transport) never re-selects; streaming fallback only before first byte; unknown-quota still reserves against local-safety.
Boundaries: test suite. Data/API impact: none. Security: n/a. Failure/rollback: n/a.
Tests: the above.
Evidence: fallback/stickiness/no-reselect report both OSes.
Deps: P4-ROUTE-012/013/014, P4-EXEC-001. Parallel-with: P4-TEST-001. Blocks: P4 gate.
DoD: all selection/fallback invariants pass.

---

### Phase P5 — Public API surface

- **Objective:** the OpenAI-compatible public inference API + `venom` extension + Venom API keys +
  telemetry headers + ingress rate limiting — a thin shell over the P4 engine.
- **Scope:** `DB`(M7), `PAPI`, `CAPI`(keys), `OBS`.
- **Out of scope:** dashboard surfaces (P6); provider breadth (P7); image generation (future scope — no V1 endpoint).
- **Entry:** P4 gate green.
- **Dependencies:** P4 (routing engine + transport + failure), P2b (control API for key management), P4-EXEC-003 (streaming).
- **Parallelization:** key management (CAPI) ∥ the public handlers; extension parsing ∥ telemetry headers.
- **Expected artifacts:** M7; `/v1/chat/completions`, `/v1/models`; `venom` extension; `X-Venom-*` headers; RPM; keys endpoints.
- **DB impact:** M7 (`venom_api_keys` hash-only, `usage_records`). **API impact:** `/v1/*`, `/keys*`.
- **Security impact:** vk hash-only; per-key RPM; response never leaks provider internals. **DS impact:** none (API Keys UI is P6).
- **Testing:** real SDK chat+stream+tools+vision; extension clamp/gates/validation; usage recorded on every terminal path.
- **Migration/recovery:** usage recorded on every terminal path (never swallowed).
- **Acceptance gate:** a real OpenAI SDK/IDE uses `venom/lite|pro|max` for chat + streaming + tools +
  vision; the `venom` extension clamps thinking above the tier ceiling (reported in diagnostics),
  enforces `required_capabilities` as hard gates, survives streaming, and rejects invalid fields with
  `venom_invalid_extension`; usage and route decisions are recorded.
- **Required evidence:** SDK E2E recording; extension validation suite; usage-recorded-on-every-terminal-path test.
- **Exit:** gate green. **Rollback/containment:** the public surface adds no new routing behavior; a bug is contained to the handler layer over the frozen engine.

#### P5-DB-001 — Migration M7: Venom API keys + usage records
Purpose: keys (hash-only) + usage.
References: [`02 §3`](02-domain-model.md), [`09 §3.11`](09-control-api.md).
Preconditions: P2b-DB-003.
Scope: `venom_api_keys` (id, label, `rpm_limit`, **hash/verifier only** — never raw), `usage_records`.
Boundaries: `internal/storage` migrations. Data impact: M7 tables. API impact: none. Security: hash-only keys. Failure/rollback: down path in dev.
Tests: migration up/down; no raw-key column.
Evidence: migration CI log.
Deps: P2b-DB-003. Parallel-with: —. Blocks: P5-PAPI-001, P5-CAPI-001.
DoD: M7 applies/rolls back; keys hash-only.

#### P5-PAPI-001 — Venom API-key authentication + per-key RPM
Purpose: authenticate `/v1/*` and rate-limit per key.
References: [`01 §6b`](01-architecture.md#6b-data-plane-public-inference-api), [`05 §6`](05-tier-engine.md#6-ingress-rate-limiting).
Preconditions: P5-DB-001.
Scope: `vk_live_*` in `Authorization`; verify against stored hash/verifier; per-key RPM (sliding window); data-plane independent bind (default `127.0.0.1:8081`; owner may choose any host:port without exposing the control plane); never serves control endpoints.
Boundaries: `internal/httpapi`(public). Data impact: none. API impact: gates `/v1/*`. Security: hash-only; `invalid_api_key` (401); `rate_limited` (429). Failure/rollback: invalid key rejected.
Tests: valid/invalid key; per-key RPM; no control-path overlap.
Evidence: vk-auth tests.
Deps: P5-DB-001. Parallel-with: P5-CAPI-001. Blocks: P5-PAPI-002/003.
DoD: vk auth + RPM; no control overlap.

#### P5-CAPI-001 — Venom API-key management endpoints
Purpose: create/list/delete keys (raw shown once).
References: [`09 §3.11`](09-control-api.md).
Preconditions: P5-DB-001, P2b-CAPI-002.
Scope: `POST /keys` `{label, rpm_limit?}` → `201 {id,label,rpm_limit,raw_key}` (raw `vk_live_*` returned **once**; hash-only stored); `GET /keys` (never raw); `DELETE /keys/{id}`.
Boundaries: `internal/httpapi`(control). Data impact: `venom_api_keys`. API impact: `/keys*`. Security: raw once; owner-gated. Failure/rollback: n/a.
Tests: create returns raw once; list never raw; delete.
Evidence: keys contract tests.
Deps: P5-DB-001, P2b-CAPI-002. Parallel-with: P5-PAPI-001. Blocks: P6 API Keys UI.
DoD: key lifecycle; raw shown once.

#### P5-PAPI-002 — `POST /v1/chat/completions` (OpenAI-compatible)
Purpose: the primary inference endpoint.
References: [`01 §6b`](01-architecture.md#6b-data-plane-public-inference-api), [`05 §1b/§5`](05-tier-engine.md#1b-public-request-contract--the-venom-extension), [`06 P5`](06-roadmap.md).
Preconditions: P5-PAPI-001, P4-ROUTE-013, P4-EXEC-003.
Scope: OpenAI-compatible body; three tier model names (`venom/lite|pro|max`); request validation; invoke the P4 engine; streaming (SSE) + non-streaming; tools + vision input; usage recorded on **every** terminal path (never swallowed); stable error envelope.
Non-goals: image generation (no V1 endpoint).
Boundaries: `internal/httpapi`(public), `internal/routing`. Data impact: `usage_records`. API impact: `/v1/chat/completions`. Security: no provider internals leaked. Failure/rollback: routing errors mapped to the public envelope.
Tests: chat/stream/tools/vision; usage recorded on success + failure paths.
Evidence: endpoint tests.
Deps: P5-PAPI-001, P4-ROUTE-013, P4-EXEC-003. Parallel-with: P5-PAPI-003. Blocks: P5-PAPI-004, P5 gate.
DoD: OpenAI-compatible chat + streaming + tools + vision; usage always recorded.

#### P5-PAPI-003 — `GET /v1/models`
Purpose: tier model listing.
References: [`01 §6b`](01-architecture.md#6b-data-plane-public-inference-api).
Preconditions: P5-PAPI-001.
Scope: return the three tier model names (`venom/lite|pro|max`) in OpenAI-compatible shape.
Boundaries: `internal/httpapi`(public). Data impact: none. API impact: `/v1/models`. Security: vk-auth. Failure/rollback: n/a.
Tests: lists exactly the three tiers.
Evidence: endpoint test.
Deps: P5-PAPI-001. Parallel-with: P5-PAPI-002. Blocks: P5 gate.
DoD: `/v1/models` returns the three tiers.

#### P5-PAPI-004 — `venom` request extension (parse + validate)
Purpose: the one namespaced request extension.
References: [`05 §1b`](05-tier-engine.md#1b-public-request-contract--the-venom-extension).
Preconditions: P5-PAPI-002, P4-ROUTE-003.
Scope: optional `venom` object — `thinking_budget` (`none|standard|extended|ultra`, downward, clamped to tier ceiling, clamp reported in diagnostics + `X-Venom-*`) + `required_capabilities` (canonical ids → Step-3 hard gates); unknown field / invalid value / unknown capability → `venom_invalid_extension` (400) naming the field (never silently coerced); preserved through non-streaming **and** streaming and across fallback; never exposes provider names/account IDs/raw model IDs/native thinking params.
Boundaries: `internal/httpapi`(public), `internal/routing`. Data impact: none. API impact: `venom` extension on `/v1/chat/completions`. Security: no provider internals leaked. Failure/rollback: invalid → typed error, no coercion.
Tests: valid extension; clamp-above-ceiling reported; required-caps gate; invalid field typed error; streaming-preserved; no internals leaked.
Evidence: extension validation suite.
Deps: P5-PAPI-002, P4-ROUTE-003. Parallel-with: P5-OBS-001. Blocks: P5 gate.
DoD: extension parsed/validated/clamped/gated; no leaks.

#### P5-OBS-001 — `X-Venom-*` response telemetry headers
Purpose: sanitized routing outcome for plain SDKs.
References: [`01 §6c`](01-architecture.md#6c-response-telemetry-headers-x-venom-), [`05 §7`](05-tier-engine.md#7-observability).
Preconditions: P5-PAPI-002, P4-OBS-001.
Scope: one sanitized builder stamps `X-Venom-Request-Id/-Tier/-Provider/-Model/-Funding/-Latency-Ms/-Tokens-In/-Tokens-Out/-Fallback-Attempts/-Version`; streaming: headers at stream start (zeroed metrics) + final values in a trailing SSE metadata comment; values pass the `sanitize` boundary; **never** an account identifier/credential/raw provider error.
Boundaries: `internal/httpapi`(public), `internal/observability`. Data impact: none. API impact: response headers. Security: sanitized (canary). Failure/rollback: n/a.
Tests: header set present; streaming trailer; no account id/secret (canary).
Evidence: header tests + canary.
Deps: P5-PAPI-002, P4-OBS-001. Parallel-with: P5-PAPI-004. Blocks: P5 gate.
DoD: one sanitized header builder; streaming trailer; secret-free.

#### P5-PAPI-005 — Ingress rate limiting
Purpose: protect Venom's own endpoints.
References: [`05 §6`](05-tier-engine.md#6-ingress-rate-limiting).
Preconditions: P5-PAPI-001, P2b-CAPI-001.
Scope: per-path per-IP sliding window for control/public paths; **per-API-key** RPM for `/v1/*` (stored with the key); process-local counter (single-owner single-process); same contract if it later scales out.
Boundaries: `internal/httpapi`. Data impact: none. API impact: 429 on limit. Security: distinct from provider-429 cooldowns. Failure/rollback: n/a.
Tests: per-IP + per-key limits; distinct from provider cooldowns.
Evidence: rate-limit tests.
Deps: P5-PAPI-001, P2b-CAPI-001. Parallel-with: P5-PAPI-004. Blocks: P5 gate.
DoD: ingress limits enforced.

#### P5-PAPI-006 — Public error envelope, privacy, cancellation, idempotency boundary
Purpose: the stable public failure contract.
References: [`05 §5`](05-tier-engine.md#5-errors-stable-envelope), [`04 §5`], [`P4-ROUTE-015`].
Preconditions: P4-ROUTE-015, P2b-CAPI-002.
Scope: `{error:{code,message,request_id,retryable}}`; routing codes (`venom_free_capacity_exhausted`, `venom_no_eligible_offering`, `venom_context_exceeds_tier`, `venom_capability_unsupported`, `venom_invalid_extension`) + `invalid_api_key`/`rate_limited`; **no** prompts/responses stored by default (privacy policy); client cancellation aborts the in-flight stream (via `Cancel`); document idempotency boundaries (inference is not replay-idempotent — usage is recorded per terminal path).
Boundaries: `internal/httpapi`(public). Data impact: none. API impact: error contract. Security: no secrets/raw errors; response identity policy. Failure/rollback: Lite fail-closed.
Tests: each error code; cancellation aborts; no prompt/response persisted by default.
Evidence: error/privacy tests.
Deps: P4-ROUTE-015, P2b-CAPI-002. Parallel-with: —. Blocks: P5 gate.
DoD: stable public envelope + privacy + cancellation.

#### P5-TEST-001 — Real-SDK end-to-end
Purpose: the P5 gate on a real client.
References: [`06 P5`](06-roadmap.md).
Preconditions: P5-PAPI-002/003/004, P5-OBS-001.
Scope: point a real OpenAI SDK/IDE at Venom; use `venom/lite|pro|max` for chat + streaming + tools + vision; the extension clamps thinking above the ceiling (reported in diagnostics/headers), enforces required-caps, survives streaming, rejects invalid with the typed error; usage + route decisions recorded. (Uses fake provider backends for CI determinism; a real-provider run is recorded evidence.)
Boundaries: E2E harness. Data/API impact: none. Security: canary. Failure/rollback: n/a.
Tests: the full [`06 P5`] gate.
Evidence: SDK E2E report both OSes + recording.
Deps: P5-PAPI-002/003/004, P5-OBS-001. Parallel-with: P5-TEST-002/003. Blocks: P5 gate.
DoD: real SDK exercises all four capabilities + the extension.

#### P5-TEST-002 — `venom` extension validation suite
Purpose: exhaustive extension validation.
References: [`05 §1b/§5`](05-tier-engine.md#1b-public-request-contract--the-venom-extension).
Preconditions: P5-PAPI-004.
Scope: valid/invalid `thinking_budget`; unknown field; unknown capability id; clamp reporting; required-caps as hard gates; streaming + fallback preservation; no provider internals leaked.
Boundaries: test suite. Data/API impact: none. Security: canary. Failure/rollback: n/a.
Tests: the above.
Evidence: extension suite report.
Deps: P5-PAPI-004. Parallel-with: P5-TEST-001/003. Blocks: P5 gate.
DoD: extension validation exhaustive.

#### P5-TEST-003 — Usage-recorded-on-every-terminal-path
Purpose: prove usage/billing is never swallowed.
References: [`05 §4/§7`](05-tier-engine.md#4-quota--consumption-accounting), [`08 §3`](08-engineering-standards.md#3-coding-standards).
Preconditions: P5-PAPI-002, P4-OBS-001, P3b-QUOTA-005.
Scope: usage + a route decision recorded on **every** terminal path (success, each failure class, cancellation, unknown-consumption); never silently swallowed (the old build's bug).
Boundaries: test suite. Data/API impact: none. Security: canary. Failure/rollback: n/a.
Tests: each terminal path records usage + decision.
Evidence: usage-recording suite report.
Deps: P5-PAPI-002, P4-OBS-001, P3b-QUOTA-005. Parallel-with: P5-TEST-001/002. Blocks: P5 gate.
DoD: usage recorded on every terminal path.

---

## 13. Phase structure — Dashboard, breadth & release (P6, P7, P8)

### Phase P6 — Dashboard completion & operations

- **Objective:** the full production dashboard for the approved navigation + connect-a-client + tray,
  so the owner operates everything without a terminal.
- **Scope:** `UI`, `CAPI`(diagnostics/benchmark/settings), `FND`(tray).
- **Out of scope:** provider breadth (P7); packaging (P8); image-generation screens (future scope).
- **Entry:** P5 gate green (for Playground/Usage data) + P4 (diagnostics) + P2a (DS) green.
- **Dependencies:** every read model + `/diagnostics/*` + `/settings`; each surface lists its own backend contract.
- **Parallelization:** most surfaces are independent once their backend contract is stable and P2a is green.
- **Expected artifacts:** the 12 nav surfaces + auth/recovery surfaces (from P2b) + connect-a-client + tray.
- **DB impact:** none new (reads read-models). **API impact:** `/diagnostics/routes*`, `/models/{id}/benchmark`, full `/settings`.
- **Security impact:** no-secret rendering; reveal clears on blur; env names only. **DS impact:** consumes `ModelIntelligence`, `Routing`, `Quota`, `Diagnostics`, `Security` domain components + primitives; **the `ui_kits/venom-console` surfaces are references, not production code.**
- **Testing:** axe a11y + visual regression per theme×density + Playwright critical flows; no-secret-rendering.
- **Migration/recovery:** none. **Acceptance gate:** the owner can operate everything implemented
  through P6 from the dashboard + tray without touching a terminal. (Backup/Restore is a P8
  capability: its endpoints, container behavior, and dashboard surface land in P8 and are exercised
  end-to-end at the **P8 gate** via `P8-UI-001` + `P8-BKP-003` — not here.)
- **Required evidence:** Playwright flows (connect account, view fleet, read a route explanation, create a key, connect a client), a11y + visual-regression reports.
- **Exit:** gate green. **Rollback/containment:** UI is additive over stable APIs; a broken surface is reverted without backend impact.

**UI rule for every surface:** built only from `@venom/design-system` inventory components + tokens;
compose within the standard page anatomy from `patterns/patterns.md`; render **every domain state** per
`states/state-matrix.md` (loading/empty/unknown/stale/degraded/error, icon **+ text**, never color-alone,
never a fabricated number); fork from the relevant `ui_kits/venom-console` reference screen; feed view
models from the control API (components never fetch). Each card names its **required backend contract**.

#### P6-CAPI-001 — Diagnostics, benchmark & full settings endpoints
Purpose: backends the dashboard needs beyond earlier phases.
References: [`09 §3.9/§2`](09-control-api.md), [`05 §7`](05-tier-engine.md#7-observability).
Preconditions: P4-OBS-001, P3b-CAPI-002, P2b-CAPI-005.
Scope: `GET /diagnostics/routes` + `GET /diagnostics/routes/{request_id}` (route_decision + attempts; no prompts/responses/raw errors); `POST /models/{id}/benchmark` (async → job, owner-enabled canonical quality rating); full `GET/PUT /settings` (theme/density, staleness windows, probe caps, binds, enrichment).
Boundaries: `internal/httpapi`(control). Data impact: benchmark records. API impact: those endpoints. Security: owner-gated; secret-free diagnostics. Failure/rollback: n/a.
Tests: route explanation reads records; benchmark async; settings round-trip.
Evidence: contract tests.
Deps: P4-OBS-001, P3b-CAPI-002, P2b-CAPI-005. Parallel-with: UI tasks. Blocks: P6-UI-003/005/008/010.
DoD: diagnostics/benchmark/settings endpoints work, secret-free.

#### P6-UI-001 — Overview surface
Purpose: the at-a-glance operator home.
References: [`07 §6`](07-design-system.md), [`Design_System ui_kits Overview`].
Preconditions: P2b-UI-001. Backend contract: `/providers`, `/accounts`, `/models`, `/diagnostics`.
Scope: fleet/health/quota/tier summary stat cards + recent activity; compose from primitives + domain components.
Boundaries: dashboard. Data/API impact: reads read-models. Security: no secrets. Failure/rollback: n/a.
Tests: renders; empty/loading/error states; axe.
Evidence: render + a11y.
Deps: P2b-UI-001. Parallel-with: other UI. Blocks: P6 gate.
DoD: overview renders all summary states accessibly.

#### P6-UI-002 — Models surface
Purpose: catalog, offerings, certification, capability truth.
References: [`07 §5/§5a`](07-design-system.md), [`Design_System domain/ModelIntelligence`].
Preconditions: P3a-CAPI-001, P3c-UI-001. Backend contract: `/models`, `/offerings`, `/offerings/{id}/certification`.
Scope: catalog table; offering detail; CertificationState chip (conjunction shown); `catalog_only` clearly not-in-a-tier; unknown context never shown as 0; trigger discover/probe.
Boundaries: dashboard. Data/API impact: reads + triggers discover/probe. Security: no secrets. Failure/rollback: n/a.
Tests: cert states render; certified+unknown = not-routable; axe.
Evidence: render + a11y.
Deps: P3a-CAPI-001, P3c-UI-001. Parallel-with: other UI. Blocks: P6 gate.
DoD: models/offerings/cert truthful.

#### P6-UI-003 — Routing surface
Purpose: display tier policy/config (weights fixed in V1).
References: [`07 §5`](07-design-system.md), [`Design_System domain/Routing`], [`05 §1/§8.4`](05-tier-engine.md#1-the-three-tiers-authoritative-policy).
Preconditions: P6-CAPI-001. Backend contract: `/settings`, routing policy read.
Scope: show tier policies (gates/ceilings/thinking/band/fallback); weights fixed (dashboard tuning deferred per [`05 §8.4`]); TierBadge via `tier.*` tokens.
Boundaries: dashboard. Data/API impact: reads. Security: no secrets. Failure/rollback: n/a.
Tests: policies render; tiers colored only via tokens; axe.
Evidence: render + a11y.
Deps: P6-CAPI-001. Parallel-with: other UI. Blocks: P6 gate.
DoD: routing policy displayed correctly.

#### P6-UI-004 — Playground surface
Purpose: send test requests to the tiers.
References: [`07 §6`](07-design-system.md), [`Design_System ui_kits Playground`].
Preconditions: P5-PAPI-002, P5-CAPI-001. Backend contract: `/v1/chat/completions`, `/keys`.
Scope: pick tier, compose a request (incl. `venom` extension), send (stream), show response + `X-Venom-*` route outcome; uses an owner key.
Boundaries: dashboard. Data/API impact: calls `/v1/*`. Security: key handled per reveal rules. Failure/rollback: n/a.
Tests: request/stream/response; extension surfaced; axe.
Evidence: render + a11y.
Deps: P5-PAPI-002, P5-CAPI-001. Parallel-with: other UI. Blocks: P6 gate.
DoD: playground exercises tiers + extension.

#### P6-UI-005 — Usage & Analytics surface
Purpose: consumption per account/model/tier.
References: [`07 §6`](07-design-system.md), [`05 §4/§7`](05-tier-engine.md#4-quota--consumption-accounting).
Preconditions: P5-TEST-003 (usage), P6-CAPI-001. Backend contract: usage read model, `/diagnostics`.
Scope: consumption charts/tables per account/model/tier (tabular numerics, data-viz tokens).
Boundaries: dashboard. Data/API impact: reads usage. Security: no secrets. Failure/rollback: n/a.
Tests: renders; empty/loading; axe.
Evidence: render + a11y.
Deps: P5-TEST-003, P6-CAPI-001. Parallel-with: other UI. Blocks: P6 gate.
DoD: consumption accounting visible.

#### P6-UI-006 — Quota & Limits surface
Purpose: aggregate quota view.
References: [`07 §5 QuotaMeter`](07-design-system.md), [`Design_System domain/Quota`].
Preconditions: P3b-UI-001. Backend contract: `/accounts`, `/quota`.
Scope: per-account per-window QuotaMeter aggregation; unknown/stale distinct; reset windows.
Boundaries: dashboard. Data/API impact: reads quota. Security: no secrets. Failure/rollback: n/a.
Tests: known/unknown/stale render distinctly; axe.
Evidence: render + a11y.
Deps: P3b-UI-001. Parallel-with: other UI. Blocks: P6 gate.
DoD: quota aggregated honestly.

#### P6-UI-007 — Token Health surface
Purpose: account health, cooldowns, circuit breakers.
References: [`07 §5a`](07-design-system.md), [`02 §3`](02-domain-model.md#account-lifecycle-multi-axis-state-model), [`05 §3`](05-tier-engine.md#3-fallback--cooldown).
Preconditions: P4-ROUTE-014, P2b-CAPI-004. Backend contract: `/accounts`, health/cooldown read.
Scope: HealthDot/StateChip for the full `display_status` set; cooldown retry-after; breaker state; reauth/expired prompt the fix action.
Boundaries: dashboard. Data/API impact: reads health. Security: no secrets. Failure/rollback: n/a.
Tests: all display_status values render distinctly; cooldown retry-after; axe.
Evidence: render + a11y.
Deps: P4-ROUTE-014, P2b-CAPI-004. Parallel-with: other UI. Blocks: P6 gate.
DoD: health/cooldown/breaker states truthful.

#### P6-UI-008 — Diagnostics (RouteExplain) surface
Purpose: "why this route?" + reconciliation.
References: [`07 §5 RouteExplain`](07-design-system.md), [`Design_System domain/Diagnostics`], [`09 §3.9`](09-control-api.md).
Preconditions: P6-CAPI-001, P3b-CAPI-002. Backend contract: `/diagnostics/routes*`, `/diagnostics/reconciliation`.
Scope: candidate set with score + exclusion reason codes (verbatim typed codes), chosen row emphasized, thinking clamp notes; reconciliation pending/unknown_consumption list + manual re-sync.
Boundaries: dashboard. Data/API impact: reads diagnostics. Security: no prompts/responses/raw errors shown. Failure/rollback: n/a.
Tests: candidate table + reasons; reconciliation view; axe.
Evidence: render + a11y.
Deps: P6-CAPI-001, P3b-CAPI-002. Parallel-with: other UI. Blocks: P6 gate.
DoD: route explanation + reconciliation visible, secret-free.

#### P6-UI-009 — API Keys surface
Purpose: manage Venom API keys.
References: [`07 §5a secret-reveal`](07-design-system.md), [`09 §3.11`](09-control-api.md).
Preconditions: P5-CAPI-001. Backend contract: `/keys*`.
Scope: create (raw shown once, copy-then-clear), list (prefix + fingerprint only), delete; RPM limit.
Boundaries: dashboard. Data/API impact: `/keys`. Security: raw shown once, cleared on blur; never persisted in DOM. Failure/rollback: n/a.
Tests: create shows raw once; list never raw; clears on blur; axe.
Evidence: render + a11y.
Deps: P5-CAPI-001. Parallel-with: other UI. Blocks: P6 gate.
DoD: keys managed; raw once; secret-safe rendering.

#### P6-UI-010 — Settings surface
Purpose: owner settings.
References: [`07 §6`](07-design-system.md), [`09 §2`](09-control-api.md).
Preconditions: P6-CAPI-001. Backend contract: full `/settings`.
Scope: theme/density, staleness windows, probe caps, binds, enrichment toggle; forms via FormRow; destructive actions confirmed.
Boundaries: dashboard. Data/API impact: `/settings`. Security: no secrets. Failure/rollback: n/a.
Tests: settings round-trip; validation errors; axe.
Evidence: render + a11y.
Deps: P6-CAPI-001. Parallel-with: other UI. Blocks: P6 gate.
DoD: settings editable + persisted.

#### P6-UI-011 — Connect-a-client page
Purpose: Quick Start + client-setup catalog.
References: [`06 P6`](06-roadmap.md), [`08 §8`](08-engineering-standards.md#8-extension-points-the-documented-recipes).
Preconditions: P5-CAPI-001, P5-PAPI-002. Backend contract: `/keys`, `/v1/*`.
Scope: first-run Quick Start (create key → connect providers → point client at `http://127.0.0.1:8081/v1` → watch requests) + a client-setup catalog generating copy-paste config for Claude Code (`ANTHROPIC_BASE_URL`/`ANTHROPIC_AUTH_TOKEN` + model envs), Codex, Cursor, Cline, Continue, generic OpenAI SDK — all against the three tier names; **exactly one generator per target config shape** (no divergent duplicates); the Venom key is **injected at display/launch time and never written into generated files by default**.
Boundaries: dashboard. Data/API impact: reads keys. Security: key injected at display, not persisted to files. Failure/rollback: n/a.
Tests: each target generates valid config; one generator per shape; key not written to file by default; axe.
Evidence: generator tests + render.
Deps: P5-CAPI-001, P5-PAPI-002. Parallel-with: other UI. Blocks: P6 gate.
DoD: quick start + catalog; one generator per shape; key not persisted.

#### P6-UI-012 — Review-queue banner + model-catalog completion
**WITHDRAWN 2026-08-07.** The banner, its `GET /certifications/review` census, and the storage query behind it were deleted. Model qualification became fully automatic (`internal/httpapi/qualification.go`'s 30s tick), so no certification waits on a human review and the census counted a queue that no longer exists. The model-catalog completion half of this task shipped and stands. Kept here as the historical record; the shipped record is `internal/httpapi/controlmux_test.go`'s `TestControlMux_ReviewCensusRouteIsGone`.

Purpose: surface the certification review backlog.
References: [`07 §5/§6`](07-design-system.md), [`04 §5`](04-model-intelligence.md#5-certification).
Preconditions: P3c-CERT-005, P6-UI-002. Backend contract: review count read.
Scope: Banner/Alert with the review count grouped by reason; catalog completion touches.
Boundaries: dashboard. Data/API impact: reads review count. Security: no secrets. Failure/rollback: n/a.
Tests: banner renders count by reason; axe.
Evidence: render + a11y.
Deps: P3c-CERT-005, P6-UI-002. Parallel-with: other UI. Blocks: P6 gate.
DoD: review queue surfaced.

#### P6-FND-001 — System tray
Purpose: desktop run-and-forget.
References: [`01 §2`](01-architecture.md#2-process-model), [`06 P6`](06-roadmap.md).
Preconditions: P0-FND-004.
Scope: pure-Go systray — Open Dashboard / Status / Restart / Quit; bare `venom` → tray mode hides the console; `venom serve` stays headless.
Boundaries: `internal/tray`, `internal/cli`. Data/API impact: none. API impact: none. Security: n/a. Failure/rollback: tray failure falls back to headless with a clear log.
Tests: tray actions (Windows); headless mode unaffected.
Evidence: tray manual-evidence recording (Windows) + headless integration test.
Deps: P0-FND-004. Parallel-with: UI tasks. Blocks: P6 gate.
DoD: tray operates the process; headless unaffected.

#### P6-TEST-001 — Dashboard a11y + visual-regression + critical flows
Purpose: mechanized UI quality.
References: [`08 §5`](08-engineering-standards.md#5-testing-strategy), [`07 §7/§8`](07-design-system.md).
Preconditions: P6-UI-001..012.
Scope: axe a11y on key page flows; visual regression per theme × density on the app surfaces (baselines in the app repo, not the DS package); Playwright critical flows (connect account, view fleet, read a route explanation, create a key, connect a client); no-secret-rendering assertion.
Boundaries: dashboard tests. Data/API impact: none. Security: no-secret rendering. Failure/rollback: n/a.
Tests: the above.
Evidence: a11y + visual + Playwright reports.
Deps: P6-UI-001..012. Parallel-with: P6-TEST-002. Blocks: P6 gate.
DoD: UI passes a11y/visual/critical-flow suites.

#### P6-TEST-002 — Operate-without-terminal acceptance
Purpose: the P6 gate.
References: [`06 P6`](06-roadmap.md).
Preconditions: P6-UI-001..012, P6-FND-001.
Scope: an E2E scenario proving the owner performs the full lifecycle implemented through P6 (setup → connect providers → discover → probe → route a request → read diagnostics → create a key → connect a client) from the dashboard + tray, no terminal. Backup/Restore is **not** part of this gate — its endpoints and surface land in P8 and are exercised end-to-end there (`P8-UI-001`, `P8-BKP-003`, P8 gate).
Boundaries: E2E harness. Data/API impact: none. Security: canary. Failure/rollback: n/a.
Tests: the full-operation scenario.
Evidence: operate-without-terminal recording + report.
Deps: P6-UI-001..012, P6-FND-001. Parallel-with: —. Blocks: P6 gate.
DoD: whole system operable from dashboard + tray.

---

### Phase P7 — Provider breadth & custom integrations

- **Objective:** the remaining 9 proven built-ins + the Custom OpenAI-Compatible path, each with
  offline fixture/contract tests (CI-blocking) + live re-verification (dated evidence, non-CI).
- **Scope:** `PROV`, `EXEC`(per-provider transports), `TEST`.
- **Out of scope:** any routing/schema/UI change (the fleet + router are provider-agnostic).
- **Entry:** P2b gate green (adapter contract + OAuth framework + enrollment) **and** P4 gate green (transport dispatcher). May begin its provider fan-out as soon as those freeze.
- **Dependencies:** `P2b-PROV-001` (frozen contract), `P2b-PROV-006` (OAuth framework), `P4-EXEC-001` (dispatcher), `P3` (discovery/cert).
- **Parallelization:** **all per-provider adapters are parallel** once the contract + dispatcher freeze; the custom path is independent.
- **Expected artifacts:** 9 adapters + native transports; the custom enrollment endpoint; fixture suites; live-verification evidence.
- **DB impact:** none (uses existing schema). **API impact:** `POST /providers/custom/accounts`. **Security impact:** per-provider secret handling; header values encrypted; fixed-port OAuth isolation. **DS impact:** none.
- **Testing:** per-adapter offline fixture/contract (CI-blocking); live re-verification + real-account validation (recorded, non-CI); custom-path onboarding.
- **Migration/recovery:** single-use refresh-token rotation persisted atomically (xai and others).
- **Acceptance gate:** every shipped integration connects a real account, discovers models, and
  certifies ≥ chat; the custom path onboards a new OpenAI-compatible provider **with no code change**.
- **Required evidence:** per-adapter fixture reports (CI) + dated live re-verification + real-account recordings; custom-path onboarding recording.
- **Exit:** gate green. **Rollback/containment:** an adapter is self-contained behind the frozen contract; removing one never affects others or the router.

**Every P7 adapter card shares this contract** (not repeated): implement the typed adapter(s) per
[`03 §1`](03-provider-integration-catalog.md#1-adapter-interfaces-the-pattern) against the frozen
`P2b-PROV-001` contract; register in the provider registry; enforce the authentic-validation rule;
**offline fixture/contract tests are CI-blocking**; **live re-verification + real-account validation
are recorded, non-CI** ([`03 §5.1`](03-provider-integration-catalog.md#51-verification-tiers-what-is-a-ci-gate-vs-what-is-manual-evidence));
never re-introduce a hardcoded model list.

#### P7-EXEC-001 — Native transports for the new providers
Purpose: `native_oauth` + `native_api` transports behind the frozen dispatcher.
References: [`01 §4.3`](01-architecture.md#43-transport-types).
Preconditions: P4-EXEC-001.
Scope: implement the `native_oauth` transport family (Claude Code, Codex, GitHub Copilot account semantics) and finish the `native_api` schema normalizers (Gemini Google→OpenAI) needed by P7 adapters; all selected **by typed capability**, never slug; OAuth refresh via the credential provider.
Boundaries: `internal/execution`. Data impact: none. API impact: none. Security: no credential leak. Failure/rollback: unresolvable → typed error.
Tests: per-transport request mapping/streaming fixtures; no slug switch.
Evidence: transport fixture tests.
Deps: P4-EXEC-001. Parallel-with: all P7 adapters. Blocks: P7 adapters that need them.
DoD: native transports behind the frozen dispatcher.

#### P7-PROV-001 — `claude-code` adapter (OAuth)
Purpose: the Claude Code integration.
References: [`03 §3 claude-code`](03-provider-integration-catalog.md#claude-code--claude-code--proven).
Preconditions: P2b-PROV-006, P7-EXEC-001.
Scope: OAuth (PKCE, JSON token exchange); authorize `https://claude.ai/oauth/authorize`, token `https://platform.claude.com/v1/oauth/token`; identity `GET /api/oauth/profile` → `account.uuid` (stable external ID); discovery `GET /v1/models` with extended `anthropic-beta` + `X-App: cli` headers (**required or 429**); refresh; quota 5h/7d windows; funding provider_evidence (Pro/Max/Team/Enterprise/Free).
Data impact: account/credential/funding/offerings. API impact: via enrollment. Security: beta/identity headers required; secrets never logged. Failure/rollback: missing headers → 429 handled as provider-unavailable.
Tests: fixture identity/discovery/quota/refresh; required-header enforcement.
Evidence: fixture report (CI) + live re-verification (evidence).
Deps: P2b-PROV-006, P7-EXEC-001. Parallel-with: other P7 adapters. Blocks: P7 gate.
DoD: connects, discovers, certifies ≥ chat.

#### P7-PROV-002 — `codex` adapter (OAuth, fixed-port)
Purpose: the Codex integration.
References: [`03 §3 codex`](03-provider-integration-catalog.md#codex--codex-openai--proven), [`03 §2a note`](03-provider-integration-catalog.md#2a-oauth-built-in).
Preconditions: P2b-PROV-006 (fixed-port listener), P7-EXEC-001.
Scope: OAuth PKCE; **fixed redirect `http://localhost:1455/auth/callback`**; identity via `userinfo` + JWT → **`chatgpt_account_id`** (stable external ID; fallbacks `organizations[0].id`, `sub`); discovery/quota `GET /backend-api/wham/usage` + rate-limit headers; required headers `ChatGPT-Account-Id`, `originator=codex_cli_rs`, `User-Agent: codex_cli_rs/...`; `prompt=login` for a second account; funding provider_evidence (`plan_type`).
Data impact: account/credential/funding/offerings. API impact: fixed-port callback. Security: fixed-port listener isolated from control plane, transaction-based. Failure/rollback: state-hash lookup replay-safe.
Tests: fixture identity/usage/headers; fixed-port isolation; prompt=login multi-account.
Evidence: fixture report (CI) + live re-verification.
Deps: P2b-PROV-006, P7-EXEC-001. Parallel-with: other P7 adapters. Blocks: P7 gate.
DoD: connects (fixed-port), discovers, certifies ≥ chat; multi-account safe.

#### P7-PROV-003 — `github-copilot` adapter (OAuth two-token)
Purpose: the Copilot integration.
References: [`03 §3 github-copilot`](03-provider-integration-catalog.md#github-copilot--github-copilot--proven).
Preconditions: P2b-PROV-006, P7-EXEC-001.
Scope: OAuth web flow + PKCE **+ two-token exchange** (GitHub token → Copilot token via `GET /copilot_internal/v2/token`); identity `GET https://api.github.com/user` → `id` (stable external ID); discovery `GET https://models.github.ai/catalog/models` (`X-GitHub-Api-Version: 2026-03-10`); quota `copilot_internal/user` (best-effort); funding provider_evidence; multi-kind credentials (`github_oauth` + `copilot_service`).
Data impact: two credential kinds on one account. API impact: via enrollment. Security: two-token lifecycle; secrets never logged. Failure/rollback: token refresh via credential provider.
Tests: fixture two-token exchange; multi-kind coexistence; discovery parse.
Evidence: fixture report (CI) + live re-verification.
Deps: P2b-PROV-006, P7-EXEC-001. Parallel-with: other P7 adapters. Blocks: P7 gate.
DoD: connects (two-token), discovers, certifies ≥ chat.

#### P7-PROV-004 — `clinepass` adapter (OAuth, paid-locked)
Purpose: the ClinePass integration.
References: [`03 §3 clinepass`](03-provider-integration-catalog.md#clinepass--clinepass--proven-was-metadata-only-implement-as-oauth).
Preconditions: P2b-PROV-006, P7-EXEC-001.
Scope: OAuth extension flow (**PKCE verifier generated but not sent**); authorize/token under `https://api.cline.bot/api/v1/auth/*` (`client_type=extension`, `provider=clinepass`); identity token `userInfo`/`GET /users/me` → **`clineUserId`**; discovery `GET /ai/cline/recommended-models`; refresh `POST /auth/refresh`; **auth header prefixed `workos:`**; extra headers `HTTP-Referer: https://cline.bot`, `X-Title: Cline`, `X-CLIENT-TYPE: venom-router`; quota balance/usages/limits; funding **paid + locked** (override rejected `funding_locked`).
Data impact: account/credential/funding(locked)/offerings. API impact: via enrollment. Security: workos-prefixed token handled; secrets never logged. Failure/rollback: locked funding override rejected.
Tests: fixture identity/discovery/refresh; workos prefix; funding locked.
Evidence: fixture report (CI) + live re-verification.
Deps: P2b-PROV-006, P7-EXEC-001. Parallel-with: other P7 adapters. Blocks: P7 gate.
DoD: connects, discovers, certifies ≥ chat; funding paid-locked.

#### P7-PROV-005 — `xai` adapter (OAuth, fixed-port, rotating refresh)
Purpose: the xAI (Grok) integration.
References: [`03 §3 xai`](03-provider-integration-catalog.md#xai--xai-grok--proven-grok-build-oauth).
Preconditions: P2b-PROV-006 (fixed-port), P7-EXEC-001.
Scope: OAuth2 + PKCE (S256); authorize `https://auth.x.ai/oauth2/authorize` (OIDC-discovered, static fallback); **fixed redirect `http://127.0.0.1:56121/callback`**; identity id_token JWT `sub` (stable external ID) + billing credits; discovery `GET /v1/language-models` (fallback `/v1/models`); refresh **single-use tokens — persist rotated token atomically**; funding provider_evidence + paid credits.
Data impact: account/credential/funding/offerings. API impact: fixed-port callback. Security: single-use refresh rotation persisted atomically; fixed-port isolated. Failure/rollback: rotation atomic — no lost refresh family.
Tests: fixture identity/discovery/rotation; fixed-port isolation.
Evidence: fixture report (CI) + live re-verification.
Deps: P2b-PROV-006, P7-EXEC-001. Parallel-with: other P7 adapters. Blocks: P7 gate.
DoD: connects (fixed-port), rotates refresh atomically, certifies ≥ chat.

#### P7-PROV-006 — `ollama-cloud` adapter (API key)
Purpose: the Ollama Cloud integration.
References: [`03 §3 ollama-cloud`](03-provider-integration-catalog.md#ollama-cloud--ollama-cloud--proven-immutable-id).
Preconditions: P2b-PROV-004 (authentic validation).
Scope: base `https://ollama.com/v1` (OpenAI-compat via `bifrost`/`openai_compatible`); identity `POST https://ollama.com/api/me` → **`account.ID`** (stable external ID); discovery `GET /v1/models`; funding free (evidence from `/api/me`); quota dashboard-only.
Data impact: account/credential/funding/offerings. API impact: via enrollment. Security: key never logged. Failure/rollback: n/a.
Tests: fixture identity/discovery; immutable ID.
Evidence: fixture report (CI) + live re-verification.
Deps: P2b-PROV-004. Parallel-with: other P7 adapters. Blocks: P7 gate.
DoD: connects, discovers, certifies ≥ chat.

#### P7-PROV-007 — `gemini-cli` adapter (API key, Google schema)
Purpose: the Gemini CLI integration.
References: [`03 §3 gemini-cli`](03-provider-integration-catalog.md#gemini-cli--gemini-cli--proven-google-schema-not-openai).
Preconditions: P2b-PROV-004, P7-EXEC-001 (native_api normalizer).
Scope: base `https://generativelanguage.googleapis.com`; auth header **`x-goog-api-key`** (not Bearer); health `GET /v1beta/models?pageSize=1`; discovery `GET /v1beta/models?pageSize=200` (paginate `nextPageToken`; filter TTS/image/live/audio); identity fingerprint, synthetic `Free` label; funding **unknown** (`evidence_required` — a "Free" label is not evidence); Google→OpenAI capability normalizer.
Data impact: account/credential/funding(unknown)/offerings. API impact: via enrollment. Security: key never logged. Failure/rollback: unknown funding excluded from routing until classified.
Tests: fixture discovery pagination/filter; x-goog-api-key; schema normalizer; funding unknown.
Evidence: fixture report (CI) + live re-verification.
Deps: P2b-PROV-004, P7-EXEC-001. Parallel-with: other P7 adapters. Blocks: P7 gate.
DoD: connects, discovers (Google schema), certifies ≥ chat; funding unknown until classified.

#### P7-PROV-008 — `agnes-ai` adapter (API key)
Purpose: the Agnes AI integration.
References: [`03 §3 agnes-ai`](03-provider-integration-catalog.md#agnes-ai--agnes-ai--proven-identity-partial).
Preconditions: P2b-PROV-004.
Scope: base `https://apihub.agnes-ai.com/v1`; identity fingerprint (synthetic Free); health/discovery `GET /v1/models` (drop video models); funding **unknown** (`evidence_required`).
Data impact: account/credential/funding(unknown)/offerings. API impact: via enrollment. Security: key never logged. Failure/rollback: unknown funding excluded until classified.
Tests: fixture discovery (video dropped); funding unknown.
Evidence: fixture report (CI) + live re-verification.
Deps: P2b-PROV-004. Parallel-with: other P7 adapters. Blocks: P7 gate.
DoD: connects, discovers, certifies ≥ chat; funding unknown.

#### P7-PROV-009 — `nvidia-nim` adapter (API key)
Purpose: the NVIDIA NIM integration.
References: [`03 §3 nvidia-nim`](03-provider-integration-catalog.md#nvidia-nim--nvidia-nim--proven-identity-partial).
Preconditions: P2b-PROV-004.
Scope: base `https://integrate.api.nvidia.com/v1`; identity fingerprint (synthetic Free); health/discovery `GET /v1/models` normalized (no static list); funding **unknown** (`evidence_required`).
Data impact: account/credential/funding(unknown)/offerings. API impact: via enrollment. Security: key never logged. Failure/rollback: unknown funding excluded until classified.
Tests: fixture discovery normalized; funding unknown.
Evidence: fixture report (CI) + live re-verification.
Deps: P2b-PROV-004. Parallel-with: other P7 adapters. Blocks: P7 gate.
DoD: connects, discovers, certifies ≥ chat; funding unknown.

#### P7-PROV-010 — Custom OpenAI-Compatible enrollment path
Purpose: onboard any OpenAI-compatible provider with no code change.
References: [`03 §2c`](03-provider-integration-catalog.md#2c-custom-openai-compatible), [`09 §3.2`](09-control-api.md).
Preconditions: P2b-PROV-004, P2b-CAPI-003, P2b-PROV-003.
Scope: `POST /providers/custom/accounts` `{base_url, api_key, headers?[{name,public?}], model_list?, funding}`; **header values stored encrypted** in the credential envelope keyed `header:{name}` (names in `settings_json`; `public` opt-in only; UI masks by default); validate via zero-cost chat probe against `base_url`; discover via `{base}/v1/models` unless `model_list` given; `auth_mode = custom_openai`; runtime reconstructs headers in memory only.
Data impact: custom provider + account + credential(envelope). API impact: `/providers/custom/accounts`. Security: header values encrypted, never in `settings_json`. Failure/rollback: no account before validation.
Tests: header values encrypted (not in settings_json); validation; discovery vs explicit list; funding per account.
Evidence: custom-path onboarding recording + fixture test.
Deps: P2b-PROV-004, P2b-CAPI-003, P2b-PROV-003. Parallel-with: P7 adapters. Blocks: P7 gate.
DoD: custom provider onboarded with no code change; header values encrypted.

#### P7-PROV-011 — Live re-verification evidence process
Purpose: dated, non-CI provider verification.
References: [`03 §5/§5.1`](03-provider-integration-catalog.md#5-re-verification-checklist-before-implementing-any-adapter).
Preconditions: all P7 adapters.
Scope: for each shipped integration, record dated evidence that current authorize/token URLs, scopes, identity fields, discovery shapes, required headers, funding signals, and quota endpoints still match [`03`]; a test requiring a live provider/real credential **never** becomes a CI gate.
Boundaries: evidence docs/records. Data/API impact: none. Security: no secrets in evidence. Failure/rollback: drift documented → adapter update task filed.
Tests: n/a (evidence gathering; the CI gate is the fixture suite).
Evidence: dated re-verification records per adapter.
Deps: P7 adapters. Parallel-with: P7-TEST-001. Blocks: P7 gate (evidence portion).
DoD: dated live re-verification recorded for every shipped integration.

#### P7-TEST-001 — Adapter fixture/contract suite (CI-blocking)
Purpose: deterministic offline provider tests.
References: [`03 §5.1`](03-provider-integration-catalog.md#51-verification-tiers-what-is-a-ci-gate-vs-what-is-manual-evidence), [`08 §5`](08-engineering-standards.md#5-testing-strategy).
Preconditions: P7 adapters + P7-PROV-010.
Scope: per-adapter recorded-fixture tests for identity, discovery, quota, refresh, and the authentic-validation rule (200-for-any-token still caught); offline, deterministic, no network — **CI-blocking**.
Boundaries: test suites. Data/API impact: none. Security: canary. Failure/rollback: n/a.
Tests: the above per adapter.
Evidence: fixture reports both OSes.
Deps: P7 adapters, P7-PROV-010. Parallel-with: P7-PROV-011. Blocks: P7 gate.
DoD: every adapter's offline fixtures pass in CI.

#### P7-TEST-002 — Breadth acceptance (real-account + custom onboarding)
Purpose: the P7 gate.
References: [`06 P7`](06-roadmap.md).
Preconditions: P7-TEST-001, P7-PROV-011, P7-PROV-010.
Scope: every shipped integration connects a real account, discovers models, certifies ≥ chat (recorded, non-CI); the custom path onboards a new OpenAI-compatible provider with no code change (recorded).
Boundaries: E2E + evidence. Data/API impact: none. Security: canary. Failure/rollback: n/a.
Tests: real-account connect+discover+certify (evidence) + custom onboarding (recorded).
Evidence: per-provider recordings + custom onboarding recording.
Deps: P7-TEST-001, P7-PROV-011, P7-PROV-010. Parallel-with: —. Blocks: P7 gate.
DoD: all integrations + custom path validated on real accounts.

---

### Phase P8 — Packaging & hardening

- **Objective:** signed reproducible builds, first-run hardening, portable encrypted backup/restore,
  optional non-loopback inference bind, and the sustained-load readiness gate.
- **Scope:** `REL`, `BKP`, `DB`(M8), `CAPI`(backup/restore), `UI`(Backup/Restore surface), `OPS`, `TEST`.
- **Out of scope:** cloud deployment requirements (not in the approved planning); new product behavior.
- **Entry:** P6 gate green (whole system operable) + P7 (breadth) as scheduled; **encryption (P1) + schema (M1–M7) stable** (hard precondition for backup).
- **Dependencies:** P1 (encryption/keyring), all M-groups (schema stable), P0 (startup), the full engine.
- **Parallelization:** build/first-run/tray-packaging ∥ backup/restore ∥ load harness.
- **Expected artifacts:** signed binaries; backup/restore endpoints + container; the production Backup/Restore dashboard surface (`P8-UI-001`); load-readiness harness; operational docs.
- **DB impact:** M8 (`backup_metadata`/records). **API impact:** `POST /backup`, `POST /restore` (async → jobs). **Security impact:** AEAD container; no raw master-key export; owner-auth re-establishment on restore. **DS impact:** consumes the `ui_kits/venom-console` Backup composition as **reference only** — the production surface is `P8-UI-001`.
- **Testing:** backup/restore round-trip + failure paths + cross-device; clean-machine install; sustained-load readiness.
- **Migration/recovery:** backup before destructive migration; restore atomic swap with rollback; live state untouched on failure.
- **Acceptance gate (deterministic):** a clean machine installs, runs, and connects providers;
  **sustained-load readiness** = **≥ 30 min** continuous at **≥ 20 RPS / ≥ 20 concurrent** against fake
  backends, **internal error rate ≤ 0.5%** (Venom-origin 5xx), **p95 routing-overhead latency
  reported**, and **zero invariant violations** (no quota overcommit on any window, no Lite paid
  selection, no secret in any log/audit); backup/restore round-trips with full data integrity and no
  plaintext exposure, including restore re-establishing the owner password from the container — and
  the backup→restore operation (creation, validation, restore, owner-password re-establishment,
  post-restore verification) is performed **end-to-end from the dashboard** (`P8-UI-001`).
- **Required evidence:** load-readiness report (with the numbers); backup/restore test matrix; backup/restore UI E2E + a11y report (`P8-UI-001`); clean-machine install recording.
- **Exit:** gate green — **V1 production-ready.**
- **Rollback/containment:** restore keeps the previous state for rollback; a destructive migration is preceded by a backup; release verification gates the artifact before publish.

#### P8-REL-001 — Reproducible signed builds
Purpose: one versioned signed artifact per platform.
References: [`08 §9`](08-engineering-standards.md#9-release--operational-readiness), [`01 §7`](01-architecture.md#7-tech-stack).
Preconditions: P2a-UI-001 (embed), P6 gate.
Scope: single static binary per platform (`CGO_ENABLED=0`), dashboard embedded via `go:embed`, versioned, signed; deterministic build.
Boundaries: build pipeline. Data/API impact: none. API impact: none. Security: signed artifact. Failure/rollback: unsigned/irreproducible build blocks release.
Tests: reproducible build; signature verify; one artifact contains the dashboard.
Evidence: signed build + reproducibility log both OSes.
Deps: P2a-UI-001, P6 gate. Parallel-with: P8-BKP-*. Blocks: P8 gate.
DoD: signed reproducible binary per platform.

#### P8-REL-002 — First-run + lifecycle hardening
Purpose: fail-closed first-run + readiness/liveness/shutdown/crash-recovery.
References: [`01 §2`](01-architecture.md#2-process-model), [`08 §9`](08-engineering-standards.md#9-release--operational-readiness).
Preconditions: P0-FND-007, P1-SEC-004.
Scope: verify lock → keyring → migrations → integrity check all fail closed before a listener opens (no half-state); `/health` liveness; graceful shutdown bounded; crash-recovery verification (startup reconciliation of sessions/reauth/reservations).
Boundaries: `internal/app`. Data/API impact: none. API impact: `/health`. Security: fail-closed order. Failure/rollback: broken first-run leaves no half-state.
Tests: fail-closed first-run; crash-recovery on restart; graceful shutdown.
Evidence: first-run + recovery tests.
Deps: P0-FND-007, P1-SEC-004. Parallel-with: P8-BKP-*. Blocks: P8 gate.
DoD: hardened, fail-closed lifecycle.

#### P8-DB-001 — Migration M8: backup metadata
Purpose: backup records.
References: [`08 §9`](08-engineering-standards.md#9-release--operational-readiness).
Preconditions: P5-DB-001 (schema otherwise stable).
Scope: `backup_metadata`/records (schema version, integrity hash, created_at, KDF params — **no passphrase/secret**).
Boundaries: `internal/storage` migrations. Data impact: M8. API impact: none. Security: no secret columns. Failure/rollback: down path in dev.
Tests: migration up/down; no secret column.
Evidence: migration CI log.
Deps: P5-DB-001. Parallel-with: —. Blocks: P8-BKP-001.
DoD: M8 applies/rolls back.

#### P8-BKP-001 — Backup export (AEAD container)
Purpose: a single portable encrypted container.
References: [`08 §9`](08-engineering-standards.md#9-release--operational-readiness), [`09 §3.10`](09-control-api.md).
Preconditions: P1-SEC-002/003, P8-DB-001 (schema stable).
Scope: consistent SQLite snapshot (`.backup`/`BEGIN IMMEDIATE`); Argon2id KDF from passphrase (`time=3, mem=64MiB, threads=4`); encrypt snapshot + **wrapped data key** (never the raw device master key) into one AEAD container (AES-256-GCM/XChaCha20-Poly1305); manifest inside the payload (schema version, integrity hash, timestamp, KDF params); atomic write (`.tmp`→rename); secure temp erase; **includes the `owner_auth` row**; passphrase never logged/stored.
Boundaries: `internal/storage`, `internal/secrets`. Data impact: reads DB + keyring. API impact: consumed by `/backup`. Security: no raw master-key export; passphrase never persisted. Failure/rollback: atomic write; temp erased.
Tests: container produced; wrapped key; manifest; passphrase never in output (canary).
Evidence: backup export test.
Deps: P1-SEC-002/003, P8-DB-001. Parallel-with: P8-REL-*. Blocks: P8-BKP-002/003, P8-CAPI-001.
DoD: single AEAD container with wrapped key + manifest + owner_auth; no plaintext.

#### P8-BKP-002 — Restore (rewrap + atomic swap + rollback + owner-password re-establish)
Purpose: safe restore.
References: [`08 §9`](08-engineering-standards.md#9-release--operational-readiness), [`09 §3.10/§5.7`](09-control-api.md).
Preconditions: P8-BKP-001.
Scope: decrypt to a temp dir (never over live DB); authenticate AEAD tag (fail on mismatch); validate manifest (schema version) + `PRAGMA integrity_check`; **rewrap the data key** to the current device keyring key (never export raw master key); atomic swap with the previous state kept for rollback; on any failure the live state is untouched + temp cleaned; **restore re-establishes the owner password from the container** (`owner_auth`); sessions revoked/rebuilt (not exported).
Boundaries: `internal/storage`, `internal/secrets`, `internal/app`. Data impact: swaps DB + keyring wrap. API impact: consumed by `/restore`. Security: no raw master-key export; typed errors `wrong_passphrase`/`corrupted_container`/`schema_incompatible`. Failure/rollback: live state untouched on any failure; rollback copy retained.
Tests: restore round-trip; wrong passphrase; corrupted; interrupted; cross-device (different master key); owner-password re-established.
Evidence: restore test matrix.
Deps: P8-BKP-001. Parallel-with: P8-REL-*. Blocks: P8-BKP-003, P8-CAPI-001.
DoD: restore rewraps + swaps atomically with rollback; owner password re-established.

#### P8-BKP-003 — Backup/restore acceptance suite
Purpose: prove all backup/restore paths.
References: [`08 §9`](08-engineering-standards.md#9-release--operational-readiness), [`09 §5.9`](09-control-api.md#5-owner-authentication-first-run-login-session-re-verification).
Preconditions: P8-BKP-001/002.
Scope: round-trip integrity; wrong passphrase; corrupted container; interrupted restore (live untouched); cross-device restore; restore re-establishes the owner login password; passphrase independent of login password; no plaintext/secret in logs/temp (canary).
Boundaries: test suite. Data/API impact: none. Security: canary. Failure/rollback: n/a.
Tests: the above matrix.
Evidence: backup/restore suite report both OSes.
Deps: P8-BKP-001/002. Parallel-with: —. Blocks: P8 gate.
DoD: full backup/restore matrix passes with no plaintext exposure.

#### P8-CAPI-001 — `POST /backup` + `POST /restore` (async)
Purpose: expose backup/restore via the control API.
References: [`09 §3.10`](09-control-api.md).
Preconditions: P8-BKP-001/002, P2b-JOBS-001.
Scope: `POST /backup` `{passphrase}` → `202 {job_id, status_url}`; `POST /restore` (multipart container + `{passphrase}`) → `202 {job_id, status_url}`; poll canonical `/jobs/{job_id}`; typed job errors (`wrong_passphrase`/`corrupted_container`/`schema_incompatible`); passphrase never logged; response never contains key material.
Boundaries: `internal/httpapi`(control). Data impact: via BKP. API impact: `/backup`,`/restore`. Security: owner-gated; passphrase never logged. Failure/rollback: live state untouched on restore failure.
Tests: async backup/restore → jobs; typed errors; passphrase never in output (canary).
Evidence: backup/restore endpoint tests.
Deps: P8-BKP-001/002, P2b-JOBS-001. Parallel-with: —. Blocks: P8-UI-001, P8 gate.
DoD: async backup/restore endpoints; secret-free.

#### P8-UI-001 — Backup & Restore surface (production UI)
Purpose: the owner operates backup and restore entirely from the dashboard — the production surface for the P8 backup/restore capability.
References: [`09 §3.10/§5.5/§5.7`](09-control-api.md#310-backup--restore-async), [`08 §9`](08-engineering-standards.md#9-release--operational-readiness), [`07 §5a/§6`](07-design-system.md), [`Design_System ui_kits/venom-console` Backup screen], [`Design_System states/state-matrix.md`].
Preconditions: P2a gate; P8-CAPI-001 (backup/restore control API); P2b-SEC-005 (re-verification); P2b-JOBS-001 (job polling).
Scope: a Backup & Restore section under the approved **Manage/Settings area** (no new top-level navigation item): create-backup flow with passphrase entry **+ confirmation** (masked, never echoed, cleared on hide/blur); async progress via the canonical `GET /jobs/{job_id}` with **persisted job state** (survives page reload); validation result display; restore flow with container upload + passphrase + explicit destructive-action confirmation **gated on a fresh (≤ 5 min) re-verification** ([`09 §5.5`] "similarly sensitive controls"); post-restore owner-password re-establishment messaging (session revoked → re-login per [`09 §5.7`]); failure states (`wrong_passphrase`, `corrupted_container`, `schema_incompatible`), interrupted-restore and recovery states rendered per `state-matrix.md` (icon + text, never color-alone, never a fabricated status).
Non-goals: container/engine behavior (P8-BKP-001/002); endpoints (P8-CAPI-001); any new navigation surface; scheduled backups (not in V1).
Boundaries: dashboard workspace.
Data impact: none. API impact: consumes `/backup`, `/restore`, `/jobs/{job_id}`.
Security: passphrase never persisted, never logged, never left in the DOM after submit (canary); restore gated on fresh re-verification; no key material or secret in any rendered state.
Failure/rollback: a failed/interrupted restore surfaces the typed job error and the untouched-live-state guarantee (P8-BKP-002); the UI returns to a recoverable state.
Tests: Playwright E2E — create backup (passphrase confirm → job progress → validation result); restore confirmation blocked without fresh re-verify, allowed with it; each typed failure state renders distinctly; job state survives reload; passphrase absent from DOM/logs (canary); axe a11y + keyboard flow.
Evidence: backup/restore UI E2E + a11y report; recording of the dashboard-operated backup→restore round-trip (rolls into the P8 gate).
Deps: P8-CAPI-001. Parallel-with: P8-REL-*. Blocks: P8 gate.
DoD: backup/restore fully operable from the dashboard with truthful states, re-verify-gated restore, and zero secret/passphrase persistence.

#### P8-REL-003 — Optional non-loopback inference bind
Purpose: let the owner expose the data plane, never the control plane.
References: [`01 §6a/§6b`](01-architecture.md#6a-control-plane-owner-ui--control-api), [`06 P8`](06-roadmap.md).
Preconditions: P5-PAPI-001, P2b-CAPI-001.
Scope: explicit config to bind the **inference** plane to a non-loopback host:port; **the control plane remains loopback-only** (no escape hatch); vk auth + RPM still required.
Boundaries: `internal/config`, `internal/httpapi`. Data impact: none. API impact: bind config. Security: control plane stays loopback-only; a config binding control off-loopback is rejected at startup. Failure/rollback: invalid bind aborts startup.
Tests: inference off-loopback works with vk; control off-loopback rejected.
Evidence: bind config tests.
Deps: P5-PAPI-001, P2b-CAPI-001. Parallel-with: P8-BKP-*. Blocks: P8 gate.
DoD: optional inference bind; control loopback-only enforced.

#### P8-REL-004 — Sustained-load readiness harness
Purpose: the quantitative readiness gate.
References: [`06 P8`](06-roadmap.md), [`08 §9`](08-engineering-standards.md#9-release--operational-readiness).
Preconditions: P4 gate, P5 gate.
Scope: drive the routing hot path against fake provider backends for **≥ 30 min** at **≥ 20 RPS / ≥ 20 concurrent**; measure **internal (Venom-origin) error rate ≤ 0.5%** (excluding provider-origin), **report p95 routing-overhead latency** (Venom decision + reservation only), and assert **zero invariant violations** (no overcommit on any window, no Lite paid selection, no secret in any log/audit). Conservative planning contracts, tightenable without architecture change.
Boundaries: load harness. Data/API impact: none. Security: canary during load. Failure/rollback: n/a.
Tests: the sustained-load scenario with the numbers.
Evidence: load-readiness report (numbers + p95).
Deps: P4 gate, P5 gate. Parallel-with: P8-BKP-*. Blocks: P8 gate.
DoD: readiness thresholds met; zero invariant violations under load.

#### P8-REL-005 — Release verification, rollback/forward-fix, versioning, notes
Purpose: gate the artifact before publish.
References: [`08 §9`](08-engineering-standards.md#9-release--operational-readiness).
Preconditions: P8-REL-001/002/004, P8-BKP-003.
Scope: release verification checklist (all gates green both OSes); rollback/forward-fix procedure; semantic versioning; release notes; backup-before-destructive-migration hook wired into the migration path.
Boundaries: build/release pipeline. Data/API impact: none. API impact: none. Security: n/a. Failure/rollback: destructive migration preceded by an automatic backup.
Tests: release checklist gate; backup-before-migration hook fires.
Evidence: release verification log; version + notes.
Deps: P8-REL-001/002/004, P8-BKP-003. Parallel-with: P8-OPS-001. Blocks: P8 gate.
DoD: release verified, versioned, documented; destructive migrations backed up first.

#### P8-OPS-001 — Operational documentation
Purpose: the runbooks an operator needs.
References: [`08 §9/§10`](08-engineering-standards.md#9-release--operational-readiness), [`09 §5.7`](09-control-api.md).
Preconditions: P8-REL-002, P8-BKP-002, P2b-SEC-007.
Scope: install + run modes (tray/serve), config reference (defaults→env→flags), backup/restore runbook, lost-password recovery (restore + local reset), readiness/shutdown, crash recovery; preserves the local/private product model (no cloud deployment requirement).
Boundaries: docs. Data/API impact: none. API impact: none. Security: recovery documented without exposing secrets. Failure/rollback: n/a.
Tests: docs reviewed against the shipped behavior.
Evidence: operational docs.
Deps: P8-REL-002, P8-BKP-002, P2b-SEC-007. Parallel-with: P8-REL-005. Blocks: P8 gate.
DoD: complete operator runbooks; local/private model preserved.

#### P8-TEST-001 — Clean-machine install acceptance
Purpose: prove install→run→connect on a clean machine.
References: [`06 P8`](06-roadmap.md).
Preconditions: P8-REL-001/002, P7 gate.
Scope: a clean machine (Windows primary, Linux) installs the signed binary, runs first-run setup, and connects providers.
Boundaries: E2E. Data/API impact: none. Security: canary. Failure/rollback: n/a.
Tests: clean-machine install→run→connect.
Evidence: clean-machine recording both OSes.
Deps: P8-REL-001/002, P7 gate. Parallel-with: P8-TEST-002. Blocks: P8 gate.
DoD: clean-machine install works both OSes.

#### P8-TEST-002 — Zero-invariant-violation-under-load assertion
Purpose: bind the load run to the safety invariants.
References: [`06 P8`](06-roadmap.md), [`08 §9`](08-engineering-standards.md#9-release--operational-readiness).
Preconditions: P8-REL-004.
Scope: during (and after) the load run, assert **zero** quota overcommit on any window, **zero** Lite paid selections, **zero** secrets in any log/audit (canary), and usage recorded on every terminal path.
Boundaries: load-harness assertions. Data/API impact: none. Security: canary. Failure/rollback: n/a.
Tests: the invariant assertions under load.
Evidence: invariant-under-load report.
Deps: P8-REL-004. Parallel-with: P8-TEST-001. Blocks: P8 gate.
DoD: invariants hold under sustained load.

---

## 14. Database & migration sequence (dependency-safe)

Migration **numbering, landing, application, and integration serialize** — no two migration owners
concurrently modify or land the canonical migration sequence; schema design, migration preparation,
and isolated testing of independent groups may proceed concurrently, and a later group is
conceptually rebased onto the latest accepted schema ordering before landing (§8). Each group is one
reviewable task; forward-only in prod with a tested down path in dev; checksum-guarded,
integrity-checked on open, LF-forced. Per [`02 §5`](02-domain-model.md#5-sqlite-schema-sketch)
and [`08 §4`](08-engineering-standards.md#4-data--migrations).

| Group | Phase / Task | Tables (+ key constraints/indexes) | Transaction & invariants proven by tests |
|---|---|---|---|
| **M0** | P0 / `P0-DB-002` | schema-version/baseline | migration runner integrity + rollback |
| **M1** | P2b / `P2b-DB-001` | `owner_auth` (one row, Argon2id + params), `owner_sessions` (verifier-only, idle/absolute/reverify), `auth_events` (append-only) | one-owner-row; append-only auth audit; hash-only |
| **M2** | P2b / `P2b-DB-002` | `providers` (funding policy), `accounts` (multi-axis CHECKs, `UNIQUE(provider_id,external_id)`, `UNIQUE(id,provider_id)`), `account_credentials` (`state` CHECK + `idx_cred_active_per_kind`, `idx_cred_staged_per_kind`, `idx_cred_fingerprint`), `account_funding_evidence` (append-only + `idx_funding_current`), `oauth_transactions` | active-per-kind & staged-per-kind partial indexes; funding one-current-row; nullable-means-unknown |
| **M3** | P2b / `P2b-DB-003` | `audit_events` (append-only), `jobs` (5-state CHECK) | audit append-only; job-state CHECK |
| **M4** | P3a / `P3a-DB-001` | `models` (`canonical_key_sha256` unique), `provider_model_aliases`, `account_model_offerings` (`UNIQUE(account_id, provider_model_id)`), `offering_operations`, `certifications` (6-state CHECK, **no `rejected`**, capability truth **separate** column), `discovery_runs` (generation) | six-state CHECK; truth separate; offering identity unique; nullable-means-unknown |
| **M5** | P3b / `P3b-DB-001` | `quota_windows` (`window_key` **NOT NULL**, `UNIQUE(account_id,source,unit,window_type,window_key)`, `version`), `quota_reservations` (5-state CHECK, `dispatched_at`, `UNIQUE(request_id,attempt_id)`), `quota_reservation_allocations` (PK `(reservation_id,window_id)`), `cooldowns` | 5-state CHECK, no stored `expired`; window_key NOT NULL; `BEGIN IMMEDIATE` atomic reservation across windows |
| **M6** | P4 / `P4-DB-001` | `route_decisions`, `route_attempts` (secret-free), circuit-breaker state, deficit cells (`(tier,bucket,funding_class)`) | records secret-free (canary); deficit per bucket not global |
| **M7** | P5 / `P5-DB-001` | `venom_api_keys` (**hash-only**), `usage_records` | keys hash-only; usage on every terminal path |
| **M8** | P8 / `P8-DB-001` | `backup_metadata` (no secret columns) | no passphrase/secret stored |

**Ordering guarantee:** consumers never precede their schema — `M1` (auth) before any control
mutation; `M2` (accounts/credentials/funding) before enrollment; `M4` (offerings/cert) before
discovery/routing; `M5` (quota) before reservation/routing; `M6` before route records; `M7` before the
public API; `M8` before backup. No group is collapsed; each is independently reviewable and reversible in dev.

---

## 15. Security implementation sequence

Order (security precedes every sensitive capability):
`P1 keyring/crypto/sanitize/canary` → `P2b M1 → owner setup → login → session → CSRF → re-verify →
rate-limit/audit → local-reset recovery` → `P2b-CAPI-001 network+auth middleware` → **only then** any
control mutation (`CAPI-003/004/005`) → credential reveal (`CAPI-004`) gated on `SEC-005` freshness →
`P8` backup re-establishes owner password.

| Requirement | Task(s) | Negative/security tests |
|---|---|---|
| Keyring outside DB, bound AAD, rotation, fail-closed reconcile | `P1-SEC-001..004` | wrong-AAD fails; missing-key aborts startup; crash-mid-rotation recovers |
| Full redaction boundary + canary | `P1-SEC-005/006` | injected secret in no output; partial redaction never emitted |
| First-run setup (Argon2id, params stored) | `P2b-SEC-001` | second setup rejected; password never logged |
| Login/logout, opaque session, cookie rules | `P2b-SEC-002` | generic `invalid_credentials`; cookie `HttpOnly/SameSite=Strict/Path` |
| Idle (30 m) + absolute (12 h) + revocation + restart | `P2b-SEC-003` | expiry rejected; absolute not extended; password change revokes all |
| Session-bound CSRF before side effect | `P2b-SEC-004` | forged/cross-session/missing → 403 before mutation |
| 5-min re-verification freshness | `P2b-SEC-005` | stale → `reverification_required`; reuse past 5 min rejected |
| Rate limit + lockout + audit-without-secret | `P2b-SEC-006` | lockout after threshold; audit stores no secret (canary) |
| Loopback + Host-allowlist + no XFF trust | `P2b-CAPI-001` | non-loopback 403; bad Host 403 before session; no XFF bypass |
| Credential reveal gated + no-store + once | `P2b-CAPI-004` | reveal without fresh reverify rejected; once; no-store |
| Lost-password recovery (restore + local reset) | `P2b-SEC-007`, `P8-BKP-002` | local reset → first-run; restore re-establishes password; credentials stay keyring-protected |
| Fixed-port OAuth independence | `P2b-PROV-006`, `P7-PROV-002/005` | fixed-port callback not session-bound; isolated from control port |
| Secrets never logged (canary, every build) | `P1-SEC-006` + per-phase | canary green on every gate |

No control-plane mutation is planned before its authentication, authorization, and CSRF requirements
exist (`P2b-CAPI-001` hard-depends on `SEC-002/004`; every mutation endpoint hard-depends on `CAPI-001`).

---

## 16. Provider implementation sequence

Layering per [`03`](03-provider-integration-catalog.md):
1. **Framework** — `P2b-PROV-001` (typed adapter contract, registry, freeze).
2. **Definitions** — `P2b-PROV-002` (11 built-ins + custom descriptor, funding policies).
3. **Credential + validation** — `P2b-PROV-003` (envelope), `P2b-PROV-004` (authentic validation).
4. **OAuth framework** — `P2b-PROV-006` (PKCE, transactions, fixed-port, `prompt=login`).
5. **Reauthentication staging** — `P2b-PROV-008`.
6. **Proven vertical slice** — `P2b-PROV-005` (opencode-zen API-key), `P2b-PROV-007` (antigravity OAuth).
7. **Breadth (parallel after contract + dispatcher freeze)** — `P7-PROV-001..009`, `P7-EXEC-001`.
8. **Custom path** — `P7-PROV-010`.
9. **Verification** — fixture/contract (CI-blocking, `P2b`/`P7-TEST-001`) vs live re-verification + real-account (recorded, non-CI, `P7-PROV-011`/`P7-TEST-002`).

| slug | confidence | auth | stable external ID | discovery | quota | funding default | task | special quirks |
|---|---|---|---|---|---|---|---|---|
| opencode-zen | proven | API key | fingerprint | `/v1/models` ∩ models.dev | — | free (owner_policy) | `P2b-PROV-005` | free-set intersect (free-safety P3a) |
| antigravity | proven* | OAuth (confidential) | email+project_id | `:fetchAvailableModels` | remaining fractions | provider_evidence | `P2b-PROV-007` | client secret env; "Setup required" when unset |
| claude-code | proven | OAuth PKCE | `account.uuid` | `/v1/models` (+beta hdrs) | 5h/7d | provider_evidence | `P7-PROV-001` | beta/`X-App` headers required or 429 |
| codex | proven | OAuth fixed-port | `chatgpt_account_id` | `/wham/usage` | wham + headers | provider_evidence | `P7-PROV-002` | fixed redirect :1455; `prompt=login`; required headers |
| github-copilot | proven | OAuth two-token | `user.id` | `models.github.ai/catalog` | copilot_internal (best-effort) | provider_evidence | `P7-PROV-003` | GitHub→Copilot token exchange; multi-kind creds |
| clinepass | proven | OAuth (`workos:`) | `clineUserId` | `/recommended-models` | balance/usages | **paid (locked)** | `P7-PROV-004` | PKCE verifier not sent; extra headers |
| xai | proven | OAuth fixed-port | JWT `sub` | `/v1/language-models` | billing credits | provider_evidence | `P7-PROV-005` | fixed redirect :56121; single-use refresh rotation |
| ollama-cloud | proven | API key | `account.ID` | `/v1/models` | dashboard-only | free (evidence) | `P7-PROV-006` | identity via `/api/me` |
| gemini-cli | proven | API key (`x-goog-api-key`) | fingerprint | `/v1beta/models` | — | **unknown** (`evidence_required`) | `P7-PROV-007` | Google schema normalizer; pagination |
| agnes-ai | proven (identity partial) | API key | fingerprint | `/v1/models` | — | **unknown** (`evidence_required`) | `P7-PROV-008` | drop video models |
| nvidia-nim | proven (identity partial) | API key | fingerprint | `/v1/models` | — | **unknown** (`evidence_required`) | `P7-PROV-009` | normalized, no static list |
| *(custom)* | — | OpenAI-compatible | fingerprint | `{base}/v1/models` | — | per account | `P7-PROV-010` | header values encrypted in envelope |

\* antigravity is proven but env-gated; **no provider fact is fabricated** — every adapter records dated
live re-verification before/at implementation. **Blocking unknowns** (per [`03 §5`]): several quota
endpoints are `—`/best-effort (opencode-zen, agnes-ai, gemini-cli, nvidia-nim) → those accounts rely on
the mandatory local-safety budget; antigravity has no immutable `sub` (documented weakness). Live tests
are **never** CI-blocking.

---

## 17. Routing & quota sequence

Quota invariants (P3b) exist **before** routing (P4). Routing runs the approved Step 1–8 order:

| Step | Concept | Task | Invariant |
|---|---|---|---|
| 1 | Normalize + required-capability extraction | `P4-ROUTE-001` | union with `venom.required_capabilities` |
| — | Thinking normalization | `P4-ROUTE-003` | clamp to tier ceiling then certified-max; graceful degrade |
| 2 | Candidate pool | `P4-ROUTE-004` | only `certified ∧ supported`; immutable snapshot |
| 3 | Hard gates (funding/context/capability/health/quota/cooldown) | `P4-ROUTE-005` | Lite free-only; unknown ⇒ excluded |
| 4 | Route groups (anti-inflation) | `P4-ROUTE-006` | N accounts → one group scored once |
| 5 | Scoring | `P4-ROUTE-007` | per-tier weights; Lite hard-eligibility only |
| 6 | Competitive band | `P4-ROUTE-008` | Pro ≤0.08 / Max ≤0.03; never auto-widen |
| 7 | Distribute + select | `P4-ROUTE-009/010/011/012` | Pro deficit per `(tier,bucket,funding_class)`; Max DRR+P2C, no funding target; stickiness preference-only |
| 8 | Reserve → execute → reconcile/fallback | `P4-ROUTE-013/014` + `P4-EXEC-*` | per-attempt atomic reservation; bounded fallback; no cross-funding |

Quota mechanics (P3b): windows + `window_key` normalization (`QUOTA-001`); mandatory local-safety
(`QUOTA-002`); estimate dimensions (`QUOTA-003`); atomic all-or-nothing reservation (`QUOTA-004`);
five-state machine (`QUOTA-005`); discriminated janitor (`QUOTA-006`); reconciliation worker
(`QUOTA-007`); sync + scope-correct cooldowns (`QUOTA-008`). **Exactly five reservation states**
(`reserved | reconciliation_pending | settled | released | unknown_consumption`); no stored `expired`.

---

## 18. API sequence

**Control API (`/api/control/v1`)** — built behind the network+auth gate, per [`09`](09-control-api.md):
auth handshake (`P2b-SEC-*`) → middleware + conventions (`CAPI-001/002`) → jobs surface (`JOBS-001`) →
providers/enrollment (`CAPI-003`) → account lifecycle incl. reveal (`CAPI-004`) → settings (`CAPI-005`) →
discovery/models/cert (`P3a-CAPI-*`) → quota/reconciliation (`P3b-CAPI-*`) → probe (`P3c-CAPI-001`) →
keys (`P5-CAPI-001`) → diagnostics/benchmark/full-settings (`P6-CAPI-001`) → backup/restore (`P8-CAPI-001`).
Canonical async surface: **one** `GET /jobs/{job_id}` (OAuth transaction-status the sole exception).

**Public API (`/v1`)** — a thin shell over the frozen engine, after P4: `PAPI-001` vk auth + RPM →
`PAPI-002` `/v1/chat/completions` (routing + streaming + usage) → `PAPI-003` `/v1/models` →
`PAPI-004` `venom` extension → `OBS-001` `X-Venom-*` headers → `PAPI-005` ingress limits →
`PAPI-006` error envelope + privacy + cancellation. Public surface is **exactly**
`POST /v1/chat/completions` + `GET /v1/models`; **no V1 image endpoint** (future scope).

---

## 19. Design System & UI sequence

The Design System is an approved local package `@venom/design-system@1.0.0` (gate-passed). It is
**consumed, never modified**.

- **Integration (P2a):** workspace + `file:` dependency (`DS-001`); build the package first so `dist/`
  exists; global styles + tokens + theme/density via `applyTheme`/`applyDensity` (`DS-002`); Tailwind
  preset `venomTailwindPreset` + no-raw-values lint (`DS-003`); `go:embed` pipeline (`UI-001`); inventory
  render across 3 themes × 2 densities + DS `validate` re-verified green (`DS-004`); version pin +
  no-edit/no-copy adherence check (`DS-005`).
- **Import surface used:** root, `/primitives`, `/domain` (`ProviderFleet`, `ModelIntelligence`, `Quota`,
  `Routing`, `Security`, `Diagnostics`), `/tokens`, `/themes`, `/density`, `/icons`, `/tailwind`,
  `/styles.css`. Domain-state rendering follows `states/state-matrix.md`; composition follows
  `patterns/patterns.md`; screens fork from `ui_kits/venom-console` **as references, not production code**.
- **Persistence:** theme/density server-side via `GET/PUT /settings` (`CAPI-005`), applied before first paint. **No browser storage.**
- **Production surfaces (approved navigation):**

| Group | Surface | Task | Required backend contract |
|---|---|---|---|
| — | Overview | `P6-UI-001` | `/providers`,`/accounts`,`/models`,`/diagnostics` |
| Operate | Providers (Provider Fleet) | `P2b-UI-003` | `/providers`,`/accounts*`, OAuth flow |
| Operate | Models | `P6-UI-002` | `/models`,`/offerings`,`/certification` |
| Operate | Routing | `P6-UI-003` | `/settings`, routing policy |
| Operate | Playground | `P6-UI-004` | `/v1/chat/completions`,`/keys` |
| Insights | Usage & Analytics | `P6-UI-005` | usage read model, `/diagnostics` |
| Insights | Quota & Limits | `P6-UI-006` | `/accounts`,`/quota` |
| Insights | Token Health | `P6-UI-007` | `/accounts`, health/cooldown |
| Insights | Diagnostics | `P6-UI-008` | `/diagnostics/routes*`,`/diagnostics/reconciliation` |
| Manage | API Keys | `P6-UI-009` | `/keys*` |
| Manage | Settings | `P6-UI-010` | full `/settings` |
| Auth/recovery | first-run setup, login, session-expiry, re-verify | `P2b-UI-002` | `/auth/*` |
| Manage | Backup & Restore (within the Manage/Settings area) | `P8-UI-001` | `/backup`,`/restore`,`/jobs/{job_id}` |
| Onboarding | Connect a client | `P6-UI-011` | `/keys`,`/v1/*` |

Every surface renders all domain states per `states/state-matrix.md`; **no UI starts before its backend
contract is stable and P2a is green.** No image-generation UI (future scope).

---

## 20. Testing & evidence program

Per [`08 §5`](08-engineering-standards.md#5-testing-strategy). Full test→task mapping in
[Appendix B](11-appendix-B-requirement-traceability.md).

**20.1 Static gates (every build, both OSes — `P0-TEST-001` + per-phase additions):** gofmt/goimports;
`go vet` + golangci-lint (staticcheck/errcheck/ineffassign/gocyclo/forbidigo); import-graph/layering
(acyclic, forbidden edges); no-slug-switch vet; schema-lint (nullable-means-unknown, one-current-row);
no-hardcoding (no model-name literals, no static model list); **secret canary**; package-export checks;
race detector; **DS `npm run validate` (12/12) + no-raw-values + no-package-copy** (app CI).

**20.2 Unit:** state transitions (account, credential, funding, certification six-state, reservation
five-state); evidence precedence; eligibility gates; scoring; competitive band; Pro deficit; Max DRR;
P2C; error classification; quota calculations; owner-auth rules; request/extension validation; thinking normalization.

**20.3 DB integration (temp SQLite):** migrations up/down; constraints; partial indexes (cred
active/staged-per-kind, funding current, window identity); atomic reservation + concurrent no-overcommit;
settlement/release; reconciliation; staged credential swap + interruption; job leasing; audit append-only.

**20.4 Provider fixtures (CI-blocking):** per adapter — request construction, headers, parsing,
streaming, error mapping, discovery, identity, quota, refresh, authentic-validation (200-for-any-token caught).

**20.5 API:** public OpenAI compatibility; control contracts; auth/CSRF; polling; idempotency; typed
errors; redaction.

**20.6 UI:** axe a11y; keyboard; domain states; visual regression (per theme × density, app baselines);
responsive; **no-secret rendering**; DS integration.

**20.7 E2E:** first-run setup; enrollment; discovery; probe; certification; routable request; fallback;
quota exhaustion; reauthentication; credential reveal; API-key creation; backup/restore; crash recovery.

**20.8 Load/reliability (P8):** the approved quantitative gates (≥30 min, ≥20 RPS/≥20 concurrent, ≤0.5%
internal error, p95 routing overhead reported, zero invariant violations). No performance claim is made
beyond executed evidence.

**Fixture vs live (providers):** offline fixture/contract tests are the **CI gate**; live re-verification
and real-account validation are **recorded, non-CI** ([`03 §5.1`]). A test needing a live provider/real
credential never becomes a flaky universal CI gate.

**Deterministic acceptance tests (CI-blocking, called out):** the **Cartesian certification test**
(`P3c-CERT-007`, 18 combinations); the **six reservation no-leak/no-double-charge tests**
(`P3b-TEST-001`); the **Pro convergence test** (`P4-TEST-001`, N=2,000, ±5 pp per bucket); the **Max
quota-fairness/quality-band test** (`P4-TEST-001`); the **owner-auth negative suite** (`P2b-TEST-001`).

---

## 21. Implementation gates matrix (phase-gate)

Every gate names exact commands/suites, expected results, retained evidence, CI-blocking vs
manual-evidence, and who authorizes proceeding. A gate passes only through demonstrable behavior.

| Phase | Gate (pass condition) | Command / suite | Evidence retained | Blocking? | Authorizes next |
|---|---|---|---|---|---|
| P0 | boots; `/health` 200; one fake chat via Bifrost; migrations verified; clean shutdown | `go test ./...` + boot + smoke, Win+Linux | boot log, smoke, migration integrity | CI-blocking | P1 |
| P1 | encrypt/decrypt w/ bound AAD; rotation atomic; canary passes | `secrets`/`sanitize` suites + canary | crypto/rotation/canary reports | CI-blocking | P2a, P2b |
| P2a | DS `validate` 12/12; app consumes versioned package; inventory renders 3 themes × 2 densities; no UI before green | DS `npm run validate` + app inventory render + adherence check | DS report.md, per-theme renders, adherence log | CI-blocking (+DS report evidence) | P2b UI |
| P2b | owner-auth suite (setup/expiry/CSRF/reveal/lockout, no-secret); 2 real accounts connect w/ correct identity/funding/health; duplicates friendly | `P2b-TEST-001/002` (CI) + `P2b-TEST-003` fixtures (CI) + real-account recordings (evidence) | auth suite, fixture reports, connect recordings | CI-blocking (fixtures/auth); real-account = evidence | P3a, P3b |
| P3a | discovery w/ provenance; `/models` reflects catalog+cert; free never surfaces paid (fail-closed); no probes ran | `P3a-TEST-001` | discovery run, `/models` snapshot, free-safety test | CI-blocking | P3c |
| P3b | six no-leak tests + concurrency no-overcommit + unknown-still-reserves + window-key normalization + exhaustion isolation | `P3b-TEST-001` | six-test report, concurrency/normalization reports | CI-blocking | P3c, P4 |
| P3c | context/capabilities probed w/ provenance; offerings reach `certified`; Cartesian 18-combo passes; zero hardcoded model data | `P3c-CERT-007` + `P3c-TEST-001` | Cartesian report, probe provenance, no-hardcoding lint | CI-blocking | P4 |
| P4 | Lite 0 paid + fail-closed; Pro ±5 pp/N=2,000/band; Max quota-fair+band (not 50/50); fallback boundaries; stickiness safe; Bifrost no re-select | `P4-TEST-001/002` | Lite/Pro/Max scenario reports, fallback/stickiness reports | CI-blocking | P5 |
| P5 | real SDK chat+stream+tools+vision; extension clamp/gates/validation; usage+decisions recorded | `P5-TEST-001/002/003` (fake backends CI) + real-provider recording (evidence) | SDK E2E, extension suite, usage report, recording | CI-blocking (fake); real = evidence | P6 |
| P6 | owner operates everything implemented through P6 from dashboard+tray, no terminal (Backup/Restore E2E is a P8 gate item) | `P6-TEST-001/002` (a11y/visual/Playwright) | a11y/visual/flow reports, operate recording | CI-blocking (a11y/visual/flows); operate = evidence | P8 (release) |
| P7 | every integration connects/discovers/certifies ≥ chat; custom path onboards w/ no code change | `P7-TEST-001` fixtures (CI) + `P7-TEST-002` real-account (evidence) + `P7-PROV-011` re-verification | fixture reports, dated re-verification, connect recordings | CI-blocking (fixtures); live = evidence | P8 |
| P8 | clean-machine install/run/connect; load readiness (≥30 min/≥20 RPS/≥20 conc/≤0.5%/p95 reported/zero invariant violations); backup/restore round-trip + owner-password re-establish + post-restore verification, no plaintext, **operated end-to-end from the dashboard (`P8-UI-001`)** | `P8-TEST-001/002`, `P8-REL-004`, `P8-BKP-003`, `P8-UI-001` E2E | load report (numbers), backup/restore matrix, backup/restore UI E2E + a11y, install recording | CI-blocking (backup/invariants/UI E2E); load + install = evidence | **V1 release** |

"Who authorizes": each CI-blocking gate is machine-authorized (green suite); each manual-evidence item
(real-account, load, install recordings, live re-verification) is authorized by the operator/reviewer
recording dated evidence. **No gate passes because files exist.**

---

## 22. Risk register

| Risk ID | Description | Likelihood | Impact | Detection | Prevention | Containment | Recovery | Owner (WS) | Retired/reduced at |
|---|---|---|---|---|---|---|---|---|---|
| R-01 | Provider protocol drift (endpoints/scopes/headers change) | High | Med | Fixture tests + dated live re-verification | Typed adapter contract; live re-verification before implementing | Adapter suspended; other providers unaffected | Update adapter behind frozen contract | PROV | reduced P7 (ongoing re-verification) |
| R-02 | Undocumented OAuth behavior (refresh-family invalidation, Auth0 reuse) | Med | High | Reauth/interruption tests; multi-account tests | `prompt=login`; state-nonce verify; staging swap; single-use refresh rotation | Reauth guarded (`reauthentication_in_progress`); active credential intact | Re-enroll; identity-mismatch guard | PROV/SEC | reduced P2b, P7 |
| R-03 | Quota uncertainty (no provider endpoint) | High | Med | Quota-state tests; unknown≠unlimited | Mandatory local-safety budget; conservative estimates | Concurrency cap = 1 until confirmed | Re-baseline at next sync | QUOTA | reduced P3b |
| R-04 | Unknown consumption after dispatch (leak/double-charge) | Med | High | Six reservation tests | `reserved→reconciliation_pending`; never auto-release; idempotent | Headroom stays debited; janitor discriminated by `dispatched_at` | Reconciliation worker → settle/release/unknown_consumption | QUOTA | reduced P3b |
| R-05 | SQLite concurrency / overcommit | Med | High | Concurrency integration tests | `BEGIN IMMEDIATE` + per-window version; all-or-nothing | Whole-txn rollback; no partial debit | Retry from fresh snapshot | QUOTA/DB | reduced P3b |
| R-06 | Migration failure (checksum/integrity/OS line endings) | Low | High | Integrity check on open; up/down CI | Checksum guard; LF forced; rollback-tested | Fail-closed on open; no listener | Down path in dev; backup before destructive migration | DB | reduced P0, P8 |
| R-07 | Credential corruption / missing key | Low | High | Startup ciphertext reconciliation | Bound AAD; crash-safe rotation | Fail-closed startup | Restore from backup; rotation resume | SEC | reduced P1 |
| R-08 | Backup loss / passphrase loss | Med | High | Backup/restore matrix | Owner-warned; independent passphrase; owner_auth in container | Backup unrecoverable if passphrase lost (documented) | Local reset (login only); no credential recovery without keyring | BKP/SEC | reduced P8 |
| R-09 | Stale metadata / incorrect capability inference | Med | Med | Precedence + probe-integrity tests | Probe truth > metadata; exact-identity-match; free-safety fail-closed | Unknown ⇒ ineligible | Re-probe; recertification worker | CERT/DISC | reduced P3a, P3c |
| R-10 | Routing starvation / fairness drift | Med | Med | Pro convergence + Max DRR tests | Per-bucket deficit; DRR+P2C; competitive band never widened | Fail-open only if all saturated | Tune weights within bounds (policy, not architecture) | ROUTE | reduced P4 |
| R-11 | Hidden paid usage in Lite | Low | High | Lite zero-paid gate; free-safety fail-closed | Categorical funding gate; free-safety independent of enrichment | Lite fails closed (`venom_free_capacity_exhausted`/`no_eligible_offering`) | N/A (never occurs by construction) | ROUTE/DISC | reduced P3a, P4 |
| R-12 | Streaming partial failure / double response | Med | Med | First-byte boundary tests | Fallback only before first byte; partial→settle/reconcile | No second response after stream begins | Reconcile partial consumption | EXEC/ROUTE | reduced P4 |
| R-13 | Secret leakage (logs/errors/traces/audit/headers) | Med | High | Canary every build | `sanitize` boundary; hash-only keys; env-only secrets | Full [REDACTED]; fail-closed on uncertainty | Rotate; canary catches regressions | SEC | reduced P1 (ongoing) |
| R-14 | Diagnostics overexposure (route explain leaks internals) | Low | Med | Secret-free record tests; extension no-leak tests | Records store ids/codes/scores only; `X-Venom-*` sanitized | Owner-only diagnostics | Redact + re-test | OBS | reduced P4, P5 |
| R-15 | Design System / application drift | Low | Med | DS `validate` 12/12 in app CI; no-raw-values; no-package-copy | Package read-only; generated files never edited; tokens-only UI | App CI blocks a raw value or a component copy | Bump DS version via its own semver/codemod policy | DS/UI | reduced P2a (ongoing) |
| R-16 | Test flakiness (live-provider tests as CI gates) | Med | Med | CI stability tracking | Live tests never CI-blocking; fixtures deterministic offline | Quarantine flaky test; fixtures gate | Convert to fixture; record live as evidence | TEST | reduced P2b, P7 |
| R-17 | Windows packaging/runtime differences | Med | Med | Full suite on Win+Linux every phase | `CGO_ENABLED=0` pure-Go; LF checksums; per-OS path tests | Cross-platform gate blocks a Windows-only regression | Fix + re-run both OSes | FND/REL | reduced (every phase) |
| R-18 | Bifrost re-selection / second execution path | Low | High | No-reselect + single-engine tests | Pool-size-1/one-key; dispatch by typed capability; vet no-slug-switch | Single dispatcher; vendored core unmodified | Revert local mod (local-mod check) | EXEC | reduced P0, P4 |

---

## 23. Decision register & change policy

**23.1 Frozen product decisions (implementers may not re-open):**

| ID | Decision | Source |
|---|---|---|
| DEC-01 | Image generation deferred (recognized/certifiable; no V1 endpoint/tier/gate) | [`05 §9`](05-tier-engine.md#9-future-scope-non-v1) |
| DEC-02 | Single-owner authentication (one identity, no users/teams/roles/RBAC; local password) | [`09 §5`](09-control-api.md#5-owner-authentication-first-run-login-session-re-verification) |
| DEC-03 | Pro funding mix ~25% paid / ~75% free (overall tier traffic; deficit controller) | [`05 §8.1`](05-tier-engine.md#8-resolved-product-decisions) |
| DEC-04 | Max: no funding-mix target — quality-first → quota-fair DRR + P2C | [`05 §8.3`](05-tier-engine.md#8-resolved-product-decisions) |
| DEC-05 | Multi-axis account lifecycle (connection × health; derived display_status) | [`02 §3`](02-domain-model.md#account-lifecycle-multi-axis-state-model) |
| DEC-06 | Six certification states (no `rejected`) + capability truth dimension | [`04 §5`](04-model-intelligence.md#5-certification) |
| DEC-07 | Five reservation states (no stored `expired`) | [`02 §3`](02-domain-model.md#quota--the-multi-window-model-budgetssources--windows--reservations--allocations) |
| DEC-08 | One active credential per (account, kind); ≤ one staged per kind | [`02 §3`](02-domain-model.md#credential-encrypted-secret-for-an-account) |
| DEC-09 | Multi-window quota (windows per account; no single-row model) | [`02 §3`](02-domain-model.md#quota--the-multi-window-model-budgetssources--windows--reservations--allocations) |
| DEC-10 | Mandatory local-safety budget on every account | [`02 §3`](02-domain-model.md#local-routing-safety-budget-mandatory-for-every-account) |
| DEC-11 | Soft disconnect only (no hard delete/purge in V1) | [`02 §3`](02-domain-model.md#account-lifecycle-multi-axis-state-model) |
| DEC-12 | Tailwind theme output generated from tokens (never hand-authored) | [`07 §2.3`](07-design-system.md), [`Design_System handoff-contract.md`] |
| DEC-13 | Design System is a local, versioned package (`@venom/design-system@1.0.0`); read-only to the app | [`07 §9`](07-design-system.md), [`Design_System SKILL.md`] |
| DEC-14 | Offline / CDN-free runtime (fonts system-stack fallback; icons vendored) | [`Design_System handoff-contract.md`] |
| DEC-15 | Exact tier names/IDs: `venom/lite`, `venom/pro`, `venom/max` (context 256K/512K/1M; thinking none/extended/ultra) | [`05 §1`](05-tier-engine.md#1-the-three-tiers-authoritative-policy) |
| DEC-16 | Competitive band Pro ≤0.08 / Max ≤0.03 (0–1 quality scale; never auto-widen) | [`05 §8.5`](05-tier-engine.md#8-resolved-product-decisions) |
| DEC-17 | Step-5 scoring weights fixed for V1 (dashboard tuning deferred) | [`05 §8.4`](05-tier-engine.md#8-resolved-product-decisions) |
| DEC-18 | Bifrost Core candidate pin is `github.com/maximhq/bifrost/core@v1.7.3` at release commit `8f0ef396f589528210f6409383c90863bfa1e99f`; fetching and integration belong exclusively to `P0-EXEC-001` | [official release](https://github.com/maximhq/bifrost/releases/tag/core/v1.7.3) |
| DEC-DS-STATUS | `README`, `07`, `08`, and this plan now consistently record the gate-passed `Design_System/` package as the current implementation | this plan §1 |
| DEC-DISC-LAYERING | P2b proves adapter discovery at fixture level; P3a owns the discovery pipeline + free-safety persistence (§1) | this plan §1 |

**23.2 Change classification (what a change requires):**

| Change type | Requires |
|---|---|
| Scoring weights / band widths / mix target / deficit tuning | **Policy tuning only** — typed bounded config; a Phase-4 gate re-run. No architecture change ([`05 §8.4/§8.5`]). |
| New/changed table, column, or index | **Migration** (new serialized M-group; up/down; integrity) + schema-lint. |
| New provider adapter | **No architecture change** — implement behind the frozen `PROV-001` contract + fixtures + re-verification ([`08 §8`]). |
| New operation (embeddings/audio) | **Architecture-adjacent** — extend operation enum + certification fixtures + effective projection; no per-model logic ([`08 §8`]). |
| New tier / status | Typed policy change + a token (`tier.*`/`status.*`) — renders via the single path ([`08 §8`]). |
| New control/public endpoint | **Architecture review** — must fit the envelope, auth/CSRF, jobs surface, redaction, audit ([`09`]). |
| Any Design System token/component change | **Design System version change** — its own semver/changelog/codemod, then re-pin in the app ([`07 §8`], [`Design_System handoff-contract.md`]). Never edit the package from the app. |
| Reopening a frozen product decision (§23.1) | **Product-owner decision** — not an implementer choice; then an implementation-plan amendment. |
| Adding a phase / reordering roadmap phases | **Implementation-plan amendment** + product-owner sign-off (roadmap identity is preserved). |
| Image generation / future scope | **Product-owner decision** + a new plan section specifying endpoint, contract, transport, discovery, certification, quota, fallback, tier policy, diagnostics, UI, phase, gate ([`05 §9`]). |

---

## 24. Release & packaging plan (consolidated)

Detailed tasks in P8. Summary: reproducible **signed single static binary** per platform with the
dashboard embedded via `go:embed` (`P8-REL-001`); fail-closed first-run + readiness/liveness/graceful
shutdown/crash recovery (`P8-REL-002`); **portable encrypted backup/restore** — single AEAD container,
Argon2id KDF, wrapped data key (never the raw master key), manifest, atomic swap with rollback, owner
password re-established on restore (`P8-BKP-001/002/003`, `P8-CAPI-001`), operated end-to-end from
the dashboard (`P8-UI-001`); **backup before destructive
migration** (`P8-REL-005`); optional non-loopback **inference** bind with the control plane
loopback-only (`P8-REL-003`); sustained-load readiness (`P8-REL-004`); release verification + rollback/
forward-fix + versioning + notes (`P8-REL-005`); operational runbooks (`P8-OPS-001`). **The local/private
product model is preserved — no cloud deployment requirement is introduced** (none exists in the approved
planning); the same binary serves tray-desktop and headless `serve`.

---

## 25. Plan self-audit (read-only adversarial review)

Performed after drafting, against the approved sources. Verified planning defects were fixed inline;
wording was not polished indefinitely.

| Check | Result |
|---|---|
| Missing requirements | None. Every [`10`] row + README principle maps to ≥1 task (Appendix B). |
| Circular dependencies | None. The phase graph (§7) and Appendix A are a DAG; spot-checked P0→P8 has no back-edge. |
| Tasks with no gate | None. Every task's `Evidence` rolls into a phase gate (§21); the phase-gate matrix has no blank cell. |
| Gates with no executable evidence | None. Every gate names commands/suites + retained artifacts; manual-evidence items are labeled. |
| Schema created after its consumers | None. §14 proves M1–M8 precede their consumers (auth→mutation; accounts→enrollment; offerings/quota→routing; keys→public API; backup last). |
| UI planned before its API | None. Every UI task lists a stable backend contract precondition; §19 enumerates them; no `UI-*` before P2a. |
| Security checks after sensitive endpoints | None. §15 proves keyring/canary (P1) precede credentials; auth+CSRF (P2b-SEC/CAPI-001) precede every mutation; reveal gated on re-verify. |
| Workers without recovery | None. Jobs/janitor/reconciliation carry leases + crash recovery (`JOBS-001`, `QUOTA-006/007`, `P3b-JOBS-001`, `P3c-JOBS-001`). |
| Migrations without rollback/forward-fix | None. Every M-group has a tested down path in dev; forward-only in prod; backup-before-destructive-migration (`P8-REL-005`). |
| Provider assumptions without evidence | None. §16 tags confidence; live re-verification (`P7-PROV-011`) is required; no fabricated facts; unknown funding stays ineligible. |
| Tasks too large to review | None found. Each task is one reviewable change; multi-state-machine work is split (e.g. QUOTA reservation vs janitor vs reconciliation are distinct tasks). |
| Duplicate responsibility | None. Single dispatcher (`EXEC`), single error envelope, single effective-offering projection, single `X-Venom-*` builder, single client-config generator per shape, single jobs surface. |
| Unsupported product scope | None. No teams/orgs/RBAC/billing/marketplace/image-UI/mandatory-TopologyGraph/hard-delete introduced; all excluded per the mission + [`05 §9`]. |
| Design System duplication | None. The package is consumed (`file:`/handoff copy), never copied or edited; a no-package-copy check (`DS-005`) enforces it. |
| Release work without operational verification | None. P8 includes clean-machine install, load readiness, backup/restore matrix, and operational docs. |

**Fixes applied during audit:** (a) explicitly labeled the P2b/P3a discovery layering as a decision
(`DEC-DISC-LAYERING`) rather than leaving an apparent overlap; (b) made `P2a` entry note the DS tasks
have no hard dependency on P1 (parallelization) while keeping the gate order; (c) pinned that the
`ui_kits/venom-console` surfaces are references, not production code, in both §13 and §19.

**Errata (closed after the final independent audit, 2026-07-22 — documentation-only):**
(1) Appendix A `P3c-TEST-001` deps fully qualified to `P3a-DISC-003,004`; (2) seven contradictory
`parallel_safe_with` annotations removed where a hard dependency exists between the pair
(`P3a-DISC-002`, `P3b-QUOTA-001`, `P3c-CERT-001`, `P3c-QUOTA-001`, `P4-ROUTE-005/006/007`) — hard
dependencies are authoritative; (3) migration parallelism clarified (§8/§14 + Appendix A): design/
preparation/isolated testing parallel, numbering/landing/application/integration serialized;
(4) Backup/Restore removed from the P6 gate and `P6-TEST-002` (its endpoints land in P8) and placed
end-to-end in the P8 gate; (5) production Backup/Restore UI ownership assigned to the new
`P8-UI-001` (task count 177 → 178, `P8` 13 → 14); (6) `HLTH` clarified as a cross-cutting
workstream implemented via `P2b-DOM-001`/`P2b-CAPI-004`/`P3b-QUOTA-008`/`P4-ROUTE-014`/`P6-UI-007`;
(7) `P0-ENV-001` added as the environment-bootstrap unit (task count 178 → **179**, `P0` 14 → 15,
workstreams 21 → 22), with exact tool-version pins and a fresh-shell PATH resolution gate.

---

*End of implementation plan. Machine-readable companions:*
[Appendix A — task dependency matrix](11-appendix-A-task-dependency-matrix.md) ·
[Appendix B — requirement traceability](11-appendix-B-requirement-traceability.md).



