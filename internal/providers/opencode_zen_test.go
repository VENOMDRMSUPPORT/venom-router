package providers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fixtureOpenCodeZenServer stands in for opencode.ai/zen: it accepts
// exactly one valid key ("good-key") on the chat probe and serves a
// fixed /v1/models catalog. Both probe implementations here are
// test-only (internal/providers' production code never imports
// net/http) and mirror what PROV-005's real production probes
// (internal/accounts/application) do.
func fixtureOpenCodeZenServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer good-key" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	})
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"model-a"},{"id":"model-b"}]}`))
	})
	return httptest.NewServer(mux)
}

func fixtureChatProbe(t *testing.T, serverURL string) ChatProbe {
	t.Helper()
	return func(ctx context.Context, baseURL, key string) (int, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, serverURL+"/v1/chat/completions", strings.NewReader(`{"max_tokens":1}`))
		if err != nil {
			return 0, err
		}
		req.Header.Set("Authorization", "Bearer "+key)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0, err
		}
		defer func() { _ = resp.Body.Close() }()
		return resp.StatusCode, nil
	}
}

func fixtureModelsProbe(t *testing.T, serverURL string) ModelsProbe {
	t.Helper()
	return func(ctx context.Context, baseURL, key string) ([]byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, serverURL+"/v1/models", nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+key)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer func() { _ = resp.Body.Close() }()
		return io.ReadAll(resp.Body)
	}
}

func TestOpenCodeZenAdapter_ConnectAPIKey_ValidKeyReportsFingerprintIdentity(t *testing.T) {
	server := fixtureOpenCodeZenServer(t)
	defer server.Close()

	adapter := NewOpenCodeZenAdapter(fixtureChatProbe(t, server.URL), fixtureModelsProbe(t, server.URL))
	identity, creds, err := adapter.ConnectAPIKey(context.Background(), "good-key")
	if err != nil {
		t.Fatalf("ConnectAPIKey: %v", err)
	}
	if identity.Plan != "Free" {
		t.Fatalf("Plan = %q, want Free", identity.Plan)
	}
	if identity.ExternalID == "" || identity.ExternalID == "good-key" {
		t.Fatalf("ExternalID = %q, want a non-empty fingerprint distinct from the raw key", identity.ExternalID)
	}
	wantFingerprint := fingerprintAPIKey(NormalizeAPIKey("good-key"))
	if identity.ExternalID != wantFingerprint {
		t.Fatalf("ExternalID = %q, want the key's own fingerprint %q", identity.ExternalID, wantFingerprint)
	}
	if creds.Value != "good-key" {
		t.Fatalf("StoredCredentials.Value = %q, want the normalized key", creds.Value)
	}
}

func TestOpenCodeZenAdapter_ConnectAPIKey_InvalidKeyRejected(t *testing.T) {
	server := fixtureOpenCodeZenServer(t)
	defer server.Close()

	adapter := NewOpenCodeZenAdapter(fixtureChatProbe(t, server.URL), fixtureModelsProbe(t, server.URL))
	_, creds, err := adapter.ConnectAPIKey(context.Background(), "bad-key")
	if !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("ConnectAPIKey(bad-key) error = %v, want ErrInvalidCredential", err)
	}
	if creds.Value != "" {
		t.Fatalf("StoredCredentials on an invalid key = %+v, want empty", creds)
	}
}

func TestOpenCodeZenAdapter_ConnectAPIKey_UnavailableClassifiedDistinctly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	adapter := NewOpenCodeZenAdapter(fixtureChatProbe(t, server.URL), fixtureModelsProbe(t, server.URL))
	_, _, err := adapter.ConnectAPIKey(context.Background(), "any-key")
	if !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("ConnectAPIKey during a 503 error = %v, want ErrProviderUnavailable", err)
	}
	if errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("unavailable was also classified as ErrInvalidCredential — must be distinct")
	}
}

func TestOpenCodeZenAdapter_DiscoverModels_ParsesFixture(t *testing.T) {
	server := fixtureOpenCodeZenServer(t)
	defer server.Close()

	adapter := NewOpenCodeZenAdapter(fixtureChatProbe(t, server.URL), fixtureModelsProbe(t, server.URL))
	models, err := adapter.DiscoverModels(context.Background(), StoredCredentials{Value: "good-key"})
	if err != nil {
		t.Fatalf("DiscoverModels: %v", err)
	}
	if len(models) != 2 || models[0].ProviderModelID != "model-a" || models[1].ProviderModelID != "model-b" {
		t.Fatalf("models = %+v, want [model-a model-b]", models)
	}
}

func TestRegisterOpenCodeZen_CapabilitiesDeriveApiKeyAndDiscovery(t *testing.T) {
	server := fixtureOpenCodeZenServer(t)
	defer server.Close()

	reg := NewRegistry()
	if err := RegisterOpenCodeZen(reg, fixtureChatProbe(t, server.URL), fixtureModelsProbe(t, server.URL)); err != nil {
		t.Fatalf("RegisterOpenCodeZen: %v", err)
	}

	caps := DerivedCapabilities(reg, OpenCodeZenID)
	if len(caps) != 2 || caps[0] != "api_key" || caps[1] != "discovery" {
		t.Fatalf("DerivedCapabilities = %v, want [api_key discovery]", caps)
	}
}

// TestOpenCodeZenAdapter_Canary_KeyNeverAppearsInIdentityOrDiscoveryOutput
// pushes a distinctive canary key through ConnectAPIKey and
// DiscoverModels and asserts no fragment of it appears in the returned
// identity, stored-credential fingerprint field, or discovered models.
func TestOpenCodeZenAdapter_Canary_KeyNeverAppearsInIdentityOrDiscoveryOutput(t *testing.T) {
	const canaryKey = "CANARY-9f3Kx2Qw8pLm0Zt7Vb4Nr1Hy6Dc5Ea-opencodezen"

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer "+canaryKey {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	})
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"model-a"}]}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	adapter := NewOpenCodeZenAdapter(fixtureChatProbe(t, server.URL), fixtureModelsProbe(t, server.URL))
	identity, creds, err := adapter.ConnectAPIKey(context.Background(), canaryKey)
	if err != nil {
		t.Fatalf("ConnectAPIKey: %v", err)
	}

	assertNoKeyFragment(t, identity.ExternalID, canaryKey, "identity.ExternalID")
	assertNoKeyFragment(t, identity.Plan, canaryKey, "identity.Plan")
	// creds.Value legitimately carries the normalized key at this
	// transient handoff point (it becomes the plaintext PROV-003
	// encrypts) — asserting it here would be a false failure, so only
	// the ExternalID/Plan (what's REPORTED, not what's stored) are
	// checked for leakage.
	_ = creds

	models, err := adapter.DiscoverModels(context.Background(), StoredCredentials{Value: canaryKey})
	if err != nil {
		t.Fatalf("DiscoverModels: %v", err)
	}
	for _, m := range models {
		assertNoKeyFragment(t, m.ProviderModelID, canaryKey, "discovered model id")
		assertNoKeyFragment(t, m.DisplayName, canaryKey, "discovered model display name")
	}
}

func assertNoKeyFragment(t *testing.T, output, secret, where string) {
	t.Helper()
	const minWindow = 8
	for start := 0; start+minWindow <= len(secret); start++ {
		end := start + minWindow
		if strings.Contains(output, secret[start:end]) {
			t.Fatalf("%s leaked key fragment %q", where, secret[start:end])
		}
	}
}
