package httpapi

// discovery_acceptance_test.go is P3a-TEST-001: the P3a phase gate,
// mechanized. Every unit it certifies (P3a-DB-001, DISC-001..006,
// CERT-001/002, CAPI-001/002/003, JOBS-001) already has its own unit
// tests; this suite does NOT re-test any of their internals. It proves
// the four gate criteria (docs/11 P3a "Acceptance gate", docs/06 "Phase
// 3a — Gate") end-to-end, through the REAL composed surface, so that a
// regression in how the pieces are WIRED TOGETHER — not just in one
// piece's own logic — is caught here even if every narrower test stays
// green.
//
// Two-layer design, and why:
//
//   - Layer A — through the REAL composed ControlMux (zero network): every
//     HTTP-surface criterion (read models, certification read, owner
//     gating, the enrichment toggle), over a catalog seeded directly into
//     the frozen M4 tables via the same seeders discovery_test.go and
//     models_test.go already use.
//   - Layer B — through DiscoveryHandler / intelligence.DiscoveryService
//     with a fake ModelDiscoveryAdapter: every discovery-RUN criterion.
//     Layer B exists because ControlMux deliberately builds its own
//     provider registry over REAL HTTP seams (opencode-zen) and exposes no
//     injection point — adding one purely for tests would be a production
//     change for test convenience, which this unit is expressly forbidden
//     to make. Consequently POST /accounts/{id}/discover is exercised
//     through the real ControlMux ONLY on its rejection paths (see
//     TestP3aGate_ControlSurfaceIsOwnerGated) — never a successful 202,
//     whose background goroutine would call opencode-zen for real.
//
// Every test that triggers a 202 discover through DiscoveryHandler waits
// for the job's terminal state (waitForJobTerminal, discovery_test.go)
// before returning — the background run outlives the handler call and
// would otherwise race t.TempDir() teardown.

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/application"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/intelligence"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// --- shared helpers (this file only) ---

// p3aOwnerMux builds a real ControlMux over db, completes first-run setup,
// and returns the mux plus the session cookie and CSRF token a caller adds
// to any subsequent request — the Layer A entry point every test in this
// file that needs the real composed surface uses.
func p3aOwnerMux(t *testing.T, db *storage.DB) (http.Handler, *http.Cookie, string) {
	t.Helper()
	mux := ControlMux(testAllowedHost, fakeSPA(), db, testKeyring(t))
	cookie, csrfToken := setupOwnerWithCSRF(t, mux)
	return mux, cookie, csrfToken
}

func p3aGet(t *testing.T, mux http.Handler, cookie *http.Cookie, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := newAuthRequest(t, http.MethodGet, path, nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func p3aMutate(t *testing.T, mux http.Handler, cookie *http.Cookie, csrfToken, method, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := newAuthRequest(t, method, path, bytesBuffer(body))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrfToken)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// p3aSeedAccount seeds a bare provider + connected account (no model, no
// offering) — the minimal fixture Layer B's DiscoveryService-driven tests
// need before a run applies anything.
func p3aSeedAccount(t *testing.T, db *storage.DB, accountID, providerID string) {
	t.Helper()
	if _, err := db.Conn().Exec(
		`INSERT INTO providers (id, display_name, auth_mode, funding_mode, funding_locked, created_at, updated_at) VALUES (?, ?, 'api_key', 'owner_policy', 0, 0, 0)`,
		providerID, providerID,
	); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if _, err := db.Conn().Exec(
		`INSERT INTO accounts (id, provider_id, external_id, auth_type, connection_state, health_state, created_at, updated_at)
		 VALUES (?, ?, ?, 'api_key', 'connected', 'healthy', 0, 0)`,
		accountID, providerID, accountID,
	); err != nil {
		t.Fatalf("seed account: %v", err)
	}
}

// p3aTrivialLeaser is a minimal intelligence.CredentialLeaser for Layer B
// tests that drive DiscoveryService directly — it never touches real
// credential storage, it just hands a placeholder byte slice to fn.
type p3aTrivialLeaser struct{}

func (p3aTrivialLeaser) Use(_ context.Context, _ string, fn func([]byte) error) error {
	return fn([]byte("not-a-real-credential"))
}

// p3aTrapAPIKeyAdapter fails the test the instant ConnectAPIKey is
// invoked. A provider's authentic-key-validation probe IS a chat
// completion call to the provider — the only inference-shaped surface
// that exists this phase — so registering this in place of a real/fake
// APIKeyAdapter and never seeing it fire is exactly criterion 4 ("no
// inference probes run yet"), proven at the adapter boundary rather than
// merely inferred from certification state.
type p3aTrapAPIKeyAdapter struct{ t *testing.T }

func (a p3aTrapAPIKeyAdapter) ConnectAPIKey(_ context.Context, _ string) (providers.IdentityResult, providers.StoredCredentials, error) {
	a.t.Fatalf("ConnectAPIKey (an authentic-validation chat probe) was invoked during discovery — no inference probe may run in P3a")
	return providers.IdentityResult{}, providers.StoredCredentials{}, nil
}

// p3aOfferingsSnapshot serializes every offering ListOfferings currently
// reports for accountID — used to prove a failed/malformed run leaves the
// catalog byte-for-byte untouched (04 §1: "keep the previous snapshot
// intact"), comparing the full read model rather than one hand-picked
// column.
func p3aOfferingsSnapshot(t *testing.T, catalog *storage.CatalogRepo, accountID string) string {
	t.Helper()
	rows, _, err := catalog.ListOfferings(context.Background(), storage.CatalogListParams{AccountID: accountID, Limit: 1000})
	if err != nil {
		t.Fatalf("ListOfferings: %v", err)
	}
	b, err := json.Marshal(rows)
	if err != nil {
		t.Fatalf("marshal offerings snapshot: %v", err)
	}
	return string(b)
}

// p3aDiscoveryRunRow reads back one discovery_runs row's status and
// generation directly — DiscoveryRepo exposes no Get for this table, and
// this is a legitimate observable-database-state check, not a
// re-derivation of DiscoveryRepo's own decision.
func p3aDiscoveryRunStatus(t *testing.T, db *storage.DB, runID string) (status string, generation int64) {
	t.Helper()
	if err := db.Conn().QueryRow(`SELECT status, generation FROM discovery_runs WHERE id = ?`, runID).Scan(&status, &generation); err != nil {
		t.Fatalf("query discovery_runs %q: %v", runID, err)
	}
	return status, generation
}

// p3aAssertNoCanaryInCatalog scans every TEXT column of the M4 catalog
// tables plus jobs for canary, failing the test if it appears anywhere —
// the broad defense-in-depth net behind the specific evidence-column
// check TestP3aGate_DiscoveryRunStoresProvenance also performs.
func p3aAssertNoCanaryInCatalog(t *testing.T, db *storage.DB, canary string) {
	t.Helper()
	checks := []struct{ table, column string }{
		{"models", "display_name"},
		{"models", "native_modalities_json"},
		{"account_model_offerings", "capabilities_json"},
		{"account_model_offerings", "pricing_json"},
		{"account_model_offerings", "lifecycle_json"},
		{"offering_operations", "operation"},
		{"certifications", "evidence_ref"},
		{"discovery_runs", "reason_code"},
		{"jobs", "result_ref"},
		{"jobs", "error"},
	}
	for _, c := range checks {
		rows, err := db.Conn().Query(`SELECT ` + c.column + ` FROM ` + c.table) //nolint:gosec // fixed internal identifiers, not user input
		if err != nil {
			t.Fatalf("scan %s.%s: %v", c.table, c.column, err)
		}
		for rows.Next() {
			var v sql.NullString
			if err := rows.Scan(&v); err != nil {
				_ = rows.Close()
				t.Fatalf("scan %s.%s row: %v", c.table, c.column, err)
			}
			if v.Valid && strings.Contains(v.String, canary) {
				_ = rows.Close()
				t.Fatalf("%s.%s contains the canary secret %q: %s", c.table, c.column, canary, v.String)
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			t.Fatalf("scan %s.%s: %v", c.table, c.column, err)
		}
		_ = rows.Close()
	}
}

func p3aFindOffering(t *testing.T, offerings []effectiveOfferingJSON, providerModelID string) effectiveOfferingJSON {
	t.Helper()
	for _, o := range offerings {
		if o.ProviderModelID == providerModelID {
			return o
		}
	}
	t.Fatalf("no offering with provider_model_id %q in %d offerings", providerModelID, len(offerings))
	return effectiveOfferingJSON{}
}

func p3aDecodeOfferings(t *testing.T, body []byte) []effectiveOfferingJSON {
	t.Helper()
	var env struct {
		Data []effectiveOfferingJSON `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode offerings: %v; body = %q", err, body)
	}
	return env.Data
}

// =====================================================================
// (1) TestP3aGate_DiscoveryRunStoresProvenance
// =====================================================================

func TestP3aGate_DiscoveryRunStoresProvenance(t *testing.T) {
	clock := fixedDiscoveryClock()
	f := newDiscoveryFixture(t, func() time.Time { return clock }, discoveryFixtureOpts{WithDiscovery: true, WithCredential: true})

	const canary = "sk-live-CANARY-should-never-appear"
	f.discAdapter.models = []providers.DiscoveredModel{
		{
			ProviderModelID: "model-free", DisplayName: "Free Model",
			Capabilities: []string{"chat", "unrecognized-cap-xyz"},
			Pricing:      map[string]any{"cost": map[string]any{"input": 0, "output": 0}},
			// api_key is a secret-shaped MAP KEY: sanitizeMap redacts its
			// entire value regardless of content (internal/sanitize.IsSecretKey).
			// This is the ONE evidence field the discovery pipeline actually
			// promises to strip — a bare canary string with no secret-shaped
			// key would never be redacted by design, so it would be a
			// meaningless (vacuously-passing) canary.
			Evidence: map[string]any{"api_key": canary, "note": "seen at discovery time"},
		},
		{
			ProviderModelID: "model-paid", DisplayName: "Paid Model",
			Capabilities: []string{"chat"},
			Pricing:      map[string]any{"cost": map[string]any{"input": 5, "output": 10}},
		},
	}

	mux := newTestDiscoveryMux(f.handler)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, discoveryRequest(http.MethodPost, "/api/control/v1/accounts/"+f.accountID+"/discover"))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body = %q", rec.Code, rec.Body.String())
	}
	data := decodeDiscoverResponse(t, rec.Body.Bytes())

	row := waitForJobTerminal(t, f.jobs, data.JobID, 2*time.Second)
	if row.Status != storage.JobCompleted {
		t.Fatalf("job Status = %q, want completed (error = %+v)", row.Status, row.Error)
	}
	if row.Kind != "discovery" {
		t.Fatalf("job Kind = %q, want discovery", row.Kind)
	}
	wantRef := "/api/control/v1/models?account_id=" + f.accountID
	if row.ResultRef != wantRef {
		t.Fatalf("ResultRef = %q, want %q", row.ResultRef, wantRef)
	}

	offerings, _, err := f.catalog.ListOfferings(context.Background(), storage.CatalogListParams{AccountID: f.accountID, Limit: 10})
	if err != nil {
		t.Fatalf("ListOfferings: %v", err)
	}
	assertP3aProvenanceOfferings(t, offerings)
	assertP3aProvenanceRunRow(t, f.db, f.accountID)

	p3aAssertNoCanaryInCatalog(t, f.db, canary)
	if strings.Contains(rec.Body.String(), canary) {
		t.Fatalf("202 response leaked the canary secret: %s", rec.Body.String())
	}

	// Layer A: through the REAL ControlMux, the provenance tuple 04 §2b
	// requires is visible on /offerings.
	mux2, cookie, _ := p3aOwnerMux(t, f.db)
	rec2 := p3aGet(t, mux2, cookie, "/api/control/v1/offerings?account_id="+f.accountID)
	if rec2.Code != http.StatusOK {
		t.Fatalf("GET /offerings status = %d, want 200; body = %q", rec2.Code, rec2.Body.String())
	}
	list := p3aDecodeOfferings(t, rec2.Body.Bytes())
	assertP3aProvenanceCostFacts(t, list)
	if strings.Contains(rec2.Body.String(), canary) {
		t.Fatalf("GET /offerings leaked the canary secret: %s", rec2.Body.String())
	}
}

// assertP3aProvenanceOfferings checks the persisted offering/operation
// rows for TestP3aGate_DiscoveryRunStoresProvenance, split out to keep
// that test's cyclomatic complexity down.
func assertP3aProvenanceOfferings(t *testing.T, offerings []storage.CatalogOfferingRow) (free, paid *storage.CatalogOfferingRow) {
	t.Helper()
	if len(offerings) != 2 {
		t.Fatalf("len(offerings) = %d, want 2", len(offerings))
	}
	for i := range offerings {
		switch offerings[i].ProviderModelID {
		case "model-free":
			free = &offerings[i]
		case "model-paid":
			paid = &offerings[i]
		}
	}
	if free == nil || paid == nil {
		t.Fatalf("offerings = %+v, want model-free and model-paid", offerings)
	}
	if free.Availability != "available" || paid.Availability != "available" {
		t.Fatalf("availability = %q/%q, want available/available", free.Availability, paid.Availability)
	}
	if free.Pricing == nil || paid.Pricing == nil {
		t.Fatalf("pricing not persisted: free=%v paid=%v", free.Pricing, paid.Pricing)
	}
	if len(free.Operations) != 1 || free.Operations[0].Operation != "chat" {
		t.Fatalf("model-free operations = %+v, want exactly one chat operation (the unrecognized capability must not produce a row)", free.Operations)
	}
	if free.Operations[0].CertificationStatus != "discovered" || free.Operations[0].CapabilityTruth != "unknown" || free.Operations[0].CertificationVersion != 1 {
		t.Fatalf("model-free chat certification baseline = %+v, want discovered/unknown/1", free.Operations[0])
	}
	if len(paid.Operations) != 1 || paid.Operations[0].Operation != "chat" {
		t.Fatalf("model-paid operations = %+v, want exactly one chat operation", paid.Operations)
	}
	return free, paid
}

// assertP3aProvenanceRunRow checks the discovery_runs row for
// TestP3aGate_DiscoveryRunStoresProvenance. The run id is minted
// separately from the job id (discoveryIDCounter mints both in sequence
// inside serveDiscover), so the run is read back by account rather than
// assuming an id relationship this test has no business asserting.
func assertP3aProvenanceRunRow(t *testing.T, db *storage.DB, accountID string) {
	t.Helper()
	var runStatus string
	var runGeneration int64
	if err := db.Conn().QueryRow(`SELECT status, generation FROM discovery_runs WHERE account_id = ?`, accountID).Scan(&runStatus, &runGeneration); err != nil {
		t.Fatalf("query discovery_runs for %q: %v", accountID, err)
	}
	if runStatus != "applied" {
		t.Fatalf("discovery_runs.status = %q, want applied", runStatus)
	}
	if runGeneration < 1 {
		t.Fatalf("discovery_runs.generation = %d, want >= 1 (monotonic)", runGeneration)
	}
}

// assertP3aProvenanceCostFacts checks the /offerings-rendered cost
// provenance tuple for TestP3aGate_DiscoveryRunStoresProvenance.
func assertP3aProvenanceCostFacts(t *testing.T, list []effectiveOfferingJSON) {
	t.Helper()
	freeJSON := p3aFindOffering(t, list, "model-free")
	paidJSON := p3aFindOffering(t, list, "model-paid")
	if freeJSON.Cost.Source != "provider_price" || freeJSON.Cost.IsFree == nil || !*freeJSON.Cost.IsFree || !freeJSON.Cost.ExactIdentityMatch {
		t.Fatalf("model-free cost = %+v, want provider_price/is_free=true/exact_identity_match=true", freeJSON.Cost)
	}
	if paidJSON.Cost.Source != "provider_price" || paidJSON.Cost.IsFree == nil || *paidJSON.Cost.IsFree || !paidJSON.Cost.ExactIdentityMatch {
		t.Fatalf("model-paid cost = %+v, want provider_price/is_free=false/exact_identity_match=true", paidJSON.Cost)
	}
}

// =====================================================================
// (2) TestP3aGate_ModelsReflectsCatalogAndCertification
// =====================================================================

func TestP3aGate_ModelsReflectsCatalogAndCertification(t *testing.T) {
	db := testControlDB(t)
	const acct = "acct-p3a-cat"

	// Two offerings sharing one canonical model: A (available, certified
	// chat) and C (withdrawn, discovered chat) — proves grouping AND that
	// a withdrawn offering still appears (04 §5: catalog visible, never
	// filtered).
	modelsSeedOffering(t, db, offeringSeed{
		AccountID: acct, ProviderID: "prov-p3a-cat", ProviderModelID: "model-a", ModelID: "model-shared",
		ModelDisplayName: "Shared Model",
		ContextLength:    modelsIntPtr(4096),
		CapabilitiesJSON: modelsStrPtr(`["chat"]`),
		Operations:       []offeringOpSeed{{Operation: "chat", Status: "certified", Truth: "supported"}},
	})
	modelsSeedOffering(t, db, offeringSeed{
		AccountID: acct, ProviderID: "prov-p3a-cat", ProviderModelID: "model-c", ModelID: "model-shared",
		ContextLength:    modelsIntPtr(4096),
		CapabilitiesJSON: modelsStrPtr(`["chat"]`),
		Operations:       []offeringOpSeed{{Operation: "chat", Status: "discovered", Truth: "unknown"}},
	})
	if _, err := db.Conn().Exec(
		`UPDATE account_model_offerings SET availability = 'withdrawn' WHERE account_id = ? AND provider_model_id = ?`,
		acct, "model-c",
	); err != nil {
		t.Fatalf("withdraw model-c: %v", err)
	}

	// B: known chat offering with UNKNOWN context (its own model).
	modelsSeedOffering(t, db, offeringSeed{
		AccountID: acct, ProviderID: "prov-p3a-cat", ProviderModelID: "model-b", ModelID: "model-b",
		CapabilitiesJSON: modelsStrPtr(`["chat"]`),
		Operations:       []offeringOpSeed{{Operation: "chat", Status: "discovered", Truth: "unknown"}},
	})

	// D: image-generation-only offering (its own model) — catalog_only.
	modelsSeedOffering(t, db, offeringSeed{
		AccountID: acct, ProviderID: "prov-p3a-cat", ProviderModelID: "model-d", ModelID: "model-d",
		ContextLength:    modelsIntPtr(4096),
		CapabilitiesJSON: modelsStrPtr(`["image_generation"]`),
		Operations:       []offeringOpSeed{{Operation: "image_generation", Status: "discovered", Truth: "unknown"}},
	})

	mux, cookie, _ := p3aOwnerMux(t, db)

	offeringsRec := p3aGet(t, mux, cookie, "/api/control/v1/offerings?account_id="+acct)
	if offeringsRec.Code != http.StatusOK {
		t.Fatalf("GET /offerings status = %d, want 200; body = %q", offeringsRec.Code, offeringsRec.Body.String())
	}
	offerings := p3aDecodeOfferings(t, offeringsRec.Body.Bytes())
	if len(offerings) != 4 {
		t.Fatalf("len(offerings) = %d, want 4 (nothing filtered out)", len(offerings))
	}

	a := p3aFindOffering(t, offerings, "model-a")
	c := p3aFindOffering(t, offerings, "model-c")
	b := p3aFindOffering(t, offerings, "model-b")
	d := p3aFindOffering(t, offerings, "model-d")

	assertP3aRawCatalogVisible(t, c, d)
	assertP3aUnknownContext(t, offeringsRec.Body.String(), b)
	assertP3aCertificationRenderedVerbatim(t, a)
	assertP3aTruthfulUnknowns(t, offerings)

	// GET /models groups by canonical model: model-shared's group must
	// contain exactly A and C, each identical to what /offerings rendered.
	modelsRec := p3aGet(t, mux, cookie, "/api/control/v1/models?account_id="+acct)
	if modelsRec.Code != http.StatusOK {
		t.Fatalf("GET /models status = %d, want 200; body = %q", modelsRec.Code, modelsRec.Body.String())
	}
	var modelsEnv struct {
		Data []modelGroupJSON `json:"data"`
	}
	if err := json.Unmarshal(modelsRec.Body.Bytes(), &modelsEnv); err != nil {
		t.Fatalf("decode /models: %v; body = %q", err, modelsRec.Body.String())
	}
	var sharedGroup *modelGroupJSON
	for i := range modelsEnv.Data {
		if modelsEnv.Data[i].ModelID == "model-shared" {
			sharedGroup = &modelsEnv.Data[i]
		}
	}
	if sharedGroup == nil {
		t.Fatalf("no model-shared group in %+v", modelsEnv.Data)
	}
	if len(sharedGroup.Offerings) != 2 {
		t.Fatalf("model-shared group offerings = %d, want 2", len(sharedGroup.Offerings))
	}
	groupedA := p3aFindOffering(t, sharedGroup.Offerings, "model-a")
	groupedC := p3aFindOffering(t, sharedGroup.Offerings, "model-c")
	if !jsonEqual(t, groupedA, a) {
		t.Fatalf("grouped model-a differs from /offerings' model-a:\ngrouped = %+v\noffering = %+v", groupedA, a)
	}
	if !jsonEqual(t, groupedC, c) {
		t.Fatalf("grouped model-c differs from /offerings' model-c:\ngrouped = %+v\noffering = %+v", groupedC, c)
	}

	// GET /offerings/{id}/certification for A's chat op shares the SAME
	// underlying (state, truth) verdict /offerings reported for that same
	// operation — one shared truth, two surfaces. Their "routable" fields
	// are DELIBERATELY not required to match: models.Routable (04 §5)
	// documents itself as answering only the certification-layer question
	// (state=certified && truth=supported), which is exactly what
	// certificationJSON.Routable renders; /offerings' capability Routable
	// is the FULLER end-to-end admission — that same certification-layer
	// predicate additionally ANDed with "effective" (native ∧ provider ∧
	// transport support, 04 §3) — so the two legitimately diverge whenever
	// effective is false, exactly as it is for every offering this phase.
	opID := "model-a-op-chat" // modelsSeedOffering's deterministic id convention
	certRec := p3aGet(t, mux, cookie, "/api/control/v1/offerings/"+opID+"/certification")
	if certRec.Code != http.StatusOK {
		t.Fatalf("GET certification status = %d, want 200; body = %q", certRec.Code, certRec.Body.String())
	}
	var certEnv struct {
		Data certificationJSON `json:"data"`
	}
	if err := json.Unmarshal(certRec.Body.Bytes(), &certEnv); err != nil {
		t.Fatalf("decode certification: %v", err)
	}
	aChatCap := findCapability(t, a.Capabilities, "chat")
	if certEnv.Data.State != aChatCap.State || certEnv.Data.CapabilityTruth != aChatCap.Truth {
		t.Fatalf("certification read state/truth = %s/%s, want /offerings' chat capability state/truth %s/%s", certEnv.Data.State, certEnv.Data.CapabilityTruth, aChatCap.State, aChatCap.Truth)
	}
	wantCertRoutable := models.Routable(models.CertificationState(certEnv.Data.State), models.CapabilityTruth(certEnv.Data.CapabilityTruth))
	if certEnv.Data.CertifiedAndSupported != wantCertRoutable {
		t.Fatalf("certification certified_and_supported = %v, want models.Routable(state,truth) = %v (the certification-layer predicate alone)", certEnv.Data.CertifiedAndSupported, wantCertRoutable)
	}
	if aChatCap.Routable != (wantCertRoutable && aChatCap.Effective) {
		t.Fatalf("/offerings chat Routable = %v, want models.Routable(state,truth) && effective = %v", aChatCap.Routable, wantCertRoutable && aChatCap.Effective)
	}
}

// assertP3aRawCatalogVisible proves criterion 2's "raw catalog visible,
// not filtered": the withdrawn (c) and image-only (d) offerings still
// appear in /offerings, ineligible for every tier with the documented
// reason codes — never simply absent.
func assertP3aRawCatalogVisible(t *testing.T, c, d effectiveOfferingJSON) {
	t.Helper()
	if c.Availability != "withdrawn" {
		t.Fatalf("model-c availability = %q, want withdrawn (still rendered, not filtered)", c.Availability)
	}
	for tier, elig := range c.Tiers {
		if elig.Eligible {
			t.Fatalf("model-c (withdrawn) tier %s eligible = true, want false", tier)
		}
		if !containsString(elig.Reasons, intelligence.ReasonNotAvailable) {
			t.Fatalf("model-c (withdrawn) tier %s reasons = %v, want %s", tier, elig.Reasons, intelligence.ReasonNotAvailable)
		}
	}
	if d.Classification != "catalog_only" {
		t.Fatalf("model-d classification = %q, want catalog_only", d.Classification)
	}
	for tier, elig := range d.Tiers {
		if elig.Eligible {
			t.Fatalf("model-d (image-only) tier %s eligible = true, want false", tier)
		}
		if !containsString(elig.Reasons, intelligence.ReasonCatalogOnly) {
			t.Fatalf("model-d (image-only) tier %s reasons = %v, want %s", tier, elig.Reasons, intelligence.ReasonCatalogOnly)
		}
	}
}

func assertP3aUnknownContext(t *testing.T, rawBody string, b effectiveOfferingJSON) {
	t.Helper()
	if b.EffectiveContextTokens != nil {
		t.Fatalf("model-b effective_context_tokens = %v, want nil", *b.EffectiveContextTokens)
	}
	if strings.Contains(rawBody, `"effective_context_tokens":0`) {
		t.Fatalf("body contains a fabricated 0 for unknown context")
	}
}

func assertP3aCertificationRenderedVerbatim(t *testing.T, a effectiveOfferingJSON) {
	t.Helper()
	cap := findCapability(t, a.Capabilities, "chat")
	if cap.State != "certified" || cap.Truth != "supported" {
		t.Fatalf("model-a chat state/truth = %s/%s, want certified/supported", cap.State, cap.Truth)
	}
	wantRoutable := models.Routable(models.CertificationState(cap.State), models.CapabilityTruth(cap.Truth)) && cap.Effective
	if cap.Routable != wantRoutable {
		t.Fatalf("model-a chat routable = %v, want models.Routable(state,truth) && effective = %v", cap.Routable, wantRoutable)
	}
}

// assertP3aTruthfulUnknowns proves EVERY capability across every seeded
// offering reports effective:false and routable:false — including model-a's
// certified+supported chat — because no native-capability fact and no
// transport registry exist this phase (04 §2/§3). This assertion is
// EXPECTED TO CHANGE once a later unit (P4-ish, wiring a real transport
// registry / native-capability source) supplies that evidence; until then,
// asserting effective:true here would be fabricating a fact.
func assertP3aTruthfulUnknowns(t *testing.T, offerings []effectiveOfferingJSON) {
	t.Helper()
	for _, o := range offerings {
		for _, c := range o.Capabilities {
			if c.Effective {
				t.Fatalf("%s capability %s effective = true, want false (native/transport unknown this phase)", o.ProviderModelID, c.Operation)
			}
			if c.Routable {
				t.Fatalf("%s capability %s routable = true, want false (native/transport unknown this phase)", o.ProviderModelID, c.Operation)
			}
		}
	}
}

func findCapability(t *testing.T, caps []capabilityJSON, operation string) capabilityJSON {
	t.Helper()
	for _, c := range caps {
		if c.Operation == operation {
			return c
		}
	}
	t.Fatalf("no capability %q in %+v", operation, caps)
	return capabilityJSON{}
}

func jsonEqual(t *testing.T, a, b any) bool {
	t.Helper()
	ab, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	bb, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(ab) == string(bb)
}

// =====================================================================
// (3) TestP3aGate_FreeSafetyFailsClosed
// =====================================================================

func TestP3aGate_FreeSafetyFailsClosed(t *testing.T) {
	db := testControlDB(t)
	const acct = "acct-p3a-fs"

	seedFreeSafetyOffering := func(providerModelID string, pricingJSON *string) {
		modelsSeedOffering(t, db, offeringSeed{
			AccountID: acct, ProviderID: "prov-p3a-fs", ProviderModelID: providerModelID, ModelID: providerModelID + "-model",
			ContextLength:    modelsIntPtr(4096),
			CapabilitiesJSON: modelsStrPtr(`["chat"]`),
			PricingJSON:      pricingJSON,
			Operations:       []offeringOpSeed{{Operation: "chat", Status: "certified", Truth: "supported"}},
		})
	}
	seedFreeSafetyOffering("model-free", modelsStrPtr(`{"cost":{"input":0,"output":0}}`))
	seedFreeSafetyOffering("model-paid", modelsStrPtr(`{"cost":{"input":5,"output":10}}`))
	seedFreeSafetyOffering("model-miss", nil)
	seedFreeSafetyOffering("model-malformed", modelsStrPtr(`{not-valid-json`))

	mux, cookie, csrfToken := p3aOwnerMux(t, db)

	readOfferings := func() []effectiveOfferingJSON {
		rec := p3aGet(t, mux, cookie, "/api/control/v1/offerings?account_id="+acct)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /offerings status = %d, want 200; body = %q", rec.Code, rec.Body.String())
		}
		return p3aDecodeOfferings(t, rec.Body.Bytes())
	}

	offerings := readOfferings()
	free := p3aFindOffering(t, offerings, "model-free")
	paid := p3aFindOffering(t, offerings, "model-paid")
	miss := p3aFindOffering(t, offerings, "model-miss")
	malformed := p3aFindOffering(t, offerings, "model-malformed")

	// Positive control FIRST: if this fails, the rest of this test is
	// vacuous — a free account must be able to route SOMETHING.
	if !free.Tiers["venom/lite"].Eligible {
		t.Fatalf("POSITIVE CONTROL FAILED: model-free venom/lite eligible = false, want true — the rest of this test would be vacuous")
	}

	if paid.Tiers["venom/lite"].Eligible {
		t.Fatalf("model-paid venom/lite eligible = true, want false (a free account must never surface a paid model)")
	}
	if !containsString(paid.Tiers["venom/lite"].Reasons, intelligence.ReasonCostIneligible) {
		t.Fatalf("model-paid venom/lite reasons = %v, want %s", paid.Tiers["venom/lite"].Reasons, intelligence.ReasonCostIneligible)
	}
	// 04 §2b: known-paid is legitimately eligible for pro/max — only lite
	// excludes it. Asserting this too proves the exclusion is tier-specific,
	// not an overzealous blanket rejection of every paid fact.
	if !paid.Tiers["venom/pro"].Eligible || !paid.Tiers["venom/max"].Eligible {
		t.Fatalf("model-paid pro/max = %+v/%+v, want both eligible (known-paid is admitted outside lite)", paid.Tiers["venom/pro"], paid.Tiers["venom/max"])
	}

	for _, unknown := range []struct {
		name string
		o    effectiveOfferingJSON
	}{{"model-miss (dataset miss)", miss}, {"model-malformed", malformed}} {
		if unknown.o.Cost.IsFree != nil {
			t.Fatalf("%s cost.is_free = %v, want null (never false-as-sentinel)", unknown.name, *unknown.o.Cost.IsFree)
		}
		for _, tier := range []string{"venom/lite", "venom/pro", "venom/max"} {
			elig := unknown.o.Tiers[tier]
			if elig.Eligible {
				t.Fatalf("%s tier %s eligible = true, want false (unknown fails closed for EVERY tier, 04 §2b)", unknown.name, tier)
			}
			if !containsString(elig.Reasons, intelligence.ReasonCostIneligible) {
				t.Fatalf("%s tier %s reasons = %v, want %s", unknown.name, tier, elig.Reasons, intelligence.ReasonCostIneligible)
			}
		}
	}

	// Enrichment independence: capture, toggle on, re-read, compare;
	// toggle off, re-read, compare again.
	before := readOfferings()

	toggle := func(enabled bool) {
		body := []byte(`{"enabled":false}`)
		if enabled {
			body = []byte(`{"enabled":true}`)
		}
		rec := p3aMutate(t, mux, cookie, csrfToken, http.MethodPut, "/api/control/v1/settings/enrichment", body)
		if rec.Code != http.StatusOK {
			t.Fatalf("PUT /settings/enrichment(%v) status = %d, want 200; body = %q", enabled, rec.Code, rec.Body.String())
		}
		// Confirm the write really took effect — otherwise "cost unchanged"
		// would be true for the wrong reason (a silently-failed write).
		getRec := p3aGet(t, mux, cookie, "/api/control/v1/settings")
		var settingsEnv struct {
			Data struct {
				EnrichmentEnabled bool `json:"enrichment_enabled"`
			} `json:"data"`
		}
		if err := json.Unmarshal(getRec.Body.Bytes(), &settingsEnv); err != nil {
			t.Fatalf("decode /settings: %v", err)
		}
		if settingsEnv.Data.EnrichmentEnabled != enabled {
			t.Fatalf("GET /settings enrichment_enabled = %v after toggling to %v — the write did not take effect", settingsEnv.Data.EnrichmentEnabled, enabled)
		}
	}

	toggle(true)
	afterOn := readOfferings()
	assertP3aCostsIdentical(t, before, afterOn)

	toggle(false)
	afterOff := readOfferings()
	assertP3aCostsIdentical(t, before, afterOff)
}

func assertP3aCostsIdentical(t *testing.T, before, after []effectiveOfferingJSON) {
	t.Helper()
	if len(before) != len(after) {
		t.Fatalf("offering count changed: before=%d after=%d", len(before), len(after))
	}
	for _, b := range before {
		a := p3aFindOffering(t, after, b.ProviderModelID)
		if !jsonEqual(t, a.Cost, b.Cost) {
			t.Fatalf("%s cost changed after an enrichment toggle:\nbefore = %+v\nafter  = %+v", b.ProviderModelID, b.Cost, a.Cost)
		}
	}
}

// =====================================================================
// (4) TestP3aGate_GenerationGuard
// =====================================================================

func TestP3aGate_GenerationGuard(t *testing.T) {
	t.Run("newer_generation_applies_older_superseded", subtestP3aNewerWinsOlderSuperseded)
	t.Run("explicit_empty_list_is_authoritative_withdraw", subtestP3aEmptyListWithdraws)
	// A DIFFERENT withdrawal path than the one above: an explicit empty
	// list takes the Withdraw=true branch in DiscoveryRepo.Apply, which
	// never calls withdrawMissing at all. This subtest exercises the
	// OTHER branch — a non-empty follow-up snapshot that simply omits a
	// previously-seen model — which is the only path that calls
	// withdrawMissing, and is otherwise NOT independently covered by any
	// other subtest here.
	t.Run("model_missing_from_followup_snapshot_is_withdrawn", subtestP3aMissingModelWithdrawn)
	t.Run("malformed_model_keeps_last_known_good", subtestP3aMalformedKeepsLastKnownGood)
}

// subtestP3aMissingModelWithdrawn is TestP3aGate_GenerationGuard's
// "model_missing_from_followup_snapshot_is_withdrawn" subtest: a model
// reported by one run but omitted from the NEXT non-empty run is
// withdrawn (still visible via the read model, per criterion 2), while a
// model present in both stays available.
func subtestP3aMissingModelWithdrawn(t *testing.T) {
	db := testControlDB(t)
	const acct = "acct-gen-missing"
	p3aSeedAccount(t, db, acct, "prov-gen-missing")
	discRepo := storage.NewDiscoveryRepo(db, discoveryIDCounter())
	clock := func() time.Time { return time.Unix(5000, 0) }
	adapter := &fakeDiscoveryAdapter{models: []providers.DiscoveredModel{
		{ProviderModelID: "model-stays", DisplayName: "Stays"},
		{ProviderModelID: "model-drops", DisplayName: "Drops"},
	}}
	svc := intelligence.NewDiscoveryService(adapter, p3aTrivialLeaser{}, discRepo, discRepo, clock)

	seedResult, err := svc.Run(context.Background(), intelligence.RunParams{AccountID: acct, ProviderID: "prov-gen-missing", CredentialID: "cred-1", RunID: "run-both"})
	if err != nil || seedResult.Outcome != intelligence.OutcomeApplied {
		t.Fatalf("seed Run = (%+v, %v), want OutcomeApplied", seedResult, err)
	}

	adapter.models = []providers.DiscoveredModel{{ProviderModelID: "model-stays", DisplayName: "Stays"}}
	followupResult, err := svc.Run(context.Background(), intelligence.RunParams{AccountID: acct, ProviderID: "prov-gen-missing", CredentialID: "cred-1", RunID: "run-one-only"})
	if err != nil || followupResult.Outcome != intelligence.OutcomeApplied {
		t.Fatalf("followup Run = (%+v, %v), want OutcomeApplied", followupResult, err)
	}

	catalog := storage.NewCatalogRepo(db)
	offerings, _, err := catalog.ListOfferings(context.Background(), storage.CatalogListParams{AccountID: acct, Limit: 10})
	if err != nil {
		t.Fatalf("ListOfferings: %v", err)
	}
	if len(offerings) != 2 {
		t.Fatalf("len(offerings) = %d, want 2 (both still visible, per criterion 2)", len(offerings))
	}
	stays := p3aFindOffering(t, effectiveOfferingsFromCatalogRows(offerings), "model-stays")
	drops := p3aFindOffering(t, effectiveOfferingsFromCatalogRows(offerings), "model-drops")
	if stays.Availability != "available" {
		t.Fatalf("model-stays availability = %q, want available", stays.Availability)
	}
	if drops.Availability != "withdrawn" {
		t.Fatalf("model-drops availability = %q, want withdrawn (omitted from the follow-up snapshot)", drops.Availability)
	}
}

// effectiveOfferingsFromCatalogRows adapts []storage.CatalogOfferingRow to
// the minimal shape p3aFindOffering needs (ProviderModelID + Availability),
// so subtestP3aMissingModelWithdrawn can reuse that helper without pulling
// in the full HTTP JSON projection for a storage-layer-only check.
func effectiveOfferingsFromCatalogRows(rows []storage.CatalogOfferingRow) []effectiveOfferingJSON {
	out := make([]effectiveOfferingJSON, len(rows))
	for i, r := range rows {
		out[i] = effectiveOfferingJSON{ProviderModelID: r.ProviderModelID, Availability: r.Availability}
	}
	return out
}

// subtestP3aNewerWinsOlderSuperseded is TestP3aGate_GenerationGuard's
// "newer_generation_applies_older_superseded" subtest, split out to keep
// the parent test's cyclomatic complexity down.
func subtestP3aNewerWinsOlderSuperseded(t *testing.T) {
	db := testControlDB(t)
	const acct = "acct-gen-race"
	p3aSeedAccount(t, db, acct, "prov-gen-race")
	discRepo := storage.NewDiscoveryRepo(db, discoveryIDCounter())
	now := time.Unix(2000, 0)

	genOlder, err := discRepo.BeginRun(context.Background(), acct, "run-older", now)
	if err != nil {
		t.Fatalf("BeginRun(older): %v", err)
	}
	genNewer, err := discRepo.BeginRun(context.Background(), acct, "run-newer", now)
	if err != nil {
		t.Fatalf("BeginRun(newer): %v", err)
	}

	newerSnap := intelligence.DiscoverySnapshot{
		AccountID: acct, ProviderID: "prov-gen-race", Generation: genNewer,
		Models: []intelligence.DiscoverySnapshotModel{{CanonicalKey: "key-newer", ProviderModelID: "model-newer"}},
	}
	if applied, err := discRepo.Apply(context.Background(), "run-newer", newerSnap, now); err != nil || !applied {
		t.Fatalf("Apply(newer) = (%v, %v), want (true, nil)", applied, err)
	}

	olderSnap := intelligence.DiscoverySnapshot{
		AccountID: acct, ProviderID: "prov-gen-race", Generation: genOlder,
		Models: []intelligence.DiscoverySnapshotModel{
			{CanonicalKey: "key-older-1", ProviderModelID: "model-older-1"},
			{CanonicalKey: "key-older-2", ProviderModelID: "model-older-2"},
		},
	}
	applied, err := discRepo.Apply(context.Background(), "run-older", olderSnap, now)
	if err != nil || applied {
		t.Fatalf("Apply(older) = (%v, %v), want (false, nil) — the slower older run must be superseded, never applied", applied, err)
	}

	status, _ := p3aDiscoveryRunStatus(t, db, "run-older")
	if status != "superseded" {
		t.Fatalf("run-older status = %q, want superseded", status)
	}
	newerStatus, _ := p3aDiscoveryRunStatus(t, db, "run-newer")
	if newerStatus != "applied" {
		t.Fatalf("run-newer status = %q, want applied", newerStatus)
	}

	// Observable outcome through the READ MODEL (not raw SQL): exactly
	// the newer generation's offering, never the older run's.
	catalog := storage.NewCatalogRepo(db)
	offerings, _, err := catalog.ListOfferings(context.Background(), storage.CatalogListParams{AccountID: acct, Limit: 10})
	if err != nil {
		t.Fatalf("ListOfferings: %v", err)
	}
	if len(offerings) != 1 || offerings[0].ProviderModelID != "model-newer" {
		t.Fatalf("offerings via CatalogRepo = %+v, want exactly [model-newer] (the older run's snapshot must never have landed)", offerings)
	}
}

// subtestP3aEmptyListWithdraws is TestP3aGate_GenerationGuard's
// "explicit_empty_list_is_authoritative_withdraw" subtest.
func subtestP3aEmptyListWithdraws(t *testing.T) {
	db := testControlDB(t)
	const acct = "acct-gen-withdraw"
	p3aSeedAccount(t, db, acct, "prov-gen-withdraw")
	discRepo := storage.NewDiscoveryRepo(db, discoveryIDCounter())
	clock := func() time.Time { return time.Unix(3000, 0) }
	adapter := &fakeDiscoveryAdapter{models: []providers.DiscoveredModel{{ProviderModelID: "model-x", DisplayName: "Model X"}}}
	svc := intelligence.NewDiscoveryService(adapter, p3aTrivialLeaser{}, discRepo, discRepo, clock)

	result1, err := svc.Run(context.Background(), intelligence.RunParams{AccountID: acct, ProviderID: "prov-gen-withdraw", CredentialID: "cred-1", RunID: "run-seed"})
	if err != nil || result1.Outcome != intelligence.OutcomeApplied {
		t.Fatalf("seed Run = (%+v, %v), want OutcomeApplied", result1, err)
	}

	adapter.models = nil // explicit empty list
	result2, err := svc.Run(context.Background(), intelligence.RunParams{AccountID: acct, ProviderID: "prov-gen-withdraw", CredentialID: "cred-1", RunID: "run-empty"})
	if err != nil || result2.Outcome != intelligence.OutcomeApplied {
		t.Fatalf("empty-list Run = (%+v, %v), want OutcomeApplied (an explicit empty list is an applied withdrawal, never a failure)", result2, err)
	}

	catalog := storage.NewCatalogRepo(db)
	offerings, _, err := catalog.ListOfferings(context.Background(), storage.CatalogListParams{AccountID: acct, Limit: 10})
	if err != nil {
		t.Fatalf("ListOfferings: %v", err)
	}
	if len(offerings) != 1 || offerings[0].Availability != "withdrawn" {
		t.Fatalf("offerings after empty-list run = %+v, want model-x withdrawn (still visible, per criterion 2)", offerings)
	}
}

// subtestP3aMalformedKeepsLastKnownGood is TestP3aGate_GenerationGuard's
// "malformed_model_keeps_last_known_good" subtest.
func subtestP3aMalformedKeepsLastKnownGood(t *testing.T) {
	db := testControlDB(t)
	const acct = "acct-gen-malformed"
	p3aSeedAccount(t, db, acct, "prov-gen-malformed")
	discRepo := storage.NewDiscoveryRepo(db, discoveryIDCounter())
	clock := func() time.Time { return time.Unix(4000, 0) }
	adapter := &fakeDiscoveryAdapter{models: []providers.DiscoveredModel{{ProviderModelID: "model-good", DisplayName: "Good Model"}}}
	svc := intelligence.NewDiscoveryService(adapter, p3aTrivialLeaser{}, discRepo, discRepo, clock)
	catalog := storage.NewCatalogRepo(db)

	seedResult, err := svc.Run(context.Background(), intelligence.RunParams{AccountID: acct, ProviderID: "prov-gen-malformed", CredentialID: "cred-1", RunID: "run-good"})
	if err != nil || seedResult.Outcome != intelligence.OutcomeApplied {
		t.Fatalf("seed Run = (%+v, %v), want OutcomeApplied", seedResult, err)
	}
	before := p3aOfferingsSnapshot(t, catalog, acct)

	assertP3aRunFailsKeepingSnapshot(t, db, catalog, svc, adapter, acct, "prov-gen-malformed", "run-bad-id",
		[]providers.DiscoveredModel{{ProviderModelID: "", DisplayName: "No ID"}},
		intelligence.ReasonInvalidModel, before)

	tooMany := make([]providers.DiscoveredModel, intelligence.MaxDiscoveredModels+1)
	for i := range tooMany {
		tooMany[i] = providers.DiscoveredModel{ProviderModelID: "extra-model", DisplayName: "Extra"}
	}
	assertP3aRunFailsKeepingSnapshot(t, db, catalog, svc, adapter, acct, "prov-gen-malformed", "run-too-many",
		tooMany, intelligence.ReasonTooManyModels, before)
}

// assertP3aRunFailsKeepingSnapshot points adapter at badModels, drives one
// discovery run through svc, and asserts it ends OutcomeFailed with
// wantReason, its discovery_runs row is 'failed', and the catalog is
// byte-for-byte unchanged from before.
func assertP3aRunFailsKeepingSnapshot(t *testing.T, db *storage.DB, catalog *storage.CatalogRepo, svc *intelligence.DiscoveryService, adapter *fakeDiscoveryAdapter, accountID, providerID, runID string, badModels []providers.DiscoveredModel, wantReason string, before string) {
	t.Helper()
	adapter.models = badModels

	result, err := svc.Run(context.Background(), intelligence.RunParams{AccountID: accountID, ProviderID: providerID, CredentialID: "cred-1", RunID: runID})
	if err != nil || result.Outcome != intelligence.OutcomeFailed || result.ReasonCode != wantReason {
		t.Fatalf("Run(%s) = (%+v, %v), want OutcomeFailed/%s", runID, result, err, wantReason)
	}
	status, _ := p3aDiscoveryRunStatus(t, db, runID)
	if status != "failed" {
		t.Fatalf("%s status = %q, want failed", runID, status)
	}
	after := p3aOfferingsSnapshot(t, catalog, accountID)
	if before != after {
		t.Fatalf("catalog changed after a failed run (%s):\nbefore = %s\nafter  = %s", runID, before, after)
	}
}

// =====================================================================
// (5) TestP3aGate_NoInferenceProbeRan
// =====================================================================

func TestP3aGate_NoInferenceProbeRan(t *testing.T) {
	clock := fixedDiscoveryClock()
	db := testControlDB(t)
	const accountID = "acct-no-probe"
	const providerID = "prov-no-probe"
	p3aSeedAccount(t, db, accountID, providerID)

	credRepo := storage.NewAccountCredentialRepo(db)
	kr := testKeyring(t)
	credSvc := application.NewCredentialService(credRepo, kr, func() time.Time { return clock })
	const credentialID = "cred-no-probe"
	if _, err := credSvc.Store(context.Background(), application.StoreCredentialParams{
		ID:           credentialID,
		AccountID:    accountID,
		ProviderID:   providerID,
		Kind:         domain.CredentialKindAPIKey,
		Active:       true,
		PlaintextKey: "not-a-real-credential",
	}); err != nil {
		t.Fatalf("store credential: %v", err)
	}

	// A registry whose APIKeyAdapter is the TRAP: discovery must never call
	// the provider's authentic-validation probe (the only inference-shaped
	// surface that exists this phase).
	discAdapter := &fakeDiscoveryAdapter{models: []providers.DiscoveredModel{
		{ProviderModelID: "model-a", DisplayName: "Model A", Capabilities: []string{"chat"}},
		{ProviderModelID: "model-b", DisplayName: "Model B", Capabilities: []string{"chat", "vision"}},
	}}
	reg := providers.NewRegistry()
	if err := reg.Register(providers.Definition{
		ID:        providers.ProviderID(providerID),
		AuthMode:  providers.AuthModeAPIKey,
		APIKey:    p3aTrapAPIKeyAdapter{t: t},
		Discovery: discAdapter,
	}); err != nil {
		t.Fatalf("register trap adapter: %v", err)
	}

	accountRepo := storage.NewAccountRepo(db)
	catalogRepo := storage.NewCatalogRepo(db)
	jobRepo := storage.NewJobRepo(db)
	discoveryRepo := storage.NewDiscoveryRepo(db, discoveryIDCounter())
	handler := NewDiscoveryHandler(
		accountRepo, credRepo, catalogRepo, jobRepo, discoveryRepo, reg, credSvc,
		newAuditEmitter(db, nil), newIdempotencyStore(), discoveryIDCounter(), func() time.Time { return clock },
	)

	mux := newTestDiscoveryMux(handler)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, discoveryRequest(http.MethodPost, "/api/control/v1/accounts/"+accountID+"/discover"))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body = %q", rec.Code, rec.Body.String())
	}
	data := decodeDiscoverResponse(t, rec.Body.Bytes())
	row := waitForJobTerminal(t, jobRepo, data.JobID, 2*time.Second)
	if row.Status != storage.JobCompleted {
		t.Fatalf("job Status = %q, want completed (error = %+v) — the trap adapter would have failed the test directly if it fired", row.Status, row.Error)
	}

	var offCertNotDiscovered int
	if err := db.Conn().QueryRow(
		`SELECT COUNT(*) FROM certifications WHERE status != 'discovered' OR capability_truth != 'unknown'`,
	).Scan(&offCertNotDiscovered); err != nil {
		t.Fatalf("count non-baseline certifications: %v", err)
	}
	if offCertNotDiscovered != 0 {
		t.Fatalf("%d certifications advanced past discovered/unknown — discovery alone must never advance certification", offCertNotDiscovered)
	}

	var withEvidence int
	if err := db.Conn().QueryRow(
		`SELECT COUNT(*) FROM certifications WHERE evidence_ref IS NOT NULL AND evidence_ref != ''`,
	).Scan(&withEvidence); err != nil {
		t.Fatalf("count certifications with evidence_ref: %v", err)
	}
	if withEvidence != 0 {
		t.Fatalf("%d certifications carry evidence_ref — no probe evidence should exist yet", withEvidence)
	}
}

// =====================================================================
// (6) TestP3aGate_ControlSurfaceIsOwnerGated
// =====================================================================

func TestP3aGate_ControlSurfaceIsOwnerGated(t *testing.T) {
	db := testControlDB(t)

	// Seed one account whose provider genuinely has no discovery adapter in
	// the REAL ControlMux registry (anything other than opencode-zen /
	// antigravity), and one account under opencode-zen (which DOES have a
	// real Discovery adapter registered) but with NO active credential —
	// both rejection paths are reachable with zero network calls, since
	// each is rejected before any adapter method is ever invoked.
	p3aSeedAccount(t, db, "acct-no-disc", "prov-unregistered")
	p3aSeedAccount(t, db, "acct-no-cred", string(providers.OpenCodeZenID))

	mux := ControlMux(testAllowedHost, fakeSPA(), db, testKeyring(t))

	unauthed := []struct{ method, path string }{
		{http.MethodGet, "/api/control/v1/models"},
		{http.MethodGet, "/api/control/v1/offerings"},
		{http.MethodGet, "/api/control/v1/offerings/does-not-exist/certification"},
		{http.MethodPost, "/api/control/v1/accounts/does-not-exist/discover"},
		{http.MethodPut, "/api/control/v1/settings/enrichment"},
	}
	for _, r := range unauthed {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, newAuthRequest(t, r.method, r.path, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s unauthenticated status = %d, want 401", r.method, r.path, rec.Code)
		}
	}

	cookie, csrfToken := setupOwnerWithCSRF(t, mux)

	csrfLess := []struct{ method, path string }{
		{http.MethodPost, "/api/control/v1/accounts/does-not-exist/discover"},
		{http.MethodPut, "/api/control/v1/settings/enrichment"},
	}
	for _, r := range csrfLess {
		req := newAuthRequest(t, r.method, r.path, bytesBuffer([]byte(`{"enabled":true}`)))
		req.AddCookie(cookie) // no X-CSRF-Token
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s %s without CSRF status = %d, want 403", r.method, r.path, rec.Code)
		}
		if code := decodeErrorCode(t, rec.Body.Bytes()); code != "csrf_failed" {
			t.Fatalf("%s %s error code = %q, want csrf_failed", r.method, r.path, code)
		}
	}
	if n := countRowsQuery(t, db, `SELECT COUNT(*) FROM jobs`); n != 0 {
		t.Fatalf("jobs row count = %d, want 0 (CSRF rejection happens before any side effect)", n)
	}
	if n := countRowsQuery(t, db, `SELECT COUNT(*) FROM owner_settings WHERE enrichment_enabled = 1`); n != 0 {
		t.Fatalf("owner_settings enrichment_enabled rows = %d, want 0 (CSRF rejection happens before any write)", n)
	}

	rejections := []struct {
		path       string
		wantStatus int
		wantCode   string
	}{
		{"/api/control/v1/accounts/does-not-exist/discover", http.StatusNotFound, "not_found"},
		{"/api/control/v1/accounts/acct-no-disc/discover", http.StatusConflict, "discovery_unsupported"},
		{"/api/control/v1/accounts/acct-no-cred/discover", http.StatusConflict, "credential_unavailable"},
	}
	for _, r := range rejections {
		rec := p3aMutate(t, mux, cookie, csrfToken, http.MethodPost, r.path, nil)
		if rec.Code != r.wantStatus {
			t.Fatalf("POST %s status = %d, want %d; body = %q", r.path, rec.Code, r.wantStatus, rec.Body.String())
		}
		if code := decodeErrorCode(t, rec.Body.Bytes()); code != r.wantCode {
			t.Fatalf("POST %s error code = %q, want %q", r.path, code, r.wantCode)
		}
	}
	if n := countRowsQuery(t, db, `SELECT COUNT(*) FROM jobs`); n != 0 {
		t.Fatalf("jobs row count = %d, want 0 after every rejection path", n)
	}
	if n := countRowsQuery(t, db, `SELECT COUNT(*) FROM discovery_runs`); n != 0 {
		t.Fatalf("discovery_runs row count = %d, want 0 after every rejection path", n)
	}
}
