package execution

// WireSchema is the execution-side mirror of providers.WireSchema
// (P7-EXEC-001 part 2). internal/execution may never import internal/providers
// (layering), so the two vocabularies are maintained in parallel and kept in
// sync by the byte-value test in internal/httpapi/transportresolver_test.go —
// exactly like TransportType mirrors providers.TransportKind.
//
// The native_oauth transport serves several wire PROTOCOLS behind one
// OAuth-bearer transport; ResolvedRoute.WireSchema (set from the provider's
// catalog Definition, never a slug switch) selects which mapping runs.
type WireSchema string

const (
	WireSchemaGoogleGenerateContent WireSchema = "google_generate_content"
	WireSchemaAnthropicMessages     WireSchema = "anthropic_messages"
	WireSchemaOpenAIChat            WireSchema = "openai_chat"
)
