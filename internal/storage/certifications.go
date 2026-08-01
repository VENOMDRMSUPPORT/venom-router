package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/intelligence"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
)

// CertificationRepo implements intelligence.CertificationStore and
// intelligence.ReviewQueue over the frozen M4 certifications table
// (00006_catalog_discovery.sql). storage importing intelligence types is
// the permitted direction (internal/staticgate enforces the reverse
// never holds) — mirrors DiscoveryRepo's own doc comment. This repo
// never makes a certification-lifecycle DECISION itself (which
// transition is legal, what a probe outcome means) — that is
// intelligence.CertificationDriver's job; this repo only durably
// records the Load/CompareAndSwap/ListForReview operations the driver
// and the review drainer ask of it.
type CertificationRepo struct {
	db  *DB
	now func() time.Time
}

// NewCertificationRepo builds a repository over db's existing
// connection. now defaults to time.Now when nil.
func NewCertificationRepo(db *DB, now func() time.Time) *CertificationRepo {
	if now == nil {
		now = time.Now
	}
	return &CertificationRepo{db: db, now: now}
}

// ErrCertificationNotFound is returned by Load when no certifications
// row exists for the given offering_operation_id.
var ErrCertificationNotFound = errors.New("storage: certification not found")

// Load implements intelligence.CertificationStore.Load.
func (r *CertificationRepo) Load(ctx context.Context, offeringOperationID string) (models.Certification, error) {
	row := r.db.Conn().QueryRowContext(ctx,
		`SELECT offering_operation_id, status, capability_truth, version, certified_at, evidence_ref, created_at, updated_at
		 FROM certifications WHERE offering_operation_id = ?`,
		offeringOperationID,
	)
	cert, err := scanCertificationRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return models.Certification{}, fmt.Errorf("%w: %q", ErrCertificationNotFound, offeringOperationID)
	}
	if err != nil {
		return models.Certification{}, fmt.Errorf("storage: load certification %q: %w", offeringOperationID, err)
	}
	return cert, nil
}

// CompareAndSwap implements intelligence.CertificationStore.CompareAndSwap:
// ONE conditional UPDATE guarded on (offering_operation_id, status,
// version) TOGETHER — the port's documented contract, since Transition
// only bumps Version on probing -> certified, so version alone cannot
// separate consecutive edges (e.g. two different probing -> suspended
// calls never bump Version, so a version-only guard would let a second,
// stale caller's write through). Zero rows affected returns
// intelligence.ErrCertificationConflict, unchanged.
func (r *CertificationRepo) CompareAndSwap(ctx context.Context, previous, next models.Certification) error {
	var certifiedAtArg sql.NullInt64
	if next.CertifiedAt != nil {
		certifiedAtArg = sql.NullInt64{Int64: next.CertifiedAt.Unix(), Valid: true}
	}
	var evidenceRefArg sql.NullString
	if next.EvidenceRef != "" {
		evidenceRefArg = sql.NullString{String: next.EvidenceRef, Valid: true}
	}

	res, err := r.db.Conn().ExecContext(ctx,
		`UPDATE certifications
		 SET status = ?, capability_truth = ?, version = ?, certified_at = ?, evidence_ref = ?, updated_at = ?
		 WHERE offering_operation_id = ? AND status = ? AND version = ?`,
		string(next.State), string(next.Truth), next.Version, certifiedAtArg, evidenceRefArg, next.UpdatedAt.Unix(),
		previous.OfferingOperationID, string(previous.State), previous.Version,
	)
	if err != nil {
		return fmt.Errorf("storage: compare-and-swap certification %q: %w", previous.OfferingOperationID, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("storage: compare-and-swap certification %q: rows affected: %w", previous.OfferingOperationID, err)
	}
	if affected != 1 {
		return intelligence.ErrCertificationConflict
	}
	return nil
}

// ListForReview implements intelligence.ReviewQueue.ListForReview: rows
// whose status is observed, suspended, or expired — NEVER certified or
// probing — in deterministic (offering_operation_id ASC) order, at most
// limit rows. Attempts is always reported as 0: this repo has no access
// to probe_runs' per-operation attempt count (a different repo, a
// different table), and intelligence.ReviewDrainer.Drain does not
// consult ReviewItem.Attempts today — it is part of the port's shape for
// a future consumer, not a value this repo can honestly fabricate.
func (r *CertificationRepo) ListForReview(ctx context.Context, limit int) ([]intelligence.ReviewItem, error) {
	rows, err := r.db.Conn().QueryContext(ctx,
		`SELECT offering_operation_id, status, capability_truth
		 FROM certifications
		 WHERE status IN ('observed', 'suspended', 'expired')
		 ORDER BY offering_operation_id ASC
		 LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("storage: list certifications for review: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []intelligence.ReviewItem
	for rows.Next() {
		var id, status, truth string
		if err := rows.Scan(&id, &status, &truth); err != nil {
			return nil, fmt.Errorf("storage: list certifications for review: scan: %w", err)
		}
		state, err := models.ParseCertificationState(status)
		if err != nil {
			return nil, fmt.Errorf("storage: list certifications for review: %w", err)
		}
		capTruth, err := models.ParseCapabilityTruth(truth)
		if err != nil {
			return nil, fmt.Errorf("storage: list certifications for review: %w", err)
		}
		out = append(out, intelligence.ReviewItem{OfferingOperationID: id, State: state, Truth: capTruth})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: list certifications for review: %w", err)
	}
	return out, nil
}

// ListForAdmissionCensus returns up to limit certification rows — EVERY row,
// whatever its status — plus whether the scan was cut short by limit. It backs
// the standing routing-admission census (04 §5's "review count grouped by
// reason") that GET /certifications/review reports.
//
// It deliberately does NOT reuse ListForReview's status filter. ListForReview
// surfaces the rows the review DRAINER can advance (observed/suspended/expired);
// the census asks a different question — which offering-operations are not
// routable — and the answer includes a state the drainer can do nothing with:
// `certified` whose capability_truth is `unknown` or `unsupported`. That row is
// not in the drainer's backlog (there is no probe to re-run from `certified`) yet
// models.Routable rejects it, so a census built on the drainer's filter would
// report the single most important conjunction failure in the model as no
// problem at all.
//
// It returns the (state, truth) FACTS and no verdict: deciding routability is
// intelligence.Admit's job, and this repo never re-derives the conjunction.
//
// `truncated` exists because a silently-capped count reads as a complete one.
// The query asks for limit+1 rows to detect the overflow, then returns at most
// limit — so the caller can say "at least N" rather than "N". Order is
// deterministic (offering_operation_id ASC), so a truncated census always
// truncates the same way rather than sampling at random between calls.
func (r *CertificationRepo) ListForAdmissionCensus(ctx context.Context, limit int) ([]intelligence.ReviewItem, bool, error) {
	if limit < 0 {
		limit = 0
	}

	rows, err := r.db.Conn().QueryContext(ctx,
		`SELECT offering_operation_id, status, capability_truth
		 FROM certifications
		 ORDER BY offering_operation_id ASC
		 LIMIT ?`,
		limit+1,
	)
	if err != nil {
		return nil, false, fmt.Errorf("storage: list certifications for admission census: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var (
		out       []intelligence.ReviewItem
		truncated bool
	)
	for rows.Next() {
		var id, status, truth string
		if err := rows.Scan(&id, &status, &truth); err != nil {
			return nil, false, fmt.Errorf("storage: list certifications for admission census: scan: %w", err)
		}
		if len(out) == limit {
			// The (limit+1)-th row: proof of overflow, never returned.
			truncated = true
			break
		}
		state, err := models.ParseCertificationState(status)
		if err != nil {
			return nil, false, fmt.Errorf("storage: list certifications for admission census: %w", err)
		}
		capTruth, err := models.ParseCapabilityTruth(truth)
		if err != nil {
			return nil, false, fmt.Errorf("storage: list certifications for admission census: %w", err)
		}
		out = append(out, intelligence.ReviewItem{OfferingOperationID: id, State: state, Truth: capTruth})
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("storage: list certifications for admission census: %w", err)
	}
	return out, truncated, nil
}

// ListStaleCertified returns up to limit offering_operation_ids whose
// certification is 'certified' and whose updated_at is strictly before
// olderThan (04 §5 edge 9: "evidence staleness (TTL)") — the query
// P3c-JOBS-001's RecertifyTick drives. A row updated EXACTLY at
// olderThan is not stale (the TTL boundary is exclusive on the fresh
// side), in deterministic (offering_operation_id ASC) order.
func (r *CertificationRepo) ListStaleCertified(ctx context.Context, olderThan time.Time, limit int) ([]string, error) {
	rows, err := r.db.Conn().QueryContext(ctx,
		`SELECT offering_operation_id FROM certifications
		 WHERE status = 'certified' AND updated_at < ?
		 ORDER BY offering_operation_id ASC
		 LIMIT ?`,
		olderThan.Unix(), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("storage: list stale certified: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("storage: list stale certified: scan: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: list stale certified: %w", err)
	}
	return out, nil
}

func scanCertificationRow(row *sql.Row) (models.Certification, error) {
	var (
		id, status, truth string
		version           int
		certifiedAt       sql.NullInt64
		evidenceRef       sql.NullString
		createdAt         int64
		updatedAt         int64
	)
	if err := row.Scan(&id, &status, &truth, &version, &certifiedAt, &evidenceRef, &createdAt, &updatedAt); err != nil {
		return models.Certification{}, err
	}

	state, err := models.ParseCertificationState(status)
	if err != nil {
		return models.Certification{}, fmt.Errorf("storage: scan certification %q: %w", id, err)
	}
	capTruth, err := models.ParseCapabilityTruth(truth)
	if err != nil {
		return models.Certification{}, fmt.Errorf("storage: scan certification %q: %w", id, err)
	}

	cert := models.Certification{
		OfferingOperationID: id,
		State:               state,
		Truth:               capTruth,
		Version:             version,
		EvidenceRef:         evidenceRef.String,
		CreatedAt:           time.Unix(createdAt, 0).UTC(),
		UpdatedAt:           time.Unix(updatedAt, 0).UTC(),
	}
	if certifiedAt.Valid {
		t := time.Unix(certifiedAt.Int64, 0).UTC()
		cert.CertifiedAt = &t
	}
	return cert, nil
}

// Compile-time proof CertificationRepo structurally satisfies every
// intelligence port it is meant to adapt.
var (
	_ intelligence.CertificationStore = (*CertificationRepo)(nil)
	_ intelligence.ReviewQueue        = (*CertificationRepo)(nil)
)
