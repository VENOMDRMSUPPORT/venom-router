package routing

import (
	"errors"
	"testing"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
)

// TestParseCapability_KnownValuesRoundTrip proves the closed six-value
// §1b request-capability vocabulary parses back to itself.
func TestParseCapability_KnownValuesRoundTrip(t *testing.T) {
	known := []Capability{
		CapabilityChat,
		CapabilityStreaming,
		CapabilityTools,
		CapabilityStructuredOutput,
		CapabilityVision,
		CapabilityReasoning,
	}
	for _, want := range known {
		got, err := ParseCapability(string(want))
		if err != nil {
			t.Fatalf("ParseCapability(%q) error = %v, want nil", want, err)
		}
		if got != want {
			t.Fatalf("ParseCapability(%q) = %q, want %q", want, got, want)
		}
	}
}

// TestParseCapability_RejectsNonRequestable proves fail-closed parsing —
// including the two models.Operation values that are real certification
// operations but are NOT requestable capabilities (05 §1b):
// image_generation (future scope) and context_window (a gate input, not
// a capability).
func TestParseCapability_RejectsNonRequestable(t *testing.T) {
	for _, bad := range []string{"", "image_generation", "context_window", "Vision", "thinking"} {
		_, err := ParseCapability(bad)
		if !errors.Is(err, ErrUnknownCapability) {
			t.Fatalf("ParseCapability(%q) error = %v, want ErrUnknownCapability", bad, err)
		}
	}
}

// TestCapability_OperationMapping proves the explicit capability →
// models.Operation mapping: the five certification-backed capabilities
// map to their identically named operations; reasoning has no operation
// mapping (its certification-side truth arrives with the ROUTE-004
// wiring).
func TestCapability_OperationMapping(t *testing.T) {
	mapped := map[Capability]models.Operation{
		CapabilityChat:             models.OperationChat,
		CapabilityStreaming:        models.OperationStreaming,
		CapabilityTools:            models.OperationTools,
		CapabilityStructuredOutput: models.OperationStructuredOutput,
		CapabilityVision:           models.OperationVision,
	}
	for capability, wantOp := range mapped {
		op, ok := capability.OperationMapping()
		if !ok {
			t.Fatalf("%q.OperationMapping() ok = false, want true", capability)
		}
		if op != wantOp {
			t.Fatalf("%q.OperationMapping() = %q, want %q", capability, op, wantOp)
		}
	}

	if op, ok := CapabilityReasoning.OperationMapping(); ok {
		t.Fatalf("reasoning.OperationMapping() = %q, true; want no mapping (reasoning is not a models.Operation)", op)
	}
	if op, ok := Capability("bogus").OperationMapping(); ok {
		t.Fatalf("bogus.OperationMapping() = %q, true; want no mapping (fail closed)", op)
	}
}
