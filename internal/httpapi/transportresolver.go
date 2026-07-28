package httpapi

import (
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
