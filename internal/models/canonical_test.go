package models

import (
	"errors"
	"testing"
)

// TestCanonicalKey_GoldenDigest pins the encoding scheme with a hard-coded
// expected hex digest, so an accidental change to the length-prefixed
// encoding (e.g. dropping the length prefixes) cannot pass silently even if
// every other test still happens to agree internally.
func TestCanonicalKey_GoldenDigest(t *testing.T) {
	const wantHex = "a83afd0947d7332160553ee612120fe59addfbac47488048b4fe119ac5c7ea7d"

	got, err := CanonicalKey("acme", "gpt-1")
	if err != nil {
		t.Fatalf("CanonicalKey: unexpected error: %v", err)
	}
	if got != wantHex {
		t.Fatalf("CanonicalKey(%q, %q) = %s, want golden digest %s", "acme", "gpt-1", got, wantHex)
	}
}

func TestCanonicalKey_Deterministic(t *testing.T) {
	a, err := CanonicalKey("acme", "gpt-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b, err := CanonicalKey("acme", "gpt-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a != b {
		t.Fatalf("CanonicalKey is not deterministic: %s != %s", a, b)
	}
}

// TestCanonicalKey_Injective proves the length-prefixed encoding cannot
// collide across field boundaries: ("a","bc"), ("ab","c"), ("a|b","c"),
// ("a","b|c"), and a NUL-containing pair must all derive distinct keys.
func TestCanonicalKey_Injective(t *testing.T) {
	pairs := [][2]string{
		{"a", "bc"},
		{"ab", "c"},
		{"a|b", "c"},
		{"a", "b|c"},
		{"a\x00b", "c"},
	}

	seen := make(map[string][2]string)
	for _, p := range pairs {
		key, err := CanonicalKey(p[0], p[1])
		if err != nil {
			t.Fatalf("CanonicalKey(%q, %q): unexpected error: %v", p[0], p[1], err)
		}
		if prior, exists := seen[key]; exists {
			t.Fatalf("CanonicalKey collision: %v and %v both produced %s", prior, p, key)
		}
		seen[key] = p
	}
}

// TestCanonicalKey_ProviderScoped proves the same provider_model_id under
// two different providers yields two different keys — no cross-provider
// equivalence in v1.
func TestCanonicalKey_ProviderScoped(t *testing.T) {
	a, err := CanonicalKey("provider-one", "shared-model-id")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b, err := CanonicalKey("provider-two", "shared-model-id")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a == b {
		t.Fatalf("CanonicalKey must be provider-scoped: got identical key %s for two different providers", a)
	}
}

func TestCanonicalKey_EmptyFieldsRejected(t *testing.T) {
	cases := []struct {
		name            string
		providerID      string
		providerModelID string
	}{
		{"empty provider", "", "gpt-1"},
		{"empty model", "acme", ""},
		{"both empty", "", ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := CanonicalKey(c.providerID, c.providerModelID)
			if !errors.Is(err, ErrEmptyCanonicalKeyField) {
				t.Fatalf("CanonicalKey(%q, %q) error = %v, want ErrEmptyCanonicalKeyField", c.providerID, c.providerModelID, err)
			}
		})
	}
}

func TestNewCanonicalModel_DerivesCanonicalKey(t *testing.T) {
	m, err := NewCanonicalModel("model-1", "acme", "gpt-1", "GPT-1", nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want, err := CanonicalKey("acme", "gpt-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.CanonicalKey != want {
		t.Fatalf("CanonicalModel.CanonicalKey = %s, want derived key %s", m.CanonicalKey, want)
	}
}

// TestNewCanonicalModel_UnknownIsNotZero proves an unknown native context
// or quality rating is representable and distinguishable from a real zero
// value: the fields are nil (unknown), never a stored zero.
func TestNewCanonicalModel_UnknownIsNotZero(t *testing.T) {
	m, err := NewCanonicalModel("model-1", "acme", "gpt-1", "GPT-1", nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.NativeContextTokens != nil {
		t.Fatalf("NativeContextTokens = %v, want nil (unknown)", m.NativeContextTokens)
	}
	if m.QualityRating != nil {
		t.Fatalf("QualityRating = %v, want nil (unknown)", m.QualityRating)
	}

	zero := 0
	zeroF := 0.0
	m2, err := NewCanonicalModel("model-2", "acme", "gpt-2", "GPT-2", &zero, nil, &zeroF)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m2.NativeContextTokens == nil || *m2.NativeContextTokens != 0 {
		t.Fatalf("NativeContextTokens = %v, want known zero (0)", m2.NativeContextTokens)
	}
	if m2.QualityRating == nil || *m2.QualityRating != 0 {
		t.Fatalf("QualityRating = %v, want known zero (0)", m2.QualityRating)
	}
	// The two must be distinguishable: one is unknown (nil), one is a known 0.
	if (m.NativeContextTokens == nil) == (m2.NativeContextTokens == nil) {
		t.Fatalf("unknown and known-zero must be distinguishable, both reported nil-ness = %v", m.NativeContextTokens == nil)
	}
}

func TestNewCanonicalModel_RejectsOutOfRangeQuality(t *testing.T) {
	tooHigh := 100.1
	tooLow := -0.1

	if _, err := NewCanonicalModel("model-1", "acme", "gpt-1", "GPT-1", nil, nil, &tooHigh); !errors.Is(err, ErrQualityRatingOutOfRange) {
		t.Fatalf("quality=%v error = %v, want ErrQualityRatingOutOfRange", tooHigh, err)
	}
	if _, err := NewCanonicalModel("model-1", "acme", "gpt-1", "GPT-1", nil, nil, &tooLow); !errors.Is(err, ErrQualityRatingOutOfRange) {
		t.Fatalf("quality=%v error = %v, want ErrQualityRatingOutOfRange", tooLow, err)
	}
}

func TestNewCanonicalModel_RejectsEmptyIdentity(t *testing.T) {
	if _, err := NewCanonicalModel("", "acme", "gpt-1", "GPT-1", nil, nil, nil); err == nil {
		t.Fatal("expected error for empty model ID, got nil")
	}
	if _, err := NewCanonicalModel("model-1", "", "gpt-1", "GPT-1", nil, nil, nil); !errors.Is(err, ErrEmptyCanonicalKeyField) {
		t.Fatalf("expected ErrEmptyCanonicalKeyField for empty provider ID, got %v", err)
	}
}
