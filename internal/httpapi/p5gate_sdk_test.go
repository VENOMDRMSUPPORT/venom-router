package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// P5-TEST-001 — the docs/06 Phase 5 acceptance gate, mechanized. The gate reads:
// "point a real OpenAI SDK / IDE at Venom and use venom/lite|pro|max for chat +
// streaming + tools + vision; the venom extension clamps thinking above the tier
// ceiling (reported in diagnostics), enforces required_capabilities as hard
// gates, survives streaming, and rejects invalid fields with the typed error;
// usage and route decisions are recorded."
//
// GOVERNOR DECISION (P5-TEST-001 card): the CI gate proves WIRE-PROTOCOL
// CONFORMANCE with a plain net/http client — the exact request/response shapes a
// real OpenAI SDK depends on — because no `openai` dependency may be added. The
// real-SDK run is the dated manual runbook docs/evidence/P5-TEST-001-real-sdk-runbook.md
// plus the opt-in harness p5gate_realsdk_test.go (which t.Skips with no env).

// setFundingPaid flips a seeded offering's funding evidence to paid, so a
// free-only tier (Lite) must refuse it while a free+paid tier (Pro/Max) admits
// it — the seed helper otherwise records free funding.
func setFundingPaid(t *testing.T, db *storage.DB, acct string) {
	t.Helper()
	if _, err := db.Conn().Exec(`UPDATE account_funding_evidence SET funding='paid' WHERE account_id=?`, acct); err != nil {
		t.Fatalf("set funding paid: %v", err)
	}
}

// countRowsWhere counts rows of table where col = val.
func countRowsWhere(t *testing.T, db *storage.DB, table, col, val string) int {
	t.Helper()
	var n int
	if err := db.Conn().QueryRow("SELECT count(*) FROM "+table+" WHERE "+col+" = ?", val).Scan(&n); err != nil {
		t.Fatalf("count %s where %s: %v", table, col, err)
	}
	return n
}

// parseSSEDataFrames returns the ordered `data: ` payloads of an SSE body,
// ignoring `:` comment lines exactly as a compliant SSE reader (and a real
// OpenAI SDK) does.
func parseSSEDataFrames(body string) []string {
	var out []string
	for _, ln := range strings.Split(body, "\n") {
		if strings.HasPrefix(ln, "data: ") {
			out = append(out, strings.TrimPrefix(ln, "data: "))
		}
	}
	return out
}

// TestP5Gate_ChatCompletionsWireConformance pins the exact non-streaming OpenAI
// completion object shape an SDK parses.
//
// T1-M2: rename `object` to `type` in the completion body ⇒ RED.
func TestP5Gate_ChatCompletionsWireConformance(t *testing.T) {
	_, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) { _, _ = w.Write([]byte(completionJSON("hello world"))) })
	db, kr := e2eEnv(t, srv.URL)
	seedCredentialedOffering(t, db, kr, "acct-1", "prov/model-a", "model-a")
	h := newE2EHandler(t, db, kr, srv.URL, nil)

	rec := postChat(t, h, `{"model":"venom/pro","messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Result().Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	var resp struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Model   string `json:"model"`
		Choices []struct {
			Index   int `json:"index"`
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body %s", err, rec.Body.String())
	}
	if resp.Object != "chat.completion" {
		t.Fatalf("object = %q, want chat.completion (an SDK keys on this)", resp.Object)
	}
	if resp.ID == "" || resp.Model != "venom/pro" {
		t.Fatalf("id/model wrong: id=%q model=%q", resp.ID, resp.Model)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("choices = %d, want 1", len(resp.Choices))
	}
	c := resp.Choices[0]
	if c.Index != 0 || c.Message.Role != "assistant" || c.Message.Content != "hello world" || c.FinishReason != "stop" {
		t.Fatalf("choice shape wrong: %+v", c)
	}
}

// TestP5Gate_StreamingWireConformance pins the SSE chunk shape + framing + the
// [DONE] terminator an SDK's streaming parser depends on.
//
// T1-M1: drop [DONE] from the stream ⇒ RED.
func TestP5Gate_StreamingWireConformance(t *testing.T) {
	_, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) {
		fl, _ := w.(http.Flusher)
		for _, d := range []string{"Hel", "lo"} {
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"" + d + "\"}}]}\n\n"))
			if fl != nil {
				fl.Flush()
			}
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	})
	db, kr := e2eEnv(t, srv.URL)
	seedCredentialedOffering(t, db, kr, "acct-1", "prov/model-a", "model-a")
	certifyOperation(t, db, "acct-1", "prov/model-a", "streaming")
	h := newE2EHandler(t, db, kr, srv.URL, nil)

	rec := postChat(t, h, `{"model":"venom/pro","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Result().Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}
	frames := parseSSEDataFrames(rec.Body.String())
	if len(frames) == 0 || frames[len(frames)-1] != "[DONE]" {
		t.Fatalf("stream must terminate with data: [DONE]; frames=%v", frames)
	}
	// At least one chunk carries the OpenAI chunk shape.
	var sawChunk bool
	for _, f := range frames {
		if f == "[DONE]" {
			continue
		}
		var chunk struct {
			Object  string `json:"object"`
			Choices []struct {
				Index int `json:"index"`
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(f), &chunk); err != nil {
			t.Fatalf("chunk not JSON: %q", f)
		}
		if chunk.Object == "chat.completion.chunk" && len(chunk.Choices) == 1 && chunk.Choices[0].Delta.Content != "" {
			sawChunk = true
		}
	}
	if !sawChunk {
		t.Fatalf("no chunk had object=chat.completion.chunk with delta.content; frames=%v", frames)
	}
}

// TestP5Gate_ToolsWireConformance proves a tools array reaches the provider and
// the returned tool call surfaces in the completion.
func TestP5Gate_ToolsWireConformance(t *testing.T) {
	var up *capturingUpstream
	up, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"type":"function","function":{"name":"get_weather","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`))
	})
	db, kr := e2eEnv(t, srv.URL)
	seedCredentialedOffering(t, db, kr, "acct-1", "prov/model-a", "model-a")
	certifyOperation(t, db, "acct-1", "prov/model-a", "tools")
	h := newE2EHandler(t, db, kr, srv.URL, nil)

	body := `{"model":"venom/pro","messages":[{"role":"user","content":"w?"}],"tools":[{"type":"function","function":{"name":"get_weather","description":"g","parameters":{"type":"object"}}}]}`
	rec := postChat(t, h, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(string(up.lastBody), `"tools"`) || !strings.Contains(string(up.lastBody), "get_weather") {
		t.Fatalf("tools did not reach the provider: %s", up.lastBody)
	}
	if !strings.Contains(rec.Body.String(), "tool_calls") {
		t.Fatalf("completion lacks tool_calls: %s", rec.Body.String())
	}
}

// TestP5Gate_VisionEndToEnd proves an image content part reaches the provider in
// the OpenAI array-content form on a VISION-CERTIFIED offering. Vision is
// genuinely assertable at the wire level here (this makes no claim about a
// provider's actual vision behavior — that is the probe side).
//
// T1-M3: drop image parts before dispatch ⇒ RED.
func TestP5Gate_VisionEndToEnd(t *testing.T) {
	var up *capturingUpstream
	up, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) { _, _ = w.Write([]byte(completionJSON("saw it"))) })
	db, kr := e2eEnv(t, srv.URL)
	seedCredentialedOffering(t, db, kr, "acct-1", "prov/model-a", "model-a")
	certifyOperation(t, db, "acct-1", "prov/model-a", "vision")
	h := newE2EHandler(t, db, kr, srv.URL, nil)

	body := `{"model":"venom/pro","messages":[{"role":"user","content":[{"type":"text","text":"what is this"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]}]}`
	rec := postChat(t, h, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(string(up.lastBody), "image_url") || !strings.Contains(string(up.lastBody), "data:image/png;base64,AAAA") {
		t.Fatalf("image part did not reach the provider in array form: %s", up.lastBody)
	}
}

// TestP5Gate_ThreeTierModelsRoute proves all three tier model names route with an
// appropriate fleet, and that Lite is FREE-ONLY: a paid-only offering routes for
// Pro but is refused for Lite.
//
// T1-M4: let Lite reach a paid offering ⇒ the Lite-refuses-paid assertion RED.
func TestP5Gate_ThreeTierModelsRoute(t *testing.T) {
	for _, model := range []string{"venom/lite", "venom/pro", "venom/max"} {
		_, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) { _, _ = w.Write([]byte(completionJSON("ok"))) })
		db, kr := e2eEnv(t, srv.URL)
		seedCredentialedOffering(t, db, kr, "acct-1", "prov/model-a", "model-a") // free
		h := newE2EHandler(t, db, kr, srv.URL, nil)
		rec := postChat(t, h, `{"model":"`+model+`","messages":[{"role":"user","content":"hi"}]}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s did not route on a free fleet: status %d body %s", model, rec.Code, rec.Body.String())
		}
	}

	// Lite free-only vs a PAID-only fleet: Pro routes, Lite refuses. Each check
	// runs on its OWN db+handler — sharing a db would collide their reservations
	// (both handlers mint the same request ids), making Lite fail for a
	// reservation reason and masking a funding-gate regression.
	proSrv := func() { // Pro routes a paid offering (positive control).
		_, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) { _, _ = w.Write([]byte(completionJSON("ok"))) })
		db, kr := e2eEnv(t, srv.URL)
		seedCredentialedOffering(t, db, kr, "acct-1", "prov/model-a", "model-a")
		setFundingPaid(t, db, "acct-1")
		h := newE2EHandler(t, db, kr, srv.URL, nil)
		if rec := postChat(t, h, `{"model":"venom/pro","messages":[{"role":"user","content":"hi"}]}`); rec.Code != http.StatusOK {
			t.Fatalf("Pro must route a paid offering: status %d body %s", rec.Code, rec.Body.String())
		}
	}
	proSrv()

	// Lite refuses the same paid-only fleet.
	_, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) { _, _ = w.Write([]byte(completionJSON("ok"))) })
	db, kr := e2eEnv(t, srv.URL)
	seedCredentialedOffering(t, db, kr, "acct-1", "prov/model-a", "model-a")
	setFundingPaid(t, db, "acct-1")
	liteH := newE2EHandler(t, db, kr, srv.URL, nil)
	if rec := postChat(t, liteH, `{"model":"venom/lite","messages":[{"role":"user","content":"hi"}]}`); rec.Code == http.StatusOK {
		t.Fatalf("Lite is free-only and must REFUSE a paid-only fleet; got 200")
	}
}

// TestP5Gate_ExtensionClampsThinkingReported proves a thinking budget above the
// tier ceiling is clamped and the clamp is REPORTED in the X-Venom-* diagnostics.
//
// ⚠️ KNOWN LIMIT (report, do not fake): reasoning certification is not populated
// by the candidate snapshot, so the APPLIED level is `none` for every request.
// This test asserts the CLAMP and its REPORTING (both true). "Applied above none"
// is UNPROVEN pending reasoning certification in the snapshot; no fake reasoning
// fact is seeded.
//
// T1-M5: stop reporting the thinking clamp ⇒ RED.
func TestP5Gate_ExtensionClampsThinkingReported(t *testing.T) {
	_, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) { _, _ = w.Write([]byte(completionJSON("ok"))) })
	db, kr := e2eEnv(t, srv.URL)
	seedCredentialedOffering(t, db, kr, "acct-1", "prov/model-a", "model-a")
	h := newE2EHandler(t, db, kr, srv.URL, nil)

	rec := postChat(t, h, `{"model":"venom/pro","messages":[{"role":"user","content":"hi"}],"venom":{"thinking_budget":"ultra"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("clamped request must succeed: status %d", rec.Code)
	}
	if rec.Result().Header.Get("X-Venom-Thinking-Clamped") != "true" {
		t.Fatalf("thinking clamp not reported in diagnostics: %q", rec.Result().Header.Get("X-Venom-Thinking-Clamped"))
	}
}

// TestP5Gate_RequiredCapabilitiesHardGate proves required_capabilities are hard
// gates: an uncertifiable capability never routes.
func TestP5Gate_RequiredCapabilitiesHardGate(t *testing.T) {
	up, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) { _, _ = w.Write([]byte(completionJSON("ok"))) })
	db, kr := e2eEnv(t, srv.URL)
	seedCredentialedOffering(t, db, kr, "acct-1", "prov/model-a", "model-a") // chat only
	h := newE2EHandler(t, db, kr, srv.URL, nil)

	rec := postChat(t, h, `{"model":"venom/pro","messages":[{"role":"user","content":"hi"}],"venom":{"required_capabilities":["vision"]}}`)
	if rec.Code == http.StatusOK {
		t.Fatalf("a required uncertified capability must NOT route; got 200")
	}
	if up.calls != 0 {
		t.Fatalf("provider called %d times though no eligible route; want 0", up.calls)
	}
}

// TestP5Gate_ExtensionSurvivesStreaming proves the extension's applied/clamp
// facts survive streaming — present in the trailing SSE comment.
func TestP5Gate_ExtensionSurvivesStreaming(t *testing.T) {
	_, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) {
		fl, _ := w.(http.Flusher)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n"))
		if fl != nil {
			fl.Flush()
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	})
	db, kr := e2eEnv(t, srv.URL)
	seedCredentialedOffering(t, db, kr, "acct-1", "prov/model-a", "model-a")
	certifyOperation(t, db, "acct-1", "prov/model-a", "streaming")
	h := newE2EHandler(t, db, kr, srv.URL, nil)

	rec := postChat(t, h, `{"model":"venom/pro","stream":true,"messages":[{"role":"user","content":"hi"}],"venom":{"thinking_budget":"ultra"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "x-venom-thinking-clamped=true") {
		t.Fatalf("streaming trailer must carry the clamp fact; body:\n%s", rec.Body.String())
	}
}

// TestP5Gate_InvalidExtensionRejected proves an invalid extension field is the
// typed venom_invalid_extension 400.
func TestP5Gate_InvalidExtensionRejected(t *testing.T) {
	_, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) { _, _ = w.Write([]byte(completionJSON("ok"))) })
	db, kr := e2eEnv(t, srv.URL)
	seedCredentialedOffering(t, db, kr, "acct-1", "prov/model-a", "model-a")
	h := newE2EHandler(t, db, kr, srv.URL, nil)

	rec := postChat(t, h, `{"model":"venom/pro","messages":[{"role":"user","content":"hi"}],"venom":{"thinking_budget":"turbo"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rec.Code)
	}
	if got := decodePublicError(t, rec.Body.Bytes()).Code; got != CodeInvalidExtension {
		t.Fatalf("code = %q, want %s", got, CodeInvalidExtension)
	}
}

// TestP5Gate_UsageAndDecisionRecorded proves the gate's happy path records BOTH
// a usage row and a route_decisions row for the same request id.
//
// T1-M6: skip the route-decision write ⇒ RED.
func TestP5Gate_UsageAndDecisionRecorded(t *testing.T) {
	_, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) { _, _ = w.Write([]byte(completionJSON("ok"))) })
	db, kr := e2eEnv(t, srv.URL)
	seedCredentialedOffering(t, db, kr, "acct-1", "prov/model-a", "model-a")
	h := newE2EHandler(t, db, kr, srv.URL, nil)

	rec := postChat(t, h, `{"model":"venom/pro","messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	rid := rec.Result().Header.Get("X-Venom-Request-Id")
	if rid == "" {
		t.Fatalf("missing X-Venom-Request-Id")
	}
	if n := countRowsWhere(t, db, "usage_records", "request_id", rid); n != 1 {
		t.Fatalf("usage_records for request %q = %d, want 1", rid, n)
	}
	if n := countRowsWhere(t, db, "route_decisions", "request_id", rid); n != 1 {
		t.Fatalf("route_decisions for request %q = %d, want 1", rid, n)
	}
}

// TestP5Gate_Canary proves a marker planted in the prompt and in the provider's
// response appears in no DB row and no response header (05 §7).
func TestP5Gate_Canary(t *testing.T) {
	const promptMarker = "P5GATECANARYprompt"
	const respMarker = "P5GATECANARYresp"
	_, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) { _, _ = w.Write([]byte(completionJSON(respMarker))) })
	db, kr := e2eEnv(t, srv.URL)
	seedCredentialedOffering(t, db, kr, "acct-1", "prov/model-a", "model-a")
	h := newE2EHandler(t, db, kr, srv.URL, nil)

	rec := postChat(t, h, `{"model":"venom/pro","messages":[{"role":"user","content":"`+promptMarker+`"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	for _, table := range []string{"usage_records", "route_decisions", "route_attempts"} {
		dump := dumpTable(t, db, table)
		if strings.Contains(dump, promptMarker) || strings.Contains(dump, respMarker) {
			t.Fatalf("content marker leaked into %s", table)
		}
	}
	for key, vals := range rec.Result().Header {
		for _, v := range vals {
			if strings.Contains(v, promptMarker) || strings.Contains(v, respMarker) {
				t.Fatalf("content marker leaked into header %s", key)
			}
		}
	}
}
