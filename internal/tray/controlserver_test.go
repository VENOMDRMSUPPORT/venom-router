package tray

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeControls records which TrayControls method each route invoked and lets a
// test set the State() snapshot and SetAutostart's error/argument.
type fakeControls struct {
	calls        []string
	state        ControlState
	autostartArg *bool
	autostartErr error
}

func (f *fakeControls) rec(name string)     { f.calls = append(f.calls, name) }
func (f *fakeControls) State() ControlState { f.rec("State"); return f.state }
func (f *fakeControls) StartProd()          { f.rec("StartProd") }
func (f *fakeControls) StopProd()           { f.rec("StopProd") }
func (f *fakeControls) StartDev()           { f.rec("StartDev") }
func (f *fakeControls) StopDev()            { f.rec("StopDev") }
func (f *fakeControls) OpenProdDashboard()  { f.rec("OpenProdDashboard") }
func (f *fakeControls) OpenDevDashboard()   { f.rec("OpenDevDashboard") }
func (f *fakeControls) OpenDevLogs()        { f.rec("OpenDevLogs") }
func (f *fakeControls) OpenLogs()           { f.rec("OpenLogs") }
func (f *fakeControls) SetAutostart(enabled bool) error {
	f.rec("SetAutostart")
	f.autostartArg = &enabled
	return f.autostartErr
}
func (f *fakeControls) Quit() { f.rec("Quit") }

const testToken = "test-token-abc"
const testOrigin = "http://127.0.0.1:9999"

// authedPost issues a same-origin POST carrying the valid token.
func authedPost(t *testing.T, h http.Handler, path string, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("X-Control-Token", testToken)
	req.Header.Set("Origin", testOrigin)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// TestControlServer_PostRoutesDispatch pins that each POST route invokes
// exactly the matching TrayControls method.
func TestControlServer_PostRoutesDispatch(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/prod/start", "StartProd"},
		{"/prod/stop", "StopProd"},
		{"/prod/open", "OpenProdDashboard"},
		{"/dev/start", "StartDev"},
		{"/dev/stop", "StopDev"},
		{"/dev/open", "OpenDevDashboard"},
		{"/dev/logs", "OpenDevLogs"},
		{"/logs", "OpenLogs"},
		{"/quit", "Quit"},
	}
	for _, tc := range cases {
		f := &fakeControls{}
		h := newControlHandler(f, testToken, testOrigin)
		rr := authedPost(t, h, tc.path, "")
		if rr.Code != http.StatusNoContent {
			t.Errorf("%s: status = %d, want 204", tc.path, rr.Code)
		}
		if len(f.calls) != 1 || f.calls[0] != tc.want {
			t.Errorf("%s: calls = %v, want [%s]", tc.path, f.calls, tc.want)
		}
	}
}

// TestControlServer_AutostartParsesEnabled pins that /autostart parses the
// enabled flag from the JSON body and forwards it to SetAutostart.
func TestControlServer_AutostartParsesEnabled(t *testing.T) {
	f := &fakeControls{}
	h := newControlHandler(f, testToken, testOrigin)

	rr := authedPost(t, h, "/autostart", `{"enabled":true}`)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rr.Code)
	}
	if f.autostartArg == nil || *f.autostartArg != true {
		t.Errorf("SetAutostart arg = %v, want true", f.autostartArg)
	}
}

// TestControlServer_RejectsMissingToken pins that a token-gated request with no
// token is refused 403 and performs no side effect.
func TestControlServer_RejectsMissingToken(t *testing.T) {
	f := &fakeControls{}
	h := newControlHandler(f, testToken, testOrigin)

	req := httptest.NewRequest(http.MethodPost, "/prod/stop", nil)
	req.Header.Set("Origin", testOrigin) // same origin, but no token
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rr.Code)
	}
	if len(f.calls) != 0 {
		t.Errorf("side effect ran despite missing token: %v", f.calls)
	}
}

// TestControlServer_RejectsWrongToken pins that a mismatched token is refused.
func TestControlServer_RejectsWrongToken(t *testing.T) {
	f := &fakeControls{}
	h := newControlHandler(f, testToken, testOrigin)

	req := httptest.NewRequest(http.MethodPost, "/prod/stop", nil)
	req.Header.Set("X-Control-Token", "not-the-token")
	req.Header.Set("Origin", testOrigin)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rr.Code)
	}
	if len(f.calls) != 0 {
		t.Errorf("side effect ran despite wrong token: %v", f.calls)
	}
}

// TestControlServer_RejectsForeignOrigin pins that a request from another
// origin is refused even with a valid token (defense against a page that
// somehow learned the token but calls cross-origin).
func TestControlServer_RejectsForeignOrigin(t *testing.T) {
	f := &fakeControls{}
	h := newControlHandler(f, testToken, testOrigin)

	req := httptest.NewRequest(http.MethodPost, "/prod/stop", nil)
	req.Header.Set("X-Control-Token", testToken)
	req.Header.Set("Origin", "http://evil.example")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rr.Code)
	}
	if len(f.calls) != 0 {
		t.Errorf("side effect ran despite foreign origin: %v", f.calls)
	}
}

// TestControlServer_GetRootServesPageWithToken pins that GET / is token-free
// (the top-level app-window navigation can't set a custom header) and returns
// the HTML control page with the per-startup token baked in for its fetch()es.
func TestControlServer_GetRootServesPageWithToken(t *testing.T) {
	f := &fakeControls{}
	h := newControlHandler(f, testToken, testOrigin)

	req := httptest.NewRequest(http.MethodGet, "/", nil) // deliberately no token
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("content-type = %q, want text/html", ct)
	}
	if !strings.Contains(rr.Body.String(), testToken) {
		t.Error("served page does not contain the session token")
	}
	if got := rr.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("cache-control = %q, want no-store so a restarted app cannot reuse an expired control token", got)
	}
}

// TestControlServer_StateReturnsJSON pins the /state snapshot shape.
func TestControlServer_StateReturnsJSON(t *testing.T) {
	f := &fakeControls{state: ControlState{
		Prod:            "running",
		DevAvailable:    true,
		DevOverall:      "Stopped",
		DevBackend:      "Stopped",
		DevFrontend:     "Stopped",
		DevError:        "",
		DevLogAvailable: true,
		Autostart:       true,
	}}
	h := newControlHandler(f, testToken, testOrigin)

	req := httptest.NewRequest(http.MethodGet, "/state", nil)
	req.Header.Set("X-Control-Token", testToken)
	req.Header.Set("Origin", testOrigin)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var got ControlState
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("body is not valid JSON: %v (%s)", err, rr.Body.String())
	}
	if got != f.state {
		t.Errorf("state = %+v, want %+v", got, f.state)
	}
	if cacheControl := rr.Header().Get("Cache-Control"); cacheControl != "no-store" {
		t.Errorf("cache-control = %q, want no-store so polling observes lifecycle changes", cacheControl)
	}
}
