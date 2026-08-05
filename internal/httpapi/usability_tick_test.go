package httpapi

// usability_tick_test.go pins usabilityTick.Run — the scheduler tick body that
// sweeps every opencode-zen account needing verification. One account's failure
// (a bad credential, a catalog hiccup) must never abort the sweep of the rest,
// and a lister failure is surfaced so the scheduler logs it.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
)

func TestUsabilityAccountEligible_RequiresConnectedAndHealthy(t *testing.T) {
	cases := []struct {
		name string
		a    domain.Account
		want bool
	}{
		{"connected healthy", domain.Account{ConnectionState: domain.ConnectionConnected, HealthState: domain.HealthHealthy}, true},
		{"connected degraded", domain.Account{ConnectionState: domain.ConnectionConnected, HealthState: domain.HealthDegraded}, false},
		{"connected expired", domain.Account{ConnectionState: domain.ConnectionConnected, HealthState: domain.HealthExpired}, false},
		{"stopped healthy", domain.Account{ConnectionState: domain.ConnectionStopped, HealthState: domain.HealthHealthy}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := usabilityAccountEligible(tc.a); got != tc.want {
				t.Fatalf("eligible = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestUsabilityTick_VerifiesEveryAccount(t *testing.T) {
	var verified []string
	tick := &usabilityTick{
		list: func(context.Context) ([]accountToVerify, error) {
			return []accountToVerify{
				{AccountID: "acct-1", CredentialID: "cred-1"},
				{AccountID: "acct-2", CredentialID: "cred-2"},
			}, nil
		},
		verify: func(_ context.Context, target accountToVerify) (usabilityRunSummary, error) {
			verified = append(verified, target.AccountID)
			return usabilityRunSummary{}, nil
		},
	}

	if err := tick.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(verified) != 2 || verified[0] != "acct-1" || verified[1] != "acct-2" {
		t.Fatalf("verified = %v, want [acct-1 acct-2]", verified)
	}
}

func TestUsabilityTick_OneAccountFailureDoesNotAbortTheSweep(t *testing.T) {
	var verified []string
	tick := &usabilityTick{
		list: func(context.Context) ([]accountToVerify, error) {
			return []accountToVerify{
				{AccountID: "acct-1", CredentialID: "cred-1"},
				{AccountID: "acct-2", CredentialID: "cred-2"},
				{AccountID: "acct-3", CredentialID: "cred-3"},
			}, nil
		},
		verify: func(_ context.Context, target accountToVerify) (usabilityRunSummary, error) {
			verified = append(verified, target.AccountID)
			if target.AccountID == "acct-2" {
				return usabilityRunSummary{}, errors.New("credential decrypt failed")
			}
			return usabilityRunSummary{}, nil
		},
	}

	if err := tick.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v, want nil (a per-account failure is not fatal)", err)
	}
	if len(verified) != 3 {
		t.Fatalf("verified %v, want all three attempted despite acct-2 failing", verified)
	}
}

// laneRendezvous is the concurrency proof for the per-provider fan-out: each
// lane announces itself and then waits for the OTHER lane to announce. Both
// waits can only complete if the two lanes really run at the same time; a
// sequential sweep leaves the second lane's channel closed until the first has
// already returned, so the first lane's wait falls through to the failsafe and
// its flag stays false.
type laneRendezvous struct {
	mu      sync.Mutex
	both    map[string]bool
	started map[string]chan struct{}
}

func newLaneRendezvous(providerIDs ...string) *laneRendezvous {
	r := &laneRendezvous{both: map[string]bool{}, started: map[string]chan struct{}{}}
	for _, id := range providerIDs {
		r.started[id] = make(chan struct{})
	}
	return r
}

// arrive announces providerID's lane and waits for every other lane to arrive.
func (r *laneRendezvous) arrive(providerID string) {
	close(r.started[providerID])
	for id, ch := range r.started {
		if id == providerID {
			continue
		}
		select {
		case <-ch:
		case <-time.After(5 * time.Second):
			// Failsafe only: without it a sequential Run would deadlock
			// instead of failing the assertion.
			return
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.both[providerID] = true
}

func (r *laneRendezvous) concurrent(providerID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.both[providerID]
}

func TestUsabilityTick_ProviderLanesRunConcurrently(t *testing.T) {
	rv := newLaneRendezvous("prov-a", "prov-b")

	var (
		mu        sync.Mutex
		deadlines = map[string]time.Time{}
		undated   []string
	)
	start := time.Now()

	tick := &usabilityTick{
		list: func(context.Context) ([]accountToVerify, error) {
			return []accountToVerify{
				{ProviderID: "prov-a", AccountID: "acct-a1", CredentialID: "cred-a1"},
				{ProviderID: "prov-b", AccountID: "acct-b1", CredentialID: "cred-b1"},
			}, nil
		},
		verify: func(ctx context.Context, target accountToVerify) (usabilityRunSummary, error) {
			if dl, ok := ctx.Deadline(); ok {
				mu.Lock()
				deadlines[target.ProviderID] = dl
				mu.Unlock()
			} else {
				mu.Lock()
				undated = append(undated, target.ProviderID)
				mu.Unlock()
			}
			rv.arrive(target.ProviderID)
			return usabilityRunSummary{}, nil
		},
	}

	if err := tick.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	end := time.Now()

	for _, id := range []string{"prov-a", "prov-b"} {
		if !rv.concurrent(id) {
			t.Fatalf("lane %s never observed the other lane running: the providers were swept sequentially", id)
		}
	}

	// The sweep budget lives in the LANE now, not in one closure around the
	// whole tick: every lane must therefore hand its accounts a context that
	// already carries its own usabilitySweepBudget deadline. Each lane sets
	// that deadline at some instant INSIDE Run, so the only exact claim is
	// that it lands one full budget after some point in [start, end].
	mu.Lock()
	defer mu.Unlock()
	if len(undated) > 0 {
		t.Fatalf("lanes %v ran with no deadline: the per-lane sweep budget is missing", undated)
	}
	for _, id := range []string{"prov-a", "prov-b"} {
		dl, ok := deadlines[id]
		if !ok {
			t.Fatalf("lane %s never ran", id)
		}
		if dl.Before(start.Add(usabilitySweepBudget)) || dl.After(end.Add(usabilitySweepBudget)) {
			t.Fatalf("lane %s deadline = %v, want one %v budget from an instant in [%v, %v]",
				id, dl, usabilitySweepBudget, start, end)
		}
	}
}

func TestUsabilityTick_OneProviderLaneFailureDoesNotAbortAnother(t *testing.T) {
	var (
		mu       sync.Mutex
		verified []string
	)
	tick := &usabilityTick{
		list: func(context.Context) ([]accountToVerify, error) {
			return []accountToVerify{
				{ProviderID: "prov-a", AccountID: "acct-a1", CredentialID: "cred-a1"},
				{ProviderID: "prov-b", AccountID: "acct-b1", CredentialID: "cred-b1"},
				{ProviderID: "prov-b", AccountID: "acct-b2", CredentialID: "cred-b2"},
			}, nil
		},
		verify: func(_ context.Context, target accountToVerify) (usabilityRunSummary, error) {
			mu.Lock()
			verified = append(verified, target.AccountID)
			mu.Unlock()
			if target.ProviderID == "prov-a" {
				return usabilityRunSummary{}, errors.New("prov-a credential decrypt failed")
			}
			return usabilityRunSummary{}, nil
		},
	}

	if err := tick.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v, want nil (a per-lane failure is not fatal)", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(verified) != 3 {
		t.Fatalf("verified %v, want all three attempted despite prov-a failing", verified)
	}
	// Accounts inside one lane stay SEQUENTIAL and in order.
	var laneB []string
	for _, id := range verified {
		if id == "acct-b1" || id == "acct-b2" {
			laneB = append(laneB, id)
		}
	}
	if len(laneB) != 2 || laneB[0] != "acct-b1" || laneB[1] != "acct-b2" {
		t.Fatalf("lane prov-b order = %v, want [acct-b1 acct-b2]", laneB)
	}
}

func TestUsabilityTick_ListerErrorSurfaces(t *testing.T) {
	wantErr := errors.New("account list failed")
	tick := &usabilityTick{
		list:   func(context.Context) ([]accountToVerify, error) { return nil, wantErr },
		verify: func(context.Context, accountToVerify) (usabilityRunSummary, error) { return usabilityRunSummary{}, nil },
	}
	if err := tick.Run(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("Run() error = %v, want the lister error", err)
	}
}
