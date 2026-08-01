package intelligence

import (
	"sort"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
)

// Typed reason codes for TierEligibility.Reasons (04 §2b/§3). CatalogOnly
// is used for BOTH ClassificationCatalogOnly and ClassificationUnclassified
// — CERT-002's classifications are both "not a routing candidate," even
// though only one of them is media-only in the strict sense.
const (
	ReasonCostIneligible = "cost_ineligible"
	ReasonContextUnknown = "context_unknown"
	ReasonNotAvailable   = "not_available"
	ReasonCatalogOnly    = "catalog_only"
)

// ProjectionInput is everything Project needs to build one offering's
// EffectiveOffering. Canonical is consumed as-is — Project never adds
// fields to models.CanonicalModel/Offering/Certification, it only reads
// them. A nil NativeCapabilities/TransportOperations means UNKNOWN and a
// non-nil empty slice means KNOWN-NONE; both fail closed to "no operation
// is effective", so Project treats them identically by design — unproven
// support is never routable, exactly as an absence of support is not.
type ProjectionInput struct {
	ProviderID          string
	Canonical           models.CanonicalModel
	NativeCapabilities  []models.Operation
	Offering            models.Offering
	TransportOperations []models.Operation
	Certifications      map[models.Operation]models.Certification
	Cost                ResolvedCostFact
	Classification      OfferingClassification
}

// EffectiveCapability is one operation's fully-resolved routing status.
type EffectiveCapability struct {
	Operation models.Operation
	Effective bool
	State     models.CertificationState
	Truth     models.CapabilityTruth
	Routable  bool

	// OfferingOperationID identifies the offering_operations row this
	// capability's certification belongs to — the key POST
	// /offerings/{id}/probe is addressed by.
	//
	// It is read verbatim from the models.Certification in ProjectionInput and is
	// EMPTY when this operation has no certification row. That absence is
	// meaningful, not a gap: an operation reachable only through native or
	// transport support has no offering_operations row, so there is nothing to
	// probe. Callers must treat "" as "not probeable" and never substitute a
	// composed or borrowed id — one that pointed at a different row would probe
	// the wrong operation.
	OfferingOperationID string
}

// TierEligibility is one tier's final eligibility decision plus the
// informational flags/reasons behind it. Reasons is nil exactly when
// Eligible is true, and sorted ascending otherwise.
type TierEligibility struct {
	Eligible bool
	Stale    bool
	Penalty  bool
	Reasons  []string
}

// EffectiveOffering is 04 §3's ONE shared effective-offering projection:
// the dashboard, the router, and diagnostics all read this — and only
// this — for context, capabilities, quality, and tier eligibility. No
// consumer may re-derive any of these independently.
type EffectiveOffering struct {
	Identity               models.OfferingIdentity
	ProviderID             string
	DisplayName            string
	Availability           models.Availability
	EffectiveContextTokens *int
	ContextProvenance      models.ContextProvenance
	Capabilities           []EffectiveCapability
	QualityScore           float64
	QualityKnown           bool
	Cost                   ResolvedCostFact
	Classification         OfferingClassification
	Tiers                  map[Tier]TierEligibility
}

// Project builds THE single shared effective-offering projection (04 §3):
// effective context via models.EffectiveContext, effective capability as
// the three-way intersection of native support, provider exposure, and
// transport support, quality via models.QualityScore, and per-tier
// eligibility by calling Eligible (04 §2b's table) and then narrowing it
// with three independent, subtract-only gates (context, availability,
// classification). No consumer of EffectiveOffering may re-derive any of
// these facts — this is the one place they are computed.
func Project(in ProjectionInput) EffectiveOffering {
	contextTokens, provenance := models.EffectiveContext(in.Canonical.NativeContextTokens, in.Offering.ContextLength)

	out := EffectiveOffering{
		Identity:               in.Offering.Identity,
		ProviderID:             in.ProviderID,
		DisplayName:            in.Canonical.DisplayName,
		Availability:           in.Offering.Availability,
		EffectiveContextTokens: contextTokens,
		ContextProvenance:      provenance,
		Capabilities:           projectCapabilities(in.NativeCapabilities, in.Offering.Capabilities, in.TransportOperations, in.Certifications),
		QualityScore:           models.QualityScore(in.Canonical.QualityRating),
		QualityKnown:           in.Canonical.QualityRating != nil,
		Cost:                   in.Cost,
		Classification:         in.Classification,
		Tiers:                  make(map[Tier]TierEligibility, 3),
	}

	for _, tier := range []Tier{TierLite, TierPro, TierMax} {
		out.Tiers[tier] = projectTierEligibility(in, tier, contextTokens)
	}

	return out
}

// projectCapabilities emits one EffectiveCapability per operation in the
// union of native/provider/transport support, in models.Operations()
// order (deterministic regardless of any map iteration elsewhere). A nil
// native or nil transport set means UNKNOWN — nothing is effective for any
// operation, fail closed, never "everything supported."
func projectCapabilities(native, providerExposed, transport []models.Operation, certs map[models.Operation]models.Certification) []EffectiveCapability {
	union := make(map[models.Operation]bool)
	for _, op := range native {
		union[op] = true
	}
	for _, op := range providerExposed {
		union[op] = true
	}
	for _, op := range transport {
		union[op] = true
	}

	var out []EffectiveCapability
	for _, op := range models.Operations() {
		if !union[op] {
			continue
		}

		effective := native != nil && transport != nil &&
			containsOperation(native, op) &&
			containsOperation(providerExposed, op) &&
			containsOperation(transport, op)

		state := models.CertDiscovered
		truth := models.TruthUnknown
		// Empty unless THIS operation has its own certification row — never
		// carried over from another operation's row.
		offeringOperationID := ""
		if cert, ok := certs[op]; ok {
			state = cert.State
			truth = cert.Truth
			offeringOperationID = cert.OfferingOperationID
		}

		out = append(out, EffectiveCapability{
			Operation:           op,
			Effective:           effective,
			State:               state,
			Truth:               truth,
			Routable:            models.Routable(state, truth) && effective,
			OfferingOperationID: offeringOperationID,
		})
	}
	return out
}

// projectTierEligibility starts from Eligible's 04 §2b table decision
// (never re-derived) and applies three independent gates that can only
// SUBTRACT eligibility. Stale/Penalty are copied from Eligible verbatim —
// these extra gates affect only Eligible/Reasons.
func projectTierEligibility(in ProjectionInput, tier Tier, contextTokens *int) TierEligibility {
	base := Eligible(in.Cost, tier)

	eligible := base.Eligible
	var reasons []string

	if !base.Eligible {
		reasons = append(reasons, ReasonCostIneligible)
	}
	if contextTokens == nil {
		eligible = false
		reasons = append(reasons, ReasonContextUnknown)
	}
	if in.Offering.Availability != models.AvailabilityAvailable {
		eligible = false
		reasons = append(reasons, ReasonNotAvailable)
	}
	if in.Classification != ClassificationRoutableCandidate {
		eligible = false
		reasons = append(reasons, ReasonCatalogOnly)
	}

	sort.Strings(reasons)
	if eligible {
		reasons = nil
	}

	return TierEligibility{
		Eligible: eligible,
		Stale:    base.Stale,
		Penalty:  base.Penalty,
		Reasons:  reasons,
	}
}
