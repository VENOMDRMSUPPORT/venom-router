package httpui

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

// builtDist returns a fake dashboard build tree: a real index.html plus
// an assets/ directory, exactly what `npm run build` + the embed copy
// step produce — the shape newSPAHandler must accept.
func builtDist() fstest.MapFS {
	return fstest.MapFS{
		"index.html":         {Data: []byte("<!doctype html><html><body>venom dashboard</body></html>")},
		"assets/app-abc1.js": {Data: []byte("console.log('venom');")},
	}
}

// TestNewSPAHandler_FailsClosed_MissingIndex proves construction fails
// closed (returns an error, no handler) when the embedded tree has no
// index.html at all — the "dashboard build never ran" case (01
// §1/§3, P2a-UI-001's fail-closed DoD).
func TestNewSPAHandler_FailsClosed_MissingIndex(t *testing.T) {
	fsys := fstest.MapFS{
		".buildmarker": {Data: []byte("placeholder\n")},
	}

	h, err := newSPAHandler(fsys)
	if err == nil {
		t.Fatalf("newSPAHandler() succeeded with no index.html, want a fail-closed error")
	}
	if h != nil {
		t.Fatalf("newSPAHandler() returned a non-nil handler alongside an error")
	}
}

// TestNewSPAHandler_FailsClosed_EmptyIndex proves an index.html present
// but empty (a corrupt/truncated build) also fails closed rather than
// serving an empty SPA silently.
func TestNewSPAHandler_FailsClosed_EmptyIndex(t *testing.T) {
	fsys := fstest.MapFS{
		"index.html":         {Data: []byte("   \n")},
		"assets/app-abc1.js": {Data: []byte("console.log('venom');")},
	}

	h, err := newSPAHandler(fsys)
	if err == nil {
		t.Fatalf("newSPAHandler() succeeded with a blank index.html, want a fail-closed error")
	}
	if h != nil {
		t.Fatalf("newSPAHandler() returned a non-nil handler alongside an error")
	}
}

// TestNewSPAHandler_FailsClosed_NoAssets proves a tree with only the
// committed compile-time placeholder (index.html present, no assets/
// directory) — the exact shape of a fresh checkout that never ran the
// dashboard build+embed task — fails closed instead of serving the
// placeholder as if it were the real dashboard.
func TestNewSPAHandler_FailsClosed_NoAssets(t *testing.T) {
	fsys := fstest.MapFS{
		"index.html": {Data: []byte("<!doctype html><html><body>placeholder, not built</body></html>")},
	}

	h, err := newSPAHandler(fsys)
	if err == nil {
		t.Fatalf("newSPAHandler() succeeded with no assets/ dir, want a fail-closed error")
	}
	if h != nil {
		t.Fatalf("newSPAHandler() returned a non-nil handler alongside an error")
	}
}

// TestNewSPAHandler_ServesIndexAtRoot proves GET / serves the real
// embedded index.html with a 200 and an HTML content type.
func TestNewSPAHandler_ServesIndexAtRoot(t *testing.T) {
	h, err := newSPAHandler(builtDist())
	if err != nil {
		t.Fatalf("newSPAHandler() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Body.String(); got != "<!doctype html><html><body>venom dashboard</body></html>" {
		t.Fatalf("GET / body = %q, want the embedded index.html content", got)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Fatalf("GET / Content-Type = %q, want text/html; charset=utf-8", ct)
	}
}

// TestNewSPAHandler_ServesRealAssetWithCorrectType proves a real
// embedded asset is served by path with a correct Content-Type inferred
// from its extension, not the SPA fallback.
func TestNewSPAHandler_ServesRealAssetWithCorrectType(t *testing.T) {
	h, err := newSPAHandler(builtDist())
	if err != nil {
		t.Fatalf("newSPAHandler() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/assets/app-abc1.js", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /assets/app-abc1.js status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Body.String(); got != "console.log('venom');" {
		t.Fatalf("GET /assets/app-abc1.js body = %q, want the embedded asset content", got)
	}
	ct := rec.Header().Get("Content-Type")
	if ct != "text/javascript; charset=utf-8" && ct != "application/javascript" {
		t.Fatalf("GET /assets/app-abc1.js Content-Type = %q, want a JS content type", ct)
	}
}

// TestNewSPAHandler_SPAFallback_UnknownRoute proves an unknown,
// non-asset route (a client-side SPA route) falls back to index.html
// with 200 — NOT a 404 — so the React router can take over client-side.
func TestNewSPAHandler_SPAFallback_UnknownRoute(t *testing.T) {
	h, err := newSPAHandler(builtDist())
	if err != nil {
		t.Fatalf("newSPAHandler() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/providers/42/edit", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /providers/42/edit status = %d, want %d (SPA fallback)", rec.Code, http.StatusOK)
	}
	if got := rec.Body.String(); got != "<!doctype html><html><body>venom dashboard</body></html>" {
		t.Fatalf("GET /providers/42/edit body = %q, want the index.html fallback", got)
	}
}

// TestNewSPAHandler_NoDirectoryListing proves requesting a directory
// path (e.g. the assets root with a trailing slash) never returns a
// directory listing — it falls back to the SPA index instead.
func TestNewSPAHandler_NoDirectoryListing(t *testing.T) {
	h, err := newSPAHandler(builtDist())
	if err != nil {
		t.Fatalf("newSPAHandler() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/assets/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /assets/ status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	if body != "<!doctype html><html><body>venom dashboard</body></html>" {
		t.Fatalf("GET /assets/ body = %q, want the SPA fallback (no directory listing)", body)
	}
}

// TestNewSPAHandler_NeverServesDotfiles proves a request for a hidden
// file (e.g. the compile-time .buildmarker placeholder) never leaks
// through as a served asset — it falls back to the SPA index instead.
func TestNewSPAHandler_NeverServesDotfiles(t *testing.T) {
	fsys := builtDist()
	fsys[".buildmarker"] = &fstest.MapFile{Data: []byte("placeholder\n")}

	h, err := newSPAHandler(fsys)
	if err != nil {
		t.Fatalf("newSPAHandler() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/.buildmarker", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /.buildmarker status = %d, want %d", rec.Code, http.StatusOK)
	}
	if body := rec.Body.String(); body == "placeholder\n" {
		t.Fatalf("GET /.buildmarker served the raw hidden file instead of the SPA fallback")
	}
}

// TestNew_UsesEmbeddedDist is a thin smoke test for the production
// entry point: it must not panic and must resolve the "dist" embed root
// via fs.Sub before delegating to newSPAHandler. Whether it succeeds or
// fails closed depends on whether the dashboard build+embed task has
// been run in this working tree — both are valid outcomes here; the
// substantive fail-closed/serving behavior is proven above against
// newSPAHandler directly.
func TestNew_UsesEmbeddedDist(t *testing.T) {
	h, err := New()
	if err != nil {
		t.Logf("New() failed closed (dashboard build+embed not present in this tree): %v", err)
		return
	}
	if h == nil {
		t.Fatalf("New() returned a nil handler with a nil error")
	}
}
