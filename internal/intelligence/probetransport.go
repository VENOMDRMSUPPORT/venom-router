package intelligence

import (
	"context"
	"errors"
	"fmt"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
)

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

// ProbeMessage is one chat-shaped message in a probe's fixed request body.
type ProbeMessage struct {
	Role    string
	Content string
	// Parts carries multimodal content (e.g. a vision fixture's image).
	// Content stays authoritative when Parts is empty, matching
	// execution.Message's own rule — every pre-existing construction of
	// ProbeMessage keeps its exact meaning.
	Parts []ProbePart
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
	// Tools are declared to the provider so a tools probe can actually
	// elicit a tool call. Empty leaves the wire body unchanged.
	Tools []ProbeTool
	// ResponseFormat constrains the reply's shape ("json_object"). Empty
	// leaves the wire body unchanged.
	ResponseFormat      string
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
