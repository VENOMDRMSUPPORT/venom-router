package httpapi

// usageread_test.go exercises GET /api/control/v1/usage (P6-CAPI-EXTRA-2,
// enables P6-UI-005).
//
// The property worth more than all the others here: a NULL metric is UNKNOWN and
// never becomes a 0 on the wire. `sum` is JSON null when no contributing row
// reported a value, `average` is null for the same reason and is NEVER divided by
// the row count, and `unknown_count` is always present so the dashboard can say
// "3 of 40 requests report no token count" instead of presenting a floor as a
// total.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// newUsageFixture wires a UsageHandler over a fresh migrated DB.
func newUsageFixture(t *testing.T) (*UsageHandler, *storage.DB) {
	t.Helper()
	db := testControlDB(t)
	return NewUsageHandler(storage.NewUsageRecordRepo(db)), db
}

// seedUsageRow inserts one usage row directly; nil metrics stay NULL.
func seedUsageRow(t *testing.T, db *storage.DB, id, tier string, account, model *string, tokensIn, tokensOut, latency *int, at int64) {
	t.Helper()
	var acctArg, modelArg, inArg, outArg, latArg any
	if account != nil {
		acctArg = *account
	}
	if model != nil {
		modelArg = *model
	}
	if tokensIn != nil {
		inArg = *tokensIn
	}
	if tokensOut != nil {
		outArg = *tokensOut
	}
	if latency != nil {
		latArg = *latency
	}
	if _, err := db.Conn().Exec(
		`INSERT INTO usage_records (id, request_id, tier, account_id, provider_model_id, status, latency_ms, tokens_in, tokens_out, created_at)
		 VALUES (?, ?, ?, ?, ?, 'success', ?, ?, ?, ?)`,
		id, id+"-req", tier, acctArg, modelArg, latArg, inArg, outArg, at,
	); err != nil {
		t.Fatalf("seed usage %s: %v", id, err)
	}
}

func usageIntPtr(v int) *int       { return &v }
func usageStrPtr(v string) *string { return &v }

// serveUsage drives the handler and returns the decoded envelope.
func serveUsage(t *testing.T, h *UsageHandler, method, query string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, "/api/control/v1/usage"+query, nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Host = testAllowedHost
	rec := httptest.NewRecorder()
	h.ServeUsage(rec, req)
	var body map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode %q: %v", rec.Body.String(), err)
		}
	}
	return rec, body
}

func usageData(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	data, ok := body["data"].(map[string]any)
	if !ok {
		t.Fatalf("data is not an object: %#v", body["data"])
	}
	return data
}

// usageGroupByKey finds a group in one of the grouping arrays. A nil key is
// matched by the sentinel "" (the JSON null case).
func usageGroupByKey(t *testing.T, data map[string]any, dimension, key string) map[string]any {
	t.Helper()
	list, ok := data[dimension].([]any)
	if !ok {
		t.Fatalf("%s is not a list: %#v", dimension, data[dimension])
	}
	for _, entry := range list {
		g := entry.(map[string]any)
		k := g["key"]
		if key == "" && k == nil {
			return g
		}
		if k == key {
			return g
		}
	}
	t.Fatalf("%s group %q not found in %#v", dimension, key, list)
	return nil
}

func usageMetric(t *testing.T, group map[string]any, name string) map[string]any {
	t.Helper()
	m, ok := group[name].(map[string]any)
	if !ok {
		t.Fatalf("metric %q is not an object: %#v", name, group[name])
	}
	return m
}

// TestUsage_UnknownMetricsAreNullNotZero is the central fail-closed proof: a NULL
// token count is excluded from the sum, excluded from the average's denominator,
// and REPORTED as an unknown count.
func TestUsage_UnknownMetricsAreNullNotZero(t *testing.T) {
	h, db := newUsageFixture(t)
	acct := usageStrPtr("acct-1")
	// Two known token counts (10, 30) and one unknown.
	seedUsageRow(t, db, "u1", "pro", acct, nil, usageIntPtr(10), usageIntPtr(1), usageIntPtr(100), 1000)
	seedUsageRow(t, db, "u2", "pro", acct, nil, usageIntPtr(30), usageIntPtr(3), nil, 1001)
	seedUsageRow(t, db, "u3", "pro", acct, nil, nil, nil, usageIntPtr(300), 1002)

	rec, body := serveUsage(t, h, http.MethodGet, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	data := usageData(t, body)

	totals, ok := data["totals"].(map[string]any)
	if !ok {
		t.Fatalf("totals is not an object: %#v", data["totals"])
	}
	if totals["requests"] != float64(3) {
		t.Fatalf("requests = %#v, want 3", totals["requests"])
	}

	tokensIn := usageMetric(t, totals, "tokens_in")
	if tokensIn["sum"] != float64(40) {
		t.Errorf("tokens_in.sum = %#v, want 40 (the NULL row contributes nothing, not 0)", tokensIn["sum"])
	}
	if tokensIn["known_count"] != float64(2) {
		t.Errorf("tokens_in.known_count = %#v, want 2", tokensIn["known_count"])
	}
	if tokensIn["unknown_count"] != float64(1) {
		t.Errorf("tokens_in.unknown_count = %#v, want 1 — without this the dashboard cannot say \"1 of 3 unknown\"", tokensIn["unknown_count"])
	}
	// The average's denominator is known_count (2), NOT the request count (3).
	if tokensIn["average"] != float64(20) {
		t.Errorf("tokens_in.average = %#v, want 20 (40/2) — dividing by the 3 requests would give 13.33 and understate every known row", tokensIn["average"])
	}

	// Latency has its own independent tally: u2 reported none.
	latency := usageMetric(t, totals, "latency_ms")
	if latency["sum"] != float64(400) || latency["unknown_count"] != float64(1) {
		t.Errorf("latency_ms = (sum %#v, unknown %#v), want (400, 1) — each dimension tallies separately",
			latency["sum"], latency["unknown_count"])
	}
}

// TestUsage_AllUnknownGroupReportsNullSumAndAverage proves a group whose every row
// is unknown serves `sum: null` and `average: null` — never 0, which would claim a
// measured absence of consumption.
func TestUsage_AllUnknownGroupReportsNullSumAndAverage(t *testing.T) {
	h, db := newUsageFixture(t)
	acct := usageStrPtr("acct-dark")
	seedUsageRow(t, db, "u1", "lite", acct, nil, nil, nil, nil, 1000)
	seedUsageRow(t, db, "u2", "lite", acct, nil, nil, nil, nil, 1001)

	rec, body := serveUsage(t, h, http.MethodGet, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	data := usageData(t, body)
	totals := data["totals"].(map[string]any)

	for _, name := range []string{"tokens_in", "tokens_out", "latency_ms"} {
		m := usageMetric(t, totals, name)
		for _, field := range []string{"sum", "average"} {
			v, present := m[field]
			if !present {
				t.Errorf("%s.%s is absent, want an explicit null", name, field)
			}
			if v != nil {
				t.Errorf("%s.%s = %#v, want null — every contributing row was unknown", name, field, v)
			}
		}
		if m["unknown_count"] != float64(2) {
			t.Errorf("%s.unknown_count = %#v, want 2", name, m["unknown_count"])
		}
		if m["known_count"] != float64(0) {
			t.Errorf("%s.known_count = %#v, want 0", name, m["known_count"])
		}
	}
	// The requests still happened, and that count is real.
	if totals["requests"] != float64(2) {
		t.Errorf("requests = %#v, want 2", totals["requests"])
	}
	// And no zero-sum snuck onto the wire.
	if strings.Contains(rec.Body.String(), `"sum":0`) {
		t.Fatalf("body contains a zero sum for an all-unknown group: %s", rec.Body.String())
	}
}

// TestUsage_GroupsByAccountModelAndTier proves the three groupings are served
// independently, each with its own tallies.
func TestUsage_GroupsByAccountModelAndTier(t *testing.T) {
	h, db := newUsageFixture(t)
	a1, a2 := usageStrPtr("acct-1"), usageStrPtr("acct-2")
	m1, m2 := usageStrPtr("model-1"), usageStrPtr("model-2")
	seedUsageRow(t, db, "u1", "pro", a1, m1, usageIntPtr(10), usageIntPtr(1), usageIntPtr(10), 1000)
	seedUsageRow(t, db, "u2", "pro", a1, m2, usageIntPtr(20), usageIntPtr(2), usageIntPtr(20), 1001)
	seedUsageRow(t, db, "u3", "lite", a2, m1, nil, usageIntPtr(3), usageIntPtr(30), 1002)

	_, body := serveUsage(t, h, http.MethodGet, "")
	data := usageData(t, body)

	acct1 := usageGroupByKey(t, data, "by_account", "acct-1")
	if acct1["requests"] != float64(2) || usageMetric(t, acct1, "tokens_in")["sum"] != float64(30) {
		t.Errorf("acct-1 = (%#v requests, tokens %#v), want (2, 30)", acct1["requests"], usageMetric(t, acct1, "tokens_in")["sum"])
	}
	acct2 := usageGroupByKey(t, data, "by_account", "acct-2")
	if usageMetric(t, acct2, "tokens_in")["sum"] != nil {
		t.Errorf("acct-2 tokens_in.sum = %#v, want null", usageMetric(t, acct2, "tokens_in")["sum"])
	}

	model1 := usageGroupByKey(t, data, "by_model", "model-1")
	tokens := usageMetric(t, model1, "tokens_in")
	if tokens["known_count"] != float64(1) || tokens["unknown_count"] != float64(1) {
		t.Errorf("model-1 tokens_in known/unknown = %#v/%#v, want 1/1", tokens["known_count"], tokens["unknown_count"])
	}

	if usageGroupByKey(t, data, "by_tier", "pro")["requests"] != float64(2) {
		t.Errorf("tier pro requests wrong")
	}
	if usageGroupByKey(t, data, "by_tier", "lite")["requests"] != float64(1) {
		t.Errorf("tier lite requests wrong")
	}
}

// TestUsage_UnattributedGroupHasNullKey proves a row with a NULL account_id forms
// its own group with `key: null`, rather than being dropped or folded into a named
// account. Dropping understates the total; folding attributes consumption to an
// account that did not incur it.
func TestUsage_UnattributedGroupHasNullKey(t *testing.T) {
	h, db := newUsageFixture(t)
	seedUsageRow(t, db, "u1", "pro", usageStrPtr("acct-known"), nil, usageIntPtr(5), nil, nil, 1000)
	seedUsageRow(t, db, "u2", "pro", nil, nil, usageIntPtr(7), nil, nil, 1001)

	_, body := serveUsage(t, h, http.MethodGet, "")
	data := usageData(t, body)

	unattributed := usageGroupByKey(t, data, "by_account", "")
	if unattributed["requests"] != float64(1) {
		t.Errorf("unattributed requests = %#v, want 1", unattributed["requests"])
	}
	if usageMetric(t, unattributed, "tokens_in")["sum"] != float64(7) {
		t.Errorf("unattributed tokens_in.sum = %#v, want 7", usageMetric(t, unattributed, "tokens_in")["sum"])
	}
	// Both rows counted overall.
	if data["totals"].(map[string]any)["requests"] != float64(2) {
		t.Errorf("totals.requests = %#v, want 2", data["totals"].(map[string]any)["requests"])
	}
}

// TestUsage_TimeWindowAndTruncation proves the ?from/?to window is applied and
// echoed, and that a capped scan reports `truncated: true` so the dashboard can
// present the numbers as a floor.
func TestUsage_TimeWindowAndTruncation(t *testing.T) {
	h, db := newUsageFixture(t)
	acct := usageStrPtr("acct-1")
	seedUsageRow(t, db, "before", "pro", acct, nil, usageIntPtr(1), nil, nil, 900)
	seedUsageRow(t, db, "at-from", "pro", acct, nil, usageIntPtr(2), nil, nil, 1000)
	seedUsageRow(t, db, "inside", "pro", acct, nil, usageIntPtr(4), nil, nil, 1500)
	seedUsageRow(t, db, "at-to", "pro", acct, nil, usageIntPtr(8), nil, nil, 2000)

	_, body := serveUsage(t, h, http.MethodGet, "?from=1000&to=2000")
	data := usageData(t, body)

	// from inclusive, to exclusive: 2 + 4 = 6.
	if usageMetric(t, data["totals"].(map[string]any), "tokens_in")["sum"] != float64(6) {
		t.Fatalf("tokens_in.sum = %#v, want 6", usageMetric(t, data["totals"].(map[string]any), "tokens_in")["sum"])
	}
	window, ok := data["window"].(map[string]any)
	if !ok {
		t.Fatalf("window is not an object: %#v", data["window"])
	}
	if window["from"] != float64(1000) || window["to"] != float64(2000) {
		t.Fatalf("window = %#v, want from 1000 to 2000", window)
	}

	// An unbounded window reports both ends as null rather than inventing them.
	_, allBody := serveUsage(t, h, http.MethodGet, "")
	allWindow := usageData(t, allBody)["window"].(map[string]any)
	for _, end := range []string{"from", "to"} {
		v, present := allWindow[end]
		if !present {
			t.Errorf("window.%s absent, want an explicit null", end)
		}
		if v != nil {
			t.Errorf("window.%s = %#v, want null for an unbounded window", end, v)
		}
	}

	// Truncation.
	_, capped := serveUsage(t, h, http.MethodGet, "?limit=2")
	cappedData := usageData(t, capped)
	if cappedData["truncated"] != true {
		t.Fatalf("truncated = %#v, want true for limit=2 over 4 rows", cappedData["truncated"])
	}
	if cappedData["scanned"] != float64(2) {
		t.Fatalf("scanned = %#v, want 2", cappedData["scanned"])
	}
}

// TestUsage_EmptyWindowReportsNoMeasurement proves an empty window reports 0
// requests (a real row count) with UNKNOWN sums — nothing was measured, so nothing
// is claimed. This is what lets the dashboard tell "no traffic" apart from "traffic
// with no token data".
func TestUsage_EmptyWindowReportsNoMeasurement(t *testing.T) {
	h, _ := newUsageFixture(t)

	_, body := serveUsage(t, h, http.MethodGet, "")
	data := usageData(t, body)
	totals := data["totals"].(map[string]any)

	if totals["requests"] != float64(0) {
		t.Fatalf("requests = %#v, want 0", totals["requests"])
	}
	tokensIn := usageMetric(t, totals, "tokens_in")
	if tokensIn["sum"] != nil {
		t.Fatalf("tokens_in.sum = %#v, want null for an empty window", tokensIn["sum"])
	}
	if tokensIn["unknown_count"] != float64(0) {
		t.Fatalf("tokens_in.unknown_count = %#v, want 0 — no row was unknown because no row exists", tokensIn["unknown_count"])
	}
	for _, dimension := range []string{"by_account", "by_model", "by_tier"} {
		list, ok := data[dimension].([]any)
		if !ok {
			t.Fatalf("%s is not a list: %#v", dimension, data[dimension])
		}
		if len(list) != 0 {
			t.Fatalf("%s = %#v, want an empty list", dimension, list)
		}
	}
}

// TestUsage_FieldSetFreeze pins the exact key sets and doubles as the SECRET
// CANARY: the payload carries ids, counts, tier names and timestamps only, and no
// key may contain a content-carrying substring. A future field named `prompt`,
// `response`, `raw_error` or `external_id` fails here even if nobody remembers to
// poison it.
func TestUsage_FieldSetFreeze(t *testing.T) {
	h, db := newUsageFixture(t)
	seedUsageRow(t, db, "u1", "pro", usageStrPtr("acct-1"), usageStrPtr("model-1"), usageIntPtr(10), usageIntPtr(2), usageIntPtr(50), 1000)

	rec, body := serveUsage(t, h, http.MethodGet, "")
	data := usageData(t, body)

	wantTop := []string{"by_account", "by_model", "by_tier", "limit", "scanned", "totals", "truncated", "window"}
	if got := sortedKeys(data); strings.Join(got, ",") != strings.Join(wantTop, ",") {
		t.Fatalf("top-level key set = %v, want %v", got, wantTop)
	}
	wantGroup := []string{"key", "latency_ms", "requests", "tokens_in", "tokens_out"}
	if got := sortedKeys(data["totals"].(map[string]any)); strings.Join(got, ",") != strings.Join(wantGroup, ",") {
		t.Fatalf("group key set = %v, want %v", got, wantGroup)
	}
	wantMetric := []string{"average", "known_count", "sum", "unknown_count"}
	if got := sortedKeys(usageMetric(t, data["totals"].(map[string]any), "tokens_in")); strings.Join(got, ",") != strings.Join(wantMetric, ",") {
		t.Fatalf("metric key set = %v, want %v", got, wantMetric)
	}

	forbidden := []string{
		"prompt", "response", "content", "message", "body", "text", "raw",
		"credential", "token_value", "secret", "authorization", "api_key", "external_id", "email",
	}
	var walk func(prefix string, v any)
	walk = func(prefix string, v any) {
		switch node := v.(type) {
		case map[string]any:
			for k, child := range node {
				for _, bad := range forbidden {
					if strings.Contains(k, bad) {
						t.Fatalf("payload key %q (at %s) contains the content-carrying substring %q", k, prefix, bad)
					}
				}
				walk(prefix+"."+k, child)
			}
		case []any:
			for i, child := range node {
				walk(prefix, child)
				_ = i
			}
		}
	}
	walk("data", data)

	// The api_key_id column exists on the table but is deliberately NOT projected:
	// key attribution belongs to the keys surface, and this aggregate has no need
	// of it.
	if strings.Contains(rec.Body.String(), "api_key") {
		t.Fatalf("payload mentions api_key: %s", rec.Body.String())
	}
}

// TestUsage_MethodNotAllowed proves the surface is read-only.
func TestUsage_MethodNotAllowed(t *testing.T) {
	h, _ := newUsageFixture(t)
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			rec, body := serveUsage(t, h, method, "")
			if rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want 405", rec.Code)
			}
			if _, present := body["data"]; present {
				t.Fatalf("a 405 must carry no data, got %#v", body["data"])
			}
		})
	}
}

// TestUsage_IsOwnerGatedThroughTheRealMux proves the route goes through
// `gated(...)` in the REAL ControlMux — every other test here drives the handler
// directly, so a bare networkGate would leave them all green while exposing
// consumption history to any loopback caller.
func TestUsage_IsOwnerGatedThroughTheRealMux(t *testing.T) {
	db := testControlDB(t)
	realMux := ControlMux(testAllowedHost, fakeSPA(), db, testKeyring(t))

	rec := httptest.NewRecorder()
	realMux.ServeHTTP(rec, newAuthRequest(t, http.MethodGet, "/api/control/v1/usage", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401 (body %s)", rec.Code, rec.Body.String())
	}
	// Vacuity guard: the 401 must come from the AUTH gate, not from the route
	// being absent (an unregistered path falls through to the SPA).
	if strings.Contains(rec.Body.String(), "<html") {
		t.Fatalf("the route is not registered — the request fell through to the SPA: %s", rec.Body.String())
	}
}

// TestUsage_ReadErrorServesNoPartialAggregate proves a failed read is a typed 500
// with no data — never an empty aggregate, which would render as "no usage".
func TestUsage_ReadErrorServesNoPartialAggregate(t *testing.T) {
	h, db := newUsageFixture(t)
	// Drop the table out from under the reader: the query now fails for real.
	if _, err := db.Conn().Exec(`DROP TABLE usage_records`); err != nil {
		t.Fatalf("drop table: %v", err)
	}

	rec, body := serveUsage(t, h, http.MethodGet, "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (body %s)", rec.Code, rec.Body.String())
	}
	if _, present := body["data"]; present {
		t.Fatalf("a read error must carry NO data — an empty aggregate renders as \"no usage\"; got %#v", body["data"])
	}
	if strings.Contains(rec.Body.String(), "usage_records") {
		t.Fatalf("the 500 leaked the storage error: %s", rec.Body.String())
	}
}

// usageUnusedTime keeps the time import honest if the window helpers change.
var _ = time.Unix
