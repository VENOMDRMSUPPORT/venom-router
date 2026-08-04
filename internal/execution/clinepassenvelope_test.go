package execution

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestOpenAIChat_DecodesClinepassEnvelopedResponse pins clinepass's REAL
// non-stream completion shape: the OpenAI body is wrapped in the standard
// cline envelope {success, data:{choices...}} (legacy wire reference §7).
// A decoder that only reads top-level choices would fail every clinepass
// chat with "no choices in response".
func TestOpenAIChat_DecodesClinepassEnvelopedResponse(t *testing.T) {
	var cap oauthCapture
	srv := oauthCaptureServer(t, &cap,
		`{"success":true,"data":{"choices":[{"message":{"role":"assistant","content":"enveloped ok"},"finish_reason":"stop"}]}}`)
	tr := NewNativeOAuthTransport(&http.Client{}, 5*time.Second)

	resp, err := tr.Execute(context.Background(), oauthRoute(srv.URL, WireSchemaOpenAIChat, "cline-model", "tok"), NormalizedRequest{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if resp.Message.Content != "enveloped ok" {
		t.Fatalf("Content = %q, want the enveloped data.choices message", resp.Message.Content)
	}
	if resp.FinishReason != "stop" {
		t.Fatalf("FinishReason = %q, want stop", resp.FinishReason)
	}
}

// TestOpenAIChat_BareResponseStillDecodes proves the envelope unwrap is a
// fallback — a bare OpenAI body keeps decoding exactly as before.
func TestOpenAIChat_BareResponseStillDecodes(t *testing.T) {
	var cap oauthCapture
	srv := oauthCaptureServer(t, &cap,
		`{"choices":[{"message":{"role":"assistant","content":"bare ok"},"finish_reason":"stop"}]}`)
	tr := NewNativeOAuthTransport(&http.Client{}, 5*time.Second)

	resp, err := tr.Execute(context.Background(), oauthRoute(srv.URL, WireSchemaOpenAIChat, "m", "tok"), NormalizedRequest{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if resp.Message.Content != "bare ok" {
		t.Fatalf("Content = %q, want bare choices message", resp.Message.Content)
	}
}

// TestOpenAIChat_EnvelopedErrorStringSurfaces pins the cline envelope error
// shape {success:false, error:"..."}: the provider's wording must reach the
// typed failure (the classifier keys on it), not an empty message.
func TestOpenAIChat_EnvelopedErrorStringSurfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"success":false,"error":"insufficient credits"}`))
	}))
	t.Cleanup(srv.Close)
	tr := NewNativeOAuthTransport(&http.Client{}, 5*time.Second)

	_, err := tr.Execute(context.Background(), oauthRoute(srv.URL, WireSchemaOpenAIChat, "m", "tok"), NormalizedRequest{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err == nil {
		t.Fatalf("Execute error = nil, want the 402 failure")
	}
	var httpErr *nativeOAuthHTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("error %T is not a nativeOAuthHTTPError: %v", err, err)
	}
	if httpErr.status != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402", httpErr.status)
	}
	if httpErr.message != "insufficient credits" {
		t.Fatalf("message = %q, want the envelope's error string (the failure classifier keys on it)", httpErr.message)
	}
}
