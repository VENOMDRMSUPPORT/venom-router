package httpapi

// routingpolicy_test.go exercises GET /api/control/v1/routing/policy
// (P6-CAPI-EXTRA, enables P6-UI-003).
//
// The load-bearing property of this file is that EVERY expectation is derived
// from routing.Policies() at test time — never from a literal transcribed out
// of docs/05 §1. A test carrying its own copy of the numbers would agree with a
// handler carrying its own copy of the numbers while both disagreed with the
// engine that actually routes traffic.

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/routing"
)

// servePolicy drives the handler and returns the decoded envelope.
func servePolicy(t *testing.T, h *RoutingPolicyHandler, method string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, "/api/control/v1/routing/policy", nil)
	rec := httptest.NewRecorder()
	h.ServePolicy(rec, req)
	var body map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode %q: %v", rec.Body.String(), err)
		}
	}
	return rec, body
}

// policyTiersByName decodes the served body into tier -> object.
func policyTiersByName(t *testing.T, body map[string]any) map[string]map[string]any {
	t.Helper()
	data, ok := body["data"].(map[string]any)
	if !ok {
		t.Fatalf("data is not an object: %#v", body["data"])
	}
	tiers, ok := data["tiers"].([]any)
	if !ok {
		t.Fatalf("data.tiers is not a list: %#v", data["tiers"])
	}
	out := map[string]map[string]any{}
	for _, entry := range tiers {
		m, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("tier entry is not an object: %#v", entry)
		}
		name, _ := m["tier"].(string)
		out[name] = m
	}
	return out
}

// TestRoutingPolicy_BodyEqualsEnginePolicies is the anti-hardcoding proof: for
// all three tiers, every served field equals the value routing.Policies()
// reports RIGHT NOW. Replacing any served value with a literal — even a
// literal that happens to match today's shipped policy — fails here, because
// the expectation is read from the engine, not written down beside it.
func TestRoutingPolicy_BodyEqualsEnginePolicies(t *testing.T) {
	policies, err := routing.Policies()
	if err != nil {
		t.Fatalf("routing.Policies(): %v", err)
	}

	rec, body := servePolicy(t, NewRoutingPolicyHandler(), http.MethodGet)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	byTier := policyTiersByName(t, body)

	// All three tiers, in the engine's own fixed order.
	wantOrder := []routing.Tier{routing.TierLite, routing.TierPro, routing.TierMax}
	if len(byTier) != len(wantOrder) {
		t.Fatalf("served %d tiers, want %d: %#v", len(byTier), len(wantOrder), byTier)
	}

	for _, tier := range wantOrder {
		want := policies[tier]
		got, ok := byTier[string(tier)]
		if !ok {
			t.Fatalf("tier %q missing from the served body: %#v", tier, byTier)
		}
		t.Run(string(tier), func(t *testing.T) {
			if got["funding"] != string(want.Funding) {
				t.Errorf("funding = %#v, want %q", got["funding"], want.Funding)
			}
			if got["context_ceiling_tokens"] != float64(want.ContextCeilingTokens) {
				t.Errorf("context_ceiling_tokens = %#v, want %d", got["context_ceiling_tokens"], want.ContextCeilingTokens)
			}
			if got["thinking_ceiling"] != string(want.ThinkingCeiling) {
				t.Errorf("thinking_ceiling = %#v, want %q", got["thinking_ceiling"], want.ThinkingCeiling)
			}
			if got["attempt_budget"] != float64(want.AttemptBudget) {
				t.Errorf("attempt_budget = %#v, want %d", got["attempt_budget"], want.AttemptBudget)
			}
			if got["scored"] != want.Scored {
				t.Errorf("scored = %#v, want %v", got["scored"], want.Scored)
			}
			if got["latency_tie_break_only"] != want.LatencyTieBreakOnly {
				t.Errorf("latency_tie_break_only = %#v, want %v", got["latency_tie_break_only"], want.LatencyTieBreakOnly)
			}

			if !want.Scored {
				// An UNSCORED tier has no weights and no band — those facts are
				// not-applicable, not zero. Serving 0 would read as "quality is
				// weighted at zero", a scoring claim Lite never makes.
				for _, key := range []string{"weights", "competitive_band"} {
					v, present := got[key]
					if !present {
						t.Errorf("unscored tier omits %q entirely, want an explicit null", key)
					}
					if v != nil {
						t.Errorf("unscored tier %q = %#v, want null (not-applicable is not 0)", key, v)
					}
				}
				return
			}

			if got["competitive_band"] != want.BandWidth {
				t.Errorf("competitive_band = %#v, want %v", got["competitive_band"], want.BandWidth)
			}
			weights, ok := got["weights"].(map[string]any)
			if !ok {
				t.Fatalf("weights is not an object: %#v", got["weights"])
			}
			for key, wantWeight := range map[string]float64{
				"quality":             want.Weights.Quality,
				"reliability":         want.Weights.Reliability,
				"quota_headroom":      want.Weights.QuotaHeadroom,
				"evidence_confidence": want.Weights.EvidenceConfidence,
				"cost_class":          want.Weights.CostClass,
				"latency":             want.Weights.Latency,
			} {
				if weights[key] != wantWeight {
					t.Errorf("weights.%s = %#v, want %v", key, weights[key], wantWeight)
				}
			}
		})
	}
}

// TestRoutingPolicy_TierOrderIsFixed proves the list is built from an explicit
// slice, not by ranging over Policies()' map (whose iteration order Go
// randomizes per run). A tier list that reshuffled between calls would make the
// surface's reading order unstable.
func TestRoutingPolicy_TierOrderIsFixed(t *testing.T) {
	h := NewRoutingPolicyHandler()
	want := []string{"lite", "pro", "max"}

	for i := 0; i < 5; i++ {
		_, body := servePolicy(t, h, http.MethodGet)
		data := body["data"].(map[string]any)
		tiers := data["tiers"].([]any)
		got := make([]string, 0, len(tiers))
		for _, entry := range tiers {
			got = append(got, entry.(map[string]any)["tier"].(string))
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("call %d: tier order = %v, want %v", i, got, want)
		}
	}
}

// TestRoutingPolicy_FieldSetFreeze pins the EXACT key sets. Adding a field
// fails this test just as surely as removing one — a policy surface that grew a
// field nobody reviewed is how a value the engine never validated would reach
// the dashboard.
func TestRoutingPolicy_FieldSetFreeze(t *testing.T) {
	wantTierKeys := []string{
		"attempt_budget", "competitive_band", "context_ceiling_tokens", "funding",
		"latency_tie_break_only", "scored", "thinking_ceiling", "tier", "weights",
	}
	wantWeightKeys := []string{
		"cost_class", "evidence_confidence", "latency", "quality", "quota_headroom", "reliability",
	}

	_, body := servePolicy(t, NewRoutingPolicyHandler(), http.MethodGet)
	byTier := policyTiersByName(t, body)

	for name, tier := range byTier {
		if got := sortedKeys(tier); strings.Join(got, ",") != strings.Join(wantTierKeys, ",") {
			t.Fatalf("tier %q key set = %v, want %v", name, got, wantTierKeys)
		}
	}
	weights, ok := byTier["pro"]["weights"].(map[string]any)
	if !ok {
		t.Fatalf("pro weights is not an object: %#v", byTier["pro"]["weights"])
	}
	if got := sortedKeys(weights); strings.Join(got, ",") != strings.Join(wantWeightKeys, ",") {
		t.Fatalf("weights key set = %v, want %v", got, wantWeightKeys)
	}
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestRoutingPolicy_PolicyErrorServesNoPartialBody proves the fail-closed
// posture: when the engine cannot produce a VALIDATED policy set, the endpoint
// serves a typed 500 and NO policy data at all. Serving whatever tiers happened
// to validate would hand the dashboard a policy the router itself rejected.
// It runs BOTH shapes of a failed policy load, and the second one is the one
// that actually pins the error check.
//
// GOVERNOR NOTE (review-time fix): this test originally used the partial fixture
// ALONE, and it did not bite. Deleting the `if err != nil` branch entirely left
// it GREEN — because a map missing pro/max still trips the separate per-tier
// `!ok` guard further down, which returns the same typed 500. The test was
// pinning the missing-tier fallback, not the error check it is named for. The
// COMPLETE fixture below closes that: a full, individually-plausible three-tier
// map arriving WITH an error is exactly the case where only the error check can
// save the caller, since every `!ok` lookup succeeds. That is also the realistic
// case — Policies() validates cross-tier invariants (ascending context ceilings,
// Lite's product rules), so a complete-but-rejected table is precisely what it
// returns on failure.
func TestRoutingPolicy_PolicyErrorServesNoPartialBody(t *testing.T) {
	sentinel := errors.New("policy validation exploded")

	tests := []struct {
		name     string
		policies map[routing.Tier]routing.TierPolicy
	}{
		{
			// A PARTIAL, plausible-looking map alongside the error: the handler
			// must discard it entirely rather than serve the tiers it received.
			name: "a partial policy map is discarded, not served",
			policies: map[routing.Tier]routing.TierPolicy{
				routing.TierLite: {Tier: routing.TierLite, Funding: routing.FundingFreeOnly, AttemptBudget: 3},
			},
		},
		{
			// A COMPLETE map alongside the error. Every per-tier lookup succeeds,
			// so the error check is the ONLY thing standing between the caller and
			// a 200 carrying a policy set the engine itself rejected.
			name: "a complete but REJECTED policy map is still refused",
			policies: map[routing.Tier]routing.TierPolicy{
				routing.TierLite: {Tier: routing.TierLite, Funding: routing.FundingFreeOnly, AttemptBudget: 3},
				routing.TierPro:  {Tier: routing.TierPro, Funding: routing.FundingFreeAndPaid, AttemptBudget: 4, Scored: true},
				routing.TierMax:  {Tier: routing.TierMax, Funding: routing.FundingFreeAndPaid, AttemptBudget: 5, Scored: true},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &RoutingPolicyHandler{policies: func() (map[routing.Tier]routing.TierPolicy, error) {
				return tt.policies, sentinel
			}}

			rec, body := servePolicy(t, h, http.MethodGet)
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500 (body %s)", rec.Code, rec.Body.String())
			}
			if _, present := body["data"]; present {
				t.Fatalf("a policy error must carry NO data field, got %#v", body["data"])
			}
			errObj, ok := body["error"].(map[string]any)
			if !ok {
				t.Fatalf("body has no typed error object: %#v", body)
			}
			if errObj["code"] != "internal" {
				t.Fatalf("error code = %#v, want internal", errObj["code"])
			}
			// The engine's own error text must not be echoed to the client.
			if strings.Contains(rec.Body.String(), sentinel.Error()) || strings.Contains(rec.Body.String(), "lite") {
				t.Fatalf("the 500 body leaked engine internals: %s", rec.Body.String())
			}
		})
	}
}

// TestRoutingPolicy_MethodNotAllowed proves the surface is read-only. There is
// deliberately no PUT: docs/05 §8.4 defers dashboard weight tuning past V1, so
// an accepted write would be a capability the engine does not have.
func TestRoutingPolicy_MethodNotAllowed(t *testing.T) {
	h := NewRoutingPolicyHandler()
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			rec, body := servePolicy(t, h, method)
			if rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want 405", rec.Code)
			}
			if _, present := body["data"]; present {
				t.Fatalf("a 405 must carry no data, got %#v", body["data"])
			}
		})
	}
}

// TestRoutingPolicy_IsOwnerGatedThroughTheRealMux proves the route is
// registered through `gated(...)` in the REAL ControlMux. Every other test in
// this file drives the handler directly, so swapping `gated` for a bare
// networkGate would leave them all green while exposing the router's policy to
// any loopback caller.
func TestRoutingPolicy_IsOwnerGatedThroughTheRealMux(t *testing.T) {
	db := testControlDB(t)
	realMux := ControlMux(testAllowedHost, fakeSPA(), db, testKeyring(t))

	rec := httptest.NewRecorder()
	realMux.ServeHTTP(rec, newAuthRequest(t, http.MethodGet, "/api/control/v1/routing/policy", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401 (body %s)", rec.Code, rec.Body.String())
	}
	// Vacuity guard: the 401 must come from the AUTH gate, not from the route
	// being absent (an unregistered path would fall through to the SPA).
	if strings.Contains(rec.Body.String(), "<html") {
		t.Fatalf("the route is not registered — the request fell through to the SPA: %s", rec.Body.String())
	}
}
