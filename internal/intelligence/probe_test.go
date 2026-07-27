package intelligence

import (
	"errors"
	"testing"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
)

func TestClassifyProbeSignal_Table(t *testing.T) {
	tests := []struct {
		kind       ProbeSignalKind
		execution  ProbeExecution
		truth      models.CapabilityTruth
		definitive bool
		reschedule bool
		reason     ProbeReasonCode
	}{
		{SignalCapabilityResponse, ProbeSucceeded, models.TruthSupported, true, false, ReasonCapabilityConfirmed},
		{SignalSemanticRejection, ProbeSucceeded, models.TruthUnsupported, true, false, ReasonCapabilityAbsent},
		{SignalRateLimited, ProbeRetryableFailure, models.TruthUnknown, false, true, ReasonRateLimited},
		{SignalTimeout, ProbeRetryableFailure, models.TruthUnknown, false, true, ReasonTimeout},
		{SignalServerError, ProbeRetryableFailure, models.TruthUnknown, false, true, ReasonServerError},
		{SignalNetworkError, ProbeRetryableFailure, models.TruthUnknown, false, true, ReasonNetworkError},
		{SignalUnauthorized, ProbeTerminalFailure, models.TruthUnknown, false, false, ReasonCredentialBlocked},
		{SignalForbidden, ProbeTerminalFailure, models.TruthUnknown, false, false, ReasonCredentialBlocked},
		{SignalMalformedRequest, ProbeInconclusive, models.TruthUnknown, false, false, ReasonMalformedProbe},
	}

	if len(tests) != len(probeSignalKindSet) {
		t.Fatalf("test table has %d rows, want %d (one per signal kind)", len(tests), len(probeSignalKindSet))
	}

	for _, tt := range tests {
		t.Run(string(tt.kind), func(t *testing.T) {
			out, err := ClassifyProbeSignal(tt.kind)
			if err != nil {
				t.Fatalf("ClassifyProbeSignal(%q) error = %v", tt.kind, err)
			}
			if out.Execution != tt.execution {
				t.Errorf("Execution = %q, want %q", out.Execution, tt.execution)
			}
			if out.Truth != tt.truth {
				t.Errorf("Truth = %q, want %q", out.Truth, tt.truth)
			}
			if out.Definitive != tt.definitive {
				t.Errorf("Definitive = %v, want %v", out.Definitive, tt.definitive)
			}
			if out.Reschedule != tt.reschedule {
				t.Errorf("Reschedule = %v, want %v", out.Reschedule, tt.reschedule)
			}
			if out.Reason != tt.reason {
				t.Errorf("Reason = %q, want %q", out.Reason, tt.reason)
			}
		})
	}
}

func TestClassifyProbeSignal_InfraFailureNeverFlipsCapability(t *testing.T) {
	infraKinds := []ProbeSignalKind{
		SignalRateLimited, SignalTimeout, SignalServerError, SignalNetworkError,
		SignalUnauthorized, SignalForbidden, SignalMalformedRequest,
	}
	for _, kind := range infraKinds {
		t.Run(string(kind), func(t *testing.T) {
			out, err := ClassifyProbeSignal(kind)
			if err != nil {
				t.Fatalf("ClassifyProbeSignal(%q) error = %v", kind, err)
			}
			if out.Truth != models.TruthUnknown {
				t.Errorf("Truth = %q, want %q", out.Truth, models.TruthUnknown)
			}
			if out.Definitive {
				t.Errorf("Definitive = true, want false")
			}
		})
	}

	// Positive control: the two capability-bearing signals DO resolve truth.
	supported, err := ClassifyProbeSignal(SignalCapabilityResponse)
	if err != nil {
		t.Fatalf("ClassifyProbeSignal(capability_response) error = %v", err)
	}
	if supported.Truth != models.TruthSupported || !supported.Definitive {
		t.Errorf("capability_response: Truth=%q Definitive=%v, want TruthSupported/true", supported.Truth, supported.Definitive)
	}

	unsupported, err := ClassifyProbeSignal(SignalSemanticRejection)
	if err != nil {
		t.Fatalf("ClassifyProbeSignal(semantic_rejection) error = %v", err)
	}
	if unsupported.Truth != models.TruthUnsupported || !unsupported.Definitive {
		t.Errorf("semantic_rejection: Truth=%q Definitive=%v, want TruthUnsupported/true", unsupported.Truth, unsupported.Definitive)
	}
}

func TestClassifyProbeSignal_DefinitiveIffTruthResolved(t *testing.T) {
	for _, kind := range probeSignalKindSet {
		t.Run(string(kind), func(t *testing.T) {
			out, err := ClassifyProbeSignal(kind)
			if err != nil {
				t.Fatalf("ClassifyProbeSignal(%q) error = %v", kind, err)
			}
			resolved := out.Truth == models.TruthSupported || out.Truth == models.TruthUnsupported
			if out.Definitive != resolved {
				t.Errorf("Definitive = %v, but Truth=%q resolved=%v — must match", out.Definitive, out.Truth, resolved)
			}
		})
	}
}

func TestClassifyProbeSignal_UnknownKindFailsClosed(t *testing.T) {
	for _, bad := range []ProbeSignalKind{"", "rejected", "capability_response ", "bogus_signal"} {
		t.Run(string(bad), func(t *testing.T) {
			out, err := ClassifyProbeSignal(bad)
			if !errors.Is(err, ErrUnknownProbeSignalKind) {
				t.Fatalf("err = %v, want ErrUnknownProbeSignalKind", err)
			}
			if out != (ProbeOutcome{}) {
				t.Errorf("out = %+v, want zero value", out)
			}
		})
	}
}

func TestParseProbeExecution_ClosedVocabulary(t *testing.T) {
	for _, v := range probeExecutionSet {
		t.Run(string(v)+"/valid", func(t *testing.T) {
			got, err := ParseProbeExecution(string(v))
			if err != nil {
				t.Fatalf("ParseProbeExecution(%q) error = %v", v, err)
			}
			if got != v {
				t.Errorf("got %q, want %q", got, v)
			}
		})
	}

	for _, bad := range []string{"", "rejected", "succeeded ", "probing"} {
		t.Run(bad+"/invalid", func(t *testing.T) {
			_, err := ParseProbeExecution(bad)
			if !errors.Is(err, ErrUnknownProbeExecution) {
				t.Fatalf("err = %v, want ErrUnknownProbeExecution", err)
			}
		})
	}

	if len(ProbeExecutions()) != 6 {
		t.Fatalf("len(ProbeExecutions()) = %d, want 6", len(ProbeExecutions()))
	}

	first := ProbeExecutions()
	first[0] = "mutated"
	second := ProbeExecutions()
	if second[0] == "mutated" {
		t.Fatal("mutating a returned slice affected a later call — ProbeExecutions must return a defensive copy")
	}
}

func TestParseProbeSignalKind_ClosedVocabulary(t *testing.T) {
	for _, v := range probeSignalKindSet {
		t.Run(string(v)+"/valid", func(t *testing.T) {
			got, err := ParseProbeSignalKind(string(v))
			if err != nil {
				t.Fatalf("ParseProbeSignalKind(%q) error = %v", v, err)
			}
			if got != v {
				t.Errorf("got %q, want %q", got, v)
			}
		})
	}

	for _, bad := range []string{"", "rejected", "succeeded ", "throttled"} {
		t.Run(bad+"/invalid", func(t *testing.T) {
			_, err := ParseProbeSignalKind(bad)
			if !errors.Is(err, ErrUnknownProbeSignalKind) {
				t.Fatalf("err = %v, want ErrUnknownProbeSignalKind", err)
			}
		})
	}
}
