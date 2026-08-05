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

// usabilitySweepBudget caps how long any ONE PHASE of a sweep may run. The boot
// scheduler runs ticks SEQUENTIALLY on a 30s interval and hands the tick the
// root context with no per-tick timeout of its own, so an unbounded sweep (60
// models that each time out at 15s, or one account query stuck behind a long
// transaction on the single-connection SQLite pool) would starve the
// quota/probe ticks.
//
// usabilityTick.Run therefore applies this budget TWICE OVER, never once around
// the whole sweep:
//
//   - the LIST phase gets its own deadline (it runs before any lane exists);
//   - EACH provider lane gets its own, independent deadline.
//
// Because the lanes run in PARALLEL, that still bounds one sweep's probe phase
// at a single budget of wall-clock no matter how many providers are swept —
// while guaranteeing that a provider which burns its whole budget on timeouts
// shortens nobody else's. Probes still in flight when a lane's deadline fires
// return a context error (a transport failure -> skipped, never recorded), and
// the remaining ops are picked up on the next tick.
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
	bases := liveProviderBaseURLs()
	return map[string]usabilityProviderSpec{
		string(providers.OpenCodeZenID): {baseURL: providers.OpenCodeZenBaseURL, probe: probeOpenCodeZenChatUsability},
		string(providers.ClinePassID):   {baseURL: providers.ClinePassBaseURL, probe: probeClinePassChatUsability},
		// agnes-ai / ollama-cloud / nvidia-nim (P7-EXEC probes, task-2): plain
		// OpenAI-compatible chat, so the probe appends ONLY "/chat/completions"
		// onto liveProviderBaseURLs()'s already-versioned entry — never a
		// retyped literal, so the two tables can never drift apart.
		string(providers.AgnesAIID):     {baseURL: bases[providers.AgnesAIID], probe: probeOpenAICompatibleChatUsability},
		string(providers.OllamaCloudID): {baseURL: bases[providers.OllamaCloudID], probe: probeOpenAICompatibleChatUsability},
		string(providers.NvidiaNIMID):   {baseURL: bases[providers.NvidiaNIMID], probe: probeOpenAICompatibleChatUsability},
		// gemini-cli (task-3): the probe appends
		// "/models/{id}:generateContent" onto liveProviderBaseURLs()'s
		// VERSIONED entry (GeminiCLIBaseURL + "/v1beta") — the bare host would
		// 404 every probe.
		string(providers.GeminiCLIID): {baseURL: bases[providers.GeminiCLIID], probe: probeGeminiChatUsability},
		// claude-code (task-4): the probe appends "/v1/messages" onto
		// liveProviderBaseURLs()'s BARE-host entry (ClaudeCodeAPIBase) — the
		// versioned gemini-style base would double the /v1 segment.
		string(providers.ClaudeCodeID): {baseURL: bases[providers.ClaudeCodeID], probe: probeClaudeCodeChatUsability},
	}
}

// Capability probes may only strengthen evidence for an account that is
// operational now. Historical offerings stay inspectable, but degraded,
// expired, stopped, or not-yet-verified accounts cannot be re-certified by the
// background sweep.
func usabilityAccountEligible(a domain.Account) bool {
	return a.ConnectionState == domain.ConnectionConnected && a.HealthState == domain.HealthHealthy
}

// UsabilityService is BuildUsabilityService's product: the scheduled sweep's
// Tick (unchanged behavior, just relocated) plus the fast-lane VerifyAccount
// (design 2026-08-05) a caller can fire immediately after an event that just
// made an account's models worth re-checking — today, a successful discovery
// run (DiscoveryHandler). Both funcs are built over the SAME verifiers map,
// so the fast lane and the sweep can never drift into two different
// certification/probe wirings.
type UsabilityService struct {
	// Tick is the scheduler-tick body — usabilityTick.Run, exactly as it ran
	// under the former BuildUsabilityTick.
	Tick func(context.Context) error
	// VerifyAccount runs ONE account's usability pass right now, like a single
	// sweep lane: it re-checks eligibility and resolves the active credential
	// itself, so a caller only ever has to hand it ids. A provider absent from
	// usabilityProviderSpecs() (map miss) is a no-op — it never touches the
	// DB. An ineligible account (unhealthy, disconnected) or one with no
	// active credential is skipped, exactly like the sweep would skip it.
	// Errors are swallowed (logged nowhere, retried by the next sweep) —
	// mirroring how usabilityTick.Run treats one account's failure.
	VerifyAccount func(ctx context.Context, providerID, accountID string)
}

// BuildUsabilityService constructs the model-usability verification
// composition root (design 2026-08-03; extended to clinepass 2026-08-04;
// refactored into a service exposing a fast lane 2026-08-05). It is this
// package's composition root for the sweep, mirroring BuildSchedulerWorkers'
// role: it builds its own CertificationDriver over the shared certifications
// table (stateless; CompareAndSwap handles concurrency with the probe
// workers' own driver), the catalog lister, the decrypt-once credential
// lease, one verifier per probe-capable provider, and the connected-account
// lister closure. now defaults to time.Now.
func BuildUsabilityService(db *storage.DB, kr *secrets.Keyring, now func() time.Time) (*UsabilityService, error) {
	if now == nil {
		now = time.Now
	}

	certRepo := storage.NewCertificationRepo(db, now)
	certAuditor := newCertificationAuditorAdapter(newAuditEmitter(db, nil))
	driver, err := intelligence.NewCertificationDriver(certRepo, certAuditor, intelligence.DefaultProbeRetryBudget, now)
	if err != nil {
		return nil, fmt.Errorf("httpapi: build usability service: %w", err)
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
			// One fresh pacer per account per sweep: verifyAccount is called
			// once per account, so this factory fires exactly that often.
			newPacer: func() *usabilityPacer {
				return newUsabilityPacer(usabilityProbeMaxConcurrency, now)
			},
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

	// verifyAccount is the fast lane: it runs like a single sweep lane for
	// exactly one account, re-doing the SAME eligibility + credential
	// resolution the sweep's own list() closure does above, since the caller
	// (DiscoveryHandler) only ever hands it ids.
	verifyAccount := func(ctx context.Context, providerID, accountID string) {
		verifier, ok := verifiers[providerID]
		if !ok {
			// No probe spec for this provider — never touch the DB.
			return
		}
		account, ok, err := accountRepo.GetByID(ctx, accountID)
		if err != nil || !ok {
			return
		}
		// The sweep's own list() closure above gets provider membership for
		// free — it only ever queries AccountRepo.List scoped to providerID in
		// the first place. VerifyAccount instead loads the account by id
		// alone, so it must check membership explicitly: without this, a
		// mismatched (providerID, accountID) pair would lease account Y's
		// decrypted credential and probe it against provider X's baseURL and
		// verifier (wrong probe, wrong endpoint, wrong credential entirely).
		if account.ProviderID != providerID {
			return
		}
		if !usabilityAccountEligible(account) {
			return
		}
		credentialID, ok := activeCredentialIDFor(ctx, credentialRepo, accountID)
		if !ok {
			return
		}
		// Same per-phase budget the sweep gives one lane — the fast lane IS a
		// one-account lane, not the whole sweep, so it gets the whole budget.
		laneCtx, cancel := context.WithTimeout(ctx, tick.sweepBudget())
		defer cancel()
		_, _ = verifier.verifyAccount(laneCtx, accountID, credentialID)
	}

	return &UsabilityService{Tick: tick.Run, VerifyAccount: verifyAccount}, nil
}
