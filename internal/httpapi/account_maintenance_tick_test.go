package httpapi

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
)

func maintenanceTarget(accountID string, health domain.HealthState, lastCheck *time.Time) accountMaintenanceTarget {
	return accountMaintenanceTarget{
		Account: domain.Account{
			ID:                accountID,
			ConnectionState:   domain.ConnectionConnected,
			HealthState:       health,
			LastHealthCheckAt: lastCheck,
		},
		CredentialID: accountID + "-cred",
	}
}

// TestAccountMaintenanceTick_PurgesInactiveModelsAfterEverySweep catches the
// catalog cleanup being skipped when the only account is expired. Expired
// accounts intentionally skip provider calls, but their model rows must still
// disappear automatically.
func TestAccountMaintenanceTick_PurgesInactiveModelsAfterEverySweep(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	purged := 0
	tick := &accountMaintenanceTick{
		list: func(context.Context) ([]accountMaintenanceTarget, error) {
			return []accountMaintenanceTarget{maintenanceTarget("expired", domain.HealthExpired, nil)}, nil
		},
		probeHealth: func(context.Context, accountMaintenanceTarget) (domain.HealthState, error) {
			return domain.HealthExpired, nil
		},
		syncQuota: func(context.Context, accountMaintenanceTarget) error { return nil },
		purgeInactiveModels: func(context.Context) error {
			purged++
			return nil
		},
		now:             func() time.Time { return now },
		lastQuotaAt:     make(map[string]time.Time),
		lastDiscoveryAt: make(map[string]time.Time),
	}

	if err := tick.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if purged != 1 {
		t.Fatalf("purge calls = %d, want exactly 1 after the sweep", purged)
	}
}

// TestAccountMaintenanceTick_DiscoversRecoveredAndPeriodicallyHealthyAccounts
// catches a purged catalog never coming back. Recovery discovers immediately;
// an already-healthy account is refreshed on the bounded discovery cadence,
// never every 30-second scheduler tick.
func TestAccountMaintenanceTick_DiscoversRecoveredAndPeriodicallyHealthyAccounts(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	staleCheck := now.Add(-6 * time.Minute)
	discovered := map[string]int{}
	tick := &accountMaintenanceTick{
		list: func(context.Context) ([]accountMaintenanceTarget, error) {
			return []accountMaintenanceTarget{
				maintenanceTarget("recovering", domain.HealthDegraded, &staleCheck),
				maintenanceTarget("healthy", domain.HealthHealthy, &staleCheck),
			}, nil
		},
		probeHealth: func(_ context.Context, target accountMaintenanceTarget) (domain.HealthState, error) {
			return domain.HealthHealthy, nil
		},
		discoverModels: func(_ context.Context, target accountMaintenanceTarget) error {
			discovered[target.Account.ID]++
			return nil
		},
		syncQuota:           func(context.Context, accountMaintenanceTarget) error { return nil },
		purgeInactiveModels: func(context.Context) error { return nil },
		now:                 func() time.Time { return now },
		lastQuotaAt:         make(map[string]time.Time),
		lastDiscoveryAt:     make(map[string]time.Time),
	}

	if err := tick.Run(context.Background()); err != nil {
		t.Fatalf("Run 1: %v", err)
	}
	if discovered["recovering"] != 1 || discovered["healthy"] != 1 {
		t.Fatalf("discovered after first sweep = %v, want recovering+healthy once", discovered)
	}

	now = now.Add(30 * time.Second)
	if err := tick.Run(context.Background()); err != nil {
		t.Fatalf("Run 2: %v", err)
	}
	if discovered["recovering"] != 1 || discovered["healthy"] != 1 {
		t.Fatalf("discovered after 30s = %v, want cadence throttle", discovered)
	}

	now = now.Add(16 * time.Minute)
	if err := tick.Run(context.Background()); err != nil {
		t.Fatalf("Run 3: %v", err)
	}
	if discovered["recovering"] != 2 || discovered["healthy"] != 2 {
		t.Fatalf("discovered after 16m = %v, want both refreshed again", discovered)
	}
}

// TestAccountMaintenanceTick_HealthThrottledByPersistedTimestamp proves the
// health probe runs for never-checked and stale accounts but not for one
// checked moments ago — the persisted last_health_check_at is the clock.
func TestAccountMaintenanceTick_HealthThrottledByPersistedTimestamp(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	fresh := now.Add(-time.Minute)
	stale := now.Add(-6 * time.Minute)

	probed := map[string]int{}
	tick := &accountMaintenanceTick{
		list: func(context.Context) ([]accountMaintenanceTarget, error) {
			return []accountMaintenanceTarget{
				maintenanceTarget("never", domain.HealthUnknown, nil),
				maintenanceTarget("stale", domain.HealthHealthy, &stale),
				maintenanceTarget("fresh", domain.HealthHealthy, &fresh),
				maintenanceTarget("expired", domain.HealthExpired, &stale),
			}, nil
		},
		probeHealth: func(_ context.Context, target accountMaintenanceTarget) (domain.HealthState, error) {
			probed[target.Account.ID]++
			return target.Account.HealthState, nil
		},
		syncQuota:   func(context.Context, accountMaintenanceTarget) error { return nil },
		now:         func() time.Time { return now },
		lastQuotaAt: make(map[string]time.Time),
	}

	if err := tick.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if probed["never"] != 1 || probed["stale"] != 1 {
		t.Fatalf("probed = %v, want never+stale probed once each", probed)
	}
	if probed["fresh"] != 0 {
		t.Fatalf("probed = %v, want fresh skipped (checked 1m ago < 5m interval)", probed)
	}
	if probed["expired"] != 0 {
		t.Fatalf("probed = %v, want expired skipped so definitive reauth evidence is preserved", probed)
	}
}

// TestAccountMaintenanceTick_QuotaEvery15MinutesWithFailureCooldown proves
// quota syncs at most once per 15 minutes per account, that the stamp is
// written BEFORE the attempt (a failing provider is retried on the interval,
// never every 30s tick), and that an expired account is never quota-synced.
func TestAccountMaintenanceTick_QuotaEvery15MinutesWithFailureCooldown(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	recent := now.Add(-time.Minute)

	synced := map[string]int{}
	reconciled := map[string]int{}
	tick := &accountMaintenanceTick{
		list: func(context.Context) ([]accountMaintenanceTarget, error) {
			return []accountMaintenanceTarget{
				maintenanceTarget("ok", domain.HealthHealthy, &recent),
				maintenanceTarget("failing", domain.HealthHealthy, &recent),
				maintenanceTarget("dead", domain.HealthExpired, &recent),
			}, nil
		},
		probeHealth: func(_ context.Context, target accountMaintenanceTarget) (domain.HealthState, error) {
			return target.Account.HealthState, nil
		},
		syncQuota: func(_ context.Context, target accountMaintenanceTarget) error {
			synced[target.Account.ID]++
			if target.Account.ID == "failing" {
				return errors.New("provider 500")
			}
			return nil
		},
		reconcileFunding: func(_ context.Context, target accountMaintenanceTarget) error {
			reconciled[target.Account.ID]++
			return nil
		},
		now:         clock,
		lastQuotaAt: make(map[string]time.Time),
	}

	// Tick 1: ok + failing sync (first sight), dead never does.
	if err := tick.Run(context.Background()); err != nil {
		t.Fatalf("Run 1: %v", err)
	}
	// Tick 2, 30s later: nobody is due.
	now = now.Add(30 * time.Second)
	if err := tick.Run(context.Background()); err != nil {
		t.Fatalf("Run 2: %v", err)
	}
	if synced["ok"] != 1 || synced["failing"] != 1 {
		t.Fatalf("synced after 30s = %v, want both at 1 (15m throttle)", synced)
	}
	if synced["dead"] != 0 {
		t.Fatalf("synced = %v, want expired account never quota-synced", synced)
	}
	if reconciled["dead"] != 1 {
		t.Fatalf("reconciled = %v, want expired account's local funding policy healed once", reconciled)
	}

	// Tick 3, past 15 minutes: both due again (the failure cooled down
	// exactly one interval, not forever and not every tick).
	now = now.Add(16 * time.Minute)
	if err := tick.Run(context.Background()); err != nil {
		t.Fatalf("Run 3: %v", err)
	}
	if synced["ok"] != 2 || synced["failing"] != 2 {
		t.Fatalf("synced after 16m = %v, want both at 2", synced)
	}
}

// fakeFundingRepo is an in-memory fundingReconcileRepo.
type fakeFundingRepo struct {
	current    *domain.FundingEvidence
	appended   []domain.FundingEvidence
	superseded []*domain.FundingEvidence
}

func (f *fakeFundingRepo) CurrentForAccount(context.Context, string) (domain.FundingEvidence, bool, error) {
	if f.current == nil {
		return domain.FundingEvidence{}, false, nil
	}
	return *f.current, true, nil
}

func (f *fakeFundingRepo) AppendSupersession(_ context.Context, superseded *domain.FundingEvidence, newCurrent domain.FundingEvidence, _ time.Time) error {
	f.superseded = append(f.superseded, superseded)
	f.appended = append(f.appended, newCurrent)
	return nil
}

// TestReconcileFixedFunding_HealsDriftedRow pins the healing path for the
// pre-fix OAuth enrollment rows: a clinepass account whose current funding is
// unknown/unlocked provider_policy gets the catalog's paid+locked row
// appended (superseding the drifted one) — while an owner_override current
// row is normalized because the owner has now made ClinePass OAuth an
// automatic paid-only contract; an already-correct row is left alone.
func TestReconcileFixedFunding_HealsDriftedRow(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	account := domain.Account{ID: "acct-1", ProviderID: "clinepass", ConnectionState: domain.ConnectionConnected}

	t.Run("drifted unknown/unlocked row is superseded by paid+locked", func(t *testing.T) {
		repo := &fakeFundingRepo{current: &domain.FundingEvidence{
			ID: "old", AccountID: "acct-1", Funding: domain.FundingUnknown,
			Source: domain.FundingSourceProviderPolicy, Locked: false, ObservedAt: now.Add(-time.Hour),
		}}
		if err := reconcileFixedFunding(context.Background(), repo, account, now); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		if len(repo.appended) != 1 {
			t.Fatalf("appended %d rows, want 1", len(repo.appended))
		}
		got := repo.appended[0]
		if got.Funding != domain.FundingPaid || !got.Locked || got.Source != domain.FundingSourceProviderPolicy {
			t.Fatalf("healed row = %+v, want paid + locked provider_policy", got)
		}
		if len(repo.superseded) != 1 || repo.superseded[0] == nil || repo.superseded[0].ID != "old" {
			t.Fatalf("superseded = %+v, want the drifted row", repo.superseded)
		}
	})

	t.Run("already-correct row is a no-op", func(t *testing.T) {
		repo := &fakeFundingRepo{current: &domain.FundingEvidence{
			ID: "ok", AccountID: "acct-1", Funding: domain.FundingPaid,
			Source: domain.FundingSourceProviderPolicy, Locked: true, ObservedAt: now.Add(-time.Hour),
		}}
		if err := reconcileFixedFunding(context.Background(), repo, account, now); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		if len(repo.appended) != 0 {
			t.Fatalf("appended %d rows for a correct row, want 0", len(repo.appended))
		}
	})

	t.Run("legacy owner override is normalized by the owner-approved paid-only contract", func(t *testing.T) {
		repo := &fakeFundingRepo{current: &domain.FundingEvidence{
			ID: "own", AccountID: "acct-1", Funding: domain.FundingUnknown,
			Source: domain.FundingSourceOwnerOverride, ObservedAt: now.Add(-time.Hour),
		}}
		if err := reconcileFixedFunding(context.Background(), repo, account, now); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		if len(repo.appended) != 1 || repo.appended[0].Funding != domain.FundingPaid || !repo.appended[0].Locked {
			t.Fatalf("appended = %+v, want one locked paid provider-policy row", repo.appended)
		}
		if len(repo.superseded) != 1 || repo.superseded[0] == nil || repo.superseded[0].ID != "own" {
			t.Fatalf("superseded = %+v, want the legacy owner override", repo.superseded)
		}
	})

	t.Run("non-fixed provider is out of scope", func(t *testing.T) {
		repo := &fakeFundingRepo{}
		other := domain.Account{ID: "acct-2", ProviderID: "opencode-zen", ConnectionState: domain.ConnectionConnected}
		if err := reconcileFixedFunding(context.Background(), repo, other, now); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		if len(repo.appended) != 0 {
			t.Fatalf("appended %d rows for a non-fixed provider, want 0", len(repo.appended))
		}
	})

	t.Run("missing current row gets the catalog row created", func(t *testing.T) {
		repo := &fakeFundingRepo{}
		if err := reconcileFixedFunding(context.Background(), repo, account, now); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		if len(repo.appended) != 1 || repo.appended[0].Funding != domain.FundingPaid {
			t.Fatalf("appended = %+v, want one paid row", repo.appended)
		}
		if len(repo.superseded) != 1 || repo.superseded[0] != nil {
			t.Fatalf("superseded = %+v, want [nil] (no prior row)", repo.superseded)
		}
	})
}

// TestAccountMaintenanceTick_ListFailureIsReturned mirrors the other ticks:
// a lister failure surfaces to the scheduler.
func TestAccountMaintenanceTick_ListFailureIsReturned(t *testing.T) {
	tick := &accountMaintenanceTick{
		list: func(context.Context) ([]accountMaintenanceTarget, error) { return nil, errors.New("db locked") },
		probeHealth: func(context.Context, accountMaintenanceTarget) (domain.HealthState, error) {
			t.Fatal("probe must not run")
			return domain.HealthUnknown, nil
		},
		syncQuota: func(context.Context, accountMaintenanceTarget) error {
			t.Fatal("quota must not run")
			return nil
		},
		now:         time.Now,
		lastQuotaAt: make(map[string]time.Time),
	}
	if err := tick.Run(context.Background()); err == nil {
		t.Fatalf("Run returned nil, want the lister error")
	}
}
