package httpapi

// probeadapters_test.go exercises the P3c-EXEC-001 production
// probeTransportAdapter directly (not through ProbeHandler — probe_test.go
// already covers the handler's own admission/certification pipeline over
// a fake transport). These tests prove the REAL adapter: a genuine
// execution.OpenAICompatibleTransport against a local httptest server,
// end to end through intelligence.ExtractContextLimit.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/application"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/execution"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/intelligence"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

const probeAdapterTestProviderID = "prov-adapter"
const probeAdapterTestAccountID = "acct-adapter"
const probeAdapterCanaryKey = "sk-canary-never-leaks-1234567890"

// newProbeAdapterFixture builds a real, migrated DB with one connected
// account + active API-key credential (the canary key), and returns the
// credential repo + service the adapter leases through.
func newProbeAdapterFixture(t *testing.T) (*storage.DB, *storage.AccountCredentialRepo, *application.CredentialService) {
	t.Helper()
	db := testControlDB(t)
	p3aSeedAccount(t, db, probeAdapterTestAccountID, probeAdapterTestProviderID)

	credRepo := storage.NewAccountCredentialRepo(db)
	credSvc := application.NewCredentialService(credRepo, testKeyring(t), nil)
	if _, err := credSvc.Store(context.Background(), application.StoreCredentialParams{
		ID: "cred-adapter", AccountID: probeAdapterTestAccountID, ProviderID: probeAdapterTestProviderID,
		Kind: domain.CredentialKindAPIKey, Active: true, PlaintextKey: probeAdapterCanaryKey,
	}); err != nil {
		t.Fatalf("store credential: %v", err)
	}
	return db, credRepo, credSvc
}

// funcExecuteTransport is a minimal execution.InferenceTransport whose
// Execute delegates to an injected func, so a test can capture the exact
// execution.NormalizedRequest the adapter built without standing up a real
// HTTP server (probeadapters_test.go otherwise drives a real
// OpenAICompatibleTransport against httptest — this fake exists only to
// inspect the request the adapter constructs, e.g. whether Tools/Parts/
// ResponseFormat/Operation actually reached it).
type funcExecuteTransport struct {
	execute func(ctx context.Context, route execution.ResolvedRoute, req execution.NormalizedRequest) (*execution.NormalizedResponse, error)
}

func (f *funcExecuteTransport) Execute(ctx context.Context, route execution.ResolvedRoute, req execution.NormalizedRequest) (*execution.NormalizedResponse, error) {
	return f.execute(ctx, route, req)
}
func (f *funcExecuteTransport) Stream(_ context.Context, _ execution.ResolvedRoute, _ execution.NormalizedRequest) (<-chan execution.Chunk, error) {
	return nil, errStubNotImplemented
}
func (f *funcExecuteTransport) Cancel(_ context.Context, _ execution.ResolvedRoute, _ string) error {
	return errStubNotImplemented
}
func (f *funcExecuteTransport) NormalizeError(_ error, _ execution.ResolvedRoute) execution.VenomError {
	return execution.VenomError{Code: "func-transport"}
}
func (f *funcExecuteTransport) Failure(_ error, _ execution.ResolvedRoute) execution.TypedFailure {
	return execution.TypedFailure{}
}
func (f *funcExecuteTransport) SupportedCapabilities(_ execution.ResolvedRoute) []execution.Operation {
	return nil
}

var _ execution.InferenceTransport = (*funcExecuteTransport)(nil)

// newProbeAdapterWithFakeTransport builds a real probeTransportAdapter —
// real migrated DB, real seeded account "acct-1" with an active API-key
// credential under provider "prov-1" — wired to a funcExecuteTransport
// running execute, so a test can inspect the NormalizedRequest Probe
// actually sent without any network call.
func newProbeAdapterWithFakeTransport(t *testing.T, execute func(ctx context.Context, route execution.ResolvedRoute, req execution.NormalizedRequest) (*execution.NormalizedResponse, error)) *probeTransportAdapter {
	t.Helper()
	const accountID = "acct-1"
	const providerID = "prov-1"

	db := testControlDB(t)
	p3aSeedAccount(t, db, accountID, providerID)
	credRepo := storage.NewAccountCredentialRepo(db)
	credSvc := application.NewCredentialService(credRepo, testKeyring(t), nil)
	if _, err := credSvc.Store(context.Background(), application.StoreCredentialParams{
		ID: "cred-1", AccountID: accountID, ProviderID: providerID,
		Kind: domain.CredentialKindAPIKey, Active: true, PlaintextKey: "sk-fake-transport-key",
	}); err != nil {
		t.Fatalf("store credential: %v", err)
	}

	transport := &funcExecuteTransport{execute: execute}
	return newProbeTransportAdapter(
		map[string]execution.InferenceTransport{providerID: transport},
		map[string]string{providerID: "http://unused.invalid"},
		credRepo, credSvc,
	)
}

// TestProbeAdapter_CarriesToolsPartsAndResponseFormatToTheTransport is
// task-1's central proof: today, Probe builds every request as a plain
// chat message with no Tools, no Parts, no ResponseFormat and a hardcoded
// OperationChat, so a tools probe declares no tool and a vision probe's
// image part never reaches the transport as an image. Both are
// structurally unable to satisfy their RequiredWitness. This test proves
// the adapter now carries all four through untouched.
func TestProbeAdapter_CarriesToolsPartsAndResponseFormatToTheTransport(t *testing.T) {
	var got execution.NormalizedRequest
	adapter := newProbeAdapterWithFakeTransport(t, func(_ context.Context, _ execution.ResolvedRoute, req execution.NormalizedRequest) (*execution.NormalizedResponse, error) {
		got = req
		return &execution.NormalizedResponse{HTTPStatus: 200, Message: execution.Message{Content: "ok"}}, nil
	})

	_, err := adapter.Probe(context.Background(), intelligence.ProbeRequest{
		AccountID: "acct-1", ProviderID: "prov-1", ProviderModelID: "m-1",
		OfferingOperationID: "oo-1", Operation: models.OperationTools,
		Messages: []intelligence.ProbeMessage{{
			Role: "user",
			Parts: []intelligence.ProbePart{
				{Kind: intelligence.ProbePartText, Text: "what colour"},
				{Kind: intelligence.ProbePartImage, ImageBase64: "iVBORw0KGgo=", MediaType: "image/png"},
			},
		}},
		Tools:           []intelligence.ProbeTool{{Name: "add", Description: "adds two numbers", ParametersJSON: `{"type":"object"}`}},
		ResponseFormat:  "json_object",
		MaxOutputTokens: 16,
	})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}

	if len(got.Tools) != 1 || got.Tools[0].Name != "add" {
		t.Fatalf("Tools = %#v, want the declared add tool — a tools probe that declares no tool can never produce a tool call", got.Tools)
	}
	if got.ResponseFormat != "json_object" {
		t.Fatalf("ResponseFormat = %q, want json_object", got.ResponseFormat)
	}
	if len(got.Messages) != 1 || len(got.Messages[0].Parts) != 2 {
		t.Fatalf("Messages = %#v, want one message carrying two parts — an image pasted into a text string is not an image", got.Messages)
	}
	if got.Messages[0].Parts[1].Kind != execution.ContentPartImage || got.Messages[0].Parts[1].ImageBase64 == "" {
		t.Fatalf("part 1 = %#v, want a base64 image part", got.Messages[0].Parts[1])
	}
	if got.Operation != execution.OperationTools {
		t.Fatalf("Operation = %q, want the operation under test, not a hardcoded chat", got.Operation)
	}
}

// TestExecOperationFor_ContextWindowFallsBackToChat pins the fallback half
// of the operation-conversion table: context_window (and any other
// models.Operation the five-value execution.Operation vocabulary does not
// carry) maps to execution.OperationChat rather than dropping the request
// — ContextProbe deliberately sends a chat-shaped oversized request and
// must keep working after this change.
func TestExecOperationFor_ContextWindowFallsBackToChat(t *testing.T) {
	rows := []struct {
		in   models.Operation
		want execution.Operation
	}{
		{models.OperationChat, execution.OperationChat},
		{models.OperationStreaming, execution.OperationStreaming},
		{models.OperationTools, execution.OperationTools},
		{models.OperationStructuredOutput, execution.OperationStructuredOutput},
		{models.OperationVision, execution.OperationVision},
		{models.OperationContextWindow, execution.OperationChat},
		{models.OperationImageGeneration, execution.OperationChat},
		{models.OperationReasoning, execution.OperationChat},
	}
	for _, row := range rows {
		t.Run(string(row.in), func(t *testing.T) {
			if got := execOperationFor(row.in); got != row.want {
				t.Fatalf("execOperationFor(%q) = %q, want %q", row.in, got, row.want)
			}
		})
	}
}

// TestProbeWitnessOf_NamesTheFixtureColourAsVisionAnswer proves the vision
// witness is now reachable: a response naming the vision fixture's colour
// (case-insensitively) classifies as WitnessVisionAnswer, and a response
// naming a different colour does not — probeWitnessOf cannot structurally
// distinguish a vision answer from text, so this is a content assertion by
// design, not a structural one.
func TestProbeWitnessOf_NamesTheFixtureColourAsVisionAnswer(t *testing.T) {
	t.Run("names the fixture colour", func(t *testing.T) {
		resp := &execution.NormalizedResponse{Message: execution.Message{Role: "assistant", Content: "The image is " + intelligence.VisionFixtureColour + "."}}
		if got := probeWitnessOf(resp); got != intelligence.WitnessVisionAnswer {
			t.Fatalf("probeWitnessOf() = %q, want %q", got, intelligence.WitnessVisionAnswer)
		}
	})
	t.Run("case-insensitive", func(t *testing.T) {
		resp := &execution.NormalizedResponse{Message: execution.Message{Role: "assistant", Content: strings.ToUpper(intelligence.VisionFixtureColour)}}
		if got := probeWitnessOf(resp); got != intelligence.WitnessVisionAnswer {
			t.Fatalf("probeWitnessOf() = %q, want %q", got, intelligence.WitnessVisionAnswer)
		}
	})
	t.Run("a different colour does not match", func(t *testing.T) {
		resp := &execution.NormalizedResponse{Message: execution.Message{Role: "assistant", Content: "The image appears to be teal."}}
		if got := probeWitnessOf(resp); got != intelligence.WitnessTextOnly {
			t.Fatalf("probeWitnessOf() = %q, want %q", got, intelligence.WitnessTextOnly)
		}
	})
}

// TestProbeWitnessOf_ClassifiesToolCallResponse is mutation row 1.3's
// direct pinning test: a response carrying tool calls must classify as
// WitnessToolCall, never silently downgraded to WitnessTextOnly.
func TestProbeWitnessOf_ClassifiesToolCallResponse(t *testing.T) {
	resp := &execution.NormalizedResponse{
		Message:   execution.Message{Role: "assistant", Content: ""},
		ToolCalls: []execution.ToolCall{{Name: "add", ArgumentsJSON: `{"a":2,"b":2}`}},
	}
	if got := probeWitnessOf(resp); got != intelligence.WitnessToolCall {
		t.Fatalf("probeWitnessOf() = %q, want %q", got, intelligence.WitnessToolCall)
	}
}

// TestProbeWitnessOf_ClassifiesStructuredJSON proves a JSON-object
// content response (no tool calls) classifies as WitnessStructuredJSON.
func TestProbeWitnessOf_ClassifiesStructuredJSON(t *testing.T) {
	resp := &execution.NormalizedResponse{Message: execution.Message{Role: "assistant", Content: `{"ok":true}`}}
	if got := probeWitnessOf(resp); got != intelligence.WitnessStructuredJSON {
		t.Fatalf("probeWitnessOf() = %q, want %q", got, intelligence.WitnessStructuredJSON)
	}
}

// TestProbeWitnessOf_TextOnlyNeverClaimsVisionAnswer is mutation row 1.4's
// direct pinning test: a plain-text response (no tool calls, non-JSON
// content, and not naming the vision fixture's colour) must classify as
// WitnessTextOnly. probeWitnessOf's vision check is a content assertion
// against intelligence.VisionFixtureColour specifically (see
// TestProbeWitnessOf_NamesTheFixtureColourAsVisionAnswer) — it is not a
// structural distinction, so any response that doesn't name that colour,
// including this one, must still classify as WitnessTextOnly.
func TestProbeWitnessOf_TextOnlyNeverClaimsVisionAnswer(t *testing.T) {
	resp := &execution.NormalizedResponse{Message: execution.Message{Role: "assistant", Content: "the sky is blue"}}
	if got := probeWitnessOf(resp); got != intelligence.WitnessTextOnly {
		t.Fatalf("probeWitnessOf() = %q, want %q", got, intelligence.WitnessTextOnly)
	}
}

// TestProbeTransportAdapter_EndToEndContextLimit is this unit's central
// proof (§3): the real adapter + a real HTTP rejection + ExtractContextLimit
// recover 128000 — the first time a real response drives the ladder.
// MUTATION 1.5: putting SafeMessage instead of RawMessage into
// ProbeResult.Message breaks this, since SafeMessage never carries the
// provider's rejection phrase ExtractContextLimit's rung 2 needs.
func TestProbeTransportAdapter_EndToEndContextLimit(t *testing.T) {
	const rawMessage = "maximum context length is 128000 tokens"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"code": "context_length_exceeded", "message": rawMessage},
		})
	}))
	t.Cleanup(srv.Close)

	_, credRepo, credSvc := newProbeAdapterFixture(t)
	transport := execution.NewOpenAICompatibleTransport(&http.Client{}, 0)
	adapter := newProbeTransportAdapter(
		map[string]execution.InferenceTransport{probeAdapterTestProviderID: transport},
		map[string]string{probeAdapterTestProviderID: srv.URL},
		credRepo, credSvc,
	)

	result, err := adapter.Probe(context.Background(), intelligence.ProbeRequest{
		AccountID: probeAdapterTestAccountID, ProviderID: probeAdapterTestProviderID,
		ProviderModelID: "model-adapter", OfferingOperationID: "op-adapter",
		Messages:        []intelligence.ProbeMessage{{Role: "user", Content: "probe"}},
		MaxOutputTokens: 8,
	})
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if result.HTTPStatus != http.StatusBadRequest {
		t.Fatalf("HTTPStatus = %d, want %d", result.HTTPStatus, http.StatusBadRequest)
	}
	if result.ProviderCode != "context_length_exceeded" {
		t.Fatalf("ProviderCode = %q, want context_length_exceeded", result.ProviderCode)
	}

	limit, rung, ok := intelligence.ExtractContextLimit(result, nil, probeAdapterTestProviderID)
	if !ok || limit != 128000 {
		t.Fatalf("ExtractContextLimit = (%d, %q, %v), want (128000, _, true)", limit, rung, ok)
	}
}

// TestProbeTransportAdapter_CredentialNeverOutlivesTheLease proves the
// decrypted credential plaintext is used only inside
// CredentialService.Use's callback and never retained anywhere the caller
// can observe afterward. MUTATION 1.7: retaining the credential on the
// adapter struct (e.g. a lastCredential field set during Probe) turns
// this RED.
func TestProbeTransportAdapter_CredentialNeverOutlivesTheLease(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "ok"}, "finish_reason": "stop"}},
		})
	}))
	t.Cleanup(srv.Close)

	_, credRepo, credSvc := newProbeAdapterFixture(t)
	transport := execution.NewOpenAICompatibleTransport(&http.Client{}, 0)
	adapter := newProbeTransportAdapter(
		map[string]execution.InferenceTransport{probeAdapterTestProviderID: transport},
		map[string]string{probeAdapterTestProviderID: srv.URL},
		credRepo, credSvc,
	)

	result, err := adapter.Probe(context.Background(), intelligence.ProbeRequest{
		AccountID: probeAdapterTestAccountID, ProviderID: probeAdapterTestProviderID,
		ProviderModelID: "model-adapter", OfferingOperationID: "op-adapter",
		Messages:        []intelligence.ProbeMessage{{Role: "user", Content: "probe"}},
		MaxOutputTokens: 8,
	})
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}

	// The transport really did receive the credential (proving the lease
	// was genuinely used), but nothing about it survives in the adapter's
	// own state or in the result handed back to the caller.
	if gotAuth != "Bearer "+probeAdapterCanaryKey {
		t.Fatalf("server saw Authorization = %q, want the canary key — the lease must have actually been used", gotAuth)
	}

	adapterRender := fmt.Sprintf("%+v", adapter)
	if strings.Contains(adapterRender, probeAdapterCanaryKey) {
		t.Fatalf("adapter retains the canary credential: %s", adapterRender)
	}
	resultRender := fmt.Sprintf("%+v", result)
	if strings.Contains(resultRender, probeAdapterCanaryKey) {
		t.Fatalf("ProbeResult retains the canary credential: %s", resultRender)
	}
}

// TestProbeTransportAdapter_UnavailableProviderRefuses proves a provider
// absent from the adapter's transports map is honestly unavailable — no
// job/credential lease/network call is ever attempted for it. MUTATION
// 1.8: making Available return true for every provider turns this RED.
func TestProbeTransportAdapter_UnavailableProviderRefuses(t *testing.T) {
	_, credRepo, credSvc := newProbeAdapterFixture(t)
	adapter := newProbeTransportAdapter(
		map[string]execution.InferenceTransport{}, map[string]string{},
		credRepo, credSvc,
	)

	if adapter.Available("some-unwired-provider") {
		t.Fatalf("Available() = true, want false for a provider with no wired transport")
	}

	_, err := adapter.Probe(context.Background(), intelligence.ProbeRequest{
		AccountID: probeAdapterTestAccountID, ProviderID: "some-unwired-provider",
		ProviderModelID: "model-adapter", OfferingOperationID: "op-adapter",
	})
	if err != ErrProbeTransportUnavailable {
		t.Fatalf("Probe() error = %v, want ErrProbeTransportUnavailable", err)
	}
}
