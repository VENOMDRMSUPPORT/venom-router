package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// Public data-plane error codes (01 §6b, 05 §5). This unit ships only the two
// authentication/limit codes in the public shape; the FULL public error
// envelope (parity with the OpenAI error object across every failure mode) is
// P5-PAPI-006's job — see writePublicError's comment for the boundary.
const (
	publicErrInvalidAPIKey = "invalid_api_key"
	publicErrRateLimited   = "rate_limited"
)

// vkLiveKeyPrefix is the required raw-key prefix for a Venom API key
// (01 §6b/§8). The prefix is NOT a secret and is echoed in key_prefix; the
// entropy lives in the bytes after it.
const vkLiveKeyPrefix = "vk_live_"

// defaultPerKeyRPM is the requests-per-minute limit applied to a key whose
// stored rpm_limit is NULL ("no explicit limit configured"). A NULL limit is
// the configured DEFAULT, never "unlimited" (05 §6). It is a documented
// package default rather than a config surface for this unit; a config knob is
// additive later and needs no schema change (rpm_limit already overrides
// per-key).
const defaultPerKeyRPM = 60

// perKeyRPMWindow is the sliding window RPM is measured over.
const perKeyRPMWindow = time.Minute

// apiKeyContextKey is the unexported context key the authenticated key id is
// stored under, so a downstream data-plane handler (P5-PAPI-002's request
// path) can attribute usage to the key without re-verifying it. Distinct
// unexported type per the standard context-key idiom.
type apiKeyContextKey struct{}

// apiKeyIDFromContext returns the authenticated key id vkAuthenticator placed
// on ctx, ok=false if none (a handler invoked without the middleware).
func apiKeyIDFromContext(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(apiKeyContextKey{}).(string)
	return id, ok
}

// HashAPIKey is the deterministic verifier over a raw vk_live_* key:
// hex(sha256(raw)), 64 lowercase hex chars. It is used BOTH at creation
// (P5-CAPI-001) and at every authentication, so the two can never disagree.
//
// This is hex(sha256), NOT argon2/bcrypt, by deliberate design: a Venom key is
// a 256-bit CSPRNG secret with no low-entropy structure, so it needs no KDF
// stretching, and a per-row salt would force a full-table scan on every
// request instead of an indexed equality lookup. Do NOT "upgrade" this into a
// salted KDF — that would turn every data-plane request into an O(n) scan.
func HashAPIKey(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// keyedSlidingWindowLimiter is a per-KEY sliding-window rate limiter (05 §6:
// "per-API-key RPM ... sliding window"). It is deliberately NOT the unkeyed
// fixed-window fixedWindowLimiter (auth.go) — that one serves the setup/reveal
// endpoints with unrelated semantics, and conflating the two would couple
// independent policies. The clock is injected (no time.Now inside), so RPM is
// fully deterministic under test.
type keyedSlidingWindowLimiter struct {
	mu     sync.Mutex
	window time.Duration
	hits   map[string][]time.Time
}

func newKeyedSlidingWindowLimiter(window time.Duration) *keyedSlidingWindowLimiter {
	return &keyedSlidingWindowLimiter{window: window, hits: make(map[string][]time.Time)}
}

// Allow records one hit for key at now and reports whether it is within limit
// over the trailing window. Hits older than the window are dropped first. The
// per-key bucket is what makes one key's traffic independent of another's.
func (l *keyedSlidingWindowLimiter) Allow(key string, limit int, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	cutoff := now.Add(-l.window)
	kept := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= limit {
		l.hits[key] = kept
		return false
	}
	l.hits[key] = append(kept, now)
	return true
}

// vkAuthenticator verifies a Bearer vk_live_* key and enforces per-key RPM
// (P5-PAPI-001, 01 §6b, 05 §6). It is the data-plane analogue of
// ownerSessionGate: a middleware every /v1/* route is wrapped with.
type vkAuthenticator struct {
	keys       *storage.APIKeyRepo
	limiter    *keyedSlidingWindowLimiter
	defaultRPM int
	now        func() time.Time
}

// newVKAuthenticator builds an authenticator over the keys repo. now defaults
// to time.Now when nil (tests inject a fixed clock).
func newVKAuthenticator(keys *storage.APIKeyRepo, now func() time.Time) *vkAuthenticator {
	if now == nil {
		now = time.Now
	}
	return &vkAuthenticator{
		keys:       keys,
		limiter:    newKeyedSlidingWindowLimiter(perKeyRPMWindow),
		defaultRPM: defaultPerKeyRPM,
		now:        now,
	}
}

// Middleware wraps next with vk authentication + per-key RPM. On success it
// attaches the authenticated key id to the request context and touches
// last_used_at; on any auth failure it writes an IDENTICAL invalid_api_key 401
// (no enumeration oracle between missing / malformed / unknown / revoked), and
// on an over-limit request a rate_limited 429. Neither response — nor any log
// or audit row — ever echoes the presented key or any fragment of it.
func (a *vkAuthenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, ok := bearerToken(r)
		if !ok || !strings.HasPrefix(raw, vkLiveKeyPrefix) {
			// Missing or malformed: identical response to an unknown key.
			writePublicError(w, http.StatusUnauthorized, publicErrInvalidAPIKey, "invalid API key")
			return
		}

		key, found, err := a.keys.LookupByHash(r.Context(), HashAPIKey(raw))
		if err != nil || !found || key.Revoked() {
			// Unknown, lookup error, and revoked are INDISTINGUISHABLE by body
			// and status — a probe cannot tell "no such key" from "revoked".
			writePublicError(w, http.StatusUnauthorized, publicErrInvalidAPIKey, "invalid API key")
			return
		}

		// A NULL stored rpm_limit means the configured default, never unlimited.
		limit := a.defaultRPM
		if key.RPMLimit != nil {
			limit = *key.RPMLimit
		}
		if !a.limiter.Allow(key.ID, limit, a.now()) {
			writePublicError(w, http.StatusTooManyRequests, publicErrRateLimited, "rate limit exceeded")
			return
		}

		// Best-effort last-used bookkeeping; never gates the request.
		_ = a.keys.TouchLastUsed(r.Context(), key.ID, a.now())

		ctx := context.WithValue(r.Context(), apiKeyContextKey{}, key.ID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// bearerToken extracts the raw token from an "Authorization: Bearer <token>"
// header (case-insensitive scheme). ok is false when the header is absent or
// not a single-token Bearer credential.
func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return "", false
	}
	const scheme = "bearer "
	if len(h) <= len(scheme) || !strings.EqualFold(h[:len(scheme)], scheme) {
		return "", false
	}
	token := strings.TrimSpace(h[len(scheme):])
	if token == "" || strings.ContainsAny(token, " \t") {
		return "", false
	}
	return token, true
}

// writePublicError writes the minimal public data-plane error shape
// (01 §6b). It is intentionally a small, self-contained JSON object rather
// than the control-plane envelope (writeAuthError) — the public plane is
// OpenAI-compatible and must not leak the control envelope's request_id
// bookkeeping. The FULL public error envelope (every code, OpenAI parity) is
// P5-PAPI-006; this unit ships only invalid_api_key / rate_limited. message is
// always a fixed, caller-supplied string — never the presented key.
func writePublicError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"code": code, "message": message},
	})
}
