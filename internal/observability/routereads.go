package observability

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// routereads.go is the READ half of the route-record model whose writer lives
// next door in routerecords.go (P6-CAPI-001, 05 §7 / 09 §3.9). It reads the
// SAME two frozen tables the recorder writes — route_decisions and
// route_attempts — and adds nothing to them: this file introduces no column,
// no table, and no status value.
//
// It repeats the writer's fail-closed posture on the way OUT as well as on
// the way in. normalizeStatus is applied to every status read back, so a
// value that reached the column by some path other than RecordAttempt (a
// hand-written INSERT, a restored backup, a future writer) still surfaces as
// RouteStatusUnknown rather than as free text that could carry raw provider
// error material into a diagnostics payload.
//
// Unknown is never fabricated into a value: a NULL latency stays nil, a NULL
// finished_at stays nil, and a NULL scores object stays a nil map — never 0,
// never a zero time, never an empty-but-present map.

// ErrRouteDecisionNotFound is returned by GetExplanation when no
// route_decisions row carries the requested request id. It is a TYPED error
// precisely so a caller can map it to a 404 — returning a zero-value
// RouteExplanation with a nil error would turn a missing record into an
// empty-but-successful answer.
var ErrRouteDecisionNotFound = errors.New("observability: route decision not found")

// RouteExplanation is one request's full "why this route?" record: the
// decision plus every attempt made under it, ordered by attempt number.
type RouteExplanation struct {
	Decision RouteDecision
	Attempts []RouteAttempt
}

// RouteReader reads route records. It holds the same *sql.DB RouteRecorder
// does, so reader and writer share one connection pool.
type RouteReader struct {
	db *sql.DB
}

// NewRouteReader builds a reader over db.
func NewRouteReader(db *sql.DB) *RouteReader {
	return &RouteReader{db: db}
}

// decisionColumns is the single source of truth for the decision SELECT list,
// shared by ListDecisions and GetExplanation so the two can never scan a
// different column order.
const decisionColumns = `id, request_id, tier, workload_profile_bucket, candidate_summary,
	exclusion_reasons, chosen_provider_id, chosen_provider_model_id, chosen_funding,
	scores, requested_thinking, applied_thinking, tier_clamped, certified_clamped, created_at`

// ListDecisions returns decisions newest first, windowed by limit/offset.
//
// The ORDER BY carries a SECOND key (id) beyond created_at. created_at has
// only one-second resolution, so two decisions recorded in the same second
// tie on the primary key; without the tie-break SQLite may return the tied
// pair in either order between calls, and a client paging through the list
// would silently see one row twice and miss another. A negative offset is
// clamped to 0 and an offset past the end yields an empty slice — paging
// inputs are advisory, exactly as parsePageParams treats them, never an
// error.
func (r *RouteReader) ListDecisions(ctx context.Context, limit, offset int) ([]RouteDecision, error) {
	if limit < 0 {
		limit = 0
	}
	if offset < 0 {
		offset = 0
	}

	rows, err := r.db.QueryContext(ctx,
		`SELECT `+decisionColumns+`
		 FROM route_decisions
		 ORDER BY created_at DESC, id DESC
		 LIMIT ? OFFSET ?`,
		limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("observability: list route decisions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	// Always a non-nil slice so an empty page marshals as [] rather than null.
	out := make([]RouteDecision, 0, limit)
	for rows.Next() {
		d, scanErr := scanDecision(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("observability: list route decisions: %w", err)
	}
	return out, nil
}

// GetExplanation returns the decision recorded for requestID plus its
// attempts, ordered by attempt number (with the row id as a deterministic
// tie-break, mirroring ListDecisions' reasoning). An unknown request id is
// ErrRouteDecisionNotFound.
//
// When one request id has more than one decision row (a retry that
// re-entered routing under the same correlation id), the NEWEST decision is
// the one explained — the same newest-first convention ListDecisions uses.
func (r *RouteReader) GetExplanation(ctx context.Context, requestID string) (RouteExplanation, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+decisionColumns+`
		 FROM route_decisions
		 WHERE request_id = ?
		 ORDER BY created_at DESC, id DESC
		 LIMIT 1`,
		requestID,
	)

	decision, err := scanDecision(row)
	if errors.Is(err, sql.ErrNoRows) {
		return RouteExplanation{}, fmt.Errorf("%w: request_id %q", ErrRouteDecisionNotFound, requestID)
	}
	if err != nil {
		return RouteExplanation{}, err
	}

	attempts, err := r.attemptsFor(ctx, decision.ID)
	if err != nil {
		return RouteExplanation{}, err
	}
	return RouteExplanation{Decision: decision, Attempts: attempts}, nil
}

func (r *RouteReader) attemptsFor(ctx context.Context, decisionID string) ([]RouteAttempt, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, route_decision_id, attempt_number, provider_id, account_id,
			offering_operation_id, latency_ms, status, thinking_clamped,
			reservation_id, started_at, finished_at
		 FROM route_attempts
		 WHERE route_decision_id = ?
		 ORDER BY attempt_number ASC, id ASC`,
		decisionID,
	)
	if err != nil {
		return nil, fmt.Errorf("observability: list route attempts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	// Non-nil so a decision with no attempts marshals as [] rather than null.
	out := make([]RouteAttempt, 0)
	for rows.Next() {
		var (
			a          RouteAttempt
			latency    sql.NullInt64
			status     string
			clamped    int
			reservatn  sql.NullString
			startedAt  int64
			finishedAt sql.NullInt64
		)
		if err := rows.Scan(
			&a.ID, &a.RouteDecisionID, &a.AttemptNumber, &a.ProviderID, &a.AccountID,
			&a.OfferingOperationID, &latency, &status, &clamped,
			&reservatn, &startedAt, &finishedAt,
		); err != nil {
			return nil, fmt.Errorf("observability: scan route attempt: %w", err)
		}

		if latency.Valid {
			v := int(latency.Int64)
			a.LatencyMS = &v
		}
		// Fail closed on the way out too: a status outside the closed
		// vocabulary becomes RouteStatusUnknown rather than being surfaced
		// verbatim, however it reached the column.
		a.Status = normalizeStatus(RouteStatus(status))
		a.ThinkingClamped = clamped != 0
		a.ReservationID = reservatn.String
		a.StartedAt = time.Unix(startedAt, 0)
		if finishedAt.Valid {
			t := time.Unix(finishedAt.Int64, 0)
			a.FinishedAt = &t
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("observability: list route attempts: %w", err)
	}
	return out, nil
}

// rowScanner is the shared shape of *sql.Row and *sql.Rows, so one decision
// scanner serves both the single-row and the list query.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanDecision(s rowScanner) (RouteDecision, error) {
	var (
		d                                          RouteDecision
		summaryJSON, reasonsJSON                   string
		scoresJSON                                 sql.NullString
		chosenProvider, chosenModel, chosenFunding sql.NullString
		requestedThinking, appliedThinking         sql.NullString
		tierClamped, certifiedClamped              int
		createdAt                                  int64
	)
	if err := s.Scan(
		&d.ID, &d.RequestID, &d.Tier, &d.WorkloadProfileBucket, &summaryJSON,
		&reasonsJSON, &chosenProvider, &chosenModel, &chosenFunding,
		&scoresJSON, &requestedThinking, &appliedThinking, &tierClamped, &certifiedClamped, &createdAt,
	); err != nil {
		// sql.ErrNoRows is passed through unwrapped so GetExplanation's
		// errors.Is check stays exact.
		if errors.Is(err, sql.ErrNoRows) {
			return RouteDecision{}, err
		}
		return RouteDecision{}, fmt.Errorf("observability: scan route decision: %w", err)
	}

	if err := json.Unmarshal([]byte(summaryJSON), &d.CandidateSummary); err != nil {
		return RouteDecision{}, fmt.Errorf("observability: decode candidate summary: %w", err)
	}
	if err := json.Unmarshal([]byte(reasonsJSON), &d.ExclusionReasons); err != nil {
		return RouteDecision{}, fmt.Errorf("observability: decode exclusion reasons: %w", err)
	}
	// A NULL scores column stays a nil map — "no scores were recorded" is not
	// the same claim as "scored, with no dimensions".
	if scoresJSON.Valid {
		if err := json.Unmarshal([]byte(scoresJSON.String), &d.Scores); err != nil {
			return RouteDecision{}, fmt.Errorf("observability: decode scores: %w", err)
		}
	}

	d.ChosenProviderID = chosenProvider.String
	d.ChosenProviderModelID = chosenModel.String
	d.ChosenFunding = chosenFunding.String
	d.RequestedThinking = requestedThinking.String
	d.AppliedThinking = appliedThinking.String
	d.TierClamped = tierClamped != 0
	d.CertifiedClamped = certifiedClamped != 0
	d.CreatedAt = time.Unix(createdAt, 0)

	return d, nil
}
