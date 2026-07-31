package tray

import (
	"context"
	"errors"
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

func newTestSupervisor(t *testing.T, runner ProcessRunner, probe HealthProbe) *DevSupervisor {
	t.Helper()
	return NewDevSupervisor(DevSupervisorOptions{
		Root:    filepath.Join("C:", "repo"),
		DataDir: filepath.Join("C:", "data"),
		Runner:  runner,
		Probe:   probe,
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

func TestDevSupervisor_StartSpawnsBothComponentsWithApprovedSpecs(t *testing.T) {
	r := &fakeRunner{}
	s := newTestSupervisor(t, r, probeAlways(false))

	s.Start()

	specs := r.spawned()
	if len(specs) != 2 {
		t.Fatalf("spawned %d processes, want 2 (frontend+backend)", len(specs))
	}
	fe, be := specs[0], specs[1]

	if fe.Dir != filepath.Join("C:", "repo", "dashboard") {
		t.Errorf("frontend dir = %q", fe.Dir)
	}
	if fe.Name != "cmd" {
		t.Errorf("frontend command = %q, want cmd (npm runs through cmd /c on Windows)", fe.Name)
	}
	// --host 127.0.0.1 pins vite to the IPv4 loopback the health probe and
	// dashboard URL use (Node otherwise resolves localhost to ::1 only).
	wantArgs := []string{"/c", "npm", "run", "dev", "--", "--port", "5173", "--strictPort", "--host", "127.0.0.1"}
	if strings.Join(fe.Args, " ") != strings.Join(wantArgs, " ") {
		t.Errorf("frontend args = %v, want %v", fe.Args, wantArgs)
	}
	if len(fe.ExtraEnv) != 1 || fe.ExtraEnv[0] != "VENOM_DEV_API_TARGET=http://127.0.0.1:8082" {
		t.Errorf("frontend env = %v, want the dev API proxy target", fe.ExtraEnv)
	}

	if be.Dir != filepath.Join("C:", "repo") {
		t.Errorf("backend dir = %q", be.Dir)
	}
	if be.Name != "go" {
		t.Errorf("backend command = %q, want go", be.Name)
	}
	wantBE := []string{"run", "./cmd/venom", "serve", "-bind", "127.0.0.1:8082"}
	if strings.Join(be.Args, " ") != strings.Join(wantBE, " ") {
		t.Errorf("backend args = %v, want %v", be.Args, wantBE)
	}
	wantEnv := "VENOM_DATA_DIR=" + filepath.Join("C:", "data", "dev")
	if len(be.ExtraEnv) != 1 || be.ExtraEnv[0] != wantEnv {
		t.Errorf("backend env = %v, want [%q] (isolated lock/DB)", be.ExtraEnv, wantEnv)
	}

	v := s.Status()
	if v.Frontend != DevStarting || v.Backend != DevStarting || v.Overall != DevStarting {
		t.Errorf("after Start: %+v, want all Starting", v)
	}
}

func TestDevSupervisor_RefreshPromotesStartingToRunning(t *testing.T) {
	r := &fakeRunner{}
	s := newTestSupervisor(t, r, probeAlways(true))
	s.Start()

	s.Refresh(context.Background())

	v := s.Status()
	if v.Frontend != DevRunning || v.Backend != DevRunning || v.Overall != DevRunning {
		t.Errorf("after healthy Refresh: %+v, want all Running", v)
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

func TestDevSupervisor_SpawnFailureMarksOnlyThatComponentError(t *testing.T) {
	r := &fakeRunner{failFor: map[string]error{"go": errors.New("no toolchain")}}
	s := newTestSupervisor(t, r, probeAlways(false))

	s.Start()

	v := s.Status()
	if v.Backend != DevError {
		t.Errorf("backend = %v, want Error on spawn failure", v.Backend)
	}
	if v.Frontend != DevStarting {
		t.Errorf("frontend = %v, want Starting (unaffected)", v.Frontend)
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

func TestDevSupervisor_StopKillsBothAndStaysStopped(t *testing.T) {
	r := &fakeRunner{}
	s := newTestSupervisor(t, r, probeAlways(true))
	s.Start()
	s.Refresh(context.Background())

	s.Stop()

	if !r.handle(0).killed || !r.handle(1).killed {
		t.Fatal("Stop did not kill both process handles")
	}
	v := s.Status()
	if v.Frontend != DevStopped || v.Backend != DevStopped || v.Overall != DevStopped {
		t.Errorf("after Stop: %+v, want all Stopped", v)
	}
	// The kill-induced Wait return must NOT flip Stopped to Error (generation
	// guard) — give the watcher goroutines a moment to run.
	time.Sleep(50 * time.Millisecond)
	if v := s.Status(); v.Frontend != DevStopped || v.Backend != DevStopped {
		t.Errorf("watcher clobbered deliberate Stop: %+v", v)
	}
}

// TestDevSupervisor_RefreshNeverResurrectsStoppedOrErrored pins the Starting
// gate in refreshComponent: a foreign process answering on a dev port (for
// example another repo's vite already sitting on 5173) must not make a
// component this supervisor did NOT start — or one that crashed — report
// Running. Only Starting may be promoted by a healthy probe.
func TestDevSupervisor_RefreshNeverResurrectsStoppedOrErrored(t *testing.T) {
	r := &fakeRunner{}
	s := newTestSupervisor(t, r, probeAlways(true))

	// Never started: everything Stopped while the probe answers.
	s.Refresh(context.Background())
	if v := s.Status(); v.Frontend != DevStopped || v.Backend != DevStopped {
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
	if v := s.Status(); v.Frontend != DevStopped || v.Backend != DevStopped {
		t.Fatalf("Refresh resurrected a stopped component: %+v", v)
	}
}

func TestDevSupervisor_RestartAfterErrorSpawnsFreshProcesses(t *testing.T) {
	r := &fakeRunner{}
	s := newTestSupervisor(t, r, probeAlways(false))
	s.Start()
	r.handle(1).exit(errors.New("crash"))
	eventually(t, func() bool { return s.Status().Backend == DevError },
		"backend never reached Error")

	s.Restart()

	if got := len(r.spawned()); got != 4 {
		t.Fatalf("total spawns = %d, want 4 (2 initial + 2 restart)", got)
	}
	if v := s.Status(); v.Overall != DevStarting {
		t.Errorf("after Restart: %+v, want Starting", v)
	}
}

func TestDevSupervisor_UnavailableWhenNoRoot(t *testing.T) {
	r := &fakeRunner{}
	s := NewDevSupervisor(DevSupervisorOptions{Root: "", DataDir: "x", Runner: r, Probe: probeAlways(true)})

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
		f, b DevComponentState
		want string
	}{
		{DevStopped, DevStopped, "Dev Status: Stopped · Frontend stopped · Backend stopped"},
		{DevStarting, DevRunning, "Dev Status: Starting · Frontend starting · Backend running"},
		{DevRunning, DevRunning, "Dev Status: Running · Frontend running · Backend running"},
		{DevError, DevRunning, "Dev Status: Error · Frontend error · Backend running"},
	}
	for _, tc := range cases {
		v := DevStatusView{Overall: overallDevState(tc.f, tc.b), Frontend: tc.f, Backend: tc.b}
		if got := v.statusLine(); got != tc.want {
			t.Errorf("statusLine(%v,%v) = %q, want %q", tc.f, tc.b, got, tc.want)
		}
	}
}

func TestOverallDevState(t *testing.T) {
	cases := []struct {
		f, b, want DevComponentState
	}{
		{DevStopped, DevStopped, DevStopped},
		{DevStarting, DevStopped, DevStarting},
		{DevStarting, DevStarting, DevStarting},
		{DevRunning, DevStarting, DevStarting},
		{DevRunning, DevRunning, DevRunning},
		{DevRunning, DevStopped, DevStarting},
		{DevError, DevRunning, DevError},
		{DevError, DevStarting, DevError},
	}
	for _, tc := range cases {
		if got := overallDevState(tc.f, tc.b); got != tc.want {
			t.Errorf("overallDevState(%v,%v) = %v, want %v", tc.f, tc.b, got, tc.want)
		}
	}
}
