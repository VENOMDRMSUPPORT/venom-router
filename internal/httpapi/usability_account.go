package httpapi

import (
	"context"
	"sync"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/intelligence"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
)

// usabilityProbeMaxConcurrency is the ceiling on how many of ONE account's
// models are probed at once. The pacer starts here and only ever shrinks from
// it (AIMD), so this is the politest-case width, not a target: four parallel
// chat completions against a single free account is brisk enough to finish a
// 60-model catalog inside the sweep budget while staying far below anything a
// provider would read as abuse.
const usabilityProbeMaxConcurrency = 4

// chatOffering is one free model's chat offering-operation to verify: the
// certification row id to drive and the provider model id to probe.
type chatOffering struct {
	OfferingOperationID string
	ProviderModelID     string
}

// usabilityRunSummary reports what one per-account verification pass did, for
// the caller's logging/metrics — never any secret or provider text.
type usabilityRunSummary struct {
	Probed        int
	Usable        int
	StoppedOnAuth bool
	// CertifiedDeclared counts the non-chat capabilities certified from their
	// provider declaration this pass (no runtime probe).
	CertifiedDeclared int
}

// declaredCapability is one declared non-chat capability (tools, vision, …) to
// certify from its provider declaration.
type declaredCapability struct {
	OfferingOperationID string
	Operation           string
}

// declaredSupportedOutcome is the fixed verdict the declaration path records: a
// DEFINITIVE, capability_confirmed "supported". Definitive drives the
// certification's probing -> certified edge carrying TruthSupported (04 §5).
// These non-chat capabilities have no runtime prober; the offering-operation
// exists only because discovery saw the capability declared in models.dev, so
// that declaration IS the evidence (owner decision 2026-08-03).
var declaredSupportedOutcome = intelligence.ProbeOutcome{
	Execution:  intelligence.ProbeSucceeded,
	Truth:      models.TruthSupported,
	Definitive: true,
	Reason:     intelligence.ReasonCapabilityConfirmed,
}

// certifyDeclaredCapabilities certifies every supplied non-chat capability
// supported, from its declaration, by recording declaredSupportedOutcome
// against each (probing -> certified). A per-op failure is swallowed so one bad
// row never aborts the rest — the sweep re-runs next interval; it returns how
// many were recorded.
func certifyDeclaredCapabilities(ctx context.Context, rec certRecorder, caps []declaredCapability) int {
	certified := 0
	for _, c := range caps {
		if _, err := rec.RecordAttempt(ctx, c.OfferingOperationID, declaredSupportedOutcome, 1); err != nil {
			continue
		}
		certified++
	}
	return certified
}

// verifyAccountChatUsability probes every supplied chat offering-operation
// (each already in `probing` — the existing drainer owns observed -> probing)
// and records the verdict, STOPPING the whole account the instant a probe
// reports zenChatAuthFailure: the credential itself is rejected, so probing the
// remaining models would only repeat the same auth failure and burn budget. A
// single-model transport failure is skipped (not a verdict); the scheduler
// re-runs the pass later.
//
// The offerings are probed by a worker pool whose width is the pacer's current
// Concurrency(), RE-READ between waves so a mid-wave halving takes effect on
// the very next one. Three rules hold the pool honest:
//
//   - a probe the pacer REFUSES (Admit() == false: the breaker is open) is
//     SKIPPED, not verdicted — nothing is recorded, and the next sweep retries
//     it, so a paused account can never be mistaken for an unsupported one;
//   - a TRANSPORT failure feeds the pacer nothing at all: an unreachable host
//     says nothing about the provider's rate-limit window, so neither
//     OnSuccess nor OnRateLimited is called;
//   - only zenChatFreeExhausted shrinks the window. Every other verdict means
//     the request went THROUGH (the provider answered, even to refuse), which
//     is exactly what OnSuccess reports.
//
// A nil pacer is treated as an unpaced account at full width, so a caller that
// has no pacer to hand still gets the bounded pool rather than a panic.
func verifyAccountChatUsability(ctx context.Context, rec certRecorder, probe usabilityProbeFn, pacer *usabilityPacer, baseURL, key string, offerings []chatOffering) usabilityRunSummary {
	if pacer == nil {
		pacer = newUsabilityPacer(usabilityProbeMaxConcurrency, nil)
	}

	// Stop-on-auth is a cancel, not a return: the probes already in flight must
	// be released before the summary is read, and cancelling their context is
	// what makes them give up rather than run the full request out.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		mu sync.Mutex
		s  usabilityRunSummary
	)

	for next := 0; next < len(offerings); {
		wave := pacer.Concurrency()
		if wave < 1 {
			wave = 1
		}
		if remaining := len(offerings) - next; wave > remaining {
			wave = remaining
		}

		var wg sync.WaitGroup
		for _, off := range offerings[next : next+wave] {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if !pacer.Admit() {
					return
				}
				res, err := executeChatUsabilityProbe(ctx, rec, probe, baseURL, key, off.OfferingOperationID, off.ProviderModelID, 1)
				if err != nil {
					return
				}
				if res.Verdict == zenChatFreeExhausted {
					pacer.OnRateLimited(res.RetryAfter)
				} else {
					pacer.OnSuccess()
				}

				mu.Lock()
				defer mu.Unlock()
				s.Probed++
				if res.Verdict == zenChatUsable {
					s.Usable++
				}
				// The auth verdict itself was already recorded by
				// executeChatUsabilityProbe above — this only flags the stop
				// and releases the rest of the account.
				if res.Verdict == zenChatAuthFailure {
					s.StoppedOnAuth = true
					cancel()
				}
			}()
		}
		wg.Wait()
		next += wave

		mu.Lock()
		stopped := s.StoppedOnAuth
		mu.Unlock()
		if stopped {
			break
		}
	}

	return s
}
