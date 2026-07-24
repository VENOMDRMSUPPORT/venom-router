package domain

// IneligibleReason is a typed reason an account is not routing-eligible
// (02 §3). Exactly these six values exist; there is no "connecting"
// reason because connecting accounts fall under ReasonAccountDisconnected
// (both mean "no usable connected credential yet").
type IneligibleReason string

const (
	ReasonAccountStopped      IneligibleReason = "account_stopped"
	ReasonAccountDisconnected IneligibleReason = "account_disconnected"
	ReasonCredentialExpired   IneligibleReason = "credential_expired"
	ReasonAccountUnavailable  IneligibleReason = "account_unavailable"
	ReasonCoolingDown         IneligibleReason = "cooling_down"
	ReasonReauthInProgress    IneligibleReason = "reauth_in_progress"
)

// Eligibility is the result of ProjectEligibility: either Eligible is
// true and Reason is empty, or Eligible is false and Reason names why.
type Eligibility struct {
	Eligible bool
	Reason   IneligibleReason
}

// ProjectEligibility decides routing eligibility per 02 §3: all of
// connection_state = connected, health_state ∈ {healthy, degraded,
// unknown}, an active non-expired credential, and no active cooldown
// must hold. cred and cooldownActive are caller-supplied — this pure
// package does not read credentials or cooldowns itself.
//
// Precedence when multiple conditions fail (undocumented by 02 §3, which
// only lists the reason vocabulary): connection axis first (an account
// that is not connected can't be routed regardless of anything else),
// then reauth-in-progress, then cooldown, then credential status, then
// health state — mirroring DeriveDisplayStatus's precedence for
// consistency.
func ProjectEligibility(a Account, cred CredentialStatus, cooldownActive bool) Eligibility {
	switch {
	case a.ConnectionState == ConnectionStopped:
		return Eligibility{Reason: ReasonAccountStopped}
	case a.ConnectionState != ConnectionConnected:
		// Covers both "disconnected" and "connecting" — neither has a
		// connected, usable credential yet.
		return Eligibility{Reason: ReasonAccountDisconnected}
	case a.ReauthInProgress:
		return Eligibility{Reason: ReasonReauthInProgress}
	case cooldownActive:
		return Eligibility{Reason: ReasonCoolingDown}
	case !cred.Active || cred.Expired || a.HealthState == HealthExpired:
		// health_state == expired is definitionally "credential expired,
		// refresh not yet successful" (02 §3's Axis 2 table), so it
		// shares this reason rather than needing a seventh.
		return Eligibility{Reason: ReasonCredentialExpired}
	case a.HealthState == HealthUnavailable:
		return Eligibility{Reason: ReasonAccountUnavailable}
	default:
		return Eligibility{Eligible: true}
	}
}
