package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
)

// accountSelectColumns lists every accounts column AccountRepo reads, in
// scanAccount's exact scan order.
const accountSelectColumns = `id, provider_id, external_id, display_name, auth_type, connection_state, health_state, reauth_in_progress, identity_email, identity_plan, last_health_check_at, last_health_error, created_at, updated_at`

// AccountRepo reads accounts rows (M2) into domain.Account. Row creation
// happens only through EnrollmentRepo.CreateConnectedAccount's atomic
// transaction (P2b-PROV-003) — this repo is read-only by design, since
// every account this phase is born already-connected.
type AccountRepo struct {
	db *DB
}

// NewAccountRepo builds a repository over db's existing connection.
func NewAccountRepo(db *DB) *AccountRepo {
	return &AccountRepo{db: db}
}

// GetByID reads back a single account by id. ok is false if none exists.
func (r *AccountRepo) GetByID(ctx context.Context, id string) (domain.Account, bool, error) {
	row := r.db.Conn().QueryRowContext(ctx, `SELECT `+accountSelectColumns+` FROM accounts WHERE id = ?`, id)
	acc, ok, err := scanAccount(row)
	if err != nil {
		return domain.Account{}, false, fmt.Errorf("storage: get account %q: %w", id, err)
	}
	return acc, ok, nil
}

// GetByProviderExternalID reads back the account uniquely identified by
// (provider_id, external_id) — the M2 UNIQUE(provider_id, external_id)
// constraint's read-side counterpart. ok is false if none exists.
func (r *AccountRepo) GetByProviderExternalID(ctx context.Context, providerID, externalID string) (domain.Account, bool, error) {
	row := r.db.Conn().QueryRowContext(ctx,
		`SELECT `+accountSelectColumns+` FROM accounts WHERE provider_id = ? AND external_id = ?`,
		providerID, externalID,
	)
	acc, ok, err := scanAccount(row)
	if err != nil {
		return domain.Account{}, false, fmt.Errorf("storage: get account by (provider,external_id) (%q,%q): %w", providerID, externalID, err)
	}
	return acc, ok, nil
}

func scanAccount(row *sql.Row) (domain.Account, bool, error) {
	var (
		a                                                         domain.Account
		connectionState, healthState                              string
		reauthInProgress                                          int
		displayName, identityEmail, identityPlan, lastHealthError sql.NullString
		lastHealthCheckAt                                         sql.NullInt64
		createdAt, updatedAt                                      int64
	)
	err := row.Scan(
		&a.ID, &a.ProviderID, &a.ExternalID, &displayName, &a.AuthType,
		&connectionState, &healthState, &reauthInProgress,
		&identityEmail, &identityPlan, &lastHealthCheckAt, &lastHealthError,
		&createdAt, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Account{}, false, nil
	}
	if err != nil {
		return domain.Account{}, false, err
	}

	a.DisplayName = displayName.String
	a.IdentityEmail = identityEmail.String
	a.IdentityPlan = identityPlan.String
	a.LastHealthError = lastHealthError.String
	a.ConnectionState = domain.ConnectionState(connectionState)
	a.HealthState = domain.HealthState(healthState)
	a.ReauthInProgress = reauthInProgress != 0
	if lastHealthCheckAt.Valid {
		t := time.Unix(lastHealthCheckAt.Int64, 0).UTC()
		a.LastHealthCheckAt = &t
	}
	a.CreatedAt = time.Unix(createdAt, 0).UTC()
	a.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return a, true, nil
}
