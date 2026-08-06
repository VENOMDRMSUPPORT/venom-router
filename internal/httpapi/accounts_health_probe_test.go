package httpapi

// ServeHealth's live-probe path (the P3 HealthAdapter slot, now filled):
// with a registered HealthAdapter and NO body-carried target, POST
// /accounts/{id}/health runs the provider probe with the account's stored
// credentials, transitions to the OBSERVED state, and stamps
// last_health_check_at / last_health_error. Every guard failing — no
// registry, no adapter, a body-carried target — keeps the P2b placeholder
// behavior byte-for-byte, which the tests below pin alongside the new
// path.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/application"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/intelligence"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// recordingHealthAdapter is a deterministic providers.HealthAdapter that
// returns a fixed observation and records the credential plaintext it was
// handed (so a test can prove the REAL decrypt-and-lease path ran).
type recordingHealthAdapter struct {
	obs      providers.HealthObservation
	err      error
	gotCreds []string
	calls    int
}

func (a *recordingHealthAdapter) CheckAccountHealth(_ context.Context, creds providers.StoredCredentials) (providers.HealthObservation, error) {
	a.calls++
	a.gotCreds = append(a.gotCreds, creds.Value)
	return a.obs, a.err
}

func (a *recordingHealthAdapter) CheckOfferingHealth(_ context.Context, creds providers.StoredCredentials, _ string) (providers.HealthObservation, error) {
	return a.CheckAccountHealth(context.Background(), creds)
}

// healthProbeRegistry registers adapter as prov-a's HealthAdapter (the
// provider newTestAccountsHandlerV2 seeds its account under).
func healthProbeRegistry(t *testing.T, adapter providers.HealthAdapter) *providers.Registry {
	t.Helper()
	reg := providers.NewRegistry()
	if err := reg.Register(providers.Definition{
		ID:        "prov-a",
		AuthMode:  providers.AuthModeAPIKey,
		Transport: providers.TransportKindOpenAICompatible,
		APIKey:    newFakeAPIKeyAdapter(),
		Health:    adapter,
	}); err != nil {
		t.Fatalf("register health-probe fixture provider: %v", err)
	}
	return reg
}

// readHealthRow reads the three columns the probe path owns.
func readHealthRow(t *testing.T, db *storage.DB, accountID string) (healthState string, checkedAt sql.NullInt64, lastErr sql.NullString) {
	t.Helper()
	if err := db.Conn().QueryRow(
		`SELECT health_state, last_health_check_at, last_health_error FROM accounts WHERE id = ?`, accountID,
	).Scan(&healthState, &checkedAt, &lastErr); err != nil {
		t.Fatalf("read health row: %v", err)
	}
	return healthState, checkedAt, lastErr
}

func TestHealth_LiveProbe_TransitionsHealthyAndStampsCheckedAt(t *testing.T) {
	clock := fixedAccountTestClock()
	const canaryKey = "zen-canary-key-for-health-probe"
	h, db, accountID, _ := newTestAccountsHandlerV2(t, clock, canaryKey)

	// Start from unknown so the healthy outcome is a REAL transition, not
	// the seeded value surviving.
	if _, err := db.Conn().Exec(`UPDATE accounts SET health_state = 'unknown' WHERE id = ?`, accountID); err != nil {
		t.Fatalf("reset health_state: %v", err)
	}

	adapter := &recordingHealthAdapter{obs: providers.HealthObservation{
		Status: "healthy", Scope: "account", CredentialValid: true, TransportReachable: true, CheckedAt: clock.Unix(),
	}}
	h = h.WithProviderRegistry(healthProbeRegistry(t, adapter))

	req := newAccountsRequest(http.MethodPost, "/api/control/v1/accounts/"+accountID+"/health", accountID, nil)
	rec := httptest.NewRecorder()
	h.ServeHealth(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("health status = %d, want 200; body = %q", rec.Code, rec.Body.String())
	}

	if adapter.calls != 1 {
		t.Fatalf("probe calls = %d, want exactly 1", adapter.calls)
	}
	// The probe received the DECRYPTED stored credential — the real
	// CredentialService lease path, not a placeholder.
	if len(adapter.gotCreds) != 1 || adapter.gotCreds[0] != canaryKey {
		t.Fatalf("probe credential = %q, want the decrypted stored key", adapter.gotCreds)
	}

	healthState, checkedAt, lastErr := readHealthRow(t, db, accountID)
	if healthState != string(domain.HealthHealthy) {
		t.Fatalf("health_state = %q, want healthy", healthState)
	}
	if !checkedAt.Valid || checkedAt.Int64 != clock.Unix() {
		t.Fatalf("last_health_check_at = %+v, want the injected clock %d", checkedAt, clock.Unix())
	}
	if lastErr.Valid {
		t.Fatalf("last_health_error = %q, want NULL after a healthy probe", lastErr.String)
	}

	// The projection reflects it in the same response.
	var envelope struct {
		Data struct {
			HealthState       string `json:"health_state"`
			LastHealthCheckAt string `json:"last_health_check_at"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	if envelope.Data.HealthState != "healthy" || envelope.Data.LastHealthCheckAt == "" {
		t.Fatalf("projection = %+v, want healthy with a non-empty last_health_check_at", envelope.Data)
	}
}

func TestHealth_LiveProbe_ExpiredObservationPersistsExpiredAndSafeError(t *testing.T) {
	clock := fixedAccountTestClock()
	h, db, accountID, _ := newTestAccountsHandlerV2(t, clock, "irrelevant-key")

	adapter := &recordingHealthAdapter{obs: providers.HealthObservation{
		Status: "expired", Scope: "account", CredentialValid: false, TransportReachable: true,
		Failure: &providers.HealthFailure{Class: "auth", Retryable: false, SafeMessage: "provider rejected the credential (401/403)"},
	}}
	h = h.WithProviderRegistry(healthProbeRegistry(t, adapter))

	req := newAccountsRequest(http.MethodPost, "/api/control/v1/accounts/"+accountID+"/health", accountID, nil)
	rec := httptest.NewRecorder()
	h.ServeHealth(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("health status = %d, want 200; body = %q", rec.Code, rec.Body.String())
	}

	healthState, checkedAt, lastErr := readHealthRow(t, db, accountID)
	if healthState != string(domain.HealthExpired) {
		t.Fatalf("health_state = %q, want expired", healthState)
	}
	if !checkedAt.Valid {
		t.Fatalf("last_health_check_at NULL, want stamped for an attempted probe")
	}
	if !lastErr.Valid || lastErr.String != "provider rejected the credential (401/403)" {
		t.Fatalf("last_health_error = %+v, want the observation's safe message", lastErr)
	}
}

func TestHealth_LiveProbe_UnreachableObservationPersistsUnavailable(t *testing.T) {
	clock := fixedAccountTestClock()
	h, db, accountID, _ := newTestAccountsHandlerV2(t, clock, "irrelevant-key")

	adapter := &recordingHealthAdapter{obs: providers.HealthObservation{
		Status: "unreachable", Scope: "account",
		Failure: &providers.HealthFailure{Class: "unavailable", Retryable: true, SafeMessage: "provider unavailable or rate limited"},
	}}
	h = h.WithProviderRegistry(healthProbeRegistry(t, adapter))

	req := newAccountsRequest(http.MethodPost, "/api/control/v1/accounts/"+accountID+"/health", accountID, nil)
	rec := httptest.NewRecorder()
	h.ServeHealth(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("health status = %d, want 200; body = %q", rec.Code, rec.Body.String())
	}

	healthState, _, _ := readHealthRow(t, db, accountID)
	if healthState != string(domain.HealthUnavailable) {
		t.Fatalf("health_state = %q, want unavailable (unreachable maps to unavailable, never expired)", healthState)
	}
}

func TestHealth_BodyTarget_SkipsProbeByteForByte(t *testing.T) {
	clock := fixedAccountTestClock()
	h, db, accountID, _ := newTestAccountsHandlerV2(t, clock, "irrelevant-key")

	adapter := &recordingHealthAdapter{obs: providers.HealthObservation{Status: "healthy"}}
	h = h.WithProviderRegistry(healthProbeRegistry(t, adapter))

	body, _ := json.Marshal(map[string]any{"health_state": "degraded"})
	req := newAccountsRequest(http.MethodPost, "/api/control/v1/accounts/"+accountID+"/health", accountID, body)
	rec := httptest.NewRecorder()
	h.ServeHealth(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("health status = %d, want 200; body = %q", rec.Code, rec.Body.String())
	}

	if adapter.calls != 0 {
		t.Fatalf("probe calls = %d, want 0 — a body-carried target must keep the original path", adapter.calls)
	}
	healthState, checkedAt, _ := readHealthRow(t, db, accountID)
	if healthState != string(domain.HealthDegraded) {
		t.Fatalf("health_state = %q, want the body-carried degraded", healthState)
	}
	if checkedAt.Valid {
		t.Fatalf("last_health_check_at = %d, want NULL — no probe ran", checkedAt.Int64)
	}
}

func TestHealth_NoAdapter_KeepsPlaceholderBehavior(t *testing.T) {
	clock := fixedAccountTestClock()
	h, db, accountID, _ := newTestAccountsHandlerV2(t, clock, "irrelevant-key")
	// No WithProviderRegistry at all — every pre-existing composition.

	req := newAccountsRequest(http.MethodPost, "/api/control/v1/accounts/"+accountID+"/health", accountID, nil)
	rec := httptest.NewRecorder()
	h.ServeHealth(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("health status = %d, want 200; body = %q", rec.Code, rec.Body.String())
	}

	healthState, checkedAt, lastErr := readHealthRow(t, db, accountID)
	if healthState != string(domain.HealthUnknown) {
		t.Fatalf("health_state = %q, want the placeholder default unknown", healthState)
	}
	if checkedAt.Valid || lastErr.Valid {
		t.Fatalf("probe evidence columns written (%+v, %+v) with no adapter — must stay NULL", checkedAt, lastErr)
	}
}

// TestDiscover_ModelsDevFailureSurfacesAsFailedJob wires the REAL
// opencode-zen adapter (catalog probe healthy, models.dev probe failing)
// into the discovery handler and proves the HTTP job goes to FAILED with
// the typed discovery code — never an empty-catalog success — when the
// free-set cannot be established (03 §3's fail-loud policy end to end).
func TestDiscover_ModelsDevFailureSurfacesAsFailedJob(t *testing.T) {
	clock := fixedDiscoveryClock()
	db := testControlDB(t)

	const providerID = "opencode-zen"
	const accountID = "acct-zen-1"
	if _, err := db.Conn().Exec(
		`INSERT INTO providers (id, display_name, auth_mode, funding_mode, funding_locked, created_at, updated_at) VALUES (?, ?, 'api_key', 'owner_policy', 0, 0, 0)`,
		providerID, "OpenCode Zen",
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

	credRepo := storage.NewAccountCredentialRepo(db)
	credSvc := application.NewCredentialService(credRepo, testKeyring(t), func() time.Time { return clock })
	if _, err := credSvc.Store(context.Background(), application.StoreCredentialParams{
		ID: "cred-zen-1", AccountID: accountID, ProviderID: providerID,
		Kind: domain.CredentialKindAPIKey, Active: true, PlaintextKey: "zen-key",
	}); err != nil {
		t.Fatalf("store credential: %v", err)
	}

	reg := providers.NewRegistry()
	zenModels := func(_ context.Context, _, _ string) ([]byte, error) {
		return []byte(`{"data":[{"id":"free-a"}]}`), nil
	}
	modelsDevDown := func(_ context.Context) ([]byte, error) {
		return nil, errors.New("models.dev is down")
	}
	if err := providers.RegisterOpenCodeZen(reg, nil, zenModels, modelsDevDown, func() time.Time { return clock }); err != nil {
		t.Fatalf("register zen: %v", err)
	}

	jobs := storage.NewJobRepo(db)
	h := NewDiscoveryHandler(
		storage.NewAccountRepo(db), credRepo, storage.NewCatalogRepo(db), jobs,
		storage.NewDiscoveryRepo(db, discoveryIDCounter()), reg, credSvc,
		newAuditEmitter(db, nil), newIdempotencyStore(), discoveryIDCounter(),
		func() time.Time { return clock },
	)
	mux := newTestDiscoveryMux(h)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, discoveryRequest(http.MethodPost, "/api/control/v1/accounts/"+accountID+"/discover"))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("discover status = %d, want 202; body = %q", rec.Code, rec.Body.String())
	}
	data := decodeDiscoverResponse(t, rec.Body.Bytes())

	row := waitForJobTerminal(t, jobs, data.JobID, 2*time.Second)
	if row.Status != storage.JobFailed {
		t.Fatalf("job status = %q, want failed — a missing free-set must never read as an empty-catalog success", row.Status)
	}
	if row.Error == nil || row.Error.Code != intelligence.ReasonDiscoveryFailed {
		t.Fatalf("job error = %+v, want the typed %q", row.Error, intelligence.ReasonDiscoveryFailed)
	}

	// And nothing was written to the catalog — the run failed BEFORE any
	// snapshot apply, so no offering (filtered or unfiltered) exists.
	var offerings int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM account_model_offerings WHERE account_id = ?`, accountID).Scan(&offerings); err != nil {
		t.Fatalf("count offerings: %v", err)
	}
	if offerings != 0 {
		t.Fatalf("catalog offerings after a failed run = %d, want 0", offerings)
	}
}

// zenSuccessSeedAccount seeds the opencode-zen provider + a connected
// account + an active stored credential — the shared fixture of the
// failed-discovery test above and the succeeded-discovery test below.
func zenSuccessSeedAccount(t *testing.T, db *storage.DB, accountID string, clock time.Time) (*storage.AccountCredentialRepo, *application.CredentialService) {
	t.Helper()
	if _, err := db.Conn().Exec(
		`INSERT INTO providers (id, display_name, auth_mode, funding_mode, funding_locked, created_at, updated_at) VALUES (?, ?, 'api_key', 'owner_policy', 0, 0, 0)`,
		"opencode-zen", "OpenCode Zen",
	); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if _, err := db.Conn().Exec(
		`INSERT INTO accounts (id, provider_id, external_id, auth_type, connection_state, health_state, created_at, updated_at)
		 VALUES (?, 'opencode-zen', ?, 'api_key', 'connected', 'healthy', 0, 0)`,
		accountID, accountID,
	); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	credRepo := storage.NewAccountCredentialRepo(db)
	credSvc := application.NewCredentialService(credRepo, testKeyring(t), func() time.Time { return clock })
	if _, err := credSvc.Store(context.Background(), application.StoreCredentialParams{
		ID: "cred-zen-ok", AccountID: accountID, ProviderID: "opencode-zen",
		Kind: domain.CredentialKindAPIKey, Active: true, PlaintextKey: "zen-key",
	}); err != nil {
		t.Fatalf("store credential: %v", err)
	}
	return credRepo, credSvc
}

// TestDiscover_ZenSuccessPersistsExplicitOperationsAndLimits is the
// succeeded-discovery counterpart of the ModelsDev failure test above,
// and the end-to-end pin for the "7 models, zero operations" defect: the
// REAL opencode-zen adapter (catalog + models.dev seams faked, everything
// else production code) is driven through POST /discover, and the applied
// snapshot must persist offering_operations rows grounded in the entry's
// explicit facts — chat for every surviving model, tools/vision/
// context_window/reasoning only where declared — plus the declared
// limits, and GET /offerings must render each capability with its
// offering_operation_id (the probeable handle the per-model Test control
// needs).
//
// 2026-08-06: the zen adapter now delegates to the shared
// OperationsFromFacts derivation (internal/providers/modelsdev.go)
// instead of the deleted zen-local zenCapabilities, which had hand-rolled
// a narrower mapping that read only tool_call and image input. Both
// fixture entries below explicitly declare `limit.context` and
// `reasoning:true`, so "context_window" and "reasoning" are now
// catalog-backed facts that must surface — dropping them (the old
// behavior) is exactly the audited defect this task fixes.
//
// Before the adapter reported capabilities, this test failed at the very
// first operations assertion: DiscoverModels returned Capabilities nil,
// so intelligence derived zero Operations and storage created ZERO
// offering_operations rows — nothing probeable, "Test All" a no-op
// (verified red against the pre-change adapter, 2026-08-02).
func TestDiscover_ZenSuccessPersistsExplicitOperationsAndLimits(t *testing.T) {
	clock := fixedDiscoveryClock()
	db := testControlDB(t)
	const accountID = "acct-zen-ok"
	credRepo, credSvc := zenSuccessSeedAccount(t, db, accountID, clock)

	reg := providers.NewRegistry()
	zenModels := func(_ context.Context, _, _ string) ([]byte, error) {
		return []byte(`{"data":[{"id":"tooled-free"},{"id":"seeing-free"},{"id":"paid-x"}]}`), nil
	}
	// The live dataset's shape (2026-08-02): every free model declares
	// tool_call:true and reasoning:true; only one has "image" in
	// modalities.input; limit = {context, output}. reasoning:true and the
	// declared limit.context now DO map to "reasoning" and
	// "context_window" respectively (OperationsFromFacts, shared with
	// every other models.dev-backed adapter) — the exact two operations
	// the pre-fix zen-local mapping silently dropped. paid-x is priced and
	// must not survive.
	modelsDevUp := func(_ context.Context) ([]byte, error) {
		return []byte(`{"opencode":{"models":{
			"tooled-free":{"cost":{"input":0,"output":0},"tool_call":true,"reasoning":true,"limit":{"context":262144,"output":32768}},
			"seeing-free":{"cost":{"input":0,"output":0},"tool_call":true,"reasoning":true,"modalities":{"input":["text","image"],"output":["text"]},"limit":{"context":200000,"output":32000}},
			"paid-x":{"cost":{"input":1.5,"output":3}}
		}}}`), nil
	}
	if err := providers.RegisterOpenCodeZen(reg, nil, zenModels, modelsDevUp, func() time.Time { return clock }); err != nil {
		t.Fatalf("register zen: %v", err)
	}

	jobs := storage.NewJobRepo(db)
	h := NewDiscoveryHandler(
		storage.NewAccountRepo(db), credRepo, storage.NewCatalogRepo(db), jobs,
		storage.NewDiscoveryRepo(db, discoveryIDCounter()), reg, credSvc,
		newAuditEmitter(db, nil), newIdempotencyStore(), discoveryIDCounter(),
		func() time.Time { return clock },
	)
	mux := newTestDiscoveryMux(h)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, discoveryRequest(http.MethodPost, "/api/control/v1/accounts/"+accountID+"/discover"))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("discover status = %d, want 202; body = %q", rec.Code, rec.Body.String())
	}
	data := decodeDiscoverResponse(t, rec.Body.Bytes())
	row := waitForJobTerminal(t, jobs, data.JobID, 2*time.Second)
	if row.Status != storage.JobCompleted {
		t.Fatalf("job status = %q, want completed (error = %+v)", row.Status, row.Error)
	}

	// The defect pin: the applied snapshot created offering_operations
	// rows, exactly the explicitly-grounded set per model and no other.
	// Both fixture entries declare limit.context and reasoning:true, so
	// "context_window" and "reasoning" are now present too (grounded in
	// those explicit fields) — the pre-fix zen-local mapping dropped both.
	// Lists are alphabetical to match the query's ORDER BY operation.
	wantOps := map[string][]string{
		"tooled-free": {"chat", "context_window", "reasoning", "tools"},
		"seeing-free": {"chat", "context_window", "reasoning", "tools", "vision"},
	}
	for modelID, want := range wantOps {
		rows, err := db.Conn().Query(
			`SELECT operation FROM offering_operations WHERE account_id = ? AND provider_model_id = ? ORDER BY operation`,
			accountID, modelID,
		)
		if err != nil {
			t.Fatalf("query offering_operations for %s: %v", modelID, err)
		}
		var got []string
		for rows.Next() {
			var op string
			if err := rows.Scan(&op); err != nil {
				_ = rows.Close()
				t.Fatalf("scan operation: %v", err)
			}
			got = append(got, op)
		}
		_ = rows.Close()
		if len(got) != len(want) {
			t.Fatalf("%s operations = %v, want %v (chat always; tools/vision/context_window/reasoning only when declared)", modelID, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s operations = %v, want %v", modelID, got, want)
			}
		}
	}
	var paidOps int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM offering_operations WHERE account_id = ? AND provider_model_id = 'paid-x'`, accountID).Scan(&paidOps); err != nil {
		t.Fatalf("count paid-x operations: %v", err)
	}
	if paidOps != 0 {
		t.Fatalf("paid-x has %d operations — a paid model must not survive the free-only intersection at all", paidOps)
	}

	// The declared limits round-trip (nil never coerced, values verbatim).
	var ctxLen, maxOut sql.NullInt64
	var maxIn sql.NullInt64
	if err := db.Conn().QueryRow(
		`SELECT context_length, max_input_tokens, max_output_tokens FROM account_model_offerings WHERE account_id = ? AND provider_model_id = 'tooled-free'`,
		accountID,
	).Scan(&ctxLen, &maxIn, &maxOut); err != nil {
		t.Fatalf("query tooled-free limits: %v", err)
	}
	if !ctxLen.Valid || ctxLen.Int64 != 262144 || !maxOut.Valid || maxOut.Int64 != 32768 {
		t.Fatalf("tooled-free limits = (%+v, %+v), want context 262144 / output 32768 from the entry's limit object", ctxLen, maxOut)
	}
	if maxIn.Valid {
		t.Fatalf("tooled-free max_input_tokens = %d, want NULL (the entry declares no limit.input — absent stays unknown)", maxIn.Int64)
	}

	// Layer A: through the REAL composed ControlMux, GET /offerings renders
	// each capability with its offering_operation_id — the probeable handle
	// the dashboard's per-model Test control requires.
	muxA, cookie, _ := p3aOwnerMux(t, db)
	recA := p3aGet(t, muxA, cookie, "/api/control/v1/offerings?account_id="+accountID)
	if recA.Code != http.StatusOK {
		t.Fatalf("GET /offerings status = %d, want 200; body = %q", recA.Code, recA.Body.String())
	}
	list := p3aDecodeOfferings(t, recA.Body.Bytes())
	if len(list) != 2 {
		t.Fatalf("len(offerings) = %d, want 2", len(list))
	}
	for _, modelID := range []string{"tooled-free", "seeing-free"} {
		o := p3aFindOffering(t, list, modelID)
		chatCap := findCapability(t, o.Capabilities, "chat")
		if chatCap.OfferingOperationID == "" {
			t.Fatalf("%s chat capability has no offering_operation_id — nothing would be probeable and the per-model Test control stays disabled", modelID)
		}
		if o.Classification == "no_operations_declared" {
			t.Fatalf("%s classification = no_operations_declared — the exact live defect this change removes", modelID)
		}
	}
}
