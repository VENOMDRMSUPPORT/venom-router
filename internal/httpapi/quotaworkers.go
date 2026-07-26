package httpapi

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// QuotaWorkers hosts the P3b-JOBS-001 reconciliation + quota-janitor
// worker ticks as invocable, tested components. Both ticks record their
// sweep as ONE tracked job (09 §3.12), so their progress and outcome are
// readable through the same GET /jobs/{job_id} surface as every other
// job this package produces.
//
// DEVIATION (disclosed): the card names this package's location as
// "internal/quota workers", but internal/quota is a staticgate-enforced
// PURE domain package that may not import internal/storage — the
// orchestration below (creating job rows, claiming/reconciling
// reservations, driving the janitor) necessarily composes storage repos,
// so it lives here in internal/httpapi where every other job-producing
// handler in this package (discovery, quota-refresh) already does the
// same composition. See the governor's own note in this batch's prompt
// (§5, Unit 3) — this was a pre-authorized deviation, not an
// undisclosed one.
type QuotaWorkers struct {
	reconciliation *storage.ReconciliationRepo
	lifecycle      *storage.QuotaLifecycleRepo
	jobs           *storage.JobRepo
	audit          *auditEmitter
	policy         quota.ReconciliationPolicy
	owner          string
	now            func() time.Time
	newID          func() string

	// testAfterClaimHook, when non-nil, runs exactly once immediately
	// after ClaimPending succeeds and before ReconcileTick's loop begins
	// reconciling any claimed item. It exists ONLY so this package's own
	// tests can deterministically simulate a lease stolen "between claim
	// and reconcile" (a real concurrent worker or the janitor winning
	// that race) without a flaky timing-based test — production code
	// never sets it, and NewQuotaWorkers has no parameter for it.
	testAfterClaimHook func(claimed []storage.PendingReservation)
}

// NewQuotaWorkers builds the worker set. owner is this worker instance's
// STABLE lease-attribution token — the same value is used for every
// ClaimPending and ReconcileOne call a single ReconcileTick makes, so a
// lease this instance holds is always attributable to it, never to a
// fresh identity per item. now/newID default to time.Now /
// newOAuthTransactionID when nil, exactly like every other injectable
// clock/id-minter in this package.
func NewQuotaWorkers(
	reconciliation *storage.ReconciliationRepo,
	lifecycle *storage.QuotaLifecycleRepo,
	jobs *storage.JobRepo,
	audit *auditEmitter,
	policy quota.ReconciliationPolicy,
	owner string,
	now func() time.Time,
	newID func() string,
) *QuotaWorkers {
	if now == nil {
		now = time.Now
	}
	if newID == nil {
		newID = newOAuthTransactionID
	}
	return &QuotaWorkers{
		reconciliation: reconciliation,
		lifecycle:      lifecycle,
		jobs:           jobs,
		audit:          audit,
		policy:         policy,
		owner:          owner,
		now:            now,
		newID:          newID,
	}
}

// ReconcileTickResult tallies one ReconcileTick sweep's outcome — counts
// only, per 09 §3.12's result_ref contract; the result_ref this tick's
// job row is terminated with is built ONLY from these counts, never a
// reservation id, provider payload, or any other content.
type ReconcileTickResult struct {
	Claimed            int
	Settled            int
	UnknownConsumption int
	LeaseLost          int
}

// ReconcileTick claims one batch of reconciliation_pending reservations
// under this worker's stable owner token, reconciles each independently,
// and records the whole sweep as ONE tracked job of kind reconciliation.
// An item whose lease was stolen by another worker or the janitor
// (quota.ErrLeaseNotHeld) is skipped — counted, not fatal — so one lost
// race never fails the whole tick or its job. Ticks are idempotent in
// the sense that running one with nothing claimable creates a job that
// succeeds with zero counts, never an error. A recovered panic marks the
// job failed with a typed code rather than leaving it stuck running or
// crashing the process, mirroring runDiscovery/runQuotaRefresh's own
// top-of-function recover.
func (w *QuotaWorkers) ReconcileTick(ctx context.Context) (result ReconcileTickResult, err error) {
	jobID := w.newID()
	startedAt := w.now()
	if createErr := w.jobs.Create(ctx, jobID, string(storage.JobKindReconciliation), startedAt); createErr != nil {
		return ReconcileTickResult{}, createErr
	}
	if runErr := w.jobs.MarkRunning(ctx, jobID, startedAt); runErr != nil {
		_ = w.jobs.MarkTerminal(context.Background(), jobID, storage.JobFailed, w.now(), "",
			&storage.JobError{Code: "internal", Message: "reconciliation tick failed to start"},
			storage.DefaultJobRetention)
		return ReconcileTickResult{}, runErr
	}

	defer func() {
		if rec := recover(); rec != nil {
			_ = w.jobs.MarkTerminal(context.Background(), jobID, storage.JobFailed, w.now(), "",
				&storage.JobError{Code: "internal", Message: "reconciliation tick failed unexpectedly"},
				storage.DefaultJobRetention)
			err = fmt.Errorf("httpapi: reconciliation tick panicked: %v", rec)
		}
	}()

	claimed, claimErr := w.reconciliation.ClaimPending(ctx, w.owner, quota.DefaultLeaseTTL)
	if claimErr != nil {
		_ = w.jobs.MarkTerminal(context.Background(), jobID, storage.JobFailed, w.now(), "",
			&storage.JobError{Code: "internal", Message: "reconciliation tick failed to claim a batch"},
			storage.DefaultJobRetention)
		return ReconcileTickResult{}, claimErr
	}
	if w.testAfterClaimHook != nil {
		w.testAfterClaimHook(claimed)
	}

	for _, p := range claimed {
		result.Claimed++
		outcome, reconcileErr := w.reconciliation.ReconcileOne(ctx, w.owner, p)
		if errors.Is(reconcileErr, quota.ErrLeaseNotHeld) {
			// Another worker (or the janitor) reclaimed this item's lease
			// first — one lost race must not fail the whole tick.
			result.LeaseLost++
			continue
		}
		if reconcileErr != nil {
			// An unexpected per-item storage error: skip it (never abort
			// the batch for one bad item) and let the next sweep retry it.
			continue
		}
		switch outcome.Outcome {
		case quota.ReservationSettled:
			result.Settled++
		case quota.ReservationUnknownConsumption:
			result.UnknownConsumption++
		}
	}

	resultRef := fmt.Sprintf("claimed=%d,settled=%d,unknown_consumption=%d,lease_lost=%d",
		result.Claimed, result.Settled, result.UnknownConsumption, result.LeaseLost)
	_ = w.jobs.MarkTerminal(context.Background(), jobID, storage.JobCompleted, w.now(), resultRef, nil, storage.DefaultJobRetention)
	return result, nil
}

// JanitorTick runs QuotaLifecycleRepo's janitor sweep (recovering
// reservations stuck past their processing deadline) and records it as
// ONE tracked job, mirroring ReconcileTick's job-lifecycle shape exactly.
//
// DEVIATION (disclosed): the frozen storage.JobKind vocabulary
// (internal/storage/jobs.go, not in this batch's exclusive file list)
// registers only discovery/reconciliation/quota_sync — there is no
// distinct "janitor" kind. JanitorTick reuses JobKindReconciliation:
// the janitor operates on the exact same reconciliation_pending state
// machine ReconcileTick does (it is the mechanism that unblocks
// ClaimPending's re-picks), so this is the closest existing kind rather
// than a semantic mismatch like quota_sync (which 09 §3.12/jobs.go
// documents as the provider-evidence WINDOW-INGESTION sweep — an
// unrelated concern). Adding a fourth kind would require editing
// jobs.go, which constraint §2.12's exclusive file list does not permit
// for this batch; the governor should decide whether a dedicated
// "janitor" kind is worth its own future migration-free follow-up.
func (w *QuotaWorkers) JanitorTick(ctx context.Context) (result quota.JanitorResult, err error) {
	jobID := w.newID()
	startedAt := w.now()
	if createErr := w.jobs.Create(ctx, jobID, string(storage.JobKindReconciliation), startedAt); createErr != nil {
		return quota.JanitorResult{}, createErr
	}
	if runErr := w.jobs.MarkRunning(ctx, jobID, startedAt); runErr != nil {
		_ = w.jobs.MarkTerminal(context.Background(), jobID, storage.JobFailed, w.now(), "",
			&storage.JobError{Code: "internal", Message: "janitor tick failed to start"},
			storage.DefaultJobRetention)
		return quota.JanitorResult{}, runErr
	}

	defer func() {
		if rec := recover(); rec != nil {
			_ = w.jobs.MarkTerminal(context.Background(), jobID, storage.JobFailed, w.now(), "",
				&storage.JobError{Code: "internal", Message: "janitor tick failed unexpectedly"},
				storage.DefaultJobRetention)
			err = fmt.Errorf("httpapi: janitor tick panicked: %v", rec)
		}
	}()

	sweep, sweepErr := w.lifecycle.Janitor(ctx)
	if sweepErr != nil {
		_ = w.jobs.MarkTerminal(context.Background(), jobID, storage.JobFailed, w.now(), "",
			&storage.JobError{Code: "internal", Message: "janitor tick failed"},
			storage.DefaultJobRetention)
		return quota.JanitorResult{}, sweepErr
	}

	resultRef := fmt.Sprintf("released=%d,pended=%d,reclaimed=%d,unknown_consumption=%d",
		sweep.Released, sweep.Pended, sweep.Reclaimed, sweep.UnknownConsumption)
	_ = w.jobs.MarkTerminal(context.Background(), jobID, storage.JobCompleted, w.now(), resultRef, nil, storage.DefaultJobRetention)
	return sweep, nil
}
