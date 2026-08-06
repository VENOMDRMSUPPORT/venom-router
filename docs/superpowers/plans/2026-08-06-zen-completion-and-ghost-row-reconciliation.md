# opencode-zen Completion and Ghost-Row Reconciliation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the owner's two connected providers report complete, correct capabilities — and stop the dashboard showing capability rows that can never resolve.

**Architecture:** Two data-layer fixes, no UI work. First, opencode-zen stops hand-rolling its own capability derivation and shares the one in `modelsdev.go`, keeping only its free-only cost filter as its own policy. Second, discovery reconciles an offering's operation rows against what it currently declares, deleting rows that were never certified and are no longer declared — which self-heals the leftovers from a mechanism removed in the previous plan.

**Tech Stack:** Go, SQLite (goose migrations — **none needed**; reconciliation happens in the discovery transaction). No dashboard changes.

**Spec:** `docs/superpowers/specs/2026-08-05-model-qualification-pipeline-design.md`

## Live evidence this plan is built on

Audited on 2026-08-06 against the owner's running instance (`/api/control/v1/offerings`) and the live models.dev dataset. All 20 offerings across the owner's two connected providers.

**ClinePass — capabilities and context are correct.** All 11 catalogued models match models.dev exactly, including every context and max-output number. `cline-pass/qwen3.8-max` is genuinely absent from models.dev and correctly reports nothing beyond chat.

**ClinePass carries 8 ghost rows.** `structured_output` sits in `probing`/`unknown` on `mimo-v2.5`, `mimo-v2.5-pro`, `minimax-m3`, `qwen3.7-max` and `qwen3.7-plus` — none of which models.dev declares `structured_output` for. `qwen3.8-max` carries three: `tools`, `structured_output`, `context_window`. These are leftovers of the `CandidateOperations` mechanism removed in commit `bc56160`; the rows persisted in the database. They drive the amber "Certification review backlog (at least 7)" banner, the "Needs review" badges, and every yellow "Not routable" row the owner asked about.

**opencode-zen is missing operations on all 8 models:**

| Model | models.dev declares | live shows | missing |
|---|---|---|---|
| `big-pickle` | chat, tools, structured_output, context_window, reasoning | chat, tools | structured_output, context_window, reasoning |
| `deepseek-v4-flash-free` | chat, tools, structured_output, context_window, reasoning | chat, tools | structured_output, context_window, reasoning |
| `laguna-s-2.1-free` | chat, tools, context_window, reasoning | chat, tools | context_window, reasoning |
| `ling-3.0-flash-free` | chat, tools, context_window, reasoning | chat, tools | context_window, reasoning |
| `longcat-2.0-free` | chat, tools, context_window, reasoning | chat, tools | context_window, reasoning |
| `mimo-v2.5-free` | chat, tools, vision, context_window, reasoning | chat, tools, vision | context_window, reasoning |
| `nemotron-3-ultra-free` | chat, tools, context_window, reasoning | chat, tools | context_window, reasoning |
| `north-mini-code-free` | chat, tools, structured_output, context_window, reasoning | chat, tools | structured_output, context_window, reasoning |

Context numbers are already correct for all eight; it is the operation rows that are not emitted.

## Global Constraints

- **English-only files.** Zero Arabic in any repo file — code, comments, docs, commit messages.
- **Strict TDD.** Write the failing test, run it, see it fail for the right reason, then implement.
- **No live network in tests.** models.dev is reached only through injected seams; use the fixture at `internal/providers/testdata/modelsdev-fixture.json`, embedded as `modelsDevFixture` in `modelsdev_test.go`.
- **Mutation-proof every fix.** Mutate the composition root, not a test-owned copy, and confirm the test fails at a RUNTIME assertion, not a compile error.
- **No tautological assertions.**
- **Never-downgrade invariant.** Source unavailability is "no new evidence", never "capability withdrawn". This is load-bearing for Task 2: a certified capability must survive a discovery run that temporarily cannot see the catalog.
- **`nil` means unknown, never `0`.**
- **Commit per green step.** Full `task gate` on Windows before any green claim; read the step ORDER, an early failure masks later steps.
- **Do not touch** `G:\Venom-Router` or `F:\projects\venom-router`.

---

## File Structure

| File | Responsibility | Change |
|---|---|---|
| `internal/providers/opencode_zen.go` | zen adapter: free-only filter + discovery | Modify: parse the full fact set, delete `zenModelFacts` and `zenCapabilities`, delegate to the shared derivation |
| `internal/providers/opencode_zen_test.go` | zen adapter tests | Modify |
| `internal/storage/discovery.go` | discovery snapshot apply | Modify: reconcile operation rows inside the existing transaction |
| `internal/storage/discovery_test.go` | storage tests | Modify |

---

## Task 1: opencode-zen shares the one capability derivation

`zenCapabilities` (`internal/providers/opencode_zen.go:249`) reads only `ToolCall`, `ImageInput` and the chat gate. `structured_output`, `reasoning`, `context_window` and `image_generation` are never emitted, so every one of the owner's eight zen models is missing at least two operations.

The cost filter is genuinely zen's own policy and stays. The FACT TYPE and the DERIVATION are not zen-specific and should not be duplicated.

**Files:**
- Modify: `internal/providers/opencode_zen.go` — `zenModelFacts` (`:105-127`), `modelsDevModel` (`:300-318`), `parseModelsDevFreeSet` (`:325-357`), `zenCapabilities` (`:249-262`), the `DiscoveredModel` construction (`:207-214`)
- Test: `internal/providers/opencode_zen_test.go`

**Interfaces:**
- Consumes: `ModelsDevFacts` and `OperationsFromFacts` from `internal/providers/modelsdev.go`; `containsImageModality`, `allTextModalities`, `declaresNonTextOnlyOutput`.
- Produces: `parseModelsDevFreeSet(body []byte) (map[string]ModelsDevFacts, error)` — same signature except the value type. `zenModelFacts` and `zenCapabilities` are deleted.

- [ ] **Step 1: Write the failing test**

Add to `internal/providers/opencode_zen_test.go`. Use the adapter's existing test helpers for the models-list and models.dev seams — read the neighbouring tests and match their style rather than inventing a new harness.

```go
func TestOpenCodeZen_DiscoveryEmitsEveryCatalogBackedOperation(t *testing.T) {
	// Two free entries taken from the live dataset's shape: one declaring
	// structured_output and reasoning, one declaring neither.
	dataset := []byte(`{"opencode":{"models":{
		"rich-free":{"id":"rich-free","name":"Rich","tool_call":true,"structured_output":true,"reasoning":true,
			"modalities":{"input":["text","image"],"output":["text"]},
			"limit":{"context":200000,"input":160000,"output":32000},
			"cost":{"input":0,"output":0}},
		"plain-free":{"id":"plain-free","name":"Plain","tool_call":true,"structured_output":false,"reasoning":false,
			"modalities":{"input":["text"],"output":["text"]},
			"limit":{"context":256000,"output":64000},
			"cost":{"input":0,"output":0}}
	}}}`)

	got := discoverZenModels(t, []string{"rich-free", "plain-free"}, dataset)
	byID := map[string]DiscoveredModel{}
	for _, m := range got {
		byID[m.ProviderModelID] = m
	}

	rich := byID["rich-free"]
	for _, want := range []string{"chat", "tools", "structured_output", "vision", "context_window", "reasoning"} {
		if !slices.Contains(rich.Capabilities, want) {
			t.Fatalf("rich-free capabilities = %v, want %q present — the dataset declares it explicitly", rich.Capabilities, want)
		}
	}
	if rich.MaxInputTokens == nil || *rich.MaxInputTokens != 160000 {
		t.Fatalf("rich-free MaxInputTokens = %v, want 160000 from limit.input", rich.MaxInputTokens)
	}

	plain := byID["plain-free"]
	for _, notWant := range []string{"structured_output", "vision", "reasoning", "image_generation"} {
		if slices.Contains(plain.Capabilities, notWant) {
			t.Fatalf("plain-free capabilities = %v, want %q ABSENT — the dataset does not declare it", plain.Capabilities, notWant)
		}
	}
	for _, want := range []string{"chat", "tools", "context_window"} {
		if !slices.Contains(plain.Capabilities, want) {
			t.Fatalf("plain-free capabilities = %v, want %q present", plain.Capabilities, want)
		}
	}
}
```

Write `discoverZenModels(t, liveIDs []string, dataset []byte) []DiscoveredModel` as a test helper that drives the REAL `OpenCodeZenAdapter.DiscoverModels` through its injected seams. Do not construct `DiscoveredModel` values by hand — the derivation is what is under test.

- [ ] **Step 2: Run it and confirm it fails**

Run: `go test ./internal/providers/ -run TestOpenCodeZen_DiscoveryEmitsEveryCatalogBackedOperation -v`
Expected: FAIL — `structured_output`, `context_window` and `reasoning` are absent from `rich-free`.

- [ ] **Step 3: Widen the wire struct**

In `modelsDevModel` (`opencode_zen.go:300-318`), add the two fields the parse currently ignores, next to the existing `ToolCall`:

```go
	StructuredOutput bool `json:"structured_output"`
	Reasoning        bool `json:"reasoning"`
```

- [ ] **Step 4: Replace `zenModelFacts` with the shared fact type**

Delete the `zenModelFacts` struct (`opencode_zen.go:105-127`) entirely and change `parseModelsDevFreeSet` to build the shared type:

```go
func parseModelsDevFreeSet(body []byte) (map[string]ModelsDevFacts, error) {
```

and inside the free-entry branch:

```go
		if *m.Cost.Input == 0 && *m.Cost.Output == 0 {
			freeSet[id] = ModelsDevFacts{
				DisplayName:               m.Name,
				ToolCall:                  m.ToolCall,
				StructuredOutput:          m.StructuredOutput,
				Reasoning:                 m.Reasoning,
				ImageInput:                containsImageModality(m.Modalities.Input),
				ImageOutput:               containsImageModality(m.Modalities.Output),
				OutputAllText:             allTextModalities(m.Modalities.Output),
				OutputDeclaresNonTextOnly: declaresNonTextOnlyOutput(m.Modalities.Output),
				Deprecated:                m.Status == "deprecated",
				Context:                   m.Limit.Context,
				MaxInput:                  m.Limit.Input,
				Output:                    m.Limit.Output,
			}
		}
```

If `modelsDevModel` has no `Name` field, add `Name string \`json:"name"\`` — the shared type carries a display name and zen currently uses the raw id.

Update the map declaration and every other reference to the old type. The free-only cost filter above this branch is UNCHANGED — that is zen's own owner-policy contract and must not move into the shared parse.

- [ ] **Step 5: Delete `zenCapabilities` and delegate**

Remove `zenCapabilities` (`opencode_zen.go:249-262`) and change the `DiscoveredModel` construction (`:207-214`):

```go
		models = append(models, DiscoveredModel{
			ProviderModelID: m.ID,
			DisplayName:     m.ID,
			Capabilities:    OperationsFromFacts(facts),
			ContextLength:   facts.Context,
			MaxInputTokens:  facts.MaxInput,
			MaxOutputTokens: facts.Output,
		})
```

Leave `DisplayName: m.ID` as is — changing what the owner sees in the model list is not this task's business.

Add a comment above the call recording why the derivation is shared: zen's models.dev entries carry the same fields as every other provider's, so a second copy of the mapping only creates a second place to drift. The one thing that IS zen-specific — the free-only cost filter — stays in `parseModelsDevFreeSet`.

- [ ] **Step 6: Run and confirm it passes**

Run: `go test ./internal/providers/ -run TestOpenCodeZen -v`
Expected: PASS. Existing zen tests that asserted the narrower capability list must be UPDATED — read the dataset each one supplies and confirm the wider expectation is grounded in that entry's explicit fields before changing it. Never loosen an assertion, never delete a failing test.

- [ ] **Step 7: Confirm the cost filter is untouched**

Run the existing free-only tests specifically: `go test ./internal/providers/ -run 'FreeSet|Free' -v`
Expected: PASS unchanged. If a free/paid classification changed, you have moved the cost filter by mistake — revert and reapply.

- [ ] **Step 8: Mutation-proof**

Set `Reasoning: false` in the `parseModelsDevFreeSet` literal. `TestOpenCodeZen_DiscoveryEmitsEveryCatalogBackedOperation` MUST fail at a runtime assertion. Restore.

- [ ] **Step 9: Full package, then commit**

Run: `go test ./internal/providers/... -count=1`, `gofmt -l internal/providers`, `go vet ./internal/providers/...`, `go build ./...`

```bash
git add internal/providers/
git commit -m "fix(opencode-zen): share the one models.dev capability derivation

zenCapabilities read only tool_call and image input, so every one of the
eight zen models the owner has connected was missing context_window and
reasoning, and three were also missing structured_output -- all of them
declared explicitly in the same dataset entry the adapter already reads.

The free-only cost filter is zen's own owner-policy contract and stays.
The fact type and the mapping are not zen-specific, and a second copy
only created a second place to drift.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## Task 2: discovery reconciles an offering's operation rows

Eight `offering_operations` rows in the owner's live database describe operations no adapter declares any more. They were created by the `CandidateOperations` mechanism that commit `bc56160` removed; the code is gone but the rows are not. They sit in `probing`/`unknown`, no probe path can resolve them, and they are what the amber backlog banner counts.

A one-shot migration would clear today's rows and leave the same thing to recur whenever an adapter's declarations shrink. Reconciliation inside discovery fixes both: the existing rows disappear on the next discovery run, and the class of problem cannot come back.

**Files:**
- Modify: `internal/storage/discovery.go` — `applyModel` (`:231-313`), after the operation-row upsert loop
- Test: `internal/storage/discovery_test.go`

**Interfaces:**
- Consumes: `intelligence.DiscoverySnapshotModel.Operations` and `.CandidateOperations` (the union already written by the existing loop), the open `*sql.Tx`.
- Produces: no new exported symbol. Behaviour: after applying a model, any `offering_operations` row for that offering whose operation is NOT in the applied union AND whose certification has `capability_truth = 'unknown'` is deleted along with its `certifications` row.

**The safety rule, and why it is exactly this rule.** A row may only be pruned when it carries no evidence. `capability_truth = 'unknown'` means no probe ever resolved it and no declaration ever certified it — deleting it discards nothing. A row whose truth is `supported` or `unsupported` is EVIDENCE and must survive, even when the current run no longer declares that operation: a discovery run that cannot reach models.dev falls back to a narrower declaration set, and pruning certified rows there would be exactly the downgrade-on-missing-evidence the never-downgrade invariant forbids.

- [ ] **Step 1: Write the failing test**

In `internal/storage/discovery_test.go`, following the file's existing snapshot-apply test style:

```go
func TestApply_PrunesUndeclaredUncertifiedOperationRows(t *testing.T) {
	repo, db := newDiscoveryTestRepo(t)
	ctx := context.Background()

	// Run 1: the model declares chat and tools.
	applySnapshot(t, repo, ctx, "acct-1", "prov-1", []intelligence.DiscoverySnapshotModel{
		snapshotModel("m-1", []models.Operation{models.OperationChat, models.OperationTools}),
	})
	if got := operationsFor(t, db, "acct-1", "m-1"); !reflect.DeepEqual(got, []string{"chat", "tools"}) {
		t.Fatalf("after run 1 operations = %v, want [chat tools]", got)
	}

	// Run 2: tools is no longer declared and was never certified.
	applySnapshot(t, repo, ctx, "acct-1", "prov-1", []intelligence.DiscoverySnapshotModel{
		snapshotModel("m-1", []models.Operation{models.OperationChat}),
	})
	if got := operationsFor(t, db, "acct-1", "m-1"); !reflect.DeepEqual(got, []string{"chat"}) {
		t.Fatalf("after run 2 operations = %v, want [chat] — an undeclared, never-certified row must not survive; it is what the review backlog counts and no probe can resolve it", got)
	}
	if n := certificationCountFor(t, db, "acct-1", "m-1"); n != 1 {
		t.Fatalf("certifications for m-1 = %d, want 1 — the pruned operation's certification row must go with it", n)
	}
}

func TestApply_KeepsUndeclaredButCertifiedOperationRows(t *testing.T) {
	repo, db := newDiscoveryTestRepo(t)
	ctx := context.Background()

	applySnapshot(t, repo, ctx, "acct-1", "prov-1", []intelligence.DiscoverySnapshotModel{
		snapshotModel("m-1", []models.Operation{models.OperationChat, models.OperationTools}),
	})
	certifyOperationRow(t, db, "acct-1", "m-1", "tools", "supported")

	// The catalog was unreachable this run, so only chat is declared.
	applySnapshot(t, repo, ctx, "acct-1", "prov-1", []intelligence.DiscoverySnapshotModel{
		snapshotModel("m-1", []models.Operation{models.OperationChat}),
	})

	got := operationsFor(t, db, "acct-1", "m-1")
	if !reflect.DeepEqual(got, []string{"chat", "tools"}) {
		t.Fatalf("operations = %v, want [chat tools] — a certified capability is evidence and must survive a run that could not see the catalog (never-downgrade invariant)", got)
	}
}
```

Write the helpers (`newDiscoveryTestRepo`, `applySnapshot`, `snapshotModel`, `operationsFor`, `certificationCountFor`, `certifyOperationRow`) if the file has no equivalents — read it first and reuse whatever exists. `operationsFor` returns the operations sorted, so the assertions are order-independent. `certifyOperationRow` must set the truth through the same column the production code reads.

- [ ] **Step 2: Run and confirm the first test fails, the second passes**

Run: `go test ./internal/storage/ -run 'TestApply_.*OperationRows' -v`
Expected: `PrunesUndeclaredUncertified` FAILS (the stale row survives). `KeepsUndeclaredButCertified` PASSES already — it pins behaviour that must NOT change, so it passing now is correct and it must still pass after Step 3.

- [ ] **Step 3: Reconcile inside the transaction**

In `applyModel` (`internal/storage/discovery.go`), directly after the loop that upserts the union of `m.Operations` and `m.CandidateOperations` and populates `seenOps`, add:

```go
	// Reconcile: an operation row this offering no longer declares, and which
	// no probe or declaration ever resolved, describes nothing. It cannot be
	// certified (ListNonChatOperationsToCertify requires the operation to
	// appear in capabilities_json) and no probe path can reach it, so it would
	// sit in the review backlog forever. The CandidateOperations mechanism
	// removed in bc56160 left exactly these rows behind in live databases.
	//
	// A row whose truth is already supported or unsupported is EVIDENCE and is
	// kept, even though this run did not declare it: a discovery run that
	// cannot reach the catalog falls back to a narrower declaration set, and
	// deleting certified rows there would be a downgrade on missing evidence.
	declared := make([]any, 0, len(seenOps)+2)
	placeholders := make([]string, 0, len(seenOps))
	for op := range seenOps {
		declared = append(declared, string(op))
		placeholders = append(placeholders, "?")
	}
	notIn := ""
	if len(placeholders) > 0 {
		notIn = " AND oo.operation NOT IN (" + strings.Join(placeholders, ", ") + ")"
	}
	args := append([]any{accountID, m.ProviderModelID}, declared...)

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM certifications WHERE offering_operation_id IN (
		     SELECT oo.id FROM offering_operations oo
		     JOIN certifications c ON c.offering_operation_id = oo.id
		     WHERE oo.account_id = ? AND oo.provider_model_id = ?
		       AND c.capability_truth = 'unknown'`+notIn+`)`,
		args...,
	); err != nil {
		return fmt.Errorf("storage: prune stale certifications (%q,%q): %w", accountID, m.ProviderModelID, err)
	}

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM offering_operations
		 WHERE account_id = ? AND provider_model_id = ?
		   AND id NOT IN (SELECT offering_operation_id FROM certifications)`,
		accountID, m.ProviderModelID,
	); err != nil {
		return fmt.Errorf("storage: prune stale operations (%q,%q): %w", accountID, m.ProviderModelID, err)
	}
```

Two statements in this order because the certifications row references the operation row. The second delete is deliberately keyed on "has no certification row left" rather than repeating the operation filter — every operation row is created with a certification row in the same transaction (`ensureOfferingOperation`), so a row without one is exactly a row whose certification was just pruned.

Verify the real column names against the schema in `internal/storage/migrations/00006_catalog_discovery.sql` before running — use `capability_truth` only if that is what the table actually calls it. Add `"strings"` to the imports if absent.

- [ ] **Step 4: Run and confirm both pass**

Run: `go test ./internal/storage/ -run 'TestApply_.*OperationRows' -v`
Expected: both PASS.

- [ ] **Step 5: Run the whole storage package**

Run: `go test ./internal/storage/... -count=1`
Expected: PASS. A pre-existing test that asserted a stale row survives a re-apply was pinning the defect — update it and say which in your report. If a test breaks in a way you cannot explain as "the stale row is now pruned", STOP and report BLOCKED.

- [ ] **Step 6: Mutation-proof**

Change `c.capability_truth = 'unknown'` to `c.capability_truth != ''`. `TestApply_KeepsUndeclaredButCertifiedOperationRows` MUST fail — that is the never-downgrade guard doing its job. Restore, and confirm both tests pass again.

- [ ] **Step 7: Full gate, then commit**

Run: `go test ./... -count=1`, `gofmt -l internal`, `go vet ./...`, `go build ./...`, then `task gate` on Windows. Read the step ORDER and confirm the later steps executed.

```bash
git add internal/storage/
git commit -m "fix(storage): reconcile operation rows against what discovery declares

Eight rows in the owner's live database describe operations no adapter
declares any more -- leftovers of the CandidateOperations mechanism
removed in bc56160. They sit in probing/unknown, no probe path can reach
them, and they are what the dashboard's certification-review backlog
counts.

Discovery now deletes an operation row that is no longer declared AND
whose truth was never resolved. A certified row is evidence and survives
even when the current run declares less, because a run that could not
reach the catalog falls back to a narrower set and pruning there would
be a downgrade on missing evidence.

Reconciling rather than migrating means the existing rows clear on the
next discovery run and the class of problem cannot recur.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## Self-Review

**Evidence coverage.** opencode-zen's three missing operations → Task 1. The eight ClinePass ghost rows → Task 2. `cline-pass/qwen3.8-max`'s unknown context is NOT addressed here — it is genuinely absent from models.dev, and the automatic context measurement that would resolve it belongs to the spec's Phase 3.

**Deliberately out of scope:** every UI change the live audit surfaced — the `Discover`/`Benchmark`/`Probe` buttons, the amber banner itself, the `Cost unknown` chip, the `1 offering` caption, capability labels beside the icons, the missing `context_window` icon, and the `1M` versus `1.048576M` formatting. Task 2 removes the DATA that makes the banner and the "Needs review" badges appear; deleting the components is the next plan's work.

**Type consistency.** `parseModelsDevFreeSet` changes its value type from `zenModelFacts` to `ModelsDevFacts`; `zenModelFacts` and `zenCapabilities` are deleted, so no caller can reference the old shape and still compile. `OperationsFromFacts` already returns `[]string`, matching `DiscoveredModel.Capabilities`.

**Risk the reviewer should weigh.** Task 2 issues a `DELETE` inside the discovery transaction. The guard that makes it safe is a single SQL predicate. Both tests exist specifically to pin the two sides of it, and Step 6 mutates the predicate to prove the keep-side guard is real rather than incidental.
