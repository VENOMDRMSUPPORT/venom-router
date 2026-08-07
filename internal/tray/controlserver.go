package tray

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
)

// controlPageTemplate is the self-contained control window (inline CSS + JS, no
// external assets). __VENOM_CONTROL_TOKEN__ is replaced per request with the
// per-startup token so the page's fetch()es can authenticate.
//
//go:embed controlpage.html
var controlPageTemplate string

// controlTokenHeader carries the per-startup control token on every request
// except the GET / bootstrap. Deliberately NOT under the reserved X-Venom-
// response-header prefix (that namespace belongs to the single telemetry
// builder, internal/httpapi/venomheaders.go) — this is an unrelated request
// header on the loopback control listener.
const controlTokenHeader = "X-Control-Token"

// ControlState is the JSON snapshot the control page polls from GET /state.
// Dev states are the human titles ("Stopped"/"Starting"/"Running"/"Error") so
// the page can render and compare them directly.
type ControlState struct {
	Prod            string `json:"prod"` // "running" | "stopped" | "error"
	DevAvailable    bool   `json:"devAvailable"`
	DevOverall      string `json:"devOverall"`
	DevBackend      string `json:"devBackend"`
	DevFrontend     string `json:"devFrontend"`
	DevError        string `json:"devError,omitempty"`
	DevLogAvailable bool   `json:"devLogAvailable"`
	Autostart       bool   `json:"autostart"`
}

// TrayControls is the set of operations the control window drives. It is
// satisfied by trayControlsAdapter (a thin closure over the Controller, the
// DevSupervisor, the autostart funcs, and the root cancel); the control server
// depends only on this interface, so its routing and security are testable
// against a fake.
type TrayControls interface {
	State() ControlState
	StartProd()
	StopProd()
	StartDev()
	StopDev()
	OpenProdDashboard()
	OpenDevDashboard()
	OpenDevLogs()
	OpenLogs()
	SetAutostart(enabled bool) error
	Quit()
}

// ControlServer is the tray-owned loopback HTTP listener backing the control
// window. It binds an ephemeral 127.0.0.1 port and lives for the tray's whole
// lifetime, independent of the production app server — so the window can drive
// prod/dev even while production is stopped.
type ControlServer struct {
	srv *http.Server
	ln  net.Listener
	url string
}

// NewControlServer binds the loopback listener, mints a per-startup token, and
// builds the hardened handler. Call Start to serve and Shutdown to stop.
func NewControlServer(controls TrayControls) (*ControlServer, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("tray: control listener: %w", err)
	}
	token, err := randomToken()
	if err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("tray: control token: %w", err)
	}
	selfOrigin := "http://" + ln.Addr().String()
	return &ControlServer{
		srv: &http.Server{Handler: newControlHandler(controls, token, selfOrigin)},
		ln:  ln,
		url: selfOrigin + "/",
	}, nil
}

// URL is the control window's address (http://127.0.0.1:<port>/).
func (cs *ControlServer) URL() string { return cs.url }

// Start serves in the background until Shutdown.
func (cs *ControlServer) Start() { go func() { _ = cs.srv.Serve(cs.ln) }() }

// Shutdown stops the listener gracefully.
func (cs *ControlServer) Shutdown(ctx context.Context) error { return cs.srv.Shutdown(ctx) }

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// newControlHandler builds the control server's HTTP handler. token and
// selfOrigin drive the security middleware (added by securityMiddleware); the
// route mux dispatches each path to the matching TrayControls method.
func newControlHandler(controls TrayControls, token, selfOrigin string) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		// The page embeds a per-startup control token. Reusing it after a tray
		// restart makes every button fail authentication with no visible effect.
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, strings.ReplaceAll(controlPageTemplate, "__VENOM_CONTROL_TOKEN__", token))
	})

	mux.HandleFunc("/state", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		// This is a live lifecycle snapshot polled by the control window. Edge
		// otherwise may reuse the initial Stopped response after Start succeeds.
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(controls.State())
	})

	mux.Handle("/prod/start", postAction(controls.StartProd))
	mux.Handle("/prod/stop", postAction(controls.StopProd))
	mux.Handle("/prod/open", postAction(controls.OpenProdDashboard))
	mux.Handle("/dev/start", postAction(controls.StartDev))
	mux.Handle("/dev/stop", postAction(controls.StopDev))
	mux.Handle("/dev/open", postAction(controls.OpenDevDashboard))
	mux.Handle("/dev/logs", postAction(controls.OpenDevLogs))
	mux.Handle("/logs", postAction(controls.OpenLogs))
	mux.Handle("/quit", postAction(controls.Quit))

	mux.HandleFunc("/autostart", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if err := controls.SetAutostart(body.Enabled); err != nil {
			http.Error(w, "autostart failed", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	return securityMiddleware(token, selfOrigin, mux)
}

// securityMiddleware hardens the control listener against localhost CSRF /
// DNS-rebinding. Every request EXCEPT the GET / bootstrap must carry the
// per-startup token in X-Venom-Control-Token (a custom header, so any
// cross-origin caller is forced through a CORS preflight the server never
// approves), and any request whose Origin is present and not our own is
// refused before any side effect. GET / is exempt because it is the top-level
// navigation from the app-window, which cannot set a custom header; it only
// returns the page, and same-origin policy keeps the baked-in token secret.
func securityMiddleware(token, selfOrigin string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/" {
			next.ServeHTTP(w, r)
			return
		}
		if o := r.Header.Get("Origin"); o != "" && o != selfOrigin {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if r.Header.Get(controlTokenHeader) != token {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// postAction wraps a no-arg side effect as a POST-only handler returning 204.
func postAction(fn func()) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		fn()
		w.WriteHeader(http.StatusNoContent)
	})
}
