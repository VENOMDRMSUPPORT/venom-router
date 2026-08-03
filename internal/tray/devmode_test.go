package tray

import (
	"context"
	"reflect"
	"sync"
	"testing"
)

// recorder captures the ordered sequence of lifecycle calls the orchestration
// makes across the prod and dev fakes.
type recorder struct {
	mu     sync.Mutex
	events []string
}

func (r *recorder) add(e string) {
	r.mu.Lock()
	r.events = append(r.events, e)
	r.mu.Unlock()
}

func (r *recorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.events...)
}

// fakeProd records prod lifecycle calls. stopLeaves is the coarse state Stop
// settles into (StateStopped for a clean shutdown; anything else models a
// dirty/timed-out shutdown that may still hold the lock).
type fakeProd struct {
	rec        *recorder
	state      State
	stopLeaves State
}

func (p *fakeProd) Stop() {
	p.rec.add("prod.Stop")
	p.state = p.stopLeaves
}

func (p *fakeProd) Start(context.Context) {
	p.rec.add("prod.Start")
	p.state = StateRunning
}

func (p *fakeProd) Status() StatusView { return StatusView{State: p.state} }

// fakeDev records dev children calls.
type fakeDev struct {
	rec       *recorder
	available bool
}

func (d *fakeDev) Available() bool { return d.available }
func (d *fakeDev) Start()          { d.rec.add("dev.Start") }
func (d *fakeDev) Stop()           { d.rec.add("dev.Stop") }

// TestEnterDevMode_StopsProdThenStartsDev pins the order: production must be
// fully stopped (8081 + lock freed) BEFORE the dev backend watcher starts —
// only one process may hold the one DB at a time.
func TestEnterDevMode_StopsProdThenStartsDev(t *testing.T) {
	rec := &recorder{}
	prod := &fakeProd{rec: rec, state: StateRunning, stopLeaves: StateStopped}
	dev := &fakeDev{rec: rec, available: true}

	EnterDevMode(context.Background(), prod, dev)

	want := []string{"prod.Stop", "dev.Start"}
	if got := rec.snapshot(); !reflect.DeepEqual(got, want) {
		t.Errorf("event order = %v, want %v", got, want)
	}
}

// TestEnterDevMode_SkipsDevWhenProdNotStopped pins the safety guard: if
// production's shutdown did not come out clean (lock possibly still held), the
// dev children must NOT start — running the backend watcher against a DB
// another process may still hold is the two-writer hazard.
func TestEnterDevMode_SkipsDevWhenProdNotStopped(t *testing.T) {
	rec := &recorder{}
	prod := &fakeProd{rec: rec, state: StateRunning, stopLeaves: StateError}
	dev := &fakeDev{rec: rec, available: true}

	EnterDevMode(context.Background(), prod, dev)

	want := []string{"prod.Stop"}
	if got := rec.snapshot(); !reflect.DeepEqual(got, want) {
		t.Errorf("event order = %v, want %v (dev must not start after a dirty prod stop)", got, want)
	}
}

// TestEnterDevMode_NoopWhenDevUnavailable pins that with no dev repo root,
// EnterDevMode touches nothing — production keeps running.
func TestEnterDevMode_NoopWhenDevUnavailable(t *testing.T) {
	rec := &recorder{}
	prod := &fakeProd{rec: rec, state: StateRunning, stopLeaves: StateStopped}
	dev := &fakeDev{rec: rec, available: false}

	EnterDevMode(context.Background(), prod, dev)

	if got := rec.snapshot(); len(got) != 0 {
		t.Errorf("event order = %v, want no calls when dev is unavailable", got)
	}
}

// TestExitDevMode_StopsDevThenStartsProd pins the reverse order: the dev
// children (backend blocks until fully dead → lock freed) must stop BEFORE
// production re-boots.
func TestExitDevMode_StopsDevThenStartsProd(t *testing.T) {
	rec := &recorder{}
	prod := &fakeProd{rec: rec, state: StateStopped, stopLeaves: StateStopped}
	dev := &fakeDev{rec: rec, available: true}

	ExitDevMode(context.Background(), prod, dev)

	want := []string{"dev.Stop", "prod.Start"}
	if got := rec.snapshot(); !reflect.DeepEqual(got, want) {
		t.Errorf("event order = %v, want %v", got, want)
	}
}
