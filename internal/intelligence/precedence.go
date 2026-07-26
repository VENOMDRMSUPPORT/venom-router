package intelligence

import (
	"errors"
	"fmt"
	"time"
)

// EvidenceSource is the evidence-precedence ladder (04 §4), higher wins:
// owner_override > verified_probe > provider_metadata > provider_discovery
// > external_registry > heuristic > unknown.
type EvidenceSource string

const (
	SourceOwnerOverride     EvidenceSource = "owner_override"
	SourceVerifiedProbe     EvidenceSource = "verified_probe"
	SourceProviderMetadata  EvidenceSource = "provider_metadata"
	SourceProviderDiscovery EvidenceSource = "provider_discovery"
	SourceExternalRegistry  EvidenceSource = "external_registry"
	SourceHeuristic         EvidenceSource = "heuristic"
	SourceUnknown           EvidenceSource = "unknown"
)

// ErrUnknownEvidenceSource is returned by ParseEvidenceSource for any value
// outside the fixed seven-rung ladder.
var ErrUnknownEvidenceSource = errors.New("intelligence: unrecognized evidence source")

// sourceRank assigns each ladder rung an explicit rank; higher wins.
var sourceRank = map[EvidenceSource]int{
	SourceOwnerOverride:     6,
	SourceVerifiedProbe:     5,
	SourceProviderMetadata:  4,
	SourceProviderDiscovery: 3,
	SourceExternalRegistry:  2,
	SourceHeuristic:         1,
	SourceUnknown:           0,
}

// ParseEvidenceSource fails closed on any value outside the exact
// seven-rung ladder.
func ParseEvidenceSource(s string) (EvidenceSource, error) {
	if _, ok := sourceRank[EvidenceSource(s)]; ok {
		return EvidenceSource(s), nil
	}
	return "", fmt.Errorf("%w: %q", ErrUnknownEvidenceSource, s)
}

// Rank returns s's explicit ladder rank; higher wins. An unrecognized
// EvidenceSource ranks as SourceUnknown's rank (0) — the lowest rung —
// rather than panicking.
func (s EvidenceSource) Rank() int {
	return sourceRank[s]
}

// VerificationStatus is how an individual piece of evidence was obtained
// (04 §4 tie-break): verified > observed > declared.
type VerificationStatus string

const (
	VerificationVerified VerificationStatus = "verified"
	VerificationObserved VerificationStatus = "observed"
	VerificationDeclared VerificationStatus = "declared"
)

// ErrUnknownVerificationStatus is returned by ParseVerificationStatus for
// any value outside the three-value vocabulary.
var ErrUnknownVerificationStatus = errors.New("intelligence: unrecognized verification status")

var verificationRank = map[VerificationStatus]int{
	VerificationVerified: 3,
	VerificationObserved: 2,
	VerificationDeclared: 1,
}

// ParseVerificationStatus fails closed on any value outside the exact
// three-value vocabulary.
func ParseVerificationStatus(s string) (VerificationStatus, error) {
	if _, ok := verificationRank[VerificationStatus(s)]; ok {
		return VerificationStatus(s), nil
	}
	return "", fmt.Errorf("%w: %q", ErrUnknownVerificationStatus, s)
}

// Scope is enough information to compare two pieces of evidence for
// specificity (04 §4's final tie-break dimension): an account- and
// model-scoped claim is more specific than a provider-wide or
// registry-wide one.
type Scope struct {
	AccountID       string
	ProviderModelID string
}

// Specificity returns a higher value for a more specific scope. An empty
// field means "not scoped to this dimension" (broader).
func (s Scope) Specificity() int {
	n := 0
	if s.ProviderModelID != "" {
		n++
	}
	if s.AccountID != "" {
		n++
	}
	return n
}

// Evidence is one claim about a field/scope (04 §2b / §4 provenance): its
// source and verification status, a confidence in [0,1], when it was
// observed, whether it is a proven-negative claim, and the claimed value.
// DatasetVersion and ExactIdentityMatch carry the rest of 04 §2b's
// provenance tuple. Value, DatasetVersion, and any scope identifiers are
// id/code/scalar only — never secret material or an upstream response
// body.
type Evidence struct {
	Field          string
	Scope          Scope
	Source         EvidenceSource
	Verification   VerificationStatus
	Confidence     float64
	ObservedAt     time.Time
	ProvenNegative bool
	Value          any

	DatasetVersion     string
	ExactIdentityMatch bool
}

// valid reports whether e is well-formed enough to participate in
// resolution: a real (non-unknown) source, a confidence in [0,1], and an
// ObservedAt that is neither zero nor in the future relative to now.
func (e Evidence) valid(now time.Time) bool {
	if e.Source == SourceUnknown || e.Source == "" {
		return false
	}
	if e.Confidence < 0 || e.Confidence > 1 {
		return false
	}
	if e.ObservedAt.IsZero() || e.ObservedAt.After(now) {
		return false
	}
	return true
}

// ResolutionKind is Resolve's outcome shape: a known value, an explicit
// unknown, or an explicit "probe suggested" (heuristic evidence can never
// itself be the winning, certification-grade resolution — 04 §4).
type ResolutionKind string

const (
	ResolutionKnown          ResolutionKind = "known"
	ResolutionUnknown        ResolutionKind = "unknown"
	ResolutionProbeSuggested ResolutionKind = "probe_suggested"
)

// ReasonCode is a typed reason accompanying a Resolution.
type ReasonCode string

const (
	ReasonResolved               ReasonCode = "resolved"
	ReasonNoEvidence             ReasonCode = "no_evidence"
	ReasonAllDisqualified        ReasonCode = "all_disqualified"
	ReasonHeuristicCannotCertify ReasonCode = "heuristic_cannot_certify"
)

// Resolution is Resolve's result: either a known value with its winning
// evidence (provenance), or an explicit unknown/probe-suggested outcome
// with a typed reason. A zero-value Resolution is never presented as a
// fact — Kind must always be checked.
type Resolution struct {
	Kind   ResolutionKind
	Value  any
	Winner Evidence
	Reason ReasonCode
}

// Resolve applies 04 §4's evidence-precedence rules to decide field's
// winning value among evidence, as of now (the injected clock; also used
// to reject evidence observed in the future). It never returns a
// fabricated value and never a winner without provenance.
//
// Resolution proceeds in four stages:
//
//  1. Filter to evidence matching field, then to well-formed evidence
//     (valid). Empty input, or a fully-disqualified set, resolves to
//     ResolutionUnknown.
//  2. Owner-override immunity: if any owner_override evidence remains, it
//     wins outright over everything else — owner overrides are never
//     auto-overwritten by any other source, no matter how fresh or
//     confident. Multiple owner_override candidates are decided among
//     themselves by the normal tie-break chain (step 4).
//  3. Proven-negative persistence: among the remaining (non-owner)
//     evidence, the best proven-negative claim (if any) wins over every
//     positive claim whose verification status is lower-or-equal to its
//     own. Only a positive with a STRICTLY higher verification status
//     revalidates the field, in which case the negative is dropped from
//     further consideration.
//  4. Otherwise, the winner is decided by source rank (the ladder), then
//     the tie-break chain: verification status -> confidence ->
//     freshness -> scope specificity -> a final deterministic tiebreak.
//     If the resulting winner is heuristic-sourced, the outcome is
//     downgraded to ResolutionProbeSuggested — heuristics may schedule a
//     probe but can never certify.
func Resolve(field string, evidence []Evidence, now time.Time) Resolution {
	var matched []Evidence
	for _, e := range evidence {
		if e.Field == field {
			matched = append(matched, e)
		}
	}
	if len(matched) == 0 {
		return Resolution{Kind: ResolutionUnknown, Reason: ReasonNoEvidence}
	}

	var candidates []Evidence
	for _, e := range matched {
		if e.valid(now) {
			candidates = append(candidates, e)
		}
	}
	if len(candidates) == 0 {
		return Resolution{Kind: ResolutionUnknown, Reason: ReasonAllDisqualified}
	}

	var owners, rest []Evidence
	for _, e := range candidates {
		if e.Source == SourceOwnerOverride {
			owners = append(owners, e)
		} else {
			rest = append(rest, e)
		}
	}
	if len(owners) > 0 {
		winner := pickBest(owners)
		return Resolution{Kind: ResolutionKnown, Value: winner.Value, Winner: winner, Reason: ReasonResolved}
	}

	var negatives, positives []Evidence
	for _, e := range rest {
		if e.ProvenNegative {
			negatives = append(negatives, e)
		} else {
			positives = append(positives, e)
		}
	}

	if len(negatives) > 0 {
		champNeg := pickBest(negatives)
		var revalidating []Evidence
		for _, p := range positives {
			if verificationRank[p.Verification] > verificationRank[champNeg.Verification] {
				revalidating = append(revalidating, p)
			}
		}
		if len(revalidating) == 0 {
			return Resolution{Kind: ResolutionKnown, Value: champNeg.Value, Winner: champNeg, Reason: ReasonResolved}
		}
		positives = revalidating
	}

	if len(positives) == 0 {
		// Only disqualified-by-negative-rule candidates remain (should be
		// unreachable given the branches above, but fail closed).
		return Resolution{Kind: ResolutionUnknown, Reason: ReasonAllDisqualified}
	}

	champ := pickBest(positives)
	if champ.Source == SourceHeuristic {
		return Resolution{Kind: ResolutionProbeSuggested, Winner: champ, Reason: ReasonHeuristicCannotCertify}
	}
	return Resolution{Kind: ResolutionKnown, Value: champ.Value, Winner: champ, Reason: ReasonResolved}
}

// pickBest reduces a non-empty, homogeneous (all-owner, all-negative, or
// all-positive) evidence slice to its single champion via the standard
// ladder + tie-break chain. The comparison is a strict total order (source
// rank, then verification, confidence, freshness, specificity, and a
// final deterministic tiebreak over fixed fields), so the result never
// depends on slice order.
func pickBest(list []Evidence) Evidence {
	best := list[0]
	for _, e := range list[1:] {
		if evidenceBetter(e, best) {
			best = e
		}
	}
	return best
}

// evidenceBetter reports whether a outranks b under the standard ladder +
// tie-break chain (source rank -> verification -> confidence -> freshness
// -> scope specificity -> final deterministic tiebreak).
func evidenceBetter(a, b Evidence) bool {
	if a.Source.Rank() != b.Source.Rank() {
		return a.Source.Rank() > b.Source.Rank()
	}
	if verificationRank[a.Verification] != verificationRank[b.Verification] {
		return verificationRank[a.Verification] > verificationRank[b.Verification]
	}
	if a.Confidence != b.Confidence {
		return a.Confidence > b.Confidence
	}
	if !a.ObservedAt.Equal(b.ObservedAt) {
		return a.ObservedAt.After(b.ObservedAt)
	}
	if a.Scope.Specificity() != b.Scope.Specificity() {
		return a.Scope.Specificity() > b.Scope.Specificity()
	}
	return finalTiebreakKey(a) > finalTiebreakKey(b)
}

// finalTiebreakKey produces the last-resort deterministic tiebreak: a
// composite string over fixed, order-independent fields (source, dataset
// version, the claimed value's string form, and the observation instant).
// It only needs to be a strict, order-independent total order — not
// meaningful — since every prior stage has already failed to distinguish
// the two candidates.
func finalTiebreakKey(e Evidence) string {
	return string(e.Source) + "|" + e.DatasetVersion + "|" + fmt.Sprint(e.Value) + "|" + e.ObservedAt.UTC().Format(time.RFC3339Nano)
}
