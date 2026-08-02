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
	"strings"
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
	// zenProbeCreditsErrorBody reproduces the recorded live 401 for a VALID
	// key with a zero workspace balance — the provider could only compute the
	// balance after recognizing the key, so this proves authentication.
	zenProbeCreditsErrorBody = `{"type":"error","error":{"type":"CreditsError","message":"Insufficient balance. Manage your billing here: https://opencode.ai/workspace/ws-abc123/billing"}}`
	// zenProbeUnknownErrorBody is a 401 whose error type none of the
	// classifiers recognise — it must fall to unavailable, never invalid.
	zenProbeUnknownErrorBody = `{"type":"error","error":{"type":"TeapotError","message":"I am a teapot and cannot brew coffee"}}`
)

// zenProbeMessageFragments are distinctive substrings of the provider error
// messages above. No fragment may ever appear in a value that leaves the seam
// (a returned error string, a status code carries none). Used by the
// message-never-leaks canary.
var zenProbeMessageFragments = []string{
	"Insufficient balance",
	"opencode.ai/workspace",
	"billing",
	"Invalid API key",
	"Missing API key",
	"not supported",
	"teapot",
}

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

// zenProbeChatHandlerAuthedResponse behaves like zenProbeChatHandler for the
// model check and the missing/wrong-key checks, but when a VALID model AND the
// correct key are presented — the point at which authentication has succeeded
// — it returns the given status and body instead of 200. This reproduces the
// cases where a recognized key still draws a 401 (e.g. CreditsError billing,
// or an unrecognised error type).
func zenProbeChatHandlerAuthedResponse(validModel, validKey string, authedStatus int, authedBody string, capture *zenProbeCapture) http.HandlerFunc {
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
			w.WriteHeader(authedStatus)
			_, _ = io.WriteString(w, authedBody)
		}
	}
}

// TestOpenCodeZenChatProbeSeam_CreditsErrorAuthenticatesEnrolls proves the
// first change: a 401 CreditsError (valid key, zero balance) is treated as
// proof authentication SUCCEEDED, so the key classifies valid and enrollment
// proceeds — the inability to spend belongs to the funding/quota layers, not
// credential validation. Mutation: classify CreditsError as invalid -> this
// test goes RED.
func TestOpenCodeZenChatProbeSeam_CreditsErrorAuthenticatesEnrolls(t *testing.T) {
	const validModel, validKey = "catalog-opus-x", "genuine-key"
	capture := &zenProbeCapture{}
	server := zenProbeServer(t, zenProbeModelsHandler(validModel),
		zenProbeChatHandlerAuthedResponse(validModel, validKey, http.StatusUnauthorized, zenProbeCreditsErrorBody, capture))

	status := providers.ValidateAPIKey(context.Background(), openCodeZenChatProbeSeam, server.URL, validKey)
	if !capture.sawAuth() {
		t.Fatalf("the probe never reached the endpoint with a valid model, so the CreditsError path was not actually exercised")
	}
	if status != providers.ValidationValid {
		t.Fatalf("a 401 CreditsError (valid key, no balance) classified %q, want valid — the balance lookup proves the key was recognized; the empty balance is a funding concern, not a bad credential", status)
	}
}

// TestOpenCodeZenChatProbeSeam_MissingKeyAuthErrorInvalid proves the second
// AuthError case ("Missing API key.") still classifies invalid. The seam
// always sends a bearer header, so the provider's own no-auth branch is
// unreachable end-to-end; the fixture therefore feeds that exact envelope on
// the authed path to pin the classifier on the "Missing API key." wording.
// TestOpenCodeZen..._WrongKeyWithValidModelStillInvalid pins "Invalid API key."
func TestOpenCodeZenChatProbeSeam_MissingKeyAuthErrorInvalid(t *testing.T) {
	const validModel, key = "catalog-opus-x", "genuine-key"
	capture := &zenProbeCapture{}
	server := zenProbeServer(t, zenProbeModelsHandler(validModel),
		zenProbeChatHandlerAuthedResponse(validModel, key, http.StatusUnauthorized, zenProbeAuthMissingBody, capture))

	status := providers.ValidateAPIKey(context.Background(), openCodeZenChatProbeSeam, server.URL, key)
	if status != providers.ValidationInvalid {
		t.Fatalf("an AuthError \"Missing API key.\" response classified %q, want invalid — a clear missing-key message is a positive auth signal", status)
	}
}

// TestOpenCodeZenChatProbeSeam_Unrecognised401Unavailable proves the inverted
// default: a 401 whose error type none of the classifiers recognise must be
// unavailable, never invalid — falsely rejecting a good credential is an
// unrecoverable dead end. Mutation: restore the old fall-through so an
// unrecognised 401 becomes invalid -> this test goes RED.
func TestOpenCodeZenChatProbeSeam_Unrecognised401Unavailable(t *testing.T) {
	const validModel, key = "catalog-opus-x", "genuine-key"
	capture := &zenProbeCapture{}
	server := zenProbeServer(t, zenProbeModelsHandler(validModel),
		zenProbeChatHandlerAuthedResponse(validModel, key, http.StatusUnauthorized, zenProbeUnknownErrorBody, capture))

	status := providers.ValidateAPIKey(context.Background(), openCodeZenChatProbeSeam, server.URL, key)
	if status != providers.ValidationUnavailable {
		t.Fatalf("an unrecognised 401 error type classified %q, want unavailable — only a positive auth signal may produce invalid", status)
	}
}

// TestOpenCodeZenChatProbeSeam_ProviderMessageNeverLeaves is the package-
// boundary canary: for every 401 variant, the seam's returned value (status +
// error) must carry none of the provider's message text. The seam is the only
// place that reads the body, so if nothing textual leaves here, nothing
// downstream (payload, log, error) can carry it.
func TestOpenCodeZenChatProbeSeam_ProviderMessageNeverLeaves(t *testing.T) {
	const validModel, key = "catalog-opus-x", "genuine-key"
	bodies := map[string]string{
		"credits":      zenProbeCreditsErrorBody,
		"authInvalid":  zenProbeAuthInvalidBody,
		"modelError":   zenProbeModelErrorBody,
		"unrecognised": zenProbeUnknownErrorBody,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			capture := &zenProbeCapture{}
			server := zenProbeServer(t, zenProbeModelsHandler(validModel),
				zenProbeChatHandlerAuthedResponse(validModel, key, http.StatusUnauthorized, body, capture))

			_, err := openCodeZenChatProbeSeam(context.Background(), server.URL, key)
			got := ""
			if err != nil {
				got = err.Error()
			}
			for _, frag := range zenProbeMessageFragments {
				if strings.Contains(got, frag) {
					t.Fatalf("seam leaked provider message fragment %q in its returned error %q", frag, got)
				}
			}
		})
	}
}
