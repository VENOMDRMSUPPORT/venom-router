package httpapi

import (
	"context"
	"fmt"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/application"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/intelligence"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/secrets"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// usabilitySweepAccountLimit bounds one sweep's account fan-out. opencode-zen
// realistically has a handful of accounts; this is a runaway guard, not a page
// size the owner tunes.
const usabilitySweepAccountLimit = 100

// usabilitySweepBudget caps how long one sweep may run. The boot scheduler runs
// ticks SEQUENTIALLY on a 30s interval, so an unbounded sweep (60 models that
// each time out at 15s) would starve the quota/probe ticks. A per-run deadline
// keeps every sweep well under the interval; probes still in flight when it
// fires return a context error (a transport failure -> skipped, never recorded),
// and the remaining ops are picked up on the next tick.
const usabilitySweepBudget = 25 * time.Second

// usabilityProviderSpec is one provider's model-usability probe wiring: the
// base URL its chat endpoint lives under and the probe that runs one model's
// real completion and classifies it. The probe receives the leased credential
// PLAINTEXT verbatim (an API key for zen; the token JSON for clinepass —
// each probe owns its own credential decoration).
type usabilityProviderSpec struct {
	baseURL string
	probe   usabilityProbeFn
}

// usabilityProviderSpecs is the ONE list of providers with a live per-model
// chat-usability probe. A provider absent from this map is simply never
// swept — its models stay in `probing` until a probe is written for it.
func usabilityProviderSpecs() map[string]usabilityProviderSpec {
	return map[string]usabilityProviderSpec{
		string(providers.OpenCodeZenID): {baseURL: providers.OpenCodeZenBaseURL, probe: probeOpenCodeZenChatUsability},
		string(providers.ClinePassID):   {baseURL: providers.ClinePassBaseURL, probe: probeClinePassChatUsability},
	}
}

// Capability probes may only strengthen evidence for an account that is
// operational now. Historical offerings stay inspectable, but degraded,
// expired, stopped, or not-yet-verified accounts cannot be re-certified by the
// background sweep.
func usabilityAccountEligible(a domain.Account) bool {
	return a.ConnectionState == domain.ConnectionConnected && a.HealthState == domain.HealthHealthy
}

// BuildUsabilityTick constructs the model-usability verification sweep the
// boot scheduler runs (design 2026-08-03; extended to clinepass 2026-08-04).
// It is this package's composition root for the sweep, mirroring
// BuildSchedulerWorkers' role: it builds its own CertificationDriver over the
// shared certifications table (stateless; CompareAndSwap handles concurrency
// with the probe workers' own driver), the catalog lister, the decrypt-once
// credential lease, one verifier per probe-capable provider, and the
// connected-account lister closure, and returns the tick's Run for the
// scheduler to drive. now defaults to time.Now.
func BuildUsabilityTick(db *storage.DB, kr *secrets.Keyring, now func() time.Time) (func(context.Context) error, error) {
	if now == nil {
		now = time.Now
	}

	certRepo := storage.NewCertificationRepo(db, now)
	certAuditor := newCertificationAuditorAdapter(newAuditEmitter(db, nil))
	driver, err := intelligence.NewCertificationDriver(certRepo, certAuditor, intelligence.DefaultProbeRetryBudget, now)
	if err != nil {
		return nil, fmt.Errorf("httpapi: build usability tick: %w", err)
	}

	credentialRepo := storage.NewAccountCredentialRepo(db)
	accountRepo := storage.NewAccountRepo(db)
	catalogRepo := storage.NewCatalogRepo(db)
	credService := application.NewCredentialService(credentialRepo, kr, nil)

	specs := usabilityProviderSpecs()
	verifiers := make(map[string]*usabilityVerifier, len(specs))
	for providerID, spec := range specs {
		verifiers[providerID] = &usabilityVerifier{
			offerings: catalogRepo,
			declared:  catalogRepo,
			creds:     credService,
			driver:    driver,
			probe:     spec.probe,
			baseURL:   spec.baseURL,
		}
	}

	tick := &usabilityTick{
		list: func(ctx context.Context) ([]accountToVerify, error) {
			var out []accountToVerify
			for providerID := range verifiers {
				accounts, _, err := accountRepo.List(ctx, "", usabilitySweepAccountLimit, providerID)
				if err != nil {
					return nil, fmt.Errorf("httpapi: usability sweep list accounts: %w", err)
				}
				for _, a := range accounts {
					if !usabilityAccountEligible(a) {
						continue
					}
					credID, ok := activeCredentialIDFor(ctx, credentialRepo, a.ID)
					if !ok {
						continue
					}
					out = append(out, accountToVerify{ProviderID: providerID, AccountID: a.ID, CredentialID: credID})
				}
			}
			return out, nil
		},
		verify: func(ctx context.Context, target accountToVerify) (usabilityRunSummary, error) {
			verifier, ok := verifiers[target.ProviderID]
			if !ok {
				return usabilityRunSummary{}, fmt.Errorf("httpapi: usability sweep: no probe spec for provider %q", target.ProviderID)
			}
			return verifier.verifyAccount(ctx, target.AccountID, target.CredentialID)
		},
	}

	// Bound each sweep so a run of timing-out probes can never overrun the
	// scheduler's interval and starve the other (sequential) ticks.
	return func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, usabilitySweepBudget)
		defer cancel()
		return tick.Run(ctx)
	}, nil
}
