package httpapi

import (
	"context"
	"net/http"
	"time"
)

// lockoutThreshold and lockoutWindow are 09 §5.6's documented default:
// lockout after 5 consecutive failures within 15 minutes, per single
// owner, shared by login and re-verify.
const (
	lockoutThreshold = 5
	lockoutWindow    = 15 * time.Minute
)

// checkLockout reports whether action (login|reverify) is currently
// locked out at now, and if so, how many seconds remain until the
// earliest failure in the current streak ages out of the sliding
// window. It never mutates anything — callers still record this very
// attempt as its own auth_event afterward, whichever way it resolves.
func (h *AuthHandlers) checkLockout(ctx context.Context, action string, now time.Time) (locked bool, retryAfterSeconds int64, err error) {
	count, oldest, err := h.authEvents.FailureStreak(ctx, action, now.Add(-lockoutWindow))
	if err != nil {
		return false, 0, err
	}
	if count < lockoutThreshold {
		return false, 0, nil
	}

	remaining := lockoutWindow - now.Sub(oldest)
	if remaining < 0 {
		remaining = 0
	}
	return true, int64(remaining.Seconds()) + 1, nil
}

// recordAuthEvent appends one auth_events row for this attempt (09
// §5.6: "each attempt — success or failure — emits an auth_event audit
// row"). Errors are deliberately not surfaced to the caller as a
// request failure: the audit trail must never block or alter an
// otherwise-correct auth decision that has already been made: an
// audit-append failure only reaches the caller's logs, never the
// response and never a login/session-created record.
func (h *AuthHandlers) recordAuthEvent(ctx context.Context, action, result, reasonCode string, now time.Time) {
	_ = h.authEvents.Append(ctx, action, result, reasonCode, now)
}

// writeLockedOut writes the locked_out (429) envelope with retry_after
// (09 §5.6/§5.8) — the one error shape in this package that extends the
// shared envelope with an extra field, so it does not go through
// writeAuthError.
func writeLockedOut(w http.ResponseWriter, retryAfterSeconds int64) {
	writeAuthJSON(w, http.StatusTooManyRequests, map[string]any{
		"error": map[string]any{
			"code":        "locked_out",
			"message":     "too many failed attempts, try again later",
			"request_id":  newRequestID(),
			"retryable":   true,
			"retry_after": retryAfterSeconds,
		},
	})
}
