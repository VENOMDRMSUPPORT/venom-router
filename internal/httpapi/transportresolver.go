package httpapi

import (
	"fmt"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/execution"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
)

// BuildDispatcher builds the two maps that newProbeTransportAdapter needs —
// probeTransports (providerID → InferenceTransport) and probeBaseURLs
// (providerID → base URL string) — from the registry and two injected
// tables. It is the sole entry-point ControlMux uses to wire transports;
// it never touches a slug switch.
//
// impls maps each known TransportType to the InferenceTransport
// implementation that serves it (one impl per type, shared across
// providers). baseURLsByProvider maps each provider's catalog ProviderID
// to the fully-resolved base URL its transport will call.
//
// Fail-closed invariants:
//   - A providerID in baseURLsByProvider that is not registered in reg → error.
//   - A provider whose TransportKind has no entry in impls → error.
//   - An unknown TransportKind string (should not happen if reg.Register is
//     the only write path to the registry) → error.
//
// Any provider NOT mentioned in baseURLsByProvider is simply absent from
// the returned maps — Available() reports it unavailable and ServeProbe
// refuses with 409 probe_unsupported before creating any job row.
func BuildDispatcher(
	reg *providers.Registry,
	impls map[execution.TransportType]execution.InferenceTransport,
	baseURLsByProvider map[providers.ProviderID]string,
) (probeTransports map[string]execution.InferenceTransport, probeBaseURLs map[string]string, err error) {
	probeTransports = make(map[string]execution.InferenceTransport, len(baseURLsByProvider))
	probeBaseURLs = make(map[string]string, len(baseURLsByProvider))

	for providerID, baseURL := range baseURLsByProvider {
		def, ok := reg.Definition(providerID)
		if !ok {
			return nil, nil, fmt.Errorf("httpapi: BuildDispatcher: provider %q is not registered", providerID)
		}

		// Convert catalog-side TransportKind to execution-side TransportType
		// via a plain string cast — the vocabulary sync test in
		// transportresolver_test.go proves they share the same string values.
		transportType := execution.TransportType(def.Transport)

		impl, ok := impls[transportType]
		if !ok {
			return nil, nil, fmt.Errorf("httpapi: BuildDispatcher: provider %q uses transport %q but no implementation is wired for it", providerID, transportType)
		}

		probeTransports[string(providerID)] = impl
		probeBaseURLs[string(providerID)] = baseURL
	}

	return probeTransports, probeBaseURLs, nil
}
