package tray

import (
	"context"
	"errors"
	"testing"
	"time"
)

type scriptLC struct {
	shutdownErr    error
	shutdownHang   bool
	bootErr        error
	bootCalled     int
	shutdownCalled int
}

func (s *scriptLC) Boot(context.Context) error { s.bootCalled++; return s.bootErr }
func (s *scriptLC) Shutdown(context.Context) error {
	s.shutdownCalled++
	if s.shutdownHang {
		select {}
	}
	return s.shutdownErr
}
func (s *scriptLC) Healthy(context.Context) bool { return true }
func (s *scriptLC) DashboardURL() string         { return "http://127.0.0.1:8081/" }

func newTestController(lc ServerLifecycle) *Controller {
	return NewController(lc, noopOpener{}, Options{
		ShutdownTimeout: 100 * time.Millisecond,
		WatchdogMargin:  50 * time.Millisecond,
		Exit:            func(int) {}, // never kill the test process
		UIStop:          func() {},
	})
}

func TestRestart_CleanShutdown_RebootsAndRuns(t *testing.T) {
	lc := &scriptLC{}
	c := newTestController(lc)
	c.Restart(context.Background())
	if lc.bootCalled != 1 {
		t.Fatalf("bootCalled=%d want 1", lc.bootCalled)
	}
	if c.Status().State != StateRunning {
		t.Fatalf("state=%v want Running", c.Status().State)
	}
}

func TestRestart_ShutdownError_SkipsBoot_EntersError(t *testing.T) {
	lc := &scriptLC{shutdownErr: errors.New("db close failed")}
	c := newTestController(lc)
	c.Restart(context.Background())
	if lc.bootCalled != 0 {
		t.Fatalf("bootCalled=%d want 0 (dirty shutdown must skip Boot)", lc.bootCalled)
	}
	if c.Status().State != StateError {
		t.Fatalf("state=%v want Error", c.Status().State)
	}
}

func TestRestart_ShutdownHang_TimesOut_SkipsBoot(t *testing.T) {
	lc := &scriptLC{shutdownHang: true}
	c := newTestController(lc)
	c.Restart(context.Background())
	if lc.bootCalled != 0 {
		t.Fatalf("bootCalled=%d want 0 (timeout must skip Boot)", lc.bootCalled)
	}
	if c.Status().State != StateError {
		t.Fatalf("state=%v want Error", c.Status().State)
	}
}

func TestStart_FromStopped_Boots_EntersRunning(t *testing.T) {
	lc := &scriptLC{}
	c := newTestController(lc)
	c.Start(context.Background())
	if lc.bootCalled != 1 {
		t.Fatalf("bootCalled=%d want 1", lc.bootCalled)
	}
	if c.Status().State != StateRunning {
		t.Fatalf("state=%v want Running", c.Status().State)
	}
}

func TestStart_AlreadyRunning_NoOp_DoesNotReboot(t *testing.T) {
	lc := &scriptLC{}
	c := newTestController(lc)
	c.Start(context.Background())
	if lc.bootCalled != 1 {
		t.Fatalf("bootCalled=%d want 1 after first Start", lc.bootCalled)
	}
	c.Start(context.Background())
	if lc.bootCalled != 1 {
		t.Fatalf("bootCalled=%d want 1 (Start while Running must no-op)", lc.bootCalled)
	}
	if c.Status().State != StateRunning {
		t.Fatalf("state=%v want Running", c.Status().State)
	}
}

func TestStop_WhileRunning_ShutsDown_EntersStopped_NoExit(t *testing.T) {
	lc := &scriptLC{}
	exited := false
	c := NewController(lc, noopOpener{}, Options{
		ShutdownTimeout: 100 * time.Millisecond,
		WatchdogMargin:  50 * time.Millisecond,
		Exit:            func(int) { exited = true },
		UIStop:          func() {},
	})
	c.Start(context.Background())
	if c.Status().State != StateRunning {
		t.Fatalf("state=%v want Running before Stop", c.Status().State)
	}
	c.Stop()
	if c.Status().State != StateStopped {
		t.Fatalf("state=%v want Stopped", c.Status().State)
	}
	if exited {
		t.Fatal("Stop must never call the exiter")
	}
}

func TestStop_AlreadyStopped_NoOp(t *testing.T) {
	lc := &scriptLC{}
	c := newTestController(lc)
	if c.Status().State != StateStopped {
		t.Fatalf("state=%v want Stopped initially", c.Status().State)
	}
	c.Stop()
	if lc.shutdownCalled != 0 {
		t.Fatalf("shutdownCalled=%d want 0 (Stop while already Stopped must no-op, not call Shutdown)", lc.shutdownCalled)
	}
	if c.Status().State != StateStopped {
		t.Fatalf("state=%v want Stopped", c.Status().State)
	}

	// Second guard: bring the controller genuinely through a real Stop first
	// (Running -> Stopped via an actual Shutdown call), then prove a further
	// Stop() while already Stopped performs no additional Shutdown call.
	c.Start(context.Background())
	if c.Status().State != StateRunning {
		t.Fatalf("state=%v want Running before the real Stop", c.Status().State)
	}
	c.Stop()
	if c.Status().State != StateStopped {
		t.Fatalf("state=%v want Stopped after the real Stop", c.Status().State)
	}
	if lc.shutdownCalled != 1 {
		t.Fatalf("shutdownCalled=%d want 1 after the real Stop", lc.shutdownCalled)
	}
	c.Stop() // already Stopped now: must no-op again
	if lc.shutdownCalled != 1 {
		t.Fatalf("shutdownCalled=%d want still 1 (no-op Stop must not call Shutdown again)", lc.shutdownCalled)
	}
	if c.Status().State != StateStopped {
		t.Fatalf("state=%v want Stopped", c.Status().State)
	}
}

func TestStopThenStart_RoundTrip(t *testing.T) {
	lc := &scriptLC{}
	c := newTestController(lc)
	c.Start(context.Background())
	c.Stop()
	c.Start(context.Background())
	if lc.bootCalled != 2 {
		t.Fatalf("bootCalled=%d want 2 (Start, Stop, Start)", lc.bootCalled)
	}
	if c.Status().State != StateRunning {
		t.Fatalf("state=%v want Running", c.Status().State)
	}
}

func TestStop_ShutdownError_EntersError_NoExit_ThenStartStillBoots(t *testing.T) {
	lc := &scriptLC{shutdownErr: errors.New("db close failed")}
	exited := false
	c := NewController(lc, noopOpener{}, Options{
		ShutdownTimeout: 100 * time.Millisecond,
		WatchdogMargin:  50 * time.Millisecond,
		Exit:            func(int) { exited = true },
		UIStop:          func() {},
	})
	c.Start(context.Background())
	if c.Status().State != StateRunning {
		t.Fatalf("state=%v want Running before Stop", c.Status().State)
	}
	c.Stop()
	if c.Status().State != StateError {
		t.Fatalf("state=%v want Error after a dirty Stop", c.Status().State)
	}
	if exited {
		t.Fatal("Stop must never call the exiter, even on shutdown error")
	}
	// A following Start must still work: Error != Running, so Start proceeds.
	c.Start(context.Background())
	if c.Status().State != StateRunning {
		t.Fatalf("state=%v want Running after a following Start", c.Status().State)
	}
	if lc.bootCalled != 2 {
		t.Fatalf("bootCalled=%d want 2", lc.bootCalled)
	}
}
