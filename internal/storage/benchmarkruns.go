package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// BenchmarkRunRepo persists benchmark_runs rows (00017_benchmark_runs.sql)
// — the durable record of one local benchmark attempt against a model, plus
// the read paths (LatestForModel for one model, LatestForModels for a whole
// page of them) that surface the most recent attempt's rating and timing to a
// consumer.
type BenchmarkRunRepo struct {
	db  *DB
	now func() time.Time
}

// NewBenchmarkRunRepo builds a repository over db's existing connection.
// now defaults to time.Now when nil, mirroring this package's other
// repositories (e.g. NewCertificationRepo, NewProbeRunRepo).
func NewBenchmarkRunRepo(db *DB, now func() time.Time) *BenchmarkRunRepo {
	if now == nil {
		now = time.Now
	}
	return &BenchmarkRunRepo{db: db, now: now}
}

// BenchmarkRun is one benchmark_runs row. TTFTMillis, TokensPerSec, and
// Rating are nullable pointers: nil means "no successful request to
// measure" / "the success gate failed" (see the migration's column
// comments), never a zero value standing in for absent.
type BenchmarkRun struct {
	ID              string
	ModelID         string
	AccountID       string
	ProviderID      string
	ProviderModelID string
	Requests        int
	Successes       int
	TTFTMillis      *int64
	TokensPerSec    *float64
	Rating          *float64
	StartedAt       time.Time
	FinishedAt      time.Time
}

// Insert writes one benchmark_runs row. created_at is stamped from the
// repo's own now func, mirroring this package's other repos — it is not
// part of the caller-supplied BenchmarkRun because it records when this
// repo wrote the row, not when the benchmark ran (that is StartedAt/
// FinishedAt).
func (r *BenchmarkRunRepo) Insert(ctx context.Context, run BenchmarkRun) error {
	var ttftArg sql.NullInt64
	if run.TTFTMillis != nil {
		ttftArg = sql.NullInt64{Int64: *run.TTFTMillis, Valid: true}
	}
	var tpsArg sql.NullFloat64
	if run.TokensPerSec != nil {
		tpsArg = sql.NullFloat64{Float64: *run.TokensPerSec, Valid: true}
	}
	var ratingArg sql.NullFloat64
	if run.Rating != nil {
		ratingArg = sql.NullFloat64{Float64: *run.Rating, Valid: true}
	}

	if _, err := r.db.Conn().ExecContext(ctx,
		`INSERT INTO benchmark_runs
		    (id, model_id, account_id, provider_id, provider_model_id, requests, successes, ttft_ms, tokens_per_sec, rating, started_at, finished_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ID, run.ModelID, run.AccountID, run.ProviderID, run.ProviderModelID,
		run.Requests, run.Successes, ttftArg, tpsArg, ratingArg,
		run.StartedAt.Unix(), run.FinishedAt.Unix(), r.now().Unix(),
	); err != nil {
		return fmt.Errorf("storage: insert benchmark run %q: %w", run.ID, err)
	}
	return nil
}

// LatestForModels is LatestForModel's BATCHED sibling: for each id in
// modelIDs it returns THAT model's most recent benchmark_runs row by
// finished_at, in ONE query. A model with no run at all is ABSENT from the
// returned map — never present with a zero value, so a caller can never
// mistake "never benchmarked" for "benchmarked at the epoch".
//
// It exists so a read model rendering a whole page of model groups can
// resolve every group's benchmark provenance without one query per group
// (internal/httpapi's ServeModels, mirroring the batched
// ProbeRunRepo.SucceededOfferingOperationIDs lookup on the same page).
//
// Placeholder-growth note (same discipline as
// SucceededOfferingOperationIDs): the IN(...) list grows with the number of
// distinct canonical models on ONE page of offerings, bounded by httpapi's
// maxPageLimit of 200, far below modernc.org/sqlite's 32766 placeholder
// ceiling. Never remove the caller-side page bound on the assumption this
// query can absorb an unbounded id list.
//
// Ties on finished_at are broken by id DESC so the result is deterministic
// rather than whichever row the scan happened to reach first.
func (r *BenchmarkRunRepo) LatestForModels(ctx context.Context, modelIDs []string) (map[string]BenchmarkRun, error) {
	out := make(map[string]BenchmarkRun, len(modelIDs))
	if len(modelIDs) == 0 {
		return out, nil
	}

	seen := make(map[string]bool, len(modelIDs))
	placeholders := make([]string, 0, len(modelIDs))
	args := make([]any, 0, len(modelIDs))
	for _, id := range modelIDs {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		placeholders = append(placeholders, "?")
		args = append(args, id)
	}
	if len(args) == 0 {
		return out, nil
	}

	query := `SELECT id, model_id, account_id, provider_id, provider_model_id, requests, successes,
	                 ttft_ms, tokens_per_sec, rating, started_at, finished_at
	          FROM (
	              SELECT id, model_id, account_id, provider_id, provider_model_id, requests, successes,
	                     ttft_ms, tokens_per_sec, rating, started_at, finished_at,
	                     ROW_NUMBER() OVER (PARTITION BY model_id ORDER BY finished_at DESC, id DESC) AS rn
	              FROM benchmark_runs
	              WHERE model_id IN (` + strings.Join(placeholders, ",") + `)
	          )
	          WHERE rn = 1`
	rows, err := r.db.Conn().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("storage: latest benchmark runs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		run, err := scanBenchmarkRun(rows)
		if err != nil {
			return nil, fmt.Errorf("storage: latest benchmark runs: %w", err)
		}
		out[run.ModelID] = run
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: latest benchmark runs: %w", err)
	}
	return out, nil
}

// benchmarkRunScanner is what both read paths below scan from: *sql.Row
// (LatestForModel) and *sql.Rows (LatestForModels) share this one method, so
// the column decoding — including the nullable pointers and the Unix-second
// timestamps — lives in exactly one place and cannot drift between them.
type benchmarkRunScanner interface {
	Scan(dest ...any) error
}

// scanBenchmarkRun decodes one benchmark_runs row in the column order both
// read paths select. Nullable columns stay nil pointers when NULL, never zero
// values (see BenchmarkRun's field docs).
func scanBenchmarkRun(src benchmarkRunScanner) (BenchmarkRun, error) {
	var (
		run        BenchmarkRun
		ttft       sql.NullInt64
		tps        sql.NullFloat64
		rating     sql.NullFloat64
		startedAt  int64
		finishedAt int64
	)
	if err := src.Scan(
		&run.ID, &run.ModelID, &run.AccountID, &run.ProviderID, &run.ProviderModelID,
		&run.Requests, &run.Successes, &ttft, &tps, &rating,
		&startedAt, &finishedAt,
	); err != nil {
		return BenchmarkRun{}, err
	}
	if ttft.Valid {
		v := ttft.Int64
		run.TTFTMillis = &v
	}
	if tps.Valid {
		v := tps.Float64
		run.TokensPerSec = &v
	}
	if rating.Valid {
		v := rating.Float64
		run.Rating = &v
	}
	run.StartedAt = time.Unix(startedAt, 0).UTC()
	run.FinishedAt = time.Unix(finishedAt, 0).UTC()
	return run, nil
}

// LatestForModel returns modelID's most recent benchmark_runs row by
// finished_at (idx_benchmark_runs_model backs this query), or
// (BenchmarkRun{}, false, nil) when no row exists for modelID.
func (r *BenchmarkRunRepo) LatestForModel(ctx context.Context, modelID string) (BenchmarkRun, bool, error) {
	row := r.db.Conn().QueryRowContext(ctx,
		`SELECT id, model_id, account_id, provider_id, provider_model_id, requests, successes, ttft_ms, tokens_per_sec, rating, started_at, finished_at
		 FROM benchmark_runs
		 WHERE model_id = ?
		 ORDER BY finished_at DESC
		 LIMIT 1`,
		modelID,
	)

	run, err := scanBenchmarkRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return BenchmarkRun{}, false, nil
	}
	if err != nil {
		return BenchmarkRun{}, false, fmt.Errorf("storage: latest benchmark run for model %q: %w", modelID, err)
	}
	return run, true, nil
}
