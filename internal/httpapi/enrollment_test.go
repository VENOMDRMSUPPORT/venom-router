package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/application"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// --- A deterministic, in-memory fake APIKeyAdapter (no real network
// call, no real provider) — the only kind of adapter these tests use. ---

type fakeAPIKeyAdapter struct {
	validKey string
	identity providers.IdentityResult
	creds    providers.StoredCredentials
	fixedErr error // when non-nil, ConnectAPIKey always returns this regardless of key
	calls    int
}

func (f *fakeAPIKeyAdapter) ConnectAPIKey(_ context.Context, key string) (providers.IdentityResult, providers.StoredCredentials, error) {
	f.calls++
	if f.fixedErr != nil {
		return providers.IdentityResult{}, providers.StoredCredentials{}, f.fixedErr
	}
	if key != f.validKey {
		return providers.IdentityResult{}, providers.StoredCredentials{}, providers.ErrInvalidCredential
	}
	return f.identity, f.creds, nil
}

func newFakeAPIKeyAdapter() *fakeAPIKeyAdapter {
	return &fakeAPIKeyAdapter{
		validKey: "good-key",
		identity: providers.IdentityResult{ExternalID: "fake-ext-1", Plan: "Free"},
		creds:    providers.StoredCredentials{Value: "good-key"},
	}
}

func enrollmentIDGenerator(prefix string) application.IDGenerator {
	n := 0
	return func() string {
		n++
		return prefix + "-" + strconv.Itoa(n)
	}
}

// newTestEnrollmentHandler builds an EnrollmentHandler wired over a
// fresh migrated DB, with adapter registered under providerID in a
// fresh, test-local Registry — deliberately NOT ControlMux's own
// (opencode-zen-only, real-network) registry, mirroring
// oauth_test.go's newTestOAuthHandler.
func newTestEnrollmentHandler(t *testing.T, providerID string, adapter providers.APIKeyAdapter) (*EnrollmentHandler, *storage.DB) {
	t.Helper()
	db := testControlDB(t)
	if _, err := db.Conn().Exec(
		`INSERT INTO providers (id, display_name, auth_mode, funding_mode, created_at, updated_at) VALUES (?, ?, 'api_key', 'owner_policy', 0, 0)`,
		providerID, providerID,
	); err != nil {
		t.Fatalf("seed provider: %v", err)
	}

	reg := providers.NewRegistry()
	if err := reg.Register(providers.Definition{ID: providers.ProviderID(providerID), AuthMode: providers.AuthModeAPIKey, Transport: providers.TransportKindOpenAICompatible, APIKey: adapter}); err != nil {
		t.Fatalf("register fake adapter: %v", err)
	}

	accountRepo := storage.NewAccountRepo(db)
	connectSvc := application.NewConnectService(storage.NewEnrollmentRepo(db), accountRepo, testKeyring(t), enrollmentIDGenerator("id"), nil)
	fundingRepo := storage.NewFundingEvidenceRepo(db)
	idem := newIdempotencyStore()
	audit := newAuditEmitter(db, nil)
	return NewEnrollmentHandler(connectSvc, reg, fundingRepo, accountRepo, idem, audit), db
}

func newTestEnrollmentMux(h *EnrollmentHandler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/control/v1/providers/{id}/accounts", h.ServeConnect)
	return mux
}

func connectRequest(providerID, apiKey, funding string) *http.Request {
	body := map[string]any{"api_key": apiKey}
	if funding != "" {
		body["funding"] = funding
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/control/v1/providers/"+providerID+"/accounts", bytes.NewReader(b))
	// Harmless for the bare-mux unit tests below; required for the
	// ControlMux-based gating tests further down, which sit behind the
	// loopback + Host-allowlist network gate.
	req.RemoteAddr = "127.0.0.1:54321"
	req.Host = testAllowedHost
	return req
}

// --- Success ---

func TestEnrollment_ValidKey_Creates201AccountNoKeyLeak(t *testing.T) {
	const canaryKey = "good-key-CANARY-3fQ7mZ0kR9xVb1Nc6Ea-secret"
	adapter := newFakeAPIKeyAdapter()
	adapter.validKey = canaryKey
	adapter.creds = providers.StoredCredentials{Value: canaryKey}

	h, db := newTestEnrollmentHandler(t, "fake-provider", adapter)
	mux := newTestEnrollmentMux(h)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, connectRequest("fake-provider", canaryKey, ""))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %q", rec.Code, rec.Body.String())
	}
	assertNoFragment(t, rec.Body.String(), canaryKey, "enrollment success response body")

	var body struct {
		Data struct {
			ID              string `json:"id"`
			ProviderID      string `json:"provider"`
			ExternalID      string `json:"external_id"`
			ConnectionState string `json:"connection_state"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v; body = %q", err, rec.Body.String())
	}
	if body.Data.ID == "" || body.Data.ProviderID != "fake-provider" || body.Data.ExternalID != "fake-ext-1" {
		t.Fatalf("unexpected account projection: %+v", body.Data)
	}
	if body.Data.ConnectionState != "connected" {
		t.Fatalf("connection_state = %q, want connected", body.Data.ConnectionState)
	}

	if n := countRows(t, db, "accounts"); n != 1 {
		t.Fatalf("accounts row count = %d, want 1", n)
	}

	// Canary: the raw key must never land in any DB column either.
	rows, err := db.Conn().Query(`SELECT ciphertext, nonce, key_id, fingerprint_sha256 FROM account_credentials`)
	if err != nil {
		t.Fatalf("query account_credentials: %v", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var ciphertext, nonce, keyID []byte
		var fingerprint string
		if err := rows.Scan(&ciphertext, &nonce, &keyID, &fingerprint); err != nil {
			t.Fatalf("scan account_credentials row: %v", err)
		}
		assertNoFragment(t, string(ciphertext), canaryKey, "account_credentials.ciphertext")
		assertNoFragment(t, fingerprint, canaryKey, "account_credentials.fingerprint_sha256")
	}
}

// --- Invalid credential ---

func TestEnrollment_InvalidKey_TypedErrorZeroRows(t *testing.T) {
	adapter := newFakeAPIKeyAdapter()
	h, db := newTestEnrollmentHandler(t, "fake-provider", adapter)
	mux := newTestEnrollmentMux(h)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, connectRequest("fake-provider", "totally-wrong-key", ""))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body = %q", rec.Code, rec.Body.String())
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != "invalid_credential" {
		t.Fatalf("error code = %q, want invalid_credential", code)
	}
	if n := countRows(t, db, "accounts"); n != 0 {
		t.Fatalf("accounts row count = %d, want 0", n)
	}
}

// --- Provider unavailable ---

func TestEnrollment_ProviderUnavailable_502RetryableZeroRows(t *testing.T) {
	adapter := newFakeAPIKeyAdapter()
	adapter.fixedErr = providers.ErrProviderUnavailable
	h, db := newTestEnrollmentHandler(t, "fake-provider", adapter)
	mux := newTestEnrollmentMux(h)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, connectRequest("fake-provider", "good-key", ""))

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body = %q", rec.Code, rec.Body.String())
	}
	var body struct {
		Error struct {
			Code      string `json:"code"`
			Retryable bool   `json:"retryable"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Error.Code != "provider_unavailable" || !body.Error.Retryable {
		t.Fatalf("error = %+v, want code=provider_unavailable retryable=true", body.Error)
	}
	if n := countRows(t, db, "accounts"); n != 0 {
		t.Fatalf("accounts row count = %d, want 0", n)
	}
}

// --- Duplicate identity ---

func TestEnrollment_DuplicateIdentity_AccountAlreadyConnected(t *testing.T) {
	adapter := newFakeAPIKeyAdapter()
	h, db := newTestEnrollmentHandler(t, "fake-provider", adapter)
	mux := newTestEnrollmentMux(h)

	rec1 := httptest.NewRecorder()
	mux.ServeHTTP(rec1, connectRequest("fake-provider", "good-key", ""))
	if rec1.Code != http.StatusCreated {
		t.Fatalf("first connect status = %d, want 201; body = %q", rec1.Code, rec1.Body.String())
	}

	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, connectRequest("fake-provider", "good-key", ""))
	if rec2.Code != http.StatusConflict {
		t.Fatalf("second connect status = %d, want 409; body = %q", rec2.Code, rec2.Body.String())
	}
	if code := decodeErrorCode(t, rec2.Body.Bytes()); code != "account_already_connected" {
		t.Fatalf("error code = %q, want account_already_connected", code)
	}
	if n := countRows(t, db, "accounts"); n != 1 {
		t.Fatalf("accounts row count = %d, want 1 (no second row from the duplicate attempt)", n)
	}
}

// --- Idempotency-Key replay ---

func TestEnrollment_IdempotencyKeyReplay_ServiceCalledOnce(t *testing.T) {
	adapter := newFakeAPIKeyAdapter()
	h, _ := newTestEnrollmentHandler(t, "fake-provider", adapter)
	mux := newTestEnrollmentMux(h)

	req1 := connectRequest("fake-provider", "good-key", "")
	req1.Header.Set("Idempotency-Key", "replay-key-1")
	rec1 := httptest.NewRecorder()
	mux.ServeHTTP(rec1, req1)

	req2 := connectRequest("fake-provider", "good-key", "")
	req2.Header.Set("Idempotency-Key", "replay-key-1")
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)

	if adapter.calls != 1 {
		t.Fatalf("adapter.ConnectAPIKey called %d times, want exactly 1 (replay must not re-run it)", adapter.calls)
	}
	if rec1.Code != rec2.Code || rec1.Body.String() != rec2.Body.String() {
		t.Fatalf("replay response differs from original: first={%d %q} second={%d %q}",
			rec1.Code, rec1.Body.String(), rec2.Code, rec2.Body.String())
	}
}

// --- Unknown provider ---

func TestEnrollment_UnknownProvider_404(t *testing.T) {
	adapter := newFakeAPIKeyAdapter()
	h, _ := newTestEnrollmentHandler(t, "fake-provider", adapter)
	mux := newTestEnrollmentMux(h)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, connectRequest("does-not-exist", "good-key", ""))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %q", rec.Code, rec.Body.String())
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != "not_found" {
		t.Fatalf("error code = %q, want not_found", code)
	}
}

// --- Gating: POST .../providers/{id}/accounts is owner-session + CSRF gated ---

// TestControlMux_Enrollment_UnauthenticatedRejected proves the real
// ControlMux composition (opencode-zen registered, real seams — but
// unreachable in a test process, so the request never gets that far)
// rejects an unauthenticated enrollment attempt with 401 before the
// handler ever runs.
func TestControlMux_Enrollment_UnauthenticatedRejected(t *testing.T) {
	mux := ControlMux(testAllowedHost, fakeSPA(), testControlDB(t), testKeyring(t))

	req := newAuthRequest(t, http.MethodPost, "/api/control/v1/providers/opencode-zen/accounts", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 without any session", rec.Code)
	}
}

// TestControlMux_Enrollment_SessionWithoutCSRFRejected proves a mutating
// enrollment call with a valid session but no CSRF token is rejected 403
// before the handler runs.
func TestControlMux_Enrollment_SessionWithoutCSRFRejected(t *testing.T) {
	mux := ControlMux(testAllowedHost, fakeSPA(), testControlDB(t), testKeyring(t))
	cookie, _ := setupOwnerWithCSRF(t, mux)

	req := newAuthRequest(t, http.MethodPost, "/api/control/v1/providers/opencode-zen/accounts", nil)
	req.AddCookie(cookie) // no X-CSRF-Token
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for a mutation with no CSRF token", rec.Code)
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != "csrf_failed" {
		t.Fatalf("error code = %q, want csrf_failed", code)
	}
}

// TestControlMux_Enrollment_UnknownProviderReachesHandler proves a fully
// authenticated + CSRF'd enrollment call passes the gate and reaches
// EnrollmentHandler.serveConnect (which then 404s for an unregistered
// provider id — that 404, rather than a 401/403, is exactly the proof
// the gate let it through).
func TestControlMux_Enrollment_UnknownProviderReachesHandler(t *testing.T) {
	mux := ControlMux(testAllowedHost, fakeSPA(), testControlDB(t), testKeyring(t))
	cookie, csrfToken := setupOwnerWithCSRF(t, mux)

	req := connectRequest("does-not-exist", "irrelevant-key", "")
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrfToken)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
		t.Fatalf("status = %d, want the gate to pass (not 401/403) once session+CSRF are valid; body = %q", rec.Code, rec.Body.String())
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 not_found from EnrollmentHandler.serveConnect; body = %q", rec.Code, rec.Body.String())
	}
}

// TestControlMux_Enrollment_EmitsAuditEventOnFailure proves the shared
// P2b-OBS-001 emitter records exactly one audit_event for an enrollment
// attempt reaching the real ControlMux composition.
func TestControlMux_Enrollment_EmitsAuditEventOnFailure(t *testing.T) {
	db := testControlDB(t)
	mux := ControlMux(testAllowedHost, fakeSPA(), db, testKeyring(t))
	cookie, csrfToken := setupOwnerWithCSRF(t, mux)

	req := connectRequest("does-not-exist", "irrelevant-key", "")
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrfToken)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %q", rec.Code, rec.Body.String())
	}

	var count int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action = ?`, AuditActionAccountConnect).Scan(&count); err != nil {
		t.Fatalf("count audit_events: %v", err)
	}
	if count != 1 {
		t.Fatalf("audit_events rows for action %q = %d, want exactly 1", AuditActionAccountConnect, count)
	}
}
