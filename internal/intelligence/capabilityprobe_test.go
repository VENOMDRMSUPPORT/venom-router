package intelligence

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
)

func admittingCapabilityGuard(t *testing.T, now time.Time) *ProbeGuard {
	t.Helper()
	g, err := NewProbeGuard(DefaultProbeSafetyPolicy(), &fakeProbeReserver{reservationID: "rsv-cap"}, &fakeSpendReader{}, &fakeInFlightReader{}, &fakeCooldownReader{}, clockAt(now))
	if err != nil {
		t.Fatalf("NewProbeGuard error = %v", err)
	}
	return g
}

func refusingCapabilityGuard(t *testing.T, now time.Time) *ProbeGuard {
	t.Helper()
	g, err := NewProbeGuard(DefaultProbeSafetyPolicy(), &fakeProbeReserver{t: t, trap: true}, &fakeSpendReader{}, &fakeInFlightReader{count: 1}, &fakeCooldownReader{}, clockAt(now))
	if err != nil {
		t.Fatalf("NewProbeGuard error = %v", err)
	}
	return g
}

func capabilityOperations() []models.Operation {
	return []models.Operation{models.OperationTools, models.OperationStructuredOutput, models.OperationVision}
}

func TestCapabilityProbe_SupportedPerOperation(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	for _, op := range capabilityOperations() {
		t.Run(string(op), func(t *testing.T) {
			required, err := RequiredWitness(op)
			if err != nil {
				t.Fatalf("RequiredWitness error = %v", err)
			}
			transport := &fakeProbeTransport{result: ProbeResult{HTTPStatus: 200, Witness: required}}
			guard := admittingCapabilityGuard(t, now)
			probe, err := NewCapabilityProbe(transport, guard, clockAt(now))
			if err != nil {
				t.Fatalf("NewCapabilityProbe error = %v", err)
			}
			req := baseProbeRequest()
			req.Operation = op
			report, err := probe.Run(context.Background(), req)
			if err != nil {
				t.Fatalf("Run error = %v", err)
			}
			if report.Outcome.Truth != models.TruthSupported || !report.Outcome.Definitive {
				t.Fatalf("Truth=%q Definitive=%v, want supported/true", report.Outcome.Truth, report.Outcome.Definitive)
			}
			if len(report.Evidence) != 1 {
				t.Fatalf("Evidence len = %d, want 1", len(report.Evidence))
			}
			if report.Evidence[0].Field != CapabilityField(op) {
				t.Errorf("Field = %q, want %q", report.Evidence[0].Field, CapabilityField(op))
			}
			if report.Evidence[0].Value != true {
				t.Errorf("Value = %v, want true", report.Evidence[0].Value)
			}
		})
	}
}

func TestCapabilityProbe_ChatSuccessDoesNotCertifyTools(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	transport := &fakeProbeTransport{result: ProbeResult{HTTPStatus: 200, Witness: WitnessTextOnly}}
	guard := admittingCapabilityGuard(t, now)
	probe, err := NewCapabilityProbe(transport, guard, clockAt(now))
	if err != nil {
		t.Fatalf("NewCapabilityProbe error = %v", err)
	}
	req := baseProbeRequest()
	req.Operation = models.OperationTools
	report, err := probe.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if report.Outcome.Truth != models.TruthUnknown {
		t.Errorf("Truth = %q, want unknown", report.Outcome.Truth)
	}
	if report.Outcome.Execution != ProbeInconclusive {
		t.Errorf("Execution = %q, want inconclusive", report.Outcome.Execution)
	}
	if len(report.Evidence) != 0 {
		t.Errorf("Evidence len = %d, want 0", len(report.Evidence))
	}
}

func TestCapabilityProbe_SemanticRejectionIsUnsupported(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)

	t.Run("recognized code yields unsupported", func(t *testing.T) {
		transport := &fakeProbeTransport{result: ProbeResult{HTTPStatus: 400, ProviderCode: "tool_use_not_supported", Message: "tools are not supported for this model"}}
		guard := admittingCapabilityGuard(t, now)
		probe, err := NewCapabilityProbe(transport, guard, clockAt(now))
		if err != nil {
			t.Fatalf("NewCapabilityProbe error = %v", err)
		}
		req := baseProbeRequest()
		req.Operation = models.OperationTools
		report, err := probe.Run(context.Background(), req)
		if err != nil {
			t.Fatalf("Run error = %v", err)
		}
		if report.Outcome.Truth != models.TruthUnsupported {
			t.Fatalf("Truth = %q, want unsupported", report.Outcome.Truth)
		}
		if len(report.Evidence) != 1 || !report.Evidence[0].ProvenNegative {
			t.Fatalf("Evidence = %+v, want one ProvenNegative entry", report.Evidence)
		}
		if report.Evidence[0].Value != false {
			t.Errorf("Value = %v, want false", report.Evidence[0].Value)
		}
	})

	t.Run("bare 400 with no recognized code yields inconclusive", func(t *testing.T) {
		transport := &fakeProbeTransport{result: ProbeResult{HTTPStatus: 400, ProviderCode: "bad_request", Message: "malformed request"}}
		guard := admittingCapabilityGuard(t, now)
		probe, err := NewCapabilityProbe(transport, guard, clockAt(now))
		if err != nil {
			t.Fatalf("NewCapabilityProbe error = %v", err)
		}
		req := baseProbeRequest()
		req.Operation = models.OperationTools
		report, err := probe.Run(context.Background(), req)
		if err != nil {
			t.Fatalf("Run error = %v", err)
		}
		if report.Outcome.Truth != models.TruthUnknown {
			t.Fatalf("Truth = %q, want unknown", report.Outcome.Truth)
		}
		if len(report.Evidence) != 0 {
			t.Fatalf("Evidence len = %d, want 0", len(report.Evidence))
		}
	})
}

func TestCapabilityProbe_InfraFailureNeverUnsupported(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	rows := []ProbeResult{
		{HTTPStatus: 429},
		{Transport: TransportTimeout},
		{HTTPStatus: 500},
		{HTTPStatus: 401},
		{HTTPStatus: 403},
	}
	for _, res := range rows {
		t.Run(string(res.Transport)+"/status", func(t *testing.T) {
			transport := &fakeProbeTransport{result: res}
			guard := admittingCapabilityGuard(t, now)
			probe, err := NewCapabilityProbe(transport, guard, clockAt(now))
			if err != nil {
				t.Fatalf("NewCapabilityProbe error = %v", err)
			}
			req := baseProbeRequest()
			req.Operation = models.OperationTools
			report, err := probe.Run(context.Background(), req)
			if err != nil {
				t.Fatalf("Run error = %v", err)
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

func TestCapabilityProbe_EvidenceIsPerOperationOnly(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	// Probe vision (not tools) deliberately: a regression that hardcodes
	// the evidence field to tools would otherwise slip past unnoticed if
	// this test only ever exercised the tools operation.
	transport := &fakeProbeTransport{result: ProbeResult{HTTPStatus: 200, Witness: WitnessVisionAnswer}}
	guard := admittingCapabilityGuard(t, now)
	probe, err := NewCapabilityProbe(transport, guard, clockAt(now))
	if err != nil {
		t.Fatalf("NewCapabilityProbe error = %v", err)
	}
	req := baseProbeRequest()
	req.Operation = models.OperationVision
	report, err := probe.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if len(report.Evidence) != 1 {
		t.Fatalf("Evidence len = %d, want 1", len(report.Evidence))
	}
	field := report.Evidence[0].Field
	if field != CapabilityField(models.OperationVision) {
		t.Errorf("Field = %q, want %q", field, CapabilityField(models.OperationVision))
	}
	if field == CapabilityField(models.OperationTools) || field == CapabilityField(models.OperationStructuredOutput) {
		t.Errorf("Field leaked into another operation: %q", field)
	}
}

// TestRequiredWitness_MappingIsLiteral pins the operation -> witness mapping
// to literal expected values. Every other test in this file derives the
// transport's witness from RequiredWitness itself, so the two sides move
// together and a swapped mapping stays invisible: a tools probe could then
// certify on a structured-JSON response, which is the very "chat success
// does not certify tools" rule (04 §5) inverted. The cross-witness rows
// below prove the mismatch direction too.
func TestRequiredWitness_MappingIsLiteral(t *testing.T) {
	want := map[models.Operation]ProbeWitness{
		models.OperationTools:            WitnessToolCall,
		models.OperationStructuredOutput: WitnessStructuredJSON,
		models.OperationVision:           WitnessVisionAnswer,
	}
	for op, expected := range want {
		t.Run(string(op), func(t *testing.T) {
			got, err := RequiredWitness(op)
			if err != nil {
				t.Fatalf("RequiredWitness(%q) error = %v", op, err)
			}
			if got != expected {
				t.Fatalf("RequiredWitness(%q) = %q, want %q", op, got, expected)
			}
		})
	}

	for _, op := range models.Operations() {
		if _, ok := want[op]; ok {
			continue
		}
		t.Run("unsupported/"+string(op), func(t *testing.T) {
			if _, err := RequiredWitness(op); !errors.Is(err, ErrNoCapabilityFixture) {
				t.Fatalf("RequiredWitness(%q) error = %v, want ErrNoCapabilityFixture", op, err)
			}
		})
	}
}

// TestCapabilityProbe_ForeignWitnessNeverCertifies is the behavioural half of
// the mapping invariant: a 2xx carrying another operation's witness is
// inconclusive, never supported.
func TestCapabilityProbe_ForeignWitnessNeverCertifies(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	rows := []struct {
		op      models.Operation
		witness ProbeWitness
	}{
		{models.OperationTools, WitnessStructuredJSON},
		{models.OperationTools, WitnessVisionAnswer},
		{models.OperationStructuredOutput, WitnessToolCall},
		{models.OperationVision, WitnessToolCall},
	}
	for _, row := range rows {
		t.Run(string(row.op)+"/"+string(row.witness), func(t *testing.T) {
			transport := &fakeProbeTransport{result: ProbeResult{HTTPStatus: 200, Witness: row.witness}}
			probe, err := NewCapabilityProbe(transport, admittingCapabilityGuard(t, now), clockAt(now))
			if err != nil {
				t.Fatalf("NewCapabilityProbe error = %v", err)
			}
			req := baseProbeRequest()
			req.Operation = row.op
			report, err := probe.Run(context.Background(), req)
			if err != nil {
				t.Fatalf("Run error = %v", err)
			}
			if report.Outcome.Truth != models.TruthUnknown || report.Outcome.Execution != ProbeInconclusive {
				t.Fatalf("Truth=%q Execution=%q, want unknown/inconclusive", report.Outcome.Truth, report.Outcome.Execution)
			}
			if len(report.Evidence) != 0 {
				t.Fatalf("Evidence len = %d, want 0", len(report.Evidence))
			}
			if report.ReservationID != "rsv-cap" {
				t.Fatalf("ReservationID = %q, want the guard's reservation id", report.ReservationID)
			}
		})
	}
}

func TestCapabilityProbe_ProvenNegativeSurvivesResolve(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	field := CapabilityField(models.OperationTools)
	scope := Scope{AccountID: "acct-1", ProviderModelID: "model-1"}

	// The negative evidence comes from an actual Run() call — not a
	// hand-built Evidence — so a regression that drops ProvenNegative
	// inside Run is caught here too, not only by the semantic-rejection
	// test.
	transport := &fakeProbeTransport{result: ProbeResult{HTTPStatus: 400, ProviderCode: "tool_use_not_supported"}}
	guard := admittingCapabilityGuard(t, now)
	probe, err := NewCapabilityProbe(transport, guard, clockAt(now))
	if err != nil {
		t.Fatalf("NewCapabilityProbe error = %v", err)
	}
	req := baseProbeRequest()
	req.Operation = models.OperationTools
	report, err := probe.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if len(report.Evidence) != 1 || !report.Evidence[0].ProvenNegative {
		t.Fatalf("Evidence = %+v, want one ProvenNegative entry", report.Evidence)
	}
	negative := report.Evidence[0]

	externalPositive := Evidence{
		Field: field, Scope: scope, Source: SourceExternalRegistry, Verification: VerificationDeclared,
		Confidence: 0.5, ObservedAt: now.Add(time.Hour), Value: true,
	}

	res := Resolve(field, []Evidence{negative, externalPositive}, now.Add(2*time.Hour))
	if res.Kind != ResolutionKnown || res.Value != false {
		t.Fatalf("Resolve = %+v, want known/false (proven negative should survive a weaker external claim)", res)
	}

	ownerOverride := Evidence{
		Field: field, Scope: scope, Source: SourceOwnerOverride, Verification: VerificationDeclared,
		Confidence: 1.0, ObservedAt: now.Add(time.Hour), Value: true,
	}
	res2 := Resolve(field, []Evidence{negative, ownerOverride}, now.Add(2*time.Hour))
	if res2.Kind != ResolutionKnown || res2.Value != true {
		t.Fatalf("Resolve with owner override = %+v, want known/true (owner override beats the proven negative)", res2)
	}
}

func TestCapabilityFixture_ClosedSet(t *testing.T) {
	supported := map[models.Operation]bool{
		models.OperationTools:            true,
		models.OperationStructuredOutput: true,
		models.OperationVision:           true,
	}
	for _, op := range models.Operations() {
		t.Run(string(op), func(t *testing.T) {
			messages, maxOutput, err := CapabilityFixture(op)
			if supported[op] {
				if err != nil {
					t.Fatalf("CapabilityFixture(%q) error = %v, want a fixture", op, err)
				}
				if len(messages) == 0 || maxOutput <= 0 {
					t.Fatalf("CapabilityFixture(%q) = (%v, %d), want a non-empty fixture", op, messages, maxOutput)
				}
			} else {
				if !errors.Is(err, ErrNoCapabilityFixture) {
					t.Fatalf("CapabilityFixture(%q) error = %v, want ErrNoCapabilityFixture", op, err)
				}
			}
		})
	}
}

func TestCapabilityProbe_RefusalNeverCallsTransport(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	trap := &fakeProbeTransport{t: t, trap: true}
	guard := refusingCapabilityGuard(t, now)
	probe, err := NewCapabilityProbe(trap, guard, clockAt(now))
	if err != nil {
		t.Fatalf("NewCapabilityProbe error = %v", err)
	}
	req := baseProbeRequest()
	req.Operation = models.OperationTools
	_, err = probe.Run(context.Background(), req)
	if reason, ok := RefusalOf(err); !ok || reason != RefusalConcurrency {
		t.Fatalf("refusal = %v (ok=%v), want probe_concurrency", reason, ok)
	}
}

func TestCapabilityProbe_SnippetIsRedacted(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	const canary = "sk-canary-DEADBEEF0123456789"
	transport := &fakeProbeTransport{result: ProbeResult{
		HTTPStatus: 400,
		Message:    "Authorization: Bearer " + canary,
	}}
	guard := admittingCapabilityGuard(t, now)
	probe, err := NewCapabilityProbe(transport, guard, clockAt(now))
	if err != nil {
		t.Fatalf("NewCapabilityProbe error = %v", err)
	}
	req := baseProbeRequest()
	req.Operation = models.OperationTools
	report, err := probe.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if strings.Contains(report.Snippet, canary) {
		t.Fatalf("Snippet contains the canary credential: %q", report.Snippet)
	}
}
