package execution

// canary_test.go (governor review addition, P4-EXEC-002): the load-bearing
// secret-safety proof. A credential-shaped token alone proves nothing —
// upstream redaction would catch it anyway — so a PLAIN, non-credential
// marker is planted in the provider error body and asserted absent from
// every owner/client-visible surface (SafeMessage, Error() string,
// Evidence deep-walked, VenomError) while asserted PRESENT in RawMessage,
// the one sanctioned probe-path carrier. Presence in RawMessage is what
// keeps the absence assertions non-vacuous: the body demonstrably reached
// the classification layer and was withheld, not dropped.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const (
	plainCanaryMarker  = "zebra-quilt-9137"
	credentialCanary   = "sk-canary-credential-4242"
	canaryProviderBody = `{"error":{"code":"quota_exhausted","message":"raw provider text zebra-quilt-9137 holding sk-canary-credential-4242","scope":"account"}}`
	geminiCanaryBody   = `{"error":{"code":429,"status":"quota_exhausted","message":"raw provider text zebra-quilt-9137 holding sk-canary-credential-4242","scope":"account"}}`
)

// assertNoCanaryOutsideRawMessage walks every non-RawMessage surface of a
// TypedFailure + its VenomError sibling + the error string, and fails if
// either canary appears anywhere; then asserts the plain marker IS in
// RawMessage so the negatives cannot pass by the body being dropped.
func assertNoCanaryOutsideRawMessage(t *testing.T, label string, err error, f TypedFailure, ve VenomError) {
	t.Helper()

	for _, canary := range []string{plainCanaryMarker, credentialCanary} {
		if strings.Contains(f.SafeMessage, canary) {
			t.Errorf("%s: SafeMessage contains %q — raw provider text leaked", label, canary)
		}
		if strings.Contains(err.Error(), canary) {
			t.Errorf("%s: Error() string contains %q — raw provider text leaked", label, canary)
		}
		if strings.Contains(ve.Message, canary) || strings.Contains(ve.Code, canary) {
			t.Errorf("%s: VenomError contains %q — raw provider text leaked", label, canary)
		}
		for k, v := range f.Evidence {
			if strings.Contains(fmt.Sprintf("%v", v), canary) {
				t.Errorf("%s: Evidence[%q] contains %q — raw provider text leaked", label, k, canary)
			}
		}
	}

	if !strings.Contains(f.RawMessage, plainCanaryMarker) {
		t.Errorf("%s: RawMessage does NOT contain the plain marker — the body never reached classification, making the absence assertions vacuous", label)
	}
}

// TestSecretCanary_OpenAICompatible plants the canary body behind a 429
// and proves the marker surfaces ONLY in RawMessage.
func TestSecretCanary_OpenAICompatible(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(canaryProviderBody))
	}))
	t.Cleanup(srv.Close)

	transport := NewOpenAICompatibleTransport(&http.Client{}, 5*time.Second)
	route := ResolvedRoute{Provider: "p", AccountID: "a", ModelID: "m", BaseURL: srv.URL,
		Credential: StoredCredentials{Value: "sk-live-key"}}
	_, err := transport.Execute(t.Context(), route, NormalizedRequest{
		Operation: OperationChat, Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("Execute() error = nil, want the 429 rejection")
	}

	f := transport.Failure(err, route)
	ve := transport.NormalizeError(err, route)
	assertNoCanaryOutsideRawMessage(t, "openai_compatible", err, f, ve)

	// The classification itself must still be the spec row (quota/account),
	// so withholding the text costs no fidelity.
	if f.FailureClass != FailureClassQuota || f.Scope != FailureScopeAccount {
		t.Fatalf("classification = %s/%s, want quota_error/account", f.FailureClass, f.Scope)
	}
}

// TestSecretCanary_NativeOAuth is the same proof through the Gemini-shaped
// transport.
func TestSecretCanary_NativeOAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(geminiCanaryBody))
	}))
	t.Cleanup(srv.Close)

	transport := NewNativeOAuthTransport(&http.Client{}, 5*time.Second)
	route := ResolvedRoute{Provider: "p", AccountID: "a", ModelID: "m", BaseURL: srv.URL,
		Credential: StoredCredentials{Value: "ya29.live-token"}}
	_, err := transport.Execute(t.Context(), route, NormalizedRequest{
		Operation: OperationChat, Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("Execute() error = nil, want the 429 rejection")
	}

	f := transport.Failure(err, route)
	ve := transport.NormalizeError(err, route)
	assertNoCanaryOutsideRawMessage(t, "native_oauth", err, f, ve)

	if f.FailureClass != FailureClassQuota || f.Scope != FailureScopeAccount {
		t.Fatalf("classification = %s/%s, want quota_error/account", f.FailureClass, f.Scope)
	}
}

// TestNormalizeErrorCoherentWithFailure proves the two error shapes are
// derived from ONE classification and can never disagree (P4-EXEC-002):
// for the same error, VenomError's code/message/retryable mirror
// TypedFailure's class/SafeMessage/Retryable — on all three transports.
func TestNormalizeErrorCoherentWithFailure(t *testing.T) {
	httpErr := &openAICompatHTTPError{status: 503, code: "", message: "raw text"}
	geminiErr := &nativeOAuthHTTPError{status: 503, code: "", message: "raw text"}
	bifrostErr := &bifrostExecError{status: 503, code: "", message: "raw text"}

	openai := NewOpenAICompatibleTransport(&http.Client{}, time.Second)
	native := NewNativeOAuthTransport(&http.Client{}, time.Second)
	bf := &BifrostTransport{}

	pairs := []struct {
		name string
		f    TypedFailure
		ve   VenomError
	}{
		{"openai_compatible", openai.Failure(httpErr, ResolvedRoute{}), openai.NormalizeError(httpErr, ResolvedRoute{})},
		{"native_oauth", native.Failure(geminiErr, ResolvedRoute{}), native.NormalizeError(geminiErr, ResolvedRoute{})},
		{"bifrost", bf.Failure(bifrostErr, ResolvedRoute{}), bf.NormalizeError(bifrostErr, ResolvedRoute{})},
	}
	for _, p := range pairs {
		if p.ve.Code != string(p.f.FailureClass) {
			t.Errorf("%s: VenomError.Code = %q, TypedFailure.FailureClass = %q — the two shapes disagree", p.name, p.ve.Code, p.f.FailureClass)
		}
		if p.ve.Message != p.f.SafeMessage {
			t.Errorf("%s: VenomError.Message = %q, TypedFailure.SafeMessage = %q — the two shapes disagree", p.name, p.ve.Message, p.f.SafeMessage)
		}
		if p.ve.Retryable != p.f.Retryable {
			t.Errorf("%s: VenomError.Retryable = %v, TypedFailure.Retryable = %v — the two shapes disagree", p.name, p.ve.Retryable, p.f.Retryable)
		}
	}
}
