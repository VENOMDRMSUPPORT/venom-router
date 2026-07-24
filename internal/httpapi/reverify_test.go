package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/secrets"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// newTestAuthHandlersWithDB is newTestAuthHandlers but also returns the
// underlying *storage.DB, for tests that need to query row counts
// directly rather than through a repository method.
func newTestAuthHandlersWithDB(t *testing.T, clock *time.Time) (*AuthHandlers, *storage.DB) {
	t.Helper()
	db := testControlDB(t)
	h := NewAuthHandlers(db)
	h.now = func() time.Time { return *clock }
	return h, db
}

func doReverify(t *testing.T, h *AuthHandlers, cookie *http.Cookie, csrfToken, password string) *httptest.ResponseRecorder {
	t.Helper()
	req := newAuthRequest(t, http.MethodPost, "/api/control/v1/auth/reverify", setupRequestBody(password))
	req.AddCookie(cookie)
	if csrfToken != "" {
		req.Header.Set("X-CSRF-Token", csrfToken)
	}
	rec := httptest.NewRecorder()
	h.ServeReverify(rec, req)
	return rec
}

func TestReverify_StampsExactlyFiveMinutes(t *testing.T) {
	clock := fixedClockAt2026()
	h, _ := newTestAuthHandlersWithDB(t, &clock)

	setupRec := setupOwnerDirect(t, h, testSetupPassword)
	cookie := sessionCookieFrom(t, setupRec)
	var setupBody struct {
		CSRFToken string `json:"csrf_token"`
	}
	decodeJSON(t, setupRec.Body.Bytes(), &setupBody)

	clock = clock.Add(2 * time.Minute)
	rec := doReverify(t, h, cookie, setupBody.CSRFToken, testSetupPassword)
	if rec.Code != http.StatusOK {
		t.Fatalf("reverify status = %d, want 200; body = %q", rec.Code, rec.Body.String())
	}

	tokenHash := secrets.HashSessionHandle(cookie.Value)
	row, ok, err := h.ownerSessions.GetByTokenHash(context.Background(), tokenHash)
	if err != nil || !ok {
		t.Fatalf("GetByTokenHash: ok=%v err=%v", ok, err)
	}
	want := clock.Add(5 * time.Minute)
	if row.ReverifyFreshUntil == nil || !row.ReverifyFreshUntil.Equal(want) {
		t.Fatalf("ReverifyFreshUntil = %v, want exactly %v", row.ReverifyFreshUntil, want)
	}
}

func TestReverify_WrongPasswordRejectedNoStamp(t *testing.T) {
	clock := fixedClockAt2026()
	h, _ := newTestAuthHandlersWithDB(t, &clock)

	setupRec := setupOwnerDirect(t, h, testSetupPassword)
	cookie := sessionCookieFrom(t, setupRec)
	var setupBody struct {
		CSRFToken string `json:"csrf_token"`
	}
	decodeJSON(t, setupRec.Body.Bytes(), &setupBody)

	rec := doReverify(t, h, cookie, setupBody.CSRFToken, "totally-the-wrong-password")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for wrong password; body = %q", rec.Code, rec.Body.String())
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != "invalid_credentials" {
		t.Fatalf("error code = %q, want invalid_credentials", code)
	}

	tokenHash := secrets.HashSessionHandle(cookie.Value)
	row, ok, err := h.ownerSessions.GetByTokenHash(context.Background(), tokenHash)
	if err != nil || !ok {
		t.Fatalf("GetByTokenHash: ok=%v err=%v", ok, err)
	}
	if row.ReverifyFreshUntil != nil {
		t.Fatalf("ReverifyFreshUntil = %v, want nil after a wrong password", row.ReverifyFreshUntil)
	}
}

func TestReverify_MintsNoNewSession(t *testing.T) {
	clock := fixedClockAt2026()
	h, db := newTestAuthHandlersWithDB(t, &clock)

	setupRec := setupOwnerDirect(t, h, testSetupPassword)
	cookie := sessionCookieFrom(t, setupRec)
	var setupBody struct {
		CSRFToken string `json:"csrf_token"`
	}
	decodeJSON(t, setupRec.Body.Bytes(), &setupBody)

	beforeCount := countRows(t, db, "owner_sessions")

	rec := doReverify(t, h, cookie, setupBody.CSRFToken, testSetupPassword)
	if rec.Code != http.StatusOK {
		t.Fatalf("reverify status = %d, want 200; body = %q", rec.Code, rec.Body.String())
	}

	afterCount := countRows(t, db, "owner_sessions")
	if afterCount != beforeCount {
		t.Fatalf("owner_sessions row count changed: before=%d after=%d, want unchanged (reverify mints no new session)", beforeCount, afterCount)
	}

	// The response must not set a NEW session cookie value (no new session).
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName {
			t.Fatalf("reverify response unexpectedly set a session cookie (it must never mint a new session)")
		}
	}
}

func TestReverify_RequiresValidSession(t *testing.T) {
	clock := fixedClockAt2026()
	h, _ := newTestAuthHandlersWithDB(t, &clock)
	_ = setupOwnerDirect(t, h, testSetupPassword)

	req := newAuthRequest(t, http.MethodPost, "/api/control/v1/auth/reverify", setupRequestBody(testSetupPassword))
	rec := httptest.NewRecorder()
	h.ServeReverify(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 without a session cookie; body = %q", rec.Code, rec.Body.String())
	}
}

func TestReverify_RequiresCSRF(t *testing.T) {
	clock := fixedClockAt2026()
	h, _ := newTestAuthHandlersWithDB(t, &clock)
	setupRec := setupOwnerDirect(t, h, testSetupPassword)
	cookie := sessionCookieFrom(t, setupRec)

	rec := doReverify(t, h, cookie, "", testSetupPassword)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 without a CSRF token; body = %q", rec.Code, rec.Body.String())
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != "csrf_failed" {
		t.Fatalf("error code = %q, want csrf_failed", code)
	}
}

func TestIsReverifyFresh_BoundaryIsStrictlyBeforeFiveMinutes(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	freshUntil := base.Add(5 * time.Minute)
	row := storage.OwnerSessionRow{ReverifyFreshUntil: &freshUntil}

	if !IsReverifyFresh(row, base.Add(4*time.Minute+59*time.Second)) {
		t.Fatalf("IsReverifyFresh at 4m59s = false, want true")
	}
	if IsReverifyFresh(row, freshUntil) {
		t.Fatalf("IsReverifyFresh AT exactly 5m = true, want false (strict boundary)")
	}
	if IsReverifyFresh(row, base.Add(5*time.Minute+time.Second)) {
		t.Fatalf("IsReverifyFresh at 5m1s = true, want false")
	}

	neverRow := storage.OwnerSessionRow{}
	if IsReverifyFresh(neverRow, base) {
		t.Fatalf("IsReverifyFresh for a never-reverified session = true, want false")
	}
}

func countRows(t *testing.T, db *storage.DB, table string) int {
	t.Helper()
	var n int
	if err := db.Conn().QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		t.Fatalf("count %s rows: %v", table, err)
	}
	return n
}
