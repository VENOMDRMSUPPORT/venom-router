package intelligence

import (
	"context"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
)

// Field-name constants for the Evidence this package emits into
// P3a-DISC-006's precedence engine, so downstream Resolve callers never
// have to hand-spell a string that could drift from what Enrich actually
// produces. CapabilityField derives the per-operation field name — each
// operation is its own independent precedence field, mirroring how
// certification is per-operation (04 §5).
const (
	FieldNativeContextTokens = "native_context_tokens"
	FieldCapability          = "capability"
	FieldFamily              = "family"
	FieldReleaseDate         = "release_date"
	FieldQualityRating       = "quality_rating"
)

// CapabilityField returns op's precedence-engine field name.
func CapabilityField(op models.Operation) string {
	return FieldCapability + "." + string(op)
}

// MetadataEntry is one models.dev METADATA lookup result (04 §2/§2b) — a
// DIFFERENT read from pipeline A's CostDatasetEntry, with separate
// provenance. nil/empty fields mean "the registry said nothing about this
// field" (unknown), never "zero" or "none" as a fact.
type MetadataEntry struct {
	NativeContextTokens *int
	Capabilities        []models.Operation
	Family              string
	ReleaseDate         *time.Time
	ExactIdentityMatch  bool
	DatasetVersion      string
}

// QualityEntry is one analysis-leaderboard lookup result (04 §3). Rating
// is on the canonical model's documented 0-100 scale; nil means the
// leaderboard has no signal for this model.
type QualityEntry struct {
	Rating             *float64
	SourceRef          string
	ExactIdentityMatch bool
	DatasetVersion     string
}

// MetadataRegistry is the injected models.dev METADATA read — a distinct
// seam from pipeline A's CostDataset, mirroring providers.ChatProbe/
// ModelsProbe's function-type pattern so this package stays free of
// net/http. hit=false is a legitimate "no entry" input, not a failure. The
// real HTTP client is wired by a later unit, never here.
type MetadataRegistry func(ctx context.Context, providerID, providerModelID string) (entry MetadataEntry, hit bool, err error)

// QualityIndex is the injected analysis-leaderboard read, structured
// identically to MetadataRegistry.
type QualityIndex func(ctx context.Context, providerID, providerModelID string) (entry QualityEntry, hit bool, err error)

// EnrichmentCache is the last-known-good store for enrichment reads (04
// §2b). Unlike pipeline A's CostCache, it carries NO timestamps and has NO
// expiry: "enrichment has no staleness gate — stale enrichment is simply
// not refreshed." A cached entry is valid indefinitely once written.
type EnrichmentCache interface {
	GetMetadata(providerID, providerModelID string) (MetadataEntry, bool)
	SetMetadata(providerID, providerModelID string, entry MetadataEntry)
	GetQuality(providerID, providerModelID string) (QualityEntry, bool)
	SetQuality(providerID, providerModelID string, entry QualityEntry)
}

// EnrichmentSwitch is the injected owner-toggle read (04 §2b: "off by
// default; owner-enabled"). The real settings-backed implementation is
// wired by P3a-CAPI-003 (PUT /settings/enrichment), never here. A nil
// EnrichmentSwitch means OFF — EnrichmentService normalizes this itself.
type EnrichmentSwitch func(ctx context.Context) bool

// enrichmentCacheRow is one NewInMemoryEnrichmentCache entry. Metadata and
// quality are tracked with independent presence flags because a key may
// have one, both, or neither cached (they arrive via separate seams and
// separate calls).
type enrichmentCacheRow struct {
	metadata    MetadataEntry
	hasMetadata bool
	quality     QualityEntry
	hasQuality  bool
}

// inMemoryEnrichmentCache is a minimal, dependency-free EnrichmentCache: a
// plain map keyed by (providerID, providerModelID). No I/O, no wall-clock
// read — consistent with this package's purity requirement.
type inMemoryEnrichmentCache struct {
	entries map[string]enrichmentCacheRow
}

// NewInMemoryEnrichmentCache returns a ready-to-use, process-local
// EnrichmentCache, mirroring NewInMemoryCostCache's convenience-default
// role for pipeline A.
func NewInMemoryEnrichmentCache() EnrichmentCache {
	return &inMemoryEnrichmentCache{entries: make(map[string]enrichmentCacheRow)}
}

func (c *inMemoryEnrichmentCache) GetMetadata(providerID, providerModelID string) (MetadataEntry, bool) {
	row, ok := c.entries[enrichmentCacheKey(providerID, providerModelID)]
	if !ok || !row.hasMetadata {
		return MetadataEntry{}, false
	}
	return row.metadata, true
}

func (c *inMemoryEnrichmentCache) SetMetadata(providerID, providerModelID string, entry MetadataEntry) {
	key := enrichmentCacheKey(providerID, providerModelID)
	row := c.entries[key]
	row.metadata = entry
	row.hasMetadata = true
	c.entries[key] = row
}

func (c *inMemoryEnrichmentCache) GetQuality(providerID, providerModelID string) (QualityEntry, bool) {
	row, ok := c.entries[enrichmentCacheKey(providerID, providerModelID)]
	if !ok || !row.hasQuality {
		return QualityEntry{}, false
	}
	return row.quality, true
}

func (c *inMemoryEnrichmentCache) SetQuality(providerID, providerModelID string, entry QualityEntry) {
	key := enrichmentCacheKey(providerID, providerModelID)
	row := c.entries[key]
	row.quality = entry
	row.hasQuality = true
	c.entries[key] = row
}

func enrichmentCacheKey(providerID, providerModelID string) string {
	return providerID + "\x00" + providerModelID
}
