package storage

import (
	"context"
	"fmt"
	"time"
)

// UsageRecordRepo writes usage_records rows over M7 (00012_api_keys_usage.sql).
//
// Unlike observability.RouteRecorder — which logs-and-swallows a write error by
// design for route records — a usage write is NEVER swallowed: usage is billing
// truth recorded on every terminal path, and the old build's bug was dropping
// it silently (05 §7, card P5-PAPI-002). Insert returns its error so the caller
// surfaces it.
//
// Every column is a correlation id, tier, typed status, or nullable numeric
// metric — there is NO column for a prompt, response, token content, raw
// provider error, or Authorization header (the M7 table has none by design).
type UsageRecordRepo struct {
	db *DB
}

// NewUsageRecordRepo builds the repository over db's existing connection.
func NewUsageRecordRepo(db *DB) *UsageRecordRepo { return &UsageRecordRepo{db: db} }

// UsageRecord is one usage_records row. Pointer fields are nil ⇒ NULL (unknown),
// never a sentinel zero.
type UsageRecord struct {
	ID               string
	RequestID        string
	APIKeyID         *string
	Tier             string
	ProviderID       *string
	AccountID        *string
	ProviderModelID  *string
	Funding          *string
	Status           string
	LatencyMS        *int
	TokensIn         *int
	TokensOut        *int
	FallbackAttempts *int
	CreatedAt        time.Time
}

// Insert appends one usage_records row. The error is returned (never
// swallowed) so the caller can surface a persistence failure.
func (r *UsageRecordRepo) Insert(ctx context.Context, rec UsageRecord) error {
	_, err := r.db.Conn().ExecContext(ctx,
		`INSERT INTO usage_records
		    (id, request_id, api_key_id, tier, provider_id, account_id, provider_model_id, funding, status, latency_ms, tokens_in, tokens_out, fallback_attempts, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.ID, rec.RequestID, nullString(rec.APIKeyID), rec.Tier,
		nullString(rec.ProviderID), nullString(rec.AccountID), nullString(rec.ProviderModelID), nullString(rec.Funding),
		rec.Status, nullInt(rec.LatencyMS), nullInt(rec.TokensIn), nullInt(rec.TokensOut), nullInt(rec.FallbackAttempts),
		rec.CreatedAt.Unix(),
	)
	if err != nil {
		return fmt.Errorf("storage: insert usage record %q: %w", rec.ID, err)
	}
	return nil
}

func nullString(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func nullInt(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}
