package httpapi

import (
	"context"
	"sync"
)

// accountToVerify is one account the sweep should verify: its provider (for
// probe dispatch), its id, and the active credential id to lease.
type accountToVerify struct {
	ProviderID   string
	AccountID    string
	CredentialID string
}

// usabilityTick is the scheduler-tick body that sweeps every account needing
// a model-usability pass (opencode-zen and clinepass today — each provider
// with a registered probe spec in BuildUsabilityTick). Its two dependencies
// are closures so the tick logic stays trivially testable and the messy
// composition-root wiring (AccountRepo.List filtered to connected accounts of
// probe-capable providers + active credential resolution; the assembled
// per-provider usabilityVerifier) lives in BuildUsabilityTick.
type usabilityTick struct {
	list   func(ctx context.Context) ([]accountToVerify, error)
	verify func(ctx context.Context, target accountToVerify) (usabilityRunSummary, error)
}

// Run verifies every account the lister returns, one LANE per provider running
// in parallel. A single account's failure (bad credential, catalog hiccup) is
// swallowed so it never aborts the sweep of the rest — the scheduler runs the
// tick again next interval. Only a lister failure (nothing could be swept at
// all) is returned, for the scheduler to log.
//
// Why lanes: the providers are independent remote hosts, so sweeping them
// one after another makes the whole sweep as slow as the sum of its slowest
// provider's timeouts, while a single stalled provider (a hung TLS handshake,
// a 15s-timeout catalog) silently starves every provider queued behind it.
// Accounts stay SEQUENTIAL inside a lane: they share one provider's rate-limit
// budget, and the per-model pacer inside verifyAccountChatUsability is the
// parallelism that provider is allowed.
//
// Each lane gets its OWN usabilitySweepBudget deadline rather than sharing one
// around the whole sweep — a provider that burns its full budget on timeouts
// must not shorten anybody else's.
func (t *usabilityTick) Run(ctx context.Context) error {
	accounts, err := t.list(ctx)
	if err != nil {
		return err
	}

	lanes, order := groupAccountsByProvider(accounts)

	var wg sync.WaitGroup
	for _, providerID := range order {
		targets := lanes[providerID]
		wg.Add(1)
		go func() {
			defer wg.Done()
			laneCtx, cancel := context.WithTimeout(ctx, usabilitySweepBudget)
			defer cancel()
			for _, a := range targets {
				if _, err := t.verify(laneCtx, a); err != nil {
					continue
				}
			}
		}()
	}
	wg.Wait()

	return nil
}

// groupAccountsByProvider buckets the sweep's accounts into one lane per
// provider, returning the lanes plus the provider ids in FIRST-SEEN order so
// the fan-out is deterministic and each lane preserves the lister's account
// order (accounts inside a lane are swept sequentially, in that order).
func groupAccountsByProvider(accounts []accountToVerify) (map[string][]accountToVerify, []string) {
	lanes := make(map[string][]accountToVerify)
	var order []string
	for _, a := range accounts {
		if _, seen := lanes[a.ProviderID]; !seen {
			order = append(order, a.ProviderID)
		}
		lanes[a.ProviderID] = append(lanes[a.ProviderID], a)
	}
	return lanes, order
}
