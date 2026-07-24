package domain

import (
	"errors"
	"fmt"
	"time"
)

// Funding is an account's classified funding status (02 §2). All
// Offerings under an account inherit this value; there is no
// per-Offering override.
type Funding string

const (
	FundingFree    Funding = "free"
	FundingPaid    Funding = "paid"
	FundingUnknown Funding = "unknown"
)

// FundingSource is the single canonical vocabulary for a funding-evidence
// row's authority origin (02 §2). No other value (e.g. a bare
// "provider_default") may appear in code, schema, or UI.
type FundingSource string

const (
	FundingSourceProviderPolicy   FundingSource = "provider_policy"
	FundingSourceProviderEvidence FundingSource = "provider_evidence"
	FundingSourceOwnerPolicy      FundingSource = "owner_policy"
	FundingSourceOwnerOverride    FundingSource = "owner_override"
)

// FundingMode is a provider definition's default funding policy (02 §2),
// used only to decide how the first evidence row for a newly connected
// account is stamped.
type FundingMode string

const (
	FundingModeFixed            FundingMode = "fixed"
	FundingModeOwnerPolicy      FundingMode = "owner_policy"
	FundingModeProviderEvidence FundingMode = "provider_evidence"
	FundingModeEvidenceRequired FundingMode = "evidence_required"
)

// FundingEvidence is one append-only funding-evidence row (02 §2 / §5).
// No secret material — funding classification is never a secret.
type FundingEvidence struct {
	ID           string
	AccountID    string
	Funding      Funding
	Source       FundingSource
	Locked       bool // true iff this is a provider_policy row from a `fixed` funding_mode with funding_locked=1
	Confidence   float64
	Reason       string
	ObservedAt   time.Time
	SupersededAt *time.Time // nil = current
}

// IsCurrent reports whether e is a non-superseded row.
func (e FundingEvidence) IsCurrent() bool {
	return e.SupersededAt == nil
}

// IsUnclassified reports whether e is the evidence_required "cannot
// classify" stamp: unknown funding recorded with source = provider_policy
// and not locked (02 §2). An account whose current row satisfies this is
// excluded from all production routing (and never Lite-eligible) until a
// classifying row (provider_evidence or an owner action) supersedes it.
// This is a funding-classification predicate, distinct from DOM-001's
// routing-eligibility projection (ProjectEligibility).
func (e FundingEvidence) IsUnclassified() bool {
	return e.Funding == FundingUnknown && e.Source == FundingSourceProviderPolicy && !e.Locked
}

// CurrentFundingRow returns the one non-superseded row in rows — "exactly
// one current row" is the append-only supersession invariant (02 §2),
// structurally enforced in storage by a partial-unique index; this
// package just reads whichever one satisfies it. ok is false if rows is
// empty or none has SupersededAt == nil.
func CurrentFundingRow(rows []FundingEvidence) (FundingEvidence, bool) {
	for _, r := range rows {
		if r.IsCurrent() {
			return r, true
		}
	}
	return FundingEvidence{}, false
}

// StampFirstEvidence returns an account's first funding-evidence row at
// connect time, per 02 §2's "which mode stamps the first row" table:
// fixed → provider_policy; owner_policy → owner_policy; provider_evidence
// → provider_evidence; evidence_required → provider_policy with
// funding = unknown, never locked (it records that the catalog cannot
// classify the account, never fabricated evidence).
//
// value/locked/confidence are the caller-supplied inputs for whichever
// mode applies (e.g. the provider's fixed value + locked flag for
// FundingModeFixed, or the fetched provider-evidence classification +
// confidence for FundingModeProviderEvidence); they are ignored for
// FundingModeEvidenceRequired, which always stamps unknown/unlocked
// regardless of what is passed.
func StampFirstEvidence(mode FundingMode, accountID string, value Funding, locked bool, confidence float64, now time.Time) (FundingEvidence, error) {
	row := FundingEvidence{AccountID: accountID, ObservedAt: now}

	switch mode {
	case FundingModeFixed:
		row.Funding = value
		row.Source = FundingSourceProviderPolicy
		row.Locked = locked
		row.Confidence = confidence
	case FundingModeOwnerPolicy:
		row.Funding = value
		row.Source = FundingSourceOwnerPolicy
		row.Confidence = confidence
	case FundingModeProviderEvidence:
		row.Funding = value
		row.Source = FundingSourceProviderEvidence
		row.Confidence = confidence
	case FundingModeEvidenceRequired:
		row.Funding = FundingUnknown
		row.Source = FundingSourceProviderPolicy
		row.Locked = false
	default:
		return FundingEvidence{}, fmt.Errorf("domain: StampFirstEvidence: unknown FundingMode %q", mode)
	}

	return row, nil
}

// ErrFundingLocked is returned when a candidate evidence row attempts to
// supersede a locked provider_policy row (02 §2 rule 1). It carries the
// funding_locked reason code the control API surfaces on rejection.
var ErrFundingLocked = errors.New("domain: funding_locked: current row is an immutable locked provider_policy row")

// DecideFundingSupersession applies 02 §2's 4-rule funding authority to
// decide whether candidate may supersede current, current being the
// account's presently current (non-superseded) row:
//
//  1. current is a locked provider_policy row → always rejected, with
//     ErrFundingLocked.
//  2. current is owner_override and candidate is not → rejected (nil
//     error: this is an ordinary "not fresher" outcome, not a violation
//     — only a subsequent owner_override supersedes an owner_override).
//  3. candidate is provider_evidence superseding a provider_evidence or
//     owner_policy current row → allowed only if candidate is fresher
//     AND at least as confident; otherwise rejected (nil error — stale
//     or lower-confidence evidence simply doesn't win, it isn't invalid).
//  4. anything else (an owner action, or a candidate whose current row
//     isn't locked/isn't owner_override) supersedes — this is the
//     "absent the above" default the rules describe.
func DecideFundingSupersession(current FundingEvidence, candidate FundingEvidence, now time.Time) (bool, error) {
	if current.Source == FundingSourceProviderPolicy && current.Locked {
		return false, ErrFundingLocked
	}

	if current.Source == FundingSourceOwnerOverride && candidate.Source != FundingSourceOwnerOverride {
		return false, nil
	}

	if candidate.Source == FundingSourceProviderEvidence &&
		(current.Source == FundingSourceProviderEvidence || current.Source == FundingSourceOwnerPolicy) {
		fresher := candidate.ObservedAt.After(current.ObservedAt)
		confidentEnough := candidate.Confidence >= current.Confidence
		return fresher && confidentEnough, nil
	}

	return true, nil
}

// FundingSupersessionResult is the outcome of ApplyFundingSupersession.
type FundingSupersessionResult struct {
	Superseded     bool
	UpdatedCurrent FundingEvidence // current with SupersededAt stamped; only meaningful when Superseded
	NewCurrent     FundingEvidence // candidate, promoted to current; only meaningful when Superseded
}

// ApplyFundingSupersession decides (via DecideFundingSupersession)
// whether candidate supersedes current and, if so, returns current with
// SupersededAt stamped to now plus candidate as the new current row.
// Neither current nor candidate is mutated in place. On rejection, the
// result's Superseded is false and err is non-nil only for the locked
// case (ErrFundingLocked); the ordinary "not fresher/not owner action"
// rejections return a false Superseded with a nil error, since the
// current row simply remains in force rather than being invalid.
func ApplyFundingSupersession(current FundingEvidence, candidate FundingEvidence, now time.Time) (FundingSupersessionResult, error) {
	ok, err := DecideFundingSupersession(current, candidate, now)
	if err != nil {
		return FundingSupersessionResult{}, err
	}
	if !ok {
		return FundingSupersessionResult{Superseded: false}, nil
	}

	stampedCurrent := current
	stampedCurrent.SupersededAt = &now
	return FundingSupersessionResult{
		Superseded:     true,
		UpdatedCurrent: stampedCurrent,
		NewCurrent:     candidate,
	}, nil
}
