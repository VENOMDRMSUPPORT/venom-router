# Venom Router — Greenfield Foundation Spec

> A private, single-owner AI gateway that unifies many LLM provider accounts behind
> **three** production model tiers — `venom/lite`, `venom/pro`, `venom/max` — and always
> routes each request to the best available model, dynamically, with zero hardcoding.

**Status:** Greenfield planning. This repository is being (re)built from scratch. An older
implementation exists (`github.com/VENOMDRMSUPPORT/venom-router`) but it is **NOT** an
architectural source of truth — see [How to treat the old repo](#how-to-treat-the-old-repo).

---

## 1. What Venom Router is

The owner holds **many** LLM subscriptions and **multiple accounts per provider**. Some accounts
get strong models at zero marginal cost; others get free tiers on strong models where holding
several accounts multiplies the usable quota. Venom Router pools all of that behind
one clean, OpenAI-compatible surface and exposes only three products:

| Product | Backend quality target | Context | Thinking policy |
|---|---|---|---|
| **`venom/lite`** | 100% free accounts only — no PAID offering ever enters | 256K | none (no extended thinking) |
| **`venom/pro`**  | mostly free + some paid | 512K | extended thinking, ~25% paid / ~75% free |
| **`venom/max`**  | best available, balanced | 1M | "ultra" thinking, **no funding-mix target** — quality-first, then quota-fair |

All three are **multimodal and expose the same V1 capability surface** (chat, streaming, tools,
structured output, vision input, reasoning). Tiers differ **only** in
quality, context ceiling, thinking budget, and the free-vs-paid backend policy (Lite: free only;
Pro: a ~25/75 paid/free target; Max: **no funding-mix target** — quality-first, then quota-fair) —
never in which capabilities are available. **Image generation is future scope**, not part of the V1 guaranteed
surface; image-only models are cataloged but never enter a tier (see
[tier engine](docs/05-tier-engine.md) §1 and the future-scope note in §9).

It is a **personal** system: no external end-users. But per-account **consumption / quota
accounting** is a first-class concern, because free quota is the scarce resource being pooled.

---

## 2. Non-negotiable principles

These are invariants. Every design decision below serves them. Violating one is a bug.

1. **Zero hardcoding of models or capabilities.** No model name, context window, capability
   flag, or price is ever compiled in. Everything is discovered, probed, and certified at
   runtime per account, then stored with provenance and kept fresh. A model name may pick a
   logo — never a behavior.
2. **Free / Paid is a property of the _account_ and the _offering_, never the provider.** The
   same provider can hold a free account and a paid account at the same time; the same model
   can carry different terms on different accounts. "Free provider" is a category error and
   must not exist anywhere in the code, schema, or UI.
3. **Everything is account-scoped.** Discovery, quota, health, and certification are facts
   about a `(provider, account, …)` tuple, because entitlements depend on the seat/plan/
   subscription behind that specific account.
4. **The routable, certifiable unit is the offering-operation `(provider, account, model, operation)`.**
   Certification is per-operation: passing chat does not certify tools, vision, or streaming.
5. **Single owner, local trust — but authenticated.** One process, one node, one SQLite writer,
   loopback by default. There is exactly **one owner identity** (a local password, no users/teams/
   roles/RBAC). The owner sets a password on first run; the control plane requires an authenticated,
   opaque server-side session (HttpOnly SameSite=Strict cookie + session-bound CSRF), and sensitive
   operations require password re-verification. **Loopback and Host-header protections are mandatory
   but do not replace authentication.** Inference still requires a Venom API key. (See
   [domain model](docs/02-domain-model.md) §3 and [control API](docs/09-control-api.md) §5.)
6. **Venom decides; the transport executes.** Venom pre-selects the exact route and hands the
   chosen transport one account / one key / one model. The embedded Bifrost core is the primary
   transport for OpenAI-compatible API-key providers; OAuth and non-OpenAI providers use
   Venom-owned native transports. **No transport ever independently picks a provider or account.**
   (See [architecture](docs/01-architecture.md) §4.)
7. **Fail closed.** Unknown ⇒ ineligible. `venom/lite` never silently falls back to a paid or
   unknown-cost account, even under exhaustion — it returns a structured error instead.
8. **Secrets are sacred.** Provider credentials are encrypted at rest with the key stored
   outside the database; Venom API keys are stored hash-only. Credentials, tokens, OAuth
   `code`/`state`/PKCE verifiers, and `Authorization` headers are never logged.
9. **SQLite is the source of truth.** All state flows through typed repositories. No ad-hoc SQL
   from handlers; no second authoritative catalog inside Bifrost.
10. **One design system, no drift.** Every dashboard surface is built from shared tokens and a
    fixed component inventory; no hardcoded colors, spacings, or one-off widgets. The look is
    multi-theme, accessible (WCAG AA), and enterprise-grade by construction. (See
    [design system](docs/07-design-system.md).)

---

## 3. Glossary (the system vocabulary)

Use these terms precisely and consistently across code, schema, API, and UI.

| Term | Meaning |
|---|---|
| **Provider** | An *integration definition* — a vendor Venom knows how to talk to (e.g. `opencode-zen`, `antigravity`, `claude-code`). Identified by a slug. Carries an auth mode, capability list, and a *default* funding policy. **Not** an account. |
| **Integration** | A provider's connection recipe. Two families: **Built-in / Ready** (`OAuth`, `API Key`) and **Custom** (`OpenAI-Compatible`). Every built-in provider has exactly one auth mode. "All Integrations" = the full catalog Venom ships. |
| **Account** | A *connected instance* of a provider — one login / one API key. Identity = `(provider_id, external_id)` where `external_id` is the provider's immutable ID when available. **Multiple accounts per provider are normal.** Holds encrypted credentials, an operational state, funding evidence, quota, and health — all independent per account. |
| **Credential** | The encrypted secret behind an account (API key, or OAuth access/refresh token). Canonical invariant: **one active credential per `(account_id, credential_kind)`** — an account may hold several active credentials of *different* kinds at once (e.g. a GitHub OAuth credential **and** a Copilot service credential), but never two active credentials of the same kind. |
| **Offering** | An account's exposure of a model. Identity = **`(account_id, provider_model_id)`**. The same model on two accounts is two distinct offerings. All Offerings under an account inherit its funding classification. No per-Offering funding override in v1. |
| **Offering-operation** | An offering narrowed to one operation = **`(provider, account, model, operation)`** — the only routable + certifiable unit. Chat vs. vision on one offering are two offering-operations, certified independently. |
| **Operation** | A specific capability/endpoint: chat, streaming, tools, structured output, vision input, plus the context-length contract. Certification is per-operation. `image_generation` is a recognized operation **reserved for future scope** — discoverable/certifiable but not part of the V1 tier surface. |
|| **Canonical model** | Venom's stable identity for a discovered model, **provider-scoped** (SHA-256 of `(provider_id, provider_model_id)`). Two providers with the same model name produce two distinct canonical models. Cross-provider equivalence is **not** supported in v1. |
| **Funding** | An account fact (`free` / `paid` / `unknown`), inherited by all Offerings under that account. No per-Offering funding override in v1. `free` = verified zero marginal cost; `paid` = known cost; `unknown` = excluded from production routing until classified. Backed by an append-only evidence trail whose every row carries a `source` ∈ `{provider_policy, provider_evidence, owner_policy, owner_override}` (see [domain model](docs/02-domain-model.md) §2). A plan label, a `:free` suffix, or a missing price never *proves* free. |
| **Tier / Policy** | One of the three public products (`venom/lite\|pro\|max`). A tier *is* a policy: hard eligibility gates + ranking weights + a per-tier free/paid distribution policy (Lite: free only; Pro: ~25/75 mix target; Max: no funding-mix target, quota-fair) + fallback rules. |
| **Quota** | Provider-native usage headroom for an account, modeled as **multiple concurrent windows** (e.g. 5-hour + 7-day usage, RPM + TPM, balance), each with used/remaining/reserved/reset/confidence/observed-at. "Free" ≠ unlimited, and **unknown ≠ unlimited**. |
| **Local safety budget** | A mandatory, owner-policy routing-safety budget every account carries — at least a concurrent-in-flight cap and an estimated-consumption cap — so an account with **no provider quota endpoint** is still bounded. Distinct from (never confused with) provider quota evidence; every execution reserves against it. (See [domain model](docs/02-domain-model.md) §3.) |
| **Certification** | A revocable state proving an offering-operation actually works, established once by observation/probing and re-checked on drift. Canonical states: `discovered → observed → probing → certified`; `certified ⇄ suspended`; `probing → suspended → probing` (terminal/exhausted probe failure and recovery); `certified → expired → probing`. **There is no `rejected` state.** Probe outcomes are recorded separately as **capability truth** (`unknown`/`supported`/`unsupported`); routable requires state `certified` **and** truth `supported`. (Authoritative machine: [04 §5](docs/04-model-intelligence.md).) |
| **Probe** | A bounded runtime request that produces evidence (e.g. an oversized request to read the real context limit out of the rejection error). |
| **Evidence** | Every resolved fact carries value + source + confidence + observed/expiry + exact-identity-match. Precedence: `owner override > Venom probe > provider-native metadata > provider discovery > external registry > heuristic > unknown`. |
| **Design token** | The single source of truth for one visual decision (a color, spacing, radius, type step, elevation, motion value). Every UI value resolves to a token; a raw hex or pixel literal in a component is a bug. (See [07-design-system](docs/07-design-system.md).) |
| **Venom API key** | The `vk_live_*` key clients present to Venom. Shown once at creation; only a hash/verifier is stored. Distinct from provider credentials. |

---

## 4. Document index

Read in order. Each doc is self-contained but assumes the principles above.

1. **[01-architecture.md](docs/01-architecture.md)** — process model, components, tech stack, the Bifrost boundary, SQLite, dashboard, security.
2. **[02-domain-model.md](docs/02-domain-model.md)** — the Provider / Account / Offering / Tier model, the integration taxonomy, free/paid semantics, and the SQLite schema sketch.
3. **[03-provider-integration-catalog.md](docs/03-provider-integration-catalog.md)** — the adapter interfaces, the enrollment flows, and the **11 proven integrations** with exact wire contracts. *This is the one asset carried over from the old build.*
4. **[04-model-intelligence.md](docs/04-model-intelligence.md)** — account-scoped discovery, dynamic capability/context probing, certification, and the effective-offering read model. *No hardcoding, ever.*
5. **[05-tier-engine.md](docs/05-tier-engine.md)** — the `lite/pro/max` policies, per-request offering selection, per-tier free/paid distribution (Pro mix target; Max quota-fair), fallback/cooldown, and quota accounting.
6. **[06-roadmap.md](docs/06-roadmap.md)** — the phased greenfield build plan with gates.
7. **[07-design-system.md](docs/07-design-system.md)** — the authoritative requirements and acceptance contract for the implemented Venom Design System (token architecture, multi-theme contract, component inventory, accessibility/density rules, drift-prevention guardrails). The versioned package lives in `Design_System/`.
8. **[08-engineering-standards.md](docs/08-engineering-standards.md)** — how the project is built so it stays professional and drift-free: layering rules, definition of done, testing gates, extension points, and the change checklist.
9. **[09-control-api.md](docs/09-control-api.md)** — the owner/control-plane API contracts (planning-level, language-neutral): enrollment, OAuth, reauthentication, discovery, health/quota sync, probes, certification, diagnostics, settings, backup/restore.
10. **[10-requirements-coverage.md](docs/10-requirements-coverage.md)** — the requirement → subsystem → roadmap-phase → deliverable → acceptance-gate coverage matrix.
11. **[11-implementation-plan.md](docs/11-implementation-plan.md)** — the authoritative execution order, task boundaries, dependencies, evidence, and phase gates. Its two appendices carry the machine-readable task graph and requirement traceability.

> **Design System status:** The Design System is implemented and approved in `Design_System/` as
> `@venom/design-system@1.0.0`. Application consumption begins in Phase P2a. Do not rebuild or
> modify the package during P0 or P1.


---

## 5. How to treat the old repo

`github.com/VENOMDRMSUPPORT/venom-router` is a **behavioral reference for one thing only:
how each provider is correctly connected** (the "All Integrations" wire logic — auth,
identity, model discovery, quota endpoints). Doc 03 distills that and is the only place the
old repo is trusted.

Everything else in the old repo — its routing engine (three disagreeing generations), its
certification code, its schema, its tests, its abstractions — is considered unreliable and is
**not** a source of truth. Its own audit lists 75+ correctness issues. We keep the proven
provider wire contracts and the user-facing enrollment experience (the Provider Fleet UI); we
rebuild the internals clean around Go interfaces, SQLite, embedded Bifrost, account-scoped
discovery, and certified offerings.

**Anti-patterns from the old build to consciously avoid** (its audit found all of these):
catalog rows marked "certified" with unknown facts; `venom/lite` allowing paid/cheap routes or
an 8K context floor; branching on provider slug strings instead of typed policy; a hardcoded
OAuth client secret committed to source; OAuth callbacks not verifying the `state` nonce;
non-atomic quota checks that let concurrent requests overcommit; traces that stored raw errors
and account identifiers; three duplicated execution engines that drifted apart; silently
swallowed usage/billing writes.
