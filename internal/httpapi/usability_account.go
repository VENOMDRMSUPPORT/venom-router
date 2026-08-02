package httpapi

import (
	"context"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
)

// chatOffering is one free model's chat offering-operation to verify: the
// certification row id to drive and the provider model id to probe.
type chatOffering struct {
	OfferingOperationID string
	ProviderModelID     string
}

// certLifecycle is the slice of intelligence.CertificationDriver the
// per-account run drives: move observed -> probing, then record the verdict.
// *CertificationDriver satisfies it; tests fake it.
type certLifecycle interface {
	StartProbe(ctx context.Context, offeringOperationID string) (models.Certification, error)
	certRecorder
}

// usabilityRunSummary reports what one per-account verification pass did, for
// the caller's logging/metrics — never any secret or provider text.
type usabilityRunSummary struct {
	Probed        int
	Usable        int
	StoppedOnAuth bool
}

// verifyAccountChatUsability drives every supplied free chat offering-operation
// from observed through a probe to a recorded verdict, and STOPS the whole
// account the instant a probe reports zenChatAuthFailure: the credential itself
// is rejected, so probing the remaining models would only repeat the same auth
// failure and burn budget. An offering whose StartProbe is rejected (it is not
// in `observed` — e.g. already certified this cycle) is skipped, not forced.
// A transport failure on a single model is skipped too (not a verdict); the
// scheduler re-runs the pass later.
func verifyAccountChatUsability(ctx context.Context, lc certLifecycle, probe usabilityProbeFn, baseURL, key string, offerings []chatOffering) usabilityRunSummary {
	var s usabilityRunSummary
	for _, off := range offerings {
		if _, err := lc.StartProbe(ctx, off.OfferingOperationID); err != nil {
			continue
		}
		verdict, err := executeChatUsabilityProbe(ctx, lc, probe, baseURL, key, off.OfferingOperationID, off.ProviderModelID, 1)
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
