package httpapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
)

// TestClinePassGetSeam_WorkosPrefixAndHeaders proves the adapter's own
// authenticated GETs carry Authorization: Bearer workos:<token> (prefix applied
// at the wire) and the cline headers, and return the raw status.
func TestClinePassGetSeam_WorkosPrefixAndHeaders(t *testing.T) {
	var h http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"clineUserId":"u"}`))
	}))
	t.Cleanup(srv.Close)

	status, _, err := clinePassGetSeam(context.Background(), srv.URL+"/api/v1/users/me", "rawtoken")
	if err != nil {
		t.Fatalf("seam error = %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if h.Get("Authorization") != "Bearer workos:rawtoken" {
		t.Fatalf("Authorization = %q, want 'Bearer workos:rawtoken'", h.Get("Authorization"))
	}
	if h.Get("X-CLIENT-TYPE") != "venom-router" || h.Get("X-Title") != "Cline" || h.Get("HTTP-Referer") != "https://cline.bot" {
		t.Fatalf("cline headers missing: %v", h)
	}
}

// TestClinePassGetSeam_WorkosPrefixIdempotent proves an already-prefixed token
// is not double-prefixed.
func TestClinePassGetSeam_WorkosPrefixIdempotent(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	if _, _, err := clinePassGetSeam(context.Background(), srv.URL, "workos:already"); err != nil {
		t.Fatalf("seam error = %v", err)
	}
	if auth != "Bearer workos:already" {
		t.Fatalf("Authorization = %q, want no double prefix", auth)
	}
}

// TestClinePassGetSeam_BlankTokenSendsNoAuth proves a blank access token sends
// NO Authorization header: the recommended-models endpoint is public and must
// not receive a workos:-prefixed header (legacy 2026-08-03 fetches it
// headerless).
func TestClinePassGetSeam_BlankTokenSendsNoAuth(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	if _, _, err := clinePassGetSeam(context.Background(), srv.URL, ""); err != nil {
		t.Fatalf("seam error = %v", err)
	}
	if auth != "" {
		t.Fatalf("Authorization = %q, want none for the public models endpoint", auth)
	}
}

// clinePassWiringModelID is the one live id the fake transport below agrees
// exists in both the recommended-models response and the models.dev dataset —
// production intersects them by id, so both canned bodies must use the same
// value.
const clinePassWiringModelID = "cline-pass/wiring-test-model"

// clinePassWiringFakeTransport is an in-process http.RoundTripper that
// answers ONLY the three exact outbound requests
// ClinePassAdapter.DiscoverModels makes through the REAL seams (usage-limits,
// recommended-models, and the public models.dev dataset), with fixed,
// minimal, successful bodies — anything else fails the test loudly rather
// than silently falling through to a real socket.
type clinePassWiringFakeTransport struct{ t *testing.T }

func (rt clinePassWiringFakeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	url := req.URL.String()
	switch {
	case strings.Contains(url, "/plan/usage-limits"):
		return clinePassWiringJSONResponse(req, `{"success":true,"data":{"limits":[{"type":"five_hour","percentUsed":1}]}}`), nil
	case strings.Contains(url, "/recommended-models"):
		return clinePassWiringJSONResponse(req, fmt.Sprintf(`{"clinePass":[{"id":%q,"name":"wire name"}]}`, clinePassWiringModelID)), nil
	case url == "https://models.dev/api.json":
		return clinePassWiringJSONResponse(req, fmt.Sprintf(
			`{"cline-pass":{"id":"cline-pass","api":%q,"models":{%q:{"id":%q,"name":"Wired Display Name","tool_call":true,"structured_output":true,"modalities":{"input":["text","image"],"output":["text"]},"limit":{"context":4096,"output":2048}}}}}`,
			providers.ClinePassBaseURL+"/api/v1", clinePassWiringModelID, clinePassWiringModelID,
		)), nil
	default:
		rt.t.Fatalf("unexpected outbound HTTP request during the clinepass composition-root wiring test: %s %s", req.Method, url)
		return nil, fmt.Errorf("unexpected request: %s %s", req.Method, url)
	}
}

func clinePassWiringJSONResponse(req *http.Request, body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Request:    req,
	}
}

// TestRegisterClinePass_DiscoverModelsJoinsRealModelsDevSeam is the
// composition-root mutation proof for registerClinePass's models.dev wiring
// (task-6 Step 7): it builds the registry EXACTLY as ControlMux/publicMux do
// (registerClinePass(reg) — the real clinePassGetSeam/clinePassPostSeam and
// the real openCodeZenModelsDevProbeSeam, no test-injected fakes for any of
// the three), swapping only the process-wide http.DefaultTransport (which all
// three seams resolve to, since neither clinePassHTTPClient nor
// openCodeZenHTTPClient sets its own Transport) to intercept the exact
// outbound requests DiscoverModels makes — mirroring the established pattern
// in TestControlMux_DiscoverySuccess_FiresUsabilityTriggerThroughRealWiring
// (usability_trigger_controlmux_test.go). If openCodeZenModelsDevProbeSeam
// were ever swapped out for a stub inside registerClinePass (or the wiring
// quietly dropped), this is the test that would notice: it asserts a
// DisplayName, ContextLength and capability that can ONLY come from the
// models.dev dataset actually being joined through the real seam.
func TestRegisterClinePass_DiscoverModelsJoinsRealModelsDevSeam(t *testing.T) {
	// GUARD: swaps a PROCESS-WIDE global; safe only because this package's
	// tests never run in parallel (see the identical guard in
	// usability_trigger_controlmux_test.go).
	originalTransport := http.DefaultTransport
	http.DefaultTransport = clinePassWiringFakeTransport{t: t}
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	reg := providers.NewRegistry()
	if err := registerClinePass(reg); err != nil {
		t.Fatalf("registerClinePass: %v", err)
	}
	def, ok := reg.Definition(providers.ClinePassID)
	if !ok {
		t.Fatal("clinepass not registered")
	}

	creds := providers.StoredCredentials{Value: `{"access_token":"t","refresh_token":"r","expires_at":9999999999}`}
	models, err := def.Discovery.DiscoverModels(context.Background(), creds)
	if err != nil {
		t.Fatalf("DiscoverModels: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("models = %+v, want 1", models)
	}
	m := models[0]
	if m.DisplayName != "Wired Display Name" {
		t.Fatalf("DisplayName = %q, want the models.dev catalog name — the real openCodeZenModelsDevProbeSeam must be wired for this to appear", m.DisplayName)
	}
	if m.ContextLength == nil || *m.ContextLength != 4096 {
		t.Fatalf("ContextLength = %v, want 4096 from the models.dev limit.context", m.ContextLength)
	}
	hasTools := false
	for _, capability := range m.Capabilities {
		if capability == "tools" {
			hasTools = true
		}
	}
	if !hasTools {
		t.Fatalf("Capabilities = %v, want tools present (declared by the models.dev entry)", m.Capabilities)
	}
}
