# Venom Catalog Service & Venom Score — design

Status: proposed · Date: 2026-08-12 · Supersedes the hand-maintained catalog in
`catalog/src/data/catalog.ts` and the single-source sync in
`catalog/scripts/sync-ollama-cloud.mjs`.

Every number in this document was measured against live sources on 2026-08-12,
not assumed. The measurement scripts are reproducible and ship with the service
(`catalog/sync/analysis/`).

---

## 1. Why this exists

The catalog is hand-typed. It renders "58 Verified Models · 100% API Verified"
while the four providers actually serve **115**. Ollama Cloud — the one provider
already automated — was wrong by 4 models on the day its own doc claimed it was
verified, because it reads a single source.

The failure is structural, not clerical: **one source cannot answer two
questions.**

| Question | Answerable by |
|---|---|
| Which models exist right now? | the provider's own live API |
| What is this model? | `models.dev` (specs), OpenRouter (benchmarks, pricing) |

Measured proof of complementarity, same run, both directions:

- `models.dev` lists 26 OpenCode Zen models the provider no longer serves.
- The live OpenCode Go roster carries models `models.dev` has not yet indexed.

Neither source can produce the other's answer. The service therefore reads
both, and never lets one stand in for the other.

---

## 2. Architecture

A standalone Node service in `catalog/`, bound to loopback, owning a SQLite
database. The SPA and (later) the Go router are clients.

```
catalog/
├── sync/
│   ├── engine.ts            the ONE pipeline: fetch, validate, diff, gate, apply
│   ├── sources/
│   │   ├── models-dev.ts    specs        (183 providers)
│   │   └── openrouter.ts    benchmarks + pricing (406 models)
│   ├── providers/           one declarative adapter per provider (~30 lines each)
│   │   ├── opencode-zen.ts  ├── opencode-go.ts
│   │   ├── clinepass.ts     └── ollama-cloud.ts
│   ├── score/               identity → evidence → Venom Score
│   └── analysis/            the reproducible measurement scripts behind this doc
├── server/                  HTTP API + scheduler
├── db/migrations/
├── overlays/                human-reviewed facts no machine can derive
├── data/                    catalog.db + snapshot/   (git-ignored)
└── web/                     the existing SPA, reading the API
```

**One engine, N declarative adapters.** A provider adapter declares its roster
URL, its response parser, and its `models.dev` key. It contains no fetching,
retry, diffing or transaction logic — a second copy of those would be a defect
under `CLAUDE.md §7`, not a variation. Adding a fifth provider is one file, and
it inherits every guarantee below on day one.

### 2.1 Storage: SQLite is the truth, JSON is the transport

SQLite (`data/catalog.db`, `better-sqlite3`) because the requirements demand
things files cannot give: an event log queryable by date, atomic multi-row
application, and a format the Go router already speaks. A JSON snapshot is
regenerated from the DB after every successful sync, for the SPA's offline
fallback and for a human-readable `git diff` of what changed.

Derived data (the DB, snapshots) is git-ignored. Curated data (overlays) is
committed and reviewed. Nothing that a machine can derive is ever hand-edited.

---

## 3. Failure model — eight layers

No layer is optional; each catches a class the others cannot.

| # | Layer | Catches | Behaviour |
|---|---|---|---|
| 1 | Per-provider isolation | one provider's outage | independent fetch + independent transaction |
| 2 | Fetch discipline | network, timeout, 429 | explicit timeout, 3 retries with exponential backoff, honours `Retry-After`, `ETag`; total failure ⇒ **zero writes** |
| 3 | Contract validation | upstream changed shape | response validated against a schema before touching the DB; non-conforming ⇒ full rejection |
| 4 | Delta gate | catastrophic false deletion | a run removing >30% or >5 models is **quarantined**, recorded, not applied |
| 5 | Two-strike retirement | transient flapping | a missing model is marked `missing` with a counter; `retired` only after 3 consecutive absences |
| 6 | Atomicity | half-written state | one provider sync = one SQLite transaction |
| 7 | No physical delete | irreversible loss | rows only transition `active → missing → retired`; there is no `DELETE` |
| 8 | Read path is offline | API down because a provider is down | the API always serves last-known-good plus `stale_since` |

Layers 4 and 5 solve different problems and both are required: 4 defends against
a catastrophic response, 5 against eventual consistency at the provider.

---

## 4. Identity resolution — the part that must never guess

A wrong score is worse than no score, because it is indistinguishable from a
right one. Identity is therefore resolved by **deterministic rules only**, and
ambiguity always loses.

### 4.1 Normalisation

Vendor prefix and plan variants (`:free`, `:batch`, `:beta`, …) are stripped;
`.`, `-`, `_` and `:` are treated as one separator class. **Nothing else is
removed.** Size and version tokens are part of the identity and never collapse.

### 4.2 Rules, in order

| Rule | Binds when | Evidence it is safe |
|---|---|---|
| **R1 exact** | normalised ids are equal | — |
| **R2 free-variant** | id ends `-free` and the base resolves | OpenRouter publishes identical benchmark values for `X` and `X:free` — verified on `gpt-oss-20b` (15.2 = 15.2), `gemma-4-31b-it` (29.7 = 29.7), `nemotron-3-ultra-550b-a55b` (38.3 = 38.3) |
| **R3 exact-size** | id is `name:SIZE`; candidate's own size token equals `SIZE` exactly | see below |
| **R4 overlay** | a human reviewed and recorded the mapping once | reviewed entry carries a `reason` field |
| else | — | **no quality evidence is attached** |

**R3 is the rule that prevents the catastrophic case.** Measured behaviour:

| Model | R3 accepts | R3 rejects — what fuzzy matching takes |
|---|---|---|
| `qwen3.5:397b` | `qwen3.5-397b-a17b` → 34.3 | `qwen3.5-9b` → 21.8 |
| `gpt-oss:20b` | `gpt-oss-20b` → 15.2 | `gpt-oss-120b` → 24.1 |
| `gemma4:31b` | `gemma-4-31b-it` → 29.7 | `gemma-4-26b-a4b-it` → 26.1 |
| `mistral-large-3:675b` | *nothing* | — (honest: no counterpart exists) |

Fuzzy matching, tried once during investigation, was wrong on 3 of 5 cases with
the correct answer sitting one row away. It is banned outright — there is no
similarity threshold at which it becomes safe, because its failures are silent.

### 4.3 Ambiguity is a queue, not a coin flip

When a rule yields more than one distinct identity, the row enters a **review
queue** and receives no direct evidence. A human resolves it once into
`overlays/identity.json`, after which it is deterministic and auditable.

Current queue: 1 row (`nemotron-3-nano:30b`, ambiguous between
`nemotron-3-nano-30b-a3b` and `nemotron-3-nano-omni-30b-a3b-reasoning`).

---

## 5. Evidence sources — what actually exists

Measured on 2026-08-12:

| Source | Content | Coverage |
|---|---|---|
| `models.dev/api.json` | context, output, modalities, capabilities, cost. **Zero benchmark fields across all 183 providers.** | 97% of our rows |
| OpenRouter `benchmarks.artificial_analysis` | `intelligence_index`, `coding_index`, `agentic_index` | 157 models |
| OpenRouter `benchmarks.design_arena` | Elo per arena/category | 151 models |

Artificial Analysis' own API returns 401; OpenRouter republishes it without
auth. AA is treated as **evidence**, never as the definition of the score.

---

## 6. Calibration — tested, not assumed

**Question:** can `design_arena` Elo fill AA's gaps?

Restricting to the `models/*` sub-arena materially improves the relation; the
`agents/*` sub-arena is noise by comparison (ρ = 0.66 vs 0.92) and is excluded.

| Metric | Value |
|---|---|
| Overlap set | n = 52 distinct models (83 raw rows held only 57 identities before plan twins were collapsed) |
| Spearman ρ | **0.926** |
| r² | 0.848 |
| Fit | refitted every run; 2026-08-12: `ii = 0.10178 · elo − 83.31` |
| RMSE | 5.5 |
| **LOO-CV RMSE** | **5.73** |
| sd(ii) baseline | 14.06 |
| **Leave-one-VENDOR-out RMSE** | **7.92** — the honest figure for an unseen vendor |

The fit generalises (LOO ≈ in-sample, so it is not overfitted) and cuts error to
43% of the natural spread. **Accepted**, with the LOO-CV RMSE published as the
calibrated tier's uncertainty.

**Vendor bias, measured and then adversarially re-tested.** `mistralai` carries
a −12.7 residual with a standard error of 0.69. The gate excludes it, but the
justification is *scope, not accuracy*: under leave-one-vendor-out the gate
moves pooled error only from 7.94 to 7.92, which is nothing. What the holdout
does establish is that a held-out `mistralai` is predicted at RMSE 15.49
against a natural spread of 14.06 — the calibration has no predictive power
there at all. So an excluded group receives **no calibrated value**, rather
than being dropped from the fit and scored from it anyway.

Two safeguards were added after that test: exclusion requires the bias to be
both large and precisely estimated (`|mean| − 2·SE > threshold`), so a scattered
group cannot be dropped on noise; and the threshold sits on a stable plateau —
any value in 8–12 excludes the same single vendor.

### 6.1 Two estimators tested and **rejected**

| Estimator | LOO RMSE | Verdict |
|---|---|---|
| Same-family mean | 13.82 (vs 15.65 baseline) | **Rejected** — 12% better than knowing nothing; 90th-pct error 21.1 points |
| Structural regression (context, price, recency, vendor) | 5.89 | **Rejected** — see below |

The structural regression looked like the best estimator available. It is not,
and LOO was the wrong test for it:

- **Temporal holdout** (train on older, predict the newest 25% — the actual
  situation, since unscored models are the new ones): RMSE rises to 7.17–8.14
  with a systematic **+3.3 bias**, i.e. it under-rates new models specifically.
- **Zero-price failure**: forcing `price = 0` on real frontier models drops the
  prediction by 16–23 points (`claude-fable-5`: actual 62.1, predicted 65.1,
  predicted-if-free **42.1**). The ground truth contains only 2 zero-priced
  models, so the estimator has effectively never seen a free frontier model —
  and this catalog is built around them.

Adopting it would have made the catalog systematically slander every free
frontier model it exists to showcase. **No structural estimator ships.**

---

## 7. Venom Score

### 7.1 Shape: two sub-scores, never a hidden blend

`VQ` and `VO` measure different things with different coverage and different
evidence strength. Fusing them into one published number would let a cheap
long-context model impersonate an intelligent one wherever quality is unknown.

| Sub-score | Meaning | Coverage today |
|---|---|---|
| **VQ — Venom Quality** | how capable the model is | 95/115 = **83%** (→ ~86% after the overlay queue) |
| **VO — Venom Operational** | how well it serves in this router | 112/115 = **97%** |

Every model row therefore always carries a Venom Score; what varies is which
component is evidence-backed, and that is stated on the row rather than hidden.

### 7.2 VQ evidence levels

| Level | Source | Uncertainty | Displayed precision |
|---|---|---|---|
| `measured` | AA, exact identity | ±1.0 | one decimal |
| `calibrated` | `design_arena models/*` via the published fit | **±5.73** for a vendor represented in the fit (LOO-CV); **±7.92** for one that is not (leave-one-vendor-out) | integer |
| `bounded` | a reviewed relation to a measured model yields a one-sided bound | one-sided | `≥ N`, integer |
| `unrated` | nothing exists | — | `—`, never `0`, never sorted as `0` |

Precision is tied to evidence: a calibrated value with ±6.6 uncertainty is never
rendered as `37.42`. Two rows whose intervals overlap are rendered as tied.

### 7.3 VO composition

Computed from facts available for ~97% of rows: context window, max output,
capability breadth (tools / reasoning / structured output / attachment /
modalities), and price. Each dimension is normalised to a within-catalog
percentile — a statement of fact ("this context is in the 80th percentile
here"), not a judgement.

Reliability and latency belong in VO and are **absent in v1** because measuring
them requires calling the providers, which requires secrets. The service today
holds none: all four rosters, `models.dev` and OpenRouter are unauthenticated.
That property is worth keeping until the measurement work is deliberately
scheduled. VO v1 declares the dimensions it lacks rather than silently
redistributing their weight.

### 7.4 Weights are published policy, not invention

Any single number requires weights. The sin is undeclared weights, not weights.
Profiles live in `overlays/score-profiles.json`, are versioned, and are
selectable in the UI:

```
balanced (default) · coding · cheapest · long-context
```

`methodologyVersion` changes whenever a profile or a formula changes; every
stored score records the version that produced it, so a score can always be
reproduced or invalidated.

---

## 8. Schema

```sql
model_scores (
  provider_id, model_id,
  kind            TEXT,      -- 'VQ' | 'VO'
  value           REAL,
  uncertainty     REAL,      -- half-width; NULL for unrated
  bound           TEXT,      -- NULL | 'lower' | 'upper'
  evidence_level  TEXT,      -- measured | calibrated | bounded | unrated
  source          TEXT,      -- 'artificial_analysis' | 'design_arena' | 'derived'
  source_model_id TEXT,      -- the exact upstream id the value came from
  identity_rule   TEXT,      -- exact | free-variant | exact-size | overlay
  methodology_ver TEXT,
  calibration_ver TEXT,
  computed_at     TEXT,
  PRIMARY KEY (provider_id, model_id, kind)
)
score_events   (…, old_value, new_value, old_level, new_level, reason, at)
calibrations   (version, source_a, source_b, n, rho, slope, intercept,
                loo_rmse, vendor_bias_json, fitted_at)
identity_review(provider_id, model_id, candidates_json, status, resolved_by, at)
```

Every stored score answers, without a lookup: where the number came from, which
upstream row produced it, which rule bound the identity, how uncertain it is,
and under which methodology version it was computed.

---

## 9. Update pipeline

A score is recomputed when any input changes: a new benchmark appears, an
existing one moves, an identity is resolved in the overlay, a calibration is
refitted, or the methodology version changes. Recomputation is a pure function
of stored evidence plus the versioned methodology, so it is re-runnable at any
time and never a manual migration.

Levels upgrade automatically and are recorded as events:
`unrated → bounded → calibrated → measured`. Nothing downgrades silently; a
downgrade (a benchmark withdrawn upstream) is an event with a reason.

---

## 10. UI

The models table gains `VQ` and `VO` columns. Each VQ cell shows the value plus
a small level marker (`measured` / `calibrated` / `bounded` / `—`). The row
detail panel shows full provenance: source, upstream id, identity rule,
uncertainty, methodology and calibration versions, and the timestamp.

Sorting is by point value; rows whose uncertainty intervals overlap are rendered
as tied rather than ordered, so a 0.3-point gap between two ±6.6 values never
reads as a ranking.

---

## 11. Migration & delivery order

| # | Milestone | Exit criterion |
|---|---|---|
| **M0** | DB, migrations, engine with injected I/O, TDD | engine proven on fixtures, no network in tests |
| **M1** | Four provider adapters | 115 live rows in the DB; counts match the live APIs |
| **M2** | Identity + evidence + Venom Score | tier tally reproduces §7; review queue populated |
| **M3** | HTTP API + scheduler + snapshot | service serves last-known-good with staleness |
| **M4** | SPA reads the API; VQ/VO columns; provenance panel | page shows 115, not 58 |
| **M5** | First-party reliability/latency measurement | VO gains its missing dimensions |

`catalog/src/` moves to `catalog/web/` in M4. The current
`ollama-cloud.generated.ts` and `sync-ollama-cloud.mjs` are deleted in M1 —
their behaviour is subsumed by the engine, and keeping both would be the
duplication `CLAUDE.md §7` forbids.

---

## 12. Acceptance criteria

1. Live row count equals the sum of the four provider rosters, verified against
   a fresh fetch.
2. No score exists whose `identity_rule` is absent, and no fuzzy matcher exists
   in the codebase.
3. Every ambiguous identity appears in `identity_review`, never in a score.
4. Every score row carries source, upstream id, uncertainty, and both versions.
5. A simulated empty roster response leaves the DB unchanged and produces a
   `quarantined` run.
6. A simulated one-run disappearance does not retire a model; three do.
7. Recomputing scores from stored evidence reproduces the same values
   bit-for-bit at the same methodology version.
8. The calibration is refitted on every run and its LOO-CV RMSE recorded; if it
   degrades beyond a declared threshold, calibrated scores are withheld rather
   than published.
9. `task gate` green.
