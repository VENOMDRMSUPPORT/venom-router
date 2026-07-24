package httpapi

import (
	"context"
	"net/http"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/secrets"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// validSession is the outcome of a successful validateSession call: the
// still-live (and just-renewed) session row plus its token hash, for
// callers that need to bind further state to this exact session (CSRF
// issuance/validation in SEC-004, the reverify stamp in SEC-005).
type validSession struct {
	Row       storage.OwnerSessionRow
	TokenHash []byte
}

// validateSession is the reusable session-lifecycle gate (09 §5.3) that
// every authenticated route in this package (and, later, CAPI-001's
// blanket middleware) applies: it resolves the incoming session cookie,
// enforces idle (30 min sliding) and absolute (12 h hard cap) expiry,
// and — only on a still-valid request — renews last_seen_at/
// idle_expires_at (never past absolute_expires_at). An expired session
// is revoked as a side effect (it must never be resurrected by a later
// request racing in with a stale cookie).
//
// Failure modes are distinguished by errCode so callers can map to the
// exact typed error 09 §5.8 documents: "invalid_credentials" for an
// absent/unrecognized cookie (the same no-leak posture as login),
// "session_expired" for a revoked or expired-but-still-present session,
// "internal" for a storage failure.
func (h *AuthHandlers) validateSession(ctx context.Context, r *http.Request) (session validSession, ok bool, errCode string) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return validSession{}, false, "invalid_credentials"
	}

	tokenHash := secrets.HashSessionHandle(cookie.Value)
	row, found, err := h.ownerSessions.GetByTokenHash(ctx, tokenHash)
	if err != nil {
		return validSession{}, false, "internal"
	}
	if !found {
		return validSession{}, false, "invalid_credentials"
	}
	if row.RevokedAt != nil {
		return validSession{}, false, "session_expired"
	}

	now := h.now().UTC()
	if !now.Before(row.AbsoluteExpiresAt) || !now.Before(row.IdleExpiresAt) {
		// Past idle or absolute expiry: revoke so a stale cookie racing
		// in on a later request can never be resurrected, then report
		// session_expired regardless of which deadline was hit.
		_ = h.ownerSessions.Revoke(ctx, tokenHash)
		return validSession{}, false, "session_expired"
	}

	newIdle := now.Add(secrets.DefaultIdleTTL)
	if newIdle.After(row.AbsoluteExpiresAt) {
		newIdle = row.AbsoluteExpiresAt
	}
	if err := h.ownerSessions.Renew(ctx, tokenHash, now, newIdle); err != nil {
		return validSession{}, false, "internal"
	}
	row.LastSeenAt = now
	row.IdleExpiresAt = newIdle

	return validSession{Row: row, TokenHash: tokenHash}, true, ""
}

// writeSessionError maps validateSession's errCode to the documented
// HTTP status (09 §5.8) and writes the shared error envelope.
func writeSessionError(w http.ResponseWriter, errCode string) {
	switch errCode {
	case "session_expired":
		writeAuthError(w, http.StatusUnauthorized, "session_expired", "session expired", false)
	case "internal":
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
	default:
		writeAuthError(w, http.StatusUnauthorized, "invalid_credentials", "invalid credentials", false)
	}
}
