# 04 — Model Intelligence (discovery, probing, certification)

How Venom learns what every model can actually do — **without hardcoding a single model name,
context window, or capability**. This is the pipeline that turns "an account exists" into "these
certified offerings are safe to route."

Guiding rule, everywhere: **absent ≠ false, absent ≠ zero. Unknown stays unknown.** A nullable
number that we haven't learned is `NULL`, not `0`. A capability we haven't verified is not
`false` — it's unproven, and unproven is ineligible for tiers.

---

## 1. Account-scoped discovery

Discovery runs **per account**, never per provider. Which models an account can use depends on
its seat/plan/subscription/project — two accounts of the same provider can see different models.

Flow per discovery run:
1. Validate the account is in a usable state; take the provider from the trusted account record
   (never client-supplied).
2. Allocate a monotonic **generation** and a run row.
3. Lease the account's credential inside a callback scope (plaintext never lands in a long-lived
   field), call the provider's `DiscoverModels`.
4. Normalize + validate every model (bounds, UTF-8, control chars; cap the count), compute the
   canonical key, sanitize evidence.
5. Apply the snapshot atomically **only if still the newest generation** (a slower older run is
   marked superseded). On failure, keep the previous snapshot intact and record a safe error code.

Semantics:
- An **explicit empty list is authoritative** → withdraw prior offerings for that account.
- A missing/malformed/truncated response is a **failure**, not "zero models" → keep last-known-good.
- **Free-account filtering (routing-critical free-safety):** when an account is `free`, discovery
  must **never** surface a paid model. This resolution is **deterministic, fail-closed,
  provenance-aware, and independent of the optional metadata-enrichment toggle** (§2 source 3):
  - If the provider exposes an authenticated per-model price, a model with verified
    `cost.input == 0 && cost.output == 0 && status != "deprecated"` is `free-safe`.
  - Where the provider's list carries **no per-model price** (e.g. OpenCode Zen), zero-cost is
    resolved against a **cost/entitlement dataset** (`models.dev`) consulted **at discovery time**.
    This free-safety lookup is **always available to discovery** — it is *not* gated by the optional
    enrichment feature — cached with a TTL (~10 min) plus a last-known-good snapshot.
  - **Fail closed:** a model whose zero-cost cannot be verified (no provider price **and** no
    dataset hit, or the dataset is unavailable with no fresh cached entry) is classified `unknown`
    for that account and **withdrawn from the free account's offerings** — never surfaced as free.
  - The model list itself is **never** hardcoded; only the cost fact is looked up, and it is stored
    with provenance. The provider catalog alone is not an entitlement statement.
  The full contract (cache/TTL, staleness, provenance, provider handling, per-tier behavior) is in
  **§2b**.

---

## 2. Capability & context detection (dynamic, three sources)

Every important field is a **provenance-wrapped value**, not a bare scalar:

```
Fact<T> = { value: T | null, source, confidence: 0..1, observed_at, expires_at?, exact_identity_match }
```

Capabilities tracked (per operation): `chat, streaming, tools, structured_output, vision,
image_generation, reasoning/thinking, coding, embeddings, audio`, plus `context_window,
max_input_tokens, max_output_tokens, input_modalities, output_modalities`.

Facts come from three complementary sources, merged by precedence (§4):

1. **Provider discovery / metadata** — only fields the provider *explicitly* returns become
   facts. E.g. Antigravity `supportsImages → vision`, `supportsThinking → thinking`;
   `maxTokens → context_length`. Never infer a capability from the model's name or ID. A
   zero/negative declared limit fails the record rather than being stored.

2. **Runtime probing** — empirical verification where declarations are missing or suspect:
   - **Context-window probe:** send one deliberately oversized request (e.g. ~3,000,000 tokens,
     `max_tokens: 8`) and **read the real limit out of the provider's rejection error**
     (structured field → OpenAI "maximum context length is N tokens" gated on
     `context_length_exceeded` → provider-specific regex → generic number-near-keyword →
     otherwise `no_signal`, never a guess). One request per model per attempt; 7-day cooldown;
     per-provider circuit breaker; concurrency caps; account pre-check. Evidence redacted to a
     short snippet with credential shapes stripped.
   - **Capability probes:** tiny requests that exercise tools / structured output / vision on the
     specific offering-operation.
   - **Probe outcome taxonomy (separates capability truth from probe execution):**

     | Layer | Concept | Values |
     |---|---|---|
     | **Capability truth** | Whether the offering-operation actually supports the capability | `unknown`, `supported`, `unsupported` |
     | **Probe execution** | What happened when we tried to test it | `pending`, `running`, `succeeded`, `inconclusive`, `retryable_failure`, `terminal_failure` |

     **Rules:**
     - A capability probe that returns a genuine capability response → **supported**.
     - A reliable semantic rejection from the provider proving the capability is absent → **unsupported**.
     - HTTP 429, timeout, network error, or 5xx → **unknown** (capability truth unchanged), with probe
       execution set to `retryable_failure` and the probe rescheduled.
     - HTTP 401/403 → **credential/account issue**, not a capability judgment. The probe execution is
       `terminal_failure` for that offering, but the capability truth remains `unknown` and is re-evaluated
       once credentials are refreshed.
     - A malformed probe request → `inconclusive`; the probe tooling is fixed and re-run.
     - `terminal_failure` means the probe cannot succeed for this offering-operation in its current
       state, but does **not** set the capability to `unsupported`.
   - **Hard rule:** a quota or rate-limit failure during a probe **must never flip a capability to
     `false`.** Only a genuine "unsupported" semantic response may. Probe failures for
     infrastructure reasons leave the fact unknown and reschedule.

3. **External metadata enrichment (optional, owner-enabled, off by default)** — background
   enrichment of **non-routing-critical** facts from public registries (`models.dev` for
   identity/context/capabilities/pricing; an analysis leaderboard for quality signals). Never in
   the request hot path; never sends prompts or credentials upstream; last-successful snapshot kept
   on failure. External data is the **weakest** source and can only *enable* a hard capability after
   exact identity mapping + schema validation — a name/family match stays soft evidence. **This
   optional pipeline is distinct from the routing-critical free-safety lookup in §1/§2b; disabling
   it never weakens free-safety.**

---

## 2b. Free-safety resolution vs. metadata enrichment (two separate pipelines)

Two pipelines read external cost/metadata; they have **different criticality and different
availability rules**. They must never be conflated.

| | **A. Free-safety resolution** | **B. Metadata enrichment** |
|---|---|---|
| Purpose | Prove an offering is zero-cost so a `free` account (and `venom/lite`) can never touch paid capacity. | Enrich non-routing-critical facts (context, family, release date, quality hints). |
| Criticality | **Routing-critical.** Gates Lite eligibility and free-account discovery. | Cosmetic / ranking-assisting only. |
| Default state | **Always on** for free-account discovery — not a toggle. | **Off by default**; owner-enabled. |
| Source | Provider's authenticated per-model price first; else the `models.dev` cost dataset. | `models.dev` + analysis leaderboard. |
| On source unavailable | **Fail closed** — unverifiable ⇒ `unknown` ⇒ excluded from free routing. | Skip enrichment; keep last-known-good; no routing impact. |
| Precedence | Provider price > cost dataset; owner override wins over both. | Weakest source (§4); can only *enable* after exact identity match. |

**`models.dev` usage:** consumed by **both** pipelines, but as **separate reads with separate
provenance**. A/A free-safety reads only the cost fact and is available to discovery regardless of
the enrichment toggle; B/enrichment reads the broader metadata and only when enabled. Disabling B
never disables A.

**Cache / TTL / staleness (both):** responses cached with a ~10-minute TTL and a persisted
last-known-good snapshot. For free-safety, a cost fact is usable while fresh **or** while within a
bounded staleness window (owner-configurable, default 24 h) with a `stale` provenance flag; beyond
that window, or with no cached entry, the model resolves to `unknown` (fail closed). Enrichment has
no staleness gate — stale enrichment is simply not refreshed.

**Provenance storage:** every resolved cost/metadata fact stores `source` (`provider_price` |
`models_dev` | `owner_override`), dataset `version`/snapshot id, `observed_at`, `confidence`, and
`exact_identity_match`. Free-safety facts require `exact_identity_match = true`; a family/name match
is never sufficient to prove `free`.

**OpenCode Zen and similar (price-less providers):** the provider list has no per-model price, so
free-safety comes entirely from the `models.dev` cost dataset at discovery; only verified zero-cost
models become offerings on a free account. If the dataset can't confirm zero cost, the model is
withdrawn from that free account — the provider catalog is never treated as an entitlement.

**Per-tier behavior by funding evidence:** (an account of an `evidence_required` provider —
[02 §2](02-domain-model.md#2-free--paid--the-semantics-precisely) — starts in the `unknown` row
below and moves only when authenticated provider evidence or an allowed owner override establishes
`free`/`paid`; the initial `unknown` stamp is a policy fact, never fabricated provider evidence)

| Funding evidence for the offering | `venom/lite` | `venom/pro` | `venom/max` |
|---|---|---|---|
| **known free** (verified zero-cost) | eligible | eligible | eligible |
| **known paid** | **never eligible** (categorically excluded) | eligible | eligible |
| **unknown** | never eligible | never eligible | never eligible |
| **stale free** (within staleness window) | eligible, flagged `stale`, background refresh | eligible, score penalty | eligible, score penalty |
| **stale beyond window / no data** | resolves to `unknown` → excluded | `unknown` → excluded | `unknown` → excluded |
| **conflicting** (provider says free, dataset says paid, or vice-versa) | **fail closed → treat as paid/unknown → excluded from Lite** | resolve by precedence (owner override > provider price > dataset); if unresolved → `unknown` → excluded | same as Pro |

This preserves the two non-negotiables simultaneously: **Lite never consumes paid capacity**, and
**no model list or commercial status is ever hardcoded.**

---

## 3. Canonical facts vs. effective offering

Three layers, kept distinct:

- **Canonical model** — provider-scoped identity + native facts ("what the model *is*": native
  context (`native_context_tokens`), native modalities, and optionally a one-time quality rating).
  The identity key = `SHA-256(provider_id, provider_model_id)`, making each canonical model unique
  to its provider. Two providers exposing the same model name produce two separate canonical models.
- **Offering** — a provider account's *exposure* of a model ("what this account can do with it"):
  the provider model ID, provider-specific caps/restrictions, provider-declared pricing,
  lifecycle, and per-operation certification. Offering identity = `(account_id, provider_model_id)`.
  All Offerings under an account inherit the account's funding classification.
- **Account route facts** — the *operational* layer: funding classification, health, quota,
  cooldown, latency, reliability, credentials.

Resolution rules:
- **Effective context** — determined by combining the native canonical fact with the provider cap:
  - If both `native_context_tokens` and the provider's cap are known → `min(native, provider_cap)`.
  - If only one is known → that value, with provenance indicating which source was used.
  - If neither is known → `NULL` (unknown). **Unknown context is ineligible for all tiers** (fail closed).
  A provider cap never overwrites the native canonical value; it only narrows it.
- **Effective capability** = native support AND provider exposure AND adapter transport support
  (an intersection). A provider restriction affects only that offering, never the canonical model
  or sibling offerings.
- **Quality rating** — a 0–100 scalar on the canonical model, always anchored to a documented
  source (benchmark version, probe run, or owner override), observed date, and confidence value.
  `NULL` means "no quality signal available" — the model remains routable but receives a neutral
  (0.5) ranking score. Quality is never a hard gate.

The rest of the system (the `/models` endpoint, routing, tier status, diagnostics) reads **one
shared "effective offering" projection** so the dashboard and the router can never disagree. No
consumer re-derives context, capabilities, quality, or eligibility on its own.

---

## 4. Evidence precedence

When sources disagree about a field, resolve by this order (higher wins):

```
owner override
  > verified Venom probe (proven positive/negative for this exact offering)
  > provider-native metadata (authenticated, account-specific)
  > provider discovery / catalog
  > external registry (models.dev / analysis)
  > heuristic
  > unknown
```

Plus: **owner overrides are never auto-overwritten**; a **proven narrower restriction beats a
broader positive claim** (an external "supports 1M context" can't override a probed provider cap);
**proven negative evidence wins** for the same field/scope until revalidated; heuristics may
*schedule* a probe but can never certify eligibility. Ties break by verification status
(verified > observed > declared) → confidence → freshness → scope specificity.

---

## 5. Certification

**Certification = proof, by observation, that a specific offering-operation actually works** —
distinct from account health. It is **per operation** and **revocable**.

Two separable certifications:
- **Canonical quality** — one reproducible rating per exact model version (never re-run per
  provider). Optional for routing; if present it enriches ranking, if absent quality stays `NULL`
  (never invented). May come from a public benchmark (exact match, with a versioned calibration
  to a 0–100 scale) or a small deterministic internal suite; provider latency/availability never
  contaminate the quality score.
- **Offering-operation capability** — deterministic proof, with fixed tiny fixtures, that this
  offering exposes each operation (chat, streaming, tools, structured_output, vision, context_window).
  Chat success does **not** certify tools. (`image_generation` is a recognized operation
  **reserved for future scope** — certifiable when the feature lands, but not routed by any V1 tier;
  see [05 §9](05-tier-engine.md#9-future-scope-non-v1).)

Lifecycle of an offering-operation's certification (**the canonical state machine** — every other
document defers to this section):

```
                         ┌──────────────────────────────────┐
                         │         discovered               │
                         └────────────┬─────────────────────┘
                                      │
                                      ▼
                              ┌───────────────┐
                              │    observed    │
                              └───────┬───────┘
                                      │
                                      ▼
              (re-probe)      ┌───────────────┐   (probing ⇄ suspended:
           ┌─────────────────▶│    probing    │◀── terminal/exhausted probe
           │                  └───────┬───────┘    failure and recovery)
           │                         ╱ ╲                    │
           │                        ╱   ╲                   │
           │                       ▼     ▼                  │
           │              ┌──────────┐ ┌──────────┐         │
           │              │certified │⇄│suspended │─────────┘
           │              └────┬─────┘ └──────────┘
           │                   │
           │                   ▼
           │              ┌──────────┐
           └──────────────│ expired  │
                          └──────────┘
```

States: `discovered`, `observed`, `probing`, `certified`, `suspended`, `expired` — **exactly six.
There is no `rejected` state.** A transient, terminal, or unsupported probe outcome never creates a
generic "rejected" certification state; outcomes are represented through **capability truth** and
the probe-execution classification (§2).

**Capability truth** (unknown/supported/unsupported) is a separate dimension from the
certification state. The certification state tracks administrative lifecycle, while the capability
truth tracks what the probe proved.

- **Probe outcome → effect on capability truth:**
  - `succeeded` (genuine capability response) → `supported`
  - semantic `unsupported` response (reliable proof of absence) → `unsupported`
  - `retryable_failure` (429/timeout/5xx/network) → capability stays `unknown`, probe retried
  - `terminal_failure` (401/403/protocol) → capability stays `unknown`, probe paused until the
    blocking condition changes
  - `inconclusive` (malformed probe) → capability stays `unknown`, probe tooling fixed

**Legal certification transitions** (each emits an `audit_event`; owner: `intelligence`, persisted
by `storage`):

| # | Transition | Trigger |
|---|---|---|
| 1 | `discovered → observed` | First concrete evidence for the offering-operation recorded (discovery snapshot / provider metadata). |
| 2 | `observed → probing` | Probe scheduled/started for the operation. |
| 3 | `probing → probing` (stay) | `retryable_failure` or `inconclusive` within the bounded per-cycle retry budget (owner-policy default: 3 attempts, exponential backoff). Capability truth stays `unknown`. |
| 4 | `probing → certified` | The probe produced a **definitive verdict** — capability truth resolved to `supported` **or** `unsupported`. `certified` means "verdict established by observation"; routability additionally requires truth = `supported` (below). |
| 5 | `probing → suspended` | `terminal_failure` (credential/protocol block) **or** the per-cycle retry budget exhausted. Reason-coded (`credential_blocked`, `probe_retry_budget_exhausted`, …); capability truth stays `unknown`. |
| 6 | `certified → suspended` | Invalid credentials, hard quota exhaustion, repeated protocol failure, provider removal, or a verified capability contradiction. |
| 7 | `suspended → certified` | The suspension cause cleared **and** the previously recorded verdict is still fresh/valid (administrative resume). |
| 8 | `suspended → probing` | The suspension cause cleared but the verdict must be (re-)established — including offering-operations suspended out of `probing` that never held a verdict. |
| 9 | `certified → expired` | Evidence staleness (TTL) or drift detected. |
| 10 | `expired → probing` | Refresh probe scheduled. |

**Invalid transitions** (rejected by the domain layer, audited, state unchanged):
`discovered → certified` and `observed → certified` (no probe verdict); `expired → certified`
(a stale verdict must be re-proven via `probing`); `certified → probing` directly (must pass
through `expired` or `suspended`); `suspended → expired`; any transition into or out of a
`rejected` state (the state does not exist); any transition that sets `certified` while the
operation has no recorded probe verdict.

**Repeated probe failures** therefore have exactly two representations, neither of which is a
`rejected` state: the operation **remains in `probing`** (retryable failures inside the retry
budget, rescheduled with backoff) or **moves to `suspended`** (terminal failure, or budget
exhausted) with a typed reason, returning to `probing` when the blocking condition changes.

- **Certification state × capability truth → routing:**
  - **Routing admission requires both:** certification state = `certified` **and** capability
    truth = `supported`. This conjunction is the only routable combination.
  - `certified` with truth `unsupported` or `unknown` → **not routable** (verdict negative / not proven).
  - `suspended` → **not routable** (paused), regardless of truth.
  - `expired` → **not routable**; refresh probe scheduled (a prior truth is stale evidence, §4).
  - `discovered` / `observed` / `probing` → **not routable**.

**Deterministic acceptance test (Cartesian, CI-blocking — Phase 3c gate):** a table-driven test
enumerates the full Cartesian product of the 6 certification states × 3 capability truths
(18 combinations) and asserts that **exactly one** combination — (`certified`, `supported`) — is
routable and all 17 others are not, plus one legality case per invalid transition above (attempted
transition rejected, state unchanged, audit event emitted). (Referenced by
[06 Phase 3c](06-roadmap.md#phase-3c--probing-inference-based-quota-protected),
[08 §5](08-engineering-standards.md#5-testing-strategy), and [10](10-requirements-coverage.md).)

Immediate **suspension** on: invalid credentials, hard quota exhaustion, repeated protocol
failure, provider removal, or a verified capability contradiction. Stale or contradicted evidence
invalidates certification.

**Quota states** — quota is modeled as **multiple concurrent windows per account** (provider
windows + mandatory local-safety windows, [02 §3](02-domain-model.md#quota--the-multi-window-model-budgetssources--windows--reservations--allocations)).
**Each window** carries one of the following states (from the provider response, confidence, and
staleness); an attempt takes the **most restrictive** state across its applicable windows:

| Per-window state | Meaning | Routing impact |
|---|---|---|
| `available` | Sufficient headroom confirmed (remaining ≥ request need) on this window | **Passes** — this window is not a blocker |
| `insufficient` | Known remaining but below the request's estimated need | **Blocks** for this request; other requests with lower need may still pass |
| `exhausted` | High-confidence zero remaining | **Blocks** until this window's reset |
| `unknown` | No provider data or too stale to trust (applies to **provider-evidence** windows) | **Not a hard eligibility gate**, but execution **still reserves against the account's local-safety windows** — a score penalty applies in Pro/Max. `unknown` does **not** mean `unlimited`. |
| `stale` | Data older than the staleness threshold (~15 min) | Treated as `unknown` with a background refresh trigger |

Rules:
- `available` on all applicable windows is the only unconditional pass. Any `insufficient` or
  `exhausted` window blocks execution.
- **`unknown` provider quota never means unlimited and never skips a reservation.** A FREE account
  with `unknown` provider quota is still eligible for Lite (it cannot cause paid spend), and any
  account with `unknown` provider quota may remain eligible in Pro/Max with a **score penalty** —
  but in **every** case the attempt must obtain a successful reservation against the account's
  **local-safety** windows (concurrency + estimated consumption), which is what bounds an account
  whose provider exposes no quota endpoint (default concurrency cap = 1 in-flight until provider
  quota is confirmed).
- `stale` is treated as `unknown` with an automatic background refresh.
- A 429 response from a provider triggers a **cooldown** at the correct scope (account, offering,
  or provider) and schedules a quota refresh. The 429 is **never** interpreted as `exhausted` for
  routing exclusion beyond the cooldown duration.
- Funding, the local-safety budget, health, and cooldown remain **independent gates** — provider
  quota state alone does not override them, and provider evidence is never conflated with the
  local-safety budget.

**Routing admission** — an offering-operation is routable only when *all* hold: canonical identity
resolved; every routing-required fact present (esp. a verified context number); certification state
`certified` **with** capability truth `supported` (the conjunction defined above); funding explicit
(`free`/`paid`, not `unknown`); at least one healthy
account with valid credentials; quota is not `exhausted` or `insufficient` for the request's
estimated need; not cooling down. Anything missing → not routable, with a typed reason
(`identity_unresolved`, `context_unverified`, `capability_not_certified`, `funding_unknown`,
`no_healthy_account`, `quota_exhausted`, `quota_insufficient`, `cooling_down`, …).

A bounded background **review drainer** works the backlog (idempotent, small batches, never
re-touches already-certified rows) and the dashboard shows a review count grouped by reason.
Media-only models (image/embedding/translation) get a terminal `catalog_only` state: visible,
never entering the tiers, not counted as a failure.

---

## 6. What this guarantees for the tiers

Because discovery is account-scoped, capabilities/context are probed not guessed, funding is
verified, and certification is per-operation, the tier engine can trust one simple fact about
every candidate it sees: **it is a certified, funded, healthy offering-operation with a non-exhausted
context and known quota.** No tier logic ever needs a model name or a hardcoded capability — it
filters and ranks over verified facts. That is the whole point of this pipeline.
