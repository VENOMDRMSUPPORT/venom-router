# Catalog Resolution and Routability Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make enabling ClinePass produce, with no human action, a full capability set and a real context window for every one of its models — and make those capabilities actually routable.

**Architecture:** A vertical slice. models.dev becomes a capability and limits source joined by an explicitly-mapped, base-URL-verified provider key; the offering's declared capability set becomes the union of the provider wire and that catalog; the transports stop under-declaring what they can carry; and the projection is finally handed the two inputs it needs to compute `effective` and `routable`. ClinePass is the pilot because its wire is the thinnest (`{id, name}` only) and its catalog coverage is the best-verified (11 of 12 ids join exactly). Replicating to the other adapters is mechanical and is a later plan.

**Tech Stack:** Go 1.x, SQLite (goose migrations — **none needed here**), `testing` stdlib. React/TS dashboard is untouched by this plan.

**Spec:** `docs/superpowers/specs/2026-08-05-model-qualification-pipeline-design.md`

## Global Constraints

- **English-only files.** Zero Arabic in any repo file — code, comments, docs, commit messages.
- **Strict TDD.** Write the failing test, run it, see it fail for the right reason, then implement.
- **No live network in tests.** models.dev and provider endpoints are reached only through injected seams. Use the vendored fixture from Task 3.
- **Mutation-proof every fix.** Mutate the **composition root**, not a test-owned copy. For each test, delete or invert the production behaviour it claims to pin and confirm the test fails. A test that still passes proves nothing. This has bitten this project twice.
- **No tautological assertions.** A test must never compare production output to a constant that production itself supplies.
- **No guessing.** Every metadata value is attributable to a named source. Never derive a capability from a model's name, id substring, or family.
- **Never-downgrade invariant.** Source unavailability is "no new evidence", never "capability withdrawn". Infrastructure failure never flips a capability to `unsupported`.
- **`nil` means unknown, never `0`.** Pointer limits stay `nil` when absent.
- **Commit per green step.** Full `task gate` on Windows before any "green" claim.
- **Do not touch** `G:\Venom-Router` or `F:\projects\venom-router`.

---

## File Structure

| File | Responsibility | Change |
|---|---|---|
| `internal/execution/types.go` | transport seam types | Modify: add `OperationStructuredOutput`, add `NormalizedRequest.ResponseFormat` |
| `internal/execution/openaicompat.go` | OpenAI-compatible wire | Modify: serialize `response_format`; correct `SupportedCapabilities` |
| `internal/execution/nativeoauth.go` | OAuth wire codecs | Modify: `openAIChatCodec` serializes `response_format`; correct all three codecs' `capabilities()` |
| `internal/execution/anthropicwire.go` | Anthropic wire | Modify: fail closed on `ResponseFormat` |
| `internal/execution/geminiwire.go` | Gemini wire | Modify: fail closed on `ResponseFormat` |
| `internal/execution/nativeapi.go` | Gemini transport | Modify: correct `SupportedCapabilities` |
| `internal/providers/modelsdev.go` | models.dev facts source | Modify: parse `reasoning`, image output, `limit.input`; shared operation derivation |
| `internal/providers/modelsdevkeys.go` | **NEW** — provider-slug → dataset-key map and its verification | Create |
| `internal/providers/testdata/modelsdev-fixture.json` | **NEW** — trimmed real dataset subset | Create |
| `internal/providers/clinepass.go` | ClinePass adapter | Modify: accept and consult the catalog |
| `internal/httpapi/clinepass_seams.go` | ClinePass registration | Modify: pass the models.dev probe |
| `internal/httpapi/provider_registry.go` | the one registry composition | Modify: nothing structural, verify the call still compiles |
| `internal/httpapi/publicmux.go` | duplicate registry list | Modify: delegate to `newProviderRegistry()` |
| `internal/httpapi/models.go` | read-model assembler | Modify: pass real `NativeCapabilities`/`TransportOperations`; project token limits |
| `internal/httpapi/controlmux.go` | composition root | Modify: hand the transport map to `ModelsHandler` |
| `internal/models/effective.go` | context precedence | Modify: third provenance source |
| `internal/intelligence/readmodel.go` | projection | Modify: project `MaxInputTokens`/`MaxOutputTokens` |

---

## Task 1: Let a request ask for structured output

Structured output is the one capability models.dev declares that no transport can currently express — there is no `response_format` anywhere in `internal/execution`. Without this, `structured_output` could never be `effective` and the owner's models would never route on it.

**Files:**
- Modify: `internal/execution/types.go:106-127` (`NormalizedRequest`)
- Modify: `internal/execution/openaicompat.go:81-92` (`chatRequest`), `:250-260`, `:328-338`
- Modify: `internal/execution/nativeoauth.go:345-356` (`openAIChatCodec` encode)
- Modify: `internal/execution/anthropicwire.go:85-88`
- Modify: `internal/execution/geminiwire.go:117-120`
- Test: `internal/execution/openaicompat_test.go`, `internal/execution/anthropicwire_test.go`, `internal/execution/geminiwire_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `execution.NormalizedRequest.ResponseFormat string` — empty means unset and the wire body must be byte-identical to today's. The only recognized non-empty value in this task is `"json_object"`.

- [ ] **Step 1: Write the failing test for OpenAI-compatible serialization**

In `internal/execution/openaicompat_test.go`:

```go
func TestOpenAICompatible_ResponseFormatIsSerialized(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{}"}}]}`))
	}))
	defer srv.Close()

	tr := NewOpenAICompatibleTransport(srv.Client(), 0)
	route := ResolvedRoute{Provider: "p", AccountID: "a", ModelID: "m", BaseURL: srv.URL, Credential: StoredCredentials{Value: "k"}}
	req := NormalizedRequest{
		Operation:      OperationChat,
		Messages:       []Message{{Role: "user", Content: "hi"}},
		ResponseFormat: "json_object",
	}
	if _, err := tr.Execute(context.Background(), route, req); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var body map[string]any
	if err := json.Unmarshal(gotBody, &body); err != nil {
		t.Fatalf("unmarshal sent body: %v", err)
	}
	rf, ok := body["response_format"].(map[string]any)
	if !ok {
		t.Fatalf("sent body = %s, want a response_format object", gotBody)
	}
	if rf["type"] != "json_object" {
		t.Fatalf("response_format.type = %v, want \"json_object\"", rf["type"])
	}
}

func TestOpenAICompatible_NoResponseFormatKeyWhenUnset(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer srv.Close()

	tr := NewOpenAICompatibleTransport(srv.Client(), 0)
	route := ResolvedRoute{Provider: "p", AccountID: "a", ModelID: "m", BaseURL: srv.URL, Credential: StoredCredentials{Value: "k"}}
	req := NormalizedRequest{Operation: OperationChat, Messages: []Message{{Role: "user", Content: "hi"}}}
	if _, err := tr.Execute(context.Background(), route, req); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if bytes.Contains(gotBody, []byte("response_format")) {
		t.Fatalf("sent body = %s, want no response_format key when unset", gotBody)
	}
}
```

- [ ] **Step 2: Run the tests and confirm they fail**

Run: `go test ./internal/execution/ -run TestOpenAICompatible_.*ResponseFormat -v`
Expected: FAIL — `ResponseFormat` is not a field of `NormalizedRequest` (compile error).

- [ ] **Step 3: Add the field and serialize it**

In `internal/execution/types.go`, inside `NormalizedRequest` after `ToolChoice`:

```go
	// ResponseFormat asks the provider to constrain the reply's shape
	// (OpenAI `response_format`). Empty = unset, so the serialized body is
	// byte-identical to a request that never set it. The only value this
	// seam recognizes is "json_object"; a transport that cannot express it
	// returns ErrRequestFeatureUnsupported rather than dropping it, because
	// silently ignoring it would return prose to a caller that requires JSON.
	ResponseFormat string
```

In `internal/execution/openaicompat.go`, add to the `chatRequest` struct:

```go
	ResponseFormat *chatResponseFormat `json:"response_format,omitempty"`
```

and next to it:

```go
// chatResponseFormat is OpenAI's response_format object. Only the type
// discriminator is expressed; a JSON Schema variant is not part of this seam.
type chatResponseFormat struct {
	Type string `json:"type"`
}

// buildChatResponseFormat maps the normalized value onto the wire object.
// Empty yields nil so the key is omitted entirely.
func buildChatResponseFormat(v string) (*chatResponseFormat, error) {
	if v == "" {
		return nil, nil
	}
	if v != "json_object" {
		return nil, fmt.Errorf("%w: response_format %q", ErrRequestFeatureUnsupported, v)
	}
	return &chatResponseFormat{Type: v}, nil
}
```

At both `chatRequest` construction sites (`openaicompat.go:250-260` and `:328-338`), add the field. Each construction site is preceded by request-building code that can already return an error, so resolve the value first:

```go
	responseFormat, err := buildChatResponseFormat(req.ResponseFormat)
	if err != nil {
		return nil, err
	}
```

then add `ResponseFormat: responseFormat,` to the literal. In the streaming construction site, match that function's existing error-return shape.

Apply the same three edits to `openAIChatCodec` in `internal/execution/nativeoauth.go:345-356`.

- [ ] **Step 4: Run the tests and confirm they pass**

Run: `go test ./internal/execution/ -run TestOpenAICompatible_.*ResponseFormat -v`
Expected: PASS

- [ ] **Step 5: Write the fail-closed tests for the two codecs that cannot express it**

In `internal/execution/anthropicwire_test.go`:

```go
func TestAnthropicWire_ResponseFormatFailsClosed(t *testing.T) {
	_, err := buildAnthropicRequest(ResolvedRoute{ModelID: "m"}, NormalizedRequest{
		Messages:       []Message{{Role: "user", Content: "hi"}},
		ResponseFormat: "json_object",
	})
	if !errors.Is(err, ErrRequestFeatureUnsupported) {
		t.Fatalf("err = %v, want ErrRequestFeatureUnsupported — silently dropping response_format would return prose to a caller that requires JSON", err)
	}
}
```

In `internal/execution/geminiwire_test.go`, the same test against `buildGeminiRequest`.

Use the exact builder function names and signatures already in those files (`anthropicwire.go:~80`, `geminiwire.go:~105`); adjust the call to match.

- [ ] **Step 6: Run them and confirm they fail**

Run: `go test ./internal/execution/ -run 'ResponseFormatFailsClosed' -v`
Expected: FAIL — no error returned.

- [ ] **Step 7: Make them fail closed**

In `internal/execution/anthropicwire.go`, directly after the existing `ToolChoice` guard at `:85-88`:

```go
	if req.ResponseFormat != "" {
		return anthropicMessagesRequest{}, fmt.Errorf("%w: response_format", ErrRequestFeatureUnsupported)
	}
```

In `internal/execution/geminiwire.go`, directly after the `ToolChoice` guard at `:117-120`:

```go
	if req.ResponseFormat != "" {
		return geminiGenerateReq{}, fmt.Errorf("%w: response_format", ErrRequestFeatureUnsupported)
	}
```

- [ ] **Step 8: Run the full execution package**

Run: `go test ./internal/execution/...`
Expected: PASS, with no pre-existing test broken. Any test asserting an exact request body must still pass — that is the byte-identical guarantee.

- [ ] **Step 9: Mutation-proof**

Delete the `ResponseFormat: responseFormat,` line from the non-streaming `chatRequest` literal and re-run `TestOpenAICompatible_ResponseFormatIsSerialized`. It MUST fail. Restore it.

- [ ] **Step 10: Commit**

```bash
git add internal/execution/
git commit -m "feat(execution): carry response_format on the transport seam

Structured output is the one capability models.dev declares that no
transport could express. The two OpenAI-shaped codecs now serialize it;
the anthropic and gemini codecs fail closed rather than dropping it,
because silently ignoring it returns prose to a caller requiring JSON.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## Task 2: Stop the transports under-declaring themselves

Every transport declares `[chat, streaming]`. All of them also serialize `Tools` (`openaicompat.go:257`, `nativeoauth.go:353`, `anthropicwire.go:108`, `geminiwire.go:137`) and image `Parts` (`openaicompat.go:189`, `anthropicwire.go:150`, `geminiwire.go:157`). The declarations predate P5-EXEC-004 and were never updated. Because `effective` is an intersection, feeding these stale declarations into the projection would cap every offering at chat and streaming.

**Files:**
- Modify: `internal/execution/types.go:48-59` (`Operation` constants)
- Modify: `internal/execution/openaicompat.go:529-532`
- Modify: `internal/execution/nativeoauth.go:277-279`, `:319-321`, `:437-439`
- Modify: `internal/execution/nativeapi.go:223-232`
- Test: `internal/execution/capabilities_declaration_test.go` (**new file**)

**Interfaces:**
- Consumes: `NormalizedRequest.ResponseFormat` from Task 1.
- Produces: `execution.OperationStructuredOutput Operation = "structured_output"`. Every `SupportedCapabilities` now returns the operations its codec genuinely serializes.

- [ ] **Step 1: Write the falsifiable declaration test**

A declaration that cannot be falsified by deleting the behaviour it describes is worthless. This test drives a real request through each codec for each declared operation and asserts the codec did not fail closed.

Create `internal/execution/capabilities_declaration_test.go`:

```go
package execution

import (
	"errors"
	"testing"
)

// oneByOnePNG is a 1x1 PNG, base64, used only to exercise an image part.
const oneByOnePNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="

// requestFor builds the minimal request that exercises one operation.
func requestFor(op Operation) NormalizedRequest {
	req := NormalizedRequest{Operation: OperationChat, Messages: []Message{{Role: "user", Content: "hi"}}}
	switch op {
	case OperationStreaming:
		req.Stream = true
	case OperationTools:
		req.Tools = []ToolDefinition{{Name: "add", Description: "adds", ParametersJSON: `{"type":"object"}`}}
	case OperationVision:
		req.Messages = []Message{{Role: "user", Parts: []ContentPart{
			{Kind: ContentPartText, Text: "what colour"},
			{Kind: ContentPartImage, ImageBase64: oneByOnePNG, MediaType: "image/png"},
		}}}
	case OperationStructuredOutput:
		req.ResponseFormat = "json_object"
	}
	return req
}

// TestDeclaredCapabilitiesAreExpressible proves each transport declares only
// operations its own request builder can actually serialize. Deleting a
// serialization branch must break this test — that is the point.
func TestDeclaredCapabilitiesAreExpressible(t *testing.T) {
	cases := []struct {
		name  string
		route ResolvedRoute
		build func(ResolvedRoute, NormalizedRequest) error
		decl  []Operation
	}{
		{
			name:  "openai_compatible",
			route: ResolvedRoute{ModelID: "m", BaseURL: "https://example.invalid"},
			build: func(r ResolvedRoute, q NormalizedRequest) error {
				_, err := buildOpenAICompatChatRequest(r, q)
				return err
			},
			decl: (&OpenAICompatibleTransport{}).SupportedCapabilities(ResolvedRoute{}),
		},
		{
			name:  "anthropic_messages",
			route: ResolvedRoute{ModelID: "m"},
			build: func(r ResolvedRoute, q NormalizedRequest) error {
				_, err := buildAnthropicRequest(r, q)
				return err
			},
			decl: anthropicMessagesCodec{}.capabilities(),
		},
		{
			name:  "google_generate_content",
			route: ResolvedRoute{ModelID: "m"},
			build: func(r ResolvedRoute, q NormalizedRequest) error {
				_, err := buildGeminiRequest(r, q)
				return err
			},
			decl: googleGenerateContentCodec{}.capabilities(),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if len(tc.decl) == 0 {
				t.Fatalf("%s declares no capabilities", tc.name)
			}
			for _, op := range tc.decl {
				if op == OperationChat || op == OperationStreaming {
					continue // shape-independent; covered by the transport's own tests
				}
				if err := tc.build(tc.route, requestFor(op)); errors.Is(err, ErrRequestFeatureUnsupported) {
					t.Fatalf("%s declares %q but its request builder fails closed on it", tc.name, op)
				}
			}
		})
	}
}
```

If the concrete builder names differ from `buildOpenAICompatChatRequest` / `buildAnthropicRequest` / `buildGeminiRequest`, use the real ones — read the files first and adjust only the call, never the assertion.

- [ ] **Step 2: Run it against the current declarations**

Run: `go test ./internal/execution/ -run TestDeclaredCapabilitiesAreExpressible -v`
Expected: PASS trivially — nothing but chat/streaming is declared, so the loop body never runs. This is the "useless canary" state; Step 3 gives it teeth.

- [ ] **Step 3: Add the constant and widen the declarations**

In `internal/execution/types.go`, add to the `Operation` block:

```go
	OperationStructuredOutput Operation = "structured_output"
```

`openaicompat.go:529-532`:

```go
// SupportedCapabilities reports every operation this transport's request
// builder genuinely serializes: chat and streaming, plus tools
// (buildChatTools), vision (image content parts) and structured output
// (response_format). It must never claim an operation the builder answers
// with ErrRequestFeatureUnsupported — TestDeclaredCapabilitiesAreExpressible
// pins that.
func (t *OpenAICompatibleTransport) SupportedCapabilities(_ ResolvedRoute) []Operation {
	return []Operation{OperationChat, OperationStreaming, OperationTools, OperationVision, OperationStructuredOutput}
}
```

`nativeoauth.go:437-439` (`openAIChatCodec`) — identical list, since it shares `buildChatTools` and serializes `response_format` after Task 1:

```go
func (openAIChatCodec) capabilities() []Operation {
	return []Operation{OperationChat, OperationStreaming, OperationTools, OperationVision, OperationStructuredOutput}
}
```

`nativeoauth.go:319-321` (`anthropicMessagesCodec`) — tools and inline-base64 images yes, `response_format` fails closed:

```go
func (anthropicMessagesCodec) capabilities() []Operation {
	return []Operation{OperationChat, OperationStreaming, OperationTools, OperationVision}
}
```

`nativeoauth.go:277-279` (`googleGenerateContentCodec`) — same reasoning:

```go
func (googleGenerateContentCodec) capabilities() []Operation {
	return []Operation{OperationChat, OperationStreaming, OperationTools, OperationVision}
}
```

`nativeapi.go:223-232` — the Gemini transport builds through `buildGeminiRequest`:

```go
func (t *NativeAPITransport) SupportedCapabilities(_ ResolvedRoute) []Operation {
	return []Operation{OperationChat, OperationStreaming, OperationTools, OperationVision}
}
```

Leave `bifrost.go:252` at `[]Operation{OperationChat}` — that shim fails closed on every rich feature via `requestCarriesRichFeatures`, so `[chat]` is already honest.

- [ ] **Step 4: Run and confirm the test now has teeth and passes**

Run: `go test ./internal/execution/ -run TestDeclaredCapabilitiesAreExpressible -v`
Expected: PASS, and the subtests now actually exercise tools/vision/structured-output branches.

- [ ] **Step 5: Mutation-proof**

Temporarily change `buildChatTools(req.Tools)` to `nil` in `openaicompat.go`'s non-streaming construction — the test should still pass (tools are omitted, not rejected), which shows this test alone is insufficient for tools. Then instead add an early `return nil, fmt.Errorf("%w: tools", ErrRequestFeatureUnsupported)` when `len(req.Tools) > 0`; the test MUST now fail. Restore.

Record both observations in the commit message: the guard catches fail-closed drift, not silent omission. Silent omission is covered by the transports' existing body-assertion tests.

- [ ] **Step 6: Run the whole package and commit**

Run: `go test ./internal/execution/...`

```bash
git add internal/execution/
git commit -m "fix(execution): declare the capabilities the transports actually serialize

Every transport declared [chat, streaming] while its request builder had
serialized Tools and image Parts since P5-EXEC-004. Because the read
model intersects transport support with the rest, those stale
declarations would have capped every offering at chat and streaming.

Adds a falsifiable guard: each declared operation is driven through the
codec's own request builder and must not come back
ErrRequestFeatureUnsupported.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## Task 3: Read the models.dev fields we currently discard

`ModelsDevFacts` (`modelsdev.go:33-42`) never parses `reasoning`, never looks at `modalities.output` for image output, and never reads `limit.input` — all three are present on the dataset for every entry.

**Files:**
- Modify: `internal/providers/modelsdev.go:33-60`, `:131-156`
- Create: `internal/providers/testdata/modelsdev-fixture.json`
- Test: `internal/providers/modelsdev_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `ModelsDevFacts` gains `Reasoning bool`, `ImageOutput bool`, `MaxInput *int`.

- [ ] **Step 1: Create the fixture**

Create `internal/providers/testdata/modelsdev-fixture.json`. These are real entries copied verbatim from `https://models.dev/api.json` on 2026-08-05, trimmed to the providers and fields this repo needs:

```json
{
  "cline-pass": {
    "id": "cline-pass",
    "name": "Cline Pass",
    "api": "https://api.cline.bot/api/v1",
    "models": {
      "cline-pass/kimi-k3": {
        "id": "cline-pass/kimi-k3",
        "name": "Kimi K3",
        "family": "kimi-k3",
        "attachment": true,
        "reasoning": true,
        "tool_call": true,
        "structured_output": true,
        "modalities": { "input": ["text", "image", "video"], "output": ["text"] },
        "limit": { "context": 1048576, "output": 131072 }
      },
      "cline-pass/kimi-k2.7-code": {
        "id": "cline-pass/kimi-k2.7-code",
        "name": "Kimi K2.7 Code",
        "family": "kimi-k2",
        "attachment": true,
        "reasoning": true,
        "tool_call": true,
        "structured_output": true,
        "modalities": { "input": ["text", "image", "video"], "output": ["text"] },
        "limit": { "context": 262144, "output": 262144 }
      },
      "cline-pass/mimo-v2.5": {
        "id": "cline-pass/mimo-v2.5",
        "name": "MiMo-V2.5",
        "family": "mimo",
        "attachment": true,
        "reasoning": true,
        "tool_call": true,
        "modalities": { "input": ["text", "image", "audio", "video"], "output": ["text"] },
        "limit": { "context": 1048576, "output": 131072 }
      },
      "cline-pass/deprecated-example": {
        "id": "cline-pass/deprecated-example",
        "name": "Deprecated Example",
        "reasoning": false,
        "tool_call": false,
        "status": "deprecated",
        "modalities": { "input": ["text"], "output": ["text"] },
        "limit": { "context": 8192, "output": 4096 }
      },
      "cline-pass/image-out-example": {
        "id": "cline-pass/image-out-example",
        "name": "Image Out Example",
        "reasoning": false,
        "tool_call": false,
        "modalities": { "input": ["text"], "output": ["image"] },
        "limit": { "context": 4096 }
      }
    }
  },
  "ollama-cloud": {
    "id": "ollama-cloud",
    "name": "Ollama Cloud",
    "api": "https://ollama.com/v1",
    "models": {
      "glm-5.1": {
        "id": "glm-5.1",
        "name": "GLM 5.1",
        "reasoning": true,
        "tool_call": true,
        "structured_output": true,
        "modalities": { "input": ["text"], "output": ["text"] },
        "limit": { "context": 200000, "input": 190000, "output": 32000 }
      }
    }
  },
  "anthropic": {
    "id": "anthropic",
    "name": "Anthropic",
    "models": {
      "claude-sonnet-4-6": {
        "id": "claude-sonnet-4-6",
        "name": "Claude Sonnet 4.6",
        "reasoning": true,
        "tool_call": true,
        "structured_output": true,
        "modalities": { "input": ["text", "image", "pdf"], "output": ["text"] },
        "limit": { "context": 1000000, "output": 64000 }
      }
    }
  }
}
```

The last two `cline-pass` entries are synthetic edge cases and are named so; every other entry is real. Note `ollama-cloud/glm-5.1` carries `limit.input`, which no real fixture entry needed before, and `anthropic` deliberately has **no** `api` key — that is the real dataset's shape and Task 4 depends on it.

- [ ] **Step 2: Write the failing test**

In `internal/providers/modelsdev_test.go`:

```go
//go:embed testdata/modelsdev-fixture.json
var modelsDevFixture []byte

func TestParseModelsDevFacts_ReadsReasoningImageOutputAndMaxInput(t *testing.T) {
	facts, err := parseModelsDevFacts(modelsDevFixture, "cline-pass")
	if err != nil {
		t.Fatalf("parseModelsDevFacts: %v", err)
	}

	k3, ok := facts["cline-pass/kimi-k3"]
	if !ok {
		t.Fatalf("facts = %v, want an entry for cline-pass/kimi-k3", facts)
	}
	if !k3.Reasoning {
		t.Fatal("kimi-k3 Reasoning = false, want true (the dataset declares reasoning: true)")
	}
	if k3.ImageOutput {
		t.Fatal("kimi-k3 ImageOutput = true, want false (its output modality is text only)")
	}
	if k3.Context == nil || *k3.Context != 1048576 {
		t.Fatalf("kimi-k3 Context = %v, want 1048576", k3.Context)
	}
	if k3.MaxInput != nil {
		t.Fatalf("kimi-k3 MaxInput = %v, want nil (the entry declares no limit.input)", *k3.MaxInput)
	}

	img := facts["cline-pass/image-out-example"]
	if !img.ImageOutput {
		t.Fatal("image-out-example ImageOutput = false, want true")
	}

	ollama, err := parseModelsDevFacts(modelsDevFixture, "ollama-cloud")
	if err != nil {
		t.Fatalf("parseModelsDevFacts(ollama-cloud): %v", err)
	}
	glm := ollama["glm-5.1"]
	if glm.MaxInput == nil || *glm.MaxInput != 190000 {
		t.Fatalf("glm-5.1 MaxInput = %v, want 190000", glm.MaxInput)
	}
}
```

Add `"embed"` to the file's imports (and a blank line before it if `goimports` requires).

- [ ] **Step 3: Run and confirm it fails**

Run: `go test ./internal/providers/ -run TestParseModelsDevFacts_ReadsReasoning -v`
Expected: FAIL — `k3.Reasoning` undefined (compile error).

- [ ] **Step 4: Add the fields**

In `internal/providers/modelsdev.go`, add to `ModelsDevFacts`:

```go
	Reasoning        bool   // explicit `reasoning`
	ImageOutput      bool   // `modalities.output` explicitly contains "image"
	MaxInput         *int   // `limit.input`; nil when absent
```

Add to `modelsDevRawEntry`:

```go
	Reasoning bool `json:"reasoning"`
```

and inside its `Limit` struct:

```go
		Input *int `json:"input"`
```

In `parseModelsDevFacts`, add to the constructed `ModelsDevFacts`:

```go
			Reasoning:   e.Reasoning,
			ImageOutput: containsImageModality(e.Modalities.Output),
			MaxInput:    e.Limit.Input,
```

`containsImageModality` already exists and is reused verbatim — the output list is checked with the same predicate as the input list.

- [ ] **Step 5: Run and confirm it passes**

Run: `go test ./internal/providers/ -run TestParseModelsDevFacts_ReadsReasoning -v`
Expected: PASS

- [ ] **Step 6: Mutation-proof**

Change `Reasoning: e.Reasoning` to `Reasoning: false`. The test MUST fail. Restore.

- [ ] **Step 7: Commit**

```bash
git add internal/providers/modelsdev.go internal/providers/modelsdev_test.go internal/providers/testdata/modelsdev-fixture.json
git commit -m "feat(providers): read reasoning, image output and limit.input from models.dev

These three are present on every dataset entry and were parsed by
nobody, which is why no model in the fleet has ever reported reasoning.

Adds a vendored fixture of real dataset entries so the parse is testable
without live network.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## Task 4: Map our provider slugs to dataset keys, and verify the mapping

Our slugs and models.dev's keys differ (`clinepass` vs `cline-pass`, `nvidia-nim` vs `nvidia`, `opencode-zen` vs `opencode`). A wrong key silently produces no facts, or worse, the wrong ones. The dataset's own `api` field is the check — but it cannot be the *lookup*, because `opencode.ai` is the host of two distinct dataset providers (`opencode` at `/zen/v1` and `opencode-go` at `/zen/go/v1`).

**Files:**
- Create: `internal/providers/modelsdevkeys.go`
- Test: `internal/providers/modelsdevkeys_test.go` (**new file**)

**Interfaces:**
- Consumes: the fixture from Task 3.
- Produces:
  - `func ModelsDevKeyFor(providerID ProviderID) (string, bool)` — the explicit map.
  - `func (s *ModelsDevSource) FactsForProvider(ctx context.Context, providerID ProviderID, baseURL string) (map[string]ModelsDevFacts, error)` — resolves the key, verifies it against `baseURL`, and returns facts. A verification failure returns an empty map and a nil error (no enrichment is not a failure), never wrong facts.

- [ ] **Step 1: Write the failing test**

Create `internal/providers/modelsdevkeys_test.go`:

```go
package providers

import (
	"context"
	"testing"
)

func fixtureSource(t *testing.T) *ModelsDevSource {
	t.Helper()
	return NewModelsDevSource(func(context.Context) ([]byte, error) {
		return modelsDevFixture, nil
	}, nil)
}

func TestModelsDevKeyFor_MapsOurSlugs(t *testing.T) {
	for _, tc := range []struct {
		id   ProviderID
		want string
	}{
		{ClinePassID, "cline-pass"},
		{NvidiaNIMID, "nvidia"},
		{OpenCodeZenID, "opencode"},
		{OllamaCloudID, "ollama-cloud"},
		{ClaudeCodeID, "anthropic"},
	} {
		got, ok := ModelsDevKeyFor(tc.id)
		if !ok || got != tc.want {
			t.Fatalf("ModelsDevKeyFor(%q) = (%q, %v), want (%q, true)", tc.id, got, ok, tc.want)
		}
	}
	if _, ok := ModelsDevKeyFor(AgnesAIID); ok {
		t.Fatal("ModelsDevKeyFor(agnes-ai) reported a key; agnes-ai has no models.dev entry and must report none")
	}
}

func TestFactsForProvider_ReturnsFactsWhenTheBaseURLMatches(t *testing.T) {
	facts, err := fixtureSource(t).FactsForProvider(context.Background(), ClinePassID, ClinePassBaseURL)
	if err != nil {
		t.Fatalf("FactsForProvider: %v", err)
	}
	if _, ok := facts["cline-pass/kimi-k3"]; !ok {
		t.Fatalf("facts = %v, want the cline-pass entries", facts)
	}
}

func TestFactsForProvider_RefusesOnBaseURLMismatch(t *testing.T) {
	facts, err := fixtureSource(t).FactsForProvider(context.Background(), ClinePassID, "https://impostor.example.com")
	if err != nil {
		t.Fatalf("FactsForProvider returned an error; a refusal is empty facts, not a failure: %v", err)
	}
	if len(facts) != 0 {
		t.Fatalf("facts = %v, want empty — the dataset api host did not match our base URL, so the key must be refused", facts)
	}
}

func TestFactsForProvider_SkipsTheCheckWhenTheDatasetDeclaresNoAPI(t *testing.T) {
	// anthropic carries no `api` key in the real dataset. The explicit map is
	// then the only evidence and the check cannot run — it must not refuse.
	facts, err := fixtureSource(t).FactsForProvider(context.Background(), ClaudeCodeID, ClaudeCodeAPIBase)
	if err != nil {
		t.Fatalf("FactsForProvider: %v", err)
	}
	if _, ok := facts["claude-sonnet-4-6"]; !ok {
		t.Fatalf("facts = %v, want the anthropic entries", facts)
	}
}

func TestFactsForProvider_UnmappedProviderYieldsNoFacts(t *testing.T) {
	facts, err := fixtureSource(t).FactsForProvider(context.Background(), AgnesAIID, AgnesAIBaseURL)
	if err != nil {
		t.Fatalf("FactsForProvider: %v", err)
	}
	if len(facts) != 0 {
		t.Fatalf("facts = %v, want empty for an unmapped provider", facts)
	}
}
```

- [ ] **Step 2: Run and confirm it fails**

Run: `go test ./internal/providers/ -run 'TestModelsDevKeyFor|TestFactsForProvider' -v`
Expected: FAIL — `ModelsDevKeyFor` and `FactsForProvider` undefined.

- [ ] **Step 3: Implement**

Create `internal/providers/modelsdevkeys.go`:

```go
package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// modelsDevKeys maps our provider slug onto the models.dev dataset key.
//
// The map is AUTHORITATIVE and the dataset's `api` field is only a CHECK.
// Resolution by URL is impossible in general: opencode.ai is the host of two
// dataset providers — "opencode" (/zen/v1) and "opencode-go" (/zen/go/v1) —
// so a host match cannot select between them. Stating the intent here and
// verifying it below is both explicit and self-checking.
//
// Every entry was verified against the live dataset on 2026-08-05. Providers
// absent from this map simply get no catalog enrichment; agnes-ai has no
// models.dev entry under any key, which is an upstream gap, not a bug here.
var modelsDevKeys = map[ProviderID]string{
	ClinePassID:     "cline-pass",
	NvidiaNIMID:     "nvidia",
	OpenCodeZenID:   "opencode",
	OllamaCloudID:   "ollama-cloud",
	GitHubCopilotID: "github-copilot",
	ClaudeCodeID:    "anthropic",
	GeminiCLIID:     "google",
	XAIID:           "xai",
}

// ModelsDevKeyFor reports the dataset key for providerID, and false when the
// provider has no mapped entry.
func ModelsDevKeyFor(providerID ProviderID) (string, bool) {
	key, ok := modelsDevKeys[providerID]
	return key, ok
}

// modelsDevProviderAPI extracts one provider entry's `api` field. An absent
// or empty value yields "" — several real entries (anthropic, openai, google,
// xai) carry no api at all.
func modelsDevProviderAPI(body []byte, key string) string {
	var dataset map[string]struct {
		API string `json:"api"`
	}
	if err := json.Unmarshal(body, &dataset); err != nil {
		return ""
	}
	return dataset[key].API
}

// sameHost reports whether two URLs share a host. Both must parse and both
// must carry a host; anything else is not a match.
func sameHost(a, b string) bool {
	ua, err := url.Parse(a)
	if err != nil || ua.Host == "" {
		return false
	}
	ub, err := url.Parse(b)
	if err != nil || ub.Host == "" {
		return false
	}
	return ua.Host == ub.Host
}

// FactsForProvider returns the models.dev facts for providerID, keyed by the
// provider's own model id.
//
// It fails CLOSED in both directions that matter: an unmapped provider and a
// provider whose dataset entry declares an `api` on a different host both
// yield an EMPTY map. Neither is an error — "no catalog facts" is a fact, and
// discovery must still list the provider's live models. Only a fetch or
// top-level parse failure returns a non-nil error.
//
// When the dataset entry declares no `api` (anthropic, google, openai, xai),
// the check cannot run and the map entry stands as the only evidence. That is
// recorded here rather than silently assumed.
func (s *ModelsDevSource) FactsForProvider(ctx context.Context, providerID ProviderID, baseURL string) (map[string]ModelsDevFacts, error) {
	key, mapped := ModelsDevKeyFor(providerID)
	if !mapped {
		return map[string]ModelsDevFacts{}, nil
	}

	facts, err := s.Facts(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("providers: models.dev facts for %q: %w", providerID, err)
	}

	s.mu.Lock()
	raw := s.raw
	s.mu.Unlock()
	if api := modelsDevProviderAPI(raw, key); api != "" && !sameHost(api, baseURL) {
		// The dataset moved under us. Refuse the key rather than join the
		// wrong provider's facts onto our models.
		return map[string]ModelsDevFacts{}, nil
	}

	return facts, nil
}
```

If any of `GitHubCopilotID`, `XAIID`, `GeminiCLIID`, `AgnesAIID`, `ClaudeCodeAPIBase`, `AgnesAIBaseURL` is spelled differently in `internal/providers/`, use the real identifier — read `catalog.go` and the adapter files rather than guessing.

- [ ] **Step 4: Run and confirm it passes**

Run: `go test ./internal/providers/ -run 'TestModelsDevKeyFor|TestFactsForProvider' -v`
Expected: PASS

- [ ] **Step 5: Mutation-proof**

Change the `ClinePassID` entry to `"opencode"`. `TestFactsForProvider_RefusesOnBaseURLMismatch` should still pass, but `TestFactsForProvider_ReturnsFactsWhenTheBaseURLMatches` MUST fail — the api host check catches the wrong key. Restore.

- [ ] **Step 6: Commit**

```bash
git add internal/providers/modelsdevkeys.go internal/providers/modelsdevkeys_test.go
git commit -m "feat(providers): explicit models.dev key map with base-URL verification

Our slugs and the dataset keys differ (clinepass/cline-pass,
nvidia-nim/nvidia, opencode-zen/opencode). The map states the intent and
the dataset's own api field verifies it; a host mismatch refuses the key
rather than joining another provider's facts.

Resolution BY url is deliberately not used: opencode.ai hosts two
dataset providers, so a host match cannot select between them.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## Task 5: One shared derivation from facts to operations

Three adapters each hand-roll the facts-to-capabilities mapping and they disagree — `modelsFromLiveIDs` emits tools/structured_output/vision, `opencode_zen.go` never reads `structured_output`. One function, one set of rules, one place to test.

**Files:**
- Modify: `internal/providers/modelsdev.go:191-244` (`modelsFromLiveIDs`)
- Test: `internal/providers/modelsdev_test.go`

**Interfaces:**
- Consumes: `ModelsDevFacts` from Task 3.
- Produces: `func OperationsFromFacts(f ModelsDevFacts) []string` — the declared operation strings a dataset entry supports, always beginning with `"chat"`, in `models.Operations()` order.

- [ ] **Step 1: Write the failing test**

```go
func TestOperationsFromFacts_DerivesEveryCatalogBackedOperation(t *testing.T) {
	facts, err := parseModelsDevFacts(modelsDevFixture, "cline-pass")
	if err != nil {
		t.Fatalf("parseModelsDevFacts: %v", err)
	}

	got := OperationsFromFacts(facts["cline-pass/kimi-k3"])
	want := []string{"chat", "tools", "structured_output", "vision", "context_window", "reasoning"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("OperationsFromFacts(kimi-k3) = %v, want %v", got, want)
	}

	img := OperationsFromFacts(facts["cline-pass/image-out-example"])
	if !slices.Contains(img, "image_generation") {
		t.Fatalf("OperationsFromFacts(image-out-example) = %v, want image_generation present", img)
	}

	// An entry with no facts at all still supports chat: the endpoint is a
	// chat-completions gateway. It must claim nothing else.
	bare := OperationsFromFacts(ModelsDevFacts{})
	if !reflect.DeepEqual(bare, []string{"chat"}) {
		t.Fatalf("OperationsFromFacts(zero) = %v, want [chat] only", bare)
	}
}
```

`want` is written in `models.Operations()` order: chat, streaming, tools, structured_output, vision, context_window, image_generation, reasoning. `streaming` is absent because it is a transport fact, not a catalog fact.

- [ ] **Step 2: Run and confirm it fails**

Run: `go test ./internal/providers/ -run TestOperationsFromFacts -v`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement, and reuse it**

Add to `internal/providers/modelsdev.go`:

```go
// OperationsFromFacts derives the operation strings a models.dev entry
// DECLARES, in models.Operations() order. Every value is grounded in an
// explicit dataset field; nothing is inferred from the model id.
//
// "chat" is unconditional: these are chat-completions catalogs, and an entry
// existing in one is the declaration. "streaming" is deliberately ABSENT —
// models.dev carries no streaming field, and streaming is a property of the
// transport we send with, not of the model (see the transport's
// SupportedCapabilities). "context_window" is emitted when the entry declares
// a context limit, which is what makes the number itself a catalog-backed fact.
func OperationsFromFacts(f ModelsDevFacts) []string {
	ops := []string{"chat"}
	if f.ToolCall {
		ops = append(ops, "tools")
	}
	if f.StructuredOutput {
		ops = append(ops, "structured_output")
	}
	if f.ImageInput {
		ops = append(ops, "vision")
	}
	if f.Context != nil {
		ops = append(ops, "context_window")
	}
	if f.ImageOutput {
		ops = append(ops, "image_generation")
	}
	if f.Reasoning {
		ops = append(ops, "reasoning")
	}
	return ops
}
```

Then replace the hand-rolled block inside `modelsFromLiveIDs` (`modelsdev.go:216-227`) with:

```go
		caps := []string{"chat"}
		if known {
			caps = OperationsFromFacts(f)
		}
```

- [ ] **Step 4: Stop dropping image-output models**

`modelsdev.go:213-215` currently drops any entry whose output modalities are not all text. With `image_generation` in scope that hides the model instead of classifying it. Narrow the drop to deprecation only:

```go
		if known && f.Deprecated {
			continue
		}
```

Add the test:

```go
func TestModelsFromLiveIDs_KeepsImageOutputModelsAndDropsDeprecated(t *testing.T) {
	facts, err := parseModelsDevFacts(modelsDevFixture, "cline-pass")
	if err != nil {
		t.Fatalf("parseModelsDevFacts: %v", err)
	}
	out := modelsFromLiveIDs([]string{"cline-pass/image-out-example", "cline-pass/deprecated-example"}, facts)

	if len(out) != 1 {
		t.Fatalf("got %d models, want 1 — the deprecated entry is dropped and the image-output entry is kept", len(out))
	}
	if out[0].ProviderModelID != "cline-pass/image-out-example" {
		t.Fatalf("kept %q, want the image-output model", out[0].ProviderModelID)
	}
	if !slices.Contains(out[0].Capabilities, "image_generation") {
		t.Fatalf("capabilities = %v, want image_generation", out[0].Capabilities)
	}
}
```

`OutputAllText` remains on the struct and is still parsed.

> **Correction applied during implementation (fix round 1, commit `d25cba1`).**
> This step originally justified keeping image-output models by claiming
> `internal/intelligence/classification.go` consumes a media-only signal to keep
> them out of chat routing. **That claim was false** — `OutputAllText` has no
> consumer outside `internal/providers`, `Classify` short-circuits on the first
> `chat` operation, and `classification_test.go:38-46` pins that a chat-exposing
> offering is never `catalog_only`. Keeping these models while `OperationsFromFacts`
> still emitted `chat` unconditionally would have made image-generation models
> routable for chat.
>
> The delivered fix grounds `chat` in declared text output: output absent or
> empty → emit `chat` (unknown must not drop a chat model); output contains
> `"text"` → emit `chat`; output explicitly non-empty without `"text"` → do not
> emit `chat`. A new field `OutputDeclaresNonTextOnly` carries that third case,
> distinct from `OutputAllText` (which is true for both "absent" and "all text").

- [ ] **Step 5: Run the package**

Run: `go test ./internal/providers/...`
Expected: PASS. If an existing ollama-cloud or nvidia-nim test asserted the old narrower capability list, update the EXPECTATION only after confirming the new list is grounded in the fixture's explicit fields — never loosen an assertion to make it pass.

- [ ] **Step 6: Mutation-proof**

Remove the `if f.Reasoning` branch. `TestOperationsFromFacts_DerivesEveryCatalogBackedOperation` MUST fail. Restore.

- [ ] **Step 7: Commit**

```bash
git add internal/providers/
git commit -m "refactor(providers): one derivation from models.dev facts to operations

Three adapters hand-rolled this mapping and disagreed: modelsFromLiveIDs
emitted tools/structured_output/vision while opencode-zen never read
structured_output. One function now owns the rules.

Also stops dropping models whose output modality is an image. With
image_generation in the operation vocabulary, hiding them is wrong;
classification already keeps media-only offerings out of chat routing.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## Task 6: Give ClinePass its catalog

ClinePass declares `Capabilities: []string{"chat"}` and three candidate operations, on the stated ground that "it has no models.dev entry" (`clinepass.go:527-535`). That premise is false: the dataset has `cline-pass` with 11 models, and 11 of the 12 live ids join exactly.

**Files:**
- Modify: `internal/providers/clinepass.go:124-132` (struct + constructor), `:491-544` (`DiscoverModels`), `:813-826` (`RegisterClinePass`)
- Modify: `internal/httpapi/clinepass_seams.go:100-102`
- Test: `internal/providers/clinepass_test.go`

**Interfaces:**
- Consumes: `FactsForProvider` (Task 4), `OperationsFromFacts` (Task 5).
- Produces: `NewClinePassAdapter(postProbe ClinePassPostProbe, getProbe ClinePassGetProbe, modelsDevProbe ModelsDevProbe, now func() time.Time) *ClinePassAdapter` and `RegisterClinePass(reg *Registry, postProbe ClinePassPostProbe, getProbe ClinePassGetProbe, modelsDevProbe ModelsDevProbe, now func() time.Time) error`.

- [ ] **Step 1: Write the failing test**

In `internal/providers/clinepass_test.go`:

```go
func TestClinePass_DiscoveryJoinsTheModelsDevCatalog(t *testing.T) {
	getProbe := func(_ context.Context, url, _ string) (int, []byte, error) {
		switch {
		case strings.Contains(url, clinePassUsageLimitsPath):
			return 200, []byte(`{"success":true,"data":{"limits":[{"type":"five_hour","percentUsed":1}]}}`), nil
		case strings.Contains(url, clinePassModelsPath):
			return 200, []byte(`{"clinePass":[
				{"id":"cline-pass/kimi-k3","name":"cline-pass/kimi-k3"},
				{"id":"cline-pass/qwen3.8-max","name":"cline-pass/qwen3.8-max"}
			]}`), nil
		}
		return 404, nil, nil
	}
	adapter := NewClinePassAdapter(nil, getProbe, func(context.Context) ([]byte, error) {
		return modelsDevFixture, nil
	}, nil)

	got, err := adapter.DiscoverModels(context.Background(), StoredCredentials{
		Value: `{"access_token":"t","refresh_token":"r","expires_at":9999999999}`,
	})
	if err != nil {
		t.Fatalf("DiscoverModels: %v", err)
	}
	byID := map[string]DiscoveredModel{}
	for _, m := range got {
		byID[m.ProviderModelID] = m
	}

	k3 := byID["cline-pass/kimi-k3"]
	for _, want := range []string{"chat", "tools", "structured_output", "vision", "context_window", "reasoning"} {
		if !slices.Contains(k3.Capabilities, want) {
			t.Fatalf("kimi-k3 capabilities = %v, want %q present from the catalog", k3.Capabilities, want)
		}
	}
	if k3.ContextLength == nil || *k3.ContextLength != 1048576 {
		t.Fatalf("kimi-k3 ContextLength = %v, want 1048576 from limit.context", k3.ContextLength)
	}
	if k3.MaxOutputTokens == nil || *k3.MaxOutputTokens != 131072 {
		t.Fatalf("kimi-k3 MaxOutputTokens = %v, want 131072 from limit.output", k3.MaxOutputTokens)
	}
	if k3.DisplayName != "Kimi K3" {
		t.Fatalf("kimi-k3 DisplayName = %q, want %q from the catalog name", k3.DisplayName, "Kimi K3")
	}

	// An uncatalogued live id must still be listed, with nothing invented.
	newer, ok := byID["cline-pass/qwen3.8-max"]
	if !ok {
		t.Fatal("an uncatalogued live id was dropped; the live list is authoritative for existence")
	}
	if !reflect.DeepEqual(newer.Capabilities, []string{"chat"}) {
		t.Fatalf("uncatalogued capabilities = %v, want [chat] only — nothing may be inferred from its siblings", newer.Capabilities)
	}
	if newer.ContextLength != nil {
		t.Fatalf("uncatalogued ContextLength = %v, want nil", *newer.ContextLength)
	}
}

func TestClinePass_CatalogUnavailableLeavesLiveModelsListed(t *testing.T) {
	getProbe := func(_ context.Context, url, _ string) (int, []byte, error) {
		switch {
		case strings.Contains(url, clinePassUsageLimitsPath):
			return 200, []byte(`{"success":true,"data":{"limits":[{"type":"five_hour","percentUsed":1}]}}`), nil
		case strings.Contains(url, clinePassModelsPath):
			return 200, []byte(`{"clinePass":[{"id":"cline-pass/kimi-k3","name":"x"}]}`), nil
		}
		return 404, nil, nil
	}
	adapter := NewClinePassAdapter(nil, getProbe, func(context.Context) ([]byte, error) {
		return nil, errors.New("models.dev unreachable")
	}, nil)

	got, err := adapter.DiscoverModels(context.Background(), StoredCredentials{
		Value: `{"access_token":"t","refresh_token":"r","expires_at":9999999999}`,
	})
	if err != nil {
		t.Fatalf("DiscoverModels must succeed without the catalog; discovery is not gated on an external registry: %v", err)
	}
	if len(got) != 1 || !reflect.DeepEqual(got[0].Capabilities, []string{"chat"}) {
		t.Fatalf("got %v, want the live model listed as chat-only", got)
	}
}
```

- [ ] **Step 2: Run and confirm it fails**

Run: `go test ./internal/providers/ -run TestClinePass_ -v`
Expected: FAIL — `NewClinePassAdapter` takes two arguments.

- [ ] **Step 3: Widen the adapter**

In `internal/providers/clinepass.go`, add a field to `ClinePassAdapter`:

```go
	facts *ModelsDevSource
```

and change the constructor:

```go
// NewClinePassAdapter builds the adapter over the injected seams. modelsDevProbe
// supplies the public models.dev dataset, which is clinepass's only source of
// capability and limit facts: its own discovery wire returns {id, name} only.
func NewClinePassAdapter(postProbe ClinePassPostProbe, getProbe ClinePassGetProbe, modelsDevProbe ModelsDevProbe, now func() time.Time) *ClinePassAdapter {
	return &ClinePassAdapter{
		postProbe: postProbe,
		getProbe:  getProbe,
		facts:     NewModelsDevSource(modelsDevProbe, now),
	}
}
```

- [ ] **Step 4: Consult the catalog in `DiscoverModels`**

Replace the loop body's `DiscoveredModel` construction (`clinepass.go:527-541`) with:

```go
	// The discovery wire returns {id, name} only, so models.dev is this
	// provider's ONLY capability and limits source. Its entries are keyed
	// exactly as the live ids are (verified 2026-08-05: 11 of 12 join with no
	// normalization), and the key mapping is base-URL verified by
	// FactsForProvider. A live id with no catalog entry passes through as
	// chat-only with nil limits: the live list is authoritative for WHICH
	// models exist, and nothing is ever inferred from a sibling model's id.
	//
	// A catalog that cannot be fetched yields no facts and no error — every
	// live model is still listed. Discovery is never gated on an external
	// registry.
	catalog, factsErr := a.facts.FactsForProvider(ctx, ClinePassID, ClinePassBaseURL)
	if factsErr != nil {
		catalog = map[string]ModelsDevFacts{}
	}

	for _, m := range list.ClinePass {
		if m.ID == "" {
			continue
		}
		if _, duplicate := seen[m.ID]; duplicate {
			continue
		}
		seen[m.ID] = struct{}{}

		f, known := catalog[m.ID]
		if known && f.Deprecated {
			continue
		}

		display := m.Name
		if known && f.DisplayName != "" {
			display = f.DisplayName
		}
		if display == "" {
			display = m.ID
		}

		dm := DiscoveredModel{
			ProviderModelID: m.ID,
			DisplayName:     display,
			Capabilities:    []string{"chat"},
		}
		if known {
			dm.Capabilities = OperationsFromFacts(f)
			dm.ContextLength = f.Context
			dm.MaxInputTokens = f.MaxInput
			dm.MaxOutputTokens = f.Output
		}
		out = append(out, dm)
	}
```

`CandidateOperations` is dropped entirely: the candidate mechanism existed only because nothing could be declared. With the catalog joined, the operations are declared, and a live id with no catalog entry genuinely has no evidence for anything but chat.

- [ ] **Step 5: Widen the registration and both composition lists**

`internal/providers/clinepass.go:813`:

```go
func RegisterClinePass(reg *Registry, postProbe ClinePassPostProbe, getProbe ClinePassGetProbe, modelsDevProbe ModelsDevProbe, now func() time.Time) error {
	adapter := NewClinePassAdapter(postProbe, getProbe, modelsDevProbe, now)
```

`internal/httpapi/clinepass_seams.go:100-102`:

```go
func registerClinePass(reg *providers.Registry) error {
	return providers.RegisterClinePass(reg, clinePassPostSeam, clinePassGetSeam, openCodeZenModelsDevProbeSeam, nil)
}
```

`openCodeZenModelsDevProbeSeam` (`internal/httpapi/opencode_zen_seams.go:239`) is the existing shared fetcher for the public dataset — reuse it, do not add a second one.

Both registry lists call `registerClinePass(reg)` with no arguments, so `provider_registry.go:36` and `publicmux.go:117` need no edit.

- [ ] **Step 6: Run and confirm it passes**

Run: `go test ./internal/providers/ ./internal/httpapi/ -run 'ClinePass' -v`
Expected: PASS. Existing ClinePass tests that asserted `Capabilities: ["chat"]` and the three candidates must be UPDATED — they pinned the false premise. Update them to assert the catalog-joined result, and delete assertions that no longer describe anything.

- [ ] **Step 7: Mutation-proof at the composition root**

In `internal/httpapi/clinepass_seams.go`, replace `openCodeZenModelsDevProbeSeam` with a function returning `nil, errors.New("x")`. Run the httpapi ClinePass tests. At least one MUST fail — if none do, no test covers the real wiring and one must be added. Restore.

- [ ] **Step 8: Commit**

```bash
git add internal/providers/clinepass.go internal/providers/clinepass_test.go internal/httpapi/clinepass_seams.go
git commit -m "feat(clinepass): join the models.dev catalog for capabilities and limits

The adapter declared chat-only on the stated ground that clinepass has
no models.dev entry. Verified live on 2026-08-05: the dataset has key
cline-pass with 11 models, and 11 of the 12 ids the discovery wire
returns join exactly, with no normalization.

kimi-k3 now reports tools, structured_output, vision, reasoning and a
1,048,576-token context instead of chat and ctx unknown.

An uncatalogued live id stays listed as chat-only: the live list is
authoritative for which models exist, and nothing is inferred from a
sibling. A catalog fetch failure lists every live model unchanged.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## Task 7: Hand the projection its two missing inputs

`internal/httpapi/models.go:233,235` passes `NativeCapabilities: nil` and `TransportOperations: nil`, so `readmodel.go:190-193` computes `effective == false` for every capability of every offering, and `routable` with it. This is why the report shows `WORKING 12` beside `ENABLED 0`.

**Files:**
- Modify: `internal/httpapi/models.go:20-50` (struct), `:54-83` (builders), `:222-241` (projection input)
- Modify: `internal/httpapi/controlmux.go:286-288`
- Test: `internal/httpapi/models_test.go`

**Interfaces:**
- Consumes: corrected `SupportedCapabilities` (Task 2), catalog-filled `capabilities_json` (Task 6).
- Produces: `func (h *ModelsHandler) WithTransports(transports map[string]execution.InferenceTransport) *ModelsHandler` — copy-returning, matching `WithProbeRuns`/`WithBenchmarkRuns`.

- [ ] **Step 1: Write the failing test**

```go
func TestServeOfferings_CapabilitiesAreRoutableWhenCertifiedAndCarriable(t *testing.T) {
	db := newTestDB(t)
	seedCertifiedOffering(t, db, seedArgs{
		AccountID:    "acct-1",
		ProviderID:   "clinepass",
		ModelID:      "cline-pass/kimi-k3",
		Capabilities: []string{"chat", "tools", "vision"},
		Certified:    []string{"chat", "tools", "vision"},
		ContextTokens: 1048576,
	})

	h := NewModelsHandler(storage.NewCatalogRepo(db), nil).
		WithTransports(map[string]execution.InferenceTransport{
			"clinepass": execution.NewOpenAICompatibleTransport(http.DefaultClient, 0),
		})

	rec := httptest.NewRecorder()
	h.ServeOfferings(rec, httptest.NewRequest(http.MethodGet, "/api/control/v1/offerings?account_id=acct-1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body struct {
		Data []struct {
			Capabilities []struct {
				Operation string `json:"operation"`
				Effective bool   `json:"effective"`
				Routable  bool   `json:"routable"`
			} `json:"capabilities"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Data) != 1 {
		t.Fatalf("got %d offerings, want 1", len(body.Data))
	}

	routable := map[string]bool{}
	for _, c := range body.Data[0].Capabilities {
		routable[c.Operation] = c.Routable
	}
	for _, op := range []string{"chat", "tools", "vision"} {
		if !routable[op] {
			t.Fatalf("capability %q routable = false; it is certified+supported, declared by the offering, and the openai-compatible transport carries it", op)
		}
	}
}
```

Use the package's existing test-DB and seeding helpers. If no helper seeds a certified offering with capabilities and a context number, write one in the test file — it is fixture setup, not production code, and it must build the rows through the real `DiscoveryRepo`/`CertificationDriver` paths rather than raw INSERTs, so it cannot drift from production behaviour.

- [ ] **Step 2: Run and confirm it fails**

Run: `go test ./internal/httpapi/ -run TestServeOfferings_CapabilitiesAreRoutable -v`
Expected: FAIL — `WithTransports` undefined; and once added, `routable` is false because both inputs are still nil.

- [ ] **Step 3: Add the builder and the field**

In `internal/httpapi/models.go`, add to `ModelsHandler`:

```go
	// transports maps provider id to the transport that serves it, so the
	// projection can intersect an offering's declared capabilities with what
	// this build can actually put on the wire. It is the SAME map the probe
	// path uses, built once at the composition root. A provider absent from
	// it has no wired transport: nothing it declares is carriable, so nothing
	// is routable — fail closed, never a fabricated capability.
	transports map[string]execution.InferenceTransport
```

and the builder:

```go
// WithTransports returns a copy of h that intersects capabilities with each
// provider's transport support.
func (h *ModelsHandler) WithTransports(transports map[string]execution.InferenceTransport) *ModelsHandler {
	next := *h
	next.transports = transports
	return &next
}
```

- [ ] **Step 4: Convert and pass both inputs**

Add to `internal/httpapi/models.go`:

```go
// transportOperationsFor reports the operations this build can put on the
// wire for providerID, converted from execution's vocabulary onto the
// domain's. A provider with no wired transport reports nil, which Project
// reads as UNKNOWN and fails closed on.
//
// execution.Operation carries only the four operations a transport can
// actually express; context_window, image_generation and reasoning are
// deliberately not transport operations — a transport carries requests, not a
// context limit, and the other two have no wire expression on this seam.
func (h *ModelsHandler) transportOperationsFor(providerID string) []models.Operation {
	transport, wired := h.transports[providerID]
	if !wired {
		return nil
	}
	var out []models.Operation
	for _, op := range transport.SupportedCapabilities(execution.ResolvedRoute{Provider: execution.ProviderID(providerID)}) {
		if parsed, err := models.ParseOperation(string(op)); err == nil {
			out = append(out, parsed)
		}
	}
	return out
}
```

Then replace the two nil literals at `models.go:233,235`:

```go
	return intelligence.ProjectionInput{
		ProviderID: row.ProviderID,
		Canonical:  canonical,
		// The canonical model id is CanonicalKey(providerID, providerModelID),
		// so a canonical model is already provider-scoped and carries no
		// capability fact distinct from the resolved offering fact. Passing the
		// offering's own resolved set is therefore the honest value, not a
		// second store to drift; `effective` reduces to resolved-and-carriable.
		NativeCapabilities:  offeringOps,
		Offering:            offering,
		TransportOperations: h.transportOperationsFor(row.ProviderID),
		Certifications:      certs,
		Cost:                cost,
		Classification:      classification,
		ProvedOperations:    provedOps,
	}
```

`offeringOps` is already in scope at that point — it is what `offering.Capabilities` is built from.

- [ ] **Step 5: Wire it at the composition root**

`internal/httpapi/controlmux.go:286-288`. `probeTransports` is built at `:365-379`, which is AFTER this block — move the `modelsHandler` construction below the `probeTransports` loop, or hoist the loop above `:274`. Prefer hoisting the loop, so the two maps are built once before every consumer:

```go
	modelsHandler := NewModelsHandler(catalogRepo, nil).
		WithProbeRuns(probeRunRepo).
		WithBenchmarkRuns(benchmarkRunRepo).
		WithTransports(probeTransports)
```

Add an import for `internal/execution` in `models.go` if absent.

- [ ] **Step 6: Run and confirm it passes**

Run: `go test ./internal/httpapi/ -run TestServeOfferings_CapabilitiesAreRoutable -v`
Expected: PASS

- [ ] **Step 7: Mutation-proof at the composition root**

Delete `.WithTransports(probeTransports)` from `controlmux.go`. A test MUST fail. If only the unit test in Step 1 fails and no test exercising the real `controlmux` wiring does, add one that builds the mux and asserts a routable capability — the wiring is the thing being fixed, so the wiring needs its own guard. Restore.

- [ ] **Step 8: Run the full suite and commit**

Run: `go test ./...`
Expected: PASS. Tests that asserted `routable: false` or `ENABLED: 0` were pinning the defect — update them to the corrected behaviour and say so in the message.

```bash
git add internal/httpapi/
git commit -m "fix(httpapi): give the projection the inputs it needs to compute routable

models.go passed NativeCapabilities and TransportOperations as nil, so
readmodel's effective — and routable with it — was false for every
capability of every offering, whatever any adapter declared or any probe
proved. That is the WORKING 12 / ENABLED 0 contradiction on the report.

The native axis is the offering's own resolved set: a canonical model id
is CanonicalKey(providerID, providerModelID), so it is already
provider-scoped and holds no distinct capability fact. Transport support
comes from the same map the probe path uses.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## Task 8: Let the catalog answer the context question, and surface the token limits

`models.EffectiveContext` reads only `native_context_tokens` and `Offering.ContextLength`. With Task 6 the catalog fills `ContextLength`, so context resolves — but the read model still has no `MaxInputTokens`/`MaxOutputTokens` fields, so those two columns remain write-only.

**Files:**
- Modify: `internal/intelligence/readmodel.go:104-117` (`EffectiveOffering`), `:127-150` (`Project`)
- Modify: `internal/httpapi/models.go:291-305` (`effectiveOfferingJSON`), and its construction
- Test: `internal/intelligence/readmodel_test.go`, `internal/httpapi/models_test.go`

**Interfaces:**
- Consumes: `offering.MaxInputTokens` / `offering.MaxOutputTokens`, already populated (`models.go:212-213`).
- Produces: `EffectiveOffering.MaxInputTokens *int` and `.MaxOutputTokens *int`; wire fields `max_input_tokens` and `max_output_tokens`, both nullable.

- [ ] **Step 1: Write the failing test**

```go
func TestProject_CarriesTheOfferingTokenLimits(t *testing.T) {
	in := ProjectionInput{
		ProviderID: "clinepass",
		Offering: models.Offering{
			Identity:        models.OfferingIdentity{AccountID: "a", ProviderModelID: "m"},
			Availability:    models.AvailabilityAvailable,
			ContextLength:   intPtr(1048576),
			MaxInputTokens:  intPtr(900000),
			MaxOutputTokens: intPtr(131072),
		},
	}
	got := Project(in)
	if got.MaxInputTokens == nil || *got.MaxInputTokens != 900000 {
		t.Fatalf("MaxInputTokens = %v, want 900000 — the column has been written since discovery and read by nobody", got.MaxInputTokens)
	}
	if got.MaxOutputTokens == nil || *got.MaxOutputTokens != 131072 {
		t.Fatalf("MaxOutputTokens = %v, want 131072", got.MaxOutputTokens)
	}
	if got.EffectiveContextTokens == nil || *got.EffectiveContextTokens != 1048576 {
		t.Fatalf("EffectiveContextTokens = %v, want 1048576 from the offering's catalog-filled context length", got.EffectiveContextTokens)
	}
	if got.ContextProvenance != models.ContextProvenanceProviderCap {
		t.Fatalf("ContextProvenance = %q, want provider_cap", got.ContextProvenance)
	}
}
```

Use the package's existing `intPtr` helper if one exists; otherwise define it in the test file.

- [ ] **Step 2: Run and confirm it fails**

Run: `go test ./internal/intelligence/ -run TestProject_CarriesTheOfferingTokenLimits -v`
Expected: FAIL — no such fields.

- [ ] **Step 3: Add the fields**

In `internal/intelligence/readmodel.go`, add to `EffectiveOffering` after `ContextProvenance`:

```go
	// MaxInputTokens and MaxOutputTokens are the offering's own declared
	// per-request limits, distinct from the context window: a model may accept
	// a 1M-token context while capping a single reply at 131k. Both are nil
	// when the provider and the catalog are silent. They were persisted at
	// discovery and read by nobody until now.
	MaxInputTokens  *int
	MaxOutputTokens *int
```

and in `Project`'s literal:

```go
		MaxInputTokens:         in.Offering.MaxInputTokens,
		MaxOutputTokens:        in.Offering.MaxOutputTokens,
```

- [ ] **Step 4: Add the wire fields**

In `internal/httpapi/models.go`, add to `effectiveOfferingJSON` after `ContextProvenance`:

```go
	MaxInputTokens         *int                `json:"max_input_tokens"`
	MaxOutputTokens        *int                `json:"max_output_tokens"`
```

and populate both at its construction site from the projected values. Both are nullable and NOT `omitempty`: a `null` distinguishes "unknown" from "the key is missing because of a bug", the same reasoning the file already applies to `effective_context_tokens`.

- [ ] **Step 5: Run and confirm they pass**

Run: `go test ./internal/intelligence/ ./internal/httpapi/ -run 'TokenLimits|Offerings' -v`
Expected: PASS

- [ ] **Step 6: Mutation-proof**

Change `MaxOutputTokens: in.Offering.MaxOutputTokens` to `nil`. The test MUST fail. Restore.

- [ ] **Step 7: Full gate**

Run: `task gate`
Expected: every step green on Windows. This is the only claim-worthy check; read the step ORDER before trusting it — an early failure masks later steps.

- [ ] **Step 8: Commit**

```bash
git add internal/intelligence/ internal/httpapi/
git commit -m "feat(readmodel): project the offering token limits

max_input_tokens and max_output_tokens have been written at discovery
since M4 and read by nobody: EffectiveOffering had no field for them, so
they never reached the API. They are distinct from the context window —
kimi-k3 accepts 1M of context and caps a reply at 131k — and the tier
engine needs both.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## Task 9: Remove the duplicate provider registration list

`internal/httpapi/publicmux.go:109-119` is a second, hand-maintained copy of `provider_registry.go:16-38`. Task 6 changed a registration signature and this is exactly the kind of edit that gets applied to one list and not the other.

**Files:**
- Modify: `internal/httpapi/publicmux.go:109-119`
- Test: `internal/httpapi/publicmux_test.go`

**Interfaces:**
- Consumes: `newProviderRegistry()` (`provider_registry.go:16`).
- Produces: nothing new.

- [ ] **Step 1: Write the failing test**

```go
func TestPublicMux_FallbackRegistryMatchesTheCompositionRoot(t *testing.T) {
	want := newProviderRegistry()
	got := publicMuxFallbackRegistry()

	wantIDs := providerIDsOf(want)
	gotIDs := providerIDsOf(got)
	if !reflect.DeepEqual(wantIDs, gotIDs) {
		t.Fatalf("fallback registry = %v, want %v — two hand-maintained lists drift; there must be one", gotIDs, wantIDs)
	}
}
```

`providerIDsOf` returns the registry's registered ids, sorted. If the `Registry` type exposes no such accessor, add one to `internal/providers/registry.go` — it is a read-only listing and is genuinely useful:

```go
// IDs returns every registered provider id, sorted, so two registries can be
// compared and a composition can be asserted against.
func (r *Registry) IDs() []ProviderID {
	out := make([]ProviderID, 0, len(r.defs))
	for id := range r.defs {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
```

Use the real field name for the registry's internal map.

- [ ] **Step 2: Run and confirm it fails**

Run: `go test ./internal/httpapi/ -run TestPublicMux_FallbackRegistry -v`
Expected: FAIL — `publicMuxFallbackRegistry` undefined.

- [ ] **Step 3: Collapse the duplicate**

Replace `publicmux.go:109-119` with:

```go
		if reg == nil {
			reg = publicMuxFallbackRegistry()
		}
```

and add, near it:

```go
// publicMuxFallbackRegistry builds the provider registry publicMux uses when
// none was injected. It delegates to the ONE composition list rather than
// repeating it: a second hand-maintained list drifts the moment a provider is
// added or a registration signature changes.
func publicMuxFallbackRegistry() *providers.Registry {
	return newProviderRegistry()
}
```

Note the behaviour difference this fixes: the old list registered antigravity LAST and the composition root registers it FIRST. Registration order is not significant to `Registry.Register`, but the old list is also missing nothing else — confirm by running the test, not by reading.

- [ ] **Step 4: Run and confirm it passes**

Run: `go test ./internal/httpapi/ -run TestPublicMux_FallbackRegistry -v`
Expected: PASS

- [ ] **Step 5: Mutation-proof**

Add `_ = registerOpenCodeZen(reg)` a second time inside `newProviderRegistry`. The test should still pass (a duplicate registration is either rejected or idempotent). Instead REMOVE `_ = registerClinePass(reg)` from `newProviderRegistry`; the test MUST fail. Restore.

- [ ] **Step 6: Full gate and commit**

Run: `task gate`

```bash
git add internal/httpapi/ internal/providers/registry.go
git commit -m "refactor(httpapi): one provider registration list, not two

publicmux carried a hand-maintained copy of newProviderRegistry's list.
Adding the models.dev probe to RegisterClinePass is exactly the change
that gets applied to one copy and not the other.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## Self-Review

**Spec coverage.** §4.1 extended facts → Task 3. §4.1 verified key map → Task 4. §4.1 capability derivation → Task 5. §4.1 image-output models kept → Task 5 Step 4. §4.3 routability → Task 7. §4.3.1 transport under-declaration → Tasks 1 and 2. §4.4 context and token limits → Tasks 6 and 8. §8.1 collapsed native axis → Task 7 Step 4.

**Deferred to the next plan, deliberately:** wiring the catalog into gemini-cli, claude-code, opencode-zen, ollama-cloud and nvidia-nim (Task 6 is the pilot; replication is mechanical once its shape is reviewed); reading gemini's `thinking` wire field; the operation-aware `ReviewDrainer`; everything in spec Phases 3, 4 and 5.

**Dropped from the spec, with reason:** "an `offering_operations` row for all eight operations" is no longer needed. It existed to make capabilities probeable on models whose adapter declared nothing — the catalog now declares them at the source, so rows follow declarations as they always did, and no `ReviewDrainer` change is required in this plan. The spec is amended accordingly.

**Type consistency.** `execution.Operation` (5 values after Task 2) → `models.Operation` (8 values) conversion happens in exactly one place, `transportOperationsFor` (Task 7), via `models.ParseOperation`. `OperationsFromFacts` returns `[]string`, matching `DiscoveredModel.Capabilities`. `FactsForProvider` takes `ProviderID` and returns the same map type as `Facts`.

**Known gap this plan does not close:** `structured_output` is carriable only over the two OpenAI-shaped codecs. On anthropic-messages and google-generate-content it fails closed, so a claude-code or gemini-cli offering will show `structured_output` as certified-but-not-routable. That is honest and correct — we cannot route what we cannot send — and closing it means mapping the operation onto each provider's native mechanism, which is its own piece of work.
