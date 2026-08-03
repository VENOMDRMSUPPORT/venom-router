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

// BuildUsabilityTick constructs the opencode-zen model-usability verification
// sweep the boot scheduler runs (design 2026-08-03). It is this package's
// composition root for the sweep, mirroring BuildSchedulerWorkers' role: it
// builds its own CertificationDriver over the shared certifications table
// (stateless; CompareAndSwap handles concurrency with the probe workers' own
// driver), the catalog lister, the decrypt-once credential lease, and the
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
	verifier := &usabilityVerifier{
		offerings: catalogRepo,
		declared:  catalogRepo,
		creds:     application.NewCredentialService(credentialRepo, kr, nil),
		driver:    driver,
		probe:     probeOpenCodeZenChatUsability,
		baseURL:   providers.OpenCodeZenBaseURL,
	}

	tick := &usabilityTick{
		list: func(ctx context.Context) ([]accountToVerify, error) {
			accounts, _, err := accountRepo.List(ctx, "", usabilitySweepAccountLimit, string(providers.OpenCodeZenID))
			if err != nil {
				return nil, fmt.Errorf("httpapi: usability sweep list accounts: %w", err)
			}
			out := make([]accountToVerify, 0, len(accounts))
			for _, a := range accounts {
				if a.ConnectionState != domain.ConnectionConnected {
					continue
				}
				credID, ok := activeCredentialIDFor(ctx, credentialRepo, a.ID)
				if !ok {
					continue
				}
				out = append(out, accountToVerify{AccountID: a.ID, CredentialID: credID})
			}
			return out, nil
		},
		verify: verifier.verifyAccount,
	}

	// Bound each sweep so a run of timing-out probes can never overrun the
	// scheduler's interval and starve the other (sequential) ticks.
	return func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, usabilitySweepBudget)
		defer cancel()
		return tick.Run(ctx)
	}, nil
}
