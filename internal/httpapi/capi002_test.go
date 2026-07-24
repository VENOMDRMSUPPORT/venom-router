package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/sanitize"
)

// --- Envelope shape ---

func TestEnvelope_WriteDataShape(t *testing.T) {
	rec := httptest.NewRecorder()
	writeData(rec, http.StatusOK, map[string]any{"foo": "bar"})

	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := got["data"]; !ok {
		t.Fatalf("response missing \"data\" key: %s", rec.Body.String())
	}
	if _, ok := got["meta"]; ok {
		t.Fatalf("response has \"meta\" when none was requested: %s", rec.Body.String())
	}
}

func TestEnvelope_WriteDataMetaShape(t *testing.T) {
	rec := httptest.NewRecorder()
	writeDataMeta(rec, http.StatusOK, map[string]any{"x": 1}, map[string]any{"next_cursor": "c2"})

	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	meta, ok := got["meta"].(map[string]any)
	if !ok {
		t.Fatalf("response missing \"meta\" object: %s", rec.Body.String())
	}
	if meta["next_cursor"] != "c2" {
		t.Fatalf("meta.next_cursor = %v, want %q", meta["next_cursor"], "c2")
	}
}

func TestEnvelope_WriteErrorDetailsShape(t *testing.T) {
	rec := httptest.NewRecorder()
	writeErrorDetails(rec, http.StatusBadRequest, "validation_error", "bad request", false, map[string]any{"field": "password"})

	var got struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Error.Code != "validation_error" {
		t.Fatalf("code = %q, want validation_error", got.Error.Code)
	}
	if got.Error.Details["field"] != "password" {
		t.Fatalf("details.field = %v, want password", got.Error.Details["field"])
	}
}

func TestEnvelope_WriteErrorDetails_NilOmitsDetails(t *testing.T) {
	rec := httptest.NewRecorder()
	writeErrorDetails(rec, http.StatusInternalServerError, "internal", "internal error", true, nil)

	if strings.Contains(rec.Body.String(), "details") {
		t.Fatalf("response unexpectedly contains \"details\" when none was given: %s", rec.Body.String())
	}
}

// --- Idempotency ---

func TestIdempotency_ReplaySameKeyReturnsFirstResultNoRerun(t *testing.T) {
	store := newIdempotencyStore()
	var runs int
	handler := func(w http.ResponseWriter, r *http.Request) {
		runs++
		writeData(w, http.StatusCreated, map[string]any{"run": runs})
	}

	req1 := httptest.NewRequest(http.MethodPost, "/api/control/v1/test/create", nil)
	req1.Header.Set(idempotencyHeader, "key-a")
	rec1 := httptest.NewRecorder()
	store.Execute(rec1, req1, "POST /test/create", handler)

	req2 := httptest.NewRequest(http.MethodPost, "/api/control/v1/test/create", nil)
	req2.Header.Set(idempotencyHeader, "key-a")
	rec2 := httptest.NewRecorder()
	store.Execute(rec2, req2, "POST /test/create", handler)

	if runs != 1 {
		t.Fatalf("handler ran %d times, want 1 (a replay must not re-run it)", runs)
	}
	if rec1.Code != rec2.Code || rec1.Body.String() != rec2.Body.String() {
		t.Fatalf("replay response differs from original: first={%d %q} second={%d %q}",
			rec1.Code, rec1.Body.String(), rec2.Code, rec2.Body.String())
	}
}

func TestIdempotency_DifferentKeyRunsFresh(t *testing.T) {
	store := newIdempotencyStore()
	var runs int
	handler := func(w http.ResponseWriter, r *http.Request) {
		runs++
		writeData(w, http.StatusCreated, map[string]any{"run": runs})
	}

	req1 := httptest.NewRequest(http.MethodPost, "/api/control/v1/test/create", nil)
	req1.Header.Set(idempotencyHeader, "key-a")
	store.Execute(httptest.NewRecorder(), req1, "POST /test/create", handler)

	req2 := httptest.NewRequest(http.MethodPost, "/api/control/v1/test/create", nil)
	req2.Header.Set(idempotencyHeader, "key-b")
	store.Execute(httptest.NewRecorder(), req2, "POST /test/create", handler)

	if runs != 2 {
		t.Fatalf("handler ran %d times, want 2 (a different key must run fresh)", runs)
	}
}

func TestIdempotency_NoKeyAlwaysRunsFresh(t *testing.T) {
	store := newIdempotencyStore()
	var runs int
	handler := func(w http.ResponseWriter, r *http.Request) {
		runs++
		writeData(w, http.StatusCreated, map[string]any{"run": runs})
	}

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/control/v1/test/create", nil)
		store.Execute(httptest.NewRecorder(), req, "POST /test/create", handler)
	}

	if runs != 3 {
		t.Fatalf("handler ran %d times, want 3 (no Idempotency-Key means no idempotency applied)", runs)
	}
}

func TestIdempotency_SameKeyDifferentRouteRunsFresh(t *testing.T) {
	store := newIdempotencyStore()
	var runs int
	handler := func(w http.ResponseWriter, r *http.Request) {
		runs++
		writeData(w, http.StatusCreated, nil)
	}

	req1 := httptest.NewRequest(http.MethodPost, "/a", nil)
	req1.Header.Set(idempotencyHeader, "shared-key")
	store.Execute(httptest.NewRecorder(), req1, "POST /a", handler)

	req2 := httptest.NewRequest(http.MethodPost, "/b", nil)
	req2.Header.Set(idempotencyHeader, "shared-key")
	store.Execute(httptest.NewRecorder(), req2, "POST /b", handler)

	if runs != 2 {
		t.Fatalf("handler ran %d times, want 2 (idempotency is keyed per-route, not globally per-key)", runs)
	}
}

// --- Optimistic concurrency ---

func TestConcurrency_MismatchRejected(t *testing.T) {
	req := httptest.NewRequest(http.MethodPut, "/x", nil)
	req.Header.Set("If-Match", `"v1"`)

	rec := httptest.NewRecorder()
	if requireMatchingVersion(rec, ifMatchVersion(req), "v2") {
		t.Fatalf("requireMatchingVersion = true, want false for a version mismatch")
	}
	if rec.Code != http.StatusPreconditionFailed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusPreconditionFailed)
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != "precondition_failed" {
		t.Fatalf("error code = %q, want precondition_failed", code)
	}
}

func TestConcurrency_MatchProceeds(t *testing.T) {
	req := httptest.NewRequest(http.MethodPut, "/x", nil)
	req.Header.Set("If-Match", `"v2"`)

	rec := httptest.NewRecorder()
	if !requireMatchingVersion(rec, ifMatchVersion(req), "v2") {
		t.Fatalf("requireMatchingVersion = false, want true for a matching version")
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("requireMatchingVersion wrote a response on success: %q", rec.Body.String())
	}
}

func TestIfMatchVersion_StripsQuotes(t *testing.T) {
	req := httptest.NewRequest(http.MethodPut, "/x", nil)
	req.Header.Set("If-Match", `"quoted-version"`)
	if got := ifMatchVersion(req); got != "quoted-version" {
		t.Fatalf("ifMatchVersion = %q, want %q (quotes stripped)", got, "quoted-version")
	}
}

// --- Pagination ---

func TestPagination_ParsesLimitAndCursor(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/x?limit=10&cursor=abc", nil)
	p := parsePageParams(req, defaultPageLimit, maxPageLimit)
	if p.Limit != 10 || p.Cursor != "abc" {
		t.Fatalf("params = %+v, want {10 abc}", p)
	}
}

func TestPagination_ClampsLimitToMax(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/x?limit=999999", nil)
	p := parsePageParams(req, defaultPageLimit, maxPageLimit)
	if p.Limit != maxPageLimit {
		t.Fatalf("Limit = %d, want clamped to %d", p.Limit, maxPageLimit)
	}
}

func TestPagination_DefaultsWhenMissingOrInvalid(t *testing.T) {
	for _, q := range []string{"", "?limit=0", "?limit=-5", "?limit=not-a-number"} {
		req := httptest.NewRequest(http.MethodGet, "/x"+q, nil)
		p := parsePageParams(req, defaultPageLimit, maxPageLimit)
		if p.Limit != defaultPageLimit {
			t.Fatalf("query %q: Limit = %d, want default %d", q, p.Limit, defaultPageLimit)
		}
	}
}

func TestPaginationMeta_EmptyCursorOmitted(t *testing.T) {
	if paginationMeta("") != nil {
		t.Fatalf("paginationMeta(\"\") = %v, want nil", paginationMeta(""))
	}
	meta := paginationMeta("next-1")
	m, ok := meta.(map[string]any)
	if !ok || m["next_cursor"] != "next-1" {
		t.Fatalf("paginationMeta(\"next-1\") = %v, want {next_cursor: next-1}", meta)
	}
}

// --- Redaction ---

// TestRedaction_SecretValueNeverEchoedRaw is CAPI-002's canary: a
// distinctive secret value fed through redactedFields under a
// secret-shaped key must never survive into the written response —
// only the sanitize placeholder may appear — while an ordinary field
// passes through unchanged.
func TestRedaction_SecretValueNeverEchoedRaw(t *testing.T) {
	const canarySecret = "CANARY-9f3Kx2Qw8pLm0Zt7Vb4Nr1Hy6Dc5Ea-redaction"

	fields := redactedFields(map[string]string{
		"api_key":      canarySecret,
		"display_name": "My Account",
	})

	rec := httptest.NewRecorder()
	writeData(rec, http.StatusOK, fields)

	assertNoFragment(t, rec.Body.String(), canarySecret, "redacted response body")
	if fields["api_key"] != sanitize.Placeholder {
		t.Fatalf("fields[api_key] = %v, want the sanitize placeholder", fields["api_key"])
	}
	if fields["display_name"] != "My Account" {
		t.Fatalf("fields[display_name] = %v, want unchanged (not a secret field)", fields["display_name"])
	}
}
