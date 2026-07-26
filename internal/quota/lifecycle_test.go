package quota

import (
	"errors"
	"testing"
)

var allReservationStates = []ReservationState{
	ReservationReserved,
	ReservationReconciliationPending,
	ReservationSettled,
	ReservationReleased,
	ReservationUnknownConsumption,
}

// TestCanTransition_FullCartesian enumerates the full 5x5 (25-pair)
// product of ReservationState and proves exactly the six documented
// edges (02 §3) return nil, and all 19 others return
// errors.Is(err, ErrIllegalTransition). Asserting the legal count is
// exactly 6 means a seventh edge silently added to legalTransitions
// fails this test even if no other assertion happens to cover it.
func TestCanTransition_FullCartesian(t *testing.T) {
	legalCount := 0
	for _, from := range allReservationStates {
		for _, to := range allReservationStates {
			err := CanTransition(from, to)
			want := legalTransitions[[2]ReservationState{from, to}]
			if want {
				legalCount++
				if err != nil {
					t.Errorf("CanTransition(%s, %s) = %v, want nil (documented legal edge)", from, to, err)
				}
				continue
			}
			if !errors.Is(err, ErrIllegalTransition) {
				t.Errorf("CanTransition(%s, %s) = %v, want ErrIllegalTransition", from, to, err)
			}
		}
	}
	if legalCount != 6 {
		t.Fatalf("legal edge count = %d, want exactly 6", legalCount)
	}
}

// TestCanTransition_NamedIllegalCases names the specific illegal cases
// 02 §3 calls out or that are easy to get backwards accidentally.
func TestCanTransition_NamedIllegalCases(t *testing.T) {
	t.Run("reconciliation_pending never returns to reserved", func(t *testing.T) {
		if err := CanTransition(ReservationReconciliationPending, ReservationReserved); !errors.Is(err, ErrIllegalTransition) {
			t.Fatalf("error = %v, want ErrIllegalTransition", err)
		}
	})

	terminals := []ReservationState{ReservationSettled, ReservationReleased, ReservationUnknownConsumption}
	for _, term := range terminals {
		for _, to := range allReservationStates {
			t.Run(string(term)+" has no outgoing edges", func(t *testing.T) {
				if err := CanTransition(term, to); !errors.Is(err, ErrIllegalTransition) {
					t.Fatalf("CanTransition(%s, %s) = %v, want ErrIllegalTransition (terminal state)", term, to, err)
				}
			})
		}
	}

	for _, s := range allReservationStates {
		t.Run(string(s)+" self-edge is illegal", func(t *testing.T) {
			if err := CanTransition(s, s); !errors.Is(err, ErrIllegalTransition) {
				t.Fatalf("CanTransition(%s, %s) = %v, want ErrIllegalTransition (self-edge)", s, s, err)
			}
		})
	}
}

func TestAllocationStateFor(t *testing.T) {
	tests := []struct {
		reservation ReservationState
		want        AllocationState
	}{
		{ReservationReserved, AllocationReserved},
		{ReservationReconciliationPending, AllocationReserved},
		{ReservationSettled, AllocationSettled},
		{ReservationReleased, AllocationReleased},
		{ReservationUnknownConsumption, AllocationUnknownConsumption},
	}
	for _, tc := range tests {
		got, err := AllocationStateFor(tc.reservation)
		if err != nil {
			t.Fatalf("AllocationStateFor(%s): %v, want success", tc.reservation, err)
		}
		if got != tc.want {
			t.Fatalf("AllocationStateFor(%s) = %s, want %s", tc.reservation, got, tc.want)
		}
	}

	if _, err := AllocationStateFor(ReservationState("bogus")); !errors.Is(err, ErrUnknownReservationState) {
		t.Fatalf("AllocationStateFor(bogus) error = %v, want ErrUnknownReservationState", err)
	}
}

func TestParseReservationState_FailClosed(t *testing.T) {
	for _, s := range allReservationStates {
		got, err := ParseReservationState(string(s))
		if err != nil {
			t.Fatalf("ParseReservationState(%q): %v, want success", s, err)
		}
		if got != s {
			t.Fatalf("ParseReservationState(%q) = %q, want %q", s, got, s)
		}
	}
	for _, bad := range []string{"expired", "", "Reserved", "reconciliationpending"} {
		got, err := ParseReservationState(bad)
		if !errors.Is(err, ErrUnknownReservationState) {
			t.Fatalf("ParseReservationState(%q) error = %v, want ErrUnknownReservationState", bad, err)
		}
		if got != "" {
			t.Fatalf("ParseReservationState(%q) = %q, want zero value", bad, got)
		}
	}
}

func TestParseAllocationState_FailClosed(t *testing.T) {
	legal := []AllocationState{AllocationReserved, AllocationSettled, AllocationReleased, AllocationUnknownConsumption}
	for _, s := range legal {
		got, err := ParseAllocationState(string(s))
		if err != nil {
			t.Fatalf("ParseAllocationState(%q): %v, want success", s, err)
		}
		if got != s {
			t.Fatalf("ParseAllocationState(%q) = %q, want %q", s, got, s)
		}
	}
	// reconciliation_pending is a RESERVATION state, never a valid
	// allocation state — it must be rejected here.
	for _, bad := range []string{"reconciliation_pending", "", "Reserved", "expired"} {
		got, err := ParseAllocationState(bad)
		if !errors.Is(err, ErrUnknownAllocationState) {
			t.Fatalf("ParseAllocationState(%q) error = %v, want ErrUnknownAllocationState", bad, err)
		}
		if got != "" {
			t.Fatalf("ParseAllocationState(%q) = %q, want zero value", bad, got)
		}
	}
}

func TestIsTerminalReservationState(t *testing.T) {
	tests := []struct {
		state ReservationState
		want  bool
	}{
		{ReservationReserved, false},
		{ReservationReconciliationPending, false},
		{ReservationSettled, true},
		{ReservationReleased, true},
		{ReservationUnknownConsumption, true},
	}
	for _, tc := range tests {
		if got := IsTerminalReservationState(tc.state); got != tc.want {
			t.Fatalf("IsTerminalReservationState(%s) = %v, want %v", tc.state, got, tc.want)
		}
	}
}
