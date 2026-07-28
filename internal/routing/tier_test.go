package routing

import (
	"errors"
	"testing"
)

// TestParseTier_KnownValuesRoundTrip proves every value of the closed
// three-tier vocabulary (05 §1) parses back to itself.
func TestParseTier_KnownValuesRoundTrip(t *testing.T) {
	for _, want := range []Tier{TierLite, TierPro, TierMax} {
		got, err := ParseTier(string(want))
		if err != nil {
			t.Fatalf("ParseTier(%q) error = %v, want nil", want, err)
		}
		if got != want {
			t.Fatalf("ParseTier(%q) = %q, want %q", want, got, want)
		}
	}
}

// TestParseTier_UnknownValuesRejected proves fail-closed parsing: no case
// folding, no trimming, no aliases.
func TestParseTier_UnknownValuesRejected(t *testing.T) {
	for _, bad := range []string{"", "ultra", "Lite", "pro ", "venom/max"} {
		_, err := ParseTier(bad)
		if !errors.Is(err, ErrUnknownTier) {
			t.Fatalf("ParseTier(%q) error = %v, want ErrUnknownTier", bad, err)
		}
	}
}

// TestParseThinkingLevel_KnownValuesRoundTrip proves the closed four-level
// vocabulary (05 §1a) parses back to itself.
func TestParseThinkingLevel_KnownValuesRoundTrip(t *testing.T) {
	for _, want := range []ThinkingLevel{ThinkingNone, ThinkingStandard, ThinkingExtended, ThinkingUltra} {
		got, err := ParseThinkingLevel(string(want))
		if err != nil {
			t.Fatalf("ParseThinkingLevel(%q) error = %v, want nil", want, err)
		}
		if got != want {
			t.Fatalf("ParseThinkingLevel(%q) = %q, want %q", want, got, want)
		}
	}
}

// TestParseThinkingLevel_UnknownValuesRejected proves fail-closed parsing.
func TestParseThinkingLevel_UnknownValuesRejected(t *testing.T) {
	for _, bad := range []string{"", "medium", "None", "extended ", "max"} {
		_, err := ParseThinkingLevel(bad)
		if !errors.Is(err, ErrUnknownThinkingLevel) {
			t.Fatalf("ParseThinkingLevel(%q) error = %v, want ErrUnknownThinkingLevel", bad, err)
		}
	}
}

// TestThinkingLevel_Ordering proves the none < standard < extended < ultra
// total order that UNIT 4's clamps depend on, in both directions, and that
// a value outside the vocabulary has no rank (fail closed).
func TestThinkingLevel_Ordering(t *testing.T) {
	ordered := []ThinkingLevel{ThinkingNone, ThinkingStandard, ThinkingExtended, ThinkingUltra}
	for i, lower := range ordered {
		for j, higher := range ordered {
			got := lower.atMost(higher)
			want := i <= j
			if got != want {
				t.Errorf("%q.atMost(%q) = %v, want %v", lower, higher, got, want)
			}
		}
	}

	if _, ok := ThinkingLevel("bogus").rank(); ok {
		t.Fatalf("ThinkingLevel(%q).rank() ok = true, want false (unknown level has no rank)", "bogus")
	}
	if r, ok := ThinkingUltra.rank(); !ok || r != 3 {
		t.Fatalf("ThinkingUltra.rank() = %d, %v; want 3, true", r, ok)
	}
}
