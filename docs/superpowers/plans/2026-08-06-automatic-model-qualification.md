# Automatic Model Qualification Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Enabling a provider must automatically produce a performance score for every model and upgrade declared capabilities to measured ones — with no button for the owner to press, because there is no longer a button.

**Architecture:** Almost entirely wiring. The measurement machinery already exists, is tested, and runs never: `runBenchmarkSuite` → `measureBenchmarkStream` → `SetQualityRating` produces the score, and `CapabilityProbe` → `ProbeGuard` → `probeTransportAdapter` proves capabilities. Both are reachable only through HTTP endpoints that no longer have callers. This plan widens the one seam that makes two of the capability probes structurally impossible, then gives both machines automatic triggers on the existing scheduler.

**Tech Stack:** Go, SQLite, the existing `internal/execution` transports and `internal/intelligence` probe stack. No dashboard change. No migration.

**Spec:** `docs/superpowers/specs/2026-08-05-model-qualification-pipeline-design.md` §4.2 (Phase 2 — Live Qualification).

## Why this plan is mandatory, not optional

Commit `8611675` deleted the dashboard's `startBenchmark` and `f55c068` deleted `startProbe`. `POST /api/control/v1/models/{id}/benchmark` and `POST /api/control/v1/offerings/{id}/probe` are still mounted (`controlmux.go:355`, `:408`) with **no caller anywhere** — not the dashboard, not a tick, not a sweep. Consequences on the owner's live fleet right now:

- `models.quality_rating` has exactly one writer (`catalog.go:652`), reachable only from `runBenchmark`, reachable only from the deleted button. **`Not rated` is permanently unearnable.**
- No capability can move from `declared` to `probed`, so the provenance mark restored at `4347dc6` reads `declared` on every chip forever.

The owner sanctioned removing the buttons. The sequencing — shipping the spec's Phase 4 before its Phase 3 — was the controller's error. This closes it.

## Ground truth this plan is built on

Read at `c7af154`. Two structural facts shape every task below.

**The usability sweep and the probe stack are disjoint systems.** The sweep (`usabilityProviderSpecs()`, `usability_wiring.go:55-77`) hand-rolls `net/http` per provider across seven functions and never touches `intelligence.ProbeTransport`, `execution.InferenceTransport`, `CapabilityFixture` or `ProbeGuard`. The capability-probe chain is reachable only via the callerless probe endpoint. So the sweep gets none of `ProbeGuard`'s quota admission, and the capability machinery has never executed in production.

**The two paths disagree on concurrency because they share no limiter:** `DefaultProbeSafetyPolicy().MaxInFlightPerProvider` is 1 (`probesafety.go:101`); `usabilityProbeMaxConcurrency` is 4 (`usability_account.go:17`).

**Both probes that matter are impossible as written.** `probeadapters.go:206-210` builds every probe as `NormalizedRequest{Operation: OperationChat, Messages, MaxTokens}` — no `Tools`, no `Parts`, no `Stream`, no `ResponseFormat`. The tools fixture asks the model to "use the add tool if one is available" while declaring none; the vision fixture pastes a data URI into a text string. `probeWitnessOf` (`:143-152`) has no branch that can return `WitnessVisionAnswer`.

**What already works and must be reused, not rebuilt:**

| Machine | Location | State |
|---|---|---|
| Streamed TTFT + tokens/sec measurement | `benchmark_stream.go:240-276` (`measureBenchmarkStream`) | complete, tested |
| Suite + median + rating | `benchmark_engine.go:141-188` | complete, tested |
| The one `quality_rating` write, correctly scaled | `benchmark.go:305` + `benchmarkRatingColumnScale:76` | complete, tested |
| Capability probe with witness invariant | `intelligence/capabilityprobe.go` | complete; fixtures unusable |
| Quota-admitted probe budgeting | `intelligence/probesafety.go` | complete; never invoked |
| Per-account AIMD pacer + breaker | `usability_pacer.go` | complete, in use by the sweep |

## Global Constraints

- **English-only files.** Zero Arabic in any repo file — code, comments, docs, commit messages.
- **Strict TDD.** Failing test, run it, see it fail for the right reason, then implement.
- **No live network in tests.** Every provider call goes through an injected seam.
- **Mutation-proof at the composition root**, not a test-owned copy, and the proof must be a RUNTIME assertion failure.
- **Reuse over rebuild.** If a task tempts you to write a second median, a second stream drainer, or a second rating formula, stop — one already exists and is listed above.
- **Never fabricate a verdict from an infrastructure failure.** `04 §2` hard rule: a quota or rate-limit failure during a probe must never flip a capability to `false`. Only a genuine unsupported semantic response may.
- **Never downgrade on missing evidence.** A capability already `certified/supported` from a declaration must not become `unsupported` because a measurement could not run.
- **Commit per green step.** Full `task gate` on Windows before any green claim; read the step ORDER — an early failure masks later steps.
- **Do not touch** `internal/tray/`, `Taskfile.yml`, `.github/scripts/`, or `dashboard/`.

---

## File Structure

| File | Responsibility | Change |
|---|---|---|
| `internal/intelligence/probetransport.go` | probe port types | Modify: `ProbeMessage` gains parts; `ProbeRequest` gains tools, response format, stream |
| `internal/intelligence/capabilityprobe.go` | fixtures + witness rules | Modify: real tool definition, real image part, content-assertion vision witness |
| `internal/httpapi/probeadapters.go` | port → transport | Modify: map the new fields; recognize a vision answer |
| `internal/httpapi/qualification.go` | **NEW** — the automatic pass | Create |
| `internal/httpapi/qualification_test.go` | its tests | Create |
| `internal/app/boot.go` | scheduler wiring | Modify: register the tick |

---

## Task 1: Widen the probe port to match the transport beneath it

`intelligence.ProbeRequest` carries less than `execution.NormalizedRequest` can express, and `probeadapters.go` drops what little overlaps. That single narrowing is why the tools and vision probes cannot pass.

**Files:**
- Modify: `internal/intelligence/probetransport.go` (`ProbeMessage:12-15`, `ProbeRequest:24-33`)
- Modify: `internal/httpapi/probeadapters.go` (`Probe:179-236`, `probeWitnessOf:143-152`)
- Test: `internal/httpapi/probeadapters_test.go`

**Interfaces:**
- Consumes: `execution.ContentPart`, `ToolDefinition`, `NormalizedRequest.{Tools,ToolChoice,ResponseFormat}` — all already exist.
- Produces: `ProbeMessage.Parts []ProbePart`; `ProbeRequest.{Tools []ProbeTool, ResponseFormat string}`.

- [ ] **Step 1: Write the failing test**

In `internal/httpapi/probeadapters_test.go`, using the package's existing fake-transport helper (read the neighbouring tests first):

```go
func TestProbeAdapter_CarriesToolsPartsAndResponseFormatToTheTransport(t *testing.T) {
	var got execution.NormalizedRequest
	adapter := newProbeAdapterWithFakeTransport(t, func(_ context.Context, _ execution.ResolvedRoute, req execution.NormalizedRequest) (*execution.NormalizedResponse, error) {
		got = req
		return &execution.NormalizedResponse{HTTPStatus: 200, Message: execution.Message{Content: "ok"}}, nil
	})

	_, err := adapter.Probe(context.Background(), intelligence.ProbeRequest{
		AccountID: "acct-1", ProviderID: "prov-1", ProviderModelID: "m-1",
		OfferingOperationID: "oo-1", Operation: models.OperationTools,
		Messages: []intelligence.ProbeMessage{{
			Role: "user",
			Parts: []intelligence.ProbePart{
				{Kind: intelligence.ProbePartText, Text: "what colour"},
				{Kind: intelligence.ProbePartImage, ImageBase64: "iVBORw0KGgo=", MediaType: "image/png"},
			},
		}},
		Tools:           []intelligence.ProbeTool{{Name: "add", Description: "adds two numbers", ParametersJSON: `{"type":"object"}`}},
		ResponseFormat:  "json_object",
		MaxOutputTokens: 16,
	})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}

	if len(got.Tools) != 1 || got.Tools[0].Name != "add" {
		t.Fatalf("Tools = %#v, want the declared add tool — a tools probe that declares no tool can never produce a tool call", got.Tools)
	}
	if got.ResponseFormat != "json_object" {
		t.Fatalf("ResponseFormat = %q, want json_object", got.ResponseFormat)
	}
	if len(got.Messages) != 1 || len(got.Messages[0].Parts) != 2 {
		t.Fatalf("Messages = %#v, want one message carrying two parts — an image pasted into a text string is not an image", got.Messages)
	}
	if got.Messages[0].Parts[1].Kind != execution.ContentPartImage || got.Messages[0].Parts[1].ImageBase64 == "" {
		t.Fatalf("part 1 = %#v, want a base64 image part", got.Messages[0].Parts[1])
	}
	if got.Operation != execution.OperationTools {
		t.Fatalf("Operation = %q, want the operation under test, not a hardcoded chat", got.Operation)
	}
}
```

- [ ] **Step 2: Run it and confirm it fails**

Run: `go test ./internal/httpapi/ -run TestProbeAdapter_CarriesToolsPartsAndResponseFormat -v`
Expected: FAIL — the fields do not exist (compile error), which is correct at this step.

- [ ] **Step 3: Widen the port types**

In `internal/intelligence/probetransport.go`, add above `ProbeMessage`:

```go
// ProbePartKind mirrors execution.ContentPartKind. This package must not
// import internal/execution (see the layering test), so the vocabulary is
// restated here and mapped at the adapter.
type ProbePartKind string

const (
	ProbePartText  ProbePartKind = "text"
	ProbePartImage ProbePartKind = "image"
)

// ProbePart is one part of a multimodal probe message. An image probe that
// pastes a data URI into a text string is not an image probe — the provider
// receives text and answers as text, which is exactly why the vision fixture
// could never pass before this existed.
type ProbePart struct {
	Kind        ProbePartKind
	Text        string
	ImageURL    string
	ImageBase64 string
	MediaType   string
}

// ProbeTool is a function tool a probe declares. A tools probe that asks the
// model to "use the add tool if one is available" while declaring no tool
// cannot produce a tool call — the witness invariant then fails every time,
// and the capability reads unknown forever.
type ProbeTool struct {
	Name           string
	Description    string
	ParametersJSON string
}
```

Add `Parts []ProbePart` to `ProbeMessage` with a comment saying `Content` stays authoritative when `Parts` is empty, matching `execution.Message`. Add to `ProbeRequest`:

```go
	// Tools are declared to the provider so a tools probe can actually
	// elicit a tool call. Empty leaves the wire body unchanged.
	Tools []ProbeTool
	// ResponseFormat constrains the reply's shape ("json_object"). Empty
	// leaves the wire body unchanged.
	ResponseFormat string
```

- [ ] **Step 4: Map them at the adapter**

In `probeadapters.go`'s `Probe`, replace the message loop and the `NormalizedRequest` literal so that: parts map one-for-one onto `execution.ContentPart`; tools map onto `execution.ToolDefinition`; `ResponseFormat` passes through; and `Operation` is the real one rather than a hardcoded `OperationChat`.

Convert the operation with a small helper that maps `models.Operation` onto `execution.Operation` and falls back to `execution.OperationChat` for an operation the execution vocabulary does not carry — `context_window` is probed with a chat-shaped request and must keep working.

- [ ] **Step 5: Run and confirm it passes**

Run: `go test ./internal/httpapi/... ./internal/intelligence/... -count=1`
Expected: PASS. Existing probe tests must be unaffected — an unset `Tools`/`Parts`/`ResponseFormat` must produce a byte-identical request to today's.

- [ ] **Step 6: Give the fixtures real teeth**

In `internal/intelligence/capabilityprobe.go`, change `CapabilityFixture` to return the tool declaration and image part alongside the prose. Its signature widens; update `capabilityprobe.go`'s own call sites (`:179`, `:183`).

- The tools fixture declares an `add` tool with a real minimal JSON Schema.
- The vision fixture carries a `ProbePart{Kind: ProbePartImage}` with a solid-colour PNG and asks for the colour in one word. **Replace `visionFixtureDataURI`'s 1×1 transparent PNG** — a 1×1 transparent pixel has no colour to name. Use a small solid-colour PNG and record which colour in a constant beside it.
- The structured-output fixture sets `ResponseFormat: "json_object"` in addition to its prose.

- [ ] **Step 7: Make the vision witness reachable**

`probeWitnessOf` currently cannot return `WitnessVisionAnswer`. A vision answer is a **content assertion**: the reply names the fixture's colour. Give the adapter the expected answer so the classification stays in one place, and return `WitnessVisionAnswer` when the response content contains it, case-insensitively.

Add a test proving a response naming the colour yields `WitnessVisionAnswer` and one naming a different colour does not.

- [ ] **Step 8: Mutation-proof**

Delete the `Tools:` field from the `NormalizedRequest` literal in `probeadapters.go`. The Step 1 test MUST fail at a runtime assertion. Restore.

- [ ] **Step 9: Full gate, then commit**

Run: `go test ./... -count=1`, `gofmt -l internal`, `go vet ./...`, `go build ./...`, then `task gate`.

```bash
git add internal/intelligence/ internal/httpapi/
git commit -m "feat(probe): carry tools, image parts and response format to the transport

The probe port was defined narrower than the transport beneath it, and the
adapter dropped what overlapped: every probe went out as a plain chat
request with no Tools, no Parts and no ResponseFormat, and with a
hardcoded chat operation.

So the tools probe asked the model to use a tool it never declared, and the
vision probe pasted a data URI into a text string. Neither could ever
satisfy its witness. Of eight operations exactly one was truly provable.

Also replaces the vision fixture's 1x1 transparent PNG - a transparent
pixel has no colour to name - and makes the vision witness a content
assertion rather than an unreachable constant.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## Task 2: Produce the performance score automatically

Everything needed already exists and is tested. It has no caller.

**Files:**
- Create: `internal/httpapi/qualification.go`, `internal/httpapi/qualification_test.go`
- Modify: `internal/app/boot.go` (tick registration, `:530-545`)

**Interfaces:**
- Consumes: `runBenchmarkSuite` (`benchmark_engine.go:141`), `benchmarkStreamFn` (`:106`), `buildBenchmarkStreamFn` (`benchmark.go:141`), `storage.BenchmarkRunRepo.Insert`, `CatalogRepo.SetQualityRating`, `storage.BenchmarkRunRepo.LatestForModels`.
- Produces: `func BuildQualificationTick(db *storage.DB, kr *secrets.Keyring, now func() time.Time) (func(context.Context) error, error)` registered as scheduler tick `model_qualification`.

- [ ] **Step 1: Write the failing test**

```go
func TestQualificationTick_ScoresAModelThatHasNeverBeenMeasured(t *testing.T) {
	db := newTestDB(t)
	seedLiveChatOffering(t, db, "acct-1", "prov-1", "m-1")

	var streamed int
	tick := newQualificationTickForTest(t, db, func(context.Context, string, string, string, string, int) (benchmarkSample, error) {
		streamed++
		return benchmarkSample{OK: true, TTFT: 200 * time.Millisecond, TokensPerSec: 40}, nil
	})

	if err := tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if streamed == 0 {
		t.Fatal("the tick measured nothing; with the benchmark button deleted this tick is the only writer of a quality rating")
	}

	rating := qualityRatingOf(t, db, "m-1")
	if rating == nil {
		t.Fatal("quality_rating is still NULL — Not rated would stay unearnable")
	}
	if *rating <= 0 || *rating > 100 {
		t.Fatalf("quality_rating = %v, want the 0-100 column scale", *rating)
	}
}

func TestQualificationTick_SkipsAModelMeasuredRecently(t *testing.T) {
	db := newTestDB(t)
	seedLiveChatOffering(t, db, "acct-1", "prov-1", "m-1")
	seedBenchmarkRun(t, db, "m-1", time.Now().Add(-time.Hour))

	var streamed int
	tick := newQualificationTickForTest(t, db, func(context.Context, string, string, string, string, int) (benchmarkSample, error) {
		streamed++
		return benchmarkSample{OK: true}, nil
	})
	_ = tick(context.Background())

	if streamed != 0 {
		t.Fatalf("streamed %d times, want 0 — re-measuring every 30s would spend the owner's quota on a number that barely moves", streamed)
	}
}
```

Write the helpers against the real repos, not raw INSERTs. `seedLiveChatOffering` must produce a row `targetOffering`'s `LiveOnly: true` query actually returns — read `catalog.go:122-141` for what that requires.

- [ ] **Step 2: Run and confirm both fail**

Run: `go test ./internal/httpapi/ -run TestQualificationTick -v`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement the tick**

Create `internal/httpapi/qualification.go`. It selects models that need measuring, runs the existing suite, and persists through the existing writers. Do not reimplement any of the measurement.

Selection rule: a model with a live chat offering whose most recent `benchmark_runs` row is older than a TTL, or which has none. Use `BenchmarkRunRepo.LatestForModels` for the freshness read — it already exists and is indexed. Define the TTL as a named constant with a comment: measuring every scheduler round would spend real quota on a number that changes only when the provider's performance does.

Bound the work per round — a fixed small number of models per tick, so a fleet of 200 models does not stampede one provider. Document the cap and log what was skipped, because a silent cap reads as "everything was measured".

Wire `buildBenchmarkStreamFn` exactly as `NewBenchmarkHandler` does; do not construct a second dispatcher.

- [ ] **Step 4: Register the tick**

In `internal/app/boot.go`, add to the scheduler list after `model_usability`:

```go
		SchedulerTick{Name: "model_qualification", Run: qualificationRun},
```

Build it beside `usabilityService`, before the mux, and comment why it exists: the dashboard's benchmark trigger was removed on the owner's instruction, and this tick is now the only writer of `models.quality_rating`.

- [ ] **Step 5: Run and confirm both pass**

Run: `go test ./internal/httpapi/ -run TestQualificationTick -v`, then `go test ./... -count=1`.

- [ ] **Step 6: Composition-root mutation proof**

Remove the `model_qualification` entry from the scheduler list in `boot.go`. A test MUST fail. If only the unit test fails and nothing exercising the real boot wiring does, add a test that asserts the registered tick names include it — the wiring is the deliverable. Restore.

- [ ] **Step 7: Full gate, then commit**

```bash
git add internal/httpapi/ internal/app/
git commit -m "feat(qualification): measure the performance score automatically

Deleting the dashboard's benchmark trigger left models.quality_rating with
no writer at all: the endpoint is still mounted but nothing calls it, so
'Not rated' had become permanently unearnable on the owner's fleet.

This registers a scheduler tick over the measurement machinery that
already existed and was already tested - runBenchmarkSuite,
measureBenchmarkStream, SetQualityRating - rather than building a second
one. A freshness TTL and a per-round cap keep it from spending quota on a
number that barely moves.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## Task 3: Upgrade declared capabilities to measured ones

`certifyDeclaredCapabilities` (`usability_account.go:69-75`) certifies every non-chat declared capability `supported` **without inspecting the operation and without any runtime evidence**. That was correct while no prober existed. After Task 1, three of them are genuinely probeable.

**Files:**
- Modify: `internal/httpapi/qualification.go`
- Test: `internal/httpapi/qualification_test.go`

**Interfaces:**
- Consumes: `intelligence.NewCapabilityProbe`, `ProbeGuard`, the `probeTransportAdapter` from `controlmux.go:403`, `CertificationDriver.RecordAttempt`.
- Produces: capabilities whose `provenance` becomes `probed` in the read model.

- [ ] **Step 1: Write the failing test**

```go
func TestQualificationTick_UpgradesADeclaredCapabilityToProbed(t *testing.T) {
	db := newTestDB(t)
	seedCertifiedDeclaredCapability(t, db, "acct-1", "prov-1", "m-1", "tools")

	tick := newQualificationTickForTest(t, db,
		withCapabilityProbeResult(intelligence.ProbeResult{HTTPStatus: 200, Witness: intelligence.WitnessToolCall}))
	if err := tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}

	if !hasSucceededProbeRun(t, db, "acct-1", "m-1", "tools") {
		t.Fatal("no succeeded probe run recorded; the read model derives provenance=probed from exactly this, so the chip would keep reading 'declared' forever")
	}
}

func TestQualificationTick_RateLimitNeverDowngradesADeclaredCapability(t *testing.T) {
	db := newTestDB(t)
	seedCertifiedDeclaredCapability(t, db, "acct-1", "prov-1", "m-1", "tools")

	tick := newQualificationTickForTest(t, db,
		withCapabilityProbeResult(intelligence.ProbeResult{HTTPStatus: 429}))
	_ = tick(context.Background())

	state, truth := certificationOf(t, db, "acct-1", "m-1", "tools")
	if state != "certified" || truth != "supported" {
		t.Fatalf("certification = %s/%s, want certified/supported unchanged — 04 §2's hard rule is that a rate limit must never flip a capability to false", state, truth)
	}
}
```

- [ ] **Step 2: Run and confirm they fail**

- [ ] **Step 3: Probe the probeable, leave the rest alone**

In the tick, for each offering-operation that is `certified/supported` with no succeeded probe run, and whose operation is one of `tools`, `structured_output`, `vision`, run `CapabilityProbe` through `ProbeGuard`. Record the outcome with the existing `CertificationDriver`.

`context_window`, `reasoning`, `image_generation` and `chat` are not in that set: the first is measured in Task 4, and the rest have no probe by design (`reasoning` and `image_generation` have no wire expression on this seam; `chat` is proven by the usability sweep).

**The two invariants that govern this task, and the tests above pin both:**
- An inconclusive or infrastructure outcome leaves the existing certification untouched. `ClassifyProbeSignal` already encodes this — do not add a second interpretation.
- Only `reliableUnsupportedCodes` (`capabilityprobe.go:81-85`) may move a capability to `unsupported`.

- [ ] **Step 4: Reconcile the two concurrency limits**

`DefaultProbeSafetyPolicy().MaxInFlightPerProvider` is 1; the sweep's `usabilityProbeMaxConcurrency` is 4. They disagree because the two paths share no limiter. This tick runs under `ProbeGuard`, so it gets 1. Decide deliberately whether that is right for capability probes, state the reasoning in a comment, and if you raise it, raise it in the policy rather than bypassing the guard.

- [ ] **Step 5: Run, mutation-proof, commit**

Mutation: make the tick record a verdict on an inconclusive outcome. `TestQualificationTick_RateLimitNeverDowngrades...` MUST fail. Restore.

```bash
git add internal/httpapi/
git commit -m "feat(qualification): upgrade declared capabilities to measured ones

certifyDeclaredCapabilities certifies every non-chat declared capability
supported without inspecting the operation and without runtime evidence.
That was correct while no prober existed; after the probe port was
widened, tools, structured_output and vision are genuinely probeable.

The tick probes exactly those three and records the outcome through the
existing certification driver, so the read model's provenance moves from
declared to probed. An inconclusive or rate-limited outcome leaves the
existing certification untouched - a quota failure must never flip a
capability to false.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## Task 4: Measure the context window for models no catalog covers

`cline-pass/qwen3.8-max` reads `ctx unknown` on the owner's fleet and always will: it is absent from models.dev, and the context probe is gated behind `ExpensiveProbesEnabled: false` with no caller.

**Files:**
- Modify: `internal/httpapi/qualification.go`
- Test: `internal/httpapi/qualification_test.go`

**Interfaces:**
- Consumes: `intelligence.NewContextProbe`, `DiscoveryRepo.SetNativeContextTokens` (`storage/discovery.go:454`).
- Produces: `models.native_context_tokens` written for offerings whose context is unknown.

- [ ] **Step 1: Write the failing test**

```go
func TestQualificationTick_MeasuresContextOnlyWhenTheCatalogDidNot(t *testing.T) {
	db := newTestDB(t)
	seedLiveChatOffering(t, db, "acct-1", "prov-1", "known")   // context_length set
	seedLiveChatOfferingNoContext(t, db, "acct-1", "prov-1", "unknown")

	var probed []string
	tick := newQualificationTickForTest(t, db, withContextProbe(func(_ context.Context, modelID string) (int, bool) {
		probed = append(probed, modelID)
		return 200000, true
	}))
	_ = tick(context.Background())

	if !slices.Contains(probed, "unknown") {
		t.Fatal("an uncatalogued model was never measured; it would read ctx unknown forever")
	}
	if slices.Contains(probed, "known") {
		t.Fatal("a catalogued model was re-measured — the context probe declares 3,000,000 input tokens and is the most expensive probe in the system")
	}
}
```

- [ ] **Step 2-3: Run, then implement**

Run the context probe only for offerings whose effective context is nil. Enable expensive probes for this narrow path — through the policy, per-probe, not by disabling the guard. Honour the existing 7-day cooldown.

Also revive the dead rung: `probe.go:526` passes `rules = nil` to `NewContextProbe`, so the provider-specific regex ladder never runs. Supply the rules if any exist; if none are defined anywhere, say so in the report rather than leaving a silently dead parameter.

- [ ] **Step 4: Mutation-proof, gate, commit**

Mutation: remove the "context is nil" filter so every model is probed. The second assertion MUST fail. Restore.

```bash
git add internal/httpapi/
git commit -m "feat(qualification): measure context for models no catalog covers

cline-pass/qwen3.8-max is absent from models.dev and reads ctx unknown on
the owner's fleet with no path to ever resolving: the context probe is
disabled by default and had no caller.

The tick now runs it, and only for offerings whose context is genuinely
unknown - it declares 3,000,000 input tokens and is the most expensive
probe in the system, so re-measuring a catalogued model would spend real
quota to learn what the dataset already stated.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## Self-Review

**Spec coverage.** §4.2's mandatory-measurement rule → Task 2. Its gap-filling rule for capabilities → Task 3. Its "context measured only for models the catalog does not cover" → Task 4. The probe-port widening §4.2 names as the enabling change → Task 1.

**Deliberately out of scope.** `reasoning` and `image_generation` remain declared-only: `NormalizedResponse` carries no reasoning field and no image output part, verified by a repo-wide search of `internal/execution`. Merging the usability sweep onto the execution transport — the sweep's seven hand-rolled HTTP probes duplicate what the transport already does for all seven providers, but replacing a working, provider-tuned system is its own plan with its own risk.

**Known limitation this plan does not close.** The sweep still runs outside `ProbeGuard`, so its chat probes are not quota-admitted while this tick's capability probes are. That asymmetry is recorded rather than fixed here; fixing it means moving the sweep onto the guard, which belongs with the merge above.

**Risk.** Task 3 writes certification verdicts from automatic measurements for the first time. Both invariants that protect against a wrong verdict — infrastructure failure is never a verdict, and only three provider codes may prove absence — already exist in `ClassifyProbeSignal` and `reliableUnsupportedCodes`. The task's instruction is to route through them, not to reinterpret them, and its second test pins exactly that.
