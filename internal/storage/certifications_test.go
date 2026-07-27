package storage

import (
	"context"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/intelligence"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
)

func newCertRepoFixture(t *testing.T, now time.Time, seed string) (*CertificationRepo, *DB, string) {
	t.Helper()
	db := migratedCatalogDB(t)
	opID := seedOfferingOperationChain(t, db, "acct-"+seed, "prov-"+seed, "model-"+seed, "pm-"+seed)
	if err := insertCertification(db, opID, "discovered", "unknown"); err != nil {
		t.Fatalf("seed certification baseline: %v", err)
	}
	repo := NewCertificationRepo(db, fixedClock(now))
	return repo, db, opID
}

// TestCertificationRepo_LoadRoundTrips proves Load reads back exactly
// what a certification row holds, and a missing row returns the typed
// not-found error.
func TestCertificationRepo_LoadRoundTrips(t *testing.T) {
	now := time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)
	repo, _, opID := newCertRepoFixture(t, now, "load")
	ctx := context.Background()

	got, err := repo.Load(ctx, opID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.OfferingOperationID != opID || got.State != models.CertDiscovered || got.Truth != models.TruthUnknown || got.Version != 1 {
		t.Fatalf("Load = %+v, want discovered/unknown/version=1 baseline for %q", got, opID)
	}

	if _, err := repo.Load(ctx, "does-not-exist"); err == nil {
		t.Fatalf("Load(missing) succeeded, want ErrCertificationNotFound")
	}
}

// TestCertificationRepo_CompareAndSwapGuardsStateAndVersion proves CAS
// guards on (offering_operation_id, status, version) TOGETHER: a stale
// State with a matching Version conflicts, a stale Version with a
// matching State conflicts, and the correct (State, Version) pair
// commits.
func TestCertificationRepo_CompareAndSwapGuardsStateAndVersion(t *testing.T) {
	now := time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)
	repo, _, opID := newCertRepoFixture(t, now, "cas")
	ctx := context.Background()

	baseline, err := repo.Load(ctx, opID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Stale STATE, correct version: must conflict.
	staleState := baseline
	staleState.State = models.CertObserved // wrong: real current state is discovered
	next := staleState
	next.State = models.CertObserved
	next.UpdatedAt = now
	if err := repo.CompareAndSwap(ctx, staleState, next); err == nil {
		t.Fatalf("CompareAndSwap with stale State succeeded, want ErrCertificationConflict")
	}

	// Stale VERSION, correct state: must conflict.
	staleVersion := baseline
	staleVersion.Version = baseline.Version + 5 // wrong: real current version is 1
	next2 := staleVersion
	next2.State = models.CertObserved
	next2.UpdatedAt = now
	if err := repo.CompareAndSwap(ctx, staleVersion, next2); err == nil {
		t.Fatalf("CompareAndSwap with stale Version succeeded, want ErrCertificationConflict")
	}

	// Correct (State, Version): must commit.
	want := baseline
	want.State = models.CertObserved
	want.UpdatedAt = now
	if err := repo.CompareAndSwap(ctx, baseline, want); err != nil {
		t.Fatalf("CompareAndSwap with correct (state,version): %v, want success", err)
	}
	got, err := repo.Load(ctx, opID)
	if err != nil {
		t.Fatalf("Load after CAS: %v", err)
	}
	if got.State != models.CertObserved {
		t.Fatalf("state after CAS = %q, want observed", got.State)
	}
}

// TestCertificationRepo_ListForReviewExcludesCertifiedAndProbing proves
// ListForReview surfaces observed/suspended/expired rows and NEVER a
// certified or probing row.
func TestCertificationRepo_ListForReviewExcludesCertifiedAndProbing(t *testing.T) {
	now := time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)
	db := migratedCatalogDB(t)
	repo := NewCertificationRepo(db, fixedClock(now))
	ctx := context.Background()

	seed := func(state, name string) string {
		id := seedOfferingOperationChain(t, db, "acct-review", "prov-review", "model-review", "pm-review-"+name)
		if err := insertCertification(db, id, state, "unknown"); err != nil {
			t.Fatalf("seed certification %q: %v", name, err)
		}
		return id
	}

	observedID := seed("observed", "observed")
	suspendedID := seed("suspended", "suspended")
	expiredID := seed("expired", "expired")
	certifiedID := seed("certified", "certified")
	probingID := seed("probing", "probing")
	discoveredID := seed("discovered", "discovered") // stays discovered

	items, err := repo.ListForReview(ctx, 100)
	if err != nil {
		t.Fatalf("ListForReview: %v", err)
	}
	got := map[string]bool{}
	for _, it := range items {
		got[it.OfferingOperationID] = true
	}
	for _, want := range []string{observedID, suspendedID, expiredID} {
		if !got[want] {
			t.Errorf("ListForReview missing expected id %q (state observed/suspended/expired)", want)
		}
	}
	for _, mustNotAppear := range []string{certifiedID, probingID, discoveredID} {
		if got[mustNotAppear] {
			t.Errorf("ListForReview returned %q, want it excluded (certified/probing/discovered are never review candidates)", mustNotAppear)
		}
	}
}

// TestCertificationRepo_ListStaleCertifiedBoundary proves the TTL
// boundary is exclusive: a row updated exactly at olderThan is not
// stale, one updated before it is.
func TestCertificationRepo_ListStaleCertifiedBoundary(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	db := migratedCatalogDB(t)
	repo := NewCertificationRepo(db, fixedClock(now))
	ctx := context.Background()

	staleID := seedOfferingOperationChain(t, db, "acct-stale", "prov-stale", "model-stale", "pm-stale")
	boundaryID := seedOfferingOperationChain(t, db, "acct-stale", "prov-stale", "model-stale", "pm-boundary")
	freshID := seedOfferingOperationChain(t, db, "acct-stale", "prov-stale", "model-stale", "pm-fresh")

	cutoff := now.Add(-24 * time.Hour)
	setCertified := func(id string, updatedAt time.Time) {
		if err := insertCertification(db, id, "discovered", "unknown"); err != nil {
			t.Fatalf("seed certification baseline: %v", err)
		}
		if _, err := db.Conn().Exec(
			`UPDATE certifications SET status = 'certified', updated_at = ? WHERE offering_operation_id = ?`,
			updatedAt.Unix(), id,
		); err != nil {
			t.Fatalf("set certified: %v", err)
		}
	}
	setCertified(staleID, cutoff.Add(-time.Hour)) // strictly before cutoff -> stale
	setCertified(boundaryID, cutoff)              // exactly at cutoff -> NOT stale
	setCertified(freshID, cutoff.Add(time.Hour))  // after cutoff -> not stale

	ids, err := repo.ListStaleCertified(ctx, cutoff, 100)
	if err != nil {
		t.Fatalf("ListStaleCertified: %v", err)
	}
	got := map[string]bool{}
	for _, id := range ids {
		got[id] = true
	}
	if !got[staleID] {
		t.Errorf("ListStaleCertified missing %q (strictly before cutoff)", staleID)
	}
	if got[boundaryID] {
		t.Errorf("ListStaleCertified returned %q (exactly at cutoff), want excluded (boundary is exclusive)", boundaryID)
	}
	if got[freshID] {
		t.Errorf("ListStaleCertified returned %q (after cutoff), want excluded", freshID)
	}
}

// TestCertificationRepo_SatisfiesIntelligencePorts is a runtime-reachable
// proof (alongside the compile-time var _ assertions in certifications.go)
// that CertificationRepo's methods are callable through both ports.
func TestCertificationRepo_SatisfiesIntelligencePorts(t *testing.T) {
	repo, _, _ := newCertRepoFixture(t, time.Now(), "ports")
	var (
		_ intelligence.CertificationStore = repo
		_ intelligence.ReviewQueue        = repo
	)
}
