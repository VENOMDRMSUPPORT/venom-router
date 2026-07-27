package intelligence

import (
	"context"
	"errors"
	"fmt"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
)

// ProbeMessage is one chat-shaped message in a probe's fixed request body.
type ProbeMessage struct {
	Role    string
	Content string
}

// ProbeRequest is the transport-level request one probe attempt sends.
// AccountID/ProviderID/ProviderModelID/OfferingOperationID identify the
// offering-operation under test; RequestID/AttemptID are not part of this
// type — a real request/attempt identity is minted upstream (P3c-JOBS-001)
// once probes run as tracked jobs, which this package does not yet see, so
// ContextProbe/CapabilityProbe derive a local admission identity from
// OfferingOperationID and the injected clock instead (see contextprobe.go).
type ProbeRequest struct {
	AccountID           string
	ProviderID          string
	ProviderModelID     string
	OfferingOperationID string
	Operation           models.Operation
	Messages            []ProbeMessage
	MaxOutputTokens     int
	DeclaredInputTokens int
}

// ProbeTransportFailure is the closed vocabulary for a probe attempt that
// never reached a real HTTP response.
type ProbeTransportFailure string

const (
	TransportNone    ProbeTransportFailure = "none"
	TransportTimeout ProbeTransportFailure = "timeout"
	TransportNetwork ProbeTransportFailure = "network"
)

// ErrUnknownProbeTransportFailure is returned by ParseProbeTransportFailure
// for any value outside the exact three-value vocabulary.
var ErrUnknownProbeTransportFailure = errors.New("intelligence: unrecognized probe transport failure")

// ParseProbeTransportFailure fails closed on any value outside the exact
// three-value vocabulary.
func ParseProbeTransportFailure(s string) (ProbeTransportFailure, error) {
	switch ProbeTransportFailure(s) {
	case TransportNone, TransportTimeout, TransportNetwork:
		return ProbeTransportFailure(s), nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnknownProbeTransportFailure, s)
	}
}

// ProbeWitness is the closed vocabulary for what a 2xx probe response
// actually demonstrated — the fact CapabilityProbe checks against
// RequiredWitness so a chat-shaped success can never certify tools.
type ProbeWitness string

const (
	WitnessNone           ProbeWitness = "none"
	WitnessTextOnly       ProbeWitness = "text_only"
	WitnessToolCall       ProbeWitness = "tool_call"
	WitnessStructuredJSON ProbeWitness = "structured_json"
	WitnessVisionAnswer   ProbeWitness = "vision_answer"
)

// ErrUnknownProbeWitness is returned by ParseProbeWitness for any value
// outside the exact five-value vocabulary.
var ErrUnknownProbeWitness = errors.New("intelligence: unrecognized probe witness")

// ParseProbeWitness fails closed on any value outside the exact
// five-value vocabulary.
func ParseProbeWitness(s string) (ProbeWitness, error) {
	switch ProbeWitness(s) {
	case WitnessNone, WitnessTextOnly, WitnessToolCall, WitnessStructuredJSON, WitnessVisionAnswer:
		return ProbeWitness(s), nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnknownProbeWitness, s)
	}
}

// ProbeResult is one probe attempt's raw transport result. Message is the
// provider's raw rejection text — INPUT ONLY: it must never be stored,
// placed into Evidence, or returned to any caller un-redacted; both
// ContextProbe and CapabilityProbe read it only to extract a limit/witness
// and to build a redacted Snippet.
type ProbeResult struct {
	HTTPStatus             int
	ProviderCode           string
	Message                string
	StructuredContextLimit *int
	Witness                ProbeWitness
	Transport              ProbeTransportFailure
}

// ProbeTransport is the local port over which a probe attempt is actually
// sent. internal/execution's transport is wired to it by a later unit;
// this package never imports internal/execution directly (net/http is
// transitively forbidden here — see the staticgate layering test).
type ProbeTransport interface {
	Probe(ctx context.Context, req ProbeRequest) (ProbeResult, error)
}
