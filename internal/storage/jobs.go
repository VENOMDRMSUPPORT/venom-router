package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// JobKind is the typed vocabulary for jobs.kind (09 §3.12): the job type a
// caller passes to Create and a client reads back from GET /jobs/{job_id}.
// This package's Create still takes a plain kind string (existing callers
// depend on that signature) — JobKind is what a NEW caller passes, and
// ParseJobKind is how a reader validates one it read back.
type JobKind string

// JobKindDiscovery is the P3a-JOBS-001 discovery job kind (09 §3.12's
// documented kind vocabulary: discovery | probe | benchmark | backup |
// restore — only discovery is wired this phase).
const JobKindDiscovery JobKind = "discovery"

// JobKindReconciliation is the P3b-QUOTA-007 background job kind: the
// worker sweep that resolves reconciliation_pending reservations.
const JobKindReconciliation JobKind = "reconciliation"

// JobKindQuotaSync is the P3b-QUOTA-008 background job kind: the worker
// sweep that ingests provider-evidence quota windows.
const JobKindQuotaSync JobKind = "quota_sync"

// JobKindProbe is the P3c-CAPI-001/JOBS-001 background job kind: a
// capability/context probe attempt (09 §3.8/§3.12's documented kind
// vocabulary: discovery | probe | benchmark | backup | restore).
const JobKindProbe JobKind = "probe"

// JobKindBenchmark is the P6-CAPI-001 canonical-quality job kind: one
// analysis-leaderboard read resolved through the precedence engine for a
// single canonical model (04 §3/§5). It runs NO inference — it is the third
// registered member of 09 §3.12's documented kind vocabulary.
const JobKindBenchmark JobKind = "benchmark"

// ErrUnknownJobKind is returned by ParseJobKind for any value outside the
// registered vocabulary — fail closed, never silently accept an
// unrecognized kind.
var ErrUnknownJobKind = errors.New("storage: unrecognized job kind")

// ParseJobKind fails closed on any value outside the exact registered
// vocabulary — no case folding, no trimming. "discovery",
// "reconciliation", "quota_sync", "probe", and "benchmark" are registered;
// backup/restore are later units' concern (P8).
func ParseJobKind(s string) (JobKind, error) {
	switch JobKind(s) {
	case JobKindDiscovery, JobKindReconciliation, JobKindQuotaSync, JobKindProbe, JobKindBenchmark:
		return JobKind(s), nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnknownJobKind, s)
	}
}

// JobStatus is one M4 jobs.status value (09 §3.12).
type JobStatus string

const (
	JobPending   JobStatus = "pending"
	JobRunning   JobStatus = "running"
	JobCompleted JobStatus = "completed"
	JobFailed    JobStatus = "failed"
	JobExpired   JobStatus = "expired"
)

// DefaultJobRetention is 09 §3.12's documented default: a terminal job
// (completed/failed/expired) is retained for 24 hours before ReapExpired
// removes it.
const DefaultJobRetention = 24 * time.Hour

// JobError is a job's terminal failure, serialized into the jobs.error
// TEXT column as JSON. It is a user-safe {code, message} pair, never a
// raw provider error or any secret — enforcing that at the call site is
// each future job-producing unit's responsibility; this package only
// stores and returns whatever JobError it is given, unmodified.
type JobError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// JobRow is one jobs row (M4, 00004_audit_jobs.sql).
type JobRow struct {
	ID             string
	Kind           string
	Status         JobStatus
	StartedAt      *time.Time
	FinishedAt     *time.Time
	ResultRef      string // "" = none — a reference (e.g. an account_id), never a secret value
	Error          *JobError
	CreatedAt      time.Time
	RetentionUntil *time.Time
}

// JobRepo persists jobs rows over the M4 schema.
type JobRepo struct {
	db *DB
}

// NewJobRepo builds a repository over db's existing connection.
func NewJobRepo(db *DB) *JobRepo {
	return &JobRepo{db: db}
}

// Create inserts a new job row in the "pending" status.
func (r *JobRepo) Create(ctx context.Context, id, kind string, now time.Time) error {
	_, err := r.db.Conn().ExecContext(ctx,
		`INSERT INTO jobs (id, kind, status, created_at) VALUES (?, ?, ?, ?)`,
		id, kind, string(JobPending), now.Unix(),
	)
	if err != nil {
		return fmt.Errorf("storage: create job %q: %w", id, err)
	}
	return nil
}

// GetByID reads back a single job by id. ok is false if no such job exists.
func (r *JobRepo) GetByID(ctx context.Context, id string) (JobRow, bool, error) {
	row, err := scanJobRow(r.db.Conn().QueryRowContext(ctx,
		`SELECT id, kind, status, started_at, finished_at, result_ref, error, created_at, retention_until
		 FROM jobs WHERE id = ?`,
		id,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return JobRow{}, false, nil
	}
	if err != nil {
		return JobRow{}, false, fmt.Errorf("storage: get job %q: %w", id, err)
	}
	return row, true, nil
}

// MarkRunning transitions a still-pending job to "running", stamping
// started_at. Marking an already-running or terminal job is a no-op
// (the WHERE clause only ever matches a pending row) rather than an
// error, matching this codebase's other state-transition repos.
func (r *JobRepo) MarkRunning(ctx context.Context, id string, startedAt time.Time) error {
	_, err := r.db.Conn().ExecContext(ctx,
		`UPDATE jobs SET status = ?, started_at = ? WHERE id = ? AND status = ?`,
		string(JobRunning), startedAt.Unix(), id, string(JobPending),
	)
	if err != nil {
		return fmt.Errorf("storage: mark job %q running: %w", id, err)
	}
	return nil
}

// MarkTerminal transitions a job to a terminal status (completed,
// failed, or expired — any other value is rejected), stamping
// finished_at and retention_until = finishedAt + retentionTTL.
// resultRef ("" = none) and jobErr (nil = none) are stored verbatim;
// this package does not inspect or redact their content — see JobError's
// doc comment.
func (r *JobRepo) MarkTerminal(ctx context.Context, id string, status JobStatus, finishedAt time.Time, resultRef string, jobErr *JobError, retentionTTL time.Duration) error {
	switch status {
	case JobCompleted, JobFailed, JobExpired:
	default:
		return fmt.Errorf("storage: MarkTerminal: %q is not a terminal status", status)
	}

	var resultRefArg sql.NullString
	if resultRef != "" {
		resultRefArg = sql.NullString{String: resultRef, Valid: true}
	}

	var errArg sql.NullString
	if jobErr != nil {
		b, err := json.Marshal(jobErr)
		if err != nil {
			return fmt.Errorf("storage: marshal job error for %q: %w", id, err)
		}
		errArg = sql.NullString{String: string(b), Valid: true}
	}

	retentionUntil := finishedAt.Add(retentionTTL).Unix()

	_, err := r.db.Conn().ExecContext(ctx,
		`UPDATE jobs SET status = ?, finished_at = ?, result_ref = ?, error = ?, retention_until = ? WHERE id = ?`,
		string(status), finishedAt.Unix(), resultRefArg, errArg, retentionUntil, id,
	)
	if err != nil {
		return fmt.Errorf("storage: mark job %q terminal: %w", id, err)
	}
	return nil
}

// ReapExpired deletes every job whose retention_until has passed as of
// now, returning the number of rows removed.
func (r *JobRepo) ReapExpired(ctx context.Context, now time.Time) (int64, error) {
	res, err := r.db.Conn().ExecContext(ctx,
		`DELETE FROM jobs WHERE retention_until IS NOT NULL AND retention_until < ?`,
		now.Unix(),
	)
	if err != nil {
		return 0, fmt.Errorf("storage: reap expired jobs: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("storage: reap expired jobs: rows affected: %w", err)
	}
	return n, nil
}

func scanJobRow(row *sql.Row) (JobRow, error) {
	var (
		out                                   JobRow
		status                                string
		startedAt, finishedAt, retentionUntil sql.NullInt64
		resultRef, errText                    sql.NullString
		createdAt                             int64
	)
	if err := row.Scan(&out.ID, &out.Kind, &status, &startedAt, &finishedAt, &resultRef, &errText, &createdAt, &retentionUntil); err != nil {
		return JobRow{}, err
	}

	out.Status = JobStatus(status)
	out.CreatedAt = time.Unix(createdAt, 0).UTC()
	if startedAt.Valid {
		t := time.Unix(startedAt.Int64, 0).UTC()
		out.StartedAt = &t
	}
	if finishedAt.Valid {
		t := time.Unix(finishedAt.Int64, 0).UTC()
		out.FinishedAt = &t
	}
	if retentionUntil.Valid {
		t := time.Unix(retentionUntil.Int64, 0).UTC()
		out.RetentionUntil = &t
	}
	if resultRef.Valid {
		out.ResultRef = resultRef.String
	}
	if errText.Valid {
		var jobErr JobError
		if err := json.Unmarshal([]byte(errText.String), &jobErr); err != nil {
			return JobRow{}, fmt.Errorf("storage: unmarshal job error: %w", err)
		}
		out.Error = &jobErr
	}

	return out, nil
}
