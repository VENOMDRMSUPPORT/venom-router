package tray

import "context"

// trayControlsAdapter is the thin glue that satisfies TrayControls over the
// real tray objects: the production Controller, the DevSupervisor, the
// autostart funcs, and the root cancel. Lifecycle calls that can block (prod
// stop, dev teardown) are launched in their own goroutine so a control-server
// HTTP handler returns promptly and the page polls /state for the result.
type trayControlsAdapter struct {
	ctx    context.Context
	c      *Controller
	dev    *DevSupervisor
	cancel context.CancelFunc
}

var _ TrayControls = (*trayControlsAdapter)(nil)

func (a *trayControlsAdapter) State() ControlState {
	dv := a.dev.Status()
	devError := dv.FrontendDetail
	if devError == "" {
		devError = dv.BackendDetail
	}
	return ControlState{
		Prod:            prodStateName(a.c.Status().State),
		DevAvailable:    a.dev.Available(),
		DevOverall:      dv.Overall.title(),
		DevBackend:      dv.Backend.title(),
		DevFrontend:     dv.Frontend.title(),
		DevError:        devError,
		DevLogAvailable: dv.LogPath != "",
		Autostart:       autostartEnabled(),
	}
}

func (a *trayControlsAdapter) StartProd() { go a.c.Start(a.ctx) }
func (a *trayControlsAdapter) StopProd()  { go a.c.Stop() }
func (a *trayControlsAdapter) StartDev()  { go EnterDevMode(a.ctx, a.c, a.dev) }
func (a *trayControlsAdapter) StopDev()   { go ExitDevMode(a.ctx, a.c, a.dev) }

func (a *trayControlsAdapter) OpenProdDashboard() { a.c.OpenDashboard() }
func (a *trayControlsAdapter) OpenDevDashboard()  { a.c.OpenURL(a.dev.DashboardURL()) }
func (a *trayControlsAdapter) OpenDevLogs()       { a.c.OpenURL(a.dev.LogPath()) }
func (a *trayControlsAdapter) OpenLogs()          { a.c.OpenLogs() }

func (a *trayControlsAdapter) SetAutostart(enabled bool) error {
	if enabled {
		return enableAutostart()
	}
	return disableAutostart()
}

func (a *trayControlsAdapter) Quit() { a.cancel() }

// prodStateName maps the coarse production State to the lowercase token the
// control page renders and compares.
func prodStateName(s State) string {
	switch s {
	case StateRunning:
		return "running"
	case StateError:
		return "error"
	default:
		return "stopped"
	}
}
