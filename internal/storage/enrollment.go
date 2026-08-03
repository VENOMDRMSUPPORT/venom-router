package storage

import (
	"context"
	"fmt"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/secrets"
)

// EnrollmentRepo performs the atomic multi-table enrollment insert 03
// §2b describes: "one transaction inserts account + credential + first
// funding evidence." It is a separate type from AccountRepo/
// AccountCredentialRepo/FundingEvidenceRepo (which are read-mostly)
// because this is the ONE place account rows are ever created this
// phase — every account is born already-connected, atomically, with its
// first credential and funding classification, or not created at all.
type EnrollmentRepo struct {
	db *DB
}

// NewEnrollmentRepo builds a repository over db's existing connection.
func NewEnrollmentRepo(db *DB) *EnrollmentRepo {
	return &EnrollmentRepo{db: db}
}

// CreateConnectedAccount inserts account, credential (with its
// envelope, owned by providerID), and funding — in that order — inside
// ONE database/sql transaction, rolling back all three on any error
// (a FK violation, a CHECK violation, a partial-unique-index conflict).
// A caller never observes a partially-enrolled account: either all
// three rows exist, or none do.
func (r *EnrollmentRepo) CreateConnectedAccount(
	ctx context.Context,
	account domain.Account,
	providerID string,
	cred domain.Credential,
	credEnv secrets.Envelope,
	funding domain.FundingEvidence,
) error {
	tx, err := r.db.Conn().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("storage: begin enrollment tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op once Commit has succeeded

	accountEpoch := account.CreatedAt.Unix()
	// last_health_check_at is stamped when the account is created already
	// health-observed (the API-key connect authenticates the key before
	// enrollment); nil stays NULL ("never checked"), e.g. the OAuth path.
	var lastHealthCheckArg any
	if account.LastHealthCheckAt != nil {
		lastHealthCheckArg = account.LastHealthCheckAt.Unix()
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO accounts
			(id, provider_id, external_id, display_name, auth_type, connection_state, health_state, identity_email, identity_plan, last_health_check_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		account.ID, account.ProviderID, account.ExternalID, account.DisplayName, account.AuthType,
		string(account.ConnectionState), string(account.HealthState),
		account.IdentityEmail, account.IdentityPlan, lastHealthCheckArg, accountEpoch, accountEpoch,
	); err != nil {
		return fmt.Errorf("storage: enrollment: insert account: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO account_credentials
			(id, account_id, provider_id, kind, state, fingerprint_sha256, key_id, nonce, ciphertext, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		cred.ID, cred.AccountID, providerID, string(cred.Kind), string(cred.State), cred.Fingerprint,
		credEnv.KeyID, credEnv.Nonce, credEnv.Ciphertext, accountEpoch, accountEpoch,
	); err != nil {
		return fmt.Errorf("storage: enrollment: insert credential: %w", err)
	}

	var reasonArg any
	if funding.Reason != "" {
		reasonArg = funding.Reason
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO account_funding_evidence (id, account_id, funding, source, locked, confidence, reason, observed_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		funding.ID, funding.AccountID, string(funding.Funding), string(funding.Source),
		boolToInt(funding.Locked), funding.Confidence, reasonArg, funding.ObservedAt.Unix(),
	); err != nil {
		return fmt.Errorf("storage: enrollment: insert funding evidence: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("storage: enrollment: commit: %w", err)
	}
	return nil
}
