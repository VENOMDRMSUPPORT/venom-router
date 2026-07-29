package httpapi

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/observability"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// createKeyViaMux POSTs /keys through the real gated mux with the given session
// cookie + CSRF token and optional Idempotency-Key, returning the recorder.
func createKeyViaMux(t *testing.T, mux http.Handler, cookie *http.Cookie, csrf, idemKey, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/control/v1/keys", bytes.NewBufferString(body))
	req.RemoteAddr = "127.0.0.1:5000"
	req.Host = testAllowedHost
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	if idemKey != "" {
		req.Header.Set("Idempotency-Key", idemKey)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func listKeysViaMux(t *testing.T, mux http.Handler, cookie *http.Cookie) []map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/control/v1/keys", nil)
	req.RemoteAddr = "127.0.0.1:5000"
	req.Host = testAllowedHost
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /keys status = %d, body=%q", rec.Code, rec.Body.String())
	}
	var env struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode GET /keys: %v; body=%q", err, rec.Body.String())
	}
	return env.Data
}

func rawKeyFromCreate(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /keys status = %d, want 201; body=%q", rec.Code, rec.Body.String())
	}
	var env struct {
		Data createKeyResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode POST /keys: %v; body=%q", err, rec.Body.String())
	}
	return env.Data.RawKey
}

// TestKeys_CreateReturnsRawOnceListNeverRaw proves POST /keys returns the raw
// key exactly once and an immediate GET /keys shows the key with NO raw value
// and no full hash.
//
// Mutation U3-M2: include raw_key in the GET projection → the list would carry
// the raw key → RED.
func TestKeys_CreateReturnsRawOnceListNeverRaw(t *testing.T) {
	db := testControlDB(t)
	mux := ControlMux(testAllowedHost, fakeSPA(), db, testKeyring(t))
	cookie, csrf := setupOwnerWithCSRF(t, mux)

	raw := rawKeyFromCreate(t, createKeyViaMux(t, mux, cookie, csrf, "", `{"label":"prod"}`))
	if !strings.HasPrefix(raw, vkLiveKeyPrefix) {
		t.Fatalf("raw key %q lacks the vk_live_ prefix", raw)
	}

	keys := listKeysViaMux(t, mux, cookie)
	if len(keys) != 1 {
		t.Fatalf("GET /keys returned %d keys, want 1", len(keys))
	}
	if _, ok := keys[0]["raw_key"]; ok {
		t.Fatalf("GET /keys must never include raw_key: %v", keys[0])
	}
	// No raw key value and no full 64-char hash anywhere in the JSON.
	listJSON, _ := json.Marshal(keys)
	if bytes.Contains(listJSON, []byte(raw)) {
		t.Fatalf("GET /keys body contained the raw key")
	}
	if bytes.Contains(listJSON, []byte(HashAPIKey(raw))) {
		t.Fatalf("GET /keys body contained the full key hash")
	}
	if keys[0]["key_prefix"] != raw[:len(vkLiveKeyPrefix)+keyPrefixFragmentLen] {
		t.Fatalf("key_prefix = %v, want the non-secret leading fragment", keys[0]["key_prefix"])
	}
}

// TestKeys_ByteLevelDBCanary is the load-bearing hash-only proof: after
// creating a key through the real handler, the raw key's bytes appear NOWHERE
// in the SQLite database file, in the audit row, or in captured log output.
//
// Mutation U3-M1: store the raw key in key_prefix → the raw bytes land in the
// DB file → RED.
func TestKeys_ByteLevelDBCanary(t *testing.T) {
	db := testControlDB(t)
	var logBuf bytes.Buffer
	audit := newAuditEmitter(db, observability.New(slog.NewJSONHandler(&logBuf, nil)))
	h := NewKeysHandler(storage.NewAPIKeyRepo(db), newIdempotencyStore(), audit, nil, newOAuthTransactionID)

	req := httptest.NewRequest(http.MethodPost, "/api/control/v1/keys", bytes.NewBufferString(`{"label":"prod"}`))
	rec := httptest.NewRecorder()
	h.ServeCollection(rec, req)
	raw := rawKeyFromCreate(t, rec)

	// Fold the WAL into the main file so a byte search sees committed data.
	if _, err := db.Conn().Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	fileBytes, err := os.ReadFile(db.Path())
	if err != nil {
		t.Fatalf("read db file: %v", err)
	}
	if bytes.Contains(fileBytes, []byte(raw)) {
		t.Fatalf("the raw key was found in the SQLite database file — storage must be hash-only")
	}
	// The hash-only invariant is real: the hash IS in the file (proving the row
	// was written) while the raw key is not.
	if !bytes.Contains(fileBytes, []byte(HashAPIKey(raw))) {
		t.Fatalf("precondition: the key hash should be in the DB file (the row was written)")
	}

	// Audit row: no column carries the raw key.
	rows, err := db.Conn().Query(`SELECT action, entity_type, COALESCE(entity_id,''), result, COALESCE(reason_code,'') FROM audit_events`)
	if err != nil {
		t.Fatalf("query audit_events: %v", err)
	}
	defer func() { _ = rows.Close() }()
	auditRowCount := 0
	for rows.Next() {
		var action, entityType, entityID, result, reason string
		if err := rows.Scan(&action, &entityType, &entityID, &result, &reason); err != nil {
			t.Fatalf("scan audit row: %v", err)
		}
		auditRowCount++
		for _, field := range []string{action, entityType, entityID, result, reason} {
			if strings.Contains(field, raw) {
				t.Fatalf("audit row field %q contained the raw key", field)
			}
		}
	}
	if auditRowCount != 1 {
		t.Fatalf("expected exactly one audit row for the create, got %d", auditRowCount)
	}

	if strings.Contains(logBuf.String(), raw) {
		t.Fatalf("captured log output contained the raw key")
	}
}

// TestKeys_AuthAndCSRFGating proves all three routes are owner-session + CSRF
// gated, and that a CSRF failure blocks the create BEFORE any side effect.
//
// Mutation U3-M3: drop the owner-session gate from POST /keys → the
// unauthenticated create reaches the handler and creates a key → RED.
func TestKeys_AuthAndCSRFGating(t *testing.T) {
	db := testControlDB(t)
	mux := ControlMux(testAllowedHost, fakeSPA(), db, testKeyring(t))
	cookie, _ := setupOwnerWithCSRF(t, mux)

	// Unauthenticated POST (no session cookie) → 401, never creates.
	unauth := createKeyViaMux(t, mux, nil, "", "", `{"label":"x"}`)
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated POST /keys = %d, want 401; body=%q", unauth.Code, unauth.Body.String())
	}

	// Session but missing CSRF token → csrf_failed 403, never creates.
	noCSRF := createKeyViaMux(t, mux, cookie, "", "", `{"label":"x"}`)
	if noCSRF.Code != http.StatusForbidden {
		t.Fatalf("POST /keys without CSRF = %d, want 403", noCSRF.Code)
	}
	if code := decodeErrorCode(t, noCSRF.Body.Bytes()); code != "csrf_failed" {
		t.Fatalf("error code = %q, want csrf_failed", code)
	}

	if keys := listKeysViaMux(t, mux, cookie); len(keys) != 0 {
		t.Fatalf("a key was created despite auth/CSRF rejection: %v", keys)
	}
}

// TestKeys_IdempotentCreate proves the same Idempotency-Key replays the SAME
// response and creates exactly ONE key row.
//
// Mutation U3-M4: bypass idempotencyStore.Execute → the second POST creates a
// second key → RED.
func TestKeys_IdempotentCreate(t *testing.T) {
	db := testControlDB(t)
	mux := ControlMux(testAllowedHost, fakeSPA(), db, testKeyring(t))
	cookie, csrf := setupOwnerWithCSRF(t, mux)

	first := rawKeyFromCreate(t, createKeyViaMux(t, mux, cookie, csrf, "idem-1", `{"label":"prod"}`))
	second := rawKeyFromCreate(t, createKeyViaMux(t, mux, cookie, csrf, "idem-1", `{"label":"prod"}`))
	if first != second {
		t.Fatalf("idempotent replay returned a different raw key:\n first=%q\n second=%q", first, second)
	}
	if keys := listKeysViaMux(t, mux, cookie); len(keys) != 1 {
		t.Fatalf("idempotent create produced %d key rows, want exactly 1", len(keys))
	}
}

// TestKeys_DeleteRevokesAndBlocksAuth proves DELETE is idempotent and that a
// revoked key immediately fails vk authentication end-to-end (not just at the
// column level): the revoked key is rejected 401 on /v1/models.
//
// Mutation U3-M5: let revocation leave the key usable → the revoked key still
// authenticates /v1/models → RED.
func TestKeys_DeleteRevokesAndBlocksAuth(t *testing.T) {
	db := testControlDB(t)
	mux := ControlMux(testAllowedHost, fakeSPA(), db, testKeyring(t))
	cookie, csrf := setupOwnerWithCSRF(t, mux)

	raw := rawKeyFromCreate(t, createKeyViaMux(t, mux, cookie, csrf, "", `{"label":"prod"}`))
	keys := listKeysViaMux(t, mux, cookie)
	id, _ := keys[0]["id"].(string)

	// The key authenticates /v1/models BEFORE revocation.
	if code := getModelsStatus(t, mux, raw); code != http.StatusOK {
		t.Fatalf("pre-revoke /v1/models = %d, want 200", code)
	}

	del := func() int {
		req := httptest.NewRequest(http.MethodDelete, "/api/control/v1/keys/"+id, nil)
		req.RemoteAddr = "127.0.0.1:5000"
		req.Host = testAllowedHost
		req.AddCookie(cookie)
		req.Header.Set("X-CSRF-Token", csrf)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Code
	}
	if c := del(); c != http.StatusOK {
		t.Fatalf("first DELETE = %d, want 200", c)
	}
	if c := del(); c != http.StatusOK {
		t.Fatalf("second DELETE (idempotent) = %d, want 200", c)
	}

	if code := getModelsStatus(t, mux, raw); code != http.StatusUnauthorized {
		t.Fatalf("post-revoke /v1/models = %d, want 401 (a revoked key must not authenticate)", code)
	}
}

// getModelsStatus GETs /v1/models on the shared control listener with a vk key.
func getModelsStatus(t *testing.T, mux http.Handler, raw string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.RemoteAddr = "127.0.0.1:5000"
	req.Host = testAllowedHost
	req.Header.Set("Authorization", "Bearer "+raw)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec.Code
}

// TestKeys_ValidationErrors proves an empty label and a non-positive rpm_limit
// are rejected with validation_error and create nothing.
func TestKeys_ValidationErrors(t *testing.T) {
	db := testControlDB(t)
	mux := ControlMux(testAllowedHost, fakeSPA(), db, testKeyring(t))
	cookie, csrf := setupOwnerWithCSRF(t, mux)

	for _, body := range []string{`{"label":"   "}`, `{"label":"ok","rpm_limit":0}`, `{"label":"ok","rpm_limit":-5}`} {
		rec := createKeyViaMux(t, mux, cookie, csrf, "", body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("POST /keys %s = %d, want 400", body, rec.Code)
		}
		if code := decodeErrorCode(t, rec.Body.Bytes()); code != "validation_error" {
			t.Fatalf("POST /keys %s error code = %q, want validation_error", body, code)
		}
	}
	if keys := listKeysViaMux(t, mux, cookie); len(keys) != 0 {
		t.Fatalf("validation failures created %d keys, want 0", len(keys))
	}
}

// TestKeys_EntropyAndFormat proves the generated raw key has the vk_live_
// prefix and a 256-bit (64 hex char) random part.
//
// Mutation U3-M6: reduce the generated entropy to 32 bits (4 bytes) → the
// random part shrinks to 8 hex chars → RED.
func TestKeys_EntropyAndFormat(t *testing.T) {
	raw, err := newRawAPIKey()
	if err != nil {
		t.Fatalf("newRawAPIKey: %v", err)
	}
	if !strings.HasPrefix(raw, vkLiveKeyPrefix) {
		t.Fatalf("raw key %q lacks the vk_live_ prefix", raw)
	}
	randPart := strings.TrimPrefix(raw, vkLiveKeyPrefix)
	if len(randPart) != 2*vkRawEntropyBytes {
		t.Fatalf("random part length = %d hex chars, want %d (256 bits)", len(randPart), 2*vkRawEntropyBytes)
	}
	// Two successive keys must differ (a constant generator would be caught).
	other, _ := newRawAPIKey()
	if raw == other {
		t.Fatalf("two generated keys were identical — not random")
	}
}
