package intelligence

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// CostSource is pipeline A's (free-safety resolution, 04 §1/§2b)
// provenance vocabulary. It is DISTINCT from accounts/domain.FundingSource
// (which classifies an ACCOUNT's funding authority) — this vocab instead
// records where one OFFERING's cost fact came from. The two must never be
// conflated: an account can be funding-classified "free" via
// owner_policy while an individual offering's cost fact is independently
// sourced from provider_price or models_dev.
type CostSource string

const (
	// CostSourceProviderPrice is the provider's own authenticated
	// per-model price (04 §1): the highest-ranked non-override source.
	CostSourceProviderPrice CostSource = "provider_price"
	// CostSourceModelsDev is the models.dev cost/entitlement dataset,
	// consulted only when the provider carries no usable per-model price.
	CostSourceModelsDev CostSource = "models_dev"
	// CostSourceOwnerOverride is an explicit owner-supplied override,
	// which wins outright over both other sources (04 §2b's precedence
	// row), mirroring P3a-DISC-006's owner-immunity rule.
	CostSourceOwnerOverride CostSource = "owner_override"
)

// ErrUnknownCostSource is returned by ParseCostSource for any value
// outside the fixed three-value vocabulary.
var ErrUnknownCostSource = errors.New("intelligence: unrecognized cost source")

// ParseCostSource fails closed on any value outside the exact three-value
// vocabulary — no case folding, no trimming.
func ParseCostSource(s string) (CostSource, error) {
	switch CostSource(s) {
	case CostSourceProviderPrice, CostSourceModelsDev, CostSourceOwnerOverride:
		return CostSource(s), nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnknownCostSource, s)
	}
}

// ResolvedCostFact is pipeline A's resolved, provenance-complete cost fact
// for one offering (04 §1/§2b). IsFree is nil when the offering's zero-cost
// status could not be verified — unknown stays unknown, NEVER coerced to
// false or true as a sentinel. Conflict is true when the provider's own
// price and the models.dev dataset disagree; per 04 §2b's per-tier table
// this forces exclusion from venom/lite regardless of which source wins
// the ordinary precedence resolution used for venom/pro and venom/max.
// Source, DatasetVersion, ObservedAt, Confidence, and ExactIdentityMatch
// are the required provenance tuple (04 §2b): a free-safety fact is only
// ever trustworthy with ExactIdentityMatch=true — a family/name match is
// never sufficient to prove free. Stale marks a fact reused from the
// last-known-good cache beyond its TTL but within the staleness window.
type ResolvedCostFact struct {
	IsFree             *bool
	Source             CostSource
	Conflict           bool
	DatasetVersion     string
	ObservedAt         time.Time
	Confidence         float64
	ExactIdentityMatch bool
	Stale              bool
}

// CostDatasetEntry is one models.dev cost-dataset lookup result: whether
// the dataset itself considers the model zero-cost, whether the match was
// exact (never a family/name match), and the dataset snapshot/version id.
type CostDatasetEntry struct {
	IsFree             bool
	ExactIdentityMatch bool
	DatasetVersion     string
}

// CostDataset is the injected models.dev cost-dataset read (04 §2b),
// mirroring providers.ChatProbe/ModelsProbe's function-type-seam pattern
// so this package stays free of net/http — the real HTTP client is wired
// by a later unit, never here. hit=false means the dataset has no entry
// for this model, a legitimate input rather than a failure. A non-nil err
// means the read itself failed (network, parse, etc.); FreeSafetyResolver
// treats a miss and an error identically for fail-closed purposes.
type CostDataset func(ctx context.Context, providerID, providerModelID string) (entry CostDatasetEntry, hit bool, err error)

// CostCache is the last-known-good store for models.dev cost lookups (04
// §2b's cache/TTL/staleness contract). It is a pure storage seam: the
// TTL/staleness DECISIONS (what counts as fresh, stale-but-usable, or
// expired) belong entirely to FreeSafetyResolver, never to a CostCache
// implementation. The composition root supplies a concrete implementation
// (in-memory or persisted); NewInMemoryCostCache in this package is a
// ready-to-use default that needs no I/O.
type CostCache interface {
	// Get returns the most recently cached entry for (providerID,
	// providerModelID) and fetchedAt — the time THIS process last
	// successfully populated it via Set, never the dataset's own internal
	// timestamp. ok=false means no entry has ever been cached for this key.
	Get(providerID, providerModelID string) (entry CostDatasetEntry, fetchedAt time.Time, ok bool)
	// Set records entry as the new last-known-good for (providerID,
	// providerModelID), stamping fetchedAt.
	Set(providerID, providerModelID string, entry CostDatasetEntry, fetchedAt time.Time)
}

// FreeSafetyConfig holds 04 §2b's owner-configurable cache/staleness
// knobs. DatasetTTL is how long a cached dataset entry is reused without
// re-querying the seam (~10-minute default). StalenessWindow is how long a
// cached entry remains usable — marked stale — after the seam misses or is
// unavailable (24-hour default) before the fact resolves to unknown.
type FreeSafetyConfig struct {
	DatasetTTL      time.Duration
	StalenessWindow time.Duration
}

// DefaultFreeSafetyConfig returns 04 §2b's documented defaults verbatim: a
// 10-minute dataset TTL and a 24-hour staleness window.
func DefaultFreeSafetyConfig() FreeSafetyConfig {
	return FreeSafetyConfig{DatasetTTL: 10 * time.Minute, StalenessWindow: 24 * time.Hour}
}

// costCacheEntry is one NewInMemoryCostCache row.
type costCacheEntry struct {
	entry     CostDatasetEntry
	fetchedAt time.Time
}

// inMemoryCostCache is a minimal, dependency-free CostCache: a plain map
// keyed by (providerID, providerModelID). It performs no I/O and reads no
// wall-clock time itself (fetchedAt is always caller-supplied), so it adds
// no impurity to this package.
type inMemoryCostCache struct {
	entries map[string]costCacheEntry
}

// NewInMemoryCostCache returns a ready-to-use, process-local CostCache. It
// is a convenience default for callers with no persistence requirement;
// the composition root may substitute any other CostCache implementation.
func NewInMemoryCostCache() CostCache {
	return &inMemoryCostCache{entries: make(map[string]costCacheEntry)}
}

func (c *inMemoryCostCache) Get(providerID, providerModelID string) (CostDatasetEntry, time.Time, bool) {
	row, ok := c.entries[costCacheKey(providerID, providerModelID)]
	if !ok {
		return CostDatasetEntry{}, time.Time{}, false
	}
	return row.entry, row.fetchedAt, true
}

func (c *inMemoryCostCache) Set(providerID, providerModelID string, entry CostDatasetEntry, fetchedAt time.Time) {
	c.entries[costCacheKey(providerID, providerModelID)] = costCacheEntry{entry: entry, fetchedAt: fetchedAt}
}

// costCacheKey combines providerID and providerModelID with a separator
// that cannot appear in either field's expected shape (both are typically
// slug-like); this is an internal convenience key, not a canonical
// identity (unlike models.CanonicalKey, which this package does not need
// here since the cache is process-local, not a persisted identity).
func costCacheKey(providerID, providerModelID string) string {
	return providerID + "\x00" + providerModelID
}
