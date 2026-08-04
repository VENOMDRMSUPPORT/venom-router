package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
)

// TokenRefreshOutcome is what one RefreshIfNeeded call did — the tick
// aggregates these for its summary log, and tests assert on them.
type TokenRefreshOutcome string

const (
	// TokenRefreshRotated: the provider issued a new token and it was
	// persisted in place of the old one.
	TokenRefreshRotated TokenRefreshOutcome = "rotated"
	// TokenRefreshFresh: the stored token is not close to expiry; nothing
	// was sent to the provider.
	TokenRefreshFresh TokenRefreshOutcome = "fresh"
	// TokenRefreshReauthRequired: the provider definitively rejected the
	// refresh (invalid/expired/revoked grant) — the account's health was
	// transitioned to expired; only a new login fixes it.
	TokenRefreshReauthRequired TokenRefreshOutcome = "reauth_required"
	// TokenRefreshTransient: the refresh could not complete (network,
	// 5xx, no refresh token in store) — nothing changed; retry later.
	TokenRefreshTransient TokenRefreshOutcome = "transient"
)

// DefaultTokenRefreshLead is how long before expiry a token is refreshed.
// The legacy reference refreshed 5 minutes before expiry on use and 2
// hours ahead in its hourly worker; with a 30-second scheduler tick a
// 15-minute lead keeps a 1-hour clinepass token permanently fresh without
// hammering the refresh endpoint (each token refreshes once per lifetime,
// ~15 minutes before it would die).
const DefaultTokenRefreshLead = 15 * time.Minute

// TokenRefreshReauthMessage replaces ambiguous transport-status text only
// after the provider explicitly proves the refresh token is invalid.
const TokenRefreshReauthMessage = "OAuth session expired or was revoked — sign in again"

// TokenRefreshService keeps OAuth access tokens alive: it reads an
// account's active credential, asks the provider's OAuthAdapter for a new
// token when the stored one is close to expiry, and persists the rotated
// token in place. This is the ONE refresh orchestration in the codebase —
// the scheduler tick (httpapi) drives it for every connected OAuth
// account, exactly like the legacy reference's proactive refresh worker.
type TokenRefreshService struct {
	creds    *CredentialService
	accounts AccountRepo
	lead     time.Duration
	now      func() time.Time
}

// NewTokenRefreshService builds the service. lead <= 0 uses
// DefaultTokenRefreshLead; now nil uses time.Now.
func NewTokenRefreshService(creds *CredentialService, accounts AccountRepo, lead time.Duration, now func() time.Time) *TokenRefreshService {
	if lead <= 0 {
		lead = DefaultTokenRefreshLead
	}
	if now == nil {
		now = time.Now
	}
	return &TokenRefreshService{creds: creds, accounts: accounts, lead: lead, now: now}
}

// RefreshIfNeeded refreshes account's active credential through adapter
// when it expires within the lead window (or carries no readable expiry —
// refresh is the only way to learn one). The provider call runs INSIDE the
// credential lease callback, mirroring every other adapter call site, so
// the stored plaintext never escapes that scope; only the adapter's NEW
// token leaves it, and only into Rotate.
//
// A definitive provider rejection (providers.ErrInvalidCredential — the
// refresh token itself is dead) transitions the account's health to
// expired so the dashboard shows "re-login required" instead of a token
// that silently stopped working. Any other failure is transient: nothing
// is changed, the next tick retries.
func (s *TokenRefreshService) RefreshIfNeeded(ctx context.Context, account domain.Account, credentialID string, adapter providers.OAuthAdapter) (TokenRefreshOutcome, error) {
	var (
		outcome  TokenRefreshOutcome
		newValue string
		newExp   *time.Time
	)

	leaseErr := s.creds.Use(ctx, credentialID, func(plaintext []byte) error {
		stored := providers.StoredCredentials{Value: string(plaintext)}

		if exp, ok := providers.OAuthTokenExpiry(stored); ok {
			if time.Unix(exp, 0).Sub(s.now()) > s.lead {
				outcome = TokenRefreshFresh
				return nil
			}
		}

		refreshed, err := adapter.RefreshCredentials(ctx, stored)
		if err != nil {
			return err
		}
		newValue = refreshed.Value
		if exp, ok := providers.OAuthTokenExpiry(refreshed); ok {
			t := time.Unix(exp, 0).UTC()
			newExp = &t
		}
		outcome = TokenRefreshRotated
		return nil
	})

	if leaseErr != nil {
		if errors.Is(leaseErr, providers.ErrInvalidCredential) {
			if err := s.markReauthRequired(ctx, account); err != nil {
				return TokenRefreshReauthRequired, fmt.Errorf("application: token refresh: mark reauth required: %w", err)
			}
			return TokenRefreshReauthRequired, leaseErr
		}
		return TokenRefreshTransient, leaseErr
	}

	if outcome != TokenRefreshRotated {
		return outcome, nil
	}

	if err := s.creds.Rotate(ctx, credentialID, newValue, newExp); err != nil {
		return TokenRefreshTransient, fmt.Errorf("application: token refresh: persist rotated token: %w", err)
	}
	if account.HealthState == domain.HealthExpired {
		if err := s.markRecoveredForProbe(ctx, account); err != nil {
			return TokenRefreshTransient, fmt.Errorf("application: token refresh: mark recovered for probe: %w", err)
		}
	}
	return TokenRefreshRotated, nil
}

func (s *TokenRefreshService) markRecoveredForProbe(ctx context.Context, account domain.Account) error {
	next, err := account.TransitionHealth(domain.HealthUnknown, domain.CredentialStatus{Active: true}, s.now())
	if err != nil {
		return err
	}
	_, ok, err := s.accounts.UpdateHealthStateWithError(ctx, account.ID, next, "", s.now())
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("application: token refresh: account %q not found", account.ID)
	}
	return nil
}

// markReauthRequired persists the expired health state for account —
// the domain transition is validated (connected accounts only; the tick
// filters on that), then recorded verbatim.
func (s *TokenRefreshService) markReauthRequired(ctx context.Context, account domain.Account) error {
	next, err := account.TransitionHealth(domain.HealthExpired, domain.CredentialStatus{}, s.now())
	if err != nil {
		return err
	}
	_, ok, err := s.accounts.UpdateHealthStateWithError(ctx, account.ID, next, TokenRefreshReauthMessage, s.now())
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("application: token refresh: account %q not found", account.ID)
	}
	return nil
}
