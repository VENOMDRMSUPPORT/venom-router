package execution

import (
	"context"
	"errors"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
)

// P4-TEST-002 — the "Bifrost never re-selects" safety gate (01 §4.1/§4.5).
//
// A transport receives a fully-decided single-choice ResolvedRoute and can
// neither re-select nor widen it: it can never promote venom/lite to a bigger
// model, hop to another account, or fan out to another provider. BifrostTransport
// enforces this on EVERY entry point (Execute, Stream, Cancel) via checkRoute,
// rejecting any route it was not configured for BEFORE any network call — the
// key whitelist (pool size 1, one model, retries disabled) is defense in depth
// behind that guard.
//
// This gate assertion complements TestBifrostTransport_CannotReselectRoute
// (which covers Execute) by proving the guard holds on all three methods, and by
// pinning the gate name into the CI log as the P4-TEST-002 no-reselect evidence.

// TestP4Gate_TransportNeverReselectsRoute drives the invariant across Execute,
// Stream, and Cancel.
//
// Mutation M3-N1: make checkRoute return nil unconditionally → a mismatched route
// is no longer rejected (Execute would call out against the CONFIGURED route, and
// Stream/Cancel would return their not-implemented error instead of the route
// error) → this test RED (on both the ErrRouteNotConfigured and the zero-network
// assertions).
func TestP4Gate_TransportNeverReselectsRoute(t *testing.T) {
	srv, requestCount := newFakeOpenAIServer(t, "must never be returned for a mismatched route")

	transport, err := NewBifrostTransport(context.Background(), BifrostTransportConfig{
		Provider: ProviderID(schemas.OpenAI),
		ModelID:  "gpt-test-model",
		APIKey:   "sk-test-key",
		BaseURL:  srv.URL,
	})
	if err != nil {
		t.Fatalf("NewBifrostTransport() error = %v", err)
	}
	t.Cleanup(transport.Close)

	baseRoute := ResolvedRoute{
		Provider:   ProviderID(schemas.OpenAI),
		AccountID:  "acct_smoke",
		Credential: StoredCredentials{Value: "sk-test-key"},
		ModelID:    "gpt-test-model",
		BaseURL:    srv.URL,
	}
	req := NormalizedRequest{Operation: OperationChat, Messages: []Message{{Role: "user", Content: "hi"}}}

	otherModel := baseRoute
	otherModel.ModelID = "some-other-model"
	otherProvider := baseRoute
	otherProvider.Provider = ProviderID("anthropic")

	// Every entry point must reject a route it was not configured for.
	for _, mismatch := range []struct {
		name  string
		route ResolvedRoute
	}{
		{"other-model", otherModel},
		{"other-provider", otherProvider},
	} {
		if _, err := transport.Execute(context.Background(), mismatch.route, req); !errors.Is(err, ErrRouteNotConfigured) {
			t.Fatalf("Execute(%s): err = %v, want ErrRouteNotConfigured", mismatch.name, err)
		}
		if _, err := transport.Stream(context.Background(), mismatch.route, req); !errors.Is(err, ErrRouteNotConfigured) {
			t.Fatalf("Stream(%s): err = %v, want ErrRouteNotConfigured", mismatch.name, err)
		}
		if err := transport.Cancel(context.Background(), mismatch.route, "req-1"); !errors.Is(err, ErrRouteNotConfigured) {
			t.Fatalf("Cancel(%s): err = %v, want ErrRouteNotConfigured", mismatch.name, err)
		}
	}

	// The rejection happened before any network call — not merely that the HTTP
	// response looked wrong.
	if got := requestCount.Load(); got != 0 {
		t.Fatalf("fake server received %d requests for mismatched routes, want 0 (reject before any network call)", got)
	}

	// The correctly-configured route is accepted through the SAME instance: Execute
	// succeeds, and Stream/Cancel pass checkRoute (they return their own
	// not-implemented error, NOT the route-mismatch error).
	if _, err := transport.Execute(context.Background(), baseRoute, req); err != nil {
		t.Fatalf("Execute(matching) error = %v", err)
	}
	if got := requestCount.Load(); got != 1 {
		t.Fatalf("fake server received %d requests after the matching Execute, want 1", got)
	}
	if _, err := transport.Stream(context.Background(), baseRoute, req); errors.Is(err, ErrRouteNotConfigured) {
		t.Fatalf("Stream(matching) must pass checkRoute; got the route-mismatch error")
	}
	if err := transport.Cancel(context.Background(), baseRoute, "req-1"); errors.Is(err, ErrRouteNotConfigured) {
		t.Fatalf("Cancel(matching) must pass checkRoute; got the route-mismatch error")
	}
}
