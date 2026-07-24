package domain

import (
	"errors"
	"testing"
	"time"
)

func TestStampFirstEvidence(t *testing.T) {
	cases := []struct {
		name        string
		mode        FundingMode
		value       Funding
		locked      bool
		confidence  float64
		wantFunding Funding
		wantSource  FundingSource
		wantLocked  bool
	}{
		{
			name: "fixed", mode: FundingModeFixed, value: FundingPaid, locked: true, confidence: 1,
			wantFunding: FundingPaid, wantSource: FundingSourceProviderPolicy, wantLocked: true,
		},
		{
			name: "owner_policy", mode: FundingModeOwnerPolicy, value: FundingFree, confidence: 1,
			wantFunding: FundingFree, wantSource: FundingSourceOwnerPolicy, wantLocked: false,
		},
		{
			name: "provider_evidence", mode: FundingModeProviderEvidence, value: FundingPaid, confidence: 0.9,
			wantFunding: FundingPaid, wantSource: FundingSourceProviderEvidence, wantLocked: false,
		},
		{
			// value/locked are deliberately set to something that would be
			// wrong if honored, to prove evidence_required ignores them.
			name: "evidence_required", mode: FundingModeEvidenceRequired, value: FundingPaid, locked: true, confidence: 1,
			wantFunding: FundingUnknown, wantSource: FundingSourceProviderPolicy, wantLocked: false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := StampFirstEvidence(c.mode, "acct1", c.value, c.locked, c.confidence, fixedNow)
			if err != nil {
				t.Fatalf("StampFirstEvidence: unexpected error: %v", err)
			}
			if got.Funding != c.wantFunding {
				t.Fatalf("Funding = %s, want %s", got.Funding, c.wantFunding)
			}
			if got.Source != c.wantSource {
				t.Fatalf("Source = %s, want %s", got.Source, c.wantSource)
			}
			if got.Locked != c.wantLocked {
				t.Fatalf("Locked = %v, want %v", got.Locked, c.wantLocked)
			}
			if !got.ObservedAt.Equal(fixedNow) {
				t.Fatalf("ObservedAt = %v, want %v (injected clock)", got.ObservedAt, fixedNow)
			}
			if got.SupersededAt != nil {
				t.Fatalf("SupersededAt = %v, want nil (freshly stamped row is current)", got.SupersededAt)
			}
		})
	}
}

func TestStampFirstEvidence_UnknownModeRejected(t *testing.T) {
	_, err := StampFirstEvidence(FundingMode("bogus"), "acct1", FundingFree, false, 1, fixedNow)
	if err == nil {
		t.Fatalf("StampFirstEvidence with an unknown mode succeeded, want rejection")
	}
}

func TestDecideFundingSupersession_Rule1_LockedProviderPolicyRejectsOverride(t *testing.T) {
	current := FundingEvidence{Source: FundingSourceProviderPolicy, Locked: true, Funding: FundingPaid, ObservedAt: fixedNow}
	candidate := FundingEvidence{Source: FundingSourceOwnerOverride, Funding: FundingFree, ObservedAt: fixedNow.Add(time.Hour)}

	_, err := DecideFundingSupersession(current, candidate, fixedNow.Add(time.Hour))
	if !errors.Is(err, ErrFundingLocked) {
		t.Fatalf("override of a locked provider_policy row: err = %v, want ErrFundingLocked", err)
	}
}

func TestDecideFundingSupersession_Rule2_OwnerOverrideNeverAutoSuperseded(t *testing.T) {
	current := FundingEvidence{Source: FundingSourceOwnerOverride, Funding: FundingFree, ObservedAt: fixedNow}
	candidate := FundingEvidence{Source: FundingSourceProviderEvidence, Funding: FundingPaid, Confidence: 1, ObservedAt: fixedNow.Add(time.Hour)}

	ok, err := DecideFundingSupersession(current, candidate, fixedNow.Add(time.Hour))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatalf("provider_evidence superseded an owner_override row, want rejection (rule 2)")
	}
}

func TestDecideFundingSupersession_Rule2_OwnerOverrideSupersededByNewOwnerAction(t *testing.T) {
	current := FundingEvidence{Source: FundingSourceOwnerOverride, Funding: FundingFree, ObservedAt: fixedNow}
	candidate := FundingEvidence{Source: FundingSourceOwnerOverride, Funding: FundingPaid, ObservedAt: fixedNow.Add(time.Hour)}

	ok, err := DecideFundingSupersession(current, candidate, fixedNow.Add(time.Hour))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatalf("a new owner_override did not supersede the prior owner_override, want success")
	}
}

func TestDecideFundingSupersession_Rule3_ProviderEvidenceSupersedesFresherAndConfident(t *testing.T) {
	current := FundingEvidence{Source: FundingSourceOwnerPolicy, Funding: FundingFree, Confidence: 0.5, ObservedAt: fixedNow}
	candidate := FundingEvidence{Source: FundingSourceProviderEvidence, Funding: FundingPaid, Confidence: 0.9, ObservedAt: fixedNow.Add(time.Hour)}

	ok, err := DecideFundingSupersession(current, candidate, fixedNow.Add(time.Hour))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatalf("fresher, more confident provider_evidence did not supersede owner_policy, want success")
	}
}

func TestDecideFundingSupersession_Rule3_StaleProviderEvidenceRejected(t *testing.T) {
	current := FundingEvidence{Source: FundingSourceProviderEvidence, Funding: FundingPaid, Confidence: 0.9, ObservedAt: fixedNow.Add(time.Hour)}
	candidate := FundingEvidence{Source: FundingSourceProviderEvidence, Funding: FundingFree, Confidence: 0.9, ObservedAt: fixedNow}

	ok, err := DecideFundingSupersession(current, candidate, fixedNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatalf("a stale provider_evidence row superseded the current one, want rejection")
	}
}

func TestDecideFundingSupersession_Rule3_LowerConfidenceRejected(t *testing.T) {
	current := FundingEvidence{Source: FundingSourceProviderEvidence, Funding: FundingPaid, Confidence: 0.9, ObservedAt: fixedNow}
	candidate := FundingEvidence{Source: FundingSourceProviderEvidence, Funding: FundingFree, Confidence: 0.5, ObservedAt: fixedNow.Add(time.Hour)}

	ok, err := DecideFundingSupersession(current, candidate, fixedNow.Add(time.Hour))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatalf("a lower-confidence provider_evidence row superseded the current one, want rejection")
	}
}

func TestDecideFundingSupersession_Rule4_OwnerActionSupersedesConnectTimeStamp(t *testing.T) {
	current := FundingEvidence{Source: FundingSourceOwnerPolicy, Funding: FundingFree, ObservedAt: fixedNow}
	candidate := FundingEvidence{Source: FundingSourceOwnerOverride, Funding: FundingPaid, ObservedAt: fixedNow.Add(time.Hour)}

	ok, err := DecideFundingSupersession(current, candidate, fixedNow.Add(time.Hour))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatalf("owner_override did not supersede the connect-time owner_policy stamp, want success")
	}
}

func TestDecideFundingSupersession_EvidenceRequiredStampIsClassifiedByProviderEvidence(t *testing.T) {
	unclassified := FundingEvidence{Source: FundingSourceProviderPolicy, Funding: FundingUnknown, Locked: false, ObservedAt: fixedNow}
	if !unclassified.IsUnclassified() {
		t.Fatalf("sanity check failed: fixture row is not IsUnclassified()")
	}
	candidate := FundingEvidence{Source: FundingSourceProviderEvidence, Funding: FundingPaid, Confidence: 0.8, ObservedAt: fixedNow.Add(time.Hour)}

	ok, err := DecideFundingSupersession(unclassified, candidate, fixedNow.Add(time.Hour))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatalf("provider_evidence did not classify the evidence_required stamp, want success")
	}
}

func TestApplyFundingSupersession_StampsPriorAndPromotesCandidate(t *testing.T) {
	current := FundingEvidence{ID: "e1", Source: FundingSourceOwnerPolicy, Funding: FundingFree, ObservedAt: fixedNow}
	candidate := FundingEvidence{ID: "e2", Source: FundingSourceOwnerOverride, Funding: FundingPaid, ObservedAt: fixedNow.Add(time.Hour)}

	result, err := ApplyFundingSupersession(current, candidate, fixedNow.Add(time.Hour))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Superseded {
		t.Fatalf("Superseded = false, want true")
	}
	if result.UpdatedCurrent.SupersededAt == nil || !result.UpdatedCurrent.SupersededAt.Equal(fixedNow.Add(time.Hour)) {
		t.Fatalf("UpdatedCurrent.SupersededAt = %v, want stamped %v", result.UpdatedCurrent.SupersededAt, fixedNow.Add(time.Hour))
	}
	if result.NewCurrent.ID != "e2" {
		t.Fatalf("NewCurrent.ID = %s, want %q", result.NewCurrent.ID, "e2")
	}
	if current.SupersededAt != nil {
		t.Fatalf("original current input was mutated: SupersededAt = %v", current.SupersededAt)
	}
}

func TestApplyFundingSupersession_RejectionLeavesResultUnsupersededWithError(t *testing.T) {
	current := FundingEvidence{ID: "e1", Source: FundingSourceProviderPolicy, Locked: true, Funding: FundingPaid, ObservedAt: fixedNow}
	candidate := FundingEvidence{ID: "e2", Source: FundingSourceOwnerOverride, Funding: FundingFree, ObservedAt: fixedNow}

	result, err := ApplyFundingSupersession(current, candidate, fixedNow)
	if !errors.Is(err, ErrFundingLocked) {
		t.Fatalf("err = %v, want ErrFundingLocked", err)
	}
	if result.Superseded {
		t.Fatalf("Superseded = true on rejection, want false")
	}
}

func TestCurrentFundingRow(t *testing.T) {
	superseded := fixedNow
	rows := []FundingEvidence{
		{ID: "e1", SupersededAt: &superseded},
		{ID: "e2", SupersededAt: nil},
	}

	got, ok := CurrentFundingRow(rows)
	if !ok {
		t.Fatalf("CurrentFundingRow: ok = false, want true")
	}
	if got.ID != "e2" {
		t.Fatalf("CurrentFundingRow = %s, want %q", got.ID, "e2")
	}

	if _, ok := CurrentFundingRow(nil); ok {
		t.Fatalf("CurrentFundingRow(nil): ok = true, want false")
	}
}

func TestFundingEvidence_IsUnclassified(t *testing.T) {
	evidenceRequiredStamp := FundingEvidence{Funding: FundingUnknown, Source: FundingSourceProviderPolicy, Locked: false}
	if !evidenceRequiredStamp.IsUnclassified() {
		t.Fatalf("evidence_required stamp: IsUnclassified() = false, want true")
	}

	lockedPaid := FundingEvidence{Funding: FundingPaid, Source: FundingSourceProviderPolicy, Locked: true}
	if lockedPaid.IsUnclassified() {
		t.Fatalf("locked paid provider_policy row: IsUnclassified() = true, want false")
	}

	ownerFree := FundingEvidence{Funding: FundingFree, Source: FundingSourceOwnerPolicy}
	if ownerFree.IsUnclassified() {
		t.Fatalf("owner_policy free row: IsUnclassified() = true, want false")
	}
}
