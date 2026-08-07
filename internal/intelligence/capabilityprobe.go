package intelligence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
)

// CapabilityProbeDeclaredInputTokens is the fixed, conservative declared
// input-token figure used for every capability probe's cost estimate.
// These fixtures are tiny and fixed by construction, so this is an
// explicit constant, never derived from a tokenizer.
const CapabilityProbeDeclaredInputTokens = 50

// capabilityProbeMaxOutputTokens bounds every capability fixture's
// response — enough to see a tool call / JSON object / short answer,
// never a real chat-sized reply.
const capabilityProbeMaxOutputTokens = 16

// VisionFixtureColour is the solid colour of visionFixtureImageBase64's
// image, named in lower case exactly as a model's plain-text answer would
// name it. It is exported so the probe adapter (internal/httpapi) can check
// a vision response's content for this word, case-insensitively, to
// classify intelligence.WitnessVisionAnswer — a witness that stays
// reachable only because the adapter is told what colour to expect; there
// is no structural way to tell "the model answered the vision question"
// apart from any other prose otherwise.
const VisionFixtureColour = "magenta"

// visionFixtureImageBase64 is a fixed, tiny (4x4 pixel), inline, fully
// opaque SOLID-COLOUR PNG — never a network URL — used by the vision
// fixture. It replaces a prior 1x1 TRANSPARENT pixel: a transparent pixel
// has no colour to name, so even a working vision path could never
// produce the expected answer. This one is solid VisionFixtureColour
// (magenta, RGB 255/0/255), verified by round-tripping through image/png's
// own decoder before being committed here (see task-1-report.md).
const visionFixtureImageBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAQAAAAECAIAAAAmkwkpAAAAGElEQVR4nGL5z/CfAQaYGJAAbg4gAAD//2U8AgmWdSNJAAAAAElFTkSuQmCC"

// StructuredOutputFixtureField is the exact field name the structured-
// output fixture's own prompt asks the model to return
// (VisionFixtureColour's own pattern, applied here — whole-branch review,
// FIX 7). It is exported so the probe adapter (internal/httpapi) can check
// a structured-output response's PARSED content for this specific key,
// case-sensitively, to classify intelligence.WitnessStructuredJSON — a
// witness that stays honest only because the adapter checks for the exact
// field this fixture asked for, not merely "the content parses as any JSON
// object at all" (04 §4.2: without this, ResponseFormat: "json_object"
// alone — many providers enforce that wire-level contract regardless of
// whether the model understood the prompt — would make the witness
// self-fulfilling: any object the provider's own json_object mode produces
// would certify the capability, proving nothing about whether the MODEL
// actually followed a structured-output instruction).
const StructuredOutputFixtureField = "ok"

// toolsFixtureAddTool is the tools fixture's declared function tool — a
// minimal, real JSON Schema for a two-argument add function. A tools probe
// that asks the model to "use the add tool if one is available" while
// declaring no tool can never produce a tool call; this is the tool that
// makes the fixture's own prompt possible to satisfy.
var toolsFixtureAddTool = ProbeTool{
	Name:           "add",
	Description:    "Adds two numbers and returns their sum.",
	ParametersJSON: `{"type":"object","properties":{"a":{"type":"number"},"b":{"type":"number"}},"required":["a","b"]}`,
}

// ErrNoCapabilityFixture is returned by CapabilityFixture and
// RequiredWitness for any operation outside the exactly three this batch
// certifies (tools, structured_output, vision).
var ErrNoCapabilityFixture = errors.New("intelligence: no capability fixture for this operation")

// CapabilityFixture returns the tiny, fixed, deterministic request body
// (messages, declared tools, and response-format directive) and
// MaxOutputTokens used to certify op. Only tools, structured_output, and
// vision are certified in this batch (04 §5 names exactly these three
// alongside chat/streaming/context_window as "recognized operations";
// chat/streaming/context_window are certified elsewhere, and
// image_generation is reserved future scope) — every other
// models.Operation returns ErrNoCapabilityFixture.
//
// The tools fixture declares toolsFixtureAddTool alongside its prompt — a
// tools probe that asks for a tool it never declares can never produce a
// tool call. The vision fixture carries the image as a real ProbePart
// (never a data URI pasted into text) so a transport can actually forward
// it as an image. The structured-output fixture sets responseFormat to
// "json_object" alongside its prose.
func CapabilityFixture(op models.Operation) (messages []ProbeMessage, tools []ProbeTool, responseFormat string, maxOutputTokens int, err error) {
	switch op {
	case models.OperationTools:
		return []ProbeMessage{
			{Role: "user", Content: "What is 2+2? Use the add tool if one is available."},
		}, []ProbeTool{toolsFixtureAddTool}, "", capabilityProbeMaxOutputTokens, nil
	case models.OperationStructuredOutput:
		return []ProbeMessage{
			{Role: "user", Content: fmt.Sprintf(`Return a JSON object with exactly one field %q set to true.`, StructuredOutputFixtureField)},
		}, nil, "json_object", capabilityProbeMaxOutputTokens, nil
	case models.OperationVision:
		return []ProbeMessage{
			{
				Role: "user",
				Parts: []ProbePart{
					{Kind: ProbePartText, Text: "What colour is this image? Answer with one word."},
					{Kind: ProbePartImage, ImageBase64: visionFixtureImageBase64, MediaType: "image/png"},
				},
			},
		}, nil, "", capabilityProbeMaxOutputTokens, nil
	default:
		return nil, nil, "", 0, fmt.Errorf("%w: %q", ErrNoCapabilityFixture, op)
	}
}

// RequiredWitness returns the ProbeWitness a genuine 2xx response must
// carry to certify op — the crown invariant that a chat-shaped success
// (WitnessTextOnly) can never certify tools/structured_output/vision.
func RequiredWitness(op models.Operation) (ProbeWitness, error) {
	switch op {
	case models.OperationTools:
		return WitnessToolCall, nil
	case models.OperationStructuredOutput:
		return WitnessStructuredJSON, nil
	case models.OperationVision:
		return WitnessVisionAnswer, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrNoCapabilityFixture, op)
	}
}

// reliableUnsupportedCodes is the fixed, documented set of provider codes
// treated as a reliable semantic rejection proving a capability absent. A
// generic 4xx with no such code is never inferred as unsupported — it is
// inconclusive instead (04 §5: "chat success does not certify tools",
// and the symmetric rule that a bare rejection never proves absence
// either).
var reliableUnsupportedCodes = map[string]bool{
	"tool_use_not_supported": true,
	"unsupported_capability": true,
	"model_not_supported":    true,
}

// classifyCapabilityProbeResult maps res onto a ProbeSignalKind for one
// capability probe (04 §2/§5):
//   - a transport failure (timeout/network) maps directly;
//   - 429/401/403 map directly (infrastructure, never a capability
//     judgment);
//   - a 2xx certifies the capability ONLY when its Witness matches
//     required exactly — any other witness (notably WitnessTextOnly) is
//     malformed_request (inconclusive): a chat-shaped success never
//     certifies tools;
//   - a 4xx whose ProviderCode is in reliableUnsupportedCodes is a
//     semantic_rejection (unsupported); every other 4xx is
//     malformed_request (inconclusive) — a bare status code never proves
//     "unsupported" on its own.
func classifyCapabilityProbeResult(res ProbeResult, required ProbeWitness) ProbeSignalKind {
	switch res.Transport {
	case TransportTimeout:
		return SignalTimeout
	case TransportNetwork:
		return SignalNetworkError
	}

	switch {
	case res.HTTPStatus == 429:
		return SignalRateLimited
	case res.HTTPStatus >= 500:
		return SignalServerError
	case res.HTTPStatus == 401:
		return SignalUnauthorized
	case res.HTTPStatus == 403:
		return SignalForbidden
	case res.HTTPStatus >= 200 && res.HTTPStatus < 300:
		if res.Witness == required {
			return SignalCapabilityResponse
		}
		return SignalMalformedRequest
	case res.HTTPStatus >= 400:
		if reliableUnsupportedCodes[res.ProviderCode] {
			return SignalSemanticRejection
		}
		return SignalMalformedRequest
	default:
		return SignalMalformedRequest
	}
}

// ErrNilCapabilityProbeDependency is returned by NewCapabilityProbe when
// transport or guard is nil.
var ErrNilCapabilityProbeDependency = errors.New("intelligence: capability probe requires a transport and a guard")

// CapabilityProbe runs the per-operation capability probes (04 §2/§5):
// tiny fixed fixtures exercising tools/structured_output/vision on one
// specific offering-operation, admitted through a ProbeGuard first.
type CapabilityProbe struct {
	transport ProbeTransport
	guard     *ProbeGuard
	now       func() time.Time
}

// NewCapabilityProbe builds a CapabilityProbe. transport and guard are
// required. now defaults to time.Now when nil.
func NewCapabilityProbe(transport ProbeTransport, guard *ProbeGuard, now func() time.Time) (*CapabilityProbe, error) {
	if transport == nil || guard == nil {
		return nil, ErrNilCapabilityProbeDependency
	}
	if now == nil {
		now = time.Now
	}
	return &CapabilityProbe{transport: transport, guard: guard, now: now}, nil
}

// CapabilityProbeReport is Run's result: the operation certified, the
// probe-execution/capability-truth outcome, the Evidence to merge into
// Resolve (empty unless the outcome is definitive), the redacted message
// snippet, and the reservation obtained for this attempt.
type CapabilityProbeReport struct {
	Operation     models.Operation
	Outcome       ProbeOutcome
	Evidence      []Evidence
	Snippet       string
	ReservationID string
}

// Run executes one capability-probe attempt for req.Operation (04 §2/§5):
// look up the fixed fixture (an unsupported operation is a typed error
// before anything else runs), admit through the guard
// (Class=ProbeStandard), call the transport exactly once, classify the
// result, and emit exactly one Evidence entry — for req.Operation only —
// when the outcome is definitive: supported carries Value=true, and
// unsupported carries Value=false with ProvenNegative=true so
// precedence.go's proven-negative rule keeps it until a strictly higher
// verification revalidates it.
func (p *CapabilityProbe) Run(ctx context.Context, req ProbeRequest) (CapabilityProbeReport, error) {
	messages, tools, responseFormat, maxOutput, err := CapabilityFixture(req.Operation)
	if err != nil {
		return CapabilityProbeReport{}, err
	}
	required, err := RequiredWitness(req.Operation)
	if err != nil {
		return CapabilityProbeReport{}, err
	}

	req.Messages = messages
	req.Tools = tools
	req.ResponseFormat = responseFormat
	req.MaxOutputTokens = maxOutput
	req.DeclaredInputTokens = CapabilityProbeDeclaredInputTokens

	now := p.now()
	inputTokens := CapabilityProbeDeclaredInputTokens
	outputTokens := maxOutput
	// RequestID/AttemptID are not part of ProbeRequest (see
	// probetransport.go's doc comment) — derive a local admission
	// identity from the operation, offering-operation, and clock.
	attemptID := fmt.Sprintf("capprobe:%s:%s:%d", req.Operation, req.OfferingOperationID, now.UnixNano())

	admission, err := p.guard.Admit(ctx, ProbeAdmissionRequest{
		AccountID:           req.AccountID,
		ProviderID:          req.ProviderID,
		OfferingOperationID: req.OfferingOperationID,
		RequestID:           attemptID,
		AttemptID:           attemptID,
		Operation:           req.Operation,
		Class:               ProbeStandard,
		Cost:                quota.EstimateInput{InputTokens: &inputTokens, MaxOutputTokens: &outputTokens},
	})
	if err != nil {
		return CapabilityProbeReport{}, err
	}

	res, err := p.transport.Probe(ctx, req)
	if err != nil {
		return CapabilityProbeReport{}, fmt.Errorf("intelligence: capability probe transport call failed: %w", err)
	}

	kind := classifyCapabilityProbeResult(res, required)
	outcome, err := ClassifyProbeSignal(kind)
	if err != nil {
		return CapabilityProbeReport{}, err
	}

	report := CapabilityProbeReport{
		Operation:     req.Operation,
		Outcome:       outcome,
		Snippet:       redactProbeSnippet(res.Message),
		ReservationID: admission.ReservationID,
	}

	if outcome.Definitive {
		report.Evidence = []Evidence{{
			Field:          CapabilityField(req.Operation),
			Scope:          Scope{AccountID: req.AccountID, ProviderModelID: req.ProviderModelID},
			Source:         SourceVerifiedProbe,
			Verification:   VerificationVerified,
			Confidence:     1.0,
			ObservedAt:     now,
			ProvenNegative: outcome.Truth == models.TruthUnsupported,
			Value:          outcome.Truth == models.TruthSupported,
		}}
	}

	return report, nil
}
