package httpapi

// usability_tick_test.go pins usabilityTick.Run — the scheduler tick body that
// sweeps every opencode-zen account needing verification. One account's failure
// (a bad credential, a catalog hiccup) must never abort the sweep of the rest,
// and a lister failure is surfaced so the scheduler logs it.

import (
	"context"
	"errors"
	"testing"
)

func TestUsabilityTick_VerifiesEveryAccount(t *testing.T) {
	var verified []string
	tick := &usabilityTick{
		list: func(context.Context) ([]accountToVerify, error) {
			return []accountToVerify{
				{AccountID: "acct-1", CredentialID: "cred-1"},
				{AccountID: "acct-2", CredentialID: "cred-2"},
			}, nil
		},
		verify: func(_ context.Context, accountID, _ string) (usabilityRunSummary, error) {
			verified = append(verified, accountID)
			return usabilityRunSummary{}, nil
		},
	}

	if err := tick.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(verified) != 2 || verified[0] != "acct-1" || verified[1] != "acct-2" {
		t.Fatalf("verified = %v, want [acct-1 acct-2]", verified)
	}
}

func TestUsabilityTick_OneAccountFailureDoesNotAbortTheSweep(t *testing.T) {
	var verified []string
	tick := &usabilityTick{
		list: func(context.Context) ([]accountToVerify, error) {
			return []accountToVerify{
				{AccountID: "acct-1", CredentialID: "cred-1"},
				{AccountID: "acct-2", CredentialID: "cred-2"},
				{AccountID: "acct-3", CredentialID: "cred-3"},
			}, nil
		},
		verify: func(_ context.Context, accountID, _ string) (usabilityRunSummary, error) {
			verified = append(verified, accountID)
			if accountID == "acct-2" {
				return usabilityRunSummary{}, errors.New("credential decrypt failed")
			}
			return usabilityRunSummary{}, nil
		},
	}

	if err := tick.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v, want nil (a per-account failure is not fatal)", err)
	}
	if len(verified) != 3 {
		t.Fatalf("verified %v, want all three attempted despite acct-2 failing", verified)
	}
}

func TestUsabilityTick_ListerErrorSurfaces(t *testing.T) {
	wantErr := errors.New("account list failed")
	tick := &usabilityTick{
		list:   func(context.Context) ([]accountToVerify, error) { return nil, wantErr },
		verify: func(context.Context, string, string) (usabilityRunSummary, error) { return usabilityRunSummary{}, nil },
	}
	if err := tick.Run(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("Run() error = %v, want the lister error", err)
	}
}
