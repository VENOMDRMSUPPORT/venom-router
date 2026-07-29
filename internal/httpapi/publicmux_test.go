package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestPublicMux_NoControlSurface proves the standalone data-plane mux serves
// ONLY /v1/* — every control path, /health, and the SPA root return 404 (a
// probe of the public bind cannot even learn a control surface exists).
//
// Mutation U2-M5: mount the SPA / control routes on the public-only mux → one
// of these paths stops 404-ing → RED.
func TestPublicMux_NoControlSurface(t *testing.T) {
	db := testControlDB(t)
	mux := PublicMux(db, func() time.Time { return vkFixedNow })

	for _, path := range []string{
		"/api/control/v1/auth/login",
		"/api/control/v1/accounts",
		"/health",
		"/",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("public mux GET %s status = %d, want 404 (no control surface on the data plane)", path, rec.Code)
		}
	}
}

// TestControlMux_V1IsVKGatedNotOwnerSession proves that on the shared control
// mux the /v1 surface is authenticated by a vk key, NOT the owner session:
//   - a valid vk key alone (no owner cookie) reaches 200;
//   - an owner session alone (no vk key) is rejected 401 invalid_api_key.
//
// Mutation U2-M5 (mount control/SPA on public mux) is proved by the test
// above; this one guards the reverse overlap direction.
func TestControlMux_V1IsVKGatedNotOwnerSession(t *testing.T) {
	db := testControlDB(t)
	mux := ControlMux(testAllowedHost, fakeSPA(), db, testKeyring(t))
	seedAPIKey(t, db, "k-1", "vk_live_ctrl0000", nil, false)

	// Valid vk key alone → 200 (never owner-session gated).
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Host = testAllowedHost
	req.Header.Set("Authorization", "Bearer vk_live_ctrl0000")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("vk key alone on /v1/models = %d, want 200; body=%q", rec.Code, rec.Body.String())
	}

	// Owner session alone (valid cookie, no vk key) → 401: an owner session
	// does NOT authenticate the data plane.
	cookie, _ := setupOwnerWithCSRF(t, mux)
	req2 := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req2.RemoteAddr = "127.0.0.1:54321"
	req2.Host = testAllowedHost
	req2.AddCookie(cookie)
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("owner session alone on /v1/models = %d, want 401 (session must not authenticate /v1)", rec2.Code)
	}
	if code := decodeErrorCode(t, rec2.Body.Bytes()); code != publicErrInvalidAPIKey {
		t.Fatalf("owner-session-alone error code = %q, want %q", code, publicErrInvalidAPIKey)
	}
}
