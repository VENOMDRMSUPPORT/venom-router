//go:build !windows

package tray

import (
	"context"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/observability"
)

// RunNativeUI on non-Windows has no tray: it logs and blocks until ctx is
// cancelled (headless fallback). The server booted by the caller keeps running.
func RunNativeUI(ctx context.Context, _ context.CancelFunc, c *Controller) error {
	c.log.Info("tray: system tray unavailable on this platform; running headless",
		observability.String("mode", "headless-fallback"))
	<-ctx.Done()
	return nil
}

// NewOpener on non-Windows reports unsupported (dashboard/log opening is a
// desktop affordance not needed on a headless host).
func NewOpener() Opener { return unsupportedOpener{} }

type unsupportedOpener struct{}

func (unsupportedOpener) Open(string) error { return nil }
