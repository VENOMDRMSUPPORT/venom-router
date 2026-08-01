package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/intelligence"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// ModelsHandler serves the P3a-CAPI-001 effective-offering read model:
// GET /api/control/v1/models and GET /api/control/v1/offerings. Both read
// storage.CatalogRepo's plain rows and assemble intelligence.Project's
// ProjectionInput — the ONE shared effective-offering projection (04 §3) —
// never re-deriving context, capability, quality, or eligibility itself.
// Owner-session + CSRF gated via ControlMux's `gated`; GET emits no audit
// event (reads are not audited, like GET /accounts and GET /settings).
type ModelsHandler struct {
	catalog  *storage.CatalogRepo
	resolver *intelligence.FreeSafetyResolver
	now      func() time.Time
}

// NewModelsHandler builds the handler over catalog (the read-only M4 view)
// and now (defaulting to time.Now when nil). It constructs its own
// FreeSafetyResolver with a NIL CostDataset seam: no models.dev HTTP client
// exists yet in this codebase (wiring one is a later unit's concern), so
// every offering's cost fact here can only ever come from the account's own
// persisted provider pricing_json or resolve to unknown (fail closed, 04
// §2b) — it never makes a network call. The resolver's in-memory cost cache
// is process-local and freshly empty at boot, which is correct: with no
// dataset seam there is nothing for it to ever populate anyway.
func NewModelsHandler(catalog *storage.CatalogRepo, now func() time.Time) *ModelsHandler {
	if now == nil {
		now = time.Now
	}
	resolver := intelligence.NewFreeSafetyResolver(nil, intelligence.NewInMemoryCostCache(), intelligence.DefaultFreeSafetyConfig(), now)
	return &ModelsHandler{catalog: catalog, resolver: resolver, now: now}
}

// buildProjection assembles one offering's intelligence.ProjectionInput from
// its storage.CatalogOfferingRow and calls intelligence.Project — the ONLY
// place this handler computes anything; every field below is either read
// verbatim from row or handed to a shared intelligence function.
func (h *ModelsHandler) buildProjection(ctx context.Context, row storage.CatalogOfferingRow) intelligence.EffectiveOffering {
	return intelligence.Project(h.buildProjectionInput(ctx, row))
}

// buildProjectionInput assembles one offering's intelligence.ProjectionInput
// from its storage.CatalogOfferingRow — split out from buildProjection so a
// test can assert directly on NativeCapabilities/TransportOperations (both
// of which must stay nil this phase) without that assertion collapsing
// through Project's AND-of-both-nil-sources "effective" computation, which
// on its own cannot distinguish "only one seam was wrongly populated" from
// "neither was."
func (h *ModelsHandler) buildProjectionInput(ctx context.Context, row storage.CatalogOfferingRow) intelligence.ProjectionInput {
	availability, err := models.ParseAvailability(row.Availability)
	if err != nil {
		// Fail closed (04 §2/§3): an unparseable/corrupt availability value
		// is never treated as available.
		availability = models.AvailabilityUnknown
	}

	var offeringOps []models.Operation
	for _, c := range row.Capabilities {
		if op, err := models.ParseOperation(c); err == nil {
			offeringOps = append(offeringOps, op)
		}
	}

	certs := make(map[models.Operation]models.Certification, len(row.Operations))
	for _, opRow := range row.Operations {
		op, err := models.ParseOperation(opRow.Operation)
		if err != nil {
			continue
		}
		state, err := models.ParseCertificationState(opRow.CertificationStatus)
		if err != nil {
			state = models.CertDiscovered
		}
		truth, err := models.ParseCapabilityTruth(opRow.CapabilityTruth)
		if err != nil {
			truth = models.TruthUnknown
		}
		certs[op] = models.Certification{
			OfferingOperationID: opRow.ID,
			State:               state,
			Truth:               truth,
			Version:             opRow.CertificationVersion,
			CertifiedAt:         opRow.CertifiedAt,
			EvidenceRef:         opRow.EvidenceRef,
		}
	}

	canonical := models.CanonicalModel{
		DisplayName:         row.ModelDisplayName,
		NativeContextTokens: row.NativeContextTokens,
		NativeModalities:    row.NativeModalities,
		QualityRating:       row.QualityRating,
	}

	offering := models.Offering{
		Identity:        models.OfferingIdentity{AccountID: row.AccountID, ProviderModelID: row.ProviderModelID},
		Availability:    availability,
		ContextLength:   row.ContextLength,
		MaxInputTokens:  row.MaxInputTokens,
		MaxOutputTokens: row.MaxOutputTokens,
		Capabilities:    offeringOps,
		FirstSeenAt:     row.FirstSeenAt,
		LastSeenAt:      row.LastSeenAt,
	}

	cost := h.resolver.Resolve(ctx, row.ProviderID, row.ProviderModelID, row.Pricing, nil)
	classification := intelligence.Classify(offeringOps, row.NativeModalities).Classification

	return intelligence.ProjectionInput{
		ProviderID: row.ProviderID,
		Canonical:  canonical,
		// NativeCapabilities and TransportOperations are ALWAYS nil this
		// phase (04 §2/§3): M4 persists no native-capability fact and no
		// transport-registry exists yet. Populating either from the
		// offering's own exposed capabilities, a model name, or an adapter
		// id would fabricate a capability that was never actually observed
		// — Project's fail-closed intersection already treats a nil source
		// as UNKNOWN, so every capability here correctly reports
		// effective=false until a later unit supplies real evidence.
		NativeCapabilities:  nil,
		Offering:            offering,
		TransportOperations: nil,
		Certifications:      certs,
		Cost:                cost,
		Classification:      classification,
	}
}

// --- JSON projection ---

// capabilityJSON is one operation's capability truth on the wire.
//
// OfferingOperationID is `omitempty` DELIBERATELY, and the omission carries
// meaning: it identifies the offering_operations row POST /offerings/{id}/probe
// is keyed by, and an operation with no such row has nothing to probe. Serving
// `"offering_operation_id": ""` would hand a client a present-but-meaningless
// identifier it could pass straight to the probe endpoint; omitting the key says
// "not probeable" unambiguously. The value is never composed here — it comes from
// the certification row via intelligence.Project.
type capabilityJSON struct {
	Operation           string `json:"operation"`
	Effective           bool   `json:"effective"`
	State               string `json:"state"`
	Truth               string `json:"truth"`
	Routable            bool   `json:"routable"`
	OfferingOperationID string `json:"offering_operation_id,omitempty"`
}

type costJSON struct {
	IsFree             *bool   `json:"is_free"`
	Source             string  `json:"source,omitempty"`
	Conflict           bool    `json:"conflict"`
	DatasetVersion     string  `json:"dataset_version,omitempty"`
	ObservedAt         string  `json:"observed_at,omitempty"`
	Confidence         float64 `json:"confidence"`
	ExactIdentityMatch bool    `json:"exact_identity_match"`
	Stale              bool    `json:"stale"`
}

type tierJSON struct {
	Eligible bool     `json:"eligible"`
	Stale    bool     `json:"stale"`
	Penalty  bool     `json:"penalty"`
	Reasons  []string `json:"reasons,omitempty"`
}

// effectiveOfferingJSON is one offering's rendered EffectiveOffering (04
// §3). effective_context_tokens is a *int so an unknown context renders
// JSON null, NEVER 0 — the read-model's central truthfulness invariant.
type effectiveOfferingJSON struct {
	AccountID              string              `json:"account_id"`
	ProviderID             string              `json:"provider_id"`
	ProviderModelID        string              `json:"provider_model_id"`
	DisplayName            string              `json:"display_name,omitempty"`
	Availability           string              `json:"availability"`
	EffectiveContextTokens *int                `json:"effective_context_tokens"`
	ContextProvenance      string              `json:"context_provenance"`
	Capabilities           []capabilityJSON    `json:"capabilities"`
	QualityScore           float64             `json:"quality_score"`
	QualityKnown           bool                `json:"quality_known"`
	Cost                   costJSON            `json:"cost"`
	Classification         string              `json:"classification"`
	Tiers                  map[string]tierJSON `json:"tiers"`
}

func toEffectiveOfferingJSON(eo intelligence.EffectiveOffering) effectiveOfferingJSON {
	caps := make([]capabilityJSON, 0, len(eo.Capabilities))
	for _, c := range eo.Capabilities {
		caps = append(caps, capabilityJSON{
			Operation:           string(c.Operation),
			Effective:           c.Effective,
			State:               string(c.State),
			Truth:               string(c.Truth),
			Routable:            c.Routable,
			OfferingOperationID: c.OfferingOperationID,
		})
	}

	cost := costJSON{
		IsFree:             eo.Cost.IsFree,
		Source:             string(eo.Cost.Source),
		Conflict:           eo.Cost.Conflict,
		DatasetVersion:     eo.Cost.DatasetVersion,
		Confidence:         eo.Cost.Confidence,
		ExactIdentityMatch: eo.Cost.ExactIdentityMatch,
		Stale:              eo.Cost.Stale,
	}
	if !eo.Cost.ObservedAt.IsZero() {
		cost.ObservedAt = eo.Cost.ObservedAt.Format(time.RFC3339)
	}

	tiers := make(map[string]tierJSON, len(eo.Tiers))
	for tier, elig := range eo.Tiers {
		tiers[string(tier)] = tierJSON{
			Eligible: elig.Eligible,
			Stale:    elig.Stale,
			Penalty:  elig.Penalty,
			Reasons:  elig.Reasons,
		}
	}

	return effectiveOfferingJSON{
		AccountID:              eo.Identity.AccountID,
		ProviderID:             eo.ProviderID,
		ProviderModelID:        eo.Identity.ProviderModelID,
		DisplayName:            eo.DisplayName,
		Availability:           string(eo.Availability),
		EffectiveContextTokens: eo.EffectiveContextTokens,
		ContextProvenance:      string(eo.ContextProvenance),
		Capabilities:           caps,
		QualityScore:           eo.QualityScore,
		QualityKnown:           eo.QualityKnown,
		Cost:                   cost,
		Classification:         string(eo.Classification),
		Tiers:                  tiers,
	}
}

// --- GET /offerings ---

// ServeOfferings implements GET /api/control/v1/offerings (09 §2/04 §3): a
// cursor-paginated list of the shared effective-offering projection.
// Optional ?account_id= restricts to one account.
func (h *ModelsHandler) ServeOfferings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAuthError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false)
		return
	}

	ctx := r.Context()
	page := parsePageParams(r, defaultPageLimit, maxPageLimit)
	accountID := r.URL.Query().Get("account_id")

	rows, nextCursor, err := h.catalog.ListOfferings(ctx, storage.CatalogListParams{
		AccountID: accountID,
		Limit:     page.Limit,
		Cursor:    page.Cursor,
	})
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}

	items := make([]effectiveOfferingJSON, 0, len(rows))
	for _, row := range rows {
		items = append(items, toEffectiveOfferingJSON(h.buildProjection(ctx, row)))
	}

	writeDataMeta(w, http.StatusOK, items, paginationMeta(nextCursor))
}

// --- GET /models ---

// modelGroupJSON groups a page of offerings by their shared canonical
// model (04 §3). Grouping is presentation only — every field on the
// grouped offerings is exactly the same effectiveOfferingJSON /offerings
// renders; no context/capability/quality/eligibility value is computed
// here that Project did not already produce.
type modelGroupJSON struct {
	ModelID             string                  `json:"model_id"`
	DisplayName         string                  `json:"display_name,omitempty"`
	NativeContextTokens *int                    `json:"native_context_tokens"`
	QualityRating       *float64                `json:"quality_rating"`
	Offerings           []effectiveOfferingJSON `json:"offerings"`
}

// ServeModels implements GET /api/control/v1/models (09 §2/04 §3): the same
// effective-offering projections as /offerings, grouped by canonical model
// within the page. Optional ?account_id= restricts to one account; the
// underlying offering list is paginated exactly like /offerings, and
// grouping happens within that page.
func (h *ModelsHandler) ServeModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAuthError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false)
		return
	}

	ctx := r.Context()
	page := parsePageParams(r, defaultPageLimit, maxPageLimit)
	accountID := r.URL.Query().Get("account_id")

	rows, nextCursor, err := h.catalog.ListOfferings(ctx, storage.CatalogListParams{
		AccountID: accountID,
		Limit:     page.Limit,
		Cursor:    page.Cursor,
	})
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}

	var order []string
	groups := make(map[string]*modelGroupJSON)
	for _, row := range rows {
		g, ok := groups[row.ModelID]
		if !ok {
			g = &modelGroupJSON{
				ModelID:             row.ModelID,
				DisplayName:         row.ModelDisplayName,
				NativeContextTokens: row.NativeContextTokens,
				QualityRating:       row.QualityRating,
			}
			groups[row.ModelID] = g
			order = append(order, row.ModelID)
		}
		g.Offerings = append(g.Offerings, toEffectiveOfferingJSON(h.buildProjection(ctx, row)))
	}

	items := make([]modelGroupJSON, 0, len(order))
	for _, modelID := range order {
		items = append(items, *groups[modelID])
	}

	writeDataMeta(w, http.StatusOK, items, paginationMeta(nextCursor))
}
