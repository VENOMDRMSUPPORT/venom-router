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
func RunNativeUI(ctx context.Context, cancel context.CancelFunc, c *Controller, dev *DevSupervisor) error {
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

		mOpenProd := systray.AddMenuItem("Open Production Dashboard", "Open the production dashboard in your browser")
		mStatus := systray.AddMenuItem(statusTitle(c.Status()), "")
		mStatus.Disable()
		mStartProd := systray.AddMenuItem("Start Production", "Start the production server")
		mStopProd := systray.AddMenuItem("Stop Production", "Stop the production server without exiting")
		mRestartProd := systray.AddMenuItem("Restart Production", "Restart the production server")
		systray.AddSeparator()
		mOpenDev := systray.AddMenuItem("Open Development Dashboard", "Open the vite dev server in your browser")
		mDevStatus := systray.AddMenuItem(dev.StatusLine(), "")
		mDevStatus.Disable()
		mStartDev := systray.AddMenuItem("Start Development", "Start the dev frontend (vite) and dev backend")
		mStopDev := systray.AddMenuItem("Stop Development", "Stop the dev frontend and backend")
		mRestartDev := systray.AddMenuItem("Restart Development", "Restart the dev frontend and backend")
		systray.AddSeparator()
		mLogs := systray.AddMenuItem("View Logs", "Open the log file")
		mAutostart := systray.AddMenuItemCheckbox("Start with Windows", "Launch Venom Router automatically when you sign in", autostartEnabled())
		systray.AddSeparator()
		mQuit := systray.AddMenuItem("Quit", "Stop the server and exit")

		apply := func(items [4]*systray.MenuItem, e menuEnablement) {
			set := func(m *systray.MenuItem, on bool) {
				if on {
					m.Enable()
					return
				}
				m.Disable()
			}
			set(items[0], e.Open)
			set(items[1], e.Start)
			set(items[2], e.Stop)
			set(items[3], e.Restart)
		}
		prodItems := [4]*systray.MenuItem{mOpenProd, mStartProd, mStopProd, mRestartProd}
		devItems := [4]*systray.MenuItem{mOpenDev, mStartDev, mStopDev, mRestartDev}

		syncMenu := func() {
			apply(prodItems, prodEnablement(c.Status().State))
			apply(devItems, devEnablement(dev.Available(), dev.Status()))
			mStatus.SetTitle(statusTitle(c.Status()))
			mDevStatus.SetTitle(dev.StatusLine())
			systray.SetTooltip(statusTitle(c.Status()))
		}
		syncMenu()

		go func() {
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-mOpenProd.ClickedCh:
					c.OpenDashboard()
				case <-mStartProd.ClickedCh:
					go c.Start(ctx) // don't block the UI goroutine
				case <-mStopProd.ClickedCh:
					go c.Stop()
				case <-mRestartProd.ClickedCh:
					go c.Restart(ctx)
				case <-mOpenDev.ClickedCh:
					c.OpenURL(dev.DashboardURL())
				case <-mStartDev.ClickedCh:
					go dev.Start()
				case <-mStopDev.ClickedCh:
					go dev.Stop()
				case <-mRestartDev.ClickedCh:
					go dev.Restart()
				case <-mLogs.ClickedCh:
					c.OpenLogs()
				case <-mAutostart.ClickedCh:
					go toggleAutostart(c, mAutostart)
				case <-mQuit.ClickedCh:
					cancel() // funnel into the single ctx.Done() watcher
					return
				case <-ticker.C:
					c.Refresh(ctx)
					dev.Refresh(ctx)
					syncMenu()
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

// toggleAutostart flips the start-with-Windows registration and re-syncs the
// checkbox from the actual registry state.
func toggleAutostart(c *Controller, m *systray.MenuItem) {
	var err error
	if m.Checked() {
		err = disableAutostart()
	} else {
		err = enableAutostart()
	}
	if err != nil {
		c.log.Error("tray: toggling start-with-Windows failed",
			observability.String("err", err.Error()))
	}
	if autostartEnabled() {
		m.Check()
		return
	}
	m.Uncheck()
}
