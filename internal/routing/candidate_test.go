package routing

import (
	"testing"

	accountsdomain "github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
)

// certifiedOffering returns a CandidateOffering that satisfies every
// BuildCandidatePool admission condition for primary=OperationChat.
func certifiedOffering(funding accountsdomain.Funding) CandidateOffering {
	return CandidateOffering{
		ProviderID:      "p1",
		AccountID:       "a1",
		ProviderModelID: "m1",
		Funding:         funding,
		AccountHealth:   accountsdomain.HealthHealthy,
		CredentialValid: true,
		Cooling:         false,
		Certifications: map[models.Operation]models.Certification{
			models.OperationChat: {
				State: models.CertCertified,
				Truth: models.TruthSupported,
			},
		},
	}
}

// TestBuildCandidatePool_InclusionAndExclusionConditions is the mutation
// table for UNIT 1: one positive case plus one single-condition flip per
// admission criterion.
//
// Mutation rows (break → RED → restore → GREEN):
//
//	M1: drop the health check → the "unhealthy account" case passes through → restore.
//	M2: accept any truth (drop truth check) → the "wrong truth" case passes → restore.
//	M3: overwrite Funding on output → funding-passthrough test fails → restore.
func TestBuildCandidatePool_InclusionAndExclusionConditions(t *testing.T) {
	const primary = models.OperationChat

	tests := []struct {
		name     string
		offering CandidateOffering
		want     int // expected count in result
	}{
		{
			name:     "all conditions met",
			offering: certifiedOffering(accountsdomain.FundingFree),
			want:     1,
		},
		{
			name: "uncertified state",
			offering: func() CandidateOffering {
				o := certifiedOffering(accountsdomain.FundingFree)
				o.Certifications[primary] = models.Certification{
					State: models.CertObserved,
					Truth: models.TruthSupported,
				}
				return o
			}(),
			want: 0,
		},
		{
			name: "wrong truth (unsupported)",
			offering: func() CandidateOffering {
				o := certifiedOffering(accountsdomain.FundingFree)
				o.Certifications[primary] = models.Certification{
					State: models.CertCertified,
					Truth: models.TruthUnsupported,
				}
				return o
			}(),
			want: 0,
		},
		{
			name: "unhealthy account",
			offering: func() CandidateOffering {
				o := certifiedOffering(accountsdomain.FundingFree)
				o.AccountHealth = accountsdomain.HealthDegraded
				return o
			}(),
			want: 0,
		},
		{
			name: "invalid credential",
			offering: func() CandidateOffering {
				o := certifiedOffering(accountsdomain.FundingFree)
				o.CredentialValid = false
				return o
			}(),
			want: 0,
		},
		{
			name: "cooling",
			offering: func() CandidateOffering {
				o := certifiedOffering(accountsdomain.FundingFree)
				o.Cooling = true
				return o
			}(),
			want: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := BuildCandidatePool(primary, []CandidateOffering{tc.offering})
			if len(got) != tc.want {
				t.Fatalf("BuildCandidatePool: got %d candidates, want %d", len(got), tc.want)
			}
		})
	}
}

// TestBuildCandidatePool_FundingPassthrough proves the Funding field is
// carried byte-identical on output for all three funding values.
// BuildCandidatePool MUST NOT inspect or alter Funding.
//
// Mutation row M3: overwrite output Funding to FundingFree → this test goes
// RED on the Paid and Unknown cases → restore.
func TestBuildCandidatePool_FundingPassthrough(t *testing.T) {
	fundings := []accountsdomain.Funding{
		accountsdomain.FundingFree,
		accountsdomain.FundingPaid,
		accountsdomain.FundingUnknown,
	}
	for _, f := range fundings {
		in := certifiedOffering(f)
		got := BuildCandidatePool(models.OperationChat, []CandidateOffering{in})
		if len(got) != 1 {
			t.Fatalf("funding %q: got %d candidates, want 1", f, len(got))
		}
		if got[0].Funding != f {
			t.Fatalf("funding %q: output Funding = %q, want byte-identical", f, got[0].Funding)
		}
	}
}

// TestBuildCandidatePool_InputImmutability calls BuildCandidatePool then
// asserts the input slice contents are unchanged.
func TestBuildCandidatePool_InputImmutability(t *testing.T) {
	offerings := []CandidateOffering{
		certifiedOffering(accountsdomain.FundingFree),
		certifiedOffering(accountsdomain.FundingPaid),
	}
	snapshot := make([]CandidateOffering, len(offerings))
	copy(snapshot, offerings)

	_ = BuildCandidatePool(models.OperationChat, offerings)

	for i := range offerings {
		if offerings[i].AccountID != snapshot[i].AccountID ||
			offerings[i].Funding != snapshot[i].Funding ||
			offerings[i].AccountHealth != snapshot[i].AccountHealth ||
			offerings[i].CredentialValid != snapshot[i].CredentialValid ||
			offerings[i].Cooling != snapshot[i].Cooling {
			t.Fatalf("input slice was mutated at index %d", i)
		}
	}
}

// TestBuildCandidatePool_Determinism verifies the same input twice produces
// identical output.
func TestBuildCandidatePool_Determinism(t *testing.T) {
	offerings := []CandidateOffering{
		certifiedOffering(accountsdomain.FundingFree),
		certifiedOffering(accountsdomain.FundingPaid),
	}
	a := BuildCandidatePool(models.OperationChat, offerings)
	b := BuildCandidatePool(models.OperationChat, offerings)
	if len(a) != len(b) {
		t.Fatalf("non-deterministic: lengths %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].AccountID != b[i].AccountID || a[i].Funding != b[i].Funding {
			t.Fatalf("non-deterministic at index %d", i)
		}
	}
}
