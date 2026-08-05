package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// BenchmarkRunRepo persists benchmark_runs rows (00017_benchmark_runs.sql)
// — the durable record of one local benchmark attempt against a model, plus
// the read path (LatestForModel) that surfaces the most recent attempt's
// rating and timing to a consumer.
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

	var (
		run        BenchmarkRun
		ttft       sql.NullInt64
		tps        sql.NullFloat64
		rating     sql.NullFloat64
		startedAt  int64
		finishedAt int64
	)
	err := row.Scan(
		&run.ID, &run.ModelID, &run.AccountID, &run.ProviderID, &run.ProviderModelID,
		&run.Requests, &run.Successes, &ttft, &tps, &rating,
		&startedAt, &finishedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return BenchmarkRun{}, false, nil
	}
	if err != nil {
		return BenchmarkRun{}, false, fmt.Errorf("storage: latest benchmark run for model %q: %w", modelID, err)
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

	return run, true, nil
}
