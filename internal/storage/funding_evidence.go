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

// AppendSupersession records a supersession the pure domain layer already
// decided (domain.ApplyFundingSupersession), in ONE storage-side
// transaction. When superseded is non-nil, that row's superseded_at is
// stamped to now (it stops being current); newCurrent is then inserted as
// the account's new current row. When superseded is nil (the account had
// no current funding row), only newCurrent is inserted. The M2
// idx_funding_current partial-unique index is the structural backstop
// guaranteeing exactly one current row per account across the stamp +
// insert — a concurrent second supersession for the same account would
// trip it and roll the whole transaction back.
//
// This mirrors EnrollmentRepo's and ReauthRepo's one-tx discipline: the
// stamp and the insert either both land or neither does, so an account
// can never be observed with zero current rows mid-supersession.
func (r *FundingEvidenceRepo) AppendSupersession(ctx context.Context, superseded *domain.FundingEvidence, newCurrent domain.FundingEvidence, now time.Time) error {
	tx, err := r.db.Conn().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("storage: begin funding supersession tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op once Commit has succeeded

	if superseded != nil {
		if _, err := tx.ExecContext(ctx,
			`UPDATE account_funding_evidence SET superseded_at = ? WHERE id = ? AND superseded_at IS NULL`,
			now.Unix(), superseded.ID,
		); err != nil {
			return fmt.Errorf("storage: funding supersession: stamp prior current: %w", err)
		}
	}

	var reasonArg any
	if newCurrent.Reason != "" {
		reasonArg = newCurrent.Reason
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO account_funding_evidence (id, account_id, funding, source, locked, confidence, reason, observed_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		newCurrent.ID, newCurrent.AccountID, string(newCurrent.Funding), string(newCurrent.Source),
		boolToInt(newCurrent.Locked), newCurrent.Confidence, reasonArg, newCurrent.ObservedAt.Unix(),
	); err != nil {
		return fmt.Errorf("storage: funding supersession: insert new current: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("storage: funding supersession: commit: %w", err)
	}
	return nil
}
