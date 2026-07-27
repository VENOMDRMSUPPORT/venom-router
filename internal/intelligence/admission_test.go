package intelligence

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
)

func TestAdmit_ConjunctionIsTheOnlyRoutableCombination(t *testing.T) {
	states := models.CertificationStates()
	truths := []models.CapabilityTruth{models.TruthUnknown, models.TruthSupported, models.TruthUnsupported}

	routableCount := 0
	for _, s := range states {
		for _, tr := range truths {
			in := AdmissionInput{
				State: s, Truth: tr,
				IdentityResolved: true, ContextVerified: true, FundingKnown: true, HealthyAccount: true,
			}
			verdict := Admit(in)
			wantRoutable := s == models.CertCertified && tr == models.TruthSupported
			if verdict.Routable != wantRoutable {
				t.Errorf("state=%q truth=%q Routable=%v, want %v", s, tr, verdict.Routable, wantRoutable)
			}
			if wantRoutable {
				routableCount++
				if len(verdict.Reasons) != 0 {
					t.Errorf("state=%q truth=%q Reasons=%v, want none", s, tr, verdict.Reasons)
				}
			} else {
				found := false
				for _, r := range verdict.Reasons {
					if r == AdmissionCapabilityNotCertified {
						found = true
					}
				}
				if !found {
					t.Errorf("state=%q truth=%q Reasons=%v, want capability_not_certified", s, tr, verdict.Reasons)
				}
			}
		}
	}
	if routableCount != 1 {
		t.Fatalf("routableCount = %d, want exactly 1 (only certified+supported)", routableCount)
	}
}

func TestAdmit_EveryNonCertificationGateBlocks(t *testing.T) {
	base := AdmissionInput{
		State: models.CertCertified, Truth: models.TruthSupported,
		IdentityResolved: true, ContextVerified: true, FundingKnown: true, HealthyAccount: true,
	}

	// Positive control: everything satisfied.
	verdict := Admit(base)
	if !verdict.Routable || len(verdict.Reasons) != 0 {
		t.Fatalf("base verdict = %+v, want routable with zero reasons", verdict)
	}

	tests := []struct {
		name   string
		modify func(in *AdmissionInput)
		reason AdmissionReason
	}{
		{"identity unresolved", func(in *AdmissionInput) { in.IdentityResolved = false }, AdmissionIdentityUnresolved},
		{"context unverified", func(in *AdmissionInput) { in.ContextVerified = false }, AdmissionContextUnverified},
		{"funding unknown", func(in *AdmissionInput) { in.FundingKnown = false }, AdmissionFundingUnknown},
		{"no healthy account", func(in *AdmissionInput) { in.HealthyAccount = false }, AdmissionNoHealthyAccount},
		{"quota exhausted", func(in *AdmissionInput) { in.QuotaExhausted = true }, AdmissionQuotaExhausted},
		{"quota insufficient", func(in *AdmissionInput) { in.QuotaInsufficient = true }, AdmissionQuotaInsufficient},
		{"cooling down", func(in *AdmissionInput) { in.CoolingDown = true }, AdmissionCoolingDown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := base
			tt.modify(&in)
			v := Admit(in)
			if v.Routable {
				t.Fatalf("Routable = true, want false")
			}
			if len(v.Reasons) != 1 || v.Reasons[0] != tt.reason {
				t.Fatalf("Reasons = %v, want [%q]", v.Reasons, tt.reason)
			}
		})
	}
}

func TestAdmit_ReasonsAreSortedDeduplicatedAndNonEmpty(t *testing.T) {
	in := AdmissionInput{
		State: models.CertObserved, Truth: models.TruthUnknown,
		IdentityResolved: false, ContextVerified: false, FundingKnown: false, HealthyAccount: false,
		QuotaExhausted: true, QuotaInsufficient: true, CoolingDown: true,
	}
	v := Admit(in)
	if v.Routable {
		t.Fatal("Routable = true, want false")
	}
	if len(v.Reasons) == 0 {
		t.Fatal("Reasons is empty on a non-routable verdict")
	}
	want := AdmissionReasons()
	if !reflect.DeepEqual(v.Reasons, want) {
		t.Fatalf("Reasons = %v, want %v (fixed order, no duplicates)", v.Reasons, want)
	}
	seen := make(map[AdmissionReason]bool)
	for _, r := range v.Reasons {
		if seen[r] {
			t.Fatalf("duplicate reason %q", r)
		}
		seen[r] = true
	}
}

// --- review drainer fakes ---

type fakeReviewQueue struct {
	store    *fakeCertStore
	ids      []string
	err      error
	gotLimit int
}

func (q *fakeReviewQueue) ListForReview(_ context.Context, limit int) ([]ReviewItem, error) {
	q.gotLimit = limit
	if q.err != nil {
		return nil, q.err
	}
	var items []ReviewItem
	for _, id := range q.ids {
		if len(items) >= limit {
			break
		}
		c, ok := q.store.certs[id]
		if !ok {
			continue
		}
		items = append(items, ReviewItem{OfferingOperationID: id, State: c.State, Truth: c.Truth})
	}
	return items, nil
}

// perIDErrorStore wraps a *fakeCertStore and injects a Load error for
// exactly one id, leaving every other id unaffected.
type perIDErrorStore struct {
	*fakeCertStore
	failID string
}

func (s *perIDErrorStore) Load(ctx context.Context, id string) (models.Certification, error) {
	if id == s.failID {
		return models.Certification{}, fmt.Errorf("perIDErrorStore: injected failure for %q", id)
	}
	return s.fakeCertStore.Load(ctx, id)
}

func TestReviewDrainer_AdvancesOnlyEligibleRows(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	store := newFakeCertStore(
		models.Certification{OfferingOperationID: "observed-1", State: models.CertObserved},
		models.Certification{OfferingOperationID: "suspended-1", State: models.CertSuspended, Truth: models.TruthUnsupported},
		models.Certification{OfferingOperationID: "expired-1", State: models.CertExpired, Truth: models.TruthSupported},
		models.Certification{OfferingOperationID: "certified-1", State: models.CertCertified, Truth: models.TruthSupported},
		models.Certification{OfferingOperationID: "probing-1", State: models.CertProbing, Truth: models.TruthUnknown},
	)
	auditor := &fakeAuditor{}
	driver, err := NewCertificationDriver(store, auditor, 3, clockAt(now))
	if err != nil {
		t.Fatalf("NewCertificationDriver error = %v", err)
	}

	ids := []string{"observed-1", "suspended-1", "expired-1", "certified-1", "probing-1"}
	queue := &fakeReviewQueue{store: store, ids: ids}
	drainer, err := NewReviewDrainer(queue, driver, 10, clockAt(now))
	if err != nil {
		t.Fatalf("NewReviewDrainer error = %v", err)
	}

	result, err := drainer.Drain(context.Background())
	if err != nil {
		t.Fatalf("Drain error = %v", err)
	}
	if result.Scanned != 5 || result.Advanced != 3 || result.Skipped != 2 || result.Failed != 0 {
		t.Fatalf("result = %+v, want Scanned=5 Advanced=3 Skipped=2 Failed=0", result)
	}

	loaded := make(map[string]bool)
	for _, id := range store.loadCalls {
		loaded[id] = true
	}
	for _, id := range []string{"certified-1", "probing-1"} {
		if loaded[id] {
			t.Fatalf("driver was invoked for %q, which must never be re-touched", id)
		}
	}
	for _, id := range []string{"observed-1", "suspended-1", "expired-1"} {
		if !loaded[id] {
			t.Fatalf("driver was never invoked for %q, which should have been advanced", id)
		}
	}

	if store.certs["observed-1"].State != models.CertProbing {
		t.Errorf("observed-1 state = %q, want probing", store.certs["observed-1"].State)
	}
	if store.certs["suspended-1"].State != models.CertProbing {
		t.Errorf("suspended-1 state = %q, want probing", store.certs["suspended-1"].State)
	}
	if store.certs["expired-1"].State != models.CertProbing {
		t.Errorf("expired-1 state = %q, want probing", store.certs["expired-1"].State)
	}
	if store.certs["certified-1"].State != models.CertCertified {
		t.Errorf("certified-1 state = %q, want certified (untouched)", store.certs["certified-1"].State)
	}
	if store.certs["probing-1"].State != models.CertProbing {
		t.Errorf("probing-1 state = %q, want probing (untouched)", store.certs["probing-1"].State)
	}
}

func TestReviewDrainer_IsIdempotent(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	store := newFakeCertStore(models.Certification{OfferingOperationID: "observed-1", State: models.CertObserved})
	auditor := &fakeAuditor{}
	driver, err := NewCertificationDriver(store, auditor, 3, clockAt(now))
	if err != nil {
		t.Fatalf("NewCertificationDriver error = %v", err)
	}
	queue := &fakeReviewQueue{store: store, ids: []string{"observed-1"}}
	drainer, err := NewReviewDrainer(queue, driver, 10, clockAt(now))
	if err != nil {
		t.Fatalf("NewReviewDrainer error = %v", err)
	}

	r1, err := drainer.Drain(context.Background())
	if err != nil {
		t.Fatalf("first Drain error = %v", err)
	}
	if r1.Advanced != 1 {
		t.Fatalf("first drain Advanced = %d, want 1", r1.Advanced)
	}

	r2, err := drainer.Drain(context.Background())
	if err != nil {
		t.Fatalf("second Drain error = %v", err)
	}
	if r2.Advanced != 0 || r2.Skipped != 1 {
		t.Fatalf("second drain = %+v, want Advanced=0 Skipped=1 (already in probing)", r2)
	}
}

func TestReviewDrainer_BoundedBatch(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	certs := make([]models.Certification, 0, 50)
	ids := make([]string, 0, 50)
	for i := 0; i < 50; i++ {
		id := fmt.Sprintf("oo-%d", i)
		ids = append(ids, id)
		certs = append(certs, models.Certification{OfferingOperationID: id, State: models.CertObserved})
	}
	store := newFakeCertStore(certs...)
	auditor := &fakeAuditor{}
	driver, err := NewCertificationDriver(store, auditor, 3, clockAt(now))
	if err != nil {
		t.Fatalf("NewCertificationDriver error = %v", err)
	}
	queue := &fakeReviewQueue{store: store, ids: ids}
	drainer, err := NewReviewDrainer(queue, driver, 10, clockAt(now))
	if err != nil {
		t.Fatalf("NewReviewDrainer error = %v", err)
	}

	result, err := drainer.Drain(context.Background())
	if err != nil {
		t.Fatalf("Drain error = %v", err)
	}
	if queue.gotLimit != 10 {
		t.Fatalf("ListForReview limit = %d, want 10 (batchSize)", queue.gotLimit)
	}
	if result.Scanned > 10 {
		t.Fatalf("Scanned = %d, want <= 10", result.Scanned)
	}
}

func TestReviewDrainer_OneItemFailureIsSkippedNotFatal(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	base := newFakeCertStore(
		models.Certification{OfferingOperationID: "a", State: models.CertObserved},
		models.Certification{OfferingOperationID: "b", State: models.CertObserved},
		models.Certification{OfferingOperationID: "c", State: models.CertObserved},
	)
	wrapped := &perIDErrorStore{fakeCertStore: base, failID: "b"}
	auditor := &fakeAuditor{}
	driver, err := NewCertificationDriver(wrapped, auditor, 3, clockAt(now))
	if err != nil {
		t.Fatalf("NewCertificationDriver error = %v", err)
	}
	queue := &fakeReviewQueue{store: base, ids: []string{"a", "b", "c"}}
	drainer, err := NewReviewDrainer(queue, driver, 10, clockAt(now))
	if err != nil {
		t.Fatalf("NewReviewDrainer error = %v", err)
	}

	result, err := drainer.Drain(context.Background())
	if err != nil {
		t.Fatalf("Drain error = %v", err)
	}
	if result.Failed != 1 {
		t.Fatalf("Failed = %d, want 1", result.Failed)
	}
	if result.Advanced != 2 {
		t.Fatalf("Advanced = %d, want 2", result.Advanced)
	}
	if base.certs["a"].State != models.CertProbing {
		t.Errorf("a state = %q, want probing", base.certs["a"].State)
	}
	if base.certs["c"].State != models.CertProbing {
		t.Errorf("c state = %q, want probing", base.certs["c"].State)
	}
	if base.certs["b"].State != models.CertObserved {
		t.Errorf("b state = %q, want unchanged observed after its failure", base.certs["b"].State)
	}
}

func TestNewReviewDrainer_RejectsInvalidConstruction(t *testing.T) {
	store := newFakeCertStore()
	auditor := &fakeAuditor{}
	driver, err := NewCertificationDriver(store, auditor, 3, nil)
	if err != nil {
		t.Fatalf("NewCertificationDriver error = %v", err)
	}
	queue := &fakeReviewQueue{store: store}

	if _, err := NewReviewDrainer(nil, driver, 10, nil); !errors.Is(err, ErrNilReviewDrainerDependency) {
		t.Errorf("nil queue: err = %v, want ErrNilReviewDrainerDependency", err)
	}
	if _, err := NewReviewDrainer(queue, nil, 10, nil); !errors.Is(err, ErrNilReviewDrainerDependency) {
		t.Errorf("nil driver: err = %v, want ErrNilReviewDrainerDependency", err)
	}
	if _, err := NewReviewDrainer(queue, driver, 0, nil); !errors.Is(err, ErrInvalidReviewBatchSize) {
		t.Errorf("zero batch size: err = %v, want ErrInvalidReviewBatchSize", err)
	}
	if _, err := NewReviewDrainer(queue, driver, -1, nil); !errors.Is(err, ErrInvalidReviewBatchSize) {
		t.Errorf("negative batch size: err = %v, want ErrInvalidReviewBatchSize", err)
	}
}
