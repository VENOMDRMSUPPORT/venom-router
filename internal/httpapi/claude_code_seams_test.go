package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestClaudeCodeGetSeam_RequiredHeaders proves the authenticated GET carries
// the claude-code required headers (03 §3): missing any is a 429 outage. It
// also proves the raw status + body are returned unfolded (the adapter, not the
// seam, classifies).
func TestClaudeCodeGetSeam_RequiredHeaders(t *testing.T) {
	var h http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h = r.Header.Clone()
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"account":{}}`))
	}))
	t.Cleanup(srv.Close)

	status, body, err := claudeCodeGetSeam(context.Background(), srv.URL+"/api/oauth/profile", "at-secret")
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
	if !strings.Contains(h.Get("anthropic-beta"), "oauth-2025-04-20") {
		t.Errorf("anthropic-beta = %q, want to contain oauth-2025-04-20", h.Get("anthropic-beta"))
	}
	if h.Get("X-App") != "cli" {
		t.Errorf("X-App = %q, want cli", h.Get("X-App"))
	}
	if !strings.HasPrefix(h.Get("User-Agent"), "claude-cli/") {
		t.Errorf("User-Agent = %q, want claude-cli/ prefix", h.Get("User-Agent"))
	}
}

// TestClaudeCodeTokenSeam_PostsForm proves the token seam POSTs the form body
// as x-www-form-urlencoded.
func TestClaudeCodeTokenSeam_PostsForm(t *testing.T) {
	var gotCT, gotGrant string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		_ = r.ParseForm()
		gotGrant = r.PostForm.Get("grant_type")
		_, _ = w.Write([]byte(`{"access_token":"x"}`))
	}))
	t.Cleanup(srv.Close)

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	if _, err := claudeCodeTokenSeam(context.Background(), srv.URL, form); err != nil {
		t.Fatalf("seam error = %v", err)
	}
	if gotCT != "application/x-www-form-urlencoded" {
		t.Fatalf("Content-Type = %q, want form-urlencoded", gotCT)
	}
	if gotGrant != "authorization_code" {
		t.Fatalf("grant_type = %q, want authorization_code", gotGrant)
	}
}
