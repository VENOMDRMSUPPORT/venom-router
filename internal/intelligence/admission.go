package intelligence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
)

// AdmissionReason is the closed, exact eight-value vocabulary of typed
// routing-admission blockers (04 §5's "Routing admission" paragraph). No
// more, no fewer.
type AdmissionReason string

const (
	AdmissionIdentityUnresolved     AdmissionReason = "identity_unresolved"
	AdmissionContextUnverified      AdmissionReason = "context_unverified"
	AdmissionCapabilityNotCertified AdmissionReason = "capability_not_certified"
	AdmissionFundingUnknown         AdmissionReason = "funding_unknown"
	AdmissionNoHealthyAccount       AdmissionReason = "no_healthy_account"
	AdmissionQuotaExhausted         AdmissionReason = "quota_exhausted"
	AdmissionQuotaInsufficient      AdmissionReason = "quota_insufficient"
	AdmissionCoolingDown            AdmissionReason = "cooling_down"
)

// admissionReasonSet is the fixed order every returned reason list is
// built in (built as an explicit slice, never derived by ranging over a
// map) — the same order Admit checks its gates in.
var admissionReasonSet = []AdmissionReason{
	AdmissionIdentityUnresolved,
	AdmissionContextUnverified,
	AdmissionCapabilityNotCertified,
	AdmissionFundingUnknown,
	AdmissionNoHealthyAccount,
	AdmissionQuotaExhausted,
	AdmissionQuotaInsufficient,
	AdmissionCoolingDown,
}

// ErrUnknownAdmissionReason is returned by ParseAdmissionReason for any
// value outside the exact eight-value vocabulary.
var ErrUnknownAdmissionReason = errors.New("intelligence: unrecognized admission reason")

// ParseAdmissionReason fails closed on any value outside the exact
// eight-value vocabulary.
func ParseAdmissionReason(s string) (AdmissionReason, error) {
	for _, r := range admissionReasonSet {
		if AdmissionReason(s) == r {
			return r, nil
		}
	}
	return "", fmt.Errorf("%w: %q", ErrUnknownAdmissionReason, s)
}

// AdmissionReasons returns the fixed eight-value admission-reason
// vocabulary, in the order 04 §5 lists it. Each call returns a fresh
// defensive copy.
func AdmissionReasons() []AdmissionReason {
	out := make([]AdmissionReason, len(admissionReasonSet))
	copy(out, admissionReasonSet)
	return out
}

// AdmissionInput is everything Admit needs to decide routability for one
// offering-operation (04 §5's routing-admission paragraph). Every
// boolean here is a fact this package CONSUMES, never computes: funding,
// health, and quota evaluation belong to other units (P4-ROUTE and
// P3b's quota engine); this package only owns the certification-state ×
// capability-truth half of the conjunction.
type AdmissionInput struct {
	State             models.CertificationState
	Truth             models.CapabilityTruth
	IdentityResolved  bool
	ContextVerified   bool
	FundingKnown      bool
	HealthyAccount    bool
	QuotaExhausted    bool
	QuotaInsufficient bool
	CoolingDown       bool
}

// AdmissionVerdict is Admit's result: whether the offering-operation is
// routable, and — when it is not — every reason it failed, in
// AdmissionReasons' fixed order, never duplicated. A non-routable
// verdict always carries at least one reason.
type AdmissionVerdict struct {
	Routable bool
	Reasons  []AdmissionReason
}

// Admit implements 04 §5's routing-admission conjunction: routable only
// when every condition holds. The certification half of the conjunction
// is delegated to models.Routable — never re-derived here — contributing
// AdmissionCapabilityNotCertified when it is false. Reasons are appended
// in admissionReasonSet's fixed order (never by ranging over a map), so
// the result is deterministic and reproducible.
func Admit(in AdmissionInput) AdmissionVerdict {
	var reasons []AdmissionReason

	if !in.IdentityResolved {
		reasons = append(reasons, AdmissionIdentityUnresolved)
	}
	if !in.ContextVerified {
		reasons = append(reasons, AdmissionContextUnverified)
	}
	if !models.Routable(in.State, in.Truth) {
		reasons = append(reasons, AdmissionCapabilityNotCertified)
	}
	if !in.FundingKnown {
		reasons = append(reasons, AdmissionFundingUnknown)
	}
	if !in.HealthyAccount {
		reasons = append(reasons, AdmissionNoHealthyAccount)
	}
	if in.QuotaExhausted {
		reasons = append(reasons, AdmissionQuotaExhausted)
	}
	if in.QuotaInsufficient {
		reasons = append(reasons, AdmissionQuotaInsufficient)
	}
	if in.CoolingDown {
		reasons = append(reasons, AdmissionCoolingDown)
	}

	return AdmissionVerdict{Routable: len(reasons) == 0, Reasons: reasons}
}

// ReviewItem is one certification row the review drainer's queue
// surfaces as needing work.
type ReviewItem struct {
	OfferingOperationID string
	State               models.CertificationState
	Truth               models.CapabilityTruth
	Attempts            int
}

// ReviewQueue is the local port over the review backlog. The
// storage-backed adapter belongs to a later unit (P3c-CAPI-001 /
// P3c-JOBS-001); this package only declares the port. limit bounds how
// many items a single ListForReview call may return.
type ReviewQueue interface {
	ListForReview(ctx context.Context, limit int) ([]ReviewItem, error)
}

// ReviewReasonCount is one entry of DrainResult.ByReason — "the review
// count grouped by reason" the dashboard renders (04 §5).
type ReviewReasonCount struct {
	Reason AdmissionReason
	Count  int
}

// DrainResult is Drain's outcome: how many items were scanned, how many
// were advanced (a probe/re-probe was started), how many were skipped
// (already certified or already probing), and how many failed (an
// individual item's error, never fatal to the batch). ByReason is
// sorted in AdmissionReasons' fixed order.
type DrainResult struct {
	Scanned, Advanced, Skipped, Failed int
	ByReason                           []ReviewReasonCount
}

// ErrNilReviewDrainerDependency is returned by NewReviewDrainer when
// queue or driver is nil.
var ErrNilReviewDrainerDependency = errors.New("intelligence: review drainer requires a queue and a driver")

// ErrInvalidReviewBatchSize is returned by NewReviewDrainer for a
// batchSize below 1.
var ErrInvalidReviewBatchSize = errors.New("intelligence: review drainer batch size must be at least 1")

// ReviewDrainer works the certification review backlog in small, bounded
// batches (04 §5: "A bounded background review drainer works the
// backlog... idempotent, small batches, never re-touches already-
// certified rows"). It never evaluates funding/health/quota itself —
// those live outside the certification domain — it only drives
// observed/suspended/expired rows forward through CertificationDriver.
type ReviewDrainer struct {
	queue     ReviewQueue
	driver    *CertificationDriver
	batchSize int
	now       func() time.Time
}

// NewReviewDrainer builds a ReviewDrainer. queue and driver are
// required; batchSize must be at least 1. now defaults to time.Now when
// nil.
func NewReviewDrainer(queue ReviewQueue, driver *CertificationDriver, batchSize int, now func() time.Time) (*ReviewDrainer, error) {
	if queue == nil || driver == nil {
		return nil, ErrNilReviewDrainerDependency
	}
	if batchSize < 1 {
		return nil, fmt.Errorf("%w: got %d", ErrInvalidReviewBatchSize, batchSize)
	}
	if now == nil {
		now = time.Now
	}
	return &ReviewDrainer{queue: queue, driver: driver, batchSize: batchSize, now: now}, nil
}

// Drain reads at most batchSize items and, for each one, drives its
// certification forward: observed -> StartProbe (edge 2);
// suspended/expired -> ReProbe (edge 8/10). A certified or probing row
// is never touched — this is what makes repeated draining idempotent,
// since after the first successful advance an item is in probing and
// every later drain skips it. One item's error is recorded (Failed) and
// never aborts the batch.
func (d *ReviewDrainer) Drain(ctx context.Context) (DrainResult, error) {
	items, err := d.queue.ListForReview(ctx, d.batchSize)
	if err != nil {
		return DrainResult{}, fmt.Errorf("intelligence: list review items: %w", err)
	}

	result := DrainResult{Scanned: len(items)}

	for _, item := range items {
		switch item.State {
		case models.CertObserved:
			if _, err := d.driver.StartProbe(ctx, item.OfferingOperationID); err != nil {
				result.Failed++
				continue
			}
			result.Advanced++
		case models.CertSuspended, models.CertExpired:
			if _, err := d.driver.ReProbe(ctx, item.OfferingOperationID); err != nil {
				result.Failed++
				continue
			}
			result.Advanced++
		default:
			// certified and probing rows are never re-touched.
			result.Skipped++
		}
	}

	// Every scanned item, by construction, has not yet reached
	// (certified, supported) — ReviewQueue only ever surfaces backlog
	// rows — so the entire batch buckets under the one admission reason
	// this drainer has enough information to compute on its own. Funding,
	// health, and quota reasons require inputs this package does not
	// own (see AdmissionInput's doc comment) and are not attributed here.
	if result.Scanned > 0 {
		result.ByReason = []ReviewReasonCount{{Reason: AdmissionCapabilityNotCertified, Count: result.Scanned}}
	}

	return result, nil
}
