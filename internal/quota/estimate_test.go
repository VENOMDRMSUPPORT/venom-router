package quota

import (
	"errors"
	"reflect"
	"testing"
)

func TestEstimate_CanonicalDimensions(t *testing.T) {
	in := EstimateInput{InputTokens: intPtr(120), MaxOutputTokens: intPtr(300)}

	got, err := Estimate(in, DefaultEstimatePolicy())
	if err != nil {
		t.Fatalf("Estimate(): %v", err)
	}

	want := []Allocation{
		{Unit: UnitRequests, Cost: 1, Source: EstimateSourceFromRequest},
		{Unit: UnitConcurrency, Cost: 1, Source: EstimateSourceFromRequest},
		{Unit: UnitInputTokens, Cost: 120, Source: EstimateSourceFromRequest},
		{Unit: UnitOutputTokens, Cost: 300, Source: EstimateSourceFromRequest},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Estimate() = %+v, want %+v (exact order and contents)", got, want)
	}
}

func TestEstimate_OutputTokensFallsBackToPolicyDefault(t *testing.T) {
	pol := DefaultEstimatePolicy()

	t.Run("supplied max_tokens wins", func(t *testing.T) {
		got, err := Estimate(EstimateInput{MaxOutputTokens: intPtr(777)}, pol)
		if err != nil {
			t.Fatalf("Estimate(): %v", err)
		}
		out := lastAllocation(t, got, UnitOutputTokens)
		if out.Cost != 777 || out.Source != EstimateSourceFromRequest {
			t.Fatalf("output_tokens = %+v, want cost=777 source=from_request", out)
		}
	})

	t.Run("nil max_tokens falls back to policy default", func(t *testing.T) {
		got, err := Estimate(EstimateInput{}, pol)
		if err != nil {
			t.Fatalf("Estimate(): %v", err)
		}
		out := lastAllocation(t, got, UnitOutputTokens)
		if out.Cost != pol.DefaultOutputTokens || out.Source != EstimateSourcePolicyDefault {
			t.Fatalf("output_tokens = %+v, want cost=%v source=policy_default", out, pol.DefaultOutputTokens)
		}
	})
}

func TestEstimate_UnknownInputTokensIsNotZero(t *testing.T) {
	got, err := Estimate(EstimateInput{MaxOutputTokens: intPtr(100)}, DefaultEstimatePolicy())
	if err != nil {
		t.Fatalf("Estimate(): %v", err)
	}
	for _, a := range got {
		if a.Unit == UnitInputTokens {
			t.Fatalf("found an input_tokens allocation %+v when InputTokens was nil, want none (unknown is not zero)", a)
		}
	}
}

func TestEstimate_NoCreditConversionWithoutVerifiedRule(t *testing.T) {
	base := EstimateInput{InputTokens: intPtr(120), MaxOutputTokens: intPtr(300)}
	pol := DefaultEstimatePolicy()

	negatives := []struct {
		name       string
		conversion *CreditConversionRule
	}{
		{"nil rule", nil},
		{"unverified rule", &CreditConversionRule{Unit: UnitCredits, CreditsPerToken: 0.01, Verified: false, RuleID: "r-unverified"}},
		{"verified rule on the wrong unit", &CreditConversionRule{Unit: UnitRequests, CreditsPerToken: 0.01, Verified: true, RuleID: "r-wrong-unit"}},
		{"verified rule with zero rate", &CreditConversionRule{Unit: UnitCredits, CreditsPerToken: 0, Verified: true, RuleID: "r-zero-rate"}},
	}
	for _, tc := range negatives {
		t.Run(tc.name, func(t *testing.T) {
			in := base
			in.Conversion = tc.conversion
			got, err := Estimate(in, pol)
			if err != nil {
				t.Fatalf("Estimate(): %v", err)
			}
			for _, a := range got {
				if a.Unit == UnitCredits || a.Unit == UnitBalance {
					t.Fatalf("found a credit allocation %+v, want none", a)
				}
			}
		})
	}

	// Positive control: a verified rule with a positive rate on
	// UnitCredits produces exactly one credit allocation, tagged
	// provider_conversion, with the arithmetically correct cost.
	t.Run("verified rule on UnitCredits produces exactly one allocation", func(t *testing.T) {
		in := base
		in.Conversion = &CreditConversionRule{Unit: UnitCredits, CreditsPerToken: 0.01, Verified: true, RuleID: "r-ok"}
		got, err := Estimate(in, pol)
		if err != nil {
			t.Fatalf("Estimate(): %v", err)
		}
		var credits []Allocation
		for _, a := range got {
			if a.Unit == UnitCredits {
				credits = append(credits, a)
			}
		}
		if len(credits) != 1 {
			t.Fatalf("credit allocations = %+v, want exactly 1", credits)
		}
		wantCost := (120.0 + 300.0) * 0.01
		if credits[0].Cost != wantCost {
			t.Fatalf("credit cost = %v, want %v ((input+output)*rate)", credits[0].Cost, wantCost)
		}
		if credits[0].Source != EstimateSourceProviderConversion {
			t.Fatalf("credit source = %q, want %q", credits[0].Source, EstimateSourceProviderConversion)
		}
	})
}

func TestEstimate_IsDeterministic(t *testing.T) {
	in := EstimateInput{
		InputTokens:     intPtr(50),
		MaxOutputTokens: intPtr(200),
		Conversion:      &CreditConversionRule{Unit: UnitCredits, CreditsPerToken: 0.02, Verified: true, RuleID: "r-det"},
	}
	pol := DefaultEstimatePolicy()

	first, err := Estimate(in, pol)
	if err != nil {
		t.Fatalf("Estimate(): %v", err)
	}
	for i := 0; i < 50; i++ {
		got, err := Estimate(in, pol)
		if err != nil {
			t.Fatalf("Estimate() iteration %d: %v", i, err)
		}
		if !reflect.DeepEqual(got, first) {
			t.Fatalf("Estimate() iteration %d = %+v, want byte-identical %+v", i, got, first)
		}
	}
}

func TestParseEstimateSource_FailClosed(t *testing.T) {
	for _, s := range []EstimateSource{EstimateSourceFromRequest, EstimateSourceProviderConversion, EstimateSourcePolicyDefault} {
		got, err := ParseEstimateSource(string(s))
		if err != nil {
			t.Fatalf("ParseEstimateSource(%q): %v, want success", s, err)
		}
		if got != s {
			t.Fatalf("ParseEstimateSource(%q) = %q, want %q", s, got, s)
		}
	}
	for _, bad := range []string{"unknown", "", "From_Request"} {
		got, err := ParseEstimateSource(bad)
		if !errors.Is(err, ErrUnknownEstimateSource) {
			t.Fatalf("ParseEstimateSource(%q) error = %v, want ErrUnknownEstimateSource", bad, err)
		}
		if got != "" {
			t.Fatalf("ParseEstimateSource(%q) = %q, want zero value", bad, got)
		}
	}
}

func TestEstimate_RejectsInvalidPolicyAndNegativeCounts(t *testing.T) {
	t.Run("valid input succeeds", func(t *testing.T) {
		if _, err := Estimate(EstimateInput{InputTokens: intPtr(1)}, DefaultEstimatePolicy()); err != nil {
			t.Fatalf("Estimate(): %v, want success", err)
		}
	})
	t.Run("non-positive policy default is rejected", func(t *testing.T) {
		_, err := Estimate(EstimateInput{}, EstimatePolicy{DefaultOutputTokens: 0})
		if !errors.Is(err, ErrInvalidEstimatePolicy) {
			t.Fatalf("Estimate() error = %v, want ErrInvalidEstimatePolicy", err)
		}
	})
	t.Run("negative input tokens is rejected", func(t *testing.T) {
		_, err := Estimate(EstimateInput{InputTokens: intPtr(-1)}, DefaultEstimatePolicy())
		if !errors.Is(err, ErrInvalidEstimateInput) {
			t.Fatalf("Estimate() error = %v, want ErrInvalidEstimateInput", err)
		}
	})
	t.Run("negative max output tokens is rejected", func(t *testing.T) {
		_, err := Estimate(EstimateInput{MaxOutputTokens: intPtr(-1)}, DefaultEstimatePolicy())
		if !errors.Is(err, ErrInvalidEstimateInput) {
			t.Fatalf("Estimate() error = %v, want ErrInvalidEstimateInput", err)
		}
	})
}

func lastAllocation(t *testing.T, allocations []Allocation, unit Unit) Allocation {
	t.Helper()
	for i := len(allocations) - 1; i >= 0; i-- {
		if allocations[i].Unit == unit {
			return allocations[i]
		}
	}
	t.Fatalf("no allocation found for unit %q in %+v", unit, allocations)
	return Allocation{}
}
