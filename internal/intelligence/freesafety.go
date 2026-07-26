package intelligence

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Tier is the fixed three-value routing-tier vocabulary 04 §2b's per-tier
// eligibility table is keyed on.
type Tier string

const (
	TierLite Tier = "venom/lite"
	TierPro  Tier = "venom/pro"
	TierMax  Tier = "venom/max"
)

// ErrUnknownTier is returned by ParseTier for any value outside the fixed
// three-value vocabulary.
var ErrUnknownTier = errors.New("intelligence: unrecognized tier")

// ParseTier fails closed on any value outside the exact three-value
// vocabulary — no case folding, no trimming.
func ParseTier(s string) (Tier, error) {
	switch Tier(s) {
	case TierLite, TierPro, TierMax:
		return Tier(s), nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnknownTier, s)
	}
}

// EligibilityResult is Eligible's outcome: whether the tier may route to
// the offering at all, plus the informational flags 04 §2b's table
// attaches to a stale-free fact (Stale for every tier; Penalty — a score
// penalty, never an exclusion — for venom/pro and venom/max only).
type EligibilityResult struct {
	Eligible bool
	Stale    bool
	Penalty  bool
}

// Eligible applies 04 §2b's per-tier eligibility table to a resolved cost
// fact. It never derives eligibility from anything except fact itself —
// no enrichment flag, no account funding mode, no environment — so the
// same fact always yields the same result for a given tier.
//
// venom/lite: eligible only for a verified free fact with no conflicting
// evidence — a conflict FAILS CLOSED for lite regardless of which source
// would otherwise win under precedence (04 §2b's conflicting row: "fail
// closed → treat as paid/unknown → excluded from Lite").
//
// venom/pro and venom/max: eligible whenever the cost fact is KNOWN
// (free or paid) — including a resolved conflict, where the precedence-
// resolved IsFree already reflects "owner override > provider price >
// cost dataset" (04 §2b: "resolve by precedence... if unresolved →
// unknown → excluded"). An unknown fact excludes every tier alike,
// including pro/max — 04's guiding rule that unproven is ineligible for
// tiers applies here too, not only to lite.
//
// An unrecognized tier fails closed to excluded.
func Eligible(fact ResolvedCostFact, tier Tier) EligibilityResult {
	switch tier {
	case TierLite:
		if fact.Conflict || fact.IsFree == nil || !*fact.IsFree {
			return EligibilityResult{}
		}
		return EligibilityResult{Eligible: true, Stale: fact.Stale}
	case TierPro, TierMax:
		if fact.IsFree == nil {
			return EligibilityResult{}
		}
		return EligibilityResult{Eligible: true, Stale: fact.Stale, Penalty: fact.Stale}
	default:
		return EligibilityResult{}
	}
}

// FreeSafetyResolver resolves pipeline A's cost fact for one offering (04
// §1/§2b): owner override wins outright; else the provider's own
// authenticated per-model price; else the models.dev cost dataset, honored
// through a TTL-then-staleness-window cache. It is pure orchestration —
// every side effect goes through an injected port, and now is an injected
// clock, so every resolution is deterministic under test. It consults
// nothing about the optional metadata-enrichment toggle (P3a-DISC-004):
// pipeline A is always on and cannot be gated by it.
type FreeSafetyResolver struct {
	dataset CostDataset
	cache   CostCache
	cfg     FreeSafetyConfig
	now     func() time.Time
}

// NewFreeSafetyResolver builds a FreeSafetyResolver over dataset (the
// injected models.dev read seam), cache (the last-known-good store), and
// cfg (the TTL/staleness knobs — any non-positive field is replaced by
// DefaultFreeSafetyConfig's corresponding default). now defaults to
// time.Now when nil.
func NewFreeSafetyResolver(dataset CostDataset, cache CostCache, cfg FreeSafetyConfig, now func() time.Time) *FreeSafetyResolver {
	if cfg.DatasetTTL <= 0 {
		cfg.DatasetTTL = DefaultFreeSafetyConfig().DatasetTTL
	}
	if cfg.StalenessWindow <= 0 {
		cfg.StalenessWindow = DefaultFreeSafetyConfig().StalenessWindow
	}
	if now == nil {
		now = time.Now
	}
	return &FreeSafetyResolver{dataset: dataset, cache: cache, cfg: cfg, now: now}
}

// Resolve resolves one offering's cost fact. pricing is the raw,
// provider-reported Pricing map a DiscoveredModel/DiscoverySnapshotModel
// carries (04 §1's provider_price branch reads it). ownerOverride is nil
// when no owner override exists for this offering; a non-nil value is the
// owner's explicit free/paid decision, which wins outright over both other
// sources (mirroring P3a-DISC-006's owner-immunity) — whether an override
// is PRESENT is entirely the caller's concern, not this method's.
//
// Resolve never returns an error: every input, including a completely
// unavailable dataset seam, resolves to a ResolvedCostFact — in the worst
// case IsFree=nil (unknown), which is itself the fail-closed answer.
func (r *FreeSafetyResolver) Resolve(ctx context.Context, providerID, providerModelID string, pricing map[string]any, ownerOverride *bool) ResolvedCostFact {
	now := r.now()

	if ownerOverride != nil {
		return ResolvedCostFact{
			IsFree:             ownerOverride,
			Source:             CostSourceOwnerOverride,
			ExactIdentityMatch: true,
			ObservedAt:         now,
			Confidence:         1,
		}
	}

	providerFree, providerOK := extractProviderPrice(pricing)
	cached, fetchedAt, cacheHit := r.cache.Get(providerID, providerModelID)

	if providerOK {
		if cacheHit && cached.ExactIdentityMatch && cached.IsFree != providerFree {
			free := providerFree
			return ResolvedCostFact{
				IsFree:             &free,
				Source:             CostSourceProviderPrice,
				Conflict:           true,
				DatasetVersion:     cached.DatasetVersion,
				ExactIdentityMatch: true,
				ObservedAt:         now,
				Confidence:         1,
			}
		}
		free := providerFree
		return ResolvedCostFact{
			IsFree:             &free,
			Source:             CostSourceProviderPrice,
			ExactIdentityMatch: true,
			ObservedAt:         now,
			Confidence:         1,
		}
	}

	// No usable provider price: resolve via the models.dev dataset,
	// honoring the TTL-then-staleness-window cache contract (04 §2b).
	if cacheHit && now.Sub(fetchedAt) < r.cfg.DatasetTTL {
		return r.factFromDatasetEntry(cached, now, false)
	}

	if r.dataset != nil {
		entry, hit, err := r.dataset(ctx, providerID, providerModelID)
		if err == nil && hit {
			r.cache.Set(providerID, providerModelID, entry, now)
			return r.factFromDatasetEntry(entry, now, false)
		}
	}

	if cacheHit && now.Sub(fetchedAt) <= r.cfg.StalenessWindow {
		return r.factFromDatasetEntry(cached, now, true)
	}

	// No provider price, no fresh/stale-but-usable dataset entry: fail
	// closed to unknown (04 §1: "unverifiable ⇒ unknown ⇒ withdrawn").
	return ResolvedCostFact{ObservedAt: now}
}

// factFromDatasetEntry builds a ResolvedCostFact from a models.dev
// lookup result (fresh or reused-within-window). A non-exact match NEVER
// proves free (04 §2b: "a family/name match is never sufficient") — such
// an entry resolves to unknown rather than fabricating either a free or a
// paid claim from evidence too weak to support either.
func (r *FreeSafetyResolver) factFromDatasetEntry(entry CostDatasetEntry, now time.Time, stale bool) ResolvedCostFact {
	if !entry.ExactIdentityMatch {
		return ResolvedCostFact{
			Source:         CostSourceModelsDev,
			DatasetVersion: entry.DatasetVersion,
			ObservedAt:     now,
			Stale:          stale,
		}
	}
	free := entry.IsFree
	return ResolvedCostFact{
		IsFree:             &free,
		Source:             CostSourceModelsDev,
		DatasetVersion:     entry.DatasetVersion,
		ExactIdentityMatch: true,
		ObservedAt:         now,
		Confidence:         1,
		Stale:              stale,
	}
}

// extractProviderPrice inspects a DiscoveredModel's raw Pricing map for an
// authenticated per-model price (04 §1: "cost.input == 0 && cost.output ==
// 0 && status != deprecated" ⇒ free-safe). ok=false means the price is
// absent or too malformed to verify either way — the caller must fall
// through to the models.dev dataset. A non-zero cost is a definite paid
// claim regardless of status. A zero cost on a deprecated record does NOT
// count as verified-free (status disqualifies it) but is also not
// fabricated into a paid claim — ok=false, fall through to the dataset,
// rather than inventing a classification from a field that failed a
// different check.
func extractProviderPrice(pricing map[string]any) (isFree bool, ok bool) {
	if pricing == nil {
		return false, false
	}
	costRaw, hasCost := pricing["cost"]
	if !hasCost {
		return false, false
	}
	costMap, isMap := costRaw.(map[string]any)
	if !isMap {
		return false, false
	}
	input, inputOK := numericValue(costMap["input"])
	output, outputOK := numericValue(costMap["output"])
	if !inputOK || !outputOK {
		return false, false
	}
	if input != 0 || output != 0 {
		return false, true
	}
	if status, _ := pricing["status"].(string); status == "deprecated" {
		return false, false
	}
	return true, true
}

// numericValue extracts a float64 from the handful of numeric shapes a
// Pricing map's values may take (encoding/json produces float64; test
// fixtures and in-process callers may also use int/int64 directly).
func numericValue(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}
