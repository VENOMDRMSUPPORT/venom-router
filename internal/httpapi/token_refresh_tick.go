package httpapi

import (
	"context"
	"fmt"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/application"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/secrets"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// tokenRefreshPageLimit is the account-list page size the sweep reads. The
// sweep pages until the cursor is exhausted, so this bounds memory, not
// coverage.
const tokenRefreshPageLimit = 200

// tokenRefreshFailureCooldown is how long a credential whose refresh FAILED
// is left alone before the sweep retries it — the legacy reference used the
// same 15-minute cooldown for its quota worker. Without it, a dead refresh
// token would be retried every 30-second scheduler tick.
const tokenRefreshFailureCooldown = 15 * time.Minute

// tokenRefreshCallBudget bounds one account's refresh round-trip so a hung
// provider cannot stall the sweep past the scheduler interval.
const tokenRefreshCallBudget = 30 * time.Second

// This was the pre-fix message written when ClinePass collapsed every 401/403
// refresh response into a dead credential. It is the only expired state that
// may receive a guarded automatic recovery attempt.
const legacyAmbiguousClinePassRefreshError = "provider rejected the credential (401/403)"

func tokenRefreshAccountEligible(a domain.Account) bool {
	if a.ConnectionState != domain.ConnectionConnected {
		return false
	}
	if a.HealthState != domain.HealthExpired {
		return true
	}
	return a.ProviderID == string(providers.ClinePassID) && a.LastHealthError == legacyAmbiguousClinePassRefreshError
}

// tokenRefreshTarget is one account the sweep should keep alive: the
// domain account (for the health transition on a dead refresh token) and
// its active credential id.
type tokenRefreshTarget struct {
	Account      domain.Account
	CredentialID string
}

// tokenRefreshTick is the scheduler-tick body that keeps every connected
// OAuth account's token alive (the current app's counterpart of the legacy
// proactive refresh worker). Dependencies are closures so the tick logic
// stays trivially testable and the composition-root wiring lives in
// BuildTokenRefreshTick — the same shape as usabilityTick.
//
// retryNotBefore is the per-credential failure cooldown. The boot Scheduler
// runs ticks sequentially on one goroutine, so the map needs no lock.
type tokenRefreshTick struct {
	list           func(ctx context.Context) ([]tokenRefreshTarget, error)
	refresh        func(ctx context.Context, target tokenRefreshTarget) (application.TokenRefreshOutcome, error)
	now            func() time.Time
	cooldown       time.Duration
	retryNotBefore map[string]time.Time
}

// Run refreshes every target the lister returns. One credential's failure
// only cools THAT credential down — it never aborts the sweep of the rest.
// Only a lister failure (nothing could be swept at all) is returned, for
// the scheduler to log.
func (t *tokenRefreshTick) Run(ctx context.Context) error {
	targets, err := t.list(ctx)
	if err != nil {
		return err
	}
	for _, target := range targets {
		if until, ok := t.retryNotBefore[target.CredentialID]; ok && t.now().Before(until) {
			continue
		}
		outcome, _ := t.refresh(ctx, target)
		switch outcome {
		case application.TokenRefreshRotated, application.TokenRefreshFresh:
			delete(t.retryNotBefore, target.CredentialID)
		default:
			t.retryNotBefore[target.CredentialID] = t.now().Add(t.cooldown)
		}
	}
	return nil
}

// BuildTokenRefreshTick constructs the OAuth token-refresh sweep the boot
// scheduler runs. It is this package's composition root for the sweep,
// mirroring BuildUsabilityService: its own provider registry (built from the
// same newProviderRegistry list the request path uses), the accounts +
// credentials repos, and the application-layer TokenRefreshService that owns
// the actual refresh-and-rotate rules. now defaults to time.Now.
//
// Sweep scope: CONNECTED accounts whose provider registers an OAuthAdapter
// and that have an active credential. Expired accounts are normally skipped;
// the one exception is ClinePass rows carrying the exact legacy ambiguous
// 401/403 message, which receive a guarded recovery attempt so old false
// expiries can either recover or be replaced by definitive reauth guidance.
func BuildTokenRefreshTick(db *storage.DB, kr *secrets.Keyring, now func() time.Time) (func(context.Context) error, error) {
	if now == nil {
		now = time.Now
	}

	reg := newProviderRegistry()
	credentialRepo := storage.NewAccountCredentialRepo(db)
	accountRepo := storage.NewAccountRepo(db)
	service := application.NewTokenRefreshService(
		application.NewCredentialService(credentialRepo, kr, now),
		accountRepo,
		application.DefaultTokenRefreshLead,
		now,
	)

	tick := &tokenRefreshTick{
		list: func(ctx context.Context) ([]tokenRefreshTarget, error) {
			var out []tokenRefreshTarget
			cursor := ""
			for {
				accounts, next, err := accountRepo.List(ctx, cursor, tokenRefreshPageLimit, "")
				if err != nil {
					return nil, fmt.Errorf("httpapi: token refresh sweep list accounts: %w", err)
				}
				for _, a := range accounts {
					if !tokenRefreshAccountEligible(a) {
						continue
					}
					if _, ok := reg.OAuthAdapter(providers.ProviderID(a.ProviderID)); !ok {
						continue
					}
					credID, ok := activeCredentialIDFor(ctx, credentialRepo, a.ID)
					if !ok {
						continue
					}
					out = append(out, tokenRefreshTarget{Account: a, CredentialID: credID})
				}
				if next == "" {
					return out, nil
				}
				cursor = next
			}
		},
		refresh: func(ctx context.Context, target tokenRefreshTarget) (application.TokenRefreshOutcome, error) {
			adapter, ok := reg.OAuthAdapter(providers.ProviderID(target.Account.ProviderID))
			if !ok {
				return application.TokenRefreshTransient, fmt.Errorf("httpapi: token refresh: no OAuth adapter for %q", target.Account.ProviderID)
			}
			callCtx, cancel := context.WithTimeout(ctx, tokenRefreshCallBudget)
			defer cancel()
			return service.RefreshIfNeeded(callCtx, target.Account, target.CredentialID, adapter)
		},
		now:            now,
		cooldown:       tokenRefreshFailureCooldown,
		retryNotBefore: make(map[string]time.Time),
	}

	return tick.Run, nil
}
