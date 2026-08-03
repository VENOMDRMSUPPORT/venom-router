package providers

import (
	"errors"
	"testing"
)

// This suite reuses fakeOAuthAdapter / fakeAPIKeyAdapter from registry_test.go.

// TestRegister_WireSchemaValidation is mutation row 2: a native_oauth
// Definition MUST carry a valid WireSchema, and any other transport MUST leave
// it empty. Both directions are fail-closed.
func TestRegister_WireSchemaValidation(t *testing.T) {
	cases := []struct {
		name      string
		transport TransportKind
		schema    WireSchema
		oauth     bool
		wantErr   bool
	}{
		{"native_oauth with valid schema", TransportKindNativeOAuth, WireSchemaAnthropicMessages, true, false},
		{"native_oauth with no schema", TransportKindNativeOAuth, "", true, true},
		{"native_oauth with unknown schema", TransportKindNativeOAuth, WireSchema("responses"), true, true},
		{"openai_compatible with a schema", TransportKindOpenAICompatible, WireSchemaOpenAIChat, false, true},
		{"openai_compatible with no schema", TransportKindOpenAICompatible, "", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := NewRegistry()
			def := Definition{ID: "p", Transport: tc.transport, WireSchema: tc.schema}
			if tc.oauth {
				def.AuthMode = AuthModeOAuth
				def.OAuth = fakeOAuthAdapter{}
			} else {
				def.AuthMode = AuthModeAPIKey
				def.APIKey = fakeAPIKeyAdapter{}
			}
			err := reg.Register(def)
			if tc.wantErr && err == nil {
				t.Fatalf("Register(%s) error = nil, want a rejection", tc.name)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("Register(%s) error = %v, want nil", tc.name, err)
			}
		})
	}
}

// TestParseWireSchema_ClosedVocabulary is mutation row 1: ParseWireSchema
// accepts EXACTLY the three values with a live consumer this batch, and fails
// closed on "" and on any unknown value — an empty or unrecognized schema must
// never resolve to a default (that would send one provider's request in another
// provider's wire protocol).
func TestParseWireSchema_ClosedVocabulary(t *testing.T) {
	valid := []WireSchema{
		WireSchemaGoogleGenerateContent,
		WireSchemaAnthropicMessages,
		WireSchemaOpenAIChat,
	}
	for _, v := range valid {
		got, err := ParseWireSchema(string(v))
		if err != nil || got != v {
			t.Fatalf("ParseWireSchema(%q) = (%q, %v), want (%q, nil)", v, got, err, v)
		}
	}

	for _, bad := range []string{"", "gemini", "openai", "anthropic", "responses", "unknown"} {
		if _, err := ParseWireSchema(bad); !errors.Is(err, ErrUnknownWireSchema) {
			t.Fatalf("ParseWireSchema(%q) error = %v, want ErrUnknownWireSchema (fail closed)", bad, err)
		}
	}
}
