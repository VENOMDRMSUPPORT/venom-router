package quota

import (
	"errors"
	"math/rand"
	"reflect"
	"testing"
	"time"
)

// TestReservationID_DeterministicAndInjective proves ReservationID (02 §3:
// "reservation_id = f(request_id, attempt_id)") is a pure, deterministic,
// injective function of its two fields, and fails closed on an empty
// field. The golden literal below was computed once, independently of
// this package (a length-prefixed SHA-256 over "req-1"/"attempt-1"
// verified against a standalone Go program), and pasted here — it is
// never derived by calling ReservationID itself, so a regression that
// changes the hash's shape cannot silently rewrite its own oracle.
func TestReservationID_DeterministicAndInjective(t *testing.T) {
	const golden = "89ef05b0e77982dcf5b3f60b7458e4e06c1a22eacd07151e5f3ecf3c0b905179"

	first, err := ReservationID("req-1", "attempt-1")
	if err != nil {
		t.Fatalf("ReservationID: %v", err)
	}
	if first != golden {
		t.Fatalf("ReservationID(\"req-1\",\"attempt-1\") = %q, want golden %q", first, golden)
	}

	for i := 0; i < 100; i++ {
		got, err := ReservationID("req-1", "attempt-1")
		if err != nil {
			t.Fatalf("ReservationID iteration %d: %v", i, err)
		}
		if got != first {
			t.Fatalf("ReservationID iteration %d = %q, want %q (must be deterministic)", i, got, first)
		}
	}

	// Injectivity: a naive delimiter-free concatenation would confuse
	// ("a","bc") with ("ab","c"). The length-prefix must keep them distinct.
	idAB, err := ReservationID("a", "bc")
	if err != nil {
		t.Fatalf("ReservationID(\"a\",\"bc\"): %v", err)
	}
	idBA, err := ReservationID("ab", "c")
	if err != nil {
		t.Fatalf("ReservationID(\"ab\",\"c\"): %v", err)
	}
	if idAB == idBA {
		t.Fatalf("ReservationID(\"a\",\"bc\") == ReservationID(\"ab\",\"c\") = %q, want distinct ids", idAB)
	}

	for _, tc := range []struct{ requestID, attemptID string }{
		{"", "attempt-1"},
		{"req-1", ""},
		{"", ""},
	} {
		if _, err := ReservationID(tc.requestID, tc.attemptID); !errors.Is(err, ErrInvalidReservationIdentity) {
			t.Fatalf("ReservationID(%q,%q) error = %v, want ErrInvalidReservationIdentity", tc.requestID, tc.attemptID, err)
		}
	}
}

func windowFixture(id string, unit Unit, remaining, limitValue *float64, version int64) Window {
	return Window{
		ID:         id,
		AccountID:  "acct-fixture",
		Source:     SourceProviderEvidence,
		Unit:       unit,
		WindowType: "test",
		Key:        "test:" + id,
		Remaining:  remaining,
		LimitValue: limitValue,
		Version:    version,
		Confidence: 1,
		Freshness:  FreshnessFresh,
		ObservedAt: time.Now(),
	}
}

func TestApplicableDebits_MatchesEveryWindowSharingTheUnit(t *testing.T) {
	windows := []Window{
		windowFixture("win-rpm", UnitRequests, float64Ptr(100), nil, 5),
		windowFixture("win-local-safety", UnitRequests, nil, float64Ptr(50), 3),
		windowFixture("win-input-tokens", UnitInputTokens, float64Ptr(1000), nil, 1),
	}
	allocations := []Allocation{
		{Unit: UnitRequests, Cost: 1, Source: EstimateSourceFromRequest},
	}

	got, err := ApplicableDebits(windows, allocations)
	if err != nil {
		t.Fatalf("ApplicableDebits: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(debits) = %d, want 2 (both requests windows)", len(got))
	}

	byID := map[string]WindowDebit{}
	for _, d := range got {
		byID[d.WindowID] = d
	}
	rpm, ok := byID["win-rpm"]
	if !ok {
		t.Fatalf("win-rpm not debited: %+v", got)
	}
	if rpm.Cost != 1 || rpm.Unit != UnitRequests || rpm.EstimateSource != EstimateSourceFromRequest || rpm.ExpectedVersion != 5 {
		t.Fatalf("win-rpm debit = %+v, want cost=1 unit=requests source=from_request version=5", rpm)
	}
	local, ok := byID["win-local-safety"]
	if !ok {
		t.Fatalf("win-local-safety not debited: %+v", got)
	}
	if local.Cost != 1 || local.ExpectedVersion != 3 {
		t.Fatalf("win-local-safety debit = %+v, want cost=1 version=3", local)
	}
	if _, ok := byID["win-input-tokens"]; ok {
		t.Fatalf("win-input-tokens (different unit) was debited, want excluded: %+v", got)
	}
}

func TestApplicableDebits_ExcludesUnknownCapacityWindows(t *testing.T) {
	windows := []Window{
		windowFixture("win-unknown", UnitRequests, nil, nil, 1),
		windowFixture("win-local-safety", UnitRequests, nil, float64Ptr(10), 1),
	}
	allocations := []Allocation{{Unit: UnitRequests, Cost: 1, Source: EstimateSourceFromRequest}}

	got, err := ApplicableDebits(windows, allocations)
	if err != nil {
		t.Fatalf("ApplicableDebits: %v", err)
	}
	for _, d := range got {
		if d.WindowID == "win-unknown" {
			t.Fatalf("unknown-capacity window was debited: %+v", got)
		}
	}
	if len(got) != 1 || got[0].WindowID != "win-local-safety" {
		t.Fatalf("debits = %+v, want exactly [win-local-safety]", got)
	}
}

func TestApplicableDebits_EmptySetIsAnError(t *testing.T) {
	t.Run("no window matches the allocation's unit", func(t *testing.T) {
		windows := []Window{windowFixture("win-tokens", UnitInputTokens, float64Ptr(100), nil, 1)}
		allocations := []Allocation{{Unit: UnitRequests, Cost: 1, Source: EstimateSourceFromRequest}}
		got, err := ApplicableDebits(windows, allocations)
		if !errors.Is(err, ErrNoApplicableWindow) {
			t.Fatalf("error = %v, want ErrNoApplicableWindow", err)
		}
		if got != nil {
			t.Fatalf("debits = %+v, want nil", got)
		}
	})

	t.Run("the only matching window has unknown capacity", func(t *testing.T) {
		windows := []Window{windowFixture("win-unknown", UnitRequests, nil, nil, 1)}
		allocations := []Allocation{{Unit: UnitRequests, Cost: 1, Source: EstimateSourceFromRequest}}
		got, err := ApplicableDebits(windows, allocations)
		if !errors.Is(err, ErrNoApplicableWindow) {
			t.Fatalf("error = %v, want ErrNoApplicableWindow", err)
		}
		if got != nil {
			t.Fatalf("debits = %+v, want nil", got)
		}
	})

	t.Run("a single applicable window succeeds", func(t *testing.T) {
		windows := []Window{windowFixture("win-ok", UnitRequests, float64Ptr(100), nil, 1)}
		allocations := []Allocation{{Unit: UnitRequests, Cost: 1, Source: EstimateSourceFromRequest}}
		got, err := ApplicableDebits(windows, allocations)
		if err != nil {
			t.Fatalf("ApplicableDebits: %v, want success", err)
		}
		if len(got) != 1 {
			t.Fatalf("debits = %+v, want exactly 1", got)
		}
	})
}

func TestApplicableDebits_IsDeterministic(t *testing.T) {
	windows := []Window{
		windowFixture("win-c", UnitRequests, float64Ptr(10), nil, 1),
		windowFixture("win-a", UnitRequests, float64Ptr(20), nil, 2),
		windowFixture("win-b", UnitRequests, float64Ptr(30), nil, 3),
	}
	allocations := []Allocation{{Unit: UnitRequests, Cost: 1, Source: EstimateSourceFromRequest}}

	first, err := ApplicableDebits(windows, allocations)
	if err != nil {
		t.Fatalf("ApplicableDebits: %v", err)
	}
	for i := 0; i < 50; i++ {
		got, err := ApplicableDebits(windows, allocations)
		if err != nil {
			t.Fatalf("ApplicableDebits iteration %d: %v", i, err)
		}
		if !reflect.DeepEqual(got, first) {
			t.Fatalf("ApplicableDebits iteration %d = %+v, want byte-identical %+v", i, got, first)
		}
	}

	// A shuffled input window order must produce the same output order.
	shuffled := make([]Window, len(windows))
	copy(shuffled, windows)
	rng := rand.New(rand.NewSource(1))
	rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })

	gotShuffled, err := ApplicableDebits(shuffled, allocations)
	if err != nil {
		t.Fatalf("ApplicableDebits(shuffled): %v", err)
	}
	if !reflect.DeepEqual(gotShuffled, first) {
		t.Fatalf("ApplicableDebits(shuffled input) = %+v, want the same canonical order %+v", gotShuffled, first)
	}
}
