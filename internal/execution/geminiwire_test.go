package execution

import (
	"errors"
	"testing"
)

// TestGeminiWire_ResponseFormatFailsClosed proves buildGeminiRequest fails
// closed on ResponseFormat rather than silently dropping it — silently
// dropping it would return prose to a caller that requires JSON.
func TestGeminiWire_ResponseFormatFailsClosed(t *testing.T) {
	_, err := buildGeminiRequest(NormalizedRequest{
		Messages:       []Message{{Role: "user", Content: "hi"}},
		ResponseFormat: "json_object",
	})
	if !errors.Is(err, ErrRequestFeatureUnsupported) {
		t.Fatalf("err = %v, want ErrRequestFeatureUnsupported — silently dropping response_format would return prose to a caller that requires JSON", err)
	}
}
