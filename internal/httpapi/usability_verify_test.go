package httpapi

// usability_verify_test.go pins executeChatUsabilityProbe — the missing hop the
// code map identified: a row driven to `probing` had nothing that actually ran
// the transport probe and recorded the verdict. This unit runs the usability
// probe and feeds the mapped ProbeOutcome to a certification recorder, with the
// two honesty rules a live verifier must never break:
//   - a TRANSPORT failure (provider unreachable) is NOT a verdict: RecordAttempt
//     must not be called, so an outage can never mark a model unsupported.
//   - a provider ERROR response IS a verdict: it is recorded.

import (
	"context"
	"errors"
	"testing"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/intelligence"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
)

// fakeCertRecorder captures the single RecordAttempt call a verify makes.
type fakeCertRecorder struct {
	calls   int
	lastOp  string
	lastOut intelligence.ProbeOutcome
	lastTry int
}

func (f *fakeCertRecorder) RecordAttempt(_ context.Context, offeringOperationID string, outcome intelligence.ProbeOutcome, attempts int) (models.Certification, error) {
	f.calls++
	f.lastOp = offeringOperationID
	f.lastOut = outcome
	f.lastTry = attempts
	return models.Certification{}, nil
}

func fakeProbe(v zenChatUsability, err error) usabilityProbeFn {
	return func(context.Context, string, string, string) (zenChatUsability, error) {
		return v, err
	}
}

func TestExecuteChatUsabilityProbe_UsableRecordsSupportedVerdict(t *testing.T) {
	rec := &fakeCertRecorder{}
	verdict, err := executeChatUsabilityProbe(context.Background(), rec, fakeProbe(zenChatUsable, nil), "http://x", "key", "op-1", "big-pickle", 1)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if verdict != zenChatUsable {
		t.Fatalf("verdict = %v, want zenChatUsable", verdict)
	}
	if rec.calls != 1 {
		t.Fatalf("RecordAttempt calls = %d, want 1", rec.calls)
	}
	if rec.lastOp != "op-1" || rec.lastTry != 1 {
		t.Fatalf("recorded op=%q attempt=%d, want op-1/1", rec.lastOp, rec.lastTry)
	}
	if !rec.lastOut.Definitive || rec.lastOut.Truth != models.TruthSupported {
		t.Fatalf("recorded outcome = %+v, want definitive supported", rec.lastOut)
	}
}

func TestExecuteChatUsabilityProbe_PaidRecordsUnsupportedVerdict(t *testing.T) {
	rec := &fakeCertRecorder{}
	if _, err := executeChatUsabilityProbe(context.Background(), rec, fakeProbe(zenChatPaidUnusable, nil), "http://x", "key", "op-2", "gpt-5.5-pro", 1); err != nil {
		t.Fatalf("error = %v", err)
	}
	if rec.calls != 1 || !rec.lastOut.Definitive || rec.lastOut.Truth != models.TruthUnsupported {
		t.Fatalf("recorded = calls %d outcome %+v, want 1 definitive unsupported", rec.calls, rec.lastOut)
	}
}

func TestExecuteChatUsabilityProbe_TransportErrorNeverRecords(t *testing.T) {
	rec := &fakeCertRecorder{}
	wantErr := errors.New("connection refused")
	_, err := executeChatUsabilityProbe(context.Background(), rec, fakeProbe(zenChatInconclusive, wantErr), "http://x", "key", "op-3", "big-pickle", 1)
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want the transport error", err)
	}
	if rec.calls != 0 {
		t.Fatalf("RecordAttempt calls = %d, want 0 — an unreachable provider is not a verdict", rec.calls)
	}
}
