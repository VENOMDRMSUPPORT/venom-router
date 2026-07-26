package models

import (
	"errors"
	"strings"
	"testing"
	"time"
)

var certFixedNow = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func TestCertificationStates_ExactlySix(t *testing.T) {
	states := CertificationStates()
	if len(states) != 6 {
		t.Fatalf("CertificationStates() has %d entries, want exactly 6: %v", len(states), states)
	}
	for _, s := range states {
		if string(s) == "rejected" || string(s) == "catalog_only" {
			t.Fatalf("CertificationStates() must never contain %q", s)
		}
	}
}

func TestParseCertificationState_RejectsRejectedAndCatalogOnly(t *testing.T) {
	if _, err := ParseCertificationState("rejected"); !errors.Is(err, ErrUnknownCertificationState) {
		t.Fatalf(`ParseCertificationState("rejected") error = %v, want ErrUnknownCertificationState`, err)
	}
	if _, err := ParseCertificationState("catalog_only"); !errors.Is(err, ErrUnknownCertificationState) {
		t.Fatalf(`ParseCertificationState("catalog_only") error = %v, want ErrUnknownCertificationState`, err)
	}
}

func TestParseCertificationState_AcceptsAllSix(t *testing.T) {
	for _, s := range []string{"discovered", "observed", "probing", "certified", "suspended", "expired"} {
		if _, err := ParseCertificationState(s); err != nil {
			t.Fatalf("ParseCertificationState(%q): unexpected error: %v", s, err)
		}
	}
}

// TestPackageNeverMentionsRejected is a static grep-style guard: the
// string "rejected" (the certification-state sense) must not appear
// anywhere in this package's identifiers/constants, since 04 §5 states the
// state does not exist. This is intentionally coarse; it only scans this
// test's own knowledge of the enum, not the whole file, to avoid false
// positives on unrelated words.
func TestPackageNeverMentionsRejected(t *testing.T) {
	for _, s := range CertificationStates() {
		if strings.Contains(string(s), "reject") {
			t.Fatalf("CertificationStates() contains a state referencing 'reject': %s", s)
		}
	}
}

func TestParseCapabilityTruth_AcceptsThreeValues(t *testing.T) {
	for _, s := range []string{"unknown", "supported", "unsupported"} {
		if _, err := ParseCapabilityTruth(s); err != nil {
			t.Fatalf("ParseCapabilityTruth(%q): unexpected error: %v", s, err)
		}
	}
}

func TestParseCapabilityTruth_RejectsUnrecognized(t *testing.T) {
	if _, err := ParseCapabilityTruth("maybe"); !errors.Is(err, ErrUnknownCapabilityTruth) {
		t.Fatalf("ParseCapabilityTruth(maybe) error = %v, want ErrUnknownCapabilityTruth", err)
	}
}

// legalCertEdges is the ground truth used both to assert the 10 legal
// edges succeed and, by exclusion, that every other (from, to) pair in the
// full 6x6 Cartesian product is rejected.
var legalCertEdges = map[[2]CertificationState]bool{
	{CertDiscovered, CertObserved}: true,
	{CertObserved, CertProbing}:    true,
	{CertProbing, CertProbing}:     true,
	{CertProbing, CertCertified}:   true,
	{CertProbing, CertSuspended}:   true,
	{CertCertified, CertSuspended}: true,
	{CertSuspended, CertCertified}: true,
	{CertSuspended, CertProbing}:   true,
	{CertCertified, CertExpired}:   true,
	{CertExpired, CertProbing}:     true,
}

func TestCertificationTransition_FullCartesian(t *testing.T) {
	states := CertificationStates()
	if len(legalCertEdges) != 10 {
		t.Fatalf("test fixture error: legalCertEdges has %d entries, want 10", len(legalCertEdges))
	}

	total := 0
	legalCount := 0
	for _, from := range states {
		for _, to := range states {
			total++
			legal := legalCertEdges[[2]CertificationState{from, to}]
			if legal {
				legalCount++
			}

			c := certForEdge(from, to)
			retry := RetryPolicy{Attempts: 1, Budget: 3}

			got, err := c.Transition(to, resolvedVerdictFor(from, to), retry, certFixedNow)

			if legal {
				if err != nil {
					t.Errorf("Transition(%s -> %s): unexpected error: %v", from, to, err)
				}
				if got.State != to {
					t.Errorf("Transition(%s -> %s): State = %s, want %s", from, to, got.State, to)
				}
			} else {
				if err == nil {
					t.Errorf("Transition(%s -> %s): expected rejection, got success", from, to)
					continue
				}
				if got != c {
					t.Errorf("Transition(%s -> %s): rejected transition mutated the record: got %+v, want unchanged %+v", from, to, got, c)
				}
			}
		}
	}

	if legalCount != 10 {
		t.Fatalf("test fixture error: only %d of 36 pairs marked legal, want 10", legalCount)
	}
	if total != 36 {
		t.Fatalf("test fixture error: %d total pairs, want 36 (6x6)", total)
	}
}

// certForEdge builds a starting Certification whose State is from and
// whose Truth is set up so this specific edge's guard should be
// satisfiable if the edge is legal (e.g. a resolved Truth for
// suspended -> certified's "prior verdict still valid" guard).
func certForEdge(from, to CertificationState) Certification {
	c := Certification{State: from, Truth: TruthUnknown, Version: 1}
	if from == CertSuspended && to == CertCertified {
		c.Truth = TruthSupported
	}
	return c
}

// resolvedVerdictFor returns the verdict argument Transition should be
// called with for edges that consume one (probing -> certified); it is
// ignored by every other edge.
func resolvedVerdictFor(from, to CertificationState) CapabilityTruth {
	if from == CertProbing && to == CertCertified {
		return TruthSupported
	}
	return TruthUnknown
}

func TestCertificationTransition_ProbingToCertifiedRequiresResolvedVerdict(t *testing.T) {
	c := Certification{State: CertProbing, Truth: TruthUnknown}

	for _, verdict := range []CapabilityTruth{TruthSupported, TruthUnsupported} {
		got, err := c.Transition(CertCertified, verdict, RetryPolicy{Attempts: 1, Budget: 3}, certFixedNow)
		if err != nil {
			t.Fatalf("Transition(probing -> certified, verdict=%s): unexpected error: %v", verdict, err)
		}
		if got.Truth != verdict {
			t.Fatalf("Transition(probing -> certified, verdict=%s): Truth = %s, want %s", verdict, got.Truth, verdict)
		}
	}

	got, err := c.Transition(CertCertified, TruthUnknown, RetryPolicy{Attempts: 1, Budget: 3}, certFixedNow)
	if !errors.Is(err, ErrVerdictRequired) {
		t.Fatalf("Transition(probing -> certified, verdict=unknown) error = %v, want ErrVerdictRequired", err)
	}
	if got != c {
		t.Fatalf("rejected transition mutated the record: got %+v, want unchanged %+v", got, c)
	}
}

func TestCertificationTransition_SuspendedToCertifiedRequiresValidPriorVerdict(t *testing.T) {
	c := Certification{State: CertSuspended, Truth: TruthUnknown}
	got, err := c.Transition(CertCertified, TruthUnknown, RetryPolicy{}, certFixedNow)
	if !errors.Is(err, ErrNoValidVerdict) {
		t.Fatalf("Transition(suspended -> certified, no prior verdict) error = %v, want ErrNoValidVerdict", err)
	}
	if got != c {
		t.Fatalf("rejected transition mutated the record: got %+v, want unchanged %+v", got, c)
	}

	resolved := Certification{State: CertSuspended, Truth: TruthSupported}
	got2, err := resolved.Transition(CertCertified, TruthUnknown, RetryPolicy{}, certFixedNow)
	if err != nil {
		t.Fatalf("Transition(suspended -> certified, valid prior verdict): unexpected error: %v", err)
	}
	if got2.State != CertCertified {
		t.Fatalf("State = %s, want certified", got2.State)
	}
}

func TestCertificationTransition_ProbingSelfLoopRespectsRetryBudget(t *testing.T) {
	c := Certification{State: CertProbing, Truth: TruthUnknown}

	got, err := c.Transition(CertProbing, TruthUnknown, RetryPolicy{Attempts: 3, Budget: 3}, certFixedNow)
	if err != nil {
		t.Fatalf("Transition(probing -> probing, within budget): unexpected error: %v", err)
	}
	if got.State != CertProbing {
		t.Fatalf("State = %s, want probing", got.State)
	}

	got2, err := c.Transition(CertProbing, TruthUnknown, RetryPolicy{Attempts: 4, Budget: 3}, certFixedNow)
	if !errors.Is(err, ErrRetryBudgetExceeded) {
		t.Fatalf("Transition(probing -> probing, over budget) error = %v, want ErrRetryBudgetExceeded", err)
	}
	if got2 != c {
		t.Fatalf("rejected transition mutated the record: got %+v, want unchanged %+v", got2, c)
	}
}

// TestCertificationTransition_TruthNotMutatedByEdges3_5_6 proves edges
// probing->probing, probing->suspended, and certified->suspended never
// touch Truth as a side effect — it is carried forward unchanged
// regardless of the verdict argument passed in.
func TestCertificationTransition_TruthNotMutatedByEdges3_5_6(t *testing.T) {
	cases := []struct {
		name string
		from CertificationState
		to   CertificationState
	}{
		{"probing->probing", CertProbing, CertProbing},
		{"probing->suspended", CertProbing, CertSuspended},
		{"certified->suspended", CertCertified, CertSuspended},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Certification{State: tc.from, Truth: TruthUnknown}
			got, err := c.Transition(tc.to, TruthSupported, RetryPolicy{Attempts: 1, Budget: 3}, certFixedNow)
			if err != nil {
				t.Fatalf("Transition(%s): unexpected error: %v", tc.name, err)
			}
			if got.Truth != TruthUnknown {
				t.Fatalf("Transition(%s): Truth = %s, want unchanged unknown (verdict arg must be ignored)", tc.name, got.Truth)
			}
		})
	}
}

func TestRoutable_ExactlyOneOfEighteenCombinations(t *testing.T) {
	states := CertificationStates()
	truths := []CapabilityTruth{TruthUnknown, TruthSupported, TruthUnsupported}

	total := 0
	routableCount := 0
	for _, s := range states {
		for _, tr := range truths {
			total++
			got := Routable(s, tr)
			if s == CertCertified && tr == TruthSupported {
				routableCount++
				if !got {
					t.Errorf("Routable(%s, %s) = false, want true", s, tr)
				}
			} else if got {
				t.Errorf("Routable(%s, %s) = true, want false", s, tr)
			}
		}
	}

	if total != 18 {
		t.Fatalf("test fixture error: %d total combinations, want 18 (6x3)", total)
	}
	if routableCount != 1 {
		t.Fatalf("test fixture error: %d combinations marked routable, want exactly 1", routableCount)
	}
}
