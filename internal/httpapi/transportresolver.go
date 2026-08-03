package httpapi

import (
	"context"
	"errors"
	"fmt"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/execution"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
)

// ErrProviderTransportUnresolvable is returned by the registry-backed
// TransportTypeResolver when the route's provider is not registered or
// carries a transport kind outside the closed vocabulary — fail closed,
// never a default transport.
var ErrProviderTransportUnresolvable = errors.New("httpapi: provider transport unresolvable")

// registryTransportResolver is THE implementation of
// execution.TransportTypeResolver (01 §4.4: transport selection is
// declared in the provider catalog): it looks the route's provider up in
// the registry and converts its catalog-declared TransportKind to the
// execution-side TransportType. The conversion goes through
// ParseTransportKind so an out-of-vocabulary value stored by any future
// write path is rejected here too, not just at Register time; the
// vocabulary sync test in transportresolver_test.go proves the two
// closed sets share identical string values.
type registryTransportResolver struct {
	reg *providers.Registry
}

// NewRegistryTransportResolver builds the catalog-backed resolver the
// execution.Dispatcher is composed with.
func NewRegistryTransportResolver(reg *providers.Registry) execution.TransportTypeResolver {
	return registryTransportResolver{reg: reg}
}

func (r registryTransportResolver) TransportTypeFor(route execution.ResolvedRoute) (execution.TransportType, error) {
	def, ok := r.reg.Definition(providers.ProviderID(route.Provider))
	if !ok {
		return "", fmt.Errorf("%w: provider %q is not registered", ErrProviderTransportUnresolvable, route.Provider)
	}
	kind, err := providers.ParseTransportKind(string(def.Transport))
	if err != nil {
		return "", fmt.Errorf("%w: provider %q: %v", ErrProviderTransportUnresolvable, route.Provider, err)
	}
	return execution.TransportType(kind), nil
}

// schemaStampingTransport wraps an InferenceTransport and stamps
// route.WireSchema from the provider's catalog Definition before EVERY call
// (P7-EXEC-001 part 2). This is how the native_oauth transport learns which
// wire schema a route speaks — from the REGISTRY, never a literal at the call
// site and never inferred from the model id. It is applied to the native_oauth
// entry in liveTransportImpls, so BOTH the request-path dispatcher and the
// certification probe path (which derive their transports from that one table)
// get the same registry-sourced schema. Non-native_oauth providers carry an
// empty WireSchema and their transports ignore it, so wrapping is harmless
// there; only native_oauth is wrapped.
type schemaStampingTransport struct {
	reg   *providers.Registry
	inner execution.InferenceTransport
}

func newSchemaStampingTransport(reg *providers.Registry, inner execution.InferenceTransport) *schemaStampingTransport {
	return &schemaStampingTransport{reg: reg, inner: inner}
}

// stamp copies route with WireSchema resolved from the registry. The
// providers.WireSchema -> execution.WireSchema cast is safe: the vocabulary
// sync test proves the two sets carry byte-identical string values.
func (s *schemaStampingTransport) stamp(route execution.ResolvedRoute) execution.ResolvedRoute {
	if def, ok := s.reg.Definition(providers.ProviderID(route.Provider)); ok {
		route.WireSchema = execution.WireSchema(def.WireSchema)
	}
	return route
}

func (s *schemaStampingTransport) Execute(ctx context.Context, route execution.ResolvedRoute, req execution.NormalizedRequest) (*execution.NormalizedResponse, error) {
	return s.inner.Execute(ctx, s.stamp(route), req)
}
func (s *schemaStampingTransport) Stream(ctx context.Context, route execution.ResolvedRoute, req execution.NormalizedRequest) (<-chan execution.Chunk, error) {
	return s.inner.Stream(ctx, s.stamp(route), req)
}
func (s *schemaStampingTransport) Cancel(ctx context.Context, route execution.ResolvedRoute, requestID string) error {
	return s.inner.Cancel(ctx, s.stamp(route), requestID)
}
func (s *schemaStampingTransport) NormalizeError(err error, route execution.ResolvedRoute) execution.VenomError {
	return s.inner.NormalizeError(err, s.stamp(route))
}
func (s *schemaStampingTransport) Failure(err error, route execution.ResolvedRoute) execution.TypedFailure {
	return s.inner.Failure(err, s.stamp(route))
}
func (s *schemaStampingTransport) SupportedCapabilities(route execution.ResolvedRoute) []execution.Operation {
	return s.inner.SupportedCapabilities(s.stamp(route))
}

var _ execution.InferenceTransport = (*schemaStampingTransport)(nil)

// BuildInferenceDispatcher composes the single execution.Dispatcher
// (01 §4.5: one execution path, one interface) from the provider
// registry and the per-type transport implementations. Routes whose
// provider is unregistered, or whose declared type has no entry in
// impls, are rejected by the dispatcher at call time as unresolvable —
// fail closed, never a fallback transport. ROUTE-013 is the consumer
// that wires this into the request flow.
func BuildInferenceDispatcher(
	reg *providers.Registry,
	impls map[execution.TransportType]execution.InferenceTransport,
) *execution.Dispatcher {
	return execution.NewDispatcher(NewRegistryTransportResolver(reg), impls)
}

// BuildProbeTransportMaps builds the two maps newProbeTransportAdapter
// needs — probeTransports (providerID → InferenceTransport) and
// probeBaseURLs (providerID → base URL string) — from the registry and
// two injected tables. Nothing calls it in production yet: it exists for
// the composition root that will wire probe transports from the catalog
// instead of hand-built maps, and it never touches a slug switch.
//
// impls maps each known TransportType to the InferenceTransport
// implementation that serves it (one impl per type, shared across
// providers). baseURLsByProvider maps each provider's catalog ProviderID
// to the fully-resolved base URL its transport will call.
//
// Fail-closed invariants:
//   - A providerID in baseURLsByProvider that is not registered in reg → error.
//   - A provider whose TransportKind has no entry in impls → error.
//
// Any provider NOT mentioned in baseURLsByProvider is simply absent from
// the returned maps — Available() reports it unavailable and ServeProbe
// refuses with 409 probe_unsupported before creating any job row.
func BuildProbeTransportMaps(
	reg *providers.Registry,
	impls map[execution.TransportType]execution.InferenceTransport,
	baseURLsByProvider map[providers.ProviderID]string,
) (probeTransports map[string]execution.InferenceTransport, probeBaseURLs map[string]string, err error) {
	probeTransports = make(map[string]execution.InferenceTransport, len(baseURLsByProvider))
	probeBaseURLs = make(map[string]string, len(baseURLsByProvider))

	for providerID, baseURL := range baseURLsByProvider {
		def, ok := reg.Definition(providerID)
		if !ok {
			return nil, nil, fmt.Errorf("httpapi: BuildProbeTransportMaps: provider %q is not registered", providerID)
		}

		// Convert catalog-side TransportKind to execution-side TransportType
		// via a plain string cast — the vocabulary sync test in
		// transportresolver_test.go proves they share the same string values.
		transportType := execution.TransportType(def.Transport)

		impl, ok := impls[transportType]
		if !ok {
			return nil, nil, fmt.Errorf("httpapi: BuildProbeTransportMaps: provider %q uses transport %q but no implementation is wired for it", providerID, transportType)
		}

		probeTransports[string(providerID)] = impl
		probeBaseURLs[string(providerID)] = baseURL
	}

	return probeTransports, probeBaseURLs, nil
}
