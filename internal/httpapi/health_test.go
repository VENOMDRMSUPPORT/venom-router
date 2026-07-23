package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testAllowedHost = "127.0.0.1:8081"

// TestHealthMux_LoopbackAllowedHostReturns200 is Test B1: a loopback
// client with an allowed Host and no session gets a 200 liveness
// response.
func TestHealthMux_LoopbackAllowedHostReturns200(t *testing.T) {
	mux := HealthMux(testAllowedHost)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Host = testAllowedHost

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %q", rec.Code, http.StatusOK, rec.Body.String())
	}
}

// TestHealthMux_NotUnderControlAPI_NoOwnerData is Test B2: /health is
// served at exactly "/health" (not under /api/control/v1), and its body
// carries no owner-shaped data. It also confirms the same mux does not
// additionally serve a second liveness surface under
// /api/control/v1/health.
func TestHealthMux_NotUnderControlAPI_NoOwnerData(t *testing.T) {
	mux := HealthMux(testAllowedHost)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.RemoteAddr = "127.0.0.1:1"
	req.Host = testAllowedHost
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /health status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	for _, forbidden := range []string{"account", "owner", "session", "credential"} {
		if strings.Contains(strings.ToLower(body), forbidden) {
			t.Fatalf("response body appears to carry owner-ish data (contains %q): %q", forbidden, body)
		}
	}

	req2 := httptest.NewRequest(http.MethodGet, "/api/control/v1/health", nil)
	req2.RemoteAddr = "127.0.0.1:1"
	req2.Host = testAllowedHost
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)
	if rec2.Code == http.StatusOK {
		t.Fatalf("GET /api/control/v1/health unexpectedly returned 200 — a second liveness surface must not exist in V1")
	}
}

// TestNetworkGate_DisallowedHostRejected is Test B3: a disallowed Host
// header is rejected with 403 before any liveness response, even from a
// loopback socket.
func TestNetworkGate_DisallowedHostRejected(t *testing.T) {
	mux := HealthMux(testAllowedHost)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.RemoteAddr = "127.0.0.1:54321" // loopback — only Host should fail
	req.Host = "evil.example.com"

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d for disallowed Host", rec.Code, http.StatusForbidden)
	}
}

// TestNetworkGate_NonLoopbackRejectedEvenWithSpoofedXFF is Test B4: a
// request whose actual RemoteAddr is non-loopback is rejected, and a
// spoofed X-Forwarded-For claiming loopback is never consulted.
//
// How the non-loopback socket is simulated: httptest.NewRequest builds a
// plain *http.Request without going through a real net.Listener/TCP
// accept, so RemoteAddr is just a string field on that struct — setting
// it directly to a real, non-loopback address (203.0.113.5, TEST-NET-3
// per RFC 5737, reserved for documentation/testing) is the standard way
// to unit-test RemoteAddr-based logic without needing an actual routable
// non-loopback network path. mux.ServeHTTP is called directly (as
// http.Handler), the same code path a real net/http server would invoke
// per-request with the real socket's RemoteAddr already populated.
func TestNetworkGate_NonLoopbackRejectedEvenWithSpoofedXFF(t *testing.T) {
	mux := HealthMux(testAllowedHost)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.RemoteAddr = "203.0.113.5:54321"
	req.Host = testAllowedHost // Host allowed — only RemoteAddr should fail
	req.Header.Set("X-Forwarded-For", "127.0.0.1")

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d for non-loopback RemoteAddr (even with spoofed X-Forwarded-For)", rec.Code, http.StatusForbidden)
	}
}

// TestNetworkGate_BareLoopbackHostnamesAllowedRegardlessOfPort confirms
// the three bare hostnames are accepted independent of allowedHost,
// exactly as 01 §6a lists them as standalone accepted values.
func TestNetworkGate_BareLoopbackHostnamesAllowedRegardlessOfPort(t *testing.T) {
	for _, host := range []string{"localhost", "127.0.0.1", "::1"} {
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		req.RemoteAddr = "127.0.0.1:1"
		req.Host = host

		rec := httptest.NewRecorder()
		HealthMux("some.other.configured.host:9999").ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("Host=%q: status = %d, want %d", host, rec.Code, http.StatusOK)
		}
	}
}
