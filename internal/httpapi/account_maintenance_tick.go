package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/application"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/intelligence"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/secrets"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// Sweep cadence — the legacy reference's proven rhythm: health checks every
// 5 minutes, provider quota refresh every 15 (docs/evidence/
// clinepass-legacy-wire-reference.md §5/§8). The scheduler tick fires every
// 30s; these intervals throttle per account inside it.
const (
	accountHealthSweepEvery = 5 * time.Minute
	accountQuotaSweepEvery  = 15 * time.Minute
	accountModelSweepEvery  = 15 * time.Minute
	maintenanceCallBudget   = 30 * time.Second
	maintenancePageLimit    = 200
)

// accountMaintenanceTarget is one connected account the sweep maintains.
type accountMaintenanceTarget struct {
	Account      domain.Account
	CredentialID string
}

// accountMaintenanceTick is the scheduler-tick body that keeps every
// connected account's health status and provider quota evidence fresh
// WITHOUT the owner pressing refresh — the current app's counterpart of the
// legacy health-check/quota workers. Dependencies are closures (same shape
// as usabilityTick/tokenRefreshTick); the composition-root wiring lives in
// BuildAccountMaintenanceTick.
//
// Health due-ness is persisted (accounts.last_health_check_at), so restarts
// and the on-demand probe endpoint share one clock. Quota due-ness is
// in-memory per process: one extra quota fetch right after boot is exactly
// what a fresh dashboard wants, and a failure naturally cools down 15
// minutes (the stamp is written before the attempt).
type accountMaintenanceTick struct {
	list                func(ctx context.Context) ([]accountMaintenanceTarget, error)
	probeHealth         func(ctx context.Context, target accountMaintenanceTarget) (domain.HealthState, error)
	discoverModels      func(ctx context.Context, target accountMaintenanceTarget) error
	syncQuota           func(ctx context.Context, target accountMaintenanceTarget) error
	purgeInactiveModels func(ctx context.Context) error
	// reconcileFunding heals a fixed-funding account whose current funding
	// evidence drifted from the catalog policy (e.g. rows stamped
	// unknown/unlocked by the pre-fix OAuth enrollment). Optional; runs on
	// the quota cadence. Never overrides an owner_override row.
	reconcileFunding func(ctx context.Context, target accountMaintenanceTarget) error
	now              func() time.Time
	lastQuotaAt      map[string]time.Time // account id -> last quota attempt
	lastDiscoveryAt  map[string]time.Time // account id -> last discovery attempt
}

// Run sweeps every target: a due health probe first (its result may mark the
// account expired), then — on the quota cadence — a funding-policy
// reconciliation and a quota sync for accounts whose credential is not
// known-dead. One account's failure never aborts the rest; only a lister
// failure is returned for the scheduler to log.
func (t *accountMaintenanceTick) Run(ctx context.Context) error {
	targets, err := t.list(ctx)
	if err != nil {
		return err
	}
	now := t.now()
	for _, target := range targets {
		currentHealth := target.Account.HealthState
		// Token refresh owns expired-credential recovery. Probing the provider
		// with the same dead access token would overwrite its definitive
		// reauthentication guidance with another generic 401/403 observation.
		// Fixed funding reconciliation is local policy, however, so it can and
		// should still heal legacy ClinePass rows without touching the network.
		if target.Account.HealthState == domain.HealthExpired {
			if last, ok := t.lastQuotaAt[target.Account.ID]; !ok || now.Sub(last) >= accountQuotaSweepEvery {
				t.lastQuotaAt[target.Account.ID] = now
				if t.reconcileFunding != nil {
					_ = t.reconcileFunding(ctx, target)
				}
			}
			continue
		}
		if healthDue(target.Account, now) {
			observed, probeErr := t.probeHealth(ctx, target)
			if probeErr != nil {
				currentHealth = domain.HealthUnknown
			} else {
				currentHealth = observed
			}
		}
		// A live catalog is rebuilt automatically on recovery and refreshed on
		// a bounded cadence. Stamping before the attempt prevents a failing
		// provider from being hammered every 30-second scheduler tick.
		if currentHealth == domain.HealthHealthy && t.discoverModels != nil {
			if last, ok := t.lastDiscoveryAt[target.Account.ID]; !ok || now.Sub(last) >= accountModelSweepEvery {
				t.lastDiscoveryAt[target.Account.ID] = now
				_ = t.discoverModels(ctx, target)
			}
		}
		if last, ok := t.lastQuotaAt[target.Account.ID]; !ok || now.Sub(last) >= accountQuotaSweepEvery {
			t.lastQuotaAt[target.Account.ID] = now
			if t.reconcileFunding != nil {
				_ = t.reconcileFunding(ctx, target)
			}
			_ = t.syncQuota(ctx, target)
		}
	}
	if t.purgeInactiveModels != nil {
		return t.purgeInactiveModels(ctx)
	}
	return nil
}

// healthDue reports whether the account's last persisted health check is
// old enough (or absent) for another probe.
func healthDue(a domain.Account, now time.Time) bool {
	return a.LastHealthCheckAt == nil || now.Sub(*a.LastHealthCheckAt) >= accountHealthSweepEvery
}

// fundingReconcileRepo is the slice of application.FundingRepo the
// reconciliation needs; *storage.FundingEvidenceRepo satisfies it.
type fundingReconcileRepo interface {
	CurrentForAccount(ctx context.Context, accountID string) (domain.FundingEvidence, bool, error)
	AppendSupersession(ctx context.Context, superseded *domain.FundingEvidence, newCurrent domain.FundingEvidence, now time.Time) error
}

// reconcileFixedFunding heals an account of a FIXED-funding provider whose
// current funding evidence drifted from the catalog policy — concretely, the
// rows the pre-fix OAuth enrollment stamped unknown/unlocked for clinepass.
// It appends a corrected provider_policy row through the SAME domain
// supersession rules every other funding write uses. ClinePass is the one
// owner-approved migration exception: this OAuth integration no longer has a
// manual free/paid choice, so a legacy owner_override is explicitly retired in
// favor of the locked provider policy. An already-correct row is a no-op and a
// locked-but-wrong row is left alone. Idempotent by the equality pre-check.
func reconcileFixedFunding(ctx context.Context, repo fundingReconcileRepo, account domain.Account, now time.Time) error {
	if catalogFundingModeFor(account.ProviderID) != domain.FundingModeFixed {
		return nil
	}
	fixedValue, locked := catalogFundingFixedFor(account.ProviderID)
	if fixedValue == domain.FundingUnknown {
		return nil // a fixed mode without a declared value has nothing to enforce
	}

	current, hasCurrent, err := repo.CurrentForAccount(ctx, account.ID)
	if err != nil {
		return err
	}
	if hasCurrent {
		if current.Source == domain.FundingSourceProviderPolicy &&
			current.Funding == fixedValue && current.Locked == locked {
			return nil // already the catalog row
		}
	}

	candidate := domain.FundingEvidence{
		ID:         newMaintenanceRowID(),
		AccountID:  account.ID,
		Funding:    fixedValue,
		Source:     domain.FundingSourceProviderPolicy,
		Locked:     locked,
		Confidence: 1.0,
		Reason:     "catalog fixed-funding reconciliation",
		ObservedAt: now,
	}

	if !hasCurrent {
		return repo.AppendSupersession(ctx, nil, candidate, now)
	}
	if current.Source == domain.FundingSourceOwnerOverride && account.ProviderID == string(providers.ClinePassID) {
		// This is not automated evidence overriding an owner decision. The
		// owner changed the product contract on 2026-08-04: ClinePass OAuth is
		// paid-only and automatically detected, so the old choice is invalid.
		candidate.Reason = "owner-approved ClinePass OAuth paid-only migration"
		return repo.AppendSupersession(ctx, &current, candidate, now)
	}
	result, err := domain.ApplyFundingSupersession(current, candidate, now)
	if err != nil || !result.Superseded {
		return err // ErrFundingLocked (leave it), or an ordinary "current stands"
	}
	return repo.AppendSupersession(ctx, &current, candidate, now)
}

// newMaintenanceRowID mints a fresh high-entropy row id for rows the
// maintenance sweep itself creates (same shape as newOAuthTransactionID).
func newMaintenanceRowID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("maint-fallback-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// BuildAccountMaintenanceTick constructs the periodic health + quota sweep
// the boot scheduler runs. Its own composition root, mirroring
// BuildTokenRefreshTick: the shared provider registry list, the accounts +
// credentials repos, the decrypt-once credential lease, and the SAME
// reconciliation write path the on-demand quota-refresh endpoint uses
// (SyncQuotaWindows), so there is exactly one definition of "persist
// provider quota evidence". now defaults to time.Now.
func BuildAccountMaintenanceTick(db *storage.DB, kr *secrets.Keyring, now func() time.Time) (func(context.Context) error, error) {
	if now == nil {
		now = time.Now
	}

	reg := newProviderRegistry()
	credentialRepo := storage.NewAccountCredentialRepo(db)
	accountRepo := storage.NewAccountRepo(db)
	credService := application.NewCredentialService(credentialRepo, kr, now)
	reconciliation := storage.NewReconciliationRepo(db, nil, quota.DefaultReconciliationPolicy(), storage.NewQuotaLifecycleRepo(db, nil, nil), nil)
	fundingRepo := storage.NewFundingEvidenceRepo(db)
	discoveryRepo := storage.NewDiscoveryRepo(db, newMaintenanceRowID)
	modelLifecycleRepo := storage.NewModelLifecycleRepo(db)

	tick := &accountMaintenanceTick{
		list: func(ctx context.Context) ([]accountMaintenanceTarget, error) {
			var out []accountMaintenanceTarget
			cursor := ""
			for {
				accounts, next, err := accountRepo.List(ctx, cursor, maintenancePageLimit, "")
				if err != nil {
					return nil, fmt.Errorf("httpapi: account maintenance sweep list: %w", err)
				}
				for _, a := range accounts {
					if a.ConnectionState != domain.ConnectionConnected {
						continue
					}
					credID, ok := activeCredentialIDFor(ctx, credentialRepo, a.ID)
					if !ok {
						continue
					}
					out = append(out, accountMaintenanceTarget{Account: a, CredentialID: credID})
				}
				if next == "" {
					return out, nil
				}
				cursor = next
			}
		},
		probeHealth: func(ctx context.Context, target accountMaintenanceTarget) (domain.HealthState, error) {
			adapter, ok := reg.HealthAdapter(providers.ProviderID(target.Account.ProviderID))
			if !ok {
				return target.Account.HealthState, nil // provider registers no health capability
			}
			callCtx, cancel := context.WithTimeout(ctx, maintenanceCallBudget)
			defer cancel()

			var (
				obs      providers.HealthObservation
				probeErr error
			)
			leaseErr := credService.Use(callCtx, target.CredentialID, func(plaintext []byte) error {
				obs, probeErr = adapter.CheckAccountHealth(callCtx, providers.StoredCredentials{Value: string(plaintext)})
				return nil
			})
			if leaseErr != nil {
				return domain.HealthUnknown, leaseErr
			}
			checkedAt := now()
			var healthTarget domain.HealthState
			var safeMessage string
			if probeErr != nil {
				// The adapter was reached but did not complete: an honest
				// unknown, with a fixed safe message — never raw provider text
				// (mirrors AccountsHandler.runHealthProbe).
				healthTarget = domain.HealthUnknown
				safeMessage = "health probe did not complete"
			} else {
				healthTarget = healthTargetFromObservation(obs)
				if obs.Failure != nil {
					safeMessage = obs.Failure.SafeMessage
				}
			}
			next, terr := target.Account.TransitionHealth(healthTarget, domain.CredentialStatus{Active: true}, checkedAt)
			if terr != nil {
				return domain.HealthUnknown, terr
			}
			_, _, uerr := accountRepo.UpdateHealthObservation(ctx, target.Account.ID, next, checkedAt, safeMessage, checkedAt)
			if uerr != nil {
				return domain.HealthUnknown, uerr
			}
			return next.HealthState, nil
		},
		discoverModels: func(ctx context.Context, target accountMaintenanceTarget) error {
			adapter, ok := reg.ModelDiscoveryAdapter(providers.ProviderID(target.Account.ProviderID))
			if !ok {
				return nil
			}
			callCtx, cancel := context.WithTimeout(ctx, maintenanceCallBudget)
			defer cancel()
			svc := intelligence.NewDiscoveryService(adapter, credService, discoveryRepo, discoveryRepo, now)
			result, err := svc.Run(callCtx, intelligence.RunParams{
				AccountID:    target.Account.ID,
				ProviderID:   target.Account.ProviderID,
				CredentialID: target.CredentialID,
				RunID:        newMaintenanceRowID(),
			})
			if err != nil {
				return err
			}
			if result.Outcome == intelligence.OutcomeFailed {
				return fmt.Errorf("httpapi: account maintenance discovery failed: %s", result.ReasonCode)
			}
			return nil
		},
		reconcileFunding: func(ctx context.Context, target accountMaintenanceTarget) error {
			return reconcileFixedFunding(ctx, fundingRepo, target.Account, now())
		},
		syncQuota: func(ctx context.Context, target accountMaintenanceTarget) error {
			adapter, ok := reg.QuotaAdapter(providers.ProviderID(target.Account.ProviderID))
			if !ok {
				return nil // provider registers no quota capability
			}
			callCtx, cancel := context.WithTimeout(ctx, maintenanceCallBudget)
			defer cancel()

			var (
				result   providers.QuotaResult
				fetchErr error
			)
			leaseErr := credService.Use(callCtx, target.CredentialID, func(plaintext []byte) error {
				result, fetchErr = adapter.FetchQuota(callCtx, providers.StoredCredentials{Value: string(plaintext)})
				return nil
			})
			if leaseErr != nil {
				return leaseErr
			}
			if fetchErr != nil {
				return fetchErr
			}
			specs, mapErr := quota.WindowsFromProviderResult(result, now())
			if mapErr != nil {
				return mapErr
			}
			return reconciliation.SyncQuotaWindows(ctx, target.Account.ID, specs, nil)
		},
		purgeInactiveModels: func(ctx context.Context) error {
			_, err := modelLifecycleRepo.PurgeInactive(ctx)
			return err
		},
		now:             now,
		lastQuotaAt:     make(map[string]time.Time),
		lastDiscoveryAt: make(map[string]time.Time),
	}

	return tick.Run, nil
}
