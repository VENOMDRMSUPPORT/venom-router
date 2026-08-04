package httpapi

import "github.com/VENOMDRMSUPPORT/venom-router/internal/providers"

// newProviderRegistry builds the provider adapter registry over the real
// HTTP seams — the ONE list of live registrations. ControlMux (the request
// path) and the background ticks (token refresh) both build from here, so a
// newly registered provider is automatically visible to every consumer and
// the two can never drift.
//
// Registration errors are safely discardable at every call: each register*
// helper only fails on a duplicate ID (impossible on a fresh registry) or a
// structurally invalid Definition (pinned by that provider's own tests), and
// antigravity deliberately registers nothing when its confidential-client
// env vars are absent — see each helper's doc comment.
func newProviderRegistry() *providers.Registry {
	reg := providers.NewRegistry()
	// antigravity (P2b-PROV-007): confidential OAuth client — registered only
	// when its client id/secret env vars are both configured.
	_ = registerAntigravityIfConfigured(reg)
	// opencode-zen (P2b-PROV-005/CAPI-003): API-key adapter.
	_ = registerOpenCodeZen(reg)
	// ollama-cloud (P7-PROV-006): API-key adapter, native /api/me validation.
	_ = registerOllamaCloud(reg)
	// agnes-ai (P7-PROV-008): OpenAI-compatible API-key adapter.
	_ = registerAgnesAI(reg)
	// nvidia-nim (P7-PROV-009): OpenAI-compatible API-key adapter.
	_ = registerNvidiaNIM(reg)
	// gemini-cli (P7-PROV-007): Google schema over the native_api transport.
	_ = registerGeminiCLI(reg)
	// claude-code (P7-PROV-001): public OAuth client, anthropic_messages wire
	// schema over native_oauth.
	_ = registerClaudeCode(reg)
	// clinepass (P7-PROV-004): active-subscription OAuth extension flow,
	// openai_chat wire schema; funding policy is paid-locked.
	_ = registerClinePass(reg)
	return reg
}
