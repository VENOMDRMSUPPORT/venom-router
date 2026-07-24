package httpapi

import (
	"context"
	"net/http"
)

// sessionContextKey is the unexported context key ownerSessionGate
// stores the resolved validSession under, so downstream handlers can
// read the authenticated session without re-validating it themselves.
// Unexported and a distinct type (not a bare string) per the standard
// context-key-collision-avoidance idiom.
type sessionContextKey struct{}

// sessionFromContext returns the validSession ownerSessionGate placed on
// ctx, and ok=false if none is present (e.g. a handler invoked directly
// in a test without going through the gate).
func sessionFromContext(ctx context.Context) (validSession, bool) {
	session, ok := ctx.Value(sessionContextKey{}).(validSession)
	return session, ok
}

// ownerSessionGate is the control-plane authentication middleware
// (P2b-CAPI-001, 09 §1/§5): every authenticated /api/control/v1/* route
// (everything except the /auth/* handshake subtree, /health, and the
// SPA) is wrapped with this, behind networkGateJSON. Per request:
//  1. validateSession (SEC-003) — an absent/unknown/expired/revoked
//     session is rejected with the appropriate typed error and next
//     never runs.
//  2. requireCSRF (SEC-004) — a mutating request without a valid
//     session-bound CSRF token is rejected with csrf_failed 403,
//     BEFORE next (and therefore before any handler side effect) runs.
//     GET is exempt, exactly as requireCSRF already implements.
//
// Only once both pass does next run, with the resolved session
// retrievable via sessionFromContext.
func (h *AuthHandlers) ownerSessionGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, ok, errCode := h.validateSession(r.Context(), r)
		if !ok {
			writeSessionError(w, errCode)
			return
		}

		if !h.requireCSRF(w, r, session.TokenHash) {
			return
		}

		ctx := context.WithValue(r.Context(), sessionContextKey{}, session)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
