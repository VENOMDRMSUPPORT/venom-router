package observability

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// routerecords.go persists the secret-free route-decision and route-attempt
// records (P4-OBS-001, 05 §7 / 01 §6c) into M6's frozen route_decisions and
// route_attempts tables. Privacy is STRUCTURAL: those tables have no column for
// a prompt, response, token content, raw provider error, or Authorization
// material, so storing one is impossible — and this recorder additionally
// normalizes the one free-form field (status) to a closed vocabulary so a raw
// error can never be smuggled through it. A write failure is logged and
// swallowed (never aborts routing), mirroring httpapi's auditEmitter precedent.
//
// This package may import database/sql and be handed a *storage.DB's *sql.DB;
// it is NOT one of the staticgate domain-purity packages.

// RouteStatus is the closed, normalized attempt-status vocabulary. Anything
// outside it — including a raw provider error string — normalizes to
// RouteStatusUnknown, so free provider text can never reach the status column.
type RouteStatus string

const (
	RouteStatusSuccess  RouteStatus = "success"
	RouteStatusFailure  RouteStatus = "failure"
	RouteStatusPartial  RouteStatus = "partial"
	RouteStatusTimeout  RouteStatus = "timeout"
	RouteStatusRejected RouteStatus = "reservation_rejected" // reservation rejected before execution (04 §5: never the bare token "rejected")
	RouteStatusPending  RouteStatus = "reconciliation_pending"
	RouteStatusUnknown  RouteStatus = "unknown"
)

// normalizeStatus fails closed: a value outside the closed set becomes
// RouteStatusUnknown rather than being stored verbatim.
func normalizeStatus(s RouteStatus) RouteStatus {
	switch s {
	case RouteStatusSuccess, RouteStatusFailure, RouteStatusPartial,
		RouteStatusTimeout, RouteStatusRejected, RouteStatusPending, RouteStatusUnknown:
		return s
	default:
		return RouteStatusUnknown
	}
}

// CandidateSummary is the secret-free candidate-set summary: counts and group
// keys (provider+model identities) only — never a provider payload.
type CandidateSummary struct {
	TotalCandidates int      `json:"total_candidates"`
	EligibleGroups  int      `json:"eligible_groups"`
	GroupKeys       []string `json:"group_keys,omitempty"`
}

// RouteDecision is one route_decisions row. Every field is a correlation id,
// typed code, count, score, or clamp flag — never content.
type RouteDecision struct {
	ID                    string
	RequestID             string
	Tier                  string
	WorkloadProfileBucket string
	CandidateSummary      CandidateSummary
	// ExclusionReasons maps a typed exclusion reason code (the routing
	// hardgates constants) to a count. Keys are codes, values are counts.
	ExclusionReasons      map[string]int
	ChosenProviderID      string
	ChosenProviderModelID string
	ChosenFunding         string
	// Scores maps a named score dimension to its numeric value (no content).
	Scores            map[string]float64
	RequestedThinking string
	AppliedThinking   string
	TierClamped       bool
	CertifiedClamped  bool
	CreatedAt         time.Time
}

// RouteAttempt is one route_attempts row.
type RouteAttempt struct {
	ID                  string
	RouteDecisionID     string
	AttemptNumber       int
	ProviderID          string
	AccountID           string
	OfferingOperationID string
	LatencyMS           *int // nil ⇒ NULL
	Status              RouteStatus
	ThinkingClamped     bool
	ReservationID       string // "" ⇒ NULL
	StartedAt           time.Time
	FinishedAt          *time.Time // nil ⇒ NULL
}

// RouteRecorder writes route records through db, logging and swallowing any
// write error so recording can never break routing.
type RouteRecorder struct {
	db  *sql.DB
	log *Logger
}

// NewRouteRecorder builds a recorder over db. log receives write-failure
// records; if nil, Default() is used.
func NewRouteRecorder(db *sql.DB, log *Logger) *RouteRecorder {
	if log == nil {
		log = Default()
	}
	return &RouteRecorder{db: db, log: log}
}

// RecordDecision persists a route_decisions row. On any write error it logs and
// returns nil — recording must never abort routing.
func (r *RouteRecorder) RecordDecision(ctx context.Context, d RouteDecision) error {
	if err := r.insertDecision(ctx, d); err != nil {
		r.log.Error("route decision record failed", Err(err), String("request_id", d.RequestID))
		return nil
	}
	return nil
}

// RecordAttempt persists a route_attempts row. On any write error it logs and
// returns nil — recording must never abort routing.
func (r *RouteRecorder) RecordAttempt(ctx context.Context, a RouteAttempt) error {
	if err := r.insertAttempt(ctx, a); err != nil {
		r.log.Error("route attempt record failed", Err(err), String("route_decision_id", a.RouteDecisionID))
		return nil
	}
	return nil
}

func (r *RouteRecorder) insertDecision(ctx context.Context, d RouteDecision) error {
	summaryJSON, err := marshalJSON(d.CandidateSummary)
	if err != nil {
		return err
	}
	reasonsJSON, err := marshalJSON(coalesceReasons(d.ExclusionReasons))
	if err != nil {
		return err
	}
	var scoresArg any
	if d.Scores != nil {
		scoresJSON, merr := marshalJSON(d.Scores)
		if merr != nil {
			return merr
		}
		scoresArg = scoresJSON
	}

	_, err = r.db.ExecContext(ctx,
		`INSERT INTO route_decisions (
			id, request_id, tier, workload_profile_bucket, candidate_summary,
			exclusion_reasons, chosen_provider_id, chosen_provider_model_id,
			chosen_funding, scores, requested_thinking, applied_thinking,
			tier_clamped, certified_clamped, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.RequestID, d.Tier, d.WorkloadProfileBucket, summaryJSON,
		reasonsJSON, nullString(d.ChosenProviderID), nullString(d.ChosenProviderModelID),
		nullString(d.ChosenFunding), scoresArg, nullString(d.RequestedThinking), nullString(d.AppliedThinking),
		boolToInt(d.TierClamped), boolToInt(d.CertifiedClamped), d.CreatedAt.Unix(),
	)
	return err
}

func (r *RouteRecorder) insertAttempt(ctx context.Context, a RouteAttempt) error {
	var latencyArg, finishedArg any
	if a.LatencyMS != nil {
		latencyArg = *a.LatencyMS
	}
	if a.FinishedAt != nil {
		finishedArg = a.FinishedAt.Unix()
	}

	_, err := r.db.ExecContext(ctx,
		`INSERT INTO route_attempts (
			id, route_decision_id, attempt_number, provider_id, account_id,
			offering_operation_id, latency_ms, status, thinking_clamped,
			reservation_id, started_at, finished_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.RouteDecisionID, a.AttemptNumber, a.ProviderID, a.AccountID,
		a.OfferingOperationID, latencyArg, string(normalizeStatus(a.Status)), boolToInt(a.ThinkingClamped),
		nullString(a.ReservationID), a.StartedAt.Unix(), finishedArg,
	)
	return err
}

// coalesceReasons returns a non-nil map so the NOT NULL exclusion_reasons column
// always receives a JSON object ("{}" rather than "null").
func coalesceReasons(m map[string]int) map[string]int {
	if m == nil {
		return map[string]int{}
	}
	return m
}

func marshalJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// nullString maps "" to a SQL NULL (nil arg), else the string itself.
func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
