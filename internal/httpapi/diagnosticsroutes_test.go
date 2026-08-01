package httpapi

// diagnosticsroutes_test.go exercises the P6-CAPI-001 route-explanation
// surface: GET /diagnostics/routes and GET /diagnostics/routes/{request_id}
// (09 §3.9, 05 §7). Records are seeded through the REAL
// observability.RouteRecorder, so these tests read exactly what production
// writes.

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/observability"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

var routesFixedNow = time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)

type routesFixture struct {
	handler  *DiagnosticsHandler
	recorder *observability.RouteRecorder
	// db is exposed so a test can force a value PAST the recorder's own
	// normalization straight into the column — the read path's fail-closed
	// behaviour is only testable that way.
	db *storage.DB
}

// newRoutesFixture wires a DiagnosticsHandler with a real RouteReader over a
// fresh migrated DB, plus the RouteRecorder used to seed it.
func newRoutesFixture(t *testing.T) *routesFixture {
	t.Helper()
	db := testControlDB(t)
	reader := observability.NewRouteReader(db.Conn())
	handler := NewDiagnosticsHandler(nil, nil, newAuditEmitter(db, nil)).WithRoutes(reader)
	return &routesFixture{
		handler:  handler,
		recorder: observability.NewRouteRecorder(db.Conn(), nil),
		db:       db,
	}
}

func (f *routesFixture) seedDecision(t *testing.T, d observability.RouteDecision) {
	t.Helper()
	if err := f.recorder.RecordDecision(t.Context(), d); err != nil {
		t.Fatalf("RecordDecision(%s): %v", d.ID, err)
	}
}

func (f *routesFixture) seedAttempt(t *testing.T, a observability.RouteAttempt) {
	t.Helper()
	if err := f.recorder.RecordAttempt(t.Context(), a); err != nil {
		t.Fatalf("RecordAttempt(%s): %v", a.ID, err)
	}
}

// routesSampleDecision is a fully-populated decision so every projected field
// has a non-zero value to assert against.
func routesSampleDecision(id, requestID string, at time.Time) observability.RouteDecision {
	return observability.RouteDecision{
		ID:                    id,
		RequestID:             requestID,
		Tier:                  "pro",
		WorkloadProfileBucket: "standard",
		CandidateSummary: observability.CandidateSummary{
			TotalCandidates: 7, EligibleGroups: 3, GroupKeys: []string{"prov-a/model-a", "prov-b/model-b"},
		},
		ExclusionReasons:      map[string]int{"funding_ineligible": 2, "capability_uncertified": 1},
		ChosenProviderID:      "prov-a",
		ChosenProviderModelID: "model-a",
		ChosenFunding:         "free",
		Scores:                map[string]float64{"chosen_quality": 0.91},
		RequestedThinking:     "extended",
		AppliedThinking:       "standard",
		TierClamped:           true,
		CertifiedClamped:      false,
		CreatedAt:             at,
	}
}

// getRoutes performs GET on the list route and returns the decoded envelope.
func (f *routesFixture) getRoutes(t *testing.T, query string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/control/v1/diagnostics/routes"+query, nil)
	rec := httptest.NewRecorder()
	f.handler.ServeRoutes(rec, req)
	var body map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode list body %q: %v", rec.Body.String(), err)
		}
	}
	return rec, body
}

// getExplanation performs GET on the explanation route for requestID.
func (f *routesFixture) getExplanation(t *testing.T, requestID string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/control/v1/diagnostics/routes/"+requestID, nil)
	req.SetPathValue("request_id", requestID)
	rec := httptest.NewRecorder()
	f.handler.ServeRouteExplanation(rec, req)
	var body map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode explanation body %q: %v", rec.Body.String(), err)
		}
	}
	return rec, body
}

func keysOf(t *testing.T, m map[string]any) []string {
	t.Helper()
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func dataList(t *testing.T, body map[string]any) []any {
	t.Helper()
	data, ok := body["data"].([]any)
	if !ok {
		t.Fatalf("data is not a list: %#v", body["data"])
	}
	return data
}

func dataObject(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	data, ok := body["data"].(map[string]any)
	if !ok {
		t.Fatalf("data is not an object: %#v", body["data"])
	}
	return data
}

// TestDiagnosticsRoutes_ListOrderingAndPagination proves the list is newest
// first with a deterministic tie-break, and that limit/cursor page it exactly
// as GET /accounts' shared helper does — an offset past the end being an
// EMPTY list rather than an error.
func TestDiagnosticsRoutes_ListOrderingAndPagination(t *testing.T) {
	f := newRoutesFixture(t)

	// dec-a and dec-b tie on created_at; dec-c is newer.
	f.seedDecision(t, routesSampleDecision("dec-a", "req-a", routesFixedNow))
	f.seedDecision(t, routesSampleDecision("dec-b", "req-b", routesFixedNow))
	f.seedDecision(t, routesSampleDecision("dec-c", "req-c", routesFixedNow.Add(time.Minute)))

	idsFor := func(query string) []string {
		rec, body := f.getRoutes(t, query)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
		}
		out := []string{}
		for _, entry := range dataList(t, body) {
			out = append(out, entry.(map[string]any)["decision_id"].(string))
		}
		return out
	}

	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{name: "default page is newest first, tie broken by id", query: "", want: []string{"dec-c", "dec-b", "dec-a"}},
		{name: "limit bounds the page", query: "?limit=2", want: []string{"dec-c", "dec-b"}},
		{name: "cursor resumes after the first page", query: "?limit=2&cursor=2", want: []string{"dec-a"}},
		{name: "cursor past the end is an empty list", query: "?limit=2&cursor=99", want: []string{}},
		{name: "unparsable cursor starts from the beginning", query: "?limit=1&cursor=not-a-number", want: []string{"dec-c"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := idsFor(tc.query)
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("decision ids = %v, want %v", got, tc.want)
			}
		})
	}

	// A full page advertises the next cursor; the last page does not.
	_, body := f.getRoutes(t, "?limit=2")
	meta, ok := body["meta"].(map[string]any)
	if !ok {
		t.Fatalf("a full page must advertise meta.next_cursor, got %#v", body["meta"])
	}
	if meta["next_cursor"] != "2" {
		t.Fatalf("next_cursor = %#v, want \"2\"", meta["next_cursor"])
	}
	_, lastBody := f.getRoutes(t, "?limit=50")
	if _, present := lastBody["meta"]; present {
		t.Fatalf("the last page must omit meta entirely, got %#v", lastBody["meta"])
	}
}

// routeEntryKeys / routeAttemptKeys / routeExplanationKeys FREEZE the wire
// contract. They are sorted key sets, so ADDING a field fails this test just
// as surely as removing one — which is exactly the point: a new field is how
// a prompt, a raw provider error, or an account external id would arrive.
var (
	// The list entry additionally carries `outcome` (P6-CAPI-EXTRA): the
	// rolled-up terminal status + total latency of the decision's attempts.
	// The EXPLANATION deliberately does not — it returns the attempts
	// themselves (see routeExplanationKeys below and ListedDecision's doc
	// comment in internal/observability).
	routeEntryKeys = []string{
		"candidates", "chosen", "created_at", "decision_id", "exclusion_reasons",
		"outcome", "request_id", "scores", "thinking", "tier", "workload_profile_bucket",
	}
	routeOutcomeKeys     = []string{"terminal_status", "total_latency_ms"}
	routeExplanationKeys = []string{
		"attempts", "candidates", "chosen", "created_at", "decision_id", "exclusion_reasons",
		"request_id", "scores", "thinking", "tier", "workload_profile_bucket",
	}
	routeAttemptKeys = []string{
		"account_id", "attempt", "finished_at", "latency_ms", "offering_operation_id",
		"provider_id", "reservation_id", "started_at", "status", "thinking_clamped",
	}
)

// TestDiagnosticsRoutes_FieldSetFreeze pins the EXACT top-level key set of a
// list entry, of an explanation payload, and of one attempt.
func TestDiagnosticsRoutes_FieldSetFreeze(t *testing.T) {
	f := newRoutesFixture(t)
	f.seedDecision(t, routesSampleDecision("dec-1", "req-1", routesFixedNow))
	lat := 42
	fin := routesFixedNow.Add(time.Second)
	f.seedAttempt(t, observability.RouteAttempt{
		ID: "att-1", RouteDecisionID: "dec-1", AttemptNumber: 1,
		ProviderID: "prov-a", AccountID: "acct-a", OfferingOperationID: "off-a",
		LatencyMS: &lat, Status: observability.RouteStatusSuccess, ThinkingClamped: true,
		ReservationID: "resv-a", StartedAt: routesFixedNow, FinishedAt: &fin,
	})

	rec, body := f.getRoutes(t, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", rec.Code)
	}
	entries := dataList(t, body)
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if got := keysOf(t, entries[0].(map[string]any)); strings.Join(got, ",") != strings.Join(routeEntryKeys, ",") {
		t.Fatalf("list-entry key set = %v, want %v", got, routeEntryKeys)
	}

	recX, bodyX := f.getExplanation(t, "req-1")
	if recX.Code != http.StatusOK {
		t.Fatalf("explanation status = %d, want 200 (body %s)", recX.Code, recX.Body.String())
	}
	data := dataObject(t, bodyX)
	if got := keysOf(t, data); strings.Join(got, ",") != strings.Join(routeExplanationKeys, ",") {
		t.Fatalf("explanation key set = %v, want %v", got, routeExplanationKeys)
	}

	attempts, ok := data["attempts"].([]any)
	if !ok || len(attempts) != 1 {
		t.Fatalf("attempts = %#v, want exactly 1", data["attempts"])
	}
	if got := keysOf(t, attempts[0].(map[string]any)); strings.Join(got, ",") != strings.Join(routeAttemptKeys, ",") {
		t.Fatalf("attempt key set = %v, want %v", got, routeAttemptKeys)
	}

	// Field fidelity, not just presence: the typed status, exclusion reason
	// counts, and clamp flags must be the recorded values.
	att := attempts[0].(map[string]any)
	if att["status"] != string(observability.RouteStatusSuccess) {
		t.Fatalf("attempt status = %#v, want success", att["status"])
	}
	if att["latency_ms"] != float64(lat) {
		t.Fatalf("attempt latency_ms = %#v, want %d", att["latency_ms"], lat)
	}
	reasons := data["exclusion_reasons"].(map[string]any)
	if reasons["funding_ineligible"] != float64(2) || reasons["capability_uncertified"] != float64(1) {
		t.Fatalf("exclusion_reasons = %#v, want the recorded typed codes with their counts", reasons)
	}
	thinking := data["thinking"].(map[string]any)
	if thinking["requested"] != "extended" || thinking["applied"] != "standard" ||
		thinking["tier_clamped"] != true || thinking["certified_clamped"] != false {
		t.Fatalf("thinking = %#v, want the recorded clamp record", thinking)
	}
}

// TestDiagnosticsRoutes_ListOutcomeRollup proves the list entry's `outcome`
// object reports the decision's rolled-up terminal status and total latency —
// and, above all, that BOTH are JSON null for a decision with no attempt rows.
// A `0` or a fabricated "success" there is the exact failure this endpoint
// exists to avoid: the Overview surface renders this field as the request's
// result, so a zero-latency success would be an invented outcome.
func TestDiagnosticsRoutes_ListOutcomeRollup(t *testing.T) {
	f := newRoutesFixture(t)

	// dec-done has two attempts; dec-none has none. dec-none is newer so it
	// sorts first.
	f.seedDecision(t, routesSampleDecision("dec-done", "req-done", routesFixedNow))
	lat1, lat2 := 90, 35
	f.seedAttempt(t, observability.RouteAttempt{
		ID: "att-1", RouteDecisionID: "dec-done", AttemptNumber: 1,
		ProviderID: "prov-a", AccountID: "acct-a", OfferingOperationID: "off-a",
		LatencyMS: &lat1, Status: observability.RouteStatusFailure, StartedAt: routesFixedNow,
	})
	f.seedAttempt(t, observability.RouteAttempt{
		ID: "att-2", RouteDecisionID: "dec-done", AttemptNumber: 2,
		ProviderID: "prov-b", AccountID: "acct-b", OfferingOperationID: "off-b",
		LatencyMS: &lat2, Status: observability.RouteStatusSuccess, StartedAt: routesFixedNow,
	})
	f.seedDecision(t, routesSampleDecision("dec-none", "req-none", routesFixedNow.Add(time.Minute)))

	rec, body := f.getRoutes(t, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	byID := map[string]map[string]any{}
	for _, entry := range dataList(t, body) {
		m := entry.(map[string]any)
		byID[m["decision_id"].(string)] = m
	}

	done, ok := byID["dec-done"]
	if !ok {
		t.Fatalf("dec-done missing from the list: %#v", byID)
	}
	doneOutcome, ok := done["outcome"].(map[string]any)
	if !ok {
		t.Fatalf("dec-done outcome is not an object: %#v", done["outcome"])
	}
	if got := keysOf(t, doneOutcome); strings.Join(got, ",") != strings.Join(routeOutcomeKeys, ",") {
		t.Fatalf("outcome key set = %v, want %v", got, routeOutcomeKeys)
	}
	if doneOutcome["terminal_status"] != string(observability.RouteStatusSuccess) {
		t.Fatalf("terminal_status = %#v, want success (the LAST attempt decides)", doneOutcome["terminal_status"])
	}
	if doneOutcome["total_latency_ms"] != float64(lat1+lat2) {
		t.Fatalf("total_latency_ms = %#v, want %d", doneOutcome["total_latency_ms"], lat1+lat2)
	}

	none, ok := byID["dec-none"]
	if !ok {
		t.Fatalf("dec-none missing from the list: %#v", byID)
	}
	noneOutcome, ok := none["outcome"].(map[string]any)
	if !ok {
		t.Fatalf("dec-none outcome is not an object: %#v", none["outcome"])
	}
	// Present-and-null, not absent: the client must be able to tell "no
	// attempt was recorded" from "this response shape lacks the field".
	for _, key := range routeOutcomeKeys {
		v, present := noneOutcome[key]
		if !present {
			t.Fatalf("attempt-less decision omits outcome.%s entirely, want an explicit null", key)
		}
		if v != nil {
			t.Fatalf("attempt-less decision outcome.%s = %#v, want null (never 0, never a fabricated status)", key, v)
		}
	}

	// And the EXPLANATION carries no outcome object at all — its key set is
	// frozen separately above, but assert the absence directly too.
	_, xBody := f.getExplanation(t, "req-done")
	if _, present := dataObject(t, xBody)["outcome"]; present {
		t.Fatalf("the explanation payload must not carry an outcome rollup — it returns the attempts themselves")
	}
}

// TestDiagnosticsRoutes_OutcomeRollupNormalizesJunkStatus proves the rollup
// path is as fail-closed as every other status read: a value forced into the
// column past the recorder's normalization surfaces as the closed-vocabulary
// `unknown`, never as free provider text on this new field.
func TestDiagnosticsRoutes_OutcomeRollupNormalizesJunkStatus(t *testing.T) {
	f := newRoutesFixture(t)
	f.seedDecision(t, routesSampleDecision("dec-junk", "req-junk", routesFixedNow))

	const junk = "upstream said: Bearer sk-live-DEADBEEF expired"
	if _, err := f.db.Conn().ExecContext(t.Context(),
		`INSERT INTO route_attempts (id, route_decision_id, attempt_number, provider_id,
			account_id, offering_operation_id, status, thinking_clamped, started_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?)`,
		"att-junk", "dec-junk", 1, "prov-a", "acct-a", "off-a", junk, routesFixedNow.Unix(),
	); err != nil {
		t.Fatalf("insert junk status: %v", err)
	}

	rec, body := f.getRoutes(t, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	entry := dataList(t, body)[0].(map[string]any)
	outcome := entry["outcome"].(map[string]any)
	if outcome["terminal_status"] != string(observability.RouteStatusUnknown) {
		t.Fatalf("terminal_status = %#v, want %q", outcome["terminal_status"], observability.RouteStatusUnknown)
	}
	if strings.Contains(rec.Body.String(), "sk-live-DEADBEEF") {
		t.Fatalf("the rollup leaked raw provider text: %s", rec.Body.String())
	}
}

// TestDiagnosticsRoutes_UnknownRequestIDIs404 proves an unknown request id is
// a TYPED 404 — never a 500, and never an empty 200 that a dashboard would
// render as "no candidates were considered".
func TestDiagnosticsRoutes_UnknownRequestIDIs404(t *testing.T) {
	f := newRoutesFixture(t)

	rec, body := f.getExplanation(t, "no-such-request")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("body has no error object: %#v", body)
	}
	if errObj["code"] != "not_found" {
		t.Fatalf("error code = %#v, want not_found", errObj["code"])
	}
	if _, present := body["data"]; present {
		t.Fatalf("a 404 must carry no data field, got %#v", body["data"])
	}
	// The internal error text (which names the request id and the package)
	// must not be echoed back.
	if strings.Contains(rec.Body.String(), "observability:") {
		t.Fatalf("404 body leaked the internal error: %s", rec.Body.String())
	}
}

// TestDiagnosticsRoutes_SecretCanary is the load-bearing privacy proof.
//
// SCOPE NOTE (why only `status` is poisoned): 09 §3.9 REQUIRES this payload to
// carry the provider/account/model ids, the tier, and the exclusion reason
// codes verbatim. Poisoning those and demanding they vanish would be asserting
// the opposite of the contract. The privacy guarantee here is structural
// instead, and has exactly two halves, both asserted below:
//
//  1. `status` is the ONE column whose value could ever originate in provider
//     text (which is precisely why RouteRecorder normalizes it on write). A
//     poisoned status must surface as the closed-vocabulary `unknown` on every
//     surface, carrying neither marker.
//  2. There is no field for content AT ALL. The frozen key sets are checked
//     against a denylist of content-carrying names, so a future field called
//     `raw_error`, `prompt`, `response`, or `external_id` fails this test even
//     if nobody remembers to poison it.
//
// Both a credential-shaped marker and a PLAIN marker are used: the plain one
// is load-bearing, since a `vk_live_*` string could be caught by some
// unrelated redactor and prove nothing about this projection.
func TestDiagnosticsRoutes_SecretCanary(t *testing.T) {
	const credMarker = "vk_live_CANARYSECRET0123456789"
	const plainMarker = "ZZQQ-canary-marker"
	poison := credMarker + " " + plainMarker

	db := testControlDB(t)
	var logBuf bytes.Buffer
	logger := observability.New(slog.NewJSONHandler(&logBuf, nil))
	reader := observability.NewRouteReader(db.Conn())
	handler := NewDiagnosticsHandler(nil, nil, newAuditEmitter(db, logger)).WithRoutes(reader)
	recorder := observability.NewRouteRecorder(db.Conn(), logger)

	if err := recorder.RecordDecision(t.Context(), routesSampleDecision("dec-poison", "req-poison", routesFixedNow)); err != nil {
		t.Fatalf("RecordDecision: %v", err)
	}
	if err := recorder.RecordAttempt(t.Context(), observability.RouteAttempt{
		ID: "att-poison", RouteDecisionID: "dec-poison", AttemptNumber: 1,
		ProviderID: "prov-a", AccountID: "acct-a", OfferingOperationID: "off-a",
		Status: observability.RouteStatus(poison), StartedAt: routesFixedNow,
	}); err != nil {
		t.Fatalf("RecordAttempt: %v", err)
	}
	// Additionally force the poison PAST the writer's normalization, straight
	// into the column, so the READ path is the thing under test here.
	if _, err := db.Conn().ExecContext(t.Context(),
		`INSERT INTO route_attempts (id, route_decision_id, attempt_number, provider_id,
			account_id, offering_operation_id, status, thinking_clamped, started_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?)`,
		"att-poison-raw", "dec-poison", 2, "prov-a", "acct-a", "off-a", poison, routesFixedNow.Unix(),
	); err != nil {
		t.Fatalf("insert raw poisoned status: %v", err)
	}

	// Vacuity guard: this canary is only meaningful if the poison genuinely
	// reached the column.
	var stored int
	if err := db.Conn().QueryRow(
		`SELECT COUNT(*) FROM route_attempts WHERE status = ?`, poison,
	).Scan(&stored); err != nil {
		t.Fatalf("count poisoned rows: %v", err)
	}
	if stored != 1 {
		t.Fatalf("poisoned status was not stored (count %d) — this canary would be vacuous", stored)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/control/v1/diagnostics/routes", nil)
	listRec := httptest.NewRecorder()
	handler.ServeRoutes(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", listRec.Code)
	}

	xRec, xBody := func() (*httptest.ResponseRecorder, map[string]any) {
		req := httptest.NewRequest(http.MethodGet, "/api/control/v1/diagnostics/routes/req-poison", nil)
		req.SetPathValue("request_id", "req-poison")
		rec := httptest.NewRecorder()
		handler.ServeRouteExplanation(rec, req)
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode explanation: %v", err)
		}
		return rec, body
	}()
	if xRec.Code != http.StatusOK {
		t.Fatalf("explanation status = %d, want 200 (body %s)", xRec.Code, xRec.Body.String())
	}

	// Half 1: neither marker reaches any surface, and both poisoned attempts
	// report the closed-vocabulary `unknown`.
	surfaces := map[string]string{
		"list response":        listRec.Body.String(),
		"explanation response": xRec.Body.String(),
		"log buffer":           logBuf.String(),
	}
	for name, text := range surfaces {
		for markerName, marker := range map[string]string{"credential-shaped": credMarker, "plain": plainMarker} {
			if strings.Contains(text, marker) {
				t.Fatalf("%s leaked the %s canary marker %q: %s", name, markerName, marker, text)
			}
		}
	}
	attempts := dataObject(t, xBody)["attempts"].([]any)
	if len(attempts) != 2 {
		t.Fatalf("attempts = %d, want 2", len(attempts))
	}
	for i, a := range attempts {
		if got := a.(map[string]any)["status"]; got != string(observability.RouteStatusUnknown) {
			t.Fatalf("attempt[%d] status = %#v, want %q", i, got, observability.RouteStatusUnknown)
		}
	}

	// Half 2: the payload carries no content-shaped field AT ALL. The key sets
	// checked here are read off the ACTUAL responses, not off this file's
	// frozen constants — so a new production field named `raw_error`,
	// `prompt`, `response`, or `external_id` fails this test even though
	// nobody thought to poison it.
	forbiddenKeySubstrings := []string{
		"prompt", "response", "content", "message", "body", "text", "raw",
		"credential", "token", "secret", "authorization", "api_key", "external_id", "email",
	}
	var listEntry map[string]any
	if entries := dataList(t, mustDecode(t, listRec)); len(entries) > 0 {
		listEntry = entries[0].(map[string]any)
	} else {
		t.Fatalf("list returned no entries — half 2 would be vacuous")
	}
	actualKeySets := map[string][]string{
		"list entry":  keysOf(t, listEntry),
		"explanation": keysOf(t, dataObject(t, xBody)),
		"attempt":     keysOf(t, attempts[0].(map[string]any)),
	}
	// P6-CAPI-EXTRA extended this canary rather than adding a second one: the
	// list entry's new nested `outcome` object is a NEW place a
	// content-carrying field could be added, so its keys are harvested off the
	// real response and checked against the same denylist.
	listOutcome, ok := listEntry["outcome"].(map[string]any)
	if !ok {
		t.Fatalf("list entry outcome is not an object: %#v — this canary half would be vacuous", listEntry["outcome"])
	}
	actualKeySets["list entry outcome"] = keysOf(t, listOutcome)
	// The rollup reads the SAME poisoned status column, so it must fail closed
	// too: `unknown`, never the marker text.
	if got := listOutcome["terminal_status"]; got != string(observability.RouteStatusUnknown) {
		t.Fatalf("outcome.terminal_status = %#v, want %q for a poisoned status column", got, observability.RouteStatusUnknown)
	}
	for surface, keys := range actualKeySets {
		for _, key := range keys {
			for _, forbidden := range forbiddenKeySubstrings {
				if strings.Contains(key, forbidden) {
					t.Fatalf("%s projection key %q contains the content-carrying substring %q — diagnostics must be secret-free by construction",
						surface, key, forbidden)
				}
			}
		}
	}
}

func mustDecode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return body
}

// TestDiagnosticsRoutes_AreOwnerGatedThroughTheRealMux proves both routes are
// registered through `gated(...)` in the REAL ControlMux, not merely reachable
// on a locally-assembled mux. Every other test in this file drives the handler
// directly, so swapping `gated` for a bare networkGate would leave them all
// green while exposing route records to any loopback caller.
func TestDiagnosticsRoutes_AreOwnerGatedThroughTheRealMux(t *testing.T) {
	db := testControlDB(t)
	realMux := ControlMux(testAllowedHost, fakeSPA(), db, testKeyring(t))

	for _, path := range []string{
		"/api/control/v1/diagnostics/routes",
		"/api/control/v1/diagnostics/routes/req-1",
	} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			realMux.ServeHTTP(rec, newAuthRequest(t, http.MethodGet, path, nil))
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("unauthenticated status = %d, want 401 (body %s)", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestDiagnosticsRoutes_MethodNotAllowed proves both routes are read-only.
func TestDiagnosticsRoutes_MethodNotAllowed(t *testing.T) {
	f := newRoutesFixture(t)

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		t.Run("list "+method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/api/control/v1/diagnostics/routes", nil)
			rec := httptest.NewRecorder()
			f.handler.ServeRoutes(rec, req)
			if rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want 405", rec.Code)
			}
		})
		t.Run("explanation "+method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/api/control/v1/diagnostics/routes/req-1", nil)
			req.SetPathValue("request_id", "req-1")
			rec := httptest.NewRecorder()
			f.handler.ServeRouteExplanation(rec, req)
			if rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want 405", rec.Code)
			}
		})
	}
}
