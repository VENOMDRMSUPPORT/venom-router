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

// ErrOAuthAccountIdentityMismatch is returned by Complete when
// ReauthAccountID is non-empty (a targeted reauthentication begun via
// POST .../accounts/{id}/reauth/begin, P2b-PROV-008) but the exchanged
// identity does not resolve to that exact account — either because it
// resolves to no existing account at all, or because it resolves to a
// DIFFERENT existing account. Nothing is staged or swapped; the target
// account and its credentials are left completely untouched. Deliberately
// one sentinel for both sub-cases, matching this package's other
// canary-safe, non-oracle-shaped rejections.
var ErrOAuthAccountIdentityMismatch = errors.New("application: oauth: reauthentication identity does not match the target account")

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
	// OmitStateFromCallback is true when Adapter implements
	// providers.OmitStateFromCallback and the provider's authorize
	// redirect does NOT echo `state`. Begin then uses a state-less
	// transaction id + callback URL (see Begin), so the callback can
	// still be bound to exactly one transaction.
	OmitStateFromCallback bool
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
	// received. It is hashed immediately and never stored or logged. For
	// providers that omit state (see OmitStateFromCallback), RawState is
	// empty and TransactionID carries the callback's binding instead.
	RawState string
	// TransactionID, when non-empty, is the callback's transaction id for
	// the state-less path (providers.OmitStateFromCallback): the callback
	// URL carried the unguessable transaction id in its path, and
	// Complete consumes the row by id rather than by state hash. It is
	// ignored when RawState is non-empty (the state-hash path wins).
	TransactionID string
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
	// FundingFixed is the catalog's declared fixed classification and
	// FundingLocked its lock flag — consulted only when FundingMode is
	// FundingModeFixed (02 §2: the catalog's fixed value IS the evidence
	// for that mode; stamping anything else would misclassify a
	// paid-locked provider like clinepass as unknown/overridable).
	FundingFixed  domain.Funding
	FundingLocked bool
	// OwnerFunding, when non-nil, overrides the provider's own funding
	// classification, exactly as ConnectAPIKeyAccountParams.OwnerFunding
	// does for the API-key flow.
	OwnerFunding *domain.Funding
	// ReauthAccountID, when non-empty, marks this transaction as a
	// TARGETED reauthentication for that specific account (begun via
	// POST .../accounts/{id}/reauth/begin, P2b-PROV-008) — the exchanged
	// identity MUST resolve to exactly this account, or Complete rejects
	// with ErrOAuthAccountIdentityMismatch and stages/swaps nothing.
	// Empty for a normal (non-targeted) begin/complete flow, where an
	// identity that happens to resolve to an existing account is still
	// reauthenticated (see Complete's doc comment) but no specific target
	// is enforced.
	ReauthAccountID string
	// Validate, when non-nil, is an extra check run against the freshly
	// staged credential before it is swapped in as active (03 §2e) — a
	// lightweight seam for a provider-specific validation probe. A nil
	// Validate treats the adapter's own successful CompleteOAuth exchange
	// as sufficient validation, which is what every production call site
	// (httpapi) supplies today.
	Validate ReauthValidator
}

// ReauthValidator performs a lightweight, provider-specific check that a
// freshly staged reauthentication credential actually works, before it
// is swapped in as active (P2b-PROV-008 §1c). It receives the resolved
// StoredCredentials (never a caller-supplied plaintext) and returns a
// non-nil error to reject the swap — Complete then discards the staged
// row and clears reauth_in_progress, leaving the prior active credential
// untouched.
type ReauthValidator func(ctx context.Context, creds providers.StoredCredentials) error

// OAuthEnrollmentService is the provider-agnostic OAuth enrollment
// framework (P2b-PROV-006): PKCE generation, a replay-safe
// consume-before-exchange transaction lifecycle, and — on a successful
// exchange for a brand-new (provider, external_id) pair — the same
// atomic "account + first credential + first funding evidence" persist
// ConnectService performs for the API-key flow. When the exchanged
// identity instead resolves to an EXISTING account, Complete
// reauthenticates it (P2b-PROV-008, 03 §2e): stage the new credential,
// validate it, then atomically swap it in as active while retiring the
// old one — see reauthenticate's doc comment.
type OAuthEnrollmentService struct {
	tx          OAuthTransactionRepo
	enrollment  EnrollmentPort
	accounts    AccountRepo
	credentials CredentialRepo
	reauth      ReauthRepo
	kr          *secrets.Keyring
	newID       IDGenerator
	now         func() time.Time
}

// NewOAuthEnrollmentService builds the service. now defaults to
// time.Now when nil.
func NewOAuthEnrollmentService(tx OAuthTransactionRepo, enrollment EnrollmentPort, accounts AccountRepo, credentials CredentialRepo, reauth ReauthRepo, kr *secrets.Keyring, newID IDGenerator, now func() time.Time) *OAuthEnrollmentService {
	if now == nil {
		now = time.Now
	}
	return &OAuthEnrollmentService{
		tx: tx, enrollment: enrollment, accounts: accounts, credentials: credentials, reauth: reauth,
		kr: kr, newID: newID, now: now,
	}
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

	stateHash := HashOAuthState(state)
	if err := s.tx.Create(ctx, stateHash, p.ProviderID, transactionID, verifierEnv, now, expiresAt); err != nil {
		return BeginOAuthResult{}, fmt.Errorf("application: oauth: persist transaction: %w", err)
	}

	// A provider that omits `state` from its redirect (clinepass) cannot bind
	// the callback via the state nonce. The dashboard completes via
	// POST /oauth/complete with the transaction id it already holds from
	// Begin (legacy venom-router-legacy pattern: opener knows flow_id, callback
	// only relays the code). The authorize redirect_uri stays the plain
	// `{origin}/callback` — embedding `?state=<txid>` broke token exchange /
	// redirect preservation on the live Cline authorize endpoint.
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

	// State-less providers (providers.OmitStateFromCallback): the callback
	// carries the unguessable transaction id, not a state nonce. Consume
	// by id; the state-hash path is the default and wins whenever a state
	// was actually echoed.
	if p.RawState == "" && p.TransactionID != "" {
		rowProviderID, verifierEnv, ok, err := s.tx.ConsumeByTransactionID(ctx, p.TransactionID, now)
		if err != nil {
			return "", domain.Account{}, fmt.Errorf("application: oauth: consume transaction by id: %w", err)
		}
		if !ok {
			return "", domain.Account{}, ErrOAuthTransactionInvalid
		}
		if subtle.ConstantTimeCompare([]byte(rowProviderID), []byte(p.ProviderID)) != 1 {
			return p.TransactionID, domain.Account{}, ErrOAuthTransactionInvalid
		}

		verifierIdentity := secrets.RecordIdentity{
			Purpose:  oauthVerifierAADPurpose,
			Provider: rowProviderID,
			Account:  "",
			Record:   p.TransactionID,
			Kind:     oauthVerifierAADKind,
		}
		verifier, err := secrets.Decrypt(s.kr, verifierIdentity, verifierEnv)
		if err != nil {
			return p.TransactionID, domain.Account{}, fmt.Errorf("application: oauth: decrypt verifier: %w", err)
		}
		defer zeroBytes(verifier)

		return s.completeAfterConsume(ctx, p, p.TransactionID, rowProviderID, verifier, now)
	}

	stateHash := HashOAuthState(p.RawState)

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

	return s.completeAfterConsume(ctx, p, rowTransactionID, rowProviderID, verifier, now)
}

// completeAfterConsume is the shared tail of Complete for BOTH the
// state-hash path and the state-less (transaction-id) path: the row has
// already been irreversibly consumed, the verifier has already been
// decrypted (and will be zeroed by the caller's deferred zeroBytes),
// and everything left — the adapter exchange, the existing-account /
// reauthentication branch, and the new-account enrollment — is
// identical regardless of how the row was looked up. transactionID is
// the row's transaction id (used for reporting on every failure path).
func (s *OAuthEnrollmentService) completeAfterConsume(ctx context.Context, p CompleteOAuthParams, transactionID, rowProviderID string, verifier []byte, now time.Time) (string, domain.Account, error) {
	identity, storedCreds, err := p.Adapter.CompleteOAuth(ctx, p.Code, string(verifier), p.RedirectURI)
	if err != nil {
		switch {
		case errors.Is(err, providers.ErrInvalidCredential):
			return transactionID, domain.Account{}, providers.ErrInvalidCredential
		case errors.Is(err, providers.ErrProviderUnavailable):
			return transactionID, domain.Account{}, providers.ErrProviderUnavailable
		default:
			return transactionID, domain.Account{}, fmt.Errorf("application: oauth: adapter CompleteOAuth: %w", err)
		}
	}

	existingAccount, foundExisting, err := s.accounts.GetByProviderExternalID(ctx, rowProviderID, identity.ExternalID)
	if err != nil {
		return transactionID, domain.Account{}, fmt.Errorf("application: oauth: check existing account: %w", err)
	}

	// A targeted reauthentication (POST .../accounts/{id}/reauth/begin,
	// P2b-PROV-008) demands the exchanged identity resolve to EXACTLY
	// that account — a different existing account, or no existing
	// account at all, is rejected uniformly with
	// ErrOAuthAccountIdentityMismatch and nothing is staged or swapped.
	if p.ReauthAccountID != "" && (!foundExisting || existingAccount.ID != p.ReauthAccountID) {
		return transactionID, domain.Account{}, ErrOAuthAccountIdentityMismatch
	}

	// An identity that resolves to an existing account — targeted or
	// not — is a reauthentication (P2b-PROV-008), never a second
	// account for the same (provider, external_id): stage the new
	// credential, validate it, then atomically swap it in as active
	// while retiring the old one. See reauthenticate's doc comment.
	if foundExisting {
		updated, err := s.reauthenticate(ctx, existingAccount, rowProviderID, storedCreds, p, now)
		if err != nil {
			return transactionID, domain.Account{}, err
		}
		return transactionID, updated, nil
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
		return transactionID, domain.Account{}, fmt.Errorf("application: oauth: stamp funding: %w", err)
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
		return transactionID, domain.Account{}, fmt.Errorf("application: oauth: encrypt credential: %w", err)
	}
	cred := domain.Credential{ID: credentialID, AccountID: accountID, Kind: domain.CredentialKindOAuth2, State: domain.CredentialActive, Fingerprint: fingerprint}

	if err := s.enrollment.CreateConnectedAccount(ctx, newAccount, rowProviderID, cred, credEnv, funding); err != nil {
		return transactionID, domain.Account{}, fmt.Errorf("application: oauth: enrollment: %w", err)
	}

	return transactionID, newAccount, nil
}

// firstFundingEvidence mirrors ConnectService.firstFundingEvidence for
// the OAuth flow: an owner override always wins; otherwise
// domain.StampFirstEvidence applies p.FundingMode, using identity.Funding
// (the provider's own connect-time classification) as the value for
// FundingModeProviderEvidence — the one mode where the provider's
// response, not a fixed catalog default, is the source of truth.
//
// For FundingModeProviderEvidence specifically, the stamped confidence
// is identity.Confidence — the adapter's OWN confidence in its
// classification (P2b-PROV-007, e.g. antigravity's 0.95 for a
// recognized plan) — never a hard-coded constant: a fixed 1.0 here
// would misrepresent connect-time provider evidence as certain when the
// adapter itself reported otherwise. Every other mode keeps the prior
// fixed 1.0 (an administrative/catalog default, not provider evidence,
// so there is nothing for the adapter to be less than certain about).
func (s *OAuthEnrollmentService) firstFundingEvidence(p CompleteOAuthParams, identity providers.IdentityResult, accountID, fundingID string, now time.Time) (domain.FundingEvidence, error) {
	if p.OwnerFunding != nil {
		return domain.FundingEvidence{
			ID: fundingID, AccountID: accountID, Funding: *p.OwnerFunding,
			Source: domain.FundingSourceOwnerOverride, Confidence: 1.0, ObservedAt: now,
		}, nil
	}

	value := domain.FundingUnknown
	locked := false
	confidence := 1.0
	if p.FundingMode == domain.FundingModeProviderEvidence {
		if identity.Funding == string(domain.FundingFree) {
			value = domain.FundingFree
		} else if identity.Funding == string(domain.FundingPaid) {
			value = domain.FundingPaid
		} else {
			value = domain.FundingUnknown
		}
		confidence = identity.Confidence
	}
	if p.FundingMode == domain.FundingModeFixed {
		// The catalog's fixed classification is the evidence for this mode
		// (02 §2): value and lock come from the catalog verbatim, never a
		// hard-coded unknown. An unset FundingFixed stays unknown honestly.
		if p.FundingFixed != "" {
			value = p.FundingFixed
		}
		locked = p.FundingLocked
	}

	stamped, err := domain.StampFirstEvidence(p.FundingMode, accountID, value, locked, confidence, now)
	if err != nil {
		return domain.FundingEvidence{}, err
	}
	stamped.ID = fundingID
	return stamped, nil
}

// reauthenticate implements P2b-PROV-008's atomic reauthentication-
// staging flow (03 §2e) for an account Complete has already determined
// the exchanged identity resolves to: stage the freshly exchanged
// credential (rejecting with domain.ErrReauthenticationInProgress —
// surfaced by httpapi as reauthentication_in_progress — if one of this
// kind is already staged), optionally validate it via p.Validate, then
// atomically swap it in as active (retiring the old active credential
// of the same kind, if any) via the ONE-transaction ReauthRepo port. A
// validation or swap failure discards the staged row and clears
// reauth_in_progress, leaving the prior active credential completely
// untouched — the caller (Complete) gets the error back either way.
// existing.IdentityEmail/IdentityPlan/ExternalID and every other account
// field besides its credential/health_state/reauth_in_progress are left
// exactly as they were; connection_state stays connected throughout.
func (s *OAuthEnrollmentService) reauthenticate(ctx context.Context, existing domain.Account, providerID string, storedCreds providers.StoredCredentials, p CompleteOAuthParams, now time.Time) (domain.Account, error) {
	const kind = domain.CredentialKindOAuth2

	existingCreds, err := s.credentials.ListForAccount(ctx, existing.ID)
	if err != nil {
		return domain.Account{}, fmt.Errorf("application: reauth: list existing credentials: %w", err)
	}
	if err := domain.CanStageCredential(existingCreds, kind); err != nil {
		return domain.Account{}, err
	}

	stagedID := s.newID()
	fingerprint := fingerprintCredentialKey(storedCreds.Value)
	credIdentity := secrets.RecordIdentity{
		Purpose: credentialAADPurpose, Provider: providerID, Account: existing.ID, Record: stagedID, Kind: string(kind),
	}
	env, err := secrets.Encrypt(s.kr, credIdentity, []byte(storedCreds.Value))
	if err != nil {
		return domain.Account{}, fmt.Errorf("application: reauth: encrypt staged credential: %w", err)
	}
	stagedCred := domain.Credential{ID: stagedID, AccountID: existing.ID, Kind: kind, State: domain.CredentialStaged, Fingerprint: fingerprint}

	if err := s.reauth.StageCredential(ctx, existing.ID, providerID, stagedCred, env, now); err != nil {
		return domain.Account{}, fmt.Errorf("application: reauth: stage credential: %w", err)
	}

	if p.Validate != nil {
		if err := p.Validate(ctx, storedCreds); err != nil {
			if discardErr := s.reauth.DiscardStaged(ctx, existing.ID, stagedID); discardErr != nil {
				return domain.Account{}, fmt.Errorf("application: reauth: validation failed (%v) and rollback failed: %w", err, discardErr)
			}
			return domain.Account{}, fmt.Errorf("application: reauth: staged credential failed validation: %w", err)
		}
	}

	if err := s.reauth.SwapStagedToActive(ctx, existing.ID, providerID, kind, stagedID, now); err != nil {
		if discardErr := s.reauth.DiscardStaged(ctx, existing.ID, stagedID); discardErr != nil {
			return domain.Account{}, fmt.Errorf("application: reauth: swap failed (%v) and rollback failed: %w", err, discardErr)
		}
		return domain.Account{}, fmt.Errorf("application: reauth: swap staged credential: %w", err)
	}

	updated, ok, err := s.accounts.GetByID(ctx, existing.ID)
	if err != nil {
		return domain.Account{}, fmt.Errorf("application: reauth: reload account: %w", err)
	}
	if !ok {
		return domain.Account{}, fmt.Errorf("application: reauth: account %q vanished after a successful swap", existing.ID)
	}
	return updated, nil
}

// SweepStaleStagedCredentials discards every staged credential row older
// than olderThan (P2b-PROV-008 §5, crash recovery): a process crash
// between staging (reauthenticate, above) and its swap/rollback would
// otherwise leave a staged row and reauth_in_progress=1 behind
// indefinitely. The active credential is NEVER touched by this — only
// staged rows past olderThan are discarded via ReauthRepo.DiscardStaged,
// which itself never touches anything but a staged row and the
// reauth_in_progress flag. Wiring this into a boot-time sweep is a
// follow-up (see internal/app.Boot) — this method only proves the
// reclaim logic itself; it returns how many stale rows were discarded.
func (s *OAuthEnrollmentService) SweepStaleStagedCredentials(ctx context.Context, olderThan time.Time) (int, error) {
	stale, err := s.reauth.StaleStagedCredentials(ctx, olderThan)
	if err != nil {
		return 0, fmt.Errorf("application: sweep stale staged credentials: list: %w", err)
	}

	n := 0
	for _, c := range stale {
		if err := s.reauth.DiscardStaged(ctx, c.AccountID, c.ID); err != nil {
			return n, fmt.Errorf("application: sweep stale staged credentials: discard %q: %w", c.ID, err)
		}
		n++
	}
	return n, nil
}

// HashOAuthState returns hex(sha256(state)) — the ONLY form of the
// OAuth `state` value this package ever passes to a storage port. The
// raw state itself is never a Create/ConsumeByStateHash argument.
// Exported so httpapi's callback handler (P2b-PROV-008) can compute the
// identical hash to peek a pending transaction's id (via
// storage.OAuthTransactionRepo.PeekTransactionIDByStateHash) BEFORE
// calling Complete — needed to resolve the reauth-binding cache early
// enough for the account_identity_mismatch guard to apply before
// anything is staged/swapped.
func HashOAuthState(state string) string {
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
