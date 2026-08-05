package httpapi

// models_test.go exercises the P3a-CAPI-001 effective-offering read model
// (internal/httpapi/models.go): GET /models and GET /offerings read
// storage.CatalogRepo and render intelligence.Project's shared projection
// verbatim — unknown context/native/transport facts stay unknown (never a
// fabricated 0/false), and the routes are owner-session + CSRF gated
// through the real ControlMux composition.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/intelligence"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

func fixedModelsClock() time.Time {
	return time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
}

func modelsIntPtr(v int) *int             { return &v }
func modelsFloat64Ptr(v float64) *float64 { return &v }
func modelsStrPtr(v string) *string       { return &v }
func modelsBoolPtr(v bool) *bool          { return &v }

// newTestModelsHandler builds a ModelsHandler over a fresh migrated DB with
// an injectable clock.
func newTestModelsHandler(t *testing.T, clock func() time.Time) (*ModelsHandler, *storage.DB) {
	t.Helper()
	db := testControlDB(t)
	return NewModelsHandler(storage.NewCatalogRepo(db), clock), db
}

// offeringOpSeed is one offering_operations + certifications row to seed.
type offeringOpSeed struct {
	Operation string
	Status    string // "" defaults to "discovered"
	Truth     string // "" defaults to "unknown"
}

// offeringSeed is everything modelsSeedOffering needs to seed one full
// offering row (provider + account + model + offering + its operations)
// directly against the M4 tables — this handler's CatalogRepo is read-only,
// so tests seed the underlying schema directly rather than through
// DiscoveryRepo.
type offeringSeed struct {
	AccountID, ProviderID, ProviderModelID, ModelID string
	ModelDisplayName                                string
	NativeContextTokens                             *int
	NativeModalitiesJSON                            *string
	QualityRating                                   *float64
	ContextLength, MaxInputTokens, MaxOutputTokens  *int
	CapabilitiesJSON                                *string
	PricingJSON                                     *string
	Operations                                      []offeringOpSeed
}

func modelsSeedOffering(t *testing.T, db *storage.DB, s offeringSeed) {
	t.Helper()

	if _, err := db.Conn().Exec(
		`INSERT OR IGNORE INTO providers (id, display_name, auth_mode, funding_mode, created_at, updated_at) VALUES (?, ?, 'api_key', 'fixed', 0, 0)`,
		s.ProviderID, s.ProviderID,
	); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if _, err := db.Conn().Exec(
		`INSERT OR IGNORE INTO accounts (id, provider_id, external_id, auth_type, connection_state, health_state, created_at, updated_at)
		 VALUES (?, ?, ?, 'api_key', 'connected', 'healthy', 0, 0)`,
		s.AccountID, s.ProviderID, s.AccountID,
	); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := db.Conn().Exec(
		`INSERT OR IGNORE INTO models (id, canonical_key_sha256, display_name, native_context_tokens, native_modalities_json, quality_rating, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, 0, 0)`,
		s.ModelID, s.ModelID+"-key", s.ModelDisplayName, s.NativeContextTokens, s.NativeModalitiesJSON, s.QualityRating,
	); err != nil {
		t.Fatalf("seed model: %v", err)
	}
	if _, err := db.Conn().Exec(
		`INSERT INTO account_model_offerings
		    (account_id, provider_id, provider_model_id, model_id, availability, context_length, max_input_tokens, max_output_tokens, capabilities_json, pricing_json, first_seen_at, last_seen_at)
		 VALUES (?, ?, ?, ?, 'available', ?, ?, ?, ?, ?, 0, 0)`,
		s.AccountID, s.ProviderID, s.ProviderModelID, s.ModelID,
		s.ContextLength, s.MaxInputTokens, s.MaxOutputTokens, s.CapabilitiesJSON, s.PricingJSON,
	); err != nil {
		t.Fatalf("seed offering: %v", err)
	}
	for _, op := range s.Operations {
		status := op.Status
		if status == "" {
			status = "discovered"
		}
		truth := op.Truth
		if truth == "" {
			truth = "unknown"
		}
		opID := s.ProviderModelID + "-op-" + op.Operation
		if _, err := db.Conn().Exec(
			`INSERT INTO offering_operations (id, account_id, provider_id, provider_model_id, operation, created_at, updated_at) VALUES (?, ?, ?, ?, ?, 0, 0)`,
			opID, s.AccountID, s.ProviderID, s.ProviderModelID, op.Operation,
		); err != nil {
			t.Fatalf("seed offering_operation %s: %v", op.Operation, err)
		}
		if _, err := db.Conn().Exec(
			`INSERT INTO certifications (offering_operation_id, status, capability_truth, version, created_at, updated_at) VALUES (?, ?, ?, 1, 0, 0)`,
			opID, status, truth,
		); err != nil {
			t.Fatalf("seed certification for %s: %v", op.Operation, err)
		}
	}
}

func modelsRequest(method, path string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Host = testAllowedHost
	return req
}

// TestModelsHandler_ServesOnlyHealthyConnectedOfferings catches the public
// read-model exposing catalog rows whose accounts cannot currently serve a
// request. Storage may still contain those rows until the maintenance purge;
// the Live Models API must fail closed immediately.
func TestModelsHandler_ServesOnlyHealthyConnectedOfferings(t *testing.T) {
	h, db := newTestModelsHandler(t, fixedModelsClock)
	modelsSeedOffering(t, db, offeringSeed{
		AccountID: "acct-model-live", ProviderID: "prov-model-live",
		ProviderModelID: "pm-live", ModelID: "canonical-live", ModelDisplayName: "Live Model",
		// The honest gate (universal-probes-and-honest-gate Task 9) requires a
		// certified+supported chat offering-operation before LiveOnly admits a
		// row — without this, "canonical-live" would now be excluded for the
		// same reason "canonical-dead" is, defeating this test's intent of
		// isolating account health as the discriminator.
		Operations: []offeringOpSeed{{Operation: "chat", Status: "certified", Truth: "supported"}},
	})
	modelsSeedOffering(t, db, offeringSeed{
		AccountID: "acct-model-expired", ProviderID: "prov-model-dead",
		ProviderModelID: "pm-dead", ModelID: "canonical-dead", ModelDisplayName: "Dead Model",
		// Same certified+supported chat op as the live seed above: account
		// health must be the ONLY discriminator this test isolates. Without
		// this, canonical-dead would be excluded for two independent reasons
		// (unhealthy account AND uncertified chat), and the health-clause
		// assertion below would no longer be pinned to the health clause.
		Operations: []offeringOpSeed{{Operation: "chat", Status: "certified", Truth: "supported"}},
	})
	if _, err := db.Conn().Exec(`UPDATE accounts SET connection_state = 'connected', health_state = 'healthy' WHERE id = 'acct-model-live'`); err != nil {
		t.Fatalf("mark live account: %v", err)
	}
	if _, err := db.Conn().Exec(`UPDATE accounts SET connection_state = 'connected', health_state = 'expired' WHERE id = 'acct-model-expired'`); err != nil {
		t.Fatalf("mark expired account: %v", err)
	}

	rec := httptest.NewRecorder()
	h.ServeModels(rec, modelsRequest(http.MethodGet, "/api/control/v1/models"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data []struct {
			ModelID string `json:"model_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(env.Data) != 1 || env.Data[0].ModelID != "canonical-live" {
		t.Fatalf("models = %+v, want only canonical-live", env.Data)
	}
}

// --- TestModels_UnknownContextSerializesNull ---

// TestModels_UnknownContextSerializesNull proves an offering with no native
// and no provider context renders "effective_context_tokens": null, and
// the raw body never contains "effective_context_tokens":0.
func TestModels_UnknownContextSerializesNull(t *testing.T) {
	clock := fixedModelsClock()
	h, db := newTestModelsHandler(t, func() time.Time { return clock })

	modelsSeedOffering(t, db, offeringSeed{
		AccountID: "acct-ctx", ProviderID: "prov-ctx", ProviderModelID: "model-ctx", ModelID: "cm-ctx",
	})

	rec := httptest.NewRecorder()
	h.ServeOfferings(rec, modelsRequest(http.MethodGet, "/api/control/v1/offerings?account_id=acct-ctx"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %q", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	if strings.Contains(body, `"effective_context_tokens":0`) {
		t.Fatalf("body contains a fabricated 0 for unknown context: %s", body)
	}
	if !strings.Contains(body, `"effective_context_tokens":null`) {
		t.Fatalf("body missing explicit null for unknown context: %s", body)
	}
}

// TestModels_RenderedTiersComeFromProject closes the gap that
// TestModels_MatchesProjectExactly leaves open: that test compares the
// in-memory EffectiveOffering, so a re-derivation introduced in the JSON
// RENDERER — the only thing a consumer actually reads — would slip past it.
// The seeded offering is deliberately free-cost (so 04 §2b's cost table
// alone would make every tier eligible) but has UNKNOWN context, which
// 04 §3's gate turns into ineligible-for-all-tiers. Any renderer that
// recomputes eligibility from the cost fact instead of copying Project's
// Tiers therefore renders lite as eligible, and this test goes RED.
func TestModels_RenderedTiersComeFromProject(t *testing.T) {
	clock := fixedModelsClock()
	h, db := newTestModelsHandler(t, func() time.Time { return clock })

	modelsSeedOffering(t, db, offeringSeed{
		AccountID: "acct-rt", ProviderID: "prov-rt", ProviderModelID: "model-rt", ModelID: "cm-rt",
		CapabilitiesJSON: modelsStrPtr(`["chat"]`),
		PricingJSON:      modelsStrPtr(`{"cost":{"input":0,"output":0}}`),
		Operations:       []offeringOpSeed{{Operation: "chat", Status: "certified", Truth: "supported"}},
	})

	ctx := context.Background()
	rows, _, err := storage.NewCatalogRepo(db).ListOfferings(ctx, storage.CatalogListParams{AccountID: "acct-rt", Limit: 10})
	if err != nil {
		t.Fatalf("ListOfferings: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(rows))
	}
	projected := h.buildProjection(ctx, rows[0])

	// Precondition: the cost fact really is verified-free, so cost alone
	// would admit every tier and only the context gate can exclude them.
	if projected.Cost.IsFree == nil || !*projected.Cost.IsFree {
		t.Fatalf("seeded cost fact = %+v, want verified free (the test is vacuous otherwise)", projected.Cost)
	}
	if projected.EffectiveContextTokens != nil {
		t.Fatalf("seeded effective context = %v, want unknown (the test is vacuous otherwise)", *projected.EffectiveContextTokens)
	}

	rec := httptest.NewRecorder()
	h.ServeOfferings(rec, modelsRequest(http.MethodGet, "/api/control/v1/offerings?account_id=acct-rt"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %q", rec.Code, rec.Body.String())
	}
	var env struct {
		Data []effectiveOfferingJSON `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v; body = %q", err, rec.Body.String())
	}
	if len(env.Data) != 1 {
		t.Fatalf("len(data) = %d, want 1", len(env.Data))
	}

	for _, tier := range []intelligence.Tier{intelligence.TierLite, intelligence.TierPro, intelligence.TierMax} {
		want := projected.Tiers[tier]
		got, ok := env.Data[0].Tiers[string(tier)]
		if !ok {
			t.Fatalf("rendered tiers missing %q: %+v", tier, env.Data[0].Tiers)
		}
		if got.Eligible != want.Eligible || got.Stale != want.Stale || got.Penalty != want.Penalty {
			t.Fatalf("rendered tier %q = %+v, want Project's %+v (the renderer must copy, never re-derive)", tier, got, want)
		}
		if !reflect.DeepEqual(got.Reasons, want.Reasons) {
			t.Fatalf("rendered tier %q reasons = %v, want Project's %v", tier, got.Reasons, want.Reasons)
		}
		if got.Eligible {
			t.Fatalf("rendered tier %q is eligible despite unknown context (04 §3 fails closed)", tier)
		}
		if !containsString(got.Reasons, intelligence.ReasonContextUnknown) {
			t.Fatalf("rendered tier %q reasons = %v, want to include %s", tier, got.Reasons, intelligence.ReasonContextUnknown)
		}
	}
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// --- TestModels_MatchesProjectExactly ---

// TestModels_MatchesProjectExactly proves buildProjection's assembled
// EffectiveOffering is byte-for-byte what intelligence.Project produces for
// the same hand-built ProjectionInput — the handler must read the shared
// projection, never re-derive eligibility, capability, or context itself.
func TestModels_MatchesProjectExactly(t *testing.T) {
	clock := fixedModelsClock()
	h, db := newTestModelsHandler(t, func() time.Time { return clock })

	modelsSeedOffering(t, db, offeringSeed{
		AccountID: "acct-m", ProviderID: "prov-m", ProviderModelID: "model-m", ModelID: "cm-m",
		ModelDisplayName:     "Model M",
		NativeContextTokens:  modelsIntPtr(8000),
		NativeModalitiesJSON: modelsStrPtr(`["text"]`),
		QualityRating:        modelsFloat64Ptr(72),
		ContextLength:        modelsIntPtr(4000),
		CapabilitiesJSON:     modelsStrPtr(`["chat","vision"]`),
		PricingJSON:          modelsStrPtr(`{"cost":{"input":0,"output":0}}`),
		Operations: []offeringOpSeed{
			{Operation: "chat", Status: "certified", Truth: "supported"},
			{Operation: "vision", Status: "discovered", Truth: "unknown"},
		},
	})

	ctx := context.Background()
	rows, _, err := storage.NewCatalogRepo(db).ListOfferings(ctx, storage.CatalogListParams{AccountID: "acct-m", Limit: 10})
	if err != nil {
		t.Fatalf("ListOfferings: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(rows))
	}
	row := rows[0]

	got := h.buildProjection(ctx, row)

	certs := make(map[models.Operation]models.Certification, len(row.Operations))
	for _, opRow := range row.Operations {
		op, err := models.ParseOperation(opRow.Operation)
		if err != nil {
			t.Fatalf("unexpected unparseable seeded operation %q", opRow.Operation)
		}
		state, err := models.ParseCertificationState(opRow.CertificationStatus)
		if err != nil {
			t.Fatalf("unexpected unparseable seeded state %q", opRow.CertificationStatus)
		}
		truth, err := models.ParseCapabilityTruth(opRow.CapabilityTruth)
		if err != nil {
			t.Fatalf("unexpected unparseable seeded truth %q", opRow.CapabilityTruth)
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

	wantCanonical := models.CanonicalModel{
		DisplayName:         "Model M",
		NativeContextTokens: modelsIntPtr(8000),
		NativeModalities:    []string{"text"},
		QualityRating:       modelsFloat64Ptr(72),
	}
	wantOffering := models.Offering{
		Identity:      models.OfferingIdentity{AccountID: "acct-m", ProviderModelID: "model-m"},
		Availability:  models.AvailabilityAvailable,
		ContextLength: modelsIntPtr(4000),
		Capabilities:  []models.Operation{models.OperationChat, models.OperationVision},
	}
	wantCost := intelligence.ResolvedCostFact{
		IsFree:             modelsBoolPtr(true),
		Source:             intelligence.CostSourceProviderPrice,
		ExactIdentityMatch: true,
		ObservedAt:         clock,
		Confidence:         1,
	}
	wantClassification := intelligence.Classify([]models.Operation{models.OperationChat, models.OperationVision}, []string{"text"}).Classification

	want := intelligence.Project(intelligence.ProjectionInput{
		ProviderID:          "prov-m",
		Canonical:           wantCanonical,
		NativeCapabilities:  nil,
		Offering:            wantOffering,
		TransportOperations: nil,
		Certifications:      certs,
		Cost:                wantCost,
		Classification:      wantClassification,
	})

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildProjection() =\n%+v\nwant (intelligence.Project()) =\n%+v", got, want)
	}
}

// --- TestModels_NativeAndTransportStayUnknown ---

// TestModels_NativeAndTransportStayUnknown proves every rendered capability
// has effective:false and routable:false even when the offering exposes
// chat and a certification says certified+supported, because native
// capability and transport support are UNKNOWN this phase (04 §2/§3: fail
// closed, never fabricated from provider exposure alone).
func TestModels_NativeAndTransportStayUnknown(t *testing.T) {
	h, db := newTestModelsHandler(t, nil)

	modelsSeedOffering(t, db, offeringSeed{
		AccountID: "acct-nt", ProviderID: "prov-nt", ProviderModelID: "model-nt", ModelID: "cm-nt",
		CapabilitiesJSON: modelsStrPtr(`["chat"]`),
		Operations: []offeringOpSeed{
			{Operation: "chat", Status: "certified", Truth: "supported"},
		},
	})

	// Assert directly on the assembled ProjectionInput first: this is what
	// independently catches EITHER seam being wrongly populated on its
	// own, since Project's "effective" is the AND of both — a check on the
	// end-to-end HTTP body alone cannot distinguish "only native populated"
	// from "neither populated" (both yield effective=false).
	rows, _, err := storage.NewCatalogRepo(db).ListOfferings(context.Background(), storage.CatalogListParams{AccountID: "acct-nt", Limit: 10})
	if err != nil {
		t.Fatalf("ListOfferings: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(rows))
	}
	in := h.buildProjectionInput(context.Background(), rows[0])
	if in.NativeCapabilities != nil {
		t.Fatalf("ProjectionInput.NativeCapabilities = %v, want nil (unknown this phase)", in.NativeCapabilities)
	}
	if in.TransportOperations != nil {
		t.Fatalf("ProjectionInput.TransportOperations = %v, want nil (unknown this phase)", in.TransportOperations)
	}

	rec := httptest.NewRecorder()
	h.ServeOfferings(rec, modelsRequest(http.MethodGet, "/api/control/v1/offerings?account_id=acct-nt"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %q", rec.Code, rec.Body.String())
	}

	var env struct {
		Data []struct {
			Capabilities []struct {
				Operation string `json:"operation"`
				Effective bool   `json:"effective"`
				Routable  bool   `json:"routable"`
			} `json:"capabilities"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v; body = %q", err, rec.Body.String())
	}
	if len(env.Data) != 1 {
		t.Fatalf("len(data) = %d, want 1", len(env.Data))
	}
	if len(env.Data[0].Capabilities) == 0 {
		t.Fatalf("capabilities = [], want at least one (chat)")
	}
	for _, c := range env.Data[0].Capabilities {
		if c.Effective {
			t.Fatalf("capability %q effective = true, want false (native/transport unknown)", c.Operation)
		}
		if c.Routable {
			t.Fatalf("capability %q routable = true, want false (native/transport unknown)", c.Operation)
		}
	}
}

// --- TestModels_CostFromPersistedPricingOnly ---

// TestModels_CostFromPersistedPricingOnly proves cost resolution reads
// ONLY the account's persisted provider pricing_json this phase: a
// zero-cost value resolves is_free=true/source=provider_price, and an
// absent/malformed value resolves is_free=null (unknown) — the resolver's
// dataset seam is nil, so no network call is even possible.
func TestModels_CostFromPersistedPricingOnly(t *testing.T) {
	h, db := newTestModelsHandler(t, nil)

	modelsSeedOffering(t, db, offeringSeed{
		AccountID: "acct-free", ProviderID: "prov-cost", ProviderModelID: "model-free", ModelID: "cm-free",
		PricingJSON: modelsStrPtr(`{"cost":{"input":0,"output":0}}`),
	})
	modelsSeedOffering(t, db, offeringSeed{
		AccountID: "acct-unknown", ProviderID: "prov-cost", ProviderModelID: "model-unknown", ModelID: "cm-unknown",
	})

	type costOut struct {
		AccountID string `json:"account_id"`
		Cost      struct {
			IsFree *bool  `json:"is_free"`
			Source string `json:"source,omitempty"`
		} `json:"cost"`
	}

	for _, tc := range []struct {
		accountID  string
		wantIsFree *bool
		wantSource string
	}{
		{"acct-free", modelsBoolPtr(true), "provider_price"},
		{"acct-unknown", nil, ""},
	} {
		rec := httptest.NewRecorder()
		h.ServeOfferings(rec, modelsRequest(http.MethodGet, "/api/control/v1/offerings?account_id="+tc.accountID))
		if rec.Code != http.StatusOK {
			t.Fatalf("[%s] status = %d, want 200; body = %q", tc.accountID, rec.Code, rec.Body.String())
		}
		var env struct {
			Data []costOut `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatalf("[%s] decode: %v", tc.accountID, err)
		}
		if len(env.Data) != 1 {
			t.Fatalf("[%s] len(data) = %d, want 1", tc.accountID, len(env.Data))
		}
		got := env.Data[0].Cost
		if (got.IsFree == nil) != (tc.wantIsFree == nil) {
			t.Fatalf("[%s] is_free = %v, want %v", tc.accountID, got.IsFree, tc.wantIsFree)
		}
		if got.IsFree != nil && tc.wantIsFree != nil && *got.IsFree != *tc.wantIsFree {
			t.Fatalf("[%s] is_free = %v, want %v", tc.accountID, *got.IsFree, *tc.wantIsFree)
		}
		if got.Source != tc.wantSource {
			t.Fatalf("[%s] source = %q, want %q", tc.accountID, got.Source, tc.wantSource)
		}
	}
}

// --- Gating (ControlMux composition) ---

// TestControlMux_Models_And_Offerings_AreOwnerGated proves both routes are
// registered through `gated` (owner session + CSRF middleware): no session
// -> 401 for both; a valid session -> 200 for both, THROUGH THE MUX.
// --- offering_operation_id (P6-CAPI-EXTRA-2) ---

// modelsOfferingsCapabilities fetches GET /offerings and returns the first
// offering's `capabilities` array as decoded objects.
func modelsOfferingsCapabilities(t *testing.T, h *ModelsHandler) []map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeOfferings(rec, modelsRequest(http.MethodGet, "/api/control/v1/offerings"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var env struct {
		Data []struct {
			Capabilities []map[string]any `json:"capabilities"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v (body %s)", err, rec.Body.String())
	}
	if len(env.Data) != 1 {
		t.Fatalf("offerings = %d, want exactly 1", len(env.Data))
	}
	return env.Data[0].Capabilities
}

// TestModels_CapabilityCarriesItsOwnOfferingOperationID is the fix for a REAL
// shipped gap: GET /models named each operation but never identified its row, so
// the Models surface's probe control had to ship DISABLED — POST
// /offerings/{id}/probe is keyed by exactly the id this projection dropped.
//
// Two operations are seeded, each with its own offering_operations row, and each
// capability must report ITS OWN id. Returning the first row's id for every
// capability — the easy wrong implementation — would point a probe at the wrong
// operation, so it is caught here by construction.
func TestModels_CapabilityCarriesItsOwnOfferingOperationID(t *testing.T) {
	h, db := newTestModelsHandler(t, fixedModelsClock)
	modelsSeedOffering(t, db, offeringSeed{
		AccountID: "acct-opid", ProviderID: "prov-opid",
		ProviderModelID: "pm-opid", ModelID: "model-opid",
		ContextLength:    modelsIntPtr(100000),
		CapabilitiesJSON: modelsStrPtr(`["chat","tools"]`),
		Operations: []offeringOpSeed{
			{Operation: "chat", Status: "certified", Truth: "supported"},
			{Operation: "tools", Status: "observed", Truth: "unknown"},
		},
	})

	caps := modelsOfferingsCapabilities(t, h)
	byOp := map[string]map[string]any{}
	for _, c := range caps {
		byOp[c["operation"].(string)] = c
	}

	// The seed's own id convention — the expectation is derived from the SEED,
	// so it cannot drift from what was actually written.
	want := map[string]string{
		"chat":  "pm-opid-op-chat",
		"tools": "pm-opid-op-tools",
	}
	for op, wantID := range want {
		c, ok := byOp[op]
		if !ok {
			t.Fatalf("capability %q missing from the payload: %#v", op, byOp)
		}
		got, present := c["offering_operation_id"]
		if !present {
			t.Fatalf("capability %q has no offering_operation_id — the probe control cannot be enabled without it", op)
		}
		if got != wantID {
			t.Errorf("capability %q offering_operation_id = %#v, want %q (each capability must carry ITS OWN row's id)", op, got, wantID)
		}
	}

	// Cross-check against the projection itself, so the wire value cannot drift
	// from intelligence.Project's answer.
	rows, _, err := storage.NewCatalogRepo(db).ListOfferings(context.Background(), storage.CatalogListParams{Limit: 10})
	if err != nil {
		t.Fatalf("ListOfferings: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	for _, c := range h.buildProjection(context.Background(), rows[0]).Capabilities {
		if wantID, tracked := want[string(c.Operation)]; tracked && c.OfferingOperationID != wantID {
			t.Errorf("projection %q OfferingOperationID = %q, want %q", c.Operation, c.OfferingOperationID, wantID)
		}
	}
}

// TestModels_CapabilityWithoutOperationRowOmitsTheID proves the field is OMITTED
// — not empty-string, not synthesized — for a capability with no
// offering_operations row.
//
// An operation the provider exposes but that was never turned into an
// offering_operations row has nothing to probe. `offering_operation_id: ""` would
// be a present-but-meaningless identifier that a client could pass to
// POST /offerings/{id}/probe; omitting the key says "not probeable" unambiguously.
func TestModels_CapabilityWithoutOperationRowOmitsTheID(t *testing.T) {
	h, db := newTestModelsHandler(t, fixedModelsClock)
	modelsSeedOffering(t, db, offeringSeed{
		AccountID: "acct-noop", ProviderID: "prov-noop",
		ProviderModelID: "pm-noop", ModelID: "model-noop",
		ContextLength: modelsIntPtr(100000),
		// The provider exposes chat, but NO offering_operations row is seeded.
		CapabilitiesJSON: modelsStrPtr(`["chat"]`),
		Operations:       nil,
	})

	caps := modelsOfferingsCapabilities(t, h)
	if len(caps) == 0 {
		t.Fatalf("no capabilities projected — this test would be vacuous")
	}
	for _, c := range caps {
		if _, present := c["offering_operation_id"]; present {
			t.Errorf("capability %q carries offering_operation_id = %#v, want the key OMITTED (no row means nothing to probe)",
				c["operation"], c["offering_operation_id"])
		}
	}

	// And the raw body carries no empty-string form of the field either.
	rec := httptest.NewRecorder()
	h.ServeOfferings(rec, modelsRequest(http.MethodGet, "/api/control/v1/offerings"))
	if strings.Contains(rec.Body.String(), `"offering_operation_id":""`) {
		t.Fatalf("body contains an empty-string offering_operation_id: %s", rec.Body.String())
	}
}

func TestControlMux_Models_And_Offerings_AreOwnerGated(t *testing.T) {
	mux := ControlMux(testAllowedHost, fakeSPA(), testControlDB(t), testKeyring(t))

	for _, path := range []string{"/api/control/v1/models", "/api/control/v1/offerings"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, newAuthRequest(t, http.MethodGet, path, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("GET %s unauthenticated status = %d, want 401", path, rec.Code)
		}
	}

	cookie, _ := setupOwnerWithCSRF(t, mux)
	for _, path := range []string{"/api/control/v1/models", "/api/control/v1/offerings"} {
		req := newAuthRequest(t, http.MethodGet, path, nil)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s with session status = %d, want 200; body = %q", path, rec.Code, rec.Body.String())
		}
	}
}
