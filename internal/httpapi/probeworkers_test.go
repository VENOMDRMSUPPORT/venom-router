package httpapi

// probeworkers_test.go exercises the P3c-JOBS-001 review-drain,
// recertification, and stale-probe-reclaim tick workers
// (internal/httpapi/probeworkers.go). Functional tests build a
// ProbeWorkers directly over a fresh migrated DB — mirroring
// quotaworkers_test.go's posture.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/intelligence"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// probeWorkersTestTimeout bounds every test in this file per the
// deadlock hazard documented on storage's transactional repos: a fast,
// loud failure instead of a hang.
const probeWorkersTestTimeout = 10 * time.Second

type probeWorkersFixture struct {
	workers    *ProbeWorkers
	db         *storage.DB
	jobs       *storage.JobRepo
	certs      *storage.CertificationRepo
	probeRuns  *storage.ProbeRunRepo
	accountID  string
	providerID string
}

// newProbeWorkersFixture seeds a provider + connected account over a
// fresh migrated DB and wires a ProbeWorkers over it with a real
// CertificationRepo/ReviewDrainer/CertificationDriver.
func newProbeWorkersFixture(t *testing.T, clock func() time.Time) *probeWorkersFixture {
	t.Helper()
	db := testControlDB(t)
	const accountID = "acct-pworkers"
	const providerID = "prov-pworkers"
	p3aSeedAccount(t, db, accountID, providerID)

	jobRepo := storage.NewJobRepo(db)
	certRepo := storage.NewCertificationRepo(db, clock)
	probeRunRepo := storage.NewProbeRunRepo(db, clock, 7*24*time.Hour)
	audit := newAuditEmitter(db, nil)
	certAuditor := newCertificationAuditorAdapter(audit)
	driver, err := intelligence.NewCertificationDriver(certRepo, certAuditor, intelligence.DefaultProbeRetryBudget, clock)
	if err != nil {
		t.Fatalf("NewCertificationDriver: %v", err)
	}
	drainer, err := intelligence.NewReviewDrainer(certRepo, driver, 100, clock)
	if err != nil {
		t.Fatalf("NewReviewDrainer: %v", err)
	}

	workers := NewProbeWorkers(certRepo, probeRunRepo, drainer, driver, jobRepo, 24*time.Hour, 10*time.Minute, clock, probeIDCounter())

	return &probeWorkersFixture{
		workers: workers, db: db, jobs: jobRepo, certs: certRepo, probeRuns: probeRunRepo,
		accountID: accountID, providerID: providerID,
	}
}

// seedCertAt seeds a fresh offering-operation + certification row in the
// given state, stamping updated_at directly (bypassing the driver, so
// tests can construct exact staleness fixtures).
func (f *probeWorkersFixture) seedCertAt(t *testing.T, name, state string, updatedAt time.Time) string {
	t.Helper()
	seedModelForCert(t, f.db, "model-"+name)
	seedOfferingForCert(t, f.db, f.accountID, f.providerID, "pm-"+name, "model-"+name)
	opID := "op-" + name
	seedOfferingOperationForCert(t, f.db, opID, f.accountID, f.providerID, "pm-"+name, "tools", state, "unknown", 1, "")
	if _, err := f.db.Conn().Exec(`UPDATE certifications SET updated_at = ? WHERE offering_operation_id = ?`, updatedAt.Unix(), opID); err != nil {
		t.Fatalf("stamp updated_at for %q: %v", opID, err)
	}
	return opID
}

func certStatus(t *testing.T, db *storage.DB, opID string) string {
	t.Helper()
	var status string
	if err := db.Conn().QueryRow(`SELECT status FROM certifications WHERE offering_operation_id = ?`, opID).Scan(&status); err != nil {
		t.Fatalf("query status for %q: %v", opID, err)
	}
	return status
}

// --- DrainTick ---

func TestProbeWorkers_DrainTickIsATrackedJob(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), probeWorkersTestTimeout)
	defer cancel()
	now := time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)
	f := newProbeWorkersFixture(t, func() time.Time { return now })

	opID := f.seedCertAt(t, "drain", "observed", now)

	result, err := f.workers.DrainTick(ctx)
	if err != nil {
		t.Fatalf("DrainTick: %v", err)
	}
	if result.Scanned != 1 || result.Advanced != 1 {
		t.Fatalf("result = %+v, want scanned=1 advanced=1", result)
	}
	if got := certStatus(t, f.db, opID); got != "probing" {
		t.Fatalf("cert status after drain = %q, want probing", got)
	}

	var count int
	var kind, status, resultRef string
	if err := f.db.Conn().QueryRow(`SELECT COUNT(*) FROM jobs`).Scan(&count); err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	if count != 1 {
		t.Fatalf("jobs row count = %d, want 1", count)
	}
	if err := f.db.Conn().QueryRow(`SELECT kind, status, result_ref FROM jobs LIMIT 1`).Scan(&kind, &status, &resultRef); err != nil {
		t.Fatalf("query job row: %v", err)
	}
	if kind != "probe" {
		t.Fatalf("job kind = %q, want probe", kind)
	}
	if status != "completed" {
		t.Fatalf("job status = %q, want completed", status)
	}
	if resultRef != "scanned=1,advanced=1,skipped=0,failed=0" {
		t.Fatalf("result_ref = %q, want counts-only", resultRef)
	}
}

func TestProbeWorkers_EmptyQueueSucceedsWithZeroCounts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), probeWorkersTestTimeout)
	defer cancel()
	now := time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)
	f := newProbeWorkersFixture(t, func() time.Time { return now })

	result, err := f.workers.DrainTick(ctx)
	if err != nil {
		t.Fatalf("DrainTick: %v", err)
	}
	if result.Scanned != 0 || result.Advanced != 0 || result.Skipped != 0 || result.Failed != 0 || result.ByReason != nil {
		t.Fatalf("result = %+v, want all-zero counts and nil ByReason", result)
	}
}

// --- RecertifyTick ---

func TestProbeWorkers_RecertifyExpiresStaleCertifiedRows(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), probeWorkersTestTimeout)
	defer cancel()
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	f := newProbeWorkersFixture(t, func() time.Time { return now })

	cutoff := now.Add(-24 * time.Hour)
	staleID := f.seedCertAt(t, "recert-stale", "certified", cutoff.Add(-time.Hour))
	boundaryID := f.seedCertAt(t, "recert-boundary", "certified", cutoff) // exactly at TTL -> NOT stale
	freshID := f.seedCertAt(t, "recert-fresh", "certified", cutoff.Add(time.Hour))

	result, err := f.workers.RecertifyTick(ctx)
	if err != nil {
		t.Fatalf("RecertifyTick: %v", err)
	}
	if result.Scanned != 1 || result.Expired != 1 || result.Failed != 0 {
		t.Fatalf("result = %+v, want scanned=1 expired=1 failed=0 (only the strictly-stale row)", result)
	}

	if got := certStatus(t, f.db, staleID); got != "expired" {
		t.Fatalf("stale row status = %q, want expired", got)
	}
	if got := certStatus(t, f.db, boundaryID); got != "certified" {
		t.Fatalf("boundary row (exactly at TTL) status = %q, want certified (untouched — boundary is exclusive)", got)
	}
	if got := certStatus(t, f.db, freshID); got != "certified" {
		t.Fatalf("fresh row status = %q, want certified (untouched)", got)
	}
}

func TestProbeWorkers_OneRowFailureIsCountedNotFatal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), probeWorkersTestTimeout)
	defer cancel()
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	f := newProbeWorkersFixture(t, clock)

	cutoff := now.Add(-24 * time.Hour)
	goodID := f.seedCertAt(t, "recert-good", "certified", cutoff.Add(-time.Hour))
	badID := f.seedCertAt(t, "recert-bad", "certified", cutoff.Add(-time.Hour))

	// Rebuild the driver over a store that fails Expire for badID only —
	// ListStaleCertified (via f.certs, the real repo) still surfaces both
	// rows; only the driver's Expire call for badID is injected to fail,
	// proving one row's error does not abort the sweep.
	failingStore := &failOneIDStore{inner: f.certs, failID: badID}
	audit := newAuditEmitter(f.db, nil)
	driver, err := intelligence.NewCertificationDriver(failingStore, newCertificationAuditorAdapter(audit), intelligence.DefaultProbeRetryBudget, clock)
	if err != nil {
		t.Fatalf("NewCertificationDriver: %v", err)
	}
	drainer, err := intelligence.NewReviewDrainer(f.certs, driver, 100, clock)
	if err != nil {
		t.Fatalf("NewReviewDrainer: %v", err)
	}
	workers := NewProbeWorkers(f.certs, f.probeRuns, drainer, driver, f.jobs, 24*time.Hour, 10*time.Minute, clock, probeIDCounter())

	result, err := workers.RecertifyTick(ctx)
	if err != nil {
		t.Fatalf("RecertifyTick: %v", err)
	}
	if result.Scanned != 2 {
		t.Fatalf("result.Scanned = %d, want 2", result.Scanned)
	}
	if result.Expired != 1 || result.Failed != 1 {
		t.Fatalf("result = %+v, want expired=1 failed=1 (one row's failure must not abort the sweep)", result)
	}
	if got := certStatus(t, f.db, goodID); got != "expired" {
		t.Fatalf("good row status = %q, want expired", got)
	}
	if got := certStatus(t, f.db, badID); got != "certified" {
		t.Fatalf("bad row status = %q, want certified (untouched — its Expire call failed)", got)
	}
}

// failOneIDStore wraps a real intelligence.CertificationStore, injecting
// a Load failure for exactly one id — a test-only fake used to prove
// RecertifyTick's per-row failure isolation deterministically, without
// relying on a real race.
type failOneIDStore struct {
	inner  *storage.CertificationRepo
	failID string
}

func (f *failOneIDStore) Load(ctx context.Context, id string) (models.Certification, error) {
	if id == f.failID {
		return models.Certification{}, errors.New("httpapi: injected test failure")
	}
	return f.inner.Load(ctx, id)
}

func (f *failOneIDStore) CompareAndSwap(ctx context.Context, previous, next models.Certification) error {
	return f.inner.CompareAndSwap(ctx, previous, next)
}

// --- ReclaimTick ---

func TestProbeWorkers_ReclaimFreesTheInFlightSlot(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), probeWorkersTestTimeout)
	defer cancel()
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	f := newProbeWorkersFixture(t, func() time.Time { return now })

	opID := f.seedCertAt(t, "reclaim", "probing", now)
	if err := f.probeRuns.Start(ctx, storage.ProbeRunParams{
		ID: "run-stale-inflight", OfferingOperationID: opID, AccountID: f.accountID, ProviderID: f.providerID,
		Operation: "tools", Class: intelligence.ProbeStandard, StartedAt: now.Add(-20 * time.Minute),
	}); err != nil {
		t.Fatalf("seed stale in-flight run: %v", err)
	}

	// Positive control: the stale run genuinely blocks before the reclaim.
	before, err := f.probeRuns.InFlightProbes(ctx, f.providerID)
	if err != nil {
		t.Fatalf("InFlightProbes (before): %v", err)
	}
	if before != 1 {
		t.Fatalf("InFlightProbes before reclaim = %d, want 1 (positive control — otherwise this test is vacuous)", before)
	}

	result, err := f.workers.ReclaimTick(ctx)
	if err != nil {
		t.Fatalf("ReclaimTick: %v", err)
	}
	if result.Reclaimed != 1 {
		t.Fatalf("result.Reclaimed = %d, want 1", result.Reclaimed)
	}

	after, err := f.probeRuns.InFlightProbes(ctx, f.providerID)
	if err != nil {
		t.Fatalf("InFlightProbes (after): %v", err)
	}
	if after != 0 {
		t.Fatalf("InFlightProbes after reclaim = %d, want 0 (the stale slot must be freed)", after)
	}
}
