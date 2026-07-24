package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

type providerListResponse struct {
	Data struct {
		Providers []providerJSON `json:"providers"`
	} `json:"data"`
}

func TestProvidersList_ReturnsElevenBuiltinsPlusCustom(t *testing.T) {
	mux := ControlMux(testAllowedHost, fakeSPA(), testControlDB(t))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, newAuthRequest(t, http.MethodGet, "/api/control/v1/providers", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /providers status = %d, want %d; body = %q", rec.Code, http.StatusOK, rec.Body.String())
	}

	var got providerListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v; body = %q", err, rec.Body.String())
	}
	if len(got.Data.Providers) != 12 {
		t.Fatalf("providers count = %d, want 12 (11 builtins + custom)", len(got.Data.Providers))
	}
}

func TestProvidersGet_UnknownID404(t *testing.T) {
	mux := ControlMux(testAllowedHost, fakeSPA(), testControlDB(t))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, newAuthRequest(t, http.MethodGet, "/api/control/v1/providers/does-not-exist", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != "not_found" {
		t.Fatalf("error code = %q, want %q", code, "not_found")
	}
}

func TestProvidersGet_KnownIDReturnsEntry(t *testing.T) {
	mux := ControlMux(testAllowedHost, fakeSPA(), testControlDB(t))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, newAuthRequest(t, http.MethodGet, "/api/control/v1/providers/clinepass", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %q", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got struct {
		Data providerJSON `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Data.ID != "clinepass" {
		t.Fatalf("id = %q, want clinepass", got.Data.ID)
	}
}

// TestProvidersList_AntigravityConfiguredReflectsEnv proves configured
// and missing_env are derived from the real environment at request
// time (never hard-coded, never leaking values — only names).
func TestProvidersList_AntigravityConfiguredReflectsEnv(t *testing.T) {
	const secretVar = "VENOM_ANTIGRAVITY_CLIENT_SECRET"
	const idVar = "VENOM_ANTIGRAVITY_CLIENT_ID"

	_ = os.Unsetenv(secretVar)
	_ = os.Unsetenv(idVar)

	mux := ControlMux(testAllowedHost, fakeSPA(), testControlDB(t))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, newAuthRequest(t, http.MethodGet, "/api/control/v1/providers/antigravity", nil))

	var unsetGot struct {
		Data providerJSON `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &unsetGot); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if unsetGot.Data.Configured {
		t.Fatalf("configured = true with both env vars unset, want false")
	}
	if len(unsetGot.Data.MissingEnv) != 2 {
		t.Fatalf("missing_env = %v, want 2 entries", unsetGot.Data.MissingEnv)
	}
	for _, v := range unsetGot.Data.MissingEnv {
		if v != secretVar && v != idVar {
			t.Fatalf("missing_env contained unexpected value %q", v)
		}
	}

	// Deliberately no English words (e.g. "secret", "client", "id") in
	// these values — those substrings legitimately appear elsewhere in
	// the response (the antigravity description text, JSON keys), which
	// would make assertNoFragment's window scan false-positive on the
	// structure rather than a real leak.
	const canarySecretValue = "xq7fK2pLm9Zt4Vb1Nr6Hy3Dc5Ea8Ws"
	const canaryIDValue = "Jk2Qm7Xr4Nt9Vy6Bp3Ld8Fc1Ha5Zs0Rw"
	t.Setenv(secretVar, canarySecretValue)
	t.Setenv(idVar, canaryIDValue)

	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, newAuthRequest(t, http.MethodGet, "/api/control/v1/providers/antigravity", nil))
	var setGot struct {
		Data providerJSON `json:"data"`
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &setGot); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !setGot.Data.Configured {
		t.Fatalf("configured = false with both env vars set, want true")
	}
	if len(setGot.Data.MissingEnv) != 0 {
		t.Fatalf("missing_env = %v, want empty when configured", setGot.Data.MissingEnv)
	}

	// The response must never contain the env var VALUES, only names.
	assertNoFragment(t, rec2.Body.String(), canarySecretValue, "GET /providers/antigravity response body")
	assertNoFragment(t, rec2.Body.String(), canaryIDValue, "GET /providers/antigravity response body")
}
