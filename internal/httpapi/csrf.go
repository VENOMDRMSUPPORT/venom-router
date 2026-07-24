package httpapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
)

// csrfCookieName is the readable (non-HttpOnly) cookie the SPA reads
// and echoes back as the X-CSRF-Token header (09 §5.4).
const csrfCookieName = "XSRF-TOKEN"

// issueCSRFToken derives a session-bound CSRF token from sessionTokenHash
// via HMAC-SHA256 under this process's own random key: deterministic per
// session (so it can be recomputed on any later request against the same
// session, e.g. GET /auth/session re-reporting it after a restart) and
// unforgeable without the key. It is never persisted itself — h.csrfKey
// is the only secret involved, generated once per process and never
// written to storage.
func (h *AuthHandlers) issueCSRFToken(sessionTokenHash []byte) string {
	mac := hmac.New(sha256.New, h.csrfKey)
	mac.Write(sessionTokenHash)
	return hex.EncodeToString(mac.Sum(nil))
}

// setCSRFCookie sets the readable XSRF-TOKEN cookie the SPA echoes back
// as X-CSRF-Token (09 §5.4). Unlike the session cookie it is explicitly
// NOT HttpOnly — client-side script must be able to read it — but
// carries the same Path/SameSite/Secure-when-TLS posture.
func setCSRFCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    token,
		Path:     controlAPIPath,
		HttpOnly: false,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteStrictMode,
	})
}

// csrfRequiredForMethod reports whether method is a mutating method
// that must present a valid CSRF token (09 §5.4: "every mutating
// request (POST/PUT/DELETE)"). GET (and HEAD/OPTIONS, never used by
// this API) never require one.
func csrfRequiredForMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch:
		return true
	default:
		return false
	}
}

// validateCSRF reports whether r carries a valid X-CSRF-Token for the
// session identified by sessionTokenHash. Comparison is constant-time
// and session-bound by construction: a token minted for a different
// session hashes differently under the same key, so it can never match
// here regardless of how it was obtained.
func (h *AuthHandlers) validateCSRF(r *http.Request, sessionTokenHash []byte) bool {
	got := r.Header.Get("X-CSRF-Token")
	if got == "" {
		return false
	}
	want := h.issueCSRFToken(sessionTokenHash)
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// requireCSRF is the reusable per-request CSRF gate every session-bound
// mutating handler in this package applies (and the seam CAPI-001 will
// later wrap every control-plane mutation with): GET is always exempt;
// any other method must present a valid, session-bound X-CSRF-Token
// header. On failure it writes the csrf_failed (403) envelope itself
// and returns false — callers must return immediately without having
// performed any side effect yet.
func (h *AuthHandlers) requireCSRF(w http.ResponseWriter, r *http.Request, sessionTokenHash []byte) bool {
	if !csrfRequiredForMethod(r.Method) {
		return true
	}
	if !h.validateCSRF(r, sessionTokenHash) {
		writeAuthError(w, http.StatusForbidden, "csrf_failed", "csrf validation failed", false)
		return false
	}
	return true
}
