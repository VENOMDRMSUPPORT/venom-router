package domain

import "time"

// ConnectionState is Axis 1 of the account lifecycle (02 §3): the
// owner/enrollment lifecycle. See legalConnectionTransitions for the
// legal transition graph.
type ConnectionState string

const (
	ConnectionConnecting   ConnectionState = "connecting"
	ConnectionConnected    ConnectionState = "connected"
	ConnectionStopped      ConnectionState = "stopped"
	ConnectionDisconnected ConnectionState = "disconnected"
)

// HealthState is Axis 2 of the account lifecycle (02 §3): observed
// operational health, meaningful only while ConnectionState ==
// ConnectionConnected.
type HealthState string

const (
	HealthUnknown     HealthState = "unknown"
	HealthHealthy     HealthState = "healthy"
	HealthDegraded    HealthState = "degraded"
	HealthUnavailable HealthState = "unavailable"
	HealthExpired     HealthState = "expired"
)

// Account is the pure account entity: the two persisted lifecycle axes
// plus identity/observability fields (02 §3). display_status is
// deliberately absent as a field — it is derived, never persisted (see
// DeriveDisplayStatus).
type Account struct {
	ID                string
	ProviderID        string
	ExternalID        string
	DisplayName       string
	AuthType          string
	ConnectionState   ConnectionState
	HealthState       HealthState
	ReauthInProgress  bool
	IdentityEmail     string
	IdentityPlan      string
	LastHealthCheckAt *time.Time // nil = never checked
	LastHealthError   string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// CredentialStatus is the caller-supplied summary of an account's active
// credential that the domain needs for transition/eligibility rules. This
// package does not read credentials from storage — the caller (a later
// unit) resolves this from the account_credentials table.
type CredentialStatus struct {
	Active  bool // an active (non-retired) credential of the relevant kind exists
	Expired bool // true if that credential's expires_at is in the past
}
