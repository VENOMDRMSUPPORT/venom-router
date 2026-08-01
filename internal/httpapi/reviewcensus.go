package httpapi

import (
	"context"
	"net/http"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/intelligence"
)

// reviewcensus.go serves GET /api/control/v1/certifications/review
// (P6-CAPI-EXTRA, enables P6-UI-012): the STANDING census 04 §5 calls "a review
// count grouped by reason".
//
// ─── WHAT THIS ENDPOINT REFUSES TO SAY ──────────────────────────────────────
//
// A standing census has no request. Six of 04 §5's eight admission reasons —
// and one of them provably, by the spec's own wording — cannot be evaluated
// without one, or cannot be evaluated from the certifications table at all. This
// endpoint therefore reports TWO EXPLICIT LISTS rather than one count table:
//
//	evaluated_reasons      — reasons this census actually computed
//	not_evaluated_reasons  — reasons it did not, named out loud
//
// and `by_reason` carries entries for the EVALUATED reasons only. That split is
// the whole design. A reason reported as `count: 0` reads as "we looked and
// found none"; for a reason nobody evaluated, that is simply false, and it is
// false in the most dangerous direction — it renders as an all-clear. An absent
// row is no better: absence also reads as "none found". So the unevaluated
// reasons are named, in their own list, with no count at all.
//
// The two lists PARTITION intelligence.AdmissionReasons(): not_evaluated is
// COMPUTED as the complement of evaluated, never written out by hand, so a
// ninth reason added to the domain vocabulary cannot slip through unclassified.
//
// ─── THE SPLIT, AND WHY ─────────────────────────────────────────────────────
//
// EVALUATED (1 of 8):
//
//	capability_not_certified — the certification state x capability truth
//	  conjunction. It is standing state, it lives in the certifications table,
//	  and models.Routable is its complete definition. Computable, so computed.
//
// NOT EVALUATED (7 of 8), each for a stated reason:
//
//	quota_insufficient  — REQUEST-DEPENDENT by 04 §5, verbatim: quota must not be
//	  "exhausted or insufficient FOR THE REQUEST'S ESTIMATED NEED". Insufficiency
//	  is a relation between remaining headroom and one request's estimate. A
//	  standing census has no request, so the relation has no second operand.
//	  There is no honest standing answer, not even 0.
//	identity_unresolved — canonical-identity resolution is a catalog fact
//	  (models / account_model_offerings), not a certification one.
//	context_unverified  — the verified context number lives on the offering, and
//	  its provenance comes out of intelligence.Project's read model.
//	funding_unknown     — an ACCOUNT fact (funding evidence + owner override).
//	no_healthy_account  — an ACCOUNT fact (the multi-axis health model).
//	quota_exhausted     — a QUOTA-WINDOW fact. Note this one is NOT
//	  request-dependent and could be censused by a future unit that reads quota
//	  windows; it is unevaluated here only because this census's single bounded
//	  query is over certifications. It is listed rather than guessed.
//	cooling_down        — a COOLDOWN fact, scoped per account/offering/provider.
//
// Every one of those seven is reachable only through a repo this endpoint does
// not hold. Rather than widen the census into a fleet-wide join whose cost and
// staleness nobody has specified, it reports exactly what its one bounded query
// can support and names the rest. Widening it is a later unit's deliberate
// choice, and the not_evaluated list is the standing record of what is missing.
//
// ─── AND WHAT IT NEVER PUBLISHES ────────────────────────────────────────────
//
// No `routable` verdict, anywhere. See censusVerdict below.

// certificationCensusReader is the census port: storage.CertificationRepo's
// ListForAdmissionCensus. Declared as an interface here so the failure path is
// testable — a real repo cannot be asked to fail on demand.
type certificationCensusReader interface {
	ListForAdmissionCensus(ctx context.Context, limit int) ([]intelligence.ReviewItem, bool, error)
}

// ReviewCensusHandler serves the census. Owner-session + CSRF gated via
// ControlMux's `gated`; a read, so no audit event.
type ReviewCensusHandler struct {
	certs certificationCensusReader
}

// NewReviewCensusHandler builds the handler over the certification repo.
func NewReviewCensusHandler(certs certificationCensusReader) *ReviewCensusHandler {
	return &ReviewCensusHandler{certs: certs}
}

// censusEvaluatedReasons is the exact subset of the closed eight-value
// vocabulary this census computes. It is the SINGLE place that list is written
// down: the not-evaluated list is derived from it, so the two can never overlap
// and can never together miss a reason. See this file's header for why each of
// the other seven is absent.
var censusEvaluatedReasons = []intelligence.AdmissionReason{
	intelligence.AdmissionCapabilityNotCertified,
}

// censusNotEvaluatedReasons returns AdmissionReasons() minus the evaluated set,
// in the domain's own fixed order. Computed, never transcribed — that is what
// makes the partition provable rather than merely intended.
func censusNotEvaluatedReasons() []intelligence.AdmissionReason {
	evaluated := make(map[intelligence.AdmissionReason]bool, len(censusEvaluatedReasons))
	for _, r := range censusEvaluatedReasons {
		evaluated[r] = true
	}
	out := make([]intelligence.AdmissionReason, 0, len(intelligence.AdmissionReasons()))
	for _, r := range intelligence.AdmissionReasons() {
		if !evaluated[r] {
			out = append(out, r)
		}
	}
	return out
}

// censusVerdict runs one certification row through intelligence.Admit — never a
// re-derived conjunction — and returns only its REASONS.
//
// The non-certification inputs are pinned to their NON-BLOCKING values so that
// the only reason Admit can possibly return is the certification one, which is
// the only one this census evaluates. That pinning is precisely why the verdict's
// Routable field is discarded and never projected: with identity, context,
// funding, health, quota and cooldown all asserted true, `Routable: true` would
// mean "routable IF every fact this census did not check happens to hold" — a
// statement about the fixture, not about the offering. Publishing it would turn
// a pinned input into a routability claim, which is exactly the fabrication this
// codebase forbids.
func censusVerdict(item intelligence.ReviewItem) []intelligence.AdmissionReason {
	verdict := intelligence.Admit(intelligence.AdmissionInput{
		State: item.State,
		Truth: item.Truth,
		// Pinned non-blocking — see the doc comment. NOT claims about reality.
		IdentityResolved:  true,
		ContextVerified:   true,
		FundingKnown:      true,
		HealthyAccount:    true,
		QuotaExhausted:    false,
		QuotaInsufficient: false,
		CoolingDown:       false,
	})
	// verdict.Routable is DELIBERATELY DISCARDED here and never serialized.
	return verdict.Reasons
}

// reviewCensusReasonJSON is one evaluated reason and its count. `reason` is the
// typed AdmissionReason verbatim — a code, never a human phrase, so the
// dashboard can render the code the API actually stated.
type reviewCensusReasonJSON struct {
	Reason string `json:"reason"`
	Count  int    `json:"count"`
}

// reviewCensusJSON is the response body.
//
// `scanned` is what was actually examined, not what exists — and `truncated`
// says whether the two differ. A capped count presented as a complete one is the
// difference between "6 offerings need review" and "at least 6 of an unknown
// number do".
type reviewCensusJSON struct {
	Scanned   int  `json:"scanned"`
	Limit     int  `json:"limit"`
	Truncated bool `json:"truncated"`

	// EvaluatedReasons / NotEvaluatedReasons partition the closed eight-value
	// vocabulary. Both are always present and never omitempty: an absent list is
	// as misleading as an absent count.
	EvaluatedReasons    []string `json:"evaluated_reasons"`
	NotEvaluatedReasons []string `json:"not_evaluated_reasons"`

	// ByReason carries an entry for every EVALUATED reason — including one with
	// a count of 0, which is the honest all-clear. It carries NO entry for an
	// unevaluated reason, at any count.
	ByReason []reviewCensusReasonJSON `json:"by_reason"`
}

// ServeCensus implements GET /api/control/v1/certifications/review.
func (h *ReviewCensusHandler) ServeCensus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAuthError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false)
		return
	}
	if h.certs == nil {
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}

	page := parsePageParams(r, defaultPageLimit, maxPageLimit)

	items, truncated, err := h.certs.ListForAdmissionCensus(r.Context(), page.Limit)
	if err != nil {
		// No partial census: an empty one renders as an all-clear, which is the
		// worst possible reading of "we could not ask".
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}

	// Seed every EVALUATED reason at 0 so a clean backlog reports an explicit
	// zero rather than an absent row.
	counts := make(map[intelligence.AdmissionReason]int, len(censusEvaluatedReasons))
	for _, reason := range censusEvaluatedReasons {
		counts[reason] = 0
	}
	for _, item := range items {
		for _, reason := range censusVerdict(item) {
			// Only evaluated reasons are counted. The pinned inputs make any
			// other reason unreachable today; this guard keeps it that way if the
			// pinning ever changes, rather than silently publishing a count for a
			// reason still advertised as not evaluated.
			if _, evaluated := counts[reason]; evaluated {
				counts[reason]++
			}
		}
	}

	out := reviewCensusJSON{
		Scanned:             len(items),
		Limit:               page.Limit,
		Truncated:           truncated,
		EvaluatedReasons:    make([]string, 0, len(censusEvaluatedReasons)),
		NotEvaluatedReasons: []string{},
		ByReason:            make([]reviewCensusReasonJSON, 0, len(censusEvaluatedReasons)),
	}
	// Built by ranging the fixed slices, never a map, so the order is stable.
	for _, reason := range censusEvaluatedReasons {
		out.EvaluatedReasons = append(out.EvaluatedReasons, string(reason))
		out.ByReason = append(out.ByReason, reviewCensusReasonJSON{Reason: string(reason), Count: counts[reason]})
	}
	for _, reason := range censusNotEvaluatedReasons() {
		out.NotEvaluatedReasons = append(out.NotEvaluatedReasons, string(reason))
	}

	writeData(w, http.StatusOK, out)
}
