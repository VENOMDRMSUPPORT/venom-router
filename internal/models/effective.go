package models

// ContextProvenance records which source determined EffectiveContext's
// resolved value (04 §3): the canonical native fact, the offering's
// provider-declared cap, or neither (unknown).
type ContextProvenance string

const (
	ContextUnknown     ContextProvenance = "unknown"
	ContextNative      ContextProvenance = "native"
	ContextProviderCap ContextProvenance = "provider_cap"
)

// EffectiveContext resolves a model's effective context window from the
// canonical native fact and the offering's provider-declared cap (04 §3):
//
//   - both known → min(native, cap); provenance is ContextProviderCap only
//     when the cap STRICTLY narrows (cap < native) — an equal or wider cap
//     never overwrites the native value, it only ever narrows it.
//   - only one known → that value, with the matching provenance.
//   - neither known → (nil, ContextUnknown).
//
// A non-nil but non-positive input (<= 0) is treated as UNKNOWN, never as
// a real value (04 §2: "a zero/negative declared limit fails the record
// rather than being stored") — this mirrors P3a-DISC-002/003's identical
// fail-closed rule for a declared numeric limit.
func EffectiveContext(native, providerCap *int) (*int, ContextProvenance) {
	n := positiveOrNil(native)
	c := positiveOrNil(providerCap)

	switch {
	case n != nil && c != nil:
		if *c < *n {
			v := *c
			return &v, ContextProviderCap
		}
		v := *n
		return &v, ContextNative
	case n != nil:
		v := *n
		return &v, ContextNative
	case c != nil:
		v := *c
		return &v, ContextProviderCap
	default:
		return nil, ContextUnknown
	}
}

func positiveOrNil(v *int) *int {
	if v == nil || *v <= 0 {
		return nil
	}
	return v
}

// QualityScore derives 04 §3's ranking score from a canonical model's
// quality rating: rating/100 (clamped to [0,1]) when known, and exactly
// 0.5 — a neutral score, never a gate — when rating is nil ("no quality
// signal available ... the model remains routable but receives a neutral
// (0.5) ranking score").
func QualityScore(rating *float64) float64 {
	if rating == nil {
		return 0.5
	}
	switch r := *rating; {
	case r < 0:
		return 0
	case r > 100:
		return 1
	default:
		return r / 100
	}
}
