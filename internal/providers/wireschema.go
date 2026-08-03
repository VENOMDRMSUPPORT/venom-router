package providers

import (
	"errors"
	"fmt"
)

// WireSchema is the SECOND typed, catalog-declared dimension a native_oauth
// provider carries (P7-EXEC-001 part 2). The five-value TransportKind
// vocabulary is CLOSED, yet native_oauth must serve providers whose wire
// PROTOCOLS differ (Google generateContent for antigravity, the Anthropic
// Messages body for claude-code, an OpenAI-shaped body for clinepass), and
// 01 §4.5 forbids selecting behaviour by provider slug. A new transport TYPE
// is not the answer (the vocabulary is closed and these all share the same
// OAuth-bearer transport); a slug switch is forbidden. A typed, catalog-
// declared schema is the only compliant way to pick the wire mapping.
//
// This vocabulary carries EXACTLY the values with a live consumer this batch;
// codex (Responses) and copilot land with their own adapters, so their values
// are deliberately absent — an unreachable value would be the inert-code shape
// the project rejects.
type WireSchema string

const (
	// WireSchemaGoogleGenerateContent is Gemini's generateContent /
	// streamGenerateContent schema (antigravity — existing behaviour).
	WireSchemaGoogleGenerateContent WireSchema = "google_generate_content"
	// WireSchemaAnthropicMessages is Anthropic's /v1/messages schema
	// (claude-code, P7-PROV-001).
	WireSchemaAnthropicMessages WireSchema = "anthropic_messages"
	// WireSchemaOpenAIChat is the OpenAI chat-completions schema served over an
	// OAuth-bearer transport (clinepass, P7-PROV-004).
	WireSchemaOpenAIChat WireSchema = "openai_chat"
)

// ErrUnknownWireSchema is returned by ParseWireSchema for any value outside the
// closed vocabulary above, INCLUDING the empty string. Fail-closed: an unknown
// or missing schema is rejected, never silently accepted or defaulted.
var ErrUnknownWireSchema = errors.New("providers: unrecognized wire schema")

// ParseWireSchema returns the WireSchema for s, or ErrUnknownWireSchema if s is
// not in the closed set. Implemented with if/else — a switch on string-literal
// cases is forbidden (01 §4.5 / 08 §8), exactly as ParseTransportKind is.
func ParseWireSchema(s string) (WireSchema, error) {
	if s == string(WireSchemaGoogleGenerateContent) {
		return WireSchemaGoogleGenerateContent, nil
	}
	if s == string(WireSchemaAnthropicMessages) {
		return WireSchemaAnthropicMessages, nil
	}
	if s == string(WireSchemaOpenAIChat) {
		return WireSchemaOpenAIChat, nil
	}
	return "", fmt.Errorf("%w: %q", ErrUnknownWireSchema, s)
}
