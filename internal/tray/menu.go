package tray

// This file is the menu's pure decision logic — extracted from the Windows
// adapter so the enablement rules and label copy are testable on any OS.

// statusTitle renders the production info line exactly as the approved
// screenshot shows it ("Status: Error"): coarse state only, no detail.
func statusTitle(s StatusView) string {
	switch s.State {
	case StateRunning:
		return "Status: Running"
	case StateError:
		return "Status: Error"
	default:
		return "Status: Stopped"
	}
}

// menuEnablement says which of a section's four actionable items are enabled.
type menuEnablement struct {
	Open    bool
	Start   bool
	Stop    bool
	Restart bool
}

// prodEnablement: the dashboard only opens on a running server; Start only
// offered when not running; Stop/Restart only when running. This produces
// the greyed items visible in the approved screenshot.
func prodEnablement(s State) menuEnablement {
	if s == StateRunning {
		return menuEnablement{Open: true, Stop: true, Restart: true}
	}
	return menuEnablement{Start: true}
}

// devEnablement: everything disabled when no dev repo was found. Otherwise
// Start is offered from Stopped/Error; Stop/Restart whenever anything is
// live or wedged; Open as soon as the frontend (vite) itself is up.
func devEnablement(available bool, v DevStatusView) menuEnablement {
	if !available {
		return menuEnablement{}
	}
	anyActive := v.Frontend != DevStopped || v.Backend != DevStopped
	return menuEnablement{
		Open:    v.Frontend == DevRunning,
		Start:   v.Overall == DevStopped || v.Overall == DevError,
		Stop:    anyActive,
		Restart: anyActive,
	}
}
