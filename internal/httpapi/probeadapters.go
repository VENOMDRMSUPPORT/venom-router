package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/application"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/execution"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/intelligence"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// auditActionOfferingProbe is P3c-CAPI-001's audit action code (mirrors
// discovery.go's AuditActionAccountDiscover / audit.go's own convention).
// It lives here rather than in audit.go — audit.go is not one of this
// batch's touchable files — but it is the SAME const-block pattern audit.go
// already uses; nothing about its shape is special-cased.
const auditActionOfferingProbe = "offering_probe" // POST /offerings/{id}/probe

// auditActionCertificationTransitioned is the audit action code
// certificationAuditorAdapter emits under (04 §5: "each [transition]
// emits an audit_event").
const auditActionCertificationTransitioned = "certification_transitioned"

// probeReserverAdapter implements intelligence.ProbeReserver over the
// frozen P3b reservation engine (storage.QuotaReservationRepo). It never
// re-derives the reservation contract itself — Reserve's own BEGIN
// IMMEDIATE / single-connection discipline is untouched here.
type probeReserverAdapter struct {
	reservations *storage.QuotaReservationRepo
}

func newProbeReserverAdapter(reservations *storage.QuotaReservationRepo) *probeReserverAdapter {
	return &probeReserverAdapter{reservations: reservations}
}

// ReserveProbe implements intelligence.ProbeReserver: maps the port's flat
// signature onto storage.ReserveParams and returns the reservation id.
// Any failure (including storage.ErrReservationRejected) is returned
// unwrapped — intelligence.ProbeGuard.Admit already translates ANY
// non-nil error from this port into the single RefusalQuotaRejected
// reason (probesafety.go), so no translation belongs here.
func (a *probeReserverAdapter) ReserveProbe(ctx context.Context, accountID, requestID, attemptID string, allocations []quota.Allocation) (string, error) {
	result, err := a.reservations.Reserve(ctx, storage.ReserveParams{
		AccountID:   accountID,
		RequestID:   requestID,
		AttemptID:   attemptID,
		Allocations: allocations,
	})
	if err != nil {
		return "", err
	}
	return result.ReservationID, nil
}

// certificationAuditorAdapter implements intelligence.CertificationAuditor
// over the shared auditEmitter. It writes ids/typed codes ONLY — the
// record's own fields (OfferingOperationID, typed state/reason/suspension
// codes) are the entire payload; there is no free-text field on
// intelligence.CertificationAuditRecord this adapter could leak a secret
// through even if it tried.
type certificationAuditorAdapter struct {
	audit *auditEmitter
}

func newCertificationAuditorAdapter(audit *auditEmitter) *certificationAuditorAdapter {
	return &certificationAuditorAdapter{audit: audit}
}

// CertificationTransitioned implements intelligence.CertificationAuditor.
// auditEmitter.Emit never returns an error (a write failure is logged and
// swallowed there, by design — see audit.go), so this always returns nil.
func (a *certificationAuditorAdapter) CertificationTransitioned(ctx context.Context, rec intelligence.CertificationAuditRecord) error {
	result := AuditResultFailure
	if rec.Accepted {
		result = AuditResultSuccess
	}
	a.audit.Emit(ctx, auditActionCertificationTransitioned, result, AuditResourceOffering, rec.OfferingOperationID, string(rec.Reason))
	return nil
}

// ErrProbeTransportUnavailable is returned by probeTransportAdapter.Probe
// for a provider absent from its transports map. RESOLVED AMBIGUITY (P3c-
// EXEC-001, see this batch's final report): internal/execution's seam now
// carries everything a probe needs (NormalizedRequest.MaxTokens,
// NormalizedResponse.{ToolCalls,HTTPStatus}, InferenceTransport.Failure's
// TypedFailure.{HTTPStatus,ProviderCode,RawMessage}) — this sentinel no
// longer means "the seam cannot express a probe request"; it means
// exactly what ServeProbe's Available() precondition already checks for:
// no transport is wired for THIS provider (today, only opencode-zen is).
var ErrProbeTransportUnavailable = errors.New("httpapi: no probe transport is wired for this provider")

// probeTransportAdapter is the production intelligence.ProbeTransport: a
// DATA lookup (never a slug switch) from provider id to the
// execution.InferenceTransport that serves it, plus the CredentialService
// it leases the account's active credential through for the single call
// each Probe makes.
type probeTransportAdapter struct {
	transports  map[string]execution.InferenceTransport
	baseURLs    map[string]string
	credentials *storage.AccountCredentialRepo
	credService *application.CredentialService
}

// newProbeTransportAdapter builds the adapter. transports/baseURLs are
// built once, at the composition root (ControlMux), from the providers
// that actually have a wired transport today — a provider absent from
// transports is simply unavailable, never a fabricated capability.
// baseURLs carries each wired provider's FULL resolved base (already
// including any version path segment its transport's own fixed
// "/chat/completions" suffix needs), independent of whatever base_url the
// providers catalog/table stores for that provider's OTHER adapters.
func newProbeTransportAdapter(
	transports map[string]execution.InferenceTransport,
	baseURLs map[string]string,
	credentials *storage.AccountCredentialRepo,
	credService *application.CredentialService,
) *probeTransportAdapter {
	return &probeTransportAdapter{
		transports: transports, baseURLs: baseURLs,
		credentials: credentials, credService: credService,
	}
}

// Available reports whether providerID has a wired transport. ServeProbe
// consults this SYNCHRONOUSLY, before creating any job row, so a probe
// request for an unwired provider is refused (409 probe_unsupported)
// before any side effect.
func (a *probeTransportAdapter) Available(providerID string) bool {
	_, ok := a.transports[providerID]
	return ok
}

// probeWitnessOf classifies resp per this batch's rule (§3): a non-empty
// ToolCalls is tool_call; content that parses as a JSON OBJECT is
// structured_json; content naming the vision fixture's expected colour
// (intelligence.VisionFixtureColour), case-insensitively, is vision_answer;
// anything else is text_only. The vision check is necessarily a CONTENT
// assertion, not a structural one — this transport has no way to tell a
// vision answer apart from any other prose except by what it says — which
// is exactly why WitnessVisionAnswer was dead before the adapter was given
// the fixture's expected answer to check against. All classification stays
// here, in one place.
func probeWitnessOf(resp *execution.NormalizedResponse) intelligence.ProbeWitness {
	if len(resp.ToolCalls) > 0 {
		return intelligence.WitnessToolCall
	}
	var asObject map[string]any
	if json.Unmarshal([]byte(resp.Message.Content), &asObject) == nil {
		return intelligence.WitnessStructuredJSON
	}
	if strings.Contains(strings.ToLower(resp.Message.Content), strings.ToLower(intelligence.VisionFixtureColour)) {
		return intelligence.WitnessVisionAnswer
	}
	return intelligence.WitnessTextOnly
}

// probeTransportFailureOf classifies execErr — the raw error
// InferenceTransport.Execute returned — onto the closed
// intelligence.ProbeTransportFailure vocabulary. It inspects execErr
// directly (via errors.Is against execution's own typed sentinels) rather
// than the derived TypedFailure, since a genuine HTTP rejection (handled
// separately, Transport=none) carries neither sentinel.
func probeTransportFailureOf(execErr error) intelligence.ProbeTransportFailure {
	switch {
	case errors.Is(execErr, execution.ErrTransportTimeout):
		return intelligence.TransportTimeout
	case errors.Is(execErr, execution.ErrTransportNetwork):
		return intelligence.TransportNetwork
	default:
		return intelligence.TransportNone
	}
}

// execContentPartsFrom maps ProbePart onto execution.ContentPart one for
// one. A nil/empty input returns nil (never an empty non-nil slice) so an
// unset Parts leaves the serialized wire body byte-identical to a request
// that never set it at all, matching execution.Message's own contract.
func execContentPartsFrom(parts []intelligence.ProbePart) []execution.ContentPart {
	if len(parts) == 0 {
		return nil
	}
	out := make([]execution.ContentPart, 0, len(parts))
	for _, p := range parts {
		out = append(out, execution.ContentPart{
			Kind:        execution.ContentPartKind(p.Kind),
			Text:        p.Text,
			ImageURL:    p.ImageURL,
			ImageBase64: p.ImageBase64,
			MediaType:   p.MediaType,
		})
	}
	return out
}

// execToolsFrom maps ProbeTool onto execution.ToolDefinition one for one.
// A nil/empty input returns nil for the same byte-identical-wire-body
// reason as execContentPartsFrom.
func execToolsFrom(tools []intelligence.ProbeTool) []execution.ToolDefinition {
	if len(tools) == 0 {
		return nil
	}
	out := make([]execution.ToolDefinition, 0, len(tools))
	for _, tl := range tools {
		out = append(out, execution.ToolDefinition{
			Name:           tl.Name,
			Description:    tl.Description,
			ParametersJSON: tl.ParametersJSON,
		})
	}
	return out
}

// execOperationFor maps models.Operation (the eight-value catalog/offering
// vocabulary) onto execution.Operation (the five-value transport
// vocabulary). context_window, image_generation, and reasoning have no
// execution-layer equivalent — context_window is deliberately probed with
// a chat-shaped oversized request (ContextProbe) and must keep working
// after this mapping exists, so any operation the execution vocabulary
// does not carry falls back to execution.OperationChat rather than
// dropping the request.
func execOperationFor(op models.Operation) execution.Operation {
	switch op {
	case models.OperationChat:
		return execution.OperationChat
	case models.OperationStreaming:
		return execution.OperationStreaming
	case models.OperationTools:
		return execution.OperationTools
	case models.OperationStructuredOutput:
		return execution.OperationStructuredOutput
	case models.OperationVision:
		return execution.OperationVision
	default:
		return execution.OperationChat
	}
}

// Probe implements intelligence.ProbeTransport: lease the account's
// active credential, build the ResolvedRoute and NormalizedRequest INSIDE
// the lease callback (the plaintext never outlives it — it is used to
// populate route.Credential.Value locally and never assigned to any
// field that escapes this function), call Execute exactly once, and
// classify the result. req.MaxOutputTokens is always > 0 for a real probe
// (both ContextProbe and CapabilityProbe set it before calling this port),
// so it is always carried through as NormalizedRequest.MaxTokens.
func (a *probeTransportAdapter) Probe(ctx context.Context, req intelligence.ProbeRequest) (intelligence.ProbeResult, error) {
	transport, ok := a.transports[req.ProviderID]
	if !ok {
		return intelligence.ProbeResult{}, ErrProbeTransportUnavailable
	}
	baseURL := a.baseURLs[req.ProviderID]

	credentialID, ok := activeCredentialIDFor(ctx, a.credentials, req.AccountID)
	if !ok {
		return intelligence.ProbeResult{}, fmt.Errorf("httpapi: probe: account %q has no active credential", req.AccountID)
	}

	messages := make([]execution.Message, 0, len(req.Messages))
	for _, m := range req.Messages {
		messages = append(messages, execution.Message{
			Role:    m.Role,
			Content: m.Content,
			Parts:   execContentPartsFrom(m.Parts),
		})
	}
	maxTokens := req.MaxOutputTokens

	var result intelligence.ProbeResult
	err := a.credService.Use(ctx, credentialID, func(plaintext []byte) error {
		route := execution.ResolvedRoute{
			Provider:   execution.ProviderID(req.ProviderID),
			AccountID:  req.AccountID,
			Credential: execution.StoredCredentials{Value: string(plaintext)},
			ModelID:    req.ProviderModelID,
			BaseURL:    baseURL,
		}
		normReq := execution.NormalizedRequest{
			Operation:      execOperationFor(req.Operation),
			Messages:       messages,
			MaxTokens:      &maxTokens,
			Tools:          execToolsFrom(req.Tools),
			ResponseFormat: req.ResponseFormat,
		}

		resp, execErr := transport.Execute(ctx, route, normReq)
		if execErr != nil {
			failure := transport.Failure(execErr, route)
			result = intelligence.ProbeResult{
				HTTPStatus:   failure.HTTPStatus,
				ProviderCode: failure.ProviderCode,
				Message:      failure.RawMessage,
				Transport:    probeTransportFailureOf(execErr),
			}
			return nil
		}

		result = intelligence.ProbeResult{
			HTTPStatus: resp.HTTPStatus,
			Witness:    probeWitnessOf(resp),
			Message:    resp.Message.Content,
			Transport:  intelligence.TransportNone,
		}
		return nil
	})
	if err != nil {
		return intelligence.ProbeResult{}, err
	}
	return result, nil
}

// Compile-time proof the production adapters structurally satisfy their
// intelligence ports.
var (
	_ intelligence.ProbeReserver        = (*probeReserverAdapter)(nil)
	_ intelligence.CertificationAuditor = (*certificationAuditorAdapter)(nil)
	_ intelligence.ProbeTransport       = (*probeTransportAdapter)(nil)
)
