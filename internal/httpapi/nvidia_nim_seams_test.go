package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
)

func nvidiaTwoStepServer(t *testing.T, modelsStatus int, modelsBody string, chatStatus int) (*httptest.Server, *bool) {
	t.Helper()
	chatCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/models"):
			w.WriteHeader(modelsStatus)
			_, _ = w.Write([]byte(modelsBody))
		case strings.HasSuffix(r.URL.Path, "/chat/completions"):
			chatCalled = true
			w.WriteHeader(chatStatus)
			_, _ = w.Write([]byte(`{}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &chatCalled
}

// TestNvidiaChatProbeSeam_ValidatesViaChat proves a garbage key whose /models
// answers 200 but whose chat answers 401 is INVALID (authentic chat probe).
func TestNvidiaChatProbeSeam_ValidatesViaChat(t *testing.T) {
	srv, chatCalled := nvidiaTwoStepServer(t, http.StatusOK, `{"data":[{"id":"m1"}]}`, http.StatusUnauthorized)
	status := providers.ValidateAPIKey(context.Background(), nvidiaChatProbeSeam, srv.URL, "garbage")
	if !*chatCalled {
		t.Fatal("the chat-completions endpoint was never called")
	}
	if status != providers.ValidationInvalid {
		t.Fatalf("classification = %q, want invalid", status)
	}
}

// TestNvidiaChatProbeSeam_EmptyCatalogIsUnavailable proves an empty model list
// is unavailable, never invalid.
func TestNvidiaChatProbeSeam_EmptyCatalogIsUnavailable(t *testing.T) {
	srv, _ := nvidiaTwoStepServer(t, http.StatusOK, `{"data":[]}`, http.StatusOK)
	if status := providers.ValidateAPIKey(context.Background(), nvidiaChatProbeSeam, srv.URL, "k"); status != providers.ValidationUnavailable {
		t.Fatalf("classification = %q, want unavailable", status)
	}
}
