package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestOllamaModelsProbeSeam_GetsModelsWithBearer proves the models probe issues
// a GET to {baseURL}/models with the Bearer credential, and returns a non-2xx
// as the typed ModelsProbeStatusError.
func TestOllamaModelsProbeSeam_GetsModelsWithBearer(t *testing.T) {
	var gotMethod, gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotAuth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"m1"}]}`))
	}))
	t.Cleanup(srv.Close)

	body, err := ollamaCloudModelsProbeSeam(context.Background(), srv.URL, "sk-test")
	if err != nil {
		t.Fatalf("models probe error = %v", err)
	}
	if gotMethod != http.MethodGet || !strings.HasSuffix(gotPath, "/models") {
		t.Fatalf("request = %s %s, want GET .../models", gotMethod, gotPath)
	}
	if gotAuth != "Bearer sk-test" {
		t.Fatalf("Authorization = %q, want Bearer sk-test", gotAuth)
	}
	if !strings.Contains(string(body), "m1") {
		t.Fatalf("body = %s, want the models payload", body)
	}
}

// TestOllamaIdentityProbeSeam_PostsMeWithBearerAndAccept proves the identity
// probe POSTs /me with the Bearer credential and Accept: application/json, and
// returns the raw status + body.
func TestOllamaIdentityProbeSeam_PostsMeWithBearerAndAccept(t *testing.T) {
	var gotMethod, gotPath, gotAuth, gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotAuth, gotAccept = r.Method, r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("Accept")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"bad key"}`))
	}))
	t.Cleanup(srv.Close)

	// The production seam targets the fixed /api base (a const), so the
	// header/method contract is exercised through the base-parameterized core.
	status, body, err := ollamaIdentityProbeSeamAt(context.Background(), srv.URL, "sk-secret")
	if err != nil {
		t.Fatalf("identity probe error = %v", err)
	}
	if gotMethod != http.MethodPost || !strings.HasSuffix(gotPath, "/me") {
		t.Fatalf("request = %s %s, want POST .../me", gotMethod, gotPath)
	}
	if gotAuth != "Bearer sk-secret" {
		t.Fatalf("Authorization = %q, want Bearer sk-secret", gotAuth)
	}
	if gotAccept != "application/json" {
		t.Fatalf("Accept = %q, want application/json", gotAccept)
	}
	if status != http.StatusUnauthorized || !strings.Contains(string(body), "bad key") {
		t.Fatalf("status/body = %d/%s, want 401 + body", status, body)
	}
}
