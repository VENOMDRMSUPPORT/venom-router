package routing

import (
	"errors"
	"fmt"
	"sort"
)

// PartKind is the closed message-part vocabulary Step 1 extracts
// modality from: text | image | document.
type PartKind string

const (
	PartText     PartKind = "text"
	PartImage    PartKind = "image"
	PartDocument PartKind = "document"
)

// ErrUnknownPartKind is returned by Normalize for a message part whose
// kind is outside the closed vocabulary (fail closed — an unrecognized
// modality is never silently ignored).
var ErrUnknownPartKind = errors.New("routing: unrecognized message part kind")

// ErrInvalidContextNeed is returned by Normalize when the request's
// estimated context need S is not positive — an unusable estimate must
// never pass through as if it were a real size.
var ErrInvalidContextNeed = errors.New("routing: context need must be positive")

// ErrInvalidExtension is returned by Normalize when the parsed venom
// extension carries an unknown capability identifier or an invalid
// thinking level (05 §1b: typed error, never silent coercion). It wraps
// the specific cause (ErrUnknownCapability or ErrUnknownThinkingLevel).
var ErrInvalidExtension = errors.New("routing: invalid venom extension")

// MessagePart is the shape-only view of one request message part; Step 1
// needs only its kind, never its content.
type MessagePart struct {
	Kind PartKind
}

// VenomExtension is the already-parsed optional venom request extension
// (05 §1b). HTTP/JSON parsing of the public surface is P5's job — this
// package receives typed values only.
type VenomExtension struct {
	// ThinkingBudget is the requested thinking level; nil means the
	// tier default applies (during thinking normalization, not here).
	ThinkingBudget *ThinkingLevel
	// RequiredCapabilities become hard Step-3 gates once unioned with
	// the inferred set.
	RequiredCapabilities []Capability
}

// Request is the typed Step-1 input: request-shape signals plus the
// caller-computed context estimate S (the estimator wiring is P5) and
// the optional parsed venom extension. No HTTP, no JSON, no content.
type Request struct {
	Parts                     []MessagePart
	Stream                    bool
	ToolsPresent              bool
	StructuredOutputRequested bool
	// EstimatedContextTokens is the request's context need S, computed
	// by the caller.
	EstimatedContextTokens int64
	Venom                  *VenomExtension
}

// Requirements is Step 1's output: the derived hard requirements a
// request places on routing (05 §2 Step 1).
type Requirements struct {
	TextModality     bool
	VisionModality   bool
	DocumentModality bool
	// Capabilities is the inferred set unioned with the explicit
	// required set — deduplicated and deterministically sorted.
	Capabilities []Capability
	// ContextTokens is the request's context need S, passed through.
	ContextTokens int64
	// RequestedThinking is the venom-requested level; nil means the tier
	// default applies later (thinking normalization) — no tier logic
	// runs here.
	RequestedThinking *ThinkingLevel
}

// Normalize derives a request's hard requirements (05 §2 Step 1):
// modality flags, the inferred-∪-explicit capability set, the context
// need S, and the requested thinking level. It fails closed on a
// non-positive S, an unknown part kind, and any invalid extension value.
func Normalize(req Request) (Requirements, error) {
	if req.EstimatedContextTokens <= 0 {
		return Requirements{}, fmt.Errorf("%w: S = %d", ErrInvalidContextNeed, req.EstimatedContextTokens)
	}

	out := Requirements{ContextTokens: req.EstimatedContextTokens}

	capabilities := make(map[Capability]struct{})
	for _, part := range req.Parts {
		switch part.Kind {
		case PartText:
			out.TextModality = true
		case PartImage:
			out.VisionModality = true
			capabilities[CapabilityVision] = struct{}{}
		case PartDocument:
			out.DocumentModality = true
		default:
			return Requirements{}, fmt.Errorf("%w: %q", ErrUnknownPartKind, part.Kind)
		}
	}
	if req.ToolsPresent {
		capabilities[CapabilityTools] = struct{}{}
	}
	if req.StructuredOutputRequested {
		capabilities[CapabilityStructuredOutput] = struct{}{}
	}
	if req.Stream {
		capabilities[CapabilityStreaming] = struct{}{}
	}

	if req.Venom != nil {
		for _, capability := range req.Venom.RequiredCapabilities {
			parsed, err := ParseCapability(string(capability))
			if err != nil {
				return Requirements{}, fmt.Errorf("%w: required_capabilities: %w", ErrInvalidExtension, err)
			}
			capabilities[parsed] = struct{}{}
		}
		if req.Venom.ThinkingBudget != nil {
			parsed, err := ParseThinkingLevel(string(*req.Venom.ThinkingBudget))
			if err != nil {
				return Requirements{}, fmt.Errorf("%w: thinking_budget: %w", ErrInvalidExtension, err)
			}
			out.RequestedThinking = &parsed
		}
	}

	if len(capabilities) > 0 {
		sorted := make([]Capability, 0, len(capabilities))
		for capability := range capabilities {
			sorted = append(sorted, capability)
		}
		sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
		out.Capabilities = sorted
	}

	return out, nil
}
