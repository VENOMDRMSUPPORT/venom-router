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

// visionFixtureDataURI is a fixed, tiny (1x1 pixel) inline PNG data URI —
// never a network URL — used by the vision fixture.
const visionFixtureDataURI = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="

// ErrNoCapabilityFixture is returned by CapabilityFixture and
// RequiredWitness for any operation outside the exactly three this batch
// certifies (tools, structured_output, vision).
var ErrNoCapabilityFixture = errors.New("intelligence: no capability fixture for this operation")

// CapabilityFixture returns the tiny, fixed, deterministic request body
// and MaxOutputTokens used to certify op. Only tools, structured_output,
// and vision are certified in this batch (04 §5 names exactly these three
// alongside chat/streaming/context_window as "recognized operations";
// chat/streaming/context_window are certified elsewhere, and
// image_generation is reserved future scope) — every other
// models.Operation returns ErrNoCapabilityFixture.
func CapabilityFixture(op models.Operation) ([]ProbeMessage, int, error) {
	switch op {
	case models.OperationTools:
		return []ProbeMessage{
			{Role: "user", Content: "What is 2+2? Use the add tool if one is available."},
		}, capabilityProbeMaxOutputTokens, nil
	case models.OperationStructuredOutput:
		return []ProbeMessage{
			{Role: "user", Content: `Return a JSON object with exactly one field "ok" set to true.`},
		}, capabilityProbeMaxOutputTokens, nil
	case models.OperationVision:
		return []ProbeMessage{
			{Role: "user", Content: "Describe the color of this image: " + visionFixtureDataURI},
		}, capabilityProbeMaxOutputTokens, nil
	default:
		return nil, 0, fmt.Errorf("%w: %q", ErrNoCapabilityFixture, op)
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
	messages, maxOutput, err := CapabilityFixture(req.Operation)
	if err != nil {
		return CapabilityProbeReport{}, err
	}
	required, err := RequiredWitness(req.Operation)
	if err != nil {
		return CapabilityProbeReport{}, err
	}

	req.Messages = messages
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
