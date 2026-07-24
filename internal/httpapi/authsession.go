package httpapi

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/secrets"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// dummyOwnerPasswordHash is a fixed, non-secret placeholder carrying the
// documented Argon2id parameters (09 §5.1) but no real password's
// derivation. ServeLogin verifies against this whenever no owner_auth
// row exists yet, so the CPU cost of the Argon2id derivation (which
// depends only on Time/MemKiB/Threads/KeyLen, not on the specific
// salt/hash bytes) is paid identically whether or not setup has been
// completed — closing the timing side channel that would otherwise let
// an unauthenticated caller distinguish "no owner yet" from "wrong
// password" by response latency, on top of the response body already
// being identical in both cases.
var dummyOwnerPasswordHash = secrets.OwnerPasswordHash{
	Hash:    make([]byte, secrets.Argon2KeyLen),
	Salt:    make([]byte, 16), // arbitrary fixed-length placeholder; content is irrelevant to KDF cost
	Time:    secrets.Argon2Time,
	MemKiB:  secrets.Argon2MemKiB,
	Threads: secrets.Argon2Threads,
	KeyLen:  secrets.Argon2KeyLen,
}

// ownerPasswordHashFromRow adapts a persisted storage.OwnerAuthRow into
// the secrets.OwnerPasswordHash shape VerifyOwnerPassword expects.
func ownerPasswordHashFromRow(row storage.OwnerAuthRow) secrets.OwnerPasswordHash {
	return secrets.OwnerPasswordHash{
		Hash:    row.PasswordHash,
		Salt:    row.Salt,
		Time:    row.KDFTime,
		MemKiB:  row.KDFMemKiB,
		Threads: row.KDFThreads,
		KeyLen:  row.KDFKeyLen,
	}
}

type loginRequest struct {
	Password string `json:"password"`
}

// ServeLogin implements POST /auth/login (09 §5.2). It verifies the
// owner password, constant-time, and mints a new session on success. Its
// failure response is deliberately IDENTICAL — same status, code,
// message — whether there is no owner_auth row yet or the password is
// simply wrong: 09 §5.2 requires the error be "generic, never revealing
// whether setup is done."
func (h *AuthHandlers) ServeLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAuthError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false)
		return
	}

	if !h.loginLimiter.Allow(time.Now()) {
		writeAuthError(w, http.StatusTooManyRequests, "rate_limited", "too many login attempts, try again later", true)
		return
	}

	ctx := r.Context()

	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAuthError(w, http.StatusBadRequest, "validation_error", "invalid request body", false)
		return
	}

	row, exists, err := h.ownerAuth.Get(ctx)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}

	// Always run the same constant-time verify, against a real stored
	// hash when one exists or the fixed dummy otherwise — never skip it
	// or branch before it based on exists. verified is combined with
	// exists only in the boolean below, never in a separate response path.
	stored := dummyOwnerPasswordHash
	if exists {
		stored = ownerPasswordHashFromRow(row)
	}
	verified := secrets.VerifyOwnerPassword(req.Password, stored)

	if !exists || !verified {
		writeAuthError(w, http.StatusUnauthorized, "invalid_credentials", "invalid credentials", false)
		return
	}

	idleExpiresAt, absoluteExpiresAt, err := h.createSession(ctx, r, w)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}

	writeAuthJSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{
			"session": map[string]any{
				"idle_expires_at":     idleExpiresAt.Format(time.RFC3339),
				"absolute_expires_at": absoluteExpiresAt.Format(time.RFC3339),
			},
		},
	})
	// No csrf_token is issued here — session-bound CSRF issuance is
	// SEC-004; this unit deliberately omits it rather than fabricating a
	// token nothing yet validates.
}

// ServeLogout implements POST /auth/logout (09 §5.3): revokes the
// session named by the incoming cookie (idempotent — a missing or
// already-revoked cookie is a no-op, not an error) and clears the
// cookie. Always responds 200.
func (h *AuthHandlers) ServeLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAuthError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false)
		return
	}

	if cookie, err := r.Cookie(sessionCookieName); err == nil && cookie.Value != "" {
		tokenHash := secrets.HashSessionHandle(cookie.Value)
		if err := h.ownerSessions.Revoke(r.Context(), tokenHash); err != nil {
			writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
			return
		}
	}

	clearSessionCookie(w)
	writeAuthJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"logged_out": true}})
}

// ServeSession implements GET /auth/session (09 §5.2/§5.3): reports the
// current session's expiry timestamps if the incoming cookie names a
// present, non-revoked session, else the generic invalid_credentials
// (401) — the same no-leak posture as login. This does NOT enforce
// idle/absolute expiry (SEC-003's job); a session past either deadline
// but not yet revoked is still reported here as present.
func (h *AuthHandlers) ServeSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAuthError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false)
		return
	}

	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		writeAuthError(w, http.StatusUnauthorized, "invalid_credentials", "invalid credentials", false)
		return
	}

	tokenHash := secrets.HashSessionHandle(cookie.Value)
	row, ok, err := h.ownerSessions.GetByTokenHash(r.Context(), tokenHash)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}
	if !ok || row.RevokedAt != nil {
		writeAuthError(w, http.StatusUnauthorized, "invalid_credentials", "invalid credentials", false)
		return
	}

	writeAuthJSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{
			"session": map[string]any{
				"idle_expires_at":     row.IdleExpiresAt.Format(time.RFC3339),
				"absolute_expires_at": row.AbsoluteExpiresAt.Format(time.RFC3339),
			},
		},
	})
}

// clearSessionCookie clears the session cookie via the standard
// MaxAge<0 deletion idiom, at the same Path/flags it was set with, so
// the browser actually removes it rather than leaving a stale value.
func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     controlAPIPath,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}
