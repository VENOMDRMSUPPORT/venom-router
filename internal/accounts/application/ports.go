package application

import (
	"context"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/secrets"
)

// CredentialRepo is the storage port CredentialService depends on
// (P2b-PROV-003). It is declared purely in terms of accounts/domain
// types, secrets.Envelope, and stdlib — never a storage type — so this
// package never imports internal/storage. internal/storage implements
// this interface structurally (internal/storage/account_credentials.go's
// *AccountCredentialRepo already has every one of these methods with
// these exact signatures); the composition root supplies the concrete
// value.
type CredentialRepo interface {
	// ListForAccount returns every credential row (active, staged, and
	// retired) for accountID — the full set DOM-003's cardinality rules
	// (CanAddActiveCredential/CanStageCredential) need to see.
	ListForAccount(ctx context.Context, accountID string) ([]domain.Credential, error)

	// FingerprintExists reports whether a non-retired credential with
	// fingerprint already exists for providerID (03 §2c's dedup rule).
	FingerprintExists(ctx context.Context, providerID, fingerprint string) (bool, error)

	// Create persists a new credential row: cred's domain-visible
	// fields, the owning providerID, the encrypted envelope, and the
	// creation time. Never receives plaintext.
	Create(ctx context.Context, providerID string, cred domain.Credential, env secrets.Envelope, createdAt time.Time) error

	// GetCredential reads a credential back by id, including the
	// providerID it belongs to and the envelope needed to decrypt it.
	// ok is false if no such row exists.
	GetCredential(ctx context.Context, id string) (cred domain.Credential, providerID string, env secrets.Envelope, ok bool, err error)
}

// AccountRepo is the read-side account port (used by connect-time sync
// and later units to look up an existing account by its provider
// identity).
type AccountRepo interface {
	GetByID(ctx context.Context, id string) (domain.Account, bool, error)
	GetByProviderExternalID(ctx context.Context, providerID, externalID string) (domain.Account, bool, error)
}

// FundingRepo is the read-side funding-evidence port.
type FundingRepo interface {
	CurrentForAccount(ctx context.Context, accountID string) (domain.FundingEvidence, bool, error)
}

// EnrollmentPort is the atomic "account + first credential + first
// funding evidence" insert 03 §2b requires (P2b-PROV-003 §3a): all
// three rows are created in one transaction on the storage side, or
// none are. PROV-005's connect-time sync service calls this directly
// (not through CredentialService, which only adds a credential to an
// account that already exists).
type EnrollmentPort interface {
	CreateConnectedAccount(
		ctx context.Context,
		account domain.Account,
		providerID string,
		cred domain.Credential,
		credEnv secrets.Envelope,
		funding domain.FundingEvidence,
	) error
}

// OAuthTransactionRepo is the storage port OAuthEnrollmentService depends
// on (P2b-PROV-006): the oauth_transactions table (migration 00003),
// exposed purely in terms of stdlib and secrets.Envelope — never a
// storage/database type — so this package never imports internal/storage.
// internal/storage implements this interface structurally
// (internal/storage/oauth_transactions.go); the composition root supplies
// the concrete value.
//
// Every method here is deliberately silent about the raw OAuth `state`
// value: callers pass and this port stores only hex(sha256(state)) — the
// raw state string is never a parameter or return value anywhere in this
// interface.
type OAuthTransactionRepo interface {
	// Create persists a new pending oauth_transactions row keyed by
	// stateHash (hex(sha256(state))). verifierEnv is the PKCE verifier's
	// encrypted envelope — this method never sees the verifier plaintext.
	// expiresAt is the absolute expiry (createdAt + the service's TTL).
	Create(ctx context.Context, stateHash, providerID, transactionID string, verifierEnv secrets.Envelope, createdAt, expiresAt time.Time) error

	// ConsumeByStateHash is the single replay-safe operation this port
	// exists for: in ONE storage-side transaction, it captures the row
	// named by stateHash (its provider_id, transaction_id, and verifier
	// envelope) and marks it consumed via a guarded UPDATE ... WHERE
	// consumed = 0 whose affected-row count must be exactly 1 — the
	// anti-replay invariant a concurrent second caller cannot also
	// satisfy. ok is false — with every other return zeroed and the row
	// left completely unchanged — for every failure case uniformly: no
	// such row, already consumed, or expired. These cases are
	// deliberately NOT distinguished from one another (a canary-safe,
	// non-oracle-shaped API): the caller learns only "this attempt did
	// not win", never which of the three reasons applied.
	ConsumeByStateHash(ctx context.Context, stateHash string, now time.Time) (providerID, transactionID string, verifierEnv secrets.Envelope, ok bool, err error)

	// GetStatusByTransactionID reads just enough of a row's lifecycle to
	// derive pending/expired for the status endpoint when no cached
	// terminal result exists: whether a row named by transactionID
	// exists at all (ok), whether it is already consumed, and whether
	// now is past its expiry. It deliberately never returns the
	// envelope or provider_id — the status endpoint has no need for
	// either, and this keeps a read-only status lookup from ever having
	// secret-shaped data pass through it.
	GetStatusByTransactionID(ctx context.Context, transactionID string, now time.Time) (consumed bool, expired bool, ok bool, err error)
}

// ReauthRepo is the storage port the OAuth reauthentication-staging flow
// depends on (P2b-PROV-008, 03 section 2e): stage a new credential
// alongside an account's existing active one, atomically swap it in
// (retiring the old active credential first) or roll a failed attempt
// back, and sweep staged rows a crash left behind - all in terms of
// accounts/domain types and secrets.Envelope, never a storage type, so
// this package never imports internal/storage. internal/storage
// implements this interface structurally (internal/storage/reauth.go);
// the composition root supplies the concrete value.
type ReauthRepo interface {
	// StageCredential inserts a new 'staged' account_credentials row for
	// accountID and sets accounts.reauth_in_progress = 1, in ONE
	// transaction.
	StageCredential(ctx context.Context, accountID, providerID string, cred domain.Credential, env secrets.Envelope, now time.Time) error

	// DiscardStaged deletes credentialID's row (only if it is currently
	// staged and belongs to accountID) and clears
	// accounts.reauth_in_progress for accountID, in ONE transaction. Used
	// both to roll back a failed validation/swap attempt and by the
	// crash-recovery sweep; the account's active credential (if any) is
	// never touched.
	DiscardStaged(ctx context.Context, accountID, credentialID string) error

	// SwapStagedToActive performs 03 section 2e's atomic reauthentication
	// swap in ONE transaction: any existing active credential of kind for
	// accountID is retired FIRST (state='retired', retired_at=now) -
	// before stagedCredentialID is ever promoted - so the transaction can
	// never observe two simultaneously-active rows of the same kind even
	// mid-flight; the M2 idx_cred_active_per_kind partial-unique index is
	// the structural backstop behind this ordering. On success it also
	// sets accounts.health_state = 'healthy' and clears
	// reauth_in_progress.
	SwapStagedToActive(ctx context.Context, accountID, providerID string, kind domain.CredentialKind, stagedCredentialID string, now time.Time) error

	// StaleStagedCredentials returns every staged credential row whose
	// created_at is older than olderThan - the crash-recovery sweep's
	// input (P2b-PROV-008 section 5): a process crash between staging and
	// the swap/rollback would otherwise leave a staged row and
	// reauth_in_progress=1 behind indefinitely.
	StaleStagedCredentials(ctx context.Context, olderThan time.Time) ([]domain.Credential, error)
}
