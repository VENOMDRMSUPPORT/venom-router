package httpapi

// reviewcensus_test.go exercises GET /api/control/v1/certifications/review
// (P6-CAPI-EXTRA, enables P6-UI-012).
//
// The two properties worth more than all the others here:
//
//  1. `quota_insufficient` — and every other reason this standing census cannot
//     honestly compute — is reported as NOT EVALUATED, never as a count of 0. A
//     zero reads as "we looked and found none"; not-evaluated says "we did not
//     look". The banner renders those differently, and a wrong 0 is the exact
//     shape of an all-clear that isn't.
//  2. There is no `routable` field anywhere in the payload. The census pins the
//     non-certification inputs to their non-blocking values so Admit can only
//     ever return the certification reason — which makes its Routable verdict a
//     statement about the PINNED inputs, not about reality. Publishing it would
//     turn a test fixture into a routability claim.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/intelligence"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// scriptedCensusReader is the census port with a canned answer, used only for
// the failure path (which a real repo cannot be made to take on demand). Every
// arithmetic assertion below runs against the REAL CertificationRepo instead.
type scriptedCensusReader struct {
	items     []intelligence.ReviewItem
	truncated bool
	err       error
}

func (s *scriptedCensusReader) ListForAdmissionCensus(context.Context, int) ([]intelligence.ReviewItem, bool, error) {
	return s.items, s.truncated, s.err
}

// serveCensus drives the handler and returns the decoded envelope.
func serveCensus(t *testing.T, h *ReviewCensusHandler, method, query string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, "/api/control/v1/certifications/review"+query, nil)
	rec := httptest.NewRecorder()
	h.ServeCensus(rec, req)
	var body map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode %q: %v", rec.Body.String(), err)
		}
	}
	return rec, body
}

// countsByReason flattens `by_reason` into reason -> count.
func countsByReason(t *testing.T, data map[string]any) map[string]float64 {
	t.Helper()
	list, ok := data["by_reason"].([]any)
	if !ok {
		t.Fatalf("by_reason is not a list: %#v", data["by_reason"])
	}
	out := map[string]float64{}
	for _, entry := range list {
		m, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("by_reason entry is not an object: %#v", entry)
		}
		reason, _ := m["reason"].(string)
		count, ok := m["count"].(float64)
		if !ok {
			t.Fatalf("by_reason[%s].count is not a number: %#v", reason, m["count"])
		}
		out[reason] = count
	}
	return out
}

func stringList(t *testing.T, data map[string]any, key string) []string {
	t.Helper()
	raw, ok := data[key].([]any)
	if !ok {
		t.Fatalf("%s is not a list: %#v", key, data[key])
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("%s contains a non-string: %#v", key, v)
		}
		out = append(out, s)
	}
	return out
}

// --- the seeded end-to-end fixture ---

// newCensusFixture wires a ReviewCensusHandler over a REAL CertificationRepo on
// a fresh migrated DB, and seeds the given (state, truth) mix.
func newCensusFixture(t *testing.T, mix map[string][2]string) *ReviewCensusHandler {
	t.Helper()
	db := testControlDB(t)

	names := make([]string, 0, len(mix))
	for name := range mix {
		names = append(names, name)
	}
	sort.Strings(names)

	if _, err := db.Conn().Exec(
		`INSERT INTO providers (id, display_name, auth_mode, funding_mode, funding_locked, created_at, updated_at)
		 VALUES ('prov-census', 'prov-census', 'api_key', 'owner_policy', 0, 0, 0)`,
	); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if _, err := db.Conn().Exec(
		`INSERT INTO accounts (id, provider_id, external_id, auth_type, connection_state, health_state, created_at, updated_at)
		 VALUES ('acct-census', 'prov-census', 'acct-census', 'api_key', 'connected', 'healthy', 0, 0)`,
	); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := db.Conn().Exec(
		`INSERT INTO models (id, canonical_key_sha256, created_at, updated_at) VALUES ('model-census', 'canon-census', 0, 0)`,
	); err != nil {
		t.Fatalf("seed model: %v", err)
	}

	for _, name := range names {
		pm := "pm-" + name
		if _, err := db.Conn().Exec(
			`INSERT INTO account_model_offerings (account_id, provider_id, provider_model_id, model_id, availability, first_seen_at, last_seen_at)
			 VALUES ('acct-census', 'prov-census', ?, 'model-census', 'available', 0, 0)`, pm,
		); err != nil {
			t.Fatalf("seed offering %s: %v", name, err)
		}
		opID := pm + "-op"
		if _, err := db.Conn().Exec(
			`INSERT INTO offering_operations (id, account_id, provider_id, provider_model_id, operation, created_at, updated_at)
			 VALUES (?, 'acct-census', 'prov-census', ?, 'chat', 0, 0)`, opID, pm,
		); err != nil {
			t.Fatalf("seed offering_operation %s: %v", name, err)
		}
		if _, err := db.Conn().Exec(
			`INSERT INTO certifications (offering_operation_id, status, capability_truth, created_at, updated_at)
			 VALUES (?, ?, ?, 0, 0)`, opID, mix[name][0], mix[name][1],
		); err != nil {
			t.Fatalf("seed certification %s: %v", name, err)
		}
	}

	return NewReviewCensusHandler(storage.NewCertificationRepo(db, nil))
}

// TestReviewCensus_CountsOnlyNonRoutableCertifications is the arithmetic proof
// over a REAL seeded mix. Only rows models.Routable rejects are counted, and the
// row that matters most — `certified` with `unknown` truth — IS counted, even
// though the review drainer's own backlog query cannot see it.
func TestReviewCensus_CountsOnlyNonRoutableCertifications(t *testing.T) {
	h := newCensusFixture(t, map[string][2]string{
		// Routable: the ONE combination that is (04 §5).
		"routable-a": {"certified", "supported"},
		"routable-b": {"certified", "supported"},
		// Not routable, and invisible to ListForReview's status filter.
		"certified-unknown":     {"certified", "unknown"},
		"certified-unsupported": {"certified", "unsupported"},
		// Not routable, and in the drainer's backlog.
		"observed":  {"observed", "unknown"},
		"suspended": {"suspended", "unsupported"},
		// Not routable, not in the drainer's backlog either.
		"discovered": {"discovered", "unknown"},
		"probing":    {"probing", "unknown"},
	})

	rec, body := serveCensus(t, h, http.MethodGet, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	data, ok := body["data"].(map[string]any)
	if !ok {
		t.Fatalf("data is not an object: %#v", body["data"])
	}

	if data["scanned"] != float64(8) {
		t.Fatalf("scanned = %#v, want 8 (every certification row, routable or not)", data["scanned"])
	}
	if data["truncated"] != false {
		t.Fatalf("truncated = %#v, want false", data["truncated"])
	}

	counts := countsByReason(t, data)
	// 8 rows, 2 routable => 6 blocked on the certification conjunction.
	if got := counts[string(intelligence.AdmissionCapabilityNotCertified)]; got != 6 {
		t.Fatalf("capability_not_certified = %v, want 6 (8 rows minus the 2 certified+supported)", got)
	}
}

// TestReviewCensus_EmptyBacklogIsAnExplicitZero proves a genuinely empty backlog
// reports the evaluated reason with a count of 0 — present, not omitted. An
// absent row is indistinguishable from a reason nobody evaluated, and the
// all-clear must be a positive statement.
func TestReviewCensus_EmptyBacklogIsAnExplicitZero(t *testing.T) {
	h := newCensusFixture(t, map[string][2]string{
		"routable-a": {"certified", "supported"},
	})

	rec, body := serveCensus(t, h, http.MethodGet, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	data := body["data"].(map[string]any)

	counts := countsByReason(t, data)
	got, present := counts[string(intelligence.AdmissionCapabilityNotCertified)]
	if !present {
		t.Fatalf("an EVALUATED reason with nothing to report must still appear, with 0 — got %#v", data["by_reason"])
	}
	if got != 0 {
		t.Fatalf("capability_not_certified = %v, want 0", got)
	}
}

// TestReviewCensus_UnevaluatedReasonsCarryNoCount is the central fail-closed
// proof: `quota_insufficient` is REQUEST-DEPENDENT by 04 §5 ("quota is not
// exhausted or insufficient FOR THE REQUEST'S ESTIMATED NEED") and a standing
// census has no request, so it — like every other reason this census cannot
// compute — appears in not_evaluated_reasons and NOWHERE in by_reason.
func TestReviewCensus_UnevaluatedReasonsCarryNoCount(t *testing.T) {
	h := newCensusFixture(t, map[string][2]string{
		"observed": {"observed", "unknown"},
	})

	_, body := serveCensus(t, h, http.MethodGet, "")
	data := body["data"].(map[string]any)

	notEvaluated := stringList(t, data, "not_evaluated_reasons")
	inNotEvaluated := map[string]bool{}
	for _, r := range notEvaluated {
		inNotEvaluated[r] = true
	}
	if !inNotEvaluated[string(intelligence.AdmissionQuotaInsufficient)] {
		t.Fatalf("quota_insufficient must be listed as NOT EVALUATED (it is request-dependent); not_evaluated = %v", notEvaluated)
	}

	counts := countsByReason(t, data)
	for reason := range inNotEvaluated {
		if _, present := counts[reason]; present {
			t.Fatalf("not-evaluated reason %q appears in by_reason with a count — a count of 0 reads as \"none found\", which is a different and false claim", reason)
		}
	}
}

// TestReviewCensus_VocabularyIsExactlyTheClosedEightValueSet proves the two
// lists PARTITION intelligence.AdmissionReasons(): every reason is accounted
// for exactly once, none is invented, none is silently dropped. A reason added
// to the domain vocabulary later fails here until it is deliberately classified.
func TestReviewCensus_VocabularyIsExactlyTheClosedEightValueSet(t *testing.T) {
	h := newCensusFixture(t, map[string][2]string{"observed": {"observed", "unknown"}})
	_, body := serveCensus(t, h, http.MethodGet, "")
	data := body["data"].(map[string]any)

	evaluated := stringList(t, data, "evaluated_reasons")
	notEvaluated := stringList(t, data, "not_evaluated_reasons")

	seen := map[string]int{}
	for _, r := range append(append([]string{}, evaluated...), notEvaluated...) {
		seen[r]++
	}
	want := intelligence.AdmissionReasons()
	if len(seen) != len(want) {
		t.Fatalf("the two lists name %d distinct reasons, want exactly %d", len(seen), len(want))
	}
	for _, reason := range want {
		switch seen[string(reason)] {
		case 1: // classified exactly once
		case 0:
			t.Errorf("reason %q is in neither list — it would silently vanish from the banner", reason)
		default:
			t.Errorf("reason %q is in BOTH lists (%d times)", reason, seen[string(reason)])
		}
	}
	// Every served value must parse as a real domain reason.
	for _, r := range append(append([]string{}, evaluated...), notEvaluated...) {
		if _, err := intelligence.ParseAdmissionReason(r); err != nil {
			t.Errorf("served reason %q is outside the closed vocabulary: %v", r, err)
		}
	}
	if len(evaluated) == 0 {
		t.Fatalf("evaluated_reasons is empty — a census that evaluates nothing has no reason to exist")
	}
}

// TestReviewCensus_PayloadCarriesNoRoutableVerdict proves the payload states no
// routability verdict ANYWHERE — not at the top level, not per reason. The
// census pins identity/context/funding/health/quota/cooldown to their
// non-blocking values so Admit can only surface the certification reason; that
// makes its Routable field a fact about the pinned fixture, not about the
// offering. Publishing it would turn a test input into a routing claim.
func TestReviewCensus_PayloadCarriesNoRoutableVerdict(t *testing.T) {
	h := newCensusFixture(t, map[string][2]string{
		"routable": {"certified", "supported"},
		"observed": {"observed", "unknown"},
	})

	rec, body := serveCensus(t, h, http.MethodGet, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	// Structural: no key anywhere in the payload mentions routability.
	if strings.Contains(strings.ToLower(rec.Body.String()), "routable") {
		t.Fatalf("the census payload mentions routability: %s", rec.Body.String())
	}

	data := body["data"].(map[string]any)
	wantKeys := []string{
		"by_reason", "evaluated_reasons", "limit", "not_evaluated_reasons", "scanned", "truncated",
	}
	if got := sortedKeys(data); strings.Join(got, ",") != strings.Join(wantKeys, ",") {
		t.Fatalf("data key set = %v, want %v", got, wantKeys)
	}
	for _, entry := range data["by_reason"].([]any) {
		if got := sortedKeys(entry.(map[string]any)); strings.Join(got, ",") != "count,reason" {
			t.Fatalf("by_reason entry key set = %v, want [count reason]", got)
		}
	}
}

// TestReviewCensus_ReportsTruncation proves a capped scan says so rather than
// presenting a partial count as a complete one.
func TestReviewCensus_ReportsTruncation(t *testing.T) {
	h := newCensusFixture(t, map[string][2]string{
		"observed-a": {"observed", "unknown"},
		"observed-b": {"observed", "unknown"},
		"observed-c": {"observed", "unknown"},
	})

	_, body := serveCensus(t, h, http.MethodGet, "?limit=2")
	data := body["data"].(map[string]any)

	if data["truncated"] != true {
		t.Fatalf("truncated = %#v, want true for limit=2 over 3 rows", data["truncated"])
	}
	if data["scanned"] != float64(2) {
		t.Fatalf("scanned = %#v, want 2 (what was actually examined, not what exists)", data["scanned"])
	}
	if data["limit"] != float64(2) {
		t.Fatalf("limit = %#v, want the 2 that was applied", data["limit"])
	}
}

// TestReviewCensus_ReadErrorServesNoPartialCensus proves a failed read is a
// typed 500 with no data — never an empty census, which would render as an
// all-clear.
func TestReviewCensus_ReadErrorServesNoPartialCensus(t *testing.T) {
	sentinel := errors.New("certifications table is on fire")
	h := &ReviewCensusHandler{certs: &scriptedCensusReader{err: sentinel}}

	rec, body := serveCensus(t, h, http.MethodGet, "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (body %s)", rec.Code, rec.Body.String())
	}
	if _, present := body["data"]; present {
		t.Fatalf("a read error must carry NO data field — an empty census renders as an all-clear; got %#v", body["data"])
	}
	if strings.Contains(rec.Body.String(), sentinel.Error()) {
		t.Fatalf("the 500 leaked the storage error: %s", rec.Body.String())
	}
}

// TestReviewCensus_MethodNotAllowed proves the surface is read-only.
func TestReviewCensus_MethodNotAllowed(t *testing.T) {
	h := &ReviewCensusHandler{certs: &scriptedCensusReader{}}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			rec, _ := serveCensus(t, h, method, "")
			if rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want 405", rec.Code)
			}
		})
	}
}

// TestReviewCensus_IsOwnerGatedThroughTheRealMux proves the route goes through
// `gated(...)` in the REAL ControlMux — every other test here drives the handler
// directly, so a bare networkGate would leave them all green.
func TestReviewCensus_IsOwnerGatedThroughTheRealMux(t *testing.T) {
	db := testControlDB(t)
	realMux := ControlMux(testAllowedHost, fakeSPA(), db, testKeyring(t))

	rec := httptest.NewRecorder()
	realMux.ServeHTTP(rec, newAuthRequest(t, http.MethodGet, "/api/control/v1/certifications/review", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401 (body %s)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "<html") {
		t.Fatalf("the route is not registered — the request fell through to the SPA: %s", rec.Body.String())
	}
}
