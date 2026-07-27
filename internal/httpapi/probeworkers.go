package httpapi

import (
	"context"
	"fmt"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/intelligence"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// defaultSchedulerReviewBatchSize bounds the ReviewDrainer BuildSchedulerWorkers
// constructs, mirroring recertifyBatchSize's own small-batch posture.
const defaultSchedulerReviewBatchSize = 50

// BuildSchedulerWorkers constructs the QuotaWorkers (P3b-JOBS-001) and
// ProbeWorkers (P3c-JOBS-001) instances the boot-level scheduler
// (internal/app) drives. It is this package's one composition root for
// BACKGROUND workers, mirroring ControlMux's role as the composition
// root for HTTP: internal/app has no visibility into this package's
// unexported auditEmitter type (or any other internal wiring detail), so
// it cannot construct QuotaWorkers/ProbeWorkers itself — this function
// is the seam that lets it obtain fully-wired workers through one call.
// owner is QuotaWorkers' stable lease-attribution token (see its own
// NewQuotaWorkers doc comment). now/newID default to time.Now/
// newOAuthTransactionID when nil.
func BuildSchedulerWorkers(db *storage.DB, owner string, now func() time.Time, newID func() string) (*QuotaWorkers, *ProbeWorkers, error) {
	if now == nil {
		now = time.Now
	}
	if newID == nil {
		newID = newOAuthTransactionID
	}

	audit := newAuditEmitter(db, nil)
	jobRepo := storage.NewJobRepo(db)

	quotaLifecycleRepo := storage.NewQuotaLifecycleRepo(db, now, nil)
	reconciliationRepo := storage.NewReconciliationRepo(db, now, quota.DefaultReconciliationPolicy(), quotaLifecycleRepo, nil)
	quotaWorkers := NewQuotaWorkers(reconciliationRepo, quotaLifecycleRepo, jobRepo, audit, quota.DefaultReconciliationPolicy(), owner, now, newID)

	certRepo := storage.NewCertificationRepo(db, now)
	probeRunRepo := storage.NewProbeRunRepo(db, now, intelligence.DefaultProbeSafetyPolicy().ContextProbeCooldown)
	certAuditor := newCertificationAuditorAdapter(audit)
	driver, err := intelligence.NewCertificationDriver(certRepo, certAuditor, intelligence.DefaultProbeRetryBudget, now)
	if err != nil {
		return nil, nil, fmt.Errorf("httpapi: build scheduler workers: %w", err)
	}
	drainer, err := intelligence.NewReviewDrainer(certRepo, driver, defaultSchedulerReviewBatchSize, now)
	if err != nil {
		return nil, nil, fmt.Errorf("httpapi: build scheduler workers: %w", err)
	}
	probeWorkers := NewProbeWorkers(certRepo, probeRunRepo, drainer, driver, jobRepo, 0, 0, now, newID)

	return quotaWorkers, probeWorkers, nil
}

// DefaultRecertifyTTL is RecertifyTick's default staleness window: a
// `certified` row whose evidence (updated_at) is older than this is
// considered stale (04 §5 edge 9: "evidence staleness (TTL)").
const DefaultRecertifyTTL = 30 * 24 * time.Hour

// DefaultStaleProbeAfter is ReclaimTick's default in-flight staleness
// window: a probe_runs row still pending/running after this long is
// presumed to belong to a crashed process rather than a genuinely slow
// attempt.
const DefaultStaleProbeAfter = 10 * time.Minute

// recertifyBatchSize bounds one RecertifyTick sweep, mirroring
// ReviewDrainer's own small-batch posture (04 §5: "small batches").
const recertifyBatchSize = 100

// ProbeWorkers hosts the P3c-JOBS-001 review-drain, recertification, and
// stale-probe-reclaim worker ticks as invocable, tested components — the
// P3c counterpart to QuotaWorkers (P3b-JOBS-001), mirroring its shape
// exactly: each tick records its sweep as ONE tracked job (09 §3.12)
// whose result_ref is counts only, and one item's error is counted
// (never fatal to the batch).
type ProbeWorkers struct {
	certs           *storage.CertificationRepo
	probeRuns       *storage.ProbeRunRepo
	drainer         *intelligence.ReviewDrainer
	driver          *intelligence.CertificationDriver
	jobs            *storage.JobRepo
	recertifyTTL    time.Duration
	staleProbeAfter time.Duration
	now             func() time.Time
	newID           func() string
}

// NewProbeWorkers builds the worker set. recertifyTTL/staleProbeAfter
// default to DefaultRecertifyTTL/DefaultStaleProbeAfter when <= 0.
// now/newID default to time.Now/newOAuthTransactionID when nil, exactly
// like every other injectable clock/id-minter in this package.
func NewProbeWorkers(
	certs *storage.CertificationRepo,
	probeRuns *storage.ProbeRunRepo,
	drainer *intelligence.ReviewDrainer,
	driver *intelligence.CertificationDriver,
	jobs *storage.JobRepo,
	recertifyTTL time.Duration,
	staleProbeAfter time.Duration,
	now func() time.Time,
	newID func() string,
) *ProbeWorkers {
	if recertifyTTL <= 0 {
		recertifyTTL = DefaultRecertifyTTL
	}
	if staleProbeAfter <= 0 {
		staleProbeAfter = DefaultStaleProbeAfter
	}
	if now == nil {
		now = time.Now
	}
	if newID == nil {
		newID = newOAuthTransactionID
	}
	return &ProbeWorkers{
		certs: certs, probeRuns: probeRuns, drainer: drainer, driver: driver, jobs: jobs,
		recertifyTTL: recertifyTTL, staleProbeAfter: staleProbeAfter, now: now, newID: newID,
	}
}

// failJob marks jobID terminally failed with the given typed code,
// mirroring probe.go's own failJob / QuotaWorkers' MarkTerminal-on-
// background-context convention.
func (w *ProbeWorkers) failJob(jobID, code, message string) {
	_ = w.jobs.MarkTerminal(context.Background(), jobID, storage.JobFailed, w.now(), "",
		&storage.JobError{Code: code, Message: message}, storage.DefaultJobRetention)
}

// DrainTick runs intelligence.ReviewDrainer.Drain and records the whole
// sweep as ONE tracked job of kind probe. An empty queue succeeds with
// zero counts, never an error — ticks are idempotent in that sense. A
// recovered panic marks the job failed with a typed code rather than
// leaving it stuck running or crashing the process, mirroring
// QuotaWorkers.ReconcileTick's own top-of-function recover.
func (w *ProbeWorkers) DrainTick(ctx context.Context) (result intelligence.DrainResult, err error) {
	jobID := w.newID()
	startedAt := w.now()
	if createErr := w.jobs.Create(ctx, jobID, string(storage.JobKindProbe), startedAt); createErr != nil {
		return intelligence.DrainResult{}, createErr
	}
	if runErr := w.jobs.MarkRunning(ctx, jobID, startedAt); runErr != nil {
		w.failJob(jobID, "internal", "drain tick failed to start")
		return intelligence.DrainResult{}, runErr
	}

	defer func() {
		if rec := recover(); rec != nil {
			w.failJob(jobID, "internal", "drain tick failed unexpectedly")
			err = fmt.Errorf("httpapi: drain tick panicked: %v", rec)
		}
	}()

	drained, drainErr := w.drainer.Drain(ctx)
	if drainErr != nil {
		w.failJob(jobID, "internal", "drain tick failed")
		return intelligence.DrainResult{}, drainErr
	}

	resultRef := fmt.Sprintf("scanned=%d,advanced=%d,skipped=%d,failed=%d", drained.Scanned, drained.Advanced, drained.Skipped, drained.Failed)
	_ = w.jobs.MarkTerminal(context.Background(), jobID, storage.JobCompleted, w.now(), resultRef, nil, storage.DefaultJobRetention)
	return drained, nil
}

// RecertifyTickResult tallies one RecertifyTick sweep's outcome — counts
// only, per 09 §3.12's result_ref contract.
type RecertifyTickResult struct {
	Scanned, Expired, Failed int
}

// RecertifyTick finds `certified` rows whose evidence is older than
// w.recertifyTTL and drives CertificationDriver.Expire on each (04 §5
// edge 9), so the review drainer's next pass re-probes them (edge 10).
// One row's error is counted (Failed) and skipped, never fatal to the
// sweep. An empty result set succeeds with zero counts.
func (w *ProbeWorkers) RecertifyTick(ctx context.Context) (result RecertifyTickResult, err error) {
	jobID := w.newID()
	startedAt := w.now()
	if createErr := w.jobs.Create(ctx, jobID, string(storage.JobKindProbe), startedAt); createErr != nil {
		return RecertifyTickResult{}, createErr
	}
	if runErr := w.jobs.MarkRunning(ctx, jobID, startedAt); runErr != nil {
		w.failJob(jobID, "internal", "recertify tick failed to start")
		return RecertifyTickResult{}, runErr
	}

	defer func() {
		if rec := recover(); rec != nil {
			w.failJob(jobID, "internal", "recertify tick failed unexpectedly")
			err = fmt.Errorf("httpapi: recertify tick panicked: %v", rec)
		}
	}()

	cutoff := w.now().Add(-w.recertifyTTL)
	staleIDs, listErr := w.certs.ListStaleCertified(ctx, cutoff, recertifyBatchSize)
	if listErr != nil {
		w.failJob(jobID, "internal", "recertify tick failed to list stale rows")
		return RecertifyTickResult{}, listErr
	}

	result.Scanned = len(staleIDs)
	for _, id := range staleIDs {
		if _, expireErr := w.driver.Expire(ctx, id); expireErr != nil {
			// One row's transition failure (e.g. a concurrent CAS conflict)
			// is skipped, never fatal to the whole sweep — the next tick
			// retries it.
			result.Failed++
			continue
		}
		result.Expired++
	}

	resultRef := fmt.Sprintf("scanned=%d,expired=%d,failed=%d", result.Scanned, result.Expired, result.Failed)
	_ = w.jobs.MarkTerminal(context.Background(), jobID, storage.JobCompleted, w.now(), resultRef, nil, storage.DefaultJobRetention)
	return result, nil
}

// ReclaimTickResult tallies one ReclaimTick sweep's outcome.
type ReclaimTickResult struct {
	Reclaimed int
}

// ReclaimTick calls ProbeRunRepo.ReclaimStale so a crashed process can
// never hold a per-provider in-flight slot forever — the "leases +
// retry" the P3c-JOBS-001 card asks for: the in-flight probe_runs row IS
// the lease, and this tick is what bounds it.
func (w *ProbeWorkers) ReclaimTick(ctx context.Context) (result ReclaimTickResult, err error) {
	jobID := w.newID()
	startedAt := w.now()
	if createErr := w.jobs.Create(ctx, jobID, string(storage.JobKindProbe), startedAt); createErr != nil {
		return ReclaimTickResult{}, createErr
	}
	if runErr := w.jobs.MarkRunning(ctx, jobID, startedAt); runErr != nil {
		w.failJob(jobID, "internal", "reclaim tick failed to start")
		return ReclaimTickResult{}, runErr
	}

	defer func() {
		if rec := recover(); rec != nil {
			w.failJob(jobID, "internal", "reclaim tick failed unexpectedly")
			err = fmt.Errorf("httpapi: reclaim tick panicked: %v", rec)
		}
	}()

	cutoff := w.now().Add(-w.staleProbeAfter)
	n, reclaimErr := w.probeRuns.ReclaimStale(ctx, cutoff)
	if reclaimErr != nil {
		w.failJob(jobID, "internal", "reclaim tick failed")
		return ReclaimTickResult{}, reclaimErr
	}

	result.Reclaimed = n
	resultRef := fmt.Sprintf("reclaimed=%d", n)
	_ = w.jobs.MarkTerminal(context.Background(), jobID, storage.JobCompleted, w.now(), resultRef, nil, storage.DefaultJobRetention)
	return result, nil
}
