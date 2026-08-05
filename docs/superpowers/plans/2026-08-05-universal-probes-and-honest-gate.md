# Universal Usability Probes + Honest Gate Implementation Plan (Plan 1 of 3)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Every provider's models are runtime-verified before they appear on Live Models or in any advertised count, with a probe engine that is fast (per-provider parallelism, fast-lane on discovery) and rate-limit-safe (AIMD + circuit breaker).

**Architecture:** Extends the shipped usability sweep (`internal/httpapi/usability_*.go`) from 2 to 7 providers via new per-provider probes behind the existing `usabilityProbeFn` seam; restructures the tick into per-provider parallel lanes paced by an AIMD controller with a per-account half-open breaker; adds a fast-lane trigger fired by successful discovery; and finally tightens the `LiveOnly` SQL predicate to require a certified+supported `chat` certification. Spec: `docs/superpowers/specs/2026-08-05-honest-model-verification-design.md` (§3.A–C).

**Tech Stack:** Go (modernc sqlite, table-driven tests, injected clocks), React+TypeScript dashboard (vitest), Playwright e2e.

## Global Constraints

- English only in every file (code, comments, commits). Chat is Arabic; files are not.
- Strict TDD: every task writes the failing test first and proves it fails.
- The verdict vocabulary (`zenChatUsability` and its 5 values) is REUSED by all new probes — do NOT rename it or invent a parallel taxonomy (clinepass already reuses it; renaming ripples through ~10 files for zero behavior).
- A transport failure is NEVER a verdict (no `RecordAttempt`); a provider error body IS a verdict. Preserve this rule in every new probe.
- Rate-limit responses are NEVER recorded as model verdicts (they map to the transient `zenChatFreeExhausted` → `SignalRateLimited`).
- Credentials travel only as auth headers, never logged; response bodies are read only to classify (limit with the existing `openCodeZenProbeBodyLimit` pattern).
- No live network in tests: probes are tested against `httptest.Server`; pacer/breaker tests use injected `now func() time.Time`.
- "X is wired" claims must be proven by mutating the PRODUCTION composition root (heuristics #36/#40) — the totality test in Task 5 and the mutation steps exist for this.
- Never assert a production value `== TheConstantUnderTest` (tautology trap) — pin literals.
- Gate before claiming green: `task gate` on Windows + `cd dashboard && npm run typecheck && npm run lint && npm test && npm run build` + `npm run test:e2e` (the count copy changes user-visible strings — heuristic #45).
- Known consequence: the Providers-surface visual baselines will drift when the count string changes (Task 10). Do NOT regenerate baselines yourself — that is a dispatch-job + owner-review flow; note the drift in the final report.
- The working tree carries uncommitted owner WIP in `dashboard/src/fleet/` (AccountRow.tsx, ProviderCard.tsx, fleet.css) and `Design_System/css/components-core.css`. Build on top of it; never revert or `git checkout` those files.
- Commit after every green step; pushing to main is authorized; end commit messages with the project's Co-Authored-By line.

---

### Task 1: Probe results carry Retry-After

**Files:**
- Modify: `internal/httpapi/usability_verify.go` (the `usabilityProbeFn` type + `executeChatUsabilityProbe`)
- Modify: `internal/httpapi/opencode_zen_usability.go` (`probeOpenCodeZenChatUsability`)
- Modify: `internal/httpapi/clinepass_usability.go` (`probeClinePassChatUsability`)
- Modify: `internal/httpapi/usability_account.go` (`verifyAccountChatUsability`)
- Modify: every existing test that fakes `usabilityProbeFn` (compiler finds them)
- Test: `internal/httpapi/usability_verify_test.go`, `internal/httpapi/opencode_zen_usability_test.go`

**Interfaces:**
- Produces: `type usabilityProbeResult struct { Verdict zenChatUsability; RetryAfter time.Duration }` and the new seam signature `type usabilityProbeFn func(ctx context.Context, baseURL, key, modelID string) (usabilityProbeResult, error)`. `executeChatUsabilityProbe` returns `(usabilityProbeResult, error)`. Task 6's pacer consumes `RetryAfter`; Tasks 2–4's probes must fill it.

The spec (§3.C.2) requires honoring `retry-after`/`retry-after-ms` verbatim. Today the probe seam returns only the verdict, so the header is thrown away.

- [ ] **Step 1: Write the failing test** — in `opencode_zen_usability_test.go`, add a case where the fake zen server answers 200 with a `FreeUsageLimitError` body AND a `Retry-After: 7` header; assert the probe returns `Verdict == zenChatFreeExhausted` and `RetryAfter == 7*time.Second`. Add a second case with `retry-after-ms: 1500` in the JSON body (zen's documented field) asserting `1500*time.Millisecond`. Body wins over header when both present.

```go
func TestProbeOpenCodeZen_RetryAfterSurfaced(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"error":{"type":"FreeUsageLimitError"}}`))
	}))
	defer srv.Close()
	res, err := probeOpenCodeZenChatUsability(context.Background(), srv.URL, "k", "m")
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if res.Verdict != zenChatFreeExhausted {
		t.Fatalf("verdict = %v, want zenChatFreeExhausted", res.Verdict)
	}
	if res.RetryAfter != 7*time.Second {
		t.Fatalf("retryAfter = %v, want 7s", res.RetryAfter)
	}
}
```

- [ ] **Step 2: Run to verify failure** — `go test ./internal/httpapi/ -run TestProbeOpenCodeZen_RetryAfterSurfaced` — expect a COMPILE error (`usabilityProbeResult` undefined / probe returns 2 values of old shape). A compile error is the correct red here because the seam type itself changes.
- [ ] **Step 3: Implement** — define `usabilityProbeResult` + new `usabilityProbeFn` in `usability_verify.go`; add a small helper in `opencode_zen_usability.go`:

```go
// usabilityRetryAfter extracts the provider's advertised backoff: the JSON
// body's retry-after-ms / retry-after fields win over the HTTP Retry-After
// header (seconds). 0 = nothing advertised.
func usabilityRetryAfter(header http.Header, body []byte) time.Duration {
	var env struct {
		Error struct {
			RetryAfterMS int `json:"retry-after-ms"`
			RetryAfter   int `json:"retry-after"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &env)
	if env.Error.RetryAfterMS > 0 {
		return time.Duration(env.Error.RetryAfterMS) * time.Millisecond
	}
	if env.Error.RetryAfter > 0 {
		return time.Duration(env.Error.RetryAfter) * time.Second
	}
	if s := header.Get("Retry-After"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 0
}
```

Both existing probes now `return usabilityProbeResult{Verdict: classify..., RetryAfter: usabilityRetryAfter(resp.Header, body)}, nil`. `executeChatUsabilityProbe` and `verifyAccountChatUsability` switch to `res.Verdict` (`s.Usable` counts `res.Verdict == zenChatUsable`; auth-stop checks `res.Verdict == zenChatAuthFailure`). Update the fake probes in existing tests mechanically.
- [ ] **Step 4: Run the full package** — `go test ./internal/httpapi/` — all green (the compiler already walked you to every fake).
- [ ] **Step 5: Commit** — `git commit -m "refactor(usability): probe results carry provider Retry-After"`

---

### Task 2: Generic OpenAI-compatible probe → agnes-ai, ollama-cloud, nvidia-nim

**Files:**
- Create: `internal/httpapi/openai_generic_usability.go`
- Test: `internal/httpapi/openai_generic_usability_test.go`

**Interfaces:**
- Consumes: `usabilityProbeResult`, `zenChatUsability`, `usabilityRetryAfter` (Task 1); `openCodeZenChatProbeRequest`/`openCodeZenChatProbeMessage` and `openCodeZenHTTPClient`/`openCodeZenProbeBodyLimit` (existing).
- Produces: `probeOpenAICompatibleChatUsability(ctx, baseURL, key, modelID string) (usabilityProbeResult, error)` — ONE function serving all three providers (their auth is identical `Authorization: Bearer` and their endpoint is `{base}/chat/completions` under each provider's versioned baseURL from `liveProviderBaseURLs`), and `classifyOpenAICompatibleChatUsability(status int, body []byte) zenChatUsability`.

These three providers speak plain OpenAI chat completions. Unlike zen, they signal with HTTP status first; the body refines. Taxonomy (table-driven — this IS the test):

| status | body cue | verdict |
| --- | --- | --- |
| 2xx | `choices` non-empty | `zenChatUsable` |
| 401 | any | `zenChatAuthFailure` |
| 402 | any | `zenChatPaidUnusable` |
| 403 | any | `zenChatPaidUnusable` (entitlement) |
| 404 | any | `zenChatPaidUnusable` (model not servable for this account — definitive) |
| 429 | any | `zenChatFreeExhausted` (transient) |
| 2xx | no choices / malformed | `zenChatInconclusive` |
| 5xx | any | `zenChatInconclusive` |

- [ ] **Step 1: Write the failing classifier test** — table-driven over the 8 rows above (real JSON bodies: `{"choices":[{"message":{"content":"ok"}}]}`, `{"error":{"message":"insufficient credits","code":402}}`, empty body, garbage bytes).

```go
func TestClassifyOpenAICompatibleChatUsability(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   zenChatUsability
	}{
		{"working", 200, `{"choices":[{"message":{"content":"hi"}}]}`, zenChatUsable},
		{"auth", 401, `{"error":{"message":"invalid api key"}}`, zenChatAuthFailure},
		{"payment", 402, `{"error":{"message":"insufficient credits"}}`, zenChatPaidUnusable},
		{"entitlement", 403, `{}`, zenChatPaidUnusable},
		{"unknown-model", 404, `{"error":{"message":"model not found"}}`, zenChatPaidUnusable},
		{"throttled", 429, `{}`, zenChatFreeExhausted},
		{"empty-200", 200, ``, zenChatInconclusive},
		{"server-error", 500, `{}`, zenChatInconclusive},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyOpenAICompatibleChatUsability(tc.status, []byte(tc.body)); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run to verify failure** — `go test ./internal/httpapi/ -run TestClassifyOpenAICompatible` → FAIL (undefined function).
- [ ] **Step 3: Implement classifier + probe.** The probe mirrors `probeOpenCodeZenChatUsability` line-for-line (same request struct, `MaxTokens: 1`, `"ping"`, bearer header, body limit) but posts to `baseURL+"/chat/completions"` — NOTE: `liveProviderBaseURLs` values already end with the version segment (e.g. `/v1`), unlike zen's probe which appends `/v1/...` itself. Confirm each provider's exact base in `internal/httpapi/chatcompletions.go:62-76` before choosing the path suffix, and pin it with the httptest assertion in Step 4's probe test.
- [ ] **Step 4: Probe wire test** — httptest server asserting method POST, path suffix `/chat/completions`, `Authorization: Bearer k`, and returning the working body → expect `zenChatUsable`; a second server returning 429 with `Retry-After: 3` → expect transient + 3s.
- [ ] **Step 5: Run the package, then commit** — `git commit -m "feat(usability): generic OpenAI-compatible chat probe for agnes/ollama/nvidia"`

---

### Task 3: gemini-cli probe (native_api generateContent)

**Files:**
- Create: `internal/httpapi/gemini_usability.go`
- Test: `internal/httpapi/gemini_usability_test.go`

**Interfaces:**
- Consumes: `usabilityProbeResult` (Task 1).
- Produces: `probeGeminiChatUsability(ctx, baseURL, key, modelID string) (usabilityProbeResult, error)`, `classifyGeminiChatUsability(status int, body []byte) zenChatUsability`.

Gemini is NOT OpenAI-shaped. Request: `POST {baseURL}/models/{modelID}:generateContent` with header `x-goog-api-key: {key}` and body `{"contents":[{"role":"user","parts":[{"text":"ping"}]}],"generationConfig":{"maxOutputTokens":1}}`. **Before writing the fixture, read `internal/providers/geminiwire.go` and the gemini entry in `liveProviderBaseURLs` — the wire mapping there is the verified source of truth for paths (`models/` prefix, `/v1beta`) and error shapes; the legacy-repo facts (gemini `models/` prefix, exclusion regex) are recorded in memory and `venom-router-legacy`. Do not guess.**

Google error envelope: `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":...}}`. Taxonomy:

| signal | verdict |
| --- | --- |
| 2xx + non-empty `candidates` | `zenChatUsable` |
| `status == "UNAUTHENTICATED"` or HTTP 401 | `zenChatAuthFailure` |
| `status == "PERMISSION_DENIED"` or HTTP 403 | `zenChatPaidUnusable` |
| `status == "NOT_FOUND"` or HTTP 404 | `zenChatPaidUnusable` |
| `status == "RESOURCE_EXHAUSTED"` or HTTP 429 | `zenChatFreeExhausted` |
| anything else | `zenChatInconclusive` |

- [ ] **Step 1: Write the failing classifier test** — table-driven over the 6 rows (bodies: `{"candidates":[{"content":{"parts":[{"text":"x"}]}}]}`, each error status string, garbage).
- [ ] **Step 2: Run to verify failure.**
- [ ] **Step 3: Implement classifier + probe** (own request/response structs in the new file; body-status precedence like zen: a parseable error envelope wins over the HTTP code).
- [ ] **Step 4: Probe wire test** — httptest asserting the exact path `/models/gemini-x:generateContent`, the `x-goog-api-key` header (NOT Authorization), and `maxOutputTokens: 1` in the body.
- [ ] **Step 5: Run the package, then commit** — `git commit -m "feat(usability): gemini-cli chat usability probe over generateContent"`

---

### Task 4: claude-code probe (anthropic messages, minimal spend)

**Files:**
- Create: `internal/httpapi/claude_code_usability.go`
- Test: `internal/httpapi/claude_code_usability_test.go`

**Interfaces:**
- Consumes: `usabilityProbeResult` (Task 1); the credential-decoration pattern from `probeClinePassChatUsability` (`internal/httpapi/clinepass_usability.go:148-156` — token JSON in, access token extracted, unparseable ⇒ `zenChatAuthFailure` verdict, not an error).
- Produces: `probeClaudeCodeChatUsability(ctx, baseURL, credentialPlaintext, modelID string) (usabilityProbeResult, error)`, `classifyClaudeCodeChatUsability(status int, body []byte) zenChatUsability`.

Spec D6: claude-code's probe is an entitlement confirmation at the smallest possible spend — `max_tokens: 1`, `"ping"`. **The exact headers (anthropic-version value, any oauth beta header) must be copied from the production anthropic_messages codec — find it via `grep -r "anthropic-version" internal/` — not guessed.** Request body: `{"model": modelID, "max_tokens": 1, "messages":[{"role":"user","content":"ping"}]}`. Credential: token JSON `{"access_token":...}` → `Authorization: Bearer`.

Anthropic error envelope: `{"type":"error","error":{"type":"authentication_error"|"permission_error"|"rate_limit_error"|"overloaded_error"|"not_found_error",...}}`. Taxonomy:

| error.type | verdict |
| --- | --- |
| (2xx, `content` non-empty) | `zenChatUsable` |
| `authentication_error` | `zenChatAuthFailure` |
| `permission_error`, `billing_error` | `zenChatPaidUnusable` |
| `not_found_error` | `zenChatPaidUnusable` (model not on this plan) |
| `rate_limit_error`, `overloaded_error` | `zenChatFreeExhausted` (transient) |
| unknown error type / malformed | `zenChatInconclusive` |

- [ ] **Step 1: Write the failing classifier test** (table-driven over all rows; a 200-status body carrying the error envelope must still classify by the envelope — body wins).
- [ ] **Step 2: Run to verify failure.**
- [ ] **Step 3: Implement** (unparseable/empty `access_token` ⇒ `usabilityProbeResult{Verdict: zenChatAuthFailure}, nil` exactly like clinepass).
- [ ] **Step 4: Probe wire test** — httptest asserting path `/v1/messages`, bearer header, the anthropic-version header value you copied from the codec, and `"max_tokens":1` in the body.
- [ ] **Step 5: Run the package, then commit** — `git commit -m "feat(usability): claude-code minimal entitlement probe (anthropic messages)"`

---

### Task 5: Specs map 2 → 7 + catalog-derived totality test

**Files:**
- Modify: `internal/httpapi/usability_wiring.go:42-47` (`usabilityProviderSpecs`)
- Test: `internal/httpapi/usability_totality_test.go` (new)

**Interfaces:**
- Consumes: the three new probes (Tasks 2–4); `liveProviderBaseURLs()` (`internal/httpapi/chatcompletions.go:62-76`); the provider registry (`internal/httpapi/provider_registry.go`, `newProviderRegistry()`).
- Produces: `usabilityProviderSpecs()` covering all 7 probe-capable providers — the map later tasks and the tick consume.

- [ ] **Step 1: Write the failing totality test.** Derived, not hardcoded (spec §3.B): every provider that `newProviderRegistry()` registers a discovery adapter for MUST have a `usabilityProviderSpecs()` entry whose baseURL matches `liveProviderBaseURLs()` for that provider. Inspect `provider_registry.go` first for the registry's actual accessor shape and adjust the iteration accordingly — the assertion (set equality, derived from the registry) is the fixed requirement.

```go
func TestUsabilitySpecs_TotalOverRegisteredDiscoveryAdapters(t *testing.T) {
	specs := usabilityProviderSpecs()
	bases := liveProviderBaseURLs()
	for _, providerID := range registeredDiscoveryProviderIDs() { // derive from newProviderRegistry()
		spec, ok := specs[providerID]
		if !ok {
			t.Errorf("provider %q has a discovery adapter but NO usability probe — it would ship unswept", providerID)
			continue
		}
		if base, ok := bases[providerID]; ok && spec.baseURL != base {
			t.Errorf("provider %q: usability baseURL %q != live baseURL %q", providerID, spec.baseURL, base)
		}
		if spec.probe == nil {
			t.Errorf("provider %q: nil probe", providerID)
		}
	}
	for providerID := range specs {
		if !slices.Contains(registeredDiscoveryProviderIDs(), providerID) {
			t.Errorf("usability spec for %q has no discovery adapter — dead entry", providerID)
		}
	}
}
```

- [ ] **Step 2: Run to verify failure** — RED listing exactly agnes-ai, gemini-cli, ollama-cloud, nvidia-nim, claude-code as missing.
- [ ] **Step 3: Extend the map** — add the five entries (agnes/ollama/nvidia → `probeOpenAICompatibleChatUsability`; gemini-cli → `probeGeminiChatUsability`; claude-code → `probeClaudeCodeChatUsability`), each baseURL taken from `liveProviderBaseURLs()`'s value for that provider (import/reference, don't retype string literals).
- [ ] **Step 4: Run to verify green.**
- [ ] **Step 5: Mutation proof (composition root)** — temporarily delete the `gemini-cli` line from the PRODUCTION map, run the totality test, confirm RED, restore byte-identical (sha256 before/after). Record the red output in the task report.
- [ ] **Step 6: Commit** — `git commit -m "feat(usability): all 7 discovery providers carry a chat usability probe + derived totality guard"`

---

### Task 6: Pacer — AIMD concurrency + per-account half-open breaker

**Files:**
- Create: `internal/httpapi/usability_pacer.go`
- Test: `internal/httpapi/usability_pacer_test.go`

**Interfaces:**
- Consumes: nothing project-specific (pure, clock-injected).
- Produces:

```go
// newUsabilityPacer(maxConcurrency int, now func() time.Time) *usabilityPacer
type usabilityPacer struct{ /* mu, cur, max, consecutiveRL int; pausedUntil time.Time; halfOpen bool; now func() time.Time */ }
func (p *usabilityPacer) Concurrency() int                       // current allowed parallel probes (>=1)
func (p *usabilityPacer) Admit() bool                            // breaker gate; half-open admits exactly one
func (p *usabilityPacer) OnSuccess()                             // +1 up to max; closes breaker; resets streak
func (p *usabilityPacer) OnRateLimited(retryAfter time.Duration) // halve (floor 1); 3rd consecutive opens breaker
```

Semantics (spec §3.C.2-3): multiplicative decrease on every rate-limit (halve, floor 1); additive increase on success (+1, cap max); 3 consecutive rate-limits open the breaker for `retryAfter` (or 30s when 0); after the pause one probe is admitted (half-open) — its `OnSuccess` closes the breaker, its `OnRateLimited` re-arms the same pause.

- [ ] **Step 1: Write the failing tests** — table of scenarios with a fake clock `now`:

```go
func TestPacer_AIMDAndBreaker(t *testing.T) {
	clock := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	p := newUsabilityPacer(4, now)

	if got := p.Concurrency(); got != 4 {
		t.Fatalf("initial concurrency = %d, want 4", got)
	}
	p.OnRateLimited(0)
	if got := p.Concurrency(); got != 2 {
		t.Fatalf("after 1st RL = %d, want 2 (halved)", got)
	}
	p.OnRateLimited(0)
	if got := p.Concurrency(); got != 1 {
		t.Fatalf("after 2nd RL = %d, want 1 (floor)", got)
	}
	if !p.Admit() {
		t.Fatal("breaker must still admit before the 3rd consecutive rate-limit")
	}
	p.OnRateLimited(10 * time.Second) // 3rd consecutive -> open with the advertised pause
	if p.Admit() {
		t.Fatal("breaker open: must not admit")
	}
	clock = clock.Add(11 * time.Second)
	if !p.Admit() {
		t.Fatal("cooldown elapsed: half-open must admit exactly one")
	}
	if p.Admit() {
		t.Fatal("half-open: the second concurrent admit must be refused")
	}
	p.OnSuccess()
	if !p.Admit() {
		t.Fatal("success in half-open must close the breaker")
	}
	if got := p.Concurrency(); got != 2 {
		t.Fatalf("post-success concurrency = %d, want 2 (additive +1 from 1)", got)
	}
}
```

- [ ] **Step 2: Run to verify failure.**
- [ ] **Step 3: Implement** — one mutex-guarded struct; no goroutines inside; time only through `now`.
- [ ] **Step 4: Run to verify green; run with `-race`.**
- [ ] **Step 5: Commit** — `git commit -m "feat(usability): AIMD pacer with per-account half-open circuit breaker"`

---

### Task 7: Parallel sweep — per-provider lanes, paced per-model concurrency

**Files:**
- Modify: `internal/httpapi/usability_account.go` (`verifyAccountChatUsability` gains the pacer + bounded parallelism)
- Modify: `internal/httpapi/usability_tick.go` (`usabilityTick.Run` fans out per provider)
- Modify: `internal/httpapi/usability_wiring.go` (verifier holds a per-account pacer factory; per-provider deadline replaces the global 25s)
- Test: existing `usability_account_test.go`, `usability_tick_test.go` extended

**Interfaces:**
- Consumes: `usabilityPacer` (Task 6), `usabilityProbeResult` (Task 1).
- Produces: `verifyAccountChatUsability(ctx, rec certRecorder, probe usabilityProbeFn, pacer *usabilityPacer, baseURL, key string, offerings []chatOffering) usabilityRunSummary` (new `pacer` param); `usabilityTick.Run` unchanged in signature.

Behavior:
1. **Per-model parallelism inside one account:** a worker pool sized by `pacer.Concurrency()` re-read between waves (halving takes effect next wave); each probe first checks `pacer.Admit()` — refused probes are skipped (not verdicts; the next sweep retries). `zenChatFreeExhausted` → `pacer.OnRateLimited(res.RetryAfter)`; usable/paid/inconclusive → `pacer.OnSuccess()` (the request went through — only rate-limits shrink the window). Auth failure cancels the account's remaining probes (existing stop-on-auth rule, now via context cancel) and is recorded once.
2. **Per-provider lanes in the tick:** group `accountToVerify` by ProviderID; one goroutine per provider (WaitGroup), accounts sequential within a lane; each lane gets its own `context.WithTimeout(ctx, usabilitySweepBudget)` so a slow provider exhausts only its own budget.
3. **Pacer lifetime:** one pacer per account per sweep (created in the verify closure) — sweeps start fresh; the breaker protects within a sweep, `RetryAfter` skips protect across sweeps.

- [ ] **Step 1: Write the failing account-level test** — a fake probe that records max observed concurrency (atomic counter) over 8 offerings with pacer max 4: assert max in-flight ≤ 4 and all 8 probed; a second case where the probe returns `zenChatAuthFailure` on the 2nd result: assert `StoppedOnAuth` and probed < 8; a third where the probe always returns rate-limited: assert the pacer shrank (≤ 2 probes in the final wave) and NO verdict recorded as unsupported.
- [ ] **Step 2: Run to verify failure** (compile error on the new param is the initial red).
- [ ] **Step 3: Implement** the pool (errgroup or channel semaphore — match the codebase's existing style, `grep -r "errgroup" internal/` first).
- [ ] **Step 4: Write the failing tick test** — two providers, fake verifies that each sleep on a channel: assert both lanes run concurrently (second lane's verify observed before the first lane's completes), and one lane's error doesn't abort the other.
- [ ] **Step 5: Implement the fan-out; run the full package with `-race`.**
- [ ] **Step 6: Commit** — `git commit -m "feat(usability): per-provider parallel sweep with paced per-model concurrency"`

---

### Task 8: Fast-lane — discovery success triggers immediate verification

**Files:**
- Modify: `internal/httpapi/usability_wiring.go` (`BuildUsabilityTick` → `BuildUsabilityService`)
- Modify: `internal/app/boot.go` (registration site — keep tick name `opencode_zen_usability` or rename to `model_usability`; if renamed, grep for the string in tests/docs)
- Modify: `internal/httpapi/discovery.go` (fire the trigger at `runDiscovery` success)
- Modify: `internal/httpapi/controlmux.go` (thread the trigger into `DiscoveryHandler` — same pattern as the existing `discoveryTrigger` field `EnrollmentHandler` uses)
- Test: `internal/httpapi/usability_wiring_test.go`, `internal/httpapi/discovery_test.go`

**Interfaces:**
- Consumes: everything above.
- Produces:

```go
type UsabilityService struct {
	Tick          func(context.Context) error                 // the sweep, as today
	VerifyAccount func(ctx context.Context, providerID, accountID string) // fast-lane: one account now
}
func BuildUsabilityService(db *storage.DB, kr *secrets.Keyring, now func() time.Time) (*UsabilityService, error)
```

`VerifyAccount` re-checks eligibility + resolves the active credential itself (reuse the existing `usabilityAccountEligible` + `activeCredentialIDFor` closures) so a caller can hand it just IDs. It is a no-op for providers without a spec (map miss → return). `DiscoveryHandler` calls it in a detached goroutine (`context.WithoutCancel` + its own timeout — copy the detachment pattern from `TriggerBackgroundDiscovery`) ONLY after a discovery run completes successfully; a discovery failure fires nothing.

- [ ] **Step 1: Write the failing service test** — `BuildUsabilityService` returns both funcs non-nil; `VerifyAccount` with an unknown provider returns without touching the DB (fake by calling it with a provider absent from the specs map against an empty in-memory DB — no panic, no rows).
- [ ] **Step 2: Run to verify failure.**
- [ ] **Step 3: Implement the service refactor** (the tick body moves unchanged; `VerifyAccount` is a thin resolver + `verifier.verifyAccount` call). Update `boot.go` registration.
- [ ] **Step 4: Write the failing trigger test** — in `discovery_test.go`: a successful discovery run calls the injected usability trigger with the run's provider+account exactly once; a failed run calls it zero times. Inject a recording fake through the same seam ControlMux wires.
- [ ] **Step 5: Implement the trigger wiring; run the package.**
- [ ] **Step 6: Mutation proof (composition root)** — delete the trigger-fire line from PRODUCTION `runDiscovery` success path; the Step 4 test must go RED; restore byte-identical. (This is the exact wrapper-hole shape that recurred twice — the test must drive the production handler, not its own assembly.)
- [ ] **Step 7: Commit** — `git commit -m "feat(usability): fast-lane verification fires immediately on discovery success"`

---

### Task 9: The honest gate — LiveOnly requires certified+supported chat

**Files:**
- Modify: `internal/storage/catalog.go:115-124` (the LiveOnly predicate)
- Test: `internal/storage/catalog_test.go` (real-sqlite)
- Verify (no change expected): `internal/httpapi/models.go` — `/models` passes LiveOnly=true (`models.go:318-323`), `/offerings` does NOT (`models.go:263-274`). DO NOT add LiveOnly to `/offerings` — that deadlocks certification (2026-08-04 incident).

**Interfaces:**
- Consumes: the schema (offering_operations UNIQUE(account_id, provider_model_id, operation); certifications PK offering_operation_id).
- Produces: the tightened predicate every Live Models read and Task 10's counts stand on.

- [ ] **Step 1: Write the failing real-sqlite test** — seed two offerings on one healthy connected account: model A with a `chat` offering_operation whose certification row is `status='certified', capability_truth='supported'`; model B with a chat op left at the seeded default (`discovered`/`unknown`). Assert `ListOfferings(LiveOnly: true)` returns ONLY A, and `LiveOnly: false` returns both. Add a third case: model C certified chat but on an UNHEALTHY account → excluded (existing account clause still applies).
- [ ] **Step 2: Run to verify failure** — B leaks through today.
- [ ] **Step 3: Implement** — extend the LiveOnly condition in `ListOfferings`:

```sql
AND EXISTS (
    SELECT 1
    FROM offering_operations oo
    JOIN certifications c ON c.offering_operation_id = oo.id
    WHERE oo.account_id = amo.account_id
      AND oo.provider_model_id = amo.provider_model_id
      AND oo.operation = 'chat'
      AND c.status = 'certified'
      AND c.capability_truth = 'supported'
)
```

Check with `EXPLAIN QUERY PLAN` that `idx_offering_operations_offering` carries the subquery (it does — the WHERE matches its columns); note the plan output in the report.
- [ ] **Step 4: Run storage + httpapi packages** — some httpapi read-model tests may seed offerings without chat certs and assert they appear via `/models`; fix those SEEDS (certify their chat op), never weaken the assertion. If a test asserts an UNCERTIFIED model appears on `/models`, it is asserting the bug — rewrite it to assert exclusion and cite this plan.
- [ ] **Step 5: Commit** — `git commit -m "feat(catalog): Live Models gate requires a certified+supported chat capability"`

---

### Task 10: Fleet headline counts — working first

**Files:**
- Modify: `dashboard/src/fleet/ProviderRow.tsx:212-215`
- Modify: `dashboard/src/fleet/FleetOverview.tsx` (stat-card labels feeding from `distinctModelStats` — follow `providerModelStats` at `:338-351` and the summary tiles at `:311`)
- Test: `dashboard/src/fleet/ProviderRow.test.tsx`, `dashboard/src/fleet/FleetOverview.test.tsx`
- Check: `tests/e2e/` specs and any vitest snapshot pinning the old `"N models"` copy (`grep -r "models\b" dashboard/src/fleet/*.test.tsx tests/e2e/`)

**Interfaces:**
- Consumes: `distinctModelStats` (`modelStatus.ts:49-57`) — unchanged; both numbers already flow into ProviderRow as `workingModelCount`/`uniqueModelCount`.
- Produces: the user-visible copy `"{working} working / {total} discovered"`.

- [ ] **Step 1: Write the failing test** — ProviderRow rendered with `workingModelCount={3} uniqueModelCount={12}` shows the text `3 working / 12 discovered` (and `— working / — discovered` when null).
- [ ] **Step 2: Run to verify failure** — `cd dashboard && npx vitest run src/fleet/ProviderRow.test.tsx`.
- [ ] **Step 3: Implement:**

```tsx
<span className="vnd-metric-chip vnd-metric-chip--healthy" title={`${workingModelCount ?? 0} out of ${uniqueModelCount ?? 0} models verified working`}>
  <span className="vnd-metric-dot vnd-metric-dot--healthy" aria-hidden="true" />
  {workingModelCount == null ? "—" : workingModelCount} working / {uniqueModelCount == null ? "—" : uniqueModelCount} discovered
</span>
```

Apply the same working-first treatment to the FleetOverview summary tiles that currently headline the raw total (read the component; the working number must be the prominent one, discovered stays visible).
- [ ] **Step 4: Run the dashboard suites** — `npm run typecheck && npm run lint && npx vitest run && npm run build`, then `npm run test:e2e` (user-visible copy changed — a11y/flows specs may pin the old string; fix the specs' EXPECTATIONS to the new copy, deriving from app constants where the a11y suite pattern allows).
- [ ] **Step 5: Commit** — `git commit -m "feat(fleet): provider counts lead with verified-working models"`

---

### Task 11: Full gate + evidence

- [ ] **Step 1:** `task gate` on Windows (all steps, in order — gofmt is step 5 and masks everything after it if red).
- [ ] **Step 2:** `cd dashboard && npm run typecheck && npm run lint && npx vitest run && npm run build && npm run test:e2e`.
- [ ] **Step 3:** `go test -race ./internal/httpapi/ ./internal/storage/`.
- [ ] **Step 4:** Report: per-task mutation evidence (Tasks 5, 8), the EXPLAIN QUERY PLAN note (Task 9), and the expected visual-baseline drift on the Providers surface (Task 10) — baselines are regenerated ONLY via the `visual-baselines-linux` dispatch job after owner review; do not commit new baselines.
- [ ] **Step 5:** Commit any stragglers; push is authorized.

---

## Self-Review (done at write time)

- **Spec coverage:** §3.A → Tasks 9-10; §3.B → Tasks 2-5; §3.C.1 → Task 7; §3.C.2-3 → Tasks 1, 6, 7; §3.C.4 → constants in Tasks 2-4 (max_tokens 1, existing body limits) + no-reprobe rule (already enforced by `ListChatOfferingsToVerify` selecting only `probing` rows); §3.C.5 → Task 8; §3.C.6 → ProbeGuard untouched (probes run under existing budgets). §3.D/E/F are Plans 2-3. D6 (claude-code minimal) → Task 4; its long recertify TTL = the existing 30-day default — no change needed, noted here so nobody "adds" it.
- **Placeholder scan:** all steps carry code or an exact grep/read instruction naming the source of truth; the two deliberate read-first instructions (gemini wire, anthropic headers) name the exact files.
- **Type consistency:** `usabilityProbeResult`/`usabilityProbeFn` (Task 1) consumed in 2-4, 7; `usabilityPacer` (Task 6) consumed in 7; `UsabilityService` (Task 8) consumed by boot + discovery; `zenChatUsability` values used verbatim everywhere.
