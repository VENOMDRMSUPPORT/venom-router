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

	// probeRuns is task-5's capability-provenance seam: it answers "does a
	// SUCCEEDED probe run exist for this offering_operation_id" in one
	// batched query per page (WithProbeRuns wires it in, mirroring
	// DiscoveryHandler.WithProbeRuns — a copy-returning method rather than a
	// constructor parameter, so every existing NewModelsHandler call site
	// stays valid). A nil value is the correct fail-closed default: every
	// certified+supported non-chat capability renders "declared" rather
	// than fabricating "probed" for an unknown fact.
	probeRuns *storage.ProbeRunRepo

	// benchmarkRuns answers "when was this canonical model last benchmarked,
	// and how did that run go" for a whole page of model groups in ONE
	// batched query (BenchmarkRunRepo.LatestForModels), wired by
	// WithBenchmarkRuns exactly like probeRuns above.
	//
	// It exists to give a rating the DATED provenance 04 §3 and the
	// local-benchmark spec require ("local benchmark, <date>"): without the
	// run's finished_at a surface can only ever say "Local benchmark", and
	// without its successes/requests it cannot tell that a rating SURVIVED a
	// later partial-failure run rather than being produced by it.
	//
	// A nil value is the correct fail-closed default: every group reports
	// latest_benchmark: null, which reads as "never benchmarked" — never as a
	// fabricated date.
	benchmarkRuns *storage.BenchmarkRunRepo
}

// WithProbeRuns returns a copy of h with probeRuns wired in (task-5). See
// the probeRuns field doc for why this is a copy-returning method.
func (h *ModelsHandler) WithProbeRuns(probeRuns *storage.ProbeRunRepo) *ModelsHandler {
	clone := *h
	clone.probeRuns = probeRuns
	return &clone
}

// WithBenchmarkRuns returns a copy of h with benchmarkRuns wired in. See the
// benchmarkRuns field doc for what it supplies and why nil is safe.
func (h *ModelsHandler) WithBenchmarkRuns(benchmarkRuns *storage.BenchmarkRunRepo) *ModelsHandler {
	clone := *h
	clone.benchmarkRuns = benchmarkRuns
	return &clone
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

// collectOfferingOperationThresholds gathers every offering_operations row
// id present across rows' certified operations, deduplicated, mapped to
// THAT id's own certification's certified_at (Unix seconds; 0 when the
// operation has no certified_at at all) — the batched per-id threshold set
// task-5's provenance lookup queries with, ONE query per page rather than
// one per offering-operation.
//
// The threshold is load-bearing (whole-branch-review fix, 2026-08-05): a
// succeeded probe_runs row only proves the CURRENT certification when it
// finished at or after that certification was earned. Without a per-id
// threshold, a stale succeeded run from a PRIOR (now-expired,
// re-certified-from-declaration) certification could launder the new one as
// "probed" purely because some older probe happened to have succeeded once.
func collectOfferingOperationThresholds(rows []storage.CatalogOfferingRow) map[string]int64 {
	thresholds := make(map[string]int64)
	for _, row := range rows {
		for _, op := range row.Operations {
			if op.ID == "" {
				continue
			}
			if _, seen := thresholds[op.ID]; seen {
				continue
			}
			var threshold int64
			if op.CertifiedAt != nil {
				threshold = op.CertifiedAt.Unix()
			}
			thresholds[op.ID] = threshold
		}
	}
	return thresholds
}

// succeededProbeIDs runs task-5's ONE batched probe_runs query for an
// entire page of rows. h.probeRuns == nil (WithProbeRuns never called) and
// a page with no offering-operations at all both short-circuit to nil
// without touching storage — nil is read identically to an empty map by
// every lookup below (fail closed: no known succeeded probe run demotes a
// non-chat certified capability to "declared", never fabricates "probed").
func (h *ModelsHandler) succeededProbeIDs(ctx context.Context, rows []storage.CatalogOfferingRow) (map[string]bool, error) {
	if h.probeRuns == nil {
		return nil, nil
	}
	thresholds := collectOfferingOperationThresholds(rows)
	if len(thresholds) == 0 {
		return nil, nil
	}
	return h.probeRuns.SucceededOfferingOperationIDs(ctx, thresholds)
}

// buildProjection assembles one offering's intelligence.ProjectionInput from
// its storage.CatalogOfferingRow and calls intelligence.Project — the ONLY
// place this handler computes anything; every field below is either read
// verbatim from row or handed to a shared intelligence function. succeeded
// is the page-batched task-5 provenance fact from succeededProbeIDs (nil is
// fine — every operation lookup against a nil map reads as "no succeeded
// run known").
func (h *ModelsHandler) buildProjection(ctx context.Context, row storage.CatalogOfferingRow, succeeded map[string]bool) intelligence.EffectiveOffering {
	return intelligence.Project(h.buildProjectionInput(ctx, row, succeeded))
}

// buildProjectionInput assembles one offering's intelligence.ProjectionInput
// from its storage.CatalogOfferingRow — split out from buildProjection so a
// test can assert directly on NativeCapabilities/TransportOperations (both
// of which must stay nil this phase) without that assertion collapsing
// through Project's AND-of-both-nil-sources "effective" computation, which
// on its own cannot distinguish "only one seam was wrongly populated" from
// "neither was." succeeded is task-5's page-batched succeeded-probe fact.
func (h *ModelsHandler) buildProjectionInput(ctx context.Context, row storage.CatalogOfferingRow, succeeded map[string]bool) intelligence.ProjectionInput {
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
	// provedOps is task-5's per-operation "a succeeded probe run exists"
	// fact, keyed by models.Operation (the projection's own vocabulary)
	// rather than by the raw offering_operation_id — built here, alongside
	// certs, from the SAME opRow.ID -> op mapping so it can never point at
	// the wrong operation's row.
	provedOps := make(map[models.Operation]bool, len(row.Operations))
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
		if succeeded[opRow.ID] {
			provedOps[op] = true
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
		ProvedOperations:    provedOps,
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
//
// Provenance is NEVER omitempty, unlike OfferingOperationID: "" is itself a
// meaningful closed value here (task-5) — "this capability is not
// certified+supported, so provenance does not apply" — and a client must be
// able to tell that state apart from the key being absent by some other
// bug, so the empty string is always sent explicitly.
type capabilityJSON struct {
	Operation           string `json:"operation"`
	Effective           bool   `json:"effective"`
	State               string `json:"state"`
	Truth               string `json:"truth"`
	Routable            bool   `json:"routable"`
	OfferingOperationID string `json:"offering_operation_id,omitempty"`
	Provenance          string `json:"provenance"`
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
			Provenance:          c.Provenance,
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

	// /offerings is the per-account CATALOG view, not the live surface: its
	// sub-resources (/offerings/{id}/probe, /offerings/{id}/certification)
	// must be able to reach an offering that is not live yet — that probe is
	// HOW an offering becomes live, so filtering here would deadlock
	// certification, and it breaks the P3a gate contract ("nothing filtered
	// out"). The live definition is applied by ServeModels, which is what
	// backs the Live Models surface.
	rows, nextCursor, err := h.catalog.ListOfferings(ctx, storage.CatalogListParams{
		AccountID: accountID,
		Limit:     page.Limit,
		Cursor:    page.Cursor,
	})
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}

	// ONE batched probe_runs query for this whole page (task-5) — never one
	// per offering-operation.
	succeeded, err := h.succeededProbeIDs(ctx, rows)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}

	items := make([]effectiveOfferingJSON, 0, len(rows))
	for _, row := range rows {
		items = append(items, toEffectiveOfferingJSON(h.buildProjection(ctx, row, succeeded)))
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
	ModelID             string   `json:"model_id"`
	DisplayName         string   `json:"display_name,omitempty"`
	NativeContextTokens *int     `json:"native_context_tokens"`
	QualityRating       *float64 `json:"quality_rating"`
	// LatestBenchmark is the DATED provenance for QualityRating (04 §3: a
	// rating is "always anchored to a documented source, observed date, and
	// confidence value"). It is a pointer so a model that has never been
	// benchmarked serializes `null` — NEVER a zero date, which would claim a
	// measurement that never happened. Deliberately NOT omitempty: an absent
	// key and an explicit null must not be distinguishable by accident.
	LatestBenchmark *latestBenchmarkJSON    `json:"latest_benchmark"`
	Offerings       []effectiveOfferingJSON `json:"offerings"`
}

// latestBenchmarkJSON is the most recent local benchmark run for one
// canonical model (benchmark_runs, via BenchmarkRunRepo.LatestForModels).
//
// Successes/Requests are carried alongside FinishedAt because the two answer
// different questions, and only both together are honest: FinishedAt says
// WHEN the model was last measured, while Successes < Requests says the
// rating currently on the model did NOT come from that run — the local
// benchmark writes a rating only when every request in its suite succeeds and
// otherwise leaves the previous rating in place (internal/httpapi/benchmark.go).
// Rating itself is deliberately NOT repeated here: models.quality_rating (on
// the group) is the one rating any surface may render, and a second,
// differently-scaled copy of it is exactly the disagreement this branch's
// review had to fix.
type latestBenchmarkJSON struct {
	FinishedAt string `json:"finished_at"`
	Requests   int    `json:"requests"`
	Successes  int    `json:"successes"`
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
		LiveOnly:  true,
		Limit:     page.Limit,
		Cursor:    page.Cursor,
	})
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}

	// ONE batched probe_runs query for this whole page (task-5) — never one
	// per offering-operation.
	succeeded, err := h.succeededProbeIDs(ctx, rows)
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
		g.Offerings = append(g.Offerings, toEffectiveOfferingJSON(h.buildProjection(ctx, row, succeeded)))
	}

	// ONE batched benchmark_runs query for this whole page's canonical models
	// — never one per group.
	latest, err := h.latestBenchmarks(ctx, order)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}

	items := make([]modelGroupJSON, 0, len(order))
	for _, modelID := range order {
		g := groups[modelID]
		if run, ok := latest[modelID]; ok {
			g.LatestBenchmark = &latestBenchmarkJSON{
				FinishedAt: run.FinishedAt.UTC().Format(time.RFC3339),
				Requests:   run.Requests,
				Successes:  run.Successes,
			}
		}
		items = append(items, *g)
	}

	writeDataMeta(w, http.StatusOK, items, paginationMeta(nextCursor))
}

// latestBenchmarks runs the ONE batched benchmark_runs query for a page's
// canonical model ids. h.benchmarkRuns == nil (WithBenchmarkRuns never
// called) and an empty page both short-circuit to nil without touching
// storage — nil reads identically to an empty map at the one lookup below
// (fail closed: no known run means latest_benchmark: null, never an invented
// date).
func (h *ModelsHandler) latestBenchmarks(ctx context.Context, modelIDs []string) (map[string]storage.BenchmarkRun, error) {
	if h.benchmarkRuns == nil || len(modelIDs) == 0 {
		return nil, nil
	}
	return h.benchmarkRuns.LatestForModels(ctx, modelIDs)
}
