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

// Provenance values for EffectiveCapability.Provenance (task-5: capability
// provenance). The empty string is the third closed value — deliberately
// left as a bare "" rather than a named constant, matching
// EffectiveCapability.OfferingOperationID's own "absence is meaningful"
// convention just below.
const (
	ProvenanceProbed   = "probed"
	ProvenanceDeclared = "declared"
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

	// ProvedOperations records, per operation, whether a SUCCEEDED probe run
	// exists for that operation's offering_operations row (task-5). It is
	// supplied by the httpapi assembler from a batched probe_runs query — the
	// projection itself never queries storage. A nil map (or a false/absent
	// entry) means "no succeeded probe run is known", which is the correct
	// fail-closed default: it only ever demotes a non-chat certified
	// capability's provenance to "declared", never fabricates "probed".
	ProvedOperations map[models.Operation]bool
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

	// Provenance is this operation's certification provenance (task-5):
	// "probed" when it was earned by a real runtime probe (chat's runtime
	// usability sweep/fast-lane, ALWAYS — chat has no declared path by
	// construction; or a non-chat operation with a succeeded probe_runs
	// row), "declared" when a non-chat operation was certified by
	// certifyDeclaredCapabilities with no probe evidence, and "" when this
	// operation is not certified+supported at all — provenance only
	// qualifies an earned certification, it is never inferred for anything
	// less than models.Routable's own state+truth combination.
	Provenance string
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
	// MaxInputTokens and MaxOutputTokens are the offering's own declared
	// per-request limits, distinct from the context window: a model may accept
	// a 1M-token context while capping a single reply at 131k. Both are nil
	// when the provider and the catalog are silent. They were persisted at
	// discovery and read by nobody until now.
	MaxInputTokens  *int
	MaxOutputTokens *int
	Capabilities    []EffectiveCapability
	QualityScore    float64
	QualityKnown    bool
	Cost            ResolvedCostFact
	Classification  OfferingClassification
	Tiers           map[Tier]TierEligibility
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
		MaxInputTokens:         in.Offering.MaxInputTokens,
		MaxOutputTokens:        in.Offering.MaxOutputTokens,
		Capabilities:           projectCapabilities(in.NativeCapabilities, in.Offering.Capabilities, in.TransportOperations, in.Certifications, in.ProvedOperations),
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
// operation, fail closed, never "everything supported." proved carries the
// per-operation "a succeeded probe run exists" fact (task-5's provenance
// derivation); it is independent of native/providerExposed/transport/
// effective — provenance is derived from state+truth alone, never from
// whether this offering happens to be routable right now.
func projectCapabilities(native, providerExposed, transport []models.Operation, certs map[models.Operation]models.Certification, proved map[models.Operation]bool) []EffectiveCapability {
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
	// A CANDIDATE operation (discovery.go's DiscoveredModel.CandidateOperations)
	// has a real offering_operations row and certification entry but was
	// deliberately left out of providerExposed/native/transport — the adapter
	// never declared it. It must still surface here: the row is what makes it
	// probeable at all, and Effective/Provenance below are computed from
	// providerExposed/native/transport and state+truth exactly as for any
	// other operation, so a candidate's discovered/unknown row is emitted
	// without being fabricated as declared or effective.
	for op := range certs {
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

		provenance := ""
		if models.Routable(state, truth) {
			switch {
			case op == models.OperationChat:
				// Chat has no declared path by construction — it is only ever
				// certified via the runtime usability probe (sweep/fast-lane)
				// or real use, so a certified+supported chat capability is
				// ALWAYS "probed" regardless of proved's contents.
				provenance = ProvenanceProbed
			case proved[op]:
				provenance = ProvenanceProbed
			default:
				provenance = ProvenanceDeclared
			}
		}

		out = append(out, EffectiveCapability{
			Operation:           op,
			Effective:           effective,
			State:               state,
			Truth:               truth,
			Routable:            models.Routable(state, truth) && effective,
			OfferingOperationID: offeringOperationID,
			Provenance:          provenance,
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
