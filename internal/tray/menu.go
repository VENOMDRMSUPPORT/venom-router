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
// the buttons follow the current state (owner rule): Start is offered ONLY
// when the section is fully Stopped (BOTH children down — anyActive keys off
// the combined Overall, so a live backend keeps Start disabled even if the
// frontend is Stopped); Stop/Restart whenever either child is live or wedged;
// Open as soon as the frontend (vite) itself is up. Error never offers Start —
// recovery is Restart (a clean recycle of both children) or Stop (clears the
// error state).
func devEnablement(available bool, v DevStatusView) menuEnablement {
	if !available {
		return menuEnablement{}
	}
	anyActive := v.Overall != DevStopped
	return menuEnablement{
		Open:    v.Frontend == DevRunning,
		Start:   !anyActive,
		Stop:    anyActive,
		Restart: anyActive,
	}
}
