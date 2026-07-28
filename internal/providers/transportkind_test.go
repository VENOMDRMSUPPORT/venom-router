package providers

import (
	"errors"
	"testing"
)

// TestParseTransportKind_AllKnownValues proves every declared constant
// round-trips through ParseTransportKind in both directions.
func TestParseTransportKind_AllKnownValues(t *testing.T) {
	known := []TransportKind{
		TransportKindBifrost,
		TransportKindNativeAPI,
		TransportKindNativeOAuth,
		TransportKindOpenAICompatible,
		TransportKindCustom,
	}
	for _, k := range known {
		got, err := ParseTransportKind(string(k))
		if err != nil {
			t.Errorf("ParseTransportKind(%q) error = %v, want nil", k, err)
			continue
		}
		if got != k {
			t.Errorf("ParseTransportKind(%q) = %q, want %q", k, got, k)
		}
	}
}

// TestParseTransportKind_RejectsUnknown proves unknown values are
// rejected with the typed sentinel — never silently accepted.
func TestParseTransportKind_RejectsUnknown(t *testing.T) {
	bad := []string{"", "openai", "native_api_v2", "BIFROST", " bifrost"}
	for _, s := range bad {
		if _, err := ParseTransportKind(s); !errors.Is(err, ErrUnknownTransportKind) {
			t.Errorf("ParseTransportKind(%q) error = %v, want ErrUnknownTransportKind", s, err)
		}
	}
}
