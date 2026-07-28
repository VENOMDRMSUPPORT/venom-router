package routing

// ApplyCompetitiveBand applies Step-6's fixed competitive band (05 §2 Step 6,
// §8.5) to a scored group set:
//
//   - Lite (!policy.Scored, BandWidth==0): return the input unchanged — Lite
//     has no band; this function must not touch Lite scores.
//   - Empty input: return empty output (no panic on max over zero elements).
//   - Pro/Max: find topQuality = max(s.QualityFactor); keep only scores where
//     topQuality - s.QualityFactor <= policy.BandWidth (boundary inclusive).
//     If the kept set has fewer than 2 entries, return the ORIGINAL input
//     unchanged — the band is never auto-widened; this is an invariant, not
//     a fallback heuristic.
func ApplyCompetitiveBand(scores []GroupScore, policy TierPolicy) []GroupScore {
	if len(scores) == 0 {
		return scores
	}

	if !policy.Scored {
		return scores
	}

	// Find the top quality factor.
	topQuality := scores[0].QualityFactor
	for _, s := range scores[1:] {
		if s.QualityFactor > topQuality {
			topQuality = s.QualityFactor
		}
	}

	// Keep only in-band entries (boundary inclusive: <= BandWidth).
	kept := make([]GroupScore, 0, len(scores))
	for _, s := range scores {
		if topQuality-s.QualityFactor <= policy.BandWidth {
			kept = append(kept, s)
		}
	}

	// Fewer than 2 in-band: never auto-widen — return original.
	if len(kept) < 2 {
		return scores
	}

	return kept
}
