package httpapi

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/application"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
)

func refreshTarget(accountID, credID string) tokenRefreshTarget {
	return tokenRefreshTarget{
		Account:      domain.Account{ID: accountID, ConnectionState: domain.ConnectionConnected},
		CredentialID: credID,
	}
}

// TestTokenRefreshTick_RefreshesEveryListedTarget proves the sweep visits
// every target and that one target's failure never aborts the rest.
func TestTokenRefreshTick_RefreshesEveryListedTarget(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	var refreshed []string
	tick := &tokenRefreshTick{
		list: func(context.Context) ([]tokenRefreshTarget, error) {
			return []tokenRefreshTarget{refreshTarget("a1", "c1"), refreshTarget("a2", "c2"), refreshTarget("a3", "c3")}, nil
		},
		refresh: func(_ context.Context, target tokenRefreshTarget) (application.TokenRefreshOutcome, error) {
			refreshed = append(refreshed, target.CredentialID)
			if target.CredentialID == "c2" {
				return application.TokenRefreshTransient, errors.New("provider down")
			}
			return application.TokenRefreshRotated, nil
		},
		now:            func() time.Time { return now },
		cooldown:       15 * time.Minute,
		retryNotBefore: make(map[string]time.Time),
	}

	if err := tick.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(refreshed) != 3 {
		t.Fatalf("refreshed %v, want all three targets", refreshed)
	}
}

// TestTokenRefreshTick_FailureCoolsDownOnlyThatCredential proves a failed
// credential is skipped until its cooldown elapses while healthy ones keep
// refreshing every tick, and that a later success clears the cooldown.
func TestTokenRefreshTick_FailureCoolsDownOnlyThatCredential(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	failing := true
	calls := map[string]int{}
	tick := &tokenRefreshTick{
		list: func(context.Context) ([]tokenRefreshTarget, error) {
			return []tokenRefreshTarget{refreshTarget("a1", "c-ok"), refreshTarget("a2", "c-bad")}, nil
		},
		refresh: func(_ context.Context, target tokenRefreshTarget) (application.TokenRefreshOutcome, error) {
			calls[target.CredentialID]++
			if target.CredentialID == "c-bad" && failing {
				return application.TokenRefreshTransient, errors.New("still down")
			}
			return application.TokenRefreshFresh, nil
		},
		now:            clock,
		cooldown:       15 * time.Minute,
		retryNotBefore: make(map[string]time.Time),
	}

	// Tick 1: both attempted; c-bad fails and enters cooldown.
	if err := tick.Run(context.Background()); err != nil {
		t.Fatalf("Run 1: %v", err)
	}
	// Tick 2 (30s later): c-ok attempted again, c-bad skipped.
	now = now.Add(30 * time.Second)
	if err := tick.Run(context.Background()); err != nil {
		t.Fatalf("Run 2: %v", err)
	}
	if calls["c-ok"] != 2 {
		t.Fatalf("c-ok calls = %d, want 2 (healthy credentials refresh every tick)", calls["c-ok"])
	}
	if calls["c-bad"] != 1 {
		t.Fatalf("c-bad calls = %d, want 1 (cooldown must skip it)", calls["c-bad"])
	}

	// Tick 3 (past the cooldown): c-bad retried; it now succeeds, so the
	// cooldown entry is cleared.
	now = now.Add(16 * time.Minute)
	failing = false
	if err := tick.Run(context.Background()); err != nil {
		t.Fatalf("Run 3: %v", err)
	}
	if calls["c-bad"] != 2 {
		t.Fatalf("c-bad calls = %d, want 2 (cooldown elapsed => retried)", calls["c-bad"])
	}
	if _, still := tick.retryNotBefore["c-bad"]; still {
		t.Fatalf("c-bad still in cooldown after a successful refresh")
	}
}

// TestTokenRefreshTick_ListFailureIsReturned proves a lister failure (the
// whole sweep impossible) surfaces to the scheduler.
func TestTokenRefreshTick_ListFailureIsReturned(t *testing.T) {
	tick := &tokenRefreshTick{
		list: func(context.Context) ([]tokenRefreshTarget, error) { return nil, errors.New("db locked") },
		refresh: func(context.Context, tokenRefreshTarget) (application.TokenRefreshOutcome, error) {
			t.Fatal("refresh must not run")
			return "", nil
		},
		now:            time.Now,
		cooldown:       time.Minute,
		retryNotBefore: make(map[string]time.Time),
	}
	if err := tick.Run(context.Background()); err == nil {
		t.Fatalf("Run returned nil, want the lister error")
	}
}

func TestTokenRefreshAccountEligible_RecoversOnlyLegacyAmbiguousClinePassExpiry(t *testing.T) {
	cases := []struct {
		name string
		a    domain.Account
		want bool
	}{
		{
			name: "healthy oauth account",
			a:    domain.Account{ProviderID: "codex", ConnectionState: domain.ConnectionConnected, HealthState: domain.HealthHealthy},
			want: true,
		},
		{
			name: "legacy false-expired clinepass account gets guarded recovery",
			a:    domain.Account{ProviderID: "clinepass", ConnectionState: domain.ConnectionConnected, HealthState: domain.HealthExpired, LastHealthError: legacyAmbiguousClinePassRefreshError},
			want: true,
		},
		{
			name: "definitively expired clinepass remains skipped",
			a:    domain.Account{ProviderID: "clinepass", ConnectionState: domain.ConnectionConnected, HealthState: domain.HealthExpired, LastHealthError: "refresh_token_expired"},
			want: false,
		},
		{
			name: "other expired oauth account remains skipped",
			a:    domain.Account{ProviderID: "codex", ConnectionState: domain.ConnectionConnected, HealthState: domain.HealthExpired, LastHealthError: legacyAmbiguousClinePassRefreshError},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tokenRefreshAccountEligible(tc.a); got != tc.want {
				t.Fatalf("eligible = %v, want %v", got, tc.want)
			}
		})
	}
}
