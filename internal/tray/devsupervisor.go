package tray

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/observability"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/platform"
)

// Development-environment constants (design 2026-07-31). The dev section is a
// Windows desktop affordance; the frontend spec deliberately runs npm through
// cmd /c. Ports are fixed by the approved design.
const (
	devFrontendURL   = "http://127.0.0.1:5173/"
	devBackendBind   = "127.0.0.1:8082"
	devBackendHealth = "http://" + devBackendBind + "/health"
	devAPITarget     = "VENOM_DEV_API_TARGET=http://" + devBackendBind
)

// DevComponentState is one dev child's coarse state.
type DevComponentState int

const (
	DevStopped DevComponentState = iota
	DevStarting
	DevRunning
	DevError
)

// title is the capitalized overall form ("Stopped"); label the lowercase
// per-component form ("stopped") — both exactly as the menu renders them.
func (s DevComponentState) title() string {
	switch s {
	case DevStarting:
		return "Starting"
	case DevRunning:
		return "Running"
	case DevError:
		return "Error"
	default:
		return "Stopped"
	}
}

func (s DevComponentState) label() string {
	switch s {
	case DevStarting:
		return "starting"
	case DevRunning:
		return "running"
	case DevError:
		return "error"
	default:
		return "stopped"
	}
}

// ProcessSpec describes one dev child process.
type ProcessSpec struct {
	Dir      string
	Name     string
	Args     []string
	ExtraEnv []string // KEY=VALUE entries appended to the parent env
}

// ProcessHandle is a started child as the supervisor sees it.
type ProcessHandle interface {
	// Wait blocks until the process exits.
	Wait() error
	// Kill terminates the whole process tree. Idempotent.
	Kill() error
}

// ProcessRunner spawns dev child processes (Windows: inside a kill-on-close
// Job Object; see devprocess_windows.go).
type ProcessRunner interface {
	Start(spec ProcessSpec) (ProcessHandle, error)
}

// HealthProbe reports whether url currently answers HTTP at all.
type HealthProbe func(ctx context.Context, url string) bool

// DefaultHealthProbe: any HTTP response (any status) proves the listener is
// up — vite and the dev backend both answer GET on their probe URLs.
func DefaultHealthProbe(ctx context.Context, url string) bool {
	cctx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return true
}

// DevStatusView is an immutable snapshot for the UI.
type DevStatusView struct {
	Overall  DevComponentState
	Frontend DevComponentState
	Backend  DevComponentState
}

// statusLine renders the menu's dev info line, e.g.
// "Dev Status: Starting · Frontend starting · Backend running".
func (v DevStatusView) statusLine() string {
	return "Dev Status: " + v.Overall.title() +
		" · Frontend " + v.Frontend.label() +
		" · Backend " + v.Backend.label()
}

// overallDevState aggregates: Error dominates, then Starting (any component
// not yet Running while another is up also reads Starting), then Running
// (both), else Stopped.
func overallDevState(f, b DevComponentState) DevComponentState {
	switch {
	case f == DevError || b == DevError:
		return DevError
	case f == DevRunning && b == DevRunning:
		return DevRunning
	case f == DevStarting || b == DevStarting || f == DevRunning || b == DevRunning:
		return DevStarting
	default:
		return DevStopped
	}
}

// DevSupervisorOptions configures NewDevSupervisor.
type DevSupervisorOptions struct {
	// Root is the repo root ("" = Development section unavailable).
	Root string
	// DataDir is the PRODUCTION data dir; the dev backend gets <DataDir>/dev.
	DataDir string
	Runner  ProcessRunner
	Probe   HealthProbe
	Logger  *observability.Logger
}

type devComponent struct {
	state  DevComponentState
	handle ProcessHandle
	// gen invalidates stale watcher goroutines and in-flight starts: Stop
	// bumps it, so a Wait() return from a deliberately killed process can
	// never flip Stopped to Error.
	gen int
}

// DevSupervisor drives the two dev children (vite frontend, go backend)
// through ProcessRunner. Platform-neutral; no syscalls, no os/exec.
type DevSupervisor struct {
	root    string
	dataDir string
	runner  ProcessRunner
	probe   HealthProbe
	log     *observability.Logger

	mu       sync.Mutex
	frontend devComponent
	backend  devComponent

	lifecycleMu sync.Mutex
}

// NewDevSupervisor builds a DevSupervisor, filling defaults.
func NewDevSupervisor(opts DevSupervisorOptions) *DevSupervisor {
	s := &DevSupervisor{
		root:    opts.Root,
		dataDir: opts.DataDir,
		runner:  opts.Runner,
		probe:   opts.Probe,
		log:     opts.Logger,
	}
	if s.log == nil {
		s.log = observability.Default()
	}
	if s.probe == nil {
		s.probe = DefaultHealthProbe
	}
	return s
}

// ResolveDevRoot returns the repo root the Development section controls:
// platform.DevRoot() when set, else the current working directory when it
// holds both go.mod and dashboard/package.json, else "" (unavailable).
func ResolveDevRoot() string {
	if root := platform.DevRoot(); root != "" {
		return root
	}
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	if fileExists(filepath.Join(wd, "go.mod")) && fileExists(filepath.Join(wd, "dashboard", "package.json")) {
		return wd
	}
	return ""
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// Available reports whether a dev repo root was resolved.
func (s *DevSupervisor) Available() bool { return s.root != "" }

// DashboardURL is the dev frontend (vite) URL.
func (s *DevSupervisor) DashboardURL() string { return devFrontendURL }

// Status returns the current snapshot.
func (s *DevSupervisor) Status() DevStatusView {
	s.mu.Lock()
	defer s.mu.Unlock()
	return DevStatusView{
		Overall:  overallDevState(s.frontend.state, s.backend.state),
		Frontend: s.frontend.state,
		Backend:  s.backend.state,
	}
}

// StatusLine renders the menu info line, including the unavailable form.
func (s *DevSupervisor) StatusLine() string {
	if !s.Available() {
		return "Dev Status: unavailable"
	}
	return s.Status().statusLine()
}

func (s *DevSupervisor) frontendSpec() ProcessSpec {
	return ProcessSpec{
		Dir:      filepath.Join(s.root, "dashboard"),
		Name:     "cmd",
		Args:     []string{"/c", "npm", "run", "dev", "--", "--port", "5173", "--strictPort", "--host", "127.0.0.1"},
		ExtraEnv: []string{devAPITarget},
	}
}

func (s *DevSupervisor) backendSpec() ProcessSpec {
	return ProcessSpec{
		Dir:      s.root,
		Name:     "go",
		Args:     []string{"run", "./cmd/venom", "serve", "-bind", devBackendBind},
		ExtraEnv: []string{"VENOM_DATA_DIR=" + filepath.Join(s.dataDir, "dev")},
	}
}

// Start spawns any component that is not already Starting/Running. No-op
// when the dev root is unavailable.
func (s *DevSupervisor) Start() {
	if !s.Available() {
		return
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	s.startComponent(&s.frontend, "frontend", s.frontendSpec())
	s.startComponent(&s.backend, "backend", s.backendSpec())
}

// Stop kills both components and marks them Stopped. Deliberate: the bumped
// generation makes the watcher ignore the kill-induced Wait return.
func (s *DevSupervisor) Stop() {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	s.stopComponent(&s.frontend)
	s.stopComponent(&s.backend)
}

// Restart is Stop then Start.
func (s *DevSupervisor) Restart() {
	s.Stop()
	s.Start()
}

// Refresh promotes Starting components to Running once their health probe
// answers (called from the UI ticker).
func (s *DevSupervisor) Refresh(ctx context.Context) {
	s.refreshComponent(ctx, &s.frontend, devFrontendURL)
	s.refreshComponent(ctx, &s.backend, devBackendHealth)
}

func (s *DevSupervisor) startComponent(c *devComponent, name string, spec ProcessSpec) {
	s.mu.Lock()
	if c.state == DevStarting || c.state == DevRunning {
		s.mu.Unlock()
		return
	}
	c.gen++
	gen := c.gen
	s.mu.Unlock()

	h, err := s.runner.Start(spec)

	s.mu.Lock()
	defer s.mu.Unlock()
	if c.gen != gen {
		// A Stop raced this start; don't leak the late arrival.
		if err == nil {
			_ = h.Kill()
		}
		return
	}
	if err != nil {
		c.state = DevError
		s.log.Error("tray: dev component failed to start",
			observability.String("component", name),
			observability.String("err", err.Error()))
		return
	}
	c.state = DevStarting
	c.handle = h
	go s.watch(c, name, h, gen)
}

// watch turns an unexpected child exit — clean or not — into Error: dev
// servers must not exit on their own, so a self-exit is always a defect the
// owner should see (a deliberate Stop bumps gen first and is ignored here).
func (s *DevSupervisor) watch(c *devComponent, name string, h ProcessHandle, gen int) {
	err := h.Wait()
	s.mu.Lock()
	defer s.mu.Unlock()
	if c.gen != gen {
		return
	}
	c.state = DevError
	c.handle = nil
	detail := "exited"
	if err != nil {
		detail = err.Error()
	}
	s.log.Error("tray: dev component exited unexpectedly",
		observability.String("component", name),
		observability.String("err", detail))
}

func (s *DevSupervisor) stopComponent(c *devComponent) {
	s.mu.Lock()
	c.gen++
	h := c.handle
	c.handle = nil
	c.state = DevStopped
	s.mu.Unlock()
	if h != nil {
		_ = h.Kill()
	}
}

func (s *DevSupervisor) refreshComponent(ctx context.Context, c *devComponent, url string) {
	s.mu.Lock()
	starting := c.state == DevStarting
	s.mu.Unlock()
	if !starting || !s.probe(ctx, url) {
		return
	}
	s.mu.Lock()
	if c.state == DevStarting {
		c.state = DevRunning
	}
	s.mu.Unlock()
}
