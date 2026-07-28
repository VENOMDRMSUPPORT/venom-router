package routing

import (
	"errors"
	"testing"
)

// defaultKeyer returns a BucketKeyer at the default 32K threshold.
func defaultKeyer(t *testing.T) BucketKeyer {
	t.Helper()
	keyer, err := NewBucketKeyer(DefaultLargeContextThreshold)
	if err != nil {
		t.Fatalf("NewBucketKeyer(default) error = %v, want nil", err)
	}
	return keyer
}

// TestNewBucketKeyer_ThresholdValidation proves construction in both
// directions: a positive threshold is accepted, zero and negative are
// rejected with the typed error.
func TestNewBucketKeyer_ThresholdValidation(t *testing.T) {
	if _, err := NewBucketKeyer(1); err != nil {
		t.Fatalf("NewBucketKeyer(1) error = %v, want nil", err)
	}
	for _, bad := range []int64{0, -32768} {
		if _, err := NewBucketKeyer(bad); !errors.Is(err, ErrInvalidThreshold) {
			t.Fatalf("NewBucketKeyer(%d) error = %v, want ErrInvalidThreshold", bad, err)
		}
	}
}

// TestBucketKey_SameSetSameKey is the card's exact test: a request whose
// matched properties are {vision, structured} produces the SAME canonical
// key regardless of how the request presented them — and that key is the
// sorted serialization, not the property-evaluation order (vision is
// evaluated before structured internally, so only sorting can produce
// this string).
func TestBucketKey_SameSetSameKey(t *testing.T) {
	keyer := defaultKeyer(t)

	build := func(explicit []Capability) Requirements {
		req := textRequest()
		req.Parts = append(req.Parts, MessagePart{Kind: PartImage})
		req.StructuredOutputRequested = true
		req.Venom = &VenomExtension{RequiredCapabilities: explicit}
		reqs, err := Normalize(req)
		if err != nil {
			t.Fatalf("Normalize() error = %v, want nil", err)
		}
		return reqs
	}

	first := keyer.BucketKey(build([]Capability{CapabilityVision, CapabilityStructuredOutput}))
	second := keyer.BucketKey(build([]Capability{CapabilityStructuredOutput, CapabilityVision}))
	if first != second {
		t.Fatalf("permuted presentations produced different keys: %q vs %q", first, second)
	}
	if first != "structured+vision" {
		t.Fatalf("key = %q, want %q (lowercase, lexicographically sorted, +-joined)", first, "structured+vision")
	}
}

// TestBucketKey_GoldenKeys freezes the canonical serialization with
// golden strings (not derived): the exact keys 05 §2 Step 7's cells will
// be written under. No other call site may re-serialize.
func TestBucketKey_GoldenKeys(t *testing.T) {
	keyer := defaultKeyer(t)

	cases := []struct {
		name string
		reqs Requirements
		want string
	}{
		{
			name: "nothing matched",
			reqs: Requirements{TextModality: true, ContextTokens: 1000},
			want: "standard",
		},
		{
			name: "vision only",
			reqs: Requirements{VisionModality: true, ContextTokens: 1000},
			want: "vision",
		},
		{
			name: "tool_use only",
			reqs: Requirements{Capabilities: []Capability{CapabilityTools}, ContextTokens: 1000},
			want: "tool_use",
		},
		{
			name: "structured only",
			reqs: Requirements{Capabilities: []Capability{CapabilityStructuredOutput}, ContextTokens: 1000},
			want: "structured",
		},
		{
			name: "large_context only",
			reqs: Requirements{TextModality: true, ContextTokens: 40000},
			want: "large_context",
		},
		{
			name: "all four properties",
			reqs: Requirements{
				VisionModality: true,
				Capabilities:   []Capability{CapabilityStructuredOutput, CapabilityTools},
				ContextTokens:  40000,
			},
			want: "large_context+structured+tool_use+vision",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := keyer.BucketKey(tc.reqs); got != tc.want {
				t.Fatalf("BucketKey() = %q, want %q", got, tc.want)
			}
			if again := keyer.BucketKey(tc.reqs); again != tc.want {
				t.Fatalf("second BucketKey() = %q, want %q (must be deterministic)", again, tc.want)
			}
		})
	}
}

// TestBucketKey_StandardIffNothingElseMatched proves `standard` is the
// fallback property ONLY: it appears exactly when no other property
// matched and never alongside another property.
func TestBucketKey_StandardIffNothingElseMatched(t *testing.T) {
	keyer := defaultKeyer(t)

	if got := keyer.BucketKey(Requirements{TextModality: true, ContextTokens: 500}); got != "standard" {
		t.Fatalf("no properties matched: key = %q, want %q", got, "standard")
	}

	matched := []Requirements{
		{VisionModality: true, ContextTokens: 500},
		{Capabilities: []Capability{CapabilityTools}, ContextTokens: 500},
		{Capabilities: []Capability{CapabilityStructuredOutput}, ContextTokens: 500},
		{TextModality: true, ContextTokens: 50000},
	}
	for _, reqs := range matched {
		key := keyer.BucketKey(reqs)
		if key == "standard" || containsProperty(key, "standard") {
			t.Errorf("key %q contains standard alongside a matched property; standard is the fallback only", key)
		}
	}
}

// containsProperty reports whether the canonical key contains the exact
// property between + separators.
func containsProperty(key, property string) bool {
	start := 0
	for i := 0; i <= len(key); i++ {
		if i == len(key) || key[i] == '+' {
			if key[start:i] == property {
				return true
			}
			start = i + 1
		}
	}
	return false
}

// TestBucketKey_LargeContextBoundary proves large_context triggers on
// S STRICTLY greater than the threshold — at the default and at a custom
// threshold.
func TestBucketKey_LargeContextBoundary(t *testing.T) {
	keyer := defaultKeyer(t)
	if got := keyer.BucketKey(Requirements{ContextTokens: 32768}); got != "standard" {
		t.Fatalf("S = threshold: key = %q, want %q (strictly greater, not >=)", got, "standard")
	}
	if got := keyer.BucketKey(Requirements{ContextTokens: 32769}); got != "large_context" {
		t.Fatalf("S = threshold+1: key = %q, want %q", got, "large_context")
	}

	custom, err := NewBucketKeyer(100)
	if err != nil {
		t.Fatalf("NewBucketKeyer(100) error = %v, want nil", err)
	}
	if got := custom.BucketKey(Requirements{ContextTokens: 100}); got != "standard" {
		t.Fatalf("custom threshold, S = 100: key = %q, want %q", got, "standard")
	}
	if got := custom.BucketKey(Requirements{ContextTokens: 101}); got != "large_context" {
		t.Fatalf("custom threshold, S = 101: key = %q, want %q", got, "large_context")
	}
}
