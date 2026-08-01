package storage

import (
	"context"
	"fmt"
	"strings"
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

// TestCertificationRepo_ListForAdmissionCensusCoversEveryStatus is the
// load-bearing difference between this query and ListForReview.
//
// ListForReview filters to observed/suspended/expired — the states the review
// DRAINER can advance. The admission census must NOT use that filter, because
// the single most important non-routable row in the whole model is `certified`
// with capability truth `unknown`: it is excluded from the drainer's backlog
// (there is nothing to re-probe from `certified`) yet models.Routable rejects it.
// A census built on ListForReview would report that row as no problem at all.
func TestCertificationRepo_ListForAdmissionCensusCoversEveryStatus(t *testing.T) {
	now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	db := migratedCatalogDB(t)
	repo := NewCertificationRepo(db, fixedClock(now))
	ctx := context.Background()

	seed := func(name, state, truth string) string {
		id := seedOfferingOperationChain(t, db, "acct-census", "prov-census", "model-census", "pm-census-"+name)
		if err := insertCertification(db, id, state, truth); err != nil {
			t.Fatalf("seed certification %q: %v", name, err)
		}
		return id
	}

	want := map[string][2]string{
		seed("routable", "certified", "supported"):     {"certified", "supported"},
		seed("certunknown", "certified", "unknown"):    {"certified", "unknown"},
		seed("certunsupp", "certified", "unsupported"): {"certified", "unsupported"},
		seed("observed", "observed", "unknown"):        {"observed", "unknown"},
		seed("probing", "probing", "unknown"):          {"probing", "unknown"},
		seed("discovered", "discovered", "unknown"):    {"discovered", "unknown"},
		seed("suspended", "suspended", "unsupported"):  {"suspended", "unsupported"},
		seed("expired", "expired", "unknown"):          {"expired", "unknown"},
	}

	items, truncated, err := repo.ListForAdmissionCensus(ctx, 100)
	if err != nil {
		t.Fatalf("ListForAdmissionCensus: %v", err)
	}
	if truncated {
		t.Fatalf("truncated = true for a limit of 100 over %d rows, want false", len(want))
	}
	if len(items) != len(want) {
		t.Fatalf("items = %d, want %d (every certification row, whatever its status)", len(items), len(want))
	}
	for _, it := range items {
		expect, ok := want[it.OfferingOperationID]
		if !ok {
			t.Fatalf("census returned an unseeded id %q", it.OfferingOperationID)
		}
		if string(it.State) != expect[0] || string(it.Truth) != expect[1] {
			t.Errorf("%s = (%q,%q), want (%q,%q)", it.OfferingOperationID, it.State, it.Truth, expect[0], expect[1])
		}
	}

	// Deterministic order across repeated calls, so a truncated census always
	// truncates the same way rather than sampling at random.
	var first []string
	for i := 0; i < 3; i++ {
		got, _, err := repo.ListForAdmissionCensus(ctx, 100)
		if err != nil {
			t.Fatalf("ListForAdmissionCensus call %d: %v", i, err)
		}
		ids := make([]string, 0, len(got))
		for _, it := range got {
			ids = append(ids, it.OfferingOperationID)
		}
		if i == 0 {
			first = ids
			continue
		}
		if strings.Join(ids, ",") != strings.Join(first, ",") {
			t.Fatalf("call %d order = %v, want the stable %v", i, ids, first)
		}
	}
}

// TestCertificationRepo_ListForAdmissionCensusIsBounded proves the census is
// bounded and, when it is cut short, SAYS SO. A silently truncated count reads
// as a complete one, which is the difference between "3 offerings need review"
// and "at least 3 of an unknown number do".
func TestCertificationRepo_ListForAdmissionCensusIsBounded(t *testing.T) {
	now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	db := migratedCatalogDB(t)
	repo := NewCertificationRepo(db, fixedClock(now))
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		id := seedOfferingOperationChain(t, db, "acct-bound", "prov-bound", "model-bound", fmt.Sprintf("pm-bound-%d", i))
		if err := insertCertification(db, id, "observed", "unknown"); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	tests := []struct {
		name          string
		limit         int
		wantLen       int
		wantTruncated bool
	}{
		{name: "limit under the row count truncates and reports it", limit: 2, wantLen: 2, wantTruncated: true},
		{name: "limit exactly at the row count is not truncated", limit: 5, wantLen: 5, wantTruncated: false},
		{name: "limit above the row count is not truncated", limit: 50, wantLen: 5, wantTruncated: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			items, truncated, err := repo.ListForAdmissionCensus(ctx, tc.limit)
			if err != nil {
				t.Fatalf("ListForAdmissionCensus: %v", err)
			}
			if len(items) != tc.wantLen {
				t.Fatalf("items = %d, want %d (the limit must be honoured exactly, never exceeded)", len(items), tc.wantLen)
			}
			if truncated != tc.wantTruncated {
				t.Fatalf("truncated = %v, want %v", truncated, tc.wantTruncated)
			}
		})
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
