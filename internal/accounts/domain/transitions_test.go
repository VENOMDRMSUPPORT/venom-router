package domain

import (
	"errors"
	"testing"
	"time"
)

var fixedNow = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func TestTransitionConnection_LegalEdges(t *testing.T) {
	cases := []struct {
		from ConnectionState
		to   ConnectionState
	}{
		{ConnectionConnecting, ConnectionConnected},
		{ConnectionConnecting, ConnectionDisconnected},
		{ConnectionConnected, ConnectionStopped},
		{ConnectionConnected, ConnectionDisconnected},
		{ConnectionStopped, ConnectionConnected},
		{ConnectionStopped, ConnectionDisconnected},
		{ConnectionDisconnected, ConnectionConnecting},
	}

	for _, c := range cases {
		t.Run(string(c.from)+"->"+string(c.to), func(t *testing.T) {
			a := Account{ConnectionState: c.from}
			got, err := a.TransitionConnection(c.to, fixedNow)
			if err != nil {
				t.Fatalf("TransitionConnection(%s -> %s): unexpected error: %v", c.from, c.to, err)
			}
			if got.ConnectionState != c.to {
				t.Fatalf("ConnectionState = %s, want %s", got.ConnectionState, c.to)
			}
			if !got.UpdatedAt.Equal(fixedNow) {
				t.Fatalf("UpdatedAt = %v, want %v (injected clock must stamp it)", got.UpdatedAt, fixedNow)
			}
		})
	}
}

func TestTransitionConnection_IllegalEdgesRejectedAndUnchanged(t *testing.T) {
	cases := []struct {
		from ConnectionState
		to   ConnectionState
	}{
		{ConnectionDisconnected, ConnectionConnected}, // the headline invalid transition (02 §3)
		{ConnectionDisconnected, ConnectionStopped},
		{ConnectionConnecting, ConnectionStopped},
		{ConnectionConnected, ConnectionConnecting},
		{ConnectionStopped, ConnectionConnecting},
		{ConnectionConnecting, ConnectionConnecting}, // self-loop not in the legal graph
	}

	for _, c := range cases {
		t.Run(string(c.from)+"->"+string(c.to), func(t *testing.T) {
			a := Account{ConnectionState: c.from, UpdatedAt: time.Time{}}
			got, err := a.TransitionConnection(c.to, fixedNow)
			if !errors.Is(err, ErrIllegalConnectionTransition) {
				t.Fatalf("TransitionConnection(%s -> %s) error = %v, want ErrIllegalConnectionTransition", c.from, c.to, err)
			}
			if got.ConnectionState != c.from {
				t.Fatalf("ConnectionState after rejected transition = %s, want unchanged %s", got.ConnectionState, c.from)
			}
			if !got.UpdatedAt.IsZero() {
				t.Fatalf("UpdatedAt after rejected transition = %v, want unchanged zero value", got.UpdatedAt)
			}
		})
	}
}

func TestTransitionHealth_RequiresConnected(t *testing.T) {
	for _, cs := range []ConnectionState{ConnectionConnecting, ConnectionStopped, ConnectionDisconnected} {
		t.Run(string(cs), func(t *testing.T) {
			a := Account{ConnectionState: cs, HealthState: HealthUnknown}
			got, err := a.TransitionHealth(HealthHealthy, CredentialStatus{Active: true}, fixedNow)
			if !errors.Is(err, ErrIllegalHealthTransition) {
				t.Fatalf("TransitionHealth while connection_state=%s error = %v, want ErrIllegalHealthTransition", cs, err)
			}
			if got.HealthState != HealthUnknown {
				t.Fatalf("HealthState after rejected transition = %s, want unchanged %s", got.HealthState, HealthUnknown)
			}
		})
	}
}

func TestTransitionHealth_RejectsHealthyWithExpiredCredential(t *testing.T) {
	a := Account{ConnectionState: ConnectionConnected, HealthState: HealthDegraded}

	got, err := a.TransitionHealth(HealthHealthy, CredentialStatus{Active: true, Expired: true}, fixedNow)
	if !errors.Is(err, ErrIllegalHealthTransition) {
		t.Fatalf("TransitionHealth(healthy, expired credential) error = %v, want ErrIllegalHealthTransition", err)
	}
	if got.HealthState != HealthDegraded {
		t.Fatalf("HealthState after rejected transition = %s, want unchanged %s", got.HealthState, HealthDegraded)
	}
}

func TestTransitionHealth_AllowedWhileConnectedAndNotExpired(t *testing.T) {
	for _, target := range []HealthState{HealthUnknown, HealthHealthy, HealthDegraded, HealthUnavailable, HealthExpired} {
		t.Run(string(target), func(t *testing.T) {
			a := Account{ConnectionState: ConnectionConnected, HealthState: HealthUnknown}
			got, err := a.TransitionHealth(target, CredentialStatus{Active: true, Expired: false}, fixedNow)
			if err != nil {
				t.Fatalf("TransitionHealth(%s) unexpected error: %v", target, err)
			}
			if got.HealthState != target {
				t.Fatalf("HealthState = %s, want %s", got.HealthState, target)
			}
			if !got.UpdatedAt.Equal(fixedNow) {
				t.Fatalf("UpdatedAt = %v, want %v (injected clock must stamp it)", got.UpdatedAt, fixedNow)
			}
		})
	}
}
