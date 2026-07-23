package execution

import (
	"context"
	"errors"
	"testing"
)

// recordingTransport records the exact ResolvedRoute it was called with,
// so tests can assert the dispatcher passed the route through unchanged
// — it can neither re-select nor widen it.
type recordingTransport struct {
	gotExecuteRoute ResolvedRoute
}

func (t *recordingTransport) Execute(_ context.Context, route ResolvedRoute, _ NormalizedRequest) (*NormalizedResponse, error) {
	t.gotExecuteRoute = route
	return &NormalizedResponse{}, nil
}

func (t *recordingTransport) Stream(_ context.Context, route ResolvedRoute, _ NormalizedRequest) (<-chan Chunk, error) {
	t.gotExecuteRoute = route
	ch := make(chan Chunk)
	close(ch)
	return ch, nil
}

func (t *recordingTransport) Cancel(_ context.Context, route ResolvedRoute, _ string) error {
	t.gotExecuteRoute = route
	return nil
}

func (t *recordingTransport) NormalizeError(_ error, route ResolvedRoute) VenomError {
	t.gotExecuteRoute = route
	return VenomError{}
}

func (t *recordingTransport) SupportedCapabilities(route ResolvedRoute) []Operation {
	t.gotExecuteRoute = route
	return []Operation{OperationChat}
}

// fixedResolver always resolves to the same TransportType, regardless of
// route — a stand-in for the real provider-catalog-backed resolver
// (internal/providers, not yet built).
type fixedResolver struct {
	transportType TransportType
}

func (r fixedResolver) TransportTypeFor(ResolvedRoute) (TransportType, error) {
	return r.transportType, nil
}

func testRoute() ResolvedRoute {
	return ResolvedRoute{
		Provider:   ProviderID("openai"),
		AccountID:  "acct_123",
		Credential: StoredCredentials{Value: "sk-test"},
		ModelID:    "gpt-test",
		BaseURL:    "https://example.invalid",
	}
}

// TestDispatcher_PassesRouteThroughUnchanged proves the dispatcher can
// neither re-select nor widen a ResolvedRoute: the transport it
// delegates to must receive exactly the same route value the dispatcher
// was given, for every method.
func TestDispatcher_PassesRouteThroughUnchanged(t *testing.T) {
	transport := &recordingTransport{}
	resolver := fixedResolver{transportType: TransportTypeBifrost}
	d := NewDispatcher(resolver, map[TransportType]InferenceTransport{
		TransportTypeBifrost: transport,
	})

	route := testRoute()
	ctx := context.Background()

	if _, err := d.Execute(ctx, route, NormalizedRequest{Operation: OperationChat}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if transport.gotExecuteRoute != route {
		t.Fatalf("Execute(): transport received %+v, want %+v", transport.gotExecuteRoute, route)
	}

	if _, err := d.Stream(ctx, route, NormalizedRequest{Operation: OperationStreaming}); err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if transport.gotExecuteRoute != route {
		t.Fatalf("Stream(): transport received %+v, want %+v", transport.gotExecuteRoute, route)
	}

	if err := d.Cancel(ctx, route, "req-1"); err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	if transport.gotExecuteRoute != route {
		t.Fatalf("Cancel(): transport received %+v, want %+v", transport.gotExecuteRoute, route)
	}

	if _, err := d.NormalizeError(route, errors.New("boom")); err != nil {
		t.Fatalf("NormalizeError() error = %v", err)
	}
	if transport.gotExecuteRoute != route {
		t.Fatalf("NormalizeError(): transport received %+v, want %+v", transport.gotExecuteRoute, route)
	}

	if _, err := d.SupportedCapabilities(route); err != nil {
		t.Fatalf("SupportedCapabilities() error = %v", err)
	}
	if transport.gotExecuteRoute != route {
		t.Fatalf("SupportedCapabilities(): transport received %+v, want %+v", transport.gotExecuteRoute, route)
	}
}

// TestDispatcher_UnresolvableRouteRejected proves a route whose transport
// type has no registered implementation is rejected with a typed error,
// not silently ignored or widened to some other transport.
func TestDispatcher_UnresolvableRouteRejected(t *testing.T) {
	resolver := fixedResolver{transportType: TransportTypeCustom}
	d := NewDispatcher(resolver, map[TransportType]InferenceTransport{
		TransportTypeBifrost: &recordingTransport{}, // deliberately not TransportTypeCustom
	})

	_, err := d.Execute(context.Background(), testRoute(), NormalizedRequest{})
	if !errors.Is(err, ErrUnresolvableRoute) {
		t.Fatalf("Execute() error = %v, want ErrUnresolvableRoute", err)
	}
}
