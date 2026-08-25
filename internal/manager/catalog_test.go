package manager

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCatalogAdapterAcceptsStaleHealthResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/health" {
			t.Fatalf("path = %s, want /v1/health", r.URL.Path)
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"api":{"contractVersion":"v1"},"service":{"status":"up","databaseReadable":true,"syncInFlight":false},"catalog":{"status":"stale","liveModels":12,"methodologyVersion":"m-1","staleAfterHours":24,"staleProviders":[],"providers":[]},"lastSync":null}`))
	}))
	defer server.Close()

	adapter, err := NewCatalogAdapter(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	health, err := adapter.Health(context.Background())
	if err != nil {
		t.Fatalf("Health() error = %v", err)
	}
	if health.HTTPStatus != http.StatusServiceUnavailable {
		t.Fatalf("HTTPStatus = %d, want 503", health.HTTPStatus)
	}
	if health.CatalogStatus != "stale" || health.LiveModels != 12 {
		t.Fatalf("health = %+v, want stale catalog with 12 models", health)
	}
}

func TestCatalogAdapterRejectsInvalidResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not-json"))
	}))
	defer server.Close()

	adapter, err := NewCatalogAdapter(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Health(context.Background()); err == nil {
		t.Fatal("Health() error = nil, want invalid response error")
	}
}

func TestCatalogAdapterReadyCheckRequiresServiceReadiness(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"api":{"contractVersion":"v1"},"service":{"status":"degraded","databaseReadable":true},"catalog":{"status":"stale","liveModels":1,"staleProviders":[],"providers":[]},"lastSync":null}`))
	}))
	defer server.Close()

	adapter, err := NewCatalogAdapter(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.ReadyCheck(context.Background()); err == nil {
		t.Fatal("ReadyCheck() error = nil, want degraded service error")
	}
}
