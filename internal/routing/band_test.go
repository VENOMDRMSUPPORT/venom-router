package routing

import (
	"testing"
)

// bandScore builds a minimal GroupScore with only QualityFactor set.
func bandScore(quality float64) GroupScore {
	return GroupScore{QualityFactor: quality}
}

// TestApplyCompetitiveBand_BoundaryInclusive tests Pro and Max bands with
// three entries: one at the top, one exactly at the boundary (kept), one
// just outside (dropped). Uses REAL Policies() band widths so the test
// cannot drift from the source of truth.
//
// Mutation row B-M1: change <= to < on the band comparison → the
// boundary-equal entry is dropped instead of kept → restore.
func TestApplyCompetitiveBand_BoundaryInclusive(t *testing.T) {
	ps, err := Policies()
	if err != nil {
		t.Fatalf("Policies: %v", err)
	}

	tests := []struct {
		tier Tier
	}{
		{TierPro},
		{TierMax},
	}

	for _, tc := range tests {
		t.Run(string(tc.tier), func(t *testing.T) {
			policy := ps[tc.tier]
			bw := policy.BandWidth

			// Use top = 2×bw and boundary = bw so that (top - boundary = bw)
			// is exact by Sterbenz's lemma (no floating-point rounding on the
			// subtraction). outsideBand = 0 is clearly 2×bw below top.
			top := 2.0 * bw    // exactly representable (×2 = exponent shift)
			atBoundary := bw   // exactly bw below top by Sterbenz
			outsideBand := 0.0 // 2×bw below top — clearly outside

			scores := []GroupScore{
				bandScore(top),
				bandScore(atBoundary),
				bandScore(outsideBand),
			}

			result := ApplyCompetitiveBand(scores, policy)

			if len(result) != 2 {
				t.Fatalf("tier %s (band=%.3f): got %d results, want 2 (top + boundary)", tc.tier, bw, len(result))
			}
			// top must be present
			foundTop := false
			foundBoundary := false
			for _, s := range result {
				if approxEq(s.QualityFactor, top) {
					foundTop = true
				}
				if approxEq(s.QualityFactor, atBoundary) {
					foundBoundary = true
				}
			}
			if !foundTop {
				t.Fatalf("tier %s: top entry missing from result", tc.tier)
			}
			if !foundBoundary {
				t.Fatalf("tier %s: boundary-equal entry missing from result", tc.tier)
			}
		})
	}
}

// TestApplyCompetitiveBand_NeverWidens verifies that when only ONE entry
// survives the band filter the ORIGINAL input is returned, not the
// one-element filtered set.
//
// Mutation row B-M2: return the one-element filtered set instead of original
// → len(result)==1 instead of the original count → restore.
func TestApplyCompetitiveBand_NeverWidens(t *testing.T) {
	ps, err := Policies()
	if err != nil {
		t.Fatalf("Policies: %v", err)
	}
	policy := ps[TierPro]
	bw := policy.BandWidth

	// Two entries: one at top, one far outside the band — only top passes,
	// so the kept set has 1 entry → should return original.
	scores := []GroupScore{
		bandScore(0.90),
		bandScore(0.90 - bw - 0.1), // well outside
	}

	result := ApplyCompetitiveBand(scores, policy)

	if len(result) != len(scores) {
		t.Fatalf("never-widen: got len=%d, want original len=%d", len(result), len(scores))
	}
	// Verify the out-of-band entry is present (not filtered out).
	found := false
	for _, s := range result {
		if approxEq(s.QualityFactor, 0.90-bw-0.1) {
			found = true
		}
	}
	if !found {
		t.Fatalf("never-widen: out-of-band entry missing from returned set")
	}
}

// TestApplyCompetitiveBand_LitePassthrough verifies Lite scores are returned
// unchanged regardless of their QualityFactor spread.
//
// GOVERNOR CORRECTION: the original version of this test used quality
// values {1.0, 0.5, 0.0} against Lite's BandWidth=0. That data is vacuous
// against mutation B-M3 (removing the `!policy.Scored` short-circuit):
// with band=0, only the top (1.0) entry satisfies the generic band filter,
// so the generic "fewer than two in-band → return original" fallback
// ALSO returns all 3 entries unchanged — passing for the wrong reason and
// masking the short-circuit's removal entirely (confirmed by hand: the
// mutated code passed this exact data). The corrected data below ties two
// entries at the top quality value, so the generic filter alone would
// keep exactly those 2 (satisfying the plain "len(kept) >= 2" path) and
// silently drop the third — a real, visible divergence from "unchanged"
// that only the explicit Lite short-circuit prevents.
//
// Mutation row B-M3: apply the band to Lite (remove the !policy.Scored check)
// → with 2 entries tied at top quality, the generic filter keeps only
// those 2 and drops the third → len(result) == 2 ≠ 3 → restore.
func TestApplyCompetitiveBand_LitePassthrough(t *testing.T) {
	ps, err := Policies()
	if err != nil {
		t.Fatalf("Policies: %v", err)
	}
	policy := ps[TierLite]

	scores := []GroupScore{
		bandScore(1.0),
		bandScore(1.0), // tied with the top — the generic filter would ALSO keep this
		bandScore(0.5), // NOT tied — only the explicit Lite short-circuit preserves this
	}

	result := ApplyCompetitiveBand(scores, policy)

	if len(result) != len(scores) {
		t.Fatalf("Lite passthrough: got %d results, want %d (all input unchanged)", len(result), len(scores))
	}
	for i, s := range result {
		if !approxEq(s.QualityFactor, scores[i].QualityFactor) {
			t.Fatalf("Lite passthrough: index %d quality %v ≠ input %v", i, s.QualityFactor, scores[i].QualityFactor)
		}
	}
}

// TestApplyCompetitiveBand_EmptyInput verifies empty input produces empty
// output without panic.
func TestApplyCompetitiveBand_EmptyInput(t *testing.T) {
	ps, err := Policies()
	if err != nil {
		t.Fatalf("Policies: %v", err)
	}
	for _, tier := range []Tier{TierLite, TierPro, TierMax} {
		result := ApplyCompetitiveBand([]GroupScore{}, ps[tier])
		if len(result) != 0 {
			t.Fatalf("tier %s: empty input → got %d results, want 0", tier, len(result))
		}
	}
}
