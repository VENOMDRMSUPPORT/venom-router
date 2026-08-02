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

// Development-environment constants (design 2026-08-02, one shared database).
// The dev section is a Windows desktop affordance running a single child, the
// vite frontend; it proxies /api to the PRODUCTION backend on 8081, so there
// is no dev backend and no separate database. The frontend spec deliberately
// runs npm through cmd /c. Ports are fixed by the approved design.
const (
	devFrontendURL = "http://127.0.0.1:8088/"
	devAPITarget   = "VENOM_DEV_API_TARGET=http://127.0.0.1:8081"
)

// DevComponentState is the dev child's coarse state.
type DevComponentState int

const (
	DevStopped DevComponentState = iota
	DevStarting
	DevRunning
	DevError
)

// title is the capitalized form ("Stopped"), exactly as the menu renders it.
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
// up — vite answers GET on its probe URL.
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

// DevStatusView is an immutable snapshot for the UI. With a single component,
// Overall simply mirrors Frontend; it is kept as its own field so the menu's
// enablement logic and the status line read consistently.
type DevStatusView struct {
	Overall  DevComponentState
	Frontend DevComponentState
}

// statusLine renders the menu's dev info line, e.g. "Dev Status: Starting".
func (v DevStatusView) statusLine() string {
	return "Dev Status: " + v.Overall.title()
}

// DevSupervisorOptions configures NewDevSupervisor.
type DevSupervisorOptions struct {
	// Root is the repo root ("" = Development section unavailable).
	Root   string
	Runner ProcessRunner
	Probe  HealthProbe
	Logger *observability.Logger
}

type devComponent struct {
	state  DevComponentState
	handle ProcessHandle
	// gen invalidates stale watcher goroutines and in-flight starts: Stop
	// bumps it, so a Wait() return from a deliberately killed process can
	// never flip Stopped to Error.
	gen int
}

// DevSupervisor drives the single dev child (the vite frontend) through
// ProcessRunner. Platform-neutral; no syscalls, no os/exec.
type DevSupervisor struct {
	root   string
	runner ProcessRunner
	probe  HealthProbe
	log    *observability.Logger

	mu       sync.Mutex
	frontend devComponent

	lifecycleMu sync.Mutex
}

// NewDevSupervisor builds a DevSupervisor, filling defaults.
func NewDevSupervisor(opts DevSupervisorOptions) *DevSupervisor {
	s := &DevSupervisor{
		root:   opts.Root,
		runner: opts.Runner,
		probe:  opts.Probe,
		log:    opts.Logger,
	}
	if s.log == nil {
		s.log = observability.Default()
	}
	if s.probe == nil {
		s.probe = DefaultHealthProbe
	}
	return s
}

// ResolveDevRoot returns the repo root the Development section controls.
// Resolution order (see resolveDevRoot): the VENOM_DEV_ROOT override, then
// the first of {cwd, exe dir, parent of exe dir} holding both go.mod and
// dashboard/package.json — the parent covers the shipped layout
// <repo>\dist\venom.exe, so a double-clicked bundle finds its repo without
// any env var or repo cwd. "" means unavailable.
func ResolveDevRoot() string {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = ""
	}
	exeDir := ""
	if exe, err := os.Executable(); err == nil {
		exeDir = filepath.Dir(exe)
	}
	return resolveDevRoot(platform.DevRoot(), cwd, exeDir)
}

// resolveDevRoot is the pure core of ResolveDevRoot. envRoot, when non-empty,
// wins as-is with NO marker check — an explicit override is trusted. The
// remaining candidates are tried in order and must contain BOTH repo markers;
// empty candidates (e.g. exeDir when os.Executable failed) are skipped.
func resolveDevRoot(envRoot, cwd, exeDir string) string {
	if envRoot != "" {
		return envRoot
	}
	candidates := []string{cwd, exeDir}
	if exeDir != "" {
		candidates = append(candidates, filepath.Dir(exeDir))
	}
	for _, dir := range candidates {
		if dir == "" {
			continue
		}
		if fileExists(filepath.Join(dir, "go.mod")) && fileExists(filepath.Join(dir, "dashboard", "package.json")) {
			return dir
		}
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

// Status returns the current snapshot. With one component, Overall mirrors
// the frontend state.
func (s *DevSupervisor) Status() DevStatusView {
	s.mu.Lock()
	defer s.mu.Unlock()
	return DevStatusView{
		Overall:  s.frontend.state,
		Frontend: s.frontend.state,
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
		Args:     []string{"/c", "npm", "run", "dev", "--", "--port", "8088", "--strictPort", "--host", "127.0.0.1"},
		ExtraEnv: []string{devAPITarget},
	}
}

// Start spawns the frontend unless it is already Starting/Running. No-op
// when the dev root is unavailable.
func (s *DevSupervisor) Start() {
	if !s.Available() {
		return
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	s.startComponent(&s.frontend, "frontend", s.frontendSpec())
}

// Stop kills the frontend and marks it Stopped. Deliberate: the bumped
// generation makes the watcher ignore the kill-induced Wait return.
func (s *DevSupervisor) Stop() {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	s.stopComponent(&s.frontend)
}

// Restart is Stop then Start.
func (s *DevSupervisor) Restart() {
	s.Stop()
	s.Start()
}

// Refresh promotes the frontend from Starting to Running once its health
// probe answers (called from the UI ticker).
func (s *DevSupervisor) Refresh(ctx context.Context) {
	s.refreshComponent(ctx, &s.frontend, devFrontendURL)
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

// watch turns an unexpected child exit — clean or not — into Error: the dev
// frontend must not exit on its own, so a self-exit is always a defect the
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
