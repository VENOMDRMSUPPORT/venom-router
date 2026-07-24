package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/secrets"
)

// oauthVerifierAADPurpose is the fixed RecordIdentity.Purpose every PKCE
// verifier envelope this service handles is sealed/opened under —
// distinct from credentialAADPurpose so a verifier envelope can never be
// mistaken for (or decrypted as) a credential envelope even if the two
// ever collided on every other AAD field.
const oauthVerifierAADPurpose = "oauth_verifier"

// oauthVerifierAADKind is the fixed RecordIdentity.Kind for a PKCE
// verifier envelope.
const oauthVerifierAADKind = "pkce_verifier"

// oauthTransactionTTL is how long a pending OAuth transaction remains
// completable after Begin (P2b-PROV-006): ten minutes, after which
// Complete rejects it as expired even if the state/verifier are
// otherwise perfectly valid.
const oauthTransactionTTL = 10 * time.Minute

// oauthStateEntropyBytes/oauthVerifierEntropyBytes are the number of
// crypto/rand bytes drawn for the OAuth `state` and PKCE `verifier`
// respectively. 32 raw bytes base64url-encodes to 43 characters, safely
// inside RFC 7636's 43-128 character code_verifier range and far beyond
// any practical guessing threshold for `state`.
const (
	oauthStateEntropyBytes    = 32
	oauthVerifierEntropyBytes = 32
)

// ErrOAuthTransactionInvalid is returned by Complete whenever the
// supplied state cannot be matched to a still-pending, unexpired,
// not-yet-consumed transaction, OR the transaction's stored provider_id
// does not match the callback's provider — deliberately ONE sentinel for
// every one of these cases (no row / expired / already consumed /
// provider mismatch). Distinguishing them in the error itself would turn
// this endpoint into a state-guessing oracle; the caller (httpapi) is
// expected to surface a single generic, canary-safe rejection regardless
// of which case actually applied.
var ErrOAuthTransactionInvalid = errors.New("application: oauth: transaction is invalid, expired, or already used")

// ErrOAuthAccountAlreadyConnected is returned by Complete when the
// provider's identity resolves to an (providerID, externalID) pair an
// account already exists for. Re-linking/reauthenticating that existing
// account is out of scope for this unit (a future reauth-staging task) —
// this path creates nothing new and leaves the existing account
// untouched.
var ErrOAuthAccountAlreadyConnected = errors.New("application: oauth: this provider account is already connected")

// BeginOAuthParams is Begin's input.
type BeginOAuthParams struct {
	ProviderID string
	// Adapter is the provider's registered OAuthAdapter — injected so
	// this service stays provider-agnostic, mirroring ConnectService's
	// Adapter field.
	Adapter providers.OAuthAdapter
	// RedirectURI is the exact redirect_uri the provider will redirect
	// back to after the owner authorizes — echoed unchanged to
	// CompleteOAuth by Complete.
	RedirectURI string
}

// BeginOAuthResult is Begin's output: everything the caller needs to
// send the owner to the provider's authorization page and later poll
// for the outcome.
type BeginOAuthResult struct {
	TransactionID string
	AuthorizeURL  string
	ExpiresAt     time.Time
}

// CompleteOAuthParams is Complete's input.
type CompleteOAuthParams struct {
	ProviderID string
	// Adapter is the provider's registered OAuthAdapter, exactly as at
	// Begin — the callback handler looks it up again from the registry
	// rather than this service caching it across the two calls.
	Adapter providers.OAuthAdapter
	// RawState is the callback's `state` query parameter exactly as
	// received. It is hashed immediately and never stored or logged.
	RawState string
	// Code is the callback's `code` query parameter. It is handed to
	// Adapter.CompleteOAuth and NEVER persisted anywhere — no DB column,
	// no cache entry, no log line.
	Code string
	// RedirectURI must match the RedirectURI Begin used for this same
	// transaction.
	RedirectURI string
	// FundingMode is the provider's catalog funding mode (02 §2),
	// consulted only when OwnerFunding is nil — mirrors
	// ConnectAPIKeyAccountParams.FundingMode.
	FundingMode domain.FundingMode
	// OwnerFunding, when non-nil, overrides the provider's own funding
	// classification, exactly as ConnectAPIKeyAccountParams.OwnerFunding
	// does for the API-key flow.
	OwnerFunding *domain.Funding
}

// OAuthEnrollmentService is the provider-agnostic OAuth enrollment
// framework (P2b-PROV-006): PKCE generation, a replay-safe
// consume-before-exchange transaction lifecycle, and — on a successful
// exchange for a brand-new (provider, external_id) pair — the same
// atomic "account + first credential + first funding evidence" persist
// ConnectService performs for the API-key flow. Reauthentication of an
// already-connected account is explicitly out of scope (see
// ErrOAuthAccountAlreadyConnected); this leaves the seam ready for a
// future reauth-staging unit without building that logic now.
type OAuthEnrollmentService struct {
	tx         OAuthTransactionRepo
	enrollment EnrollmentPort
	accounts   AccountRepo
	kr         *secrets.Keyring
	newID      IDGenerator
	now        func() time.Time
}

// NewOAuthEnrollmentService builds the service. now defaults to
// time.Now when nil.
func NewOAuthEnrollmentService(tx OAuthTransactionRepo, enrollment EnrollmentPort, accounts AccountRepo, kr *secrets.Keyring, newID IDGenerator, now func() time.Time) *OAuthEnrollmentService {
	if now == nil {
		now = time.Now
	}
	return &OAuthEnrollmentService{tx: tx, enrollment: enrollment, accounts: accounts, kr: kr, newID: newID, now: now}
}

// Begin starts a new OAuth transaction (P2b-PROV-006 §Begin): a
// high-entropy `state` and PKCE `verifier` are minted via crypto/rand,
// the verifier is encrypted and persisted (keyed by hex(sha256(state)) —
// the raw state itself is never stored), and the adapter's authorize URL
// is returned. The raw state is returned to the caller ONLY as part of
// the authorizeURL the adapter constructs (or, for adapters that don't
// embed it there, is not returned at all by this method) — Begin itself
// never returns the raw state value.
func (s *OAuthEnrollmentService) Begin(ctx context.Context, p BeginOAuthParams) (BeginOAuthResult, error) {
	state, err := randomURLSafeString(oauthStateEntropyBytes)
	if err != nil {
		return BeginOAuthResult{}, fmt.Errorf("application: oauth: generate state: %w", err)
	}
	verifier, err := randomURLSafeString(oauthVerifierEntropyBytes)
	if err != nil {
		return BeginOAuthResult{}, fmt.Errorf("application: oauth: generate verifier: %w", err)
	}
	challenge := pkceChallengeS256(verifier)

	transactionID := s.newID()
	now := s.now()
	expiresAt := now.Add(oauthTransactionTTL)

	verifierIdentity := secrets.RecordIdentity{
		Purpose:  oauthVerifierAADPurpose,
		Provider: p.ProviderID,
		Account:  "",
		Record:   transactionID,
		Kind:     oauthVerifierAADKind,
	}
	verifierEnv, err := secrets.Encrypt(s.kr, verifierIdentity, []byte(verifier))
	if err != nil {
		return BeginOAuthResult{}, fmt.Errorf("application: oauth: encrypt verifier: %w", err)
	}

	stateHash := hashState(state)
	if err := s.tx.Create(ctx, stateHash, p.ProviderID, transactionID, verifierEnv, now, expiresAt); err != nil {
		return BeginOAuthResult{}, fmt.Errorf("application: oauth: persist transaction: %w", err)
	}

	authorizeURL, err := p.Adapter.BeginOAuth(ctx, p.RedirectURI, state, challenge)
	if err != nil {
		return BeginOAuthResult{}, fmt.Errorf("application: oauth: adapter BeginOAuth: %w", err)
	}

	return BeginOAuthResult{TransactionID: transactionID, AuthorizeURL: authorizeURL, ExpiresAt: expiresAt}, nil
}

// Complete finishes an OAuth transaction (P2b-PROV-006 §Complete),
// replay-safe by construction: the transaction is atomically consumed
// (ConsumeByStateHash) BEFORE the provider's code is ever exchanged, so
// two concurrent callers with the identical state can never both reach
// the adapter exchange — only the one that wins the atomic consume
// proceeds; the loser is rejected with ErrOAuthTransactionInvalid and the
// adapter's CompleteOAuth is never called for it.
//
// transactionID is returned whenever the transaction was successfully
// consumed (i.e. whenever the caller now owns exclusive responsibility
// for reporting this transaction's outcome), REGARDLESS of whether the
// rest of Complete (the adapter exchange, or the enrollment persist)
// subsequently succeeds — this lets httpapi cache a terminal
// pending/failed result against the right transaction id even when the
// failure happens after consume. transactionID is "" only when consume
// itself did not win (ErrOAuthTransactionInvalid) — there is nothing to
// report in that case; the row (if any) is untouched.
func (s *OAuthEnrollmentService) Complete(ctx context.Context, p CompleteOAuthParams) (transactionID string, account domain.Account, err error) {
	now := s.now()
	stateHash := hashState(p.RawState)

	rowProviderID, rowTransactionID, verifierEnv, ok, err := s.tx.ConsumeByStateHash(ctx, stateHash, now)
	if err != nil {
		return "", domain.Account{}, fmt.Errorf("application: oauth: consume transaction: %w", err)
	}
	if !ok {
		return "", domain.Account{}, ErrOAuthTransactionInvalid
	}

	// Constant-time compare: the row is already irreversibly consumed by
	// the line above, so a mismatch here rejects with nothing left to
	// undo — there is no "un-consume" path, by design (a mismatched
	// provider on this state must never get a second chance).
	if subtle.ConstantTimeCompare([]byte(rowProviderID), []byte(p.ProviderID)) != 1 {
		return rowTransactionID, domain.Account{}, ErrOAuthTransactionInvalid
	}

	verifierIdentity := secrets.RecordIdentity{
		Purpose:  oauthVerifierAADPurpose,
		Provider: rowProviderID,
		Account:  "",
		Record:   rowTransactionID,
		Kind:     oauthVerifierAADKind,
	}
	verifier, err := secrets.Decrypt(s.kr, verifierIdentity, verifierEnv)
	if err != nil {
		return rowTransactionID, domain.Account{}, fmt.Errorf("application: oauth: decrypt verifier: %w", err)
	}
	defer zeroBytes(verifier)

	identity, storedCreds, err := p.Adapter.CompleteOAuth(ctx, p.Code, string(verifier), p.RedirectURI)
	if err != nil {
		switch {
		case errors.Is(err, providers.ErrInvalidCredential):
			return rowTransactionID, domain.Account{}, providers.ErrInvalidCredential
		case errors.Is(err, providers.ErrProviderUnavailable):
			return rowTransactionID, domain.Account{}, providers.ErrProviderUnavailable
		default:
			return rowTransactionID, domain.Account{}, fmt.Errorf("application: oauth: adapter CompleteOAuth: %w", err)
		}
	}

	if _, ok, err := s.accounts.GetByProviderExternalID(ctx, rowProviderID, identity.ExternalID); err != nil {
		return rowTransactionID, domain.Account{}, fmt.Errorf("application: oauth: check existing account: %w", err)
	} else if ok {
		return rowTransactionID, domain.Account{}, ErrOAuthAccountAlreadyConnected
	}

	accountID := s.newID()
	credentialID := s.newID()
	fundingID := s.newID()

	newAccount := domain.Account{
		ID:              accountID,
		ProviderID:      rowProviderID,
		ExternalID:      identity.ExternalID,
		AuthType:        "oauth2",
		ConnectionState: domain.ConnectionConnected,
		HealthState:     domain.HealthUnknown,
		IdentityEmail:   identity.Email,
		IdentityPlan:    identity.Plan,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	funding, err := s.firstFundingEvidence(p, identity, accountID, fundingID, now)
	if err != nil {
		return rowTransactionID, domain.Account{}, fmt.Errorf("application: oauth: stamp funding: %w", err)
	}

	fingerprint := fingerprintCredentialKey(storedCreds.Value)
	credIdentity := secrets.RecordIdentity{
		Purpose:  credentialAADPurpose,
		Provider: rowProviderID,
		Account:  accountID,
		Record:   credentialID,
		Kind:     string(domain.CredentialKindOAuth2),
	}
	credEnv, err := secrets.Encrypt(s.kr, credIdentity, []byte(storedCreds.Value))
	if err != nil {
		return rowTransactionID, domain.Account{}, fmt.Errorf("application: oauth: encrypt credential: %w", err)
	}
	cred := domain.Credential{ID: credentialID, AccountID: accountID, Kind: domain.CredentialKindOAuth2, State: domain.CredentialActive, Fingerprint: fingerprint}

	if err := s.enrollment.CreateConnectedAccount(ctx, newAccount, rowProviderID, cred, credEnv, funding); err != nil {
		return rowTransactionID, domain.Account{}, fmt.Errorf("application: oauth: enrollment: %w", err)
	}

	return rowTransactionID, newAccount, nil
}

// firstFundingEvidence mirrors ConnectService.firstFundingEvidence for
// the OAuth flow: an owner override always wins; otherwise
// domain.StampFirstEvidence applies p.FundingMode, using identity.Funding
// (the provider's own connect-time classification) as the value for
// FundingModeProviderEvidence — the one mode where the provider's
// response, not a fixed catalog default, is the source of truth.
func (s *OAuthEnrollmentService) firstFundingEvidence(p CompleteOAuthParams, identity providers.IdentityResult, accountID, fundingID string, now time.Time) (domain.FundingEvidence, error) {
	if p.OwnerFunding != nil {
		return domain.FundingEvidence{
			ID: fundingID, AccountID: accountID, Funding: *p.OwnerFunding,
			Source: domain.FundingSourceOwnerOverride, Confidence: 1.0, ObservedAt: now,
		}, nil
	}

	value := domain.FundingUnknown
	confidence := 1.0
	if p.FundingMode == domain.FundingModeProviderEvidence {
		switch identity.Funding {
		case string(domain.FundingFree):
			value = domain.FundingFree
		case string(domain.FundingPaid):
			value = domain.FundingPaid
		default:
			value = domain.FundingUnknown
		}
	}

	stamped, err := domain.StampFirstEvidence(p.FundingMode, accountID, value, false, confidence, now)
	if err != nil {
		return domain.FundingEvidence{}, err
	}
	stamped.ID = fundingID
	return stamped, nil
}

// hashState returns hex(sha256(state)) — the ONLY form of the OAuth
// `state` value this package ever passes to a storage port. The raw
// state itself is never a Create/ConsumeByStateHash argument.
func hashState(state string) string {
	sum := sha256.Sum256([]byte(state))
	return hex.EncodeToString(sum[:])
}

// randomURLSafeString draws n bytes from crypto/rand and returns their
// unpadded base64url encoding — used for both the OAuth `state` and the
// PKCE `verifier` (RFC 7636 requires the verifier be composed of
// unreserved URL-safe characters; base64url satisfies that directly).
func randomURLSafeString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate random bytes: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// pkceChallengeS256 derives the RFC 7636 S256 code_challenge from a
// code_verifier: BASE64URL-ENCODE(SHA256(ASCII(verifier))), no padding.
func pkceChallengeS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
