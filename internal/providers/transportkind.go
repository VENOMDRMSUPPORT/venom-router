package providers

import (
	"errors"
	"fmt"
)

// TransportKind identifies which execution transport path handles an
// offering for a provider (01 §4.3, §4.4). This is the providers-layer
// copy of execution.TransportType — internal/providers may never import
// internal/execution (layering), so the two sets are maintained in
// parallel and kept in sync by the vocabulary test in
// internal/httpapi/transportresolver_test.go.
type TransportKind string

const (
	TransportKindBifrost          TransportKind = "bifrost"
	TransportKindNativeAPI        TransportKind = "native_api"
	TransportKindNativeOAuth      TransportKind = "native_oauth"
	TransportKindOpenAICompatible TransportKind = "openai_compatible"
	TransportKindCustom           TransportKind = "custom"
)

// ErrUnknownTransportKind is returned by ParseTransportKind for any value
// outside the closed vocabulary above. Fail-closed: a missing kind is
// rejected at registration, never silently accepted.
var ErrUnknownTransportKind = errors.New("providers: unrecognized transport kind")

// ParseTransportKind returns the TransportKind for s, or
// ErrUnknownTransportKind if s is not in the closed set.
// Implemented with if/else — a switch on string-literal cases is
// forbidden (01 §4.5 / 08 §8).
func ParseTransportKind(s string) (TransportKind, error) {
	if s == string(TransportKindBifrost) {
		return TransportKindBifrost, nil
	}
	if s == string(TransportKindNativeAPI) {
		return TransportKindNativeAPI, nil
	}
	if s == string(TransportKindNativeOAuth) {
		return TransportKindNativeOAuth, nil
	}
	if s == string(TransportKindOpenAICompatible) {
		return TransportKindOpenAICompatible, nil
	}
	if s == string(TransportKindCustom) {
		return TransportKindCustom, nil
	}
	return "", fmt.Errorf("%w: %q", ErrUnknownTransportKind, s)
}
