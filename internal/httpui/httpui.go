// Package httpui embeds and serves the compiled React dashboard with
// SPA fallback (01 §1/§3, P2a-UI-001). It is a pure handler package: it
// knows nothing about the loopback + Host-allowlist network gate —
// internal/httpapi composes that in, exactly as it already does for
// /health.
package httpui

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// distDir embeds internal/httpui/dist — populated by the
// `dashboard:build-embed` Taskfile task, which builds dashboard/ (Vite)
// and copies its dist/ output here. Only the compile-time
// .buildmarker placeholder is tracked in git (see dist/.gitignore); the
// real build output is gitignored transient state. The "all:" prefix is
// required so the directory match succeeds (and the package compiles)
// even before any real build has run, since a plain (non-"all:")
// pattern excludes dotfile-named entries and .buildmarker would
// otherwise be the only thing there.
//
//go:embed all:dist
var distDir embed.FS

// New builds the dashboard SPA handler from the embedded dashboard
// build output. It fails closed — returns a non-nil error and a nil
// handler — if the embedded tree is the un-built placeholder rather
// than a real dashboard build: never serve an empty/placeholder SPA
// silently (P2a-UI-001's fail-closed DoD).
func New() (http.Handler, error) {
	sub, err := fs.Sub(distDir, "dist")
	if err != nil {
		return nil, fmt.Errorf("httpui: embedded dist root: %w", err)
	}
	return newSPAHandler(sub)
}

// newSPAHandler is the testable core of New: given any fs.FS rooted at
// the dashboard's built asset tree, it validates the tree looks like a
// real build (non-empty index.html plus a populated assets/ directory)
// and, only then, returns a handler that serves assets by path with SPA
// fallback to index.html for unknown, non-asset, non-hidden routes.
// Directories and dotfiles are never served directly — both fall
// through to the SPA fallback — so there is no directory listing and no
// accidental exposure of the build placeholder marker.
func newSPAHandler(fsys fs.FS) (http.Handler, error) {
	indexBytes, err := fs.ReadFile(fsys, "index.html")
	if err != nil {
		return nil, fmt.Errorf(
			"httpui: embedded dashboard build missing (no index.html) — run `task dashboard:build-embed` before building the binary: %w",
			err,
		)
	}
	if len(bytes.TrimSpace(indexBytes)) == 0 {
		return nil, errors.New("httpui: embedded index.html is empty — the dashboard build did not produce real output")
	}

	assetEntries, err := fs.ReadDir(fsys, "assets")
	if err != nil || len(assetEntries) == 0 {
		return nil, errors.New(
			"httpui: embedded dashboard has no built assets (assets/ missing or empty) — run `task dashboard:build-embed` before building the binary",
		)
	}

	fileServer := http.FileServer(http.FS(fsys))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isServableAsset(fsys, r.URL.Path) {
			fileServer.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(indexBytes)
	}), nil
}

// isServableAsset reports whether urlPath names a real, non-hidden file
// (never a directory) in fsys — the only case http.FileServer should
// handle directly. Everything else (directories, dotfiles, unknown
// paths) is the SPA's job via the index.html fallback.
func isServableAsset(fsys fs.FS, urlPath string) bool {
	clean := path.Clean(urlPath)
	rel := strings.TrimPrefix(clean, "/")
	if rel == "" || rel == "." {
		return false
	}
	for _, part := range strings.Split(rel, "/") {
		if strings.HasPrefix(part, ".") {
			return false
		}
	}
	info, err := fs.Stat(fsys, rel)
	if err != nil || info.IsDir() {
		return false
	}
	return true
}
