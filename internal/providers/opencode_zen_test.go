package providers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

// fixtureModelsDevBody builds a models.dev dataset body whose `opencode`
// entry contains exactly the given raw model JSON entries.
func fixtureModelsDevBody(modelEntries string) []byte {
	return []byte(`{"opencode":{"models":{` + modelEntries + `}},"someone-else":{"models":{"other":{"cost":{"input":0,"output":0}}}}}`)
}

// fixtureAllFreeModelsDevProbe marks every id in ids as an explicit
// zero-cost, non-deprecated models.dev entry — the "everything the zen
// catalog serves is free" fixture for tests that are not about the
// intersection itself.
func fixtureAllFreeModelsDevProbe(t *testing.T, ids ...string) ModelsDevProbe {
	t.Helper()
	entries := ""
	for i, id := range ids {
		if i > 0 {
			entries += ","
		}
		entries += `"` + id + `":{"cost":{"input":0,"output":0}}`
	}
	body := fixtureModelsDevBody(entries)
	return func(ctx context.Context) ([]byte, error) {
		return body, nil
	}
}

// fixtureClock returns an injectable clock starting at a fixed instant,
// plus the pointer a test advances to cross the cache TTL.
func fixtureClock() (*time.Time, func() time.Time) {
	t0 := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	return &t0, func() time.Time { return t0 }
}

func TestOpenCodeZenAdapter_ConnectAPIKey_ValidKeyReportsFingerprintIdentity(t *testing.T) {
	server := fixtureOpenCodeZenServer(t)
	defer server.Close()

	adapter := NewOpenCodeZenAdapter(fixtureChatProbe(t, server.URL), fixtureModelsProbe(t, server.URL), fixtureAllFreeModelsDevProbe(t, "model-a", "model-b"), nil)
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

	adapter := NewOpenCodeZenAdapter(fixtureChatProbe(t, server.URL), fixtureModelsProbe(t, server.URL), fixtureAllFreeModelsDevProbe(t, "model-a", "model-b"), nil)
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

	adapter := NewOpenCodeZenAdapter(fixtureChatProbe(t, server.URL), fixtureModelsProbe(t, server.URL), fixtureAllFreeModelsDevProbe(t, "model-a", "model-b"), nil)
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

	adapter := NewOpenCodeZenAdapter(fixtureChatProbe(t, server.URL), fixtureModelsProbe(t, server.URL), fixtureAllFreeModelsDevProbe(t, "model-a", "model-b"), nil)
	models, err := adapter.DiscoverModels(context.Background(), StoredCredentials{Value: "good-key"})
	if err != nil {
		t.Fatalf("DiscoverModels: %v", err)
	}
	if len(models) != 2 || models[0].ProviderModelID != "model-a" || models[1].ProviderModelID != "model-b" {
		t.Fatalf("models = %+v, want [model-a model-b]", models)
	}
}

func TestRegisterOpenCodeZen_CapabilitiesDeriveApiKeyHealthAndDiscovery(t *testing.T) {
	server := fixtureOpenCodeZenServer(t)
	defer server.Close()

	reg := NewRegistry()
	if err := RegisterOpenCodeZen(reg, fixtureChatProbe(t, server.URL), fixtureModelsProbe(t, server.URL), fixtureAllFreeModelsDevProbe(t, "model-a", "model-b"), nil); err != nil {
		t.Fatalf("RegisterOpenCodeZen: %v", err)
	}

	// The Health registration alone makes DerivedCapabilities report
	// "health" — zero extra code beyond the Definition entry.
	caps := DerivedCapabilities(reg, OpenCodeZenID)
	if len(caps) != 3 || caps[0] != "api_key" || caps[1] != "discovery" || caps[2] != "health" {
		t.Fatalf("DerivedCapabilities = %v, want [api_key discovery health]", caps)
	}
	if _, ok := reg.HealthAdapter(OpenCodeZenID); !ok {
		t.Fatalf("HealthAdapter(opencode-zen) ok = false, want a registered adapter")
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

	adapter := NewOpenCodeZenAdapter(fixtureChatProbe(t, server.URL), fixtureModelsProbe(t, server.URL), fixtureAllFreeModelsDevProbe(t, "model-a"), nil)
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

// ============================================================================
// Free-only discovery intersection (03 §3)
// ============================================================================

// fixtureZenCatalogProbe serves a fixed zen /v1/models body without a server.
func fixtureZenCatalogProbe(ids ...string) ModelsProbe {
	body := `{"data":[`
	for i, id := range ids {
		if i > 0 {
			body += ","
		}
		body += `{"id":"` + id + `"}`
	}
	body += `]}`
	return func(ctx context.Context, baseURL, key string) ([]byte, error) {
		return []byte(body), nil
	}
}

// TestOpenCodeZenAdapter_DiscoverModels_IntersectsFreeSetExactly is the
// intersection matrix: only a model that zen serves AND models.dev prices
// at an explicit zero AND does not mark deprecated survives. Paid,
// deprecated-but-free, unknown-cost, zen-only, and dataset-only entries
// are all excluded.
func TestOpenCodeZenAdapter_DiscoverModels_IntersectsFreeSetExactly(t *testing.T) {
	zen := fixtureZenCatalogProbe("free-a", "paid-b", "deprecated-c", "zen-only-d", "no-cost-f")
	modelsDev := func(ctx context.Context) ([]byte, error) {
		return fixtureModelsDevBody(
			`"free-a":{"cost":{"input":0,"output":0}},` +
				`"paid-b":{"cost":{"input":1.5,"output":3}},` +
				`"deprecated-c":{"cost":{"input":0,"output":0},"status":"deprecated"},` +
				`"no-cost-f":{},` + // present but WITHOUT a cost: unknown != free
				`"dataset-only-e":{"cost":{"input":0,"output":0}}`,
		), nil
	}

	adapter := NewOpenCodeZenAdapter(nil, zen, modelsDev, nil)
	models, err := adapter.DiscoverModels(context.Background(), StoredCredentials{Value: "k"})
	if err != nil {
		t.Fatalf("DiscoverModels: %v", err)
	}
	if len(models) != 1 || models[0].ProviderModelID != "free-a" {
		t.Fatalf("models = %+v, want exactly [free-a] (paid, deprecated, unknown-cost, zen-only all excluded)", models)
	}
}

// TestOpenCodeZenAdapter_DiscoverModels_CacheHonoredWithinTTL proves the
// models.dev parse is served from cache inside the ~10 min TTL and
// re-fetched after it — with an injected clock, no timers.
func TestOpenCodeZenAdapter_DiscoverModels_CacheHonoredWithinTTL(t *testing.T) {
	clockPtr, now := fixtureClock()
	fetches := 0
	modelsDev := func(ctx context.Context) ([]byte, error) {
		fetches++
		return fixtureModelsDevBody(`"free-a":{"cost":{"input":0,"output":0}}`), nil
	}

	adapter := NewOpenCodeZenAdapter(nil, fixtureZenCatalogProbe("free-a"), modelsDev, now)
	for i := 0; i < 3; i++ {
		if _, err := adapter.DiscoverModels(context.Background(), StoredCredentials{Value: "k"}); err != nil {
			t.Fatalf("DiscoverModels #%d: %v", i, err)
		}
	}
	if fetches != 1 {
		t.Fatalf("models.dev fetches within TTL = %d, want 1 (cache must serve repeats)", fetches)
	}

	*clockPtr = clockPtr.Add(11 * time.Minute)
	if _, err := adapter.DiscoverModels(context.Background(), StoredCredentials{Value: "k"}); err != nil {
		t.Fatalf("DiscoverModels after TTL: %v", err)
	}
	if fetches != 2 {
		t.Fatalf("models.dev fetches after TTL = %d, want 2 (cache must expire)", fetches)
	}
}

// TestOpenCodeZenAdapter_DiscoverModels_ModelsDevFailureIsTypedError proves
// the failure policy: no models.dev and no FRESH cache -> the typed
// ErrModelsDevUnavailable, NEVER the unfiltered list and never a silent
// empty success — a stale cache does not rescue the run either.
func TestOpenCodeZenAdapter_DiscoverModels_ModelsDevFailureIsTypedError(t *testing.T) {
	clockPtr, now := fixtureClock()
	healthy := true
	modelsDev := func(ctx context.Context) ([]byte, error) {
		if healthy {
			return fixtureModelsDevBody(`"free-a":{"cost":{"input":0,"output":0}}`), nil
		}
		return nil, errors.New("models.dev is down")
	}

	adapter := NewOpenCodeZenAdapter(nil, fixtureZenCatalogProbe("free-a"), modelsDev, now)

	// Cold start + failing dataset: typed error.
	healthy = false
	if _, err := adapter.DiscoverModels(context.Background(), StoredCredentials{Value: "k"}); !errors.Is(err, ErrModelsDevUnavailable) {
		t.Fatalf("cold-start failure error = %v, want ErrModelsDevUnavailable", err)
	}

	// Populate the cache, then fail the dataset and cross the TTL: the
	// STALE cache must not rescue the run.
	healthy = true
	if _, err := adapter.DiscoverModels(context.Background(), StoredCredentials{Value: "k"}); err != nil {
		t.Fatalf("populate cache: %v", err)
	}
	healthy = false
	*clockPtr = clockPtr.Add(11 * time.Minute)
	if _, err := adapter.DiscoverModels(context.Background(), StoredCredentials{Value: "k"}); !errors.Is(err, ErrModelsDevUnavailable) {
		t.Fatalf("stale-cache failure error = %v, want ErrModelsDevUnavailable (stale cost facts must not classify a free account's models)", err)
	}

	// Within the TTL the fresh cache still serves — the failure above was
	// about STALENESS, not about the cache existing.
	healthy = true
	if _, err := adapter.DiscoverModels(context.Background(), StoredCredentials{Value: "k"}); err != nil {
		t.Fatalf("repopulate cache: %v", err)
	}
	healthy = false
	*clockPtr = clockPtr.Add(5 * time.Minute)
	if _, err := adapter.DiscoverModels(context.Background(), StoredCredentials{Value: "k"}); err != nil {
		t.Fatalf("fresh-cache read during a dataset outage: %v, want success from cache", err)
	}
}

// TestOpenCodeZenAdapter_DiscoverModels_MissingOpenCodeEntryIsError proves a
// dataset without the `opencode` entry is a loud failure (the shape this
// adapter depends on drifted), never an authoritative "nothing is free".
func TestOpenCodeZenAdapter_DiscoverModels_MissingOpenCodeEntryIsError(t *testing.T) {
	modelsDev := func(ctx context.Context) ([]byte, error) {
		return []byte(`{"someone-else":{"models":{"m":{"cost":{"input":0,"output":0}}}}}`), nil
	}
	adapter := NewOpenCodeZenAdapter(nil, fixtureZenCatalogProbe("free-a"), modelsDev, nil)
	if _, err := adapter.DiscoverModels(context.Background(), StoredCredentials{Value: "k"}); !errors.Is(err, ErrModelsDevUnavailable) {
		t.Fatalf("missing opencode entry error = %v, want ErrModelsDevUnavailable", err)
	}
}

// ============================================================================
// Health classification (03 §3: GET /v1/models, Bearer)
// ============================================================================

// TestOpenCodeZenAdapter_CheckAccountHealth_ClassificationMatrix pins the
// full status matrix: 2xx healthy; 401/403 expired (definitive credential
// rejection, NOT retryable); 429/5xx/transport unavailable (retryable,
// NEVER expired).
func TestOpenCodeZenAdapter_CheckAccountHealth_ClassificationMatrix(t *testing.T) {
	cases := []struct {
		name          string
		probe         ModelsProbe
		wantStatus    string
		wantCredValid bool
		wantReachable bool
		wantRetryable *bool // nil = no Failure expected
	}{
		{
			name:          "200 is healthy",
			probe:         func(ctx context.Context, baseURL, key string) ([]byte, error) { return []byte(`{"data":[]}`), nil },
			wantStatus:    "healthy",
			wantCredValid: true,
			wantReachable: true,
		},
		{
			name: "401 is expired",
			probe: func(ctx context.Context, baseURL, key string) ([]byte, error) {
				return nil, &ModelsProbeStatusError{StatusCode: 401}
			},
			wantStatus:    "expired",
			wantCredValid: false,
			wantReachable: true,
			wantRetryable: boolPtr(false),
		},
		{
			name: "403 is expired",
			probe: func(ctx context.Context, baseURL, key string) ([]byte, error) {
				return nil, &ModelsProbeStatusError{StatusCode: 403}
			},
			wantStatus:    "expired",
			wantCredValid: false,
			wantReachable: true,
			wantRetryable: boolPtr(false),
		},
		{
			name: "429 is unreachable and retryable, never expired",
			probe: func(ctx context.Context, baseURL, key string) ([]byte, error) {
				return nil, &ModelsProbeStatusError{StatusCode: 429}
			},
			wantStatus:    "unreachable",
			wantCredValid: false,
			wantReachable: false,
			wantRetryable: boolPtr(true),
		},
		{
			name: "500 is unreachable and retryable",
			probe: func(ctx context.Context, baseURL, key string) ([]byte, error) {
				return nil, &ModelsProbeStatusError{StatusCode: 500}
			},
			wantStatus:    "unreachable",
			wantCredValid: false,
			wantReachable: false,
			wantRetryable: boolPtr(true),
		},
		{
			name: "transport error is unreachable and retryable",
			probe: func(ctx context.Context, baseURL, key string) ([]byte, error) {
				return nil, errors.New("dial tcp: connection refused")
			},
			wantStatus:    "unreachable",
			wantCredValid: false,
			wantReachable: false,
			wantRetryable: boolPtr(true),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, now := fixtureClock()
			adapter := NewOpenCodeZenAdapter(nil, tc.probe, nil, now)
			obs, err := adapter.CheckAccountHealth(context.Background(), StoredCredentials{Value: "k"})
			if err != nil {
				t.Fatalf("CheckAccountHealth error = %v, want nil (every outcome IS the observation)", err)
			}
			if obs.Status != tc.wantStatus {
				t.Fatalf("Status = %q, want %q", obs.Status, tc.wantStatus)
			}
			if obs.CredentialValid != tc.wantCredValid {
				t.Fatalf("CredentialValid = %v, want %v", obs.CredentialValid, tc.wantCredValid)
			}
			if obs.TransportReachable != tc.wantReachable {
				t.Fatalf("TransportReachable = %v, want %v", obs.TransportReachable, tc.wantReachable)
			}
			if obs.CheckedAt != now().Unix() {
				t.Fatalf("CheckedAt = %d, want the injected clock %d", obs.CheckedAt, now().Unix())
			}
			if tc.wantRetryable == nil {
				if obs.Failure != nil {
					t.Fatalf("Failure = %+v, want nil for a healthy observation", obs.Failure)
				}
			} else {
				if obs.Failure == nil {
					t.Fatalf("Failure = nil, want a populated failure envelope")
				}
				if obs.Failure.Retryable != *tc.wantRetryable {
					t.Fatalf("Failure.Retryable = %v, want %v", obs.Failure.Retryable, *tc.wantRetryable)
				}
			}
		})
	}
}

func boolPtr(b bool) *bool { return &b }
