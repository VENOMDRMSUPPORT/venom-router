package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/secrets"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// sessionCookieName is the cookie the owner's browser carries the opaque
// session handle in (09 §5.2). It carries only the opaque handle — never
// identity or the password.
const sessionCookieName = "venom_session"

// controlAPIPath is the base path 09 §5.2's cookie is scoped to.
const controlAPIPath = "/api/control/v1"

// AuthHandlers holds the owner-auth handlers' dependencies: the two M1
// repositories and a minimal per-endpoint rate limiter per mutating
// endpoint. Constructed once at composition (ControlMux) and shared
// across requests.
type AuthHandlers struct {
	ownerAuth       *storage.OwnerAuthRepo
	ownerSessions   *storage.OwnerSessionRepo
	setupLimiter    *fixedWindowLimiter
	loginLimiter    *fixedWindowLimiter
	reverifyLimiter *fixedWindowLimiter

	// csrfKey is a random 32-byte key generated once per AuthHandlers
	// (process lifetime), never persisted. It is the sole secret input
	// to issueCSRFToken/validateCSRF (09 §5.4) — CSRF tokens are
	// deterministic HMACs of a session's token hash under this key, so
	// they need no storage of their own and are trivially recomputed
	// (e.g. by GET /auth/session) for the lifetime of this process.
	csrfKey []byte

	// now is the injectable clock every time-based decision in this
	// package computes against (session expiry/renewal, CSRF issuance
	// implicitly via session lookups, reverify freshness, lockout
	// windows) — tests set it to a fixed/steppable function so
	// idle/absolute expiry, the 5-minute reverify window, and the
	// lockout window are provable to the second rather than racing a
	// real clock.
	now func() time.Time
}

// NewAuthHandlers builds the auth handlers over db's existing connection.
func NewAuthHandlers(db *storage.DB) *AuthHandlers {
	// crypto/rand.Read is documented (Go 1.24+, this module's floor) to
	// always fill the buffer and never return an error — unlike
	// MintOwnerSession/DeriveOwnerPasswordHash (which still check it,
	// for defense in depth on a value that leaves this process), this
	// key never leaves the process, so there is nothing a typed error
	// here could let a caller do differently; the error is deliberately
	// discarded rather than propagated through every ControlMux/
	// NewAuthHandlers call site for a condition that cannot occur.
	csrfKey := make([]byte, 32)
	_, _ = rand.Read(csrfKey)

	return &AuthHandlers{
		ownerAuth:       storage.NewOwnerAuthRepo(db),
		ownerSessions:   storage.NewOwnerSessionRepo(db),
		setupLimiter:    newFixedWindowLimiter(5, time.Minute),
		loginLimiter:    newFixedWindowLimiter(5, time.Minute),
		reverifyLimiter: newFixedWindowLimiter(5, time.Minute),
		csrfKey:         csrfKey,
		now:             time.Now,
	}
}

type setupRequest struct {
	Password string `json:"password"`
}

// ServeSetup implements POST /auth/setup (09 §5.1): first-run owner
// password setup. Precondition: no owner_auth row exists. On success it
// derives and stores the Argon2id hash, creates the first session (as
// §5.2 describes), and sets the session cookie so setup flows straight
// into a logged-in dashboard.
func (h *AuthHandlers) ServeSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAuthError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false)
		return
	}

	if !h.setupLimiter.Allow(h.now()) {
		writeAuthError(w, http.StatusTooManyRequests, "rate_limited", "too many setup attempts, try again later", true)
		return
	}

	ctx := r.Context()

	exists, err := h.ownerAuth.Exists(ctx)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}
	if exists {
		writeAuthError(w, http.StatusConflict, "setup_already_complete", "owner setup has already been completed", false)
		return
	}

	var req setupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAuthError(w, http.StatusBadRequest, "validation_error", "invalid request body", false)
		return
	}

	derived, err := secrets.DeriveOwnerPasswordHash(req.Password)
	if err != nil {
		// secrets.DeriveOwnerPasswordHash's error never includes the
		// password (see its doc comment) — safe to surface verbatim.
		writeAuthError(w, http.StatusBadRequest, "validation_error", err.Error(), false)
		return
	}

	if err := h.ownerAuth.Create(ctx, storage.OwnerAuthRow{
		PasswordHash: derived.Hash,
		Salt:         derived.Salt,
		KDFTime:      derived.Time,
		KDFMemKiB:    derived.MemKiB,
		KDFThreads:   derived.Threads,
		KDFKeyLen:    derived.KeyLen,
	}); err != nil {
		if errors.Is(err, storage.ErrOwnerAuthAlreadySet) {
			writeAuthError(w, http.StatusConflict, "setup_already_complete", "owner setup has already been completed", false)
			return
		}
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}

	idleExpiresAt, absoluteExpiresAt, tokenHash, err := h.createSession(ctx, r, w)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}

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

// createSession mints a fresh opaque session (internal/secrets),
// persists only its verifier hash, and sets the session cookie. It
// returns the computed expiry timestamps for the response body. Shared
// by ServeSetup (the first session) and ServeLogin (every subsequent
// session) — both create a session identically once the caller is
// authenticated.
func (h *AuthHandlers) createSession(ctx context.Context, r *http.Request, w http.ResponseWriter) (idleExpiresAt, absoluteExpiresAt time.Time, tokenHash []byte, err error) {
	session, err := secrets.MintOwnerSession()
	if err != nil {
		return time.Time{}, time.Time{}, nil, err
	}

	now := h.now().UTC()
	idleExpiresAt = now.Add(secrets.DefaultIdleTTL)
	absoluteExpiresAt = now.Add(secrets.DefaultAbsoluteTTL)

	if err := h.ownerSessions.Create(ctx, storage.OwnerSessionRow{
		TokenHash:         session.TokenHash,
		CreatedAt:         now,
		LastSeenAt:        now,
		IdleExpiresAt:     idleExpiresAt,
		AbsoluteExpiresAt: absoluteExpiresAt,
	}); err != nil {
		return time.Time{}, time.Time{}, nil, err
	}

	setSessionCookie(w, r, session.Handle)
	return idleExpiresAt, absoluteExpiresAt, session.TokenHash, nil
}

// ServeStatus implements GET /auth/status (09 §5.1): whether the single
// owner_auth row exists yet.
func (h *AuthHandlers) ServeStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAuthError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false)
		return
	}

	exists, err := h.ownerAuth.Exists(r.Context())
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}

	writeAuthJSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{"setup_complete": exists},
	})
}

// setSessionCookie sets the opaque session handle as an HttpOnly,
// SameSite=Strict cookie scoped to the control API path (09 §5.2). The
// cookie carries only the opaque handle. Secure is set whenever the
// request arrived over TLS; on plain-loopback HTTP (the control plane's
// default bind) it is omitted, per 09 §5.2's explicit carve-out — the
// only case Secure may be omitted.
func setSessionCookie(w http.ResponseWriter, r *http.Request, handle string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    handle,
		Path:     controlAPIPath,
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteStrictMode,
	})
}

// writeAuthJSON writes body as the success envelope (09 §1: `{"data": ...}`).
func writeAuthJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// writeAuthError writes the shared error envelope (09 §1). message is
// always a caller-supplied, pre-vetted user-safe string — this function
// never receives or forwards a raw password, secret, or provider error.
func writeAuthError(w http.ResponseWriter, status int, code, message string, retryable bool) {
	writeAuthJSON(w, status, map[string]any{
		"error": map[string]any{
			"code":       code,
			"message":    message,
			"request_id": newRequestID(),
			"retryable":  retryable,
		},
	})
}

// newRequestID generates a short random hex identifier for the error
// envelope's request_id field. It carries no information about the
// request itself, so it cannot leak anything by construction.
func newRequestID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(b)
}

// fixedWindowLimiter is a minimal, single-endpoint rate limiter (09
// §5.1: "Rate-limited"). Full lockout-with-backoff (5 consecutive
// failures / 15 minutes, audit-integrated) is SEC-006's job; this only
// blunts naive hammering of the Argon2id-cost setup endpoint until that
// lands.
type fixedWindowLimiter struct {
	mu       sync.Mutex
	max      int
	window   time.Duration
	attempts []time.Time
}

func newFixedWindowLimiter(max int, window time.Duration) *fixedWindowLimiter {
	return &fixedWindowLimiter{max: max, window: window}
}

// Allow records one attempt at now and reports whether it is within the
// limit. Attempts older than the window are dropped first, so this is a
// simple sliding-window count, not a token bucket.
func (l *fixedWindowLimiter) Allow(now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	cutoff := now.Add(-l.window)
	kept := l.attempts[:0]
	for _, t := range l.attempts {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	l.attempts = kept

	if len(l.attempts) >= l.max {
		return false
	}
	l.attempts = append(l.attempts, now)
	return true
}
