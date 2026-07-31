package httpapi

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ingressWindow is the sliding window the per-path per-IP ingress limiter
// measures over (05 §6).
const ingressWindow = time.Minute

// defaultIngressLimit is the per-path, per-IP request budget within one window.
// It is deliberately generous — this protects Venom's OWN endpoints from a
// runaway client, NOT provider quota (that is the per-key RPM + the quota
// engine) — and is set well above any single client's normal burst on one path.
// It is a package default, not a config surface for this unit; a knob is
// additive later.
const defaultIngressLimit = 600

// ingressLimiter is the per-path, per-IP sliding-window ingress limiter applied
// to BOTH the control and public surfaces (05 §6). It is DISTINCT from a
// provider-429 cooldown: an ingress rejection is Venom's own back-pressure, has
// NO storage dependency at all (so it structurally cannot create or consult a
// provider cooldown, consume a quota reservation, or reach the engine), and is
// distinct from the per-key RPM in vkAuthenticator (a request on /v1/* must
// satisfy BOTH). It reuses keyedSlidingWindowLimiter with a composite path|ip
// key; the clock is injected so the window is deterministic under test.
type ingressLimiter struct {
	limiter *keyedSlidingWindowLimiter
	limit   int
	window  time.Duration
	now     func() time.Time
}

// newIngressLimiter builds the limiter. limit <= 0 uses defaultIngressLimit,
// window <= 0 uses ingressWindow, now nil uses the wall clock.
func newIngressLimiter(limit int, window time.Duration, now func() time.Time) *ingressLimiter {
	if limit <= 0 {
		limit = defaultIngressLimit
	}
	if window <= 0 {
		window = ingressWindow
	}
	if now == nil {
		now = time.Now
	}
	return &ingressLimiter{limiter: newKeyedSlidingWindowLimiter(window), limit: limit, window: window, now: now}
}

// Middleware enforces the limit BEFORE next runs, so a rejected request never
// reaches auth, the engine, or quota. The key is path|ip so one path's traffic
// from one IP is independent of both other paths and other IPs (dropping either
// dimension collapses that independence).
func (il *ingressLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Path + "|" + clientIP(r)
		if !il.limiter.Allow(key, il.limit, il.now()) {
			il.reject(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// reject writes the 429 in the envelope shape of the surface: the OpenAI-style
// public shape on /v1/*, the control envelope elsewhere. Both carry Retry-After.
func (il *ingressLimiter) reject(w http.ResponseWriter, r *http.Request) {
	retryAfter := int(il.window.Seconds())
	if retryAfter < 1 {
		retryAfter = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
	if strings.HasPrefix(r.URL.Path, "/v1/") {
		writePublicError(w, http.StatusTooManyRequests, publicErrRateLimited, "rate limit exceeded")
		return
	}
	writeErrorDetails(w, http.StatusTooManyRequests, "rate_limited", "rate limit exceeded", true, map[string]any{"retry_after": retryAfter})
}

// clientIP returns the host portion of RemoteAddr. X-Forwarded-For is
// DELIBERATELY IGNORED: this is a loopback-first, single-owner gateway, and
// RemoteAddr is the only IP the server itself observed. Trusting the
// client-settable X-Forwarded-For would let any caller rotate the header to mint
// a fresh limiter bucket per request and bypass the per-IP limit entirely
// (05 §6). If a real reverse proxy is ever put in front, that proxy — not this
// code — owns the trusted-forwarded-for policy.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
