//go:build windows

package tray

import (
	"context"
	"runtime"
	"time"

	"fyne.io/systray"
	"golang.org/x/sys/windows"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/observability"
)

// NewOpener returns the Windows ShellExecute-backed opener.
func NewOpener() Opener { return winOpener{} }

type winOpener struct{}

func (winOpener) Open(target string) error { return shellOpen(target) }

// RunNativeUI runs the systray event loop on the current (main) goroutine. The
// caller must have already booted the server and started the ctx.Done() watcher
// that calls Controller.ShutdownAndExit. This function locks the OS thread,
// captures its id for WM_QUIT, installs the UIStop hook, hides the console when
// solely owned, and blocks in systray.Run until Quit/onExit cancel ctx.
func RunNativeUI(ctx context.Context, cancel context.CancelFunc, c *Controller) error {
	runtime.LockOSThread()
	tid := windows.GetCurrentThreadId()
	c.SetUIStop(func() {
		systray.Quit()
		postQuit(tid)
	})
	hideConsoleIfOwned()

	onReady := func() {
		systray.SetIcon(trayIcon)
		systray.SetTitle("Venom Router")
		systray.SetTooltip("Venom Router")

		mStatus := systray.AddMenuItem("Status: starting…", "")
		mStatus.Disable()
		systray.AddSeparator()
		mOpen := systray.AddMenuItem("Open Dashboard", "Open the dashboard in your browser")
		mRestart := systray.AddMenuItem("Restart", "Restart the server")
		mLogs := systray.AddMenuItem("View Logs", "Open the log file")
		systray.AddSeparator()
		mQuit := systray.AddMenuItem("Quit", "Stop the server and exit")

		go func() {
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-mOpen.ClickedCh:
					c.OpenDashboard()
				case <-mRestart.ClickedCh:
					go c.Restart(ctx) // don't block the UI goroutine
				case <-mLogs.ClickedCh:
					c.OpenLogs()
				case <-mQuit.ClickedCh:
					cancel() // funnel into the single ctx.Done() watcher
					return
				case <-ticker.C:
					c.Refresh(ctx)
					mStatus.SetTitle(statusTitle(c.Status()))
					systray.SetTooltip(statusTitle(c.Status()))
				}
			}
		}()
	}

	onExit := func() { cancel() }

	c.log.Info("tray: starting system tray", observability.String("mode", "tray"))
	systray.Run(onReady, onExit)

	// If Run returned but ctx was never cancelled (init failure that returned),
	// fall back to headless: the server is still up.
	select {
	case <-ctx.Done():
	default:
		c.log.Warn("tray: systray loop ended without init; continuing headless")
		<-ctx.Done()
	}
	return nil
}

func statusTitle(s StatusView) string {
	switch s.State {
	case StateRunning:
		return "Status: running — " + s.Detail
	case StateError:
		return "Status: error (see Logs)"
	default:
		return "Status: stopped"
	}
}
