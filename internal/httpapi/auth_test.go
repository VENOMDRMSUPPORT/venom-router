package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/secrets"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

const testSetupPassword = "correct-horse-battery-staple-9"

func setupRequestBody(password string) *bytes.Buffer {
	b, _ := json.Marshal(map[string]string{"password": password})
	return bytes.NewBuffer(b)
}

func newAuthRequest(t *testing.T, method, path string, body *bytes.Buffer) *http.Request {
	t.Helper()
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, path, body)
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.RemoteAddr = "127.0.0.1:54321"
	req.Host = testAllowedHost
	return req
}

func TestAuthStatus_FalseBeforeSetup(t *testing.T) {
	mux := ControlMux(testAllowedHost, fakeSPA(), testControlDB(t), testKeyring(t))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, newAuthRequest(t, http.MethodGet, "/api/control/v1/auth/status", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /auth/status status = %d, want %d; body = %q", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got struct {
		Data struct {
			SetupComplete bool `json:"setup_complete"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v; body = %q", err, rec.Body.String())
	}
	if got.Data.SetupComplete {
		t.Fatalf("setup_complete = true before any setup, want false")
	}
}

func TestAuthSetup_SucceedsOnceAndStatusFlipsToTrue(t *testing.T) {
	db := testControlDB(t)
	mux := ControlMux(testAllowedHost, fakeSPA(), db, testKeyring(t))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, newAuthRequest(t, http.MethodPost, "/api/control/v1/auth/setup", setupRequestBody(testSetupPassword)))

	if rec.Code != http.StatusOK {
		t.Fatalf("POST /auth/setup status = %d, want %d; body = %q", rec.Code, http.StatusOK, rec.Body.String())
	}

	// Exactly one owner_auth row.
	assertOwnerAuthRowCount(t, db, 1)
	// Exactly one owner_sessions row.
	assertOwnerSessionsRowCount(t, db, 1)

	// The session cookie is set with the documented flags.
	resp := rec.Result()
	cookies := resp.Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == sessionCookieName {
			sessionCookie = c
		}
	}
	if sessionCookie == nil {
		t.Fatalf("no %s cookie set on successful setup; Set-Cookie headers = %v", sessionCookieName, resp.Header.Values("Set-Cookie"))
	}
	if sessionCookie.Value == "" {
		t.Fatalf("session cookie value is empty")
	}
	if !sessionCookie.HttpOnly {
		t.Fatalf("session cookie HttpOnly = false, want true")
	}
	if sessionCookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("session cookie SameSite = %v, want Strict", sessionCookie.SameSite)
	}
	if sessionCookie.Path != controlAPIPath {
		t.Fatalf("session cookie Path = %q, want %q", sessionCookie.Path, controlAPIPath)
	}
	if sessionCookie.Secure {
		t.Fatalf("session cookie Secure = true on a plain-HTTP test request, want false (Secure only when transport security permits)")
	}

	// GET /auth/status now reports true.
	statusRec := httptest.NewRecorder()
	mux.ServeHTTP(statusRec, newAuthRequest(t, http.MethodGet, "/api/control/v1/auth/status", nil))
	var status struct {
		Data struct {
			SetupComplete bool `json:"setup_complete"`
		} `json:"data"`
	}
	if err := json.Unmarshal(statusRec.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode status response: %v", err)
	}
	if !status.Data.SetupComplete {
		t.Fatalf("setup_complete = false after a successful setup, want true")
	}
}

func TestAuthSetup_SecondAttemptRejected(t *testing.T) {
	db := testControlDB(t)
	mux := ControlMux(testAllowedHost, fakeSPA(), db, testKeyring(t))

	first := httptest.NewRecorder()
	mux.ServeHTTP(first, newAuthRequest(t, http.MethodPost, "/api/control/v1/auth/setup", setupRequestBody(testSetupPassword)))
	if first.Code != http.StatusOK {
		t.Fatalf("first setup status = %d, want %d; body = %q", first.Code, http.StatusOK, first.Body.String())
	}

	second := httptest.NewRecorder()
	mux.ServeHTTP(second, newAuthRequest(t, http.MethodPost, "/api/control/v1/auth/setup", setupRequestBody("a-different-long-enough-password")))
	if second.Code != http.StatusConflict {
		t.Fatalf("second setup status = %d, want %d; body = %q", second.Code, http.StatusConflict, second.Body.String())
	}
	var errBody struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(second.Body.Bytes(), &errBody); err != nil {
		t.Fatalf("decode error response: %v; body = %q", err, second.Body.String())
	}
	if errBody.Error.Code != "setup_already_complete" {
		t.Fatalf("error code = %q, want %q", errBody.Error.Code, "setup_already_complete")
	}

	// The invariant: still exactly one owner_auth row (the second attempt
	// did not overwrite or add another).
	assertOwnerAuthRowCount(t, db, 1)
	assertOwnerSessionsRowCount(t, db, 1)
}

func TestAuthSetup_RejectsTooShortPassword(t *testing.T) {
	mux := ControlMux(testAllowedHost, fakeSPA(), testControlDB(t), testKeyring(t))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, newAuthRequest(t, http.MethodPost, "/api/control/v1/auth/setup", setupRequestBody("short")))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d for a too-short password; body = %q", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

// TestAuthSetup_StoredHashMatchesDocumentedParamsAndVerifies proves the
// stored row carries the exact Argon2id KDF params and that the derived
// hash verifies the real password while rejecting a wrong one.
func TestAuthSetup_StoredHashMatchesDocumentedParamsAndVerifies(t *testing.T) {
	db := testControlDB(t)
	mux := ControlMux(testAllowedHost, fakeSPA(), db, testKeyring(t))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, newAuthRequest(t, http.MethodPost, "/api/control/v1/auth/setup", setupRequestBody(testSetupPassword)))
	if rec.Code != http.StatusOK {
		t.Fatalf("setup status = %d, want %d", rec.Code, http.StatusOK)
	}

	row, ok, err := storage.NewOwnerAuthRepo(db).Get(context.Background())
	if err != nil || !ok {
		t.Fatalf("Get() ok=%v err=%v after setup", ok, err)
	}
	if row.KDFTime != 3 || row.KDFMemKiB != 65536 || row.KDFThreads != 4 || row.KDFKeyLen != 32 {
		t.Fatalf("stored KDF params = %+v, want time=3 mem_kib=65536 threads=4 key_len=32", row)
	}

	stored := ownerPasswordHashFromRow(row)
	if !secrets.VerifyOwnerPassword(testSetupPassword, stored) {
		t.Fatalf("stored hash did not verify the real setup password")
	}
	if secrets.VerifyOwnerPassword("definitely-the-wrong-password", stored) {
		t.Fatalf("stored hash verified a wrong password, want rejection")
	}
}

// TestAuthCanary_SetupPasswordNeverLeaks pushes a distinctive canary
// password through the real POST /auth/setup path and asserts no
// fragment of it appears in: the HTTP response body, any response
// header (including Set-Cookie), or the stored owner_auth row's
// bytes (only the derived hash/salt should be present, which — being a
// one-way Argon2id output over a 16-byte random salt — is vanishingly
// unlikely to coincidentally contain a substring of the input password;
// asserting it here still proves no code path echoes the password
// verbatim into storage).
func TestAuthCanary_SetupPasswordNeverLeaks(t *testing.T) {
	const canaryPassword = "CANARY-SECRET-9f3Kx2Qw8pLm0Zt7Vb4Nr1Hy6Dc5Ea-longenough"

	db := testControlDB(t)
	mux := ControlMux(testAllowedHost, fakeSPA(), db, testKeyring(t))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, newAuthRequest(t, http.MethodPost, "/api/control/v1/auth/setup", setupRequestBody(canaryPassword)))
	if rec.Code != http.StatusOK {
		t.Fatalf("setup status = %d, want %d", rec.Code, http.StatusOK)
	}

	assertNoFragment(t, rec.Body.String(), canaryPassword, "response body")
	for key, values := range rec.Result().Header {
		for _, v := range values {
			assertNoFragment(t, v, canaryPassword, "response header "+key)
		}
	}

	row, ok, err := storage.NewOwnerAuthRepo(db).Get(context.Background())
	if err != nil || !ok {
		t.Fatalf("Get() ok=%v err=%v after setup", ok, err)
	}
	assertNoFragment(t, string(row.PasswordHash), canaryPassword, "stored password_hash")
	assertNoFragment(t, string(row.Salt), canaryPassword, "stored salt")

	// A too-short rejection must not echo the (short, distinct) password
	// it rejected either.
	const shortCanary = "SHORT9f3K"
	rejectRec := httptest.NewRecorder()
	mux2 := ControlMux(testAllowedHost, fakeSPA(), testControlDB(t), testKeyring(t))
	mux2.ServeHTTP(rejectRec, newAuthRequest(t, http.MethodPost, "/api/control/v1/auth/setup", setupRequestBody(shortCanary)))
	assertNoFragment(t, rejectRec.Body.String(), shortCanary, "validation-error response body")
}

// minFragmentWindow is the smallest secret substring length these
// canary helpers treat as a meaningful leak — the same rigor
// internal/secrets' own canary suite uses (secrets_test.
// findSecretFragment), reimplemented here since it is a private test
// helper in a different package.
const minFragmentWindow = 4

// findFragment reports the first window (>= minFragmentWindow chars)
// of secret that appears anywhere in output. It is pure (no *testing.T)
// so a meta-test can assert its return value directly, proving the
// detector actually detects a leak rather than merely never firing.
func findFragment(output, secret string) (fragment string, found bool) {
	for start := 0; start+minFragmentWindow <= len(secret); start++ {
		for end := start + minFragmentWindow; end <= len(secret); end++ {
			frag := secret[start:end]
			if strings.Contains(output, frag) {
				return frag, true
			}
		}
	}
	return "", false
}

// assertNoFragment fails the test if any window (>= 4 chars) of secret
// appears anywhere in output.
func assertNoFragment(t *testing.T, output, secret, where string) {
	t.Helper()
	if frag, found := findFragment(output, secret); found {
		t.Fatalf("%s leaked secret fragment %q", where, frag)
	}
}

func assertOwnerAuthRowCount(t *testing.T, db *storage.DB, want int) {
	t.Helper()
	var got int
	if err := db.Conn().QueryRow("SELECT COUNT(*) FROM owner_auth").Scan(&got); err != nil {
		t.Fatalf("count owner_auth rows: %v", err)
	}
	if got != want {
		t.Fatalf("owner_auth row count = %d, want %d", got, want)
	}
}

func assertOwnerSessionsRowCount(t *testing.T, db *storage.DB, want int) {
	t.Helper()
	var got int
	if err := db.Conn().QueryRow("SELECT COUNT(*) FROM owner_sessions").Scan(&got); err != nil {
		t.Fatalf("count owner_sessions rows: %v", err)
	}
	if got != want {
		t.Fatalf("owner_sessions row count = %d, want %d", got, want)
	}
}
