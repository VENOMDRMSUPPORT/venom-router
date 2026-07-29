package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// vkFixedNow is the single injected instant for deterministic RPM tests.
var vkFixedNow = time.Unix(1_700_000_000, 0).UTC()

// seedAPIKey creates a venom_api_keys row for raw (storing only its hash), with
// the given rpm limit (nil = NULL), optionally revoked.
func seedAPIKey(t *testing.T, db *storage.DB, id, raw string, rpm *int, revoked bool) {
	t.Helper()
	repo := storage.NewAPIKeyRepo(db)
	if err := repo.Create(context.Background(), storage.CreateAPIKeyParams{
		ID: id, Label: id, KeyHash: HashAPIKey(raw), KeyPrefix: raw[:len(vkLiveKeyPrefix)+4], RPMLimit: rpm, CreatedAt: vkFixedNow,
	}); err != nil {
		t.Fatalf("seed api key %s: %v", id, err)
	}
	if revoked {
		if err := repo.Revoke(context.Background(), id, vkFixedNow); err != nil {
			t.Fatalf("revoke api key %s: %v", id, err)
		}
	}
}

// vkProbeHandler is a trivial vk-gated route: it 200s and records the
// authenticated key id it saw on the context, so a test can prove both the
// gate and the context attachment.
func vkProbeHandler(seen *string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id, ok := apiKeyIDFromContext(r.Context()); ok {
			*seen = id
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}

func vkRequest(raw string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/v1/anything", nil)
	if raw != "" {
		req.Header.Set("Authorization", "Bearer "+raw)
	}
	return req
}

// TestVKAuth_ValidKeyReaches200 proves a valid key passes the middleware and
// its id is attached to the downstream context.
//
// Mutation U2-M1: accept the key without comparing the hash → an unknown key
// would also reach 200; the unknown-key case in TestVKAuth_RejectsBadKeys RED.
func TestVKAuth_ValidKeyReaches200(t *testing.T) {
	db := testControlDB(t)
	seedAPIKey(t, db, "k-1", "vk_live_valid000", nil, false)
	auth := newVKAuthenticator(storage.NewAPIKeyRepo(db), func() time.Time { return vkFixedNow })

	var seen string
	rec := httptest.NewRecorder()
	auth.Middleware(vkProbeHandler(&seen)).ServeHTTP(rec, vkRequest("vk_live_valid000"))

	if rec.Code != http.StatusOK {
		t.Fatalf("valid key status = %d, want 200; body=%q", rec.Code, rec.Body.String())
	}
	if seen != "k-1" {
		t.Fatalf("authenticated key id on context = %q, want k-1", seen)
	}
}

// TestVKAuth_RejectsBadKeys proves every non-authenticating case returns 401
// invalid_api_key: no header, a malformed (non-vk_live) token, an unknown key,
// and a revoked key.
//
// Mutation U2-M1 (skip hash compare) → the unknown case reaches 200, RED.
// Mutation U2-M2 (ignore revoked_at) → the revoked case reaches 200, RED.
func TestVKAuth_RejectsBadKeys(t *testing.T) {
	db := testControlDB(t)
	seedAPIKey(t, db, "k-live", "vk_live_present0", nil, false)
	seedAPIKey(t, db, "k-revoked", "vk_live_revoked0", nil, true)
	auth := newVKAuthenticator(storage.NewAPIKeyRepo(db), func() time.Time { return vkFixedNow })

	cases := []struct{ name, raw string }{
		{"missing", ""},
		{"malformed", "not-a-vk-key"},
		{"unknown", "vk_live_absent00"},
		{"revoked", "vk_live_revoked0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var seen string
			rec := httptest.NewRecorder()
			auth.Middleware(vkProbeHandler(&seen)).ServeHTTP(rec, vkRequest(tc.raw))
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("%s status = %d, want 401; body=%q", tc.name, rec.Code, rec.Body.String())
			}
			if code := decodeErrorCode(t, rec.Body.Bytes()); code != publicErrInvalidAPIKey {
				t.Fatalf("%s error code = %q, want %q", tc.name, code, publicErrInvalidAPIKey)
			}
			if seen != "" {
				t.Fatalf("%s reached the gated handler (seen=%q), want rejected before it", tc.name, seen)
			}
		})
	}
}

// TestVKAuth_UnknownVsRevokedNoOracle proves the 401 for an unknown key is
// byte-identical (status AND body) to the 401 for a revoked key — no
// enumeration oracle tells them apart.
//
// Mutation U2-M9: return a different status/body for revoked vs unknown → RED.
func TestVKAuth_UnknownVsRevokedNoOracle(t *testing.T) {
	db := testControlDB(t)
	seedAPIKey(t, db, "k-revoked", "vk_live_revoked0", nil, true)
	auth := newVKAuthenticator(storage.NewAPIKeyRepo(db), func() time.Time { return vkFixedNow })

	unknownRec := httptest.NewRecorder()
	auth.Middleware(vkProbeHandler(new(string))).ServeHTTP(unknownRec, vkRequest("vk_live_absent00"))
	revokedRec := httptest.NewRecorder()
	auth.Middleware(vkProbeHandler(new(string))).ServeHTTP(revokedRec, vkRequest("vk_live_revoked0"))

	if unknownRec.Code != revokedRec.Code {
		t.Fatalf("status differs: unknown=%d revoked=%d (enumeration oracle)", unknownRec.Code, revokedRec.Code)
	}
	if unknownRec.Body.String() != revokedRec.Body.String() {
		t.Fatalf("body differs:\n unknown=%q\n revoked=%q\n(enumeration oracle)", unknownRec.Body.String(), revokedRec.Body.String())
	}
}

// TestVKAuth_PerKeyRPMIsKeyed proves the RPM limiter is genuinely PER-KEY:
// key A at rpm_limit=2 is blocked on its 3rd request within the window, the
// clock advancing past the window admits it again, and key B (its own limit)
// is entirely unaffected by A's traffic.
//
// Mutation U2-M3: drop the key from the limiter's map key (global limiter) →
// key B is throttled by key A's traffic → the "B unaffected" assertion RED.
func TestVKAuth_PerKeyRPMIsKeyed(t *testing.T) {
	db := testControlDB(t)
	seedAPIKey(t, db, "k-a", "vk_live_aaaa0000", intptr(2), false)
	seedAPIKey(t, db, "k-b", "vk_live_bbbb0000", intptr(2), false)

	now := vkFixedNow
	auth := newVKAuthenticator(storage.NewAPIKeyRepo(db), func() time.Time { return now })

	call := func(raw string) int {
		rec := httptest.NewRecorder()
		auth.Middleware(vkProbeHandler(new(string))).ServeHTTP(rec, vkRequest(raw))
		return rec.Code
	}

	// Key A: 2 allowed, 3rd blocked (429) within the same window.
	if c := call("vk_live_aaaa0000"); c != http.StatusOK {
		t.Fatalf("A req1 = %d, want 200", c)
	}
	if c := call("vk_live_aaaa0000"); c != http.StatusOK {
		t.Fatalf("A req2 = %d, want 200", c)
	}
	if c := call("vk_live_aaaa0000"); c != http.StatusTooManyRequests {
		t.Fatalf("A req3 = %d, want 429", c)
	}

	// Key B is unaffected by A's over-limit traffic — proves the limiter is keyed.
	if c := call("vk_live_bbbb0000"); c != http.StatusOK {
		t.Fatalf("B req1 = %d, want 200 (a second key must be unaffected by A)", c)
	}

	// Advancing the injected clock past the window admits A again.
	now = now.Add(perKeyRPMWindow + time.Second)
	if c := call("vk_live_aaaa0000"); c != http.StatusOK {
		t.Fatalf("A after window = %d, want 200 (sliding window should have reset)", c)
	}
}

// TestVKAuth_NullRPMUsesConfiguredDefault proves a key with a NULL rpm_limit is
// still limited at the CONFIGURED DEFAULT — never unlimited.
//
// Mutation U2-M4: treat a NULL rpm_limit as unlimited → the over-default
// request is admitted → RED.
func TestVKAuth_NullRPMUsesConfiguredDefault(t *testing.T) {
	db := testControlDB(t)
	seedAPIKey(t, db, "k-null", "vk_live_null0000", nil, false) // NULL rpm_limit
	now := vkFixedNow
	auth := newVKAuthenticator(storage.NewAPIKeyRepo(db), func() time.Time { return now })
	auth.defaultRPM = 2 // shrink the configured default so the test needs only 3 requests

	call := func() int {
		rec := httptest.NewRecorder()
		auth.Middleware(vkProbeHandler(new(string))).ServeHTTP(rec, vkRequest("vk_live_null0000"))
		return rec.Code
	}
	if call() != http.StatusOK {
		t.Fatalf("first request under a NULL-limit key must pass at the default")
	}
	if call() != http.StatusOK {
		t.Fatalf("second request under a NULL-limit key must pass at the default")
	}
	if c := call(); c != http.StatusTooManyRequests {
		t.Fatalf("3rd request = %d, want 429 — a NULL rpm_limit must enforce the default, never be unlimited", c)
	}
}

// TestVKAuth_SecretCanary proves the presented raw key never appears in the
// authentication error body or any response header (the only surfaces this
// unit produces on the vk path). Both a rejected and an accepted request are
// checked.
//
// Mutation U2-M8: include the presented key in the 401 message → RED.
func TestVKAuth_SecretCanary(t *testing.T) {
	db := testControlDB(t)
	const raw = "vk_live_canary_9f3a_MARKER"
	seedAPIKey(t, db, "k-1", raw, nil, false)
	auth := newVKAuthenticator(storage.NewAPIKeyRepo(db), func() time.Time { return vkFixedNow })

	// Rejected request (wrong key) must not echo the presented value.
	rejRec := httptest.NewRecorder()
	auth.Middleware(vkProbeHandler(new(string))).ServeHTTP(rejRec, vkRequest("vk_live_wrong_MARKER_zzz"))
	assertNoSecret(t, "401 body", rejRec.Body.String(), "vk_live_wrong_MARKER_zzz")
	assertNoSecretInHeaders(t, rejRec.Header(), "vk_live_wrong_MARKER_zzz")

	// Accepted request must not echo the real key in the body or headers.
	okRec := httptest.NewRecorder()
	auth.Middleware(vkProbeHandler(new(string))).ServeHTTP(okRec, vkRequest(raw))
	assertNoSecret(t, "200 body", okRec.Body.String(), raw)
	assertNoSecretInHeaders(t, okRec.Header(), raw)
}

func assertNoSecret(t *testing.T, where, haystack, secret string) {
	t.Helper()
	if strings.Contains(haystack, secret) {
		t.Fatalf("%s leaked the presented key %q", where, secret)
	}
	// The plain non-credential marker must also be absent — a credential-shaped
	// string alone could be scrubbed by a future redactor and prove nothing.
	if strings.Contains(haystack, "MARKER") {
		t.Fatalf("%s leaked the plain marker fragment of the presented key", where)
	}
}

func assertNoSecretInHeaders(t *testing.T, h http.Header, secret string) {
	t.Helper()
	for name, vals := range h {
		for _, v := range vals {
			if strings.Contains(v, secret) || strings.Contains(v, "MARKER") {
				t.Fatalf("response header %q leaked the presented key", name)
			}
		}
	}
}

// TestKeyedSlidingWindowLimiter_Independent is a direct unit test of the keyed
// limiter: two keys accrue independent windows.
func TestKeyedSlidingWindowLimiter_Independent(t *testing.T) {
	l := newKeyedSlidingWindowLimiter(time.Minute)
	now := vkFixedNow
	if !l.Allow("a", 1, now) || l.Allow("a", 1, now) {
		t.Fatalf("key a: first Allow true, second false expected")
	}
	if !l.Allow("b", 1, now) {
		t.Fatalf("key b must have its own independent window")
	}
	if !l.Allow("a", 1, now.Add(2*time.Minute)) {
		t.Fatalf("key a after the window should be admitted again")
	}
}
