package intelligence

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
)

type fakeProbeTransport struct {
	t      *testing.T
	trap   bool
	result ProbeResult
	err    error
	calls  []ProbeRequest
}

func (f *fakeProbeTransport) Probe(_ context.Context, req ProbeRequest) (ProbeResult, error) {
	if f.trap {
		f.t.Fatal("transport must not be called on a refusal path")
	}
	f.calls = append(f.calls, req)
	if f.err != nil {
		return ProbeResult{}, f.err
	}
	return f.result, nil
}

func admittingContextGuard(t *testing.T, now time.Time) *ProbeGuard {
	t.Helper()
	policy := DefaultProbeSafetyPolicy()
	policy.ExpensiveProbesEnabled = true
	g, err := NewProbeGuard(policy, &fakeProbeReserver{reservationID: "rsv-ctx"}, &fakeSpendReader{}, &fakeInFlightReader{}, &fakeCooldownReader{}, clockAt(now))
	if err != nil {
		t.Fatalf("NewProbeGuard error = %v", err)
	}
	return g
}

func refusingContextGuard(t *testing.T, now time.Time) *ProbeGuard {
	t.Helper()
	policy := DefaultProbeSafetyPolicy()
	policy.ExpensiveProbesEnabled = false
	g, err := NewProbeGuard(policy, &fakeProbeReserver{t: t, trap: true}, &fakeSpendReader{}, &fakeInFlightReader{}, &fakeCooldownReader{}, clockAt(now))
	if err != nil {
		t.Fatalf("NewProbeGuard error = %v", err)
	}
	return g
}

func baseProbeRequest() ProbeRequest {
	return ProbeRequest{
		AccountID:           "acct-1",
		ProviderID:          "prov-1",
		ProviderModelID:     "model-1",
		OfferingOperationID: "off-op-1",
	}
}

func TestExtractContextLimit_LadderOrder(t *testing.T) {
	rule := ContextLimitRule{ProviderID: "prov-1", Pattern: regexp.MustCompile(`ctxlimit=(\d+)`)}

	t.Run("structured field wins over every text form", func(t *testing.T) {
		limit := 5000
		res := ProbeResult{
			StructuredContextLimit: &limit,
			ProviderCode:           "context_length_exceeded",
			Message:                "maximum context length is 999 tokens; ctxlimit=111",
		}
		n, rung, ok := ExtractContextLimit(res, []ContextLimitRule{rule}, "prov-1")
		if !ok || n != 5000 || rung != RungStructuredField {
			t.Fatalf("got (%d, %q, %v), want (5000, structured_field, true)", n, rung, ok)
		}
	})

	t.Run("openai phrase wins over provider regex and generic keyword", func(t *testing.T) {
		res := ProbeResult{
			ProviderCode: "context_length_exceeded",
			Message:      "maximum context length is 128000 tokens (ctxlimit=333, context length 777)",
		}
		n, rung, ok := ExtractContextLimit(res, []ContextLimitRule{rule}, "prov-1")
		if !ok || n != 128000 || rung != RungOpenAIPhrase {
			t.Fatalf("got (%d, %q, %v), want (128000, openai_phrase, true)", n, rung, ok)
		}
	})

	t.Run("provider regex wins over generic keyword", func(t *testing.T) {
		res := ProbeResult{
			ProviderCode: "some_other_code",
			Message:      "ctxlimit=222 tokens; context length 444",
		}
		n, rung, ok := ExtractContextLimit(res, []ContextLimitRule{rule}, "prov-1")
		if !ok || n != 222 || rung != RungProviderRegex {
			t.Fatalf("got (%d, %q, %v), want (222, provider_regex, true)", n, rung, ok)
		}
	})

	t.Run("generic keyword when nothing else matches", func(t *testing.T) {
		res := ProbeResult{
			ProviderCode: "some_other_code",
			Message:      "the model's context length is 99999 tokens",
		}
		n, rung, ok := ExtractContextLimit(res, nil, "prov-1")
		if !ok || n != 99999 || rung != RungGenericKeyword {
			t.Fatalf("got (%d, %q, %v), want (99999, generic_keyword, true)", n, rung, ok)
		}
	})
}

func TestExtractContextLimit_OpenAIPhraseIsGatedOnProviderCode(t *testing.T) {
	msg := "maximum context length is 128000 tokens"

	t.Run("without the gating code, rung 2 does not fire", func(t *testing.T) {
		res := ProbeResult{ProviderCode: "", Message: msg}
		_, rung, ok := ExtractContextLimit(res, nil, "prov-1")
		if rung == RungOpenAIPhrase {
			t.Fatalf("rung = %q, must not be openai_phrase without the gating provider code", rung)
		}
		_ = ok
	})

	t.Run("with the gating code, rung 2 fires", func(t *testing.T) {
		res := ProbeResult{ProviderCode: "context_length_exceeded", Message: msg}
		n, rung, ok := ExtractContextLimit(res, nil, "prov-1")
		if !ok || n != 128000 || rung != RungOpenAIPhrase {
			t.Fatalf("got (%d, %q, %v), want (128000, openai_phrase, true)", n, rung, ok)
		}
	})
}

func TestExtractContextLimit_NoSignalNeverGuesses(t *testing.T) {
	t.Run("prose with no usable number", func(t *testing.T) {
		res := ProbeResult{Message: "The model does not support such a large context."}
		n, rung, ok := ExtractContextLimit(res, nil, "prov-1")
		if ok || n != 0 || rung != RungNoSignal {
			t.Fatalf("got (%d, %q, %v), want (0, no_signal, false)", n, rung, ok)
		}
	})

	t.Run("unrelated number far from any keyword", func(t *testing.T) {
		msg := "Request id 123456789." + strings.Repeat(" ", 60) + "context length exceeded"
		res := ProbeResult{Message: msg}
		n, rung, ok := ExtractContextLimit(res, nil, "prov-1")
		if ok || n != 0 || rung != RungNoSignal {
			t.Fatalf("got (%d, %q, %v), want (0, no_signal, false)", n, rung, ok)
		}
	})

	t.Run("zero/negative extracted value is never returned", func(t *testing.T) {
		negLimit := -5
		res := ProbeResult{
			StructuredContextLimit: &negLimit,
			Message:                "context length 0 tokens",
		}
		n, rung, ok := ExtractContextLimit(res, nil, "prov-1")
		if ok || n != 0 || rung != RungNoSignal {
			t.Fatalf("got (%d, %q, %v), want (0, no_signal, false)", n, rung, ok)
		}
	})
}

func TestContextProbe_RefusalNeverCallsTransport(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	trap := &fakeProbeTransport{t: t, trap: true}
	guard := refusingContextGuard(t, now)

	probe, err := NewContextProbe(trap, guard, nil, clockAt(now))
	if err != nil {
		t.Fatalf("NewContextProbe error = %v", err)
	}
	_, err = probe.Run(context.Background(), baseProbeRequest())
	if reason, ok := RefusalOf(err); !ok || reason != RefusalOptInRequired {
		t.Fatalf("refusal = %v (ok=%v), want probe_opt_in_required", reason, ok)
	}
}

func TestContextProbe_SendsExactlyOneOversizedRequest(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	transport := &fakeProbeTransport{result: ProbeResult{HTTPStatus: 400, Message: "no signal here"}}
	guard := admittingContextGuard(t, now)

	probe, err := NewContextProbe(transport, guard, nil, clockAt(now))
	if err != nil {
		t.Fatalf("NewContextProbe error = %v", err)
	}
	if _, err := probe.Run(context.Background(), baseProbeRequest()); err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if len(transport.calls) != 1 {
		t.Fatalf("transport called %d times, want 1", len(transport.calls))
	}
	call := transport.calls[0]
	if call.DeclaredInputTokens != ContextProbeInputTokens {
		t.Errorf("DeclaredInputTokens = %d, want %d", call.DeclaredInputTokens, ContextProbeInputTokens)
	}
	if call.MaxOutputTokens != ContextProbeMaxOutputTokens {
		t.Errorf("MaxOutputTokens = %d, want %d", call.MaxOutputTokens, ContextProbeMaxOutputTokens)
	}
	if call.Operation != models.OperationContextWindow {
		t.Errorf("Operation = %q, want %q", call.Operation, models.OperationContextWindow)
	}
	// Whole-branch review, FIX 2 (Critical): DeclaredInputTokens above is a
	// QUOTA-ACCOUNTING number only — probeadapters.go's Probe never
	// serializes it onto the wire; it maps req.Messages, and only
	// req.Messages, onto the outbound request body. Before this fix,
	// nothing in Run ever set req.Messages, so the provider received
	// "messages": [] and rejected with "at least one message is required"
	// — a MALFORMED-REQUEST error carrying no context-length signal at
	// all, never the genuine context-length rejection this probe exists to
	// elicit. This assertion is exactly what a canned-result fake (every
	// other test in this file) cannot catch: it inspects the REQUEST the
	// transport actually received, not the result it was told to return.
	if len(call.Messages) == 0 {
		t.Fatal("call.Messages is empty — the provider would see \"messages\": [] and reject for a missing body, never a genuine context-length error")
	}
	// No real provider's context window is anywhere near this small in
	// characters of text, so a body under this size cannot plausibly
	// trigger a genuine context-length rejection — it can only ever
	// trigger a content-shape rejection unrelated to context length.
	const minPlausibleOversizedBodyBytes = 1_000_000
	gotSize := 0
	for _, m := range call.Messages {
		gotSize += len(m.Content)
	}
	if gotSize < minPlausibleOversizedBodyBytes {
		t.Fatalf("total message content size = %d bytes, want >= %d — the request must be genuinely oversized, not merely DECLARED oversized via DeclaredInputTokens", gotSize, minPlausibleOversizedBodyBytes)
	}
}

func contextProbeSignalRows() []struct {
	name    string
	result  ProbeResult
	wantExe ProbeExecution
	wantTr  models.CapabilityTruth
} {
	return []struct {
		name    string
		result  ProbeResult
		wantExe ProbeExecution
		wantTr  models.CapabilityTruth
	}{
		{"transport timeout", ProbeResult{Transport: TransportTimeout}, ProbeRetryableFailure, models.TruthUnknown},
		{"transport network error", ProbeResult{Transport: TransportNetwork}, ProbeRetryableFailure, models.TruthUnknown},
		{"HTTP 429", ProbeResult{HTTPStatus: 429}, ProbeRetryableFailure, models.TruthUnknown},
		{"HTTP 500", ProbeResult{HTTPStatus: 500}, ProbeRetryableFailure, models.TruthUnknown},
		{"HTTP 401", ProbeResult{HTTPStatus: 401}, ProbeTerminalFailure, models.TruthUnknown},
		{"HTTP 403", ProbeResult{HTTPStatus: 403}, ProbeTerminalFailure, models.TruthUnknown},
		{"HTTP 400 with limit", ProbeResult{HTTPStatus: 400, ProviderCode: "context_length_exceeded", Message: "maximum context length is 128000 tokens"}, ProbeSucceeded, models.TruthSupported},
		{"HTTP 400 without any signal", ProbeResult{HTTPStatus: 400, Message: "bad request"}, ProbeInconclusive, models.TruthUnknown},
		{"HTTP 200 accepted", ProbeResult{HTTPStatus: 200, Witness: WitnessTextOnly}, ProbeInconclusive, models.TruthUnknown},
	}
}

func TestContextProbe_SignalMapping(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	for _, tt := range contextProbeSignalRows() {
		t.Run(tt.name, func(t *testing.T) {
			transport := &fakeProbeTransport{result: tt.result}
			guard := admittingContextGuard(t, now)
			probe, err := NewContextProbe(transport, guard, nil, clockAt(now))
			if err != nil {
				t.Fatalf("NewContextProbe error = %v", err)
			}
			report, err := probe.Run(context.Background(), baseProbeRequest())
			if err != nil {
				t.Fatalf("Run error = %v", err)
			}
			if report.Outcome.Execution != tt.wantExe {
				t.Errorf("Execution = %q, want %q", report.Outcome.Execution, tt.wantExe)
			}
			if report.Outcome.Truth != tt.wantTr {
				t.Errorf("Truth = %q, want %q", report.Outcome.Truth, tt.wantTr)
			}
		})
	}
}

func TestContextProbe_EvidenceOnlyOnDefinitiveOutcome(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	for _, tt := range contextProbeSignalRows() {
		t.Run(tt.name, func(t *testing.T) {
			transport := &fakeProbeTransport{result: tt.result}
			guard := admittingContextGuard(t, now)
			probe, err := NewContextProbe(transport, guard, nil, clockAt(now))
			if err != nil {
				t.Fatalf("NewContextProbe error = %v", err)
			}
			report, err := probe.Run(context.Background(), baseProbeRequest())
			if err != nil {
				t.Fatalf("Run error = %v", err)
			}
			if tt.wantTr == models.TruthSupported {
				if len(report.Evidence) != 1 {
					t.Fatalf("Evidence len = %d, want 1", len(report.Evidence))
				}
				ev := report.Evidence[0]
				if ev.Field != FieldNativeContextTokens {
					t.Errorf("Field = %q, want %q", ev.Field, FieldNativeContextTokens)
				}
				if ev.Scope != (Scope{AccountID: "acct-1", ProviderModelID: "model-1"}) {
					t.Errorf("Scope = %+v", ev.Scope)
				}
				if ev.Source != SourceVerifiedProbe || ev.Verification != VerificationVerified {
					t.Errorf("Source/Verification = %q/%q, want verified_probe/verified", ev.Source, ev.Verification)
				}
				if ev.Confidence != 1.0 {
					t.Errorf("Confidence = %v, want 1.0", ev.Confidence)
				}
				if !ev.ObservedAt.Equal(now) {
					t.Errorf("ObservedAt = %v, want %v", ev.ObservedAt, now)
				}
				if ev.Value != 128000 {
					t.Errorf("Value = %v, want 128000", ev.Value)
				}
				if report.Limit == nil || *report.Limit != 128000 {
					t.Errorf("Limit = %v, want 128000", report.Limit)
				}
			} else {
				if len(report.Evidence) != 0 {
					t.Errorf("Evidence len = %d, want 0", len(report.Evidence))
				}
			}
		})
	}
}

func TestContextProbe_InfraFailureLeavesLimitUnknown(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	infraRows := []ProbeResult{
		{HTTPStatus: 429},
		{Transport: TransportTimeout},
		{HTTPStatus: 500},
		{HTTPStatus: 401},
		{HTTPStatus: 403},
	}
	for _, res := range infraRows {
		t.Run(fmt.Sprintf("status=%d transport=%s", res.HTTPStatus, res.Transport), func(t *testing.T) {
			transport := &fakeProbeTransport{result: res}
			guard := admittingContextGuard(t, now)
			probe, err := NewContextProbe(transport, guard, nil, clockAt(now))
			if err != nil {
				t.Fatalf("NewContextProbe error = %v", err)
			}
			report, err := probe.Run(context.Background(), baseProbeRequest())
			if err != nil {
				t.Fatalf("Run error = %v", err)
			}
			if report.Limit != nil {
				t.Errorf("Limit = %v, want nil", report.Limit)
			}
			if report.Outcome.Truth != models.TruthUnknown {
				t.Errorf("Truth = %q, want unknown", report.Outcome.Truth)
			}
			if len(report.Evidence) != 0 {
				t.Errorf("Evidence len = %d, want 0", len(report.Evidence))
			}
		})
	}
}

func TestContextProbe_SnippetIsRedacted(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	const canary = "sk-canary-DEADBEEF0123456789"
	// The real limit (128000) is embedded inside a second credential-shaped
	// token (sk-limit-128000): if extraction ever ran on the REDACTED
	// message instead of the raw one, this token — and the digits within
	// it — would already be gone, and the generic-keyword rung would find
	// nothing. Finding 128000 here proves extraction ran on the raw text.
	transport := &fakeProbeTransport{result: ProbeResult{
		HTTPStatus:   400,
		ProviderCode: "some_other_code",
		Message:      "the context length is sk-limit-128000 tokens. Authorization: Bearer " + canary,
	}}
	guard := admittingContextGuard(t, now)
	probe, err := NewContextProbe(transport, guard, nil, clockAt(now))
	if err != nil {
		t.Fatalf("NewContextProbe error = %v", err)
	}
	report, err := probe.Run(context.Background(), baseProbeRequest())
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if report.Limit == nil || *report.Limit != 128000 {
		t.Fatalf("Limit = %v, want 128000 (extraction must run on the raw message, not the redacted snippet)", report.Limit)
	}
	if strings.Contains(report.Snippet, canary) {
		t.Fatalf("Snippet contains the canary credential: %q", report.Snippet)
	}
	if strings.Contains(report.Snippet, "sk-limit-128000") {
		t.Fatalf("Snippet contains the unredacted limit token: %q", report.Snippet)
	}
	full := fmt.Sprintf("%+v", report)
	if strings.Contains(full, canary) {
		t.Fatalf("full report contains the canary credential: %q", full)
	}
}

// TestContextProbe_ReportCarriesTheReservation pins the only handle a caller
// has on the quota this probe reserved: without it nothing can settle or
// release the reservation, and a probe's headroom would stay debited until
// the janitor reclaims it at the processing deadline.
func TestContextProbe_ReportCarriesTheReservation(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	transport := &fakeProbeTransport{result: ProbeResult{HTTPStatus: 400, ProviderCode: "context_length_exceeded", Message: "maximum context length is 128000 tokens"}}
	probe, err := NewContextProbe(transport, admittingContextGuard(t, now), nil, clockAt(now))
	if err != nil {
		t.Fatalf("NewContextProbe error = %v", err)
	}
	report, err := probe.Run(context.Background(), baseProbeRequest())
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if report.ReservationID != "rsv-ctx" {
		t.Fatalf("ReservationID = %q, want the guard's reservation id %q", report.ReservationID, "rsv-ctx")
	}
}

func TestContextProbe_SnippetTruncated(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	longMsg := strings.Repeat("日本語テキスト", 50)
	transport := &fakeProbeTransport{result: ProbeResult{HTTPStatus: 400, Message: longMsg}}
	guard := admittingContextGuard(t, now)
	probe, err := NewContextProbe(transport, guard, nil, clockAt(now))
	if err != nil {
		t.Fatalf("NewContextProbe error = %v", err)
	}
	report, err := probe.Run(context.Background(), baseProbeRequest())
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	runeLen := len([]rune(report.Snippet))
	if runeLen != ProbeSnippetMaxRunes {
		t.Fatalf("Snippet rune length = %d, want %d", runeLen, ProbeSnippetMaxRunes)
	}
}
