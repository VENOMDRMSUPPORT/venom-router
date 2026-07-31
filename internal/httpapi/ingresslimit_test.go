package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// reqTo builds a request to path from a given remote IP (host:port), with an
// optional X-Forwarded-For header.
func reqTo(path, remoteIP, xff string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.RemoteAddr = remoteIP
	if xff != "" {
		r.Header.Set("X-Forwarded-For", xff)
	}
	return r
}

func serve(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

// TestIngress_CompositeKeyPerPathPerIP proves the limiter keys on BOTH path and
// IP: a second request on the same (path, IP) is 429, but a different IP on the
// same path — and a different path on the same IP — are each unaffected.
//
// P5-M1 (key by IP only): the different-path-same-IP case turns 429 ⇒ RED.
// P5-M2 (key by path only): the different-IP-same-path case turns 429 ⇒ RED.
func TestIngress_CompositeKeyPerPathPerIP(t *testing.T) {
	clock := time.Unix(1000, 0)
	il := newIngressLimiter(1, time.Minute, func() time.Time { return clock })
	h := il.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))

	// (A, /x): first allowed, second 429.
	if serve(h, reqTo("/x", "10.0.0.1:5000", "")).Code != 200 {
		t.Fatalf("first (A,/x) should be allowed")
	}
	if serve(h, reqTo("/x", "10.0.0.1:5001", "")).Code != http.StatusTooManyRequests {
		t.Fatalf("second (A,/x) should be 429")
	}
	// Different IP, same path ⇒ unaffected (proves the IP is part of the key).
	if got := serve(h, reqTo("/x", "10.0.0.2:6000", "")).Code; got != 200 {
		t.Fatalf("(B,/x) = %d, want 200 — a different IP must have its own bucket", got)
	}
	// Different path, same IP ⇒ unaffected (proves the path is part of the key).
	if got := serve(h, reqTo("/y", "10.0.0.1:5002", "")).Code; got != 200 {
		t.Fatalf("(A,/y) = %d, want 200 — a different path must have its own bucket", got)
	}
}

// TestIngress_WindowAdvanceAdmitsAgain proves advancing the clock past the
// window frees the bucket.
func TestIngress_WindowAdvanceAdmitsAgain(t *testing.T) {
	clock := time.Unix(1000, 0)
	il := newIngressLimiter(1, time.Minute, func() time.Time { return clock })
	h := il.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))

	if serve(h, reqTo("/x", "10.0.0.1:1", "")).Code != 200 {
		t.Fatalf("first allowed")
	}
	if serve(h, reqTo("/x", "10.0.0.1:2", "")).Code != http.StatusTooManyRequests {
		t.Fatalf("second 429")
	}
	clock = clock.Add(61 * time.Second) // past the window
	if got := serve(h, reqTo("/x", "10.0.0.1:3", "")).Code; got != 200 {
		t.Fatalf("after window = %d, want 200", got)
	}
}

// TestIngress_XForwardedForCannotBypass proves the key ignores X-Forwarded-For:
// two requests from the same RemoteAddr with DIFFERENT XFF headers still share a
// bucket, so the second is 429 (a client cannot mint a fresh bucket per request).
//
// P5-M3 (prefer X-Forwarded-For): the two requests get different keys and the
// second is allowed ⇒ RED.
func TestIngress_XForwardedForCannotBypass(t *testing.T) {
	clock := time.Unix(1000, 0)
	il := newIngressLimiter(1, time.Minute, func() time.Time { return clock })
	h := il.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))

	if serve(h, reqTo("/x", "10.0.0.9:1", "1.1.1.1")).Code != 200 {
		t.Fatalf("first allowed")
	}
	if got := serve(h, reqTo("/x", "10.0.0.9:2", "2.2.2.2")).Code; got != http.StatusTooManyRequests {
		t.Fatalf("second (same RemoteAddr, different XFF) = %d, want 429 — XFF must not change the key", got)
	}
}

// TestIngress_EnvelopePerSurface proves the 429 renders in the public shape on
// /v1/* and the control shape elsewhere, both with Retry-After.
func TestIngress_EnvelopePerSurface(t *testing.T) {
	clock := time.Unix(1000, 0)
	il := newIngressLimiter(1, time.Minute, func() time.Time { return clock })
	h := il.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))

	// Public surface.
	_ = serve(h, reqTo("/v1/chat/completions", "10.1.0.1:1", ""))
	pub := serve(h, reqTo("/v1/chat/completions", "10.1.0.1:2", ""))
	if pub.Code != http.StatusTooManyRequests || pub.Header().Get("Retry-After") == "" {
		t.Fatalf("public 429 missing status/Retry-After: %d %q", pub.Code, pub.Header().Get("Retry-After"))
	}
	var pubBody map[string]map[string]any
	_ = json.Unmarshal(pub.Body.Bytes(), &pubBody)
	if pubBody["error"]["code"] != publicErrRateLimited {
		t.Fatalf("public envelope code = %v, want %s", pubBody["error"]["code"], publicErrRateLimited)
	}
	// The public envelope carries request_id (P5-PAPI-006), correlating with the
	// X-Venom-Request-Id header. rate_limited is retryable.
	if rid, _ := pubBody["error"]["request_id"].(string); rid == "" || rid != pub.Header().Get("X-Venom-Request-Id") {
		t.Fatalf("public request_id must be present and equal X-Venom-Request-Id: body=%v header=%q", pubBody["error"]["request_id"], pub.Header().Get("X-Venom-Request-Id"))
	}
	if pubBody["error"]["retryable"] != true {
		t.Fatalf("rate_limited must be retryable, got %v", pubBody["error"]["retryable"])
	}

	// Control surface.
	_ = serve(h, reqTo("/api/control/v1/keys", "10.2.0.1:1", ""))
	ctl := serve(h, reqTo("/api/control/v1/keys", "10.2.0.1:2", ""))
	if ctl.Code != http.StatusTooManyRequests || ctl.Header().Get("Retry-After") == "" {
		t.Fatalf("control 429 missing status/Retry-After")
	}
	var ctlBody map[string]map[string]any
	_ = json.Unmarshal(ctl.Body.Bytes(), &ctlBody)
	if ctlBody["error"]["code"] != "rate_limited" || ctlBody["error"]["request_id"] == nil {
		t.Fatalf("control envelope must carry code + request_id: %s", ctl.Body.String())
	}
}

// TestIngress_429ReachesNeitherEngineNorQuota proves an ingress rejection never
// reaches the engine or quota: the provider is not called, and no usage,
// reservation, or cooldown row is written by the rejected request.
//
// P5-M4 (limiter after the engine call): the 2nd request executes ⇒ provider
// called twice / a 2nd usage row ⇒ RED.
// P5-M5 (write a cooldown on ingress 429): the limiter has no storage handle by
// construction, so this assertion pins that a 429 leaves zero cooldown rows.
func TestIngress_429ReachesNeitherEngineNorQuota(t *testing.T) {
	up, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) { _, _ = w.Write([]byte(completionJSON("ok"))) })
	db, kr := e2eEnv(t, srv.URL)
	seedCredentialedOffering(t, db, kr, "acct-1", "prov/model-a", "model-a")
	chat := newE2EHandler(t, db, kr, srv.URL, nil)

	clock := time.Unix(1000, 0)
	il := newIngressLimiter(1, time.Minute, func() time.Time { return clock })
	wrapped := il.Middleware(http.HandlerFunc(chat.ServeChat))

	body := `{"model":"venom/pro","messages":[{"role":"user","content":"hi"}]}`
	// First request executes (engine called once).
	r1 := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	r1.RemoteAddr = "10.5.0.1:1"
	rec1 := httptest.NewRecorder()
	wrapped.ServeHTTP(rec1, r1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first request status %d body %s", rec1.Code, rec1.Body.String())
	}
	// Second request from the same IP+path is ingress-429 — must NOT execute.
	r2 := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	r2.RemoteAddr = "10.5.0.1:2"
	rec2 := httptest.NewRecorder()
	wrapped.ServeHTTP(rec2, r2)
	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("second request status %d, want 429", rec2.Code)
	}

	if up.calls != 1 {
		t.Fatalf("provider called %d times; want 1 (the ingress-429 request must not reach the engine)", up.calls)
	}
	if n := countRows(t, db, "usage_records"); n != 1 {
		t.Fatalf("usage_records = %d, want 1 (ingress 429 records no usage)", n)
	}
	if n := countRows(t, db, "quota_reservations"); n != 1 {
		t.Fatalf("quota_reservations = %d, want 1 (ingress 429 consumes no reservation)", n)
	}
	if n := countRows(t, db, "cooldowns"); n != 0 {
		t.Fatalf("cooldowns = %d, want 0 (an ingress rejection is not a provider cooldown)", n)
	}
}
