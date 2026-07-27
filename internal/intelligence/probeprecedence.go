package intelligence

import (
	"fmt"
	"time"
)

// ResolveFact is the entry point callers with probe evidence must use
// instead of Resolve directly (04 §4). precedence.go's Resolve already
// gives probe evidence its rightful ladder position (verified_probe
// outranks provider_metadata/provider_discovery/external_registry),
// already makes owner overrides immune, and already persists proven
// negatives — ResolveFact calls Resolve first and changes nothing about
// any of that. The one rule 04 §4 states that Resolve does not implement
// is "a proven narrower restriction beats a broader positive claim" when
// the two claims are NOT already separated by the ladder (i.e. they tie
// on source rank): among Resolve's winning rank's candidates — excluding
// owner-override and proven-negative evidence, which are never
// second-guessed here — if any carries a numeric value strictly smaller
// than the winner's, that narrower claim wins instead.
//
// Only int, float64, and non-nil *int values are treated as numeric; a
// non-numeric or mixed-type candidate set leaves Resolve's answer
// untouched. Resolve itself remains the general-purpose ladder; this
// wrapper is additive and never edits precedence.go or its test suite.
func ResolveFact(field string, evidence []Evidence, now time.Time) Resolution {
	res := Resolve(field, evidence, now)
	if res.Kind != ResolutionKnown {
		return res
	}
	if res.Winner.Source == SourceOwnerOverride || res.Winner.ProvenNegative {
		return res
	}

	winnerNum, ok := numericEvidenceValue(res.Winner.Value)
	if !ok {
		return res
	}

	narrowest := res.Winner
	narrowestNum := winnerNum
	foundNarrower := false

	for _, e := range evidence {
		if e.Field != field || !e.valid(now) {
			continue
		}
		// No separate owner-override exclusion is needed here: source
		// ranks are unique per source (precedence.go's sourceRank table),
		// so "same rank as the winner" already implies "same source as
		// the winner" — an owner-override winner can only ever meet
		// another owner-override candidate at this point, and the
		// top-level check above is what excludes that case entirely. Only
		// ProvenNegative needs an explicit check here: it is orthogonal to
		// rank and can appear at any source.
		if e.ProvenNegative {
			continue
		}
		if e.Source.Rank() != res.Winner.Source.Rank() {
			continue
		}
		n, ok := numericEvidenceValue(e.Value)
		if !ok {
			continue
		}
		if n < narrowestNum {
			narrowestNum = n
			narrowest = e
			foundNarrower = true
		}
	}

	if !foundNarrower {
		return res
	}
	return Resolution{Kind: ResolutionKnown, Value: narrowest.Value, Winner: narrowest, Reason: res.Reason}
}

// numericEvidenceValue reports the numeric interpretation of an Evidence
// value, covering exactly the shapes this repo's probes and discovery
// produce: int (e.g. the context probe's limit), float64, and non-nil
// *int. Anything else (string, bool, nil *int, ...) is "not numeric",
// and ResolveFact's narrowing rule never applies to it.
func numericEvidenceValue(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case float64:
		return n, true
	case *int:
		if n == nil {
			return 0, false
		}
		return float64(*n), true
	default:
		return 0, false
	}
}

// MergeProbeEvidence returns a new slice — existing's entries first, then
// probe's — never mutating either input, with exact duplicates (same
// Field, Scope, Source, Verification, Value, and ObservedAt) collapsed to
// their first occurrence. The resulting order is an input to Resolve's
// deterministic tie-break chain (04 §4's freshness/specificity/final
// steps), never itself a precedence signal — Resolve, and ResolveFact
// above it, are what decide precedence.
func MergeProbeEvidence(existing []Evidence, probe []Evidence) []Evidence {
	out := make([]Evidence, 0, len(existing)+len(probe))
	seen := make(map[string]bool, len(existing)+len(probe))

	add := func(list []Evidence) {
		for _, e := range list {
			key := evidenceDedupeKey(e)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, e)
		}
	}
	add(existing)
	add(probe)
	return out
}

// evidenceDedupeKey is MergeProbeEvidence's exact-duplicate key: Field,
// Scope, Source, Verification, Value, and ObservedAt together.
func evidenceDedupeKey(e Evidence) string {
	return e.Field + "\x00" + e.Scope.AccountID + "\x00" + e.Scope.ProviderModelID + "\x00" +
		string(e.Source) + "\x00" + string(e.Verification) + "\x00" +
		fmt.Sprint(e.Value) + "\x00" + e.ObservedAt.UTC().Format(time.RFC3339Nano)
}
