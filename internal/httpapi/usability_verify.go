package httpapi

import (
	"context"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/intelligence"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
)

// usabilityProbeResult is one probe's full outcome: the semantic verdict plus
// the provider's advertised backoff (Retry-After / retry-after-ms), when it
// sent one. RetryAfter is zero when nothing was advertised — never a guessed
// default. A later pacer (task 6) consumes RetryAfter to schedule the retry.
type usabilityProbeResult struct {
	Verdict    zenChatUsability
	RetryAfter time.Duration
}

// usabilityProbeFn is the seam over the real per-model chat-usability probe
// (probeOpenCodeZenChatUsability), injected so the verify step is testable
// without a live provider.
type usabilityProbeFn func(ctx context.Context, baseURL, key, modelID string) (usabilityProbeResult, error)

// certRecorder is the slice of intelligence.CertificationDriver the verify step
// needs: turn a probe outcome into a lifecycle move. *CertificationDriver
// satisfies it; tests fake it.
type certRecorder interface {
	RecordAttempt(ctx context.Context, offeringOperationID string, outcome intelligence.ProbeOutcome, attempts int) (models.Certification, error)
}

// executeChatUsabilityProbe is the "missing hop": it runs the usability probe
// for one model whose chat offering-operation is already in `probing`, and
// records the mapped ProbeOutcome as a certification attempt.
//
// The two honesty rules of a live verifier:
//   - a TRANSPORT failure (provider unreachable / timeout) is NOT a verdict:
//     RecordAttempt is never called, so a transient outage can never flip a
//     model to unsupported. The error is returned for the caller to reschedule.
//   - a provider ERROR response IS a verdict (the classifier already read it
//     from the body) and is recorded like any other attempt.
func executeChatUsabilityProbe(ctx context.Context, rec certRecorder, probe usabilityProbeFn, baseURL, key, offeringOperationID, modelID string, attempts int) (usabilityProbeResult, error) {
	res, err := probe(ctx, baseURL, key, modelID)
	if err != nil {
		return res, err
	}
	outcome, err := zenUsabilityProbeOutcome(res.Verdict)
	if err != nil {
		return res, err
	}
	if _, err := rec.RecordAttempt(ctx, offeringOperationID, outcome, attempts); err != nil {
		return res, err
	}
	return res, nil
}
