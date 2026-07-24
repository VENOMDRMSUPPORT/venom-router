package storage

import (
	"context"
	"fmt"
	"time"
)

// AuthEventRepo appends to the M1 auth_events table (append-only via
// its own BEFORE UPDATE/DELETE triggers — this package never attempts
// to update or delete a row). Values are always typed codes —
// action/result/reason_code — never a password or any other secret;
// P2b-SEC-006's canary proves this holds for the real login/reverify
// call sites.
type AuthEventRepo struct {
	db *DB
}

// NewAuthEventRepo builds a repository over db's existing connection.
func NewAuthEventRepo(db *DB) *AuthEventRepo {
	return &AuthEventRepo{db: db}
}

// Append inserts one auth_events row. reasonCode may be "" (stored as
// NULL) when a result needs no further qualification.
func (r *AuthEventRepo) Append(ctx context.Context, action, result, reasonCode string, at time.Time) error {
	var reasonCodeArg any
	if reasonCode != "" {
		reasonCodeArg = reasonCode
	}
	_, err := r.db.Conn().ExecContext(ctx,
		`INSERT INTO auth_events (action, result, reason_code, at) VALUES (?, ?, ?, ?)`,
		action, result, reasonCodeArg, formatTimestamp(at),
	)
	if err != nil {
		return fmt.Errorf("storage: append auth_events row: %w", err)
	}
	return nil
}

// FailureStreak reports the number of consecutive "failure" rows for
// action, walking backward from the most recent row, that are both (a)
// newer than since and (b) occur after the most recent "success" row
// (a success resets the streak — 09 §5.6). When count > 0, oldestAt is
// the timestamp of the earliest row in that streak — the point a
// caller computing a lockout's retry_after counts down from, since
// further failed attempts DURING a lockout must not push that boundary
// later (it is always the earliest, chronologically first, failure in
// the unbroken streak).
func (r *AuthEventRepo) FailureStreak(ctx context.Context, action string, since time.Time) (count int, oldestAt time.Time, err error) {
	rows, err := r.db.Conn().QueryContext(ctx,
		`SELECT result, at FROM auth_events WHERE action = ? ORDER BY id DESC`,
		action,
	)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("storage: query auth_events for failure streak: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var result, at string
		if err := rows.Scan(&result, &at); err != nil {
			return 0, time.Time{}, fmt.Errorf("storage: scan auth_events row: %w", err)
		}
		ts, parseErr := parseTimestamp(at)
		if parseErr != nil {
			return 0, time.Time{}, parseErr
		}
		if ts.Before(since) {
			break
		}
		if result != "failure" {
			break
		}
		count++
		oldestAt = ts
	}
	if err := rows.Err(); err != nil {
		return 0, time.Time{}, fmt.Errorf("storage: iterate auth_events rows: %w", err)
	}

	return count, oldestAt, nil
}
