package routing

import (
	accountsdomain "github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
)

// CandidateOffering is one account's exposure of a model offering, carrying
// everything Steps 2–6 of the per-request selection algorithm need (05 §2).
// It is assembled by a later wiring unit; this package only consumes it.
type CandidateOffering struct {
	ProviderID      string
	AccountID       string
	ProviderModelID string

	// Funding is inherited from the account (02 §2): there is no per-offering
	// override. BuildCandidatePool carries it verbatim and never inspects it;
	// the funding gate lives in ApplyHardGates (Step 3).
	Funding         accountsdomain.Funding
	AccountHealth   accountsdomain.HealthState
	CredentialValid bool
	Cooling         bool

	// VerifiedContextTokens is the offering's verified context ceiling (tokens).
	// nil means unknown — ApplyHardGates treats unknown as ineligible (fail closed).
	VerifiedContextTokens *int64

	// Certifications holds the certified state for every operation certified on
	// this offering (chat, streaming, tools, structured_output, vision).
	Certifications map[models.Operation]models.Certification

	// ReasoningCertified and ReasoningCertifiedMax carry reasoning's capability
	// truth OUTSIDE Certifications because models.Operation has no "reasoning"
	// value — a documented vocabulary gap (see capability.go, 05 §2 Step 3).
	ReasoningCertified    bool
	ReasoningCertifiedMax ThinkingLevel

	// Step-5 scoring inputs (05 §2 Step 5). QualityRating is the raw 0–100
	// canonical rating; models.QualityScore converts it in scoring.go. The
	// other five arrive pre-normalized to [0,1], nil when unknown (neutral 0.5
	// applied by ScoreGroups).
	QualityRating      *float64
	Reliability        *float64
	QuotaHeadroom      *float64
	EvidenceConfidence *float64
	CostClass          *float64
	LatencyScore       *float64

	// QuotaWindows holds this account's applicable quota windows (provider
	// evidence + mandatory local-safety), consumed by Max's Step-7 DRR
	// distribution (05 §2 Step 7 stage 2). A nil/empty slice means "no window
	// data" — treated as UNKNOWN and therefore SATURATED (fail closed), never
	// as unlimited capacity. Steps 2–6 ignore this field; only SelectMaxAccount
	// reads it. QuotaHeadroom above stays the coarse Step-5 scoring signal;
	// this is the richer per-window data DRR needs.
	QuotaWindows []quota.Window

	// InFlightCount is the account's current number of in-flight requests, the
	// primary P2C live signal for Max's final pick (05 §2 Step 7 stage 3:
	// "fewer in-flight requests"). Zero is a legitimate value (idle account);
	// the auto-expiring in-flight lease that maintains this count is
	// P4-ROUTE-012's scope — this field is just the injected count.
	InFlightCount int
}

// BuildCandidatePool builds the Step-2 candidate pool (05 §2 Step 2): a
// FRESH slice containing only offerings where the primary operation is
// certified+supported, the account is healthy, the credential is valid, and
// the offering is not on cooldown. Funding is carried through verbatim —
// the funding gate is Step 3's job (ApplyHardGates in hardgates.go).
func BuildCandidatePool(primary models.Operation, offerings []CandidateOffering) []CandidateOffering {
	out := make([]CandidateOffering, 0, len(offerings))
	for _, o := range offerings {
		cert, ok := o.Certifications[primary]
		if !ok || cert.State != models.CertCertified || cert.Truth != models.TruthSupported {
			continue
		}
		if o.AccountHealth != accountsdomain.HealthHealthy {
			continue
		}
		if !o.CredentialValid {
			continue
		}
		if o.Cooling {
			continue
		}
		out = append(out, o)
	}
	return out
}
