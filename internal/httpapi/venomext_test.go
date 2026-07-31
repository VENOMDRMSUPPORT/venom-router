package httpapi

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/routing"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// --- pure parser tests -----------------------------------------------------

func TestParseVenomExtension_Table(t *testing.T) {
	// absent / null ⇒ (nil, nil)
	for _, raw := range []string{``, `null`, `  `} {
		ext, err := parseVenomExtension(json.RawMessage(raw))
		if err != nil || ext != nil {
			t.Fatalf("parse(%q) = (%v, %v), want (nil, nil)", raw, ext, err)
		}
	}
	// valid
	ext, err := parseVenomExtension(json.RawMessage(`{"thinking_budget":"extended","required_capabilities":["vision","reasoning"]}`))
	if err != nil {
		t.Fatalf("valid parse: %v", err)
	}
	if ext.ThinkingBudget == nil || *ext.ThinkingBudget != routing.ThinkingExtended {
		t.Fatalf("thinking_budget = %v, want extended", ext.ThinkingBudget)
	}
	if len(ext.RequiredCapabilities) != 2 {
		t.Fatalf("capabilities = %v, want 2", ext.RequiredCapabilities)
	}
	// unknown field inside venom ⇒ error naming it, wrapping ErrInvalidExtension
	_, err = parseVenomExtension(json.RawMessage(`{"thinking_budget":"extended","bogus":1}`))
	if err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("unknown field err = %v, want one naming bogus", err)
	}
	var ve *venomExtError
	if !asVenomExtError(err, &ve) || ve.Field != "bogus" {
		t.Fatalf("expected venomExtError naming bogus, got %v", err)
	}
	// invalid thinking value / unknown capability ⇒ named errors
	if _, err := parseVenomExtension(json.RawMessage(`{"thinking_budget":"turbo"}`)); err == nil || !strings.Contains(venomExtErrorMessage(err), "thinking_budget") {
		t.Fatalf("invalid thinking value not rejected/named: %v", err)
	}
	if _, err := parseVenomExtension(json.RawMessage(`{"required_capabilities":["telepathy"]}`)); err == nil || !strings.Contains(venomExtErrorMessage(err), "required_capabilities") {
		t.Fatalf("unknown capability not rejected/named: %v", err)
	}
}

func asVenomExtError(err error, target **venomExtError) bool {
	ve, ok := err.(*venomExtError)
	if ok {
		*target = ve
	}
	return ok
}

// --- end-to-end wiring tests ----------------------------------------------

func decisionRequestedThinking(t *testing.T, db *storage.DB) string {
	t.Helper()
	var rt sql.NullString
	if err := db.Conn().QueryRow(`SELECT requested_thinking FROM route_decisions ORDER BY rowid DESC LIMIT 1`).Scan(&rt); err != nil {
		t.Fatalf("read requested_thinking: %v", err)
	}
	return rt.String
}

// TestVenomExt_ThinkingRequestHonored proves thinking_budget flows through
// Normalize into the recorded decision (requested_thinking column).
//
// P4-M1: ignore thinking_budget entirely ⇒ requested_thinking empty ⇒ RED.
func TestVenomExt_ThinkingRequestHonored(t *testing.T) {
	_, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) { _, _ = w.Write([]byte(completionJSON("ok"))) })
	db, kr := e2eEnv(t, srv.URL)
	seedCredentialedOffering(t, db, kr, "acct-1", "prov/model-a", "model-a")
	h := newE2EHandler(t, db, kr, srv.URL, nil)

	rec := postChat(t, h, `{"model":"venom/pro","messages":[{"role":"user","content":"hi"}],"venom":{"thinking_budget":"ultra"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if got := decisionRequestedThinking(t, db); got != "ultra" {
		t.Fatalf("route_decisions.requested_thinking = %q, want ultra (thinking_budget must be honored)", got)
	}
}

// TestVenomExt_ThinkingClampReported proves a request above the tier ceiling is
// CLAMPED, the request still succeeds, and the clamp is REPORTED on the header.
//
// P4-M2: clamp silently without reporting ⇒ X-Venom-Thinking-Clamped absent ⇒ RED.
func TestVenomExt_ThinkingClampReported(t *testing.T) {
	_, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) { _, _ = w.Write([]byte(completionJSON("ok"))) })
	db, kr := e2eEnv(t, srv.URL)
	seedCredentialedOffering(t, db, kr, "acct-1", "prov/model-a", "model-a")
	h := newE2EHandler(t, db, kr, srv.URL, nil)

	rec := postChat(t, h, `{"model":"venom/pro","messages":[{"role":"user","content":"hi"}],"venom":{"thinking_budget":"ultra"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("clamped request must still succeed; status %d", rec.Code)
	}
	if got := rec.Result().Header.Get("X-Venom-Thinking-Clamped"); got != "true" {
		t.Fatalf("X-Venom-Thinking-Clamped = %q, want true (the clamp must be reported, not silent)", got)
	}
	if got := rec.Result().Header.Get("X-Venom-Thinking-Applied"); got == "" {
		t.Fatalf("X-Venom-Thinking-Applied must be present")
	}
}

// TestVenomExt_UnknownFieldRejected proves an unknown field INSIDE venom is a
// 400 venom_invalid_extension naming the field, with nothing executed.
//
// P4-M3: accept unknown field inside venom ⇒ routes (not 400) ⇒ RED.
func TestVenomExt_UnknownFieldRejected(t *testing.T) {
	up, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) { _, _ = w.Write([]byte(completionJSON("ok"))) })
	db, kr := e2eEnv(t, srv.URL)
	seedCredentialedOffering(t, db, kr, "acct-1", "prov/model-a", "model-a")
	h := newE2EHandler(t, db, kr, srv.URL, nil)

	rec := postChat(t, h, `{"model":"venom/pro","messages":[{"role":"user","content":"hi"}],"venom":{"thinking_budget":"extended","bogus":1}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for an unknown venom field", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), CodeInvalidExtension) || !strings.Contains(rec.Body.String(), "bogus") {
		t.Fatalf("body must name code %s and field bogus: %s", CodeInvalidExtension, rec.Body.String())
	}
	if up.calls != 0 {
		t.Fatalf("provider was called %d times on an invalid extension; want 0 (nothing executes)", up.calls)
	}
}

// TestVenomExt_UnknownTopLevelAccepted proves an unknown TOP-LEVEL body field is
// still accepted (SDK parity) — the counter-test proving the strict decode is
// scoped to the venom sub-object only.
//
// P4-M4: apply DisallowUnknownFields to the WHOLE body ⇒ 400 ⇒ RED.
func TestVenomExt_UnknownTopLevelAccepted(t *testing.T) {
	_, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) { _, _ = w.Write([]byte(completionJSON("ok"))) })
	db, kr := e2eEnv(t, srv.URL)
	seedCredentialedOffering(t, db, kr, "acct-1", "prov/model-a", "model-a")
	h := newE2EHandler(t, db, kr, srv.URL, nil)

	rec := postChat(t, h, `{"model":"venom/pro","messages":[{"role":"user","content":"hi"}],"user":"abc","top_p":0.9}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — unknown TOP-LEVEL fields must be ignored (parity); body %s", rec.Code, rec.Body.String())
	}
}

// TestVenomExt_RequiredCapabilityHardGate proves required_capabilities are hard
// Step-3 gates: a capability the fleet cannot certify never routes.
//
// P4-M5: drop required_capabilities before Normalize ⇒ the chat-only offering
// routes and returns 200 ⇒ RED.
func TestVenomExt_RequiredCapabilityHardGate(t *testing.T) {
	up, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) { _, _ = w.Write([]byte(completionJSON("ok"))) })
	db, kr := e2eEnv(t, srv.URL)
	seedCredentialedOffering(t, db, kr, "acct-1", "prov/model-a", "model-a") // certifies chat only
	h := newE2EHandler(t, db, kr, srv.URL, nil)

	rec := postChat(t, h, `{"model":"venom/pro","messages":[{"role":"user","content":"hi"}],"venom":{"required_capabilities":["vision"]}}`)
	if rec.Code == http.StatusOK {
		t.Fatalf("a required capability the fleet cannot certify must NOT route; got 200")
	}
	if up.calls != 0 {
		t.Fatalf("provider called %d times though no eligible route exists; want 0", up.calls)
	}
}

// TestVenomExt_InvalidThinkingValueRejected proves an invalid thinking_budget
// value is a 400, never coerced to a default.
//
// P4-M6: coerce an invalid thinking value to the tier default ⇒ routes ⇒ RED.
func TestVenomExt_InvalidThinkingValueRejected(t *testing.T) {
	up, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) { _, _ = w.Write([]byte(completionJSON("ok"))) })
	db, kr := e2eEnv(t, srv.URL)
	seedCredentialedOffering(t, db, kr, "acct-1", "prov/model-a", "model-a")
	h := newE2EHandler(t, db, kr, srv.URL, nil)

	rec := postChat(t, h, `{"model":"venom/pro","messages":[{"role":"user","content":"hi"}],"venom":{"thinking_budget":"turbo"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for an invalid thinking_budget value", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "thinking_budget") {
		t.Fatalf("body must name thinking_budget: %s", rec.Body.String())
	}
	if up.calls != 0 {
		t.Fatalf("provider called on an invalid extension; want 0")
	}
}

// TestVenomExt_StreamingTrailerCarriesThinking proves the applied thinking level
// survives into the streaming trailer.
//
// P4-M7: leave FallbackResult.ThinkingApplied unset ⇒ the trailer omits it ⇒ RED.
func TestVenomExt_StreamingTrailerCarriesThinking(t *testing.T) {
	_, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) {
		fl, _ := w.(http.Flusher)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n"))
		if fl != nil {
			fl.Flush()
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	})
	db, kr := e2eEnv(t, srv.URL)
	seedCredentialedOffering(t, db, kr, "acct-1", "prov/model-a", "model-a")
	certifyOperation(t, db, "acct-1", "prov/model-a", "streaming")
	h := newE2EHandler(t, db, kr, srv.URL, nil)

	rec := postChat(t, h, `{"model":"venom/pro","stream":true,"messages":[{"role":"user","content":"hi"}],"venom":{"thinking_budget":"ultra"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "x-venom-thinking-applied=") {
		t.Fatalf("streaming trailer must carry x-venom-thinking-applied; body:\n%s", rec.Body.String())
	}
}
