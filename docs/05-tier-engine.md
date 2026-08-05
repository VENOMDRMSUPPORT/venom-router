# 05 — Tier Engine (`lite` / `pro` / `max`)

The product layer. Three tiers, one capability surface, differing only in quality, context
ceiling, thinking budget, and per-tier free-vs-paid distribution policy. This encodes the **owner's stated
product policy**; where a number is a new owner requirement (not inherited from any prior build)
it is marked ⚑.

---

## 1. The three tiers (authoritative policy)

|| **`venom/lite`** | **`venom/pro`** | **`venom/max`** |
|---|---|---|---|
| Backend funding | **free accounts only** — PAID offerings are categorically prohibited. No PAID offering ever enters Lite, even under exhaustion or fallback. | free + paid | free + paid (all eligible accounts participate) |
| Paid/free mix target ⚑ | 0% paid (100% free) — paid is a hard rejection | **~25% paid / ~75% free** overall successful traffic (deficit mix controller) | **none** — there is **no** fixed free/paid funding-mix target; policy is **quality-first, then quota-fair** |
| Context ceiling ⚑ | **256K** (262,144 tok) | **512K** | **1M** |
| Thinking ⚑ | none | extended thinking | "ultra" thinking |
| Quality objective | zero-cost, correct | strong, mostly-free | best available |
| Fallback on exhaustion | **fail closed** (never paid) | within free+paid pool | within free+paid pool |
| Latency | tie-break only | scored (small weight) | tie-break only |

> **Max funding note (V1).** Max has **no** 50/50 (or any fixed) free-vs-paid funding target.
> Funding classification stays **observable and auditable**, but it is **not** a Max distribution
> objective. Max forms a competitive quality band, then distributes **quota-fairly** (DRR + P2C)
> across **all** eligible accounts — free or paid alike. See §2 Step 7 and [§8.1](#8-resolved-product-decisions).

All three are **multimodal and expose the identical V1 capability surface** — chat, streaming,
tools, structured output, vision input, reasoning. A tier never disables a
capability. If a tier's certified fleet genuinely can't serve a requested capability, the tier
reports honest coverage and returns a structured no-route error — it does **not** silently drop
the capability or cross into a disallowed funding pool.

Two clarifications. **Reasoning is a capability present in every tier; the _thinking budget_ is
tier-scoped** — Lite exposes reasoning but allocates no extended-thinking budget, Pro allocates
extended, Max allocates ultra (the normalization contract is §1a). **Image generation is future
scope** (see [§9](#9-future-scope-non-v1)): it is **not** part of the V1 guaranteed surface, there
is no V1 image-generation endpoint, and image-only models stay `catalog_only` and never enter a
tier. A request whose required capability isn't certified anywhere in the tier's fleet returns
`venom_capability_unsupported`.

> Naming note: the third product is **`venom/max`**; its extended-thinking profile is what the
> brief calls "ultra thinking." Tier alias = `max`, thinking profile = `ultra`.

### Context contract
A tier accepts requests up to its ceiling; a request larger than the ceiling is rejected with a
clear error (tiers are explicit — the router never auto-promotes to a higher tier). For a request
of `S` tokens (`S ≤ ceiling`), an offering is eligible only if its **verified** context `≥ S`
(with a safety margin). To reliably deliver the advertised size, the router prefers offerings
whose verified context `≥ ceiling`. **Unknown or unverified context ⇒ ineligible for every tier**
(fail closed — never assume a floor). Context caps are hard product facts, not scoring hints.

### Thinking policy (summary)
Thinking is a budget the router allocates per tier: **Lite allocates none** (a request asking for
extended thinking should target `pro`/`max`; Lite will not enable it); **Pro** enables extended
thinking; **Max** enables the larger "ultra" budget. **Pro**'s ~25/75 paid/free target is the
owner's **overall tier traffic** mix (resolved in [§8.1](#8-resolved-product-decisions)), applied
across all successful requests — not a thinking-only ratio. **Max applies no funding-mix target at
all.** The provider-neutral semantics are §1a.

## 1a. Thinking / reasoning budget normalization

`thinking_budget` is a **provider-neutral effort level**, not a raw provider parameter. It never
appears as a model ID and is never hardcoded per model.

**Levels (tier-assigned, request-overridable downward only):** `none`, `standard`, `extended`,
`ultra`. Tier defaults **and ceilings**: Lite → `none`; Pro → `extended`; Max → `ultra`. A client
may request (via the `venom.thinking_budget` extension, §1b) a level **at or below** its tier's
ceiling — e.g. `none` on Pro. A request **above the tier ceiling is clamped to that ceiling** and
the clamp is **reported in routing diagnostics** and the `X-Venom-*` headers (asking for `ultra`
on Lite clamps to `none`; asking for `ultra` on Pro clamps to `extended` — target `max` for the
full budget). This tier-ceiling clamp is distinct from, and applied before, the per-offering
certified-maximum clamp below.

**What the level controls (in order of applicability per offering):** it maps to whatever the
offering's certified reasoning mechanism actually is —
(1) a provider "reasoning effort"/"thinking" mode flag, or
(2) a reasoning **token budget** (mapped from the level to a bounded token count), or
(3) a provider-specific reasoning variant of the same canonical model exposed as a distinct
offering-operation. The mapping table lives in the adapter, discovered/certified per
offering-operation — never inferred from the model name.

**Capability detection:** `reasoning` is a certified capability like any other (§2 Step 3). An
offering that does not certify `reasoning` simply carries `thinking_budget = none` regardless of
tier.

**Clamping:** the requested/tier level is clamped to the offering's certified maximum (e.g. a model
whose certified max reasoning budget is smaller than `ultra` is driven at its max). Clamping is
recorded in diagnostics and the `X-Venom-*` headers.

**Unsupported behavior — graceful degradation, not rejection:** if the selected offering cannot
honor the requested thinking level, the router **degrades gracefully** (drops to the highest level
the offering supports, down to `none`) rather than eliminating the candidate — *because thinking is
a budget, not a hard capability gate.* The one exception: if the client **explicitly requires**
`reasoning` as a hard capability (via the request's required-capability set), it becomes a Step-3
gate and an offering lacking certified `reasoning` is ineligible.

**Fallback:** thinking level is preserved across fallback attempts and re-clamped per candidate;
degradation on one candidate never changes the tier's funding/context/capability gates.

**Diagnostics & tests:** the route-decision record stores requested level, per-candidate clamp, and
final applied level. Tests assert: tier defaults; downward override; clamp-to-certified-max;
graceful degradation vs. explicit-required-capability rejection; and that no raw model ID drives
the mapping.

---

## 1b. Public request contract — the `venom` extension

The public inference surface (`POST /v1/chat/completions`, [01 §6b](01-architecture.md#6b-data-plane-public-inference-api))
stays **OpenAI-compatible**. Venom-specific request semantics are carried by exactly **one optional,
namespaced request extension** — the `venom` object. There are **no** competing headers or
alternative request shapes.

```json
{
  "model": "venom/max",
  "messages": [],
  "venom": {
    "thinking_budget": "extended",
    "required_capabilities": ["reasoning", "vision"]
  }
}
```

**Canonical rules:**

- **`venom` is optional.** Its absence means the selected tier's defaults apply (tier thinking
  default; no extra required-capability gates beyond those the router infers from the request shape).
- **`thinking_budget`** is optional and accepts **only** `none | standard | extended | ultra`
  (the §1a levels). A client may request a **lower** budget than the selected tier's default. A
  request **above the tier ceiling is clamped to the tier ceiling** and the clamp is surfaced in
  routing diagnostics and the `X-Venom-*` headers (§1a).
- **`required_capabilities`** is an optional set of **canonical capability identifiers**
  (`chat`, `streaming`, `tools`, `structured_output`, `vision`, `reasoning`; the canonical operation
  vocabulary of [02 §3](02-domain-model.md) / [04 §2](04-model-intelligence.md)). Listed capabilities
  become **hard routing gates** at Step 3 — an offering-operation that has not certified every
  required capability is ineligible (this is the `reasoning`-as-hard-gate case noted in §1a).
- **Validation (typed errors):** unknown fields inside `venom`, an invalid `thinking_budget` value,
  or an unknown/invalid capability identifier in `required_capabilities` return a **typed
  validation error** `venom_invalid_extension` (400) naming the offending field — the request is
  **not** silently coerced.
- **Preserved end-to-end:** the parsed extension is preserved through **non-streaming and streaming**
  requests and across every fallback attempt (thinking budget re-clamped per candidate; required
  capabilities re-checked per candidate).
- **Never leaks provider internals:** the extension and its diagnostics **never** expose provider
  names, account IDs, raw provider model IDs, or provider-native thinking parameters — only the
  provider-neutral level and canonical capability identifiers. Response visibility is limited to the
  sanitized `X-Venom-*` headers (§7) and the owner-only `RouteExplain` diagnostics.

Request/response examples, the full field validation table, and the stable error codes are echoed in
the public-API coverage row of [10-requirements-coverage](10-requirements-coverage.md) and tested in
[08 §5](08-engineering-standards.md#5-testing-strategy).

---

## 2. Per-request selection algorithm

Input: the tier alias, the messages, and limits. The engine
runs against **one immutable routing snapshot** (candidate certified offerings + current quota /
health / cooldown) so a request sees a consistent world.

**Step 1 — Derive hard requirements.** Detect modality (text / vision / documents), required
capabilities (`structured_output`? tools? thinking?), and the request's context need `S`. The
inferred requirements are **unioned with any explicit `venom.required_capabilities`** from the
request extension (§1b), which become additional hard gates; `venom.thinking_budget` sets the
requested thinking level (clamped per §1a). (`image_generation` is future scope — see
[§9](#9-future-scope-non-v1) — and is not a V1 request operation.)

**Step 2 — Build the candidate pool.** All **certified** offering-operations for the requested
operation — certification state `certified` **and** capability truth `supported`
([04 §5](04-model-intelligence.md#5-certification)) — each tagged with its parent account's funding.
Only such certified, healthy, valid-credential, non-cooling offerings enter. Funding is inherited
from the account — no per-Offering override.

**Step 3 — Apply the tier's hard gates (fail closed).**
- **Funding gate:** Lite → **FREE accounts only** (an offering from a PAID account is flatly
  ineligible, regardless of model name or capability). Pro/Max → `free` or `paid` (never `unknown`
  — including the initial `unknown` stamped for `evidence_required` providers, which stays
  ineligible everywhere until classified; [02 §2](02-domain-model.md#2-free--paid--the-semantics-precisely)).
- **Context gate:** verified context `≥ S`; reject the request if `S > ceiling`.
- **Capability gate:** every required capability certified on the offering-operation.
|- **Health / quota / cooldown gates:** healthy account; quota not `exhausted` or `insufficient` for this request's estimated need; not cooling down.
- Anything unknown ⇒ excluded, with a typed reason code.

**Step 4 — Group into route groups.** Group eligible offerings by
`provider : model : funding`. **Critical anti-inflation rule:** N accounts of one offering form
**one group scored once** (using the best known quota headroom), so holding many accounts of a
provider gives **no ranking advantage** — it only adds execution capacity + quota. Account
selection happens *inside* the winning group (Step 7).

**Step 5 — Score each route group** (all factors normalized 0–1; missing factor = neutral 0.5
with reduced confidence). Suggested default weights (owner-tunable within bounds):

| factor | lite | pro | max |
|---|---|---|---|
| verified quality (task-adjusted) | — (hard-gate only) | 0.40 | 0.60 |
| recent reliability | — | 0.25 | 0.20 |
| quota headroom / reset risk | — | 0.15 | 0.05 |
| capability/evidence confidence | — | — | 0.10 |
| marginal cost class | — | 0.15 | 0.05 |
| latency | tie-break | 0.05 | tie-break |

Lite is **pure hard-eligibility** — no quality/speed scoring; among eligible free routes, latency
is the only tie-break. Pro and Max rank one combined free+paid pool by weighted utility;
**no fixed ratio is applied at scoring time** and free never gets an unconditional win.

**Step 6 — Competitive band (V1 definition, fixed).** The band operates on the **normalized
verified quality score** (the Step-5 quality factor, closed range `0.0–1.0`; a missing rating is
the neutral `0.5` per [04 §3](04-model-intelligence.md#3-canonical-facts-vs-effective-offering)) and
is applied **only after all Step-3/Step-4 hard eligibility filters**:

```
Pro:  top_quality_score − candidate_quality_score ≤ 0.08
Max:  top_quality_score − candidate_quality_score ≤ 0.03
```

Routes outside the band are dropped, so the Step-7 distribution logic can *never* promote a
materially weaker route to hit a ratio. If fewer than two candidates remain inside the band,
selection **continues with the available eligible candidates — the band is never widened
automatically**. Lite has no band (it isn't scored). Any future change to these numbers is
**policy tuning, not an architectural change** (see [§8.5](#8-resolved-product-decisions)).

**Step 7 — Distribute (per tier policy), then pick an account.** The distribution rule differs by
tier: **Lite** takes the top competitive free route (no funding logic — paid is already gone at
Step 3); **Pro** runs the funding-mix deficit controller toward its ~25/75 target; **Max** runs
**quality-first → quota-fair DRR + P2C with no funding-mix target**.

- **Workload-profile bucket (the deterministic key).** The workload profile is a deterministic,
  multi-label set of request-relevant properties — never a guessed "class." It is a **scoring and
  monitoring signal**, not a hard gate (required capabilities remain the Step-3 gates):

  | Property | Trigger | Notes |
  |---|---|---|
  | `vision` | Request contains image input(s) | Detected from modality extraction |
  | `tool_use` | Request includes `tools` parameter | Detected from request shape |
  | `structured` | Request includes `response_format` or `json_schema` | Detected from request shape |
  | `large_context` | Request's context need `S` exceeds a configurable threshold (default 32K) | Threshold is owner-tunable |
  | `standard` | None of the above apply | Fallback — always applies when no other property matches |

  The `workload_profile_bucket` is **one deterministic bucket key**: take the set of matched
  properties, **normalize (lowercase) → sort lexicographically → deduplicate**, join canonically
  (e.g. `+`-joined, or SHA-256 of that canonical string). The same property set always maps to the
  same bucket (`vision + structured` and `structured + vision` are the **same** bucket). This is a
  planning-level definition; the serialization is fixed once in code and never varies per call site.
- **Deficit state is per `(tier, workload_profile_bucket, funding_class)` — not global.** There is
  **no** single global deficit shared across all workloads; each tier tracks an independent deficit
  cell per workload bucket and funding class (`funding_class ∈ {free, paid}`), so a burst of
  vision requests never distorts the mix accounting of text requests, and vice-versa.
- **Pro — funding-mix deficit controller (workload-isolated).** Among the competitive routes, read
  recent *successful* distribution for this `(pro, bucket, ·)` cell, compute the deficit vs. Pro's
  `paid_share_target` (~25% paid), and prefer the funding pool with the larger positive deficit.
  This is **deficit-based**, not random, and only chooses among already-competitive routes — so the
  ~25/75 target is met *over time* without ever promoting a materially weaker route. Convergence is
  a tested gate with a fixed sample size and tolerance (see [§8.1](#8-resolved-product-decisions) and
  [06 Phase 4](06-roadmap.md#phase-4--tier-engine--routing)).
- **Max — quality-first, then quota-fair DRR + P2C (no funding-mix target) ⚑.** Max applies **no**
  free/paid distribution target; **all eligible accounts participate, free or paid alike**, and
  funding stays observable/auditable but is never a distribution objective. Ordered stages:
  1. **Quality band first** — the competitive band from Step 6 defines who may win.
  2. **Quota-fair deficit round-robin (DRR) across all eligible accounts**, weighted by
     **available capacity across all applicable quota windows *and* the local-safety budget**
     ([02 §3](02-domain-model.md#quota--the-multi-window-model-budgetssources--windows--reservations--allocations)):
     accounts **saturated on *any* required quota window are ineligible for that attempt**
     (fail-open only if every account is saturated); each round credit every eligible account
     `quantum = weight / Σweight`, pick the max-deficit account, debit its unit cost, and carry the
     fractional credit so long-run frequency converges to the capacity ratio deterministically.
     **Capacity fairness is defined here as an integral part of DRR + P2C — not a competing
     alternative to it.**
  3. **Power-of-two-choices (P2C)** for the final pick — between the top two DRR candidates choose
     the one with the better live signal (fewer in-flight requests, latency, health, reserved
     capacity headroom), taking an idempotent, auto-expiring in-flight lease.
  (DRR + P2C refinement adopted from the OmniRoute analysis, item B.)
- **Session stickiness (all tiers, preference only, *after* hard gates).** To preserve the
  *provider-side prompt cache* across a multi-turn conversation, a request may carry a stickiness
  key = `sha256(first user message)[:16]` (stable as the conversation grows). If a healthy binding
  exists for that key, its account is *preferred* within the winning group — **never forced**.
  Stickiness runs **only after** the Step-3 hard gates and the Step-7 distribution, so a sticky
  account that would fail any hard gate — **or that cannot obtain a valid quota reservation
  (Step 8)** — is simply never used: **stickiness may never violate quota reservations or
  eligibility.** The pin is recorded only on a successful response and dropped the moment any of
  these hold: quota headroom ≤ 15% on any applicable window, account unhealthy or cooling, the
  offering left the eligible pool, or the 15-min TTL expired. In-memory LRU (~500 entries),
  fail-open on any error. (Adopted from the OmniRoute analysis, item A.)
- **Account selection (within the group):** absent a valid sticky binding, pick the account by
  **capacity fairness** (available headroom across applicable quota windows + local-safety capacity,
  rate-limit headroom, inverse recent load, reliability, latency), respecting per-account in-flight
  leases and concurrency caps. For Max this is the DRR + P2C procedure above; for Lite/Pro it is the
  same capacity-fairness selection within the winning group.

**Step 8 — Fallback loop: per-attempt reservation → execute → reconcile/fallback.**

Each attempt has an **independent, self-contained** reserve→execute→settle/release cycle
bound to its own `(request_id, attempt_id)`. No attempt starts before its reservation
succeeds, and no reservation is inherited from a prior attempt.

The loop runs for at most `N` attempts (Lite 3 / Pro 4 / Max 5):

1. **Select candidate** — pick the next eligible `(account, offering)` from the candidate pool.
   The candidate must pass all hard gates (funding, context, capability, health, not cooling).
2. **Create attempt record** — persist `attempt_id` tied to `request_id` with the chosen
   `account_id` and `offering_id` for observability (no token content ever stored).
3. **Reserve quota for this candidate — atomically across every applicable window** — a single
   `BEGIN IMMEDIATE` transaction estimates consumption in the canonical dimensions
   ([02 §3](02-domain-model.md#quota--the-multi-window-model-budgetssources--windows--reservations--allocations):
   `requests`, `input_tokens`, `output_tokens`, `concurrency`, and `credits`/`balance` only where a
   verified conversion exists) and applies the per-window conditional UPDATE to **all** applicable
   windows for *this specific (account, offering)* — the account's provider-evidence windows **and**
   its mandatory **local-safety** windows (concurrency + estimated consumption):
   ```sql
   -- Repeated for EACH applicable window inside the one transaction:
   UPDATE quota_windows
   SET reserved = reserved + :estimated_cost,
       version  = version + 1
   WHERE id = :window_id
     AND version = :expected_version
     AND COALESCE(remaining, limit_value) - reserved >= :estimated_cost;
   ```
   - **All** applicable windows affect 1 row → write the reservation + allocation rows → COMMIT →
     proceed to execute.
   - **Any** window affects 0 rows → **ROLLBACK the whole transaction** (nothing left debited); the
     attempt is rejected **before** provider execution. **Re-evaluate** the candidate pool from a
     fresh snapshot; loop to Step 8.1 (do not reuse the failed reservation). Because the local-safety
     windows are always applicable, **unknown provider quota never bypasses reservation** — the
     attempt still needs local-safety headroom.
4. **Execute** — send the request via the resolved transport. **No write transaction is open**
   during the HTTP call.
5. **Reconcile** — second transaction, outcomes:
   - **Success** → `settle(reservation_id, actual_cost)`: convert reserved to consumed.
     Return the response. Done.
   - **Provider failure before consumption** (e.g. 401, invalid schema) → `release(reservation_id)`:
     free the reservation. Classify the failure scope; apply cooldown if needed. Loop to Step 8.1.
   - **Partial consumption** (e.g. stream cut short, provider confirms partial tokens) →
     `settle(reservation_id, actual_known_cost)`. The remaining reservation is freed.
     Treat as a failure; loop to Step 8.1 if the tier budget allows.
   - **Network cut / unknown consumption** → transition **`reserved → reconciliation_pending`**
     (the canonical reservation state machine, [02 §3](02-domain-model.md#quota--the-multi-window-model-budgetssources--windows--reservations--allocations)):
     the reservation enters the reconciliation pipeline (§4). Every allocation **stays reserved**
     (headroom remains debited); the actual cost is recorded as `unknown` until reconciliation
     confirms the final state. A `reconciliation_pending` reservation is **never auto-released
     because `expires_at` passed** — `expires_at` is a processing deadline, not a state. Do
     **not** blindly release.
6. **All candidates exhausted** → return `venom_no_eligible_offering` (503) with the earliest
   `retry_after` from any cooldown that was encountered.

**Identity and idempotency across attempts:**
```
request_id
└── attempt_id (1..N)
    ├── account_id
    ├── offering_id
    ├── reservation_id = f(request_id, attempt_id)
    └── execution outcome
```
- `reserve(reservation_id, …)` / `settle(reservation_id, …)` / `release(reservation_id, …)`
  are all **idempotent** — calling them twice with the same `reservation_id` has the same effect
  as calling once.
- The janitor runs **distinct branches** and is **never keyed on `state = 'reserved'` alone**
  (discriminated by the `dispatched_at` marker stamped before the provider call — [02 §3](02-domain-model.md#quota--the-multi-window-model-budgetssources--windows--reservations--allocations)):
  a `reserved` reservation past `expires_at` that was **never dispatched** → `released` (all
  allocations freed, audit event); a `reserved` reservation past `expires_at` that **was
  dispatched** (crash before reconcile) → `reconciliation_pending`; a `reconciliation_pending`
  reservation whose retry deadline / lease expired → reconciliation work is reclaimed and
  re-enqueued — allocations are **never freed silently** — and only at the terminal retry boundary
  (§4) does it move to `unknown_consumption`, recording a `usage_gap` audit event.
- The unsuccessful attempt's cooldown (if applicable) and its settled/released cost are recorded
  separately from the final successful route.

---

## 3. Fallback & cooldown

- **Bounded same-tier fallback** on *transient* failures only. Attempt budgets: Lite 3 / Pro 4 /
  Max 5. Each fallback attempt runs through the **independent reserve → execute → settle/release
  cycle** defined in §2 Step 8 — no reservation is shared across attempts, and each attempt
  targets a specific `(account, offering)`.
- Fallback **never crosses funding or capability boundaries** (Lite never touches paid,
  even on exhaustion) and never reuses a hard-failed account.
- **Failure scope classification** uses the `TypedFailure.Scope` returned by
  `InferenceTransport.NormalizeError` ([01-architecture.md §4.2](01-architecture.md#42-failure-taxonomy)):
  - `request` → stop, return error to caller. No cooldown.
  - `account` → try next account of the same offering. Cooldown the account; never cooldown the
    whole offering or provider for a single account failure.
  - `offering` → skip this offering, try another from the same account. Cooldown the offering;
    a single offering failure must not degrade the account or provider.
  - `provider` → skip all routes for this provider. Short provider-level cooldown; only trip on
    cross-account/cross-offering evidence (e.g. 503 from all accounts).
  - `transient_transport` → bounded retry (up to 3) with exponential backoff before fallback.
- **Scoped circuit breakers** (account / offering / provider) with half-open probes. Use
  **adaptive backoff** (each successive open cycle doubles the reset timeout, capped ~16×) and
  **lazy recovery** (breaker state refreshes on read — no background timers), so a repeatedly
  failing scope backs off further while a recovered one reopens on the next request. (Refinements
  from the OmniRoute analysis, item B.)
- **Cooldown on 429 / rate-limit:** parse the provider's `Retry-After` when present (else a
  capped default); mark the account/operation cooling; treat cooldown as an *eligibility input*
  (skip to the next route) rather than sleeping. Persist cooldowns so they survive restart.
- **Streaming:** fallback only before the first byte reaches the client; never emit a second
  response after streaming has begun.
- When every route is cooling down, return a bounded `retry_after` instead of blocking.

---

## 4. Quota & consumption accounting

Even as a single-owner system, per-account consumption is first-class — free quota is the scarce
pooled resource.

- Each account carries **multiple concurrent quota windows** ([02 §3](02-domain-model.md#quota--the-multi-window-model-budgetssources--windows--reservations--allocations))
  in **provider-native units** (requests / tokens / credits / % / balance) — e.g. a 5-hour *and* a
  7-day usage window, an RPM *and* a TPM window, a balance window — each with its own
  used / remaining / reserved / reset / confidence / observed-at. A window is stale after ~15 min
  unless a stronger provider reset window applies. **Every account also owns local-safety windows**
  (concurrency + estimated consumption), so an account with no provider quota endpoint is still
  bounded.
- Routing does **atomic reserve → execute → reconcile**: reserve estimated usage across **all
  applicable windows** before the call (one short txn), reconcile actual usage after. Two concurrent
  requests can never both pass a cap and overcommit (the old build's #6 bug), and an attempt can
  never partially reserve.
- **Atomic reservation mechanism** uses a **per-window conditional UPDATE with version field**,
  all inside one transaction — never a read-then-write split (full contract, capacity semantics for
  provider vs. local-safety windows, and the estimated-consumption dimensions are in
  [02 §3](02-domain-model.md#quota--the-multi-window-model-budgetssources--windows--reservations--allocations)):

  ```sql
  -- For EACH applicable window, within one BEGIN IMMEDIATE transaction:
  UPDATE quota_windows
  SET reserved = reserved + :estimated_cost,
      version  = version + 1
  WHERE id = :window_id
    AND version = :expected_version
    AND COALESCE(remaining, limit_value) - reserved >= :estimated_cost;
  ```

  - **All** applicable windows affect 1 row → reservation succeeded; proceed to execute.
  - **Any** window affects 0 rows → ROLLBACK the whole transaction; the attempt is rejected before
    execution. Re-evaluate the candidate pool from a fresh snapshot; do not blindly retry the same
    route.
- **Reservation identity, states, and idempotency:**
  - Each reservation gets a unique `reservation_id` tied to `(request_id, attempt_id)` so retries
    or reconciles cannot double-count.
  - The stored states are **exactly** `reserved | reconciliation_pending | settled | released |
    unknown_consumption` — the canonical machine and full transition table live in
    [02 §3](02-domain-model.md#quota--the-multi-window-model-budgetssources--windows--reservations--allocations).
    There is **no** stored `expired` state; `expires_at` is a **processing deadline**.
  - Reserve/settle/release and every state transition are **idempotent** and update **every
    allocation** of the reservation consistently — calling them twice with the same
    reservation_id has the same effect as once.
- **Release and settlement:**
  - On success → `settle(reservation_id, actual_costs)`: for each allocation, convert reserved to
    consumed on its window.
  - On failure → `release(reservation_id)`: free every allocation without consuming. `released` is
    legal **only** when execution never left Venom or the provider proves no consumption occurred;
    after dispatch, an ambiguous network/stream outcome must transition to `reconciliation_pending`
    instead.
  - On crash / deadline → the janitor's discriminated branches apply ([02 §3](02-domain-model.md#quota--the-multi-window-model-budgetssources--windows--reservations--allocations)):
    never-dispatched `reserved` past deadline → `released` (+ audit); dispatched `reserved` →
    `reconciliation_pending`; pending items are reclaimed/re-enqueued — never silently freed — and
    reach `unknown_consumption` only at the terminal retry boundary, after which the account's
    windows are re-baselined at the next authoritative quota sync.
- **Contention and concurrency:** SQLite `busy_timeout=5000` and `BEGIN IMMEDIATE` on the
  reservation transaction ensure linearizable writes across all windows. Under high contention the
  second request waits, re-reads, and may find insufficient headroom on one of the windows.
- **Quota state — unified rules (evaluated *per window*; the attempt takes the most restrictive):**
  - `available` (confirmed remaining ≥ need) → **pass** for that window.
  - `insufficient` (known remaining < need) → **block** for this request size.
  - `exhausted` (high-confidence zero) → **block** until that window resets.
  - `unknown` (no provider data) → **not a hard gate for eligibility**, but the attempt **still
    reserves against the account's local-safety windows** — `unknown` provider quota gets a Pro/Max
    score penalty and is bounded by the local-safety concurrency + consumption limits, **never
    treated as `unlimited`**. A FREE account with `unknown` provider quota is eligible for Lite
    (cannot cause paid spend) but still needs a successful local-safety reservation to execute.
  - `stale` (data older than threshold) → treat as `unknown` + background refresh trigger.
- 429 handling: triggers a **cooldown** at the correct scope (account/offering/provider) and
  schedules a quota refresh. Never interpreted as `exhausted` beyond the cooldown duration.
- **Max quota-fair balancing** consumes these per-window snapshots and the local-safety capacity
  directly (§2, Step 7); an account saturated on **any** required window is ineligible for that
  attempt.
- The dashboard surfaces per-account usage %, remaining, and reset — this is the "consumption
  accounting per model" the owner asked for.

### Unknown-consumption reconciliation

A network cut or ambiguous provider outcome after the request left Venom means the actual cost is
**unknown** (§2 Step 5, "Network cut / unknown consumption"). This path must never silently leak a
reservation or double-charge. Contract:

- **What creates it:** an attempt whose reservation is `reserved`, the request was dispatched
  (`dispatched_at` set), and the transport returned neither a confirmed success cost nor a clean
  pre-consumption failure (timeout after send, stream abort with no usage trailer, ambiguous 5xx).
  The reservation **transitions `reserved → reconciliation_pending`** (it is **not** released).
- **Reconciliation job:** a bounded background worker (idempotent, small batches) picks up
  `reconciliation_pending` reservations. For each it attempts, in order: (1) read a provider usage
  endpoint for the window if one exists; (2) match by `request_id`/`attempt_id` where the provider
  echoes an id; (3) otherwise fall back to the estimated cost with a `low` confidence tag.
- **Data sources:** provider usage/billing endpoints where available (several providers have none —
  see [03](03-provider-integration-catalog.md)); the local `route_attempt` record; the original
  estimate. **No prompt/response content is ever read or stored.**
- **Retry schedule & limits:** exponential backoff (e.g. 30 s → 5 m → 30 m), capped at a bounded
  number of attempts (default 5) or the quota reset window, whichever comes first.
- **Lease / ownership:** each pending reservation is claimed with an auto-expiring lease so only one
  worker reconciles it; a crashed worker's lease expires and the item is reclaimed.
- **Idempotency:** reconciliation is keyed by `(reservation_id / attempt_id)`; re-running it
  produces the same terminal outcome and **updates all of the reservation's window allocations
  consistently** (every affected provider and local-safety window moves together). It composes with
  the idempotent `settle`/`release` primitives.
- **Conservative treatment while unresolved:** every allocation **stays reserved** (headroom on each
  affected window remains debited) so the account cannot overcommit; the account's **local-safety
  concurrency window** holds it to one in-flight attempt until the item resolves.
- **Final outcomes:** `settle(actual)` when usage is confirmed; `settle(estimate, confidence=low)`
  when only the estimate is available at the retry cap; `release` only if the provider proves the
  request never consumed. Each outcome is applied across **all** affected window allocations. On
  terminal give-up the reservation moves to `unknown_consumption` and a `usage_gap` audit event is
  recorded; the next full quota **sync** re-baselines the account's windows.
- **Manual recovery:** the dashboard exposes pending / `unknown_consumption` items; the owner can
  trigger a re-sync or accept the estimate.
- **Behavior with no provider reconciliation API:** skip straight to `settle(estimate,
  confidence=low)` at the first retry, flag the account for re-baseline at the next quota sync, and
  surface the `usage_gap` in diagnostics — never leave the reservation reserved indefinitely.
- **Audit & retention:** every state change emits an audit event (ids/costs/confidence only);
  reconciliation records follow the same retention as usage records.

The janitor rule from §2 stands: a `reconciliation_pending` reservation is **never** auto-released
because a deadline passed — an arrived retry deadline only **reclaims and re-enqueues** the
reconciliation work (allocations stay reserved), and only the **terminal retry boundary** moves it
`reconciliation_pending → unknown_consumption` (recording `usage_gap`). Nothing frees headroom
silently.

---

## 5. Errors (stable envelope)

Every failure returns `{ error: { code, message, request_id, retryable } }` — no secrets, no raw
provider errors. Key codes:

- `venom_free_capacity_exhausted` (429) — Lite free quota temporarily gone; include earliest
  `retry_after`.
- `venom_no_eligible_offering` (503) — no certified, funded, capable route for this tier+request.
- `venom_context_exceeds_tier` (400) — request larger than the tier's ceiling.
- `venom_capability_unsupported` (400/501) — a required capability isn't certified anywhere in the
  tier's fleet.
- `venom_invalid_extension` (400) — the `venom` request extension had an unknown field, an invalid
  `thinking_budget` value, or an unknown/invalid `required_capabilities` identifier (§1b). Names the
  offending field; the request is never silently coerced.
- `invalid_api_key` (401), `rate_limited` (429, ingress).

Lite must **fail closed** with one of the first two rather than ever escalating to paid/unknown.

---

## 6. Ingress rate limiting

Protect Venom's own endpoints (distinct from provider-429 cooldowns): per-path, per-IP sliding
window for control/public paths; **per-API-key** RPM for `/v1/*` (stored with the Venom key). A
process-local sliding-window counter is sufficient for a single-owner single-process gateway;
keep the same contract if it ever moves behind multiple instances.

---

## 7. Observability

Persist route-decision records (candidate set + exclusion reason codes + chosen route + scores)
and per-attempt records (provider/account/model IDs, latency, normalized status). **Never** store
prompts, full responses, token contents, raw provider errors, or `Authorization` headers by
default. This gives the dashboard its "why did this route?" explanation and the analytics/quota
views without ever leaking secrets.

The same decision facts are surfaced to inference clients as the sanitized **`X-Venom-*` response
headers** (request-id / tier / provider / model / funding / latency / tokens / fallback-attempts)
defined in [01-architecture](01-architecture.md) §6c — one builder, the same `sanitize` boundary,
and never an account identifier or credential.

---

## 8. Resolved product decisions

These were open in an earlier draft and are now **decided** — the engine spec above reflects them.

1. **Funding-mix scope ⚑ — RESOLVED (owner): Pro only, over overall tier traffic.** **Pro** has one
   overall paid/free target (~25% paid / ~75% free) governing **all** its successful requests;
   extended thinking is enabled independently and does not change the ratio's scope. **Max has no
   funding-mix target of any kind** (§1, §2 Step 7). The deficit controller keeps **one deficit cell
   per `(tier, workload_profile_bucket, funding_class)`** — **not** one global deficit — where the
   bucket is the deterministic normalized-sorted-deduplicated key defined in §2 Step 7.
   **Pro convergence gate (deterministic):** over **N = 2,000** synthetic successful requests spread
   across the standard workload buckets (at least `standard`, `vision`, `tool_use`, `structured`,
   `large_context`), the realized paid share per bucket must land within **±5 percentage points** of
   the 25% target, with **zero** funding/eligibility invariant violations. (Planning contract, not a
   production benchmark — see [06 Phase 4](06-roadmap.md#phase-4--tier-engine--routing) and
   [08 §5](08-engineering-standards.md#5-testing-strategy); values may later be tightened without
   changing architecture.)
2. **Context-ceiling behavior — RESOLVED (default): reject.** A request larger than the tier's
   ceiling returns `venom_context_exceeds_tier` (400); tiers are explicit and the router never
   auto-promotes or silently clamps user context.
3. **Max backend balancing — RESOLVED (owner): quality-first, then quota-fair (DRR + P2C); no
   funding-mix target.** Max first forms the competitive quality band, then distributes quota-fairly
   across **all** eligible accounts (free or paid) in proportion to available capacity across all
   applicable quota windows and the local-safety budget, then finalizes with P2C (§2 Step 7). There
   is **no** 50/50 (or any fixed) free/paid target in V1; funding stays observable/auditable but is
   never a Max distribution objective. Max's gate tests **quota-fairness and quality-band behavior,
   not a funding ratio** ([06 Phase 4](06-roadmap.md#phase-4--tier-engine--routing)).
4. **Owner-tunable Step-5 weights — RESOLVED (default): fixed for V1.** The scoring weights ship
   fixed and validated for V1; exposing them in the dashboard is deferred to a later version.
5. **Competitive-band widths — RESOLVED (owner).** The band is defined on the normalized quality
   score (`0.0–1.0`): **Pro ≤ 0.08**, **Max ≤ 0.03** below the top eligible candidate's quality
   score, applied only after all hard eligibility filters (§2 Step 6). Fewer than two in-band
   candidates ⇒ continue with the available eligible candidates; **no automatic widening**. Any
   future change to these numbers is policy tuning, not an architectural change
   ([06 Phase 4](06-roadmap.md#phase-4--tier-engine--routing) gates assert them).

## 9. Future scope (non-V1)

Explicitly **not** part of the V1 guaranteed capability surface. Recorded here so no roadmap gate
falsely claims them and the future Design System is not required to build V1 screens for them.

- **Image generation.** `image_generation` remains a *recognized* operation (discoverable and
  certifiable, and image-only models are cataloged as `catalog_only`), but:
  - it is **not** part of the V1 tier capability surface;
  - there is **no** V1 public endpoint for it (the V1 data plane is `POST /v1/chat/completions` and
    `GET /v1/models` only — see [01 §6b](01-architecture.md#6b-data-plane-public-inference-api));
  - no V1 tier routes it, and no V1 acceptance gate depends on it.
  - **If promoted to a future version**, the plan must then specify: a public endpoint and
    OpenAI-compatible request/response contract, input/output (URL/base64/binary) handling, the
    operation's transport behavior, discovery, per-operation certification, funding + quota
    reservation, fallback, tier policy, diagnostics, playground/UI, a roadmap phase, and an
    acceptance gate.
- **Reasoning.** `reasoning` (added 2026-08-05, bounded additive vocabulary unfreeze — see
  [02 §3](02-domain-model.md#3-entities) / [04 §5](04-model-intelligence.md#5-certification))
  remains a *recognized* operation — a provider's declared reasoning capability (e.g. claude-code's
  official `capabilities.reasoning`) is discoverable and certifiable — but, on the same terms as
  `image_generation` above: it is **not** part of the V1 tier capability surface, no V1 tier routes
  it, and no V1 acceptance gate depends on it.
- **Embeddings / audio operations**, cross-provider model equivalence, and per-offering funding
  overrides remain future scope (unchanged from the V1 exclusions elsewhere in the spec).
