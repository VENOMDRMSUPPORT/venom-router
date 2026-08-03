package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/secrets"
)

// IDGenerator mints a fresh unique id for a new account/credential/
// funding-evidence row. Injected so this service never hard-codes an ID
// scheme; tests supply deterministic ids.
type IDGenerator func() string

// ErrConnectInvalidCredential is returned when the provider adapter
// classifies the supplied key as genuinely invalid (03 §1's authentic
// validation rule, PROV-004). No account/credential/funding row is
// created.
var ErrConnectInvalidCredential = errors.New("application: connect: the provided credential is invalid")

// ErrConnectProviderUnavailable is returned when the provider could not
// be reached or answered ambiguously — the key's validity is unknown,
// so (fail closed) no row is created either; the caller should retry.
var ErrConnectProviderUnavailable = errors.New("application: connect: the provider is currently unavailable, try again later")

// ErrConnectAccountAlreadyConnected is returned when the adapter's
// validated identity resolves to a (provider_id, external_id) pair an
// account already exists for (P2b-CAPI-003). Unlike the OAuth flow —
// where an identity that resolves to an existing account is always a
// reauthentication (P2b-PROV-008) — the API-key connect flow has no
// staged-reauthentication path of its own this phase, so a duplicate
// identity here is a straightforward, friendly conflict: nothing is
// created, and the caller should use the existing account (or its own
// reauthentication surface, once one exists for API-key accounts)
// instead of enrolling a second one for the same identity.
var ErrConnectAccountAlreadyConnected = errors.New("application: connect: an account for this provider identity already exists")

// ConnectService performs the API-key connect-time enrollment flow (03
// §2b): validate the key through the provider's APIKeyAdapter, then —
// ONLY on a valid result — atomically create the account, its first
// credential (encrypted), and its first funding-evidence row via
// EnrollmentPort. An invalid or unavailable key creates NOTHING (fail
// closed): there is no path that leaves a partially-enrolled account
// behind a rejected credential. A validated identity that already
// resolves to an existing account is likewise rejected (fail closed)
// with ErrConnectAccountAlreadyConnected, checked via AccountRepo BEFORE
// any row is created — mirroring OAuthEnrollmentService.Complete's own
// GetByProviderExternalID lookup, rather than relying on the storage
// layer's UNIQUE constraint to surface an untyped error after the fact.
type ConnectService struct {
	enrollment EnrollmentPort
	accounts   AccountRepo
	kr         *secrets.Keyring
	newID      IDGenerator
	now        func() time.Time
}

// NewConnectService builds the service. now defaults to time.Now when nil.
func NewConnectService(enrollment EnrollmentPort, accounts AccountRepo, kr *secrets.Keyring, newID IDGenerator, now func() time.Time) *ConnectService {
	if now == nil {
		now = time.Now
	}
	return &ConnectService{enrollment: enrollment, accounts: accounts, kr: kr, newID: newID, now: now}
}

// ConnectAPIKeyAccountParams is ConnectAPIKeyAccount's input.
type ConnectAPIKeyAccountParams struct {
	ProviderID string
	// Adapter is the provider's registered APIKeyAdapter (e.g.
	// providers.NewOpenCodeZenAdapter's result) — injected so this
	// service is provider-agnostic.
	Adapter providers.APIKeyAdapter
	// PlaintextKey is the owner-submitted key. It is never persisted or
	// logged directly — only the encrypted envelope PROV-003's scheme
	// produces from it is ever stored.
	PlaintextKey string
	// FundingMode is the provider's catalog funding mode (02 §2),
	// consulted only when OwnerFunding is nil.
	FundingMode domain.FundingMode
	// OwnerFunding, when non-nil, overrides the provider's own funding
	// classification: the first evidence row is stamped owner_override
	// instead of whatever FundingMode would otherwise produce (03 §2b).
	OwnerFunding *domain.Funding
}

// ConnectAPIKeyAccount implements the flow described on ConnectService's
// doc comment. It returns the newly created domain.Account on success.
func (s *ConnectService) ConnectAPIKeyAccount(ctx context.Context, p ConnectAPIKeyAccountParams) (domain.Account, error) {
	identity, storedCreds, err := p.Adapter.ConnectAPIKey(ctx, p.PlaintextKey)
	if err != nil {
		switch {
		case errors.Is(err, providers.ErrInvalidCredential):
			return domain.Account{}, ErrConnectInvalidCredential
		case errors.Is(err, providers.ErrProviderUnavailable):
			return domain.Account{}, ErrConnectProviderUnavailable
		default:
			return domain.Account{}, fmt.Errorf("application: connect: adapter error: %w", err)
		}
	}

	if _, foundExisting, err := s.accounts.GetByProviderExternalID(ctx, p.ProviderID, identity.ExternalID); err != nil {
		return domain.Account{}, fmt.Errorf("application: connect: check existing account: %w", err)
	} else if foundExisting {
		return domain.Account{}, ErrConnectAccountAlreadyConnected
	}

	now := s.now()
	accountID := s.newID()
	credentialID := s.newID()
	fundingID := s.newID()

	account := domain.Account{
		ID:              accountID,
		ProviderID:      p.ProviderID,
		ExternalID:      identity.ExternalID,
		AuthType:        "api_key",
		ConnectionState: domain.ConnectionConnected,
		// HealthState starts HEALTHY, check-timestamp stamped: reaching this
		// point means p.Adapter.ConnectAPIKey AUTHENTICATED the key (03 §1's
		// authentic-validation rule, PROV-004 — an invalid or unreachable key
		// returns above and creates nothing). That successful authenticated
		// call IS a health check, so leaving the account HealthUnknown /
		// "Checked: —" here would understate a credential we just proved live.
		// A later credential death is caught by the ongoing usability sweep /
		// health re-checks, which flip it to expired.
		HealthState:       domain.HealthHealthy,
		LastHealthCheckAt: &now,
		IdentityPlan:      identity.Plan,
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	funding, err := s.firstFundingEvidence(p, accountID, fundingID, now)
	if err != nil {
		return domain.Account{}, fmt.Errorf("application: connect: stamp funding: %w", err)
	}

	fingerprint := fingerprintCredentialKey(storedCreds.Value)
	recordIdentity := secrets.RecordIdentity{
		Purpose:  credentialAADPurpose,
		Provider: p.ProviderID,
		Account:  accountID,
		Record:   credentialID,
		Kind:     string(domain.CredentialKindAPIKey),
	}
	env, err := secrets.Encrypt(s.kr, recordIdentity, []byte(storedCreds.Value))
	if err != nil {
		return domain.Account{}, fmt.Errorf("application: connect: encrypt credential: %w", err)
	}
	cred := domain.Credential{ID: credentialID, AccountID: accountID, Kind: domain.CredentialKindAPIKey, State: domain.CredentialActive, Fingerprint: fingerprint}

	if err := s.enrollment.CreateConnectedAccount(ctx, account, p.ProviderID, cred, env, funding); err != nil {
		return domain.Account{}, fmt.Errorf("application: connect: enrollment: %w", err)
	}

	return account, nil
}

func (s *ConnectService) firstFundingEvidence(p ConnectAPIKeyAccountParams, accountID, fundingID string, now time.Time) (domain.FundingEvidence, error) {
	if p.OwnerFunding != nil {
		return domain.FundingEvidence{
			ID: fundingID, AccountID: accountID, Funding: *p.OwnerFunding,
			Source: domain.FundingSourceOwnerOverride, Confidence: 1.0, ObservedAt: now,
		}, nil
	}

	// opencode-zen's catalog mode is owner_policy/free (03 §3); the
	// "value" StampFirstEvidence uses for FundingModeOwnerPolicy is the
	// catalog's declared default, free.
	stamped, err := domain.StampFirstEvidence(p.FundingMode, accountID, domain.FundingFree, false, 1.0, now)
	if err != nil {
		return domain.FundingEvidence{}, err
	}
	stamped.ID = fundingID
	return stamped, nil
}
