package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
)

// agnesTwoStepServer serves GET /models (with modelsStatus/modelsBody) and
// POST /chat/completions (with chatStatus), recording whether the chat call
// was made.
func agnesTwoStepServer(t *testing.T, modelsStatus int, modelsBody string, chatStatus int) (*httptest.Server, *bool) {
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

// TestAgnesChatProbeSeam_ValidatesViaChatNotModelsStatus is mutation row 3: a
// key whose /models answers 200 but whose /chat/completions answers 401 is
// INVALID — validation is the authentic chat call, never the models-list
// status.
func TestAgnesChatProbeSeam_ValidatesViaChatNotModelsStatus(t *testing.T) {
	srv, chatCalled := agnesTwoStepServer(t, http.StatusOK, `{"data":[{"id":"m1"}]}`, http.StatusUnauthorized)

	status := providers.ValidateAPIKey(context.Background(), agnesChatProbeSeam, srv.URL, "garbage")
	if !*chatCalled {
		t.Fatal("the chat-completions endpoint was never called — validation must exercise the chat probe")
	}
	if status != providers.ValidationInvalid {
		t.Fatalf("classification = %q, want invalid (the chat call answered 401)", status)
	}
}

// TestAgnesChatProbeSeam_EmptyCatalogIsUnavailable is mutation row 7: when the
// model list is empty, validation is UNAVAILABLE, never invalid (a missing
// catalog says nothing about the key).
func TestAgnesChatProbeSeam_EmptyCatalogIsUnavailable(t *testing.T) {
	srv, chatCalled := agnesTwoStepServer(t, http.StatusOK, `{"data":[]}`, http.StatusOK)

	status := providers.ValidateAPIKey(context.Background(), agnesChatProbeSeam, srv.URL, "k")
	if *chatCalled {
		t.Fatal("chat call should not happen when the catalog is empty")
	}
	if status != providers.ValidationUnavailable {
		t.Fatalf("classification = %q, want unavailable (empty catalog is not an invalid key)", status)
	}
}

// TestAgnesChatProbeSeam_ValidKey proves a healthy two-step exchange (models
// 200 with a model, chat 2xx) classifies as valid.
func TestAgnesChatProbeSeam_ValidKey(t *testing.T) {
	srv, _ := agnesTwoStepServer(t, http.StatusOK, `{"data":[{"id":"m1"}]}`, http.StatusOK)
	if status := providers.ValidateAPIKey(context.Background(), agnesChatProbeSeam, srv.URL, "k"); status != providers.ValidationValid {
		t.Fatalf("classification = %q, want valid", status)
	}
}
