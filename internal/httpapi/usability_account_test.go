package httpapi

// usability_account_test.go pins verifyAccountChatUsability — the per-account
// loop that drives each free chat offering-operation from `observed` through a
// probe to a recorded verdict, and STOPS the whole account the moment a probe
// reports an AuthError (the credential itself is bad, so probing the rest is
// pointless and would just repeat the same auth failure).

import (
	"context"
	"errors"
	"testing"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/intelligence"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
)

type recordedAttempt struct {
	op       string
	outcome  intelligence.ProbeOutcome
	attempts int
}

type fakeCertLifecycle struct {
	startErrOn map[string]error
	starts     []string
	records    []recordedAttempt
}

func (f *fakeCertLifecycle) StartProbe(_ context.Context, op string) (models.Certification, error) {
	f.starts = append(f.starts, op)
	if f.startErrOn != nil {
		if err := f.startErrOn[op]; err != nil {
			return models.Certification{}, err
		}
	}
	return models.Certification{}, nil
}

func (f *fakeCertLifecycle) RecordAttempt(_ context.Context, op string, o intelligence.ProbeOutcome, a int) (models.Certification, error) {
	f.records = append(f.records, recordedAttempt{op, o, a})
	return models.Certification{}, nil
}

func probeByModel(m map[string]zenChatUsability) usabilityProbeFn {
	return func(_ context.Context, _, _, modelID string) (zenChatUsability, error) {
		return m[modelID], nil
	}
}

func TestVerifyAccountChatUsability_ProbesEveryOfferingAndCountsUsable(t *testing.T) {
	lc := &fakeCertLifecycle{}
	offerings := []chatOffering{
		{OfferingOperationID: "op-a", ProviderModelID: "big-pickle"},
		{OfferingOperationID: "op-b", ProviderModelID: "gpt-5.5-pro"},
		{OfferingOperationID: "op-c", ProviderModelID: "deepseek-v4-flash-free"},
	}
	probe := probeByModel(map[string]zenChatUsability{
		"big-pickle":             zenChatUsable,
		"gpt-5.5-pro":            zenChatPaidUnusable,
		"deepseek-v4-flash-free": zenChatUsable,
	})

	got := verifyAccountChatUsability(context.Background(), lc, probe, "http://x", "key", offerings)

	if got.Probed != 3 || got.Usable != 2 || got.StoppedOnAuth {
		t.Fatalf("summary = %+v, want Probed 3 Usable 2 StoppedOnAuth false", got)
	}
	if len(lc.records) != 3 {
		t.Fatalf("recorded %d attempts, want 3", len(lc.records))
	}
}

func TestVerifyAccountChatUsability_AuthFailureStopsTheAccount(t *testing.T) {
	lc := &fakeCertLifecycle{}
	offerings := []chatOffering{
		{OfferingOperationID: "op-a", ProviderModelID: "big-pickle"},
		{OfferingOperationID: "op-b", ProviderModelID: "bad-auth-model"},
		{OfferingOperationID: "op-c", ProviderModelID: "never-reached"},
	}
	probe := probeByModel(map[string]zenChatUsability{
		"big-pickle":     zenChatUsable,
		"bad-auth-model": zenChatAuthFailure,
		"never-reached":  zenChatUsable,
	})

	got := verifyAccountChatUsability(context.Background(), lc, probe, "http://x", "key", offerings)

	if !got.StoppedOnAuth {
		t.Fatal("StoppedOnAuth = false, want true")
	}
	// op-c must never be started once the account hit an auth failure.
	for _, s := range lc.starts {
		if s == "op-c" {
			t.Fatalf("op-c was probed after an auth failure; starts = %v", lc.starts)
		}
	}
}

func TestVerifyAccountChatUsability_StartProbeErrorSkipsWithoutRecording(t *testing.T) {
	lc := &fakeCertLifecycle{startErrOn: map[string]error{"op-a": errors.New("not in observed state")}}
	offerings := []chatOffering{
		{OfferingOperationID: "op-a", ProviderModelID: "big-pickle"},
		{OfferingOperationID: "op-b", ProviderModelID: "deepseek-v4-flash-free"},
	}
	probe := probeByModel(map[string]zenChatUsability{
		"big-pickle":             zenChatUsable,
		"deepseek-v4-flash-free": zenChatUsable,
	})

	got := verifyAccountChatUsability(context.Background(), lc, probe, "http://x", "key", offerings)

	if got.Probed != 1 || got.Usable != 1 {
		t.Fatalf("summary = %+v, want Probed 1 Usable 1 (op-a skipped)", got)
	}
	for _, r := range lc.records {
		if r.op == "op-a" {
			t.Fatal("op-a recorded a verdict despite StartProbe failing")
		}
	}
}
