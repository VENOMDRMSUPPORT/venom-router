package httpapi

import "context"

// accountToVerify is one opencode-zen account the sweep should verify: its id
// and the active credential id to lease.
type accountToVerify struct {
	AccountID    string
	CredentialID string
}

// usabilityTick is the scheduler-tick body that sweeps every opencode-zen
// account needing a usability pass. Its two dependencies are closures so the
// tick logic stays trivially testable and the messy composition-root wiring
// (AccountRepo.List filtered to connected opencode-zen accounts + active
// credential resolution; the assembled usabilityVerifier) lives in ControlMux.
type usabilityTick struct {
	list   func(ctx context.Context) ([]accountToVerify, error)
	verify func(ctx context.Context, accountID, credentialID string) (usabilityRunSummary, error)
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
		if _, err := t.verify(ctx, a.AccountID, a.CredentialID); err != nil {
			continue
		}
	}
	return nil
}
