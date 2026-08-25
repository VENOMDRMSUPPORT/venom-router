package manager

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeHandle struct {
	mu     sync.Mutex
	done   chan struct{}
	killed bool
}

func newFakeHandle() *fakeHandle { return &fakeHandle{done: make(chan struct{})} }

func (h *fakeHandle) Wait() error {
	<-h.done
	return nil
}

func (h *fakeHandle) Kill() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.killed {
		h.killed = true
		close(h.done)
	}
	return nil
}

type fakeRunner struct {
	mu      sync.Mutex
	handles []*fakeHandle
}

func (r *fakeRunner) Start(ProcessSpec) (ProcessHandle, error) {
	h := newFakeHandle()
	r.mu.Lock()
	r.handles = append(r.handles, h)
	r.mu.Unlock()
	return h, nil
}

func (r *fakeRunner) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.handles)
}

func testGroup(id GroupID) GroupDefinition {
	return GroupDefinition{
		ID:          id,
		Product:     ProductRouter,
		Environment: EnvironmentDevelopment,
		Services: []ServiceDefinition{
			{ID: "backend", Name: "Backend", Required: true, Spec: ProcessSpec{Command: "backend"}, Readiness: []ReadinessCheck{func(context.Context) error { return nil }}, StartDeadline: 200 * time.Millisecond},
			{ID: "frontend", Name: "Frontend", Required: true, Spec: ProcessSpec{Command: "frontend"}, Readiness: []ReadinessCheck{func(context.Context) error { return nil }}, StartDeadline: 200 * time.Millisecond},
		},
	}
}

func waitForState(t *testing.T, coordinator *Coordinator, id GroupID, expected State) Snapshot {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		snapshot, err := coordinator.Snapshot(id)
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.State == expected {
			return snapshot
		}
		time.Sleep(time.Millisecond)
	}
	snapshot, _ := coordinator.Snapshot(id)
	t.Fatalf("state = %s, want %s", snapshot.State, expected)
	return Snapshot{}
}

func TestCoordinatorStartsIndependentServicesAndReachesReady(t *testing.T) {
	runner := &fakeRunner{}
	coordinator, err := NewCoordinator([]GroupDefinition{testGroup("router.development")}, runner)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := coordinator.Start("router.development")
	if err != nil {
		t.Fatal(err)
	}
	if operation == "" {
		t.Fatal("Start() returned an empty operation ID")
	}
	snapshot := waitForState(t, coordinator, "router.development", StateReady)
	if runner.count() != 2 {
		t.Fatalf("started %d services, want 2", runner.count())
	}
	if len(snapshot.Services) != 2 {
		t.Fatalf("snapshot has %d services, want 2", len(snapshot.Services))
	}
}

func TestCoordinatorRejectsConcurrentStart(t *testing.T) {
	runner := &fakeRunner{}
	coordinator, err := NewCoordinator([]GroupDefinition{testGroup("router.development")}, runner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Start("router.development"); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Start("router.development"); !errors.Is(err, ErrOperationInProgress) {
		t.Fatalf("second Start() error = %v, want ErrOperationInProgress", err)
	}
}

func TestCoordinatorRejectsActiveConflict(t *testing.T) {
	first := testGroup("router.production")
	second := testGroup("router.development")
	second.Conflicts = []GroupID{first.ID}
	runner := &fakeRunner{}
	coordinator, err := NewCoordinator([]GroupDefinition{first, second}, runner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Start(first.ID); err != nil {
		t.Fatal(err)
	}
	waitForState(t, coordinator, first.ID, StateReady)
	if _, err := coordinator.Start(second.ID); !errors.Is(err, ErrConflictingGroup) {
		t.Fatalf("Start() error = %v, want ErrConflictingGroup", err)
	}
}

func TestCoordinatorStopKillsAllChildren(t *testing.T) {
	runner := &fakeRunner{}
	coordinator, err := NewCoordinator([]GroupDefinition{testGroup("router.development")}, runner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Start("router.development"); err != nil {
		t.Fatal(err)
	}
	waitForState(t, coordinator, "router.development", StateReady)
	if _, err := coordinator.Stop("router.development"); err != nil {
		t.Fatal(err)
	}
	waitForState(t, coordinator, "router.development", StateStopped)
	for _, handle := range runner.handles {
		handle.mu.Lock()
		killed := handle.killed
		handle.mu.Unlock()
		if !killed {
			t.Fatal("a child process was not killed")
		}
	}
}

func TestCoordinatorReadinessFailureEndsInError(t *testing.T) {
	group := testGroup("router.development")
	group.Services[0].Readiness = []ReadinessCheck{func(context.Context) error { return errors.New("not ready") }}
	runner := &fakeRunner{}
	coordinator, err := NewCoordinator([]GroupDefinition{group}, runner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Start(group.ID); err != nil {
		t.Fatal(err)
	}
	waitForState(t, coordinator, group.ID, StateError)
}
