package httpapi

import (
	"context"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/intelligence"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
)

// usabilityProbeFn is the seam over the real per-model chat-usability probe
// (probeOpenCodeZenChatUsability), injected so the verify step is testable
// without a live provider.
type usabilityProbeFn func(ctx context.Context, baseURL, key, modelID string) (zenChatUsability, error)

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
func executeChatUsabilityProbe(ctx context.Context, rec certRecorder, probe usabilityProbeFn, baseURL, key, offeringOperationID, modelID string, attempts int) (zenChatUsability, error) {
	verdict, err := probe(ctx, baseURL, key, modelID)
	if err != nil {
		return verdict, err
	}
	outcome, err := zenUsabilityProbeOutcome(verdict)
	if err != nil {
		return verdict, err
	}
	if _, err := rec.RecordAttempt(ctx, offeringOperationID, outcome, attempts); err != nil {
		return verdict, err
	}
	return verdict, nil
}
