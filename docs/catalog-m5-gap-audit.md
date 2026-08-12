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
