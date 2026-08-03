package httpapi

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/routing"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/secrets"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// publicTierOrder is the deterministic, exhaustive list of the three PUBLIC
// tier model names, derived from the internal/routing tier vocabulary rather
// than three bare string literals — so a tier rename in routing propagates
// here and a new/removed tier fails the exactly-three gate rather than
// silently desynchronizing the public surface (P5-PAPI-003, 01 §6b). The
// public name is "venom/<tier>"; NO provider, account, or raw provider model
// id is ever exposed — these are TIER names, not provider models.
var publicTierOrder = []routing.Tier{routing.TierLite, routing.TierPro, routing.TierMax}

// publicModelName maps a routing tier to its public "venom/<tier>" model id.
func publicModelName(t routing.Tier) string { return "venom/" + string(t) }

// modelsListHandler serves GET /v1/models (P5-PAPI-003). It is a pure,
// DB-free handler: the tier list is a fixed function of the routing vocabulary
// and reads no catalog, offering, account, or database row at all.
type modelsListHandler struct{}

func newModelsListHandler() *modelsListHandler { return &modelsListHandler{} }

// ServeModels writes the OpenAI-compatible model list: {"object":"list",
// "data":[{"id":"venom/lite","object":"model","owned_by":"venom"},...]} with
// exactly the three tier ids in publicTierOrder. created is a fixed 0 (no
// wall clock, no per-call variation) — a tier model name has no creation
// instant. It never touches a database.
func (h *modelsListHandler) ServeModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writePublicError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	data := make([]map[string]any, 0, len(publicTierOrder))
	for _, tier := range publicTierOrder {
		data = append(data, map[string]any{
			"id":       publicModelName(tier),
			"object":   "model",
			"created":  0,
			"owned_by": "venom",
		})
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
}

// publicNotFound is the /v1/ catch-all: any unknown /v1/... path is a plain
// public 404, so no unknown data-plane path falls through to the SPA or any
// control surface.
func publicNotFound(w http.ResponseWriter, _ *http.Request) {
	writePublicError(w, http.StatusNotFound, "not_found", "not found")
}

// registerPublicRoutes mounts the vk-gated /v1/* surface onto mux. Each route
// is wrapped by outer (the loopback + Host-allowlist network gate on the
// shared control listener; the identity wrapper on a standalone off-host
// data-plane listener) and THEN by vk authentication. The /v1/ catch-all sits
// behind outer only (an unknown path is a 404 regardless of key), so a probe
// of a bogus /v1 path learns nothing.
// chat, when non-nil, is the vk-gated POST /v1/chat/completions handler
// (P5-PAPI-002). Both listeners wire it in production — the shared control
// listener via ControlMux and a standalone data-plane listener via PublicMux
// (which needs the keyring for credential decryption). It is nil only when no
// keyring is available, in which case the route is absent rather than present
// and broken.
func registerPublicRoutes(mux *http.ServeMux, outer func(http.Handler) http.Handler, vk *vkAuthenticator, chat http.Handler) {
	models := newModelsListHandler()
	mux.Handle("GET /v1/models", outer(vk.Middleware(http.HandlerFunc(models.ServeModels))))
	if chat != nil {
		mux.Handle("POST /v1/chat/completions", outer(vk.Middleware(chat)))
	}
	mux.Handle("/v1/", outer(http.HandlerFunc(publicNotFound)))
}

// PublicMux builds the STANDALONE public-only data-plane mux for a separate
// data-plane bind (01 §6b/§8): /v1/* behind vk authentication and NOTHING
// else — no control routes, no SPA, no /health. There is NO loopback network
// gate here (the data-plane bind may be off-host), so vk auth is the sole
// authenticator; the outer wrapper is the identity. now is injectable for
// deterministic RPM tests; nil uses the wall clock.
//
// kr is the process keyring the chat handler needs to decrypt credentials. It
// MUST be threaded here: a standalone data-plane listener exists precisely to
// serve the public inference API, so omitting the chat route would 404 the
// PRIMARY endpoint (POST /v1/chat/completions) in exactly the mode the separate
// bind is for — while /v1/models kept working, which makes the hole look like a
// routing quirk rather than a missing feature. A nil kr still yields a mux (with
// /v1/models only) rather than panicking, but production always passes one.
func PublicMux(db *storage.DB, kr *secrets.Keyring, reg *providers.Registry, now func() time.Time) http.Handler {
	return publicMux(db, kr, reg, now)
}

// publicMux is PublicMux's clock-injectable core.
func publicMux(db *storage.DB, kr *secrets.Keyring, reg *providers.Registry, now func() time.Time) http.Handler {
	mux := http.NewServeMux()
	vk := newVKAuthenticator(storage.NewAPIKeyRepo(db), now)
	var chat http.Handler
	if kr != nil {
		if reg == nil {
			reg = providers.NewRegistry()
			_ = registerOpenCodeZen(reg)
			_ = registerOllamaCloud(reg)
			_ = registerAgnesAI(reg)
			_ = registerNvidiaNIM(reg)
			_ = registerGeminiCLI(reg)
			_ = registerClaudeCode(reg)
			_ = registerClinePass(reg)
			_ = registerAntigravityIfConfigured(reg)
		}
		chat = buildChatCompletionsHandler(db, kr, reg)
	}
	registerPublicRoutes(mux, func(h http.Handler) http.Handler { return h }, vk, chat)
	// Per-path per-IP ingress limiter (P5-PAPI-005, 05 §6): same contract as the
	// control listener, independent of the per-key RPM vk auth enforces here.
	return newIngressLimiter(0, 0, nil).Middleware(mux)
}
