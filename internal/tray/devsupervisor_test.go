package tray

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeHandle is a controllable ProcessHandle: Wait blocks until exit is
// signalled (by the test or by Kill).
type fakeHandle struct {
	exited   chan error
	killOnce sync.Once
	killed   bool
}

func newFakeHandle() *fakeHandle { return &fakeHandle{exited: make(chan error, 1)} }

func (h *fakeHandle) Wait() error { return <-h.exited }

func (h *fakeHandle) Kill() error {
	h.killOnce.Do(func() {
		h.killed = true
		h.exited <- errors.New("killed")
	})
	return nil
}

// exit simulates the child dying on its own.
func (h *fakeHandle) exit(err error) { h.exited <- err }

type fakeRunner struct {
	mu      sync.Mutex
	specs   []ProcessSpec
	handles []*fakeHandle
	// failFor makes Start fail for specs whose Name matches.
	failFor map[string]error
}

func (r *fakeRunner) Start(spec ProcessSpec) (ProcessHandle, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err, ok := r.failFor[spec.Name]; ok {
		return nil, err
	}
	h := newFakeHandle()
	r.specs = append(r.specs, spec)
	r.handles = append(r.handles, h)
	return h, nil
}

func (r *fakeRunner) spawned() []ProcessSpec {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]ProcessSpec(nil), r.specs...)
}

func (r *fakeRunner) handle(i int) *fakeHandle {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.handles[i]
}

func probeAlways(ok bool) HealthProbe {
	return func(context.Context, string) bool { return ok }
}

// blockingHandle is a ProcessHandle whose Wait blocks until releaseExit is
// called — Kill does NOT release Wait (it only records the kill). This models
// a process that has been asked to die but has not finished exiting yet, so a
// test can assert Stop blocks until the process is truly gone.
type blockingHandle struct {
	wait     chan error
	killed   chan struct{}
	killOnce sync.Once
}

func newBlockingHandle() *blockingHandle {
	return &blockingHandle{wait: make(chan error, 1), killed: make(chan struct{})}
}

func (h *blockingHandle) Wait() error { return <-h.wait }

func (h *blockingHandle) Kill() error {
	h.killOnce.Do(func() { close(h.killed) })
	return nil
}

// releaseExit simulates the process finally terminating (Wait returns).
func (h *blockingHandle) releaseExit() { h.wait <- nil }

// mixedRunner hands the backend (spec.Name == "go") a controllable
// blockingHandle and every other child a normal fakeHandle, so a test can
// drive the backend's exit timing precisely.
type mixedRunner struct {
	mu      sync.Mutex
	specs   []ProcessSpec
	backend *blockingHandle
}

func (r *mixedRunner) Start(spec ProcessSpec) (ProcessHandle, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.specs = append(r.specs, spec)
	if spec.Name == "go" {
		r.backend = newBlockingHandle()
		return r.backend, nil
	}
	return newFakeHandle(), nil
}

// TestDevSupervisor_StopWaitsForBackendExit pins the WAL-safe teardown: Stop
// must not return until the backend's process tree has fully exited (Wait
// returned → OS lock freed, DB quiescent). Production only re-boots after
// Stop returns, so this wait is what prevents two writers on the one DB.
func TestDevSupervisor_StopWaitsForBackendExit(t *testing.T) {
	r := &mixedRunner{}
	s := newTestSupervisor(t, r, probeAlways(false))
	s.Start()

	stopped := make(chan struct{})
	go func() { s.Stop(); close(stopped) }()

	// The backend process is still alive (releaseExit not called): Stop must
	// still be blocking.
	select {
	case <-stopped:
		t.Fatal("Stop returned before the backend process exited")
	case <-time.After(150 * time.Millisecond):
	}

	r.backend.releaseExit() // the process finally dies

	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return after the backend exited")
	}
}

// TestDevSupervisor_StopBackstopUnblocksOnTimeout pins the backstop: a backend
// that never finishes exiting must not wedge the UI forever — Stop falls
// through after backendStopTimeout (safe because WAL is crash-safe). The kill
// is still issued.
func TestDevSupervisor_StopBackstopUnblocksOnTimeout(t *testing.T) {
	r := &mixedRunner{}
	s := newTestSupervisor(t, r, probeAlways(false))
	s.backendStopTimeout = 100 * time.Millisecond
	s.Start()

	stopped := make(chan struct{})
	go func() { s.Stop(); close(stopped) }()

	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not fall through the backstop timeout")
	}
	select {
	case <-r.backend.killed:
	default:
		t.Fatal("backend was not killed")
	}
}

func newTestSupervisor(t *testing.T, runner ProcessRunner, probe HealthProbe) *DevSupervisor {
	t.Helper()
	return NewDevSupervisor(DevSupervisorOptions{
		Root:   filepath.Join("C:", "repo"),
		Runner: runner,
		Probe:  probe,
	})
}

// eventually polls cond for up to 2s (the watch goroutine is asynchronous).
func eventually(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(msg)
}

// writeDevRootMarkers stamps dir with both repo markers the resolver checks:
// go.mod and dashboard/package.json.
func writeDevRootMarkers(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "dashboard"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{filepath.Join(dir, "go.mod"), filepath.Join(dir, "dashboard", "package.json")} {
		if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// TestResolveDevRoot_Pure pins the resolution order of the pure core:
// env override (as-is, no marker check) > cwd > exeDir > parent(exeDir),
// where the last three must contain BOTH go.mod and dashboard/package.json.
// parent(exeDir) is the shipped layout: <repo>\dist\venom.exe double-clicked.
func TestResolveDevRoot_Pure(t *testing.T) {
	repo := t.TempDir()
	writeDevRootMarkers(t, repo)
	dist := filepath.Join(repo, "dist") // exeDir in the shipped layout; no markers itself
	if err := os.MkdirAll(dist, 0o700); err != nil {
		t.Fatal(err)
	}
	noRepo := t.TempDir() // valid directory, no markers

	cases := []struct {
		name                 string
		envRoot, cwd, exeDir string
		want                 string
	}{
		{"env wins even over a valid cwd, no marker check", filepath.Join("C:", "explicit"), repo, dist, filepath.Join("C:", "explicit")},
		{"cwd wins when it has both markers", "", repo, dist, repo},
		{"exeDir used when cwd lacks markers", "", noRepo, repo, repo},
		{"parent of exeDir covers repo\\dist\\venom.exe", "", noRepo, dist, repo},
		{"empty when nothing qualifies", "", noRepo, filepath.Join(noRepo, "dist"), ""},
		{"empty candidates are skipped", "", "", "", ""},
	}
	for _, tc := range cases {
		if got := resolveDevRoot(tc.envRoot, tc.cwd, tc.exeDir); got != tc.want {
			t.Errorf("%s: resolveDevRoot(%q, %q, %q) = %q, want %q",
				tc.name, tc.envRoot, tc.cwd, tc.exeDir, got, tc.want)
		}
	}
}

// TestDevSupervisor_StartSpawnsFrontendWithApprovedSpec pins the single dev
// child: the vite frontend, whose /api proxy now points at the PRODUCTION
// backend on 8081 (one shared database — no dev backend, no port 8082).
func TestDevSupervisor_StartSpawnsFrontendWithApprovedSpec(t *testing.T) {
	r := &fakeRunner{}
	s := newTestSupervisor(t, r, probeAlways(false))

	s.Start()

	specs := r.spawned()
	// Two children now: the frontend is spawned first (index 0), the backend
	// watcher second (asserted by TestDevSupervisor_StartSpawnsBackendWatcher).
	if len(specs) != 2 {
		t.Fatalf("spawned %d processes, want 2 (frontend + backend)", len(specs))
	}
	fe := specs[0]

	if fe.Dir != filepath.Join("C:", "repo", "dashboard") {
		t.Errorf("frontend dir = %q", fe.Dir)
	}
	if fe.Name != "node" {
		t.Errorf("frontend command = %q, want node (the dependency-free managed bootstrap)", fe.Name)
	}
	// --host 127.0.0.1 pins vite to the IPv4 loopback the health probe and
	// dashboard URL use (Node otherwise resolves localhost to ::1 only).
	wantArgs := []string{"scripts/dev-bootstrap.mjs", "--port", "8088", "--strictPort", "--host", "127.0.0.1"}
	if strings.Join(fe.Args, " ") != strings.Join(wantArgs, " ") {
		t.Errorf("frontend args = %v, want %v", fe.Args, wantArgs)
	}
	// The proxy target is production (8081): dev shares the one database.
	if len(fe.ExtraEnv) != 1 || fe.ExtraEnv[0] != "VENOM_DEV_API_TARGET=http://127.0.0.1:8081" {
		t.Errorf("frontend env = %v, want the production API proxy target", fe.ExtraEnv)
	}

	v := s.Status()
	if v.Frontend != DevStarting || v.Overall != DevStarting {
		t.Errorf("after Start: %+v, want Starting", v)
	}
}

// TestDevSupervisor_StartSpawnsBackendWatcher pins the second dev child added
// for the tray-native live-reload backend: a WATCHED source backend
// (`go run air -c .air.toml`) launched from the repo root against the ONE
// canonical database. It carries NO VENOM_DATA_DIR — dev shares the single DB
// with production — and runs `go` directly (a real executable, unlike npm
// which needs cmd /c). Frontend is spawned first, backend second.
func TestDevSupervisor_StartSpawnsBackendWatcher(t *testing.T) {
	r := &fakeRunner{}
	s := newTestSupervisor(t, r, probeAlways(false))

	s.Start()

	specs := r.spawned()
	if len(specs) != 2 {
		t.Fatalf("spawned %d processes, want 2 (frontend + backend watcher)", len(specs))
	}
	be := specs[1]

	if be.Dir != filepath.Join("C:", "repo") {
		t.Errorf("backend dir = %q, want the repo root", be.Dir)
	}
	// `go` is go.exe (a real binary): exec it directly, no cmd /c wrapper.
	if be.Name != "go" {
		t.Errorf("backend command = %q, want go", be.Name)
	}
	wantArgs := []string{"run", "github.com/air-verse/air@latest", "-c", ".air.toml"}
	if strings.Join(be.Args, " ") != strings.Join(wantArgs, " ") {
		t.Errorf("backend args = %v, want %v", be.Args, wantArgs)
	}
	// Crucial safety invariant: the backend watcher runs against the ONE
	// canonical database. NO VENOM_DATA_DIR override may leak in — a second
	// database is exactly the split-brain the one-DB design forbids.
	for _, e := range be.ExtraEnv {
		if strings.HasPrefix(e, "VENOM_DATA_DIR=") {
			t.Errorf("backend must not set VENOM_DATA_DIR (one shared DB), got %q", e)
		}
	}

	if v := s.Status(); v.Backend != DevStarting {
		t.Errorf("after Start: backend = %v, want Starting", v.Backend)
	}
}

func TestDevSupervisor_RefreshPromotesStartingToRunning(t *testing.T) {
	r := &fakeRunner{}
	s := newTestSupervisor(t, r, probeAlways(true))
	s.Start()

	s.Refresh(context.Background())

	v := s.Status()
	if v.Frontend != DevRunning || v.Overall != DevRunning {
		t.Errorf("after healthy Refresh: %+v, want Running", v)
	}
}

// TestDevSupervisor_RefreshPromotesBackendOnItsOwnURL pins that each child is
// probed on its OWN URL: a probe answering only the backend's 8081 health URL
// promotes the backend to Running while the frontend (probed on 8088) stays
// Starting. Overall stays Starting because Starting dominates Running.
func TestDevSupervisor_RefreshPromotesBackendOnItsOwnURL(t *testing.T) {
	r := &fakeRunner{}
	probe := func(_ context.Context, url string) bool { return url == devBackendURL }
	s := NewDevSupervisor(DevSupervisorOptions{
		Root:   filepath.Join("C:", "repo"),
		Runner: r,
		Probe:  probe,
	})
	s.Start()

	s.Refresh(context.Background())

	v := s.Status()
	if v.Backend != DevRunning {
		t.Errorf("backend = %v, want Running (its 8081 probe answered)", v.Backend)
	}
	if v.Frontend != DevStarting {
		t.Errorf("frontend = %v, want Starting (its 8088 probe did not answer)", v.Frontend)
	}
	if v.Overall != DevStarting {
		t.Errorf("overall = %v, want Starting (Starting dominates Running)", v.Overall)
	}
}

func TestDevSupervisor_RefreshLeavesStartingWhileUnhealthy(t *testing.T) {
	r := &fakeRunner{}
	s := newTestSupervisor(t, r, probeAlways(false))
	s.Start()

	s.Refresh(context.Background())

	if v := s.Status(); v.Overall != DevStarting {
		t.Errorf("after unhealthy Refresh: %+v, want Starting", v)
	}
}

// TestDevSupervisor_SpawnFailureMarksError pins that a failed spawn of the
// frontend surfaces as Error (for example node missing on PATH).
func TestDevSupervisor_SpawnFailureMarksError(t *testing.T) {
	r := &fakeRunner{failFor: map[string]error{"node": errors.New("no node")}}
	s := newTestSupervisor(t, r, probeAlways(false))

	s.Start()

	v := s.Status()
	if v.Frontend != DevError {
		t.Errorf("frontend = %v, want Error on spawn failure", v.Frontend)
	}
	if v.Overall != DevError {
		t.Errorf("overall = %v, want Error", v.Overall)
	}
}

func TestDevSupervisor_UnexpectedExitMarksError(t *testing.T) {
	r := &fakeRunner{}
	s := newTestSupervisor(t, r, probeAlways(false))
	s.Start()

	r.handle(0).exit(nil) // frontend dies on its own — even a clean exit is unexpected

	eventually(t, func() bool { return s.Status().Frontend == DevError },
		"frontend never reached Error after its process exited")
}

func TestDevSupervisor_UnexpectedFrontendExitRetainsDetail(t *testing.T) {
	r := &fakeRunner{}
	s := newTestSupervisor(t, r, probeAlways(false))
	s.Start()

	r.handle(0).exit(errors.New("exit status 1: Vite executable is still missing"))
	eventually(t, func() bool { return s.Status().Frontend == DevError },
		"frontend never reached Error")

	if got := s.Status().FrontendDetail; !strings.Contains(got, "Vite executable") {
		t.Fatalf("frontend detail = %q, want actionable child failure", got)
	}
}

func TestDevSupervisor_StopKillsFrontendAndStaysStopped(t *testing.T) {
	r := &fakeRunner{}
	s := newTestSupervisor(t, r, probeAlways(true))
	s.Start()
	s.Refresh(context.Background())

	s.Stop()

	if !r.handle(0).killed {
		t.Fatal("Stop did not kill the process handle")
	}
	v := s.Status()
	if v.Frontend != DevStopped || v.Overall != DevStopped {
		t.Errorf("after Stop: %+v, want Stopped", v)
	}
	// The kill-induced Wait return must NOT flip Stopped to Error (generation
	// guard) — give the watcher goroutine a moment to run.
	time.Sleep(50 * time.Millisecond)
	if v := s.Status(); v.Frontend != DevStopped {
		t.Errorf("watcher clobbered deliberate Stop: %+v", v)
	}
}

// TestDevSupervisor_RefreshNeverResurrectsStoppedOrErrored pins the Starting
// gate in refreshComponent: a foreign process answering on the dev port (for
// example another repo's vite already sitting on 8088) must not make a
// component this supervisor did NOT start — or one that crashed — report
// Running. Only Starting may be promoted by a healthy probe.
func TestDevSupervisor_RefreshNeverResurrectsStoppedOrErrored(t *testing.T) {
	r := &fakeRunner{}
	s := newTestSupervisor(t, r, probeAlways(true))

	// Never started: Stopped while the probe answers.
	s.Refresh(context.Background())
	if v := s.Status(); v.Frontend != DevStopped {
		t.Fatalf("Refresh resurrected a never-started component: %+v", v)
	}

	// Crashed while starting: Error must survive a healthy probe.
	s.Start()
	r.handle(0).exit(errors.New("crash"))
	eventually(t, func() bool { return s.Status().Frontend == DevError },
		"frontend never reached Error after its process exited")
	s.Refresh(context.Background())
	if got := s.Status().Frontend; got != DevError {
		t.Fatalf("Refresh flipped a crashed component from Error to %v", got)
	}

	// Deliberately stopped: Stopped must survive a healthy probe.
	s.Stop()
	s.Refresh(context.Background())
	if v := s.Status(); v.Frontend != DevStopped {
		t.Fatalf("Refresh resurrected a stopped component: %+v", v)
	}
}

func TestDevSupervisor_RestartAfterErrorSpawnsFreshProcess(t *testing.T) {
	r := &fakeRunner{}
	s := newTestSupervisor(t, r, probeAlways(false))
	s.Start()
	r.handle(0).exit(errors.New("crash")) // frontend (index 0) crashes
	eventually(t, func() bool { return s.Status().Frontend == DevError },
		"frontend never reached Error")

	s.Restart()

	// Restart recycles BOTH children (Stop both, Start both): 2 initial spawns
	// + 2 restart spawns = 4 total.
	if got := len(r.spawned()); got != 4 {
		t.Fatalf("total spawns = %d, want 4 (2 initial + 2 restart)", got)
	}
	if v := s.Status(); v.Overall != DevStarting {
		t.Errorf("after Restart: %+v, want Starting", v)
	}
}

func TestDevSupervisor_UnavailableWhenNoRoot(t *testing.T) {
	r := &fakeRunner{}
	s := NewDevSupervisor(DevSupervisorOptions{Root: "", Runner: r, Probe: probeAlways(true)})

	if s.Available() {
		t.Fatal("Available() = true with no root")
	}
	s.Start()
	if len(r.spawned()) != 0 {
		t.Fatal("Start spawned processes despite no dev root")
	}
	if got := s.StatusLine(); got != "Dev Status: unavailable" {
		t.Errorf("StatusLine() = %q", got)
	}
}

func TestDevSupervisor_StatusLineFormats(t *testing.T) {
	cases := []struct {
		state DevComponentState
		want  string
	}{
		{DevStopped, "Dev Status: Stopped"},
		{DevStarting, "Dev Status: Starting"},
		{DevRunning, "Dev Status: Running"},
		{DevError, "Dev Status: Error"},
	}
	for _, tc := range cases {
		v := DevStatusView{Overall: tc.state, Frontend: tc.state}
		if got := v.statusLine(); got != tc.want {
			t.Errorf("statusLine(%v) = %q, want %q", tc.state, got, tc.want)
		}
	}
}
