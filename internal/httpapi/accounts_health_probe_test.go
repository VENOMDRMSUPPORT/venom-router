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
