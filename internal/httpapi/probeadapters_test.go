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
// content) must classify as WitnessTextOnly — probeWitnessOf can never
// distinguish a vision answer from plain text, so it must never claim
// WitnessVisionAnswer for anything.
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
