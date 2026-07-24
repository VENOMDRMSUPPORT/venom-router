package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/secrets"
)

// AccountCredentialRepo persists account_credentials rows (M2). It
// stores and returns exactly the secrets.Envelope it is given — it
// never encrypts, decrypts, or otherwise inspects the plaintext a
// credential protects; that is internal/accounts/application's job
// (P2b-PROV-003). This type structurally satisfies
// application.CredentialRepo without either package importing the
// other's package as a whole — only accounts/domain and secrets, both
// of which storage already freely imports.
type AccountCredentialRepo struct {
	db *DB
}

// NewAccountCredentialRepo builds a repository over db's existing connection.
func NewAccountCredentialRepo(db *DB) *AccountCredentialRepo {
	return &AccountCredentialRepo{db: db}
}

// ListForAccount returns every credential row (active, staged, and
// retired) for accountID — the full set DOM-003's cardinality rules
// (CanAddActiveCredential/CanStageCredential) need to see.
func (r *AccountCredentialRepo) ListForAccount(ctx context.Context, accountID string) ([]domain.Credential, error) {
	rows, err := r.db.Conn().QueryContext(ctx,
		`SELECT id, kind, state, fingerprint_sha256, expires_at, retired_at FROM account_credentials WHERE account_id = ?`,
		accountID,
	)
	if err != nil {
		return nil, fmt.Errorf("storage: list credentials for account %q: %w", accountID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []domain.Credential
	for rows.Next() {
		var (
			id, kind, state, fingerprint string
			expiresAt, retiredAt         sql.NullInt64
		)
		if err := rows.Scan(&id, &kind, &state, &fingerprint, &expiresAt, &retiredAt); err != nil {
			return nil, fmt.Errorf("storage: scan credential row: %w", err)
		}
		cred := domain.Credential{ID: id, AccountID: accountID, Kind: domain.CredentialKind(kind), State: domain.CredentialState(state), Fingerprint: fingerprint}
		if expiresAt.Valid {
			t := time.Unix(expiresAt.Int64, 0).UTC()
			cred.ExpiresAt = &t
		}
		if retiredAt.Valid {
			t := time.Unix(retiredAt.Int64, 0).UTC()
			cred.RetiredAt = &t
		}
		out = append(out, cred)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: list credentials for account %q: %w", accountID, err)
	}
	return out, nil
}

// FingerprintExists reports whether a non-retired credential with
// fingerprint already exists for providerID (03 §2c's dedup rule). The
// M2 partial unique index (idx_cred_fingerprint) is the structural
// backstop this check exists to give callers a typed error ahead of.
func (r *AccountCredentialRepo) FingerprintExists(ctx context.Context, providerID, fingerprint string) (bool, error) {
	var count int
	err := r.db.Conn().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM account_credentials WHERE provider_id = ? AND fingerprint_sha256 = ? AND state != 'retired'`,
		providerID, fingerprint,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("storage: check fingerprint existence: %w", err)
	}
	return count > 0, nil
}

// Create inserts a new account_credentials row: cred's domain-visible
// fields, the owning providerID, and env's envelope columns verbatim
// (key_id/nonce/ciphertext) — this method never sees plaintext.
func (r *AccountCredentialRepo) Create(ctx context.Context, providerID string, cred domain.Credential, env secrets.Envelope, createdAt time.Time) error {
	epoch := createdAt.Unix()
	_, err := r.db.Conn().ExecContext(ctx,
		`INSERT INTO account_credentials
			(id, account_id, provider_id, kind, state, fingerprint_sha256, key_id, nonce, ciphertext, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		cred.ID, cred.AccountID, providerID, string(cred.Kind), string(cred.State), cred.Fingerprint,
		env.KeyID, env.Nonce, env.Ciphertext, epoch, epoch,
	)
	if err != nil {
		return fmt.Errorf("storage: create account_credentials row %q: %w", cred.ID, err)
	}
	return nil
}

// GetCredential reads a credential back by id, including the providerID
// it belongs to and the envelope needed to decrypt it. ok is false if
// no such row exists.
func (r *AccountCredentialRepo) GetCredential(ctx context.Context, id string) (cred domain.Credential, providerID string, env secrets.Envelope, ok bool, err error) {
	var (
		accountID, kind, state, fingerprint, keyID string
		nonce, ciphertext                          []byte
		expiresAt, retiredAt                       sql.NullInt64
	)
	scanErr := r.db.Conn().QueryRowContext(ctx,
		`SELECT account_id, provider_id, kind, state, fingerprint_sha256, expires_at, retired_at, key_id, nonce, ciphertext
		 FROM account_credentials WHERE id = ?`,
		id,
	).Scan(&accountID, &providerID, &kind, &state, &fingerprint, &expiresAt, &retiredAt, &keyID, &nonce, &ciphertext)
	if errors.Is(scanErr, sql.ErrNoRows) {
		return domain.Credential{}, "", secrets.Envelope{}, false, nil
	}
	if scanErr != nil {
		return domain.Credential{}, "", secrets.Envelope{}, false, fmt.Errorf("storage: get credential %q: %w", id, scanErr)
	}

	cred = domain.Credential{ID: id, AccountID: accountID, Kind: domain.CredentialKind(kind), State: domain.CredentialState(state), Fingerprint: fingerprint}
	if expiresAt.Valid {
		t := time.Unix(expiresAt.Int64, 0).UTC()
		cred.ExpiresAt = &t
	}
	if retiredAt.Valid {
		t := time.Unix(retiredAt.Int64, 0).UTC()
		cred.RetiredAt = &t
	}

	env = secrets.Envelope{KeyID: keyID, Nonce: nonce, Ciphertext: ciphertext}
	return cred, providerID, env, true, nil
}
