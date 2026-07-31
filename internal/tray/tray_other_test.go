//go:build !windows

package tray

import (
	"context"
	"testing"
	"time"
)

func TestRunNativeUI_Other_BlocksUntilCtxThenReturns(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- RunNativeUI(ctx, cancel, NewController(fakeLC{}, noopOpener{}, Options{Exit: func(int) {}}), NewDevSupervisor(DevSupervisorOptions{}))
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("RunNativeUI did not return after ctx cancel")
	}
}
