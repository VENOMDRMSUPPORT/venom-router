package tray

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServerAdapter_DashboardURL(t *testing.T) {
	a := NewServerLifecycle("127.0.0.1:8081", nil)
	if got := a.DashboardURL(); got != "http://127.0.0.1:8081/" {
		t.Fatalf("DashboardURL=%q", got)
	}
}

func TestServerAdapter_Healthy_ProbesHealthEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	bind := strings.TrimPrefix(srv.URL, "http://")
	a := NewServerLifecycle(bind, nil)
	if !a.Healthy(context.Background()) {
		t.Fatalf("Healthy=false, want true against a live /health")
	}
}
