package httpapi

import "context"

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

// Run verifies every account the lister returns. A single account's failure
// (bad credential, catalog hiccup) is swallowed so it never aborts the sweep of
// the rest — the scheduler runs the tick again next interval. Only a lister
// failure (nothing could be swept at all) is returned, for the scheduler to log.
func (t *usabilityTick) Run(ctx context.Context) error {
	accounts, err := t.list(ctx)
	if err != nil {
		return err
	}
	for _, a := range accounts {
		if _, err := t.verify(ctx, a); err != nil {
			continue
		}
	}
	return nil
}
