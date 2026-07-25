package tray

import (
	"context"
	"errors"
	"testing"
	"time"
)

type scriptLC struct {
	shutdownErr  error
	shutdownHang bool
	bootErr      error
	bootCalled   int
}

func (s *scriptLC) Boot(context.Context) error { s.bootCalled++; return s.bootErr }
func (s *scriptLC) Shutdown(context.Context) error {
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
