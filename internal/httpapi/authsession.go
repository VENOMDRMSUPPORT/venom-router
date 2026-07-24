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

	ctx := r.Context()
	now := h.now()

	locked, retryAfter, err := h.checkLockout(ctx, "login", now)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}
	if locked {
		h.recordAuthEvent(ctx, "login", "failure", "locked_out", now)
		writeLockedOut(w, retryAfter)
		return
	}

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
		h.recordAuthEvent(ctx, "login", "failure", "invalid_credentials", now)
		writeAuthError(w, http.StatusUnauthorized, "invalid_credentials", "invalid credentials", false)
		return
	}

	idleExpiresAt, absoluteExpiresAt, tokenHash, err := h.createSession(ctx, r, w)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}
	h.recordAuthEvent(ctx, "login", "success", "", now)

	csrfToken := h.issueCSRFToken(tokenHash)
	setCSRFCookie(w, r, csrfToken)

	writeAuthJSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{
			"session": map[string]any{
				"idle_expires_at":     idleExpiresAt.Format(time.RFC3339),
				"absolute_expires_at": absoluteExpiresAt.Format(time.RFC3339),
			},
		},
		"csrf_token": csrfToken,
	})
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
// still-valid session, else the appropriate typed error. Unlike its
// original SEC-002 shape, this now ENFORCES idle/absolute expiry via
// validateSession (SEC-003): a session past either deadline is revoked
// and rejected with session_expired, never reported as present, and a
// still-valid request's activity is renewed exactly as any other
// authenticated route's would be.
func (h *AuthHandlers) ServeSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAuthError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false)
		return
	}

	session, ok, errCode := h.validateSession(r.Context(), r)
	if !ok {
		writeSessionError(w, errCode)
		return
	}

	writeAuthJSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{
			"session": map[string]any{
				"idle_expires_at":     session.Row.IdleExpiresAt.Format(time.RFC3339),
				"absolute_expires_at": session.Row.AbsoluteExpiresAt.Format(time.RFC3339),
			},
		},
		// Recomputed (never stored) from the resolved session's token
		// hash — this is what makes CSRF issuance restart-safe: the SPA
		// can always re-fetch a valid token for its live session via
		// this GET, even though h.csrfKey is a fresh, process-lifetime
		// secret every restart (09 §5.4's design note).
		"csrf_token": h.issueCSRFToken(session.TokenHash),
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
