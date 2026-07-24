package httpapi

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/secrets"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// reverifyFreshnessTTL is 09 §5.5's exact re-verification window: a
// successful POST /auth/reverify keeps a session "fresh" for exactly 5
// minutes, no more.
const reverifyFreshnessTTL = 5 * time.Minute

type reverifyRequest struct {
	Password string `json:"password"`
}

// ServeReverify implements POST /auth/reverify (09 §5.5): a
// session-bound, password-gated freshness stamp for sensitive
// operations, independent of the session's own age. It requires an
// already-valid session (validateSession — SEC-003) and a passing CSRF
// check (requireCSRF — SEC-004; this is exactly the "session-bound
// mutation" that unit exists to guard). On a correct password it stamps
// reverify_fresh_until = now + 5 minutes on the CURRENT session only —
// it creates no new session and no new account, matching the card's
// explicit non-goal. On a wrong password it responds with the same
// generic invalid_credentials 401 login uses, and does not stamp
// anything. Rate-limited like setup/login; SEC-006 replaces this
// minimal limiter with the real lockout+audit trail.
func (h *AuthHandlers) ServeReverify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAuthError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false)
		return
	}

	ctx := r.Context()

	session, ok, errCode := h.validateSession(ctx, r)
	if !ok {
		writeSessionError(w, errCode)
		return
	}

	if !h.requireCSRF(w, r, session.TokenHash) {
		return
	}

	now := h.now()

	locked, retryAfter, err := h.checkLockout(ctx, "reverify", now)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}
	if locked {
		h.recordAuthEvent(ctx, "reverify", "failure", "locked_out", now)
		writeLockedOut(w, retryAfter)
		return
	}

	var req reverifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAuthError(w, http.StatusBadRequest, "validation_error", "invalid request body", false)
		return
	}

	row, exists, err := h.ownerAuth.Get(ctx)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}

	// The same always-run, constant-time verify posture as ServeLogin
	// (09 §5.2): never branch before it based on exists. A session
	// cannot exist without a completed setup, so !exists should be
	// unreachable in practice — the dummy-hash fallback keeps this path
	// fail-closed and side-channel-free regardless.
	stored := dummyOwnerPasswordHash
	if exists {
		stored = ownerPasswordHashFromRow(row)
	}
	verified := secrets.VerifyOwnerPassword(req.Password, stored)

	if !exists || !verified {
		h.recordAuthEvent(ctx, "reverify", "failure", "invalid_credentials", now)
		writeAuthError(w, http.StatusUnauthorized, "invalid_credentials", "invalid credentials", false)
		return
	}

	until := now.UTC().Add(reverifyFreshnessTTL)
	if err := h.ownerSessions.StampReverify(ctx, session.TokenHash, until); err != nil {
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}
	h.recordAuthEvent(ctx, "reverify", "success", "", now)

	writeAuthJSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{
			"reverify_fresh_until": until.Format(time.RFC3339),
		},
	})
}

// IsReverifyFresh reports whether session was re-verified recently
// enough to still be "fresh" at now (09 §5.5). The comparison is
// strictly Before: freshness expires exactly at the 5-minute mark, not
// one instant after — a request landing AT reverify_fresh_until is
// already stale. A session that was never re-verified (nil) is never
// fresh. This is the gate a later unit (CAPI-004) will call before a
// sensitive endpoint; which endpoints are sensitive is that unit's
// concern, not this one's.
func IsReverifyFresh(row storage.OwnerSessionRow, now time.Time) bool {
	return row.ReverifyFreshUntil != nil && now.Before(*row.ReverifyFreshUntil)
}
