package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestClinePassGetSeam_WorkosPrefixAndHeaders proves the adapter's own
// authenticated GETs carry Authorization: Bearer workos:<token> (prefix applied
// at the wire) and the cline headers, and return the raw status.
func TestClinePassGetSeam_WorkosPrefixAndHeaders(t *testing.T) {
	var h http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"clineUserId":"u"}`))
	}))
	t.Cleanup(srv.Close)

	status, _, err := clinePassGetSeam(context.Background(), srv.URL+"/api/v1/users/me", "rawtoken")
	if err != nil {
		t.Fatalf("seam error = %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if h.Get("Authorization") != "Bearer workos:rawtoken" {
		t.Fatalf("Authorization = %q, want 'Bearer workos:rawtoken'", h.Get("Authorization"))
	}
	if h.Get("X-CLIENT-TYPE") != "venom-router" || h.Get("X-Title") != "Cline" || h.Get("HTTP-Referer") != "https://cline.bot" {
		t.Fatalf("cline headers missing: %v", h)
	}
}

// TestClinePassGetSeam_WorkosPrefixIdempotent proves an already-prefixed token
// is not double-prefixed.
func TestClinePassGetSeam_WorkosPrefixIdempotent(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	if _, _, err := clinePassGetSeam(context.Background(), srv.URL, "workos:already"); err != nil {
		t.Fatalf("seam error = %v", err)
	}
	if auth != "Bearer workos:already" {
		t.Fatalf("Authorization = %q, want no double prefix", auth)
	}
}
