package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/secrets"
)

// ReauthRepo performs the atomic operations P2b-PROV-008's OAuth
// reauthentication-staging flow needs (03 §2e): stage a new credential
// alongside an account's existing active one, atomically swap it in (or
// roll a failed attempt back), and sweep staged rows a crash left
// behind. Like EnrollmentRepo, this is a separate type from
// AccountCredentialRepo/AccountRepo (which are otherwise read-mostly/
// read-only) because every one of these operations spans BOTH the
// account_credentials and accounts tables in one transaction. This type
// structurally satisfies application.ReauthRepo without either package
// importing the other's package as a whole.
type ReauthRepo struct {
	db *DB
}

// NewReauthRepo builds a repository over db's existing connection.
func NewReauthRepo(db *DB) *ReauthRepo {
	return &ReauthRepo{db: db}
}

// StageCredential inserts a new 'staged' account_credentials row for
// accountID and sets accounts.reauth_in_progress = 1, in ONE
// transaction (P2b-PROV-008 §1b). cred.State is written as 'staged'
// unconditionally — the caller's cred.State value is not consulted —
// mirroring EnrollmentRepo.CreateConnectedAccount's own "this method
// only ever writes one specific state" discipline.
func (r *ReauthRepo) StageCredential(ctx context.Context, accountID, providerID string, cred domain.Credential, env secrets.Envelope, now time.Time) error {
	tx, err := r.db.Conn().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("storage: begin stage-credential tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op once Commit has succeeded

	epoch := now.Unix()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO account_credentials
			(id, account_id, provider_id, kind, state, fingerprint_sha256, key_id, nonce, ciphertext, created_at, updated_at)
		 VALUES (?, ?, ?, ?, 'staged', ?, ?, ?, ?, ?, ?)`,
		cred.ID, accountID, providerID, string(cred.Kind), cred.Fingerprint,
		env.KeyID, env.Nonce, env.Ciphertext, epoch, epoch,
	); err != nil {
		return fmt.Errorf("storage: reauth: stage credential: insert: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE accounts SET reauth_in_progress = 1, updated_at = ? WHERE id = ?`,
		epoch, accountID,
	); err != nil {
		return fmt.Errorf("storage: reauth: stage credential: set reauth_in_progress: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("storage: reauth: stage credential: commit: %w", err)
	}
	return nil
}

// DiscardStaged deletes credentialID's row (only if it currently
// belongs to accountID and is still 'staged') and clears
// accounts.reauth_in_progress for accountID, in ONE transaction
// (P2b-PROV-008 §1c/§1f/§5). Used both to roll back a failed
// validation/swap attempt and by the crash-recovery sweep. The
// account's active credential (if any) is never touched — the DELETE's
// `AND state = 'staged'` guard structurally prevents this method from
// ever discarding anything else.
func (r *ReauthRepo) DiscardStaged(ctx context.Context, accountID, credentialID string) error {
	tx, err := r.db.Conn().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("storage: begin discard-staged tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op once Commit has succeeded

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM account_credentials WHERE id = ? AND account_id = ? AND state = 'staged'`,
		credentialID, accountID,
	); err != nil {
		return fmt.Errorf("storage: reauth: discard staged: delete: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE accounts SET reauth_in_progress = 0 WHERE id = ?`,
		accountID,
	); err != nil {
		return fmt.Errorf("storage: reauth: discard staged: clear reauth_in_progress: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("storage: reauth: discard staged: commit: %w", err)
	}
	return nil
}

// ErrNoStagedCredential is returned by SwapStagedToActive when
// stagedCredentialID names no currently-staged row for accountID — a
// defensive, should-be-unreachable guard (the caller only ever calls
// this immediately after successfully staging that exact row).
var ErrNoStagedCredential = errors.New("storage: reauth: no staged credential found to promote")

// SwapStagedToActive performs 03 §2e's atomic reauthentication swap in
// ONE transaction (P2b-PROV-008 §1d): any existing active credential of
// kind for accountID is retired FIRST (state='retired',
// retired_at=now) — before stagedCredentialID is ever promoted — so the
// transaction can never observe two simultaneously-active rows of the
// same kind even mid-flight. The M2 idx_cred_active_per_kind
// partial-unique index is the structural backstop behind this
// ordering: even if the retire-then-promote order were ever violated,
// promoting a second row to 'active' while one is already active for
// (account_id, kind) would violate that index and the whole transaction
// would roll back rather than silently leaving two active rows — both
// layers (the code's ordering AND the index) independently enforce the
// same invariant. On success this also sets accounts.health_state =
// 'healthy' and clears reauth_in_progress; connection_state is
// deliberately untouched (it stays 'connected').
func (r *ReauthRepo) SwapStagedToActive(ctx context.Context, accountID, providerID string, kind domain.CredentialKind, stagedCredentialID string, now time.Time) error {
	tx, err := r.db.Conn().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("storage: begin swap-staged-to-active tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op once Commit has succeeded

	epoch := now.Unix()

	if _, err := tx.ExecContext(ctx,
		`UPDATE account_credentials SET state = 'retired', retired_at = ?, updated_at = ?
		 WHERE account_id = ? AND provider_id = ? AND kind = ? AND state = 'active'`,
		epoch, epoch, accountID, providerID, string(kind),
	); err != nil {
		return fmt.Errorf("storage: reauth: swap: retire prior active: %w", err)
	}

	res, err := tx.ExecContext(ctx,
		`UPDATE account_credentials SET state = 'active', updated_at = ?
		 WHERE id = ? AND account_id = ? AND state = 'staged'`,
		epoch, stagedCredentialID, accountID,
	)
	if err != nil {
		return fmt.Errorf("storage: reauth: swap: promote staged: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("storage: reauth: swap: promote staged: rows affected: %w", err)
	}
	if affected != 1 {
		return fmt.Errorf("%w: %q", ErrNoStagedCredential, stagedCredentialID)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE accounts SET health_state = 'healthy', reauth_in_progress = 0, updated_at = ? WHERE id = ?`,
		epoch, accountID,
	); err != nil {
		return fmt.Errorf("storage: reauth: swap: update account: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("storage: reauth: swap: commit: %w", err)
	}
	return nil
}

// StaleStagedCredentials returns every staged credential row whose
// created_at is strictly older than olderThan — the crash-recovery
// sweep's input (P2b-PROV-008 §5): a process crash between staging and
// the swap/rollback would otherwise leave a staged row and
// reauth_in_progress=1 behind indefinitely. domain.Credential.AccountID
// and .ID are the two fields the sweep needs to call DiscardStaged for
// each row; the other fields are populated too for completeness but are
// not required by the sweep.
func (r *ReauthRepo) StaleStagedCredentials(ctx context.Context, olderThan time.Time) ([]domain.Credential, error) {
	rows, err := r.db.Conn().QueryContext(ctx,
		`SELECT id, account_id, kind, fingerprint_sha256 FROM account_credentials WHERE state = 'staged' AND created_at < ?`,
		olderThan.Unix(),
	)
	if err != nil {
		return nil, fmt.Errorf("storage: reauth: list stale staged credentials: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []domain.Credential
	for rows.Next() {
		var id, accountID, kind, fingerprint string
		if err := rows.Scan(&id, &accountID, &kind, &fingerprint); err != nil {
			return nil, fmt.Errorf("storage: reauth: scan stale staged credential row: %w", err)
		}
		out = append(out, domain.Credential{
			ID: id, AccountID: accountID, Kind: domain.CredentialKind(kind),
			State: domain.CredentialStaged, Fingerprint: fingerprint,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: reauth: list stale staged credentials: %w", err)
	}
	return out, nil
}
