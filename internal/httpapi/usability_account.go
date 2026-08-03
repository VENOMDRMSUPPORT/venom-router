package httpapi

import (
	"context"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/intelligence"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
)

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
func verifyAccountChatUsability(ctx context.Context, rec certRecorder, probe usabilityProbeFn, baseURL, key string, offerings []chatOffering) usabilityRunSummary {
	var s usabilityRunSummary
	for _, off := range offerings {
		verdict, err := executeChatUsabilityProbe(ctx, rec, probe, baseURL, key, off.OfferingOperationID, off.ProviderModelID, 1)
		if err != nil {
			continue
		}
		s.Probed++
		if verdict == zenChatUsable {
			s.Usable++
		}
		if verdict == zenChatAuthFailure {
			s.StoppedOnAuth = true
			return s
		}
	}
	return s
}
