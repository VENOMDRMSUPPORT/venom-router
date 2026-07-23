package execution

import (
	"context"
	"errors"
	"fmt"
)

// TransportType identifies which commodity execution path handles a
// ResolvedRoute (01 §4.3). It is a property of the provider catalog,
// assigned once at provider-definition time — never derived by switching
// on a provider slug at dispatch time.
type TransportType string

const (
	TransportTypeBifrost          TransportType = "bifrost"
	TransportTypeNativeAPI        TransportType = "native_api"
	TransportTypeNativeOAuth      TransportType = "native_oauth"
	TransportTypeOpenAICompatible TransportType = "openai_compatible"
	TransportTypeCustom           TransportType = "custom"
)

// TransportTypeResolver maps a resolved route to the transport type that
// handles it. The real implementation is backed by the provider catalog
// (internal/providers, not yet built) and is injected here as an
// interface: this package never needs to know how that mapping is
// produced, and in particular never contains a switch over provider
// slugs itself.
type TransportTypeResolver interface {
	TransportTypeFor(route ResolvedRoute) (TransportType, error)
}

// ErrUnresolvableRoute is returned when no transport is registered for
// the route's transport type.
var ErrUnresolvableRoute = errors.New("execution: unresolvable route")

// Dispatcher is the single InferenceTransport dispatcher (01 §4.5).
// Given an already-resolved route, it selects a transport by typed
// capability (TransportType, via TransportTypeResolver) and delegates to
// it. It never re-selects or widens the route it was handed — every
// method below passes the same ResolvedRoute value straight through,
// unmodified, to the chosen transport.
type Dispatcher struct {
	resolver   TransportTypeResolver
	transports map[TransportType]InferenceTransport
}

// NewDispatcher builds a Dispatcher. transports maps each transport type
// this process has registered to its implementation; a type with no
// entry causes any route resolving to it to be rejected as unresolvable.
func NewDispatcher(resolver TransportTypeResolver, transports map[TransportType]InferenceTransport) *Dispatcher {
	return &Dispatcher{resolver: resolver, transports: transports}
}

func (d *Dispatcher) transportFor(route ResolvedRoute) (InferenceTransport, error) {
	tt, err := d.resolver.TransportTypeFor(route)
	if err != nil {
		return nil, fmt.Errorf("execution: resolve transport type: %w", err)
	}
	t, ok := d.transports[tt]
	if !ok {
		return nil, fmt.Errorf("%w: transport type %q", ErrUnresolvableRoute, tt)
	}
	return t, nil
}

// Execute dispatches a single non-streamed request.
func (d *Dispatcher) Execute(ctx context.Context, route ResolvedRoute, req NormalizedRequest) (*NormalizedResponse, error) {
	t, err := d.transportFor(route)
	if err != nil {
		return nil, err
	}
	return t.Execute(ctx, route, req)
}

// Stream dispatches a streaming request.
func (d *Dispatcher) Stream(ctx context.Context, route ResolvedRoute, req NormalizedRequest) (<-chan Chunk, error) {
	t, err := d.transportFor(route)
	if err != nil {
		return nil, err
	}
	return t.Stream(ctx, route, req)
}

// Cancel aborts an in-flight stream or request.
func (d *Dispatcher) Cancel(ctx context.Context, route ResolvedRoute, requestID string) error {
	t, err := d.transportFor(route)
	if err != nil {
		return err
	}
	return t.Cancel(ctx, route, requestID)
}

// NormalizeError converts a provider-native error into the stable error
// envelope, via the transport selected for route.
func (d *Dispatcher) NormalizeError(route ResolvedRoute, err error) (VenomError, error) {
	t, terr := d.transportFor(route)
	if terr != nil {
		return VenomError{}, terr
	}
	return t.NormalizeError(err, route), nil
}

// SupportedCapabilities returns the operation set the transport selected
// for route can handle.
func (d *Dispatcher) SupportedCapabilities(route ResolvedRoute) ([]Operation, error) {
	t, err := d.transportFor(route)
	if err != nil {
		return nil, err
	}
	return t.SupportedCapabilities(route), nil
}
