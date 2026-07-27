package intelligence

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
)

// --- hand-written fakes (no mocking library) ---

type fakeProbeReserver struct {
	t             *testing.T
	trap          bool
	reservationID string
	err           error
	calls         int
}

func (f *fakeProbeReserver) ReserveProbe(_ context.Context, _, _, _ string, _ []quota.Allocation) (string, error) {
	f.calls++
	if f.trap {
		f.t.Fatal("ReserveProbe must not be called on a refusal path")
	}
	if f.err != nil {
		return "", f.err
	}
	return f.reservationID, nil
}

type fakeSpendReader struct {
	allocations []quota.Allocation
	err         error
	gotSince    time.Time
}

func (f *fakeSpendReader) ProbeSpendSince(_ context.Context, _ string, since time.Time) ([]quota.Allocation, error) {
	f.gotSince = since
	if f.err != nil {
		return nil, f.err
	}
	return f.allocations, nil
}

type fakeInFlightReader struct {
	count int
	err   error
}

func (f *fakeInFlightReader) InFlightProbes(_ context.Context, _ string) (int, error) {
	if f.err != nil {
		return 0, f.err
	}
	return f.count, nil
}

type fakeCooldownReader struct {
	until *time.Time
	err   error
}

func (f *fakeCooldownReader) ProbeCooldownUntil(_ context.Context, _ string) (*time.Time, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.until, nil
}

func clockAt(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

func baseRequest() ProbeAdmissionRequest {
	return ProbeAdmissionRequest{
		AccountID:           "acct-1",
		ProviderID:          "prov-1",
		OfferingOperationID: "off-op-1",
		RequestID:           "req-1",
		AttemptID:           "att-1",
		Operation:           models.OperationTools,
		Class:               ProbeStandard,
		Cost:                quota.EstimateInput{InputTokens: intPtr(100), MaxOutputTokens: intPtr(50)},
	}
}

func TestProbeGuard_AdmitsWithinPolicy(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	reserver := &fakeProbeReserver{reservationID: "rsv-1"}
	guard, err := NewProbeGuard(DefaultProbeSafetyPolicy(), reserver, &fakeSpendReader{}, &fakeInFlightReader{}, &fakeCooldownReader{}, clockAt(now))
	if err != nil {
		t.Fatalf("NewProbeGuard error = %v", err)
	}

	req := baseRequest()
	got, err := guard.Admit(context.Background(), req)
	if err != nil {
		t.Fatalf("Admit error = %v", err)
	}
	if got.ReservationID != "rsv-1" {
		t.Errorf("ReservationID = %q, want %q", got.ReservationID, "rsv-1")
	}
	want, _ := quota.Estimate(req.Cost, quota.DefaultEstimatePolicy())
	if !reflect.DeepEqual(got.Allocations, want) {
		t.Errorf("Allocations = %+v, want %+v", got.Allocations, want)
	}
	if reserver.calls != 1 {
		t.Errorf("reserver called %d times, want 1", reserver.calls)
	}

	t.Run("equality at per-probe cap admits", func(t *testing.T) {
		policy := DefaultProbeSafetyPolicy()
		policy.PerProbe = []ProbeCostCap{
			{Unit: quota.UnitRequests, Max: 1},
			{Unit: quota.UnitConcurrency, Max: 1},
			{Unit: quota.UnitInputTokens, Max: 100},
			{Unit: quota.UnitOutputTokens, Max: 50},
			{Unit: quota.UnitCredits, Max: 1000},
			{Unit: quota.UnitBalance, Max: 1000},
		}
		r := &fakeProbeReserver{reservationID: "rsv-2"}
		g, err := NewProbeGuard(policy, r, &fakeSpendReader{}, &fakeInFlightReader{}, &fakeCooldownReader{}, clockAt(now))
		if err != nil {
			t.Fatalf("NewProbeGuard error = %v", err)
		}
		req := baseRequest()
		req.Cost = quota.EstimateInput{InputTokens: intPtr(100), MaxOutputTokens: intPtr(50)}
		if _, err := g.Admit(context.Background(), req); err != nil {
			t.Fatalf("Admit at exact cap boundary should succeed, got %v", err)
		}

		req.Cost = quota.EstimateInput{InputTokens: intPtr(101), MaxOutputTokens: intPtr(50)}
		_, err = g.Admit(context.Background(), req)
		if reason, ok := RefusalOf(err); !ok || reason != RefusalCapped {
			t.Fatalf("Admit one-over-cap: refusal = %v (ok=%v), want probe_capped", reason, ok)
		}
	})
}

func TestProbeGuard_GateOrderIsDeterministic(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	future := now.Add(time.Hour)

	tightCap := func(cap float64) ProbeSafetyPolicy {
		return ProbeSafetyPolicy{
			PerProbe: []ProbeCostCap{
				{Unit: quota.UnitRequests, Max: 1},
				{Unit: quota.UnitConcurrency, Max: 1},
				{Unit: quota.UnitInputTokens, Max: cap},
				{Unit: quota.UnitOutputTokens, Max: 1000},
				{Unit: quota.UnitCredits, Max: 1000},
				{Unit: quota.UnitBalance, Max: 1000},
			},
			PerAccount: []ProbeCostCap{
				{Unit: quota.UnitRequests, Max: 500},
				{Unit: quota.UnitConcurrency, Max: 500},
				{Unit: quota.UnitInputTokens, Max: 1_000_000},
				{Unit: quota.UnitOutputTokens, Max: 100_000},
				{Unit: quota.UnitCredits, Max: 5000},
				{Unit: quota.UnitBalance, Max: 5000},
			},
			PerAccountWindow:       24 * time.Hour,
			ExpensiveProbesEnabled: false,
			MaxInFlightPerProvider: 1,
			ContextProbeCooldown:   7 * 24 * time.Hour,
		}
	}

	req := ProbeAdmissionRequest{
		AccountID:           "acct-1",
		ProviderID:          "prov-1",
		OfferingOperationID: "off-op-1",
		RequestID:           "req-1",
		AttemptID:           "att-1",
		Operation:           models.OperationContextWindow,
		Class:               ProbeExpensive,
		Cost:                quota.EstimateInput{InputTokens: intPtr(100), MaxOutputTokens: intPtr(10)},
	}

	tests := []struct {
		name           string
		expensiveOK    bool
		cap            float64
		inFlight       int
		cooldownActive bool
		wantRefusal    ProbeRefusal
		wantAdmitted   bool
	}{
		{"violates opt-in + cap + concurrency + cooldown -> opt-in wins", false, 50, 1, true, RefusalOptInRequired, false},
		{"opt-in fixed, still violates cap + concurrency + cooldown -> cap wins", true, 50, 1, true, RefusalCapped, false},
		{"cap fixed, still violates concurrency + cooldown -> concurrency wins", true, 1000, 1, true, RefusalConcurrency, false},
		{"concurrency fixed, still violates cooldown -> cooldown wins", true, 1000, 0, true, RefusalCoolingDown, false},
		{"everything fixed -> admitted", true, 1000, 0, false, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy := tightCap(tt.cap)
			policy.ExpensiveProbesEnabled = tt.expensiveOK

			var until *time.Time
			if tt.cooldownActive {
				until = &future
			}

			reserver := &fakeProbeReserver{reservationID: "rsv"}
			guard, err := NewProbeGuard(policy, reserver, &fakeSpendReader{}, &fakeInFlightReader{count: tt.inFlight}, &fakeCooldownReader{until: until}, clockAt(now))
			if err != nil {
				t.Fatalf("NewProbeGuard error = %v", err)
			}

			_, err = guard.Admit(context.Background(), req)
			if tt.wantAdmitted {
				if err != nil {
					t.Fatalf("Admit error = %v, want success", err)
				}
				return
			}
			reason, ok := RefusalOf(err)
			if !ok {
				t.Fatalf("Admit error = %v, want a ProbeRefusedError", err)
			}
			if reason != tt.wantRefusal {
				t.Errorf("refusal = %q, want %q", reason, tt.wantRefusal)
			}
		})
	}
}

func TestProbeGuard_RefusalNeverReserves(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	future := now.Add(time.Hour)

	newGuard := func(t *testing.T, policy ProbeSafetyPolicy, spend *fakeSpendReader, inflight *fakeInFlightReader, cooldown *fakeCooldownReader) *ProbeGuard {
		trap := &fakeProbeReserver{t: t, trap: true}
		g, err := NewProbeGuard(policy, trap, spend, inflight, cooldown, clockAt(now))
		if err != nil {
			t.Fatalf("NewProbeGuard error = %v", err)
		}
		return g
	}

	t.Run("probe_opt_in_required", func(t *testing.T) {
		policy := DefaultProbeSafetyPolicy()
		policy.ExpensiveProbesEnabled = false
		g := newGuard(t, policy, &fakeSpendReader{}, &fakeInFlightReader{}, &fakeCooldownReader{})
		req := baseRequest()
		req.Class = ProbeExpensive
		_, err := g.Admit(context.Background(), req)
		if reason, ok := RefusalOf(err); !ok || reason != RefusalOptInRequired {
			t.Fatalf("refusal = %v (ok=%v), want probe_opt_in_required", reason, ok)
		}
	})

	t.Run("probe_capped", func(t *testing.T) {
		policy := DefaultProbeSafetyPolicy()
		policy.PerProbe = []ProbeCostCap{{Unit: quota.UnitRequests, Max: 1}, {Unit: quota.UnitConcurrency, Max: 1}, {Unit: quota.UnitInputTokens, Max: 1}, {Unit: quota.UnitOutputTokens, Max: 1000}}
		g := newGuard(t, policy, &fakeSpendReader{}, &fakeInFlightReader{}, &fakeCooldownReader{})
		_, err := g.Admit(context.Background(), baseRequest())
		if reason, ok := RefusalOf(err); !ok || reason != RefusalCapped {
			t.Fatalf("refusal = %v (ok=%v), want probe_capped", reason, ok)
		}
	})

	t.Run("probe_account_capped", func(t *testing.T) {
		policy := DefaultProbeSafetyPolicy()
		policy.PerAccount = []ProbeCostCap{{Unit: quota.UnitRequests, Max: 1}, {Unit: quota.UnitConcurrency, Max: 1}, {Unit: quota.UnitInputTokens, Max: 1}, {Unit: quota.UnitOutputTokens, Max: 1000}}
		g := newGuard(t, policy, &fakeSpendReader{}, &fakeInFlightReader{}, &fakeCooldownReader{})
		_, err := g.Admit(context.Background(), baseRequest())
		if reason, ok := RefusalOf(err); !ok || reason != RefusalAccountCapped {
			t.Fatalf("refusal = %v (ok=%v), want probe_account_capped", reason, ok)
		}
	})

	t.Run("probe_concurrency", func(t *testing.T) {
		policy := DefaultProbeSafetyPolicy()
		g := newGuard(t, policy, &fakeSpendReader{}, &fakeInFlightReader{count: 1}, &fakeCooldownReader{})
		_, err := g.Admit(context.Background(), baseRequest())
		if reason, ok := RefusalOf(err); !ok || reason != RefusalConcurrency {
			t.Fatalf("refusal = %v (ok=%v), want probe_concurrency", reason, ok)
		}
	})

	t.Run("probe_cooling_down", func(t *testing.T) {
		policy := DefaultProbeSafetyPolicy()
		g := newGuard(t, policy, &fakeSpendReader{}, &fakeInFlightReader{}, &fakeCooldownReader{until: &future})
		req := baseRequest()
		req.Operation = models.OperationContextWindow
		_, err := g.Admit(context.Background(), req)
		if reason, ok := RefusalOf(err); !ok || reason != RefusalCoolingDown {
			t.Fatalf("refusal = %v (ok=%v), want probe_cooling_down", reason, ok)
		}
	})

	t.Run("probe_safety_unavailable", func(t *testing.T) {
		policy := DefaultProbeSafetyPolicy()
		g := newGuard(t, policy, &fakeSpendReader{err: errors.New("boom")}, &fakeInFlightReader{}, &fakeCooldownReader{})
		_, err := g.Admit(context.Background(), baseRequest())
		if reason, ok := RefusalOf(err); !ok || reason != RefusalSafetyUnavailable {
			t.Fatalf("refusal = %v (ok=%v), want probe_safety_unavailable", reason, ok)
		}
	})
}

// TestProbeGuard_ReservationRejectionIsTyped pins gate 7, the last gate and
// the one 04 §2 / the P3c-QUOTA-001 card care about most: "every probe
// obtains a reservation before execution". A reservation failure must be a
// typed probe_quota_rejected refusal, never a silent admission — an Admit
// that returned success here would hand the caller an empty reservation id
// and let the probe reach the provider with nothing reserved.
func TestProbeGuard_ReservationRejectionIsTyped(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)

	t.Run("reserver error is a typed refusal", func(t *testing.T) {
		reserver := &fakeProbeReserver{err: errors.New("insufficient headroom")}
		g, err := NewProbeGuard(DefaultProbeSafetyPolicy(), reserver, &fakeSpendReader{}, &fakeInFlightReader{}, &fakeCooldownReader{}, clockAt(now))
		if err != nil {
			t.Fatalf("NewProbeGuard error = %v", err)
		}
		got, err := g.Admit(context.Background(), baseRequest())
		if !errors.Is(err, ErrProbeRefused) {
			t.Fatalf("err = %v, want it to wrap ErrProbeRefused", err)
		}
		if reason, ok := RefusalOf(err); !ok || reason != RefusalQuotaRejected {
			t.Fatalf("refusal = %v (ok=%v), want probe_quota_rejected", reason, ok)
		}
		if got.ReservationID != "" || len(got.Allocations) != 0 {
			t.Fatalf("admission = %+v, want the zero value on a refusal", got)
		}
	})

	t.Run("positive control: a succeeding reserver admits", func(t *testing.T) {
		reserver := &fakeProbeReserver{reservationID: "rsv-ok"}
		g, err := NewProbeGuard(DefaultProbeSafetyPolicy(), reserver, &fakeSpendReader{}, &fakeInFlightReader{}, &fakeCooldownReader{}, clockAt(now))
		if err != nil {
			t.Fatalf("NewProbeGuard error = %v", err)
		}
		got, err := g.Admit(context.Background(), baseRequest())
		if err != nil {
			t.Fatalf("Admit error = %v", err)
		}
		if got.ReservationID != "rsv-ok" {
			t.Fatalf("ReservationID = %q, want %q", got.ReservationID, "rsv-ok")
		}
	})
}

func TestProbeGuard_UncappedUnitIsRefused(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	policy := ProbeSafetyPolicy{
		PerProbe: []ProbeCostCap{
			{Unit: quota.UnitRequests, Max: 1},
			{Unit: quota.UnitConcurrency, Max: 1},
			{Unit: quota.UnitInputTokens, Max: 1000},
			{Unit: quota.UnitOutputTokens, Max: 1000},
			// UnitCredits deliberately omitted.
		},
		PerAccount: []ProbeCostCap{
			{Unit: quota.UnitRequests, Max: 500},
			{Unit: quota.UnitConcurrency, Max: 500},
			{Unit: quota.UnitInputTokens, Max: 1_000_000},
			{Unit: quota.UnitOutputTokens, Max: 100_000},
			{Unit: quota.UnitCredits, Max: 5000},
		},
		PerAccountWindow:       24 * time.Hour,
		MaxInFlightPerProvider: 1,
		ContextProbeCooldown:   7 * 24 * time.Hour,
	}

	req := baseRequest()
	req.Cost = quota.EstimateInput{
		InputTokens:     intPtr(100),
		MaxOutputTokens: intPtr(10),
		Conversion:      &quota.CreditConversionRule{Unit: quota.UnitCredits, CreditsPerToken: 0.5, Verified: true},
	}

	trap := &fakeProbeReserver{t: t, trap: true}
	g, err := NewProbeGuard(policy, trap, &fakeSpendReader{}, &fakeInFlightReader{}, &fakeCooldownReader{}, clockAt(now))
	if err != nil {
		t.Fatalf("NewProbeGuard error = %v", err)
	}
	_, err = g.Admit(context.Background(), req)
	if reason, ok := RefusalOf(err); !ok || reason != RefusalCapped {
		t.Fatalf("refusal = %v (ok=%v), want probe_capped", reason, ok)
	}

	// Positive control: adding the missing cap admits.
	policy.PerProbe = append(policy.PerProbe, ProbeCostCap{Unit: quota.UnitCredits, Max: 1000})
	reserver := &fakeProbeReserver{reservationID: "rsv"}
	g2, err := NewProbeGuard(policy, reserver, &fakeSpendReader{}, &fakeInFlightReader{}, &fakeCooldownReader{}, clockAt(now))
	if err != nil {
		t.Fatalf("NewProbeGuard error = %v", err)
	}
	if _, err := g2.Admit(context.Background(), req); err != nil {
		t.Fatalf("Admit after adding the missing cap should succeed, got %v", err)
	}
}

func TestProbeGuard_PerAccountWindowAggregates(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	policy := DefaultProbeSafetyPolicy()
	policy.PerAccount = []ProbeCostCap{
		{Unit: quota.UnitRequests, Max: 500},
		{Unit: quota.UnitConcurrency, Max: 500},
		{Unit: quota.UnitInputTokens, Max: 150},
		{Unit: quota.UnitOutputTokens, Max: 100_000},
		{Unit: quota.UnitCredits, Max: 5000},
		{Unit: quota.UnitBalance, Max: 5000},
	}
	policy.PerAccountWindow = 24 * time.Hour

	req := baseRequest()
	req.Cost = quota.EstimateInput{InputTokens: intPtr(100), MaxOutputTokens: intPtr(10)}

	t.Run("prior spend plus this probe crosses cap -> refused", func(t *testing.T) {
		spend := &fakeSpendReader{allocations: []quota.Allocation{{Unit: quota.UnitInputTokens, Cost: 51}}}
		trap := &fakeProbeReserver{t: t, trap: true}
		g, err := NewProbeGuard(policy, trap, spend, &fakeInFlightReader{}, &fakeCooldownReader{}, clockAt(now))
		if err != nil {
			t.Fatalf("NewProbeGuard error = %v", err)
		}
		_, err = g.Admit(context.Background(), req)
		if reason, ok := RefusalOf(err); !ok || reason != RefusalAccountCapped {
			t.Fatalf("refusal = %v (ok=%v), want probe_account_capped", reason, ok)
		}
		wantSince := now.Add(-policy.PerAccountWindow)
		if !spend.gotSince.Equal(wantSince) {
			t.Errorf("ProbeSpendSince got since=%v, want %v", spend.gotSince, wantSince)
		}
	})

	t.Run("exactly at cap admits", func(t *testing.T) {
		spend := &fakeSpendReader{allocations: []quota.Allocation{{Unit: quota.UnitInputTokens, Cost: 50}}}
		reserver := &fakeProbeReserver{reservationID: "rsv"}
		g, err := NewProbeGuard(policy, reserver, spend, &fakeInFlightReader{}, &fakeCooldownReader{}, clockAt(now))
		if err != nil {
			t.Fatalf("NewProbeGuard error = %v", err)
		}
		if _, err := g.Admit(context.Background(), req); err != nil {
			t.Fatalf("Admit at exact per-account boundary should succeed, got %v", err)
		}
	})

	t.Run("since is derived from the injected clock and window", func(t *testing.T) {
		spend := &fakeSpendReader{}
		reserver := &fakeProbeReserver{reservationID: "rsv"}
		g, err := NewProbeGuard(policy, reserver, spend, &fakeInFlightReader{}, &fakeCooldownReader{}, clockAt(now))
		if err != nil {
			t.Fatalf("NewProbeGuard error = %v", err)
		}
		if _, err := g.Admit(context.Background(), req); err != nil {
			t.Fatalf("Admit error = %v", err)
		}
		wantSince := now.Add(-policy.PerAccountWindow)
		if !spend.gotSince.Equal(wantSince) {
			t.Errorf("ProbeSpendSince got since=%v, want %v (spend older than the window must be excluded by the storage adapter using this cutoff)", spend.gotSince, wantSince)
		}
	})
}

func TestProbeGuard_ContextCooldownOnlyAppliesToContextWindow(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	future := now.Add(time.Hour)
	policy := DefaultProbeSafetyPolicy()

	t.Run("active cooldown refuses a context_window probe", func(t *testing.T) {
		expensivePolicy := policy
		expensivePolicy.ExpensiveProbesEnabled = true
		trap := &fakeProbeReserver{t: t, trap: true}
		g, err := NewProbeGuard(expensivePolicy, trap, &fakeSpendReader{}, &fakeInFlightReader{}, &fakeCooldownReader{until: &future}, clockAt(now))
		if err != nil {
			t.Fatalf("NewProbeGuard error = %v", err)
		}
		req := baseRequest()
		req.Operation = models.OperationContextWindow
		req.Class = ProbeExpensive
		_, err = g.Admit(context.Background(), req)
		if reason, ok := RefusalOf(err); !ok || reason != RefusalCoolingDown {
			t.Fatalf("refusal = %v (ok=%v), want probe_cooling_down", reason, ok)
		}
	})

	t.Run("same active cooldown does not refuse a tools probe", func(t *testing.T) {
		reserver := &fakeProbeReserver{reservationID: "rsv"}
		g, err := NewProbeGuard(policy, reserver, &fakeSpendReader{}, &fakeInFlightReader{}, &fakeCooldownReader{until: &future}, clockAt(now))
		if err != nil {
			t.Fatalf("NewProbeGuard error = %v", err)
		}
		req := baseRequest()
		req.Operation = models.OperationTools
		if _, err := g.Admit(context.Background(), req); err != nil {
			t.Fatalf("tools probe should not be gated by the context cooldown, got %v", err)
		}
	})

	t.Run("now == until admits", func(t *testing.T) {
		until := now
		reserver := &fakeProbeReserver{reservationID: "rsv"}
		g, err := NewProbeGuard(policy, reserver, &fakeSpendReader{}, &fakeInFlightReader{}, &fakeCooldownReader{until: &until}, clockAt(now))
		if err != nil {
			t.Fatalf("NewProbeGuard error = %v", err)
		}
		req := baseRequest()
		req.Operation = models.OperationContextWindow
		if _, err := g.Admit(context.Background(), req); err != nil {
			t.Fatalf("now==until should admit, got %v", err)
		}
	})
}

func TestProbeGuard_PortErrorFailsClosed(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	policy := DefaultProbeSafetyPolicy()

	t.Run("spend reader error", func(t *testing.T) {
		trap := &fakeProbeReserver{t: t, trap: true}
		g, err := NewProbeGuard(policy, trap, &fakeSpendReader{err: errors.New("x")}, &fakeInFlightReader{}, &fakeCooldownReader{}, clockAt(now))
		if err != nil {
			t.Fatalf("NewProbeGuard error = %v", err)
		}
		_, err = g.Admit(context.Background(), baseRequest())
		if reason, ok := RefusalOf(err); !ok || reason != RefusalSafetyUnavailable {
			t.Fatalf("refusal = %v (ok=%v), want probe_safety_unavailable", reason, ok)
		}
	})

	t.Run("in-flight reader error", func(t *testing.T) {
		trap := &fakeProbeReserver{t: t, trap: true}
		g, err := NewProbeGuard(policy, trap, &fakeSpendReader{}, &fakeInFlightReader{err: errors.New("x")}, &fakeCooldownReader{}, clockAt(now))
		if err != nil {
			t.Fatalf("NewProbeGuard error = %v", err)
		}
		_, err = g.Admit(context.Background(), baseRequest())
		if reason, ok := RefusalOf(err); !ok || reason != RefusalSafetyUnavailable {
			t.Fatalf("refusal = %v (ok=%v), want probe_safety_unavailable", reason, ok)
		}
	})

	t.Run("cooldown reader error", func(t *testing.T) {
		trap := &fakeProbeReserver{t: t, trap: true}
		g, err := NewProbeGuard(policy, trap, &fakeSpendReader{}, &fakeInFlightReader{}, &fakeCooldownReader{err: errors.New("x")}, clockAt(now))
		if err != nil {
			t.Fatalf("NewProbeGuard error = %v", err)
		}
		req := baseRequest()
		req.Operation = models.OperationContextWindow
		_, err = g.Admit(context.Background(), req)
		if reason, ok := RefusalOf(err); !ok || reason != RefusalSafetyUnavailable {
			t.Fatalf("refusal = %v (ok=%v), want probe_safety_unavailable", reason, ok)
		}
	})
}

func TestProbeGuard_ExpensiveProbeRequiresOptIn(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)

	t.Run("disabled refuses", func(t *testing.T) {
		policy := DefaultProbeSafetyPolicy()
		policy.ExpensiveProbesEnabled = false
		trap := &fakeProbeReserver{t: t, trap: true}
		g, err := NewProbeGuard(policy, trap, &fakeSpendReader{}, &fakeInFlightReader{}, &fakeCooldownReader{}, clockAt(now))
		if err != nil {
			t.Fatalf("NewProbeGuard error = %v", err)
		}
		req := baseRequest()
		req.Class = ProbeExpensive
		_, err = g.Admit(context.Background(), req)
		if reason, ok := RefusalOf(err); !ok || reason != RefusalOptInRequired {
			t.Fatalf("refusal = %v (ok=%v), want probe_opt_in_required", reason, ok)
		}
	})

	t.Run("enabled admits", func(t *testing.T) {
		policy := DefaultProbeSafetyPolicy()
		policy.ExpensiveProbesEnabled = true
		reserver := &fakeProbeReserver{reservationID: "rsv"}
		g, err := NewProbeGuard(policy, reserver, &fakeSpendReader{}, &fakeInFlightReader{}, &fakeCooldownReader{}, clockAt(now))
		if err != nil {
			t.Fatalf("NewProbeGuard error = %v", err)
		}
		req := baseRequest()
		req.Class = ProbeExpensive
		if _, err := g.Admit(context.Background(), req); err != nil {
			t.Fatalf("Admit error = %v, want success", err)
		}
	})
}

func TestNewProbeGuard_RejectsInvalidConstruction(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	validPolicy := DefaultProbeSafetyPolicy()
	reserver := &fakeProbeReserver{}
	spend := &fakeSpendReader{}
	inflight := &fakeInFlightReader{}
	cooldown := &fakeCooldownReader{}

	cases := []struct {
		name     string
		policy   ProbeSafetyPolicy
		reserver ProbeReserver
		spend    ProbeSpendReader
		inflight ProbeInFlightReader
		cooldown ProbeCooldownReader
	}{
		{"nil reserver", validPolicy, nil, spend, inflight, cooldown},
		{"nil spend", validPolicy, reserver, nil, inflight, cooldown},
		{"nil inflight", validPolicy, reserver, spend, nil, cooldown},
		{"nil cooldown", validPolicy, reserver, spend, inflight, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewProbeGuard(tc.policy, tc.reserver, tc.spend, tc.inflight, tc.cooldown, clockAt(now))
			if !errors.Is(err, ErrNilProbeGuardPort) {
				t.Fatalf("err = %v, want ErrNilProbeGuardPort", err)
			}
		})
	}

	t.Run("MaxInFlightPerProvider zero", func(t *testing.T) {
		p := validPolicy
		p.MaxInFlightPerProvider = 0
		_, err := NewProbeGuard(p, reserver, spend, inflight, cooldown, clockAt(now))
		if !errors.Is(err, ErrInvalidProbeSafetyPolicy) {
			t.Fatalf("err = %v, want ErrInvalidProbeSafetyPolicy", err)
		}
	})

	t.Run("negative cooldown", func(t *testing.T) {
		p := validPolicy
		p.ContextProbeCooldown = -time.Second
		_, err := NewProbeGuard(p, reserver, spend, inflight, cooldown, clockAt(now))
		if !errors.Is(err, ErrInvalidProbeSafetyPolicy) {
			t.Fatalf("err = %v, want ErrInvalidProbeSafetyPolicy", err)
		}
	})

	t.Run("negative per-account window", func(t *testing.T) {
		p := validPolicy
		p.PerAccountWindow = -time.Second
		_, err := NewProbeGuard(p, reserver, spend, inflight, cooldown, clockAt(now))
		if !errors.Is(err, ErrInvalidProbeSafetyPolicy) {
			t.Fatalf("err = %v, want ErrInvalidProbeSafetyPolicy", err)
		}
	})

	t.Run("negative per-probe cap Max", func(t *testing.T) {
		p := validPolicy
		p.PerProbe = []ProbeCostCap{{Unit: quota.UnitRequests, Max: -1}}
		_, err := NewProbeGuard(p, reserver, spend, inflight, cooldown, clockAt(now))
		if !errors.Is(err, ErrInvalidProbeSafetyPolicy) {
			t.Fatalf("err = %v, want ErrInvalidProbeSafetyPolicy", err)
		}
	})

	t.Run("negative per-account cap Max", func(t *testing.T) {
		p := validPolicy
		p.PerAccount = []ProbeCostCap{{Unit: quota.UnitRequests, Max: -1}}
		_, err := NewProbeGuard(p, reserver, spend, inflight, cooldown, clockAt(now))
		if !errors.Is(err, ErrInvalidProbeSafetyPolicy) {
			t.Fatalf("err = %v, want ErrInvalidProbeSafetyPolicy", err)
		}
	})
}

func TestDefaultProbeSafetyPolicy_CoversEveryEstimableUnit(t *testing.T) {
	inputTokens := 100
	maxOutput := 50

	creditsAlloc, err := quota.Estimate(quota.EstimateInput{
		InputTokens:     &inputTokens,
		MaxOutputTokens: &maxOutput,
		Conversion:      &quota.CreditConversionRule{Unit: quota.UnitCredits, CreditsPerToken: 0.01, Verified: true},
	}, quota.DefaultEstimatePolicy())
	if err != nil {
		t.Fatalf("quota.Estimate (credits) error = %v", err)
	}
	balanceAlloc, err := quota.Estimate(quota.EstimateInput{
		InputTokens:     &inputTokens,
		MaxOutputTokens: &maxOutput,
		Conversion:      &quota.CreditConversionRule{Unit: quota.UnitBalance, CreditsPerToken: 0.01, Verified: true},
	}, quota.DefaultEstimatePolicy())
	if err != nil {
		t.Fatalf("quota.Estimate (balance) error = %v", err)
	}

	units := make(map[quota.Unit]bool)
	for _, a := range creditsAlloc {
		units[a.Unit] = true
	}
	for _, a := range balanceAlloc {
		units[a.Unit] = true
	}
	if len(units) == 0 {
		t.Fatal("derived unit set is empty — test fixture is broken")
	}

	policy := DefaultProbeSafetyPolicy()
	perProbe := make(map[quota.Unit]bool, len(policy.PerProbe))
	for _, c := range policy.PerProbe {
		perProbe[c.Unit] = true
	}
	perAccount := make(map[quota.Unit]bool, len(policy.PerAccount))
	for _, c := range policy.PerAccount {
		perAccount[c.Unit] = true
	}

	for u := range units {
		if !perProbe[u] {
			t.Errorf("DefaultProbeSafetyPolicy.PerProbe is missing a cap for estimable unit %q", u)
		}
		if !perAccount[u] {
			t.Errorf("DefaultProbeSafetyPolicy.PerAccount is missing a cap for estimable unit %q", u)
		}
	}
}
