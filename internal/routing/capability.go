package routing

import (
	"errors"
	"fmt"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
)

// Capability is routing's request-facing capability vocabulary — the
// CLOSED six-identifier set of 05 §1b: what a client may require via
// venom.required_capabilities and what Step 1 infers from the request
// shape. It is deliberately NOT models.Operation: image_generation
// (future scope, 05 §9) and context_window (a hard-gate input, not a
// capability) are certification operations but are never requestable.
type Capability string

const (
	CapabilityChat             Capability = "chat"
	CapabilityStreaming        Capability = "streaming"
	CapabilityTools            Capability = "tools"
	CapabilityStructuredOutput Capability = "structured_output"
	CapabilityVision           Capability = "vision"
	CapabilityReasoning        Capability = "reasoning"
)

// ErrUnknownCapability is returned by ParseCapability for any value
// outside the six-identifier §1b vocabulary — including image_generation
// and context_window.
var ErrUnknownCapability = errors.New("routing: unrecognized capability identifier")

// capabilityToOperation is the explicit mapping onto the certification
// vocabulary (02 §3): the five capabilities that exist there map to their
// identically named operations. reasoning is deliberately absent — it is
// certified through its own capability truth, and that certification-side
// wiring arrives with ROUTE-004, not through models.Operation.
var capabilityToOperation = map[Capability]models.Operation{
	CapabilityChat:             models.OperationChat,
	CapabilityStreaming:        models.OperationStreaming,
	CapabilityTools:            models.OperationTools,
	CapabilityStructuredOutput: models.OperationStructuredOutput,
	CapabilityVision:           models.OperationVision,
}

// ParseCapability fails closed on any value outside the exact six-value
// vocabulary — no case folding, no trimming, no operation names that are
// not requestable capabilities.
func ParseCapability(s string) (Capability, error) {
	switch Capability(s) {
	case CapabilityChat, CapabilityStreaming, CapabilityTools,
		CapabilityStructuredOutput, CapabilityVision, CapabilityReasoning:
		return Capability(s), nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnknownCapability, s)
	}
}

// OperationMapping returns the models.Operation this capability certifies
// against, and false when there is none (reasoning, or a value outside
// the vocabulary — fail closed).
func (c Capability) OperationMapping() (models.Operation, bool) {
	op, ok := capabilityToOperation[c]
	return op, ok
}
