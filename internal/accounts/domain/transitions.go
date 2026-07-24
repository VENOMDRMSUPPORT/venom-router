package domain

import (
	"errors"
	"fmt"
	"time"
)

// ErrIllegalConnectionTransition is returned when a connection_state
// transition is not in the legal graph (02 §3). The account is returned
// unchanged alongside this error — a rejected transition never mutates
// state.
var ErrIllegalConnectionTransition = errors.New("domain: illegal connection_state transition")

// ErrIllegalHealthTransition is returned when a health_state transition
// violates one of 02 §3's invariants: changing health while not
// connected, or setting healthy while the active credential is expired.
var ErrIllegalHealthTransition = errors.New("domain: illegal health_state transition")

// legalConnectionTransitions is the exact graph from 02 §3's Axis 1
// table: connecting→{connected,disconnected}; connected→{stopped,
// disconnected}; stopped→{connected,disconnected}; disconnected→
// {connecting}. Notably, disconnected→connected is NOT legal — a
// disconnected account can only return via re-enrollment (connecting).
var legalConnectionTransitions = map[ConnectionState]map[ConnectionState]bool{
	ConnectionConnecting: {
		ConnectionConnected:    true,
		ConnectionDisconnected: true,
	},
	ConnectionConnected: {
		ConnectionStopped:      true,
		ConnectionDisconnected: true,
	},
	ConnectionStopped: {
		ConnectionConnected:    true,
		ConnectionDisconnected: true,
	},
	ConnectionDisconnected: {
		ConnectionConnecting: true,
	},
}

// TransitionConnection applies a connection_state transition, rejecting
// any edge not in the legal graph. now is the injected clock value used
// to stamp UpdatedAt — this package never calls time.Now() itself. On
// rejection, a is returned unchanged alongside a wrapped
// ErrIllegalConnectionTransition; the caller is responsible for auditing
// the rejection (02 §3: "emits an audit_event"), which is outside this
// pure package's scope.
func (a Account) TransitionConnection(target ConnectionState, now time.Time) (Account, error) {
	if !legalConnectionTransitions[a.ConnectionState][target] {
		return a, fmt.Errorf("%w: %s -> %s", ErrIllegalConnectionTransition, a.ConnectionState, target)
	}

	next := a
	next.ConnectionState = target
	next.UpdatedAt = now
	return next, nil
}

// TransitionHealth applies a health_state transition. It is only legal
// while the account is connected, and it rejects setting HealthHealthy
// while cred reports the active credential is expired (02 §3). cred is
// caller-supplied — this package does not read credentials itself. now
// is the injected clock value used to stamp UpdatedAt. On rejection, a is
// returned unchanged alongside a wrapped ErrIllegalHealthTransition.
func (a Account) TransitionHealth(target HealthState, cred CredentialStatus, now time.Time) (Account, error) {
	if a.ConnectionState != ConnectionConnected {
		return a, fmt.Errorf("%w: cannot change health_state while connection_state = %s", ErrIllegalHealthTransition, a.ConnectionState)
	}
	if target == HealthHealthy && cred.Expired {
		return a, fmt.Errorf("%w: cannot set health_state = healthy while the active credential is expired", ErrIllegalHealthTransition)
	}

	next := a
	next.HealthState = target
	next.UpdatedAt = now
	return next, nil
}
