package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/intelligence"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
)

// ProbeRunRepo implements intelligence.ProbeSpendReader,
// intelligence.ProbeInFlightReader, intelligence.ProbeCooldownReader, and
// (task-3 fix round) intelligence.ProbeCapabilityCooldownReader over the
// M6-enabling probe_runs / probe_run_costs tables (00010_probe_runs.sql).
// It is the durable record of every probe attempt this project ever runs —
// the safety list in 04 §2 (per-account spend, per-provider concurrency,
// the context-probe cooldown) is answerable only because every attempt is
// recorded here first.
type ProbeRunRepo struct {
	db                   *DB
	now                  func() time.Time
	contextProbeCooldown time.Duration
	// capabilityProbeCooldown is 0 (disabled) unless
	// WithCapabilityProbeCooldown sets it — an OPT-IN, copy-returning field
	// (mirrors ModelsHandler.WithProbeRuns's own pattern) so every existing
	// NewProbeRunRepo call site is unaffected by its mere existence.
	capabilityProbeCooldown time.Duration
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

// WithCapabilityProbeCooldown returns a copy of r with the capability-probe
// cooldown window set (task-3 fix round, CRITICAL 2b). d <= 0 leaves the
// gate disabled (CapabilityProbeCooldownUntil always answers nil), which is
// r's default before this method is ever called — every existing
// NewProbeRunRepo call site is therefore unaffected.
func (r *ProbeRunRepo) WithCapabilityProbeCooldown(d time.Duration) *ProbeRunRepo {
	clone := *r
	clone.capabilityProbeCooldown = d
	return &clone
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

// InFlightProbesExcluding is InFlightProbes minus one run id. It exists
// because the concurrency cap can only be real if the in-flight row is
// written BEFORE the transport call, not after it — and a run that has
// already claimed its own slot must not then count itself out of
// admission (with the default cap of 1, self-counting would make every
// probe refuse itself). The caller wraps this to satisfy
// intelligence.ProbeInFlightReader; the port's own signature is untouched.
func (r *ProbeRunRepo) InFlightProbesExcluding(ctx context.Context, providerID, excludeRunID string) (int, error) {
	var count int
	if err := r.db.Conn().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM probe_runs WHERE provider_id = ? AND id <> ? AND execution IN ('pending', 'running') AND finished_at IS NULL`,
		providerID, excludeRunID,
	).Scan(&count); err != nil {
		return 0, fmt.Errorf("storage: in-flight probes for %q excluding %q: %w", providerID, excludeRunID, err)
	}
	return count, nil
}

// AttachReservation records the reservation a run obtained. The run row is
// inserted before admission (so it can hold the in-flight slot across the
// transport call), which is why the reservation id arrives afterwards
// rather than at Start.
func (r *ProbeRunRepo) AttachReservation(ctx context.Context, runID, reservationID string) error {
	if runID == "" || reservationID == "" {
		return fmt.Errorf("%w: run id and reservation id are both required", ErrInvalidProbeRunParams)
	}
	if _, err := r.db.Conn().ExecContext(ctx,
		`UPDATE probe_runs SET reservation_id = ? WHERE id = ? AND reservation_id IS NULL`,
		reservationID, runID,
	); err != nil {
		return fmt.Errorf("storage: attach reservation to probe run %q: %w", runID, err)
	}
	return nil
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
		offeringOperationID, string(models.OperationContextWindow),
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

// CapabilityProbeCooldownUntil implements
// intelligence.ProbeCapabilityCooldownReader (task-3 fix round, CRITICAL
// 2b): the most recent probe_runs row for (offeringOperationID, operation)
// — of ANY execution — plus r.capabilityProbeCooldown, or nil when no run
// exists yet or the cooldown is disabled (capabilityProbeCooldown <= 0,
// r's default before WithCapabilityProbeCooldown is called).
//
// Unlike ProbeCooldownUntil above, this deliberately does NOT filter on
// execution = 'succeeded'. ProbeCooldownUntil's succeeded-only rule is
// correct for the context probe because 04 §2 attaches ITS week-long window
// to having actually read a limit. This cooldown exists for the opposite
// reason: task-3's own failure mode is an INCONCLUSIVE or FAILED capability
// probe being re-selected and re-attempted every 30s scheduler round
// forever, because "no succeeded run yet" stays true on every single one of
// those attempts. A cooldown gated on "succeeded" would never fire for the
// exact case it exists to bound — it has to key off the last ATTEMPT,
// whatever its outcome, or it is a no-op.
func (r *ProbeRunRepo) CapabilityProbeCooldownUntil(ctx context.Context, offeringOperationID string, operation models.Operation) (*time.Time, error) {
	if r.capabilityProbeCooldown <= 0 {
		return nil, nil
	}
	var startedAt int64
	err := r.db.Conn().QueryRowContext(ctx,
		`SELECT started_at FROM probe_runs
		 WHERE offering_operation_id = ? AND operation = ?
		 ORDER BY started_at DESC LIMIT 1`,
		offeringOperationID, string(operation),
	).Scan(&startedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("storage: capability probe cooldown for %q: %w", offeringOperationID, err)
	}
	until := time.Unix(startedAt, 0).UTC().Add(r.capabilityProbeCooldown)
	return &until, nil
}

// LatestExecution returns the most recent probe_runs row's execution
// value for (offeringOperationID, operation) (04 §2's "probe execution"
// dimension, surfaced by P3c-CAPI-001's certification read) — ok is false
// when no probe of THAT operation has ever run for this offering-operation.
//
// The operation filter is load-bearing, not incidental (automatic-model-
// qualification, task 4, fix round 1): offering_operation_id alone stopped
// being a reliable proxy for "which operation was probed" once
// qualification.go's context-window probe started anchoring its own
// bookkeeping on an offering's CHAT offering_operation row id (no
// context_window row exists for a genuinely uncatalogued model — see
// contextProbeCandidate's own doc comment in qualification.go). Before that
// change, this method's only caller ever asked about a row this SAME method
// could only ever see written by ITS OWN operation (POST
// /offerings/{id}/probe's own {id} always addresses one operation's own
// certification row, and only tools/structured_output/vision probes had a
// caller). Without this filter, GET /offerings/{chatOpID}/certification
// would surface a context probe's retryable_failure/inconclusive/succeeded
// execution as if it were the CHAT capability's own probe result — a chat
// offering that is certified, supported, and working would read as if a
// probe had just failed against it.
func (r *ProbeRunRepo) LatestExecution(ctx context.Context, offeringOperationID string, operation models.Operation) (intelligence.ProbeExecution, bool, error) {
	var execution string
	err := r.db.Conn().QueryRowContext(ctx,
		`SELECT execution FROM probe_runs WHERE offering_operation_id = ? AND operation = ? ORDER BY started_at DESC LIMIT 1`,
		offeringOperationID, string(operation),
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
// (offeringOperationID, operation) — the basis P3c-CAPI-001's handler uses
// to derive intelligence.CertificationDriver.RecordAttempt's `attempts`
// parameter (that attempt's own ordinal is this count plus one, computed by
// the caller BEFORE calling Start for the current attempt). Deliberately
// ALL-TIME, never windowed: this feeds models.RetryPolicy.Attempts against
// a FIXED total retry budget (04 §5 edge 3, DefaultProbeRetryBudget), not a
// rate — a budget that reset itself after some window would let a
// persistently-failing candidate retry forever, exactly the treadmill this
// project keeps closing elsewhere.
//
// operation is load-bearing (whole-branch review, MINOR — the same family
// as SucceededOfferingOperationIDs' own identical fix, see
// OfferingOperationThreshold's doc comment): a single offering_operation_id
// can carry probe_runs rows for TWO different operations, because Task 4's
// context-window probe deliberately anchors on a live offering's own CHAT
// id. Without this filter, a context-probe attempt on that shared id would
// inflate the CAPABILITY probe's own attempt count (or vice versa),
// mirroring LatestExecution's own operation filter — which this method
// lacked before this fix, unlike its sibling.
func (r *ProbeRunRepo) CountAttempts(ctx context.Context, offeringOperationID string, operation models.Operation) (int, error) {
	var count int
	if err := r.db.Conn().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM probe_runs WHERE offering_operation_id = ? AND operation = ?`,
		offeringOperationID, string(operation),
	).Scan(&count); err != nil {
		return 0, fmt.Errorf("storage: count probe attempts for %q: %w", offeringOperationID, err)
	}
	return count, nil
}

// OfferingOperationThreshold is one requested entry for
// SucceededOfferingOperationIDs (whole-branch review, MINOR — the
// SucceededOfferingOperationIDs/CountAttempts family): the OPERATION whose
// success must be checked, alongside the earliest finished_at (Unix
// seconds) that still counts. Operation is load-bearing, not decorative:
// probe_runs.offering_operation_id is not a 1:1 key of "which capability
// this row is evidence for" — Task 4's context-window probe deliberately
// reuses a LIVE offering's CHAT offering_operation_id as its own FK anchor
// (contextProbeCandidate's own doc comment, qualification.go), so a single
// offering_operation_id can carry BOTH a chat probe_runs row (operation=
// 'chat') and a context_window probe_runs row (operation='context_window').
// Without this field, a succeeded context-probe attempt would silently mark
// that SAME id's CHAT capability "proved" too, purely because they share an
// id — a context-probe row anchored on the chat op marking chat "proved".
type OfferingOperationThreshold struct {
	Operation string
	Threshold int64
}

// SucceededOfferingOperationIDs returns the subset of ids in thresholds that
// have a SUCCEEDED probe_runs row, for THAT SAME id's own requested
// operation, finishing AT OR AFTER that id's threshold — the batched
// (task-5) query the httpapi assembler uses to derive capability provenance
// ("probed" vs "declared") for one page of offerings at a time, ONE query
// per page rather than one per offering-operation (no N+1).
//
// thresholds maps offering_operation_id -> {the operation this id's
// certification is actually FOR, the CURRENT certification's certified_at
// (Unix seconds)}. The certified_at-threshold half is the whole-branch-
// review fix (2026-08-05): the prior version matched ANY succeeded run
// ever, untied to the certification it was meant to corroborate. That let
// an out-of-cooldown certification EXPIRE, get re-certified from a bare
// DECLARATION (no new probe), and still surface as "probed" purely because
// some OLDER, pre-expiry probe run happened to have succeeded once — stale
// evidence laundering a certification it never actually attested to. A run
// only counts when it finished no earlier than the certification it is
// being asked to corroborate. The operation half is a LATER whole-branch-
// review fix (MINOR, see OfferingOperationThreshold's own doc comment):
// without it, this method's own sibling LatestExecution's operation filter
// had no counterpart here, so a context-probe success anchored on a chat
// offering_operation_id could mark chat "proved".
//
// A threshold of 0 (or negative) is the fail-closed "this id was never
// really certified" sentinel: it counts nothing, ever, regardless of any
// succeeded run's finish time — an all-time-valid Unix-epoch threshold
// would otherwise let "any run at all" satisfy it. Callers always pass the
// real certified_at for a certified+supported operation; storage enforces
// the fail-closed rule independently so a caller bug can never fabricate
// "probed".
//
// An empty input returns an empty, non-nil map without touching the
// database; an id with no succeeded run for the REQUESTED operation, only a
// failed run, a threshold of 0, or a succeeded run that finished before its
// threshold is simply absent from the result — never present with a false
// value. An id never passed in thresholds is likewise never present,
// however its own probe history reads.
//
// Implementation note: an IN (...) query cannot carry a PER-ROW threshold,
// so this fetches MAX(finished_at) grouped by (offering_operation_id,
// operation) for the requested ids in ONE query, then compares each id's
// REQUESTED-operation max against thresholds[id].Threshold here in Go —
// simpler than a joined VALUES/json_each table and exactly as correct,
// since each id's threshold is only ever compared against ITS OWN,
// operation-matched max.
func (r *ProbeRunRepo) SucceededOfferingOperationIDs(ctx context.Context, thresholds map[string]OfferingOperationThreshold) (map[string]bool, error) {
	out := make(map[string]bool, len(thresholds))
	if len(thresholds) == 0 {
		return out, nil
	}

	ids := make([]string, 0, len(thresholds))
	placeholders := make([]string, 0, len(thresholds))
	args := make([]any, 0, len(thresholds))
	for id, t := range thresholds {
		if t.Threshold <= 0 {
			// Fail closed: never query for an id whose threshold can never
			// be satisfied anyway (see doc comment above).
			continue
		}
		ids = append(ids, id)
		placeholders = append(placeholders, "?")
		args = append(args, id)
	}
	if len(ids) == 0 {
		return out, nil
	}

	// Placeholder-growth note: this IN(...) list grows with the number of
	// distinct offering_operation_ids on one page of offerings — bounded by
	// httpapi's own maxPageLimit (200 offerings) times models.Operation's
	// fixed eight-value vocabulary, i.e. at most 1600 params per call, well
	// under modernc.org/sqlite's placeholder ceiling of 32766. Never remove
	// the caller-side page bound on the assumption this query can absorb an
	// unbounded id list.
	query := `SELECT offering_operation_id, operation, MAX(finished_at) FROM probe_runs
		 WHERE execution = 'succeeded' AND offering_operation_id IN (` +
		strings.Join(placeholders, ",") + `)
		 GROUP BY offering_operation_id, operation`
	rows, err := r.db.Conn().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("storage: succeeded offering-operation ids: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var id, operation string
		var maxFinished sql.NullInt64
		if err := rows.Scan(&id, &operation, &maxFinished); err != nil {
			return nil, fmt.Errorf("storage: succeeded offering-operation ids: scan: %w", err)
		}
		want, requested := thresholds[id]
		if !requested || operation != want.Operation {
			// A succeeded run for a DIFFERENT operation than this id was
			// requested for (e.g. a context-probe row anchored on a chat
			// offering_operation_id) must never count towards this id's
			// own requested operation's provenance.
			continue
		}
		if maxFinished.Valid && maxFinished.Int64 >= want.Threshold {
			out[id] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: succeeded offering-operation ids: %w", err)
	}
	return out, nil
}

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
	_ intelligence.ProbeSpendReader              = (*ProbeRunRepo)(nil)
	_ intelligence.ProbeInFlightReader           = (*ProbeRunRepo)(nil)
	_ intelligence.ProbeCooldownReader           = (*ProbeRunRepo)(nil)
	_ intelligence.ProbeCapabilityCooldownReader = (*ProbeRunRepo)(nil)
)
