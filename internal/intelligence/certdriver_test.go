package intelligence

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
)

// --- hand-written fakes ---

type fakeCertStore struct {
	certs     map[string]models.Certification
	loadErr   error
	casErr    error
	casCalls  []casCall
	loadCalls []string
}

type casCall struct {
	previous, next models.Certification
}

func newFakeCertStore(initial ...models.Certification) *fakeCertStore {
	s := &fakeCertStore{certs: make(map[string]models.Certification)}
	for _, c := range initial {
		s.certs[c.OfferingOperationID] = c
	}
	return s
}

func (s *fakeCertStore) Load(_ context.Context, id string) (models.Certification, error) {
	s.loadCalls = append(s.loadCalls, id)
	if s.loadErr != nil {
		return models.Certification{}, s.loadErr
	}
	c, ok := s.certs[id]
	if !ok {
		return models.Certification{}, fmt.Errorf("fakeCertStore: no certification for %q", id)
	}
	return c, nil
}

func (s *fakeCertStore) CompareAndSwap(_ context.Context, previous, next models.Certification) error {
	s.casCalls = append(s.casCalls, casCall{previous: previous, next: next})
	if s.casErr != nil {
		return s.casErr
	}
	stored, ok := s.certs[previous.OfferingOperationID]
	if !ok || stored.State != previous.State || stored.Version != previous.Version {
		return ErrCertificationConflict
	}
	s.certs[next.OfferingOperationID] = next
	return nil
}

type trapCertStore struct {
	t        *testing.T
	loadCert models.Certification
}

func (s *trapCertStore) Load(_ context.Context, _ string) (models.Certification, error) {
	return s.loadCert, nil
}

func (s *trapCertStore) CompareAndSwap(_ context.Context, _, _ models.Certification) error {
	s.t.Fatal("CompareAndSwap must not be called on a rejection path")
	return nil
}

type fakeAuditor struct {
	t       *testing.T
	trap    bool
	err     error
	records []CertificationAuditRecord
}

func (a *fakeAuditor) CertificationTransitioned(_ context.Context, rec CertificationAuditRecord) error {
	if a.trap {
		a.t.Fatal("auditor must not be called")
	}
	a.records = append(a.records, rec)
	return a.err
}

// callLog + ordered wrappers prove the accept-path call order
// (CompareAndSwap before the audit call).
type callLog struct {
	events []string
}

type orderedStore struct {
	*fakeCertStore
	log *callLog
}

func (s *orderedStore) CompareAndSwap(ctx context.Context, previous, next models.Certification) error {
	s.log.events = append(s.log.events, "cas")
	return s.fakeCertStore.CompareAndSwap(ctx, previous, next)
}

type orderedAuditor struct {
	*fakeAuditor
	log *callLog
}

func (a *orderedAuditor) CertificationTransitioned(ctx context.Context, rec CertificationAuditRecord) error {
	a.log.events = append(a.log.events, "audit")
	return a.fakeAuditor.CertificationTransitioned(ctx, rec)
}

func TestCertificationDriver_EachLegalTransition(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)

	newDriver := func(cert models.Certification) (*CertificationDriver, *fakeCertStore, *fakeAuditor) {
		store := newFakeCertStore(cert)
		auditor := &fakeAuditor{}
		d, err := NewCertificationDriver(store, auditor, 3, clockAt(now))
		if err != nil {
			t.Fatalf("NewCertificationDriver error = %v", err)
		}
		return d, store, auditor
	}

	t.Run("edge 1: discovered -> observed (out of this driver's scope, driven directly)", func(t *testing.T) {
		c := models.Certification{OfferingOperationID: "oo-1", State: models.CertDiscovered, Truth: models.TruthUnknown}
		next, err := c.Transition(models.CertObserved, models.TruthUnknown, models.RetryPolicy{}, now)
		if err != nil {
			t.Fatalf("Transition error = %v", err)
		}
		if next.State != models.CertObserved {
			t.Fatalf("state = %q, want observed", next.State)
		}
	})

	t.Run("edge 2: observed -> probing via StartProbe", func(t *testing.T) {
		log := &callLog{}
		base := newFakeCertStore(models.Certification{OfferingOperationID: "oo-2", State: models.CertObserved})
		store := &orderedStore{fakeCertStore: base, log: log}
		auditor := &orderedAuditor{fakeAuditor: &fakeAuditor{}, log: log}
		d, err := NewCertificationDriver(store, auditor, 3, clockAt(now))
		if err != nil {
			t.Fatalf("NewCertificationDriver error = %v", err)
		}

		got, err := d.StartProbe(context.Background(), "oo-2")
		if err != nil {
			t.Fatalf("StartProbe error = %v", err)
		}
		if got.State != models.CertProbing {
			t.Errorf("state = %q, want probing", got.State)
		}
		if len(base.casCalls) != 1 || base.casCalls[0].previous.State != models.CertObserved {
			t.Errorf("CAS previous = %+v, want State=observed", base.casCalls[0].previous)
		}
		if len(auditor.records) != 1 || auditor.records[0].Reason != AuditProbeStarted {
			t.Errorf("audit = %+v, want reason probe_started", auditor.records)
		}
		if !reflect.DeepEqual(log.events, []string{"cas", "audit"}) {
			t.Errorf("call order = %v, want [cas audit] (CAS must commit before the audit call)", log.events)
		}
	})

	t.Run("edge 3: probing -> probing via RecordAttempt (retryable)", func(t *testing.T) {
		d, store, auditor := newDriver(models.Certification{OfferingOperationID: "oo-3", State: models.CertProbing, Truth: models.TruthUnknown})
		outcome, err := ClassifyProbeSignal(SignalTimeout)
		if err != nil {
			t.Fatal(err)
		}
		got, err := d.RecordAttempt(context.Background(), "oo-3", outcome, 1)
		if err != nil {
			t.Fatalf("RecordAttempt error = %v", err)
		}
		if got.State != models.CertProbing {
			t.Errorf("state = %q, want probing", got.State)
		}
		if store.casCalls[len(store.casCalls)-1].previous.State != models.CertProbing {
			t.Errorf("CAS previous state = %q, want probing", store.casCalls[len(store.casCalls)-1].previous.State)
		}
		if auditor.records[len(auditor.records)-1].Reason != AuditProbeRetry {
			t.Errorf("audit reason = %q, want probe_retry", auditor.records[len(auditor.records)-1].Reason)
		}
	})

	t.Run("edge 4: probing -> certified via RecordAttempt (definitive)", func(t *testing.T) {
		d, store, auditor := newDriver(models.Certification{OfferingOperationID: "oo-4", State: models.CertProbing, Truth: models.TruthUnknown})
		outcome, err := ClassifyProbeSignal(SignalCapabilityResponse)
		if err != nil {
			t.Fatal(err)
		}
		got, err := d.RecordAttempt(context.Background(), "oo-4", outcome, 1)
		if err != nil {
			t.Fatalf("RecordAttempt error = %v", err)
		}
		if got.State != models.CertCertified || got.Truth != models.TruthSupported {
			t.Errorf("got state=%q truth=%q, want certified/supported", got.State, got.Truth)
		}
		if store.casCalls[len(store.casCalls)-1].previous.State != models.CertProbing {
			t.Errorf("CAS previous state = %q, want probing", store.casCalls[len(store.casCalls)-1].previous.State)
		}
		if auditor.records[len(auditor.records)-1].Reason != AuditVerdictRecorded {
			t.Errorf("audit reason = %q, want verdict_recorded", auditor.records[len(auditor.records)-1].Reason)
		}
	})

	t.Run("edge 5: probing -> suspended via RecordAttempt (terminal failure)", func(t *testing.T) {
		d, _, auditor := newDriver(models.Certification{OfferingOperationID: "oo-5", State: models.CertProbing, Truth: models.TruthUnknown})
		outcome, err := ClassifyProbeSignal(SignalUnauthorized)
		if err != nil {
			t.Fatal(err)
		}
		got, err := d.RecordAttempt(context.Background(), "oo-5", outcome, 1)
		if err != nil {
			t.Fatalf("RecordAttempt error = %v", err)
		}
		if got.State != models.CertSuspended {
			t.Errorf("state = %q, want suspended", got.State)
		}
		last := auditor.records[len(auditor.records)-1]
		if last.Reason != AuditSuspended || last.Suspension != SuspensionCredentialBlocked {
			t.Errorf("audit = %+v, want suspended/credential_blocked", last)
		}
	})

	t.Run("edge 6: certified -> suspended via Suspend", func(t *testing.T) {
		d, _, auditor := newDriver(models.Certification{OfferingOperationID: "oo-6", State: models.CertCertified, Truth: models.TruthSupported})
		got, err := d.Suspend(context.Background(), "oo-6", SuspensionQuotaExhausted)
		if err != nil {
			t.Fatalf("Suspend error = %v", err)
		}
		if got.State != models.CertSuspended {
			t.Errorf("state = %q, want suspended", got.State)
		}
		last := auditor.records[len(auditor.records)-1]
		if last.Reason != AuditSuspended || last.Suspension != SuspensionQuotaExhausted {
			t.Errorf("audit = %+v, want suspended/quota_exhausted", last)
		}
	})

	t.Run("edge 7: suspended -> certified via Resume", func(t *testing.T) {
		d, _, auditor := newDriver(models.Certification{OfferingOperationID: "oo-7", State: models.CertSuspended, Truth: models.TruthSupported})
		got, err := d.Resume(context.Background(), "oo-7")
		if err != nil {
			t.Fatalf("Resume error = %v", err)
		}
		if got.State != models.CertCertified || got.Truth != models.TruthSupported {
			t.Errorf("got = %+v, want certified/supported", got)
		}
		if auditor.records[len(auditor.records)-1].Reason != AuditResumed {
			t.Errorf("audit reason = %q, want resumed", auditor.records[len(auditor.records)-1].Reason)
		}
	})

	t.Run("edge 8: suspended -> probing via ReProbe", func(t *testing.T) {
		d, _, auditor := newDriver(models.Certification{OfferingOperationID: "oo-8", State: models.CertSuspended, Truth: models.TruthUnsupported})
		got, err := d.ReProbe(context.Background(), "oo-8")
		if err != nil {
			t.Fatalf("ReProbe error = %v", err)
		}
		if got.State != models.CertProbing || got.Truth != models.TruthUnknown {
			t.Errorf("got = %+v, want probing/unknown (truth reset)", got)
		}
		if auditor.records[len(auditor.records)-1].Reason != AuditReProbeScheduled {
			t.Errorf("audit reason = %q, want re_probe_scheduled", auditor.records[len(auditor.records)-1].Reason)
		}
	})

	t.Run("edge 9: certified -> expired via Expire", func(t *testing.T) {
		d, _, auditor := newDriver(models.Certification{OfferingOperationID: "oo-9", State: models.CertCertified, Truth: models.TruthSupported})
		got, err := d.Expire(context.Background(), "oo-9")
		if err != nil {
			t.Fatalf("Expire error = %v", err)
		}
		if got.State != models.CertExpired {
			t.Errorf("state = %q, want expired", got.State)
		}
		if auditor.records[len(auditor.records)-1].Reason != AuditExpired {
			t.Errorf("audit reason = %q, want expired", auditor.records[len(auditor.records)-1].Reason)
		}
	})

	t.Run("edge 10: expired -> probing via ReProbe", func(t *testing.T) {
		// Unlike edge 8 (suspended -> probing), the frozen Transition's
		// default branch for expired -> probing does NOT reset Truth — the
		// prior verdict carries forward as stale evidence (04 §5: "a prior
		// truth is stale evidence, §4"), not wiped to unknown.
		d, _, auditor := newDriver(models.Certification{OfferingOperationID: "oo-10", State: models.CertExpired, Truth: models.TruthSupported})
		got, err := d.ReProbe(context.Background(), "oo-10")
		if err != nil {
			t.Fatalf("ReProbe error = %v", err)
		}
		if got.State != models.CertProbing || got.Truth != models.TruthSupported {
			t.Errorf("got = %+v, want probing/supported (prior truth carried forward, not reset)", got)
		}
		if auditor.records[len(auditor.records)-1].Reason != AuditReProbeScheduled {
			t.Errorf("audit reason = %q, want re_probe_scheduled", auditor.records[len(auditor.records)-1].Reason)
		}
	})
}

func TestCertificationDriver_RecordAttemptDrivesFromOutcome(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		kind      ProbeSignalKind
		wantState models.CertificationState
		wantTruth models.CapabilityTruth
	}{
		{SignalCapabilityResponse, models.CertCertified, models.TruthSupported},
		{SignalSemanticRejection, models.CertCertified, models.TruthUnsupported},
		{SignalRateLimited, models.CertProbing, models.TruthUnknown},
		{SignalTimeout, models.CertProbing, models.TruthUnknown},
		{SignalServerError, models.CertProbing, models.TruthUnknown},
		{SignalNetworkError, models.CertProbing, models.TruthUnknown},
		{SignalUnauthorized, models.CertSuspended, models.TruthUnknown},
		{SignalForbidden, models.CertSuspended, models.TruthUnknown},
		{SignalMalformedRequest, models.CertProbing, models.TruthUnknown},
	}
	if len(tests) != len(probeSignalKindSet) {
		t.Fatalf("test table has %d rows, want %d (one per signal kind)", len(tests), len(probeSignalKindSet))
	}

	for _, tt := range tests {
		t.Run(string(tt.kind), func(t *testing.T) {
			id := "oo-" + string(tt.kind)
			store := newFakeCertStore(models.Certification{OfferingOperationID: id, State: models.CertProbing, Truth: models.TruthUnknown})
			auditor := &fakeAuditor{}
			d, err := NewCertificationDriver(store, auditor, 3, clockAt(now))
			if err != nil {
				t.Fatalf("NewCertificationDriver error = %v", err)
			}
			outcome, err := ClassifyProbeSignal(tt.kind)
			if err != nil {
				t.Fatal(err)
			}
			got, err := d.RecordAttempt(context.Background(), id, outcome, 1)
			if err != nil {
				t.Fatalf("RecordAttempt error = %v", err)
			}
			if got.State != tt.wantState || got.Truth != tt.wantTruth {
				t.Errorf("got state=%q truth=%q, want %q/%q", got.State, got.Truth, tt.wantState, tt.wantTruth)
			}
		})
	}
}

func TestCertificationDriver_RetryBudgetExhaustedSuspends(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	const budget = 3
	id := "oo-budget"
	store := newFakeCertStore(models.Certification{OfferingOperationID: id, State: models.CertProbing, Truth: models.TruthUnknown})
	auditor := &fakeAuditor{}
	d, err := NewCertificationDriver(store, auditor, budget, clockAt(now))
	if err != nil {
		t.Fatalf("NewCertificationDriver error = %v", err)
	}
	outcome, err := ClassifyProbeSignal(SignalTimeout)
	if err != nil {
		t.Fatal(err)
	}

	for attempt := 1; attempt <= budget; attempt++ {
		got, err := d.RecordAttempt(context.Background(), id, outcome, attempt)
		if err != nil {
			t.Fatalf("attempt %d: RecordAttempt error = %v", attempt, err)
		}
		if got.State != models.CertProbing {
			t.Fatalf("attempt %d: state = %q, want probing (within budget)", attempt, got.State)
		}
	}

	got, err := d.RecordAttempt(context.Background(), id, outcome, budget+1)
	if err != nil {
		t.Fatalf("RecordAttempt error = %v", err)
	}
	if got.State != models.CertSuspended {
		t.Fatalf("state = %q, want suspended", got.State)
	}
	if got.Truth != models.TruthUnknown {
		t.Fatalf("truth = %q, want unknown", got.Truth)
	}
	last := auditor.records[len(auditor.records)-1]
	if last.Suspension != SuspensionProbeRetryBudgetExhausted {
		t.Fatalf("suspension reason = %q, want probe_retry_budget_exhausted", last.Suspension)
	}
}

// TestCertificationDriver_NonDefinitiveOutcomeNeverWritesTruth pins the
// crown rule at planRecordAttempt — the point where it is actually
// observable. models.Certification.Transition ignores its verdict
// argument for every edge except probing -> certified, so asserting only
// on the eventual stored Certification (via CompareAndSwap) cannot
// distinguish "the driver planned TruthUnknown" from "the driver planned
// outcome.Truth but Transition silently discarded it" — both produce an
// identical CAS. Testing the plan directly is what makes a regression
// here observable at all.
func TestCertificationDriver_NonDefinitiveOutcomeNeverWritesTruth(t *testing.T) {
	var outcomes []ProbeOutcome
	for _, kind := range []ProbeSignalKind{
		SignalRateLimited, SignalTimeout, SignalServerError, SignalNetworkError,
		SignalUnauthorized, SignalForbidden, SignalMalformedRequest,
	} {
		o, err := ClassifyProbeSignal(kind)
		if err != nil {
			t.Fatal(err)
		}
		outcomes = append(outcomes, o)
	}
	// Adversarial: Definitive=false but Truth=supported — a malformed
	// outcome no real ClassifyProbeSignal call would produce, but the
	// plan must still never carry it, proving the code hardcodes
	// TruthUnknown rather than merely forwarding outcome.Truth.
	outcomes = append(outcomes, ProbeOutcome{Execution: ProbeRetryableFailure, Truth: models.TruthSupported, Definitive: false, Reschedule: true})

	for i, outcome := range outcomes {
		t.Run(fmt.Sprintf("row-%d-definitive-%v", i, outcome.Definitive), func(t *testing.T) {
			plan := planRecordAttempt(outcome)
			if plan.verdict != models.TruthUnknown {
				t.Fatalf("plan.verdict = %q, want unknown", plan.verdict)
			}
		})
	}
}

func TestCertificationDriver_IllegalTransitionRejectedAndAudited(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name  string
		state models.CertificationState
		call  func(d *CertificationDriver, id string) (models.Certification, error)
	}{
		{"discovered -> certified", models.CertDiscovered, func(d *CertificationDriver, id string) (models.Certification, error) {
			outcome, _ := ClassifyProbeSignal(SignalCapabilityResponse)
			return d.RecordAttempt(context.Background(), id, outcome, 1)
		}},
		{"observed -> certified", models.CertObserved, func(d *CertificationDriver, id string) (models.Certification, error) {
			outcome, _ := ClassifyProbeSignal(SignalCapabilityResponse)
			return d.RecordAttempt(context.Background(), id, outcome, 1)
		}},
		{"expired -> certified", models.CertExpired, func(d *CertificationDriver, id string) (models.Certification, error) {
			return d.Resume(context.Background(), id)
		}},
		{"certified -> probing", models.CertCertified, func(d *CertificationDriver, id string) (models.Certification, error) {
			return d.ReProbe(context.Background(), id)
		}},
		{"suspended -> expired", models.CertSuspended, func(d *CertificationDriver, id string) (models.Certification, error) {
			return d.Expire(context.Background(), id)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := "oo-illegal"
			cert := models.Certification{OfferingOperationID: id, State: tt.state, Truth: models.TruthUnknown}
			store := &trapCertStore{t: t, loadCert: cert}
			auditor := &fakeAuditor{}
			d, err := NewCertificationDriver(store, auditor, 3, clockAt(now))
			if err != nil {
				t.Fatalf("NewCertificationDriver error = %v", err)
			}

			got, err := tt.call(d, id)
			if !errors.Is(err, models.ErrIllegalCertificationTransition) {
				t.Fatalf("err = %v, want ErrIllegalCertificationTransition", err)
			}
			if got.State != tt.state {
				t.Fatalf("state = %q, want unchanged %q", got.State, tt.state)
			}
			if len(auditor.records) != 1 {
				t.Fatalf("audit records = %d, want exactly 1", len(auditor.records))
			}
			if auditor.records[0].Accepted {
				t.Fatalf("audit Accepted = true, want false")
			}
			if auditor.records[0].Reason != AuditIllegalTransition {
				t.Fatalf("audit reason = %q, want illegal_transition", auditor.records[0].Reason)
			}
		})
	}
}

func TestCertificationDriver_ConflictSurfacesUnchanged(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	id := "oo-conflict"
	store := newFakeCertStore(models.Certification{OfferingOperationID: id, State: models.CertObserved})
	store.casErr = ErrCertificationConflict
	auditor := &fakeAuditor{}
	d, err := NewCertificationDriver(store, auditor, 3, clockAt(now))
	if err != nil {
		t.Fatalf("NewCertificationDriver error = %v", err)
	}

	_, err = d.StartProbe(context.Background(), id)
	if !errors.Is(err, ErrCertificationConflict) {
		t.Fatalf("err = %v, want ErrCertificationConflict", err)
	}
	if len(store.casCalls) != 1 {
		t.Fatalf("CAS called %d times, want exactly 1 (no silent retry)", len(store.casCalls))
	}
}

func TestCertificationDriver_AuditIsSecretFree(t *testing.T) {
	rec := CertificationAuditRecord{
		OfferingOperationID: "oo-secret",
		From:                models.CertProbing,
		To:                  models.CertSuspended,
		Reason:              AuditSuspended,
	}
	typ := reflect.TypeOf(rec)
	forbidden := []string{"message", "snippet", "evidence", "content", "body", "text"}
	for i := 0; i < typ.NumField(); i++ {
		name := strings.ToLower(typ.Field(i).Name)
		for _, bad := range forbidden {
			if strings.Contains(name, bad) {
				t.Fatalf("CertificationAuditRecord has a forbidden field %q", typ.Field(i).Name)
			}
		}
	}
}

func TestNewCertificationDriver_RejectsInvalidConstruction(t *testing.T) {
	store := newFakeCertStore()
	auditor := &fakeAuditor{}

	if _, err := NewCertificationDriver(nil, auditor, 3, nil); !errors.Is(err, ErrNilCertificationDriverPort) {
		t.Errorf("nil store: err = %v, want ErrNilCertificationDriverPort", err)
	}
	if _, err := NewCertificationDriver(store, nil, 3, nil); !errors.Is(err, ErrNilCertificationDriverPort) {
		t.Errorf("nil auditor: err = %v, want ErrNilCertificationDriverPort", err)
	}
	if _, err := NewCertificationDriver(store, auditor, 0, nil); !errors.Is(err, ErrInvalidRetryBudget) {
		t.Errorf("zero budget: err = %v, want ErrInvalidRetryBudget", err)
	}
	if _, err := NewCertificationDriver(store, auditor, -1, nil); !errors.Is(err, ErrInvalidRetryBudget) {
		t.Errorf("negative budget: err = %v, want ErrInvalidRetryBudget", err)
	}
}
