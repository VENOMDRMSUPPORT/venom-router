package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/secrets"
)

// OAuthTransactionRepo persists oauth_transactions rows (migration
// 00003) and implements the replay-safe consume-before-exchange
// invariant P2b-PROV-006 requires. It stores and returns exactly the
// secrets.Envelope it is given for the PKCE verifier — it never
// encrypts, decrypts, or otherwise inspects the verifier plaintext, and
// it never receives or stores the raw OAuth `state` value, only its
// caller-supplied sha256 hash. This type structurally satisfies
// application.OAuthTransactionRepo without either package importing the
// other's package as a whole.
type OAuthTransactionRepo struct {
	db *DB
}

// NewOAuthTransactionRepo builds a repository over db's existing connection.
func NewOAuthTransactionRepo(db *DB) *OAuthTransactionRepo {
	return &OAuthTransactionRepo{db: db}
}

// Create inserts a new pending oauth_transactions row: state_sha256 =
// stateHash (the raw state is never a parameter here), the owning
// providerID, transactionID, verifierEnv's envelope columns verbatim,
// and the row's created_at/expires_at — always with consumed = 0.
func (r *OAuthTransactionRepo) Create(ctx context.Context, stateHash, providerID, transactionID string, verifierEnv secrets.Envelope, createdAt, expiresAt time.Time) error {
	_, err := r.db.Conn().ExecContext(ctx,
		`INSERT INTO oauth_transactions
			(state_sha256, provider_id, transaction_id, key_id, nonce, ciphertext, expires_at, consumed, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?)`,
		stateHash, providerID, transactionID, verifierEnv.KeyID, verifierEnv.Nonce, verifierEnv.Ciphertext,
		expiresAt.Unix(), createdAt.Unix(),
	)
	if err != nil {
		return fmt.Errorf("storage: create oauth_transactions row: %w", err)
	}
	return nil
}

// ConsumeByStateHash is the atomic capture-then-guarded-UPDATE this
// package proves replay-safe (P2b-PROV-006): everything happens inside
// ONE database/sql transaction. It first reads the row named by
// stateHash; if it exists, is not already consumed, and has not expired
// as of now, it issues `UPDATE ... SET consumed = 1, key_id = ”,
// nonce = x”, ciphertext = x” WHERE state_sha256 = ? AND consumed = 0`
// and requires RowsAffected == 1 before committing — the guard clause
// (`AND consumed = 0`) is what makes this safe under a concurrent second
// caller racing the exact same stateHash: at most one of them can ever
// see RowsAffected == 1, and database/sql's own connection-pool
// serialization over this package's single physical SQLite connection
// (storage.Open's SetMaxOpenConns(1)) means the two callers' transactions
// never interleave in the first place. Any failure path — no such row,
// already consumed, expired, or (defensively) RowsAffected != 1 — rolls
// the transaction back via the deferred Rollback and returns ok=false
// with every other value zeroed; the row is left completely unchanged.
func (r *OAuthTransactionRepo) ConsumeByStateHash(ctx context.Context, stateHash string, now time.Time) (providerID, transactionID string, verifierEnv secrets.Envelope, ok bool, err error) {
	tx, err := r.db.Conn().BeginTx(ctx, nil)
	if err != nil {
		return "", "", secrets.Envelope{}, false, fmt.Errorf("storage: begin oauth consume tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op once Commit has succeeded

	var (
		keyID             string
		nonce, ciphertext []byte
		expiresAtEpoch    int64
		consumedFlag      int
	)
	scanErr := tx.QueryRowContext(ctx,
		`SELECT provider_id, transaction_id, key_id, nonce, ciphertext, expires_at, consumed
		 FROM oauth_transactions WHERE state_sha256 = ?`,
		stateHash,
	).Scan(&providerID, &transactionID, &keyID, &nonce, &ciphertext, &expiresAtEpoch, &consumedFlag)
	if errors.Is(scanErr, sql.ErrNoRows) {
		return "", "", secrets.Envelope{}, false, nil
	}
	if scanErr != nil {
		return "", "", secrets.Envelope{}, false, fmt.Errorf("storage: consume oauth transaction: select: %w", scanErr)
	}

	if consumedFlag != 0 || expiresAtEpoch <= now.Unix() {
		return "", "", secrets.Envelope{}, false, nil
	}

	res, execErr := tx.ExecContext(ctx,
		`UPDATE oauth_transactions SET consumed = 1, key_id = '', nonce = x'', ciphertext = x''
		 WHERE state_sha256 = ? AND consumed = 0`,
		stateHash,
	)
	if execErr != nil {
		return "", "", secrets.Envelope{}, false, fmt.Errorf("storage: consume oauth transaction: update: %w", execErr)
	}
	affected, raErr := res.RowsAffected()
	if raErr != nil {
		return "", "", secrets.Envelope{}, false, fmt.Errorf("storage: consume oauth transaction: rows affected: %w", raErr)
	}
	if affected != 1 {
		// Defensive: with the SELECT's consumed=0 check just above and
		// this package's single-connection/single-writer discipline,
		// this should be unreachable — but the guard clause is the
		// actual anti-replay invariant, not the preceding SELECT, so it
		// is still checked and still fails closed if it ever changes.
		return "", "", secrets.Envelope{}, false, nil
	}

	if err := tx.Commit(); err != nil {
		return "", "", secrets.Envelope{}, false, fmt.Errorf("storage: consume oauth transaction: commit: %w", err)
	}

	return providerID, transactionID, secrets.Envelope{KeyID: keyID, Nonce: nonce, Ciphertext: ciphertext}, true, nil
}

// GetStatusByTransactionID reads only consumed/expires_at for the row
// named by transactionID — never the envelope or provider_id, which the
// status endpoint has no need for. ok is false if no such row exists.
func (r *OAuthTransactionRepo) GetStatusByTransactionID(ctx context.Context, transactionID string, now time.Time) (consumed bool, expired bool, ok bool, err error) {
	var (
		consumedFlag   int
		expiresAtEpoch int64
	)
	scanErr := r.db.Conn().QueryRowContext(ctx,
		`SELECT consumed, expires_at FROM oauth_transactions WHERE transaction_id = ?`,
		transactionID,
	).Scan(&consumedFlag, &expiresAtEpoch)
	if errors.Is(scanErr, sql.ErrNoRows) {
		return false, false, false, nil
	}
	if scanErr != nil {
		return false, false, false, fmt.Errorf("storage: get oauth transaction status: %w", scanErr)
	}
	return consumedFlag != 0, expiresAtEpoch <= now.Unix(), true, nil
}
