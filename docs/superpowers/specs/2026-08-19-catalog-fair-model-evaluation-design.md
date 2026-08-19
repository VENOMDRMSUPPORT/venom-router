# Venom Catalog Fair Model Evaluation Design

**Status:** owner-approved design; ready for implementation planning  
**Date:** 2026-08-19  
**Scope:** Venom Catalog only. This design produces reusable evaluation data for
future consumers; it does not change Venom Router, Venom Lite, Venom Pro, or
Venom Max routing policy, model lists, or selection logic.

## 1. Goal and non-goals

Venom Catalog will publish one honest `overallScore` from 0 to 100 for each
published `provider/model` offer once the evidence required for that offer is
complete. The score is designed for task-aware model selection while keeping
specialized task scores in the database and machine-readable evaluation API,
not as extra columns in the catalog table.

The system will not invent VQ, VO, task scores, benchmark results, provider
facts, or inherited values. A model with incomplete evidence remains visible,
but its overall score is withheld until the missing applicable evaluations are
completed. Existing VQ/VO fields remain readable for backward compatibility;
the new `overallScore` is a separate methodology and does not reinterpret the
old values.

## 2. Decisions locked by the owner

- The published score is `overallScore`, not a VQ/VO blend.
- `overallScore = 0.70 * qualityScore + 0.30 * operationalScore`.
- Quality dimensions have equal weight inside `qualityScore`.
- Operational dimensions have equal weight inside `operationalScore`.
- Unsupported capabilities are excluded from the relevant quality average and
  are reported through independent coverage; unsupported is never converted to
  zero.
- A supported dimension with no trustworthy measurement is not silently
  excluded. It makes the final score `insufficient_evidence`.
- No finite evaluation can display 100%; scores use Bayesian smoothing and
  carry uncertainty and sample count.
- Quality scores belong to an exact model identity. Speed and cost efficiency
  belong to an exact `provider/model` offer.
- A provider/model offer is compared using its own conformance result. Evidence
  never crosses providers or similar-looking model ids without an exact,
  reviewed identity match.
- The Catalog UI shows one overall score and coverage. Specialized scores are
  persisted and available to machine consumers/diagnostics, but are not added as
  visible table columns.
- No Commit, Branch, Push, or Venom Router change is part of this design.

## 3. Evaluation dimensions

### 3.1 Quality dimensions (identity level)

These dimensions describe the model's task quality:

| Dimension | What is measured |
|---|---|
| `coding` | Correctness, test completion, patch quality, and regression avoidance on a fixed coding set |
| `reasoning` | Correct answers and valid reasoning outcomes on a fixed reasoning set |
| `longContext` | Retrieval and reasoning accuracy as relevant context length increases |
| `toolCalling` | Correct tool choice, arguments, sequencing, and recovery from tool errors |
| `structuredOutput` | Schema validity, constraint adherence, and semantic correctness |
| `vision` | Image understanding tasks, only when the model supports image input |

Each dimension has a fixed criterion-referenced rubric, a versioned test set,
and an evidence record. A dimension score is not a percentile and does not
change merely because new models enter the catalog.

### 3.2 Operational dimensions (provider/model level)

| Dimension | What is measured |
|---|---|
| `speed` | Standardized serving performance: TTFT, output tokens/sec, end-to-end latency, p95, and success rate |
| `costEfficiency` | Affordability of a fixed reference workload at the offer's actual billing terms |

`costEfficiency` is not multiplied by quality and does not reward a model for
being cheap while producing poor output. Free and included offers receive an
explicit reference-workload treatment; unknown pricing is not zero.

## 4. Score calculation

### 4.1 Applicability and coverage

For every quality dimension the evaluator records one of:

- `supported`: the offer/model can perform the task and a score is required;
- `unsupported`: the capability is explicitly absent and is excluded;
- `unknown`: evidence cannot establish support or absence; it is excluded from
  the provisional average but prevents a final score;
- `evaluating`: an evaluation is scheduled or running;
- `scored`: a trustworthy result exists.

Coverage is published independently:

```text
qualityCoverage = scored applicable quality dimensions /
                   applicable quality dimensions
overallCoverage  = scored applicable dimensions /
                   applicable dimensions including speed and cost
```

The denominator excludes only `unsupported`. `unknown` remains in the
applicability audit and blocks final publication. A model without Vision is not
penalized in Coding or Reasoning comparisons.

### 4.2 Bayesian-smoothed criterion score

Every evaluated dimension is reduced to weighted successful criteria and total
criteria under its rubric:

```text
rawRate      = weightedSuccesses / weightedCriteria
smoothedRate = (weightedSuccesses + 1) / (weightedCriteria + 2)
score        = 100 * smoothedRate
```

The Beta(1,1) prior is part of the methodology version. It guarantees that a
finite sample never displays exactly 0% or 100%, while the raw rate remains
stored for audit. The record also stores confidence, uncertainty, sample count,
rubric version, test-set hash, and evidence references.

External benchmark values may be converted into a criterion score only when the
benchmark's identity, task definition, scale, and publication method are
compatible with the Catalog rubric. Otherwise the benchmark is provenance-only
and the Catalog runs its standardized runtime evaluation.

### 4.3 Quality and operational averages

Let `Q` be the set of supported quality dimensions with completed scores. The
final quality average is:

```text
qualityScore = mean(score[d] for d in Q)
```

All applicable supported dimensions must be in `Q` before `qualityScore` is
final. Let `O = {speed, costEfficiency}`. Both operational dimensions must be
completed before `operationalScore` is final:

```text
operationalScore = mean(speed, costEfficiency)
overallScore = 0.70 * qualityScore + 0.30 * operationalScore
```

The server calculates using full precision and rounds only for display. The
stored result includes component values, the included/excluded dimension list,
coverage, uncertainty propagation, and `methodologyVersion`.

## 5. Evidence and source policy

### 5.1 Evidence hierarchy

1. A trustworthy external benchmark for the exact model identity and compatible
   task definition.
2. Venom Catalog standardized runtime evaluation against the exact offer.
3. Official provider/model documentation for capability applicability and
   serving facts.
4. Reviewed facts with a URL, evidence description, and review timestamp.
5. General indexes such as `models.dev`, OpenRouter, and Hugging Face for
   discovery and cross-checking.

Metadata can establish `supported`, `unsupported`, or operational facts when
the source is authoritative. Metadata alone is never a task-quality score.
Hugging Face's general model index is a discovery source, not proof that a
model passed a benchmark.

### 5.2 Exact identity and conflicts

Identity matching is exact after the existing deterministic normalization rules
and must retain vendor, version, size, variant, and release distinctions. A
similar name, family, or provider listing cannot inherit a score.

When official sources disagree on a fact, the field remains unset and the
record receives `conflicting_official_sources`; the system does not choose the
newest, largest, or first-read value. The conflict stores every distinct side,
source URL, and timestamp.

## 6. Provider Conformance Guard

The same exact model identity may be served differently by different providers.
The guard therefore runs a conformance suite for every published
`provider/model` offer:

1. Confirm exact identity including release/variant tokens.
2. Run the applicable runtime probes with fixed prompts, tools, schemas, and
   image fixtures.
3. Compare the offer's results with the identity-level quality record.
4. If results agree within the declared tolerance, expose the identity score
   with the offer's conformance evidence.
5. If divergence is proven, do not mix results. Store a
   `providerQualityOverride` for that offer, with its own score, uncertainty,
   sample count, and reason.
6. A single-provider identity score is marked provisional with lower
   confidence until a second conformance result exists.

Future providers trigger a conformance recheck. Speed and cost are always
offer-level and never copied from another provider.

## 7. Runtime evaluation protocol

Runtime evaluation is deterministic and reproducible:

- fixed prompt and input/output fixtures per dimension;
- fixed region, model parameters, timeout, and concurrency;
- warmup calls excluded from measured samples;
- repeated samples with a declared minimum count;
- captured request, response, tool, schema, and image artifacts as redacted
  hashes/references, never secrets;
- success/failure and evaluator version stored for every sample;
- provider outages and rate limits recorded as operational failures, not
  silently dropped.

Speed stores TTFT median/p95, output tokens/sec median/p95, end-to-end median/p95,
and success rate. Scores map to fixed absolute anchors, not to the fastest
current model, so catalog growth cannot move an existing model's score.

## 8. Storage model

Existing `model_scores` rows for VQ and VO remain intact for compatibility. The
new evaluation layer adds these logical records:

- `model_identity_scores(identity_id, dimension, score, raw_rate,
  uncertainty, confidence, sample_count, status, rubric_version,
  evidence_json, evaluated_at)`;
- `provider_model_scores(provider_id, model_id, dimension, score, raw_rate,
  uncertainty, confidence, sample_count, status, evidence_json,
  evaluated_at)` for speed, cost, and conformance overrides;
- `overall_model_scores(provider_id, model_id, overall_score, quality_score,
  operational_score, quality_coverage, overall_coverage, included_dimensions,
  excluded_dimensions, status, uncertainty, methodology_version, computed_at)`;
- `evaluation_runs` and `evaluation_samples` for reproducibility and audit;
- `provider_quality_overrides` for proven conformance divergence;
- existing `resolution_jobs`, `model_facts`, and `model_conflicts` remain the
  source for enrichment lifecycle and operational evidence.

All primary keys include exact identity/provider/model grain as appropriate.
No credentials, prompt secrets, or raw private responses are stored.

## 9. Resolution lifecycle and publication

The existing five-minute resolution job remains an enrichment mechanism. It may
retry source discovery and fact parsing, but it may not fabricate a benchmark
or task score. Its public states map as follows:

| Resolution | Catalog score state |
|---|---|
| `processing` | `evaluating` or `processing` |
| `awaiting_external_benchmark` | runtime evaluation required; no final score yet |
| `source_incomplete` | `insufficient_evidence` |
| `complete` with all applicable evaluations | final `overallScore` |
| old payload without resolution | `unknown` and `Unrated` |

Jobs are unique by `(providerId, modelId)`, share the full-sync lock, give full
sync priority, and resume from durable state after restart. After five minutes
the job becomes dormant and is reactivated only by a full sync, roster/source
change, new benchmark, or explicit evaluation request. A score recomputation
is required only when a score, uncertainty, applicability result, conformance
result, or methodology version changes.

Missing evidence never removes a published model from the unified table. It
appears once, with a truthful state and `Why` details. `overallScore` becomes
numeric only after the evaluator has completed every applicable dimension.

## 10. API and UI contract

The normal `ApiModel` payload exposes:

```ts
interface OverallModelScore {
  value: number | null;
  display: string;
  status: 'complete' | 'evaluating' | 'insufficient_evidence' | 'unknown';
  qualityScore: number | null;
  operationalScore: number | null;
  qualityCoverage: { scored: number; applicable: number; percent: number };
  overallCoverage: { scored: number; applicable: number; percent: number };
  uncertainty: number | null;
  methodologyVersion: string;
}
```

The table has one `Score` cell and one global `#` rank. Specialized dimensions
are not table columns. A diagnostics/evaluation endpoint may return the full
dimension records for machine consumers and the `Why` panel. The UI maps
`insufficient_evidence` to `Unrated`/`Data incomplete`, never to 0%.

Global ranking uses the server's full-precision `overall_score`; incomplete
rows have no rank and sort alphabetically after ranked rows. React never
recalculates rank from displayed percentages.

## 11. Testing and acceptance

### Scoring unit tests

- Bayesian smoothing never returns exactly 0 or 100 for finite samples.
- Equal supported dimensions produce an arithmetic mean.
- Unsupported Vision is excluded without lowering Coding.
- Unknown or supported-but-unmeasured dimensions block final publication.
- 70/30 aggregation uses full precision and independent coverage.
- Adding an unrelated model does not change criterion-referenced scores.
- Exact identity mismatch cannot reuse an identity score.
- Provider conformance divergence creates an offer override.

### Runtime and source tests

- Fixed fixtures produce reproducible scores and hashes.
- Speed tests use warmup exclusion, repeated samples, medians, p95, and fixed
  anchors.
- Cost tests distinguish free, included, per-token, and unknown pricing.
- models.dev/OpenRouter/Hugging Face metadata can establish facts but cannot
  create quality scores.
- Conflicting official facts retain both sides and publish no value.
- Provider/model records never share speed, cost, or override data.

### API/UI tests

- Every model appears exactly once in Table and Grid.
- No `No model score` or `Needs verification` section exists.
- Complete rows show overall score and rank; incomplete rows show state and no
  rank.
- Coverage is independent from score and specialized scores are absent from
  normal table columns.
- Legacy payloads without `resolution` or the new evaluation object render as
  `unknown`/`Unrated` without crashing.

### Live acceptance

- Run against a fresh isolated snapshot and database, not stale WAL state.
- Verify every published offer for all providers has either a final numeric
  `overallScore` or a truthful, auditable `insufficient_evidence` state.
- Verify `kimi-k3` remains a regression fixture for the old model-score path;
  the new overall methodology must not silently relabel the old 65.8% result.
- Verify OpenCode Go, Ollama Cloud, OpenCode Zen, and ClinePass counts match
  their current rosters and no secondary tables appear.
- Verify desktop and mobile routes at the running Catalog URL.

## 12. Delivery boundaries

Implementation must proceed in Venom Catalog phases: schema/contracts, scoring
engine, evidence/runtime evaluator, conformance and resolution integration,
API, UI, then live acceptance. Each phase has focused tests and a report.
Existing local changes are preserved. No Venom Router source, final model list,
or routing policy is edited as part of this work.

## 13. `overall-score-v1` Decision Gate

This section is the normative, owner-approved baseline. When an earlier section
describes a policy generally, the concrete values below control. Changing any
value requires a new methodology version; existing results keep the version
that produced them.

```text
methodologyVersion = overall-score-v1
rubricVersion      = catalog-rubrics-v1
testSetVersion     = catalog-testset-v1
```

The test-set SHA-256 is derived from the canonical serialized manifests and is
stored with every run. The digest is an output of the reviewed fixture content,
not a hand-selected policy constant; a changed digest without a changed
`testSetVersion` is rejected by validation.

### 13.1 Rubrics v1

Every applicable quality dimension runs 20 scenarios with three repetitions per
scenario. Criteria inside a dimension have equal weight.

| Dimension | Equal-weight criteria |
|---|---|
| `coding` | correctness; tests; edge cases; maintainability; instruction adherence |
| `reasoning` | correctness; multi-step consistency; constraint adherence; uncertainty handling; contradiction resistance |
| `longContext` | retrieval; multi-hop reasoning; position robustness; distraction resistance; synthesis |
| `toolCalling` | tool selection; arguments; sequencing; error recovery; stop condition |
| `structuredOutput` | schema validity; types; required fields; constraints; semantic correctness |
| `vision` | recognition; localization; OCR; chart/table understanding; visual reasoning |

A final dimension result requires 20 valid scenarios. A model failure is a
failed criterion. Evaluator and network failures are retried and do not become
model answers. Three repetitions are retained individually in
`evaluation_samples`; they are not collapsed before the raw success totals are
computed.

### 13.2 Speed v1

The speed fixture requests a fixed 512-token output. Each metric maps linearly
between its two absolute anchors and is clamped to `[0, 100]`. The four mapped
metrics have equal weight.

| Metric | Raw score 100 | Raw score 0 |
|---|---:|---:|
| median TTFT | 0.5 seconds | 8 seconds |
| median output tokens/second | 100 | 5 |
| p95 end-to-end latency | 5 seconds | 90 seconds |
| success rate | 99.5% | 90% |

For lower-is-better metric `x`:

```text
metricScore = clamp(100 * (zeroAnchor - x) /
                    (zeroAnchor - hundredAnchor), 0, 100)
```

For higher-is-better metric `x`:

```text
metricScore = clamp(100 * (x - zeroAnchor) /
                    (hundredAnchor - zeroAnchor), 0, 100)
```

The raw speed result is the arithmetic mean of the four metric scores. For
publication smoothing, `weightedCriteria` is the number of retained measured
requests and `weightedSuccesses = rawSpeed / 100 * weightedCriteria`. This uses
the same Beta(1,1) contract as other dimensions, so the published dimension
value cannot equal 0 or 100 on finite evidence.

### 13.3 Cost Efficiency v1

The fixed reference workload is:

```text
100 requests * (8,000 input tokens + 2,000 output tokens)
= 800,000 input tokens + 200,000 output tokens
```

The offer's published billing terms calculate the reference cost. Raw `$0`
maps to 100, raw `$50` maps to 0, and values between them map linearly and are
clamped:

```text
rawCostEfficiency = clamp(100 * (1 - referenceCostUsd / 50), 0, 100)
```

Free and included offers use zero marginal reference cost while retaining their
billing kind and quota evidence. Unknown pricing produces no cost score and
therefore prevents a complete `overallScore`. The published cost dimension uses
`weightedCriteria = 100` and `weightedSuccesses = rawCostEfficiency` in the
`overall-score-v1` smoothing contract. This treats the 100 fixed affordability
anchor units as the deterministic evidence basis and prevents an exact 0 or
100 result.

### 13.4 Conformance tolerance

A `providerQualityOverride` is created when either condition is proven:

1. The provider/model offer breaks a declared operational contract, including
   a required tool, schema, modality, or other applicability result.
2. Two independent conformance runs show an absolute dimension difference
   greater than 8 points and their 95% confidence intervals do not overlap.

One run can mark the offer `provisional`; it cannot create an override by
itself. Each independent run must satisfy the Rubrics v1 valid-scenario minimum.

### 13.5 Uncertainty and confidence

For `criteria` finite criterion outcomes:

```text
rawRate      = successes / criteria
smoothedRate = (successes + 1) / (criteria + 2)
score        = 100 * smoothedRate

uncertainty = 196 * sqrt(
  smoothedRate * (1 - smoothedRate) / (criteria + 4)
)
confidence = clamp(1 - uncertainty / 100, 0, 1)
```

Component uncertainty uses root-sum-square propagation with the same normalized
weights used by the component mean. Final uncertainty is:

```text
overallUncertainty = sqrt(
  (0.70 * qualityUncertainty)^2 +
  (0.30 * operationalUncertainty)^2
)
```

### 13.6 External benchmark acceptance

An external benchmark may produce a task score only when all conditions hold:

1. The model identity, version, size, and variant match exactly.
2. A versioned crosswalk maps the benchmark task to one Catalog rubric
   dimension.
3. The source publishes its scoring range and measurement methodology.
4. The source publishes a sample count or confidence interval.
5. The evidence is no older than 180 days at evaluation time.

If any condition fails, the record is provenance-only and the Catalog schedules
runtime evaluation. A compatible external result is normalized through its
versioned crosswalk. When sample count exists, it becomes `weightedCriteria`.
When only a normalized 95% interval exists, the effective finite evidence count
is inferred using the same approved uncertainty equation:

```text
p = clamp(normalizedPoint / 100, 0.000001, 0.999999)
u = normalizedHalfWidth / 100
effectiveCriteria = max(1, 3.8416 * p * (1 - p) / (u * u) - 4)
```

The result then uses the same smoothing, uncertainty, and methodology records
as an internal result. An absent or zero-width interval without a sample count
is not finite evidence and remains provenance-only.

### 13.7 Coexistence with `model-score-v1`

- Existing `modelScore` and `model-score-v1` behavior remain unchanged for
  compatibility.
- New `overallScore` and `overall-score-v1` data are added beside the legacy
  fields in storage and API contracts.
- The current VQ/VO rows and global legacy model rank are not rewritten or
  relabeled as overall evaluation results.
- The catalog's new Score cell, rank, coverage tile, and default sorting use
  `overallScore`. Specialized dimensions remain absent from table columns.
- Older clients continue receiving the legacy fields. A stale payload without
  `overallScore` normalizes to `status: "unknown"` and `value: null`.

### 13.8 Runtime evaluator operations

```text
methodologyVersion = overall-score-v1
evaluatorVersion   = catalog-eval-v1
evaluationRegion   = catalog-eval-cairo-1
warmupRequests     = 3
scenarioCount      = 20
repetitions        = 3
requestTimeoutMs   = 120000
providerConcurrency = 2
transientRetries   = 3
```

Only HTTP 429 and 5xx/network-transient failures are retried. Invalid model
outputs and declared provider rejections are model/offer outcomes, not
transient evaluator failures. An evaluator-internal failure is excluded and
must be rerun. A provider/network failure that remains after all three retries
is excluded from task-quality answers but is retained as a failed operational
request in the speed success rate. Provider credentials enter through
environment variables or the existing operational secret boundary; they are
never written to SQLite, snapshots, API payloads, logs, evidence artifacts, or
test fixtures. Missing credentials produce `insufficient_evidence`, never an
estimated score.
