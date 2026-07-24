package domain

import (
	"errors"
	"fmt"
	"time"
)

// CredentialKind identifies the mechanism/purpose of one credential row
// (02 §3). An account may hold multiple active credentials of different
// kinds simultaneously (e.g. a github_oauth AND a copilot_service
// credential on one account) but never two active credentials of the
// same kind. The set is extensible — provider-specific kinds may be added
// as needed.
type CredentialKind string

const (
	CredentialKindAPIKey         CredentialKind = "api_key"
	CredentialKindOAuth2         CredentialKind = "oauth2"
	CredentialKindGitHubOAuth    CredentialKind = "github_oauth"
	CredentialKindCopilotService CredentialKind = "copilot_service"
)

// CredentialState is a credential row's lifecycle state (02 §3).
type CredentialState string

const (
	CredentialActive  CredentialState = "active"
	CredentialStaged  CredentialState = "staged"
	CredentialRetired CredentialState = "retired"
)

// Credential is the domain-visible shape of one account_credentials row.
// Secret material (nonce/ciphertext/key material) never enters this
// package — only identity, kind, state, and a fingerprint for dedup.
type Credential struct {
	ID          string
	AccountID   string
	Kind        CredentialKind
	State       CredentialState
	Fingerprint string
	ExpiresAt   *time.Time
	RetiredAt   *time.Time
}

// ErrCredentialActiveConflict is returned when adding a new active
// credential would create a second active credential of the same kind
// for an account (02 §3's cardinality invariant).
var ErrCredentialActiveConflict = errors.New("domain: an active credential of this kind already exists for this account")

// ErrReauthenticationInProgress is returned when staging a new
// credential would create a second staged credential of the same kind
// for an account (02 §3 / 03 §2e). The existing staged row is never
// silently replaced.
var ErrReauthenticationInProgress = errors.New("domain: reauthentication_in_progress: a staged credential of this kind already exists for this account")

// CanAddActiveCredential reports whether adding a new active credential
// of kind to an account already holding existing is legal: at most one
// active credential per (account, kind); different kinds coexist;
// retired rows never conflict.
func CanAddActiveCredential(existing []Credential, kind CredentialKind) error {
	for _, c := range existing {
		if c.Kind == kind && c.State == CredentialActive {
			return ErrCredentialActiveConflict
		}
	}
	return nil
}

// CanStageCredential reports whether staging a new candidate credential
// of kind (for reauthentication) is legal: at most one staged credential
// per (account, kind). An active and a staged row of the same kind
// coexist during reauthentication; retired rows never conflict.
func CanStageCredential(existing []Credential, kind CredentialKind) error {
	for _, c := range existing {
		if c.Kind == kind && c.State == CredentialStaged {
			return ErrReauthenticationInProgress
		}
	}
	return nil
}

// ErrCandidateNotStaged is returned by SwapStagedToActive when candidate
// is not in CredentialStaged state (and this is not an idempotent replay
// — see SwapStagedToActive).
var ErrCandidateNotStaged = errors.New("domain: SwapStagedToActive: candidate credential is not staged")

// StageSwapResult is the outcome of SwapStagedToActive.
type StageSwapResult struct {
	Applied            bool        // false when this call was an idempotent no-op
	TransactionKey     string      // threaded through from the call, for the caller's own audit/idempotency bookkeeping
	RetiredCredential  *Credential // the prior active credential, now retired; nil if there was none
	PromotedCredential Credential  // the credential that is now active
}

// SwapStagedToActive performs the atomic reauthentication swap (02 §3 /
// 03 §2e): the account's prior active credential of the same kind, if
// any, is retired (RetiredAt stamped to now) and candidate is promoted
// from staged to active — modeled as a pure transformation; the
// application/storage layer performs the real transaction.
//
// transactionKey (the single-use OAuth state, or an advisory-lock key)
// identifies which reauthentication drove this swap; it is threaded
// through to the result for the caller's own bookkeeping. This
// function's own idempotency guard is structural, not key-based: if
// active is already CredentialRetired — meaning an earlier application
// of this exact swap already committed — replaying is a no-op: nothing
// is re-retired, nothing is re-promoted, and Applied is false. A
// candidate that is not staged (and this isn't that replay case) is
// rejected with ErrCandidateNotStaged rather than silently promoted.
func SwapStagedToActive(active *Credential, candidate Credential, transactionKey string, now time.Time) (StageSwapResult, error) {
	if active != nil && active.State == CredentialRetired {
		return StageSwapResult{Applied: false, TransactionKey: transactionKey, PromotedCredential: candidate}, nil
	}
	if candidate.State != CredentialStaged {
		return StageSwapResult{}, fmt.Errorf("%w: state = %s", ErrCandidateNotStaged, candidate.State)
	}

	result := StageSwapResult{Applied: true, TransactionKey: transactionKey}
	if active != nil {
		retired := *active
		retired.State = CredentialRetired
		retired.RetiredAt = &now
		result.RetiredCredential = &retired
	}

	promoted := candidate
	promoted.State = CredentialActive
	result.PromotedCredential = promoted

	return result, nil
}
