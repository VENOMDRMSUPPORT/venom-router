package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/secrets"
)

// credentialAADPurpose is the fixed RecordIdentity.Purpose every
// credential envelope this service handles is sealed/opened under.
const credentialAADPurpose = "credential"

// ErrFingerprintExists is returned by Store when a non-retired
// credential with the same (provider, fingerprint) already exists (03
// §2c's dedup rule) — the M2 partial unique index is the structural
// backstop this typed error gives callers a chance to avoid hitting.
var ErrFingerprintExists = errors.New("application: a credential with this fingerprint already exists for this provider")

// ErrCredentialNotFound is returned by Use when credentialID names no
// stored credential.
var ErrCredentialNotFound = errors.New("application: credential not found")

// CredentialService stores and uses encrypted account credentials
// (P2b-PROV-003, 01 §8 / 02 §3): it is the ONLY place plaintext key
// material is encrypted, decrypted, or briefly held in memory in this
// codebase's application layer. It depends only on the CredentialRepo
// port (never internal/storage) and internal/secrets.
type CredentialService struct {
	repo CredentialRepo
	kr   *secrets.Keyring
	now  func() time.Time
}

// NewCredentialService builds the service over repo (the storage-backed
// port implementation) and kr (the process's active keyring). now
// defaults to time.Now when nil; tests inject a fixed/steppable clock.
func NewCredentialService(repo CredentialRepo, kr *secrets.Keyring, now func() time.Time) *CredentialService {
	if now == nil {
		now = time.Now
	}
	return &CredentialService{repo: repo, kr: kr, now: now}
}

// NormalizeCredentialKey trims surrounding whitespace and collapses
// internal whitespace runs — the same normalization the fingerprint and
// stored plaintext are both derived from, so two keys that differ only
// in incidental whitespace fingerprint identically.
func NormalizeCredentialKey(key string) string {
	return strings.Join(strings.Fields(key), " ")
}

func fingerprintCredentialKey(normalized string) string {
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}

// StoreCredentialParams is Store's input. ID is the caller-supplied
// credential row id (a fresh UUID from the caller — this service does
// not mint ids). Active selects active vs. staged state.
type StoreCredentialParams struct {
	ID           string
	AccountID    string
	ProviderID   string
	Kind         domain.CredentialKind
	Active       bool
	PlaintextKey string
}

// Store validates cardinality (DOM-003), rejects a fingerprint
// duplicate, encrypts the normalized key under a RecordIdentity bound
// to (account, credential, kind), and persists the envelope + metadata
// via the port. The plaintext itself is never persisted or returned —
// only the resulting domain.Credential (never any envelope/key bytes).
func (s *CredentialService) Store(ctx context.Context, p StoreCredentialParams) (domain.Credential, error) {
	existing, err := s.repo.ListForAccount(ctx, p.AccountID)
	if err != nil {
		return domain.Credential{}, fmt.Errorf("application: list existing credentials: %w", err)
	}

	if p.Active {
		if err := domain.CanAddActiveCredential(existing, p.Kind); err != nil {
			return domain.Credential{}, err
		}
	} else {
		if err := domain.CanStageCredential(existing, p.Kind); err != nil {
			return domain.Credential{}, err
		}
	}

	normalized := NormalizeCredentialKey(p.PlaintextKey)
	fingerprint := fingerprintCredentialKey(normalized)

	dup, err := s.repo.FingerprintExists(ctx, p.ProviderID, fingerprint)
	if err != nil {
		return domain.Credential{}, fmt.Errorf("application: check fingerprint dedup: %w", err)
	}
	if dup {
		return domain.Credential{}, ErrFingerprintExists
	}

	identity := secrets.RecordIdentity{
		Purpose:  credentialAADPurpose,
		Provider: p.ProviderID,
		Account:  p.AccountID,
		Record:   p.ID,
		Kind:     string(p.Kind),
	}
	env, err := secrets.Encrypt(s.kr, identity, []byte(normalized))
	if err != nil {
		return domain.Credential{}, fmt.Errorf("application: encrypt credential: %w", err)
	}

	state := domain.CredentialStaged
	if p.Active {
		state = domain.CredentialActive
	}
	cred := domain.Credential{ID: p.ID, AccountID: p.AccountID, Kind: p.Kind, State: state, Fingerprint: fingerprint}

	if err := s.repo.Create(ctx, p.ProviderID, cred, env, s.now()); err != nil {
		return domain.Credential{}, fmt.Errorf("application: persist credential: %w", err)
	}
	return cred, nil
}

// ErrCredentialNotRotatable is returned by Rotate when the credential
// exists but is not in the active state (staged rows belong to an
// in-flight reauth swap; retired rows are history) — rotation applies
// exclusively to the account's live credential.
var ErrCredentialNotRotatable = errors.New("application: only an active credential can be rotated")

// Rotate replaces credentialID's sealed plaintext in place with
// newPlaintext (a refreshed OAuth token envelope), re-encrypting under
// the SAME RecordIdentity the credential was first sealed with — the row
// id, account binding, and kind are unchanged, so the AAD stays valid.
// The plaintext is stored VERBATIM (no whitespace normalization): OAuth
// credential values are adapter-marshaled JSON, and normalizing could
// corrupt string fields — mirroring how the OAuth enrollment path seals
// storedCreds.Value verbatim. expiresAt (nullable) is persisted alongside
// for observability. Never returns or logs the plaintext.
func (s *CredentialService) Rotate(ctx context.Context, credentialID, newPlaintext string, expiresAt *time.Time) error {
	cred, providerID, _, ok, err := s.repo.GetCredential(ctx, credentialID)
	if err != nil {
		return fmt.Errorf("application: get credential for rotate: %w", err)
	}
	if !ok {
		return fmt.Errorf("%w: %q", ErrCredentialNotFound, credentialID)
	}
	if cred.State != domain.CredentialActive {
		return fmt.Errorf("%w: %q is %s", ErrCredentialNotRotatable, credentialID, cred.State)
	}

	identity := secrets.RecordIdentity{
		Purpose:  credentialAADPurpose,
		Provider: providerID,
		Account:  cred.AccountID,
		Record:   cred.ID,
		Kind:     string(cred.Kind),
	}
	env, err := secrets.Encrypt(s.kr, identity, []byte(newPlaintext))
	if err != nil {
		return fmt.Errorf("application: encrypt rotated credential: %w", err)
	}

	rotated, err := s.repo.RotateCiphertext(ctx, credentialID, fingerprintCredentialKey(newPlaintext), env, expiresAt, s.now())
	if err != nil {
		return fmt.Errorf("application: persist rotated credential: %w", err)
	}
	if !rotated {
		// The row changed state between the read and the guarded UPDATE
		// (e.g. a concurrent disconnect retired it) — surface it as the
		// same typed error the pre-check uses.
		return fmt.Errorf("%w: %q", ErrCredentialNotRotatable, credentialID)
	}
	return nil
}

// Use loads credentialID, decrypts it under the same RecordIdentity it
// was sealed with, and hands the plaintext to fn — the ONLY scope the
// plaintext ever exists in memory. The buffer is zeroed immediately
// after fn returns (success or error), and is never assigned to any
// field, variable, or return value that outlives this call. A decrypt
// failure surfaces as secrets.ErrDecrypt/ErrUnknownKeyID — never with
// plaintext, and fn is never invoked in that case.
func (s *CredentialService) Use(ctx context.Context, credentialID string, fn func(plaintext []byte) error) error {
	cred, providerID, env, ok, err := s.repo.GetCredential(ctx, credentialID)
	if err != nil {
		return fmt.Errorf("application: get credential: %w", err)
	}
	if !ok {
		return fmt.Errorf("%w: %q", ErrCredentialNotFound, credentialID)
	}

	identity := secrets.RecordIdentity{
		Purpose:  credentialAADPurpose,
		Provider: providerID,
		Account:  cred.AccountID,
		Record:   cred.ID,
		Kind:     string(cred.Kind),
	}
	plaintext, err := secrets.Decrypt(s.kr, identity, env)
	if err != nil {
		return fmt.Errorf("application: decrypt credential: %w", err)
	}
	defer zeroBytes(plaintext)

	return fn(plaintext)
}

func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
