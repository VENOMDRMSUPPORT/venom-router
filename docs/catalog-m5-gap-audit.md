# Catalog M5 — final gap audit, before any probe is written

Measured 2026-08-12 against `catalog/data/catalog.db` (read-only) and against every
unauthenticated source live. Every number below carries the query or the call that
produced it, because a gap report reasoned from memory of the schema is how the
last nine-task run produced seven controller-authored defects.

No probe has run. No credential has been used. Nothing was written to the database.

---

## 0. Baseline

| Metric (as the code defines it) | Value | Source of the number |
|---|---|---|
| Active model offerings | **116** (zen 61, go 25, ollama 18, clinepass 12) | `SELECT COUNT(*) FROM models WHERE status='active'` |
| `qualityScored` = VQ value not null | **100 / 116 = 86.2 %** | `read-model.ts:291` definition, counted per provider |
| `operationalScored` = VO value not null | **116 / 116 = 100 %** | `read-model.ts:347` |
| `catalogReady` = the 7-field completeness gate | **109 / 116 = 94.0 %** | gate at `read-model.ts:234-245`, union of the missing sets below |
| Open ambiguous identities | **0** | `identity_review` is empty |
| Calibration in force | `cal-52-0.10178--83.31`, n=52, rho 0.925, LOO 5.74 vs baseline SD 14.05, accepted | `calibrations` |

Per provider `qualityScored`:

| Provider | Scored | Live | % | Unrated |
|---|---|---|---|---|
| ClinePass | 12 | 12 | **100 %** | 0 |
| Ollama Cloud | 16 | 18 | 88.9 % | 2 |
| OpenCode Go | 22 | 25 | 88.0 % | 3 |
| OpenCode Zen | 50 | 61 | 82.0 % | 11 |

ClinePass already meets the target. The other three do not, for the reason in §2.

---

## 1. The missing-value surface is 13 facts, not a catalog-wide hole

Fields with **zero** nulls across all 116 active rows: `context`, `tools`,
`reasoning`, `modalities`, `cost_kind`. Those are done.

Everything still unresolved:

| Field | Nulls | Models |
|---|---|---|
| `structured` | 7 | ollama/`deepseek-v4-flash:preview`, ollama/`mistral-large-3:675b`, go/`hy3-preview`, go/`mimo-v2-omni`, go/`qwen3.5-plus`, zen/`claude-sonnet-4`, zen/`qwen3.5-plus` |
| `attachment` | 3 | clinepass/`cline-pass/qwen3.8-max`, ollama/`deepseek-v4-flash:preview`, go/`hy3-preview` |
| `maxOutput` | 2 | ollama/`deepseek-v4-flash:preview`, go/`hy3-preview` |
| `cost_kind = unknown` | 1 | go/`hy3-preview` |

The completeness gate covers `structured`, `maxOutput` and `cost` but **not**
`attachment`. The union of the gate-relevant nulls is exactly **7 models**, which
is why `catalogReady` is 109/116. Closing 11 facts on 7 models takes it to 100 %.

### Why each `structured` null exists — three different causes, three different fixes

Asked of every free source (`gap3.mjs`: `loadSpecs` + `loadBenchmarks` + the
identity resolver + the provider's own detail endpoint):

| Model | Own models.dev entry | Pooled declaration | OpenRouter `supported_parameters` | Cause |
|---|---|---|---|---|
| ollama/`deepseek-v4-flash:preview` | absent | none at all | no record | **nobody publishes it** |
| ollama/`mistral-large-3:675b` | present, no `structured_output` key | only Ollama declares this model, and not this field | no record | **nobody publishes it** |
| go/`mimo-v2-omni` | present, no `structured_output` key | conflicts on `inputModalities`, silent on `structured` | no record | **nobody publishes it** |
| go/`hy3-preview` | absent | **conflict** on `structured` | lists no `response_format` | **sellers disagree** |
| go/`qwen3.5-plus` | present, no key | **conflict** on 5 fields | no record | **sellers disagree** |
| zen/`qwen3.5-plus` | present, no key | **conflict** on 5 fields | no record | **sellers disagree** |
| zen/`claude-sonnet-4` | present, no key | **conflict** on `structured` | lists no `response_format` | **sellers disagree** + vendor doc unasked |

`hy3-preview` is the case already documented in `models-dev.ts:136-143`: aihubmix
says true, kilo and openrouter say false. The pooling code correctly refuses to
pick a winner. Nothing records that it refused — see §3.

### One null closes for free, today

go/`hy3-preview` `attachment`: the intrinsic pool carries a **unanimous**
`attachment=false` from `tencent-tokenhub/hy3-preview`, with no conflict. It is
discarded because **`attachment` has no resolver at all** — `enrich.ts:118-141`
resolves seven fields and `attachment` is not among them, and
`resolveCapability` accepts only `tools | structured | reasoning`. The field is
declared on `IntrinsicFacts` (`models-dev.ts:76`), pooled (`:117`), and then
dropped. This is a free win and a provenance hole at the same time.

---

## 2. VQ: the 16 unrated, re-verified from source rather than inherited

Independently re-derived (not copied from `c96fad2`): **10 have no resolvable
identity in any index; 6 resolve exactly and carry an empty benchmark object.**

Identity resolved, zero benchmark fields — `{i:undefined, c:undefined, a:undefined, d:undefined}`:
zen/`gpt-5-codex` → `openai/gpt-5-codex:batch`, `gpt-5.1-codex-max`,
`gpt-5.2-codex`, `gpt-5.4-pro`, `gpt-5.5-pro`, and `laguna-s-2.1-free` →
`poolside/laguna-s-2.1` (free-variant rule).

No identity anywhere: ollama/`deepseek-v4-flash:preview`,
ollama/`mistral-large-3:675b`, go/`mimo-v2-omni`, go/`mimo-v2-pro`,
go/`qwen3.5-plus`, zen/`big-pickle`, zen/`gemini-3-flash`, zen/`gemini-3.1-pro`,
zen/`gpt-5.3-codex-spark`, zen/`qwen3.5-plus`.

Re-confirmed source facts:

- OpenRouter carries a benchmark for a minority of its own catalogue:
  intelligence 136/410, coding 160/410, agentic 138/410, designElo 150/410.
  Not covering these 16 is not an anomaly.
- models.dev has **no benchmark field of any kind** across 6292 entries. The full
  model-level key set is: `attachment, cost, description, experimental, family,
  id, interleaved, knowledge, last_updated, limit, modalities, name,
  open_weights, provider, reasoning, reasoning_options, release_date, status,
  structured_output, temperature, tool_call`.

### A micro-probe cannot produce VQ, and that is a constraint conflict, not a gap

VQ is a quality figure. The only states the mandate allows are MEASURED,
CALIBRATED, or PUBLISHED. Calibration needs an upstream index value, and there is
none for these 16. Producing a MEASURED value means running an evaluation — which
Rule #1 forbids in every form it could take (no benchmark, no long generation, no
thousands of tokens, no repeated sampling), and which the reasoning rule forbids
explicitly: a probe proves capability and transport behaviour, never quality.

So **VQ = 100 % is unreachable inside M5 as specified.** The mandate already
states the correct outcome for that case: keep VQ unknown and record the reason
where diagnostics can show it. Two escalation routes exist and both need an
explicit decision — see D1.

### CORRECTION to the c96fad2 audit: "the exact model is absent every time" is partly wrong

That claim was re-tested token by token against all 410 OpenRouter ids and
`canonical_slug`s. OpenRouter **does** carry benchmarked records for four of the
16, under dated or channel-suffixed ids the deterministic rules never tried:

| Catalog row | Upstream candidate(s) | Benchmark |
|---|---|---|
| ollama/`deepseek-v4-flash:preview` | `deepseek/deepseek-v4-flash-0731` | i 51.8, c 69.1, a 48.4, Elo 1252.8 |
| | `deepseek/deepseek-v4-flash` | Elo 1203.75 |
| zen/`gemini-3-flash` | `google/gemini-3-flash-preview` | Elo 1219 |
| zen/`gemini-3.1-pro` | `google/gemini-3.1-pro-preview` | i 47.7, c 68.8, a 23, Elo 1281.9 |
| go+zen/`qwen3.5-plus` | `qwen/qwen3.5-plus-02-15` | Elo 1171.9 |
| | `qwen/qwen3.5-plus-20260420` | none |

`designElo` is calibratable — `computeVQ` maps it through `cal-52` at
`venom-score.ts:110-128`, and the only excluded group is `mistralai`. So these
are live VQ candidates, not curiosities. **None may be bound by a machine**: the
candidates are dated snapshots whose scores differ by up to 12.6 points, which is
the exact failure the fuzzy-matching ban exists to prevent.

Provider-native evidence settled one of them outright. Ollama's roster lists
`deepseek-v4-flash:0731` **and** `deepseek-v4-flash:preview` as two separate
offerings, with different `created` stamps — 2026-07-31 and 2026-04-24. The
`:0731` row already binds to `deepseek/deepseek-v4-flash-0731` and scores a
measured 51.8. `:preview` is therefore an earlier snapshot with no dated upstream
counterpart, and binding it to either candidate would attach another release's
score. It stays unrated **because that was proven**, not assumed.

### The 16, re-partitioned by what the evidence actually supports

- **12 proven unrated.** ollama/`deepseek-v4-flash:preview` (distinct snapshot,
  above); ollama/`mistral-large-3:675b` (absent upstream *and* in the one
  calibration-excluded group); go/`mimo-v2-omni`, go/`mimo-v2-pro` (no
  `xiaomi/mimo-v2-*` upstream at all — only v2.5, which is bound and scored);
  zen/`big-pickle`, zen/`gpt-5.3-codex-spark` (absent from every index); and the
  six OpenAI/poolside variants whose records exist with every benchmark field
  empty — `gpt-5-codex`, `gpt-5.1-codex-max`, `gpt-5.2-codex`, `gpt-5.4-pro`,
  `gpt-5.5-pro`, `laguna-s-2.1`.
- **2 decidable by human overlay.** zen/`gemini-3-flash` → calibrated ≈ 40.8;
  zen/`gemini-3.1-pro` → measured 47.7. Precedent is strong: `gemini-3.5-flash`,
  `gemini-3.5-flash-lite` and `gemini-3.6-flash` all bind exact and score. The
  open question is only whether the provider's unsuffixed id denotes the upstream
  `-preview` channel, which is a documentation question for a human.
- **2 genuinely ambiguous.** go/`qwen3.5-plus` and zen/`qwen3.5-plus`: two dated
  upstream snapshots, one benchmarked, and no provider-native version evidence —
  both OpenCode rosters stamp every entry with one identical build timestamp.
  These belong in `identity_review` and will likely stay unrated.

### Additional free sources, measured for containment before any bridge is built

| Source | Reachable | Contains any of the 13 remaining tokens |
|---|---|---|
| LMArena `/leaderboard` | 200, 5.1 MB | **yes — 6**: `deepseek-v4-flash`, `mistral-large-3`, `mimo-v2-omni`, `mimo-v2-pro`, `gpt-5.1-codex-max`, `gpt-5.2-codex` |
| HuggingFace open-LLM leaderboard | 200 | none |
| LiveBench | 200 | none |
| SWE-bench experiments | 200 | none |
| EpochAI dashboard | 404 | — |
| Artificial Analysis API | 401 | — |

LMArena is the only one worth anything, and it could plausibly reach four
currently-unrated rows (`mimo-v2-omni`, `mimo-v2-pro`, `gpt-5.1-codex-max`,
`gpt-5.2-codex`). `mistral-large-3` cannot benefit: its group is excluded from
calibration, so an Elo would still yield no value. Adding LMArena means a new
fetcher, a new identity index, and a **second calibration bridge** fitted and
gated exactly like design_arena was — `rho ≥ 0.85` and `looRmse / baselineSd ≤
0.55` — and validated the way it will be used, never by LOO.

---

## 3. Two defects that matter more than the 13 missing values

### 3a. 102 of 116 models carry a source conflict that is recorded nowhere

Measured by replaying `specs.intrinsic()` over every active row:

| Field | Models with a discarded conflict |
|---|---|
| `inputModalities` | 76 |
| `structured` | 66 |
| `attachment` | 62 |
| `reasoning` | 42 |
| `tools` | 23 |

**102 of 116 models** carry at least one. `settle()` resolves a disagreement to
unknown, which is the right call and satisfies "no silent winner". But
`enrich.ts` never reads `intrinsic.conflicts`, there is no conflict table
(`model_conflicts` does not exist), and no API field exposes one. So the
disagreement is invisible: a `—` caused by two sellers contradicting each other
is indistinguishable from a `—` nobody ever published. Acceptance criterion 3
asks for the conflict to be visible with both sources and both values; today
neither side is retained.

### 3b. 219 displayed values have no provenance row

| Displayed field | Values stored | `model_facts` rows |
|---|---|---|
| `attachment` | 113 | **0** |
| `ref_cost_in_per_m` | 106 | **0** |
| `cost_kind` | 116 | 0 as its own field (folded into the `cost` blob) |

Acceptance criterion 11 is "every resolved field has provenance". `attachment`
and the reference price are shown to the user and traceable to nothing.
`billingKind`, `effectiveProviderPrice` and `referencePrice` are three distinct
facts with three different sources, written as one `cost` JSON value under a
single source label (`enrich.ts:141`).

Also missing from `model_facts` against the mandate's provenance contract:
`source_url`, `resolver_version`, `probe_version`, evidence/confidence state, and
any raw-evidence hash. The table has `field, value, source, source_ref,
resolved_at` only.

### 3c. Two latent cross-provider leaks in provider-specific resolvers

`resolveContext` falls back to `canonical.contextLength` and `resolveMaxOutput`
to `canonical.maxCompletionTokens` — both OpenRouter, i.e. **another seller's**
serving limit for a provider-specific field, which acceptance criterion 6
forbids. Currently inert: measured sources are `context` = models.dev 114 +
provider_api 2, `maxOutput` = models.dev 114, zero from OpenRouter. It fires the
moment a row has an OpenRouter entry but no models.dev entry — exactly the shape
of `hy3-preview` and `qwen3.8-max`. `maxOutput` is the worse of the two: the
OpenRouter field is literally named `top_provider.max_completion_tokens`.

---

## 4. The Venom Router still reads models.dev at runtime

Acceptance criteria 14-17 require the Go router to read the catalog only.
Measured: `internal/httpapi/opencode_zen_seams.go:233` defines
`https://models.dev/api.json`, fetched by `openCodeZenModelsDevProbeSeam`
(`:239`), which is wired into **four** provider registrations at runtime —
`RegisterOpenCodeZen`, `RegisterClinePass`, `RegisterOllamaCloud`,
`RegisterNvidiaNIM` — feeding free-safety and qualification decisions. The
runtime dependency the mandate forbids is live today, in the direction the
mandate calls wrong.

---

## 5. Probe plan

Every fact below survived §1's free-source exhaustion. Nothing here is probed to
confirm something a source already answers.

| # | Provider / model | Field | Requests | Expected billed tokens | Yield |
|---|---|---|---|---|---|
| 1 | ollama / `deepseek-v4-flash:preview` | structured | 1 | ≤ 46 | likely |
| 2 | ollama / `deepseek-v4-flash:preview` | maxOutput | 1 | 0 (400 expected) | uncertain |
| 3 | ollama / `mistral-large-3:675b` | structured | 1 | ≤ 46 | likely |
| 4 | go / `hy3-preview` | structured | 1 | ≤ 46 | likely |
| 5 | go / `hy3-preview` | maxOutput | 1 | 0 (400 expected) | uncertain |
| 6 | go / `mimo-v2-omni` | structured | 1 | ≤ 46 | likely |
| 7 | go / `qwen3.5-plus` | structured | 1 | ≤ 46 | likely |
| 8 | zen / `qwen3.5-plus` | structured | 1 | ≤ 46 | likely |
| 9 | zen / `claude-sonnet-4` | structured | 1 | ≤ 46 | likely (vendor doc may pre-empt) |
| 10 | clinepass / `cline-pass/qwen3.8-max` | attachment | 1 | ≤ 150 | likely |
| 11 | ollama / `deepseek-v4-flash:preview` | attachment | 1 | ≤ 150 | likely |

**11 probes, 9 distinct models, 3 fields. ≈ 620 billed tokens total.** With one
retry allowed per transient failure only: hard ceiling **22 requests, ≈ 1300
tokens**. `go/hy3-preview` `cost_kind` is **not** in this table — pricing is not
probeable; it needs the opencode.ai/zen/go pricing page read by a human.

Not probed, deliberately: `attachment` for go/`hy3-preview` (free win, §1),
`context`/`tools`/`reasoning`/`modalities` (zero nulls), VQ for all 16 (§2),
identity for the 10 (Rule #5).

### Probe shapes

- **structured** — one request carrying `response_format` with a two-property
  schema, `max_tokens: 16`. `true` on accepted + schema-conformant parse. `false`
  **only** on an explicit provider rejection of the parameter. Malformed request,
  5xx, timeout and rate-limit are `probe_failed`, and the value stays unknown —
  transient failure is never unsupported.
- **maxOutput** — one request with an out-of-range `max_tokens` and a 1-token
  prompt, reading the limit out of the 400 body. Costs nothing when it errors. If
  the provider clamps silently instead of erroring, the fact stays unknown with
  reason `probe_inconclusive`; discovering it by generating is not permitted.
- **attachment** — one request with a 1×1 PNG data URI and `max_tokens: 8`. No
  image is generated; one is sent.

### Budget policy

`MAX_PROBE_REQUESTS_PER_RUN` derived from the plan, not chosen: `2 ×
(facts still missing and probe-eligible)`, ceiling 24. Per model:
`probe_count, probe_cost_estimate, last_probe_at, probe_version, probe_result,
probe_failure`. Probe TTL so a fresh fact is never re-probed. Stop conditions:
budget exhausted, quota error (stop that provider), auth failure (stop that
provider), rate limit (respect `Retry-After`, no storm). Credentials from
environment only; probes are an optional layer and the UI, read API, history and
diagnostics must all still run with zero secrets present.

---

## 6. Projected end state

| Metric | Now | After M5 as specified | Gate |
|---|---|---|---|
| `catalogReady` | 109/116 = 94.0 % | **116/116 = 100 %** | 9 probes land + 1 pricing doc read + attachment resolver |
| `qualityScored` | 100/116 = 86.2 % | **102/116 = 87.9 %** | + the two reviewed gemini overlay bindings |
| `qualityScored` if the LMArena bridge is built and passes its gates | | **106/116 = 91.4 %** | + `mimo-v2-omni`, `mimo-v2-pro`, `gpt-5.1-codex-max`, `gpt-5.2-codex` |
| Conflicts visible | 0 of 102 | **102 of 102** | conflict table + API + diagnostics |
| Fields with provenance | 7 | **11+** | attachment, billingKind, effective price, reference price |
| Provider-specific leaks | 2 latent | **0** | drop both OpenRouter fallbacks |
| Router reads catalog only | no | no | separate workstream, §4 |

Per-provider `qualityScored` after the two gemini bindings: ClinePass 100 %,
Ollama Cloud 88.9 %, OpenCode Go 88.0 %, OpenCode Zen 85.2 %. With the LMArena
bridge: OpenCode Go 96.0 %, OpenCode Zen 88.5 %.

The honest headline: **catalog completeness reaches 100 %. Quality coverage
reaches 88-91 %, and the remaining 9-14 rows each carry a proven reason** — a
distinct release snapshot, a model absent from every index, a vendor the
calibration was measured to have no predictive power for, or a variant the
benchmark houses skip. None of them is a `—` we were too lazy to chase; every one
was asked of every source that exists for us, and the question was recorded.

`gpt-5.4-pro` and `gpt-5.5-pro` deserve one note: OpenRouter carries the record
and leaves every benchmark field empty, and LMArena does not list them either.
Artificial Analysis measures base models and skips pro tiers. A reviewed *lower
bound* from the measured base sibling is the one mechanism `computeVQ` already
supports for this shape (`level: 'bounded'`, `venom-score.ts:131-143`) and it is
never inferred automatically — it needs the same human review as an overlay.

---

## 7. Addendum, 2026-08-18 — the vendor's own storefront as a last-resort source

Written after this audit, against a catalog the free-only publish policy has
since narrowed to 65 offerings. Two of the gaps §1 counts had a cause the audit
did not name, and it is not one more probe.

**The condition.** `cline-pass/glm-5.3` and `cline-pass/qwen3.8-max` carried no
`context` and no `maxOutput`. Verified on the day, against the live sources
rather than from memory:

- ClinePass's roster endpoint returns `{id, name, description, tags}` and nothing
  else — for all thirteen of its models, not just the new two. Its second
  endpoint (`/api/v1/ai/cline/models`) carries **no** `cline-pass/` id at all: it
  is ClinePass's mirror of the OpenRouter index for the bring-your-own-key model
  picker, so it says nothing about what ClinePass serves.
- `models.dev` had not yet listed either model under its `cline-pass` provider.

So no seller of these two deployments published a limit anywhere. §5's probe plan
cannot close this: there is nothing to read.

**What was added.** One source, ranked last, behind its own evidence state.
When the model's own vendor sells it from the vendor's own storefront in the same
feed, that figure is about the model rather than about somebody's deployment of
it, and it is adopted as `vendor_default` — never as `first_party`, which would
claim this host confirmed it.

| Piece | Where |
|---|---|
| Which storefronts are a vendor's own, and which id namespaces mark its models | `catalog/overlays/vendors.json` |
| Membership + declaration collection | `firstPartyLimits` in `catalog/sync/sources/models-dev.ts` |
| The adoption rule — unanimity, else nothing | `adoptFirstPartyLimit` in `catalog/sync/enrich/resolvers.ts` |
| Documentation URL | read from each storefront's own `doc` field in the feed, never kept by hand |

Membership is read from the feed rather than asserted: `glm-5.3` is a Z-AI model
because some seller lists it as `zai-org/glm-5.3`. That is what keeps
`alibaba/glm-5.2` — Alibaba reselling a Z-AI model — from passing as a
first-party GLM figure just because Alibaba is a vendor of other models.

**A provenance defect found on the way, and fixed.** `enrich` writes what it
resolved back into `models.context_tokens`, and it used to read that same column
back as the provider's models.dev entry. The pipeline enriches twice, so the
second pass re-credited every fallback-derived value to a source that had not
published it — a vendor ceiling arriving labelled as the provider's own figure,
which is the exact claim this design exists to avoid. `enrich` now takes
`lookupSpec` and reads the feed. Pinned by *"a second enrichment keeps the
vendor-default provenance it recorded on the first"* in `sync/enrich/enrich.test.ts`.

**Where completeness actually stands.** 58 of 65 catalog-ready, from 56. The
seven that remain are not one class:

| Rows | Missing | Why |
|---|---|---|
| 3 (`deepseek-v4-pro:0813`, `deepseek-v4-flash:preview`, `deepseek-v4-pro:preview`) | `maxOutput` | DeepSeek's own store publishes `384000` for `deepseek-v4-pro`, but these ids carry a release tag and identity normalisation refuses to collapse version tokens — deliberately. Deciding that a dated variant inherits the base model's limits is an owner decision, not a bug fix. |
| 4 (`mistral-large-3:675b`, `hy3-preview`, `mimo-v2-omni`, `qwen3.5-plus`) | `structured` | **No source publishes it.** Not the host, not the pool across 191 providers, not the vendor's own storefront — checked field by field. |
| 1 (`hy3-preview`) | `cost` | No published price. |

So §6's *"catalog completeness reaches 100 %"* does not hold, and not for want of
probing: four of the seven remaining gaps are facts the industry has not
published about the model at all. The honest ceiling from published evidence is
61 of 65, and reaching it needs an owner decision about release-tagged variants —
not more sources.

---

## 8. Addendum, 2026-08-18 — the reviewed bound, activated

§6 named a mechanism `computeVQ` "already supports" for a model with no
published figure: a reviewed one-sided bound, `level: 'bounded'`. Two things
were wrong with that sentence.

**It was dead code.** `evidence.bound` was in the type and nothing ever
populated it — `scoreAll` built `{direct, calibratable, group}` and stopped
there. A unit test of the branch passed the whole time, which is exactly why the
gap survived: the test could not tell a wired feature from an unwired one.

**And it was unreachable anyway.** `computeVQ` opened with

```ts
if (resolution.status !== 'resolved') return UNRATED;
```

so the bound branch could only ever be reached by a row that already had a
measured or calibrated figure available — the one case that does not need it.
Every row a bound exists for was returned unrated three lines earlier.

**What changed.** The identity guard now applies to what it is actually about:
`direct` and `calibratable` are derived FROM the resolved identity and stay
gated on it; a reviewed bound is not derived from an identity — it is a human's
claim about the row, made *because* no index carries the model — so it is
checked after both and reached whether or not identity resolved. Rows with
neither still return unrated with the same reason as before.

The bound writes **no `sourceModelId`**, deliberately and with a test on it:
`read-model.ts:429` derives `canonicalId` from that column, so naming the
reference model there would make the row assert that it *is* that model — the
bind the whole mechanism exists to avoid having to make. The reference lives in
`source`, rendered as `relation: …`.

| Piece | Where |
|---|---|
| The reviewed bounds | `catalog/overlays/quality-bounds.json` |
| Loader, shared by both entry points | `catalog/sync/quality-bounds.ts` |
| Wiring into scoring | `bounds` on `ScoringDeps`, `sync/score/pipeline.ts` |
| Ranking-order guarantee | `sync/score/venom-score.test.ts` — "a measured figure still outranks it" |

**First entry: `cline-pass/glm-5.3`, `≥ 52.6` from `z-ai/glm-5.2`.** It renders
as `≥ 53 BOUNDED`, ranks tied at #5 with the model it is bounded against, and
keeps `identityState: identity_review` and `canonicalId: null`. It supersedes
itself: a measured figure outranks a bound automatically, so the entry needs no
cleanup on the day the index finally lists GLM-5.3.

**One overstatement it introduced, and fixed in the same change.** Reaching
13/13 made the provider tile say "All models benchmarked externally" — the exact
claim the `bounded` label exists to avoid. The tile now counts bounds separately.
The same tile also stopped raising a defect warning for models unrated with a
recorded reason: it had been showing the identical triangle used for a provider
serving zero models, so a catalog behaving as designed read as a broken one. It
still raises it for an unrated row with **no** reason recorded — that one is
ours.

**The bound's basis, and where it is visible.** The owner supplied Z.ai's own
GLM-5.3 page, which turned the entry from an inference into a citation. Z.ai
documents GLM-5.3 as using *the same base model* as GLM-5.2 with every
improvement coming from post-training, and reports a 50% coding gain over it on
Z.ai Code Bench. That is why the bound is `≥ 52.6` and why it stays a bound: Code
Bench is the vendor's own scale, no calibration maps it onto this one, and the
true figure may be materially higher. The same page independently confirms the
limits the vendor-storefront fallback had already resolved — a 1M-token context
window, 128K max output, text-only input — so §7's mechanism was checked against
primary documentation after the fact and agreed with it.

The evidence panel renders that relation under the figure. Shown as a bare
`VQ ≥ 53`, a bound is indistinguishable from a measurement written with a sign,
which defeats the label; it is the one number on the page that comes from a
person rather than a source, so it is the one that most needs its basis on
screen. Pinned by *"the relation behind the figure is rendered, not just the
figure"* in `EvidencePanel.test.tsx`.

Not yet exposed: the overlay's `sourceUrl`. `VQ` carries no URL field, so the
link to Z.ai's page lives in `overlays/quality-bounds.json` and reaches no
reader. Threading it through `VQ` → `model_scores` → read-model → the panel is
the obvious next step and was left out of this change rather than bundled into it.

---

## 9. Addendum, 2026-08-18 — how a provider bills is a fact about the provider

Spotted by the owner from the rendered table, not from a failing test: two
ClinePass rows read `Included · n/a` while the other eleven read `$1.40`,
`$3.00`, `$0.14`. One provider, one subscription, two costing semantics.

**The mechanism.** `resolveCost` read the feed price first and fell back to the
declared billing policy only when none was published. So a row's costing
semantics tracked *models.dev's coverage* rather than anything about the world:
the eleven rows models.dev had priced became `per_token`, and the two it had not
yet added — the same two as §7 — fell through to `included`.

**Why that is worse than cosmetic.** `included` puts `cost` in
`notApplicableDimensions`, and VO renormalises the remaining weights. So those
two rows were scored on a different basis from the other eleven while sitting in
the same ranking, directly comparable in the UI and not comparable in fact. An
evidence gap was wearing the costume of a semantic answer — the exact confusion
`src/api/client.ts` warns about in its own comment: *"`missing` is open work
nobody published, `notApplicable` is a settled answer that the question does not
apply here."*

**Which half was wrong.** The eleven. From ClinePass's own documentation
(`docs.cline.bot/getting-started/clinepass`, read 2026-08-18):

> ClinePass is a flat monthly subscription, so **you are not charged the
> individual API prices below**. These **reference prices** show the underlying
> per-1M-token rates for each model and can help you understand how usage is
> measured against your ClinePass quota.

The published table is the quota metering rate. The catalog was printing it in
the effective-price column — "this is what it costs you here" — which is the one
claim the provider explicitly denies. Its figures match ours exactly (GLM-5.2
$1.40/$4.40, Kimi K3 $3.00/$15.00, Qwen3.8 Max $2.00/$6.00), so this is the same
table, read into the wrong field.

This overturns a *deliberate, tested* decision from 2026-08-12, whose reasoning
was: ClinePass's rates carry an exact 2x markup over vendor list, therefore they
are ClinePass's own charges. The markup is real; the conclusion was not — it is
the metering rate that is marked up. Both tests that pinned the old behaviour
were rewritten rather than deleted, each carrying the quotation that overturned
it.

**The rule now.** The declared billing model decides the kind for every one of a
provider's rows, and a published per-token figure under a plan is kept as the
reference it is:

| Declared policy | Every row | A published feed price | An absent price |
|---|---|---|---|
| `subscription` | `included` | becomes the reference | still `included` |
| `free_quota` | `free` | becomes the reference | still `free` |
| `per_token` | `per_token` | is the effective price | `unknown` — a real gap |

The last column is the point: for a per-token provider an absent price is open
work (`opencode-go/hy3-preview` is exactly this, and stays in needs-verification),
while for a plan it is a settled answer. Which of the two it is now depends on
the provider's billing, never on which rows a feed got around to pricing.

All thirteen ClinePass rows are now `included`, all thirteen renormalise `cost`
out of VO, and the ranking compares like with like. The free-quota half of the
rule is inert today — models.dev prices no Ollama Cloud model — which is why it
carries a test rather than a comment.

---

## 10. Addendum, 2026-08-18 — a fact true of every row belongs above the table

The owner's question about the rendered page: *what is text like `Included · n/a`,
`cost n/a`, `identity review (1 refused)` even for — why are they not values?*

It is the right question, and for two of the three the answer was: they are
values, printed in the wrong place and in the wrong vocabulary.

**`Included · n/a` and `cost n/a`.** After §9, a ClinePass row's costing is
identical to every other ClinePass row's — that is what a plan means. The page
printed it three times per row anyway: once in each price column and again as a
VO badge. Thirteen rows, thirty-nine copies of one provider-level sentence. And
it was written in the `n/a` vocabulary the catalog reserves for facts *nobody
published*, so a settled answer rendered as a page full of holes — the same
missing-versus-notApplicable confusion §9 fixed in the data, reappearing in the
presentation.

Stated once now, in a `Billing:` callout above the table, and the columns show
the figure that actually varies: `ref $3`, `ref $15`. `ScoreCell` takes a
`statedOnce` flag rather than deciding for itself, so nothing is hidden by
default — a provider whose rows genuinely disagree gets the per-cell labels back
automatically, because `uniformCostKind` returns null and the flag goes false.

**`identity review (1 refused)`.** This one *was* a value, and it stays. But the
column holds a canonical id on every other row, so the question it asks is
*which upstream model is this*; answering with the name of a workflow state
answers a different one. It now reads `no upstream match · 1 candidate refused`
— the same two facts, phrased as an answer about the model. The count stays
because "investigated" and "untouched" must remain tellable apart, which is the
entire reason the review state exists.

Nothing was removed from the page. `ref —` still marks a row for which no
reference rate is published, the evidence panel still carries every underlying
record, and every label change is pinned by a test that asserts the *replacement*
is present, not merely that the old string is gone.

---

## 11. Addendum, 2026-08-18 — measured against Cline's own method

The owner asked for the official repository's approach to be read and applied, so
the two catalogs would work the same way. Read at `cline/cline@main`, and the
answer is worth recording because it both **confirms** this catalog's sources and
**explains a number the owner saw in the extension that contradicts ours**.

**Their method.** `sdk/packages/llms/scripts/generate-models.ts` generates the
shipped catalog at build time from two inputs: `loadModelsDevCatalog()` — the
same `models.dev` feed this catalog reads — and the same
`/api/v1/ai/cline/recommended-models` roster. For a ClinePass row,
`sdk/packages/llms/src/catalog/catalog-cline-recommended.ts` then does:

```ts
const modelSlug = entry.id.split("/").at(-1) ?? entry.id;   // cline-pass/glm-5.3 -> glm-5.3
return openRouterModels[modelSlug] || CLINE_PASS_MODEL_DEFAULTS;
```

So: strip the provider prefix, match the bare slug against the OpenRouter
catalog, and on a miss fall back to a constant.

**What that validates.** Three of our four choices are the same by independent
arrival: models.dev as the spec source, the ClinePass roster as the inventory,
and matching a `cline-pass/X` row by its bare slug against a canonical index —
which is what `normalizeId` does for identity here.

**What we deliberately do not adopt.**

```ts
const CLINE_PASS_MODEL_DEFAULTS = {
  contextWindow: 128_000, maxInputTokens: 128_000, maxTokens: 8_192,
  capabilities: ["tools", "reasoning", "temperature"],
  pricing: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
}
```

A constant applied to **any** model the OpenRouter lookup misses. It is the
reason the extension's settings pane shows `cline-pass/glm-5.3` with
**Context: 128K**, Images No, Browser No, Prompt Caching No — none of which is a
statement about GLM-5.3. It is what a miss looks like when the miss is filled in.
Z.ai publishes a **1M-token context window and 128K max output** for that model,
and `$0/$0` pricing would make every ClinePass model read as free, which even
Cline's own documentation contradicts.

That constant is precisely the failure mode this catalog exists to prevent, and
it is invisible in their UI: a fabricated 128K renders identically to a measured
one. Our figure for the same field is 1,000,000 carrying `vendor_default`, which
says out loud that it is the vendor's figure for the model rather than a limit
ClinePass published — a distinction that matters here, because a host may serve
less than a model supports and nobody has published what ClinePass serves.

**One gap this comparison exposed in our own work.** `vendor_default` was added
to the resolvers in §7 and never added to the evidence panel's state glossary, so
it rendered as a bare token with an empty tooltip — the one state whose meaning a
reader most needs, unexplained. Fixed, with a test that walks every state a
resolver can emit and fails on any that has no words.

**A correction to §10's own wording.** The replacement label there,
`no upstream match`, contradicted the row it sat on. models.dev *does* identify
`cline-pass/glm-5.3` — it is listed under Z.ai's own storefronts, and that
listing is where the same row's 1M context and 128K max output came from. What
has no entry is the reference index, the one a canonical id and a benchmark
figure are drawn from. The label now names it: `not in the reference index · 1
candidate refused`. Saying "upstream" asserted that nothing anywhere knows this
model, printed beside two facts read from somewhere.

**And why that row's `ref —` stays empty.** Checked, rather than assumed: for
GLM-5.3, `nano-gpt` and `opencode-go` publish $1.40/$4.40, `vivgrid` publishes
$1.20/$4.20, and Z.ai's own two coding plans publish $0 because they are plans,
not rates. Three different answers, so the unanimity rule that governs every
other adopted figure declines this one too. `ref —` is the result of applying the
rule, not a gap nobody looked at.

---

## 12. Addendum, 2026-08-18 — two errors the rendered table exposed

Both found by the owner asking why GLM-5.2 appeared to beat GLM-5.3. It did not
— the two are tied at #5 — but the page gave two separate reasons to think
otherwise.

**A lower bound was rounded up.** Both rows carry `value: 52.6`. glm-5.2
rendered `52.6`; glm-5.3's bound — the *same 52.6*, copied from that very
measurement — rendered `≥ 53`. The bounded branch hardcoded `precision: 0`, a
rule written for calibrated figures whose ±5-8 points cannot justify a decimal.
A bound copied from a measured value inherits that value's resolution, and the
direction matters more than the digit: `≥ 53` asserts at-least-53 where the
evidence supports at-least-52.6, so the display invented a point of headroom in
the claim's own favour. Precision is now taken from the figure itself, and the
rows read `52.6` and `≥ 52.6`.

**The ranking never said what it ranks against.** A thirteen-row table numbered
#1, #2, #5, #5, #8, #9 … with an `=` on almost every row reads as broken. Both
facts are correct: the rank is over every scored model in the catalog, so
another provider's model occupies each gap, and — verified against the API —
every one of those ties is the *same model sold by another provider*
(`kimi-k3` at #1 is tied with `kimi-k3` at Ollama Cloud and OpenCode Go), which
the provider filter hides. So the reader saw a tie with nobody and numbering
that skipped for no visible reason. The scope is now stated once under the
table's title, in both the table and grid views.

Neither was a wrong number. One was a number rounded in the direction that
flattered it, and one was a correct number whose scope was never stated — and
an unstated scope is indistinguishable from a bug.

---

## 13. Addendum, 2026-08-18 — identity and benchmark linkage were one field

The owner's instruction: replace `not in the reference index · 1 candidate
refused` with `z-ai/glm-5.3`. Not a request to fabricate — that identity is
established. models.dev lists the model under Z.ai's namespace, and that very
listing is where §7 read the row's 1M context window. The row knew what it was
and the page would not say it.

**The cause.** `canonicalId` is `model_scores.source_model_id` — the
reference-index entry a SCORE was taken from — and the model column rendered it
as the row's identity. Two questions, one field. The docs have claimed since M5
that identity and quality are independent axes; this field is where they were
still fused, so a model no index had benchmarked appeared to have no identity at
all.

**Split.** `vendorIdentity()` in `sync/sources/models-dev.ts` answers *which
model is this* from the same vendor-namespaced listing that established
membership for the limits. `enrich` records it as a fact with provenance like
any other, `read-model` serves it as `vendorModelId` **beside** `canonicalId`,
never in place of it, and the column prefers the canonical id when there is one.
The two carry different titles, because rendering them identically would claim a
measurement that does not exist.

**Two things the registry had to declare, and one it must not build.** The id is
`${canonicalPrefix}/${row's own bare id}`. The prefix is declared per vendor and
read off the reference index — the registry key and the index prefix differ
(`alibaba` vs `qwen/`), so `${vendorId}/${slug}` would have invented
`alibaba/qwen3.8-max`. And the bare id comes from the ROW, never from the
listing: the first `zai-org/` listing of GLM-5.3 is nano-gpt's
`zai-org/glm-5.3:thinking`, which produced `z-ai/glm-5.3:thinking` — a
reasoning-mode variant presented as an identity — while another seller's
capitalisation produced `moonshotai/Kimi-K2.6` next to a canonical
`moonshotai/kimi-k2.6`. The listing establishes the vendor and is cited verbatim;
it does not get to spell the model's name.

**How the derivation is checked.** Against the live catalog, every ClinePass row
that has a canonical id from the reference index now also has an independently
derived vendor id, and the two agree **exactly** on all twelve. The thirteenth,
which the index does not carry, is `z-ai/glm-5.3`.

**The `ref` prefix moved to the column header.** Twenty-six copies of one
column-level fact, the same shape as §10's `Included · n/a`. It could not simply
be deleted: a bare `$3` under a plan reads as what you pay, the one claim
ClinePass's documentation denies. So the columns now read `In · ref` /
`Out · ref` and carry the explanation in their titles, and the cells show `$3`,
`$15`, `—`. One marker per column instead of one per cell.

**One request declined, and why.** The owner asked for `cline-pass/glm-5.3` to be
marked `MEASURED`. `MEASURED` means a published benchmark measured this exact
model; no reachable index carries GLM-5.3 — OpenRouter's 413 ids, its programming
category, Artificial Analysis (401), LMArena (no public JSON) were each checked.
Labelling a reviewed bound as measured would make a fabricated figure
indistinguishable from a real one, which is the failure this catalog is built to
prevent, and it is exactly the shape of `CLINE_PASS_MODEL_DEFAULTS` in §11. The
row is already ranked among the measured ones at #5=, and it becomes `MEASURED`
on its own the day an index publishes a figure — the bound is superseded
automatically. If a source does publish one, wiring it is a small change.
