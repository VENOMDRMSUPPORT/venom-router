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
