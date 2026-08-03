package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/execution"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
)

// TestGoogleModelsProbeSeam_GoogHeaderAndPageToken is mutations row 1 (auth
// header) and row 3 (request paging parameter name): the request carries
// x-goog-api-key and NO Authorization header, and the paging value is sent as
// `pageToken` (never `nextPageToken`).
func TestGoogleModelsProbeSeam_GoogHeaderAndPageToken(t *testing.T) {
	var gotGoog, gotAuth, gotPageToken, gotNextPageToken string
	var rawAuthSeen bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotGoog = r.Header.Get("x-goog-api-key")
		gotAuth = r.Header.Get("Authorization")
		_, rawAuthSeen = r.Header["Authorization"]
		gotPageToken = r.URL.Query().Get("pageToken")
		gotNextPageToken = r.URL.Query().Get("nextPageToken")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"models":[]}`))
	}))
	t.Cleanup(srv.Close)

	status, _, err := googleModelsProbeSeam(context.Background(), srv.URL, "AIza-secret", "tok2")
	if err != nil {
		t.Fatalf("probe error = %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if gotGoog != "AIza-secret" {
		t.Fatalf("x-goog-api-key = %q, want AIza-secret", gotGoog)
	}
	if rawAuthSeen {
		t.Fatalf("Authorization header present (%q), want ABSENT (gemini uses x-goog-api-key, not Bearer)", gotAuth)
	}
	if gotPageToken != "tok2" {
		t.Fatalf("pageToken query = %q, want tok2 (the REQUEST param is pageToken)", gotPageToken)
	}
	if gotNextPageToken != "" {
		t.Fatalf("nextPageToken query = %q, want empty (nextPageToken is a RESPONSE field, not a request param)", gotNextPageToken)
	}
}

// TestGeminiCLI_WiringResolvesNativeAPI is mutation row 9: the registered
// Transport is native_api AND a BuildProbeTransportMaps resolution over
// native_api-only impls finds an implementation for gemini-cli (fail-closed
// proof the wiring is real).
func TestGeminiCLI_WiringResolvesNativeAPI(t *testing.T) {
	reg := providers.NewRegistry()
	if err := registerGeminiCLI(reg); err != nil {
		t.Fatalf("registerGeminiCLI() error = %v", err)
	}
	def, ok := reg.Definition(providers.GeminiCLIID)
	if !ok || def.Transport != providers.TransportKindNativeAPI {
		t.Fatalf("gemini-cli Transport = %q (ok=%v), want native_api", def.Transport, ok)
	}

	impls := map[execution.TransportType]execution.InferenceTransport{
		execution.TransportTypeNativeAPI: execution.NewNativeAPITransport(&http.Client{}, 0),
	}
	pt, pb, err := BuildProbeTransportMaps(reg, impls, map[providers.ProviderID]string{
		providers.GeminiCLIID: providers.GeminiCLIBaseURL + "/v1beta",
	})
	if err != nil {
		t.Fatalf("BuildProbeTransportMaps() error = %v, want gemini-cli to resolve to the native_api impl", err)
	}
	if pt[string(providers.GeminiCLIID)] == nil {
		t.Fatal("no probe transport resolved for gemini-cli")
	}
	if pb[string(providers.GeminiCLIID)] != providers.GeminiCLIBaseURL+"/v1beta" {
		t.Fatalf("probe base URL = %q, want the /v1beta base", pb[string(providers.GeminiCLIID)])
	}
}
