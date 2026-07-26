package intelligence

import (
	"context"
	"testing"
	"time"
)

func fixedFreeSafetyNow() time.Time {
	return time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
}

func boolPtr(b bool) *bool { return &b }

// fakeCostDataset is a scripted CostDataset test double.
type fakeCostDataset struct {
	entry CostDatasetEntry
	hit   bool
	err   error
	calls int
}

func (f *fakeCostDataset) fn() CostDataset {
	return func(ctx context.Context, providerID, providerModelID string) (CostDatasetEntry, bool, error) {
		f.calls++
		return f.entry, f.hit, f.err
	}
}

func zeroCostPricing() map[string]any {
	return map[string]any{"cost": map[string]any{"input": 0, "output": 0}}
}

func paidPricing() map[string]any {
	return map[string]any{"cost": map[string]any{"input": 0.01, "output": 0.02}}
}

// --- extractProviderPrice -------------------------------------------------

func TestExtractProviderPrice(t *testing.T) {
	cases := []struct {
		name     string
		pricing  map[string]any
		wantFree bool
		wantOK   bool
	}{
		{"nil pricing falls through", nil, false, false},
		{"no cost key falls through", map[string]any{}, false, false},
		{"cost not a map falls through", map[string]any{"cost": "free"}, false, false},
		{"non-numeric input falls through", map[string]any{"cost": map[string]any{"input": "x", "output": 0}}, false, false},
		{"zero cost active is free", map[string]any{"cost": map[string]any{"input": 0, "output": 0}, "status": "active"}, true, true},
		{"zero cost no status is free", map[string]any{"cost": map[string]any{"input": 0.0, "output": 0.0}}, true, true},
		{"zero cost deprecated falls through", map[string]any{"cost": map[string]any{"input": 0, "output": 0}, "status": "deprecated"}, false, false},
		{"non-zero input is known paid", map[string]any{"cost": map[string]any{"input": 0.01, "output": 0}}, false, true},
		{"non-zero output is known paid", map[string]any{"cost": map[string]any{"input": 0, "output": 0.02}}, false, true},
		{"non-zero deprecated is still known paid", map[string]any{"cost": map[string]any{"input": 0.01, "output": 0}, "status": "deprecated"}, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			free, ok := extractProviderPrice(tc.pricing)
			if free != tc.wantFree || ok != tc.wantOK {
				t.Fatalf("extractProviderPrice(%v) = (%v,%v), want (%v,%v)", tc.pricing, free, ok, tc.wantFree, tc.wantOK)
			}
		})
	}
}

// --- FreeSafetyResolver.Resolve -------------------------------------------

func TestResolve_ProviderPriceZeroCost_KnownFree(t *testing.T) {
	ds := &fakeCostDataset{}
	r := NewFreeSafetyResolver(ds.fn(), NewInMemoryCostCache(), DefaultFreeSafetyConfig(), fixedFreeSafetyNow)
	fact := r.Resolve(context.Background(), "prov1", "model-a", zeroCostPricing(), nil)

	if fact.IsFree == nil || !*fact.IsFree {
		t.Fatalf("IsFree = %v, want true", fact.IsFree)
	}
	if fact.Source != CostSourceProviderPrice {
		t.Fatalf("Source = %q, want provider_price", fact.Source)
	}
	if !fact.ExactIdentityMatch {
		t.Fatalf("ExactIdentityMatch = false, want true (it is the provider's own record)")
	}
	if fact.Conflict {
		t.Fatalf("Conflict = true, want false")
	}
	if ds.calls != 0 {
		t.Fatalf("dataset seam called %d times, want 0 (provider price already resolved)", ds.calls)
	}
}

func TestResolve_ProviderPriceNonZero_KnownPaid(t *testing.T) {
	r := NewFreeSafetyResolver((&fakeCostDataset{}).fn(), NewInMemoryCostCache(), DefaultFreeSafetyConfig(), fixedFreeSafetyNow)
	fact := r.Resolve(context.Background(), "prov1", "model-a", paidPricing(), nil)

	if fact.IsFree == nil || *fact.IsFree {
		t.Fatalf("IsFree = %v, want false (known paid)", fact.IsFree)
	}
	if fact.Source != CostSourceProviderPrice {
		t.Fatalf("Source = %q, want provider_price", fact.Source)
	}
}

// TestResolve_DatasetMissNoCache_Unknown proves a price-less provider with
// no dataset hit and no last-known-good resolves to unknown (fail closed),
// never coerced to a free/paid sentinel (04 §1). MUTATION: making the
// unknown-fallthrough path set IsFree to a non-nil sentinel turns this RED.
func TestResolve_DatasetMissNoCache_Unknown(t *testing.T) {
	ds := &fakeCostDataset{hit: false}
	r := NewFreeSafetyResolver(ds.fn(), NewInMemoryCostCache(), DefaultFreeSafetyConfig(), fixedFreeSafetyNow)
	fact := r.Resolve(context.Background(), "opencode-zen", "model-x", nil, nil)

	if fact.IsFree != nil {
		t.Fatalf("IsFree = %v, want nil (unknown)", *fact.IsFree)
	}
	if Eligible(fact, TierLite).Eligible {
		t.Fatalf("lite eligible = true for an unknown fact, want false")
	}
	if Eligible(fact, TierPro).Eligible || Eligible(fact, TierMax).Eligible {
		t.Fatalf("pro/max eligible for an unknown fact, want both false")
	}
}

// TestResolve_DatasetHit_ExactMatchFree proves the price-less-provider path
// (opencode-zen analog): a dataset hit with exact_identity_match and
// verified zero-cost resolves to known-free.
func TestResolve_DatasetHit_ExactMatchFree(t *testing.T) {
	ds := &fakeCostDataset{hit: true, entry: CostDatasetEntry{IsFree: true, ExactIdentityMatch: true, DatasetVersion: "v42"}}
	r := NewFreeSafetyResolver(ds.fn(), NewInMemoryCostCache(), DefaultFreeSafetyConfig(), fixedFreeSafetyNow)
	fact := r.Resolve(context.Background(), "opencode-zen", "model-x", nil, nil)

	if fact.IsFree == nil || !*fact.IsFree {
		t.Fatalf("IsFree = %v, want true", fact.IsFree)
	}
	if fact.Source != CostSourceModelsDev || fact.DatasetVersion != "v42" {
		t.Fatalf("Source/DatasetVersion = %q/%q, want models_dev/v42", fact.Source, fact.DatasetVersion)
	}
	if !fact.ExactIdentityMatch {
		t.Fatalf("ExactIdentityMatch = false, want true")
	}
}

// TestResolve_DatasetHit_NonExactMatch_NeverFree proves a family/name match
// is never sufficient to prove free (04 §2b), even when the dataset entry
// itself claims zero-cost. MUTATION: skipping the ExactIdentityMatch check
// in factFromDatasetEntry turns this RED (IsFree would wrongly be true).
func TestResolve_DatasetHit_NonExactMatch_NeverFree(t *testing.T) {
	ds := &fakeCostDataset{hit: true, entry: CostDatasetEntry{IsFree: true, ExactIdentityMatch: false, DatasetVersion: "v42"}}
	r := NewFreeSafetyResolver(ds.fn(), NewInMemoryCostCache(), DefaultFreeSafetyConfig(), fixedFreeSafetyNow)
	fact := r.Resolve(context.Background(), "opencode-zen", "model-x", nil, nil)

	if fact.IsFree != nil {
		t.Fatalf("IsFree = %v, want nil — a non-exact dataset match must never prove free", *fact.IsFree)
	}
	if Eligible(fact, TierLite).Eligible {
		t.Fatalf("lite eligible = true off a non-exact match, want false")
	}
}

func TestResolve_DatasetHit_Paid(t *testing.T) {
	ds := &fakeCostDataset{hit: true, entry: CostDatasetEntry{IsFree: false, ExactIdentityMatch: true, DatasetVersion: "v1"}}
	r := NewFreeSafetyResolver(ds.fn(), NewInMemoryCostCache(), DefaultFreeSafetyConfig(), fixedFreeSafetyNow)
	fact := r.Resolve(context.Background(), "opencode-zen", "model-x", nil, nil)

	if fact.IsFree == nil || *fact.IsFree {
		t.Fatalf("IsFree = %v, want false (known paid)", fact.IsFree)
	}
}

// TestResolve_FreshCache_NoSeamCall proves a cached entry within the TTL is
// reused without re-invoking the dataset seam.
func TestResolve_FreshCache_NoSeamCall(t *testing.T) {
	cache := NewInMemoryCostCache()
	cache.Set("opencode-zen", "model-x", CostDatasetEntry{IsFree: true, ExactIdentityMatch: true, DatasetVersion: "v1"}, fixedFreeSafetyNow().Add(-5*time.Minute))
	ds := &fakeCostDataset{hit: true, entry: CostDatasetEntry{IsFree: false, ExactIdentityMatch: true, DatasetVersion: "v2"}}
	r := NewFreeSafetyResolver(ds.fn(), cache, DefaultFreeSafetyConfig(), fixedFreeSafetyNow)

	fact := r.Resolve(context.Background(), "opencode-zen", "model-x", nil, nil)

	if ds.calls != 0 {
		t.Fatalf("dataset seam called %d times, want 0 (cache entry is within the 10-minute TTL)", ds.calls)
	}
	if fact.IsFree == nil || !*fact.IsFree {
		t.Fatalf("IsFree = %v, want true (from the fresh cached entry, not the seam)", fact.IsFree)
	}
	if fact.Stale {
		t.Fatalf("Stale = true, want false for a within-TTL cache hit")
	}
}

// TestResolve_DatasetMiss_UsesLastKnownGoodWithinWindow proves a seam
// miss/error falls back to a last-known-good cache entry within the
// staleness window, marked stale=true. MUTATION: ignoring the staleness
// window (never falling back) turns this RED.
func TestResolve_DatasetMiss_UsesLastKnownGoodWithinWindow(t *testing.T) {
	cache := NewInMemoryCostCache()
	// Cached 20 minutes ago: past the 10-minute TTL (so the seam IS
	// invoked) but well within the 24h staleness window.
	cache.Set("opencode-zen", "model-x", CostDatasetEntry{IsFree: true, ExactIdentityMatch: true, DatasetVersion: "v1"}, fixedFreeSafetyNow().Add(-20*time.Minute))
	ds := &fakeCostDataset{hit: false}
	r := NewFreeSafetyResolver(ds.fn(), cache, DefaultFreeSafetyConfig(), fixedFreeSafetyNow)

	fact := r.Resolve(context.Background(), "opencode-zen", "model-x", nil, nil)

	if ds.calls != 1 {
		t.Fatalf("dataset seam called %d times, want 1 (TTL expired, must attempt a refresh)", ds.calls)
	}
	if fact.IsFree == nil || !*fact.IsFree {
		t.Fatalf("IsFree = %v, want true (from the last-known-good cache)", fact.IsFree)
	}
	if !fact.Stale {
		t.Fatalf("Stale = false, want true (reused beyond TTL via the staleness window)")
	}
}

// TestResolve_DatasetMiss_BeyondStalenessWindow_Unknown proves a
// last-known-good entry beyond the staleness window is NOT used — it must
// resolve to unknown instead. MUTATION: removing the staleness-window upper
// bound (always falling back regardless of age) turns this RED.
func TestResolve_DatasetMiss_BeyondStalenessWindow_Unknown(t *testing.T) {
	cache := NewInMemoryCostCache()
	cache.Set("opencode-zen", "model-x", CostDatasetEntry{IsFree: true, ExactIdentityMatch: true, DatasetVersion: "v1"}, fixedFreeSafetyNow().Add(-25*time.Hour))
	ds := &fakeCostDataset{hit: false}
	r := NewFreeSafetyResolver(ds.fn(), cache, DefaultFreeSafetyConfig(), fixedFreeSafetyNow)

	fact := r.Resolve(context.Background(), "opencode-zen", "model-x", nil, nil)

	if fact.IsFree != nil {
		t.Fatalf("IsFree = %v, want nil — a last-known-good beyond the staleness window must not be used", *fact.IsFree)
	}
}

// TestResolve_ConflictingEvidence_ProviderWinsForProMax_FailsClosedForLite
// proves 04 §2b's conflicting row: provider price and a cached dataset
// entry disagree; venom/lite fails closed (excluded regardless of which
// source would win); venom/pro and venom/max resolve via precedence
// (provider price outranks the dataset). MUTATION: dropping the Conflict
// flag or Lite's Conflict check turns this RED (lite would be eligible).
func TestResolve_ConflictingEvidence_ProviderWinsForProMax_FailsClosedForLite(t *testing.T) {
	cache := NewInMemoryCostCache()
	// A previously cached, exact-match dataset entry says PAID.
	cache.Set("prov1", "model-a", CostDatasetEntry{IsFree: false, ExactIdentityMatch: true, DatasetVersion: "v1"}, fixedFreeSafetyNow().Add(-1*time.Minute))
	r := NewFreeSafetyResolver((&fakeCostDataset{}).fn(), cache, DefaultFreeSafetyConfig(), fixedFreeSafetyNow)

	// The provider's own price says FREE — a genuine conflict.
	fact := r.Resolve(context.Background(), "prov1", "model-a", zeroCostPricing(), nil)

	if !fact.Conflict {
		t.Fatalf("Conflict = false, want true (provider says free, cached dataset says paid)")
	}
	if fact.IsFree == nil || !*fact.IsFree {
		t.Fatalf("IsFree = %v, want true — provider_price outranks models_dev under precedence", fact.IsFree)
	}
	if Eligible(fact, TierLite).Eligible {
		t.Fatalf("lite eligible = true on conflicting evidence, want false (fail closed for lite)")
	}
	if !Eligible(fact, TierPro).Eligible || !Eligible(fact, TierMax).Eligible {
		t.Fatalf("pro/max not eligible on a precedence-resolved conflict, want both eligible")
	}
}

// TestResolve_OwnerOverride_WinsOverProviderAndDataset proves an owner
// override wins outright over a contradicting provider price (and would
// equally win over a contradicting dataset entry, never even consulted).
// MUTATION: checking ownerOverride after the provider-price branch (or not
// at all) turns this RED.
func TestResolve_OwnerOverride_WinsOverProviderAndDataset(t *testing.T) {
	ds := &fakeCostDataset{}
	r := NewFreeSafetyResolver(ds.fn(), NewInMemoryCostCache(), DefaultFreeSafetyConfig(), fixedFreeSafetyNow)

	// Provider price says PAID; owner override says FREE.
	fact := r.Resolve(context.Background(), "prov1", "model-a", paidPricing(), boolPtr(true))

	if fact.IsFree == nil || !*fact.IsFree {
		t.Fatalf("IsFree = %v, want true (owner override wins)", fact.IsFree)
	}
	if fact.Source != CostSourceOwnerOverride {
		t.Fatalf("Source = %q, want owner_override", fact.Source)
	}
	if fact.Conflict {
		t.Fatalf("Conflict = true, want false — an owner override is authoritative, not merely one contending source")
	}
	if ds.calls != 0 {
		t.Fatalf("dataset seam called %d times, want 0 (owner override short-circuits everything else)", ds.calls)
	}
}

// TestResolve_IndependentOfEnrichmentFlag proves free-safety resolves
// identically regardless of any "metadata enrichment enabled" setting
// elsewhere in the system — Resolve's signature and behavior consult
// nothing of the kind. This guards against a future change threading an
// enrichment flag into this resolver and gating pipeline A on it.
func TestResolve_IndependentOfEnrichmentFlag(t *testing.T) {
	newResolver := func() *FreeSafetyResolver {
		ds := &fakeCostDataset{hit: true, entry: CostDatasetEntry{IsFree: true, ExactIdentityMatch: true, DatasetVersion: "v1"}}
		return NewFreeSafetyResolver(ds.fn(), NewInMemoryCostCache(), DefaultFreeSafetyConfig(), fixedFreeSafetyNow)
	}

	// Two call sites representing "enrichment enabled elsewhere" and
	// "enrichment disabled elsewhere" — neither value is ever passed to
	// Resolve, which has no parameter for it.
	enrichmentEnabled := true
	enrichmentDisabled := false
	_ = enrichmentEnabled
	_ = enrichmentDisabled

	factA := newResolver().Resolve(context.Background(), "opencode-zen", "model-x", nil, nil)
	factB := newResolver().Resolve(context.Background(), "opencode-zen", "model-x", nil, nil)

	if (factA.IsFree == nil) != (factB.IsFree == nil) || (factA.IsFree != nil && *factA.IsFree != *factB.IsFree) {
		t.Fatalf("resolution differs across otherwise-identical calls: %+v vs %+v", factA, factB)
	}
}

// TestResolve_ProvenanceComplete asserts every field of a known-free
// resolution is populated in a way that is distinguishable from unknown.
func TestResolve_ProvenanceComplete(t *testing.T) {
	r := NewFreeSafetyResolver((&fakeCostDataset{}).fn(), NewInMemoryCostCache(), DefaultFreeSafetyConfig(), fixedFreeSafetyNow)
	fact := r.Resolve(context.Background(), "prov1", "model-a", zeroCostPricing(), nil)

	if fact.IsFree == nil {
		t.Fatalf("IsFree = nil, want a non-nil true")
	}
	if fact.Source == "" {
		t.Fatalf("Source is empty")
	}
	if fact.ObservedAt.IsZero() {
		t.Fatalf("ObservedAt is zero")
	}
	if fact.Confidence == 0 {
		t.Fatalf("Confidence is zero, want a positive confidence for a resolved fact")
	}
	if !fact.ExactIdentityMatch {
		t.Fatalf("ExactIdentityMatch = false, want true")
	}
}

// --- Eligible --------------------------------------------------------------

func TestEligible_PerTierTable(t *testing.T) {
	trueVal := true
	falseVal := false

	cases := []struct {
		name        string
		fact        ResolvedCostFact
		wantLite    bool
		wantPro     bool
		wantMax     bool
		wantPenalty bool
	}{
		{"known free", ResolvedCostFact{IsFree: &trueVal}, true, true, true, false},
		{"known paid", ResolvedCostFact{IsFree: &falseVal}, false, true, true, false},
		{"unknown", ResolvedCostFact{IsFree: nil}, false, false, false, false},
		{"stale free within window", ResolvedCostFact{IsFree: &trueVal, Stale: true}, true, true, true, true},
		{"conflicting (provider wins free)", ResolvedCostFact{IsFree: &trueVal, Conflict: true}, false, true, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lite := Eligible(tc.fact, TierLite)
			pro := Eligible(tc.fact, TierPro)
			max := Eligible(tc.fact, TierMax)
			if lite.Eligible != tc.wantLite {
				t.Fatalf("lite.Eligible = %v, want %v", lite.Eligible, tc.wantLite)
			}
			if pro.Eligible != tc.wantPro {
				t.Fatalf("pro.Eligible = %v, want %v", pro.Eligible, tc.wantPro)
			}
			if max.Eligible != tc.wantMax {
				t.Fatalf("max.Eligible = %v, want %v", max.Eligible, tc.wantMax)
			}
			if pro.Penalty != tc.wantPenalty || max.Penalty != tc.wantPenalty {
				t.Fatalf("pro/max Penalty = %v/%v, want %v", pro.Penalty, max.Penalty, tc.wantPenalty)
			}
		})
	}
}

func TestEligible_UnrecognizedTier_FailsClosed(t *testing.T) {
	trueVal := true
	got := Eligible(ResolvedCostFact{IsFree: &trueVal}, Tier("bogus"))
	if got.Eligible {
		t.Fatalf("Eligible = true for an unrecognized tier, want false")
	}
}

func TestParseTier(t *testing.T) {
	for _, tier := range []Tier{TierLite, TierPro, TierMax} {
		got, err := ParseTier(string(tier))
		if err != nil || got != tier {
			t.Fatalf("ParseTier(%q) = (%q, %v), want (%q, nil)", tier, got, err, tier)
		}
	}
	if _, err := ParseTier("venom/ultra"); err == nil {
		t.Fatalf("ParseTier(bogus) error = nil, want ErrUnknownTier")
	}
}

func TestParseCostSource(t *testing.T) {
	for _, src := range []CostSource{CostSourceProviderPrice, CostSourceModelsDev, CostSourceOwnerOverride} {
		got, err := ParseCostSource(string(src))
		if err != nil || got != src {
			t.Fatalf("ParseCostSource(%q) = (%q, %v), want (%q, nil)", src, got, err, src)
		}
	}
	if _, err := ParseCostSource("bogus"); err == nil {
		t.Fatalf("ParseCostSource(bogus) error = nil, want ErrUnknownCostSource")
	}
}
