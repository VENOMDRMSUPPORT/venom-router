package tray

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/app"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/observability"
)

// ServerAdapter is the production ServerLifecycle backed by internal/app.
type ServerAdapter struct {
	bind   string
	logger *observability.Logger

	mu  sync.Mutex
	srv *app.Server
}

// NewServerLifecycle returns a ServerAdapter for the given loopback bind.
func NewServerLifecycle(bind string, logger *observability.Logger) *ServerAdapter {
	return &ServerAdapter{bind: bind, logger: logger}
}

func (a *ServerAdapter) Boot(ctx context.Context) error {
	srv, err := app.Boot(ctx, app.BootConfig{Bind: a.bind, Logger: a.logger})
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.srv = srv
	a.mu.Unlock()
	return nil
}

func (a *ServerAdapter) Shutdown(ctx context.Context) error {
	a.mu.Lock()
	srv := a.srv
	a.srv = nil
	a.mu.Unlock()
	if srv == nil {
		return nil
	}
	return srv.Shutdown(ctx)
}

// Healthy probes GET http://<bind>/health with a short timeout. The Host header
// is the bind, which the control plane's network gate accepts.
func (a *ServerAdapter) Healthy(ctx context.Context) bool {
	cctx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, "http://"+a.bind+"/health", nil)
	if err != nil {
		return false
	}
	client := &http.Client{Timeout: 1 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode == http.StatusOK
}

func (a *ServerAdapter) DashboardURL() string {
	return "http://" + a.bind + "/"
}

// compile-time guard
var _ ServerLifecycle = (*ServerAdapter)(nil)
