package httpapi

import (
	"context"
	"fmt"
	"net"
	"net/http"
)

// FixedPortOAuthServer is a standalone loopback HTTP server framework for
// a future OAuth provider whose registered redirect_uri is a fixed,
// pre-registered port (e.g. "http://127.0.0.1:<port>/callback") that
// cannot simply be the control plane's own bind port — some providers'
// OAuth app registrations are literal, unchangeable URIs. It is a
// genuinely separate net.Listener and *http.Server from ControlMux's:
// no route this server exposes is reachable through the control-plane
// mux's listener, and no control-plane route (session-bound or not) is
// reachable through this one — the caller supplies exactly the handler
// it wants served here, nothing more.
//
// This is transaction-based, not session-bound, by construction: the
// caller's handler is expected to key off a transaction/state value in
// the callback path or query, exactly like OAuthHandler.ServeCallback
// does on the control-plane mux — there is no owner-session concept
// available on this listener at all.
//
// No provider registered this phase needs a fixed port (every adapter
// this unit ships uses the control-plane callback route), so this type
// is built and proven isolated (see oauth_fixedport_test.go) but is NOT
// constructed anywhere in the production boot path (internal/app/boot.go
// never calls StartFixedPortOAuthServer) — it is a ready framework for
// whichever future provider needs it.
type FixedPortOAuthServer struct {
	ln     net.Listener
	server *http.Server
}

// StartFixedPortOAuthServer binds a new loopback-only ("127.0.0.1:<port>")
// listener and serves handler on it in a background goroutine. port may
// be 0 to let the OS choose a free port (the caller reads it back via
// Addr) — the shape tests use, so parallel test runs never collide on a
// fixed port number.
func StartFixedPortOAuthServer(port int, handler http.Handler) (*FixedPortOAuthServer, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, fmt.Errorf("httpapi: start fixed-port oauth server on port %d: %w", port, err)
	}

	srv := &http.Server{Handler: handler}
	go func() {
		_ = srv.Serve(ln)
	}()

	return &FixedPortOAuthServer{ln: ln, server: srv}, nil
}

// Addr returns the listener's actual bound address ("127.0.0.1:<port>") —
// the real port when StartFixedPortOAuthServer was called with port 0.
func (s *FixedPortOAuthServer) Addr() string {
	return s.ln.Addr().String()
}

// Shutdown gracefully stops the server, closing its listener.
func (s *FixedPortOAuthServer) Shutdown(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}
