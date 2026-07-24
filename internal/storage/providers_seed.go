package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
)

// SeedProviders idempotently upserts one M2 providers row per entry:
// running it twice (e.g. on every boot) leaves exactly one row per ID
// with the entry's current values, never a duplicate. created_at is
// only ever set on first insert; updated_at is refreshed on every call.
// Only storage may import providers — providers itself stays pure
// (01 §3 layering).
func SeedProviders(ctx context.Context, db *DB, entries []providers.CatalogEntry, now time.Time) error {
	epoch := now.Unix()

	for _, e := range entries {
		fundingFixed := sql.NullString{}
		if e.Funding.Fixed != "" {
			fundingFixed = sql.NullString{String: string(e.Funding.Fixed), Valid: true}
		}

		var existingID string
		err := db.Conn().QueryRowContext(ctx, `SELECT id FROM providers WHERE id = ?`, string(e.ID)).Scan(&existingID)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			_, insertErr := db.Conn().ExecContext(ctx,
				`INSERT INTO providers (
					id, display_name, description, auth_mode, base_url, settings_json,
					funding_mode, funding_fixed, funding_locked, funding_non_expiring,
					created_at, updated_at
				) VALUES (?, ?, ?, ?, ?, NULL, ?, ?, ?, ?, ?, ?)`,
				string(e.ID), e.DisplayName, e.Description, string(e.AuthMode), e.BaseURL,
				string(e.Funding.Mode), fundingFixed, boolToInt(e.Funding.Locked), boolToInt(e.Funding.NonExpiring),
				epoch, epoch,
			)
			if insertErr != nil {
				return fmt.Errorf("storage: seed provider %q: insert: %w", e.ID, insertErr)
			}
		case err != nil:
			return fmt.Errorf("storage: seed provider %q: check existing: %w", e.ID, err)
		default:
			_, updateErr := db.Conn().ExecContext(ctx,
				`UPDATE providers SET
					display_name = ?, description = ?, auth_mode = ?, base_url = ?,
					funding_mode = ?, funding_fixed = ?, funding_locked = ?, funding_non_expiring = ?,
					updated_at = ?
				 WHERE id = ?`,
				e.DisplayName, e.Description, string(e.AuthMode), e.BaseURL,
				string(e.Funding.Mode), fundingFixed, boolToInt(e.Funding.Locked), boolToInt(e.Funding.NonExpiring),
				epoch, string(e.ID),
			)
			if updateErr != nil {
				return fmt.Errorf("storage: seed provider %q: update: %w", e.ID, updateErr)
			}
		}
	}

	return nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ProviderRow is one seeded M2 providers row, as read back by ListProviders/GetProvider.
type ProviderRow struct {
	ID                 string
	DisplayName        string
	Description        string
	AuthMode           string
	BaseURL            string
	FundingMode        string
	FundingFixed       string // "" when NULL
	FundingLocked      bool
	FundingNonExpiring bool
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// ListProviders reads back every seeded providers row, ordered by id
// for a stable, deterministic listing.
func ListProviders(ctx context.Context, db *DB) ([]ProviderRow, error) {
	rows, err := db.Conn().QueryContext(ctx,
		`SELECT id, display_name, description, auth_mode, base_url,
			funding_mode, funding_fixed, funding_locked, funding_non_expiring,
			created_at, updated_at
		 FROM providers ORDER BY id`,
	)
	if err != nil {
		return nil, fmt.Errorf("storage: list providers: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []ProviderRow
	for rows.Next() {
		row, err := scanProviderRow(rows)
		if err != nil {
			return nil, fmt.Errorf("storage: list providers: scan: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: list providers: %w", err)
	}
	return out, nil
}

// GetProvider reads back a single providers row by id. ok is false if
// no such row exists.
func GetProvider(ctx context.Context, db *DB, id string) (row ProviderRow, ok bool, err error) {
	r := db.Conn().QueryRowContext(ctx,
		`SELECT id, display_name, description, auth_mode, base_url,
			funding_mode, funding_fixed, funding_locked, funding_non_expiring,
			created_at, updated_at
		 FROM providers WHERE id = ?`,
		id,
	)
	row, scanErr := scanProviderRow(r)
	if errors.Is(scanErr, sql.ErrNoRows) {
		return ProviderRow{}, false, nil
	}
	if scanErr != nil {
		return ProviderRow{}, false, fmt.Errorf("storage: get provider %q: %w", id, scanErr)
	}
	return row, true, nil
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows, letting
// scanProviderRow serve ListProviders and GetProvider identically.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanProviderRow(s rowScanner) (ProviderRow, error) {
	var (
		row                          ProviderRow
		fundingFixed                 sql.NullString
		fundingLocked, fundingNonExp int
		createdAt, updatedAt         int64
	)
	if err := s.Scan(
		&row.ID, &row.DisplayName, &row.Description, &row.AuthMode, &row.BaseURL,
		&row.FundingMode, &fundingFixed, &fundingLocked, &fundingNonExp,
		&createdAt, &updatedAt,
	); err != nil {
		return ProviderRow{}, err
	}
	if fundingFixed.Valid {
		row.FundingFixed = fundingFixed.String
	}
	row.FundingLocked = fundingLocked != 0
	row.FundingNonExpiring = fundingNonExp != 0
	row.CreatedAt = time.Unix(createdAt, 0).UTC()
	row.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return row, nil
}
