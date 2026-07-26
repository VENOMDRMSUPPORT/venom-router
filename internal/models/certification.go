package models

import (
	"errors"
	"fmt"
	"time"
)

// CertificationState is an offering-operation's administrative
// certification lifecycle (04 §5) — exactly six values, verbatim from the
// M4 certifications.status CHECK. There is no "rejected" state and no
// "catalog_only" state; a media-only offering's exclusion from routing is
// a separate classification dimension (P3a-CERT-002), never a seventh
// value here.
type CertificationState string

const (
	CertDiscovered CertificationState = "discovered"
	CertObserved   CertificationState = "observed"
	CertProbing    CertificationState = "probing"
	CertCertified  CertificationState = "certified"
	CertSuspended  CertificationState = "suspended"
	CertExpired    CertificationState = "expired"
)

// ErrUnknownCertificationState is returned by ParseCertificationState for
// any value outside the fixed six-value vocabulary — including "rejected"
// and "catalog_only", neither of which is a certification state.
var ErrUnknownCertificationState = errors.New("models: unrecognized certification state")

var certificationStateSet = []CertificationState{
	CertDiscovered, CertObserved, CertProbing, CertCertified, CertSuspended, CertExpired,
}

// ParseCertificationState fails closed on any value outside the exact
// six-value vocabulary.
func ParseCertificationState(s string) (CertificationState, error) {
	for _, st := range certificationStateSet {
		if CertificationState(s) == st {
			return st, nil
		}
	}
	return "", fmt.Errorf("%w: %q", ErrUnknownCertificationState, s)
}

// CertificationStates returns the fixed six-value certification-state
// enumeration.
func CertificationStates() []CertificationState {
	out := make([]CertificationState, len(certificationStateSet))
	copy(out, certificationStateSet)
	return out
}

// CapabilityTruth is what a probe proved about an offering-operation (04
// §5) — a dimension separate from CertificationState. The certification
// state tracks administrative lifecycle; capability truth tracks the
// probe's verdict.
type CapabilityTruth string

const (
	TruthUnknown     CapabilityTruth = "unknown"
	TruthSupported   CapabilityTruth = "supported"
	TruthUnsupported CapabilityTruth = "unsupported"
)

// ErrUnknownCapabilityTruth is returned by ParseCapabilityTruth for any
// value outside the three-value vocabulary.
var ErrUnknownCapabilityTruth = errors.New("models: unrecognized capability truth")

// ParseCapabilityTruth fails closed on any value outside the exact
// three-value vocabulary.
func ParseCapabilityTruth(s string) (CapabilityTruth, error) {
	switch CapabilityTruth(s) {
	case TruthUnknown, TruthSupported, TruthUnsupported:
		return CapabilityTruth(s), nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnknownCapabilityTruth, s)
	}
}

// Certification is the certifications row's domain-visible shape (04 §5):
// one offering-operation's lifecycle state, the separate capability-truth
// dimension, a version counter, and an evidence reference (an id/reference
// only — never inline evidence content or secret material).
type Certification struct {
	OfferingOperationID string
	State               CertificationState
	Truth               CapabilityTruth
	Version             int
	CertifiedAt         *time.Time
	EvidenceRef         string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// RetryPolicy is the caller-supplied per-cycle probe retry budget (04 §5
// edge 3: "owner-policy default: 3 attempts"). It is an input to
// Transition, never a package constant and never read from the
// environment — this package does not schedule, sleep, or back off; it
// only decides whether probing may legally stay in probing given the
// caller's own attempt count and budget.
type RetryPolicy struct {
	// Attempts is the attempt number this call represents (including the
	// current one).
	Attempts int
	// Budget is the maximum number of retryable attempts allowed before
	// the operation must move to suspended instead of staying in probing.
	Budget int
}

// ErrIllegalCertificationTransition is returned when the requested (from,
// to) pair is not one of the 10 edges in 04 §5's legal transition table.
// The certification is returned unchanged.
var ErrIllegalCertificationTransition = errors.New("models: illegal certification transition")

// ErrRetryBudgetExceeded is returned when a probing -> probing transition
// is attempted after the caller-supplied retry budget has been exhausted
// (04 §5 edge 3 / edge 5).
var ErrRetryBudgetExceeded = errors.New("models: certification retry budget exceeded")

// ErrVerdictRequired is returned when a transition into certified is
// attempted without a resolved capability truth (supported or
// unsupported) — 04 §5: "any transition that sets certified while the
// operation has no recorded probe verdict" is invalid.
var ErrVerdictRequired = errors.New("models: transition to certified requires a resolved capability truth")

// ErrNoValidVerdict is returned when suspended -> certified (the
// administrative resume, edge 7) is attempted while the certification's
// existing Truth is still unknown — 04 §5 requires "the previously
// recorded verdict is still fresh/valid" for this edge, which this
// package models as: a resume never invents a verdict, it only resumes
// routing on top of one already on record.
var ErrNoValidVerdict = errors.New("models: suspended -> certified requires a still-valid prior verdict")

// legalCertificationTransitions is 04 §5's legal transition table,
// verbatim (edges 1-10). Any (from, to) pair absent from this map is
// rejected by Transition — the graph is closed.
var legalCertificationTransitions = map[CertificationState]map[CertificationState]bool{
	CertDiscovered: {CertObserved: true},                                          // 1
	CertObserved:   {CertProbing: true},                                           // 2
	CertProbing:    {CertProbing: true, CertCertified: true, CertSuspended: true}, // 3, 4, 5
	CertCertified:  {CertSuspended: true, CertExpired: true},                      // 6, 9
	CertSuspended:  {CertCertified: true, CertProbing: true},                      // 7, 8
	CertExpired:    {CertProbing: true},                                           // 10
}

// Transition applies a certification-state edge from 04 §5's legal
// transition table. now is the injected clock stamping UpdatedAt. verdict
// is consumed only by probing -> certified (edge 4), where it must be
// resolved (supported or unsupported); every other edge ignores it and
// carries the certification's existing Truth forward unchanged — edges
// probing -> probing, probing -> suspended, and certified -> suspended
// never mutate Truth as a side effect. retry is consulted only by
// probing -> probing, which is legal only while retry.Attempts is within
// retry.Budget; exceeding it makes probing -> probing illegal (the
// caller must instead transition to suspended).
//
// suspended -> certified (edge 7, administrative resume) requires the
// certification's existing Truth to already be resolved — this models 04
// §5's "the previously recorded verdict is still fresh/valid": a resume
// never supplies a new verdict, it only resumes atop one already on
// record.
//
// suspended -> probing (edge 8) resets Truth to unknown: the suspension
// cause cleared but the verdict must be re-established, so the prior
// verdict is no longer treated as valid evidence going forward.
//
// On rejection, c is returned unchanged alongside a wrapped typed error.
// Auditing the rejection (04 §5: "each emits an audit_event") is the
// caller's responsibility — this pure package does not audit.
func (c Certification) Transition(target CertificationState, verdict CapabilityTruth, retry RetryPolicy, now time.Time) (Certification, error) {
	if !legalCertificationTransitions[c.State][target] {
		return c, fmt.Errorf("%w: %s -> %s", ErrIllegalCertificationTransition, c.State, target)
	}

	switch {
	case c.State == CertProbing && target == CertProbing:
		if retry.Attempts > retry.Budget {
			return c, fmt.Errorf("%w: attempt %d exceeds budget %d", ErrRetryBudgetExceeded, retry.Attempts, retry.Budget)
		}
		next := c
		next.UpdatedAt = now
		return next, nil

	case c.State == CertProbing && target == CertCertified:
		if verdict != TruthSupported && verdict != TruthUnsupported {
			return c, fmt.Errorf("%w: got %s", ErrVerdictRequired, verdict)
		}
		next := c
		next.State = target
		next.Truth = verdict
		next.CertifiedAt = &now
		next.Version++
		next.UpdatedAt = now
		return next, nil

	case c.State == CertSuspended && target == CertCertified:
		if c.Truth != TruthSupported && c.Truth != TruthUnsupported {
			return c, fmt.Errorf("%w: current truth = %s", ErrNoValidVerdict, c.Truth)
		}
		next := c
		next.State = target
		next.UpdatedAt = now
		return next, nil

	case c.State == CertSuspended && target == CertProbing:
		next := c
		next.State = target
		next.Truth = TruthUnknown
		next.UpdatedAt = now
		return next, nil

	default:
		next := c
		next.State = target
		next.UpdatedAt = now
		return next, nil
	}
}

// Routable reports whether (state, truth) is the single routable
// combination (04 §5): certified with truth = supported. All 17 other
// combinations across the 6x3 Cartesian product are not routable. Routing
// admission has further conditions outside this package — funding,
// health, quota, cooldown, and every other routing-required fact (04 §5)
// — this predicate answers only the certification-layer question.
func Routable(state CertificationState, truth CapabilityTruth) bool {
	return state == CertCertified && truth == TruthSupported
}
