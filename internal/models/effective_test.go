package models

import "testing"

func effIntPtr(v int) *int           { return &v }
func effFloatPtr(v float64) *float64 { return &v }

// TestEffectiveContext_Matrix covers 04 §3's resolution matrix, including
// the strictly-narrows boundary (equal caps must NOT be attributed to the
// provider cap) and the non-positive-is-unknown rule. MUTATION table
// targets: swapping min for max, or letting the cap overwrite the native
// value unconditionally.
func TestEffectiveContext_Matrix(t *testing.T) {
	cases := []struct {
		name           string
		native, cap    *int
		wantValue      *int
		wantProvenance ContextProvenance
	}{
		{"cap narrower than native", effIntPtr(100000), effIntPtr(50000), effIntPtr(50000), ContextProviderCap},
		{"native narrower than cap", effIntPtr(50000), effIntPtr(100000), effIntPtr(50000), ContextNative},
		{"equal — provenance stays native", effIntPtr(50000), effIntPtr(50000), effIntPtr(50000), ContextNative},
		{"only native known", effIntPtr(50000), nil, effIntPtr(50000), ContextNative},
		{"only cap known", nil, effIntPtr(50000), effIntPtr(50000), ContextProviderCap},
		{"neither known", nil, nil, nil, ContextUnknown},
		{"zero native treated as unknown", effIntPtr(0), effIntPtr(50000), effIntPtr(50000), ContextProviderCap},
		{"negative native treated as unknown", effIntPtr(-1), effIntPtr(50000), effIntPtr(50000), ContextProviderCap},
		{"zero cap treated as unknown", effIntPtr(50000), effIntPtr(0), effIntPtr(50000), ContextNative},
		{"negative cap treated as unknown", effIntPtr(50000), effIntPtr(-1), effIntPtr(50000), ContextNative},
		{"both non-positive is unknown", effIntPtr(0), effIntPtr(-1), nil, ContextUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotValue, gotProvenance := EffectiveContext(tc.native, tc.cap)
			if (gotValue == nil) != (tc.wantValue == nil) {
				t.Fatalf("EffectiveContext(%v,%v) value = %v, want %v", tc.native, tc.cap, gotValue, tc.wantValue)
			}
			if gotValue != nil && *gotValue != *tc.wantValue {
				t.Fatalf("EffectiveContext(%v,%v) value = %d, want %d", tc.native, tc.cap, *gotValue, *tc.wantValue)
			}
			if gotProvenance != tc.wantProvenance {
				t.Fatalf("EffectiveContext(%v,%v) provenance = %q, want %q", tc.native, tc.cap, gotProvenance, tc.wantProvenance)
			}
		})
	}
}

// TestQualityScore_NeutralWhenUnknown proves nil resolves to the neutral
// 0.5 score, never a gate. MUTATION: returning 0 for a nil rating turns
// this RED.
func TestQualityScore_NeutralWhenUnknown(t *testing.T) {
	cases := []struct {
		name   string
		rating *float64
		want   float64
	}{
		{"unknown", nil, 0.5},
		{"zero", effFloatPtr(0), 0.0},
		{"eighty", effFloatPtr(80), 0.8},
		{"hundred", effFloatPtr(100), 1.0},
		{"below range clamped", effFloatPtr(-10), 0.0},
		{"above range clamped", effFloatPtr(150), 1.0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := QualityScore(tc.rating); got != tc.want {
				t.Fatalf("QualityScore(%v) = %v, want %v", tc.rating, got, tc.want)
			}
		})
	}
}
