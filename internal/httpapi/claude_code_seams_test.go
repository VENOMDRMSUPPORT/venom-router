package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestClaudeCodeGetSeam_RequiredHeaders proves the authenticated GET carries
// the claude-code required headers (03 §3): missing any is a 429 outage. It
// also proves the raw status + body are returned unfolded (the adapter, not the
// seam, classifies) and that the per-call anthropic-beta value is passed
// through untouched.
func TestClaudeCodeGetSeam_RequiredHeaders(t *testing.T) {
	var h http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h = r.Header.Clone()
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"account":{}}`))
	}))
	t.Cleanup(srv.Close)

	status, body, err := claudeCodeGetSeam(context.Background(), srv.URL+"/api/oauth/profile", "at-secret", "oauth-2025-04-20")
	if err != nil {
		t.Fatalf("seam error = %v", err)
	}
	if status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 returned raw (seam must not fold non-2xx into an error)", status)
	}
	if len(body) == 0 {
		t.Fatal("body not returned")
	}
	if h.Get("Authorization") != "Bearer at-secret" {
		t.Errorf("Authorization = %q, want Bearer at-secret", h.Get("Authorization"))
	}
	if h.Get("anthropic-version") == "" {
		t.Error("anthropic-version header missing")
	}
	if h.Get("anthropic-beta") != "oauth-2025-04-20" {
		t.Errorf("anthropic-beta = %q, want the per-call value passed through", h.Get("anthropic-beta"))
	}
	if h.Get("X-App") != "cli" {
		t.Errorf("X-App = %q, want cli", h.Get("X-App"))
	}
	if !strings.HasPrefix(h.Get("User-Agent"), "claude-cli/") {
		t.Errorf("User-Agent = %q, want claude-cli/ prefix", h.Get("User-Agent"))
	}
}

// TestClaudeCodeTokenSeam_PostsJSON proves the token seam POSTs the JSON body
// (03 §3's "JSON token exchange" — the form-encoded variant was the drift the
// legacy reference corrected).
func TestClaudeCodeTokenSeam_PostsJSON(t *testing.T) {
	var gotCT string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{"access_token":"x"}`))
	}))
	t.Cleanup(srv.Close)

	if _, err := claudeCodeTokenSeam(context.Background(), srv.URL, []byte(`{"grant_type":"authorization_code"}`)); err != nil {
		t.Fatalf("seam error = %v", err)
	}
	if gotCT != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", gotCT)
	}
	if string(gotBody) != `{"grant_type":"authorization_code"}` {
		t.Fatalf("body = %q, want the JSON body passed through", gotBody)
	}
}
