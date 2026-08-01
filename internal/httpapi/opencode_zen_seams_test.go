package httpapi

// opencode_zen_seams_test.go pins the opencode-zen chat-validation probe
// (openCodeZenChatProbeSeam) against httptest servers that reproduce the
// EXACT responses the real https://opencode.ai/zen endpoint was observed to
// return:
//
//   - GET /v1/models is public: 200 with a model list even unauthenticated.
//   - POST /v1/chat/completions validates the MODEL before the key, so:
//       * a body with no/unknown model -> 401 ModelError "Model {{model}} is
//         not supported" (returned even with NO Authorization header);
//       * a well-formed body naming a real model with a wrong/missing key
//         -> 401 AuthError "Invalid API key." / "Missing API key.";
//       * a well-formed body with the correct key -> 200.
//
// The shipped defect: the probe sent {"max_tokens":1} — no model, no messages
// — so every real key hit the ModelError 401 and providers.ValidateAPIKey
// mapped that 401 to invalid, failing enrollment with 422. These tests drive
// the probe through providers.ValidateAPIKey (seam + shared classification =
// the real enrollment decision) and prove the invariants of the fix.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
)

// The three response bodies below reproduce the recorded real opencode-zen
// error envelopes verbatim (type + message). The classifier under test keys
// on these bytes, so reproducing them exactly is what makes the tests honest.
const (
	zenProbeModelErrorBody  = `{"error":{"type":"ModelError","message":"Model {{model}} is not supported"}}`
	zenProbeAuthInvalidBody = `{"error":{"type":"AuthError","message":"Invalid API key."}}`
	zenProbeAuthMissingBody = `{"error":{"type":"AuthError","message":"Missing API key."}}`
	zenProbeChatSuccessBody = `{"id":"chatcmpl-probe","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":""}}]}`
)

// zenProbeCapture records what the fixture chat endpoint actually received, so
// a test can assert the probe sent a catalog-resolved model (not a hardcoded
// one) and actually reached the auth check.
type zenProbeCapture struct {
	mu          sync.Mutex
	models      []string
	reachedAuth bool
}

func (c *zenProbeCapture) recordModel(model string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.models = append(c.models, model)
}

func (c *zenProbeCapture) markReachedAuth() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reachedAuth = true
}

func (c *zenProbeCapture) lastModel() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.models) == 0 {
		return ""
	}
	return c.models[len(c.models)-1]
}

func (c *zenProbeCapture) sawAuth() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reachedAuth
}

// zenProbeModelsHandler serves opencode-zen's public GET /v1/models: 200 with
// the given ids, regardless of the Authorization header. Call with no ids to
// reproduce an empty catalog ({"data":[]}).
func zenProbeModelsHandler(ids ...string) http.HandlerFunc {
	type model struct {
		ID string `json:"id"`
	}
	return func(w http.ResponseWriter, _ *http.Request) {
		payload := struct {
			Data []model `json:"data"`
		}{Data: []model{}}
		for _, id := range ids {
			payload.Data = append(payload.Data, model{ID: id})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}
}

// zenProbeChatHandler reproduces the real endpoint's order of checks: the
// MODEL is validated before the key. Only validModel is accepted; any other
// (including the empty model of a model-less body) yields 401 ModelError even
// with no auth header. With a valid model, a missing key -> 401 Missing, a
// wrong key -> 401 Invalid, the correct key -> 200.
func zenProbeChatHandler(validModel, validKey string, capture *zenProbeCapture) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		var req struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(raw, &req)
		capture.recordModel(req.Model)

		if req.Model == "" || req.Model != validModel {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, zenProbeModelErrorBody)
			return
		}
		capture.markReachedAuth()

		switch auth := r.Header.Get("Authorization"); {
		case auth == "":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, zenProbeAuthMissingBody)
		case auth != "Bearer "+validKey:
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, zenProbeAuthInvalidBody)
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, zenProbeChatSuccessBody)
		}
	}
}

func zenProbeServer(t *testing.T, models, chat http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", models)
	mux.HandleFunc("/v1/chat/completions", chat)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

// TestOpenCodeZenChatProbeSeam_ResolvesModelSoValidKeyEnrolls is the
// defect-pinning test: it FAILS against the shipped model-less probe body
// (which draws a 401 ModelError -> invalid) and passes only once the probe
// resolves a real model first. Mutation: revert the probe to the model-less
// body -> this test goes RED.
func TestOpenCodeZenChatProbeSeam_ResolvesModelSoValidKeyEnrolls(t *testing.T) {
	const validModel, validKey = "catalog-opus-x", "correct-key"
	capture := &zenProbeCapture{}
	server := zenProbeServer(t, zenProbeModelsHandler(validModel), zenProbeChatHandler(validModel, validKey, capture))

	status := providers.ValidateAPIKey(context.Background(), openCodeZenChatProbeSeam, server.URL, validKey)
	if status != providers.ValidationValid {
		t.Fatalf("valid key classified %q, want valid — the probe must resolve a real model before the chat call (the shipped defect sent a model-less body and got a 401 ModelError)", status)
	}
}

// TestOpenCodeZenChatProbeSeam_ModelErrorClassifiedUnavailableNotInvalid
// proves the fail-closed guard: a 401 whose body is a ModelError (a
// model/request-shape problem) must classify as unavailable, never invalid —
// the system must not brand the owner's key bad on non-auth evidence. Here the
// catalog advertises a model the chat endpoint rejects as unsupported (a
// transient catalog/endpoint disagreement). Mutation: remove the model-error
// guard -> this test goes RED.
func TestOpenCodeZenChatProbeSeam_ModelErrorClassifiedUnavailableNotInvalid(t *testing.T) {
	const advertised, accepted, key = "temporarily-unsupported", "some-other-model", "correct-key"
	capture := &zenProbeCapture{}
	server := zenProbeServer(t, zenProbeModelsHandler(advertised), zenProbeChatHandler(accepted, key, capture))

	status := providers.ValidateAPIKey(context.Background(), openCodeZenChatProbeSeam, server.URL, key)
	if status != providers.ValidationUnavailable {
		t.Fatalf("a 401 ModelError classified %q, want unavailable (fail-closed: a model/request-shape problem must never read as an invalid credential)", status)
	}
}

// TestOpenCodeZenChatProbeSeam_UsesCatalogModelNotHardcoded proves the model
// id comes from GET /v1/models, not a constant baked into the probe. The
// served id is a value no one would ever hardcode; the probe must send exactly
// it. Mutation: hardcode any model id in the probe -> this test goes RED.
func TestOpenCodeZenChatProbeSeam_UsesCatalogModelNotHardcoded(t *testing.T) {
	const catalogModel, key = "catalog-served-9f3kx2-model", "correct-key"
	capture := &zenProbeCapture{}
	server := zenProbeServer(t, zenProbeModelsHandler(catalogModel), zenProbeChatHandler(catalogModel, key, capture))

	status := providers.ValidateAPIKey(context.Background(), openCodeZenChatProbeSeam, server.URL, key)
	if got := capture.lastModel(); got != catalogModel {
		t.Fatalf("chat probe sent model %q, want the id resolved from GET /v1/models (%q) — a hardcoded model id is forbidden; the catalog changes", got, catalogModel)
	}
	if status != providers.ValidationValid {
		t.Fatalf("with the catalog model resolved and the correct key, status = %q, want valid", status)
	}
}

// TestOpenCodeZenChatProbeSeam_EmptyModelListUnavailableNotInvalid proves that
// with no model to name, the probe reports unavailable and never guesses a
// model or brands the key invalid. Mutation: make an empty model list report
// invalid -> this test goes RED.
func TestOpenCodeZenChatProbeSeam_EmptyModelListUnavailableNotInvalid(t *testing.T) {
	capture := &zenProbeCapture{}
	server := zenProbeServer(t, zenProbeModelsHandler(), zenProbeChatHandler("any", "correct-key", capture))

	status := providers.ValidateAPIKey(context.Background(), openCodeZenChatProbeSeam, server.URL, "correct-key")
	if status != providers.ValidationUnavailable {
		t.Fatalf("empty model list classified %q, want unavailable — with no model to name, the probe cannot establish anything about the key", status)
	}
	if got := capture.lastModel(); got != "" {
		t.Fatalf("probe issued a chat call (model %q) despite an empty catalog — it must report unavailable without guessing a model", got)
	}
}

// TestOpenCodeZenChatProbeSeam_ModelsReadFailureUnavailable proves that a
// failed models read reports unavailable (retryable), never invalid, and that
// the probe does not fall through to a chat call.
func TestOpenCodeZenChatProbeSeam_ModelsReadFailureUnavailable(t *testing.T) {
	capture := &zenProbeCapture{}
	failing := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) }
	server := zenProbeServer(t, failing, zenProbeChatHandler("any", "correct-key", capture))

	status := providers.ValidateAPIKey(context.Background(), openCodeZenChatProbeSeam, server.URL, "correct-key")
	if status != providers.ValidationUnavailable {
		t.Fatalf("a failed models read classified %q, want unavailable — never invalid", status)
	}
	if got := capture.lastModel(); got != "" {
		t.Fatalf("probe issued a chat call (model %q) after the models read failed — it must report unavailable first", got)
	}
}

// TestOpenCodeZenChatProbeSeam_WrongKeyWithValidModelStillInvalid is the
// other direction of the guard: a genuine 401 AuthError (valid model, wrong
// key) must still classify as invalid — the model-error guard must not swallow
// real auth failures. The sawAuth assertion also proves the probe genuinely
// reached the auth check with a valid model (so this is not a vacuous pass on
// a model-less body).
func TestOpenCodeZenChatProbeSeam_WrongKeyWithValidModelStillInvalid(t *testing.T) {
	const validModel, validKey = "catalog-opus-x", "correct-key"
	capture := &zenProbeCapture{}
	server := zenProbeServer(t, zenProbeModelsHandler(validModel), zenProbeChatHandler(validModel, validKey, capture))

	status := providers.ValidateAPIKey(context.Background(), openCodeZenChatProbeSeam, server.URL, "wrong-key")
	if !capture.sawAuth() {
		t.Fatalf("the chat probe never reached the endpoint's auth check with a valid model — a genuine auth failure was not actually exercised")
	}
	if status != providers.ValidationInvalid {
		t.Fatalf("wrong key with a valid model classified %q, want invalid — the model-error guard must not swallow genuine 401 AuthError responses", status)
	}
}
