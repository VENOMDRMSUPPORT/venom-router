package quota

import (
	"errors"
	"fmt"
)

// ReservationState is the canonical reservation lifecycle vocabulary
// (02 §3) — stored states are EXACTLY these five. expires_at is a
// processing deadline, never a terminal state, so there is deliberately
// no `expired` value here.
type ReservationState string

const (
	ReservationReserved              ReservationState = "reserved"
	ReservationReconciliationPending ReservationState = "reconciliation_pending"
	ReservationSettled               ReservationState = "settled"
	ReservationReleased              ReservationState = "released"
	ReservationUnknownConsumption    ReservationState = "unknown_consumption"
)

// ErrUnknownReservationState is returned by ParseReservationState, and by
// AllocationStateFor, for any token outside the five canonical values.
var ErrUnknownReservationState = errors.New("quota: unknown reservation state")

// ParseReservationState fails closed: an unrecognized token — including
// "expired", which is never a stored reservation state — is rejected.
func ParseReservationState(s string) (ReservationState, error) {
	switch ReservationState(s) {
	case ReservationReserved, ReservationReconciliationPending, ReservationSettled, ReservationReleased, ReservationUnknownConsumption:
		return ReservationState(s), nil
	default:
		return "", ErrUnknownReservationState
	}
}

// AllocationState is the FOUR-value per-window mirror of ReservationState
// (M5's CHECK on quota_reservation_allocations.state): an allocation is
// never reconciliation_pending — the reservation carries that state
// while every allocation stays `reserved` and headroom stays debited
// (02 §3).
type AllocationState string

const (
	AllocationReserved           AllocationState = "reserved"
	AllocationSettled            AllocationState = "settled"
	AllocationReleased           AllocationState = "released"
	AllocationUnknownConsumption AllocationState = "unknown_consumption"
)

// ErrUnknownAllocationState is returned by ParseAllocationState for any
// token outside the four canonical values — including
// "reconciliation_pending", which is a reservation state, never an
// allocation state.
var ErrUnknownAllocationState = errors.New("quota: unknown allocation state")

func ParseAllocationState(s string) (AllocationState, error) {
	switch AllocationState(s) {
	case AllocationReserved, AllocationSettled, AllocationReleased, AllocationUnknownConsumption:
		return AllocationState(s), nil
	default:
		return "", ErrUnknownAllocationState
	}
}

// AllocationStateFor maps a reservation state onto the state its
// allocations take. ReservationReconciliationPending maps to
// AllocationReserved — the headroom stays debited (02 §3) — which is why
// this is a function and not a 1:1 string cast.
func AllocationStateFor(s ReservationState) (AllocationState, error) {
	switch s {
	case ReservationReserved, ReservationReconciliationPending:
		return AllocationReserved, nil
	case ReservationSettled:
		return AllocationSettled, nil
	case ReservationReleased:
		return AllocationReleased, nil
	case ReservationUnknownConsumption:
		return AllocationUnknownConsumption, nil
	default:
		return "", ErrUnknownReservationState
	}
}

// IsTerminalReservationState reports whether s is one of the three
// terminal states (settled, released, unknown_consumption) that no
// further transition may leave.
func IsTerminalReservationState(s ReservationState) bool {
	switch s {
	case ReservationSettled, ReservationReleased, ReservationUnknownConsumption:
		return true
	default:
		return false
	}
}

// ErrIllegalTransition is returned by CanTransition for any (from, to)
// pair outside the closed six-edge graph.
var ErrIllegalTransition = errors.New("quota: illegal reservation transition")

// legalTransitions is the closed graph transcribed verbatim from 02 §3's
// transition table — exactly six edges. Every other pair (including every
// self-edge, every edge out of a terminal state, and
// reconciliation_pending -> reserved, which the doc calls out by name) is
// illegal.
var legalTransitions = map[[2]ReservationState]bool{
	{ReservationReserved, ReservationSettled}:                         true,
	{ReservationReserved, ReservationReleased}:                        true,
	{ReservationReserved, ReservationReconciliationPending}:           true,
	{ReservationReconciliationPending, ReservationSettled}:            true,
	{ReservationReconciliationPending, ReservationReleased}:           true,
	{ReservationReconciliationPending, ReservationUnknownConsumption}: true,
}

// CanTransition reports whether the (from, to) edge is legal. It does
// NOT special-case from == to (self-edges are illegal for every state,
// which TestCanTransition_FullCartesian asserts across the whole 5x5
// product) — a caller that wants "already there is a no-op" idempotency
// must check that itself before consulting CanTransition, exactly as
// storage.QuotaLifecycleRepo does.
func CanTransition(from, to ReservationState) error {
	if legalTransitions[[2]ReservationState{from, to}] {
		return nil
	}
	return fmt.Errorf("%w: %s -> %s", ErrIllegalTransition, from, to)
}
