package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
)

// FundingEvidenceRepo reads account_funding_evidence rows (M2) into
// domain.FundingEvidence. Row creation happens only through
// EnrollmentRepo.CreateConnectedAccount (the first row) and later
// supersession units (not this one).
type FundingEvidenceRepo struct {
	db *DB
}

// NewFundingEvidenceRepo builds a repository over db's existing connection.
func NewFundingEvidenceRepo(db *DB) *FundingEvidenceRepo {
	return &FundingEvidenceRepo{db: db}
}

// CurrentForAccount reads the one non-superseded row for accountID (the
// M2 partial-unique idx_funding_current's read-side counterpart). ok is
// false if the account has no funding evidence at all.
func (r *FundingEvidenceRepo) CurrentForAccount(ctx context.Context, accountID string) (domain.FundingEvidence, bool, error) {
	row := r.db.Conn().QueryRowContext(ctx,
		`SELECT id, funding, source, locked, confidence, reason, observed_at, superseded_at
		 FROM account_funding_evidence WHERE account_id = ? AND superseded_at IS NULL`,
		accountID,
	)

	var (
		e               domain.FundingEvidence
		funding, source string
		locked          int
		reason          sql.NullString
		observedAt      int64
		supersededAt    sql.NullInt64
	)
	err := row.Scan(&e.ID, &funding, &source, &locked, &e.Confidence, &reason, &observedAt, &supersededAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.FundingEvidence{}, false, nil
	}
	if err != nil {
		return domain.FundingEvidence{}, false, fmt.Errorf("storage: get current funding evidence for account %q: %w", accountID, err)
	}

	e.AccountID = accountID
	e.Funding = domain.Funding(funding)
	e.Source = domain.FundingSource(source)
	e.Locked = locked != 0
	if reason.Valid {
		e.Reason = reason.String
	}
	e.ObservedAt = time.Unix(observedAt, 0).UTC()
	if supersededAt.Valid {
		t := time.Unix(supersededAt.Int64, 0).UTC()
		e.SupersededAt = &t
	}
	return e, true, nil
}
