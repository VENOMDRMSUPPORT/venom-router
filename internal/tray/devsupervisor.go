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
// The dev section is a Windows desktop affordance running a watched source
// backend on 8081 and the Vite frontend on 8088. Vite proxies /api to that
// backend, which keeps using the canonical production data directory rather
// than a separate development database. The dependency-free Node bootstrap
// validates/repairs dashboard dependencies before it starts npm/vite.
// Ports are fixed by the approved design.
const (
	devFrontendURL = "http://127.0.0.1:8088/"
	devBackendURL  = "http://127.0.0.1:8081/health"
	devAPITarget   = "VENOM_DEV_API_TARGET=http://127.0.0.1:8081"
)

// devBackendStopTimeout bounds how long Stop waits for the backend watcher's
// process tree to fully exit before proceeding anyway. The wait exists so the
// single-instance lock + WAL are released and quiescent BEFORE production
// re-boots (no two writers on the one DB — the actual cause of the prior
// corruption incident). WAL crash-safety means proceeding after the timeout is
// still non-corrupting; the timeout only prevents a wedged child from hanging
// the UI forever.
const devBackendStopTimeout = 10 * time.Second

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
	// OutputPath, when non-empty, receives append-only child stdout/stderr.
	OutputPath string
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

// DevStatusView is an immutable snapshot for the UI. Overall is the combined
// state of the two dev children (see combineDevState); Backend and Frontend
// are exposed individually so the menu's enablement logic (Open follows the
// frontend/vite specifically) and any per-child diagnostics read consistently.
type DevStatusView struct {
	Overall        DevComponentState
	Backend        DevComponentState
	Frontend       DevComponentState
	BackendDetail  string
	FrontendDetail string
	LogPath        string
}

// combineDevState reduces the two child states to the section's Overall, worst
// first: any Error dominates, then any Starting, then any Running; only when
// both children are Stopped is the section Stopped. This keeps "active"
// (anything not Stopped) true whenever either child is live, so the menu never
// offers Start while one child is still running.
func combineDevState(a, b DevComponentState) DevComponentState {
	switch {
	case a == DevError || b == DevError:
		return DevError
	case a == DevStarting || b == DevStarting:
		return DevStarting
	case a == DevRunning || b == DevRunning:
		return DevRunning
	default:
		return DevStopped
	}
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
	// LogPath receives append-only stdout/stderr from both dev children.
	LogPath string
}

type devComponent struct {
	state  DevComponentState
	handle ProcessHandle
	detail string
	// gen invalidates stale watcher goroutines and in-flight starts: Stop
	// bumps it, so a Wait() return from a deliberately killed process can
	// never flip Stopped to Error.
	gen int
	// exited is closed by the current watcher when the child's process has
	// fully terminated (Wait returned). Captured per start so a restart's new
	// channel never aliases a prior generation's. Stop reads it to block until
	// the child is truly gone — the backend must be dead (lock freed, DB
	// quiescent) before production re-boots.
	exited chan struct{}
}

// DevSupervisor drives the single dev child (the vite frontend) through
// ProcessRunner. Platform-neutral; no syscalls, no os/exec.
type DevSupervisor struct {
	root    string
	runner  ProcessRunner
	probe   HealthProbe
	log     *observability.Logger
	logPath string

	mu       sync.Mutex
	frontend devComponent
	backend  devComponent

	// backendStopTimeout bounds Stop's wait for the backend to fully exit.
	// Defaults to devBackendStopTimeout; tests set it small.
	backendStopTimeout time.Duration

	lifecycleMu sync.Mutex
}

// NewDevSupervisor builds a DevSupervisor, filling defaults.
func NewDevSupervisor(opts DevSupervisorOptions) *DevSupervisor {
	s := &DevSupervisor{
		root:    opts.Root,
		runner:  opts.Runner,
		probe:   opts.Probe,
		log:     opts.Logger,
		logPath: opts.LogPath,
	}
	if s.log == nil {
		s.log = observability.Default()
	}
	if s.probe == nil {
		s.probe = DefaultHealthProbe
	}
	if s.backendStopTimeout <= 0 {
		s.backendStopTimeout = devBackendStopTimeout
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

// LogPath is the dedicated append-only development child log.
func (s *DevSupervisor) LogPath() string { return s.logPath }

// Status returns one consistent snapshot of both development components.
func (s *DevSupervisor) Status() DevStatusView {
	s.mu.Lock()
	defer s.mu.Unlock()
	return DevStatusView{
		Overall:        combineDevState(s.backend.state, s.frontend.state),
		Backend:        s.backend.state,
		Frontend:       s.frontend.state,
		BackendDetail:  s.backend.detail,
		FrontendDetail: s.frontend.detail,
		LogPath:        s.logPath,
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
		Dir:        filepath.Join(s.root, "dashboard"),
		Name:       "node",
		Args:       []string{"scripts/dev-bootstrap.mjs", "--port", "8088", "--strictPort", "--host", "127.0.0.1"},
		ExtraEnv:   []string{devAPITarget},
		OutputPath: s.logPath,
	}
}

// backendSpec runs the WATCHED source backend: air rebuilds and restarts
// `venom serve` on 127.0.0.1:8081 on any Go change (see .air.toml), so dev
// edits reload without ever rebuilding the shipped dist\venom.exe. It runs
// from the repo root and carries NO VENOM_DATA_DIR, so it uses the ONE
// canonical database — the same one production uses. `go` is a real
// executable, so it is exec'd directly (unlike npm, which is a .cmd shim
// needing cmd /c).
func (s *DevSupervisor) backendSpec() ProcessSpec {
	return ProcessSpec{
		Dir:        s.root,
		Name:       "go",
		Args:       []string{"run", "github.com/air-verse/air@latest", "-c", ".air.toml"},
		OutputPath: s.logPath,
	}
}

// Start spawns the frontend then the backend watcher, each unless it is
// already Starting/Running. No-op when the dev root is unavailable. The
// caller (EnterDevMode) must have stopped production first: both the backend
// watcher and production bind 8081 and take the single-instance lock on the
// one DB, so only one may run at a time.
func (s *DevSupervisor) Start() {
	if !s.Available() {
		return
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	s.startComponent(&s.frontend, "frontend", s.frontendSpec())
	s.startComponent(&s.backend, "backend", s.backendSpec())
}

// Stop kills both dev children and marks them Stopped. The frontend (vite,
// no database) is killed without waiting; the backend holds the one DB, so
// Stop BLOCKS until its process tree has fully exited (bounded by
// devBackendStopTimeout) — the lock must be free and the DB quiescent before
// production re-boots. Deliberate: the bumped generation makes each watcher
// ignore the kill-induced Wait return.
func (s *DevSupervisor) Stop() {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	s.stopComponent(&s.frontend, false)
	s.stopComponent(&s.backend, true)
}

// Restart is Stop then Start.
func (s *DevSupervisor) Restart() {
	s.Stop()
	s.Start()
}

// Refresh promotes each child from Starting to Running once its health probe
// answers (called from the UI ticker): the frontend on the vite port, the
// backend on 8081.
func (s *DevSupervisor) Refresh(ctx context.Context) {
	s.refreshComponent(ctx, &s.frontend, devFrontendURL)
	s.refreshComponent(ctx, &s.backend, devBackendURL)
}

func (s *DevSupervisor) startComponent(c *devComponent, name string, spec ProcessSpec) {
	s.mu.Lock()
	if c.state == DevStarting || c.state == DevRunning {
		s.mu.Unlock()
		return
	}
	c.gen++
	c.detail = ""
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
		c.detail = err.Error()
		s.log.Error("tray: dev component failed to start",
			observability.String("component", name),
			observability.String("err", err.Error()))
		return
	}
	c.state = DevStarting
	c.handle = h
	c.detail = ""
	exited := make(chan struct{})
	c.exited = exited
	go s.watch(c, name, h, gen, exited)
}

// watch turns an unexpected child exit — clean or not — into Error: the dev
// frontend must not exit on its own, so a self-exit is always a defect the
// owner should see (a deliberate Stop bumps gen first and is ignored here).
func (s *DevSupervisor) watch(c *devComponent, name string, h ProcessHandle, gen int, exited chan struct{}) {
	err := h.Wait()
	// Signal full process death FIRST, unconditionally: a deliberate Stop
	// (which bumped gen) blocks on this channel and must be released whether
	// or not this watcher is still the current generation.
	close(exited)
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
	c.detail = detail
	s.log.Error("tray: dev component exited unexpectedly",
		observability.String("component", name),
		observability.String("err", detail))
}

// stopComponent kills the child and marks it Stopped. When wait is true it
// then blocks until the watcher confirms the process tree has fully exited
// (bounded by devBackendStopTimeout) — used for the backend so the DB lock is
// released before anything re-opens the database.
func (s *DevSupervisor) stopComponent(c *devComponent, wait bool) {
	s.mu.Lock()
	c.gen++
	h := c.handle
	exited := c.exited
	c.handle = nil
	c.state = DevStopped
	c.detail = ""
	s.mu.Unlock()
	if h == nil {
		return
	}
	_ = h.Kill()
	if wait && exited != nil {
		select {
		case <-exited:
		case <-time.After(s.backendStopTimeout):
			s.log.Error("tray: dev backend did not exit within timeout; proceeding (WAL is crash-safe)")
		}
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
