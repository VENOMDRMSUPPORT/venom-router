package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/intelligence"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
)

// ProbeRunRepo implements intelligence.ProbeSpendReader,
// intelligence.ProbeInFlightReader, and intelligence.ProbeCooldownReader
// over the M6-enabling probe_runs / probe_run_costs tables
// (00010_probe_runs.sql). It is the durable record of every probe
// attempt this project ever runs — the safety list in 04 §2 (per-account
// spend, per-provider concurrency, the context-probe cooldown) is
// answerable only because every attempt is recorded here first.
type ProbeRunRepo struct {
	db                   *DB
	now                  func() time.Time
	contextProbeCooldown time.Duration
}

// NewProbeRunRepo builds a repository over db's existing connection.
// contextProbeCooldown is the context-probe cooldown WINDOW duration —
// intentionally a constructor parameter rather than a package constant:
// intelligence.ProbeSafetyPolicy.ContextProbeCooldown is the single owner
// of that duration (04 §2's "7-day cooldown"), and hardcoding
// 7*24*time.Hour a second time here would create a second source of
// truth that could silently drift from the policy's own value. now
// defaults to time.Now when nil.
func NewProbeRunRepo(db *DB, now func() time.Time, contextProbeCooldown time.Duration) *ProbeRunRepo {
	if now == nil {
		now = time.Now
	}
	return &ProbeRunRepo{db: db, now: now, contextProbeCooldown: contextProbeCooldown}
}

// ErrInvalidProbeRunParams is returned by Start for a structurally
// invalid ProbeRunParams. Nothing is written.
var ErrInvalidProbeRunParams = errors.New("storage: invalid probe run params")

// ProbeRunParams is one probe attempt's Start request. ID is minted by
// the caller (mirroring this package's other id-minting conventions —
// e.g. httpapi's newOAuthTransactionID) rather than by this repo, so a
// caller can correlate the run id with its own admission/reservation
// bookkeeping before Start is ever called.
type ProbeRunParams struct {
	ID                  string
	OfferingOperationID string
	AccountID           string
	ProviderID          string
	Operation           string
	Class               intelligence.ProbeClass
	ReservationID       string // "" = none
	Allocations         []quota.Allocation
	StartedAt           time.Time
}

// Start inserts one probe_runs row at execution='running',
// finished_at=NULL, plus one probe_run_costs row per allocation, ALL IN
// ONE transaction: either the run and every one of its cost rows commit
// together, or none of them do. A duplicate Unit within Allocations
// violates probe_run_costs' PRIMARY KEY(probe_run_id, unit) and rolls
// back the whole transaction — including the already-inserted probe_runs
// row — which is what TestProbeRunRepo_StartIsAtomic proves directly
// against the database rather than via a Go-side pre-check.
func (r *ProbeRunRepo) Start(ctx context.Context, p ProbeRunParams) error {
	if p.ID == "" || p.OfferingOperationID == "" || p.AccountID == "" || p.ProviderID == "" || p.Operation == "" {
		return fmt.Errorf("%w: id, offering-operation, account, provider, and operation are all required", ErrInvalidProbeRunParams)
	}
	if _, err := intelligence.ParseProbeClass(string(p.Class)); err != nil {
		return fmt.Errorf("%w: class: %v", ErrInvalidProbeRunParams, err)
	}

	tx, err := r.db.Conn().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("storage: begin probe run start tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op once Commit has succeeded

	var reservationIDArg sql.NullString
	if p.ReservationID != "" {
		reservationIDArg = sql.NullString{String: p.ReservationID, Valid: true}
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO probe_runs (id, offering_operation_id, account_id, provider_id, operation, probe_class, execution, reservation_id, started_at, finished_at)
		 VALUES (?, ?, ?, ?, ?, ?, 'running', ?, ?, NULL)`,
		p.ID, p.OfferingOperationID, p.AccountID, p.ProviderID, p.Operation, string(p.Class), reservationIDArg, p.StartedAt.Unix(),
	); err != nil {
		return fmt.Errorf("storage: insert probe run %q: %w", p.ID, err)
	}

	for _, a := range p.Allocations {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO probe_run_costs (probe_run_id, unit, cost) VALUES (?, ?, ?)`,
			p.ID, string(a.Unit), a.Cost,
		); err != nil {
			return fmt.Errorf("storage: insert probe run cost (%q,%q): %w", p.ID, a.Unit, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("storage: start probe run %q: commit: %w", p.ID, err)
	}
	return nil
}

// Finish sets id's terminal execution and finished_at. It is idempotent:
// the conditional UPDATE only ever matches a row still in ('pending',
// 'running'), so finishing an already-finished run affects zero rows —
// a no-op, never an error, and the first terminal value is never
// overwritten.
func (r *ProbeRunRepo) Finish(ctx context.Context, id string, execution intelligence.ProbeExecution, now time.Time) error {
	if _, err := intelligence.ParseProbeExecution(string(execution)); err != nil {
		return fmt.Errorf("storage: finish probe run %q: %w", id, err)
	}
	if _, err := r.db.Conn().ExecContext(ctx,
		`UPDATE probe_runs SET execution = ?, finished_at = ? WHERE id = ? AND execution IN ('pending', 'running')`,
		string(execution), now.Unix(), id,
	); err != nil {
		return fmt.Errorf("storage: finish probe run %q: %w", id, err)
	}
	return nil
}

// ProbeSpendSince implements intelligence.ProbeSpendReader: the sum of
// accountID's probe_run_costs per unit, over every probe_runs row with
// started_at >= since, in a documented fixed order (SQL-side ORDER BY
// unit ASC — never a Go-side map range). Source is stamped
// EstimateSourceFromRequest on every returned Allocation: probe costs are
// always the original per-attempt estimate ProbeGuard.Admit computed via
// quota.Estimate, and ProbeGuard's own cap check only ever reads Unit and
// Cost, never Source, so no second source-of-truth column is needed on
// probe_run_costs to make this port honest.
func (r *ProbeRunRepo) ProbeSpendSince(ctx context.Context, accountID string, since time.Time) ([]quota.Allocation, error) {
	rows, err := r.db.Conn().QueryContext(ctx,
		`SELECT prc.unit, SUM(prc.cost)
		 FROM probe_run_costs prc
		 JOIN probe_runs pr ON pr.id = prc.probe_run_id
		 WHERE pr.account_id = ? AND pr.started_at >= ?
		 GROUP BY prc.unit
		 ORDER BY prc.unit ASC`,
		accountID, since.Unix(),
	)
	if err != nil {
		return nil, fmt.Errorf("storage: probe spend since for %q: %w", accountID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []quota.Allocation
	for rows.Next() {
		var unit string
		var cost float64
		if err := rows.Scan(&unit, &cost); err != nil {
			return nil, fmt.Errorf("storage: probe spend since for %q: scan: %w", accountID, err)
		}
		out = append(out, quota.Allocation{Unit: quota.Unit(unit), Cost: cost, Source: quota.EstimateSourceFromRequest})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: probe spend since for %q: %w", accountID, err)
	}
	return out, nil
}

// InFlightProbes implements intelligence.ProbeInFlightReader: the count
// of providerID's probe_runs rows still pending/running (04 §2: "max 1
// in-flight probe" per provider). A finished run (finished_at set, or
// execution in a terminal state) never counts, regardless of which one
// finished it first — ReclaimStale below is what prevents a crashed
// process's still-"running" row from holding this slot forever.
func (r *ProbeRunRepo) InFlightProbes(ctx context.Context, providerID string) (int, error) {
	var count int
	if err := r.db.Conn().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM probe_runs WHERE provider_id = ? AND execution IN ('pending', 'running') AND finished_at IS NULL`,
		providerID,
	).Scan(&count); err != nil {
		return 0, fmt.Errorf("storage: in-flight probes for %q: %w", providerID, err)
	}
	return count, nil
}

// ProbeCooldownUntil implements intelligence.ProbeCooldownReader: the
// most recent SUCCEEDED context-window probe run for
// offeringOperationID, plus r.contextProbeCooldown, or nil if none.
//
// Only a succeeded context probe ever sets the cooldown (GOVERNOR
// DECISION, implemented exactly as specified): a retryable/inconclusive/
// terminal run does NOT set it, because an infra failure must remain
// re-attemptable under the probe's own retry budget rather than being
// locked out for a week — 04 §2 attaches the 7-day window to having
// actually read a limit, not merely to having attempted to. A succeeded
// run of any OTHER operation (tools/structured_output/vision) never sets
// this cooldown either — the `operation = 'context_window'` filter below
// is load-bearing, not incidental.
func (r *ProbeRunRepo) ProbeCooldownUntil(ctx context.Context, offeringOperationID string) (*time.Time, error) {
	var startedAt int64
	err := r.db.Conn().QueryRowContext(ctx,
		`SELECT started_at FROM probe_runs
		 WHERE offering_operation_id = ? AND operation = ? AND execution = 'succeeded'
		 ORDER BY started_at DESC LIMIT 1`,
		offeringOperationID, string(intelligenceOperationContextWindow),
	).Scan(&startedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("storage: probe cooldown for %q: %w", offeringOperationID, err)
	}
	until := time.Unix(startedAt, 0).UTC().Add(r.contextProbeCooldown)
	return &until, nil
}

// LatestExecution returns the most recent probe_runs row's execution
// value for offeringOperationID (04 §2's "probe execution" dimension,
// surfaced by P3c-CAPI-001's certification read) — ok is false when no
// probe has ever run for this offering-operation.
func (r *ProbeRunRepo) LatestExecution(ctx context.Context, offeringOperationID string) (intelligence.ProbeExecution, bool, error) {
	var execution string
	err := r.db.Conn().QueryRowContext(ctx,
		`SELECT execution FROM probe_runs WHERE offering_operation_id = ? ORDER BY started_at DESC LIMIT 1`,
		offeringOperationID,
	).Scan(&execution)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("storage: latest probe execution for %q: %w", offeringOperationID, err)
	}
	out, err := intelligence.ParseProbeExecution(execution)
	if err != nil {
		return "", false, fmt.Errorf("storage: latest probe execution for %q: %w", offeringOperationID, err)
	}
	return out, true, nil
}

// CountAttempts returns how many probe_runs rows already exist for
// offeringOperationID — the basis P3c-CAPI-001's handler uses to derive
// intelligence.CertificationDriver.RecordAttempt's `attempts` parameter
// (that attempt's own ordinal is this count plus one, computed by the
// caller BEFORE calling Start for the current attempt).
func (r *ProbeRunRepo) CountAttempts(ctx context.Context, offeringOperationID string) (int, error) {
	var count int
	if err := r.db.Conn().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM probe_runs WHERE offering_operation_id = ?`,
		offeringOperationID,
	).Scan(&count); err != nil {
		return 0, fmt.Errorf("storage: count probe attempts for %q: %w", offeringOperationID, err)
	}
	return count, nil
}

// intelligenceOperationContextWindow mirrors models.OperationContextWindow's
// wire value ("context_window") without importing internal/models into
// this file's query-building path just for one string constant that this
// package's other files already treat as plain TEXT (offering_operations
// / probe_runs' own operation column has no FK to a models type).
const intelligenceOperationContextWindow = "context_window"

// ReclaimStale marks every probe_runs row still pending/running whose
// started_at is older than olderThan as terminal_failure (finished_at =
// olderThan's caller-supplied "now" instant), so a crashed process can
// never hold a per-provider in-flight slot forever. Returns the count of
// rows reclaimed.
func (r *ProbeRunRepo) ReclaimStale(ctx context.Context, olderThan time.Time) (int, error) {
	res, err := r.db.Conn().ExecContext(ctx,
		`UPDATE probe_runs SET execution = 'terminal_failure', finished_at = ?
		 WHERE execution IN ('pending', 'running') AND finished_at IS NULL AND started_at < ?`,
		r.now().Unix(), olderThan.Unix(),
	)
	if err != nil {
		return 0, fmt.Errorf("storage: reclaim stale probe runs: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("storage: reclaim stale probe runs: rows affected: %w", err)
	}
	return int(n), nil
}

// Compile-time proof ProbeRunRepo structurally satisfies every
// intelligence port it is meant to adapt.
var (
	_ intelligence.ProbeSpendReader    = (*ProbeRunRepo)(nil)
	_ intelligence.ProbeInFlightReader = (*ProbeRunRepo)(nil)
	_ intelligence.ProbeCooldownReader = (*ProbeRunRepo)(nil)
)
