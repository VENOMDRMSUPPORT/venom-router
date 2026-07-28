package routing

import (
	"errors"
	"testing"

	accountsdomain "github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
)

// litePolicyCopy returns a copy of the real Lite TierPolicy for gate tests.
func litePolicyCopy(t *testing.T) TierPolicy {
	t.Helper()
	ps, err := Policies()
	if err != nil {
		t.Fatalf("Policies() error = %v", err)
	}
	return ps[TierLite]
}

// proPolicyCopy returns a copy of the real Pro TierPolicy.
func proPolicyCopy(t *testing.T) TierPolicy {
	t.Helper()
	ps, err := Policies()
	if err != nil {
		t.Fatalf("Policies() error = %v", err)
	}
	return ps[TierPro]
}

// eligibleCandidate builds a CandidateOffering that passes all three gates
// for a given funding and context ceiling under the Lite policy. The chat
// operation is certified+supported to satisfy capability requirements.
func eligibleCandidate(funding accountsdomain.Funding, contextTokens int64) CandidateOffering {
	return CandidateOffering{
		ProviderID:            "p1",
		AccountID:             "a1",
		ProviderModelID:       "m1",
		Funding:               funding,
		AccountHealth:         accountsdomain.HealthHealthy,
		CredentialValid:       true,
		VerifiedContextTokens: &contextTokens,
		Certifications: map[models.Operation]models.Certification{
			models.OperationChat: {
				State: models.CertCertified,
				Truth: models.TruthSupported,
			},
			models.OperationStreaming: {
				State: models.CertCertified,
				Truth: models.TruthSupported,
			},
			models.OperationTools: {
				State: models.CertCertified,
				Truth: models.TruthSupported,
			},
			models.OperationStructuredOutput: {
				State: models.CertCertified,
				Truth: models.TruthSupported,
			},
			models.OperationVision: {
				State: models.CertCertified,
				Truth: models.TruthSupported,
			},
		},
		ReasoningCertified:    true,
		ReasoningCertifiedMax: ThinkingUltra,
	}
}

// TestApplyHardGates_ContextExceedsTierCeiling verifies the request-level
// ceiling check short-circuits before any candidate is evaluated.
//
// Mutation row HG-M1: remove the S>ceiling check → candidates are evaluated
// instead of returning ErrContextExceedsTier → restore.
func TestApplyHardGates_ContextExceedsTierCeiling(t *testing.T) {
	policy := litePolicyCopy(t)

	// Build candidates that would all be eligible — proving the short-circuit
	// fires before candidates are touched.
	c := eligibleCandidate(accountsdomain.FundingFree, policy.ContextCeilingTokens)
	candidates := []CandidateOffering{c, c, c}

	reqs := Requirements{
		ContextTokens: policy.ContextCeilingTokens + 1,
	}

	eligible, excl, err := ApplyHardGates(candidates, reqs, policy)
	if !errors.Is(err, ErrContextExceedsTier) {
		t.Fatalf("err = %v, want ErrContextExceedsTier", err)
	}
	if eligible != nil {
		t.Fatalf("eligible = %v, want nil", eligible)
	}
	if excl != nil {
		t.Fatalf("excluded = %v, want nil", excl)
	}
}

// TestApplyHardGates_FundingGate_Lite checks the categorical exclusion
// table for Lite: only Free passes; Paid and Unknown are excluded.
//
// Mutation row HG-M2: allow Paid through for Lite → the Paid case passes
// instead of being excluded → restore.
func TestApplyHardGates_FundingGate_Lite(t *testing.T) {
	policy := litePolicyCopy(t)
	const ctx = int64(1000)
	reqs := Requirements{ContextTokens: ctx}

	tests := []struct {
		funding accountsdomain.Funding
		want    int // expected eligible count
	}{
		{accountsdomain.FundingFree, 1},
		{accountsdomain.FundingPaid, 0},
		{accountsdomain.FundingUnknown, 0},
	}

	for _, tc := range tests {
		c := eligibleCandidate(tc.funding, ctx)
		eligible, excl, err := ApplyHardGates([]CandidateOffering{c}, reqs, policy)
		if err != nil {
			t.Fatalf("funding %q: unexpected error: %v", tc.funding, err)
		}
		if len(eligible) != tc.want {
			t.Fatalf("funding %q: eligible = %d, want %d", tc.funding, len(eligible), tc.want)
		}
		if tc.want == 0 {
			reasons, ok := excl[0]
			if !ok {
				t.Fatalf("funding %q: index 0 missing from excluded map", tc.funding)
			}
			found := false
			for _, r := range reasons {
				if r == ReasonFundingIneligible {
					found = true
				}
			}
			if !found {
				t.Fatalf("funding %q: reasons = %v, want %q", tc.funding, reasons, ReasonFundingIneligible)
			}
		}
	}
}

// TestApplyHardGates_FundingGate_Pro checks the Pro/Max rule: Free and
// Paid pass, Unknown is excluded.
func TestApplyHardGates_FundingGate_Pro(t *testing.T) {
	policy := proPolicyCopy(t)
	const ctx = int64(1000)
	reqs := Requirements{ContextTokens: ctx}

	tests := []struct {
		funding accountsdomain.Funding
		want    int
	}{
		{accountsdomain.FundingFree, 1},
		{accountsdomain.FundingPaid, 1},
		{accountsdomain.FundingUnknown, 0},
	}

	for _, tc := range tests {
		c := eligibleCandidate(tc.funding, ctx)
		eligible, excl, err := ApplyHardGates([]CandidateOffering{c}, reqs, policy)
		if err != nil {
			t.Fatalf("funding %q: unexpected error: %v", tc.funding, err)
		}
		if len(eligible) != tc.want {
			t.Fatalf("funding %q: eligible = %d, want %d", tc.funding, len(eligible), tc.want)
		}
		if tc.want == 0 {
			reasons := excl[0]
			found := false
			for _, r := range reasons {
				if r == ReasonFundingIneligible {
					found = true
				}
			}
			if !found {
				t.Fatalf("funding %q: reasons = %v, want %q", tc.funding, reasons, ReasonFundingIneligible)
			}
		}
	}
}

// TestApplyHardGates_ContextGate_Boundary verifies the boundary semantics:
// verified context >= S passes, below S is excluded, nil is excluded.
//
// Mutation row HG-M3: change < to <= → the "exactly at S" case is excluded
// instead of passing → restore.
func TestApplyHardGates_ContextGate_Boundary(t *testing.T) {
	policy := litePolicyCopy(t)
	const S = int64(50000)
	reqs := Requirements{ContextTokens: S}

	tests := []struct {
		name   string
		ctxPtr *int64
		wantIn bool
	}{
		{"nil context", nil, false},
		{"below S", int64Ptr(S - 1), false},
		{"exactly at S (boundary, passes)", int64Ptr(S), true},
		{"above S", int64Ptr(S + 1), true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := eligibleCandidate(accountsdomain.FundingFree, S+1) // set a large value; overwrite below
			c.VerifiedContextTokens = tc.ctxPtr
			eligible, excl, err := ApplyHardGates([]CandidateOffering{c}, reqs, policy)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantIn && len(eligible) != 1 {
				t.Fatalf("want eligible, got %d candidates; excluded = %v", len(eligible), excl)
			}
			if !tc.wantIn && len(eligible) != 0 {
				t.Fatalf("want excluded, got %d eligible candidates", len(eligible))
			}
			if !tc.wantIn {
				reasons := excl[0]
				found := false
				for _, r := range reasons {
					if r == ReasonContextUnverified {
						found = true
					}
				}
				if !found {
					t.Fatalf("reasons = %v, want %q", reasons, ReasonContextUnverified)
				}
			}
		})
	}
}

func int64Ptr(v int64) *int64 { return &v }

// TestApplyHardGates_CapabilityGate verifies certified operation and
// reasoning capability checks.
//
// Mutation row HG-M4a: accept CapabilityReasoning via a missing Certifications
// entry (always-false closed map) — confirm this already fails closed on a
// candidate where ReasoningCertified=false → RED; then mutate to force
// ReasoningCertified=true → passes → restore.
func TestApplyHardGates_CapabilityGate(t *testing.T) {
	policy := proPolicyCopy(t)
	const ctx = int64(1000)

	t.Run("missing structured_output cert excluded", func(t *testing.T) {
		c := eligibleCandidate(accountsdomain.FundingFree, ctx)
		delete(c.Certifications, models.OperationStructuredOutput)
		reqs := Requirements{
			ContextTokens: ctx,
			Capabilities:  []Capability{CapabilityStructuredOutput},
		}
		eligible, excl, err := ApplyHardGates([]CandidateOffering{c}, reqs, policy)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(eligible) != 0 {
			t.Fatalf("want excluded, got %d eligible", len(eligible))
		}
		reasons := excl[0]
		found := false
		for _, r := range reasons {
			if r == ReasonCapabilityUncertified {
				found = true
			}
		}
		if !found {
			t.Fatalf("reasons = %v, want %q", reasons, ReasonCapabilityUncertified)
		}
	})

	t.Run("reasoning not certified excluded", func(t *testing.T) {
		c := eligibleCandidate(accountsdomain.FundingFree, ctx)
		c.ReasoningCertified = false
		reqs := Requirements{
			ContextTokens: ctx,
			Capabilities:  []Capability{CapabilityReasoning},
		}
		eligible, excl, err := ApplyHardGates([]CandidateOffering{c}, reqs, policy)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(eligible) != 0 {
			t.Fatalf("want excluded, got %d eligible", len(eligible))
		}
		reasons := excl[0]
		found := false
		for _, r := range reasons {
			if r == ReasonCapabilityUncertified {
				found = true
			}
		}
		if !found {
			t.Fatalf("reasons = %v, want %q", reasons, ReasonCapabilityUncertified)
		}
	})

	t.Run("reasoning certified passes", func(t *testing.T) {
		c := eligibleCandidate(accountsdomain.FundingFree, ctx)
		reqs := Requirements{
			ContextTokens: ctx,
			Capabilities:  []Capability{CapabilityReasoning},
		}
		eligible, _, err := ApplyHardGates([]CandidateOffering{c}, reqs, policy)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(eligible) != 1 {
			t.Fatalf("want eligible, got %d", len(eligible))
		}
	})

	t.Run("multiple simultaneous failures produce multiple reasons", func(t *testing.T) {
		// Unknown funding (fails gate 2) + nil context (fails gate 3) +
		// missing capability (fails gate 4) → three reasons for index 0.
		c := CandidateOffering{
			Funding:               accountsdomain.FundingUnknown,
			VerifiedContextTokens: nil,
			Certifications:        map[models.Operation]models.Certification{},
		}
		reqs := Requirements{
			ContextTokens: 1000,
			Capabilities:  []Capability{CapabilityChat},
		}
		_, excl, err := ApplyHardGates([]CandidateOffering{c}, reqs, policy)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		reasons := excl[0]
		wantReasons := map[string]bool{
			ReasonFundingIneligible:     false,
			ReasonContextUnverified:     false,
			ReasonCapabilityUncertified: false,
		}
		for _, r := range reasons {
			wantReasons[r] = true
		}
		for r, found := range wantReasons {
			if !found {
				t.Fatalf("reason %q missing from %v", r, reasons)
			}
		}
	})
}

// TestApplyHardGates_FailClosed_ZeroValueCandidate verifies that an empty
// CandidateOffering is excluded under every policy with any requirement.
func TestApplyHardGates_FailClosed_ZeroValueCandidate(t *testing.T) {
	policy := proPolicyCopy(t)
	c := CandidateOffering{} // all fields zero
	reqs := Requirements{
		ContextTokens: 1000,
		Capabilities:  []Capability{CapabilityChat},
	}
	eligible, excl, err := ApplyHardGates([]CandidateOffering{c}, reqs, policy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(eligible) != 0 {
		t.Fatalf("zero-value candidate must be excluded, got %d eligible", len(eligible))
	}
	if len(excl[0]) == 0 {
		t.Fatalf("zero-value candidate must carry at least one exclusion reason, got none")
	}
}

// TestApplyHardGates_InputImmutability asserts the input candidates slice is
// not mutated.
func TestApplyHardGates_InputImmutability(t *testing.T) {
	policy := litePolicyCopy(t)
	const ctx = int64(1000)
	candidates := []CandidateOffering{
		eligibleCandidate(accountsdomain.FundingFree, ctx),
		eligibleCandidate(accountsdomain.FundingPaid, ctx),
	}
	snapshot := make([]CandidateOffering, len(candidates))
	copy(snapshot, candidates)

	reqs := Requirements{ContextTokens: ctx}
	_, _, _ = ApplyHardGates(candidates, reqs, policy)

	for i := range candidates {
		if candidates[i].Funding != snapshot[i].Funding ||
			candidates[i].AccountID != snapshot[i].AccountID {
			t.Fatalf("input slice was mutated at index %d", i)
		}
	}
}
