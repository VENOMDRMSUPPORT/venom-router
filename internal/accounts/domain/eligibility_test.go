package domain

import "testing"

func TestProjectEligibility_EligibleCase(t *testing.T) {
	a := Account{ConnectionState: ConnectionConnected, HealthState: HealthHealthy}
	cred := CredentialStatus{Active: true, Expired: false}

	got := ProjectEligibility(a, cred, false)
	if !got.Eligible {
		t.Fatalf("Eligibility = %+v, want Eligible = true", got)
	}
	if got.Reason != "" {
		t.Fatalf("Eligible account has non-empty Reason = %q", got.Reason)
	}
}

func TestProjectEligibility_EligibleWithUnknownOrDegradedHealth(t *testing.T) {
	for _, hs := range []HealthState{HealthUnknown, HealthDegraded} {
		a := Account{ConnectionState: ConnectionConnected, HealthState: hs}
		got := ProjectEligibility(a, CredentialStatus{Active: true}, false)
		if !got.Eligible {
			t.Fatalf("health_state=%s: Eligibility = %+v, want Eligible = true", hs, got)
		}
	}
}

func TestProjectEligibility_OneCasePerReason(t *testing.T) {
	cases := []struct {
		name   string
		a      Account
		cred   CredentialStatus
		cool   bool
		reason IneligibleReason
	}{
		{
			name:   "account_stopped",
			a:      Account{ConnectionState: ConnectionStopped, HealthState: HealthHealthy},
			cred:   CredentialStatus{Active: true},
			reason: ReasonAccountStopped,
		},
		{
			name:   "account_disconnected (disconnected)",
			a:      Account{ConnectionState: ConnectionDisconnected, HealthState: HealthHealthy},
			cred:   CredentialStatus{Active: true},
			reason: ReasonAccountDisconnected,
		},
		{
			name:   "account_disconnected (connecting)",
			a:      Account{ConnectionState: ConnectionConnecting, HealthState: HealthUnknown},
			cred:   CredentialStatus{},
			reason: ReasonAccountDisconnected,
		},
		{
			name:   "reauth_in_progress",
			a:      Account{ConnectionState: ConnectionConnected, HealthState: HealthHealthy, ReauthInProgress: true},
			cred:   CredentialStatus{Active: true},
			reason: ReasonReauthInProgress,
		},
		{
			name:   "cooling_down",
			a:      Account{ConnectionState: ConnectionConnected, HealthState: HealthHealthy},
			cred:   CredentialStatus{Active: true},
			cool:   true,
			reason: ReasonCoolingDown,
		},
		{
			name:   "credential_expired (no active credential)",
			a:      Account{ConnectionState: ConnectionConnected, HealthState: HealthHealthy},
			cred:   CredentialStatus{Active: false},
			reason: ReasonCredentialExpired,
		},
		{
			name:   "credential_expired (expired credential)",
			a:      Account{ConnectionState: ConnectionConnected, HealthState: HealthHealthy},
			cred:   CredentialStatus{Active: true, Expired: true},
			reason: ReasonCredentialExpired,
		},
		{
			name:   "credential_expired (health_state = expired)",
			a:      Account{ConnectionState: ConnectionConnected, HealthState: HealthExpired},
			cred:   CredentialStatus{Active: true},
			reason: ReasonCredentialExpired,
		},
		{
			name:   "account_unavailable",
			a:      Account{ConnectionState: ConnectionConnected, HealthState: HealthUnavailable},
			cred:   CredentialStatus{Active: true},
			reason: ReasonAccountUnavailable,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ProjectEligibility(c.a, c.cred, c.cool)
			if got.Eligible {
				t.Fatalf("Eligibility = %+v, want ineligible with reason %q", got, c.reason)
			}
			if got.Reason != c.reason {
				t.Fatalf("Reason = %q, want %q", got.Reason, c.reason)
			}
		})
	}
}
