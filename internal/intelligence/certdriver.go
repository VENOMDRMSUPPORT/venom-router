package intelligence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
)

// SuspensionReason is the closed vocabulary for why an offering-operation
// was suspended (04 §5 edges 5 and 6: probing -> suspended,
// certified -> suspended).
type SuspensionReason string

const (
	SuspensionCredentialBlocked         SuspensionReason = "credential_blocked"
	SuspensionProbeRetryBudgetExhausted SuspensionReason = "probe_retry_budget_exhausted"
	SuspensionInvalidCredentials        SuspensionReason = "invalid_credentials"
	SuspensionQuotaExhausted            SuspensionReason = "quota_exhausted"
	SuspensionProtocolFailure           SuspensionReason = "protocol_failure"
	SuspensionProviderRemoved           SuspensionReason = "provider_removed"
	SuspensionCapabilityContradiction   SuspensionReason = "capability_contradiction"
)

var suspensionReasonSet = []SuspensionReason{
	SuspensionCredentialBlocked,
	SuspensionProbeRetryBudgetExhausted,
	SuspensionInvalidCredentials,
	SuspensionQuotaExhausted,
	SuspensionProtocolFailure,
	SuspensionProviderRemoved,
	SuspensionCapabilityContradiction,
}

// ErrUnknownSuspensionReason is returned by ParseSuspensionReason for any
// value outside the exact seven-value vocabulary.
var ErrUnknownSuspensionReason = errors.New("intelligence: unrecognized suspension reason")

// ParseSuspensionReason fails closed on any value outside the exact
// seven-value vocabulary.
func ParseSuspensionReason(s string) (SuspensionReason, error) {
	for _, r := range suspensionReasonSet {
		if SuspensionReason(s) == r {
			return r, nil
		}
	}
	return "", fmt.Errorf("%w: %q", ErrUnknownSuspensionReason, s)
}

// ErrCertificationConflict is returned by CertificationStore.CompareAndSwap
// when previous no longer matches the stored row. The adapter MUST guard
// on (OfferingOperationID, previous.State, previous.Version) together —
// never on Version alone: Transition only bumps Version on the
// probing -> certified edge (04 §5 edge 4), so Version alone cannot
// distinguish two consecutive edges that share a version number (e.g. two
// back-to-back probing -> probing retries). A conflict is never retried
// silently by this package — see CertificationDriver's doc comment.
var ErrCertificationConflict = errors.New("intelligence: certification compare-and-swap conflict")

// CertificationStore is the local port over the certifications row. The
// storage-backed adapter belongs to a later unit (P3c-CAPI-001 /
// P3c-JOBS-001); this package only declares the port.
type CertificationStore interface {
	Load(ctx context.Context, offeringOperationID string) (models.Certification, error)
	// CompareAndSwap commits next only if previous still matches the
	// stored row on (OfferingOperationID, State, Version) together. A
	// mismatch returns ErrCertificationConflict.
	CompareAndSwap(ctx context.Context, previous, next models.Certification) error
}

// CertificationAuditReason is the closed vocabulary for
// CertificationAuditRecord.Reason.
type CertificationAuditReason string

const (
	AuditProbeStarted        CertificationAuditReason = "probe_started"
	AuditProbeRetry          CertificationAuditReason = "probe_retry"
	AuditVerdictRecorded     CertificationAuditReason = "verdict_recorded"
	AuditSuspended           CertificationAuditReason = "suspended"
	AuditResumed             CertificationAuditReason = "resumed"
	AuditReProbeScheduled    CertificationAuditReason = "re_probe_scheduled"
	AuditExpired             CertificationAuditReason = "expired"
	AuditIllegalTransition   CertificationAuditReason = "illegal_transition"
	AuditRetryBudgetExceeded CertificationAuditReason = "retry_budget_exceeded"
	AuditVerdictRequired     CertificationAuditReason = "verdict_required"
	AuditNoValidVerdict      CertificationAuditReason = "no_valid_verdict"
	AuditObserved            CertificationAuditReason = "observed"
)

var certificationAuditReasonSet = []CertificationAuditReason{
	AuditProbeStarted, AuditProbeRetry, AuditVerdictRecorded, AuditSuspended, AuditResumed,
	AuditReProbeScheduled, AuditExpired, AuditIllegalTransition, AuditRetryBudgetExceeded,
	AuditVerdictRequired, AuditNoValidVerdict, AuditObserved,
}

// ErrUnknownCertificationAuditReason is returned by
// ParseCertificationAuditReason for any value outside the closed
// vocabulary.
var ErrUnknownCertificationAuditReason = errors.New("intelligence: unrecognized certification audit reason")

// ParseCertificationAuditReason fails closed on any value outside the
// exact vocabulary.
func ParseCertificationAuditReason(s string) (CertificationAuditReason, error) {
	for _, r := range certificationAuditReasonSet {
		if CertificationAuditReason(s) == r {
			return r, nil
		}
	}
	return "", fmt.Errorf("%w: %q", ErrUnknownCertificationAuditReason, s)
}

// CertificationAuditRecord is one certification-transition audit event
// (04 §5: "each emits an audit_event"). Ids and codes only — never free
// text, a provider message, or evidence content. Suspension is set only
// when Reason is AuditSuspended; ProbeReason carries the ProbeReasonCode
// (if any) that drove this attempt, for the audit trail's own diagnostic
// value — never a raw provider string.
type CertificationAuditRecord struct {
	OfferingOperationID string
	From, To            models.CertificationState
	Truth               models.CapabilityTruth
	Accepted            bool
	Reason              CertificationAuditReason
	Suspension          SuspensionReason
	ProbeReason         ProbeReasonCode
	At                  time.Time
}

// CertificationAuditor persists CertificationAuditRecord entries. The
// storage-backed adapter belongs to a later unit; this package only
// declares the port.
type CertificationAuditor interface {
	CertificationTransitioned(ctx context.Context, rec CertificationAuditRecord) error
}

// DefaultProbeRetryBudget is the owner-policy default retry budget (04 §5
// edge 3: "default: 3 attempts"). Exported for the CALLER (a later unit)
// to pass into NewCertificationDriver — this package never reads it
// implicitly.
const DefaultProbeRetryBudget = 3

// ErrNilCertificationDriverPort is returned by NewCertificationDriver when
// store or auditor is nil.
var ErrNilCertificationDriverPort = errors.New("intelligence: certification driver requires a store and an auditor")

// ErrInvalidRetryBudget is returned by NewCertificationDriver for a
// retryBudget below 1.
var ErrInvalidRetryBudget = errors.New("intelligence: certification driver retry budget must be at least 1")

// CertificationDriver drives every 04 §5 certification-state-machine edge
// (1-10). Edge 1 (discovered -> observed, Observe) is triggered by
// discovery evidence being recorded, not a probe verdict — P3c-CERT-008's
// storage-layer apply path drives it in-transaction directly rather than
// through this method (see DiscoveryRepo's own doc comment for why), but
// the transition itself is identical either way. It never re-implements
// or bypasses
// the frozen models.Certification.Transition state machine: every method
// here is Load -> Transition -> CompareAndSwap -> audit.
type CertificationDriver struct {
	store       CertificationStore
	auditor     CertificationAuditor
	retryBudget int
	now         func() time.Time
}

// NewCertificationDriver builds a CertificationDriver. store and auditor
// are required; retryBudget must be at least 1. now defaults to time.Now
// when nil.
func NewCertificationDriver(store CertificationStore, auditor CertificationAuditor, retryBudget int, now func() time.Time) (*CertificationDriver, error) {
	if store == nil || auditor == nil {
		return nil, ErrNilCertificationDriverPort
	}
	if retryBudget < 1 {
		return nil, fmt.Errorf("%w: got %d", ErrInvalidRetryBudget, retryBudget)
	}
	if now == nil {
		now = time.Now
	}
	return &CertificationDriver{store: store, auditor: auditor, retryBudget: retryBudget, now: now}, nil
}

// Observe drives edge 1 (discovered -> observed, 04 §5), triggered by
// "first concrete evidence for the offering-operation recorded" —
// normally a discovery snapshot (P3c-CERT-008; internal/storage's
// DiscoveryRepo drives the SAME edge directly, in-transaction, for the
// reason its own doc comment gives — this method is the port other
// callers, and this package's own tests, drive it through). It follows
// the identical Load -> Transition -> CompareAndSwap -> audit path every
// other driver method uses: called from any state other than discovered,
// Transition's frozen legality table rejects it as an illegal transition,
// audited like any other rejection, CompareAndSwap never called.
func (d *CertificationDriver) Observe(ctx context.Context, offeringOperationID string) (models.Certification, error) {
	return d.apply(ctx, offeringOperationID, models.CertObserved, models.TruthUnknown, models.RetryPolicy{}, AuditObserved, "", "")
}

// StartProbe drives edge 2 (observed -> probing).
func (d *CertificationDriver) StartProbe(ctx context.Context, offeringOperationID string) (models.Certification, error) {
	return d.apply(ctx, offeringOperationID, models.CertProbing, models.TruthUnknown, models.RetryPolicy{}, AuditProbeStarted, "", "")
}

// recordAttemptPlan is RecordAttempt's pure decision — which edge to
// attempt and what verdict to carry — computed directly from a
// ProbeOutcome, independent of models.Certification.Transition's own
// mechanics. It exists as its own step (rather than inlined into
// RecordAttempt) because Transition itself ignores the verdict argument
// for every edge except probing -> certified: a defect that swapped in
// outcome.Truth for the probing/suspended edges would otherwise be
// unobservable through Transition's output alone, since the frozen state
// machine would silently discard the wrong value anyway. Testing
// planRecordAttempt directly pins the crown rule at the one point where a
// regression is actually observable.
type recordAttemptPlan struct {
	target  models.CertificationState
	verdict models.CapabilityTruth
	reason  CertificationAuditReason
}

// planRecordAttempt implements 04 §5 / P3c-CERT-001's taxonomy:
//   - outcome.Definitive ⇒ edge 4 (probing -> certified), carrying
//     outcome.Truth as the verdict.
//   - a terminal failure ⇒ edge 5 (probing -> suspended,
//     credential_blocked) directly, bypassing the retry budget (a
//     credential/protocol block is not something retrying fixes).
//   - otherwise (retryable or inconclusive) ⇒ edge 3
//     (probing -> probing), to be retried under the caller's budget.
//
// The crown rule: verdict is the literal models.TruthUnknown in every
// branch except outcome.Definitive — never outcome.Truth.
func planRecordAttempt(outcome ProbeOutcome) recordAttemptPlan {
	if outcome.Definitive {
		return recordAttemptPlan{target: models.CertCertified, verdict: outcome.Truth, reason: AuditVerdictRecorded}
	}
	if outcome.Execution == ProbeTerminalFailure {
		return recordAttemptPlan{target: models.CertSuspended, verdict: models.TruthUnknown, reason: AuditSuspended}
	}
	return recordAttemptPlan{target: models.CertProbing, verdict: models.TruthUnknown, reason: AuditProbeRetry}
}

// RecordAttempt is the only place a ProbeOutcome becomes a lifecycle
// move. It computes planRecordAttempt(outcome) and applies it; when the
// plan targets probing (retryable/inconclusive) and the retry budget is
// exhausted, it falls through to edge 5 (probing -> suspended,
// probe_retry_budget_exhausted) instead of failing.
func (d *CertificationDriver) RecordAttempt(ctx context.Context, offeringOperationID string, outcome ProbeOutcome, attempts int) (models.Certification, error) {
	retry := models.RetryPolicy{Attempts: attempts, Budget: d.retryBudget}
	plan := planRecordAttempt(outcome)

	switch plan.target {
	case models.CertCertified:
		return d.apply(ctx, offeringOperationID, plan.target, plan.verdict, retry, plan.reason, "", outcome.Reason)
	case models.CertSuspended:
		return d.apply(ctx, offeringOperationID, plan.target, plan.verdict, retry, plan.reason, SuspensionCredentialBlocked, outcome.Reason)
	default: // models.CertProbing
		next, err := d.apply(ctx, offeringOperationID, plan.target, plan.verdict, retry, plan.reason, "", outcome.Reason)
		if err != nil && errors.Is(err, models.ErrRetryBudgetExceeded) {
			return d.apply(ctx, offeringOperationID, models.CertSuspended, models.TruthUnknown, models.RetryPolicy{}, AuditSuspended, SuspensionProbeRetryBudgetExhausted, outcome.Reason)
		}
		return next, err
	}
}

// Suspend drives edge 6 (certified -> suspended).
func (d *CertificationDriver) Suspend(ctx context.Context, offeringOperationID string, reason SuspensionReason) (models.Certification, error) {
	return d.apply(ctx, offeringOperationID, models.CertSuspended, models.TruthUnknown, models.RetryPolicy{}, AuditSuspended, reason, "")
}

// Resume drives edge 7 (suspended -> certified, the administrative
// resume). The verdict argument is unused by this edge — Transition
// itself requires the certification's EXISTING Truth to already be
// resolved (04 §5: "the previously recorded verdict is still
// fresh/valid"); Resume never supplies a new one.
func (d *CertificationDriver) Resume(ctx context.Context, offeringOperationID string) (models.Certification, error) {
	return d.apply(ctx, offeringOperationID, models.CertCertified, models.TruthUnknown, models.RetryPolicy{}, AuditResumed, "", "")
}

// ReProbe drives edge 8 (suspended -> probing) or edge 10
// (expired -> probing), chosen from the certification's CURRENT state —
// never assumed. Both edges share the same target (probing) and the same
// audit reason; Transition's own legality map is the single source of
// truth for which current states may legally reach it, so no second,
// duplicate legality check is added here. Any other current state is
// rejected by Transition as an illegal transition and audited as such.
func (d *CertificationDriver) ReProbe(ctx context.Context, offeringOperationID string) (models.Certification, error) {
	return d.apply(ctx, offeringOperationID, models.CertProbing, models.TruthUnknown, models.RetryPolicy{}, AuditReProbeScheduled, "", "")
}

// Expire drives edge 9 (certified -> expired).
func (d *CertificationDriver) Expire(ctx context.Context, offeringOperationID string) (models.Certification, error) {
	return d.apply(ctx, offeringOperationID, models.CertExpired, models.TruthUnknown, models.RetryPolicy{}, AuditExpired, "", "")
}

// apply loads the current certification, then delegates to
// transitionAndCommit.
func (d *CertificationDriver) apply(ctx context.Context, offeringOperationID string, target models.CertificationState, verdict models.CapabilityTruth, retry models.RetryPolicy, reason CertificationAuditReason, suspension SuspensionReason, probeReason ProbeReasonCode) (models.Certification, error) {
	current, err := d.store.Load(ctx, offeringOperationID)
	if err != nil {
		return models.Certification{}, fmt.Errorf("intelligence: load certification %q: %w", offeringOperationID, err)
	}
	return d.transitionAndCommit(ctx, offeringOperationID, current, target, verdict, retry, reason, suspension, probeReason)
}

// transitionAndCommit is the single Load(already done) -> Transition ->
// CompareAndSwap -> audit path every driver method funnels through.
//
// Rejection contract: when Transition returns a typed error,
// CompareAndSwap is NEVER called; an audit record with Accepted:false and
// the matching reason is emitted; the stored certification is returned
// unchanged alongside the wrapped typed error (errors.Is still succeeds
// against it even if the rejection-path audit call itself also errors —
// both errors are wrapped).
//
// Acceptance contract: the audit call happens strictly AFTER a successful
// CompareAndSwap, never before. If the auditor errors on the accept path,
// that error is returned wrapped, but the already-committed CAS is never
// undone — an audit failure is a (separately alarming) observability gap,
// not a reason to roll back a state change that has already taken effect.
func (d *CertificationDriver) transitionAndCommit(ctx context.Context, offeringOperationID string, current models.Certification, target models.CertificationState, verdict models.CapabilityTruth, retry models.RetryPolicy, reason CertificationAuditReason, suspension SuspensionReason, probeReason ProbeReasonCode) (models.Certification, error) {
	now := d.now()

	next, txErr := current.Transition(target, verdict, retry, now)
	if txErr != nil {
		rec := CertificationAuditRecord{
			OfferingOperationID: offeringOperationID,
			From:                current.State,
			To:                  target,
			Truth:               current.Truth,
			Accepted:            false,
			Reason:              auditReasonForTransitionError(txErr),
			Suspension:          suspension,
			ProbeReason:         probeReason,
			At:                  now,
		}
		if auditErr := d.auditor.CertificationTransitioned(ctx, rec); auditErr != nil {
			return current, fmt.Errorf("intelligence: transition rejected and audit failed for %q: %w: %w", offeringOperationID, txErr, auditErr)
		}
		return current, fmt.Errorf("intelligence: certification transition %s -> %s rejected for %q: %w", current.State, target, offeringOperationID, txErr)
	}

	if casErr := d.store.CompareAndSwap(ctx, current, next); casErr != nil {
		return current, fmt.Errorf("intelligence: compare-and-swap certification %q: %w", offeringOperationID, casErr)
	}

	rec := CertificationAuditRecord{
		OfferingOperationID: offeringOperationID,
		From:                current.State,
		To:                  next.State,
		Truth:               next.Truth,
		Accepted:            true,
		Reason:              reason,
		Suspension:          suspension,
		ProbeReason:         probeReason,
		At:                  now,
	}
	if auditErr := d.auditor.CertificationTransitioned(ctx, rec); auditErr != nil {
		return next, fmt.Errorf("intelligence: transition committed but audit failed for %q: %w (the compare-and-swap is not undone)", offeringOperationID, auditErr)
	}

	return next, nil
}

// auditReasonForTransitionError maps one of models.Certification.Transition's
// typed errors onto its CertificationAuditReason. Any error not among the
// three named budget/verdict errors is treated as a plain illegal
// transition (models.ErrIllegalCertificationTransition, or anything else
// this package does not specifically recognize).
func auditReasonForTransitionError(err error) CertificationAuditReason {
	switch {
	case errors.Is(err, models.ErrRetryBudgetExceeded):
		return AuditRetryBudgetExceeded
	case errors.Is(err, models.ErrVerdictRequired):
		return AuditVerdictRequired
	case errors.Is(err, models.ErrNoValidVerdict):
		return AuditNoValidVerdict
	default:
		return AuditIllegalTransition
	}
}
