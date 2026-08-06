# Model Qualification Pipeline — Design

**Date:** 2026-08-05
**Status:** Approved by owner (design); spec pending owner review
**Owner decision context:** this session grants authority to amend the standing
metadata rules where they were the cause of the defect.

---

## 1. The requirement, in one sentence

> Enabling a provider must, with no further human action, produce for every one
> of that provider's models: the complete capability set, the real context
> window, and a performance score — because those three facts are what the tier
> engine routes on.

Everything in this document exists to satisfy that sentence. Anything that does
not serve it is out of scope or is deleted.

---

## 2. What the investigation established

Four independent read-only traces plus live network verification. All claims
below are file-anchored ground truth, not inference.

### 2.1 The routability layer is inert

`internal/httpapi/models.go:233,235` passes `NativeCapabilities: nil` and
`TransportOperations: nil` into the projection. `internal/intelligence/readmodel.go:190-193`
computes `effective` as `native != nil && transport != nil && ...`, and
`readmodel.go:227` computes `routable = models.Routable(state, truth) && effective`.

Therefore **`routable` is `false` for every capability of every offering,
unconditionally** — regardless of what any adapter declared or any probe proved.
This is why the Model Test Report shows `WORKING 12` beside `ENABLED 0`.

### 2.2 The probe layer is largely ceremonial

`internal/httpapi/probeadapters.go:206-210` builds every probe request as:

```go
normReq := execution.NormalizedRequest{
    Operation: execution.OperationChat,   // hardcoded, whatever op is under test
    Messages:  messages,
    MaxTokens: &maxTokens,
}
```

No `Tools`, no `Parts`, no `Stream`. Consequences:

| Operation | Reality |
|---|---|
| `tools` | **Structurally impossible.** The fixture text says "Use the add tool if one is available" (`capabilityprobe.go:42-45`) but no tool definition is ever sent. A model with no declared tools cannot emit a tool call, so `WitnessToolCall` can never be produced. |
| `vision` | **Structurally impossible, at both ends.** The fixture inlines a data URI into a plain text string (`capabilityprobe.go:50-53`), so the provider receives text, not an image. And `probeWitnessOf` (`probeadapters.go:143-152`) has no branch that can ever return `WitnessVisionAnswer` — a dead constant. |
| `structured_output` | Works, but weak: any JSON object in the content satisfies it. |
| `context_window` | Works, but `ExpensiveProbesEnabled: false` by default (`probesafety.go:63-103`) and rung 3 is dead because `probe.go:526` passes `rules = nil`. |
| `chat`, `streaming`, `reasoning`, `image_generation` | No probe exists. |

**Of eight operations, exactly one can be truly proven today.**

The root cause is precise: the probe port was defined narrower than the
transport beneath it. `internal/execution/types.go` already has
`ContentPartImage` (`:68`), `Message.Parts` (`:99`), `NormalizedRequest.Stream`
(`:109`), `NormalizedRequest.Tools` (`:123`) and `InferenceTransport.Stream`
(`:253`). `intelligence.ProbeMessage` (`probetransport.go:12-15`) is
`{Role, Content string}` and discards all of it.

### 2.3 The catalog source is wired to two adapters out of seven

`internal/providers/modelsdev.go` is consumed only by `ollama_cloud.go` and
`nvidia_nim.go`. Its `ModelsDevFacts` struct (`modelsdev.go:33-42`) never parses
`reasoning`, never parses `modalities.output` for image output, and never parses
`limit.input`.

### 2.4 The premise the ClinePass design rests on is factually false

`docs/superpowers/plans/2026-08-05-hybrid-capabilities-and-context.md:77` states:

> "clinepass's wire returns `{id, name}` only ... **and it has no models.dev
> entry** — under 'no guessing' nothing can be *declared*."

Verified live on 2026-08-05:

- `https://models.dev/api.json` contains provider key **`cline-pass`** with 11 models.
- `https://api.cline.bot/api/v1/ai/cline/recommended-models` (public, no auth)
  returns 12 models in the `clinePass` group.
- **11 of the 12 live ids are exact string matches** to models.dev keys
  (`cline-pass/kimi-k3`, `cline-pass/kimi-k2.6`, …). No normalization needed.
  The single miss is `cline-pass/qwen3.8-max`, a model newer than the dataset.
- models.dev's `cline-pass.api` field is `https://api.cline.bot/api/v1`, which
  matches our `ClinePassBaseURL` (`clinepass.go:21`).

`cline-pass/kimi-k3` in models.dev declares: `reasoning: true`, `tool_call: true`,
`structured_output: true`, `modalities.input: [text, image, video]`,
`limit.context: 1048576`, `limit.output: 131072`.

The dataset covers **6106 models across 180 providers**, and carries
`tool_call`, `reasoning`, `modalities` and `limit` on **100%** of them. Every
provider slug in our catalog has a corresponding models.dev key.

### 2.5 Bifrost's model catalog is not a usable source

`third_party/bifrost` is a git submodule. Only `bifrost/core` is in our
`go.mod`; `framework/modelcatalog` is **not compiled into our binary**. It
fetches from `getbifrost.ai` at runtime and ships no offline dataset (6 models
in test fixtures). Its `datasheet.Entry` type has **no** field for
`supports_vision`, `supports_pdf_input` or `supports_native_streaming` — those
keys exist in the upstream JSON and are silently discarded by `json.Unmarshal`.
None of our model ids are present. It is rejected as a source.

### 2.6 The score is misnamed and double-counted

`internal/httpapi/benchmark_engine.go:177-188` computes
`0.5*min(tokensPerSec/80,1) + 0.5*max(0, 1-ttft_ms/2000)` — a throughput and
latency measurement. It is stored in `models.quality_rating` (0–100), exposed as
both `quality_rating` (0–100, group level) and `quality_score` (0–1, offering
level, `effective.go:57-74`), and consumed as the **quality** term of the routing
composite in `internal/routing/scoring.go:55` — which *also* carries a separate
`latency` term. Latency is therefore counted twice.

### 2.7 Cost is structurally unobtainable

All three sources in `FreeSafetyResolver.Resolve` are dead: the owner-override
argument is hardcoded `nil` (`models.go:219`), **no provider adapter anywhere
sets `DiscoveredModel.Pricing`**, and the models.dev seam is constructed `nil`
(`models.go:81`). `cost.is_free` is `null` for every offering. The `Free`/`Paid`
branches are unreachable in production.

---

## 3. The corrected metadata rule

This **replaces** the rule in
`docs/superpowers/plans/2026-08-05-hybrid-capabilities-and-context.md:15`
("metadata comes from official provider responses or models.dev exact-key
matches ONLY"). That rule constrained the *number of sources* when the intent
was to forbid *fabrication*. The narrowness was itself a defect: it excluded our
own transport's knowledge of itself, which is not a guess about a model but a
fact about our code.

> **Every metadata value must be attributable to a named, re-checkable source,
> and must carry that source as provenance. A value whose source cannot be
> named is not written — the field stays `unknown`.**
>
> Admissible sources, strongest first:
>
> 1. Owner override
> 2. Verified Venom runtime measurement, for this exact offering
> 3. Our own transport's declared capability (a fact about our code)
> 4. Provider-native authenticated metadata
> 5. Provider discovery / catalog response
> 6. External registry, **exact identity match only** (models.dev)
>
> **Nothing below line 6 may certify anything.** Similarity of name, family or
> version may *schedule a measurement* and nothing more.
>
> **Forbidden:** any value derived from a model's name, id substrings, family,
> or resemblance to another model. That — not the source count — is what "no
> guessing" means.
>
> **Never-downgrade invariant:** unavailability of a source is "no new
> evidence", never "the capability was withdrawn". An infrastructure failure
> (network, quota, rate limit, 5xx) must never flip a known capability to
> `unsupported`.

`docs/04-model-intelligence.md:209-225` already encodes essentially this ladder;
this rule aligns the working plan layer with the governing doc and adds tier 3,
which neither previously named.

---

## 4. Architecture

One pipeline. Two phases. No buttons.

```
provider enabled / account connected
            |
            v
   [ Discovery ]  live model ids from the provider   (authoritative: WHICH models exist)
            |
            v
   [ Phase 1: Catalog Resolution ]  no provider network
            |   - exact-key join against models.dev
            |   - streaming from our own transport
            |   - provenance: catalog | transport | provider
            v
   offerings now have: capabilities, context, limits, display names
            |
            v
   [ Phase 2: Live Qualification ]  a few tiny real requests
            |   - proves chat, streaming, tools
            |   - measures TTFT + tokens/sec  -> score
            |   - context probe ONLY for models the catalog does not cover
            |   - provenance upgrades: catalog -> measured
            v
   offerings are routable, with a score
```

### 4.1 Phase 1 — Catalog Resolution

**Purpose:** complete, instant, zero-cost capability and limit facts.

**Unit: `internal/providers/modelsdev.go` (extended)**

`ModelsDevFacts` gains the fields the dataset already carries and we discard:

| Field | models.dev source |
|---|---|
| `Reasoning bool` | `reasoning` |
| `ImageOutput bool` | `modalities.output` contains `"image"` |
| `MaxInput *int` | `limit.input` |

Existing fields (`ToolCall`, `StructuredOutput`, `ImageInput`, `Context`,
`Output`, `DisplayName`, `Deprecated`) are unchanged. The deliberate refusal to
read `cost` stands (see §4.6).

**Unit: provider-key resolution, proven not guessed**

An explicit map from our provider slug to the models.dev key lives in one place.
Every entry below was verified against the live dataset on 2026-08-05:

| Our slug | models.dev key | Evidence |
|---|---|---|
| `clinepass` | `cline-pass` | `api` host matches `api.cline.bot`; 11 of 12 live ids join exactly |
| `nvidia-nim` | `nvidia` | `api` = `https://integrate.api.nvidia.com/v1`, exact host match |
| `ollama-cloud` | `ollama-cloud` | `api` = `https://ollama.com/v1`, exact host match |
| `github-copilot` | `github-copilot` | `api` = `https://api.githubcopilot.com`, exact host match |
| `opencode-zen` | `opencode` | `api` = `https://opencode.ai/zen/v1`. **Host-ambiguous** — see below |
| `xai` | `xai` | Exact slug identity; dataset `api` is empty |
| `claude-code` | `anthropic` | Dataset `api` empty; joined by model-id overlap (`claude-sonnet-4-6`, `claude-opus-4-5`, …) |
| `gemini-cli` | `google` | Dataset `api` empty; joined by model-id overlap (`gemini-*`) |
| `codex` | `openai` | Dataset `api` empty; joined by model-id overlap |
| `agnes-ai` | *(none)* | **Absent from models.dev entirely.** See §4.1.1 |
| `antigravity` | *(n/a)* | Registers no discovery adapter; contributes no offerings |

**Why the map is authoritative and the URL is only a check.** `opencode.ai` is
the host of *two* dataset providers — `opencode` (`/zen/v1`, 86 models) and
`opencode-go` (`/zen/go/v1`, 24 models). A URL match alone therefore cannot
select a key. The explicit map states the intent; the URL check refuses a
mismatch. Discovery-by-URL is not used.

**Verification at fetch time:** when the mapped entry carries a non-empty `api`,
its host must match that provider's configured base URL. A mismatch means the
dataset changed shape under us — the key is refused and that provider gets no
catalog enrichment (fail closed, never a wrong join). Entries with an empty
`api` skip the check; the map is the only evidence and is annotated as such in
code. The count of live ids that joined is logged for every discovery run, so a
silently-broken mapping is visible rather than merely producing nothing.

#### 4.1.1 The agnes-ai gap

`agnes-ai` has no models.dev entry under any key. Its models get their
capabilities from its own wire response — which already declares `tools`,
`vision` and `structured_output` (`agnes_ai.go:30-39`) — plus Phase 2
qualification. This is stated here so its narrower capability picture is
understood as a real upstream gap, not a bug in this pipeline.

**Unit: capability derivation**

| Operation | Derived from |
|---|---|
| `chat` | the provider's endpoint is a chat-completions gateway |
| `streaming` | **our transport** — `execution.InferenceTransport.SupportedCapabilities(route)`, which already returns `[chat, streaming]` and is read by nobody (§4.3.1) |
| `tools` | `tool_call` |
| `structured_output` | `structured_output` |
| `vision` | `modalities.input` contains `image` |
| `reasoning` | `reasoning` |
| `image_generation` | `modalities.output` contains `image` |
| `context_window` | `limit.context` (a number, not a boolean) |

**Behaviour change:** a model whose output modalities are not all text is
currently *dropped from the catalog entirely* (`modelsdev.go:213-215`). With
`image_generation` in scope this is wrong — such a model is discovered and
classified, not hidden.

**Correction (2026-08-06, found in review during implementation).** An earlier
draft of this section justified that change by asserting that
`internal/intelligence/classification.go` keeps media-only offerings out of chat
routing. **That was false.** `OutputAllText` has zero consumers outside
`internal/providers`; `Classify` short-circuits on the first `chat` operation and
returns `ClassificationRoutableCandidate` before consulting modalities at all;
and `classification_test.go:38-46` deliberately pins that a chat-exposing
offering is never `catalog_only`. Keeping these models while still asserting
`chat` for them would have routed image-generation models to chat requests.

The real fix is at the source: `chat` is **grounded in declared text output**,
exactly as every other operation is grounded in an explicit field.

| Declared `modalities.output` | `chat` emitted? |
|---|---|
| absent or empty | yes — unknown output must not drop a chat model |
| contains `"text"` | yes |
| explicitly non-empty without `"text"` | **no** |

A pure image-output entry therefore claims only `image_generation`, and
classification's media-only branch fires as designed. Emitting `chat`
unconditionally was itself a guess, and the project's own no-guessing rule
forbids it.

**Unit: operation rows**

`internal/storage/discovery.go:284-311` currently writes an
`offering_operations` row only for declared + candidate operations. A chat-only
model therefore gets exactly one row and its other seven operations are
permanently unknowable.

An earlier draft of this section required **a row for all eight operations on
every offering**, plus an operation-aware `ReviewDrainer` to stop the
un-measurable ones stranding in `probing`. Writing the implementation plan
showed that to be solving a problem at the wrong end.

That requirement existed only because adapters declared nothing, so a
capability with no row could never be probed into existence. Catalog resolution
removes the cause: the operations are **declared at the source**, so rows follow
declarations exactly as the existing mechanism already does, and no
`ReviewDrainer` change is needed.

The rule is therefore unchanged from today's, and now finally has real input:

- declared (provider wire **or** catalog) → a row, certified from the
  declaration, provenance recorded
- undeclared → no row, displayed as unknown

The candidate-operation mechanism (`DiscoveredModel.CandidateOperations`)
becomes unnecessary for any catalogued provider and is dropped from ClinePass,
which was its only user. It existed to create a probeable row for a capability
that could not be declared; with the catalog joined, it can be.

### 4.2 Phase 2 — Live Qualification

**Purpose:** the score, plus upgrading declarations to measurements.

**Enabling change — widen the probe port to match the transport underneath it:**

`intelligence.ProbeMessage` gains parts, and `ProbeRequest` gains tools, a
stream flag, and the real operation instead of a hardcoded `chat`. This is not
new capability — it is ceasing to discard capability `internal/execution`
already exposes. `probeadapters.go` maps the new fields straight through to
`execution.NormalizedRequest`.

**The governing rule for how much to measure.** Measurement is *mandatory* for
the score, because nothing else can produce it. For capabilities it is
*gap-filling*: it establishes what the catalog left silent, and opportunistically
upgrades whatever the mandatory request happens to prove. It does not re-prove
what the catalog already states. This keeps a connect cheap — typically one
request per model — and puts the effort where it is the only source of truth.

**Request 1 — always sent, once per offering.** A streaming chat request with a
trivial tool definition attached and `max_tokens` in the low tens:

- **score** — TTFT from the first chunk, tokens/sec across the stream. This is
  the request's primary purpose.
- **chat** — proven by a non-empty response. Per the verified legacy contract
  (`docs/evidence/clinepass-legacy-wire-reference.md` §7), an empty body on a
  200 is a FAILURE, not a success.
- **streaming** — proven by receiving two or more chunks with non-empty deltas.
- **tools** — proven if the model emits a tool call. Not emitting one is **not**
  proof of absence; only the three codes in `reliableUnsupportedCodes`
  (`capabilityprobe.go:81-85`) may mark `unsupported`.

**Request 2 — sent only when the catalog is silent on `structured_output`.**
A request constraining the reply to a named one-field JSON object. The witness
is that the response parses as an object *and carries that field* — tighter than
today's "any JSON object parses" (`probeadapters.go:147-150`), which a model
could satisfy by accident.

**Request 3 — sent only when the catalog declares `vision`.** A real
`ContentPart{Kind: image}` carrying a solid-colour image, with the instruction
to name the colour in one word. The witness is a content assertion on the
answer. This is the first vision test in this system that can actually pass, and
it confirms rather than gap-fills, because vision has no other proof path and a
false positive from the catalog would misroute an image request.

**`context_window`** is measured only for models the catalog does not cover,
using the existing oversized-request probe. For catalogued models the dataset
value is used. A measurement, whenever one runs, wins only when it is *narrower*
(`docs/04-model-intelligence.md:209-225`: a proven narrower restriction beats a
broader claim).

Typical cost for a catalogued provider: **one request per model.** Worst case
for an uncatalogued model: three, plus a context probe.

`reasoning` and `image_generation` have no measurement path:
`execution.NormalizedResponse` has no reasoning/thinking field and no image
output part. They remain catalog-declared, labelled as such. This is stated
plainly rather than papered over.

**Result: six of eight operations become genuinely measurable, against one today.**

**Budgets.** Qualification runs under the existing `ProbeGuard` (`probesafety.go`)
and the existing AIMD pacer and circuit breaker (`usability_pacer.go`). One
qualification per offering, cached; re-run on certification expiry, not on every
tick. Requests are tiny by construction.

### 4.3 Routability

Pass the real `NativeCapabilities` and `TransportOperations` into the projection
instead of `nil`. This single change makes `effective` and `routable` mean what
they say, and turns the ENABLED counter into a real number.

#### 4.3.1 The transports under-declare themselves

Wiring `TransportOperations` naively would make things **worse**, not better.
Every concrete transport declares the same two operations:

| Transport | Declares | file:line |
|---|---|---|
| `OpenAICompatibleTransport` | `[chat, streaming]` | `openaicompat.go:530` |
| `NativeAPITransport` | `[chat, streaming]` | `nativeapi.go:227` |
| `NativeOAuthTransport` | delegates to its codec; all three codecs return `[chat, streaming]` | `nativeoauth.go:233, 277, 319, 437` |
| `BifrostTransport` | `[chat]` | `bifrost.go:252` |

And `execution.Operation` has only **four** values — `chat`, `streaming`,
`tools`, `vision` (`execution/types.go:48-59`) — against `models.Operation`'s
eight.

Because `effective` is an intersection, feeding those declarations in as-is
would cap every offering at chat and streaming and make `tools` and `vision`
permanently unroutable. The declarations are simply **stale**: they predate
P5-EXEC-004, which gave the transports `NormalizedRequest.Tools`,
`ToolChoice` and `Message.Parts`, and a typed `ErrRequestFeatureUnsupported`
for anything they cannot express. The transports gained the ability and their
self-description was never updated.

**Therefore:** each transport's `SupportedCapabilities` is corrected to declare
what it actually serializes, and `execution.Operation` gains the constants that
correction needs. Each correction is paired with a test that fails if the
serialization it claims is removed — a declaration that cannot be falsified by
deleting the behaviour it describes is worthless.

`context_window`, `reasoning` and `image_generation` are deliberately **not**
transport operations. A transport carries requests; it does not "carry" a
context limit. Their `effective` therefore stays false, which is correct:
`context_window` is consumed as a number through `EffectiveContextTokens` and
the `AdmissionContextUnverified` gate, not as a routable operation, and the
other two are display-and-certify-only by owner decision (§9).

### 4.4 Context

Precedence: measured `native_context_tokens` → provider-native `ContextLength` →
catalog `limit.context`. Narrower wins over broader. A third provenance label,
`catalog`, joins the existing `native` and `provider_cap`.

`MaxInputTokens` and `MaxOutputTokens` are currently written to the database and
never read (`readmodel.go:104-117` has no field for them). They are projected and
displayed.

### 4.5 Score

One field, one scale, one name, on both surfaces: **`performance_score`, a
0.0–1.0 float, with a companion `performance_measured bool`**. It replaces the
`quality_rating` (0–100, group level) / `quality_score` (0–1, offering level)
pair that today expresses one fact two ways. The database column
`models.quality_rating` keeps its name and 0–100 storage — renaming it would be
a migration with no behavioural gain — but nothing outside the storage layer
uses that spelling or that scale.

It is named for what it measures. `benchmark_engine.go:177-188` computes
throughput and time-to-first-token; it is a performance measurement and calling
it "quality" is what made the two surfaces look contradictory.

It is produced automatically by Phase 2. There is no benchmark button.

When unmeasured, `performance_measured` is `false` and the score is not
rendered. The server must stop sending `0.5` as a stand-in for "unknown"
(`effective.go:62-64`) — an unknown value travels as `null`, and the dashboard
comment at `ModelsSurface.tsx:356-358`, which claims the unknown value is `0`,
is deleted along with the behaviour it misdescribes.

The routing composite's double-count of latency (§2.6) is corrected by removing
the separate `latency` term, since the performance score already contains it.
This is the only change this design makes to `internal/routing`.

### 4.6 Cost

Removed from the model cards. All three sources are structurally dead (§2.7) and
the owner obtains cost from the provider, account and quota surfaces instead. The
`FreeSafetyResolver` remains for the funding classification it genuinely serves;
only the per-model cost chip goes.

---

## 5. Simplification and deletion

The owner will not use manual testing controls. Removing them deletes more code
than this design adds.

**Deleted:**

- `POST /api/control/v1/offerings/{id}/probe` and `ProbeHandler`
  (`internal/httpapi/probe.go`), plus the `probeableOperations` gate
- The "Test" / "Test All" controls and the clickable capability chips
- The benchmark button and its endpoint trigger
- `dashboard/src/fleet/modelStatus.ts` `PROBEABLE_OPERATIONS` — a hand-maintained
  TypeScript mirror of a Go map, free to drift
- `dashboard/src/fleet/modelStatus.ts` `hasVerifiedChat` — a second hand-maintained
  mirror, this one of the SQL in `storage/catalog.go:122-141`
- The dead `coding` entry in `CapabilityChips.tsx:8-25` (not a valid operation;
  `ParseOperation` rejects it)
- `internal/intelligence/enrichment.go` — `NewEnrichmentService` has zero
  production callers and no `MetadataRegistry` implementation exists. Catalog
  resolution lives in one obvious place in the discovery path instead of two
  parallel mechanisms.
- `native_modalities_json` — a column read in two places with **no writer
  anywhere**, making `classification.go:94-96` a dead branch. Either given the
  writer it needs from Phase 1's modality data, or dropped. Phase 1 supplies
  real modality data, so it is wired, not dropped.
- `WitnessVisionAnswer` stays, but stops being a dead constant.

**Unified:**

- The two independent copies of `ContextProvenanceMark`
  (`ModelTestReport.tsx:27-46` and `ModelsSurface.tsx:144-168`) collapse into one
  design-system component.
- "Not rated" vs "Not rated — unknown" — one string.
- Both surfaces read one field per fact. Today the group header reads
  `quality_rating` while the offering row reads `quality_score`; they agree only
  by server-side construction.

**Live Models becomes display-only.** No actions, no probe column, no
"Needs review" affordance that implies the owner must act. It reports what the
pipeline determined.

**Model Test Report becomes Model Report** — a read-only per-provider view whose
purpose is exactly the owner's stated need: confirm that enabling a provider
brought in everything. Counters become WORKING / UNKNOWN / ROUTABLE.

---

## 6. Edge cases

| # | Case | Behaviour |
|---|---|---|
| 1 | models.dev unreachable during discovery | No catalog facts this cycle. Phase 2 still runs, so chat/streaming/tools/score are known. Existing facts are **never** downgraded. Next discovery fills the rest. |
| 2 | Live model absent from models.dev (`qwen3.8-max`) | No catalog facts for it. Phase 2 proves what it can; context probe attempted; remainder displayed as unknown. Never inferred from its siblings. |
| 3 | models.dev has a model the provider does not list | Not discovered. The live list is authoritative for existence. |
| 4 | Provider key absent from models.dev entirely | All that provider's models are qualification-only. |
| 5 | Base-URL verification fails | Key refused, no enrichment for that provider, logged. Fail closed. |
| 6 | Account unhealthy or credential expired at connect | Phase 2 defers; retried by the maintenance tick. Nothing marked unsupported. |
| 7 | Quota exhausted / rate limited during qualification | Defer and reschedule. Never a capability verdict (`04 §2` hard rule). |
| 8 | Provider returns 200 with an empty body | chat is **not** proven. Verified legacy rule. |
| 9 | Model supports tools but emits no tool call for our fixture | Stays catalog-declared. A non-definitive signal never downgrades a declaration. |
| 10 | Model withdrawn by the provider later | `availability = withdrawn`; last known capabilities retained, offering not routable. |
| 11 | Same model on two accounts | Two offerings, independently qualified. Scores may legitimately differ — they reflect that account's real performance. |
| 12 | Many accounts connect at once | Existing per-provider lane, per-account concurrency cap, AIMD pacer and breaker. |
| 13 | Catalog says 1M context, measurement says 200K | Measurement wins — narrower beats broader. |
| 14 | Catalog says no vision, model actually has it | Stays unknown/unsupported until the catalog corrects. We do not guess upward. |
| 15 | Dataset entry marked deprecated | Model still discovered if live; flagged deprecated. Liveness beats catalog for existence. |

---

## 7. Testing strategy

Strict TDD, per the project's standing constraints. No live network in tests.

- **Catalog resolution** — a vendored models.dev fixture (a trimmed subset, real
  shape, real entries) exercised for: exact-key hit, key miss, base-URL mismatch
  refusal, empty-`api` skip, deprecated entry, image-output model.
- **Mutation proofs** — per the recorded lesson that test-owned fixtures have
  twice hidden real defects, every fix mutates the **composition root**, and each
  test must be shown to fail when the production behaviour it claims to pin is
  removed. No assertion may compare production output to a constant that
  production itself supplies.
- **Probe port widening** — a fake transport asserting that `Tools`, `Parts`,
  `Stream` and the real `Operation` actually reach `NormalizedRequest`. This is
  the specific defect being fixed, so it gets a direct guard.
- **Qualification** — table-driven over the edge cases in §6, especially the
  never-downgrade invariant and the infra-failure-is-not-a-verdict rule.
- **Routability** — a test that fails if `NativeCapabilities` or
  `TransportOperations` is ever passed as `nil` from the composition root again.
- **Deletion safety** — the removed endpoints must have their route registrations
  and tests removed together; a lingering test proves nothing about deleted code.

Full `task gate` on Windows is the only claim-worthy check.

---

## 8. Phasing

Each phase ships green on its own.

| Phase | Content | Owner-visible result |
|---|---|---|
| **1** | Routability wiring; streaming from the transport | ENABLED becomes a real number; `streaming` appears |
| **2** | Catalog resolution: extended facts, verified key map, all seven catalog-derived operations, eight operation rows, context and limits | Capabilities and context appear complete, each labelled with its source |
| **3** | Probe-port widening; automatic qualification pass; score produced automatically | Declarations upgrade to measured; every model has a score |
| **4** | Deletion and unification; Live Models becomes display-only; cost chip removed; score renamed and unified | Cards are clean, consistent and honest |
| **5** | Documentation: the corrected rule, the false-premise correction, the removal decisions | No future reader is misled |

**Phases 1 and 2 ship together as one unit of work.** An earlier draft of this
document claimed they were independent; reading `intelligence.Project`
(`readmodel.go:190-193`) while writing the implementation plan disproved that:

```go
effective := native != nil && transport != nil &&
    containsOperation(native, op) &&
    containsOperation(providerExposed, op) &&
    containsOperation(transport, op)
```

`effective` requires a non-nil `NativeCapabilities`, and the only source for it
is catalog resolution. Wiring `TransportOperations` alone would leave `routable`
false and Phase 1 would deliver nothing visible. Catalog resolution therefore
lands first, and the routability wiring lands on top of it.

Phase 3 depends on Phase 2 only for knowing which models need a context
measurement.

### 8.1 The capability axes, resolved

The three-way intersection is only meaningful if each axis means something
distinct. As implemented they are:

| Axis | Meaning | Source after this work |
|---|---|---|
| `NativeCapabilities` | what the canonical model can do | the resolved set — see below |
| `Offering.Capabilities` | what **this provider account** exposes of it | provider wire declarations **∪** catalog declarations for that provider key |
| `TransportOperations` | what **our code** can actually carry | `execution.InferenceTransport.SupportedCapabilities(route)`, corrected per §4.3.1 |

**The native axis is deliberately collapsed onto the offering axis, and no new
column is added.** A canonical model id is `models.CanonicalKey(providerID,
providerModelID)` — it is already provider-scoped. There is therefore no
model-level capability fact that differs from the resolved offering-level fact,
and a `native_capabilities_json` column would be ceremony: a second store of the
same list, with a second writer to keep in sync and a second way to drift.
`NativeCapabilities` is passed the resolved capability set, making `effective`
evaluate to `resolved ∩ transport`, which is the correct semantics.

The `native != nil` guard keeps its meaning: it still fails closed when nothing
has been resolved. What changes is that resolution now actually happens, so the
guard stops being permanently tripped.

The union in the middle row is the load-bearing decision. A thin wire that
returns `{id, name}` is **silence, not denial** — and models.dev's `cline-pass`
key is provider-scoped evidence about that provider's own offerings, not generic
information about a model elsewhere. Treating the wire's silence as absence is
what produces today's chat-only ClinePass fleet.

A capability the transport cannot express is correctly not effective, even when
both the model and the provider support it: we cannot route what we cannot send.

---

## 9. Out of scope

- A reasoning measurement — `NormalizedResponse` carries no reasoning field.
  Catalog-declared, labelled.
- An image-generation measurement — no image output parts on the response.
  Catalog-declared, labelled.
- Routing `reasoning` or `image_generation`. Per owner decision they are
  displayed and certified but not routed in V1, matching
  `docs/05-tier-engine.md` §9. No change to that document is required.
- A second external registry. models.dev covers every provider we have except
  agnes-ai (§4.1.1); adding another registry for one provider is unjustified
  complexity.
- **Widening the eight-operation vocabulary.** `claude-code` declares
  `documents` (PDF input), `thinking` and `agents` on its wire, and models.dev
  carries `pdf` as an input modality for anthropic models. All are dropped by
  `ParseOperation`. `thinking` is already represented by `reasoning`; `agents`
  is not an operation. `documents` is a genuine capability, but adding a ninth
  operation ripples through certification, admission and the tier engine for
  something V1 does not route. Recorded as a deliberate deferral, not an
  oversight.
- Bifrost's model catalog. Rejected with evidence in §2.5.
- Re-tuning the routing weights beyond removing the double-counted latency term.

---

## 9.1 Delivered so far, and what the next plan inherits

Phases 1 and 2 shipped as `docs/superpowers/plans/2026-08-06-catalog-resolution-and-routability.md`
(commits `44042ea..af6431c`, nine tasks plus a final-review fix wave). ClinePass
models now report their catalog capability set and a real context window, and
`routable` is a real value rather than a hardcoded `false`.

**Known gaps carried forward, each a deliberate decision rather than an oversight:**

| Gap | Why it is deferred |
|---|---|
| `vision` is routable on `anthropic_messages` and `google_generate_content`, but those codecs accept only the inline base64 + media-type image form and reject a URL-only part. A client posting an OpenAI-standard `image_url` with an `http(s)` URL gets a typed request-feature-unsupported failure. | The honest fix is to normalize URL images to base64 before dispatch, which means our server fetches client-controlled URLs — an SSRF surface that needs its own design decision, not an inline patch. |
| `structured_output` is carriable only over the two OpenAI-shaped codecs; on anthropic and google it is certified but never routable. | Closing it means mapping the operation onto each provider's native mechanism (Anthropic uses tools; Gemini uses `responseSchema`). Separate work. |
| `reasoning` is certified but never routable in any build. | The transport seam has no way to express a reasoning request. Recorded at `internal/httpapi/models.go`'s `transportOperationsFor`. |
| Only ClinePass consumes the catalog through a provider-verified key. gemini-cli, claude-code and agnes-ai are still on their own wire metadata alone. | ClinePass was the deliberate pilot; replication is mechanical now that the shape is reviewed. agnes-ai has no models.dev entry at all (§4.1.1). |
| gemini-cli declares `thinking` on its wire and we still ignore it. | Belongs with the adapter-replication work above. |
| `CandidateOperations` now has zero production producers. | The mechanism, its parsing, and the storage guard are all still correct and are kept; only the docs were updated to say it is currently unused. |

**Still unbuilt from this spec:** Phase 3 (probe-port widening and the automatic
qualification pass that produces `performance_score`), Phase 4 (deletion and
unification across both UI surfaces, cost chip removal, score renaming), and
Phase 5 (the remaining documentation corrections).

## 10. Documents amended by this work

- `docs/superpowers/plans/2026-08-05-hybrid-capabilities-and-context.md:15` —
  the metadata rule, replaced by §3.
- `docs/superpowers/plans/2026-08-05-hybrid-capabilities-and-context.md:77` —
  the false claim that clinepass has no models.dev entry, corrected with the
  evidence in §2.4.
- `docs/04-model-intelligence.md` §4 — tier 3 (our own transport) added to the
  precedence ladder.
