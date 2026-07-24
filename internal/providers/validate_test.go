package providers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// testChatProbe is a real HTTP ChatProbe implementation, local to this
// test file only: internal/providers' production code never imports
// net/http (01 §3/§8 layering) — this exists purely to fixture-test
// ValidateAPIKey's classification against genuine HTTP responses.
// PROV-005's real production probe lives in internal/accounts/
// application, which may import net/http.
func testChatProbe(t *testing.T, serverURL string) ChatProbe {
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

// TestValidateAPIKey_200ForAnyTokenTrap is the "authentic validation"
// proof (03 §1): a fixture where GET /v1/models returns 200 for ANY
// token (the trap a naive host-up check would fall into) but the real
// chat-completions probe genuinely rejects a bad key. ValidateAPIKey
// must classify the bad key as invalid, proving it authenticates rather
// than merely checking the host is up.
func TestValidateAPIKey_200ForAnyTokenTrap(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK) // 200 regardless of the Authorization header
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer good-key" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	// Sanity: prove the trap is real — GET /v1/models really does return
	// 200 for a garbage token on this fixture.
	req, _ := http.NewRequest(http.MethodGet, server.URL+"/v1/models", nil)
	req.Header.Set("Authorization", "Bearer garbage-token-should-still-200")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/models: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("fixture sanity check failed: GET /v1/models = %d, want 200 for any token (the trap this test relies on)", resp.StatusCode)
	}

	status := ValidateAPIKey(context.Background(), testChatProbe(t, server.URL), server.URL, "bad-key")
	if status != ValidationInvalid {
		t.Fatalf("ValidateAPIKey(bad-key) = %q, want invalid (must authenticate via the chat probe, not just check the host is up)", status)
	}
}

func TestValidateAPIKey_429And5xx_ClassifiedUnavailableNotInvalid(t *testing.T) {
	for _, code := range []int{429, 500, 502, 503} {
		func() {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(code)
			}))
			defer server.Close()

			status := ValidateAPIKey(context.Background(), testChatProbe(t, server.URL), server.URL, "any-key")
			if status != ValidationUnavailable {
				t.Fatalf("status code %d classified as %q, want unavailable (retryable, not invalid)", code, status)
			}
		}()
	}
}

func TestValidateAPIKey_GenuineAuthFailureAndSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer correct-key" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	probe := testChatProbe(t, server.URL)

	if status := ValidateAPIKey(context.Background(), probe, server.URL, "wrong-key"); status != ValidationInvalid {
		t.Fatalf("wrong-key status = %q, want invalid", status)
	}
	if status := ValidateAPIKey(context.Background(), probe, server.URL, "correct-key"); status != ValidationValid {
		t.Fatalf("correct-key status = %q, want valid", status)
	}
}

func TestValidateAPIKey_AmbiguousStatusNeverClassifiedValid(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	status := ValidateAPIKey(context.Background(), testChatProbe(t, server.URL), server.URL, "any-key")
	if status != ValidationUnavailable {
		t.Fatalf("an ambiguous 404 classified as %q, want unavailable (fail-safe — never valid)", status)
	}
}

func TestValidateAPIKey_ProbeTransportErrorClassifiedUnavailable(t *testing.T) {
	probe := func(ctx context.Context, baseURL, key string) (int, error) {
		return 0, errors.New("dial tcp: connection refused")
	}
	status := ValidateAPIKey(context.Background(), probe, "http://unreachable.invalid", "any-key")
	if status != ValidationUnavailable {
		t.Fatalf("a transport error classified as %q, want unavailable", status)
	}
}

func TestNormalizeAPIKey_TrimsAndCollapsesWhitespace(t *testing.T) {
	cases := map[string]string{
		"  sk-abc123  ": "sk-abc123",
		"sk-abc\t\n123": "sk-abc 123",
		"no-whitespace": "no-whitespace",
		"  a   b   c  ": "a b c",
	}
	for in, want := range cases {
		if got := NormalizeAPIKey(in); got != want {
			t.Fatalf("NormalizeAPIKey(%q) = %q, want %q", in, got, want)
		}
	}
}
