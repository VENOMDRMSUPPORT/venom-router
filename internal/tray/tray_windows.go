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

	// Control window backend: a tray-owned loopback server the left-click
	// app-window drives. Optional — if it fails to bind, the tray still works
	// (left-click falls back to showing the menu).
	var controlURL string
	if server, err := NewControlServer(&trayControlsAdapter{ctx: ctx, c: c, dev: dev, cancel: cancel}); err != nil {
		c.log.Error("tray: control server unavailable", observability.String("err", err.Error()))
	} else {
		server.Start()
		controlURL = server.URL()
		// A Chromium --app window is a separate process and can survive the
		// previous tray instance. Retire it now so the owner cannot keep using a
		// page whose ephemeral control server and token no longer exist.
		retireControlWindow()
		defer func() {
			retireControlWindow()
			sctx, scancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer scancel()
			_ = server.Shutdown(sctx)
		}()
	}

	onReady := func() {
		systray.SetIcon(trayIcon)
		systray.SetTitle("Venom Router")
		systray.SetTooltip("Venom Router")

		// Left-click opens the control window (app-window of the control page),
		// or raises the one already open — the window is a singleton, so
		// clicking the icon repeatedly never stacks duplicates; right-click
		// still shows the menu below (we leave the secondary tap unset, so
		// systray's default menu appears). When the control server is
		// unavailable, leaving the tap unset makes left-click show the menu too.
		if controlURL != "" {
			systray.SetOnTapped(func() {
				go func() {
					outcome, err := openOrFocusControlWindow(controlURL)
					if err != nil {
						c.log.Error("tray: open control window failed", observability.String("err", err.Error()))
						return
					}
					c.log.Debug("tray: control window tap", observability.String("outcome", outcome.String()))
				}()
			})
		}

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
		mStartDev := systray.AddMenuItem("Start Development", "Stop production, then start the dev frontend (vite) and the auto-reloading backend on the shared database")
		mStopDev := systray.AddMenuItem("Stop Development", "Stop the dev frontend and backend, then restart production")
		mRestartDev := systray.AddMenuItem("Restart Development", "Restart the dev frontend and backend (production stays stopped)")
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
					// "Start Development" is the composite: stop production
					// (frees 8081 + the lock), then start the dev children on
					// the one DB. Run off the UI goroutine — prod.Stop blocks.
					go EnterDevMode(ctx, c, dev)
				case <-mStopDev.ClickedCh:
					// "Stop Development" reverses it: stop the dev children
					// (backend blocks until fully dead), then restart production.
					go ExitDevMode(ctx, c, dev)
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
