package domain

// DisplayStatus is the derived, UI/diagnostics-only projection of an
// account's axes (02 §3). It is never persisted as truth — always
// recomputed from the current axes plus the caller-supplied cooldown
// signal.
type DisplayStatus string

const (
	DisplayDisconnected     DisplayStatus = "disconnected"
	DisplayStopped          DisplayStatus = "stopped"
	DisplayConnecting       DisplayStatus = "connecting"
	DisplayReauthenticating DisplayStatus = "reauthenticating"
	DisplayCoolingDown      DisplayStatus = "cooling_down"
	DisplayExpired          DisplayStatus = "expired"
	DisplayUnavailable      DisplayStatus = "unavailable"
	DisplayDegraded         DisplayStatus = "degraded"
	DisplayHealthy          DisplayStatus = "healthy"
	DisplayUnknown          DisplayStatus = "unknown"
)

// DeriveDisplayStatus projects a's axes plus cooldownActive (cooldowns
// live in a separate table, keyed at account/offering/provider scope —
// 02 §3 — so this pure package takes the resolved signal as a parameter
// rather than reading it) into a single display status, in the exact
// first-match-wins precedence order documented in 02 §3:
// disconnected → stopped → connecting → reauthenticating → cooling_down
// → health_state → unknown.
func DeriveDisplayStatus(a Account, cooldownActive bool) DisplayStatus {
	switch {
	case a.ConnectionState == ConnectionDisconnected:
		return DisplayDisconnected
	case a.ConnectionState == ConnectionStopped:
		return DisplayStopped
	case a.ConnectionState == ConnectionConnecting:
		return DisplayConnecting
	case a.ReauthInProgress:
		return DisplayReauthenticating
	case cooldownActive:
		return DisplayCoolingDown
	}

	switch a.HealthState {
	case HealthExpired:
		return DisplayExpired
	case HealthUnavailable:
		return DisplayUnavailable
	case HealthDegraded:
		return DisplayDegraded
	case HealthHealthy:
		return DisplayHealthy
	default:
		return DisplayUnknown
	}
}
