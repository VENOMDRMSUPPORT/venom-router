package tray

import "testing"

func TestStatusTitle_MatchesApprovedCopy(t *testing.T) {
	cases := []struct {
		state State
		want  string
	}{
		{StateRunning, "Status: Running"},
		{StateStopped, "Status: Stopped"},
		{StateError, "Status: Error"},
	}
	for _, tc := range cases {
		if got := statusTitle(StatusView{State: tc.state}); got != tc.want {
			t.Errorf("statusTitle(%v) = %q, want %q", tc.state, got, tc.want)
		}
	}
}

func TestProdEnablement(t *testing.T) {
	cases := []struct {
		state State
		want  menuEnablement
	}{
		{StateRunning, menuEnablement{Open: true, Start: false, Stop: true, Restart: true}},
		{StateStopped, menuEnablement{Open: false, Start: true, Stop: false, Restart: false}},
		{StateError, menuEnablement{Open: false, Start: true, Stop: false, Restart: false}},
	}
	for _, tc := range cases {
		if got := prodEnablement(tc.state); got != tc.want {
			t.Errorf("prodEnablement(%v) = %+v, want %+v", tc.state, got, tc.want)
		}
	}
}

func TestDevEnablement(t *testing.T) {
	if got := devEnablement(false, DevStatusView{}); got != (menuEnablement{}) {
		t.Errorf("unavailable dev section must disable everything, got %+v", got)
	}
	cases := []struct {
		name string
		v    DevStatusView
		want menuEnablement
	}{
		{"stopped", DevStatusView{Overall: DevStopped, Frontend: DevStopped},
			menuEnablement{Open: false, Start: true, Stop: false, Restart: false}},
		{"starting", DevStatusView{Overall: DevStarting, Frontend: DevStarting},
			menuEnablement{Open: false, Start: false, Stop: true, Restart: true}},
		{"both running", DevStatusView{Overall: DevRunning, Backend: DevRunning, Frontend: DevRunning},
			menuEnablement{Open: true, Start: false, Stop: true, Restart: true}},
		// Frontend up but backend still building/booting: Open must be OFF. The
		// dev dashboard's API is served by the backend (8081); opening it before
		// the backend is ready yields a transient 500 the SPA gets stuck on.
		{"frontend up, backend starting", DevStatusView{Overall: DevStarting, Backend: DevStarting, Frontend: DevRunning},
			menuEnablement{Open: false, Start: false, Stop: true, Restart: true}},
		// Backend live while the frontend is Stopped: the section is active, so
		// Start must NOT be offered (it would double-start the backend). Open
		// stays off — the dashboard needs the frontend (vite), which is not up.
		{"backend-only active", DevStatusView{Overall: DevRunning, Backend: DevRunning, Frontend: DevStopped},
			menuEnablement{Open: false, Start: false, Stop: true, Restart: true}},
		// Error never offers Start: recovery is Restart (clean recycle of the
		// frontend) or Stop (clears the error state). Offering Start alongside
		// Stop+Restart is the owner-reported UX bug.
		{"error", DevStatusView{Overall: DevError, Frontend: DevError},
			menuEnablement{Open: false, Start: false, Stop: true, Restart: true}},
	}
	for _, tc := range cases {
		if got := devEnablement(true, tc.v); got != tc.want {
			t.Errorf("%s: devEnablement = %+v, want %+v", tc.name, got, tc.want)
		}
	}
}
