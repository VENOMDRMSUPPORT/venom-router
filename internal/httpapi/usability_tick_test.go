package httpapi

// usability_tick_test.go pins usabilityTick.Run — the scheduler tick body that
// sweeps every opencode-zen account needing verification. One account's failure
// (a bad credential, a catalog hiccup) must never abort the sweep of the rest,
// and a lister failure is surfaced so the scheduler logs it.

import (
	"context"
	"errors"
	"testing"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
)

func TestUsabilityAccountEligible_RequiresConnectedAndHealthy(t *testing.T) {
	cases := []struct {
		name string
		a    domain.Account
		want bool
	}{
		{"connected healthy", domain.Account{ConnectionState: domain.ConnectionConnected, HealthState: domain.HealthHealthy}, true},
		{"connected degraded", domain.Account{ConnectionState: domain.ConnectionConnected, HealthState: domain.HealthDegraded}, false},
		{"connected expired", domain.Account{ConnectionState: domain.ConnectionConnected, HealthState: domain.HealthExpired}, false},
		{"stopped healthy", domain.Account{ConnectionState: domain.ConnectionStopped, HealthState: domain.HealthHealthy}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := usabilityAccountEligible(tc.a); got != tc.want {
				t.Fatalf("eligible = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestUsabilityTick_VerifiesEveryAccount(t *testing.T) {
	var verified []string
	tick := &usabilityTick{
		list: func(context.Context) ([]accountToVerify, error) {
			return []accountToVerify{
				{AccountID: "acct-1", CredentialID: "cred-1"},
				{AccountID: "acct-2", CredentialID: "cred-2"},
			}, nil
		},
		verify: func(_ context.Context, target accountToVerify) (usabilityRunSummary, error) {
			verified = append(verified, target.AccountID)
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
		verify: func(_ context.Context, target accountToVerify) (usabilityRunSummary, error) {
			verified = append(verified, target.AccountID)
			if target.AccountID == "acct-2" {
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
		verify: func(context.Context, accountToVerify) (usabilityRunSummary, error) { return usabilityRunSummary{}, nil },
	}
	if err := tick.Run(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("Run() error = %v, want the lister error", err)
	}
}
