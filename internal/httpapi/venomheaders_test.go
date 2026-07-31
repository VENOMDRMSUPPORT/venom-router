package httpapi

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// --- pure builder tests ----------------------------------------------------

// TestWriteVenomHeaders_OmitsUnknownMetrics proves a nil metric pointer omits
// its header entirely rather than emitting a fabricated 0.
//
// O1-M1: emit "X-Venom-Tokens-In: 0" unconditionally ⇒ this test RED.
func TestWriteVenomHeaders_OmitsUnknownMetrics(t *testing.T) {
	h := http.Header{}
	latency := int64(12)
	writeVenomHeaders(h, venomTelemetry{
		RequestID: "req-1", Tier: "pro", Provider: "opencode-zen", Model: "venom/pro",
		LatencyMS: &latency, // known
		// TokensIn/TokensOut/Attempts nil ⇒ unknown ⇒ omitted
	})
	if got := h.Get("X-Venom-Tokens-In"); got != "" {
		t.Fatalf("X-Venom-Tokens-In = %q, want ABSENT (unknown metric must be omitted, never fabricated 0)", got)
	}
	if got := h.Get("X-Venom-Tokens-Out"); got != "" {
		t.Fatalf("X-Venom-Tokens-Out = %q, want ABSENT", got)
	}
	if got := h.Get("X-Venom-Fallback-Attempts"); got != "" {
		t.Fatalf("X-Venom-Fallback-Attempts = %q, want ABSENT", got)
	}
	if h.Get("X-Venom-Latency-Ms") != "12" {
		t.Fatalf("known latency should be present: %q", h.Get("X-Venom-Latency-Ms"))
	}
	if h.Get("X-Venom-Version") == "" {
		t.Fatalf("X-Venom-Version must always be present")
	}
}

// TestWriteVenomHeaders_NoAccountFieldExists is a structural guard: venomTelemetry
// has no account-identifier field, so the builder cannot stamp one even if a
// caller wanted to. (Compile-time proof lives in the type; this documents it.)
func TestWriteVenomHeaders_SanitizesValues(t *testing.T) {
	h := http.Header{}
	writeVenomHeaders(h, venomTelemetry{RequestID: "req-1", Tier: "pro", Provider: "Bearer sk-leak-999"})
	if strings.Contains(h.Get("X-Venom-Provider"), "sk-leak-999") {
		t.Fatalf("provider header leaked a bearer token: %q", h.Get("X-Venom-Provider"))
	}
}

// --- repo-shape guard ------------------------------------------------------

// TestVenomHeaders_SingleBuilder proves the X-Venom- prefix appears in NO
// production file other than venomheaders.go — there is exactly one builder.
func TestVenomHeaders_SingleBuilder(t *testing.T) {
	root := findRepoInternal(t)
	var offenders []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if filepath.Base(path) == "venomheaders.go" {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		// Match a header-name string LITERAL ("X-Venom-...), not prose in a
		// comment — only an actual write can name the header in a quoted string.
		if strings.Contains(string(b), `"X-Venom-`) {
			offenders = append(offenders, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(offenders) > 0 {
		t.Fatalf("X-Venom- headers written outside the single builder venomheaders.go: %v", offenders)
	}
}

func findRepoInternal(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 6; i++ {
		cand := filepath.Join(dir, "internal")
		if info, e := os.Stat(cand); e == nil && info.IsDir() {
			return cand
		}
		dir = filepath.Dir(dir)
	}
	t.Fatalf("could not locate internal/ from the test working dir")
	return ""
}

// --- end-to-end header tests ----------------------------------------------

// TestVenomHeaders_NonStreamingFullSet proves the full X-Venom-* set is stamped
// on a real completion, latency is a real measurement (> 0), and the fallback
// attempts count matches the loop.
//
// O1-M6: hardcode latencyMS to 0 ⇒ the latency>0 assertion RED.
func TestVenomHeaders_NonStreamingFullSet(t *testing.T) {
	_, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) {
		_, _ = w.Write([]byte(completionJSON("hi there")))
	})
	db, kr := e2eEnv(t, srv.URL)
	seedCredentialedOffering(t, db, kr, "acct-1", "prov/model-a", "model-a")
	h := newE2EHandler(t, db, kr, srv.URL, nil)

	rec := postChat(t, h, `{"model":"venom/pro","messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	hd := rec.Result().Header
	if hd.Get("X-Venom-Request-Id") == "" {
		t.Fatalf("missing X-Venom-Request-Id")
	}
	if hd.Get("X-Venom-Tier") != "pro" {
		t.Fatalf("X-Venom-Tier = %q, want pro", hd.Get("X-Venom-Tier"))
	}
	if hd.Get("X-Venom-Provider") != "opencode-zen" {
		t.Fatalf("X-Venom-Provider = %q, want opencode-zen", hd.Get("X-Venom-Provider"))
	}
	if hd.Get("X-Venom-Model") == "" {
		t.Fatalf("missing X-Venom-Model")
	}
	if hd.Get("X-Venom-Version") == "" {
		t.Fatalf("missing X-Venom-Version")
	}
	lat, err := strconv.Atoi(hd.Get("X-Venom-Latency-Ms"))
	if err != nil || lat <= 0 {
		t.Fatalf("X-Venom-Latency-Ms = %q, want a positive measurement", hd.Get("X-Venom-Latency-Ms"))
	}
	if hd.Get("X-Venom-Fallback-Attempts") != "1" {
		t.Fatalf("X-Venom-Fallback-Attempts = %q, want 1", hd.Get("X-Venom-Fallback-Attempts"))
	}
}

// TestVenomHeaders_UnknownTokensAbsent proves that with no provider-reported
// usage, the token-count headers are ABSENT (not "0").
func TestVenomHeaders_UnknownTokensAbsent(t *testing.T) {
	_, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) {
		_, _ = w.Write([]byte(completionJSON("hi")))
	})
	db, kr := e2eEnv(t, srv.URL)
	seedCredentialedOffering(t, db, kr, "acct-1", "prov/model-a", "model-a")
	h := newE2EHandler(t, db, kr, srv.URL, nil)

	rec := postChat(t, h, `{"model":"venom/pro","messages":[{"role":"user","content":"hi"}]}`)
	hd := rec.Result().Header
	if _, ok := hd["X-Venom-Tokens-In"]; ok {
		t.Fatalf("X-Venom-Tokens-In present (%q), want absent — token counts are unknown on this path", hd.Get("X-Venom-Tokens-In"))
	}
	if _, ok := hd["X-Venom-Tokens-Out"]; ok {
		t.Fatalf("X-Venom-Tokens-Out present, want absent")
	}
}

// TestVenomHeaders_HeaderCanary proves NO X-Venom-* header value carries the
// account id, the credential, or provider-originated content — while -Provider
// and -Model ARE present (the exclusion is targeted, not blanket silence).
//
// O1-M2: add X-Venom-Account-Id ⇒ RED. O1-M3: put provider content in -Model ⇒ RED.
func TestVenomHeaders_HeaderCanary(t *testing.T) {
	const providerContent = "PROVIDERSECRETcontent-xyz"
	_, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) {
		_, _ = w.Write([]byte(completionJSON(providerContent)))
	})
	db, kr := e2eEnv(t, srv.URL)
	seedCredentialedOffering(t, db, kr, "acct-1", "prov/model-a", "model-a")
	h := newE2EHandler(t, db, kr, srv.URL, nil)

	rec := postChat(t, h, `{"model":"venom/pro","messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	hd := rec.Result().Header
	for key, vals := range hd {
		if !strings.HasPrefix(key, "X-Venom-") {
			continue
		}
		for _, v := range vals {
			if strings.Contains(v, "acct-1") {
				t.Fatalf("header %s leaked the account id: %q", key, v)
			}
			if strings.Contains(v, upstreamCredential) || strings.Contains(v, "sk-upstream") {
				t.Fatalf("header %s leaked the credential: %q", key, v)
			}
			if strings.Contains(v, providerContent) {
				t.Fatalf("header %s leaked provider-originated content: %q", key, v)
			}
		}
	}
	// Targeted, not blanket: provider + model ARE present.
	if hd.Get("X-Venom-Provider") != "opencode-zen" {
		t.Fatalf("X-Venom-Provider should be present and correct, got %q", hd.Get("X-Venom-Provider"))
	}
	if hd.Get("X-Venom-Model") == "" {
		t.Fatalf("X-Venom-Model should be present")
	}
}

// TestVenomHeaders_StreamStartZeroedThenTrailer proves the stream-start header
// set arrives with ZEROED metrics, the trailing `: ` comment carries the final
// values, data: [DONE] is the LAST frame, and a comment-ignoring SSE reader
// still parses the stream cleanly.
//
// O1-M4: emit the trailer AFTER [DONE] ⇒ the [DONE]-is-last assertion RED.
// O1-M5: skip the stream-start header set ⇒ the zeroed-header assertion RED.
func TestVenomHeaders_StreamStartZeroedThenTrailer(t *testing.T) {
	_, srv := newUpstream(t, func(w http.ResponseWriter, _ []byte) {
		fl, _ := w.(http.Flusher)
		for _, d := range []string{"a", "b"} {
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"" + d + "\"}}]}\n\n"))
			if fl != nil {
				fl.Flush()
			}
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	})
	db, kr := e2eEnv(t, srv.URL)
	seedCredentialedOffering(t, db, kr, "acct-1", "prov/model-a", "model-a")
	certifyOperation(t, db, "acct-1", "prov/model-a", "streaming")
	h := newE2EHandler(t, db, kr, srv.URL, nil)

	rec := postChat(t, h, `{"model":"venom/pro","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	hd := rec.Result().Header
	// Stream-start headers present, with ZEROED metrics.
	if hd.Get("X-Venom-Request-Id") == "" || hd.Get("X-Venom-Tier") != "pro" {
		t.Fatalf("stream-start identity headers missing: %+v", hd)
	}
	if hd.Get("X-Venom-Latency-Ms") != "0" || hd.Get("X-Venom-Tokens-In") != "0" || hd.Get("X-Venom-Fallback-Attempts") != "0" {
		t.Fatalf("stream-start metrics must be zeroed: latency=%q tokensIn=%q attempts=%q",
			hd.Get("X-Venom-Latency-Ms"), hd.Get("X-Venom-Tokens-In"), hd.Get("X-Venom-Fallback-Attempts"))
	}

	body := rec.Body.String()
	// The trailing telemetry comment carries the FINAL provider.
	if !strings.Contains(body, ": ") || !strings.Contains(body, "x-venom-provider=opencode-zen") {
		t.Fatalf("missing trailing telemetry comment with final provider; body:\n%s", body)
	}
	// data: [DONE] is the LAST non-empty frame.
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	var last string
	for _, ln := range lines {
		if strings.TrimSpace(ln) != "" {
			last = strings.TrimSpace(ln)
		}
	}
	if last != "data: [DONE]" {
		t.Fatalf("last frame = %q, want data: [DONE]", last)
	}
	// The trailer comment appears BEFORE [DONE].
	if strings.Index(body, "x-venom-provider=") > strings.Index(body, "[DONE]") {
		t.Fatalf("telemetry trailer must precede [DONE]; body:\n%s", body)
	}
	// A strict SSE reader ignoring comment lines still sees the [DONE] data frame.
	sawDone := false
	for _, ln := range lines {
		if strings.HasPrefix(ln, ":") {
			continue // comment: ignored
		}
		if strings.TrimSpace(ln) == "data: [DONE]" {
			sawDone = true
		}
	}
	if !sawDone {
		t.Fatalf("comment-ignoring SSE reader did not see data: [DONE]")
	}
}
