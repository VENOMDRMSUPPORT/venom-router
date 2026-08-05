# Honest Model Verification: Verify-Before-Count, Universal Probes, Hybrid Capabilities, Context Write-Back, Local Benchmark

**Date:** 2026-08-05
**Status:** Design — approved decisions recorded, pending spec review
**Predecessor:** `2026-08-03-model-usability-verification-design.md` (opencode-zen +
clinepass chat-usability loop — shipped). This spec closes what that one left
open: every other provider, the display gate, capabilities, context, and rating.

---

## 1. Problem (diagnosed, with citations)

A connected provider immediately shows its full raw model catalog — paid or
free, working or not — and several intelligence columns are silently wrong:

1. **The Live Models gate never asks about verification.** `ServeModels` passes
   `LiveOnly: true`, but the SQL predicate (`internal/storage/catalog.go:115-124`)
   only requires `availability='available'` (hardcoded by discovery on every
   upsert) plus a connected+healthy account. No certification, capability,
   context, or rating condition. "Live" today means "discovered on a healthy
   account".
2. **The provider card's visible number is the raw total.**
   `ProviderRow.tsx:212-215` renders `uniqueModelCount`; the verified
   `workingModelCount` appears only in the tooltip.
3. **Only 2 of 7 probe-capable providers have a real chat-usability probe.**
   `usabilityProviderSpecs` (`internal/httpapi/usability_wiring.go:42-47`) covers
   opencode-zen and clinepass. agnes-ai, gemini-cli, ollama-cloud, nvidia-nim,
   and claude-code all have `ProbeTransport` wired (`controlmux.go:330-351`)
   but are never swept — their models stay unverified forever, not "for a while".
4. **Capabilities are hardcoded or silently dropped.** clinepass declares
   `["chat"]` (`clinepass.go:527`), gemini-cli likewise (`gemini_cli.go:180`).
   claude-code's official `reasoning`/`thinking`/`documents`/`agents` labels are
   dropped by `models.ParseOperation` (`intelligence/discovery.go:216-231`)
   because they are outside the operation vocabulary. models.dev enrichment
   reaches only zen/ollama/nvidia.
5. **Context has the machinery but not the last wire.**
   `models.native_context_tokens` has **no writer anywhere**; the working
   context probe (`intelligence/contextprobe.go`, driven from `probe.go:490-499`)
   extracts the real limit from the provider's rejection error but records only
   a certification outcome — the extracted number is thrown away. clinepass
   models show "ctx unknown" because the adapter carries no `ContextLength`.
6. **Benchmark completes without ever rating.** The benchmark job pipeline is
   fully built (handler, precedence, audit, storage write) but
   `controlmux.go:274-277` passes a nil `QualityIndex`, so every run completes
   with no rating and no inference is ever performed.

The recurring shape: the machinery exists; the last wire is missing.

## 2. Decisions (owner-approved 2026-08-05)

| # | Decision |
| --- | --- |
| D1 | **Verify-before-count.** A model absent a certified+supported `chat` capability does not appear on Live Models and is excluded from every advertised count. It remains visible in the Model Test Report as UNTESTED/FAILED. A provider with no probe shows 0 working. |
| D2 | **Hybrid capabilities.** Declared capabilities (provider metadata / models.dev) are recorded and shown as *declared*; capabilities with a real probe (tools, structured_output) may be *proven*. The UI distinguishes the two. |
| D3 | **Context: declared immediately (≈), verified on demand (✓).** The existing context probe becomes the verification path and its result is persisted. |
| D4 | **Rating: real local benchmark.** Actual inference on the owner's account measuring success, time-to-first-token, and generation speed; manual trigger only (it spends credits). No imported leaderboard numbers. |
| D5 | **Probing must be fast and rate-limit-safe** (Bifrost-informed engine, §3.C). UNTESTED becomes a seconds-long transient, not a state of the world. |
| D6 | **claude-code is metadata-first.** Its official model metadata (capabilities object, limits) is complete and trustworthy — discovery captures all of it verbatim, no guessing; the runtime probe is minimal (entitlement confirmation, smallest possible spend, long TTL). |

## 3. Design

### A. The honest gate (display + counts)

- Extend the `LiveOnly` predicate in `internal/storage/catalog.go` to
  additionally require an EXISTS over `certifications`: the offering has a
  `chat` offering-operation whose certification is `certified` with
  `capability_truth='supported'`. One source of truth — the same rows the
  usability sweep writes. No new schema, no duplicated "verified" flag,
  no frontend-only filtering.
- `/offerings` stays unfiltered (it is the catalog/test surface; filtering it
  would deadlock certification — established 2026-08-04).
- Fleet: the provider card's visible number becomes `working`, rendered as
  "N working / M discovered" (working prominent). Same for the stat cards and
  any advertised count: `distinctModelStats.working` is the headline everywhere.
- Model Test Report: unchanged — it shows the full discovered set with
  WORKING/FAILED/UNTESTED status; it is where unverified models live.
- **One row per provider offering — no cross-provider merging or exclusion**
  (owner note 2026-08-05): the same underlying model reachable through two
  providers (e.g. `claude fable 5` via antigravity and `claude-5-fable` via
  claude-code) appears **twice**, once per provider. Each Live Models row
  carries the provider's logo and name alongside the model's display name,
  capability icons, context, and rating. This matches the existing canonical
  identity (a per-`(provider, model_id)` hash — no cross-provider equivalence
  in v1) — the page must never dedupe what identity keeps distinct.

### B. Universal chat-usability probes (no provider without a judge)

Extend `usabilityProviderSpecs` from 2 to 7 providers:

| Provider | Wire | Probe fixture | Notes |
| --- | --- | --- | --- |
| opencode-zen | openai_compatible | existing | unchanged |
| clinepass | enveloped openai | existing | unchanged |
| agnes-ai | openai_compatible | shared generic probe + per-provider classifier | own error taxonomy |
| ollama-cloud | openai_compatible | shared generic probe + per-provider classifier | |
| nvidia-nim | openai_compatible | shared generic probe + per-provider classifier | non-generative text models (embeddings/rerankers) are expected to fail the probe and be excluded — that is the probe doing its job |
| gemini-cli | native_api (generateContent) | new probe over the existing gemini wire mapping | |
| claude-code | native_oauth (anthropic messages) | minimal probe, `max_tokens` = smallest accepted | D6: entitlement check only; long recertify TTL |

- **One generic OpenAI-compatible probe body, per-provider classifier seam** —
  the request shape is shared; the `(status, body) → verdict` classification is
  per-provider (taxonomies genuinely differ; zen's is the template).
- Verdict taxonomy is unchanged (usable / paid-unusable / transiently-exhausted /
  auth-stop-account / inconclusive-never-usable).
- **Totality is enforced by a derived test, not a list:** every provider in the
  builtin catalog that registers a discovery adapter must have a usability spec
  entry; the test derives its expectation from the catalog so the next provider
  cannot ship unswept (same pattern as `liveproviderwiring_test.go`). Providers
  with no adapter at all (codex, github-copilot, xai, antigravity) are exempt by
  the same derivation.

### C. The probe engine: fast and rate-limit-safe (Bifrost-informed)

Patterns adopted from Bifrost (per-provider queue/worker isolation; rate-limit
= temporary exclusion, never a verdict; half-open circuit breaker; bounded
exponential backoff):

1. **Per-provider isolation.** Replace the single global sequential sweep with
   one independent worker pool per provider (default concurrency 4, config
   knob). A slow or throttled provider never delays another. The per-sweep
   deadline becomes per-provider.
2. **AIMD rate-limit adaptation.** On the first 429 / provider rate-limit
   verdict, halve that provider's probe concurrency and honor
   `retry-after`/`retry-after-ms` verbatim; a success streak restores
   concurrency additively. A rate-limit response is **never** recorded as a
   model verdict (already the taxonomy's rule — kept).
3. **Per-account circuit breaker.** 3 consecutive rate-limit responses open the
   breaker for that account (cooldown = retry-after if present, else 30s);
   after cooldown exactly one half-open probe decides: success closes, failure
   re-arms. Probing can never hammer a throttled provider.
4. **Cheap fixtures, no waste.** Smallest accepted `max_tokens` per provider
   (1 where possible; clinepass keeps its verified "ok" fixture), 10s per-probe
   timeout, jitter between requests. A model with a valid (unexpired)
   certification is never re-probed — speed comes from parallelism plus not
   sending unnecessary requests, not from hammering.
5. **Fast-lane on discovery.** A successful discovery run immediately triggers
   the usability verification for that account (detached, same trigger pattern
   as `TriggerBackgroundDiscovery`) instead of waiting for the next 30s tick.
   Target: a 20-model account fully classified in ~10-20s.
6. All probes remain under `ProbeGuard` budgets (per-account 500/24h) — the
   engine tunes pace *below* those ceilings, never bypasses them.

### D. Hybrid capabilities with provenance

- **No discarded metadata rule.** Every adapter carries all official model
  metadata it can obtain: capabilities, context length, max output. Per-adapter
  audit in the plan: clinepass + gemini-cli lose their hardcoded `["chat"]`;
  clinepass gains models.dev enrichment (its models are known public families —
  deepseek/kimi/qwen/minimax); claude-code's official capabilities object is
  captured verbatim (D6).
- **Vocabulary: add `reasoning`** to the operation vocabulary
  (`internal/models/offering.go`) via the established bounded-additive-unfreeze
  pattern (doc cross-ref included), so claude-code's reasoning declaration stops
  being silently dropped. `thinking`/`agents`/`documents` stay out of scope —
  no routing consumer exists for them yet (no speculative vocabulary).
- **Provenance surfaced.** The `/models` + `/offerings` projections expose how
  each capability certification was earned: `probed`/`used` (runtime evidence)
  vs `declared` (certified from declaration by `certifyDeclaredCapabilities`).
  The evidence already distinguishes these paths; the projection surfaces it.
  Both UI surfaces render it: **Live Models page** and the **Model Test Report
  modal** — ✓ proven (probed/used), ≈ declared. Chat is always ✓-or-absent
  (runtime-verified by definition).
- **Capabilities render as icon chips with tooltips, never words** (owner note
  2026-08-05, reference image = the Model Test Report's capability icon boxes):
  the categorical `--vnd-cap-*` palette and the icon-box component already used
  by `ModelTestReport.tsx` (`vnd-capability-icon-box`) become the single shared
  capability renderer, extracted and reused on the Live Models page. The
  tooltip carries the operation name, provenance (proven ✓ / declared ≈), truth,
  and state; provenance is visually encoded on the chip itself (e.g. solid
  border for proven, dashed for declared) so the distinction survives without
  hovering. Vision stays declared-only (≈) this phase:
  the probe layer cannot synthesize a vision witness (`probeadapters.go:143-152`)
  and building one is out of scope.

### E. Context: connect the last wire

- **Write-back:** a successful `context_window` probe persists its extracted
  limit to `models.native_context_tokens` (the column's first writer), making
  `EffectiveContext`'s provenance `native` = verified.
- **Declared:** adapters carry the provider/models.dev-declared context
  (clinepass via models.dev; claude-code via official metadata), which surfaces
  immediately as provenance `provider_cap`.
- **Display:** ≈ for declared (`provider_cap`), ✓ for verified (`native`),
  "ctx unknown" only when genuinely neither — which after this spec means
  "not yet discovered", not "we threw the number away".
- Verification stays on-demand (the probe button / Test Report), per D3 — the
  context probe is a deliberately oversized request and is not part of the
  automatic sweep.

### F. Local benchmark (the one genuinely new unit)

- The existing benchmark job (`internal/httpapi/benchmark.go`) gains a real
  engine: a small inference suite against the model's best live offering —
  default 3 short streamed completions (config knob) measuring per run:
  success/failure, time-to-first-token, output tokens/sec.
- Results persist to a new `benchmark_runs` table (new migration; provenance:
  account, offering, fixture, timings, verdict).
- A local quality rating derives from the suite (success rate gates it; latency
  and speed scale it) and is written through the existing
  `SetQualityRating` + audit path — replacing "Not rated — unknown" on the Live
  Models page with a rating that reflects the model **on the owner's account**.
- **Manual-only** (it spends credits): triggered by the existing Benchmark
  button; never part of the automatic sweep. The nil `QualityIndex` leaderboard
  seam stays nil — no imported scores (D4).
- Rating display carries provenance ("local benchmark, <date>").

### G. Out of scope (stated, not implied)

- codex / github-copilot / xai / antigravity — no discovery adapters exist yet
  (P7 work); the totality test exempts them by derivation and will catch them
  the moment an adapter lands.
- A runtime vision witness; `thinking`/`agents`/`documents` operations;
  cross-provider canonical identity; automatic (non-manual) benchmarking;
  importing external quality scores.

## 4. Error handling

- Rate-limit (429 / provider limit errors) → transient, never a model verdict;
  breaker + AIMD absorb it (§3.C).
- Auth errors → account-level (stop the account's sweep; health reflects it) —
  existing rule, kept.
- Probe transport failure (timeout/network) → skipped, never recorded —
  existing rule, kept.
- Discovery/fast-lane failure → non-fatal; the periodic sweep remains the
  fallback.
- Benchmark failure mid-suite → the run records what it measured; no partial
  rating is written unless the success-rate gate passes.
- Secrets: credentials only ever travel as auth headers; bodies are classified
  and discarded; nothing logged — existing pattern, kept.

## 5. Data and state

- Reuse `certifications` + `CertificationDriver` for all verdicts (unchanged).
- New writer for `models.native_context_tokens` (context probe write-back).
- New migration: `benchmark_runs`.
- No new "verified" column anywhere — the gate reads certification truth.

## 6. Testing (project discipline applies)

- TDD throughout; table-driven classifier tests per provider taxonomy.
- **Totality derived from the catalog** (§3.B) — not a hardcoded list.
- **Composition-root mutations:** every "X is wired" claim is proven by
  mutating the production map/root (usability specs map, fast-lane trigger,
  LiveOnly predicate, write-back call site) — the test-owned-fixture trap has
  recurred twice; heuristics #36/#40 apply.
- **No tautologies:** gate/threshold assertions pin literals, never
  `== TheConstantUnderTest` (heuristic from the max_tokens=0 incident).
- Breaker/AIMD tests use injected clocks; no sleeps, no live network in tests.
- SQL gate: real-sqlite tests proving a certified+supported-chat model appears
  and an uncertified one does not, on both `/models` and the counts.

## 7. Open decisions

None — D1-D6 resolve the previously open items (fast-lane cap is the §3.C
concurrency knob; count display is D1; recertify TTL stays default except
claude-code's long TTL under D6, tunable later without schema change).
