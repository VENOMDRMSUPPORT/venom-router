package intelligence

import (
	"errors"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
)

// OfferingClassification is a routing-candidacy classification for an
// offering, separate from — and never a substitute for — the
// certification-state machine in internal/models (04 §5's final
// paragraph). catalog_only is deliberately NOT a seventh
// models.CertificationState: the frozen M4 certifications.status CHECK
// enumerates exactly six values, and adding a seventh here would violate
// both that constraint and the "exactly six, no rejected" invariant (04
// §5). This type instead models catalog_only as an independent,
// offering-level dimension.
type OfferingClassification string

const (
	// ClassificationRoutableCandidate is any offering not proven
	// media-only: it may or may not ultimately route (funding, quota,
	// certification, and every other routing-required fact still apply),
	// but it is not permanently excluded at this classification layer.
	ClassificationRoutableCandidate OfferingClassification = "routable_candidate"
	// ClassificationCatalogOnly is the terminal state for a media-only
	// offering (04 §5): visible, never entering the tiers, never counted
	// as a failure.
	ClassificationCatalogOnly OfferingClassification = "catalog_only"
	// ClassificationUnclassified is an offering with no operation
	// evidence and no native-modality evidence yet — distinct from
	// ClassificationCatalogOnly so "unknown, so ineligible" is never
	// mislabelled as "media-only, so catalog_only".
	ClassificationUnclassified OfferingClassification = "unclassified"
)

// ExclusionReasonKind distinguishes an informational routing exclusion
// (never a failure, error, probe failure, or health signal) from a
// genuine failure. catalog_only and the unclassified/unknown case are
// both informational.
type ExclusionReasonKind string

const (
	ExclusionInformational ExclusionReasonKind = "informational"
	ExclusionFailure       ExclusionReasonKind = "failure"
)

// Typed reason codes for ClassificationResult.Reason.
const (
	ReasonCodeMediaOnly    = "media_only"
	ReasonCodeNoOperations = "no_operations_declared"
)

// ExclusionReason is a typed, kind-tagged reason an offering is not
// (currently) a routing candidate.
type ExclusionReason struct {
	Kind ExclusionReasonKind
	Code string
}

// ClassificationResult is Classify's / Reclassify's outcome: a
// classification, whether the offering is presently a routing candidate
// at this layer, and — when it is not — a typed reason.
type ClassificationResult struct {
	Classification   OfferingClassification
	RoutingCandidate bool
	Reason           *ExclusionReason
}

// Classify derives an offering's classification from its discovered
// operation set and (when the operation set alone cannot prove it) its
// canonical model's native modalities — using only internal/models'
// vocabulary, never a hardcoded model name or provider special case.
//
// Any offering exposing models.OperationChat is a routing candidate
// outright. Absent chat, an offering is catalog_only when its operations
// are proven to be only image_generation, or — since embeddings,
// translation, and audio have no corresponding entry in the fixed
// Operation vocabulary — when its native modalities are known and
// contain no "text" entry. An empty operation set with no modality
// evidence either is genuinely unknown (ClassificationUnclassified), not
// catalog_only: this keeps "unknown, so ineligible" distinguishable from
// "media-only, so catalog_only" per 04 §5.
func Classify(operations []models.Operation, nativeModalities []string) ClassificationResult {
	for _, op := range operations {
		if op == models.OperationChat {
			return ClassificationResult{Classification: ClassificationRoutableCandidate, RoutingCandidate: true}
		}
	}

	if len(operations) > 0 && allImageGeneration(operations) {
		return catalogOnlyResult()
	}
	if len(nativeModalities) > 0 && !containsModality(nativeModalities, "text") {
		return catalogOnlyResult()
	}

	if len(operations) == 0 {
		return ClassificationResult{
			Classification:   ClassificationUnclassified,
			RoutingCandidate: false,
			Reason:           &ExclusionReason{Kind: ExclusionInformational, Code: ReasonCodeNoOperations},
		}
	}

	// Non-empty operations, no chat, not proven media-only (e.g.
	// vision/tools/structured_output without chat): those operations are
	// independently certifiable/routable, so this offering remains a
	// routing candidate at this classification layer.
	return ClassificationResult{Classification: ClassificationRoutableCandidate, RoutingCandidate: true}
}

func catalogOnlyResult() ClassificationResult {
	return ClassificationResult{
		Classification:   ClassificationCatalogOnly,
		RoutingCandidate: false,
		Reason:           &ExclusionReason{Kind: ExclusionInformational, Code: ReasonCodeMediaOnly},
	}
}

func allImageGeneration(operations []models.Operation) bool {
	for _, op := range operations {
		if op != models.OperationImageGeneration {
			return false
		}
	}
	return true
}

// containsModality reports whether modalities contains token, exact
// match only — no case folding or normalization, which would risk
// inventing a capability that was never actually observed.
func containsModality(modalities []string, token string) bool {
	for _, m := range modalities {
		if m == token {
			return true
		}
	}
	return false
}

// ErrCatalogOnlyIsTerminal is returned by Reclassify when previous is
// already ClassificationCatalogOnly: no re-derivation, however different
// its input evidence, may move a catalog_only offering back to
// routable_candidate (04 §5: catalog_only is terminal).
var ErrCatalogOnlyIsTerminal = errors.New("intelligence: catalog_only classification is terminal and cannot revert")

// Reclassify re-derives an offering's classification from fresh evidence
// via Classify, then enforces catalog_only's terminality: if previous is
// already ClassificationCatalogOnly, the call is rejected outright rather
// than silently re-deriving a routable_candidate result. This is a
// guard on top of a stateless derivation, not a state-machine
// transition — Classify itself never remembers or depends on a prior
// classification.
func Reclassify(previous OfferingClassification, operations []models.Operation, nativeModalities []string) (ClassificationResult, error) {
	if previous == ClassificationCatalogOnly {
		return ClassificationResult{}, ErrCatalogOnlyIsTerminal
	}
	return Classify(operations, nativeModalities), nil
}
