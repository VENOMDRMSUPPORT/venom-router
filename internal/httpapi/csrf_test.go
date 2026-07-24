package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/secrets"
)

func fixedClockAt2026() time.Time {
	return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
}

func decodeJSON(t *testing.T, body []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(body, v); err != nil {
		t.Fatalf("decode response: %v; body = %q", err, body)
	}
}

// csrfGuardedMutation is a minimal test-local handler wired to the same
// requireCSRF gate real mutating routes use, with an observable side
// effect (increments sideEffects) so a test can assert the side effect
// never happened when CSRF validation failed. CAPI-001 (later work)
// wraps every real control-plane mutation this way; this unit only
// needs to prove the gate itself, since /auth/reverify (the first real
// session-bound mutation) doesn't exist until SEC-005.
func csrfGuardedMutation(h *AuthHandlers, sideEffects *int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, ok, errCode := h.validateSession(r.Context(), r)
		if !ok {
			writeSessionError(w, errCode)
			return
		}
		if !h.requireCSRF(w, r, session.TokenHash) {
			return
		}
		*sideEffects++
		writeAuthJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"ok": true}})
	}
}

func TestCSRF_LoginAndSetupIssueSessionBoundToken(t *testing.T) {
	clock := fixedClockAt2026()
	h := newTestAuthHandlers(t, &clock)

	setupRec := setupOwnerDirect(t, h, testSetupPassword)
	setupCookie := sessionCookieFrom(t, setupRec)

	var setupBody struct {
		CSRFToken string `json:"csrf_token"`
	}
	decodeJSON(t, setupRec.Body.Bytes(), &setupBody)
	if setupBody.CSRFToken == "" {
		t.Fatalf("setup response missing csrf_token")
	}

	expected := h.issueCSRFToken(secrets.HashSessionHandle(setupCookie.Value))
	if setupBody.CSRFToken != expected {
		t.Fatalf("setup csrf_token = %q, want %q (HMAC of the session's own token hash)", setupBody.CSRFToken, expected)
	}
}

func TestCSRF_MissingTokenRejectedBeforeSideEffect(t *testing.T) {
	clock := fixedClockAt2026()
	h := newTestAuthHandlers(t, &clock)
	cookie := sessionCookieFrom(t, setupOwnerDirect(t, h, testSetupPassword))

	var sideEffects int
	handler := csrfGuardedMutation(h, &sideEffects)

	req := newAuthRequest(t, http.MethodPost, "/api/control/v1/test/mutate", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for a missing CSRF token; body = %q", rec.Code, rec.Body.String())
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != "csrf_failed" {
		t.Fatalf("error code = %q, want csrf_failed", code)
	}
	if sideEffects != 0 {
		t.Fatalf("sideEffects = %d, want 0 (the mutation must not run before CSRF passes)", sideEffects)
	}
}

func TestCSRF_MalformedTokenRejected(t *testing.T) {
	clock := fixedClockAt2026()
	h := newTestAuthHandlers(t, &clock)
	cookie := sessionCookieFrom(t, setupOwnerDirect(t, h, testSetupPassword))

	var sideEffects int
	handler := csrfGuardedMutation(h, &sideEffects)

	req := newAuthRequest(t, http.MethodPost, "/api/control/v1/test/mutate", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", "not-the-right-token-at-all")
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for a malformed CSRF token", rec.Code)
	}
	if sideEffects != 0 {
		t.Fatalf("sideEffects = %d, want 0", sideEffects)
	}
}

// TestCSRF_CrossSessionTokenRejected is the core forgery proof: a valid
// token minted for session A must be rejected when presented alongside
// session B's cookie, even though both sessions belong to the same
// (single) owner.
func TestCSRF_CrossSessionTokenRejected(t *testing.T) {
	clock := fixedClockAt2026()
	h := newTestAuthHandlers(t, &clock)

	setupRec := setupOwnerDirect(t, h, testSetupPassword)
	var setupBody struct {
		CSRFToken string `json:"csrf_token"`
	}
	decodeJSON(t, setupRec.Body.Bytes(), &setupBody)
	tokenA := setupBody.CSRFToken

	loginRec := httptest.NewRecorder()
	loginReq := newAuthRequest(t, http.MethodPost, "/api/control/v1/auth/login", setupRequestBody(testSetupPassword))
	h.ServeLogin(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %q", loginRec.Code, loginRec.Body.String())
	}
	cookieB := sessionCookieFrom(t, loginRec)

	var sideEffects int
	handler := csrfGuardedMutation(h, &sideEffects)

	req := newAuthRequest(t, http.MethodPost, "/api/control/v1/test/mutate", nil)
	req.AddCookie(cookieB)                 // session B's cookie...
	req.Header.Set("X-CSRF-Token", tokenA) // ...with session A's token
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for a cross-session CSRF token; body = %q", rec.Code, rec.Body.String())
	}
	if sideEffects != 0 {
		t.Fatalf("sideEffects = %d, want 0 (cross-session forgery must never reach the handler's side effect)", sideEffects)
	}
}

func TestCSRF_ValidTokenAllowsMutation(t *testing.T) {
	clock := fixedClockAt2026()
	h := newTestAuthHandlers(t, &clock)
	setupRec := setupOwnerDirect(t, h, testSetupPassword)
	cookie := sessionCookieFrom(t, setupRec)
	var setupBody struct {
		CSRFToken string `json:"csrf_token"`
	}
	decodeJSON(t, setupRec.Body.Bytes(), &setupBody)

	var sideEffects int
	handler := csrfGuardedMutation(h, &sideEffects)

	req := newAuthRequest(t, http.MethodPost, "/api/control/v1/test/mutate", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", setupBody.CSRFToken)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with a valid session-bound token; body = %q", rec.Code, rec.Body.String())
	}
	if sideEffects != 1 {
		t.Fatalf("sideEffects = %d, want 1", sideEffects)
	}
}

func TestCSRF_GetNeverRequiresToken(t *testing.T) {
	clock := fixedClockAt2026()
	h := newTestAuthHandlers(t, &clock)
	cookie := sessionCookieFrom(t, setupOwnerDirect(t, h, testSetupPassword))

	req := newAuthRequest(t, http.MethodGet, "/api/control/v1/auth/session", nil)
	req.AddCookie(cookie) // deliberately no X-CSRF-Token header
	rec := httptest.NewRecorder()
	h.ServeSession(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /auth/session without a CSRF token: status = %d, want 200 (GET is exempt); body = %q", rec.Code, rec.Body.String())
	}
}
