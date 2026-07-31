package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/application"
	accountsdomain "github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/routing"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/secrets"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// seedCredentialedOfferingWithKey is seedCredentialedOffering with a caller-chosen
// credential plaintext, so a SECOND account can be seeded without colliding on the
// per-provider credential fingerprint (DOM-003). It mirrors the seed helper: a
// certified chat offering on a healthy free account, a real keyring-encrypted
// active credential, and an ample requests window.
func seedCredentialedOfferingWithKey(t *testing.T, db *storage.DB, kr *secrets.Keyring, acct, provModelID, modelID, plaintext string) {
	t.Helper()
	seedOffering(t, db, acct, provModelID, modelID, true, intp2(200000), intp2(128000))
	if _, err := db.Conn().Exec(`DELETE FROM account_credentials WHERE account_id = ?`, acct); err != nil {
		t.Fatalf("clear placeholder credential: %v", err)
	}
	svc := application.NewCredentialService(storage.NewAccountCredentialRepo(db), kr, nil)
	if _, err := svc.Store(context.Background(), application.StoreCredentialParams{
		ID: "cred-real-" + acct, AccountID: acct, ProviderID: string(providers.OpenCodeZenID),
		Kind: accountsdomain.CredentialKindAPIKey, Active: true, PlaintextKey: plaintext,
	}); err != nil {
		t.Fatalf("store credential: %v", err)
	}
	if _, err := db.Conn().Exec(
		`INSERT INTO quota_windows
		    (id, account_id, source, unit, window_type, window_key, remaining, limit_value, reserved, version, confidence, freshness_state, observed_at, created_at, updated_at)
		 VALUES (?, ?, 'local_safety', 'requests', 'rpm', 'k-req', 1000000, 1000000, 0, 1, 1, 'fresh', 0, 0, 0)`,
		"win-"+acct, acct,
	); err != nil {
		t.Fatalf("seed quota window: %v", err)
	}
}

// P5-TEST-002 — exhaustive venom extension validation matrix (docs/05 §1b + §1a).
// PAPI-004 covered single cases; this is the full matrix.

var thinkRank = map[routing.ThinkingLevel]int{
	routing.ThinkingNone: 0, routing.ThinkingStandard: 1, routing.ThinkingExtended: 2, routing.ThinkingUltra: 3,
}

// TestP5Gate_ThinkingCeilingMatrix is the 12-cell (4 levels × 3 tiers) clamp
// matrix, every expectation DERIVED from Policies()[tier].ThinkingCeiling (never
// a hardcoded table). It exercises NormalizeThinking — the pure clamp contract
// (§1a) the header and decision row both derive from — with a fully
// reasoning-certified candidate, so the TIER-ceiling clamp is isolated from the
// per-offering certified-max clamp.
//
// NOTE (honest limit): end to end the APPLIED level is `none` for every request
// because the candidate snapshot does not populate reasoning certification. This
// matrix therefore proves the clamp CONTRACT directly rather than through the
// HTTP path; it seeds no fake reasoning fact into the snapshot/DB.
//
// T2-M1: clamp to the wrong ceiling (use Max's for every tier) ⇒ RED.
func TestP5Gate_ThinkingCeilingMatrix(t *testing.T) {
	pol, err := routing.Policies()
	if err != nil {
		t.Fatalf("Policies: %v", err)
	}
	levels := []routing.ThinkingLevel{routing.ThinkingNone, routing.ThinkingStandard, routing.ThinkingExtended, routing.ThinkingUltra}
	tiers := []routing.Tier{routing.TierLite, routing.TierPro, routing.TierMax}
	fullyCertified := routing.ThinkingCandidate{ReasoningCertified: true, CertifiedMax: routing.ThinkingUltra}

	for _, tier := range tiers {
		ceiling := pol[tier].ThinkingCeiling
		for _, lvl := range levels {
			req := lvl
			dec := routing.NormalizeThinking(&req, false, pol[tier], fullyCertified)

			wantTierClamp := thinkRank[lvl] > thinkRank[ceiling]
			wantApplied := lvl
			if wantTierClamp {
				wantApplied = ceiling
			}
			if dec.TierClamped != wantTierClamp {
				t.Fatalf("[%s/%s] TierClamped = %v, want %v (ceiling %s)", tier, lvl, dec.TierClamped, wantTierClamp, ceiling)
			}
			if dec.Applied != wantApplied {
				t.Fatalf("[%s/%s] Applied = %s, want %s (ceiling %s)", tier, lvl, dec.Applied, wantApplied, ceiling)
			}
			if dec.CertifiedClamped {
				t.Fatalf("[%s/%s] CertifiedClamped = true with a fully certified candidate", tier, lvl)
			}
		}
	}
}

// TestP5Gate_ThinkingHTTPObservable proves what the HTTP surface CAN show for
// all 12 cells: every request is accepted (thinking is a budget, never a hard
// failure), and the clamp is reported iff a non-none budget was requested (the
// certified-clamp-to-none dominates the observable, per the limit above).
func TestP5Gate_ThinkingHTTPObservable(t *testing.T) {
	models := map[routing.Tier]string{routing.TierLite: "venom/lite", routing.TierPro: "venom/pro", routing.TierMax: "venom/max"}
	levels := []string{"none", "standard", "extended", "ultra"}
	for tier, model := range models {
		for _, lvl := range levels {
			_, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) { _, _ = w.Write([]byte(completionJSON("ok"))) })
			db, kr := e2eEnv(t, srv.URL)
			seedCredentialedOffering(t, db, kr, "acct-1", "prov/model-a", "model-a")
			h := newE2EHandler(t, db, kr, srv.URL, nil)
			rec := postChat(t, h, fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}],"venom":{"thinking_budget":%q}}`, model, lvl))
			if rec.Code != http.StatusOK {
				t.Fatalf("[%s/%s] status %d body %s", tier, lvl, rec.Code, rec.Body.String())
			}
			wantReported := lvl != "none"
			got := rec.Result().Header.Get("X-Venom-Thinking-Clamped") == "true"
			if got != wantReported {
				t.Fatalf("[%s/%s] clamp reported = %v, want %v", tier, lvl, got, wantReported)
			}
		}
	}
}

// TestP5Gate_CapabilityMatrix proves every operation-mapped capability is a hard
// gate: certified ⇒ routes, uncertified ⇒ refuses (and never calls the provider).
func TestP5Gate_CapabilityMatrix(t *testing.T) {
	// chat is certified by the seed helper; the other four need an explicit
	// certified operation. reasoning is handled separately (no operation mapping;
	// the snapshot cannot certify it — see below).
	opCaps := []string{"streaming", "tools", "structured_output", "vision"}

	for _, cap := range opCaps {
		// Certified ⇒ routes.
		func() {
			_, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) { _, _ = w.Write([]byte(completionJSON("ok"))) })
			db, kr := e2eEnv(t, srv.URL)
			seedCredentialedOffering(t, db, kr, "acct-1", "prov/model-a", "model-a")
			certifyOperation(t, db, "acct-1", "prov/model-a", cap)
			h := newE2EHandler(t, db, kr, srv.URL, nil)
			rec := postChat(t, h, fmt.Sprintf(`{"model":"venom/pro","messages":[{"role":"user","content":"hi"}],"venom":{"required_capabilities":[%q]}}`, cap))
			if rec.Code != http.StatusOK {
				t.Fatalf("required %q CERTIFIED must route: status %d body %s", cap, rec.Code, rec.Body.String())
			}
		}()
		// Uncertified ⇒ refuses, provider never called.
		func() {
			up, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) { _, _ = w.Write([]byte(completionJSON("ok"))) })
			db, kr := e2eEnv(t, srv.URL)
			seedCredentialedOffering(t, db, kr, "acct-1", "prov/model-a", "model-a") // chat only
			h := newE2EHandler(t, db, kr, srv.URL, nil)
			rec := postChat(t, h, fmt.Sprintf(`{"model":"venom/pro","messages":[{"role":"user","content":"hi"}],"venom":{"required_capabilities":[%q]}}`, cap))
			if rec.Code == http.StatusOK {
				t.Fatalf("required %q UNCERTIFIED must refuse; got 200", cap)
			}
			if up.calls != 0 {
				t.Fatalf("required %q uncertified called the provider %d times; want 0", cap, up.calls)
			}
		}()
	}

	// reasoning: uncertified refuses (the snapshot never populates reasoning
	// certification, so this is always the case). certified-routes is UNREACHABLE
	// end to end and is documented in the HONESTLY-UNKNOWN section, not faked.
	up, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) { _, _ = w.Write([]byte(completionJSON("ok"))) })
	db, kr := e2eEnv(t, srv.URL)
	seedCredentialedOffering(t, db, kr, "acct-1", "prov/model-a", "model-a")
	h := newE2EHandler(t, db, kr, srv.URL, nil)
	rec := postChat(t, h, `{"model":"venom/pro","messages":[{"role":"user","content":"hi"}],"venom":{"required_capabilities":["reasoning"]}}`)
	if rec.Code == http.StatusOK {
		t.Fatalf("required reasoning must refuse (uncertified fleet); got 200")
	}
	if up.calls != 0 {
		t.Fatalf("required reasoning called the provider; want 0")
	}
}

// TestP5Gate_NonRequestableCapabilities proves the two vocabulary edge cases —
// context_window and image_generation — are REJECTED by ParseCapability (they
// are not requestable §1b identifiers), both at the parser and end to end.
//
// T2-M2: make ParseCapability accept context_window ⇒ RED.
func TestP5Gate_NonRequestableCapabilities(t *testing.T) {
	for _, cap := range []string{"context_window", "image_generation"} {
		_, err := parseVenomExtension([]byte(fmt.Sprintf(`{"required_capabilities":[%q]}`, cap)))
		if err == nil {
			t.Fatalf("parse accepted non-requestable capability %q", cap)
		}
		if !strings.Contains(venomExtErrorMessage(err), "required_capabilities") {
			t.Fatalf("rejection for %q must name required_capabilities: %v", cap, err)
		}
	}
	// End to end: a 400 venom_invalid_extension, nothing executed.
	up, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) { _, _ = w.Write([]byte(completionJSON("ok"))) })
	db, kr := e2eEnv(t, srv.URL)
	seedCredentialedOffering(t, db, kr, "acct-1", "prov/model-a", "model-a")
	h := newE2EHandler(t, db, kr, srv.URL, nil)
	rec := postChat(t, h, `{"model":"venom/pro","messages":[{"role":"user","content":"hi"}],"venom":{"required_capabilities":["context_window"]}}`)
	if rec.Code != http.StatusBadRequest || decodePublicError(t, rec.Body.Bytes()).Code != CodeInvalidExtension {
		t.Fatalf("context_window must be a 400 venom_invalid_extension; got %d %s", rec.Code, rec.Body.String())
	}
	if up.calls != 0 {
		t.Fatalf("a rejected capability executed the provider; want 0")
	}
}

// TestP5Gate_InvalidInputsMatrix drives every invalid-input shape and the two
// accepted empties, asserting each rejection is a 400 venom_invalid_extension
// naming the field, with NOTHING executed and no usage row.
//
// T2-M3: accept a wrong JSON type for thinking_budget ⇒ the wrong-type row RED.
// T2-M4: treat venom:{} as an error ⇒ the empty-object row RED.
// T2-M6: write a usage row for a rejected extension ⇒ the zero-usage assertion RED.
func TestP5Gate_InvalidInputsMatrix(t *testing.T) {
	reject := []struct {
		name, venom, field string
	}{
		{"invalid_thinking_value", `{"thinking_budget":"turbo"}`, "thinking_budget"},
		{"unknown_capability", `{"required_capabilities":["telepathy"]}`, "required_capabilities"},
		{"unknown_field", `{"bogus":1}`, "bogus"},
		// A wrong JSON TYPE (vs an unknown field) is a decoder type error whose
		// name Venom reports as the venom sub-object, not the leaf field (the
		// decoder error is not parsed for the leaf). Production detail
		// (venomext.go, frozen this batch); the rejection is still the typed 400.
		{"wrong_type_thinking", `{"thinking_budget":5}`, "venom"},
		{"wrong_type_capabilities", `{"required_capabilities":"vision"}`, "venom"},
	}
	for _, c := range reject {
		t.Run(c.name, func(t *testing.T) {
			up, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) { _, _ = w.Write([]byte(completionJSON("ok"))) })
			db, kr := e2eEnv(t, srv.URL)
			seedCredentialedOffering(t, db, kr, "acct-1", "prov/model-a", "model-a")
			h := newE2EHandler(t, db, kr, srv.URL, nil)
			rec := postChat(t, h, fmt.Sprintf(`{"model":"venom/pro","messages":[{"role":"user","content":"hi"}],"venom":%s}`, c.venom))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("%s: status %d, want 400; body %s", c.name, rec.Code, rec.Body.String())
			}
			body := decodePublicError(t, rec.Body.Bytes())
			if body.Code != CodeInvalidExtension {
				t.Fatalf("%s: code %q, want %s", c.name, body.Code, CodeInvalidExtension)
			}
			if !strings.Contains(body.Message, c.field) {
				t.Fatalf("%s: message must name %q: %q", c.name, c.field, body.Message)
			}
			if up.calls != 0 {
				t.Fatalf("%s: provider called %d times on a rejected extension; want 0", c.name, up.calls)
			}
			if n := countRows(t, db, "usage_records"); n != 0 {
				t.Fatalf("%s: %d usage rows for a REJECTED extension; want 0", c.name, n)
			}
		})
	}

	// Accepted empties: venom:null and venom:{} both route (defaults apply).
	for _, empty := range []string{`null`, `{}`} {
		_, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) { _, _ = w.Write([]byte(completionJSON("ok"))) })
		db, kr := e2eEnv(t, srv.URL)
		seedCredentialedOffering(t, db, kr, "acct-1", "prov/model-a", "model-a")
		h := newE2EHandler(t, db, kr, srv.URL, nil)
		rec := postChat(t, h, fmt.Sprintf(`{"model":"venom/pro","messages":[{"role":"user","content":"hi"}],"venom":%s}`, empty))
		if rec.Code != http.StatusOK {
			t.Fatalf("venom:%s must be accepted (defaults); status %d body %s", empty, rec.Code, rec.Body.String())
		}
	}
}

// TestP5Gate_UnknownTopLevelAcceptedParity is the counter-test proving strict
// decoding is scoped to the venom sub-object: unknown TOP-LEVEL fields are
// accepted.
func TestP5Gate_UnknownTopLevelAcceptedParity(t *testing.T) {
	_, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) { _, _ = w.Write([]byte(completionJSON("ok"))) })
	db, kr := e2eEnv(t, srv.URL)
	seedCredentialedOffering(t, db, kr, "acct-1", "prov/model-a", "model-a")
	h := newE2EHandler(t, db, kr, srv.URL, nil)
	rec := postChat(t, h, `{"model":"venom/pro","messages":[{"role":"user","content":"hi"}],"seed":7,"logit_bias":{}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("unknown top-level fields must be accepted (parity); status %d body %s", rec.Code, rec.Body.String())
	}
}

// TestP5Gate_ExtensionPreservedAcrossFallback proves the applied/clamp facts
// describe the SERVED attempt after a fallback: the first attempt fails, a second
// serves, and the clamp is still reported with attempts == 2.
//
// T2-M5: lose the applied facts across fallback ⇒ RED.
func TestP5Gate_ExtensionPreservedAcrossFallback(t *testing.T) {
	up, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) {})
	up.handleFunc = func(w http.ResponseWriter, _ []byte) {
		if up.calls == 1 { // first attempt: an ACCOUNT-scoped 429 → cool this
			w.Header().Set("X-RateLimit-Scope", "account") // account, fall to the other
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"quota"}}`))
			return
		}
		_, _ = w.Write([]byte(completionJSON("ok"))) // second attempt: success
	}
	db, kr := e2eEnv(t, srv.URL)
	seedCredentialedOffering(t, db, kr, "acct-1", "prov/model-a", "model-a")
	// A DISTINCT credential for acct-2 (a shared key would collide on the
	// per-provider fingerprint), giving the loop a second candidate to fall to.
	seedCredentialedOfferingWithKey(t, db, kr, "acct-2", "prov/model-a", "model-a", "sk-upstream-SECRET-cred-2")
	h := newE2EHandler(t, db, kr, srv.URL, nil)

	rec := postChat(t, h, `{"model":"venom/pro","messages":[{"role":"user","content":"hi"}],"venom":{"thinking_budget":"ultra"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("fallback should still serve: status %d body %s", rec.Code, rec.Body.String())
	}
	if got := rec.Result().Header.Get("X-Venom-Fallback-Attempts"); got != "2" {
		t.Fatalf("expected a fallback (attempts=2); got X-Venom-Fallback-Attempts=%q", got)
	}
	if rec.Result().Header.Get("X-Venom-Thinking-Clamped") != "true" {
		t.Fatalf("the clamp fact must survive fallback (describe the served attempt)")
	}
}

// TestP5Gate_ExtensionNoInternalsLeak proves no provider/account internal leaks
// into the body or headers on an extension path.
func TestP5Gate_ExtensionNoInternalsLeak(t *testing.T) {
	_, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) { _, _ = w.Write([]byte(completionJSON("ok"))) })
	db, kr := e2eEnv(t, srv.URL)
	seedCredentialedOffering(t, db, kr, "acct-1", "prov/model-a", "model-a")
	h := newE2EHandler(t, db, kr, srv.URL, nil)
	rec := postChat(t, h, `{"model":"venom/pro","messages":[{"role":"user","content":"hi"}],"venom":{"thinking_budget":"standard"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "acct-1") {
		t.Fatalf("account id leaked into the body: %s", rec.Body.String())
	}
	for key, vals := range rec.Result().Header {
		for _, v := range vals {
			if strings.Contains(v, "acct-1") || strings.Contains(v, upstreamCredential) {
				t.Fatalf("internal leaked into header %s: %q", key, v)
			}
		}
	}
}
