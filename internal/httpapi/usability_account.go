package httpapi

import "context"

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
