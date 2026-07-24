package storage

import (
	"context"
	"fmt"
	"time"
)

// AuditEventRow is one audit_events row to append (P2b-OBS-001, migration
// 00004_audit_jobs.sql). Every field is a typed id/code/timestamp —
// action, entity_type, entity_id, result, reason_code — never a secret,
// raw credential, or unsanitized free-form string; the caller
// (internal/httpapi's audit emitter) is responsible for only ever
// populating this with such values.
type AuditEventRow struct {
	Action     string
	EntityType string // "" stored as NULL
	EntityID   string // "" stored as NULL
	Result     string
	ReasonCode string // "" stored as NULL
	At         time.Time
}

// AuditEventRepo appends to the M3 audit_events table — the GENERAL
// audit trail (distinct from M1's auth-only auth_events table; see
// AuthEventRepo). It is append-only via its own BEFORE UPDATE/DELETE
// triggers (00004_audit_jobs.sql) — this package never attempts to
// update or delete a row.
type AuditEventRepo struct {
	db *DB
}

// NewAuditEventRepo builds a repository over db's existing connection.
func NewAuditEventRepo(db *DB) *AuditEventRepo {
	return &AuditEventRepo{db: db}
}

// Append inserts one audit_events row. at is stored as a Unix epoch
// integer, matching the table's `at INTEGER NOT NULL` column (mirroring
// how EnrollmentRepo stamps accounts.created_at/updated_at).
func (r *AuditEventRepo) Append(ctx context.Context, event AuditEventRow) error {
	var entityTypeArg, entityIDArg, reasonCodeArg any
	if event.EntityType != "" {
		entityTypeArg = event.EntityType
	}
	if event.EntityID != "" {
		entityIDArg = event.EntityID
	}
	if event.ReasonCode != "" {
		reasonCodeArg = event.ReasonCode
	}

	_, err := r.db.Conn().ExecContext(ctx,
		`INSERT INTO audit_events (action, entity_type, entity_id, result, reason_code, at) VALUES (?, ?, ?, ?, ?, ?)`,
		event.Action, entityTypeArg, entityIDArg, event.Result, reasonCodeArg, event.At.Unix(),
	)
	if err != nil {
		return fmt.Errorf("storage: append audit_events row: %w", err)
	}
	return nil
}
