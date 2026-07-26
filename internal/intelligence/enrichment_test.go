package intelligence

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
)

func floatPtr(f float64) *float64 { return &f }

// spyMetadataRegistry is a scripted MetadataRegistry test double that
// records every call it receives.
type spyMetadataRegistry struct {
	entry                               MetadataEntry
	hit                                 bool
	err                                 error
	calls                               int
	lastProviderID, lastProviderModelID string
}

func (s *spyMetadataRegistry) fn() MetadataRegistry {
	return func(ctx context.Context, providerID, providerModelID string) (MetadataEntry, bool, error) {
		s.calls++
		s.lastProviderID = providerID
		s.lastProviderModelID = providerModelID
		return s.entry, s.hit, s.err
	}
}

// spyQualityIndex is a scripted QualityIndex test double.
type spyQualityIndex struct {
	entry                               QualityEntry
	hit                                 bool
	err                                 error
	calls                               int
	lastProviderID, lastProviderModelID string
}

func (s *spyQualityIndex) fn() QualityIndex {
	return func(ctx context.Context, providerID, providerModelID string) (QualityEntry, bool, error) {
		s.calls++
		s.lastProviderID = providerID
		s.lastProviderModelID = providerModelID
		return s.entry, s.hit, s.err
	}
}

// spyCostCache wraps NewInMemoryCostCache and counts every Get/Set call,
// so a test can assert pipeline A's cache saw zero interactions
// attributable to enrichment.
type spyCostCache struct {
	inner CostCache
	calls int
}

func newSpyCostCache() *spyCostCache {
	return &spyCostCache{inner: NewInMemoryCostCache()}
}

func (s *spyCostCache) Get(providerID, providerModelID string) (CostDatasetEntry, time.Time, bool) {
	s.calls++
	return s.inner.Get(providerID, providerModelID)
}

func (s *spyCostCache) Set(providerID, providerModelID string, entry CostDatasetEntry, fetchedAt time.Time) {
	s.calls++
	s.inner.Set(providerID, providerModelID, entry, fetchedAt)
}

func alwaysOn(context.Context) bool  { return true }
func alwaysOff(context.Context) bool { return false }

func TestEnrich_OffByDefault_NilSwitchEmitsNothing(t *testing.T) {
	reg := &spyMetadataRegistry{hit: true, entry: MetadataEntry{ExactIdentityMatch: true, Family: "gpt"}}
	qual := &spyQualityIndex{hit: true, entry: QualityEntry{Rating: floatPtr(80), ExactIdentityMatch: true}}

	svcNil := NewEnrichmentService(nil, reg.fn(), qual.fn(), nil, fixedFreeSafetyNow)
	if got := svcNil.Enrich(context.Background(), Scope{}, "prov1", "model-a"); got != nil {
		t.Fatalf("Enrich with nil switch = %v, want nil", got)
	}

	svcFalse := NewEnrichmentService(alwaysOff, reg.fn(), qual.fn(), nil, fixedFreeSafetyNow)
	if got := svcFalse.Enrich(context.Background(), Scope{}, "prov1", "model-a"); got != nil {
		t.Fatalf("Enrich with false switch = %v, want nil", got)
	}

	if reg.calls != 0 || qual.calls != 0 {
		t.Fatalf("seams called (registry=%d, quality=%d), want 0 (enrichment must be off by default)", reg.calls, qual.calls)
	}
}

func TestEnrich_Enabled_EmitsExactMatchEvidenceAtExternalRegistryRank(t *testing.T) {
	release := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	reg := &spyMetadataRegistry{hit: true, entry: MetadataEntry{
		NativeContextTokens: intPtr(128000),
		Capabilities:        []models.Operation{models.OperationVision, models.OperationChat},
		Family:              "gpt",
		ReleaseDate:         &release,
		ExactIdentityMatch:  true,
		DatasetVersion:      "v7",
	}}
	qual := &spyQualityIndex{hit: true, entry: QualityEntry{Rating: floatPtr(87), ExactIdentityMatch: true, DatasetVersion: "leaderboard-3"}}

	svc := NewEnrichmentService(alwaysOn, reg.fn(), qual.fn(), nil, fixedFreeSafetyNow)
	scope := Scope{AccountID: "acct1", ProviderModelID: "model-a"}
	got := svc.Enrich(context.Background(), scope, "prov1", "model-a")

	wantFields := []string{
		FieldNativeContextTokens,
		CapabilityField(models.OperationChat),
		CapabilityField(models.OperationVision),
		FieldFamily,
		FieldReleaseDate,
		FieldQualityRating,
	}
	if len(got) != len(wantFields) {
		t.Fatalf("Enrich returned %d evidence items, want %d: %+v", len(got), len(wantFields), got)
	}
	for i, f := range wantFields {
		ev := got[i]
		if ev.Field != f {
			t.Fatalf("evidence[%d].Field = %q, want %q (order matters)", i, ev.Field, f)
		}
		if ev.Scope != scope {
			t.Fatalf("evidence[%d].Scope = %+v, want %+v", i, ev.Scope, scope)
		}
		if ev.Source != SourceExternalRegistry {
			t.Fatalf("evidence[%d].Source = %q, want %q", i, ev.Source, SourceExternalRegistry)
		}
		if ev.Verification != VerificationDeclared {
			t.Fatalf("evidence[%d].Verification = %q, want %q", i, ev.Verification, VerificationDeclared)
		}
		if !ev.ExactIdentityMatch {
			t.Fatalf("evidence[%d].ExactIdentityMatch = false, want true", i)
		}
		if !ev.ObservedAt.Equal(fixedFreeSafetyNow()) {
			t.Fatalf("evidence[%d].ObservedAt = %v, want %v", i, ev.ObservedAt, fixedFreeSafetyNow())
		}
	}
	if got[0].Value != 128000 {
		t.Fatalf("native_context_tokens Value = %v, want 128000", got[0].Value)
	}
	if got[0].DatasetVersion != "v7" {
		t.Fatalf("native_context_tokens DatasetVersion = %q, want v7", got[0].DatasetVersion)
	}
	if got[len(got)-1].DatasetVersion != "leaderboard-3" {
		t.Fatalf("quality_rating DatasetVersion = %q, want leaderboard-3", got[len(got)-1].DatasetVersion)
	}
}

// TestEnrich_NonExactMatch_DropsHardFactsKeepsSoftAsHeuristic proves 04
// §2b's hard/soft split: a non-exact match never emits native_context_
// tokens or capability.* evidence at all, but still emits family/release_
// date/quality_rating at SourceHeuristic. MUTATION: emitting hard facts
// regardless of ExactIdentityMatch turns this RED.
func TestEnrich_NonExactMatch_DropsHardFactsKeepsSoftAsHeuristic(t *testing.T) {
	release := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	reg := &spyMetadataRegistry{hit: true, entry: MetadataEntry{
		NativeContextTokens: intPtr(128000),
		Capabilities:        []models.Operation{models.OperationChat},
		Family:              "gpt-family",
		ReleaseDate:         &release,
		ExactIdentityMatch:  false,
		DatasetVersion:      "v1",
	}}
	qual := &spyQualityIndex{hit: true, entry: QualityEntry{Rating: floatPtr(50), ExactIdentityMatch: false}}

	svc := NewEnrichmentService(alwaysOn, reg.fn(), qual.fn(), nil, fixedFreeSafetyNow)
	got := svc.Enrich(context.Background(), Scope{}, "prov1", "model-a")

	seen := map[string]bool{}
	for _, ev := range got {
		if ev.Field == FieldNativeContextTokens || ev.Field == CapabilityField(models.OperationChat) {
			t.Fatalf("hard fact %q emitted on a non-exact match, want dropped entirely", ev.Field)
		}
		seen[ev.Field] = true
		if ev.Source != SourceHeuristic {
			t.Fatalf("evidence[%q].Source = %q, want %q for a non-exact match", ev.Field, ev.Source, SourceHeuristic)
		}
	}
	for _, want := range []string{FieldFamily, FieldReleaseDate, FieldQualityRating} {
		if !seen[want] {
			t.Fatalf("expected soft field %q missing from %+v", want, got)
		}
	}
}

// TestEnrich_NonExactSoftFact_ResolvesToProbeSuggested proves a heuristic-
// sourced fact can only ever suggest a probe, never certify (04 §2/§4).
func TestEnrich_NonExactSoftFact_ResolvesToProbeSuggested(t *testing.T) {
	reg := &spyMetadataRegistry{hit: true, entry: MetadataEntry{Family: "gpt-family", ExactIdentityMatch: false, DatasetVersion: "v1"}}
	svc := NewEnrichmentService(alwaysOn, reg.fn(), nil, nil, fixedFreeSafetyNow)
	scope := Scope{AccountID: "acct1", ProviderModelID: "model-a"}
	evidence := svc.Enrich(context.Background(), scope, "prov1", "model-a")

	res := Resolve(FieldFamily, evidence, fixedFreeSafetyNow())
	if res.Kind != ResolutionProbeSuggested {
		t.Fatalf("Resolve(family).Kind = %q, want %q", res.Kind, ResolutionProbeSuggested)
	}
	if res.Reason != ReasonHeuristicCannotCertify {
		t.Fatalf("Resolve(family).Reason = %q, want %q", res.Reason, ReasonHeuristicCannotCertify)
	}
}

// TestEnrich_LosesToProviderMetadata proves 04 §4's ladder: provider
// metadata outranks external-registry evidence by source rank alone,
// regardless of confidence or freshness. MUTATION: stamping
// SourceProviderMetadata instead of SourceExternalRegistry on an
// exact-match fact turns this RED (both sides would then tie-break instead
// of the provider cleanly winning by rank).
func TestEnrich_LosesToProviderMetadata(t *testing.T) {
	reg := &spyMetadataRegistry{hit: true, entry: MetadataEntry{
		NativeContextTokens: intPtr(999999), ExactIdentityMatch: true, DatasetVersion: "v1",
	}}
	svc := NewEnrichmentService(alwaysOn, reg.fn(), nil, nil, fixedFreeSafetyNow)
	scope := Scope{AccountID: "acct1", ProviderModelID: "model-a"}
	enrichmentEvidence := svc.Enrich(context.Background(), scope, "prov1", "model-a")

	// Verification/Confidence/ObservedAt are deliberately set to a WORSE
	// tie-break position than the enrichment evidence (same VerificationDeclared
	// tier, lower confidence, older timestamp) so this test isolates source
	// RANK as the only thing that can make provider metadata win: if a
	// mutation ever collapsed the two Sources to the same rank, the
	// tie-break chain would flip the result to enrichment's value instead.
	providerEvidence := Evidence{
		Field: FieldNativeContextTokens, Scope: scope,
		Source: SourceProviderMetadata, Verification: VerificationDeclared,
		Confidence: 0.1, ObservedAt: fixedFreeSafetyNow().Add(-1 * time.Hour),
		Value: 128000,
	}

	all := append([]Evidence{providerEvidence}, enrichmentEvidence...)
	res := Resolve(FieldNativeContextTokens, all, fixedFreeSafetyNow())
	if res.Kind != ResolutionKnown || res.Value != 128000 {
		t.Fatalf("Resolve(native_context_tokens) = %+v, want provider metadata (128000) to win despite enrichment being fresher/more confident", res)
	}
}

// TestEnrich_SeamFailure_UsesLastKnownGood proves the last-known-good
// fallback on a seam error. MUTATION: removing the fallback (returning
// nothing on error/miss) turns this RED.
func TestEnrich_SeamFailure_UsesLastKnownGood(t *testing.T) {
	reg := &spyMetadataRegistry{hit: true, entry: MetadataEntry{Family: "gpt", ExactIdentityMatch: true, DatasetVersion: "v1"}}
	svc := NewEnrichmentService(alwaysOn, reg.fn(), nil, nil, fixedFreeSafetyNow)

	if first := svc.Enrich(context.Background(), Scope{}, "prov1", "model-a"); len(first) == 0 {
		t.Fatalf("first Enrich returned no evidence")
	}

	reg.hit = false
	reg.entry = MetadataEntry{}
	reg.err = errors.New("registry unavailable")

	second := svc.Enrich(context.Background(), Scope{}, "prov1", "model-a")
	if !hasFieldValue(second, FieldFamily, "gpt") {
		t.Fatalf("second Enrich (seam failing) = %+v, want the cached family=gpt fact", second)
	}
}

// TestEnrich_LastKnownGoodNeverExpires proves enrichment has NO staleness
// gate, unlike pipeline A (04 §2b). MUTATION: adding any expiry check to
// the cached-entry path turns this RED.
func TestEnrich_LastKnownGoodNeverExpires(t *testing.T) {
	reg := &spyMetadataRegistry{hit: true, entry: MetadataEntry{Family: "gpt", ExactIdentityMatch: true, DatasetVersion: "v1"}}
	clock := fixedFreeSafetyNow()
	now := func() time.Time { return clock }
	svc := NewEnrichmentService(alwaysOn, reg.fn(), nil, nil, now)

	if first := svc.Enrich(context.Background(), Scope{}, "prov1", "model-a"); len(first) == 0 {
		t.Fatalf("first Enrich returned no evidence")
	}

	reg.hit = false
	reg.err = errors.New("registry unavailable")
	clock = clock.Add(30 * 24 * time.Hour)

	got := svc.Enrich(context.Background(), Scope{}, "prov1", "model-a")
	if !hasFieldValue(got, FieldFamily, "gpt") {
		t.Fatalf("Enrich 30 days later (seam failing) = %+v, want the cached fact still emitted (no staleness gate)", got)
	}
}

// TestEnrich_InvalidEntry_EmitsNothingAndIsNotCached covers every schema-
// validation rule (04 §2b): each invalid entry yields zero evidence, and a
// SUBSEQUENT failing-seam call still emits nothing — proving the invalid
// entry was never cached. MUTATION: skipping any one of these checks turns
// the corresponding case RED.
func TestEnrich_InvalidEntry_EmitsNothingAndIsNotCached(t *testing.T) {
	metadataCases := []struct {
		name  string
		entry MetadataEntry
	}{
		{"zero context", MetadataEntry{NativeContextTokens: intPtr(0), ExactIdentityMatch: true}},
		{"negative context", MetadataEntry{NativeContextTokens: intPtr(-1), ExactIdentityMatch: true}},
		{"control char in family", MetadataEntry{Family: "gpt\x01x", ExactIdentityMatch: true}},
		{"unparseable capability", MetadataEntry{Capabilities: []models.Operation{"not_a_real_operation"}, ExactIdentityMatch: true}},
		{"zero release date", MetadataEntry{ReleaseDate: &time.Time{}, ExactIdentityMatch: true}},
	}
	for _, tc := range metadataCases {
		t.Run(tc.name, func(t *testing.T) {
			reg := &spyMetadataRegistry{hit: true, entry: tc.entry}
			svc := NewEnrichmentService(alwaysOn, reg.fn(), nil, nil, fixedFreeSafetyNow)

			if got := svc.Enrich(context.Background(), Scope{}, "prov1", "model-a"); len(got) != 0 {
				t.Fatalf("Enrich(%s) = %+v, want zero evidence", tc.name, got)
			}

			reg.hit = false
			reg.entry = MetadataEntry{}
			reg.err = errors.New("seam now failing")
			if second := svc.Enrich(context.Background(), Scope{}, "prov1", "model-a"); len(second) != 0 {
				t.Fatalf("Enrich(%s) after invalid entry then a failing seam = %+v, want zero (the invalid entry must never have been cached)", tc.name, second)
			}
		})
	}

	ratingCases := []struct {
		name   string
		rating float64
	}{
		{"rating below zero", -1},
		{"rating above 100", 101},
	}
	for _, tc := range ratingCases {
		t.Run(tc.name, func(t *testing.T) {
			qual := &spyQualityIndex{hit: true, entry: QualityEntry{Rating: floatPtr(tc.rating), ExactIdentityMatch: true}}
			svc := NewEnrichmentService(alwaysOn, nil, qual.fn(), nil, fixedFreeSafetyNow)

			if got := svc.Enrich(context.Background(), Scope{}, "prov1", "model-a"); len(got) != 0 {
				t.Fatalf("Enrich(%s) = %+v, want zero evidence", tc.name, got)
			}

			qual.hit = false
			qual.entry = QualityEntry{}
			qual.err = errors.New("seam now failing")
			if second := svc.Enrich(context.Background(), Scope{}, "prov1", "model-a"); len(second) != 0 {
				t.Fatalf("Enrich(%s) after invalid rating then a failing seam = %+v, want zero", tc.name, second)
			}
		})
	}
}

// TestEnrich_SeamsReceiveIdentityOnly proves no prompt/credential/token
// ever reaches the seams — only the bare identity pair. MUTATION:
// appending anything to the identity passed to the registry seam turns
// this RED.
func TestEnrich_SeamsReceiveIdentityOnly(t *testing.T) {
	reg := &spyMetadataRegistry{hit: true, entry: MetadataEntry{ExactIdentityMatch: true}}
	qual := &spyQualityIndex{hit: true, entry: QualityEntry{ExactIdentityMatch: true}}
	svc := NewEnrichmentService(alwaysOn, reg.fn(), qual.fn(), nil, fixedFreeSafetyNow)

	svc.Enrich(context.Background(), Scope{AccountID: "acct1"}, "prov1", "model-a-exact")

	if reg.lastProviderID != "prov1" || reg.lastProviderModelID != "model-a-exact" {
		t.Fatalf("registry seam received (%q,%q), want (prov1,model-a-exact) verbatim, no suffix/prefix/token", reg.lastProviderID, reg.lastProviderModelID)
	}
	if qual.lastProviderID != "prov1" || qual.lastProviderModelID != "model-a-exact" {
		t.Fatalf("quality seam received (%q,%q), want (prov1,model-a-exact) verbatim, no suffix/prefix/token", qual.lastProviderID, qual.lastProviderModelID)
	}
}

// TestFreeSafetyUnaffectedByEnrichmentToggle is the card's mandated
// cross-check: pipeline A's resolution must be byte-for-byte identical
// regardless of whether pipeline B is disabled, enabled and succeeding, or
// enabled and failing — and pipeline A's own CostCache must see zero
// interactions caused by enrichment. MUTATION: threading an
// enrichmentEnabled flag into FreeSafetyResolver.Resolve and gating it
// turns this RED (see the batch report for the restore procedure).
func TestFreeSafetyUnaffectedByEnrichmentToggle(t *testing.T) {
	costCache := newSpyCostCache()
	costCache.Set("prov1", "model-stale", CostDatasetEntry{IsFree: true, ExactIdentityMatch: true, DatasetVersion: "v1"}, fixedFreeSafetyNow().Add(-20*time.Minute))
	resolver := NewFreeSafetyResolver((&fakeCostDataset{hit: false}).fn(), costCache, DefaultFreeSafetyConfig(), fixedFreeSafetyNow)

	offerings := []struct {
		providerID, providerModelID string
		pricing                     map[string]any
	}{
		{"prov1", "model-free", zeroCostPricing()},
		{"prov1", "model-paid", paidPricing()},
		{"opencode-zen", "model-miss", nil},
		{"prov1", "model-stale", nil},
	}

	baseline := make([]ResolvedCostFact, len(offerings))
	for i, o := range offerings {
		baseline[i] = resolver.Resolve(context.Background(), o.providerID, o.providerModelID, o.pricing, nil)
	}

	disabledSvc := NewEnrichmentService(nil, nil, nil, nil, fixedFreeSafetyNow)
	enabledSvc := NewEnrichmentService(alwaysOn,
		func(context.Context, string, string) (MetadataEntry, bool, error) {
			return MetadataEntry{NativeContextTokens: intPtr(1000), ExactIdentityMatch: true}, true, nil
		},
		func(context.Context, string, string) (QualityEntry, bool, error) {
			return QualityEntry{Rating: floatPtr(90), ExactIdentityMatch: true}, true, nil
		},
		nil, fixedFreeSafetyNow)
	failingSvc := NewEnrichmentService(alwaysOn,
		func(context.Context, string, string) (MetadataEntry, bool, error) {
			return MetadataEntry{}, false, errors.New("registry down")
		},
		func(context.Context, string, string) (QualityEntry, bool, error) {
			return QualityEntry{}, false, errors.New("leaderboard down")
		},
		nil, fixedFreeSafetyNow)

	for _, svc := range []*EnrichmentService{disabledSvc, enabledSvc, failingSvc} {
		callsBefore := costCache.calls
		svc.Enrich(context.Background(), Scope{}, "prov1", "model-free")
		if costCache.calls != callsBefore {
			t.Fatalf("Enrich touched pipeline A's CostCache: calls went from %d to %d", callsBefore, costCache.calls)
		}

		for i, o := range offerings {
			got := resolver.Resolve(context.Background(), o.providerID, o.providerModelID, o.pricing, nil)
			if !reflect.DeepEqual(got, baseline[i]) {
				t.Fatalf("ResolvedCostFact changed for %s/%s across enrichment scenarios: %+v vs baseline %+v", o.providerID, o.providerModelID, got, baseline[i])
			}
		}
	}
}

func hasFieldValue(evidence []Evidence, field string, value any) bool {
	for _, ev := range evidence {
		if ev.Field == field && ev.Value == value {
			return true
		}
	}
	return false
}
