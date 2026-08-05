package intelligence

import (
	"context"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
)

// EnrichmentService is pipeline B (04 §2/§2b): optional, owner-enabled
// background enrichment of non-routing-critical facts (context, family,
// release date, quality) from models.dev and an analysis leaderboard. It
// is STRUCTURALLY INDEPENDENT of pipeline A (P3a-DISC-003): it never
// references CostDataset, CostCache, ResolvedCostFact, FreeSafetyResolver,
// or Eligible, and never reads or writes pipeline A's cache. Disabling
// this service never weakens free-safety, because free-safety never
// depends on it in the first place (04 §2b: "disabling it never weakens
// free-safety").
type EnrichmentService struct {
	enabled  EnrichmentSwitch
	registry MetadataRegistry
	quality  QualityIndex
	cache    EnrichmentCache
	now      func() time.Time
}

// NewEnrichmentService builds an EnrichmentService. enabled is consulted
// on every Enrich call — a nil enabled behaves exactly like a switch that
// always returns false (off by default, 04 §2b). registry/quality may be
// nil, behaving like a seam that always misses. cache defaults to
// NewInMemoryEnrichmentCache when nil. now defaults to time.Now when nil.
func NewEnrichmentService(enabled EnrichmentSwitch, registry MetadataRegistry, quality QualityIndex, cache EnrichmentCache, now func() time.Time) *EnrichmentService {
	if cache == nil {
		cache = NewInMemoryEnrichmentCache()
	}
	if now == nil {
		now = time.Now
	}
	return &EnrichmentService{enabled: enabled, registry: registry, quality: quality, cache: cache, now: now}
}

// Enrich resolves scope's non-routing-critical facts for one offering
// (providerID, providerModelID) and returns them as precedence-engine
// Evidence, ready to be merged into a Resolve call alongside every other
// source. It NEVER sends a prompt or credential upstream — the seams
// receive only the identity pair — and is never on any request hot path.
//
// If enrichment is off (nil switch, or the switch reports false), Enrich
// returns nil and calls NEITHER seam — no upstream traffic at all.
func (s *EnrichmentService) Enrich(ctx context.Context, scope Scope, providerID, providerModelID string) []Evidence {
	if s.enabled == nil || !s.enabled(ctx) {
		return nil
	}

	now := s.now()
	var out []Evidence

	if entry, ok := s.resolveMetadata(ctx, providerID, providerModelID); ok {
		out = append(out, metadataEvidence(scope, entry, now)...)
	}
	if entry, ok := s.resolveQuality(ctx, providerID, providerModelID); ok {
		out = append(out, QualityEvidence(scope, entry, now)...)
	}

	return out
}

// resolveMetadata implements 04 §2b's cache contract for the metadata
// seam: a fresh seam hit is validated then cached and used; a seam
// error/miss falls back to the last-known-good cache entry (which never
// expires — no staleness gate for enrichment); a fresh hit that fails
// validation is discarded outright (not cached, no fallback attempted this
// call — an invalid read is not the same failure mode as an absent one).
func (s *EnrichmentService) resolveMetadata(ctx context.Context, providerID, providerModelID string) (MetadataEntry, bool) {
	if s.registry != nil {
		entry, hit, err := s.registry(ctx, providerID, providerModelID)
		if err == nil && hit {
			if !validMetadataEntry(entry) {
				return MetadataEntry{}, false
			}
			s.cache.SetMetadata(providerID, providerModelID, entry)
			return entry, true
		}
	}
	return s.cache.GetMetadata(providerID, providerModelID)
}

func (s *EnrichmentService) resolveQuality(ctx context.Context, providerID, providerModelID string) (QualityEntry, bool) {
	if s.quality != nil {
		entry, hit, err := s.quality(ctx, providerID, providerModelID)
		if err == nil && hit {
			if !validQualityEntry(entry) {
				return QualityEntry{}, false
			}
			s.cache.SetQuality(providerID, providerModelID, entry)
			return entry, true
		}
	}
	return s.cache.GetQuality(providerID, providerModelID)
}

// validMetadataEntry fails closed on any field that is present but
// malformed (04 §2b's schema-validation requirement). validText is
// discovery.go's UTF-8/control-character check (04 §1 step 4), reused here
// rather than duplicated; an empty string is valid text, so an absent
// Family or DatasetVersion never fails an otherwise-sound entry.
func validMetadataEntry(e MetadataEntry) bool {
	if e.NativeContextTokens != nil && *e.NativeContextTokens <= 0 {
		return false
	}
	if !validText(e.Family) || !validText(e.DatasetVersion) {
		return false
	}
	for _, c := range e.Capabilities {
		if _, err := models.ParseOperation(string(c)); err != nil {
			return false
		}
	}
	if e.ReleaseDate != nil && e.ReleaseDate.IsZero() {
		return false
	}
	return true
}

func validQualityEntry(e QualityEntry) bool {
	if e.Rating != nil && (*e.Rating < 0 || *e.Rating > 100) {
		return false
	}
	if !validText(e.SourceRef) || !validText(e.DatasetVersion) {
		return false
	}
	return true
}

// enrichmentProvenance derives the (source, verification, confidence)
// triple an enrichment fact is stamped with (04 §2b/§4): an exact-identity
// match is the weakest REAL source in the precedence ladder
// (SourceExternalRegistry), so any provider metadata or probe evidence for
// the same field always outranks it. A non-exact (family/name) match is
// stamped SourceHeuristic, which Resolve can only ever downgrade to
// probe_suggested — it can suggest a probe, never certify (04 §2:
// "external data is the weakest source and can only *enable* a hard
// capability after exact identity mapping... a name/family match stays
// soft evidence"). Both confidences are deliberately low (0.5, 0.2) so
// enrichment loses every confidence tie-break against an observed or
// verified source too.
func enrichmentProvenance(exactIdentityMatch bool) (EvidenceSource, VerificationStatus, float64) {
	if exactIdentityMatch {
		return SourceExternalRegistry, VerificationDeclared, 0.5
	}
	return SourceHeuristic, VerificationDeclared, 0.2
}

// metadataEvidence builds entry's Evidence in 04 §2b's mandated
// deterministic order: native_context_tokens, then capability.<op> in
// models.Operations() order, then family, then release_date. The HARD
// facts (context, capability) are built only inside the exact-match
// branch — on a non-exact match they are dropped entirely, never
// downgraded (04 §2b: "a name/family match is never sufficient" to prove a
// hard capability). The SOFT facts (family, release_date) are built either
// way, so they naturally still land in the same overall field order.
func metadataEvidence(scope Scope, entry MetadataEntry, now time.Time) []Evidence {
	source, verification, confidence := enrichmentProvenance(entry.ExactIdentityMatch)
	var out []Evidence

	base := func(field string, value any) Evidence {
		return Evidence{
			Field:              field,
			Scope:              scope,
			Source:             source,
			Verification:       verification,
			Confidence:         confidence,
			ObservedAt:         now,
			ProvenNegative:     false,
			Value:              value,
			DatasetVersion:     entry.DatasetVersion,
			ExactIdentityMatch: entry.ExactIdentityMatch,
		}
	}

	if entry.ExactIdentityMatch {
		if entry.NativeContextTokens != nil {
			out = append(out, base(FieldNativeContextTokens, *entry.NativeContextTokens))
		}
		for _, op := range models.Operations() {
			if containsOperation(entry.Capabilities, op) {
				out = append(out, base(CapabilityField(op), true))
			}
		}
	}

	if entry.Family != "" {
		out = append(out, base(FieldFamily, entry.Family))
	}
	if entry.ReleaseDate != nil {
		out = append(out, base(FieldReleaseDate, *entry.ReleaseDate))
	}

	return out
}

// QualityEvidence turns one analysis-leaderboard QualityEntry into
// precedence-engine Evidence (04 §2b/§4).
//
// Its ONE caller today is EnrichmentService.Enrich in this file. It stays
// EXPORTED so that any future caller reading the leaderboard stamps the
// identical provenance for the identical row rather than hand-rolling a
// second Evidence literal, which would become a second source of truth for
// how strong a leaderboard claim is.
//
// POST /models/{id}/benchmark used to be that second caller. Plan 3 of the
// local-benchmark-rating design (Task 5, 2026-08-05) replaced its imported
// leaderboard lookup with a real local measurement suite (spec D4: "No
// imported leaderboard numbers"), so it no longer produces leaderboard
// evidence at all — it stamps its own local-benchmark provenance into the
// audit trail instead (internal/httpapi.benchmarkRunProvenanceReason).
//
// An entry with a nil Rating yields NO evidence: the leaderboard knowing the
// model but having no score for it is 04 §3”'s "no quality signal available",
// which must stay unknown rather than becoming a value.
func QualityEvidence(scope Scope, entry QualityEntry, now time.Time) []Evidence {
	if entry.Rating == nil {
		return nil
	}
	source, verification, confidence := enrichmentProvenance(entry.ExactIdentityMatch)
	return []Evidence{{
		Field:              FieldQualityRating,
		Scope:              scope,
		Source:             source,
		Verification:       verification,
		Confidence:         confidence,
		ObservedAt:         now,
		ProvenNegative:     false,
		Value:              *entry.Rating,
		DatasetVersion:     entry.DatasetVersion,
		ExactIdentityMatch: entry.ExactIdentityMatch,
	}}
}

func containsOperation(ops []models.Operation, target models.Operation) bool {
	for _, op := range ops {
		if op == target {
			return true
		}
	}
	return false
}
