# Hybrid Capabilities + Context Truth Implementation Plan (Plan 2 of 3)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Capabilities and context stop being hardcoded or thrown away: every adapter carries all official metadata, capabilities distinguish *proven* (✓) from *declared* (≈) on both UI surfaces as icon chips with tooltips, and the context probe's extracted limit is finally persisted.

**Architecture:** Adapter-level metadata completion (claude-code/gemini-cli/clinepass), a `reasoning` vocabulary addition, a candidate-operations lane so metadata-less providers (clinepass) can still be runtime-proven, provenance derived at projection time (chat = runtime by construction; non-chat = proven iff a succeeded probe run exists), and the context-probe write-back into `models.native_context_tokens` (its first writer). Spec: `docs/superpowers/specs/2026-08-05-honest-model-verification-design.md` (§3.D–E + the two owner notes).

**Tech Stack:** Go, React+TypeScript (vitest), real-sqlite tests.

## Global Constraints

- Same global constraints as Plan 1 (`2026-08-05-universal-probes-and-honest-gate.md`): English-only files, strict TDD, no live network in tests, composition-root mutation proofs, no tautological assertions, full gate before green claims, don't touch the owner's uncommitted fleet WIP, commit per green step.
- **Depends on Plan 1 being merged** (uses `usabilityProbeResult`, the 7-provider specs map, and the tightened LiveOnly gate's semantics).
- "No guessing" (owner rule, D6): metadata comes from official provider responses or models.dev exact-key matches ONLY. Never infer capabilities/context from a model's name or family. If a datum has no official source, it stays unknown and the runtime probe is the only path to truth.
- The operation vocabulary is doc-frozen: the `reasoning` addition must follow the bounded-additive-unfreeze pattern (find the precedent: `git log --oneline --grep="unfreeze"` and how `WireSchema` amended doc 01 §4.3 — amend the vocabulary's doc section the same way, with a cross-ref in the code comment).

---

### Task 1: `reasoning` joins the operation vocabulary

**Files:**
- Modify: `internal/models/offering.go:40-62` (`Operation` consts + `operationSet`)
- Modify: the doc section the vocabulary comment cites (02 §3 / 04 §5 — read the comment at `offering.go:34-37`, amend the cited section additively, cross-ref the date)
- Test: `internal/models/offering_test.go`

**Interfaces:**
- Produces: `models.OperationReasoning Operation = "reasoning"`, accepted by `ParseOperation`, enumerated by `Operations()`. Task 2's adapters and Task 5's projection rely on it. It is certifiable-but-not-routed (exactly like `image_generation` — copy that precedent's comment).

- [ ] **Step 1: Write the failing test** — `ParseOperation("reasoning")` returns `OperationReasoning, nil`; `Operations()` contains it exactly once; count assertion pins the literal 8 (not `len(operationSet)` — tautology).
- [ ] **Step 2: Run to verify failure** — `go test ./internal/models/ -run TestParseOperation`.
- [ ] **Step 3: Implement** — add the const + `operationSet` entry + doc amendment in the same commit (spec-vs-gate-string heuristic: the doc and code must move together).
- [ ] **Step 4: Run `go test ./internal/models/ ./internal/intelligence/ ./internal/httpapi/`** — the projection iterates `Operations()`; nothing else should break (reasoning has no native/transport support anywhere yet, so it simply never becomes effective — fail-closed by construction).
- [ ] **Step 5: Commit** — `git commit -m "feat(models): reasoning operation (bounded additive vocabulary unfreeze)"`

---

### Task 2: Adapters carry ALL official metadata (no discarded metadata)

**Files:**
- Modify: `internal/providers/claude_code.go:414-437` (capabilities mapping — add `reasoning`; verify context/limits capture)
- Modify: `internal/providers/gemini_cli.go:170-185` (capabilities + context from the official models response)
- Test: `internal/providers/claude_code_test.go`, `internal/providers/gemini_cli_test.go` (extend the existing discovery fixtures)

**Interfaces:**
- Consumes: `models.OperationReasoning` (Task 1); `DiscoveredModel` (existing struct — check its fields with `grep -n "type DiscoveredModel" internal/providers/types.go`; it already carries `ContextLength`/`MaxInputTokens`/`MaxOutputTokens` for zen — reuse, don't add fields unless genuinely absent).
- Produces: discovery payloads where claude-code and gemini-cli models carry real capabilities and context.

Per-adapter facts (verify each against the adapter's own parsed response struct before coding — the response structs in these files are the source of truth; do not invent fields):

1. **claude-code** (`claude_code.go:414-437`): the provider's `capabilities` object is already parsed; today `reasoning` maps to nothing (dropped downstream). After Task 1 it maps to `OperationReasoning`. Also confirm whether the official model payload carries context/output limits (read the response struct the adapter decodes); if present, populate `ContextLength`/`MaxOutputTokens` — if absent, leave nil and note it in the task report (never guess).
2. **gemini-cli** (`gemini_cli.go:170-185`): the official `models` response carries `inputTokenLimit`, `outputTokenLimit`, and `supportedGenerationMethods`. Deliberate `["chat"]` hardcode (comment at 177-180) is superseded by this spec: map `generateContent` support → `chat`; populate `ContextLength` from `inputTokenLimit` and `MaxOutputTokens` from `outputTokenLimit`. Read the adapter's existing response struct first — if it currently drops these fields, extend the struct to decode them (they are in the official API response).

- [ ] **Step 1: Write the failing gemini test** — extend the existing discovery fixture with `inputTokenLimit: 1048576, outputTokenLimit: 8192, supportedGenerationMethods: ["generateContent"]`; assert the DiscoveredModel carries `ContextLength = 1048576`, `MaxOutputTokens = 8192`, capabilities contain `chat`.
- [ ] **Step 2: Run to verify failure.**
- [ ] **Step 3: Implement gemini; run green.**
- [ ] **Step 4: Write the failing claude-code test** — fixture model with `capabilities: {"reasoning": true, ...}` asserts `reasoning` in Capabilities (plus whatever context fields Step 0 reading established).
- [ ] **Step 5: Implement; run `go test ./internal/providers/`.**
- [ ] **Step 6: Commit** — `git commit -m "feat(providers): claude-code and gemini-cli discovery carries full official metadata"`

---

### Task 3: Candidate operations — a runtime-proof lane for metadata-less providers

**Files:**
- Modify: `internal/providers/types.go` (`DiscoveredModel` gains `CandidateOperations []string`)
- Modify: `internal/providers/clinepass.go:527` (declare candidates)
- Modify: `internal/intelligence/discovery.go:216-231` (parse candidates alongside capabilities)
- Modify: `internal/storage/discovery.go` (operations rows for candidates; `capabilities_json` stays declared-only)
- Modify: `internal/storage/catalog.go:295-331` (`ListNonChatOperationsToCertify` must EXCLUDE candidate ops)
- Test: `internal/storage/catalog_test.go`, `internal/intelligence/discovery_test.go`

**Interfaces:**
- Consumes: `DiscoveredModel`, `ensureOfferingOperation` (`storage/discovery.go:346-357`).
- Produces: clinepass models get `tools` + `structured_output` offering_operations rows that the *declared-certification* path never touches — only a real probe (the existing `/offerings/{id}/probe` endpoint, or Test All) can certify them.

Why: clinepass's wire returns `{id, name}` only (verified: `docs/evidence/clinepass-legacy-wire-reference.md:97-105`) and it has no models.dev entry — under "no guessing" nothing can be *declared*. But without an offering_operations row a capability can never be probed either (`probeTarget` needs a row id — `modelStatus.ts:81-87`). Candidates create the row without the declaration, so runtime proof stays reachable and `certifyDeclaredCapabilities` (which certifies from declaration evidence) must skip them — **certifying a candidate from "declaration" would be fabricating evidence.**

The distinguishing rule (no schema change): an operation is *declared* iff it appears in `account_model_offerings.capabilities_json`; an operations row absent from that list is a *candidate*. `ListNonChatOperationsToCertify` gains the filter: only rows whose operation appears in the offering's `capabilities_json` are returned for declaration-certification.

- [ ] **Step 1: Write the failing storage test** — seed an offering whose `capabilities_json = ["chat"]` but which has `tools` and `chat` operations rows in `probing`; assert `ListNonChatOperationsToCertify` returns EMPTY (tools is a candidate, chat is excluded by definition). Seed a second offering with `capabilities_json = ["chat","tools"]` → returns the tools row.
- [ ] **Step 2: Run to verify failure** — today the first case returns the tools row (the bug: it would be auto-certified from a declaration that never happened).
- [ ] **Step 3: Implement the filter** — read `capabilities_json` in the query (SQLite `EXISTS (SELECT 1 FROM json_each(amo.capabilities_json) WHERE json_each.value = oo.operation)` — confirm the modernc driver's JSON1 availability with a quick query test; if JSON1 is unavailable, filter in Go after fetching the declared list).
- [ ] **Step 4: Write the failing pipeline test** — a `DiscoveredModel{Capabilities: ["chat"], CandidateOperations: ["tools","structured_output"]}` run through discovery creates 3 operations rows but `capabilities_json` contains only `chat`.
- [ ] **Step 5: Implement** — `CandidateOperations` field; discovery parses them with the same `ParseOperation` (unknown candidates dropped the same way); storage writes rows for the union, `capabilities_json` for declared only. Then set clinepass: `Capabilities: []string{"chat"}, CandidateOperations: []string{"tools", "structured_output"}`.
- [ ] **Step 6: Run `go test ./internal/...`; commit** — `git commit -m "feat(discovery): candidate operations — probeable without fabricated declarations"`

---

### Task 4: Context write-back — the probe's number is finally persisted

**Files:**
- Modify: `internal/intelligence/contextprobe.go` (the run report must expose the extracted limit — read the report struct first; the extraction ladder already computes it)
- Modify: `internal/httpapi/probe.go:491-499` (after a successful context probe, persist the limit)
- Create: `SetNativeContextTokens` on the discovery/catalog write side (`internal/storage/discovery.go` — DiscoveryRepo owns all writes to these tables per the comment at `catalog.go:74-79`)
- Test: `internal/storage/discovery_test.go`, `internal/httpapi/probe_test.go`

**Interfaces:**
- Consumes: the context probe report (read `NewContextProbe`/its Run report type in `contextprobe.go` — if the extracted limit is not on the report, add it there first; it exists internally at the rung-extraction site).
- Produces: `(*storage.DiscoveryRepo) SetNativeContextTokens(ctx context.Context, modelID string, tokens int) error` — UPDATE `models SET native_context_tokens = ?` guarded by `tokens > 0`; and the probe handler call site. After this, `models.EffectiveContext` returns provenance `native` for probed models (`effective.go:27-48` — no change needed there).

- [ ] **Step 1: Write the failing storage test** — insert a model with NULL `native_context_tokens`; `SetNativeContextTokens(ctx, id, 128000)` → SELECT returns 128000; a second call with `0` returns an error and leaves the row untouched (a non-positive limit is never a fact — `positiveOrNil` semantics).
- [ ] **Step 2: Run to verify failure; implement; green.**
- [ ] **Step 3: Write the failing handler test** — drive the probe job path with a fake transport whose rejection body yields an extracted limit (reuse the existing context-probe test fixtures in the package); assert `models.native_context_tokens` is written when the probe extracts a limit, and NOT written when the rung ladder reports `RungNoSignal`.
- [ ] **Step 4: Implement the call site** — in the `op == models.OperationContextWindow` branch, after `RecordAttempt` succeeds, write the limit (failure to write is logged, never fails the job — the certification already recorded truthfully).
- [ ] **Step 5: Mutation proof** — delete the write-back call from the PRODUCTION handler; Step 3's test must go RED (it must drive the production handler path, not a hand-built assembly).
- [ ] **Step 6: Run the packages; commit** — `git commit -m "feat(context): context probe writes its extracted limit to native_context_tokens"`

---

### Task 5: Capability provenance in the projection

**Files:**
- Modify: `internal/intelligence/readmodel.go:39-58` (`EffectiveCapability` gains `Provenance`)
- Modify: `internal/httpapi/models.go` (the `/models` + `/offerings` payload capability entries gain `provenance`; the ProjectionInput assembly supplies it)
- Modify: `dashboard/src/api/controlClient.ts` (`EffectiveOffering` capability type gains `provenance: "probed" | "declared" | ""`)
- Test: `internal/intelligence/readmodel_test.go`, `internal/httpapi/models_test.go`

**Interfaces:**
- Consumes: `probe_runs` (the repo behind `h.probeRuns` in `probe.go` — find its list/exists reader in `internal/storage/`), certifications.
- Produces: `EffectiveCapability.Provenance string` with the closed values `"probed"` / `"declared"` / `""` (empty when the capability is not certified-supported — provenance only qualifies an earned certification).

The derivation rule (no new storage):
- `operation == chat` (certified+supported) → `"probed"` — chat is ONLY ever certified by the runtime usability probe (Plan 1) or real use; there is no declared path for chat by construction.
- non-chat certified+supported → `"probed"` iff a succeeded probe run exists for that `offering_operation_id` in `probe_runs`; else `"declared"` (it was certified by `certifyDeclaredCapabilities`).
- not certified-supported → `""`.

- [ ] **Step 1: Write the failing projection test** — table: chat/supported → probed; tools/supported with `HasSucceededProbeRun: true` → probed; tools/supported without → declared; vision/unknown → "". (The projection receives the probe-run fact as a per-operation input — extend `ProjectionInput` with `ProvedOperations map[models.Operation]bool` supplied by the httpapi assembler; the projection itself stays pure.)
- [ ] **Step 2: Run to verify failure; implement the pure part; green.**
- [ ] **Step 3: Write the failing handler test** — real-sqlite: one offering with a certified tools op + a succeeded probe_runs row → `/offerings` payload shows `"provenance":"probed"`; a second certified tools op with no probe run → `"declared"`. (Read how `models.go:93` assembles `ProjectionInput` from `CatalogOperationRow`s and add one bounded query for succeeded run ids per offering — batch it, no N+1: one `SELECT DISTINCT offering_operation_id FROM probe_runs WHERE execution = 'succeeded' AND offering_operation_id IN (...)` per page. Confirm the exact column/value names against the probe_runs schema in the migrations before writing the SQL.)
- [ ] **Step 4: Implement; run both packages.**
- [ ] **Step 5: Update `controlClient.ts` type + regenerate nothing (hand-maintained types — match the existing style); `npm run typecheck`.**
- [ ] **Step 6: Commit** — `git commit -m "feat(intelligence): capability provenance (probed vs declared) in the shared projection"`

---

### Task 6: One shared capability-chip renderer (icons + tooltip, provenance-aware)

**Files:**
- Create: `dashboard/src/fleet/CapabilityChips.tsx` (extracted from `ModelTestReport.tsx:23-40` + `:341-373`)
- Modify: `dashboard/src/fleet/ModelTestReport.tsx` (consume the shared component)
- Modify: `dashboard/src/fleet/fleet.css` (a `--declared` modifier: dashed border on `vnd-capability-icon-box`)
- Test: `dashboard/src/fleet/CapabilityChips.test.tsx`

**Interfaces:**
- Consumes: the capability entry type from `controlClient.ts` (now with `provenance`), `CAPABILITY_STYLE` map (moves into the new file), `getCapabilityStyle`.
- Produces: `<CapabilityChips capabilities={...} cap={6} />` — icon boxes with `role="img"`, `aria-label`, tooltip `title` lines: operation name, `Provenance: proven (runtime probe)` / `Provenance: declared by provider`, truth, state; dashed border via `vnd-capability-icon-box--declared` when `provenance === "declared"`. The `reasoning` icon/color already exist in the style map (`brain`, gold).

Owner requirement (2026-08-05, reference image): capabilities are ALWAYS icon chips with tooltips — never words — on both surfaces.

- [ ] **Step 1: Write the failing component test** — renders one chip per capability up to the cap with `+N` overflow; a `provenance:"declared"` chip carries the `--declared` class and its title contains "declared"; a `provenance:"probed"` chip does not; `aria-label` equals the operation.
- [ ] **Step 2: Run to verify failure** — `npx vitest run src/fleet/CapabilityChips.test.tsx`.
- [ ] **Step 3: Implement by EXTRACTION** — move the chip JSX + style map out of ModelTestReport verbatim, add the provenance modifier; ModelTestReport renders `<CapabilityChips/>`; its existing tests must stay green unchanged (the extraction is behavior-preserving there).
- [ ] **Step 4: Run the fleet test suite; commit** — `git commit -m "refactor(fleet): shared provenance-aware CapabilityChips (icons + tooltip)"`

---

### Task 7: Live Models page — capability icons, provider identity per row, honest context markers

**Files:**
- Modify: `dashboard/src/models/ModelsSurface.tsx` (rows gain `<CapabilityChips/>`, `<ProviderLogo/>` + provider name; context shows ≈/✓ by provenance; groups remain per (provider, model))
- Test: `dashboard/src/models/ModelsSurface.test.tsx`

**Interfaces:**
- Consumes: `CapabilityChips` (Task 6), `ProviderLogo` (`dashboard/src/fleet/ProviderLogo.tsx`), the payload's `context_provenance` (already serialized — verify the field name in `controlClient.ts`/the `/models` response before use) and capability `provenance` (Task 5).

Owner requirements (2026-08-05): (a) capabilities as icon chips on this page too; (b) **every provider's model is its own row — the same model via two providers appears twice, never merged, never skipped** — each row identifiable by provider logo + name; (c) context/rating visible per row.

- [ ] **Step 1: Read `ModelsSurface.tsx` fully** (rows at ~`:240`, group header at ~`:590`) and its test file — map where offerings render inside a group.
- [ ] **Step 2: Write the failing tests** — (1) two offerings of the same display name from different providers render two rows, each with its provider's logo (query by `aria-label`/test-id from ProviderLogo's contract); (2) a capability list renders icon chips (the `CapabilityChips` test-id), no bare text operation words; (3) context cell shows `≈ 200K` for `context_provenance === "provider_cap"` and `✓ 200K` for `"native"`, `ctx unknown` only for `"unknown"` (with a `title` explaining ≈ declared / ✓ verified).
- [ ] **Step 3: Run to verify failure.**
- [ ] **Step 4: Implement.** Formatting: reuse the existing token formatter in ModelsSurface (the one producing "200K" today) — prepend the provenance mark, never a new formatter.
- [ ] **Step 5: Run dashboard suites + e2e** (`npm run typecheck && npm run lint && npx vitest run && npm run build && npm run test:e2e` — Live Models copy changes; fix spec EXPECTATIONS, and note visual-baseline drift for the dispatch-job flow, same rule as Plan 1 Task 10).
- [ ] **Step 6: Commit** — `git commit -m "feat(models): capability icon chips, per-provider rows, provenance-marked context on Live Models"`

---

### Task 8: Full gate + evidence

- [ ] **Step 1:** `task gate` on Windows.
- [ ] **Step 2:** dashboard: typecheck, lint, vitest, build, e2e.
- [ ] **Step 3:** `go test -race ./internal/...`.
- [ ] **Step 4:** Report: Task 4/5 mutation evidence, the JSON1-availability finding (Task 3), the claude-code context-fields finding (Task 2), and expected visual-baseline drift.

---

## Self-Review (done at write time)

- **Spec coverage:** §3.D no-discarded-metadata → Task 2; vocabulary → Task 1; clinepass enrichment → Task 3 (models.dev has no clinepass entry — the spec's enrichment intent is honored via candidates + runtime proof, which is STRICTER than declared enrichment; noted for the owner); provenance → Tasks 5-6; icon-chips owner note → Tasks 6-7; per-provider rows owner note → Task 7; §3.E write-back/declared/display → Tasks 2, 4, 7; vision stays ≈ (no vision witness — nothing here certifies vision beyond declarations).
- **Placeholder scan:** every read-first step names the exact file/lines and fixes the assertion independent of what's found; no TBDs.
- **Type consistency:** `CandidateOperations` (Task 3) consumed by discovery pipeline in the same task; `Provenance` values `"probed"|"declared"|""` used identically in Tasks 5-7; `SetNativeContextTokens(ctx, modelID string, tokens int) error` defined and consumed in Task 4.
