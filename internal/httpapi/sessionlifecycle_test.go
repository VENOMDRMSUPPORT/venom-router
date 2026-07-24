package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/secrets"
)

// newTestAuthHandlers builds an AuthHandlers directly (bypassing
// ControlMux/the network gate, which SEC-003's session-lifecycle logic
// has nothing to do with) with an injectable, steppable clock so
// idle/absolute expiry and renewal are provable to the second.
func newTestAuthHandlers(t *testing.T, clock *time.Time) *AuthHandlers {
	t.Helper()
	h := NewAuthHandlers(testControlDB(t))
	h.now = func() time.Time { return *clock }
	return h
}

// setupOwnerDirect drives ServeSetup directly against h (not through
// ControlMux) at the clock's current time, returning the session cookie.
func setupOwnerDirect(t *testing.T, h *AuthHandlers, password string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := newAuthRequest(t, http.MethodPost, "/api/control/v1/auth/setup", setupRequestBody(password))
	h.ServeSetup(rec, req)
	if rec.Code != 200 {
		t.Fatalf("ServeSetup: status = %d, body = %q", rec.Code, rec.Body.String())
	}
	return rec
}

func sessionCookieFrom(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName {
			return c
		}
	}
	t.Fatalf("no session cookie in response")
	return nil
}

func TestSessionLifecycle_IdleExpiry_RejectsAndRevokes(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := t0
	h := newTestAuthHandlers(t, &clock)

	cookie := sessionCookieFrom(t, setupOwnerDirect(t, h, testSetupPassword))

	// Advance past idle_expires_at (t0 + 30m) by 1 second.
	clock = t0.Add(30*time.Minute + time.Second)

	req := newAuthRequest(t, http.MethodGet, "/api/control/v1/auth/session", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeSession(rec, req)

	if rec.Code != 401 {
		t.Fatalf("status = %d, want 401 for idle-expired session; body = %q", rec.Code, rec.Body.String())
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != "session_expired" {
		t.Fatalf("error code = %q, want session_expired", code)
	}

	tokenHash := secrets.HashSessionHandle(cookie.Value)
	row, ok, err := h.ownerSessions.GetByTokenHash(req.Context(), tokenHash)
	if err != nil || !ok {
		t.Fatalf("GetByTokenHash after idle expiry: ok=%v err=%v", ok, err)
	}
	if row.RevokedAt == nil {
		t.Fatalf("idle-expired session was not revoked as a side effect")
	}
}

func TestSessionLifecycle_AbsoluteExpiry_RejectsEvenWithRecentActivity(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := t0
	h := newTestAuthHandlers(t, &clock)

	cookie := sessionCookieFrom(t, setupOwnerDirect(t, h, testSetupPassword))

	// Renew repeatedly, each time well within the 30-minute idle window,
	// until just past the 12h absolute cap.
	step := 20 * time.Minute
	for elapsed := step; elapsed < 12*time.Hour; elapsed += step {
		clock = t0.Add(elapsed)
		req := newAuthRequest(t, http.MethodGet, "/api/control/v1/auth/session", nil)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		h.ServeSession(rec, req)
		if rec.Code != 200 {
			t.Fatalf("renewal at elapsed=%v: status = %d, want 200 (still within idle+absolute); body=%q", elapsed, rec.Code, rec.Body.String())
		}
	}

	// Now past the absolute cap (12h + 1s from t0) — must reject even
	// though every prior request kept idle activity fresh.
	clock = t0.Add(12*time.Hour + time.Second)
	req := newAuthRequest(t, http.MethodGet, "/api/control/v1/auth/session", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeSession(rec, req)
	if rec.Code != 401 {
		t.Fatalf("status = %d, want 401 past absolute expiry despite recent activity; body = %q", rec.Code, rec.Body.String())
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != "session_expired" {
		t.Fatalf("error code = %q, want session_expired", code)
	}
}

func TestSessionLifecycle_IdleNeverExtendsPastAbsolute(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := t0
	h := newTestAuthHandlers(t, &clock)

	cookie := sessionCookieFrom(t, setupOwnerDirect(t, h, testSetupPassword))

	// Keep the session alive with renewals every 20 minutes (well within
	// the 30-minute idle window) up to 1 minute before the 12h absolute
	// cap. At that last renewal, idle_expires_at would naturally compute
	// to now+30m — which lands PAST absolute_expires_at (t0+12h) — so it
	// must be clamped to exactly absolute, never extended beyond it.
	step := 20 * time.Minute
	lastElapsed := 12*time.Hour - time.Minute
	for elapsed := step; elapsed <= lastElapsed; elapsed += step {
		clock = t0.Add(elapsed)
		req := newAuthRequest(t, http.MethodGet, "/api/control/v1/auth/session", nil)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		h.ServeSession(rec, req)
		if rec.Code != 200 {
			t.Fatalf("renewal at elapsed=%v: status = %d, want 200; body=%q", elapsed, rec.Code, rec.Body.String())
		}
	}

	tokenHash := secrets.HashSessionHandle(cookie.Value)
	row, ok, err := h.ownerSessions.GetByTokenHash(context.Background(), tokenHash)
	if err != nil || !ok {
		t.Fatalf("GetByTokenHash: ok=%v err=%v", ok, err)
	}
	wantAbsolute := t0.Add(12 * time.Hour)
	if !row.IdleExpiresAt.Equal(wantAbsolute) {
		t.Fatalf("IdleExpiresAt = %v, want clamped to absolute %v", row.IdleExpiresAt, wantAbsolute)
	}
}

func TestSessionLifecycle_RenewalAdvancesLastSeenAndIdle(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := t0
	h := newTestAuthHandlers(t, &clock)

	cookie := sessionCookieFrom(t, setupOwnerDirect(t, h, testSetupPassword))

	clock = t0.Add(10 * time.Minute)
	req := newAuthRequest(t, http.MethodGet, "/api/control/v1/auth/session", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeSession(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200; body = %q", rec.Code, rec.Body.String())
	}

	tokenHash := secrets.HashSessionHandle(cookie.Value)
	row, ok, err := h.ownerSessions.GetByTokenHash(req.Context(), tokenHash)
	if err != nil || !ok {
		t.Fatalf("GetByTokenHash: ok=%v err=%v", ok, err)
	}
	if !row.LastSeenAt.Equal(clock) {
		t.Fatalf("LastSeenAt = %v, want %v", row.LastSeenAt, clock)
	}
	wantIdle := clock.Add(30 * time.Minute)
	if !row.IdleExpiresAt.Equal(wantIdle) {
		t.Fatalf("IdleExpiresAt = %v, want %v", row.IdleExpiresAt, wantIdle)
	}
}

// TestSessionLifecycle_RestartRevalidatesRows proves there is no
// in-memory-only trust: a FRESH AuthHandlers instance over the SAME db
// (simulating a process restart) still rejects an already-expired row.
func TestSessionLifecycle_RestartRevalidatesRows(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := t0
	db := testControlDB(t)

	h1 := NewAuthHandlers(db)
	h1.now = func() time.Time { return clock }
	cookie := sessionCookieFrom(t, setupOwnerDirect(t, h1, testSetupPassword))

	clock = t0.Add(13 * time.Hour) // past the 12h absolute cap

	// A brand new AuthHandlers over the SAME db — no shared in-memory state.
	h2 := NewAuthHandlers(db)
	h2.now = func() time.Time { return clock }

	req := newAuthRequest(t, http.MethodGet, "/api/control/v1/auth/session", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h2.ServeSession(rec, req)
	if rec.Code != 401 {
		t.Fatalf("status = %d, want 401 (a fresh handler instance must still enforce the persisted absolute expiry); body = %q", rec.Code, rec.Body.String())
	}
}

func TestOwnerSessionRepo_RevokeAll_ThroughHTTPHandlers_DirectStorageCall(t *testing.T) {
	db := testControlDB(t)
	h := NewAuthHandlers(db)
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	h.now = func() time.Time { return clock }

	c1 := sessionCookieFrom(t, setupOwnerDirect(t, h, testSetupPassword))

	loginRec := httptest.NewRecorder()
	loginReq := newAuthRequest(t, http.MethodPost, "/api/control/v1/auth/login", setupRequestBody(testSetupPassword))
	h.ServeLogin(loginRec, loginReq)
	if loginRec.Code != 200 {
		t.Fatalf("login status = %d, body = %q", loginRec.Code, loginRec.Body.String())
	}
	c2 := sessionCookieFrom(t, loginRec)

	if err := h.ownerSessions.RevokeAll(loginReq.Context(), clock); err != nil {
		t.Fatalf("RevokeAll: %v", err)
	}

	for _, cookie := range []*http.Cookie{c1, c2} {
		req := newAuthRequest(t, http.MethodGet, "/api/control/v1/auth/session", nil)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		h.ServeSession(rec, req)
		if rec.Code != 401 {
			t.Fatalf("session after RevokeAll: status = %d, want 401", rec.Code)
		}
	}
}
